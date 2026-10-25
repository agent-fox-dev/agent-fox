package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// cloneEnv is an environment on the base branch, whose git server names another branch as HEAD.
func cloneEnv(t *testing.T, base string) *env {
	t.Helper()
	e := newEnv(t, fakehub.Workspace{Branch: base})
	bare := e.hub.GitDir(slug)
	testutil.Git(t, bare, "branch", "elsewhere", base)
	testutil.Git(t, bare, "symbolic-ref", "HEAD", "refs/heads/elsewhere")
	return e
}

// stubGit makes every git command of the attempt answer as answer says, without running git.
func stubGit(e *env, answer func(ctx context.Context, argv []string) (string, int, error)) {
	e.runner.gitWrap = func([]string) gitx.Runner {
		return func(ctx context.Context, _ string, argv []string, _ ...string) (string, int, error) {
			return answer(ctx, argv)
		}
	}
}

func TestClone_ChecksOutTheBaseBranchWithTheIdentityInItsOwnConfiguration(t *testing.T) {
	e := cloneEnv(t, "develop")

	a := e.through(t, "clone", e.fix())

	clone := filepath.Join(e.dir, "clone")
	if a.cloneDir != clone {
		t.Fatalf("the clone is in %q, want %q", a.cloneDir, clone)
	}
	for args, want := range map[string]string{
		"rev-parse --abbrev-ref HEAD": "develop", "config --local user.name": "agent-fox",
		"config --local user.email": "agent-fox@noreply.invalid", "status --porcelain": "",
	} {
		if got := strings.TrimSpace(testutil.Git(t, clone, strings.Fields(args)...)); got != want {
			t.Errorf("git %s = %q, want %q", args, got, want)
		}
	}
	if got := testutil.ReadFile(t, filepath.Join(clone, "src", "greet.sh")); got != repoFiles["src/greet.sh"] {
		t.Errorf("src/greet.sh = %q", got)
	}
	if info, err := os.Stat(clone); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("clone/ = %v, %v, want mode 0700", info, err)
	}
}

// The message carries what git printed, cut short and without the token.
func TestClone_AnyOtherFailureIsATransientGitError(t *testing.T) {
	printsToken := "fatal: unable to access: Authorization: Basic " + basicToken(pat) + " (" + pat + ")"
	for name, tc := range map[string]struct {
		setup func(e *env)
		says  string
	}{
		"a 503":                 {func(e *env) { e.hub.Inject("", gitPath, fakehub.Fault{Status: 503}) }, "cannot be cloned"},
		"a 500":                 {func(e *env) { e.hub.Inject("", gitPath, fakehub.Fault{Status: 500}) }, "cannot be cloned"},
		"a hangup":              {func(e *env) { e.hub.Inject("", gitPath, fakehub.Fault{Hangup: true}) }, "cannot be cloned"},
		"a missing base branch": {func(e *env) { testutil.Git(t, e.hub.GitDir(slug), "branch", "-m", "main", "renamed") }, "cannot be cloned"},
		"a server that is slow": {func(e *env) {
			e.runner.cloneTimeout = 100 * time.Millisecond
			e.hub.Inject("", gitPath, fakehub.Fault{Delay: time.Minute})
		}, "cannot be cloned"},
		"git that cannot run": {func(e *env) {
			stubGit(e, func(context.Context, []string) (string, int, error) { return "", -1, errors.New("exec: no git") })
		}, "exec: no git"},
		"what git prints": {func(e *env) {
			stubGit(e, func(context.Context, []string) (string, int, error) { return printsToken, 128, nil })
		}, "***"},
		"a long answer of git": {func(e *env) {
			stubGit(e, func(context.Context, []string) (string, int, error) { return strings.Repeat("é", 400), 128, nil })
		}, "..."},
		"an identity that fails": {func(e *env) {
			stubGit(e, func(_ context.Context, argv []string) (string, int, error) {
				if argv[1] == "config" {
					return "error: could not lock config file", 255, nil
				}
				return "", 0, nil
			})
		}, "identity"},
	} {
		t.Run(name, func(t *testing.T) {
			e := cloneEnv(t, "main")
			tc.setup(e)

			res, err := e.run(e.fix())

			f := transient(err)
			if f == nil || f.Category != toolio.CategoryGit || !f.Retryable || len(res.Envelope) != 0 || !strings.Contains(f.Message, tc.says) {
				t.Fatalf("result %s, error %v, want a transient git error that says %q", res.Envelope, err, tc.says)
			}
			if strings.Contains(f.Message, pat) || strings.Contains(f.Message, basicToken(pat)) || len(f.Message) > 500 {
				t.Errorf("message = %q, want it short and without the token", f.Message)
			}
			if issue := e.issueNow(); len(issue.Labels) != 0 || len(issue.Comments) != 0 || len(e.hub.Leases()) != 0 {
				t.Errorf("issue %v with %d comments, %d leases, want in-progress taken off and nothing else", issue.Labels, len(issue.Comments), len(e.hub.Leases()))
			}
		})
	}
}
