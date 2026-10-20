package triage

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
)

// LedgerQuestion is the kind of the ledger record of a question.
const LedgerQuestion = "triage.question"

// runIDFormat is what the name of a run directory looks like.
var runIDFormat = regexp.MustCompile(`^[0-9]{8}_[0-9]{6}_[0-9a-f]{6}$`)

// FindPreviousQuestion finds the question of the newest earlier triage run under
// dir/.agent-fox/run that has the fingerprint and whose ledger has a
// triage.question record, or nil. A bare answer such as "B" means nothing to a
// brain without the question it answers, and the shell keeps no state between
// two invocations, so the run directories are where the question is. The run
// exclude is the one being made, and is skipped.
func FindPreviousQuestion(dir, fingerprint, exclude string) *Question {
	runs := filepath.Join(dir, ".agent-fox", "run")
	entries, err := os.ReadDir(runs)
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && runIDFormat.MatchString(e.Name()) && e.Name() != exclude {
			ids = append(ids, e.Name())
		}
	}
	slices.Sort(ids)
	slices.Reverse(ids)
	for _, id := range ids {
		run := filepath.Join(runs, id)
		var facts Facts
		data, err := os.ReadFile(filepath.Join(run, "triage.json"))
		if err != nil || json.Unmarshal(data, &facts) != nil || facts.Input.Fingerprint != fingerprint {
			continue
		}
		if q := lastQuestion(filepath.Join(run, "ledger.jsonl")); q != nil {
			return q
		}
	}
	return nil
}

// lastQuestion is the question of the last triage.question record of a ledger.
func lastQuestion(ledger string) *Question {
	data, err := os.ReadFile(ledger)
	if err != nil {
		return nil
	}
	var found *Question
	for _, line := range bytes.Split(data, []byte("\n")) {
		var entry struct {
			Kind string   `json:"kind"`
			Data Question `json:"data"`
		}
		if len(bytes.TrimSpace(line)) == 0 || json.Unmarshal(line, &entry) != nil || entry.Kind != LedgerQuestion || entry.Data.Text == "" {
			continue
		}
		q := entry.Data
		found = &q
	}
	return found
}
