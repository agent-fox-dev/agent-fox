package checks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"

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
	unlock := lockLabels(dir)
	defer unlock()
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

// lockLabels takes the lock that keeps two runs from reading labels.json at
// the same time and writing it one after the other, which would lose the
// label of the first. It is a file made exclusively, held for as long as a
// read and a write take; one that is older than ten seconds was left by a
// process that was killed and is taken over, and when it cannot be taken in
// five seconds the caller goes on without it: a label is worth a short wait and
// never a failed run.
func lockLabels(dir string) (unlock func()) {
	path := labelsPath(dir) + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return func() {}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(path) }
		}
		if !errors.Is(err, fs.ErrExist) || time.Now().After(deadline) {
			return func() {}
		}
		if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) > 10*time.Second {
			_ = os.Remove(path)
			continue
		}
		time.Sleep(5 * time.Millisecond)
	}
}
