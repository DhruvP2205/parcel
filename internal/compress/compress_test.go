package compress

import (
	"bytes"
	"crypto/rand"
	"strings"
	"testing"
)

func TestCompressDecompressRoundTrip(t *testing.T) {
	original := strings.Repeat("the quick brown fox jumps over the lazy dog ", 500)

	var compressed bytes.Buffer
	if err := CompressTo(&compressed, strings.NewReader(original)); err != nil {
		t.Fatalf("compress: %v", err)
	}
	if compressed.Len() >= len(original) {
		t.Errorf("expected highly repetitive input to shrink: got %d bytes from %d", compressed.Len(), len(original))
	}

	var decompressed bytes.Buffer
	if err := DecompressTo(&decompressed, bytes.NewReader(compressed.Bytes())); err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if decompressed.String() != original {
		t.Error("round trip did not reproduce the original bytes")
	}
}

func TestCompressDecompressRoundTripRandomData(t *testing.T) {
	original := make([]byte, 64*1024)
	if _, err := rand.Read(original); err != nil {
		t.Fatalf("rand: %v", err)
	}

	var compressed bytes.Buffer
	if err := CompressTo(&compressed, bytes.NewReader(original)); err != nil {
		t.Fatalf("compress: %v", err)
	}

	var decompressed bytes.Buffer
	if err := DecompressTo(&decompressed, bytes.NewReader(compressed.Bytes())); err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(decompressed.Bytes(), original) {
		t.Error("round trip did not reproduce random original bytes")
	}
}

func TestCompressEmptyInput(t *testing.T) {
	var compressed bytes.Buffer
	if err := CompressTo(&compressed, bytes.NewReader(nil)); err != nil {
		t.Fatalf("compress: %v", err)
	}
	var decompressed bytes.Buffer
	if err := DecompressTo(&decompressed, bytes.NewReader(compressed.Bytes())); err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if decompressed.Len() != 0 {
		t.Errorf("expected empty round trip, got %d bytes", decompressed.Len())
	}
}

func TestDecompressRejectsGarbageInput(t *testing.T) {
	garbage := bytes.NewReader([]byte{0xFF, 0x00, 0xDE, 0xAD, 0xBE, 0xEF, 0x13, 0x37})
	var out bytes.Buffer
	if err := DecompressTo(&out, garbage); err == nil {
		t.Error("expected decompressing non-DEFLATE garbage to fail")
	}
}
