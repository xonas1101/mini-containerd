package main

import "testing"

func TestNewContainerAndStateTransitions(t *testing.T) {
	c := NewContainer("busybox", []string{"/bin/sh"})
	if c.Status != "created" {
		t.Fatalf("new container status = %q, want %q", c.Status, "created")
	}
	if c.ID == "" {
		t.Fatal("new container has empty ID")
	}

	c.markRunning()
	if c.Status != "running" {
		t.Fatalf("after markRunning, status = %q, want %q", c.Status, "running")
	}

	c.markExited()
	if c.Status != "exited" {
		t.Fatalf("after markExited, status = %q, want %q", c.Status, "exited")
	}
}

func TestWriteAndReadContainer(t *testing.T) {
	old := containersRoot
	containersRoot = t.TempDir()
	defer func() { containersRoot = old }()

	c := NewContainer("busybox", []string{"/bin/sh"})
	c.Bundle = "/some/bundle"
	c.Volumes = []string{"/host:/data"}
	if err := writeNewContainerOnDisk(c); err != nil {
		t.Fatalf("writeNewContainerOnDisk: %v", err)
	}

	got, err := readContainer(c.ID)
	if err != nil {
		t.Fatalf("readContainer: %v", err)
	}
	if got.ID != c.ID || got.Bundle != c.Bundle || len(got.Volumes) != 1 {
		t.Fatalf("readContainer = %+v, want match for %+v", got, c)
	}
}

func TestListContainers(t *testing.T) {
	old := containersRoot
	containersRoot = t.TempDir()
	defer func() { containersRoot = old }()

	for i := 0; i < 3; i++ {
		c := NewContainer("busybox", []string{"/bin/sh"})
		if err := writeNewContainerOnDisk(c); err != nil {
			t.Fatalf("writeNewContainerOnDisk: %v", err)
		}
	}

	containers, err := ListContainers()
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
	if len(containers) != 3 {
		t.Fatalf("ListContainers returned %d containers, want 3", len(containers))
	}
}
