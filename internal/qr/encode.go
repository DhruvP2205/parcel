package qr

import "fmt"

// bitWriter accumulates bits MSB-first, matching how the QR spec numbers
// bits within its various fields.
type bitWriter struct {
	bits []bool
}

func (w *bitWriter) writeBits(value uint32, length int) {
	for i := length - 1; i >= 0; i-- {
		w.bits = append(w.bits, (value>>uint(i))&1 == 1)
	}
}

func (w *bitWriter) toBytes() []byte {
	out := make([]byte, (len(w.bits)+7)/8)
	for i, bit := range w.bits {
		if bit {
			out[i/8] |= 1 << uint(7-i%8)
		}
	}
	return out
}

// pickVersion returns the smallest supported version whose alphanumeric
// capacity fits data, preferring version 1 (a smaller, easier-to-scan
// code) whenever it's enough.
func pickVersion(data string) (versionSpec, error) {
	switch {
	case len(data) <= version1L.alphanumericCapacity:
		return version1L, nil
	case len(data) <= version2L.alphanumericCapacity:
		return version2L, nil
	default:
		return versionSpec{}, fmt.Errorf("qr: %q is too long to encode (%d chars, max %d)", data, len(data), version2L.alphanumericCapacity)
	}
}

// encodeAlphanumericData builds the data codewords for spec: mode
// indicator, character count, packed 11-bits-per-character-pair data,
// terminator, bit-padding to a byte boundary, then the standard
// alternating 0xEC/0x11 pad bytes up to the version's data codeword
// capacity.
func encodeAlphanumericData(data string, spec versionSpec) ([]byte, error) {
	var w bitWriter
	w.writeBits(0b0010, 4) // mode indicator: alphanumeric
	w.writeBits(uint32(len(data)), 9) // count indicator width for versions 1-9

	for i := 0; i+1 < len(data); i += 2 {
		v1, ok1 := alphanumericValue(data[i])
		v2, ok2 := alphanumericValue(data[i+1])
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("qr: character not representable in alphanumeric mode at position %d", i)
		}
		w.writeBits(uint32(v1*45+v2), 11)
	}
	if len(data)%2 == 1 {
		v, ok := alphanumericValue(data[len(data)-1])
		if !ok {
			return nil, fmt.Errorf("qr: character not representable in alphanumeric mode at position %d", len(data)-1)
		}
		w.writeBits(uint32(v), 6)
	}

	capacityBits := spec.dataCodewords * 8
	if len(w.bits) > capacityBits {
		return nil, fmt.Errorf("qr: encoded data (%d bits) exceeds version %d capacity (%d bits)", len(w.bits), spec.version, capacityBits)
	}
	remaining := capacityBits - len(w.bits)
	w.writeBits(0, min(4, remaining))

	for len(w.bits)%8 != 0 {
		w.bits = append(w.bits, false)
	}

	out := w.toBytes()
	padBytes := [2]byte{0xEC, 0x11}
	for i := 0; len(out) < spec.dataCodewords; i++ {
		out = append(out, padBytes[i%2])
	}
	return out, nil
}

// encodeFormatInfo turns 3 mask-pattern bits (ECC level L is fixed) into
// the 15-bit BCH-protected, XOR-masked value the spec places twice in
// every QR code.
func encodeFormatInfo(maskPattern int) uint16 {
	data := uint32(eccLevelBits<<3) | uint32(maskPattern)
	rem := data << 10
	for bit := 14; bit >= 10; bit-- {
		if rem&(1<<uint(bit)) != 0 {
			rem ^= formatGeneratorPoly << uint(bit-10)
		}
	}
	result := (data << 10) | rem
	result ^= formatMaskXOR
	return uint16(result)
}

// Encode builds a complete, masked QR matrix for data (an uppercase
// alphanumeric string — see alphanumericChars). It tries all 8 mask
// patterns and keeps the lowest-penalty one, exactly as the spec
// recommends.
func Encode(data string) (*Matrix, error) {
	spec, err := pickVersion(data)
	if err != nil {
		return nil, err
	}
	dataCodewords, err := encodeAlphanumericData(data, spec)
	if err != nil {
		return nil, err
	}
	ecCodewords := rsComputeECC(dataCodewords, spec.ecCodewords)
	allCodewords := append(append([]byte{}, dataCodewords...), ecCodewords...)

	var bw bitWriter
	for _, b := range allCodewords {
		bw.writeBits(uint32(b), 8)
	}
	bits := bw.bits

	var best *Matrix
	bestPenalty := -1
	for pattern := range 8 {
		m := newFunctionMatrix(spec)
		bitIdx := 0
		m.forEachDataModule(func(row, col int) {
			var v bool
			if bitIdx < len(bits) {
				v = bits[bitIdx]
			}
			bitIdx++
			m.set(row, col, v)
			m.reserved[m.idx(row, col)] = false // data modules stay maskable/re-readable, unlike function patterns
		})
		m.applyMask(pattern)
		m.setFormatInfo(encodeFormatInfo(pattern))

		p := maskPenalty(m)
		if best == nil || p < bestPenalty {
			best, bestPenalty = m, p
		}
	}
	return best, nil
}
