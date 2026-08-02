package main

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// StartContainer relaunches a stopped container, reusing its existing
// per-container rootfs and the OCI bundle it was originally created from.
// Always runs detached — there's no foreground shell to block on for a
// container that already returned control once.
func StartContainer(id string) error {
	c, err := readContainer(id)
	if err != nil {
		return fmt.Errorf("container %q not found: %w", id, err)
	}
	if c.Status == "running" {
		return fmt.Errorf("container %q is already running", id)
	}
	spec := loadSpec(c.Bundle)
	return launchChild(c, c.Bundle, spec, "", 0, true)
}

// StopContainer sends SIGTERM, waits up to timeout for the process to exit,
// and escalates to SIGKILL if it hasn't by then.
func StopContainer(id string, timeout time.Duration) error {
	c, err := readContainer(id)
	if err != nil {
		return fmt.Errorf("container %q not found: %w", id, err)
	}
	if c.Status != "running" {
		return fmt.Errorf("container %q is not running", id)
	}

	proc, err := os.FindProcess(c.PID)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal container %q: %w", id, err)
	}

	done := make(chan struct{})
	go func() {
		for proc.Signal(syscall.Signal(0)) == nil {
			time.Sleep(200 * time.Millisecond)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		_ = proc.Signal(syscall.SIGKILL)
		<-done
	}

	c.Status = "stopped"
	if err := writeNewContainerOnDisk(c); err != nil {
		return err
	}
	return CleanupCgroup(id)
}

// KillContainer sends SIGKILL immediately, no grace period.
func KillContainer(id string) error {
	c, err := readContainer(id)
	if err != nil {
		return fmt.Errorf("container %q not found: %w", id, err)
	}
	if c.Status != "running" {
		return fmt.Errorf("container %q is not running", id)
	}
	proc, err := os.FindProcess(c.PID)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGKILL); err != nil {
		return fmt.Errorf("kill container %q: %w", id, err)
	}
	c.Status = "stopped"
	if err := writeNewContainerOnDisk(c); err != nil {
		return err
	}
	return CleanupCgroup(id)
}
