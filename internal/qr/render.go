package qr

import "strings"

// quietZone is the spec-required light border around a QR code — without
// it, many scanners can't reliably distinguish the code from surrounding
// content.
const quietZone = 4

// Render draws m as a compact terminal string using Unicode half-block
// characters: each printed character represents a 1x2 stack of modules
// (top module as the foreground half, bottom module as the background
// half), halving the number of terminal rows a full-block rendering would
// need. Uses only ' ', '▀', '▄', '█' — no ANSI color codes needed since
// dark/light is conveyed by which half-block glyph is chosen.
func Render(m *Matrix) string {
	get := func(row, col int) bool {
		r := row - quietZone
		c := col - quietZone
		if r < 0 || c < 0 || r >= m.size || c >= m.size {
			return false // quiet zone is always light
		}
		return m.get(r, c)
	}

	totalSize := m.size + 2*quietZone
	var b strings.Builder
	for row := 0; row < totalSize; row += 2 {
		for col := range totalSize {
			top := get(row, col)
			bottom := false
			if row+1 < totalSize {
				bottom = get(row+1, col)
			}
			switch {
			case top && bottom:
				b.WriteRune('█')
			case top && !bottom:
				b.WriteRune('▀')
			case !top && bottom:
				b.WriteRune('▄')
			default:
				b.WriteRune(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}
