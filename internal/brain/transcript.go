package brain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"unicode/utf8"
)

// maxTranscriptField is the most a transcript line keeps of the arguments or the
// result of a call.
const maxTranscriptField = 4096

// Transcript is the file an in-process brain keeps of its tool calls: one JSON
// line for each, {seq, tool, args, decision, reason, ok, result}, with seq
// strictly increasing from 1. It is a local file that af never reads; it holds
// what events may not, the arguments and results, each cut to 4096 bytes.
type Transcript struct {
	mu  sync.Mutex
	f   *os.File
	seq int
	err error
}

// NewTranscript makes the transcript at path, which is empty.
func NewTranscript(path string) (*Transcript, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	return &Transcript{f: f}, nil
}

// openTranscript makes the transcript of a brief, or none when the brief has no
// path for one.
func openTranscript(b Brief) (*Transcript, error) {
	if b.TranscriptPath == "" {
		return nil, nil
	}
	tr, err := NewTranscript(b.TranscriptPath)
	if err != nil {
		return nil, fmt.Errorf("the transcript cannot be written: %w", err)
	}
	return tr, nil
}

// closeTranscript closes the transcript of a brief, if there is one. A
// transcript that could not be written is a warning: the run goes on, and the
// file is partial.
func closeTranscript(tr *Transcript, b Brief) {
	if tr == nil {
		return
	}
	if err := tr.Close(); err != nil && b.Warn != nil {
		b.Warn("transcript_unwritable", fmt.Sprintf("the transcript %s could not be written whole: %v", b.TranscriptPath, err))
	}
}

// Add appends the line of a call, and returns its seq.
func (t *Transcript) Add(tool, args, decision, reason string, ok bool, result string) int {
	return t.write(func(seq int) any {
		return struct {
			Seq      int    `json:"seq"`
			Tool     string `json:"tool"`
			Args     string `json:"args"`
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
			OK       bool   `json:"ok"`
			Result   string `json:"result"`
		}{seq, tool, truncate(args, maxTranscriptField), decision, reason, ok, truncate(result, maxTranscriptField)}
	})
}

// Skipped appends the line of a step that was not run.
func (t *Transcript) Skipped(step int, what string) int {
	return t.write(func(seq int) any {
		return struct {
			Seq     int    `json:"seq"`
			Skipped bool   `json:"skipped"`
			Step    int    `json:"step"`
			What    string `json:"what"`
		}{seq, true, step, truncate(what, maxTranscriptField)}
	})
}

// write appends one line, with the next seq.
func (t *Transcript) write(line func(seq int) any) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq++
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(line(t.seq)); err == nil {
		_, err = t.f.Write(buf.Bytes())
		t.noteError(err)
	} else {
		t.noteError(err)
	}
	return t.seq
}

func (t *Transcript) noteError(err error) {
	if err != nil && t.err == nil {
		t.err = err
	}
}

// Close closes the transcript, and returns the first error that writing it had.
func (t *Transcript) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	err := t.f.Close()
	if t.err != nil {
		return t.err
	}
	return err
}

// truncate cuts s to at most n bytes, at the end of a character, and marks the
// cut. What is not valid UTF-8 is replaced first, so that the line is text.
func truncate(s string, n int) string {
	const mark = "…"
	s = strings.ToValidUTF8(s, "�")
	if len(s) <= n {
		return s
	}
	cut := n - len(mark)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + mark
}
