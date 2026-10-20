package checks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
)

// labelsFile holds the labels of the gate results, in the gate directory.
const labelsFile = "labels.json"

var (
	// labelPattern is what a label looks like.
	labelPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	// idPattern is what a gate id looks like.
	idPattern = regexp.MustCompile(`^[0-9a-f]{12}$`)
)

// UsageError is a mistake in what the caller asked for, as against a failure
// to do it: a label that is not allowed, an argument that names no result.
type UsageError struct {
	Message string
}

func (e *UsageError) Error() string { return e.Message }

// ValidateLabel reports whether label can name a result: a lower-case letter
// and up to 31 lower-case letters, digits, '_' or '-', that is not spelled
// like a gate id, so that an argument is never both a label and an id. The
// empty label is no label.
func ValidateLabel(label string) error {
	switch {
	case label == "":
		return nil
	case !labelPattern.MatchString(label):
		return &UsageError{fmt.Sprintf("--label %q is not a label: use a lower-case letter and then up to 31 lower-case letters, digits, '_' or '-'", label)}
	case idPattern.MatchString(label):
		return &UsageError{fmt.Sprintf("--label %q is spelled like a gate id: use another label", label)}
	}
	return nil
}

// labelsPath is where dir keeps its labels.
func labelsPath(dir string) string {
	return filepath.Join(dir, agentdir.Name, gateDirName, labelsFile)
}

// readLabels reads the labels of dir: label to result id. A file that is not
// there, or is not a set of labels, holds none.
func readLabels(dir string) map[string]string {
	var labels map[string]string
	data, err := os.ReadFile(labelsPath(dir))
	if err != nil || json.Unmarshal(data, &labels) != nil || labels == nil {
		return map[string]string{}
	}
	return labels
}

// recordLabel maps label to the result id in labels.json, replacing the id it
// had, and writes the file atomically.
func recordLabel(dir, label, id string) error {
	labels := readLabels(dir)
	labels[label] = id
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(labels); err != nil {
		return err
	}
	path := labelsPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return agentdir.WriteFileAtomic(path, buf.Bytes())
}
