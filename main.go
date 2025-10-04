package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		panic("need subcommand: run|child")
	}
	switch os.Args[1] {
	case "run":
		if needDelegate() {
			reexecInDelegate(20) 
			return
		}
		run()
	case "child":
		child()
	default:
		panic("wrong command")
	}
}

func run() {
	fmt.Printf("Running %v as %d\n", os.Args[2:], os.Getpid())
	cmd := exec.Command("/proc/self/exe", append([]string{"child"}, os.Args[2:]...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:   syscall.CLONE_NEWUTS | syscall.CLONE_NEWNS | syscall.CLONE_NEWPID,
		Unshareflags: syscall.CLONE_NEWNS,
	}
	if err := cmd.Run(); err != nil {
		fmt.Printf("run failed: %v\n", err)
		os.Exit(1)
	}
}

func child() {
	fmt.Printf("Running %v as %d\n", os.Args[2:], os.Getpid())

	cmd := exec.Command(os.Args[2], os.Args[3:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	if err := syscall.Sethostname([]byte("runc_container")); err != nil {
		fmt.Println("sethostname failed:", err)
		os.Exit(1)
	}
	if err := syscall.Chroot("/home/akaxonas/rootfs"); err != nil {
		fmt.Println("chroot failed:", err)
		os.Exit(1)
	}
	if err := os.Chdir("/"); err != nil {
		fmt.Println("chdir failed:", err)
		os.Exit(1)
	}

	_ = os.MkdirAll("/proc", 0755)
	if err := syscall.Mount("proc", "/proc", "proc", 0, ""); err != nil {
		fmt.Println("mount proc failed:", err)
		os.Exit(1)
	}
	_ = os.MkdirAll("/sys", 0755)
	if err := syscall.Mount("sysfs", "/sys", "sysfs", 0, ""); err != nil {
		fmt.Println("mount sysfs failed:", err)
		// keep going; not fatal for running cmd
	}
	_ = os.MkdirAll("/sys/fs/cgroup", 0755)
	if err := syscall.Mount("cgroup2", "/sys/fs/cgroup", "cgroup2", 0, ""); err != nil {
		// some kernels don’t allow mounting another cgroup2; ignore visibility only
	}

	if err := cmd.Run(); err != nil {
		fmt.Println("child exec failed:", err)
		os.Exit(1)
	}

	_ = syscall.Unmount("/proc", 0)
	_ = syscall.Unmount("/sys/fs/cgroup", 0)
	_ = syscall.Unmount("/sys", 0)
}

func needDelegate() bool {
	if os.Getenv("RUNC_DELEGATED") == "1" {
		return false
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return false
	}
	p1, _ := os.ReadFile("/proc/1/comm")
	if !bytes.HasPrefix(p1, []byte("systemd")) {
		return false
	}
	self, _ := os.ReadFile("/proc/self/cgroup")
	return bytes.Contains(self, []byte("/init.scope"))
}

func reexecInDelegate(pidsMax int) {
	selfExe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	args := []string{
		"--scope",
		"-p", "Delegate=yes",
		"-p", fmt.Sprintf("TasksMax=%d", pidsMax),
		"--setenv=RUNC_DELEGATED=1",
		"--quiet",
		selfExe,
	}
	args = append(args, os.Args[1:]...)
	cmd := exec.Command("systemd-run", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		panic(err)
	}
	os.Exit(0)
}
