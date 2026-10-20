// Package conform measures whether a change proves itself: whether the tests
// that came with it fail without the rest of the change.
package conform

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
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

// The states a path was in before the revert, as the manifest records them.
const (
	stateFile    = "file"
	stateSymlink = "symlink"
	stateAbsent  = "absent"
)

// revertDirName is the directory of .agent-fox that holds the backups.
const revertDirName = "revert"

// Options say how a revert check runs.
type Options struct {
	// Table is the gate table; the checks of kind test or check are the tests.
	Table project.Table
	// CheckTimeout is the deadline of each check, as for the gate.
	CheckTimeout time.Duration
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

// RestoreError says that the tree could not be put back, or that an earlier
// check left its backup. The backup is kept, in Dir.
type RestoreError struct {
	Dir   string
	Cause error
}

func (e *RestoreError) Error() string {
	return fmt.Sprintf("%v; the backup is kept in %s", e.Cause, e.Dir)
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
// It lists what differs from since, and sets the paths that are not tests back
// to what the commit has, runs the tests, and puts the paths back as they were.
// The index is never written, in any outcome: the paths are changed in the work
// tree only, and the tree hash and the index are checked at the end. The paths
// are put back when the check is cancelled, too, and when it panics. A run that
// is killed leaves its backup in .agent-fox/revert/, and the next check refuses
// until someone has looked at it.
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

	rv := &reverter{g: g, dir: dir, commit: commit, table: opts.Table, timeout: opts.CheckTimeout, paths: code}
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

// git runs git in dir.
type git struct {
	dir string
}

// output runs git and returns what it printed, exactly, and its exit code. An
// error is git that could not be run, or the context's.
func (g git) output(ctx context.Context, args ...string) (string, int, error) {
	out, code, err := gitx.ExecRunner(ctx, g.dir, append([]string{"git"}, args...))
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

// treeEntry is what a commit has at a path.
type treeEntry struct {
	mode, typ, object string
}

// entriesAt reads what commit has at each of paths. A path it has nothing at is
// not in the result.
func (g git) entriesAt(ctx context.Context, commit string, paths []string) (map[string]treeEntry, error) {
	entries := map[string]treeEntry{}
	for chunk := range slices.Chunk(paths, 200) {
		args := append([]string{"--literal-pathspecs", "ls-tree", "-z", commit, "--"}, chunk...)
		out, code, err := g.output(ctx, args...)
		if err != nil {
			return nil, err
		}
		if code != 0 {
			return nil, g.fail("ls-tree", code, out)
		}
		for _, rec := range strings.Split(out, "\x00") {
			meta, p, ok := strings.Cut(rec, "\t")
			if fields := strings.Fields(meta); ok && len(fields) == 3 {
				entries[p] = treeEntry{mode: fields[0], typ: fields[1], object: fields[2]}
			}
		}
	}
	return entries, nil
}

// blob is the content of a blob, exactly as git has it.
func (g git) blob(ctx context.Context, object string) ([]byte, error) {
	out, code, err := g.output(ctx, "cat-file", "blob", object)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, g.fail("cat-file blob "+object, code, out)
	}
	return []byte(out), nil
}

// refuseLeftover refuses to start while a backup of an earlier check is there:
// that check was killed, and the tree may not be the one its owner left.
func refuseLeftover(dir string) error {
	root := filepath.Join(dir, agentdir.Name, revertDirName)
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			return &RestoreError{
				Dir:   filepath.Join(root, e.Name()),
				Cause: errors.New("a backup of an earlier revert-check is there: that check was killed, and the tree may not be the one it was started on. Look at the backup and put it back before checking again"),
			}
		}
	}
	return nil
}

// entry is a path the revert changes, as it was before.
type entry struct {
	Path   string      `json:"path"`
	State  string      `json:"state"`
	Mode   os.FileMode `json:"mode,omitempty"`
	MTime  time.Time   `json:"mtime,omitzero"`
	Target string      `json:"target,omitempty"`
	Copy   string      `json:"copy,omitempty"`

	ref *treeEntry // what the commit has at the path; nil when it has nothing
}

// manifest is what a backup says about itself.
type manifest struct {
	Since   string   `json:"since"`
	Tree    string   `json:"tree"`
	Entries []entry  `json:"entries"`
	Dirs    []string `json:"dirs,omitempty"`
}

// reverter sets paths back to a commit, and puts them back as they were.
type reverter struct {
	g       git
	dir     string
	commit  string
	table   project.Table
	timeout time.Duration
	paths   []string // the changed paths that are not tests

	entries   []entry
	made      []string // the directories the revert makes, shallowest first
	backup    string
	preTree   string
	indexPath string
	preIndex  []byte
}

// abs is the path of a slash-separated path of the tree.
func (rv *reverter) abs(p string) string {
	return filepath.Join(rv.dir, filepath.FromSlash(p))
}

// run reverts, runs the tests and puts everything back. The paths are put back
// whatever happens in between, and a failure to put them back replaces the
// result.
func (rv *reverter) run(ctx context.Context, res Result) (out Result, err error) {
	if err := rv.plan(ctx); err != nil {
		return Result{}, err
	}
	if len(rv.entries) == 0 {
		res.Outcome, res.Reason = OutcomeInconclusive, "nothing_to_revert"
		return res, nil
	}
	if err := rv.makeBackup(); err != nil {
		return Result{}, err
	}
	defer func() {
		if rerr := rv.restore(context.WithoutCancel(ctx)); rerr != nil {
			out, err = Result{}, rerr
		}
	}()

	if err := rv.apply(ctx); err != nil {
		return Result{}, err
	}
	reverted, err := checks.Run(ctx, rv.dir, checks.Options{Table: testChecks(rv.table), CheckTimeout: rv.timeout})
	if err != nil {
		return Result{}, err
	}
	return rv.conclude(res, reverted), nil
}

// conclude turns what the tests said on the reverted tree into the result.
func (rv *reverter) conclude(res Result, reverted checks.Result) Result {
	for _, e := range rv.entries {
		res.RevertedFiles = append(res.RevertedFiles, e.Path)
	}
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

// plan reads the state of the tree that the revert has to give back, and what
// the commit has at each path: nothing else is done yet.
func (rv *reverter) plan(ctx context.Context) error {
	var err error
	if rv.preTree, err = checks.TreeHash(ctx, rv.dir); err != nil {
		return err
	}
	if rv.indexPath, err = rv.g.indexPath(ctx); err != nil {
		return err
	}
	rv.preIndex = readIfThere(rv.indexPath)
	rv.backup = filepath.Join(rv.dir, agentdir.Name, revertDirName, rv.preTree[:12])

	atCommit, err := rv.g.entriesAt(ctx, rv.commit, rv.paths)
	if err != nil {
		return err
	}
	for _, p := range rv.paths {
		var ref *treeEntry
		if te, ok := atCommit[p]; ok {
			ref = &te
		}
		e, ok, err := rv.entryFor(p, ref)
		if err != nil {
			return err
		}
		if ok {
			rv.entries = append(rv.entries, e)
		}
	}
	rv.made = rv.dirsToMake()
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

// entryFor is how p is now, if it can be set back to the commit and put back:
// a file, a link, or nothing. A directory (a nested repository), anything else
// that is not a file, and a path where the commit has a directory or a
// submodule, are left as they are.
func (rv *reverter) entryFor(p string, ref *treeEntry) (entry, bool, error) {
	if ref != nil && ref.typ != "blob" {
		return entry{}, false, nil
	}
	info, err := os.Lstat(rv.abs(p))
	e := entry{Path: p, ref: ref}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if ref == nil {
			return entry{}, false, nil
		}
		e.State = stateAbsent
	case err != nil:
		return entry{}, false, err
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(rv.abs(p))
		if err != nil {
			return entry{}, false, err
		}
		e.State, e.Target = stateSymlink, target
	case info.Mode().IsRegular():
		e.State, e.Mode, e.MTime = stateFile, info.Mode().Perm(), info.ModTime()
	default:
		return entry{}, false, nil
	}
	return e, true, nil
}

// dirsToMake lists the directories that putting back the paths that are
// absent now creates, shallowest first, so that they can be removed again.
func (rv *reverter) dirsToMake() []string {
	missing := map[string]bool{}
	for _, e := range rv.entries {
		if e.State != stateAbsent || e.ref == nil {
			continue
		}
		for d := path.Dir(e.Path); d != "." && d != "/" && !missing[d]; d = path.Dir(d) {
			if _, err := os.Lstat(rv.abs(d)); err == nil {
				break
			}
			missing[d] = true
		}
	}
	made := slices.Collect(maps.Keys(missing))
	slices.SortFunc(made, func(a, b string) int {
		return cmp.Or(cmp.Compare(strings.Count(a, "/"), strings.Count(b, "/")), strings.Compare(a, b))
	})
	return made
}

// makeBackup copies every file the revert will change into the backup
// directory, with the index and a manifest, before anything is changed. A
// failure here has changed nothing, and takes the backup away again.
func (rv *reverter) makeBackup() (err error) {
	defer func() {
		if err != nil {
			_ = os.RemoveAll(rv.backup)
			_ = os.Remove(filepath.Dir(rv.backup))
		}
	}()
	if err := os.MkdirAll(filepath.Join(rv.backup, "files"), 0o755); err != nil {
		return err
	}
	for i := range rv.entries {
		e := &rv.entries[i]
		if e.State != stateFile {
			continue
		}
		e.Copy = path.Join("files", e.Path)
		if err := copyFile(rv.abs(e.Path), filepath.Join(rv.backup, filepath.FromSlash(e.Copy))); err != nil {
			return err
		}
	}
	if rv.preIndex != nil {
		if err := os.WriteFile(filepath.Join(rv.backup, "index"), rv.preIndex, 0o600); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(manifest{Since: rv.commit, Tree: rv.preTree, Entries: rv.entries, Dirs: rv.made}, "", "  ")
	if err != nil {
		return err
	}
	return agentdir.WriteFileAtomic(filepath.Join(rv.backup, "manifest.json"), append(data, '\n'))
}

// copyFile copies src to dst, making the directories of dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

// apply sets each path to what the commit has: its content and mode, or gone
// where the commit has nothing. The index is not touched.
func (rv *reverter) apply(ctx context.Context) error {
	for _, d := range rv.made {
		if err := os.Mkdir(rv.abs(d), 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
	}
	for _, e := range rv.entries {
		if e.ref == nil {
			if err := os.Remove(rv.abs(e.Path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			continue
		}
		content, err := rv.g.blob(ctx, e.ref.object)
		if err != nil {
			return err
		}
		if err := rv.put(e.Path, e.ref.mode, content); err != nil {
			return fmt.Errorf("%s: %w", e.Path, err)
		}
	}
	return nil
}

// put writes content to p with the git mode: a link, an executable file or a
// file. A file is replaced as a whole, so that a link in its place is replaced
// and not written through.
func (rv *reverter) put(p, mode string, content []byte) error {
	abs := rv.abs(p)
	if mode == "120000" {
		if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return os.Symlink(string(content), abs)
	}
	if err := agentdir.WriteFileAtomic(abs, content); err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if mode == "100755" {
		perm = 0o755
	}
	return os.Chmod(abs, perm)
}

// restore puts every path back as it was, from the backup, takes away the
// directories the revert made, and checks that the tree hash and the index
// are what they were. Only then is the backup removed: if anything fails it
// stays, with the manifest that says what is in it.
func (rv *reverter) restore(ctx context.Context) error {
	var problems []error
	for _, e := range rv.entries {
		if err := rv.putBack(e); err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", e.Path, err))
		}
	}
	for i := len(rv.made) - 1; i >= 0; i-- {
		_ = os.Remove(rv.abs(rv.made[i])) // only while it is empty
	}
	if len(problems) == 0 {
		if err := rv.verify(ctx); err != nil {
			problems = append(problems, err)
		}
	}
	if len(problems) > 0 {
		return &RestoreError{Dir: rv.backup, Cause: fmt.Errorf("the tree could not be put back: %w", errors.Join(problems...))}
	}
	if err := os.RemoveAll(rv.backup); err != nil {
		return &RestoreError{Dir: rv.backup, Cause: fmt.Errorf("the tree was put back, but its backup could not be removed: %w", err)}
	}
	_ = os.Remove(filepath.Dir(rv.backup)) // only while it is empty
	return nil
}

// putBack puts one path back as the manifest says it was.
func (rv *reverter) putBack(e entry) error {
	abs := rv.abs(e.Path)
	switch e.State {
	case stateAbsent:
		if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	case stateSymlink:
		if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return os.Symlink(e.Target, abs)
	}
	data, err := os.ReadFile(filepath.Join(rv.backup, filepath.FromSlash(e.Copy)))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	if err := agentdir.WriteFileAtomic(abs, data); err != nil {
		return err
	}
	if err := os.Chmod(abs, e.Mode); err != nil {
		return err
	}
	return os.Chtimes(abs, e.MTime, e.MTime)
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
