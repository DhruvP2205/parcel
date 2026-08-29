package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrSequenceReuse is returned when a caller tries to encrypt or decrypt
// using a sequence number that is not strictly greater than the last one
// seen on this stream. Because the AEAD nonce is derived deterministically
// from the sequence number, allowing reuse here would allow nonce reuse,
// which breaks AES-GCM's confidentiality and integrity guarantees. This
// also doubles as replay/duplicate-chunk rejection on the receive side.
var ErrSequenceReuse = errors.New("crypto: sequence number reused or out of order")

// Stream is one direction of an authenticated, chunked byte stream keyed by
// a session key. A fresh Stream must be created for every fresh handshake
// (e.g. after a resume reconnect derives a new Session) — it is never
// safe to reuse a Stream across two different underlying connections.
type Stream struct {
	aead     cipher.AEAD
	haveLast bool
	lastSeq  uint64
}

// NewStream builds the AES-256-GCM AEAD for one directional key.
func NewStream(key [32]byte) (*Stream, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("crypto: new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: new gcm: %w", err)
	}
	return &Stream{aead: aead}, nil
}

// nonceFor deterministically derives a 12-byte GCM nonce from a chunk
// sequence number. Safe because each Stream's key is unique to one
// direction of one session, and callers are required to pass strictly
// increasing sequence numbers.
func nonceFor(seq uint64) []byte {
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[4:], seq)
	return nonce
}

// Encrypt seals plaintext for chunk seq, authenticating aad (e.g. chunk
// metadata) alongside it. seq must be strictly greater than every seq
// passed to a previous Encrypt call on this Stream.
func (s *Stream) Encrypt(seq uint64, plaintext, aad []byte) ([]byte, error) {
	if err := s.advance(seq); err != nil {
		return nil, err
	}
	return s.aead.Seal(nil, nonceFor(seq), plaintext, aad), nil
}

// Decrypt opens ciphertext for chunk seq. Returns ErrSequenceReuse for a
// replayed or out-of-order-low seq, and an authentication error (from
// crypto/cipher) for tampered ciphertext or the wrong key.
func (s *Stream) Decrypt(seq uint64, ciphertext, aad []byte) ([]byte, error) {
	if err := s.advance(seq); err != nil {
		return nil, err
	}
	plaintext, err := s.aead.Open(nil, nonceFor(seq), ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("crypto: decrypt chunk %d: %w", seq, err)
	}
	return plaintext, nil
}

func (s *Stream) advance(seq uint64) error {
	if s.haveLast && seq <= s.lastSeq {
		return ErrSequenceReuse
	}
	s.lastSeq = seq
	s.haveLast = true
	return nil
}
