package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	pcrypto "parcel/internal/crypto"
)

// ErrConnectionLost distinguishes a dropped connection (safe, and usually
// worth retrying — the receiver's .part file is intact) from a hard
// protocol/handshake failure (not safe to blindly retry, e.g. a wrong
// code).
var ErrConnectionLost = errors.New("transfer: connection lost before transfer completed")

// SendOptions describes how to present the bytes at path to the receiver.
// Zero value means: report filepath.Base(path) as the name, plain file,
// uncompressed — exactly M2's original behavior.
type SendOptions struct {
	// Name overrides the reported file/folder name (used when path is a
	// temporary archived/compressed stand-in — see internal/source).
	Name         string
	IsArchive    bool
	IsCompressed bool
}

// Send runs the sender side of the protocol over an already-connected
// conn: precompute chunk hashes, handshake, send the header, honor the
// receiver's resume point, stream chunks, and wait for the receiver's
// final integrity confirmation.
func Send(conn net.Conn, code, path string, opts SendOptions) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	totalChunks := size / ChunkSize
	if size%ChunkSize != 0 {
		totalChunks++
	}

	hashes, err := chunkHashes(f, size, totalChunks)
	if err != nil {
		return fmt.Errorf("transfer: hashing %s: %w", path, err)
	}

	conn.SetDeadline(time.Now().Add(HandshakeTimeout))
	session, _, _, err := PerformHandshake(conn, code, pcrypto.RoleInitiator)
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

	name := opts.Name
	if name == "" {
		name = filepath.Base(path)
	}
	header := Header{
		Name:         name,
		Size:         size,
		ChunkSize:    ChunkSize,
		TotalChunks:  totalChunks,
		ChunkHashes:  hashes,
		IsArchive:    opts.IsArchive,
		IsCompressed: opts.IsCompressed,
	}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return err
	}
	if err := sendEncrypted(conn, sendStream, 0, headerBytes); err != nil {
		return fmt.Errorf("%w: sending header: %v", ErrConnectionLost, err)
	}

	conn.SetDeadline(time.Now().Add(HandshakeTimeout))
	rfBytes, err := readEncrypted(conn, recvStream, 0, maxControlFrame)
	if err != nil {
		return fmt.Errorf("%w: reading resume point: %v", ErrConnectionLost, err)
	}
	var rf ResumeFrom
	if err := json.Unmarshal(rfBytes, &rf); err != nil {
		return fmt.Errorf("transfer: parse resume point: %w", err)
	}
	if rf.NextChunk < 0 || rf.NextChunk > totalChunks {
		return fmt.Errorf("transfer: receiver reported invalid resume point %d for %d chunks", rf.NextChunk, totalChunks)
	}

	if _, err := f.Seek(rf.NextChunk*ChunkSize, io.SeekStart); err != nil {
		return err
	}
	buf := make([]byte, ChunkSize)
	for i := rf.NextChunk; i < totalChunks; i++ {
		n, err := io.ReadFull(f, buf)
		if err != nil && err != io.ErrUnexpectedEOF {
			return fmt.Errorf("transfer: reading chunk %d: %w", i, err)
		}
		seq := uint64(i) + 1
		if err := sendEncrypted(conn, sendStream, seq, buf[:n]); err != nil {
			return fmt.Errorf("%w: sending chunk %d: %v", ErrConnectionLost, i, err)
		}
	}

	conn.SetDeadline(time.Now().Add(StallTimeout))
	compBytes, err := readEncrypted(conn, recvStream, 1, maxControlFrame)
	if err != nil {
		return fmt.Errorf("%w: waiting for completion confirmation: %v", ErrConnectionLost, err)
	}
	var comp Complete
	if err := json.Unmarshal(compBytes, &comp); err != nil {
		return fmt.Errorf("transfer: parse completion message: %w", err)
	}
	if !comp.OK {
		return fmt.Errorf("transfer: receiver reported failure: %s", comp.Message)
	}
	return nil
}

// chunkHashes makes one streaming pass over f to hash every chunk before
// the header is sent — the receiver needs the full hash list up front to
// validate resume state and verify chunks as they arrive. This costs a
// second full read of the file (the send loop reads it again), trading
// I/O for bounded memory; see STDLIB.md.
func chunkHashes(f *os.File, size, totalChunks int64) ([]string, error) {
	hashes := make([]string, 0, totalChunks)
	buf := make([]byte, ChunkSize)
	for i := int64(0); i < totalChunks; i++ {
		n, err := io.ReadFull(f, buf)
		if err != nil && err != io.ErrUnexpectedEOF {
			return nil, err
		}
		sum := sha256.Sum256(buf[:n])
		hashes = append(hashes, hex.EncodeToString(sum[:]))
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return hashes, nil
}

func sendEncrypted(conn net.Conn, s *pcrypto.Stream, seq uint64, plaintext []byte) error {
	ct, err := s.Encrypt(seq, plaintext, nil)
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Now().Add(StallTimeout))
	return writeFrame(conn, ct)
}

func readEncrypted(conn net.Conn, s *pcrypto.Stream, seq uint64, maxLen int) ([]byte, error) {
	conn.SetDeadline(time.Now().Add(StallTimeout))
	ct, err := readFrame(conn, maxLen)
	if err != nil {
		return nil, err
	}
	return s.Decrypt(seq, ct, nil)
}
