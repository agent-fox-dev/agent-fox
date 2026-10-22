package recovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrNotFound says the attempt has no such file in recovery/.
var ErrNotFound = errors.New("recovery: no such file")

// Store reads and writes the files of recovery/ in an attempt directory.
type Store struct{ attemptDir string }

// NewStore is the store of the attempt directory.
func NewStore(attemptDir string) *Store { return &Store{attemptDir: attemptDir} }

// Dir is recovery/, with its path in the attempt directory.
func (s *Store) Dir() string { return filepath.Join(s.attemptDir, DirName) }

// Load reads the journal. A journal that is not there is ErrNotFound, and one that does not parse or
// that has another version of the format is an error.
func (s *Store) Load() (Journal, error) {
	data, err := os.ReadFile(filepath.Join(s.Dir(), JournalFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Journal{}, ErrNotFound
	}
	if err != nil {
		return Journal{}, err
	}
	var j Journal
	if err := json.Unmarshal(data, &j); err != nil {
		return Journal{}, fmt.Errorf("recovery: %s does not parse: %w", JournalFile, err)
	}
	if j.Version != FormatVersion {
		return Journal{}, fmt.Errorf("recovery: %s is of version %d, and this worker reads version %d", JournalFile, j.Version, FormatVersion)
	}
	return j, nil
}

// Write replaces the journal: a reader finds the journal that was there or the one that is, and never
// part of one.
func (s *Store) Write(j Journal) error {
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return fmt.Errorf("recovery: the journal cannot be encoded: %w", err)
	}
	return s.writeFile(JournalFile, append(data, '\n'))
}

// Remove removes the files of recovery/ that a first delivery starts without, and leaves the directory.
func (s *Store) Remove() error {
	var errs []error
	for _, name := range []string{JournalFile, OutcomeFile} {
		if err := os.Remove(filepath.Join(s.Dir(), name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// writeFile replaces the file of recovery/ with the content, through a temporary file of the directory
// that is synced, renamed over the file and followed by a sync of the directory, so that the file is
// the old one or the new one after a crash too. recovery/ is made with mode 0700 and the file has 0600.
func (s *Store) writeFile(name string, data []byte) error {
	dir := s.Dir()
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("recovery: %s cannot be made: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("recovery: %s cannot be given its mode: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-"+name+"-")
	if err != nil {
		return fmt.Errorf("recovery: a temporary file cannot be made in %s: %w", dir, err)
	}
	done := false
	defer func() {
		if !done {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("recovery: %s cannot be given its mode: %w", name, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("recovery: %s cannot be written: %w", name, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("recovery: %s cannot be synced: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("recovery: %s cannot be closed: %w", name, err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("recovery: %s cannot be put in place: %w", name, err)
	}
	done = true
	if d, err := os.Open(dir); err == nil { // a directory that cannot be synced is no reason to say the file was not written
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}
