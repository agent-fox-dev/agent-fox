package gitx

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Git is a thin, explicit wrapper over the git commands the tools run.
type Git struct {
	Dir   string
	run   Runner
	sleep func(time.Duration) // injectable so a push-retry test does not wait
}

// New returns a Git rooted at dir. A nil runner means ExecRunner.
func New(dir string, r Runner) *Git {
	if r == nil {
		r = ExecRunner
	}
	return &Git{Dir: dir, run: r, sleep: time.Sleep}
}

// SetSleep replaces the backoff sleep. It exists for tests.
func (g *Git) SetSleep(f func(time.Duration)) { g.sleep = f }

func (g *Git) git(ctx context.Context, args ...string) (string, int, error) {
	out, code, err := g.run(ctx, g.Dir, append([]string{"git"}, args...))
	return strings.TrimRight(out, "\n"), code, err
}

// must is for commands whose failure is fatal to the step that called them.
func (g *Git) must(ctx context.Context, args ...string) (string, error) {
	out, code, err := g.git(ctx, args...)
	if err != nil {
		return out, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	if code != 0 {
		return out, fmt.Errorf("git %s exited %d: %s", strings.Join(args, " "), code, out)
	}
	return out, nil
}

// IsRepo reports whether Dir is inside a git work tree.
func (g *Git) IsRepo(ctx context.Context) bool {
	out, code, err := g.run(StdoutOnly(ctx), g.Dir, []string{"git", "rev-parse", "--is-inside-work-tree"})
	return err == nil && code == 0 && strings.TrimSpace(out) == "true"
}

// DirtyFiles returns the porcelain status lines. Empty means a clean tree.
func (g *Git) DirtyFiles(ctx context.Context) ([]string, error) {
	out, err := g.must(ctx, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(out), nil
}

// UntrackedFiles lists the files git does not track and does not ignore, one
// path per entry, relative to the repository root.
func (g *Git) UntrackedFiles(ctx context.Context) ([]string, error) {
	out, err := g.must(ctx, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(out), nil
}

// TopLevel is the top level of the work tree that Dir is in, as git prints it:
// an absolute path with the links of its own directories resolved.
func (g *Git) TopLevel(ctx context.Context) (string, error) {
	out, err := g.raw(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimRight(out, "\r\n"), nil
}

// Head is the short hash of the current commit.
func (g *Git) Head(ctx context.Context) (string, error) {
	return g.must(ctx, "rev-parse", "--short", "HEAD")
}

// CurrentBranch is the checked-out branch, or "HEAD" when detached.
func (g *Git) CurrentBranch(ctx context.Context) (string, error) {
	return g.must(ctx, "rev-parse", "--abbrev-ref", "HEAD")
}

// DefaultBranch is origin's default branch when the remote advertises one,
// else the branch that is checked out now, else "main". It is what --pull
// updates when no branch is named.
//
// Hardcoding "main" is wrong on every repository that still uses `master`, on
// a fork whose default is a release branch, and on any repository where the
// work lands on a long-lived integration branch.
func (g *Git) DefaultBranch(ctx context.Context) string {
	if out, code, err := g.git(ctx, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil && code == 0 {
		if b := strings.TrimPrefix(strings.TrimSpace(out), "origin/"); b != "" {
			return b
		}
	}
	if b, err := g.CurrentBranch(ctx); err == nil && b != "" && b != "HEAD" {
		return b
	}
	return "main"
}

// HasRemote reports whether an origin remote is configured.
func (g *Git) HasRemote(ctx context.Context) bool {
	out, code, err := g.git(ctx, "remote", "get-url", "origin")
	return err == nil && code == 0 && strings.TrimSpace(out) != ""
}

// LocalBranchExists reports whether refs/heads/name exists.
func (g *Git) LocalBranchExists(ctx context.Context, name string) bool {
	_, code, err := g.git(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil && code == 0
}

// RemoteBranchExists reports whether origin carries a branch of that name.
func (g *Git) RemoteBranchExists(ctx context.Context, name string) bool {
	out, code, err := g.git(ctx, "ls-remote", "--heads", "origin", name)
	return err == nil && code == 0 && strings.TrimSpace(out) != ""
}

// CreateBranch creates name from the current HEAD and checks it out.
func (g *Git) CreateBranch(ctx context.Context, name string) error {
	_, err := g.must(ctx, "checkout", "-b", name)
	return err
}

// CreateBranchFrom creates name at ref and checks it out, carrying the working tree
// along: a fix is made on the tree of the base, and its branch starts at the commit the
// base was at when the run began, whatever HEAD has moved to since.
func (g *Git) CreateBranchFrom(ctx context.Context, name, ref string) error {
	if strings.HasPrefix(name, "-") || strings.HasPrefix(ref, "-") {
		return fmt.Errorf("git checkout: %q from %q: a name or a ref may not start with a hyphen", name, ref)
	}
	_, err := g.must(ctx, "checkout", "-b", name, ref)
	return err
}

// SetBranchConfig sets a key of the configuration of the branch, in the configuration of the repository itself, whether the
// branch exists yet or not. Git removes the keys of a branch when the branch is deleted.
func (g *Git) SetBranchConfig(ctx context.Context, branch, key, value string) error {
	_, err := g.must(ctx, "config", "--local", "branch."+branch+"."+key, value)
	return err
}

// BranchConfig is the value of the key of the configuration of the branch, or "" when there is none.
func (g *Git) BranchConfig(ctx context.Context, branch, key string) string {
	out, _, _ := g.git(ctx, "config", "--local", "--get", "branch."+branch+"."+key)
	return out
}

// Checkout switches to an existing branch.
func (g *Git) Checkout(ctx context.Context, name string) error {
	_, err := g.must(ctx, "checkout", name)
	return err
}

// ChangedFiles lists the paths that differ from a commit, staged or not,
// including untracked files. It is how a pipeline verifies that an
// implementation phase actually changed something before it reports success.
func (g *Git) ChangedFiles(ctx context.Context, since string) ([]string, error) {
	tracked, err := g.must(ctx, "diff", "--name-only", since)
	if err != nil {
		return nil, err
	}
	untracked, err := g.must(ctx, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, block := range []string{tracked, untracked} {
		for _, line := range nonEmptyLines(block) {
			if !seen[line] {
				seen[line] = true
				out = append(out, line)
			}
		}
	}
	return out, nil
}

// CommitAll stages everything and commits it, returning the new short hash.
//
// The message is passed through stdin rather than as an argument so that a
// multi-paragraph body survives intact and nothing in it is interpreted as a
// flag.
func (g *Git) CommitAll(ctx context.Context, message string) (string, error) {
	return g.commitAll(ctx, message, false, nil)
}

// CommitAllNoVerify is CommitAll with the repository's hooks skipped.
//
// It exists for exactly one commit: the one that parks work the checks did
// not pass. A pre-commit hook that lints would refuse that commit by
// construction — the work is unverified, which is the point — and a park
// that cannot commit leaves the model's files loose on the wrong branch.
func (g *Git) CommitAllNoVerify(ctx context.Context, message string) (string, error) {
	return g.commitAll(ctx, message, true, nil)
}

// StageAllExcept stages every change in the tree — new, modified and
// deleted files — but the untracked paths in leave.
func (g *Git) StageAllExcept(ctx context.Context, leave []string) error {
	add := []string{"add", "-A"}
	if len(leave) > 0 {
		add = append(add, "--", ".")
		for _, p := range leave {
			add = append(add, ":(exclude,literal)"+p)
		}
	}
	_, err := g.must(ctx, add...)
	return err
}

func (g *Git) commitAll(ctx context.Context, message string, noVerify bool, leave []string) (string, error) {
	if err := g.StageAllExcept(ctx, leave); err != nil {
		return "", err
	}
	argv := []string{"git", "commit", "-F", "-"}
	if noVerify {
		argv = append(argv, "--no-verify")
	}
	out, code, err := g.run(ctx, g.Dir, argv, message)
	if err != nil {
		return "", fmt.Errorf("git commit: %w", err)
	}
	if code != 0 {
		return "", fmt.Errorf("git commit exited %d: %s", code, strings.TrimSpace(out))
	}
	return g.Head(ctx)
}

// Push publishes a branch, retrying the transient half of the failure space.
//
// The retry is bounded and reported rather than silent: a push that needed
// three attempts is a fact a run summary should carry, because it usually
// means the next person to run this will wait too.
func (g *Git) Push(ctx context.Context, branch string, attempts int, log func(string)) error {
	if attempts < 1 {
		attempts = 1
	}
	if log == nil {
		log = func(string) {}
	}
	delay := 2 * time.Second
	var last error
	for i := 1; i <= attempts; i++ {
		out, code, err := g.git(ctx, "push", "-u", "origin", branch)
		if err == nil && code == 0 {
			return nil
		}
		last = fmt.Errorf("git push exited %d: %s", code, strings.TrimSpace(out))
		if err != nil {
			last = err
		}
		if i == attempts || ctx.Err() != nil || nonRetryablePush(out) {
			break
		}
		log(fmt.Sprintf("push failed, retrying in %s (attempt %d/%d)", delay, i, attempts))
		g.sleep(delay)
		delay *= 2
	}
	return last
}

// nonRetryablePush recognizes the failures after which another attempt cannot
// succeed — a bad credential, a missing repository, a host that does not
// resolve. Retrying those only makes the operator wait through the backoff
// for the same answer.
func nonRetryablePush(out string) bool {
	l := strings.ToLower(out)
	for _, p := range []string{
		"authentication failed", "permission denied", "could not resolve host",
		"connection refused", "connection timed out", "repository not found",
		"no anonymous write access", "terminal prompts disabled", "could not read username",
		"does not appear to be a git repository",
	} {
		if strings.Contains(l, p) {
			return true
		}
	}
	return false
}

// Pull fetches from origin and merges changes into the current branch.
// An optional branch argument specifies which remote branch to pull from origin.
func (g *Git) Pull(ctx context.Context, branch ...string) error {
	args := []string{"pull", "origin"}
	if len(branch) > 0 && strings.TrimSpace(branch[0]) != "" {
		args = append(args, strings.TrimSpace(branch[0]))
	}
	_, err := g.must(ctx, args...)
	return err
}

// ResetHard discards everything back to a commit. It is how a failed attempt
// on a branch nobody else has written to is thrown away.
func (g *Git) ResetHard(ctx context.Context, ref string) error {
	_, err := g.must(ctx, "reset", "--hard", ref)
	return err
}

// Stash sets the changes of the work tree aside, the untracked files with them and the ignored ones
// not, and says whether there were any to set aside. Whatever it set aside is put back by StashPop.
func (g *Git) Stash(ctx context.Context) (bool, error) {
	before, _, _ := g.git(ctx, "rev-parse", "--quiet", "--verify", "refs/stash")
	if _, err := g.must(ctx, "stash", "push", "--include-untracked", "--quiet", "--message", "agent-fox: set aside"); err != nil {
		return false, err
	}
	after, _, _ := g.git(ctx, "rev-parse", "--quiet", "--verify", "refs/stash")
	return after != before, nil
}

// StashPop puts back the changes that the last Stash set aside, and drops them.
func (g *Git) StashPop(ctx context.Context) error {
	_, err := g.must(ctx, "stash", "pop", "--quiet")
	return err
}

// ResetSoft moves the branch back to a commit and keeps everything since
// as staged changes in the tree. It is how a commit made to hold work
// provisionally is undone before the work is committed for real.
func (g *Git) ResetSoft(ctx context.Context, ref string) error {
	_, err := g.must(ctx, "reset", "--soft", ref)
	return err
}

// DeleteBranch removes a local branch.
func (g *Git) DeleteBranch(ctx context.Context, name string) error {
	_, err := g.must(ctx, "branch", "-D", name)
	return err
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// Clean removes every untracked file and directory, honouring .gitignore.
//
// It is the second half of discarding an attempt: ResetHard restores the
// tracked files, and a new file the attempt created is untracked and would
// otherwise survive into the next attempt — and into its commit, since
// CommitAll stages everything.
func (g *Git) Clean(ctx context.Context) error {
	_, err := g.must(ctx, "clean", "-fdq")
	return err
}

// Restore returns one path — a file or a whole directory — to its state at
// HEAD: tracked files are checked out again and untracked ones under it are
// removed.
//
// It is how a pipeline takes back a directory it owns after a phase that had
// write access to the whole tree. The path is git's to interpret, relative to
// the repository root.
func (g *Git) Restore(ctx context.Context, path string) error {
	if _, err := g.must(ctx, "checkout", "--", path); err != nil {
		return err
	}
	_, err := g.must(ctx, "clean", "-fdq", "--", path)
	return err
}

// CheckoutPaths sets paths to their content at ref, leaving everything else
// in the tree as it is.
func (g *Git) CheckoutPaths(ctx context.Context, ref string, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	_, err := g.must(ctx, append([]string{"checkout", ref, "--"}, paths...)...)
	return err
}

// ShowFile is a file's content at ref, byte for byte, and whether it exists
// there.
func (g *Git) ShowFile(ctx context.Context, ref, path string) (string, bool, error) {
	_, code, err := g.git(ctx, "cat-file", "-e", ref+":"+path)
	if err != nil || code != 0 {
		return "", false, err
	}
	// Not g.git: that trims the trailing newline, and this is file content.
	out, code, err := g.run(StdoutOnly(ctx), g.Dir, []string{"git", "show", ref + ":" + path})
	if err != nil {
		return "", false, err
	}
	if code != 0 {
		return "", false, fmt.Errorf("git show %s:%s exited %d: %s", ref, path, code, out)
	}
	return out, true, nil
}

// Commit is one commit on a branch: its message and the files it changed.
type Commit struct {
	Hash    string
	Message string
	Files   []string
}

// CommitsSince lists the commits HEAD has after since, oldest first, each
// with its full message and the paths it changed. It is how a run that
// continues a branch reads what the runs before it recorded there.
func (g *Git) CommitsSince(ctx context.Context, since string) ([]Commit, error) {
	out, err := g.must(ctx, "rev-list", "--reverse", since+"..HEAD")
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, h := range nonEmptyLines(out) {
		msg, err := g.must(ctx, "log", "-1", "--format=%B", h)
		if err != nil {
			return nil, err
		}
		files, err := g.must(ctx, "diff-tree", "--no-commit-id", "--name-only", "-r", h)
		if err != nil {
			return nil, err
		}
		commits = append(commits, Commit{Hash: h, Message: msg, Files: nonEmptyLines(files)})
	}
	return commits, nil
}

// ListFiles lists every file git tracks plus the untracked files that are
// not ignored, read NUL-separated so that a name with a space, a newline or a
// non-ASCII character comes back byte for byte, unquoted. Nothing is trimmed.
// The paths are de-duplicated and sorted byte-wise, and relative to Dir.
func (g *Git) ListFiles(ctx context.Context) ([]string, error) {
	out, err := g.raw(ctx, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return sortedUnique(splitNUL(out)), nil
}

// TrackedIgnored lists the tracked files that an ignore rule matches, as
// `git status` shows them when they are clean: committed before the rule was
// written, or added with -f. They are NUL-separated, byte for byte, relative to
// Dir, de-duplicated and sorted.
func (g *Git) TrackedIgnored(ctx context.Context) ([]string, error) {
	out, err := g.raw(ctx, "ls-files", "-z", "--cached", "--ignored", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return sortedUnique(splitNUL(out)), nil
}

// Gitlinks lists the paths that are submodules: the entries of the index whose mode is
// 160000, NUL-separated, byte for byte, de-duplicated and sorted. What is under one is
// another repository's.
func (g *Git) Gitlinks(ctx context.Context) ([]string, error) {
	out, err := g.raw(ctx, "ls-files", "-z", "--stage")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, record := range splitNUL(out) {
		if meta, path, ok := strings.Cut(record, "\t"); ok && strings.HasPrefix(meta, "160000 ") {
			paths = append(paths, path)
		}
	}
	return sortedUnique(paths), nil
}

// ChangedPaths lists the paths that differ from since, staged or not, and the
// untracked files that are not ignored, NUL-separated and byte for byte,
// de-duplicated and sorted. A renamed file is both of its paths, so that a
// revert puts the old one back. Dir must be the top level of the work tree:
// the two lists git gives are relative to different roots otherwise.
func (g *Git) ChangedPaths(ctx context.Context, since string) ([]string, error) {
	if strings.HasPrefix(since, "-") {
		return nil, fmt.Errorf("git diff: %q is not a ref", since)
	}
	tracked, err := g.raw(ctx, "diff", "-z", "--name-only", "--no-renames", since, "--")
	if err != nil {
		return nil, err
	}
	untracked, err := g.raw(ctx, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return sortedUnique(append(splitNUL(tracked), splitNUL(untracked)...)), nil
}

// raw runs git and returns what it printed on stdout exactly as it came, where
// Git.git trims it and mixes in stderr. A non-zero exit is an error.
func (g *Git) raw(ctx context.Context, args ...string) (string, error) {
	out, code, err := g.run(StdoutOnly(ctx), g.Dir, append([]string{"git"}, args...))
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	if code != 0 {
		return "", fmt.Errorf("git %s exited %d: %s", strings.Join(args, " "), code, strings.TrimSpace(out))
	}
	return out, nil
}

// splitNUL splits the output of a -z listing into its entries.
func splitNUL(out string) []string {
	var entries []string
	for _, e := range strings.Split(out, "\x00") {
		if e != "" {
			entries = append(entries, e)
		}
	}
	return entries
}

// sortedUnique sorts paths byte-wise and drops the repeats.
func sortedUnique(paths []string) []string {
	slices.Sort(paths)
	return slices.Compact(paths)
}

// RevParse is the full 40-hex hash of the commit that ref names.
func (g *Git) RevParse(ctx context.Context, ref string) (string, error) {
	return g.must(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
}

// IsAncestor says whether the commit a is an ancestor of the commit b (a commit is its
// own ancestor).
func (g *Git) IsAncestor(ctx context.Context, a, b string) (bool, error) {
	out, code, err := g.git(ctx, "merge-base", "--is-ancestor", a, b)
	switch {
	case err != nil:
		return false, err
	case code == 0:
		return true, nil
	case code == 1:
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor exited %d: %s", code, strings.TrimSpace(out))
}

// Ignored is the paths among paths that git's ignore rules ignore. A tracked
// path is not ignored, whatever the rules say, since the rules do not apply to
// it. Paths are slash-separated and relative to the root of the work tree, and
// are given to git and read back NUL-separated, so that no name is quoted.
func (g *Git) Ignored(ctx context.Context, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out, code, err := g.run(StdoutOnly(ctx), g.Dir, []string{"git", "check-ignore", "-z", "--stdin"}, strings.Join(paths, "\x00")+"\x00")
	switch {
	case err != nil:
		return nil, fmt.Errorf("git check-ignore: %w", err)
	case code == 1:
		return nil, nil // none of them is ignored
	case code != 0:
		return nil, fmt.Errorf("git check-ignore exited %d: %s", code, strings.TrimSpace(out))
	}
	return splitNUL(out), nil
}
