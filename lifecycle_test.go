package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// deadPID starts and waits for a trivial child process, returning its pid
// after it has exited — guaranteed not to be a live process (barring an
// extremely unlikely pid-reuse race), for testing stale-PID handling.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("running a throwaway process: %v", err)
	}
	return cmd.Process.Pid
}

func TestStopContainerHandlesAlreadyDeadProcess(t *testing.T) {
	old := containersRoot
	containersRoot = t.TempDir()
	defer func() { containersRoot = old }()

	c := NewContainer("busybox", []string{"/bin/sh"})
	c.Status = "running"
	c.PID = deadPID(t)
	if err := writeNewContainerOnDisk(c); err != nil {
		t.Fatalf("writeNewContainerOnDisk: %v", err)
	}

	if err := StopContainer(c.ID, time.Second); err != nil {
		t.Fatalf("StopContainer on an already-dead process should not error, got: %v", err)
	}

	got, err := readContainer(c.ID)
	if err != nil {
		t.Fatalf("readContainer: %v", err)
	}
	if got.Status != "stopped" {
		t.Fatalf("status = %q, want %q", got.Status, "stopped")
	}
}

func TestKillContainerHandlesAlreadyDeadProcess(t *testing.T) {
	old := containersRoot
	containersRoot = t.TempDir()
	defer func() { containersRoot = old }()

	c := NewContainer("busybox", []string{"/bin/sh"})
	c.Status = "running"
	c.PID = deadPID(t)
	if err := writeNewContainerOnDisk(c); err != nil {
		t.Fatalf("writeNewContainerOnDisk: %v", err)
	}

	if err := KillContainer(c.ID); err != nil {
		t.Fatalf("KillContainer on an already-dead process should not error, got: %v", err)
	}

	got, err := readContainer(c.ID)
	if err != nil {
		t.Fatalf("readContainer: %v", err)
	}
	if got.Status != "stopped" {
		t.Fatalf("status = %q, want %q", got.Status, "stopped")
	}
}

func TestStopContainerByShortIDPrefix(t *testing.T) {
	old := containersRoot
	containersRoot = t.TempDir()
	defer func() { containersRoot = old }()

	c := NewContainer("busybox", []string{"/bin/sh"})
	c.Status = "running"
	c.PID = deadPID(t)
	if err := writeNewContainerOnDisk(c); err != nil {
		t.Fatalf("writeNewContainerOnDisk: %v", err)
	}

	if err := StopContainer(c.ID[:4], time.Second); err != nil {
		t.Fatalf("StopContainer by short prefix: %v", err)
	}
}

func TestRemoveContainerRefusesRunningWithoutForce(t *testing.T) {
	old := containersRoot
	containersRoot = t.TempDir()
	defer func() { containersRoot = old }()

	c := NewContainer("busybox", []string{"/bin/sh"})
	c.Status = "running"
	c.PID = os.Getpid() // a real, but irrelevant, pid so Signal(0) checks succeed if force were used
	if err := writeNewContainerOnDisk(c); err != nil {
		t.Fatalf("writeNewContainerOnDisk: %v", err)
	}

	if err := RemoveContainer(c.ID, false); err == nil {
		t.Fatal("expected RemoveContainer to refuse a running container without --force")
	}

	if _, err := os.Stat(containerDir(c.ID)); err != nil {
		t.Fatalf("container dir should still exist after refused removal: %v", err)
	}
}

func TestRemoveContainerDeletesStoppedContainer(t *testing.T) {
	old := containersRoot
	containersRoot = t.TempDir()
	defer func() { containersRoot = old }()

	c := NewContainer("busybox", []string{"/bin/sh"})
	c.Status = "stopped"
	if err := writeNewContainerOnDisk(c); err != nil {
		t.Fatalf("writeNewContainerOnDisk: %v", err)
	}

	if err := RemoveContainer(c.ID, false); err != nil {
		t.Fatalf("RemoveContainer: %v", err)
	}

	if _, err := os.Stat(containerDir(c.ID)); !os.IsNotExist(err) {
		t.Fatalf("container dir should be removed, stat err = %v", err)
	}
}

func TestPruneContainersSkipsRunning(t *testing.T) {
	old := containersRoot
	containersRoot = t.TempDir()
	defer func() { containersRoot = old }()

	running := NewContainer("busybox", []string{"/bin/sh"})
	running.Status = "running"
	running.PID = os.Getpid()
	if err := writeNewContainerOnDisk(running); err != nil {
		t.Fatal(err)
	}

	stopped := NewContainer("busybox", []string{"/bin/sh"})
	stopped.Status = "stopped"
	if err := writeNewContainerOnDisk(stopped); err != nil {
		t.Fatal(err)
	}

	exited := NewContainer("busybox", []string{"/bin/sh"})
	exited.Status = "exited"
	if err := writeNewContainerOnDisk(exited); err != nil {
		t.Fatal(err)
	}

	removed, err := PruneContainers()
	if err != nil {
		t.Fatalf("PruneContainers: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("PruneContainers removed %d containers, want 2", len(removed))
	}

	if _, err := os.Stat(containerDir(running.ID)); err != nil {
		t.Fatalf("running container should not have been pruned: %v", err)
	}
	if _, err := os.Stat(filepath.Join(containersRoot, stopped.ID)); !os.IsNotExist(err) {
		t.Fatal("stopped container should have been pruned")
	}
	if _, err := os.Stat(filepath.Join(containersRoot, exited.ID)); !os.IsNotExist(err) {
		t.Fatal("exited container should have been pruned")
	}
}
