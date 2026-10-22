package recovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Outcome is the outcome of an attempt as the worker stores it, as soon as it is known and before anything
// is written to the forge or to hub for it. Envelope is the envelope the attempt ends with, Report the text
// of its report, and MaxAttempts and the labels what the jobs layer is given beside it. Mirrored says the
// mirror to the issue is done, and the envelope is the one the mirror amended. It holds no PAT, no token of a
// lease, no Authorization value and no URL with user information. The envelope is kept as the text it is, so
// that it comes back byte for byte.
type Outcome struct {
	Version       int      `json:"version"`
	Job           string   `json:"job"`
	Attempt       int      `json:"attempt"`
	Envelope      string   `json:"envelope"`
	Report        string   `json:"report"`
	MaxAttempts   int      `json:"max_attempts"`
	LabelsSet     []string `json:"labels_set"`
	LabelsRemoved []string `json:"labels_removed"`
	Mirrored      bool     `json:"mirrored"`
}

// LoadOutcome reads the stored outcome. An outcome that is not there is ErrNotFound, and one that does not
// parse or that has another version of the format is an error.
func (s *Store) LoadOutcome() (Outcome, error) {
	data, err := os.ReadFile(filepath.Join(s.Dir(), OutcomeFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Outcome{}, ErrNotFound
	}
	if err != nil {
		return Outcome{}, err
	}
	var o Outcome
	if err := json.Unmarshal(data, &o); err != nil {
		return Outcome{}, fmt.Errorf("recovery: %s does not parse: %w", OutcomeFile, err)
	}
	if o.Version != FormatVersion {
		return Outcome{}, fmt.Errorf("recovery: %s is of version %d, and this worker reads version %d", OutcomeFile, o.Version, FormatVersion)
	}
	return o, nil
}

// WriteOutcome replaces the stored outcome, as Write replaces the journal.
func (s *Store) WriteOutcome(o Outcome) error {
	data, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return fmt.Errorf("recovery: the outcome cannot be encoded: %w", err)
	}
	return s.writeFile(OutcomeFile, append(data, '\n'))
}

// RemoveOutcome removes the stored outcome, which a delivery that cannot carry on the attempt does not leave
// for the next one to find.
func (s *Store) RemoveOutcome() error {
	if err := os.Remove(filepath.Join(s.Dir(), OutcomeFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
