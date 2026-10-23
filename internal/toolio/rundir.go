package toolio

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
)

const (
	// agentDir is where af keeps its local state under a checkout.
	agentDir = agentdir.Name
	// runsDir holds the run directories, inside agentDir.
	runsDir = "run"
	// runIDAttempts is how many ids are drawn before a collision is a failure.
	runIDAttempts = 5
)

// runIDFormat is what a run id looks like: UTC time and 3 random bytes.
var runIDFormat = regexp.MustCompile(`^[0-9]{8}_[0-9]{6}_[0-9a-f]{6}$`)

// RunDir is the local state of one invocation: <dir>/.agent-fox/run/<id>/.
// It holds the envelope, the events and the ledger, and a command may add a
// report. Git ignores it through <dir>/.agent-fox/.gitignore, which the
// repository's own .gitignore knows nothing about.
type RunDir struct {
	// ID is the directory's name, YYYYMMDD_HHMMSS_<6 lowercase hex>.
	ID string
	// Path is the directory, absolute.
	Path string

	now func() time.Time

	// sink, when it is set, is told every record of the ledger once it is written.
	sink func(kind string, data json.RawMessage)

	mu       sync.Mutex
	entries  int  // lines in the ledger
	counted  bool // entries is known
	reported bool // report.md was written
}

// LedgerEntry is one line of a run's ledger.
type LedgerEntry struct {
	Seq  int             `json:"seq"`
	TS   string          `json:"ts"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

// checkDir is the usage failure for a --dir that is not a directory.
func checkDir(dir string) *Failure {
	info, err := os.Stat(dir)
	switch {
	case err != nil:
		return &Failure{Category: CategoryUsage, Message: fmt.Sprintf("--dir %s: %v", dir, err)}
	case !info.IsDir():
		return &Failure{Category: CategoryUsage, Message: fmt.Sprintf("--dir %s is not a directory", dir)}
	}
	return nil
}

// NewRunDir creates a run directory under dir, and with it dir/.agent-fox
// and its .gitignore when they are missing. now and rnd are the clock and the
// source of the id's random bytes; nil means the real ones. If the id is
// taken, another is drawn, up to five times in all.
func NewRunDir(dir string, now func() time.Time, rnd io.Reader) (*RunDir, error) {
	if now == nil {
		now = time.Now
	}
	runs, err := runsDirOf(dir)
	if err != nil {
		return nil, err
	}

	for range runIDAttempts {
		id, err := NewRunID(now, rnd)
		if err != nil {
			return nil, err
		}
		path := filepath.Join(runs, id)
		switch err := os.Mkdir(path, 0o755); {
		case err == nil:
			return &RunDir{ID: id, Path: path, now: now}, nil
		case !errors.Is(err, fs.ErrExist):
			return nil, &Failure{Category: CategoryIO, Message: fmt.Sprintf("cannot create %s: %v", path, err)}
		}
	}
	return nil, &Failure{Category: CategoryIO, Message: fmt.Sprintf("every one of %d run ids was taken in %s", runIDAttempts, runs)}
}

// NewRunID draws a run id: the time in UTC and three random bytes, in the form
// YYYYMMDD_HHMMSS_<6 hex> that a run directory is named with. now and rnd are the clock
// and the source of the random bytes; nil means the real ones.
func NewRunID(now func() time.Time, rnd io.Reader) (string, error) {
	if now == nil {
		now = time.Now
	}
	if rnd == nil {
		rnd = rand.Reader
	}
	var suffix [3]byte
	if _, err := io.ReadFull(rnd, suffix[:]); err != nil {
		return "", &Failure{Category: CategoryIO, Message: fmt.Sprintf("cannot draw a run id: %v", err)}
	}
	return now().UTC().Format("20060102_150405") + "_" + hex.EncodeToString(suffix[:]), nil
}

// runsDirOf is the directory that the run directories of dir are in, made with dir/.agent-fox
// and its .gitignore when they are missing.
func runsDirOf(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", &Failure{Category: CategoryIO, Message: fmt.Sprintf("cannot resolve %s: %v", dir, err)}
	}
	if failure := checkDir(abs); failure != nil {
		return "", failure
	}
	agent, err := agentdir.Ensure(abs)
	if err != nil {
		return "", &Failure{Category: CategoryIO, Message: fmt.Sprintf("cannot create %s: %v", filepath.Join(abs, agentDir), err)}
	}
	runs := filepath.Join(agent, runsDir)
	if err := os.MkdirAll(runs, 0o755); err != nil {
		return "", &Failure{Category: CategoryIO, Message: fmt.Sprintf("cannot create %s: %v", runs, err)}
	}
	return runs, nil
}

// newRunDirFor creates the run directory of an invocation of app: the one named with the
// run id the caller chose, if it chose one, and otherwise one with a drawn id.
func newRunDirFor(app *App, dir string) (*RunDir, error) {
	if app.RunID == "" {
		rd, err := NewRunDir(dir, app.now, app.Rand)
		if err == nil {
			rd.sink = app.LedgerSink
		}
		return rd, err
	}
	runs, err := runsDirOf(dir)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(runs, app.RunID)
	if app.ReuseRunDir {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return &RunDir{ID: app.RunID, Path: path, now: app.now, sink: app.LedgerSink}, nil
		}
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		return nil, &Failure{Category: CategoryIO, Message: fmt.Sprintf("cannot create the run directory %s: %v", path, err)}
	}
	return &RunDir{ID: app.RunID, Path: path, now: app.now, sink: app.LedgerSink}, nil
}

// OpenRunDir opens the existing run id under dir, for the commands that
// carry on a run another command started. An id that is not of the form of a
// run id, which keeps it from naming a path, or a run that is not there, is a
// usage failure; nothing is created.
func OpenRunDir(dir, id string) (*RunDir, error) {
	if !runIDFormat.MatchString(id) {
		return nil, &Failure{Category: CategoryUsage, Message: fmt.Sprintf("%q is not a run id (YYYYMMDD_HHMMSS_xxxxxx)", id)}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, &Failure{Category: CategoryIO, Message: fmt.Sprintf("cannot resolve %s: %v", dir, err)}
	}
	path := filepath.Join(abs, agentDir, runsDir, id)
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return nil, &Failure{Category: CategoryUsage, Message: fmt.Sprintf("there is no run %s under %s", id, abs)}
	}
	return &RunDir{ID: id, Path: path, now: time.Now}, nil
}

// file is the path of a file of the run directory.
func (r *RunDir) file(name string) string { return filepath.Join(r.Path, name) }

// Record appends an entry {seq, ts, kind, data} to the ledger, which is
// where commands note what they measured. The shell records nothing in it.
func (r *RunDir) Record(kind string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("ledger entry %q: %w", kind, err)
	}
	if err := r.append(kind, raw); err != nil {
		return err
	}
	if r.sink != nil {
		r.sink(kind, raw)
	}
	return nil
}

// append writes the entry to the ledger, under the lock of the run directory.
func (r *RunDir) append(kind string, raw json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.counted {
		// A run that was opened again carries on where its ledger stopped.
		existing, err := r.Ledger()
		if err != nil {
			return err
		}
		r.entries, r.counted = len(existing), true
	}
	line, err := json.Marshal(LedgerEntry{Seq: r.entries + 1, TS: stamp(r.now()), Kind: kind, Data: raw})
	if err != nil {
		return fmt.Errorf("ledger entry %q: %w", kind, err)
	}
	f, err := os.OpenFile(r.file("ledger.jsonl"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	r.entries++
	return nil
}

// Ledger reads the ledger back, in the order it was written.
func (r *RunDir) Ledger() ([]LedgerEntry, error) {
	data, err := os.ReadFile(r.file("ledger.jsonl"))
	if errors.Is(err, fs.ErrNotExist) {
		return []LedgerEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	entries := []LedgerEntry{}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var e LedgerEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("ledger line %d: %w", len(entries)+1, err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// WriteReport writes report.md, which the envelope then lists as an artifact.
func (r *RunDir) WriteReport(text string) error {
	if err := os.WriteFile(r.file("report.md"), []byte(text), 0o644); err != nil {
		return err
	}
	r.mu.Lock()
	r.reported = true
	r.mu.Unlock()
	return nil
}

// reportPath is the path of report.md if a command wrote it, else "".
func (r *RunDir) reportPath() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reported {
		return r.file("report.md")
	}
	return ""
}

// writePanic saves the stack of a recovered panic as panic.txt. The stack
// went to stderr as well, so a failure to write it is not worth another.
func (r *RunDir) writePanic(p *panicError) {
	_ = os.WriteFile(r.file("panic.txt"), []byte(fmt.Sprintf("panic: %v\n\n%s", p.value, p.stack)), 0o644)
}
