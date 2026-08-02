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
	"time"
)

const imagesRoot = "/var/lib/mini-containerd/images"

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
func LoadImage(name, tarPath string) error {
	if _, err := os.Stat(imageDir(name)); err == nil {
		return fmt.Errorf("image %q already exists", name)
	}

	rootfs := imageRootfs(name)
	if err := os.MkdirAll(rootfs, 0755); err != nil {
		return err
	}

	id, err := sha256File(tarPath)
	if err != nil {
		return err
	}

	if err := extractTar(tarPath, rootfs); err != nil {
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
	if err := writeImageManifest(img); err != nil {
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

		target := filepath.Join(dst, hdr.Name)

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
