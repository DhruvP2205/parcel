package qr

// Matrix is a QR code's module grid. reserved marks every cell that's part
// of a function pattern (finder, separator, timing, alignment, dark
// module, format info) or has otherwise already been placed — those cells
// are skipped by data placement and excluded from mask-penalty scoring,
// exactly as the spec requires.
type Matrix struct {
	size     int
	dark     []bool
	reserved []bool
}

func newMatrix(size int) *Matrix {
	return &Matrix{size: size, dark: make([]bool, size*size), reserved: make([]bool, size*size)}
}

func (m *Matrix) idx(row, col int) int { return row*m.size + col }

func (m *Matrix) get(row, col int) bool { return m.dark[m.idx(row, col)] }

func (m *Matrix) set(row, col int, dark bool) {
	i := m.idx(row, col)
	m.dark[i] = dark
	m.reserved[i] = true
}

func (m *Matrix) isReserved(row, col int) bool { return m.reserved[m.idx(row, col)] }

// newFunctionMatrix builds a matrix with every function pattern for spec
// already drawn (and reserved), ready for data placement. Used by both
// encoding (place data, then mask) and decoding (identify what's data vs
// function pattern before reading it back) — sharing this construction is
// what guarantees the two stay consistent with each other.
func newFunctionMatrix(spec versionSpec) *Matrix {
	m := newMatrix(spec.size)
	m.drawFinderPattern(0, 0)
	m.drawFinderPattern(0, spec.size-7)
	m.drawFinderPattern(spec.size-7, 0)
	m.drawTimingPatterns()
	if spec.alignmentCenter != 0 {
		m.drawAlignmentPattern(spec.alignmentCenter)
	}
	m.drawDarkModule(spec.version)
	m.setFormatInfo(0) // placeholder value; reserves the cells now, real bits patched in after masking
	return m
}

// drawFinderPattern places one 7x7 finder pattern (nested squares) with
// its required 1-module light separator ring, top-left corner at
// (topRow, leftCol).
func (m *Matrix) drawFinderPattern(topRow, leftCol int) {
	for r := -1; r <= 7; r++ {
		for c := -1; c <= 7; c++ {
			row, col := topRow+r, leftCol+c
			if row < 0 || row >= m.size || col < 0 || col >= m.size {
				continue
			}
			dark := false
			if r >= 0 && r <= 6 && c >= 0 && c <= 6 {
				dark = r == 0 || r == 6 || c == 0 || c == 6 || (r >= 2 && r <= 4 && c >= 2 && c <= 4)
			}
			m.set(row, col, dark)
		}
	}
}

// drawTimingPatterns fills the alternating dark/light strip between the
// finder patterns on row 6 and column 6, restricted to the span that
// doesn't already overlap a finder pattern's separator.
func (m *Matrix) drawTimingPatterns() {
	for i := 8; i < m.size-8; i++ {
		dark := i%2 == 0
		m.set(6, i, dark)
		m.set(i, 6, dark)
	}
}

// drawAlignmentPattern places the one 5x5 alignment pattern version 2
// uses, centered at (center, center).
func (m *Matrix) drawAlignmentPattern(center int) {
	for dr := -2; dr <= 2; dr++ {
		for dc := -2; dc <= 2; dc++ {
			dark := dr == -2 || dr == 2 || dc == -2 || dc == 2 || (dr == 0 && dc == 0)
			m.set(center+dr, center+dc, dark)
		}
	}
}

// drawDarkModule places the single always-dark module every QR version
// has at (4*version+9, 8).
func (m *Matrix) drawDarkModule(version int) {
	m.set(4*version+9, 8, true)
}

// formatInfoPositions returns the two 15-position placement lists for
// format info, index 0 = bit 14 (MSB) through index 14 = bit 0 (LSB).
// Both copies encode the same 15 bits; the QR spec duplicates them around
// different finder patterns so the code survives partial damage.
func (m *Matrix) formatInfoPositions() (copy1, copy2 [15][2]int) {
	copy1 = [15][2]int{
		{8, 0}, {8, 1}, {8, 2}, {8, 3}, {8, 4}, {8, 5}, {8, 7}, {8, 8},
		{7, 8}, {5, 8}, {4, 8}, {3, 8}, {2, 8}, {1, 8}, {0, 8},
	}
	size := m.size
	copy2 = [15][2]int{
		{size - 1, 8}, {size - 2, 8}, {size - 3, 8}, {size - 4, 8}, {size - 5, 8}, {size - 6, 8}, {size - 7, 8},
		{8, size - 8}, {8, size - 7}, {8, size - 6}, {8, size - 5}, {8, size - 4}, {8, size - 3}, {8, size - 2}, {8, size - 1},
	}
	return copy1, copy2
}

// setFormatInfo writes the 15-bit format value (see encodeFormatInfo)
// into both placement copies.
func (m *Matrix) setFormatInfo(bits uint16) {
	copy1, copy2 := m.formatInfoPositions()
	for i := range 15 {
		bit := (bits>>uint(14-i))&1 == 1
		m.set(copy1[i][0], copy1[i][1], bit)
		m.set(copy2[i][0], copy2[i][1], bit)
	}
}

// readFormatInfo reads the 15-bit format value back from copy 1 (the
// top-left copy — always present and never overlaps data, regardless of
// version).
func (m *Matrix) readFormatInfo() uint16 {
	copy1, _ := m.formatInfoPositions()
	var bits uint16
	for i := range 15 {
		if m.get(copy1[i][0], copy1[i][1]) {
			bits |= 1 << uint(14-i)
		}
	}
	return bits
}

// forEachDataModule visits every non-reserved module in the standard QR
// zigzag order: two columns at a time from the right edge, skipping the
// column-6 timing strip, alternating vertical direction each pair, and
// within a row visiting the right column of the pair before the left.
// This exact order is what both data placement (encode) and data
// extraction (decode) drive off of, so they can't disagree with each
// other about where a given bit lives.
func (m *Matrix) forEachDataModule(visit func(row, col int)) {
	upward := true
	for col := m.size - 1; col > 0; col -= 2 {
		if col == 6 {
			col--
		}
		for i := 0; i < m.size; i++ {
			row := i
			if upward {
				row = m.size - 1 - i
			}
			for _, c := range [2]int{col, col - 1} {
				if m.isReserved(row, c) {
					continue
				}
				visit(row, c)
			}
		}
		upward = !upward
	}
}

// maskFunc returns the boolean function for one of the 8 standard QR mask
// patterns, applied to (row, col) to decide whether that data module gets
// flipped.
func maskFunc(pattern int) func(row, col int) bool {
	switch pattern {
	case 0:
		return func(r, c int) bool { return (r+c)%2 == 0 }
	case 1:
		return func(r, c int) bool { return r%2 == 0 }
	case 2:
		return func(r, c int) bool { return c%3 == 0 }
	case 3:
		return func(r, c int) bool { return (r+c)%3 == 0 }
	case 4:
		return func(r, c int) bool { return (r/2+c/3)%2 == 0 }
	case 5:
		return func(r, c int) bool { return (r*c)%2+(r*c)%3 == 0 }
	case 6:
		return func(r, c int) bool { return ((r*c)%2+(r*c)%3)%2 == 0 }
	case 7:
		return func(r, c int) bool { return ((r+c)%2+(r*c)%3)%2 == 0 }
	default:
		panic("qr: invalid mask pattern")
	}
}

// applyMask XORs the given mask pattern's formula into every non-reserved
// (i.e. data) module. Calling it twice with the same pattern is its own
// inverse, which decode.go relies on to unmask before reading data back.
func (m *Matrix) applyMask(pattern int) {
	fn := maskFunc(pattern)
	for row := range m.size {
		for col := range m.size {
			if m.isReserved(row, col) {
				continue
			}
			if fn(row, col) {
				i := m.idx(row, col)
				m.dark[i] = !m.dark[i]
			}
		}
	}
}
