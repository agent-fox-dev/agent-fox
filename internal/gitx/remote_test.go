package gitx

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// clones is a bare origin whose main holds README.md, and two clones of it.
func clones(t *testing.T) (alice, bob *Git) {
	t.Helper()
	first, _ := newRepo(t)
	origin := t.TempDir()
	testutil.Git(t, origin, "clone", "-q", "--bare", first.Dir, ".")
	for _, g := range []**Git{&alice, &bob} {
		dir := t.TempDir()
		testutil.Git(t, dir, "clone", "-q", origin, ".")
		*g = New(dir, nil)
	}
	return alice, bob
}

// Pushing to and pulling from origin, and what is asked of it: whether it is
// there, its branches and its default branch.
func TestPushAndPullThroughOrigin(t *testing.T) {
	alice, bob := clones(t)
	ctx := t.Context()
	lonely, _ := newRepo(t)
	if !alice.HasRemote(ctx) || lonely.HasRemote(ctx) {
		t.Errorf("HasRemote is %v in a clone and %v in a repository with no origin", alice.HasRemote(ctx), lonely.HasRemote(ctx))
	}
	if err := alice.CreateBranch(ctx, "work"); err != nil {
		t.Fatal(err)
	}
	commit(t, alice, map[string]string{"file.txt": "hello\n"})
	if alice.RemoteBranchExists(ctx, "work") {
		t.Fatal("a branch that was never pushed is on origin")
	}

	if err := alice.Push(ctx, "work", 1, nil); err != nil {
		t.Fatal(err)
	}

	// origin's default branch wins over the branch that is checked out.
	if !alice.RemoteBranchExists(ctx, "work") || alice.DefaultBranch(ctx) != "main" {
		t.Errorf("pushed branch on origin %v, default branch %q; want true and main", alice.RemoteBranchExists(ctx, "work"), alice.DefaultBranch(ctx))
	}
	if err := bob.Pull(ctx, "work"); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if got := testutil.ReadFile(t, filepath.Join(bob.Dir, "file.txt")); got != "hello\n" {
		t.Errorf("after the pull file.txt = %q", got)
	}

	commit(t, alice, map[string]string{"file.txt": "alice\n"})
	if err := alice.Push(ctx, "work", 1, nil); err != nil {
		t.Fatal(err)
	}
	commit(t, bob, map[string]string{"file.txt": "bob\n"})
	err := bob.Pull(ctx, "work")
	if err == nil || !strings.Contains(err.Error(), "git pull origin work") {
		t.Errorf("a pull that conflicts = %v, want an error that names the command", err)
	}
}

// With no origin the default branch is the one checked out, and "main" when
// none is.
func TestDefaultBranchWithoutOrigin(t *testing.T) {
	g, dir := newRepo(t)
	testutil.Git(t, dir, "checkout", "-q", "-b", "trunk")
	if got := g.DefaultBranch(t.Context()); got != "trunk" {
		t.Errorf("DefaultBranch on trunk = %q", got)
	}
	testutil.Git(t, dir, "checkout", "-q", "--detach")
	if got := g.DefaultBranch(t.Context()); got != "main" {
		t.Errorf("DefaultBranch when detached = %q, want main", got)
	}
}

// A push is retried on the failures that pass, with a growing wait that is
// logged, and not on the ones another attempt cannot fix.
func TestPushRetriesTheTransientFailuresOnly(t *testing.T) {
	transient := "fatal: unable to access: Could not resolve proxy"
	broken := errors.New("git is not there")
	for _, tc := range []struct {
		name     string
		attempts int
		answers  []string // "" is a success; past the end the last repeats
		err      error
		cancel   bool
		calls    int
		waits    []time.Duration
		wantErr  string
	}{
		{"a success at the third attempt", 4, []string{transient, transient, ""}, nil, false, 3, []time.Duration{2 * time.Second, 4 * time.Second}, ""},
		{"every attempt fails", 3, []string{transient}, nil, false, 3, []time.Duration{2 * time.Second, 4 * time.Second}, "Could not resolve proxy"},
		{"a bad credential is not retried", 4, []string{"remote: Permission denied to user"}, nil, false, 1, nil, "Permission denied"},
		{"a missing repository is not retried", 4, []string{"ERROR: Repository not found."}, nil, false, 1, nil, "Repository not found"},
		{"fewer than one attempt is one", 0, []string{transient}, nil, false, 1, nil, "exited 128"},
		{"a git that cannot run is its error", 2, []string{transient}, broken, false, 2, []time.Duration{2 * time.Second}, "git is not there"},
		{"a cancelled context stops the retries", 4, []string{transient}, nil, true, 1, nil, "Could not resolve proxy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			if tc.cancel {
				cancel()
			}
			defer cancel()
			f := &fake{}
			f.answer = func([]string) (string, int, error) {
				out := tc.answers[min(f.calls, len(tc.answers))-1]
				if out == "" {
					return "", 0, nil
				}
				return out, 128, tc.err
			}
			g := New("/repo", f.run)
			var waits []time.Duration
			g.SetSleep(func(d time.Duration) { waits = append(waits, d) })
			var logged []string

			err := g.Push(ctx, "b", tc.attempts, func(s string) { logged = append(logged, s) })

			if (err == nil) != (tc.wantErr == "") || (err != nil && !strings.Contains(err.Error(), tc.wantErr)) {
				t.Errorf("Push = %v, want an error with %q", err, tc.wantErr)
			}
			if f.calls != tc.calls || len(waits) != len(tc.waits) || len(logged) != len(tc.waits) {
				t.Fatalf("%d attempts, waits %v, logged %q; want %d attempts and waits %v", f.calls, waits, logged, tc.calls, tc.waits)
			}
			for i, d := range tc.waits {
				if waits[i] != d || !strings.Contains(logged[i], d.String()) {
					t.Errorf("wait %d = %s logged as %q, want %s", i, waits[i], logged[i], d)
				}
			}
		})
	}
	// Without a log function the retries still happen.
	f := &fake{answer: func([]string) (string, int, error) { return "transient", 1, nil }}
	g := New("/repo", f.run)
	g.SetSleep(func(time.Duration) {})
	if err := g.Push(t.Context(), "b", 2, nil); err == nil || f.calls != 2 {
		t.Errorf("Push with no log = %v after %d attempts, want an error after 2", err, f.calls)
	}
}
