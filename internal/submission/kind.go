// Package submission is how a brain hands back its result: exactly one JSON
// object, validated before it exists. A brain is given no way to write the
// submission itself and af never parses it from the brain's output; the submit
// core is the path to a submission file, and a refused submission leaves
// nothing. A brain with hands of its own could write the file all the same, so
// what a host keeps is what it has checked again (Check) and written itself
// (Marshal).
package submission

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
)

// Problem is one thing wrong with a submission: where, and what.
type Problem struct {
	// Path says where in the payload (or the tree) the problem is; empty is
	// the payload as a whole.
	Path    string
	Message string
}

// String is the problem as a line of text.
func (p Problem) String() string {
	if p.Path == "" {
		return p.Message
	}
	return p.Path + ": " + p.Message
}

// Kind is what a brain submits and how it is checked.
type Kind struct {
	// Name is the kind's name, which run.json and the submission carry.
	Name string
	// ToolName and Description make the terminating tool of a brain that has
	// one, and Schema is the JSON Schema of its input.
	ToolName, Description string
	Schema                json.RawMessage
	// Validate checks a payload against the tree it describes (a path it cites
	// must be there) and returns the payload normalised, or every problem it
	// found. A nil Validate accepts the payload as it is.
	Validate func(ctx context.Context, tree fs.FS, payload json.RawMessage) (json.RawMessage, []Problem)
}

// Refused is the error of a submission that was not accepted: nothing was
// written, and an earlier submission is as it was.
type Refused struct {
	// Problems are all of them, sorted by Path and then by Message.
	Problems []Problem
}

// Error names every problem.
func (e *Refused) Error() string {
	lines := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		lines[i] = p.String()
	}
	if len(lines) == 1 {
		return "the submission was refused: " + lines[0]
	}
	return fmt.Sprintf("the submission was refused, %d problems: %s", len(lines), strings.Join(lines, "; "))
}

// Receipt says what was written.
type Receipt struct {
	// Path is the submission file, relative to the tree.
	Path string
	// SHA256 is the hex SHA-256 of the compacted payload, which the file
	// carries as payload_sha256.
	SHA256 string
}
