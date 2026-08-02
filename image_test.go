package main

import (
	"archive/tar"
	"os"
	"path/filepath"
	"testing"
)

func TestFormatSize(t *testing.T) {
	cases := map[int64]string{
		0:                  "0B",
		1023:               "1023B",
		1024:               "1.0KiB",
		1536:               "1.5KiB",
		1024 * 1024:        "1.0MiB",
		1024 * 1024 * 1024: "1.0GiB",
	}
	for in, want := range cases {
		if got := formatSize(in); got != want {
			t.Errorf("formatSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestSafeJoin(t *testing.T) {
	dst := "/rootfs"

	ok := []string{"a", "a/b", "./a/b", "a/./b"}
	for _, name := range ok {
		if _, err := safeJoin(dst, name); err != nil {
			t.Errorf("safeJoin(%q) unexpected error: %v", name, err)
		}
	}

	bad := []string{"../etc/passwd", "..", "/etc/passwd", "a/../../etc"}
	for _, name := range bad {
		if _, err := safeJoin(dst, name); err == nil {
			t.Errorf("safeJoin(%q) = nil error, want error (path escapes dst)", name)
		}
	}
}

func TestRejectSymlinkAncestors(t *testing.T) {
	base := t.TempDir()

	// No ancestor exists yet — should be fine.
	if err := rejectSymlinkAncestors(base, filepath.Join(base, "a", "b")); err != nil {
		t.Fatalf("unexpected error for nonexistent ancestors: %v", err)
	}

	// Plant a symlinked directory, then try to extract "through" it.
	if err := os.Symlink("/etc", filepath.Join(base, "etc")); err != nil {
		t.Fatal(err)
	}
	if err := rejectSymlinkAncestors(base, filepath.Join(base, "etc", "passwd")); err == nil {
		t.Fatal("expected error when an ancestor is a symlink, got nil")
	}

	// A plain directory ancestor should be fine.
	if err := os.Mkdir(filepath.Join(base, "real"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := rejectSymlinkAncestors(base, filepath.Join(base, "real", "file")); err != nil {
		t.Fatalf("unexpected error for real directory ancestor: %v", err)
	}
}

func TestLoadImageAndListImages(t *testing.T) {
	old := imagesRoot
	imagesRoot = t.TempDir()
	defer func() { imagesRoot = old }()

	tarPath := filepath.Join(t.TempDir(), "test.tar")
	writeTestTar(t, tarPath, map[string]string{"hello.txt": "hi\n"})

	if err := LoadImage("myimg", tarPath); err != nil {
		t.Fatalf("LoadImage: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(imageRootfs("myimg"), "hello.txt"))
	if err != nil {
		t.Fatalf("extracted file missing: %v", err)
	}
	if string(data) != "hi\n" {
		t.Fatalf("extracted content = %q, want %q", data, "hi\n")
	}

	if err := LoadImage("myimg", tarPath); err == nil {
		t.Fatal("expected error loading an image name that already exists")
	}

	images, err := ListImages()
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(images) != 1 || images[0].Name != "myimg" {
		t.Fatalf("ListImages = %+v, want one image named myimg", images)
	}
}

func TestLoadImageRejectsInvalidName(t *testing.T) {
	old := imagesRoot
	imagesRoot = t.TempDir()
	defer func() { imagesRoot = old }()

	tarPath := filepath.Join(t.TempDir(), "test.tar")
	writeTestTar(t, tarPath, map[string]string{"a.txt": "a"})

	for _, name := range []string{"..", ".", "../escape", "a/b"} {
		if err := LoadImage(name, tarPath); err == nil {
			t.Errorf("LoadImage(%q) = nil error, want error", name)
		}
	}
}

func writeTestTar(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	tw := tar.NewWriter(f)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0644, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractTarRejectsTraversal(t *testing.T) {
	dst := t.TempDir()
	tarPath := filepath.Join(t.TempDir(), "evil.tar")

	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	content := []byte("pwned")
	if err := tw.WriteHeader(&tar.Header{Name: "../escape.txt", Mode: 0644, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	f.Close()

	if err := extractTar(tarPath, dst); err == nil {
		t.Fatal("expected extractTar to reject a path-traversal entry")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dst), "escape.txt")); err == nil {
		t.Fatal("traversal entry was written outside dst")
	}
}

func TestSha256File(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	sum, err := sha256File(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if sum != want {
		t.Errorf("sha256File = %q, want %q", sum, want)
	}
}
