// Package ansi hand-rolls terminal color codes for parcel's CLI output — no
// github.com/fatih/color, no github.com/mattn/go-colorable/go-isatty. Color
// is auto-disabled when NO_COLOR is set (https://no-color.org) or when a
// stream isn't a real terminal (piped to a file, redirected, etc.), so
// scripted/logged output stays plain text.
package ansi

import "os"

// SGR (Select Graphic Rendition) codes. Combined constants (BoldRed, etc.)
// chain two escapes back-to-back — a single trailing Reset clears both.
const (
	Reset     = "\x1b[0m"
	Bold      = "\x1b[1m"
	Dim       = "\x1b[2m"
	Underline = "\x1b[4m"

	Red     = "\x1b[31m"
	Green   = "\x1b[32m"
	Yellow  = "\x1b[33m"
	Blue    = "\x1b[34m"
	Magenta = "\x1b[35m"
	Cyan    = "\x1b[36m"

	BoldRed     = Bold + Red
	BoldGreen   = Bold + Green
	BoldYellow  = Bold + Yellow
	BoldMagenta = Bold + Magenta
	BoldCyan    = Bold + Cyan
	HeaderStyle = Bold + Cyan
)

// StdoutEnabled and StderrEnabled are decided once at startup: is this
// stream a real console (not redirected), and has the user not opted out
// via NO_COLOR. isTerminal is implemented per-OS (term_windows.go /
// term_other.go) since the stdlib has no portable isatty.
var (
	StdoutEnabled = isTerminal(os.Stdout) && !noColorRequested()
	StderrEnabled = isTerminal(os.Stderr) && !noColorRequested()
)

func noColorRequested() bool {
	return os.Getenv("NO_COLOR") != ""
}

// Out wraps s in code for stdout, or returns s unchanged if stdout coloring
// is disabled.
func Out(code, s string) string {
	if !StdoutEnabled {
		return s
	}
	return code + s + Reset
}

// Err wraps s in code for stderr, or returns s unchanged if stderr coloring
// is disabled.
func Err(code, s string) string {
	if !StderrEnabled {
		return s
	}
	return code + s + Reset
}
