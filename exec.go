package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"
)

// Namespace files to join, in this order. mnt is last: setns(CLONE_NEWPID)
// only affects children forked after the call (not the caller itself), so
// order among the others doesn't matter much, but joining mnt earlier could
// make the remaining /proc/<pid>/ns/* paths unreachable if it changes what's
// mounted where — safest to open every fd first, then setns mnt last.
var nsJoinOrder = []string{"pid", "uts", "ipc", "net", "mnt"}

// ExecInContainer runs args inside container id's existing namespaces.
//
// Joining a mount namespace from Go is only reliable on the OS thread that
// calls setns, because the Go runtime is multi-threaded and namespace
// membership is a per-thread kernel property (see runtime.LockOSThread).
// We lock this goroutine to its OS thread, setns into every target
// namespace on that thread, then exec.Command from the same thread so the
// forked child inherits them. runc instead re-execs through a C constructor
// (nsenter) that runs before the Go runtime starts any other threads, which
// is more robust but out of scope for this project.
func ExecInContainer(id string, args []string) error {
	c, err := readContainer(id)
	if err != nil {
		return fmt.Errorf("container %q not found: %w", id, err)
	}
	if c.Status != "running" {
		return fmt.Errorf("container %q is not running", id)
	}
	if len(args) == 0 {
		return fmt.Errorf("command required")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	for _, ns := range nsJoinOrder {
		fd, err := os.Open(filepath.Join("/proc", fmt.Sprint(c.PID), "ns", ns))
		if err != nil {
			return fmt.Errorf("open %s namespace: %w", ns, err)
		}
		err = unix.Setns(int(fd.Fd()), 0)
		fd.Close()
		if err != nil {
			return fmt.Errorf("join %s namespace: %w", ns, err)
		}
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
