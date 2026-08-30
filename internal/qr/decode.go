package qr

import (
	"errors"
	"fmt"
)

// ErrCorrupt covers any structural problem found while decoding: bad
// format info, a Reed-Solomon syndrome that doesn't check out, or data
// that doesn't parse as a valid alphanumeric segment. Decode exists
// purely so this package can verify its own encoder end-to-end (see
// encode_test.go) — it is not a general-purpose QR reader (no image
// processing, no perspective correction, no version detection from
// scratch: the caller must know the version it encoded at).
var ErrCorrupt = errors.New("qr: corrupt or unrecognized code")

// Decode reads back the alphanumeric string encoded into m, which must
// have been produced by Encode for the given version. It re-derives the
// mask from the format info actually stored in the matrix (not assumed),
// unmasks, verifies the Reed-Solomon syndrome, and parses the data
// segment — the same steps and shared position/traversal logic Encode
// used, run in reverse.
func Decode(m *Matrix, version int) (string, error) {
	spec, err := specForVersion(version)
	if err != nil {
		return "", err
	}
	if m.size != spec.size {
		return "", fmt.Errorf("%w: matrix size %d does not match version %d", ErrCorrupt, m.size, version)
	}

	formatBits := m.readFormatInfo()
	eccLevel, mask, err := decodeFormatInfo(formatBits)
	if err != nil {
		return "", err
	}
	if eccLevel != eccLevelBits {
		return "", fmt.Errorf("%w: unexpected ECC level %d", ErrCorrupt, eccLevel)
	}

	// Read raw (still-masked) bits via a fresh function-pattern reference
	// so we know exactly which cells are data, independent of whatever
	// the caller's matrix did with its own reserved bookkeeping.
	ref := newFunctionMatrix(spec)
	totalBits := (spec.dataCodewords + spec.ecCodewords) * 8
	rawBits := make([]bool, 0, totalBits)
	ref.forEachDataModule(func(row, col int) {
		if len(rawBits) >= totalBits {
			return
		}
		rawBits = append(rawBits, m.get(row, col))
	})
	if len(rawBits) < totalBits {
		return "", fmt.Errorf("%w: not enough data modules for version %d", ErrCorrupt, version)
	}

	maskFn := maskFunc(mask)
	unmasked := make([]bool, len(rawBits))
	i := 0
	ref.forEachDataModule(func(row, col int) {
		if i >= totalBits {
			return
		}
		v := rawBits[i]
		if maskFn(row, col) {
			v = !v
		}
		unmasked[i] = v
		i++
	})

	codewords := bitsToBytes(unmasked)
	dataCodewords := codewords[:spec.dataCodewords]
	ecCodewords := codewords[spec.dataCodewords:]

	if err := verifyRSSyndrome(dataCodewords, ecCodewords); err != nil {
		return "", err
	}

	return parseAlphanumericData(dataCodewords)
}

func specForVersion(version int) (versionSpec, error) {
	switch version {
	case 1:
		return version1L, nil
	case 2:
		return version2L, nil
	default:
		return versionSpec{}, fmt.Errorf("qr: unsupported version %d", version)
	}
}

// decodeFormatInfo re-derives the 5 data bits from a possibly-corrupted
// 15-bit format field. This package only needs the zero-error case (its
// own freshly-encoded matrices), so unlike a real QR reader it doesn't
// attempt BCH error correction — a mismatch is reported as corrupt rather
// than silently repaired.
func decodeFormatInfo(bits uint16) (eccLevel, mask int, err error) {
	unmasked := bits ^ formatMaskXOR
	data := int(unmasked >> 10)
	if encodeFormatInfo(data&0b111) != bits || (data>>3) != eccLevelBits {
		return 0, 0, fmt.Errorf("%w: format info does not round-trip (got %015b)", ErrCorrupt, bits)
	}
	return data >> 3, data & 0b111, nil
}

func bitsToBytes(bits []bool) []byte {
	out := make([]byte, len(bits)/8)
	for i, bit := range bits {
		if bit {
			out[i/8] |= 1 << uint(7-i%8)
		}
	}
	return out
}

func verifyRSSyndrome(data, ecc []byte) error {
	recomputed := rsComputeECC(data, len(ecc))
	for i := range ecc {
		if recomputed[i] != ecc[i] {
			return fmt.Errorf("%w: Reed-Solomon syndrome mismatch", ErrCorrupt)
		}
	}
	return nil
}

// parseAlphanumericData is encodeAlphanumericData run backwards: mode
// indicator, count, packed character pairs, trailing odd character.
func parseAlphanumericData(data []byte) (string, error) {
	var br bitReader
	br.bits = bytesToBits(data)

	mode, err := br.read(4)
	if err != nil {
		return "", err
	}
	if mode != 0b0010 {
		return "", fmt.Errorf("%w: unsupported mode indicator %04b", ErrCorrupt, mode)
	}
	count, err := br.read(9)
	if err != nil {
		return "", err
	}

	out := make([]byte, 0, count)
	remaining := int(count)
	for remaining >= 2 {
		v, err := br.read(11)
		if err != nil {
			return "", err
		}
		out = append(out, alphanumericChars[v/45], alphanumericChars[v%45])
		remaining -= 2
	}
	if remaining == 1 {
		v, err := br.read(6)
		if err != nil {
			return "", err
		}
		out = append(out, alphanumericChars[v])
	}
	return string(out), nil
}

type bitReader struct {
	bits []bool
	pos  int
}

func (r *bitReader) read(n int) (uint32, error) {
	if r.pos+n > len(r.bits) {
		return 0, fmt.Errorf("%w: ran out of data bits", ErrCorrupt)
	}
	var v uint32
	for range n {
		v <<= 1
		if r.bits[r.pos] {
			v |= 1
		}
		r.pos++
	}
	return v, nil
}

func bytesToBits(data []byte) []bool {
	bits := make([]bool, 0, len(data)*8)
	for _, b := range data {
		for i := 7; i >= 0; i-- {
			bits = append(bits, (b>>uint(i))&1 == 1)
		}
	}
	return bits
}
