package conform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

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
	// CompileFailed is true when that run failed because the tests did not
	// compile: a failure that says nothing about behaviour, so not proof.
	// Revert leaves it to the caller to set (see CompileFailure).
	CompileFailed bool `json:"compile_failed,omitempty" description:"True when the checks failed with the implementation removed because the tests did not compile, which proves nothing about behaviour."`
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
	var impl []string
	impl, res.Tests = splitChange(changed)
	if len(impl) == 0 {
		res.Reason = "the change has no implementation file to take out: it only touches tests"
		return res, nil
	}
	snap, err := takeSnapshot(root, impl)
	if err != nil {
		return res, err
	}
	if err := snap.putBack(ctx, g, base); err != nil {
		return res, errors.Join(err, snap.restore())
	}
	res.Reverted = impl

	// The check runs under the run's own context, so a cancellation ends it
	// rather than waiting out --verify-timeout. The implementation is put
	// back from the snapshot either way; a caller that cannot afford to lose
	// it to a killed process holds it in a commit first.
	r := check(ctx)
	if err := snap.restore(); err != nil {
		return res, fmt.Errorf("the implementation could not be restored after the revert check: %w", err)
	}
	res.Check = &r
	res.Ran = r.Ran()
	switch {
	case !res.Ran:
		res.Reason = "no verification command ran"
	case r.Aborted:
		res.Reason = "the run was cancelled during the revert check"
	case r.ExitCode == -1 || r.TimedOut:
		res.Reason = "the verification could not run with the implementation removed"
	case r.OK:
		res.Reason = "the checks still pass with the implementation removed, so no test depends on it"
	default:
		res.Proves = true
	}
	return res, nil
}

// compileErrorRe matches what the common toolchains print when the code under
// test does not build, as opposed to a test that ran and failed: Go's
// "[build failed]", rustc's error codes, tsc's, javac and Maven's, Gradle's
// compile tasks, and the C# compiler's.
var compileErrorRe = regexp.MustCompile(`\[(build|setup) failed\]|error\[E\d{4}\]|could not compile ` +
	"`" + `|error TS\d+:|COMPILATION ERROR|Compilation failed|:compile\w*(Java|Kotlin) FAILED|error CS\d{4}:`)

// CompileFailure reports whether a failed run's output says the code did not
// compile.
func CompileFailure(output string) bool {
	return compileErrorRe.MatchString(output)
}

// splitChange sorts a change into its implementation and its tests.
// Documentation is the implementation only of a change that is nothing else:
// a docs task's tests are about the docs.
func splitChange(changed []string) (impl, tests []string) {
	var docs []string
	for _, p := range changed {
		switch {
		case project.IsTestPath(p):
			tests = append(tests, p)
		case project.IsDocsFile(p):
			docs = append(docs, p)
		default:
			impl = append(impl, p)
		}
	}
	if len(impl) == 0 {
		impl = docs
	}
	return impl, tests
}

// savedFile is one file as the change left it.
type savedFile struct {
	rel, full string
	content   []byte
	mode      os.FileMode
	present   bool
}

type snapshot []savedFile

func takeSnapshot(root string, paths []string) (snapshot, error) {
	var snap snapshot
	for _, p := range paths {
		s := savedFile{rel: p, full: filepath.Join(root, filepath.FromSlash(p)), mode: 0o644}
		if info, err := os.Stat(s.full); err == nil {
			b, err := os.ReadFile(s.full)
			if err != nil {
				return nil, err
			}
			s.content, s.mode, s.present = b, info.Mode().Perm(), true
		}
		snap = append(snap, s)
	}
	return snap, nil
}

// putBack sets every file to its content at base, removing one base does
// not have.
func (snap snapshot) putBack(ctx context.Context, g *gitx.Git, base string) error {
	for _, s := range snap {
		content, exists, err := g.ShowFile(ctx, base, s.rel)
		if err != nil {
			return err
		}
		if !exists {
			if err := os.Remove(s.full); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		if err := writeFile(s.full, []byte(content), s.mode); err != nil {
			return err
		}
	}
	return nil
}

// restore returns every file to what the change left.
func (snap snapshot) restore() error {
	var errs []error
	for _, s := range snap {
		if !s.present {
			if err := os.Remove(s.full); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
			continue
		}
		if err := writeFile(s.full, s.content, s.mode); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func writeFile(full string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, content, mode)
}
