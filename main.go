package main

// tool -> docker -> container runtime & manager
// This is a minimal container runtime-like program that:
// - creates new namespaces
// - sets hostname
// - chroots into a rootfs
// - mounts proc/sys/cgroup
// - then runs a command inside that isolated environment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	specs "github.com/opencontainers/runtime-spec/specs-go"
)

func loadSpec(bundle string) *specs.Spec {
	data, err := os.ReadFile(filepath.Join(bundle, "config.json"))
	if err != nil {
		panic(err)
	}

	var spec specs.Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		panic(err)
	}
	return &spec
}


func main() {
	// We expect a bundle to run containers
	if len(os.Args) < 3 {
		panic("missing bundle path")
	}


	switch os.Args[1] {

	case "run":
		// If we are running under systemd, we may need delegation
		// so that cgroup controllers can be used correctly.
		if needDelegate() {
			reexecInDelegate(20)
			return
		}
		run()

	case "child":
		// This is the process that actually runs *inside* the container
		child()

	default:
		panic("wrong command")
	}
}

func run() {
	bundle := os.Args[2]

	spec := loadSpec(bundle)

	cmd := exec.Command(
		"/proc/self/exe",
		"child",
		bundle,
	)

	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: namespaceFlags(spec),
	}

	if err := cmd.Run(); err != nil {
		panic(err)
	}
}

func namespaceFlags(spec *specs.Spec) uintptr {
	var flags uintptr
	for _, ns := range spec.Linux.Namespaces {
		switch ns.Type {
		case specs.PIDNamespace:
			flags |= syscall.CLONE_NEWPID
		case specs.UTSNamespace:
			flags |= syscall.CLONE_NEWUTS
		case specs.MountNamespace:
			flags |= syscall.CLONE_NEWNS
		}
	}
	return flags
}

func child() {
	if len(os.Args) < 3 {
		panic("child requires bundle path")
	}

	bundle := os.Args[2]
	spec := loadSpec(bundle)

	// Hostname (OCI)
	if spec.Hostname != "" {
		if err := syscall.Sethostname([]byte(spec.Hostname)); err != nil {
			panic(err)
		}
	}

	// Root filesystem (bundle-relative)
	rootfs := filepath.Join(bundle, spec.Root.Path)
// If OCI root is readonly, remount rootfs as read-only
	if spec.Root.Readonly {
		if err := syscall.Mount(
			rootfs,
			rootfs,
			"",
			syscall.MS_BIND|syscall.MS_REC,
			"",
		); err != nil {
			panic(err)
		}

		if err := syscall.Mount(
			rootfs,
			rootfs,
			"",
			syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY,
			"",
		); err != nil {
			panic(err)
		}
	}

	if err := pivotRoot(rootfs); err != nil {
		panic(err)
	}

	_ = os.Remove("/dev/ptmx")
	if err := os.Symlink("pts/ptmx", "/dev/ptmx"); err != nil {
		panic(err)
	}

	// OCI process
	proc := spec.Process
	cmd := exec.Command(proc.Args[0], proc.Args[1:]...)
	cmd.Env = proc.Env
	cmd.Dir = proc.Cwd
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	if err := syscall.Setgid(int(proc.User.GID)); err != nil {
		panic(err)
	}

	if err := syscall.Setuid(int(proc.User.UID)); err != nil {
		panic(err)
	}

	// PID 1 reaping
	go func() {
		for {
			var status syscall.WaitStatus
			syscall.Wait4(-1, &status, 0, nil)
		}
	}()

	if err := cmd.Run(); err != nil {
		panic(err)
	}
}

func pivotRoot(rootfs string) error {
	if err := syscall.Mount(rootfs, rootfs, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		return err
	}

	// Respect OCI root.readonly
	if err := syscall.Mount(
		rootfs,
		rootfs,
		"",
		syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY,
		"",
	); err != nil {
		return err
	}

	putOld := filepath.Join(rootfs, ".oldroot")

	if err := syscall.Unmount(putOld, syscall.MNT_DETACH); err != nil {
		return err
	}

	return os.RemoveAll(putOld)
}

func mountAll(spec *specs.Spec) error {
	for _, m := range spec.Mounts {
		if err := os.MkdirAll(m.Destination, 0755); err != nil {
			return err
		}

		data := strings.Join(m.Options, ",")

		if err := syscall.Mount(
			m.Source,
			m.Destination,
			m.Type,
			0,
			data,
		); err != nil {
			return err
		}
	}
	return nil
}

// -------------------------------------------------
// SYSTEMD DELEGATION LOGIC
// -------------------------------------------------

// needDelegate checks whether:
// - we are running under systemd
// - and we need a delegated cgroup
// so that this process can manage its own cgroups
func needDelegate() bool {
	// If already delegated, no need again
	if os.Getenv("RUNC_DELEGATED") == "1" {
		return false
	}

	// If systemd-run is not available, cannot delegate
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return false
	}

	// Check if PID 1 is systemd
	p1, _ := os.ReadFile("/proc/1/comm")
	if !bytes.HasPrefix(p1, []byte("systemd")) {
		return false
	}

	// Check if we are in init.scope (not delegated)
	self, _ := os.ReadFile("/proc/self/cgroup")
	return bytes.Contains(self, []byte("/init.scope"))
}

// reexecInDelegate re-launches this program using systemd-run
// with cgroup delegation enabled.
func reexecInDelegate(pidsMax int) {
	selfExe, err := os.Executable()
	if err != nil {
		panic(err)
	}

	// systemd-run arguments:
	// --scope            -> run in a transient scope unit
	// Delegate=yes      -> allow this process to manage cgroups
	// TasksMax          -> limit number of processes
	// RUNC_DELEGATED=1  -> prevent infinite recursion
	args := []string{
		"--scope",
		"-p", "Delegate=yes",
		"-p", fmt.Sprintf("TasksMax=%d", pidsMax),
		"--setenv=RUNC_DELEGATED=1",
		"--quiet",
		selfExe,
	}

	// Pass original arguments (run/child + command)
	args = append(args, os.Args[1:]...)

	cmd := exec.Command("systemd-run", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	if err := cmd.Run(); err != nil {
		panic(err)
	}

	// Exit original process — new delegated one continues
	os.Exit(0)
}
