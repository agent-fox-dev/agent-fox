package checks

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/project"
)

// The statuses of a check that ran.
const (
	StatusPass    = "pass"
	StatusFail    = "fail"
	StatusError   = "error"
	StatusTimeout = "timeout"
)

// ReasonUnavailable is why a check was skipped: its program cannot be run
// here.
const ReasonUnavailable = "unavailable"

// DefaultCheckTimeout is how long one check may run. It is shorter than the
// 30 minutes that af gate run gets in all, so that one check that hangs is a
// result, and the checks after it still run.
const DefaultCheckTimeout = 20 * time.Minute

// The tail of a check is the end of its output, so much of it at most.
const (
	tailLines = 40
	tailBytes = 4096
)

// Options say what a gate run does.
type Options struct {
	// Table is the gate table.
	Table project.Table
	// Hermetic runs the checks under gitx.HermeticRunner with a HOME of
	// their own.
	Hermetic bool
	// NoCache runs the checks even when a result is stored for the key.
	NoCache bool
	// Label is carried into the result.
	Label string
	// CheckTimeout is the deadline of each check; DefaultCheckTimeout when
	// zero.
	CheckTimeout time.Duration
}

// CheckResult is what one check did.
type CheckResult struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	Log        string `json:"log"`
	Tail       string `json:"tail"`
}

// Skipped is a check that did not run, and why.
type Skipped struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Result is a gate run.
type Result struct {
	ID          string        `json:"id"`
	Tree        string        `json:"tree"`
	TableSource string        `json:"table_source"`
	Hermetic    bool          `json:"hermetic"`
	Label       string        `json:"label"`
	Cached      bool          `json:"cached"`
	Passed      bool          `json:"passed"`
	Checks      []CheckResult `json:"checks"`
	Skipped     []Skipped     `json:"skipped"`

	// TreeChanged says that the checks changed the tree. It is a fact of
	// this run and is not stored.
	TreeChanged bool `json:"-"`
}

// Run runs the checks of opts.Table that can be run, in order, in dir, and
// stores the result under .agent-fox/gate/<id>/, unless a result is stored for
// the tree already. The label of the options, if any, is recorded for the
// result in labels.json, a hit included; one that is not allowed is a
// *UsageError, and nothing runs. A run that ctx stops stores nothing and returns the
// context's error.
func Run(ctx context.Context, dir string, opts Options) (Result, error) {
	if err := ValidateLabel(opts.Label); err != nil {
		return Result{}, err
	}
	tree, err := TreeHash(ctx, dir)
	if err != nil {
		return Result{}, err
	}
	mode := ModeReduced
	if opts.Hermetic {
		mode = ModeHermetic
	}
	key := KeyOf(tree, opts.Table.Checks, mode)

	if !opts.NoCache {
		if res, ok := loadResult(dir, key); ok && measuresTable(res, opts.Table) {
			res.Cached, res.Label, res.TableSource = true, opts.Label, opts.Table.Source
			return labelled(dir, res)
		}
	}

	res := Result{
		ID: key.ID, Tree: tree, TableSource: opts.Table.Source, Hermetic: opts.Hermetic, Label: opts.Label,
		Checks: []CheckResult{}, Skipped: []Skipped{},
	}
	logs, err := execute(ctx, dir, key, opts, &res)
	if err != nil {
		return Result{}, err
	}
	res.Passed = len(res.Checks) > 0
	for _, c := range res.Checks {
		res.Passed = res.Passed && c.Status == StatusPass
	}

	after, err := TreeHash(ctx, dir)
	if err != nil {
		return Result{}, err
	}
	res.TreeChanged = after != tree

	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	// Nothing is on disk until the run has completed: a run that is stopped
	// leaves the logs of the result that is stored as they were.
	if err := writeLogs(dir, key, logs); err != nil {
		return Result{}, err
	}
	if err := storeResult(dir, key, res); err != nil {
		return Result{}, err
	}
	return labelled(dir, res)
}

// logFile is the full output of a check, to be written when the run is done.
type logFile struct {
	name    string
	content string
}

// writeLogs writes the logs of a run that completed, each atomically.
func writeLogs(dir string, key Key, logs []logFile) error {
	if err := os.MkdirAll(key.Dir(dir), 0o755); err != nil {
		return err
	}
	for _, l := range logs {
		if err := agentdir.WriteFileAtomic(filepath.Join(key.Dir(dir), l.name), []byte(l.content)); err != nil {
			return err
		}
	}
	return nil
}

// labelled records the label of a result that is stored, if it has one.
func labelled(dir string, res Result) (Result, error) {
	if res.Label != "" {
		if err := recordLabel(dir, res.Label, res.ID); err != nil {
			return Result{}, err
		}
	}
	return res, nil
}

// measuresTable reports whether a stored result stands for the table here: a
// check it skipped must still be one that cannot be run. A result made where a
// program was missing says nothing about the checks of that program.
func measuresTable(res Result, table project.Table) bool {
	for _, s := range res.Skipped {
		for _, c := range table.Checks {
			if c.Name == s.Name && c.Available {
				return false
			}
		}
	}
	return true
}

// execute runs the available checks of the table into res, and returns their
// logs for the caller to write once the run is complete.
func execute(ctx context.Context, dir string, key Key, opts Options, res *Result) ([]logFile, error) {
	run, cleanup, err := newRunner(opts.Hermetic)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	limit := opts.CheckTimeout
	if limit <= 0 {
		limit = DefaultCheckTimeout
	}

	var logs []logFile
	for _, c := range opts.Table.Checks {
		if !c.Available {
			res.Skipped = append(res.Skipped, Skipped{Name: c.Name, Reason: ReasonUnavailable})
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cr, log, err := runCheck(ctx, dir, run, c, limit)
		if err != nil {
			return nil, err
		}
		log.name = fmt.Sprintf("%02d-%s.log", len(res.Checks)+1, slug(c.Name))
		cr.Log = key.relDir() + "/" + log.name
		res.Checks = append(res.Checks, cr)
		logs = append(logs, log)
	}
	return logs, nil
}

// newRunner is the runner the checks run under, and what to call when they
// are done. A hermetic run gets a HOME of its own, which is removed however
// the run ends.
func newRunner(hermetic bool) (gitx.Runner, func(), error) {
	if !hermetic {
		return gitx.ReducedEnvRunner, func() {}, nil
	}
	home, err := os.MkdirTemp("", "af-gate-home-")
	if err != nil {
		return nil, nil, err
	}
	return gitx.HermeticRunner(home), func() { removeTree(home) }, nil
}

// removeTree removes a directory and what is in it. A program may have left
// directories in it that cannot be written to (Go's module cache is read-only),
// and those are made writable first.
func removeTree(root string) {
	if os.RemoveAll(root) == nil {
		return
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(path, 0o700)
		}
		return nil
	})
	_ = os.RemoveAll(root)
}

// runCheck runs one check under the deadline limit. It returns ctx's error when
// ctx stopped the check, and otherwise the result and the log, which has no
// name yet.
func runCheck(ctx context.Context, dir string, run gitx.Runner, c project.Check, limit time.Duration) (CheckResult, logFile, error) {
	cctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	started := time.Now()
	out, code, err := run(cctx, dir, c.Argv)
	elapsed := time.Since(started)

	stopped := err != nil || code < 0 // a process that was killed has no exit code
	if cerr := ctx.Err(); cerr != nil && stopped {
		return CheckResult{}, logFile{}, cerr
	}

	status, note := StatusPass, ""
	switch {
	case stopped && errors.Is(cctx.Err(), context.DeadlineExceeded):
		status, code, note = StatusTimeout, -1, fmt.Sprintf("af: the check did not finish within %s and was stopped", limit)
	case err != nil:
		status, code, note = StatusError, -1, "af: "+err.Error()
	case code != 0:
		status = StatusFail
	}
	log := out
	if note != "" {
		if log != "" && !strings.HasSuffix(log, "\n") {
			log += "\n"
		}
		log += note + "\n"
	}
	return CheckResult{
		Name:       c.Name,
		Status:     status,
		ExitCode:   code,
		DurationMS: elapsed.Milliseconds(),
		Tail:       tailOf(log),
	}, logFile{content: log}, nil
}

// slug is name as part of a file name: lower-case letters and digits, joined
// by single dashes, at most 40 characters.
func slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	if s == "" {
		return "check"
	}
	return s
}

// tailOf is the last 40 lines of s, and of those at most the last 4096 bytes,
// without a character cut in two.
func tailOf(s string) string {
	body := strings.TrimSuffix(s, "\n") // a last newline ends a line, it does not start one
	start, seen := 0, 0
	for i := len(body) - 1; i >= 0; i-- {
		if body[i] == '\n' {
			if seen++; seen == tailLines {
				start = i + 1
				break
			}
		}
	}
	tail := s[start:]
	if len(tail) > tailBytes {
		tail = tail[len(tail)-tailBytes:]
		for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
			tail = tail[1:]
		}
	}
	return tail
}
