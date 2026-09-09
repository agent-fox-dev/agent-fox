package toolio

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Progress writes human-readable progress to stderr.
//
// It is on stderr and never stdout, without an option to change that: stdout
// carries exactly one JSON object, and a tool that interleaves progress into
// it is a tool whose output cannot be parsed. A caller that wants silence
// redirects stderr; a caller that wants detail passes --verbose.
type Progress struct {
	mu      sync.Mutex
	w       io.Writer
	tool    string
	verbose bool
	quiet   bool
	spin    *spinner
}

// NewProgress returns a Progress writing to w, prefixing each line with the
// tool's name. A nil w means os.Stderr.
func NewProgress(w io.Writer, tool string, verbose, quiet bool) *Progress {
	if w == nil {
		w = os.Stderr
	}
	return &Progress{w: w, tool: tool, verbose: verbose, quiet: quiet}
}

// Verbose reports whether detailed tracing was asked for.
func (p *Progress) Verbose() bool { return p != nil && p.verbose }

// Step prints one line of progress.
func (p *Progress) Step(format string, args ...any) {
	if p == nil || p.quiet {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopSpinnerLocked()
	fmt.Fprintf(p.w, "[%s] %s\n", p.tool, strings.TrimSpace(fmt.Sprintf(format, args...)))
}

// Detail prints one line only under --verbose. It is indented, because a
// detail line belongs to the step above it.
func (p *Progress) Detail(format string, args ...any) {
	if p == nil || !p.verbose || p.quiet {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopSpinnerLocked()
	fmt.Fprintf(p.w, "  %s\n", strings.TrimSpace(fmt.Sprintf(format, args...)))
}

// Raw writes text with no prefix and no trailing newline. It is how a
// model's own prose is streamed under --show-text.
func (p *Progress) Raw(s string) {
	if p == nil || p.quiet || s == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopSpinnerLocked()
	fmt.Fprint(p.w, s)
}

// Begin starts a step that will take a while, returning the function that
// ends it. Under --verbose the step is a plain line and the detail lines
// beneath it are the progress; otherwise a spinner runs so an operator can
// tell a ten-minute model call from a hang.
func (p *Progress) Begin(format string, args ...any) func(summary string) {
	if p == nil || p.quiet {
		return func(string) {}
	}
	label := strings.TrimSpace(fmt.Sprintf(format, args...))
	start := time.Now()

	p.mu.Lock()
	p.stopSpinnerLocked()
	fmt.Fprintf(p.w, "[%s] %s ", p.tool, label)
	if !p.verbose && isTerminal(p.w) {
		p.spin = startSpinner(p.w, &p.mu)
	} else {
		fmt.Fprintln(p.w)
	}
	p.mu.Unlock()

	return func(summary string) {
		p.mu.Lock()
		defer p.mu.Unlock()
		hadSpinner := p.spin != nil
		p.stopSpinnerLocked()
		line := fmt.Sprintf("(%s)", FormatDuration(time.Since(start)))
		if summary != "" {
			line += " " + summary
		}
		if hadSpinner {
			fmt.Fprintf(p.w, "%s\n", line)
			return
		}
		if p.verbose {
			fmt.Fprintf(p.w, "[%s] %s done %s\n", p.tool, label, line)
		} else {
			fmt.Fprintf(p.w, "%s\n", line)
		}
	}
}

func (p *Progress) stopSpinnerLocked() {
	if p.spin != nil {
		p.spin.stopLocked()
		p.spin = nil
	}
}

// FormatDuration renders a duration the way a run summary should read: sub-second
// precision below a second, whole units above it.
func FormatDuration(d time.Duration) string {
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	var parts []string
	if h > 0 {
		parts = append(parts, fmt.Sprintf("%dh", h))
	}
	if m > 0 {
		parts = append(parts, fmt.Sprintf("%dm", m))
	}
	if s > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", s))
	}
	return strings.Join(parts, " ")
}

// FormatTokens abbreviates a token count for a one-line summary.
func FormatTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// ------------------------------------------------------------- spinner --

var spinnerFrames = []byte{'|', '/', '-', '\\'}

// spinner animates one character in place. It shares the Progress mutex so a
// line printed from another goroutine cannot land in the middle of a frame.
type spinner struct {
	w       io.Writer
	mu      *sync.Mutex
	ticker  *time.Ticker
	done    chan struct{}
	stopped chan struct{}
}

func startSpinner(w io.Writer, mu *sync.Mutex) *spinner {
	s := &spinner{
		w:       w,
		mu:      mu,
		ticker:  time.NewTicker(100 * time.Millisecond),
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	_, _ = s.w.Write([]byte{spinnerFrames[0]}) // caller holds mu
	go s.run()
	return s
}

func (s *spinner) run() {
	defer close(s.stopped)
	frame := 1
	for {
		select {
		case <-s.done:
			return
		case <-s.ticker.C:
			s.mu.Lock()
			_, _ = s.w.Write([]byte{'\b', spinnerFrames[frame]})
			s.mu.Unlock()
			frame = (frame + 1) % len(spinnerFrames)
		}
	}
}

// stopLocked stops the animation and erases the frame. The caller holds the
// mutex, so the goroutine's own Lock is what makes this wait for it.
func (s *spinner) stopLocked() {
	s.ticker.Stop()
	close(s.done)
	s.mu.Unlock()
	<-s.stopped
	s.mu.Lock()
	_, _ = s.w.Write([]byte{'\b', ' ', '\b'})
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	stat, err := f.Stat()
	if err != nil {
		return false
	}
	return stat.Mode()&os.ModeCharDevice != 0
}
