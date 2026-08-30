package qr

import (
	"strings"
	"testing"
)

// roundTrip is this package's primary correctness check: since there's no
// way to physically scan a QR code with a phone camera in this sandboxed
// environment, every test instead encodes, then decodes with the
// from-scratch decoder in decode.go, and confirms the original string
// comes back. This proves the whole pipeline (bit packing, Reed-Solomon,
// module placement, masking, format info) is internally consistent — it
// does not by itself prove a real scanner would read it the same way; see
// the qr package doc comment and STDLIB.md for that caveat.
func roundTrip(t *testing.T, data string) {
	t.Helper()
	m, err := Encode(data)
	if err != nil {
		t.Fatalf("Encode(%q): %v", data, err)
	}
	spec, err := pickVersion(data)
	if err != nil {
		t.Fatalf("pickVersion(%q): %v", data, err)
	}
	got, err := Decode(m, spec.version)
	if err != nil {
		t.Fatalf("Decode after Encode(%q): %v", data, err)
	}
	if got != data {
		t.Errorf("round trip mismatch: got %q, want %q", got, data)
	}
}

func TestRoundTripShortCode(t *testing.T) {
	roundTrip(t, "CRIMSON-OTTER-LAGOON")
}

func TestRoundTripSingleCharacter(t *testing.T) {
	roundTrip(t, "A")
}

func TestRoundTripEmptyString(t *testing.T) {
	roundTrip(t, "")
}

func TestRoundTripEveryAlphanumericCharacter(t *testing.T) {
	roundTrip(t, alphanumericChars)
}

func TestRoundTripVersion1Boundary(t *testing.T) {
	// Exactly at version 1-L's alphanumeric capacity: must still fit in
	// version 1, not spill into version 2.
	data := strings.Repeat("A", version1L.alphanumericCapacity)
	m, err := Encode(data)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if m.size != version1L.size {
		t.Errorf("expected version 1 (size %d) at the boundary, got size %d", version1L.size, m.size)
	}
	roundTrip(t, data)
}

func TestRoundTripSpillsIntoVersion2(t *testing.T) {
	data := strings.Repeat("A", version1L.alphanumericCapacity+1)
	m, err := Encode(data)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if m.size != version2L.size {
		t.Errorf("expected version 2 (size %d) just past the version-1 boundary, got size %d", version2L.size, m.size)
	}
	roundTrip(t, data)
}

func TestRoundTripVersion2Boundary(t *testing.T) {
	data := strings.Repeat("A", version2L.alphanumericCapacity)
	roundTrip(t, data)
}

func TestEncodeRejectsTooLong(t *testing.T) {
	data := strings.Repeat("A", version2L.alphanumericCapacity+1)
	if _, err := Encode(data); err == nil {
		t.Error("expected an error for data exceeding version 2 capacity")
	}
}

func TestEncodeRejectsLowercase(t *testing.T) {
	if _, err := Encode("crimson-otter-lagoon"); err == nil {
		t.Error("expected an error for lowercase input (not in the alphanumeric charset)")
	}
}

// Every pairing code parcel can actually generate must round-trip —
// exercises realistic 4-word, hyphen-joined, uppercased codes across a
// spread of lengths, including the worst case (the four longest words in
// the list, 36 chars, still well under version 2-L's capacity).
func TestRoundTripRealisticPairingCodes(t *testing.T) {
	codes := []string{
		"ANT-FIG-SKY-OWL",
		"CRIMSON-OTTER-LAGOON-BASIL",
		"TANGERINE-OBSIDIAN-OLEANDER-WISTERIA", // the four longest real words in the list
		"SYCAMORE-PHEASANT-BRAMBLE-HICKORY",
	}
	for _, c := range codes {
		roundTrip(t, c)
	}
}

func TestFormatInfoRoundTrips(t *testing.T) {
	for pattern := range 8 {
		bits := encodeFormatInfo(pattern)
		ecc, mask, err := decodeFormatInfo(bits)
		if err != nil {
			t.Fatalf("pattern %d: decodeFormatInfo: %v", pattern, err)
		}
		if ecc != eccLevelBits {
			t.Errorf("pattern %d: got ECC bits %d, want %d", pattern, ecc, eccLevelBits)
		}
		if mask != pattern {
			t.Errorf("pattern %d: got mask %d back", pattern, mask)
		}
	}
}

func TestFormatInfoDetectsCorruption(t *testing.T) {
	bits := encodeFormatInfo(3)
	corrupted := bits ^ 0b100000000000000 // flip the top bit
	if _, _, err := decodeFormatInfo(corrupted); err == nil {
		t.Error("expected corrupted format info to be rejected")
	}
}

func TestRenderProducesQuietZoneBorder(t *testing.T) {
	m, err := Encode("PARCEL")
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out := Render(m)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("Render produced no output")
	}
	// The first rendered row is entirely quiet zone (2 quiet-zone module
	// rows compressed into 1 half-block row) — must be all spaces.
	if strings.Trim(lines[0], " ") != "" {
		t.Errorf("expected an all-light first row (quiet zone), got %q", lines[0])
	}
}
