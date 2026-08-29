package crypto

import (
	"bytes"
	"errors"
	"testing"
)

func testKey(b byte) [32]byte {
	var k [32]byte
	for i := range k {
		k[i] = b
	}
	return k
}

func TestStreamRoundTrip(t *testing.T) {
	key := testKey(0x42)
	enc, err := NewStream(key)
	if err != nil {
		t.Fatalf("new encrypt stream: %v", err)
	}
	dec, err := NewStream(key)
	if err != nil {
		t.Fatalf("new decrypt stream: %v", err)
	}

	plaintext := []byte("this chunk of the file is definitely secret")
	aad := []byte("chunk-meta")

	ciphertext, err := enc.Encrypt(0, plaintext, aad)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	got, err := dec.Decrypt(0, ciphertext, aad)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("round trip mismatch: got %q want %q", got, plaintext)
	}
}

func TestStreamWrongKeyFails(t *testing.T) {
	enc, _ := NewStream(testKey(0x01))
	dec, _ := NewStream(testKey(0x02))

	ciphertext, err := enc.Encrypt(0, []byte("secret"), nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := dec.Decrypt(0, ciphertext, nil); err == nil {
		t.Error("expected decrypt with wrong key to fail")
	}
}

func TestStreamTamperedCiphertextFails(t *testing.T) {
	key := testKey(0x99)
	enc, _ := NewStream(key)
	dec, _ := NewStream(key)

	ciphertext, err := enc.Encrypt(0, []byte("do not modify me"), nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ciphertext[len(ciphertext)-1] ^= 0xFF

	if _, err := dec.Decrypt(0, ciphertext, nil); err == nil {
		t.Error("expected decrypt of tampered ciphertext to fail")
	}
}

func TestStreamRejectsSequenceReplay(t *testing.T) {
	key := testKey(0x07)
	enc, _ := NewStream(key)
	dec, _ := NewStream(key)

	c0, _ := enc.Encrypt(0, []byte("chunk zero"), nil)
	c1, _ := enc.Encrypt(1, []byte("chunk one"), nil)

	if _, err := dec.Decrypt(0, c0, nil); err != nil {
		t.Fatalf("decrypt chunk 0: %v", err)
	}
	if _, err := dec.Decrypt(1, c1, nil); err != nil {
		t.Fatalf("decrypt chunk 1: %v", err)
	}
	// Replaying chunk 0 (or any seq <= last accepted) must be rejected.
	if _, err := dec.Decrypt(0, c0, nil); !errors.Is(err, ErrSequenceReuse) {
		t.Errorf("expected ErrSequenceReuse on replay, got %v", err)
	}
}

func TestStreamRejectsEncryptSequenceRegression(t *testing.T) {
	enc, _ := NewStream(testKey(0x11))
	if _, err := enc.Encrypt(5, []byte("hello"), nil); err != nil {
		t.Fatalf("encrypt seq 5: %v", err)
	}
	if _, err := enc.Encrypt(5, []byte("again"), nil); !errors.Is(err, ErrSequenceReuse) {
		t.Errorf("expected ErrSequenceReuse reusing seq 5, got %v", err)
	}
	if _, err := enc.Encrypt(3, []byte("backwards"), nil); !errors.Is(err, ErrSequenceReuse) {
		t.Errorf("expected ErrSequenceReuse going backwards to seq 3, got %v", err)
	}
}

func TestStreamResumeStartsAtArbitrarySequence(t *testing.T) {
	// A resumed transfer starts a fresh Stream (fresh handshake, fresh key)
	// but continues the chunk index where it left off — nonce uniqueness
	// only requires strictly increasing seq within this Stream, not that
	// it starts at zero.
	key := testKey(0x55)
	enc, _ := NewStream(key)
	dec, _ := NewStream(key)

	ciphertext, err := enc.Encrypt(1000, []byte("resumed chunk"), nil)
	if err != nil {
		t.Fatalf("encrypt at resumed seq: %v", err)
	}
	got, err := dec.Decrypt(1000, ciphertext, nil)
	if err != nil {
		t.Fatalf("decrypt at resumed seq: %v", err)
	}
	if string(got) != "resumed chunk" {
		t.Errorf("got %q", got)
	}
}

func TestStreamWrongAADFails(t *testing.T) {
	key := testKey(0x33)
	enc, _ := NewStream(key)
	dec, _ := NewStream(key)

	ciphertext, err := enc.Encrypt(0, []byte("payload"), []byte("real-aad"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := dec.Decrypt(0, ciphertext, []byte("wrong-aad")); err == nil {
		t.Error("expected decrypt with mismatched AAD to fail")
	}
}
