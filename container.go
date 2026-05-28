package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/thanhpk/randstr"
)

const containersRoot = "/var/lib/mini-containerd/containers"

type Container struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"` // "created", "running", "exited"
	PID       int       `json:"pid"`
	Image     string    `json:"image"`
	Command   []string  `json:"command"`
	CreatedAt time.Time `json:"created_at"`
	ExitCode  int       `json:"exit_code"`
}

func NewContainer(image string, command []string) *Container {
	c := &Container{
		ID:        randstr.String(16),
		Status:    "created",
		Image:     image,
		Command:   command,
		CreatedAt: time.Now(),
	}
	return c
}

func containerDir(id string) string {
	return filepath.Join(containersRoot, id)
}

func writeNewContainerOnDisk(c *Container) error {
	dir := containerDir(c.ID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("failed to create container directory %s: %v", dir, err)
		return err
	}
	f, err := os.Create(filepath.Join(dir, "config.json"))
	if err != nil {
		log.Printf("failed to create config.json for container %s: %v", c.ID, err)
		return err
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(c); err != nil {
		log.Printf("failed to encode container %s to disk: %v", c.ID, err)
		return err
	}
	log.Printf("container %s written to disk", c.ID)
	return nil
}

func (c *Container) StartContainer() {
	c.Status = "running"
	log.Printf("container %s started", c.ID)
}

func (c *Container) StopContainer() {
	c.Status = "exited"
	log.Printf("container %s exited", c.ID)
}
