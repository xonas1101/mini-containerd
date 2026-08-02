package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
)

// ShowLogs prints a container's captured stdout/stderr. If tail > 0, only
// the last tail lines are printed.
func ShowLogs(idOrPrefix string, tail int) error {
	id, err := resolveContainerID(idOrPrefix)
	if err != nil {
		return err
	}
	path := filepath.Join(containerDir(id), "logs.txt")
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("no logs for container %q: %w", id, err)
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return err
	}

	start := 0
	if tail > 0 && tail < len(lines) {
		start = len(lines) - tail
	}
	for _, l := range lines[start:] {
		fmt.Println(l)
	}
	return nil
}
