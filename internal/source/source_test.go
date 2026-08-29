package source

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareSingleFileNoCompression(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	p, err := Prepare(path, false)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer p.Cleanup()

	if p.Path != path {
		t.Errorf("expected fast path to reuse the original file, got %q", p.Path)
	}
	if p.IsArchive || p.IsCompressed {
		t.Error("plain file with compress=false should be neither archived nor compressed")
	}
	if p.Name != "note.txt" {
		t.Errorf("got name %q", p.Name)
	}
}

func TestPrepareCompressesHighlyCompressibleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repetitive.txt")
	content := []byte(strings.Repeat("aaaaaaaaaa", 10000))
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	p, err := Prepare(path, true)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer p.Cleanup()

	if !p.IsCompressed {
		t.Fatal("expected highly repetitive content to be compressed")
	}
	info, err := os.Stat(p.Path)
	if err != nil {
		t.Fatalf("stat prepared path: %v", err)
	}
	if info.Size() >= int64(len(content)) {
		t.Errorf("compressed size %d should be smaller than original %d", info.Size(), len(content))
	}
}

func TestPrepareSkipsCompressionForIncompressibleData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "random.bin")
	data := make([]byte, 64*1024)
	if _, err := rand.Read(data); err != nil {
		t.Fatalf("rand: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	p, err := Prepare(path, true)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer p.Cleanup()

	if p.IsCompressed {
		t.Error("expected random incompressible data to fall back to uncompressed")
	}
	if p.Path != path {
		t.Errorf("expected fallback to reuse the original file path, got %q", p.Path)
	}
}

func TestPrepareArchivesDirectory(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "myfolder")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("world"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	p, err := Prepare(src, false)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer p.Cleanup()

	if !p.IsArchive {
		t.Fatal("expected a directory to be archived")
	}
	if p.Name != "myfolder" {
		t.Errorf("got name %q", p.Name)
	}
	if p.Path == src {
		t.Error("archived content should live in a temp file, not the original directory")
	}
	if _, err := os.Stat(p.Path); err != nil {
		t.Fatalf("expected archive temp file to exist: %v", err)
	}

	p.Cleanup()
	if _, err := os.Stat(p.Path); !os.IsNotExist(err) {
		t.Error("expected Cleanup to remove the temp archive file")
	}
}

func TestPrepareCleanupNeverTouchesOriginalFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keep-me.txt")
	if err := os.WriteFile(path, []byte("precious"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	p, err := Prepare(path, false)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	p.Cleanup()

	if _, err := os.Stat(path); err != nil {
		t.Errorf("Cleanup must never remove the caller's original file: %v", err)
	}
}
