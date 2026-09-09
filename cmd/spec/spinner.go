package main

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// spinnerFrames are the braille spinner animation characters.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// StatusSpinner shows an animated spinner with a message on stderr
// during long-running operations. When quiet mode is active or
// AF_AGENT=1 is set, all methods are no-ops.
type StatusSpinner struct {
	message string
	writer  io.Writer
	quiet   bool
	isTTY   bool

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	done    chan struct{}
}

// NewStatusSpinner creates a new StatusSpinner. If quiet is true,
// all methods are no-ops. The writer parameter specifies where
// spinner output goes (typically os.Stderr).
func NewStatusSpinner(message string, writer io.Writer, quiet bool, isTTY bool) *StatusSpinner {
	return &StatusSpinner{
		message: message,
		writer:  writer,
		quiet:   quiet,
		isTTY:   isTTY,
	}
}

// Start begins the spinner animation.
func (s *StatusSpinner) Start() {
	if s.quiet {
		return
	}

	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.done = make(chan struct{})
	s.mu.Unlock()

	if !s.isTTY {
		// Non-TTY: write a plain text line and return.
		fmt.Fprintf(s.writer, "%s\n", s.message)
		return
	}

	// TTY: start animated spinner in a goroutine.
	go func() {
		defer close(s.done)
		tick := time.NewTicker(80 * time.Millisecond)
		defer tick.Stop()
		frame := 0
		for {
			select {
			case <-s.stopCh:
				// Clear the spinner line.
				s.mu.Lock()
				fmt.Fprintf(s.writer, "\r\x1b[K")
				s.mu.Unlock()
				return
			case <-tick.C:
				s.mu.Lock()
				msg := s.message
				s.mu.Unlock()
				fmt.Fprintf(s.writer, "\r\x1b[K%s %s", spinnerFrames[frame%len(spinnerFrames)], msg)
				frame++
			}
		}
	}()
}

// Stop halts the spinner animation and cleans up.
func (s *StatusSpinner) Stop() {
	if s.quiet {
		return
	}

	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.mu.Unlock()

	if s.isTTY && s.stopCh != nil {
		close(s.stopCh)
		<-s.done
	}
}

// Update changes the spinner message text.
func (s *StatusSpinner) Update(message string) {
	if s.quiet {
		return
	}

	s.mu.Lock()
	s.message = message
	s.mu.Unlock()

	if !s.isTTY {
		// Non-TTY: write the update as a plain text line.
		fmt.Fprintf(s.writer, "%s\n", message)
	}
}

// Log prints a permanent line above the spinner.
func (s *StatusSpinner) Log(message string) {
	if s.quiet {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isTTY {
		// Clear current spinner line, print log message, spinner
		// will redraw on next tick.
		fmt.Fprintf(s.writer, "\r\x1b[K%s\n", message)
	} else {
		fmt.Fprintf(s.writer, "%s\n", message)
	}
}
