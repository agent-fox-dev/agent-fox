package triage

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"time"
)

// RejectionsFile is the file of refused submit calls, beside submission.json.
// The brain can write it as well as af can, so nothing may depend on it: the
// counts are reported and never decide a landing.
const RejectionsFile = "rejections.jsonl"

// WarnRejectionsUnreadable is the warning of a rejections file that cannot be
// counted.
const WarnRejectionsUnreadable = "rejections_unreadable"

// rejection is one line of the file: a refused submit call.
type rejection struct {
	TS              string            `json:"ts"`
	Problems        []rejectedProblem `json:"problems"`
	UnresolvedPaths []string          `json:"unresolved_paths"`
}

type rejectedProblem struct {
	Pointer string `json:"pointer"`
	Message string `json:"message"`
}

// FormatRejection is the line of a refused submit call: {ts, problems,
// unresolved_paths}, ending in a newline.
func FormatRejection(now time.Time, problems []Problem) []byte {
	r := rejection{TS: now.UTC().Format(time.RFC3339), Problems: []rejectedProblem{}, UnresolvedPaths: []string{}}
	for _, p := range problems {
		r.Problems = append(r.Problems, rejectedProblem{Pointer: p.Pointer, Message: p.Message})
		if p.Unresolved {
			r.UnresolvedPaths = append(r.UnresolvedPaths, p.Path)
		}
	}
	line, _ := json.Marshal(r) // a struct of strings always marshals
	return append(line, '\n')
}

// CountRejections counts the refused calls in the file at path: those with at
// least one unresolved path, and all of them. A file that is not there counts 0
// and 0; one that cannot be read, or holds a line that is no refusal, counts 0
// and 0 with the warning rejections_unreadable.
func CountRejections(path string) (pathCalls, total int, warning string) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, 0, ""
	}
	if err != nil {
		return 0, 0, WarnRejectionsUnreadable
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var r rejection
		if err := json.Unmarshal(line, &r); err != nil {
			return 0, 0, WarnRejectionsUnreadable
		}
		total++
		if len(r.UnresolvedPaths) > 0 {
			pathCalls++
		}
	}
	return pathCalls, total, ""
}
