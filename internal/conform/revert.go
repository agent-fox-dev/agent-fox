// Package conform measures whether a change proves itself: whether the tests
// that came with it fail without the rest of the change.
package conform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/project"
)

// The outcomes of a revert check.
const (
	OutcomeNoChanges     = "no_changes"
	OutcomeNoCodeChanges = "no_code_changes"
	OutcomeNoTestChanges = "no_test_changes"
	OutcomeInconclusive  = "inconclusive"
	OutcomeProven        = "proven"
	OutcomeNotProven     = "not_proven"
)

// patchName is the file of .agent-fox that holds the change while a check has
// it out of the tree.
const patchName = "revert.patch"

// Options say how a revert check runs.
type Options struct {
	// Table is the gate table; the checks of kind test or check are the tests.
	Table project.Table
	// CheckTimeout is the deadline of each check, as for the gate.
	CheckTimeout time.Duration
	// TableNow, when it is set, finds the table of checks again on the reverted
	// tree. A table that is not the one that was measured (a Makefile that the
	// change added is not there to run) makes the outcome inconclusive: a
	// check that cannot run says nothing about the tests.
	TableNow func(ctx context.Context) (project.Table, error)
}

// Reverted is what the checks said about the reverted tree.
type Reverted struct {
	ID     string               `json:"id"`
	Passed bool                 `json:"passed"`
	Checks []checks.CheckResult `json:"checks"`
}

// Result is a revert check.
type Result struct {
	Outcome       string    `json:"outcome"`
	Reason        string    `json:"reason"`
	Closes        bool      `json:"closes"`
	Since         string    `json:"since"`
	TestFiles     []string  `json:"test_files"`
	RevertedFiles []string  `json:"reverted_files"`
	After         string    `json:"after"`
	Reverted      *Reverted `json:"reverted"`
	Restored      bool      `json:"restored"`
}

// RestoreError says that the tree could not be put back, or that another check
// holds the tree or left its patch. The patch is kept, in Patch: git apply puts
// the change back.
type RestoreError struct {
	Patch string
	Cause error
}

func (e *RestoreError) Error() string {
	return fmt.Sprintf("%v; the change is kept in %s", e.Cause, e.Patch)
}

func (e *RestoreError) Unwrap() error { return e.Cause }

// GitError says that git failed in a way the check cannot work around.
type GitError struct {
	Err error
}

func (e *GitError) Error() string { return e.Err.Error() }

func (e *GitError) Unwrap() error { return e.Err }

// usage is a mistake in the arguments of the check.
func usage(format string, args ...any) error {
	return &checks.UsageError{Message: fmt.Sprintf(format, args...)}
}

// Check answers one question about the change in dir since the commit since:
// do the tests of the change fail without the rest of it? dir is the top level
// of a git work tree.
//
// It lists what differs from since, takes the change of the paths that are not
// tests out of the work tree as a patch (git apply --reverse), runs the tests,
// and applies the patch again. The index is never written by the check: a check
// that rewrote it gets its bytes back, and the tree hash and the index are
// checked at the end. The change is put back when the check is cancelled, too,
// and when it panics. A run that is killed leaves its patch in
// .agent-fox/revert.patch, and the next check refuses until someone has looked
// at it.
func Check(ctx context.Context, dir, since string, opts Options) (Result, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return Result{}, err
	}
	g := git{dir: dir}
	commit, err := g.resolve(ctx, since)
	if err != nil {
		return Result{}, err
	}
	if err := refuseLeftover(dir); err != nil {
		return Result{}, err
	}
	if _, err := agentdir.Ensure(dir); err != nil {
		return Result{}, err
	}

	paths, err := g.changedPaths(ctx, commit)
	if err != nil {
		return Result{}, err
	}
	res := Result{Since: commit, TestFiles: []string{}, RevertedFiles: []string{}, Restored: true}
	var code []string
	for _, p := range paths {
		if project.IsTestPath(p) {
			res.TestFiles = append(res.TestFiles, p)
		} else {
			code = append(code, p)
		}
	}
	if outcome, reason := nothingToMeasure(len(paths), len(code), len(res.TestFiles), opts.Table); outcome != "" {
		res.Outcome, res.Reason = outcome, reason
		return res, nil
	}

	after, err := checks.Run(ctx, dir, checks.Options{Table: opts.Table, CheckTimeout: opts.CheckTimeout})
	if err != nil {
		return Result{}, err
	}
	res.After = after.ID
	if reason := notGreen(opts.Table, after); reason != "" {
		res.Outcome, res.Reason = OutcomeInconclusive, reason
		return res, nil
	}

	rv := &reverter{g: g, dir: dir, commit: commit, table: opts.Table, timeout: opts.CheckTimeout, tableNow: opts.TableNow, paths: code}
	return rv.run(ctx, res)
}

// nothingToMeasure says why no revert can be run, in this order: nothing
// changed, only tests changed, no test changed, or there is no check that runs
// tests.
func nothingToMeasure(changed, code, tests int, table project.Table) (outcome, reason string) {
	switch {
	case changed == 0:
		return OutcomeNoChanges, "no_paths_changed"
	case code == 0:
		return OutcomeNoCodeChanges, "only_test_files_changed"
	case tests == 0:
		return OutcomeNoTestChanges, "no_test_files_changed"
	case len(testChecks(table).Checks) == 0:
		return OutcomeInconclusive, "no_test_check"
	}
	return "", ""
}

// isTestKind reports whether a check of this kind runs tests.
func isTestKind(kind string) bool {
	return kind == project.KindTest || kind == project.KindCheck
}

// testChecks is the table of the checks that run tests.
func testChecks(table project.Table) project.Table {
	only := project.Table{Source: table.Source}
	for _, c := range table.Checks {
		if isTestKind(c.Kind) {
			only.Checks = append(only.Checks, c)
		}
	}
	return only
}

// notGreen says why the current tree cannot be measured against: the checks
// that run tests did not all pass on it, or none of them could run. It is ""
// when they ran and passed.
func notGreen(table project.Table, after checks.Result) string {
	kinds := map[string]string{}
	for _, c := range table.Checks {
		kinds[c.Name] = c.Kind
	}
	ran, red := 0, 0
	for _, c := range after.Checks {
		if isTestKind(kinds[c.Name]) {
			ran++
			if c.Status != checks.StatusPass {
				red++
			}
		}
	}
	switch {
	case ran == 0:
		return "test_check_unavailable"
	case red > 0:
		return "after_not_green"
	}
	return ""
}

// git runs git in dir, with env added to the environment.
type git struct {
	dir string
	env []string
}

// output runs git and returns what it printed, exactly, and its exit code. An
// error is git that could not be run, or the context's.
func (g git) output(ctx context.Context, args ...string) (string, int, error) {
	out, code, err := gitx.EnvRunner(g.env...)(gitx.StdoutOnly(ctx), g.dir, append([]string{"git"}, args...))
	if cerr := ctx.Err(); cerr != nil {
		return "", code, cerr
	}
	if err != nil {
		return "", code, &GitError{Err: err}
	}
	return out, code, nil
}

// fail is the error for git that ran and exited with a code.
func (g git) fail(what string, code int, out string) error {
	return &GitError{Err: fmt.Errorf("git %s exited %d: %s", what, code, strings.TrimSpace(out))}
}

// resolve checks that dir is the top level of a work tree and that since is a
// commit, and returns the full hash of the commit.
func (g git) resolve(ctx context.Context, since string) (string, error) {
	switch {
	case since == "":
		return "", usage("--since is required: name the commit the change starts from")
	case strings.HasPrefix(since, "-"):
		return "", usage("--since %q is not a commit", since)
	}
	top, code, err := g.output(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", usage("--dir %s is not inside a git work tree", g.dir)
	}
	if top = strings.TrimSpace(top); !sameDir(top, g.dir) {
		return "", usage("--dir %s is not the top level of its work tree, which is %s", g.dir, top)
	}
	out, code, err := g.output(ctx, "rev-parse", "--verify", "--quiet", since+"^{commit}")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", usage("--since %q does not resolve to a commit", since)
	}
	return strings.TrimSpace(out), nil
}

// sameDir reports whether two paths are the same directory, links resolved.
func sameDir(a, b string) bool {
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	return resolve(a) == resolve(b)
}

// indexPath is where git keeps the index of the work tree.
func (g git) indexPath(ctx context.Context) (string, error) {
	out, code, err := g.output(ctx, "rev-parse", "--git-path", "index")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", g.fail("rev-parse --git-path index", code, out)
	}
	p := strings.TrimSpace(out)
	if !filepath.IsAbs(p) {
		p = filepath.Join(g.dir, p)
	}
	return p, nil
}

// changedPaths lists the paths that differ from commit, untracked files
// included. git diff refreshes the index and writes it back, so the listing
// runs against a copy of it and the real index is not written.
func (g git) changedPaths(ctx context.Context, commit string) ([]string, error) {
	real, err := g.indexPath(ctx)
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "af-index-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	private := filepath.Join(tmp, "index")
	if err := copyIndex(real, private); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	listed, err := gitx.New(g.dir, gitx.EnvRunner("GIT_INDEX_FILE="+private)).ChangedPaths(ctx, commit)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		return nil, &GitError{Err: err}
	}
	paths := make([]string, 0, len(listed))
	for _, p := range listed {
		if p != agentdir.Name && !strings.HasPrefix(p, agentdir.Name+"/") {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// copyIndex copies the index at src to dst, with its modification time. Git
// calls an entry racily clean when it is not older than the index file, and
// then reads the file to see whether it changed, because a change in the same
// tick of the file system's clock leaves no trace in the stat data. A copy
// that is newer than its entries would make every such entry look unchanged.
func copyIndex(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return err
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

// change is the change of paths since commit as a binary patch that git apply
// takes, and the paths it changes. It is made on a private index read from the
// commit, with the paths added from the work tree, so the real index is not
// written. Submodules and nested repositories are left out: git apply does not
// put their content back.
func (g git) change(ctx context.Context, commit string, paths []string) (patch string, files []string, err error) {
	tmp, err := os.MkdirTemp("", "af-index-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(tmp)
	private := git{dir: g.dir, env: []string{"GIT_INDEX_FILE=" + filepath.Join(tmp, "index")}}
	if err := private.must(ctx, "read-tree", commit); err != nil {
		return "", nil, err
	}
	for chunk := range slices.Chunk(paths, 200) {
		if err := private.must(ctx, append([]string{"--literal-pathspecs", "add", "--all", "--force", "--"}, chunk...)...); err != nil {
			return "", nil, err
		}
	}
	diff := []string{"diff", "--cached", "--no-renames", "--ignore-submodules=all", "--no-ext-diff", "--no-textconv", "--no-color"}
	names, code, err := private.output(ctx, append(diff, "--name-only", "-z", commit)...)
	if err != nil {
		return "", nil, err
	}
	if code != 0 {
		return "", nil, g.fail("diff --name-only", code, names)
	}
	if files = splitNUL(names); len(files) == 0 {
		return "", nil, nil
	}
	patch, code, err = private.output(ctx, append(diff, "--binary", "--full-index", "--src-prefix=a/", "--dst-prefix=b/", commit)...)
	if err != nil {
		return "", nil, err
	}
	if code != 0 {
		return "", nil, g.fail("diff --binary", code, patch)
	}
	return patch, files, nil
}

// must runs git and fails unless it exits 0.
func (g git) must(ctx context.Context, args ...string) error {
	out, code, err := g.output(ctx, args...)
	if err != nil {
		return err
	}
	if code != 0 {
		return g.fail(args[0], code, out)
	}
	return nil
}

// apply applies the patch to the work tree, or takes it out of it, and leaves
// the index as it is. git apply changes every path or none.
func (g git) apply(ctx context.Context, patch string, reverse bool) error {
	args := []string{"apply", "--whitespace=nowarn"}
	if reverse {
		args = append(args, "--reverse")
	}
	return g.must(ctx, append(args, "--", patch)...)
}

// splitNUL splits a NUL-separated listing into its entries.
func splitNUL(out string) []string {
	var entries []string
	for _, e := range strings.Split(out, "\x00") {
		if e != "" {
			entries = append(entries, e)
		}
	}
	return entries
}

// patchPath is the file that holds the change while it is out of the tree.
func patchPath(dir string) string {
	return filepath.Join(dir, agentdir.Name, patchName)
}

// refuseLeftover refuses to start while the patch of another check is there.
func refuseLeftover(dir string) error {
	p := patchPath(dir)
	_, err := os.Lstat(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	}
	return leftover(p)
}

// leftover is the error for a patch that another check left, or holds.
func leftover(p string) error {
	return &RestoreError{
		Patch: p,
		Cause: errors.New("another revert-check is working on this checkout, or one was killed and left the change it took out. Wait for it; or, when git apply --check of the patch passes, put the change back with git apply, and remove the patch, before checking again"),
	}
}

// reverter takes the change out of the tree, and puts it back.
type reverter struct {
	g        git
	dir      string
	commit   string
	table    project.Table
	timeout  time.Duration
	tableNow func(ctx context.Context) (project.Table, error)
	paths    []string // the changed paths that are not tests

	patch     string
	preTree   string
	indexPath string
	preIndex  []byte
}

// run takes the change out, runs the tests and puts the change back. It is put
// back whatever happens in between, and a failure to put it back replaces the
// result.
func (rv *reverter) run(ctx context.Context, res Result) (out Result, err error) {
	diff, files, err := rv.g.change(ctx, rv.commit, rv.paths)
	if err != nil {
		return Result{}, err
	}
	if len(files) == 0 {
		res.Outcome, res.Reason = OutcomeInconclusive, "nothing_to_revert"
		return res, nil
	}
	if rv.preTree, err = checks.TreeHash(ctx, rv.dir); err != nil {
		return Result{}, err
	}
	if rv.indexPath, err = rv.g.indexPath(ctx); err != nil {
		return Result{}, err
	}
	rv.preIndex = readIfThere(rv.indexPath)
	if err := rv.claim(diff); err != nil {
		return Result{}, err
	}
	if err := rv.revert(ctx); err != nil {
		return Result{}, err
	}
	defer func() {
		if rerr := rv.restore(context.WithoutCancel(ctx)); rerr != nil {
			out, err = Result{}, rerr
		}
	}()

	res.RevertedFiles = files
	if rv.tableNow != nil && rv.tableChanged(ctx) {
		res.Outcome, res.Reason = OutcomeInconclusive, "table_changed_by_revert"
		return res, nil
	}
	reverted, err := checks.Run(ctx, rv.dir, checks.Options{Table: testChecks(rv.table), CheckTimeout: rv.timeout})
	if err != nil {
		return Result{}, err
	}
	return rv.conclude(res, reverted), nil
}

// claim writes the patch, exclusively: it is the claim on the tree that keeps
// two checks from working on it together, and what is left to put the change
// back by hand when a check is killed.
func (rv *reverter) claim(diff string) error {
	rv.patch = patchPath(rv.dir)
	f, err := os.OpenFile(rv.patch, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return leftover(rv.patch)
	}
	if err != nil {
		return err
	}
	_, err = io.WriteString(f, diff)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(rv.patch)
	}
	return err
}

// revert takes the change out of the work tree. Nothing cancels it: git apply
// changes every path or none, and a git that is stopped half way would leave
// neither. When it fails the tree is as it was, and the patch is taken away.
func (rv *reverter) revert(ctx context.Context) error {
	err := rv.g.apply(context.WithoutCancel(ctx), rv.patch, true)
	if err == nil {
		return nil
	}
	if verr := rv.verify(context.WithoutCancel(ctx)); verr != nil {
		return &RestoreError{Patch: rv.patch, Cause: fmt.Errorf("the change could not be taken out (%w), and the tree is not as it was: %w", err, verr)}
	}
	_ = os.Remove(rv.patch)
	return err
}

// tableChanged reports whether the checks that run tests, found again on the
// reverted tree, are not the ones that were measured. When that cannot be told
// they are taken to have changed.
func (rv *reverter) tableChanged(ctx context.Context) bool {
	now, err := rv.tableNow(ctx)
	if err != nil {
		return true
	}
	mine, theirs := testChecks(rv.table).Checks, testChecks(now).Checks
	return !slices.EqualFunc(mine, theirs, func(a, b project.Check) bool {
		return a.Name == b.Name && a.Kind == b.Kind && slices.Equal(a.Argv, b.Argv)
	})
}

// conclude turns what the tests said on the reverted tree into the result.
func (rv *reverter) conclude(res Result, reverted checks.Result) Result {
	res.Reverted = &Reverted{ID: reverted.ID, Passed: reverted.Passed, Checks: reverted.Checks}

	failed, errored := false, false
	for _, c := range reverted.Checks {
		failed = failed || c.Status == checks.StatusFail || c.Status == checks.StatusTimeout
		errored = errored || c.Status == checks.StatusError
	}
	switch {
	case len(reverted.Checks) == 0:
		res.Outcome, res.Reason = OutcomeInconclusive, "test_check_unavailable"
	case failed:
		res.Outcome, res.Reason, res.Closes = OutcomeProven, "tests_fail_without_the_change", true
	case errored:
		// A check that could not start measured nothing about the tests.
		res.Outcome, res.Reason = OutcomeInconclusive, "reverted_check_error"
	default:
		res.Outcome, res.Reason = OutcomeNotProven, "tests_pass_without_the_change"
	}
	return res
}

// restore applies the patch again, writes the index back if a check rewrote
// it, and checks that the tree hash and the index are what they were. Only
// when all is well is the patch removed. A path that a check changed while the
// change was out makes git apply refuse, and the tree stays without the
// change: what the check did is not overwritten.
func (rv *reverter) restore(ctx context.Context) error {
	var problems []error
	if err := rv.g.apply(ctx, rv.patch, false); err != nil {
		problems = append(problems, fmt.Errorf("the change could not be applied again: %w", err))
	}
	if err := rv.restoreIndex(); err != nil {
		problems = append(problems, err)
	}
	if len(problems) == 0 {
		if err := rv.verify(ctx); err != nil {
			problems = append(problems, err)
		}
	}
	if len(problems) > 0 {
		return &RestoreError{Patch: rv.patch, Cause: fmt.Errorf("the tree could not be put back: %w", errors.Join(problems...))}
	}
	if err := os.Remove(rv.patch); err != nil {
		return &RestoreError{Patch: rv.patch, Cause: fmt.Errorf("the tree was put back, but the patch could not be removed: %w", err)}
	}
	return nil
}

// readIfThere reads a file, or returns nil when it cannot be read.
func readIfThere(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}

// restoreIndex writes the bytes of the index back if a check changed them. A
// check that runs git, or a build that does, refreshes the stat data of the
// index and rewrites it, with the same entries and other bytes; the index is
// written the way git writes it, through its lock, and a git that is running
// holds the lock and makes this fail rather than lose a write.
func (rv *reverter) restoreIndex() error {
	if bytes.Equal(readIfThere(rv.indexPath), rv.preIndex) {
		return nil
	}
	if rv.preIndex == nil {
		return os.Remove(rv.indexPath)
	}
	lock := rv.indexPath + ".lock"
	f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("the index was rewritten while the check ran and cannot be written back: %w", err)
	}
	_, werr := f.Write(rv.preIndex)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(lock)
		return fmt.Errorf("the index cannot be written back: %w", werr)
	}
	return os.Rename(lock, rv.indexPath)
}

// verify checks that the tree is the one the revert started from: the same
// hash, and an index that is byte for byte the same.
func (rv *reverter) verify(ctx context.Context) error {
	tree, err := checks.TreeHash(ctx, rv.dir)
	if err != nil {
		return err
	}
	if tree != rv.preTree {
		return fmt.Errorf("the tree hash after the restore is %s, but it was %s before the revert", tree, rv.preTree)
	}
	if !bytes.Equal(readIfThere(rv.indexPath), rv.preIndex) {
		return errors.New("the index is not byte for byte as it was before the revert")
	}
	return nil
}
