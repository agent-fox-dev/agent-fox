package triage

import (
	"encoding/json"
	"errors"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/submission"
)

// BriefTools are the tools of the hands that a triage brief allows, by name, before
// any box is open to say which of them it offers: the read-only ones. None of them
// runs a command or writes a file, so a brain on the host that has no more needs no
// network.
func BriefTools() []string { return slices.Clone(readOnlyTools) }

// RejectionsPath is where the refused calls of a run are noted in a tree, beside
// its submission.
func RejectionsPath(runID string) string {
	return path.Join(path.Dir(submission.SubmissionPath(runID)), RejectionsFile)
}

// ReadBackRejections reads the refused calls of the run from the tree a brain
// worked in. A tree that has none gives no bytes and no error.
func ReadBackRejections(tree fs.FS, runID string) ([]byte, error) {
	data, err := fs.ReadFile(tree, RejectionsPath(runID))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// StaleReason says why a submission that was accepted in a box no longer holds
// against the tree of the host, which may have changed while the brain ran; it is
// empty when it holds.
func StaleReason(payload json.RawMessage, host Tree) string {
	_, problems := Validate(payload, host)
	lines := make([]string, len(problems))
	for i, p := range problems {
		lines[i] = p.String()
	}
	return strings.Join(lines, "; ")
}
