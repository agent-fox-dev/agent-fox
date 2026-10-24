package triage

import (
	"encoding/json"
	"slices"
	"strings"
)

// BriefTools are the tools of the hands that a triage brief allows, by name, before
// any box is open to say which of them it offers: the read-only ones. None of them
// runs a command or writes a file, so a brain on the host that has no more needs no
// network.
func BriefTools() []string { return slices.Clone(readOnlyTools) }

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
