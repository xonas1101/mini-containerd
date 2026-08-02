package main

import (
	"os"
	"path/filepath"
	"testing"
)

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

func TestResolveContainerID(t *testing.T) {
	old := containersRoot
	containersRoot = t.TempDir()
	defer func() { containersRoot = old }()

	c := NewContainer("busybox", []string{"/bin/sh"})
	if err := writeNewContainerOnDisk(c); err != nil {
		t.Fatalf("writeNewContainerOnDisk: %v", err)
	}

	if got, err := resolveContainerID(c.ID); err != nil || got != c.ID {
		t.Fatalf("resolveContainerID(full id) = (%q, %v), want (%q, nil)", got, err, c.ID)
	}

	prefix := c.ID[:4]
	if got, err := resolveContainerID(prefix); err != nil || got != c.ID {
		t.Fatalf("resolveContainerID(%q) = (%q, %v), want (%q, nil)", prefix, got, err, c.ID)
	}

	if _, err := resolveContainerID("doesnotexist"); err == nil {
		t.Fatal("expected error resolving a nonexistent id")
	}
}

func TestResolveContainerIDAmbiguous(t *testing.T) {
	old := containersRoot
	containersRoot = t.TempDir()
	defer func() { containersRoot = old }()

	if err := os.MkdirAll(filepath.Join(containersRoot, "abc123"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(containersRoot, "abc456"), 0755); err != nil {
		t.Fatal(err)
	}

	if _, err := resolveContainerID("abc"); err == nil {
		t.Fatal("expected error resolving an ambiguous prefix")
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
