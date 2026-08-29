// Package transfer implements the resumable, encrypted, chunked file
// transfer protocol that runs over a connected net.Conn once discovery
// (LAN multicast or, in a later milestone, rendezvous+relay) has produced
// one.
//
// Wire shape: every message, in both the plaintext handshake and the
// encrypted phase that follows it, is a 4-byte big-endian length prefix
// followed by that many bytes. After the handshake, message bodies are
// AES-GCM ciphertext produced by internal/crypto.Stream, keyed so that the
// sender's outgoing chunk stream and the receiver's outgoing control
// stream use independent keys and independent sequence-number spaces.
package transfer

import (
	"encoding/binary"
	"fmt"
	"io"
)

// ChunkSize is the amount of plaintext file data per encrypted record.
const ChunkSize = 256 * 1024

const (
	maxHandshakeFrame = 4096             // ephemeral pubkeys / confirmation MACs
	maxHeaderFrame     = 32 * 1024 * 1024 // JSON header, dominated by the per-chunk hash list
	maxControlFrame    = 4096             // ResumeFrom / Complete JSON
	maxChunkFrame       = ChunkSize + 1024 // ciphertext + GCM tag + framing slack
)

func writeFrame(w io.Writer, payload []byte) error {
	if len(payload) > 0xFFFFFFFF {
		return fmt.Errorf("transfer: frame too large (%d bytes)", len(payload))
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(payload)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("transfer: write frame length: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("transfer: write frame payload: %w", err)
	}
	return nil
}

func readFrame(r io.Reader, maxLen int) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if int(n) > maxLen {
		return nil, fmt.Errorf("transfer: frame length %d exceeds max %d", n, maxLen)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("transfer: read frame payload: %w", err)
	}
	return buf, nil
}

// Header is sent once by the sender, encrypted, at sequence 0 of its
// outgoing stream. ChunkHashes lets the receiver validate any existing
// partial download before trusting it for resume, and lets it verify each
// incoming chunk as it arrives.
type Header struct {
	Name        string   `json:"name"`
	Size        int64    `json:"size"`
	ChunkSize   int      `json:"chunk_size"`
	TotalChunks int64    `json:"total_chunks"`
	ChunkHashes []string `json:"chunk_hashes"` // hex SHA-256, index-aligned with chunk number

	// IsArchive and IsCompressed describe the bytes actually being sent
	// (Size/ChunkHashes are over these transport bytes, not the original
	// content) so the receiver knows how to reverse the transformation
	// once every chunk has arrived and been verified. See internal/source.
	IsArchive    bool `json:"is_archive"`
	IsCompressed bool `json:"is_compressed"`
}

// ResumeFrom is sent once by the receiver, encrypted, at sequence 0 of its
// outgoing stream, immediately after it has read Header and consulted any
// existing .part.meta. NextChunk is the chunk index the sender should
// start (or restart) sending from; 0 means "send everything."
type ResumeFrom struct {
	NextChunk int64 `json:"next_chunk"`
}

// Complete is sent once by the receiver, encrypted, at sequence 1 of its
// outgoing stream, after every chunk has been received and verified. The
// sender must wait for this before reporting success — having written every
// chunk to the socket is not the same as the receiver confirming integrity.
type Complete struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}
