//go:build windows

package ansi

import (
	"os"
	"syscall"
	"unsafe"
)

// enableVirtualTerminalProcessing tells the classic conhost.exe console host
// to interpret ANSI/VT escape sequences instead of printing them literally.
// Modern terminal emulators (Windows Terminal) already do this, but a plain
// cmd.exe/PowerShell console window needs it turned on explicitly per
// handle, once, on Windows 10+.
const enableVirtualTerminalProcessing = 0x0004

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
)

// isTerminal reports whether f is attached to a real Windows console (as
// opposed to redirected to a file or pipe) and, if so, opportunistically
// enables VT processing on it so ANSI codes render as colors rather than
// escape-code noise.
func isTerminal(f *os.File) bool {
	var mode uint32
	h := syscall.Handle(f.Fd())
	r, _, _ := procGetConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(&mode)))
	if r == 0 {
		return false
	}
	mode |= enableVirtualTerminalProcessing
	procSetConsoleMode.Call(uintptr(h), uintptr(mode))
	return true
}
