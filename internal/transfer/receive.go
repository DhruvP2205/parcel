package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"parcel/internal/archive"
	"parcel/internal/compress"
	pcrypto "parcel/internal/crypto"
)

// Receive runs the receiver side of the protocol over an already-connected
// conn: handshake, read the header, consult any existing .part.meta to
// decide a resume point, tell the sender, then stream and verify chunks
// into <name>.part until done, finally renaming to the real name. A
// partial file and its .meta sidecar are never deleted on failure — only
// on successful completion (renamed away) or when a fresh, unrelated
// transfer explicitly starts over.
func Receive(conn net.Conn, code, outDir string) error {
	conn.SetDeadline(time.Now().Add(HandshakeTimeout))
	session, _, _, err := PerformHandshake(conn, code, pcrypto.RoleResponder)
	if err != nil {
		return err
	}

	sendStream, err := pcrypto.NewStream(session.SendKey)
	if err != nil {
		return err
	}
	recvStream, err := pcrypto.NewStream(session.RecvKey)
	if err != nil {
		return err
	}

	headerBytes, err := readEncrypted(conn, recvStream, 0, maxHeaderFrame)
	if err != nil {
		return fmt.Errorf("%w: reading header: %v", ErrConnectionLost, err)
	}
	var header Header
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return fmt.Errorf("transfer: parse header: %w", err)
	}
	if header.ChunkSize != ChunkSize || int64(len(header.ChunkHashes)) != header.TotalChunks {
		return fmt.Errorf("transfer: malformed header from sender")
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	partPath := filepath.Join(outDir, header.Name+".part")
	metaPath := partPath + ".meta"

	nextChunk := int64(0)
	meta, ok := loadMeta(metaPath)
	if ok && meta.Size == header.Size && sameHashes(meta.ChunkHashes, header.ChunkHashes) {
		nextChunk = meta.HighestVerified + 1
	} else {
		os.Remove(partPath)
		meta = Meta{Name: header.Name, Size: header.Size, ChunkHashes: header.ChunkHashes, HighestVerified: -1}
		if err := saveMeta(metaPath, meta); err != nil {
			return err
		}
	}

	rf := ResumeFrom{NextChunk: nextChunk}
	rfBytes, err := json.Marshal(rf)
	if err != nil {
		return err
	}
	if err := sendEncrypted(conn, sendStream, 0, rfBytes); err != nil {
		return fmt.Errorf("%w: sending resume point: %v", ErrConnectionLost, err)
	}

	f, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Seek(nextChunk*ChunkSize, io.SeekStart); err != nil {
		f.Close()
		return err
	}

	for i := nextChunk; i < header.TotalChunks; i++ {
		seq := uint64(i) + 1
		plaintext, err := readEncrypted(conn, recvStream, seq, maxChunkFrame)
		if err != nil {
			f.Close()
			return fmt.Errorf("%w: reading chunk %d: %v", ErrConnectionLost, i, err)
		}
		sum := sha256.Sum256(plaintext)
		if hex.EncodeToString(sum[:]) != header.ChunkHashes[i] {
			f.Close()
			return fmt.Errorf("transfer: chunk %d failed integrity check", i)
		}
		if _, err := f.Write(plaintext); err != nil {
			f.Close()
			return err
		}
		meta.HighestVerified = i
		if err := saveMeta(metaPath, meta); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}

	comp := Complete{OK: true}
	compBytes, err := json.Marshal(comp)
	if err != nil {
		return err
	}
	if err := sendEncrypted(conn, sendStream, 1, compBytes); err != nil {
		// The file is fully verified on disk even though the sender may
		// never learn that — safe to treat as success locally.
		_ = err
	}

	if err := finalize(partPath, outDir, header); err != nil {
		return err
	}
	os.Remove(metaPath)
	return nil
}

// finalize turns the fully-received, hash-verified transport bytes at
// partPath into the real deliverable: decompress if needed, then unpack if
// it was a directory, or just rename into place for a plain file. This
// only ever runs after every chunk has already passed its hash check, so a
// failure here indicates a sender-side bug, not a network problem — it is
// not retried, unlike ErrConnectionLost.
func finalize(partPath, outDir string, header Header) error {
	contentPath := partPath
	if header.IsCompressed {
		decompressedPath := partPath + ".decompressed"
		if err := decompressFile(contentPath, decompressedPath); err != nil {
			return fmt.Errorf("transfer: decompressing %s: %w", header.Name, err)
		}
		os.Remove(contentPath)
		contentPath = decompressedPath
	}

	if header.IsArchive {
		cf, err := os.Open(contentPath)
		if err != nil {
			return err
		}
		destDir := filepath.Join(outDir, header.Name)
		err = archive.Unpack(cf, destDir)
		cf.Close()
		if err != nil {
			return fmt.Errorf("transfer: unpacking %s: %w", header.Name, err)
		}
		os.Remove(contentPath)
		return nil
	}

	finalPath := filepath.Join(outDir, header.Name)
	return os.Rename(contentPath, finalPath)
}

func decompressFile(srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	if err := compress.DecompressTo(dst, src); err != nil {
		dst.Close()
		os.Remove(dstPath)
		return err
	}
	return dst.Close()
}
