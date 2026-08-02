package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thanhpk/randstr"
)

var containersRoot = "/var/lib/mini-containerd/containers"

// Status values: "created" -> "running" -> "stopped"/"exited". "stopped"
// means an explicit stop/kill (restartable via StartContainer); "exited"
// means the process ended on its own (e.g. a foreground `run` finishing).
type Container struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	PID       int       `json:"pid"`
	Image     string    `json:"image"`
	Bundle    string    `json:"bundle"` // OCI bundle dir used to launch this container
	Command   []string  `json:"command"`
	CreatedAt time.Time `json:"created_at"`
	ExitCode  int       `json:"exit_code"`
	Volumes   []string  `json:"volumes,omitempty"` // "host:container[:ro]" specs
	Detach    bool      `json:"detach"`
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

// resolveContainerID accepts a full ID or an unambiguous prefix of one
// (like Docker's short IDs) and returns the full ID. Scans containersRoot
// directly rather than going through ListContainers/readContainer, since
// readContainer calls this and that would recurse.
func resolveContainerID(idOrPrefix string) (string, error) {
	if idOrPrefix == "" {
		return "", fmt.Errorf("container name required")
	}
	if _, err := os.Stat(filepath.Join(containerDir(idOrPrefix), "config.json")); err == nil {
		return idOrPrefix, nil
	}

	entries, err := os.ReadDir(containersRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("container %q not found", idOrPrefix)
		}
		return "", err
	}

	var matches []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), idOrPrefix) {
			matches = append(matches, e.Name())
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("container %q not found", idOrPrefix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("container ID %q is ambiguous, matches: %s", idOrPrefix, strings.Join(matches, ", "))
	}
}

func readContainer(idOrPrefix string) (*Container, error) {
	id, err := resolveContainerID(idOrPrefix)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(containerDir(id), "config.json"))
	if err != nil {
		return nil, err
	}
	var c Container
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// ListContainers returns every container under containersRoot, skipping
// (and logging) any whose config.json can't be read.
func ListContainers() ([]*Container, error) {
	entries, err := os.ReadDir(containersRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var containers []*Container
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		c, err := readContainer(e.Name())
		if err != nil {
			log.Printf("skipping container %s: %v", e.Name(), err)
			continue
		}
		containers = append(containers, c)
	}
	return containers, nil
}

func (c *Container) markRunning() {
	c.Status = "running"
	log.Printf("container %s started", c.ID)
}

func (c *Container) markExited() {
	c.Status = "exited"
	log.Printf("container %s exited", c.ID)
}
