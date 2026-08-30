package qr

// maskPenalty scores a fully-placed matrix using the QR spec's four
// penalty rules, lower is better. This only decides which of the 8 valid
// masks to prefer for scan reliability — every mask produces a fully
// correct, decodable code, so an imprecision here would at worst pick a
// slightly less optimal (but still entirely valid) mask, never break
// correctness.
func maskPenalty(m *Matrix) int {
	return runPenalty(m) + blockPenalty(m) + patternPenalty(m) + darkRatioPenalty(m)
}

// runPenalty (rule 1): for every row and column, 3 points for each run of
// 5+ same-color modules, plus 1 for each module beyond the 5th.
func runPenalty(m *Matrix) int {
	total := 0
	for row := 0; row < m.size; row++ {
		total += lineRunPenalty(func(i int) bool { return m.get(row, i) }, m.size)
	}
	for col := 0; col < m.size; col++ {
		total += lineRunPenalty(func(i int) bool { return m.get(i, col) }, m.size)
	}
	return total
}

func lineRunPenalty(at func(i int) bool, n int) int {
	total := 0
	runLen := 1
	for i := 1; i < n; i++ {
		if at(i) == at(i-1) {
			runLen++
			continue
		}
		if runLen >= 5 {
			total += 3 + (runLen - 5)
		}
		runLen = 1
	}
	if runLen >= 5 {
		total += 3 + (runLen - 5)
	}
	return total
}

// blockPenalty (rule 2): 3 points for every 2x2 block of same-color
// modules (overlapping blocks each count separately).
func blockPenalty(m *Matrix) int {
	total := 0
	for row := 0; row < m.size-1; row++ {
		for col := 0; col < m.size-1; col++ {
			v := m.get(row, col)
			if m.get(row, col+1) == v && m.get(row+1, col) == v && m.get(row+1, col+1) == v {
				total += 3
			}
		}
	}
	return total
}

// patternPenalty (rule 3): 40 points for every occurrence, in a row or
// column, of the finder-like 1:1:3:1:1 dark/light ratio with 4 light
// modules padding one side (dark-light-dark-dark-dark-light-dark, i.e.
// what a real finder pattern's center strip looks like) — this pattern
// can fool a scanner into misidentifying a false finder if left in place.
func patternPenalty(m *Matrix) int {
	darkLight := [11]bool{true, false, true, true, true, false, true, false, false, false, false}
	lightDark := [11]bool{false, false, false, false, true, false, true, true, true, false, true}

	total := 0
	for row := 0; row < m.size; row++ {
		total += lineMatchCount(func(i int) bool { return m.get(row, i) }, m.size, darkLight)
		total += lineMatchCount(func(i int) bool { return m.get(row, i) }, m.size, lightDark)
	}
	for col := 0; col < m.size; col++ {
		total += lineMatchCount(func(i int) bool { return m.get(i, col) }, m.size, darkLight)
		total += lineMatchCount(func(i int) bool { return m.get(i, col) }, m.size, lightDark)
	}
	return total * 40
}

func lineMatchCount(at func(i int) bool, n int, pattern [11]bool) int {
	if n < len(pattern) {
		return 0
	}
	count := 0
	for start := 0; start+len(pattern) <= n; start++ {
		match := true
		for i, want := range pattern {
			if at(start+i) != want {
				match = false
				break
			}
		}
		if match {
			count++
		}
	}
	return count
}

// darkRatioPenalty (rule 4): 10 points for every 5% the proportion of
// dark modules deviates from 50%.
func darkRatioPenalty(m *Matrix) int {
	dark := 0
	for _, v := range m.dark {
		if v {
			dark++
		}
	}
	total := len(m.dark)
	percent := dark * 100 / total
	prev := (percent / 5) * 5
	next := prev + 5
	absDiff := func(a, b int) int {
		if a < b {
			return b - a
		}
		return a - b
	}
	a := absDiff(prev, 50) / 5
	b := absDiff(next, 50) / 5
	if a < b {
		return a * 10
	}
	return b * 10
}
