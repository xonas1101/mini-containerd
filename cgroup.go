package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const cgroupRoot = "/sys/fs/cgroup"

func cgroupPath(id string) string {
	return filepath.Join(cgroupRoot, "mini-containerd-"+id)
}

// SetupCgroup creates a cgroup v2 directory for the container, applies the
// memory/cpu limits (either may be empty/zero to skip), and moves pid into
// it. Call this right after starting the child process and before it execs
// the container's target command — cgroup membership is inherited across
// exec, so the limits are in effect before the real workload runs.
func SetupCgroup(id string, pid int, memory string, cpus float64) error {
	dir := cgroupPath(id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create cgroup: %w", err)
	}

	if memory != "" {
		bytes, err := parseMemory(memory)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "memory.max"), []byte(strconv.FormatInt(bytes, 10)), 0644); err != nil {
			return fmt.Errorf("set memory.max: %w", err)
		}
	}

	if cpus > 0 {
		const period = 100000
		quota := int64(cpus * float64(period))
		val := fmt.Sprintf("%d %d", quota, period)
		if err := os.WriteFile(filepath.Join(dir, "cpu.max"), []byte(val), 0644); err != nil {
			return fmt.Errorf("set cpu.max: %w", err)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0644); err != nil {
		return fmt.Errorf("add pid to cgroup: %w", err)
	}
	return nil
}

// CleanupCgroup removes the container's cgroup directory. Call this once the
// container's process has exited (a non-empty cgroup directory can't be
// rmdir'd until it has no member processes left).
func CleanupCgroup(id string) error {
	err := os.Remove(cgroupPath(id))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func parseMemory(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty memory value")
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'g':
		mult = 1 << 30
		s = s[:len(s)-1]
	case 'm':
		mult = 1 << 20
		s = s[:len(s)-1]
	case 'k':
		mult = 1 << 10
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid memory value %q: %w", s, err)
	}
	return n * mult, nil
}
