// Package crypto implements parcel's session crypto by composing standard
// library primitives: X25519 (crypto/ecdh) for the key exchange, a
// hand-composed HKDF-Extract/Expand (crypto/hmac + crypto/sha256) to bind
// the exchange to the human pairing code, and AES-256-GCM (crypto/aes +
// crypto/cipher) for the encrypted, authenticated chunk stream.
//
// Threat model: plain X25519 alone protects a passive eavesdropper (nobody
// recording the exchange can compute the shared secret), but not an active
// man-in-the-middle, who can run two independent ECDH exchanges with each
// victim and relay between them. The pairing code closes that gap: it is
// mixed directly into the key derivation (not just a public label), so an
// attacker who does not know the code cannot compute a confirmation MAC
// that either side will accept, even if they fully control the network
// path. This is a lightweight, honestly-scoped construction — not a
// formally proven PAKE like SPAKE2/OPAQUE — and its guarantee is bounded by
// how well the code itself resists guessing within its short lifetime (see
// internal/codeword).
package crypto

import (
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

// Role identifies which side of the handshake a peer is playing. The
// sender that generated the pairing code is always the Initiator.
type Role int

const (
	RoleInitiator Role = iota
	RoleResponder
)

// ErrHandshakeFailed is returned when key confirmation does not match,
// meaning either side used a different code or the exchange was tampered
// with. Callers must treat this as fatal and never proceed to transfer
// data.
var ErrHandshakeFailed = errors.New("crypto: handshake confirmation failed (wrong code or tampered exchange)")

// KeyPair is an ephemeral X25519 keypair generated fresh for one session.
type KeyPair struct {
	priv *ecdh.PrivateKey
}

// GenerateKeyPair creates a fresh ephemeral X25519 keypair.
func GenerateKeyPair() (*KeyPair, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("crypto: generate keypair: %w", err)
	}
	return &KeyPair{priv: priv}, nil
}

// PublicBytes returns the wire-format public key to send to the peer.
func (kp *KeyPair) PublicBytes() []byte {
	return kp.priv.PublicKey().Bytes()
}

// ParsePublicKey parses a peer's wire-format X25519 public key.
func ParsePublicKey(b []byte) (*ecdh.PublicKey, error) {
	pub, err := ecdh.X25519().NewPublicKey(b)
	if err != nil {
		return nil, fmt.Errorf("crypto: parse peer public key: %w", err)
	}
	return pub, nil
}

// Session holds the directional AES-256-GCM keys and confirmation key
// derived from one handshake.
type Session struct {
	SendKey    [32]byte
	RecvKey    [32]byte
	confirmKey [32]byte
}

// DeriveSession runs the code-authenticated key derivation described in the
// package doc. code is the human pairing code exactly as entered on both
// sides; it must never be transmitted over this channel.
func DeriveSession(kp *KeyPair, peerPub *ecdh.PublicKey, code string, role Role) (*Session, error) {
	sharedSecret, err := kp.priv.ECDH(peerPub)
	if err != nil {
		return nil, fmt.Errorf("crypto: ecdh: %w", err)
	}

	// HKDF-Extract(salt=code, IKM=sharedSecret): folding the code into the
	// secret itself (not a public HKDF "info" label) is what gives this
	// construction its MITM resistance — see package doc.
	prk := hmacSum([]byte(code), sharedSecret)

	i2r := expand(prk, "parcel:i2r")
	r2i := expand(prk, "parcel:r2i")
	confirm := expand(prk, "parcel:confirm")

	s := &Session{confirmKey: confirm}
	switch role {
	case RoleInitiator:
		s.SendKey, s.RecvKey = i2r, r2i
	case RoleResponder:
		s.SendKey, s.RecvKey = r2i, i2r
	default:
		return nil, fmt.Errorf("crypto: unknown role %v", role)
	}
	return s, nil
}

// Confirm computes the key-confirmation MAC over the handshake transcript
// (both sides' public keys, initiator first). Both peers compute this
// independently and compare; a mismatch means a different code or a
// tampered exchange, and the handshake must abort before any file bytes
// move.
func (s *Session) Confirm(initiatorPub, responderPub []byte) []byte {
	transcript := append(append([]byte{}, initiatorPub...), responderPub...)
	return hmacSum(s.confirmKey[:], transcript)
}

// VerifyConfirm checks a peer-supplied confirmation MAC in constant time.
func (s *Session) VerifyConfirm(initiatorPub, responderPub, peerMAC []byte) error {
	want := s.Confirm(initiatorPub, responderPub)
	if !hmac.Equal(want, peerMAC) {
		return ErrHandshakeFailed
	}
	return nil
}

// hmacSum is HKDF-Extract: HMAC-Hash(salt, IKM).
func hmacSum(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// expand is a single-round HKDF-Expand, sufficient because every label we
// need is exactly one SHA-256 block (32 bytes): T(1) = HMAC(PRK, info || 0x01).
func expand(prk []byte, info string) [32]byte {
	mac := hmac.New(sha256.New, prk)
	mac.Write([]byte(info))
	mac.Write([]byte{0x01})
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}
