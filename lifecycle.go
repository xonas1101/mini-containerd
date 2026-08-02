package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// StartContainer relaunches a stopped container, reusing its existing
// per-container rootfs and the OCI bundle it was originally created from.
// Always runs detached — there's no foreground shell to block on for a
// container that already returned control once.
func StartContainer(idOrPrefix string) error {
	c, err := readContainer(idOrPrefix)
	if err != nil {
		return fmt.Errorf("container %q not found: %w", idOrPrefix, err)
	}
	if c.Status == "running" {
		return fmt.Errorf("container %q is already running", c.ID)
	}
	spec, err := loadSpec(c.Bundle)
	if err != nil {
		return err
	}
	return launchChild(c, c.Bundle, spec, "", 0, true)
}

// StopContainer sends SIGTERM, waits up to timeout for the process to exit,
// and escalates to SIGKILL if it hasn't by then. If the process is already
// gone (e.g. killed outside our control, host reboot) it's treated as
// already-stopped rather than an error, so the container doesn't get stuck.
func StopContainer(idOrPrefix string, timeout time.Duration) error {
	c, err := readContainer(idOrPrefix)
	if err != nil {
		return fmt.Errorf("container %q not found: %w", idOrPrefix, err)
	}
	if c.Status != "running" {
		return fmt.Errorf("container %q is not running", c.ID)
	}

	proc, err := os.FindProcess(c.PID)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("signal container %q: %w", c.ID, err)
	} else if err == nil {
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
	}

	c.Status = "stopped"
	if err := writeNewContainerOnDisk(c); err != nil {
		return err
	}
	return CleanupCgroup(c.ID)
}

// KillContainer sends SIGKILL immediately, no grace period. Like
// StopContainer, an already-dead process is treated as already-stopped.
func KillContainer(idOrPrefix string) error {
	c, err := readContainer(idOrPrefix)
	if err != nil {
		return fmt.Errorf("container %q not found: %w", idOrPrefix, err)
	}
	if c.Status != "running" {
		return fmt.Errorf("container %q is not running", c.ID)
	}
	proc, err := os.FindProcess(c.PID)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("kill container %q: %w", c.ID, err)
	}
	c.Status = "stopped"
	if err := writeNewContainerOnDisk(c); err != nil {
		return err
	}
	return CleanupCgroup(c.ID)
}

// RemoveContainer deletes a container's persisted state (config.json, its
// rootfs copy, logs.txt). Refuses on a running container unless force is
// set, in which case it's killed first.
func RemoveContainer(idOrPrefix string, force bool) error {
	c, err := readContainer(idOrPrefix)
	if err != nil {
		return fmt.Errorf("container %q not found: %w", idOrPrefix, err)
	}
	if c.Status == "running" {
		if !force {
			return fmt.Errorf("container %q is running (use --force to kill and remove)", c.ID)
		}
		if err := KillContainer(c.ID); err != nil {
			return err
		}
	}
	if err := CleanupCgroup(c.ID); err != nil {
		return err
	}
	return os.RemoveAll(containerDir(c.ID))
}

// PruneContainers removes every container that isn't currently running,
// returning the ids it removed.
func PruneContainers() ([]string, error) {
	containers, err := ListContainers()
	if err != nil {
		return nil, err
	}

	var removed []string
	for _, c := range containers {
		if c.Status == "running" {
			continue
		}
		if err := RemoveContainer(c.ID, false); err != nil {
			return removed, fmt.Errorf("remove container %q: %w", c.ID, err)
		}
		removed = append(removed, c.ID)
	}
	return removed, nil
}
