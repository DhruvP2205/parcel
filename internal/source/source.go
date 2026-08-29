// Package source turns a user-given path (a single file or a directory)
// into the byte stream that internal/transfer actually sends: archived if
// it's a directory, then compressed if that's worth doing.
package source

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"parcel/internal/archive"
	"parcel/internal/compress"
)

// minCompressionSavings requires compression to shrink the content by at
// least this fraction before it's used; otherwise the compressed copy is
// discarded and the uncompressed content is sent as-is. Naive DEFLATE on
// an already-compressed file (JPEG, MP4, ZIP) can end up the same size or
// even a few bytes larger — this heuristic is intentionally simple, not a
// magic-byte content sniffer, documented as a known simplification.
const minCompressionSavings = 0.02

// Prepared describes what to actually hand to transfer.Send.
type Prepared struct {
	Path         string // local path to the (possibly archived/compressed) bytes to send
	Name         string // logical name to report to the receiver
	IsArchive    bool
	IsCompressed bool
	Cleanup      func() // removes any temp files created; always safe to call
}

// Prepare inspects path and, if it's a directory, archives it; then, if
// compress is true, tries DEFLATE and keeps it only if it actually helps.
// The caller's own file is never modified or removed — only files this
// function creates itself are ever cleaned up.
func Prepare(path string, compress_ bool) (Prepared, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Prepared{}, err
	}
	isDir := info.IsDir()
	name := filepath.Base(filepath.Clean(path))

	var temps []string
	cleanup := func() {
		for _, p := range temps {
			os.Remove(p)
		}
	}

	contentPath := path
	if isDir {
		archived, err := writeTemp("parcel-archive-*", func(w io.Writer) error {
			return archive.Pack(path, w)
		})
		if err != nil {
			return Prepared{}, fmt.Errorf("source: archiving %s: %w", path, err)
		}
		temps = append(temps, archived)
		contentPath = archived
	}

	base := Prepared{Path: contentPath, Name: name, IsArchive: isDir, Cleanup: cleanup}
	if !compress_ {
		return base, nil
	}

	compressed, err := writeTemp("parcel-compressed-*", func(w io.Writer) error {
		srcF, err := os.Open(contentPath)
		if err != nil {
			return err
		}
		defer srcF.Close()
		return compress.CompressTo(w, srcF)
	})
	if err != nil {
		// Compression failing is not fatal — fall back to sending the
		// uncompressed content rather than aborting the whole transfer.
		return base, nil
	}

	origSize, origErr := fileSize(contentPath)
	compSize, compErr := fileSize(compressed)
	if origErr != nil || compErr != nil || compSize == 0 || float64(compSize) > float64(origSize)*(1-minCompressionSavings) {
		os.Remove(compressed)
		return base, nil
	}

	temps = append(temps, compressed)
	return Prepared{Path: compressed, Name: name, IsArchive: isDir, IsCompressed: true, Cleanup: cleanup}, nil
}

func writeTemp(pattern string, fill func(io.Writer) error) (string, error) {
	tmp, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	err = fill(tmp)
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

func fileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
