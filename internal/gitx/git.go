package gitx

import (
	"context"
	"fmt"
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
	out, code, err := g.git(ctx, "rev-parse", "--is-inside-work-tree")
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

// Head is the short hash of the current commit.
func (g *Git) Head(ctx context.Context) (string, error) {
	return g.must(ctx, "rev-parse", "--short", "HEAD")
}

// CurrentBranch is the checked-out branch, or "HEAD" when detached.
func (g *Git) CurrentBranch(ctx context.Context) (string, error) {
	return g.must(ctx, "rev-parse", "--abbrev-ref", "HEAD")
}

// BaseBranch is what a pull request will target: origin's default branch when
// the remote advertises one, else the branch that is checked out now.
//
// Hardcoding "main" is wrong on every repository that still uses `master`, on
// a fork whose default is a release branch, and on any repository where the
// work lands on a long-lived integration branch.
//
// The "checked out now" fallback is only right BEFORE a feature branch is
// created, which is why a pipeline asks once in pre-flight and keeps the
// answer: asked afterwards it would name the feature branch, and a merge
// would then land the branch on itself.
func (g *Git) BaseBranch(ctx context.Context) string {
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

// DiffStat is the `--stat` summary of the change since a commit, for a run
// summary that says how big the change was without printing it.
func (g *Git) DiffStat(ctx context.Context, since string) (string, error) {
	return g.must(ctx, "diff", "--stat", since)
}

// CommitAll stages everything and commits it, returning the new short hash.
//
// The message is passed through stdin rather than as an argument so that a
// multi-paragraph body survives intact and nothing in it is interpreted as a
// flag.
func (g *Git) CommitAll(ctx context.Context, message string) (string, error) {
	return g.commitAll(ctx, message, false)
}

// CommitAllNoVerify is CommitAll with the repository's hooks skipped.
//
// It exists for exactly one commit: the one that parks work the checks did
// not pass. A pre-commit hook that lints would refuse that commit by
// construction — the work is unverified, which is the point — and a park
// that cannot commit leaves the model's files loose on the wrong branch.
func (g *Git) CommitAllNoVerify(ctx context.Context, message string) (string, error) {
	return g.commitAll(ctx, message, true)
}

func (g *Git) commitAll(ctx context.Context, message string, noVerify bool) (string, error) {
	if _, err := g.must(ctx, "add", "-A"); err != nil {
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

// HeadMessage is the full message of the current commit. A pipeline reads it
// to recognize a commit it made itself — a parked attempt it may discard —
// from a commit a person made, which it may not.
func (g *Git) HeadMessage(ctx context.Context) (string, error) {
	return g.must(ctx, "log", "-1", "--format=%B")
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
