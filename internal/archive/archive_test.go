package archive

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func TestPackUnpackRoundTrip(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.txt"), "hello from a")
	writeFile(t, filepath.Join(src, "sub", "b.txt"), "hello from nested b")
	writeFile(t, filepath.Join(src, "sub", "deeper", "c.txt"), "deeply nested c")
	if err := os.MkdirAll(filepath.Join(src, "empty-dir"), 0o755); err != nil {
		t.Fatalf("mkdir empty-dir: %v", err)
	}
	writeFile(t, filepath.Join(src, "empty-file.txt"), "")

	var buf bytes.Buffer
	if err := Pack(src, &buf); err != nil {
		t.Fatalf("pack: %v", err)
	}

	dst := t.TempDir()
	if err := Unpack(&buf, dst); err != nil {
		t.Fatalf("unpack: %v", err)
	}

	assertFileContent(t, filepath.Join(dst, "a.txt"), "hello from a")
	assertFileContent(t, filepath.Join(dst, "sub", "b.txt"), "hello from nested b")
	assertFileContent(t, filepath.Join(dst, "sub", "deeper", "c.txt"), "deeply nested c")
	assertFileContent(t, filepath.Join(dst, "empty-file.txt"), "")

	info, err := os.Stat(filepath.Join(dst, "empty-dir"))
	if err != nil {
		t.Fatalf("expected empty-dir to survive round trip: %v", err)
	}
	if !info.IsDir() {
		t.Error("empty-dir should be a directory")
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("%s: got %q, want %q", path, got, want)
	}
}

func TestUnpackOnEmptySourceDirectoryCreatesDestDir(t *testing.T) {
	src := t.TempDir() // genuinely empty
	var buf bytes.Buffer
	if err := Pack(src, &buf); err != nil {
		t.Fatalf("pack: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected zero-length archive for an empty source dir, got %d bytes", buf.Len())
	}

	dst := filepath.Join(t.TempDir(), "newly-created")
	if err := Unpack(&buf, dst); err != nil {
		t.Fatalf("unpack: %v", err)
	}
	info, err := os.Stat(dst)
	if err != nil || !info.IsDir() {
		t.Fatalf("expected dest dir to be created even for an empty archive: %v", err)
	}
}

func TestUnpackRejectsPathTraversal(t *testing.T) {
	var buf bytes.Buffer
	// Hand-craft a malicious entry: a file whose path tries to escape destDir.
	if err := writeEntryHeader(&buf, typeFile, "../evil.txt", 4, 0o644); err != nil {
		t.Fatalf("write malicious header: %v", err)
	}
	buf.WriteString("evil")

	dst := t.TempDir()
	err := Unpack(&buf, dst)
	if err == nil {
		t.Fatal("expected Unpack to reject a path-traversal entry")
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(dst), "evil.txt")); !os.IsNotExist(statErr) {
		t.Error("path traversal must not have actually written a file outside destDir")
	}
}

func TestUnpackRejectsAbsolutePath(t *testing.T) {
	var buf bytes.Buffer
	if err := writeEntryHeader(&buf, typeFile, "/etc/evil.txt", 4, 0o644); err != nil {
		t.Fatalf("write malicious header: %v", err)
	}
	buf.WriteString("evil")

	if err := Unpack(&buf, t.TempDir()); err == nil {
		t.Fatal("expected Unpack to reject an absolute path entry")
	}
}

func TestUnpackRejectsTruncatedStream(t *testing.T) {
	var buf bytes.Buffer
	if err := writeEntryHeader(&buf, typeFile, "partial.txt", 100, 0o644); err != nil {
		t.Fatalf("write header: %v", err)
	}
	buf.WriteString("not enough bytes")

	if err := Unpack(&buf, t.TempDir()); err == nil {
		t.Fatal("expected Unpack to reject a stream truncated mid-file")
	}
}

func TestUnpackRejectsTruncatedHeader(t *testing.T) {
	// A single stray byte can't possibly be a valid entry header.
	buf := bytes.NewBuffer([]byte{typeFile})
	if err := Unpack(buf, t.TempDir()); err == nil {
		t.Fatal("expected Unpack to reject a truncated entry header")
	}
}

func TestEntryHeaderRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := writeEntryHeader(&buf, typeFile, "some/nested/path.bin", 424242, 0o640); err != nil {
		t.Fatalf("write: %v", err)
	}
	typ, path, size, mode, err := readEntryHeader(&buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != typeFile || path != "some/nested/path.bin" || size != 424242 || mode != 0o640 {
		t.Errorf("got (%c, %q, %d, %o)", typ, path, size, mode)
	}
}

func TestPathTooLongRejected(t *testing.T) {
	longPath := make([]byte, 70000)
	for i := range longPath {
		longPath[i] = 'a'
	}
	var buf bytes.Buffer
	err := writeEntryHeader(&buf, typeFile, string(longPath), 0, 0o644)
	if err == nil {
		t.Fatal("expected an error for a path exceeding the 16-bit length field")
	}
}

// sanity-check the on-wire length field really is big-endian uint16, since
// a mismatched byte order between write/read would silently corrupt every
// path longer than 255 bytes without ever showing up in a same-process
// round-trip test.
func TestPathLengthFieldIsBigEndian(t *testing.T) {
	var buf bytes.Buffer
	if err := writeEntryHeader(&buf, typeFile, "ab", 0, 0); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw := buf.Bytes()
	gotLen := binary.BigEndian.Uint16(raw[1:3])
	if gotLen != 2 {
		t.Errorf("expected big-endian length 2, got %d", gotLen)
	}
}
