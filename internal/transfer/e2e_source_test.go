package transfer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"parcel/internal/source"
)

// These tests chain internal/source.Prepare into Send/Receive over a real
// TCP loopback connection — the same path cmd/parcel wires up — to prove
// folder archiving and compression survive the whole pipeline together,
// not just each package in isolation.

func TestEndToEndFolderTransfer(t *testing.T) {
	srcRoot := t.TempDir()
	srcDir := filepath.Join(srcRoot, "project")
	mustWriteFile(t, filepath.Join(srcDir, "readme.txt"), "top level file")
	mustWriteFile(t, filepath.Join(srcDir, "src", "main.go"), "package main")
	mustWriteFile(t, filepath.Join(srcDir, "src", "deep", "util.go"), "package deep")
	if err := os.MkdirAll(filepath.Join(srcDir, "empty"), 0o755); err != nil {
		t.Fatalf("mkdir empty: %v", err)
	}

	prepared, err := source.Prepare(srcDir, false) // compression covered separately below
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer prepared.Cleanup()
	if !prepared.IsArchive {
		t.Fatal("expected a directory to be prepared as an archive")
	}

	dstDir := t.TempDir()
	client, server := connectedPair(t)
	defer client.Close()
	defer server.Close()

	const code = "ivory-comet-birch"
	errCh := make(chan error, 1)
	go func() {
		errCh <- Send(client, code, prepared.Path, SendOptions{
			Name:      prepared.Name,
			IsArchive: prepared.IsArchive,
		})
	}()
	if err := Receive(server, code, dstDir); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("send: %v", err)
	}

	gotDir := filepath.Join(dstDir, "project")
	assertFileEquals(t, filepath.Join(gotDir, "readme.txt"), "top level file")
	assertFileEquals(t, filepath.Join(gotDir, "src", "main.go"), "package main")
	assertFileEquals(t, filepath.Join(gotDir, "src", "deep", "util.go"), "package deep")
	if info, err := os.Stat(filepath.Join(gotDir, "empty")); err != nil || !info.IsDir() {
		t.Errorf("expected empty subdirectory to survive: %v", err)
	}

	// The transport blob (the temp archive file, now delivered under the
	// folder's name) must not still be sitting there post-unpack.
	if _, err := os.Stat(filepath.Join(dstDir, "project.part")); !os.IsNotExist(err) {
		t.Error("expected no leftover .part transport blob after unpack")
	}
}

func TestEndToEndCompressedFolderTransfer(t *testing.T) {
	srcRoot := t.TempDir()
	srcDir := filepath.Join(srcRoot, "logs")
	// Highly repetitive content so compression is actually worth it and
	// IsCompressed ends up true, exercising the decompress-then-unpack path.
	mustWriteFile(t, filepath.Join(srcDir, "app.log"), strings.Repeat("2026-08-16 INFO steady state\n", 2000))

	prepared, err := source.Prepare(srcDir, true)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer prepared.Cleanup()
	if !prepared.IsCompressed {
		t.Fatal("expected repetitive log content to compress")
	}

	dstDir := t.TempDir()
	client, server := connectedPair(t)
	defer client.Close()
	defer server.Close()

	const code = "sable-quartz-fjord"
	errCh := make(chan error, 1)
	go func() {
		errCh <- Send(client, code, prepared.Path, SendOptions{
			Name:         prepared.Name,
			IsArchive:    prepared.IsArchive,
			IsCompressed: prepared.IsCompressed,
		})
	}()
	if err := Receive(server, code, dstDir); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("send: %v", err)
	}

	assertFileEquals(t, filepath.Join(dstDir, "logs", "app.log"), strings.Repeat("2026-08-16 INFO steady state\n", 2000))
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertFileEquals(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !bytes.Equal(got, []byte(want)) {
		t.Errorf("%s: content mismatch", path)
	}
}
