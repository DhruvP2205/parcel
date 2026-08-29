// Package archive implements a small, hand-rolled format for packing a
// directory tree into a single byte stream and unpacking it again — the
// stdlib alternative to reaching for archive/tar or archive/zip.
//
// Wire shape: a flat sequence of entries, each
// [1-byte type]['F' file / 'D' directory][2-byte path length][path, forward-
// slash separated][8-byte size (files only, 0 for directories)]
// [4-byte permission bits][file content, exactly size bytes]. The stream
// simply ends at EOF — there is no entry count or index, so both Pack and
// Unpack are single streaming passes with bounded memory regardless of how
// many files or how large the tree is.
//
// Directory entries exist only so empty directories survive a round trip;
// a file entry's parent directories are always created implicitly.
package archive

import (
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	typeFile      byte = 'F'
	typeDirectory byte = 'D'
)

// Pack walks srcDir and writes the archive stream to w.
func Pack(srcDir string, w io.Writer) error {
	return filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil // don't record the root itself
		}
		relSlash := filepath.ToSlash(rel)

		info, err := d.Info()
		if err != nil {
			return err
		}

		if d.IsDir() {
			return writeEntryHeader(w, typeDirectory, relSlash, 0, uint32(info.Mode().Perm()))
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("archive: %s: unsupported file type %v (only regular files and directories are supported)", path, d.Type())
		}

		if err := writeEntryHeader(w, typeFile, relSlash, uint64(info.Size()), uint32(info.Mode().Perm())); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		n, err := io.Copy(w, f)
		if err != nil {
			return fmt.Errorf("archive: reading %s: %w", path, err)
		}
		if n != info.Size() {
			return fmt.Errorf("archive: %s changed size while being archived (expected %d, read %d)", path, info.Size(), n)
		}
		return nil
	})
}

// Unpack reads a stream produced by Pack and recreates it under destDir,
// which is created if it doesn't already exist (including for an archive
// with zero entries — an originally empty source directory).
func Unpack(r io.Reader, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	for {
		typ, relPath, size, mode, err := readEntryHeader(r)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if !safeRelPath(relPath) {
			return fmt.Errorf("archive: unsafe path in archive: %q", relPath)
		}
		target := filepath.Join(destDir, filepath.FromSlash(relPath))

		switch typ {
		case typeDirectory:
			if err := os.MkdirAll(target, os.FileMode(mode)|0o700); err != nil {
				return err
			}
		case typeFile:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(mode)|0o600)
			if err != nil {
				return err
			}
			if _, err := io.CopyN(f, r, int64(size)); err != nil {
				f.Close()
				return fmt.Errorf("archive: writing %s: %w", relPath, err)
			}
			if err := f.Close(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("archive: unknown entry type %q for %q", typ, relPath)
		}
	}
}

// safeRelPath rejects absolute paths and any ".." path segment, closing
// off the classic zip-slip path-traversal class of bug (a malicious or
// corrupted archive writing outside destDir).
func safeRelPath(p string) bool {
	if p == "" || filepath.IsAbs(p) {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." || seg == "" {
			return false
		}
	}
	return true
}

func writeEntryHeader(w io.Writer, typ byte, relPath string, size uint64, mode uint32) error {
	pathBytes := []byte(relPath)
	if len(pathBytes) > 0xFFFF {
		return fmt.Errorf("archive: path too long (%d bytes): %s", len(pathBytes), relPath)
	}
	hdr := make([]byte, 1+2+len(pathBytes)+8+4)
	i := 0
	hdr[i] = typ
	i++
	binary.BigEndian.PutUint16(hdr[i:], uint16(len(pathBytes)))
	i += 2
	i += copy(hdr[i:], pathBytes)
	binary.BigEndian.PutUint64(hdr[i:], size)
	i += 8
	binary.BigEndian.PutUint32(hdr[i:], mode)
	_, err := w.Write(hdr)
	return err
}

func readEntryHeader(r io.Reader) (typ byte, relPath string, size uint64, mode uint32, err error) {
	var typBuf [1]byte
	if _, err = io.ReadFull(r, typBuf[:]); err != nil {
		return 0, "", 0, 0, err // clean io.EOF here means "no more entries"
	}
	typ = typBuf[0]

	var lenBuf [2]byte
	if _, err = io.ReadFull(r, lenBuf[:]); err != nil {
		return 0, "", 0, 0, fmt.Errorf("archive: truncated entry header: %w", err)
	}
	pathLen := binary.BigEndian.Uint16(lenBuf[:])
	pathBytes := make([]byte, pathLen)
	if _, err = io.ReadFull(r, pathBytes); err != nil {
		return 0, "", 0, 0, fmt.Errorf("archive: truncated entry path: %w", err)
	}

	var restBuf [12]byte
	if _, err = io.ReadFull(r, restBuf[:]); err != nil {
		return 0, "", 0, 0, fmt.Errorf("archive: truncated entry metadata: %w", err)
	}
	size = binary.BigEndian.Uint64(restBuf[0:8])
	mode = binary.BigEndian.Uint32(restBuf[8:12])
	return typ, string(pathBytes), size, mode, nil
}
