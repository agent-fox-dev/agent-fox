package toolio

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// eventLine is one line of an event stream.
type eventLine struct {
	Seq   int     `json:"seq"`
	TS    string  `json:"ts"`
	RunID *string `json:"run_id"`
	Type  string  `json:"type"`
	Data  any     `json:"data"`
}

// eventSink is one file the events go to.
type eventSink struct {
	path string
	f    *os.File
	dead bool
}

// eventStream writes the events of a run, as JSON lines, to the run
// directory's events.jsonl and to the file named by --emit-events. The shell
// emits run_start, a warning event for each warning and run_end; a command
// emits its own through Run.Emit. Each line is written as it is emitted, and
// a sink that cannot be written to is given up with one warning, the
// events_unwritable of the envelope: the run goes on.
type eventStream struct {
	now  func() time.Time
	warn func(code, message string)

	mu      sync.Mutex
	runID   *string
	seq     int
	sinks   []*eventSink
	started bool
	closed  bool
	warned  bool
}

func newEventStream(now func() time.Time, warn func(code, message string)) *eventStream {
	return &eventStream{now: now, warn: warn}
}

// addFile appends the events to the file at path, creating it if needed.
func (s *eventStream) addFile(path string) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	s.mu.Lock()
	s.sinks = append(s.sinks, &eventSink{path: path, f: f, dead: err != nil})
	s.mu.Unlock()
	if err != nil {
		s.unwritable(path, err)
	}
}

// setRunID names the run in the lines that follow.
func (s *eventStream) setRunID(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runID = &id
}

// active reports whether there is a file the events can still go to.
func (s *eventStream) active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sk := range s.sinks {
		if !sk.dead {
			return true
		}
	}
	return false
}

// unwritable raises events_unwritable, the first time only.
func (s *eventStream) unwritable(path string, err error) {
	s.mu.Lock()
	first := !s.warned
	s.warned = true
	s.mu.Unlock()
	if first {
		s.warn(WarnEventsUnwritable, fmt.Sprintf("cannot write events to %s: %v", path, err))
	}
}

// emit writes one event to every sink. Before the stream has started, and
// after it was closed, it writes nothing.
func (s *eventStream) emit(typ string, data any) {
	s.mu.Lock()
	if !s.started || s.closed {
		s.mu.Unlock()
		return
	}
	s.seq++
	line := eventLine{Seq: s.seq, TS: stamp(s.now()), RunID: s.runID, Type: typ, Data: data}
	encoded, err := json.Marshal(line)
	if err != nil {
		line.Data = map[string]string{"error": fmt.Sprintf("the data cannot be encoded as JSON: %v", err)}
		encoded, _ = json.Marshal(line)
	}
	encoded = append(encoded, '\n')

	type failure struct {
		path string
		err  error
	}
	var failed []failure
	for _, sk := range s.sinks {
		if sk.dead {
			continue
		}
		if _, err := sk.f.Write(encoded); err != nil {
			sk.dead = true
			_ = sk.f.Close()
			failed = append(failed, failure{sk.path, err})
		}
	}
	s.mu.Unlock()

	// The warning is raised outside the lock: it is an event itself.
	for _, f := range failed {
		s.unwritable(f.path, f.err)
	}
}

// begin starts the stream with run_start, then the events of the warnings
// raised so far.
func (s *eventStream) begin(tool string, argv []string, input InputKind, warnings []Warning) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()

	s.emit("run_start", struct {
		Tool      string    `json:"tool"`
		Argv      []string  `json:"argv"`
		InputKind InputKind `json:"input_kind"`
	}{tool, argv, input})
	for _, w := range warnings {
		s.emit("warning", w)
	}
}

// end finishes the stream with run_end and closes its files.
func (s *eventStream) end(env *Envelope) {
	s.emit("run_end", struct {
		Status     Status `json:"status"`
		ExitCode   int    `json:"exit_code"`
		DurationMS int64  `json:"duration_ms"`
	}{env.Status, env.ExitCode, env.DurationMS})

	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, sk := range s.sinks {
		if sk.f != nil && !sk.dead {
			_ = sk.f.Close()
		}
	}
}
