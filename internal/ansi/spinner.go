package ansi

import (
	"fmt"
	"time"
)

var spinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner is a minimal hand-rolled terminal progress indicator — no
// third-party spinner/progress-bar package. On a real terminal it animates
// in place via carriage returns; otherwise it degrades to printing label
// once as a plain line, so redirected output/logs never fill up with
// carriage-return noise.
type Spinner struct {
	stop chan struct{}
	done chan struct{}
}

// StartSpinner begins animating label on stdout and returns a handle to
// stop it once the operation it describes finishes.
func StartSpinner(label string) *Spinner {
	s := &Spinner{stop: make(chan struct{}), done: make(chan struct{})}
	if !IsStdoutTerminal {
		fmt.Println(label)
		close(s.done)
		return s
	}
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		for i := 0; ; i++ {
			select {
			case <-s.stop:
				fmt.Print("\r\x1b[K")
				return
			case <-ticker.C:
				fmt.Printf("\r%s %s", Out(Cyan, spinnerFrames[i%len(spinnerFrames)]), label)
			}
		}
	}()
	return s
}

// Stop halts the animation, clears its line, and optionally prints final in
// its place.
func (s *Spinner) Stop(final string) {
	close(s.stop)
	<-s.done
	if final != "" {
		fmt.Println(final)
	}
}
