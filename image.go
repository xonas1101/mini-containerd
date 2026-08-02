package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var imagesRoot = "/var/lib/mini-containerd/images"

type Image struct {
	Name      string    `json:"name"`
	ID        string    `json:"id"`
	SizeBytes int64     `json:"size_bytes"`
	Created   time.Time `json:"created"`
	Rootfs    string    `json:"rootfs"`
}

func imageDir(name string) string {
	return filepath.Join(imagesRoot, name)
}

func imageRootfs(name string) string {
	return filepath.Join(imageDir(name), "rootfs")
}

func writeImageManifest(img *Image) error {
	f, err := os.Create(filepath.Join(imageDir(img.Name), "manifest.json"))
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(img)
}

func readImageManifest(name string) (*Image, error) {
	data, err := os.ReadFile(filepath.Join(imageDir(name), "manifest.json"))
	if err != nil {
		return nil, err
	}
	var img Image
	if err := json.Unmarshal(data, &img); err != nil {
		return nil, err
	}
	return &img, nil
}

// LoadImage extracts a .tar or .tar.gz rootfs bundle into imagesRoot/<name>/rootfs
// and writes a manifest.json describing it.
func LoadImage(name, tarPath string) (err error) {
	if filepath.Base(name) != name || name == "." || name == ".." {
		return fmt.Errorf("invalid image name %q", name)
	}
	if _, statErr := os.Stat(imageDir(name)); statErr == nil {
		return fmt.Errorf("image %q already exists", name)
	}

	rootfs := imageRootfs(name)
	if err = os.MkdirAll(rootfs, 0755); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(imageDir(name))
		}
	}()

	id, err := sha256File(tarPath)
	if err != nil {
		return err
	}

	if err = extractTar(tarPath, rootfs); err != nil {
		return err
	}

	size, err := dirSize(rootfs)
	if err != nil {
		return err
	}

	img := &Image{
		Name:      name,
		ID:        "sha256:" + id,
		SizeBytes: size,
		Created:   time.Now(),
		Rootfs:    rootfs,
	}
	if err = writeImageManifest(img); err != nil {
		return err
	}
	log.Printf("image %s loaded (%s, %d bytes)", name, img.ID, size)
	return nil
}

// ListImages returns manifests for every image under imagesRoot.
func ListImages() ([]*Image, error) {
	entries, err := os.ReadDir(imagesRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var images []*Image
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		img, err := readImageManifest(e.Name())
		if err != nil {
			log.Printf("skipping image %s: %v", e.Name(), err)
			continue
		}
		images = append(images, img)
	}
	return images, nil
}

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%dB", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func dirSize(root string) (int64, error) {
	var size int64
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// extractTar unpacks a .tar or .tar.gz file into dst.
func extractTar(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	var r io.Reader = f
	if filepath.Ext(src) == ".gz" || filepath.Ext(src) == ".tgz" {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	}

	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		target, err := safeJoin(dst, hdr.Name)
		if err != nil {
			return err
		}
		if err := rejectSymlinkAncestors(dst, filepath.Dir(target)); err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}
}

// safeJoin resolves a tar entry name against dst, rejecting absolute paths
// and ".." components that would escape dst (tar path traversal / "zip slip").
func safeJoin(dst, name string) (string, error) {
	clean := filepath.Clean(name)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid tar entry path %q", name)
	}
	return filepath.Join(dst, clean), nil
}

// rejectSymlinkAncestors walks dir's path components (relative to base) and
// refuses extraction if any existing ancestor is a symlink. Without this, a
// tar can plant a symlink (e.g. "etc" -> "/etc") and a later entry writing
// through it (e.g. "etc/passwd") would silently escape dst via normal path
// resolution, since Go/the kernel always follow symlinks in intermediate
// path components.
func rejectSymlinkAncestors(base, dir string) error {
	rel, err := filepath.Rel(base, dir)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	cur := base
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to extract through symlink at %s", cur)
		}
	}
	return nil
}
