package fix

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// recordingRunner is the real runner that notes every git command it is given.
type recordingRunner struct {
	mu   sync.Mutex
	argv [][]string
}

func (r *recordingRunner) run(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
	r.mu.Lock()
	r.argv = append(r.argv, slices.Clone(argv))
	r.mu.Unlock()
	return gitx.ExecRunner(ctx, dir, argv, stdin...)
}

// mutating are the git commands that change the checkout.
func (r *recordingRunner) mutating() []string {
	var out []string
	for _, argv := range r.argv {
		if len(argv) > 1 && slices.Contains([]string{"reset", "checkout", "commit", "branch", "switch", "clean", "restore", "add", "stash", "merge", "rebase"}, argv[1]) {
			out = append(out, strings.Join(argv, " "))
		}
	}
	return out
}

// commitFile commits a file on the checked-out branch, as a brain does that works on the
// checkout itself.
func commitFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDo(t, dir, "add", name)
	gitDo(t, dir, "commit", "-q", "-m", "brain: "+name)
	return strings.TrimSpace(gitDo(t, dir, "rev-parse", "HEAD"))
}

type noted struct {
	warnings []toolio.Warning
	records  []string
}

func (n *noted) params(g *gitx.Git) ForeignParams {
	return ForeignParams{
		Git: g,
		Warn: func(code, message string) {
			n.warnings = append(n.warnings, toolio.Warning{Code: code, Message: message})
		},
		Record: func(kind string, data any) error { n.records = append(n.records, kind); return nil },
	}
}

// TestTheCheckoutIsRecordedWithAFullHashAndItsBranch_TS_10_34 verifies TS-10-34: the loop
// records HEAD as 40 hex digits, equal to git rev-parse HEAD, with the branch.
// Verifies: 10-REQ-6.3
func TestTheCheckoutIsRecordedWithAFullHashAndItsBranch_TS_10_34(t *testing.T) {
	r := newTestRepo(t, map[string]string{"a.txt": "a\n"}, false)

	got, err := RecordCheckout(context.Background(), r.Git)

	if err != nil {
		t.Fatal(err)
	}
	if want := strings.TrimSpace(gitDo(t, r.Dir, "rev-parse", "HEAD")); got.Head != want || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(got.Head) {
		t.Errorf("Head = %q, want the 40 hex digits of %s", got.Head, want)
	}
	if got.Branch != "main" {
		t.Errorf("Branch = %q, want main", got.Branch)
	}
	if _, err := RecordCheckout(context.Background(), gitx.New(t.TempDir(), gitx.ExecRunner)); err == nil {
		t.Error("a directory that is no checkout was recorded")
	}
}

// TestAnUnchangedCheckoutIsLeftAloneAndNothingIsMutated_TS_10_35 verifies TS-10-35: a
// brain that edits files without committing leaves the branch and HEAD as they were, and
// the check issues no command that changes git and raises no warning.
// Verifies: 10-REQ-6.4
func TestAnUnchangedCheckoutIsLeftAloneAndNothingIsMutated_TS_10_35(t *testing.T) {
	r := newTestRepo(t, map[string]string{"a.txt": "a\n"}, false)
	rec := &recordingRunner{}
	g := gitx.New(r.Dir, rec.run)
	before, err := RecordCheckout(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Dir, "a.txt"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec.argv = nil
	n := &noted{}

	reset, err := before.Check(context.Background(), n.params(g))

	if err != nil || reset != 0 {
		t.Fatalf("Check = %d, %v", reset, err)
	}
	if m := rec.mutating(); len(m) != 0 {
		t.Errorf("the check issued %v", m)
	}
	if len(n.warnings) != 0 || len(n.records) != 0 {
		t.Errorf("warnings %v, records %v, want none", n.warnings, n.records)
	}
}

// TestCommitsOnTheSameBranchAreFoldedIntoTheWorkingTree verifies the part of 10-REQ-6.5 that
// is the check's: the commits go from the branch, their change stays as the work of the
// checkout, the count is warned and noted.
func TestCommitsOnTheSameBranchAreFoldedIntoTheWorkingTree(t *testing.T) {
	r := newTestRepo(t, map[string]string{"a.txt": "a\n"}, false)
	before, _ := RecordCheckout(context.Background(), r.Git)
	commitFile(t, r.Dir, "b.txt", "b\n")
	second := commitFile(t, r.Dir, "c.txt", "c\n")
	n := &noted{}

	reset, err := before.Check(context.Background(), n.params(r.Git))

	if err != nil || reset != 2 {
		t.Fatalf("Check = %d, %v, want 2 commits reset", reset, err)
	}
	if head := strings.TrimSpace(gitDo(t, r.Dir, "rev-parse", "HEAD")); head != before.Head {
		t.Errorf("HEAD = %s, want the recorded %s", head, before.Head)
	}
	if out := gitDo(t, r.Dir, "branch", "--contains", second); strings.TrimSpace(out) != "" {
		t.Errorf("the foreign commit is still on %q", out)
	}
	if status := gitDo(t, r.Dir, "status", "--porcelain"); !strings.Contains(status, "b.txt") || !strings.Contains(status, "c.txt") {
		t.Errorf("status = %q, want the change of both commits in the tree", status)
	}
	if len(n.warnings) != 1 || n.warnings[0].Code != WarnForeignCommitsReset || !strings.Contains(n.warnings[0].Message, "2") || !slices.Equal(n.records, []string{WarnForeignCommitsReset}) {
		t.Errorf("warnings %v, records %v, want one foreign_commits_reset that counts 2", n.warnings, n.records)
	}
}

// TestACheckoutThatWasMovedElsewhereIsRefusedAndLeftAsItIs verifies the other half of
// 10-REQ-6.5 and 10-REQ-6.6 at the check: another branch, a detached HEAD and rewritten
// history are failed/git, not retryable, name the recorded and the current branch and
// HEAD, and change nothing.
func TestACheckoutThatWasMovedElsewhereIsRefusedAndLeftAsItIs(t *testing.T) {
	for name, move := range map[string]func(t *testing.T, dir string){
		"another branch": func(t *testing.T, dir string) {
			gitDo(t, dir, "checkout", "-q", "-b", "elsewhere")
			commitFile(t, dir, "x.txt", "x\n")
		},
		"a detached HEAD": func(t *testing.T, dir string) { gitDo(t, dir, "checkout", "-q", "--detach") },
		"rewritten history": func(t *testing.T, dir string) {
			gitDo(t, dir, "commit", "-q", "--amend", "--allow-empty", "-m", "rewritten")
		},
		"another branch with no new commit": func(t *testing.T, dir string) { gitDo(t, dir, "checkout", "-q", "-b", "elsewhere") },
	} {
		t.Run(name, func(t *testing.T) {
			r := newTestRepo(t, map[string]string{"a.txt": "a\n"}, false)
			before, _ := RecordCheckout(context.Background(), r.Git)
			move(t, r.Dir)
			headNow := strings.TrimSpace(gitDo(t, r.Dir, "rev-parse", "HEAD"))
			branchNow := strings.TrimSpace(gitDo(t, r.Dir, "rev-parse", "--abbrev-ref", "HEAD"))
			refs := gitDo(t, r.Dir, "for-each-ref")
			n := &noted{}

			reset, err := before.Check(context.Background(), n.params(r.Git))

			var moved *MovedError
			if !errors.As(err, &moved) || reset != 0 {
				t.Fatalf("Check = %d, %v, want a MovedError", reset, err)
			}
			f := moved.Failure
			if f.Category != toolio.CategoryGit || f.Retryable {
				t.Errorf("failure = %+v, want git and not retryable", f)
			}
			for _, want := range []string{"main", before.Head, branchNow, headNow} {
				if !strings.Contains(f.Message, want) {
					t.Errorf("message = %q, want it to name %s", f.Message, want)
				}
			}
			if got := strings.TrimSpace(gitDo(t, r.Dir, "rev-parse", "HEAD")); got != headNow || gitDo(t, r.Dir, "for-each-ref") != refs {
				t.Error("the check changed HEAD or the refs")
			}
			if len(n.warnings) != 0 || len(n.records) != 0 {
				t.Errorf("warnings %v, records %v, want none", n.warnings, n.records)
			}
		})
	}
}
