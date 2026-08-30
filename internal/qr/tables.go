// Package qr implements just enough of the QR Code (ISO/IEC 18004)
// standard from scratch — no image/qrcode-style library, no dependency of
// any kind — to encode a short alphanumeric string (parcel's pairing
// code) and render it as a scannable code in a terminal.
//
// Scope is deliberately narrow: only versions 1 and 2, only error
// correction level L, only Alphanumeric encoding mode. That covers every
// pairing code parcel can generate (worst case: three 9-letter words
// joined by hyphens, uppercased, is 29 characters — fits version 2-L's
// 47-character alphanumeric capacity) while avoiding the parts of the
// full spec version 1/2 don't need: version-information blocks (only
// required from version 7 up) and multi-block Reed-Solomon interleaving
// (versions 1-2 at level L each use a single block). QR pairing is an
// optional convenience layered on top of the spoken/typed code, which
// remains the primary, fully-verified pairing path — see STDLIB.md for
// how this was verified (a matching from-scratch decoder round-trips
// every encode in this package's tests) and what wasn't (a real phone
// camera scan, which this sandboxed dev environment can't perform).
package qr

// alphanumericChars is the fixed 45-character set QR's Alphanumeric mode
// can represent; index in this string is each character's encoded value.
const alphanumericChars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ $%*+-./:"

// alphanumericValue returns c's value in the QR alphanumeric charset.
func alphanumericValue(c byte) (int, bool) {
	idx := indexByte(alphanumericChars, c)
	if idx < 0 {
		return 0, false
	}
	return idx, true
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// versionSpec describes everything about a supported (version, ECC level)
// pair needed to encode into it. Both entries use a single Reed-Solomon
// block, so no interleaving is needed.
type versionSpec struct {
	version               int
	size                  int // modules per side
	dataCodewords         int
	ecCodewords           int
	alignmentCenter       int // 0 means "no alignment pattern" (version 1)
	alphanumericCapacity  int
}

var (
	version1L = versionSpec{version: 1, size: 21, dataCodewords: 19, ecCodewords: 7, alignmentCenter: 0, alphanumericCapacity: 25}
	version2L = versionSpec{version: 2, size: 25, dataCodewords: 34, ecCodewords: 10, alignmentCenter: 18, alphanumericCapacity: 47}
)

// eccLevelBits is the QR format-info encoding for error correction level
// L. The four levels' bit patterns are fixed by the spec and don't follow
// numeric order (L=01, M=00, Q=11, H=10); only L is used here.
const eccLevelBits = 0b01

// formatGeneratorPoly and formatMaskXOR are the two fixed constants that
// turn 5 bits of format info (ECC level + mask pattern) into the 15-bit
// BCH-protected value placed twice in every QR code, regardless of
// version. See encodeFormatInfo.
const (
	formatGeneratorPoly = 0b10100110111
	formatMaskXOR       = 0b101010000010010
)
