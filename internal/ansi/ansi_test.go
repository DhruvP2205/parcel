package ansi

import "testing"

func TestOutWrapsWhenEnabled(t *testing.T) {
	orig := StdoutEnabled
	StdoutEnabled = true
	defer func() { StdoutEnabled = orig }()

	got := Out(BoldGreen, "ok")
	want := "\x1b[1m\x1b[32mok\x1b[0m"
	if got != want {
		t.Fatalf("Out() = %q, want %q", got, want)
	}
}

func TestOutPlainWhenDisabled(t *testing.T) {
	orig := StdoutEnabled
	StdoutEnabled = false
	defer func() { StdoutEnabled = orig }()

	if got := Out(BoldGreen, "ok"); got != "ok" {
		t.Fatalf("Out() = %q, want plain %q", got, "ok")
	}
}

func TestErrRespectsNoColorEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if !noColorRequested() {
		t.Fatal("noColorRequested() = false with NO_COLOR set")
	}
}
