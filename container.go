package main

import (
	"encoding/json"
	"log"
	"os"
	"time"

	"github.com/thanhpk/randstr"
)

type Container struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"` // "created", "running", "stopped", "exited"
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

func writeNewContainerOnDisk(c *Container) error {
	dir := "/var/lib/mini-docker/containers/" + c.ID
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("failed to create container directory %s: %v", dir, err)
		return err
	}
	if err := os.MkdirAll(dir+"/rootfs", 0755); err != nil {
		log.Printf("failed to create rootfs directory for container %s: %v", c.ID, err)
		return err
	}
	f, err := os.Create(dir + "/config.json")
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
	c.Status = "stopped"
	log.Printf("container %s stopped", c.ID)
}
