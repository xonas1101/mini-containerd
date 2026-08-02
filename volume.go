package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// mountVolumes bind-mounts each "host:container[:ro]" spec into rootfs. Must
// run before pivotRoot — host paths are only resolvable while the real root
// filesystem is still in view, so targets are rootfs-prefixed here rather
// than container-absolute the way mountAll's post-pivot mounts are.
func mountVolumes(rootfs string, volumes []string) error {
	for _, v := range volumes {
		hostPath, containerPath, readonly, err := parseVolume(v)
		if err != nil {
			return err
		}

		if _, err := os.Stat(hostPath); err != nil {
			return fmt.Errorf("volume host path %q: %w", hostPath, err)
		}

		target := filepath.Join(rootfs, containerPath)
		if err := os.MkdirAll(target, 0755); err != nil {
			return err
		}

		if err := syscall.Mount(hostPath, target, "", syscall.MS_BIND, ""); err != nil {
			return fmt.Errorf("bind mount %s -> %s: %w", hostPath, target, err)
		}
		if readonly {
			if err := syscall.Mount("", target, "", syscall.MS_BIND|syscall.MS_REMOUNT|syscall.MS_RDONLY, ""); err != nil {
				return fmt.Errorf("remount %s readonly: %w", target, err)
			}
		}
	}
	return nil
}

// parseVolume parses "host:container[:ro]". containerPath must be an
// absolute, clean path that doesn't escape the rootfs via "..".
func parseVolume(spec string) (hostPath, containerPath string, readonly bool, err error) {
	parts := strings.SplitN(spec, ":", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false, fmt.Errorf("invalid volume spec %q, want host:container[:ro]", spec)
	}
	hostPath = parts[0]
	containerPath = filepath.Clean(parts[1])
	if !filepath.IsAbs(containerPath) || containerPath == ".." || strings.HasPrefix(containerPath, ".."+string(os.PathSeparator)) {
		return "", "", false, fmt.Errorf("invalid volume container path %q", parts[1])
	}
	if len(parts) == 3 {
		if parts[2] != "ro" {
			return "", "", false, fmt.Errorf("invalid volume option %q, want ro", parts[2])
		}
		readonly = true
	}
	return hostPath, containerPath, readonly, nil
}
