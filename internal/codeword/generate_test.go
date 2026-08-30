package codeword

import (
	"strings"
	"testing"
)

func TestGenerateProducesDistinctKnownWords(t *testing.T) {
	for i := 0; i < 200; i++ {
		code, err := Generate()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		parts := strings.Split(code, "-")
		if len(parts) != NumWords {
			t.Fatalf("expected %d parts, got %d in %q", NumWords, len(parts), code)
		}
		seen := map[string]bool{}
		for _, p := range parts {
			if seen[p] {
				t.Fatalf("code %q repeats word %q", code, p)
			}
			seen[p] = true
		}
		if err := Validate(code); err != nil {
			t.Errorf("Validate rejected a freshly generated code %q: %v", code, err)
		}
	}
}

func TestGenerateProducesVariedCodes(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		code, err := Generate()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		seen[code] = true
	}
	if len(seen) < 45 {
		t.Errorf("expected mostly-unique codes across 50 draws, got only %d distinct", len(seen))
	}
}

func TestValidateRejectsMalformedCodes(t *testing.T) {
	cases := []string{
		"",
		"onlyoneword",
		"two-words",
		"crimson-otter-lagoon",      // one short of NumWords
		"crimson-otter-basil-notarealword",
		"crimson-otter-lagoon-basil-extra", // one over NumWords
	}
	for _, c := range cases {
		if err := Validate(c); err == nil {
			t.Errorf("Validate(%q) = nil, want error", c)
		}
	}
}
