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
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func main() {
	// We expect a subcommand: either "run" or "child"
	if len(os.Args) < 2 {
		panic("need subcommand: run|child")
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
	// This is the "parent" process.
	// It will create new namespaces and then re-exec itself as "child".
	fmt.Printf("Running %v as %d\n", os.Args[2:], os.Getpid())

	// Re-execute THIS binary, but with subcommand "child"
	// /proc/self/exe points to the currently running executable
	cmd := exec.Command("/proc/self/exe", append([]string{"child"}, os.Args[2:]...)...)

	// Hook stdio so the container feels interactive
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	// Tell the kernel to create new namespaces for the child:
	// - UTS: hostname/domain isolation
	// - NS:  mount namespace
	// - PID: process tree isolation
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:   syscall.CLONE_NEWUTS | syscall.CLONE_NEWNS | syscall.CLONE_NEWPID,
		Unshareflags: syscall.CLONE_NEWNS,
	}

	// Run the child process in the new namespaces
	if err := cmd.Run(); err != nil {
		fmt.Printf("run failed: %v\n", err)
		os.Exit(1)
	}
}

func child() {
	// This process is now inside new namespaces
	fmt.Printf("Running %v as %d\n", os.Args[2:], os.Getpid())

	// The actual command that the user wants to run in the container
	cmd := exec.Command(os.Args[2], os.Args[3:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	// ----------------------------
	// 1. Set container hostname
	// ----------------------------
	if err := syscall.Sethostname([]byte("runc_container")); err != nil {
		fmt.Println("sethostname failed:", err)
		os.Exit(1)
	}

	// ----------------------------
	// 2. Change root filesystem
	// ----------------------------
	// This makes /home/akaxonas/rootfs appear as /
	// for processes inside the container.
	if err := syscall.Chroot("/home/akaxonas/rootfs"); err != nil {
		fmt.Println("chroot failed:", err)
		os.Exit(1)
	}

	// After chroot, ensure working directory is inside new root
	if err := os.Chdir("/"); err != nil {
		fmt.Println("chdir failed:", err)
		os.Exit(1)
	}

	// ----------------------------
	// 3. Mount virtual filesystems
	// ----------------------------

	// Mount /proc so tools like ps, top, etc work
	_ = os.MkdirAll("/proc", 0755)
	if err := syscall.Mount("proc", "/proc", "proc", 0, ""); err != nil {
		fmt.Println("mount proc failed:", err)
		os.Exit(1)
	}

	// Mount /sys so kernel information is available
	_ = os.MkdirAll("/sys", 0755)
	if err := syscall.Mount("sysfs", "/sys", "sysfs", 0, ""); err != nil {
		fmt.Println("mount sysfs failed:", err)
		// not fatal — container can still run
	}

	// Mount cgroup v2 filesystem for resource control
	_ = os.MkdirAll("/sys/fs/cgroup", 0755)
	if err := syscall.Mount("cgroup2", "/sys/fs/cgroup", "cgroup2", 0, ""); err != nil {
		// Some systems already mount it or disallow remounting
		// Safe to ignore in this minimal runtime
	}

	// ----------------------------
	// 4. Execute user command
	// ----------------------------
	if err := cmd.Run(); err != nil {
		fmt.Println("child exec failed:", err)
		os.Exit(1)
	}

	// ----------------------------
	// 5. Cleanup mounts
	// ----------------------------
	_ = syscall.Unmount("/proc", 0)
	_ = syscall.Unmount("/sys/fs/cgroup", 0)
	_ = syscall.Unmount("/sys", 0)
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
