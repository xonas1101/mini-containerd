package main

import (
	"os"
	"path/filepath"
	"testing"
)

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
