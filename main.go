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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"github.com/urfave/cli/v3"
	cp "github.com/otiai10/copy"
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
	// child is an internal subcommand spawned by run() via /proc/self/exe — not user-facing
	if len(os.Args) > 1 && os.Args[1] == "child" {
		child()
		return
	}

	app := &cli.Command{
		Name:  "mini-containerd",
		Usage: "a minimal container runtime",
		Commands: []*cli.Command{
			{
				Name:      "run",
				Usage:     "run a container from a bundle",
				ArgsUsage: "<bundle>",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					bundle := cmd.Args().First()
					if bundle == "" {
						return fmt.Errorf("bundle path required")
					}
					if needDelegate() {
						reexecInDelegate(20)
						return nil
					}
					run(bundle)
					return nil
				},
			},
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(bundle string) {

	spec := loadSpec(bundle)
	
	c := NewContainer(bundle, spec.Process.Args)
	if err := writeNewContainerOnDisk(c); err != nil {
		panic(err)
	}

	rootfsDst := filepath.Join(containerDir(c.ID), "rootfs")
	if err := os.MkdirAll(rootfsDst, 0755); err != nil {
		panic(err)
	}
	if err := cp.Copy(filepath.Join(bundle, spec.Root.Path), rootfsDst); err != nil {
		panic(err)
	}

	cmd := exec.Command(
		"/proc/self/exe",
		"child",
		bundle,
	)

	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: namespaceFlags(spec),
	}

	if err := cmd.Start(); err != nil {
		panic(err)
	}

	c.PID = cmd.Process.Pid
	c.StartContainer()
	if err := writeNewContainerOnDisk(c); err != nil {
		panic(err)
	}

	waitErr := cmd.Wait()
	c.StopContainer()
	if cmd.ProcessState != nil {
		c.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err := writeNewContainerOnDisk(c); err != nil {
		panic(err)
	}
	if waitErr != nil {
		panic(waitErr)
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
	if err := syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""); err != nil {
		panic(err)
	}
	if err := pivotRoot(rootfs); err != nil {
		panic(err)
	}
	if err := mountAll(spec); err != nil {
		panic(err)
	}
	if err := setupDevices(); err != nil {
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
			_, err := syscall.Wait4(-1, &status, 0, nil)
			if err != nil {
				// No more children → exit loop
				return
			}
		}
	}()
	
	if err := cmd.Run(); err != nil {
		panic(err)
	}
}

func pivotRoot(rootfs string) error {
	// Make sure rootfs is a mount point
	if err := syscall.Mount(rootfs, rootfs, "", syscall.MS_BIND|syscall.MS_REC, ""); err != nil {
		return err
	}

	putOld := filepath.Join(rootfs, ".oldroot")
	if err := os.MkdirAll(putOld, 0700); err != nil {
		return err
	}

	// Actual pivot_root syscall
	if err := syscall.PivotRoot(rootfs, putOld); err != nil {
		return err
	}

	// Change working dir to new root
	if err := os.Chdir("/"); err != nil {
		return err
	}

	putOld = "/.oldroot"

	// Unmount old root
	if err := syscall.Unmount(putOld, syscall.MNT_DETACH); err != nil {
		return err
	}

	return os.RemoveAll(putOld)
}

func mountAll(spec *specs.Spec) error {
	for _, m := range spec.Mounts {
		dest := m.Destination

		if err := os.MkdirAll(dest, 0755); err != nil {
			return err
		}

		var flags uintptr
		var data []string

		for _, opt := range m.Options {
			switch opt {
			case "nosuid":
				flags |= syscall.MS_NOSUID
			case "noexec":
				flags |= syscall.MS_NOEXEC
			case "nodev":
				flags |= syscall.MS_NODEV
			default:
				data = append(data, opt)
			}
		}

		dataStr := strings.Join(data, ",")

		if err := syscall.Mount(
			m.Source,
			dest,
			m.Type,
			flags,
			dataStr,
		); err != nil {
			return fmt.Errorf("mount %s failed: %v", dest, err)
		}
	}
	return nil
}

func ensureCharDevice(path string, mode uint32, dev int) error {
	if err := syscall.Mknod(path, mode, dev); err == nil {
		return nil
	} else if err != syscall.EEXIST {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if ok && info.Mode()&os.ModeCharDevice != 0 && uint64(stat.Rdev) == uint64(dev) {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove existing %s: %w", path, err)
	}
	return syscall.Mknod(path, mode, dev)
}

func setupDevices() error {
	devices := []struct {
		path  string
		major uint32
		minor uint32
	}{
		{"/dev/null", 1, 3},
		{"/dev/zero", 1, 5},
		{"/dev/random", 1, 8},
		{"/dev/urandom", 1, 9},
		{"/dev/tty", 5, 0},
	}
	for _, d := range devices {
		if err := ensureCharDevice(d.path, syscall.S_IFCHR|0666, int(d.major<<8|d.minor)); err != nil {
			return fmt.Errorf("setup %s: %w", d.path, err)
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
