//go:build !windows

package ansi

import "os"

// isTerminal uses the classic "is this fd a character device" heuristic —
// true for a real tty, false for a regular file or pipe — without needing
// an ioctl/termios syscall wrapper.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
