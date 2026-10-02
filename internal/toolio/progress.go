package toolio

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/checks"
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
	// events is the JSONL sink. It is active when --emit-events is set
	// (and later, when the events file is always written).
	events *eventsSink
	// showText mirrors --show-text: Raw writes (or emits a text event) only
	// when it is set.
	showText bool
	// phase is the model phase now running, named on a text event.
	phase string
	// eventsOnStderr is set under --emit-events: the JSONL stream is what
	// stderr carries, so no human line is written beside it (a caller that
	// parses stderr line by line must never meet prose).
	eventsOnStderr bool
}

// NewProgress returns a Progress writing to w, prefixing each line with the
// tool's name. A nil w means os.Stderr.
func NewProgress(w io.Writer, tool string, verbose, quiet bool) *Progress {
	if w == nil {
		w = os.Stderr
	}
	return &Progress{w: w, tool: tool, verbose: verbose, quiet: quiet}
}

// SetEvents attaches the JSONL sink Progress's methods emit through.
func (p *Progress) SetEvents(s *eventsSink) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.events = s
	p.mu.Unlock()
}

// SetEmitEvents records whether --emit-events was given. When true, stderr
// carries the JSONL stream and no human line is written beside it.
func (p *Progress) SetEmitEvents(on bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.eventsOnStderr = on
	p.mu.Unlock()
}

// humanSuppressed reports whether stderr is given over to the JSONL stream.
func (p *Progress) humanSuppressed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.eventsOnStderr
}

// SetShowText records whether --show-text was given. Raw does nothing
// without it, under every --events value.
func (p *Progress) SetShowText(on bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.showText = on
	p.mu.Unlock()
}

// sink returns the attached sink when it is active, nil otherwise.
func (p *Progress) sink() *eventsSink {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	s := p.events
	p.mu.Unlock()
	if s.Active() {
		return s
	}
	return nil
}

// Verbose reports whether detailed tracing was asked for.
func (p *Progress) Verbose() bool { return p != nil && p.verbose }

// Step prints one line of progress. stage names the pipeline step in the
// vocabulary Result.Stage and error.Stage use; it is never shown on the
// human line, only carried on the step event under an active JSONL sink.
func (p *Progress) Step(stage, format string, args ...any) {
	if p == nil {
		return
	}
	msg := strings.TrimSpace(fmt.Sprintf(format, args...))
	if s := p.sink(); s != nil {
		s.Emit(newStepEvent(stage, msg))
	}
	if p.quiet || p.humanSuppressed() {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopSpinnerLocked()
	fmt.Fprintf(p.w, "[%s] %s\n", p.tool, msg)
}

// Detail prints one line only under --verbose. It is indented, because a
// detail line belongs to the step above it.
func (p *Progress) Detail(format string, args ...any) {
	if p == nil || !p.verbose || p.quiet || p.humanSuppressed() {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopSpinnerLocked()
	fmt.Fprintf(p.w, "  %s\n", strings.TrimSpace(fmt.Sprintf(format, args...)))
}

// Raw writes text with no prefix and no trailing newline. It is how a
// model's own prose is streamed under --show-text; without --show-text it
// does nothing. It never emits an event — text events are emitted by the
// Runner's per-turn accumulation (task 5). It writes prose to stderr only
// when --show-text is set and neither --emit-events nor --quiet is.
func (p *Progress) Raw(s string) {
	if p == nil || s == "" {
		return
	}
	p.mu.Lock()
	show := p.showText
	emit := p.eventsOnStderr
	q := p.quiet
	p.mu.Unlock()
	if !show || emit || q {
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
// tell a ten-minute model call from a hang. Under --emit-events or --quiet
// it prints nothing and returns a no-op: the spans it wraps already have
// phase_start/phase_end or check events.
func (p *Progress) Begin(format string, args ...any) func(summary string) {
	if p == nil || p.quiet || p.humanSuppressed() {
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

// Check reports a finished verification command as a check event. It does
// nothing without an active JSONL sink.
func (p *Progress) Check(res checks.Result) {
	if s := p.sink(); s != nil {
		s.Emit(newCheckEvent(res.Command, res.OK, res.ExitCode, res.DurationMS))
	}
}

// PhaseStart records the phase now running and emits phase_start.
func (p *Progress) PhaseStart(phase, task string, maxTurns int, budgetUSD float64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.phase = phase
	p.mu.Unlock()
	if s := p.sink(); s != nil {
		s.Emit(newPhaseStartEvent(phase, task, maxTurns, budgetUSD))
	}
}

// PhaseEnd emits phase_end and forgets the current phase.
func (p *Progress) PhaseEnd(phase, stopReason string, turns int, costUSD float64, durationMS int64) {
	if p == nil {
		return
	}
	if s := p.sink(); s != nil {
		s.Emit(newPhaseEndEvent(phase, stopReason, turns, costUSD, durationMS))
	}
	p.mu.Lock()
	p.phase = ""
	p.mu.Unlock()
}

// Turn emits a turn event after a model turn.
func (p *Progress) Turn(phase string, turn int, costUSD float64, inputTokens, outputTokens int64) {
	if s := p.sink(); s != nil {
		s.Emit(newTurnEvent(phase, turn, costUSD, inputTokens, outputTokens))
	}
}

// Text emits a text event carrying the accumulated prose of one model turn.
// It is called by the Runner immediately before the turn event with the same
// turn number.
func (p *Progress) Text(phase string, turn int, text string) {
	if s := p.sink(); s != nil {
		s.Emit(newTextEvent(phase, turn, text))
	}
}

// ToolCall emits a tool_call event for every model tool call.
func (p *Progress) ToolCall(info agentrun.ToolCallInfo) {
	if s := p.sink(); s != nil {
		s.Emit(newToolCallEvent(info.Phase, info.Name, info.Blocked,
			info.Arguments, info.OK, info.ExitCode, info.Error))
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
