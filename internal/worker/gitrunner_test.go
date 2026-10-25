package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// gitCall is a git command the worker ran, with the environment it ran under.
type gitCall struct {
	argv, env []string
}

func (c gitCall) getenv(name string) (string, bool) {
	for _, kv := range slices.Backward(c.env) {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

// spyGit records the git commands of the attempt, and runs them as the worker would.
func spyGit(e *env) func() []gitCall {
	var mu sync.Mutex
	var calls []gitCall
	e.runner.gitWrap = func(env []string) gitx.Runner {
		run := gitx.ExplicitEnvRunner(env)
		return func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
			mu.Lock()
			calls = append(calls, gitCall{slices.Clone(argv), env})
			mu.Unlock()
			return run(ctx, dir, argv, stdin...)
		}
	}
	return func() []gitCall { mu.Lock(); defer mu.Unlock(); return slices.Clone(calls) }
}

// commitAndPush commits on the branch and pushes it with the git runner of the attempt.
func commitAndPush(t *testing.T, a *attempt, branch string) error {
	t.Helper()
	ctx := context.Background()
	g := gitx.New(a.cloneDir, a.git)
	if err := g.CreateBranch(ctx, branch); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, a.cloneDir, "src/greet.sh", "echo hello\n")
	if _, err := g.CommitAll(ctx, "fix: the greeting"); err != nil {
		t.Fatal(err)
	}
	return g.Push(ctx, branch, 1, nil)
}

// The PAT is a Basic header scoped to the clone URL in the environment of the clone and the push alone.
func TestGitRunner_GivesThePATOnlyToTheCommandsThatReachHub(t *testing.T) {
	needed := map[string]string{"HTTPS_PROXY": "http://proxy.example.com:3128", "NO_PROXY": "localhost", "SSL_CERT_FILE": "/etc/ssl/certs/ca.pem", "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8"}
	others := map[string]string{"DATABASE_URL": "postgres://user:password@db/x", "AWS_ACCESS_KEY_ID": "AKIAEXAMPLE", "GIT_SSH_COMMAND": "ssh -i key"}
	for name, value := range needed {
		t.Setenv(name, value)
	}
	for name, value := range others {
		t.Setenv(name, value)
	}
	e := cloneEnv(t, "main")
	calls := spyGit(e)

	a := e.through(t, "clone", e.fix())
	if err := commitAndPush(t, a, "fix/42-greeting"); err != nil {
		t.Fatal(err)
	}

	token := basicToken(pat)
	for _, r := range e.hub.GitRequests() {
		if r.Authorization != "Basic "+token || r.Status >= 400 {
			t.Errorf("%s %s: Authorization %q, status %d, want the Basic credential of agent-fox:<PAT>", r.Method, r.Path, r.Authorization, r.Status)
		}
	}
	if !strings.Contains(testutil.Git(t, e.hub.GitDir(slug), "branch", "--list", "fix/42-greeting"), "fix/42-greeting") {
		t.Error("the push did not reach the git server")
	}
	remote := 0
	for _, c := range calls() {
		if strings.Contains(strings.Join(c.argv, " "), pat) || strings.Contains(strings.Join(c.argv, " "), token) {
			t.Errorf("the PAT is on the command line %v", c.argv)
		}
		header, _ := c.getenv("GIT_CONFIG_VALUE_0")
		if c.argv[1] == "clone" || c.argv[1] == "push" {
			remote++
			count, _ := c.getenv("GIT_CONFIG_COUNT")
			key, _ := c.getenv("GIT_CONFIG_KEY_0")
			if count != "1" || key != "http."+a.record.CloneURL+".extraHeader" || header != "Authorization: Basic "+token {
				t.Errorf("%v: GIT_CONFIG_COUNT %q, KEY_0 %q, VALUE_0 %q, want the header scoped to the clone URL", c.argv[:2], count, key, header)
			}
		} else if strings.Contains(strings.Join(c.env, " "), token) || strings.Contains(strings.Join(c.env, " "), "GIT_CONFIG_COUNT") {
			t.Errorf("%v does not reach a remote and is given the header", c.argv)
		}
		for name, want := range needed {
			if got, _ := c.getenv(name); got != want {
				t.Errorf("%v: %s = %q, want %q: git needs it", c.argv[:2], name, got, want)
			}
		}
		for name := range others {
			if v, ok := c.getenv(name); ok {
				t.Errorf("%v: %s = %q is in the environment of git", c.argv[:2], name, v)
			}
		}
	}
	if remote != 2 {
		t.Errorf("%d commands reached hub, want the clone and the push", remote)
	}
	for path, content := range testutil.Files(t, a.cloneDir) {
		if strings.Contains(content, pat) || strings.Contains(content, token) || (path == ".git/config" && strings.Contains(content, "extraHeader")) {
			t.Errorf("%s holds the credential", path)
		}
	}

	// The home of git is outside the attempt directory, and goes with the attempt.
	home, _ := calls()[0].getenv("HOME")
	if info, err := os.Stat(home); err != nil || !info.IsDir() || strings.HasPrefix(home, e.dir) {
		t.Fatalf("the home of git %s (%v): want a directory outside the attempt directory", home, err)
	}
	a.close()
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Errorf("the home of git is still there: %v", err)
	}
}

// A credential helper and an askpass program of the operator, which the git of the machine asks, are never asked.
func TestGitRunner_NeverAsksTheMachinesGitConfiguration(t *testing.T) {
	home, marker := t.TempDir(), filepath.Join(t.TempDir(), "asked")
	helper := testutil.WriteFile(t, home, "helper.sh", "#!/bin/sh\ntouch '"+marker+"'\necho username=intruder\necho password=intruder\n")
	_ = os.Chmod(helper, 0o755)
	gitconfig := testutil.WriteFile(t, home, ".gitconfig", "[credential]\n\thelper = "+helper+"\n[core]\n\taskPass = "+helper+"\n[user]\n\tname = Operator\n\temail = operator@example.com\n")
	for name, value := range map[string]string{"HOME": home, "GIT_CONFIG_GLOBAL": gitconfig, "GIT_ASKPASS": helper, "SSH_ASKPASS": helper, "GIT_TERMINAL_PROMPT": "1"} {
		t.Setenv(name, value)
	}
	asked := func() bool { _, err := os.Stat(marker); return err == nil }
	e := cloneEnv(t, "main")
	calls := spyGit(e)

	e.hub.Inject("", gitPath, fakehub.Fault{Status: 401})
	if _, _, err := gitx.ExecRunner(context.Background(), t.TempDir(), []string{"git", "ls-remote", e.hub.CloneURL(slug)}); err != nil || !asked() {
		t.Fatalf("the git of the machine did not ask the helper (%v): the test cannot tell that the worker does not", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if res, err := e.run(e.fix()); err != nil || len(res.Envelope) == 0 || asked() {
		t.Fatalf("a clone refused with 401: %v, %s, asked the helper %v", err, res.Envelope, asked())
	}
	e.hub.Clear()
	a := e.through(t, "clone", e.fix())
	e.hub.Inject("", gitPath, fakehub.Fault{Status: 401})
	if err := commitAndPush(t, a, "fix/42-greeting"); err == nil || asked() {
		t.Errorf("a refused push: %v, asked the helper %v", err, asked())
	}
	if author := testutil.Git(t, a.cloneDir, "log", "-1", "--format=%an <%ae>"); strings.TrimSpace(author) != "agent-fox <agent-fox@noreply.invalid>" {
		t.Errorf("the commit was made by %q", author)
	}
	for _, c := range calls() {
		for name, want := range map[string]string{"GIT_TERMINAL_PROMPT": "0", "GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1"} {
			if got, _ := c.getenv(name); got != want {
				t.Errorf("%v: %s = %q, want %q", c.argv[:2], name, got, want)
			}
		}
		_, askpass := c.getenv("GIT_ASKPASS")
		if h, _ := c.getenv("HOME"); askpass || h == home || h == "" {
			t.Errorf("%v: GIT_ASKPASS set %v, HOME %q, want no askpass and a home of its own", c.argv[:2], askpass, h)
		}
	}
}

func TestReachesRemote(t *testing.T) {
	for argv, want := range map[string]bool{
		"git clone --quiet -- u clone": true, "git push -u origin b": true, "git fetch origin": true, "git pull origin main": true,
		"git ls-remote --heads origin b": true, "git -C dir push": true, "git -c a=b push": true, "git --no-pager push": true,
		"git commit -am x": false, "git config --local user.name x": false, "git remote get-url origin": false,
		"git": false, "push": false, "sh -c push": false, "git -C": false,
	} {
		if got := reachesRemote(strings.Fields(argv)); got != want {
			t.Errorf("reachesRemote(%q) = %v, want %v", argv, got, want)
		}
	}
}

// Hub is asked three times in all while it is unavailable, with growing waits; any other answer is final.
func TestPublishBranch_AsksHubAgainOnlyWhileItIsUnavailable(t *testing.T) {
	const branch = "fix/42-nil-map"
	route := "/api/v1/workspaces/acme/branches/" + branch + "/push"
	for name, tc := range map[string]struct {
		faults []fakehub.Fault
		calls  int
		waits  int
		ok     bool
	}{
		"unavailable twice, then published": {[]fakehub.Fault{{Status: 503, Once: true}, {Status: 503, Once: true}}, 3, 2, true},
		"unavailable throughout":            {[]fakehub.Fault{{Status: 503}}, 3, 2, false},
		"forbidden":                         {[]fakehub.Fault{{Status: 403}}, 1, 0, false},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, fakehub.Workspace{})
			testutil.Git(t, e.hub.GitDir(slug), "branch", branch, "main")
			for _, f := range tc.faults {
				e.hub.Inject("POST", route, f)
			}
			var waits []time.Duration
			e.runner.sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }

			err := e.runner.publishBranch(&attempt{req: e.fix(), pat: pat})(context.Background(), branch)

			if (err == nil) != tc.ok || len(e.hub.RequestsTo("POST", route)) != tc.calls || len(waits) != tc.waits {
				t.Errorf("error %v after %d calls and the waits %v, want success %v after %d calls and %d waits", err, len(e.hub.RequestsTo("POST", route)), waits, tc.ok, tc.calls, tc.waits)
			}
			if len(waits) == 2 && waits[0] >= waits[1] {
				t.Errorf("the waits %v do not grow", waits)
			}
		})
	}

	t.Run("the end of the attempt stops the tries", func(t *testing.T) {
		e := newEnv(t, fakehub.Workspace{})
		e.hub.Inject("POST", route, fakehub.Fault{Status: 503})
		ctx, cancel := context.WithCancelCause(context.Background())
		e.runner.sleep = func(ctx context.Context, _ time.Duration) error { cancel(jobs.ErrCancelled); return context.Cause(ctx) }

		err := e.runner.publishBranch(&attempt{req: e.fix(), pat: pat})(ctx, branch)

		if !errors.Is(err, jobs.ErrCancelled) || len(e.hub.RequestsTo("POST", route)) != 1 {
			t.Errorf("error %v after %d calls, want the cancel after the first", err, len(e.hub.RequestsTo("POST", route)))
		}
	})
}

func TestWait_EndsAfterTheDurationOrWithTheContext(t *testing.T) {
	r := New(Config{})
	if err := r.wait(context.Background(), time.Millisecond); err != nil {
		t.Errorf("a wait that ran out: %v", err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(jobs.ErrShutdown)
	if err := r.wait(ctx, time.Hour); !errors.Is(err, jobs.ErrShutdown) {
		t.Errorf("a wait whose context ended: %v, want its cause", err)
	}
}
