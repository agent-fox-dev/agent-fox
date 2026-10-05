package conform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/project"
)

// RevertResult is what the checks did with the change's implementation taken
// out and its tests left in.
//
// It is the measured form of "the test fails without the fix": a test that
// still passes when the code it is meant to prove is removed proves nothing
// about that code, however it was written.
type RevertResult struct {
	// Ran is true when the check was run with the implementation removed.
	Ran bool `json:"ran" description:"True when the checks ran with the implementation removed."`
	// Reverted are the implementation files that were put back as they were
	// at the base for the run; Tests are the test files that stayed.
	Reverted []string `json:"reverted,omitempty" trust:"fact" description:"The implementation files put back to the base for the run."`
	Tests    []string `json:"tests,omitempty" trust:"fact" description:"The test files left in place."`
	// Check is the run with the implementation removed.
	Check *checks.Result `json:"check,omitempty" description:"The checks with the implementation removed."`
	// Proves is true when that run failed: the tests catch the removal.
	Proves bool `json:"proves" description:"True when the checks failed with the implementation removed, so the tests catch its absence."`
	// Reason says why the check did not run, or why it proves nothing.
	Reason string `json:"reason,omitempty" trust:"fact" description:"Why the check did not run or proves nothing."`
}

// Revert runs check with every non-test file of changed put back to its
// content at base, and then restores the tree exactly as it was. changed is
// the change's paths relative to the repository root; check is the
// verification to run.
func Revert(ctx context.Context, g *gitx.Git, root, base string, changed []string,
	check func(context.Context) checks.Result) (RevertResult, error) {

	var res RevertResult
	var impl, docs []string
	for _, p := range changed {
		switch {
		case project.IsTestPath(p):
			res.Tests = append(res.Tests, p)
		case project.IsDocsFile(p):
			docs = append(docs, p)
		default:
			impl = append(impl, p)
		}
	}
	// Documentation is the implementation only of a change that is nothing
	// else: a docs task's tests are about the docs.
	if len(impl) == 0 {
		impl = docs
	}
	if len(impl) == 0 {
		res.Reason = "the change has no implementation file to take out: it only touches tests"
		return res, nil
	}

	type saved struct {
		path    string
		content []byte
		mode    os.FileMode
		present bool
	}
	var snapshot []saved
	for _, p := range impl {
		full := filepath.Join(root, filepath.FromSlash(p))
		s := saved{path: full}
		if info, err := os.Stat(full); err == nil {
			b, err := os.ReadFile(full)
			if err != nil {
				return res, err
			}
			s.content, s.mode, s.present = b, info.Mode().Perm(), true
		}
		snapshot = append(snapshot, s)
	}
	restore := func() error {
		var errs []error
		for _, s := range snapshot {
			if !s.present {
				if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
					errs = append(errs, err)
				}
				continue
			}
			if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
				errs = append(errs, err)
				continue
			}
			if err := os.WriteFile(s.path, s.content, s.mode); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}

	for i, p := range impl {
		content, exists, err := g.ShowFile(ctx, base, p)
		if err != nil {
			return res, errors.Join(err, restore())
		}
		full := snapshot[i].path
		if !exists {
			if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
				return res, errors.Join(err, restore())
			}
			continue
		}
		mode := snapshot[i].mode
		if mode == 0 {
			mode = 0o644
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return res, errors.Join(err, restore())
		}
		if err := os.WriteFile(full, []byte(content), mode); err != nil {
			return res, errors.Join(err, restore())
		}
	}
	res.Reverted = impl

	r := check(context.WithoutCancel(ctx))
	if err := restore(); err != nil {
		return res, fmt.Errorf("the implementation could not be restored after the revert check: %w", err)
	}
	res.Check = &r
	res.Ran = r.Ran()
	switch {
	case !res.Ran:
		res.Reason = "no verification command ran"
	case r.ExitCode == -1 || r.TimedOut:
		res.Reason = "the verification could not run with the implementation removed"
	case r.OK:
		res.Reason = "the checks still pass with the implementation removed, so no test depends on it"
	default:
		res.Proves = true
	}
	return res, nil
}
