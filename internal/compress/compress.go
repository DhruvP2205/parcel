// Package compress wraps stdlib compress/flate (DEFLATE) so the rest of
// the codebase deals in plain io.Reader/io.Writer rather than the flate
// API directly.
//
// This is applied compress-then-encrypt, never the other way around:
// encrypted bytes are indistinguishable from random and don't compress at
// all, so compression only makes sense before internal/crypto touches
// anything. See internal/source for the "is it even worth compressing
// this?" size-comparison heuristic — flate on an already-compressed file
// (a JPEG, an MP4, a ZIP) can end up very slightly larger than the input,
// and this package intentionally does not hide that; it just deflates
// whatever it's given.
package compress

import (
	"compress/flate"
	"fmt"
	"io"
)

// CompressTo streams src through DEFLATE into dst.
func CompressTo(dst io.Writer, src io.Reader) error {
	fw, err := flate.NewWriter(dst, flate.DefaultCompression)
	if err != nil {
		return fmt.Errorf("compress: new writer: %w", err)
	}
	if _, err := io.Copy(fw, src); err != nil {
		fw.Close()
		return fmt.Errorf("compress: %w", err)
	}
	if err := fw.Close(); err != nil {
		return fmt.Errorf("compress: flush: %w", err)
	}
	return nil
}

// DecompressTo streams src through an inflate reader into dst.
func DecompressTo(dst io.Writer, src io.Reader) error {
	fr := flate.NewReader(src)
	defer fr.Close()
	if _, err := io.Copy(dst, fr); err != nil {
		return fmt.Errorf("decompress: %w", err)
	}
	return nil
}
