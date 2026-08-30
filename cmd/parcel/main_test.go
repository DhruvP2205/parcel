package main

import (
	"strings"
	"testing"
)

func TestColorUsageDisabledMatchesPlain(t *testing.T) {
	if got := colorUsage(false); got != usage {
		t.Fatal("colorUsage(false) must equal the plain usage text byte-for-byte")
	}
}

func TestColorUsageEnabledHighlightsWithoutLosingText(t *testing.T) {
	got := colorUsage(true)
	if got == usage {
		t.Fatal("colorUsage(true) should differ from plain usage")
	}
	for _, sub := range []string{"Usage:", "send", "receive", "relay", "-lan-only", "-relay-only"} {
		if !strings.Contains(got, sub) {
			t.Fatalf("colorUsage(true) lost expected text %q:\n%s", sub, got)
		}
	}
}
