package worker

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// gitCall is a git command the worker ran, with the environment it ran under.
type gitCall struct {
	dir  string
	argv []string
	env  []string
}

// gitSpy records the git commands of an attempt and runs them as the worker would.
type gitSpy struct {
	mu    sync.Mutex
	calls []gitCall
}

func (s *gitSpy) wrap(env []string) gitx.Runner {
	run := gitx.ExplicitEnvRunner(env)
	return func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		s.mu.Lock()
		s.calls = append(s.calls, gitCall{dir: dir, argv: slices.Clone(argv), env: env})
		s.mu.Unlock()
		return run(ctx, dir, argv, stdin...)
	}
}

// with returns the calls whose git command is sub.
func (s *gitSpy) with(sub string) []gitCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []gitCall
	for _, c := range s.calls {
		if len(c.argv) > 1 && c.argv[1] == sub {
			out = append(out, c)
		}
	}
	return out
}

func (c gitCall) getenv(name string) (string, bool) {
	for _, kv := range slices.Backward(c.env) {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

// repoFiles are the files of the repository of the workspace of the clone tests.
var repoFiles = map[string]string{"README.md": "hello\n", "src/greet.sh": "echo helo\n"}

// cloneEnv is an environment whose workspace is on a repository with the base branch given, and
// whose hub also has another branch that its git server's HEAD names, so that a clone that does
// not ask for the base branch does not get it.
func cloneEnv(t *testing.T, base string) *env {
	t.Helper()
	e := newEnv(t, fakehub.Workspace{Branch: base, Files: repoFiles})
	bare := e.hub.GitDir(slug)
	gitIn(t, bare, "branch", "elsewhere", base)
	gitIn(t, bare, "symbolic-ref", "HEAD", "refs/heads/elsewhere")
	return e
}

// gitIn runs git in dir without the configuration of the machine, and returns its output.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, errb.String())
	}
	return strings.TrimSpace(out.String())
}

// through runs the steps of an attempt up to and including the one named, and returns the
// attempt, which is closed when the test ends.
func (e *env) through(t *testing.T, last string, req jobs.Request) *attempt {
	t.Helper()
	a := &attempt{req: req, report: e.sink, started: time.Now()}
	t.Cleanup(a.close)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, s := range e.runner.stages() {
		if out := s.run(ctx, a); out != nil {
			t.Fatalf("the step %s ended the attempt: %+v, %v", s.name, out.result, out.err)
		}
		if s.name == last {
			return a
		}
	}
	t.Fatalf("there is no step %s", last)
	return nil
}

func (e *env) fixRequest() jobs.Request {
	return e.request(jobs.KindFix, jobs.Input{Text: "The greeting says helo."})
}

// TestTheCloneChecksOutTheBaseBranchAndSetsTheIdentityLocally_TS_13_34 verifies TS-13-34: the
// clone is on the base branch of the workspace and not on the branch the server names, in a
// clone/ that is private, with the committer identity in its local configuration only.
// Verifies: 13-REQ-5.2
func TestTheCloneChecksOutTheBaseBranchAndSetsTheIdentityLocally_TS_13_34(t *testing.T) {
	e := cloneEnv(t, "develop")

	a := e.through(t, "clone", e.fixRequest())

	clone := filepath.Join(e.dir, "clone")
	if a.cloneDir != clone {
		t.Fatalf("the clone is in %q, want %q", a.cloneDir, clone)
	}
	if got := gitIn(t, clone, "rev-parse", "--abbrev-ref", "HEAD"); got != "develop" {
		t.Errorf("the clone is on %q, want the base branch develop", got)
	}
	if got := gitIn(t, clone, "config", "--local", "user.name"); got != "agent-fox" {
		t.Errorf("local user.name = %q", got)
	}
	if got := gitIn(t, clone, "config", "--local", "user.email"); got != "agent-fox@noreply.invalid" {
		t.Errorf("local user.email = %q", got)
	}
	if got, err := os.ReadFile(filepath.Join(clone, "src", "greet.sh")); err != nil || string(got) != repoFiles["src/greet.sh"] {
		t.Errorf("the files of the repository are not in the clone: %q, %v", got, err)
	}
	if info, err := os.Stat(clone); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("clone/ = %v, %v, want mode 0700", info, err)
	}
	if status := gitIn(t, clone, "status", "--porcelain"); status != "" {
		t.Errorf("the clone is not clean: %q", status)
	}
}

// fixPush makes a commit on a branch in the clone of a and pushes it, through the git runner of
// the attempt, as the fix pipeline does.
func fixPush(t *testing.T, a *attempt, branch string) {
	t.Helper()
	ctx := context.Background()
	g := gitx.New(a.cloneDir, a.git)
	if err := g.CreateBranch(ctx, branch); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.cloneDir, "src", "greet.sh"), []byte("echo hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CommitAll(ctx, "fix: the greeting"); err != nil {
		t.Fatal(err)
	}
	if err := g.Push(ctx, branch, 1, nil); err != nil {
		t.Fatalf("the push: %v", err)
	}
}

// TestThePATReachesGitOnlyAsAScopedBasicHeaderThroughTheEnvironment_TS_13_35 verifies TS-13-35:
// the git server receives Authorization: Basic base64(agent-fox:<PAT>) for the clone and the
// push; no argv of any git command, no file under clone/ and, of the environments, only the ones
// of the commands that reach hub hold the PAT, as GIT_CONFIG_COUNT, KEY and VALUE for the
// header scoped to the clone URL.
// Verifies: 13-REQ-5.3
func TestThePATReachesGitOnlyAsAScopedBasicHeaderThroughTheEnvironment_TS_13_35(t *testing.T) {
	e := cloneEnv(t, "main")
	spy := &gitSpy{}
	e.runner.gitWrap = spy.wrap

	a := e.through(t, "clone", e.fixRequest())
	fixPush(t, a, "fix/42-greeting")

	token := basicToken(pat)
	header := "Authorization: Basic " + token
	requests := e.hub.GitRequests()
	if len(requests) < 4 {
		t.Fatalf("the git server saw %d requests, want those of a clone and a push", len(requests))
	}
	for _, r := range requests {
		if r.Authorization != "Basic "+token || r.Status >= 400 {
			t.Errorf("request %s %s: Authorization %q, status %d, want the Basic header of agent-fox:<PAT> and success", r.Method, r.Path, r.Authorization, r.Status)
		}
	}
	if !strings.Contains(gitIn(t, e.hub.GitDir(slug), "branch", "--list", "fix/42-greeting"), "fix/42-greeting") {
		t.Errorf("the git push did not reach the server's repository")
	}

	for _, c := range spy.calls {
		for _, arg := range c.argv {
			if strings.Contains(arg, pat) || strings.Contains(arg, token) {
				t.Errorf("the PAT is on the command line %v", c.argv)
			}
		}
	}
	remote := append(spy.with("clone"), spy.with("push")...)
	if len(remote) != 2 {
		t.Fatalf("%d git commands reach hub, want the clone and the push", len(remote))
	}
	for _, c := range remote {
		if n, _ := c.getenv("GIT_CONFIG_COUNT"); n != "1" {
			t.Errorf("%v: GIT_CONFIG_COUNT = %q, want 1", c.argv[:2], n)
		}
		if k, _ := c.getenv("GIT_CONFIG_KEY_0"); k != "http."+a.record.CloneURL+".extraHeader" {
			t.Errorf("%v: GIT_CONFIG_KEY_0 = %q, want the header scoped to the clone URL %s", c.argv[:2], k, a.record.CloneURL)
		}
		if v, _ := c.getenv("GIT_CONFIG_VALUE_0"); v != header {
			t.Errorf("%v: GIT_CONFIG_VALUE_0 = %q, want %q", c.argv[:2], v, header)
		}
	}
	for _, c := range spy.calls {
		if sub := c.argv[1]; sub == "clone" || sub == "push" {
			continue
		}
		for _, kv := range c.env {
			if strings.Contains(kv, pat) || strings.Contains(kv, token) || strings.HasPrefix(kv, "GIT_CONFIG_COUNT=") || strings.HasPrefix(kv, "GIT_CONFIG_KEY_") || strings.HasPrefix(kv, "GIT_CONFIG_VALUE_") {
				t.Errorf("%v does not reach a remote and has %q in its environment", c.argv, kv)
			}
		}
	}

	err := filepath.WalkDir(a.cloneDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if bytes.Contains(data, []byte(pat)) || bytes.Contains(data, []byte(token)) {
			t.Errorf("%s holds the PAT", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg, _ := os.ReadFile(filepath.Join(a.cloneDir, ".git", "config")); strings.Contains(string(cfg), "extraHeader") || strings.Contains(string(cfg), "Authorization") {
		t.Errorf(".git/config = %s: the header is not configuration of the clone", cfg)
	}
}

// TestACloneCommandHasAnEnvironmentWithoutTheMachinesGitConfiguration_TS_13_36 verifies TS-13-36:
// a credential helper and an askpass program of the operator's global git configuration, with
// prompts enabled in the environment, are never invoked by the clone, the git commands of the
// pipeline or the push, even when the server asks for a credential, while the default runner
// invokes them in the same situation; and the commands run in a clean environment. The runners of
// the box's sync and of checks.FileView are not the worker's.
// Verifies: 13-REQ-5.4
func TestACloneCommandHasAnEnvironmentWithoutTheMachinesGitConfiguration_TS_13_36(t *testing.T) {
	home := t.TempDir()
	marker := filepath.Join(t.TempDir(), "marker")
	helper := filepath.Join(home, "helper.sh")
	script := "#!/bin/sh\ntouch '" + marker + "'\necho username=intruder\necho password=intruder\n"
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[credential]\n\thelper = "+helper+"\n[core]\n\taskPass = "+helper+"\n[user]\n\tname = Operator\n\temail = operator@example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	t.Setenv("GIT_ASKPASS", helper)
	t.Setenv("SSH_ASKPASS", helper)
	t.Setenv("GIT_TERMINAL_PROMPT", "1")
	absent := func(what string) {
		t.Helper()
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("%s: the credential helper or askpass of the operator was invoked", what)
		}
	}

	e := cloneEnv(t, "main")
	spy := &gitSpy{}
	e.runner.gitWrap = spy.wrap

	// The control: with a server that asks for a credential, the default runner does invoke them.
	e.hub.Inject("", "/git/acme.git", fakehub.Fault{Status: 401})
	if _, _, err := gitx.ExecRunner(context.Background(), t.TempDir(), []string{"git", "ls-remote", e.hub.CloneURL(slug)}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the control did not invoke the helper: the test cannot tell it is not invoked (%v)", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	// A server that refuses the credential: the clone does not fall back on the operator's.
	res, err := e.run(e.fixRequest())
	if err != nil || len(res.Envelope) == 0 {
		t.Fatalf("a clone refused with 401: %v, %q, want an envelope", err, res.Envelope)
	}
	absent("a clone that is refused")
	e.hub.Clear()

	// A clone, the commands of the pipeline and a push that the server then refuses.
	a := e.through(t, "clone", e.fixRequest())
	absent("the clone")
	g := gitx.New(a.cloneDir, a.git)
	if err := g.CreateBranch(context.Background(), "fix/42-greeting"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.cloneDir, "src", "greet.sh"), []byte("echo hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CommitAll(context.Background(), "fix: the greeting"); err != nil {
		t.Fatal(err)
	}
	if author := gitIn(t, a.cloneDir, "log", "-1", "--format=%an <%ae>"); author != "agent-fox <agent-fox@noreply.invalid>" {
		t.Errorf("the commit was made by %q: the identity of the machine's configuration leaked in", author)
	}
	e.hub.Inject("", "/git/acme.git", fakehub.Fault{Status: 401})
	if err := g.Push(context.Background(), "fix/42-greeting", 1, nil); err == nil {
		t.Fatal("a push that the server refuses succeeded")
	}
	absent("the push")

	for _, c := range spy.calls {
		want := map[string]string{"GIT_TERMINAL_PROMPT": "0", "GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1"}
		for name, value := range want {
			if got, ok := c.getenv(name); !ok || got != value {
				t.Errorf("%v: %s = %q, want %q", c.argv, name, got, value)
			}
		}
		for _, name := range []string{"GIT_ASKPASS", "SSH_ASKPASS"} {
			if v, ok := c.getenv(name); ok {
				t.Errorf("%v: %s = %q, want none", c.argv, name, v)
			}
		}
		if h, _ := c.getenv("HOME"); h == home || h == "" {
			t.Errorf("%v: HOME = %q, want a directory of its own", c.argv, h)
		}
	}

	// checks.FileView is not run by the worker's runner.
	before := len(spy.calls)
	if _, err := checks.FileView(context.Background(), a.cloneDir); err != nil {
		t.Fatal(err)
	}
	if len(spy.calls) != before {
		t.Errorf("checks.FileView ran %d commands through the runner of the attempt", len(spy.calls)-before)
	}
}

// TestTheDirectoryOfGitIsGivenBackWhenTheAttemptEnds shows that the clean home of the git
// commands is made for the attempt and removed with it.
// Verifies: 13-REQ-5.4
func TestTheDirectoryOfGitIsGivenBackWhenTheAttemptEnds(t *testing.T) {
	e := cloneEnv(t, "main")
	spy := &gitSpy{}
	e.runner.gitWrap = spy.wrap
	a := e.through(t, "clone", e.fixRequest())
	clones := spy.with("clone")
	if len(clones) != 1 {
		t.Fatalf("%d clones were made, want 1", len(clones))
	}
	home, _ := clones[0].getenv("HOME")
	if info, err := os.Stat(home); err != nil || !info.IsDir() {
		t.Fatalf("the home of git while the attempt runs: %v", err)
	}
	if strings.HasPrefix(home, e.dir) {
		t.Errorf("the home of git %s is in the attempt directory: the worker makes no entry there but clone/ and input/", home)
	}

	a.close()

	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Errorf("the home of git is still there after the attempt: %v", err)
	}
}

// TestACloneRefusedWith401Or403EndsAsAnAuthFailureEnvelope_TS_13_37 verifies TS-13-37: the
// attempt ends with a failed envelope of category auth that is not retryable, and not with an
// error; the mirror of the failure to the issue is the part of the attempt that follows.
// Verifies: 13-REQ-5.5
func TestACloneRefusedWith401Or403EndsAsAnAuthFailureEnvelope_TS_13_37(t *testing.T) {
	for _, status := range []int{401, 403} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			e := cloneEnv(t, "main")
			e.hub.Inject("", "/git/acme.git", fakehub.Fault{Status: status})

			res, err := e.run(e.fixRequest())

			if err != nil {
				t.Fatalf("error = %v, want an envelope", err)
			}
			env := envelope(t, res)
			category, message, retryable, _ := errorOf(env)
			if env["status"] != "failed" || category != toolio.CategoryAuth || retryable {
				t.Errorf("envelope status %v, error %q %q retryable %v, want failed, auth, not retryable", env["status"], category, message, retryable)
			}
			if !strings.Contains(message, "PUT /api/v1/workspaces/acme") || strings.Contains(message, pat) {
				t.Errorf("message = %q, want the way to replace the token, and not the token", message)
			}
			if res.MaxAttempts == 0 {
				t.Error("the result does not carry the most attempts of the round")
			}
		})
	}
}

// TestAnyOtherCloneFailureIsATransientGitErrorWithNoEnvelope_TS_13_38 verifies TS-13-38: a
// server that answers 5xx, hangs up, does not answer in time or does not have the base branch is
// the infrastructure: a transient error of category git that can be retried, and no envelope. An
// attempt that is cancelled while it clones is its cancellation.
// Verifies: 13-REQ-5.6
func TestAnyOtherCloneFailureIsATransientGitErrorWithNoEnvelope_TS_13_38(t *testing.T) {
	for name, tc := range map[string]struct {
		fault   *fakehub.Fault
		timeout time.Duration
		rename  bool
	}{
		"a 503":                   {fault: &fakehub.Fault{Status: 503}},
		"a 500":                   {fault: &fakehub.Fault{Status: 500}},
		"a hangup":                {fault: &fakehub.Fault{Hangup: true}},
		"a timeout":               {fault: &fakehub.Fault{Delay: 10 * time.Second}, timeout: 300 * time.Millisecond},
		"a base branch not there": {rename: true},
	} {
		t.Run(name, func(t *testing.T) {
			e := cloneEnv(t, "main")
			if tc.rename {
				gitIn(t, e.hub.GitDir(slug), "branch", "-m", "main", "renamed")
			}
			if tc.fault != nil {
				e.hub.Inject("", "/git/acme.git", *tc.fault)
			}
			e.runner.cloneTimeout = tc.timeout
			start := time.Now()
			res, err := e.run(e.fixRequest())

			f := transient(err)
			if f == nil || f.Category != toolio.CategoryGit || !f.Retryable {
				t.Fatalf("error = %v, want a transient error carrying a failure of category git that is retryable", err)
			}
			if len(res.Envelope) != 0 {
				t.Errorf("the result holds an envelope: %s", res.Envelope)
			}
			if strings.Contains(f.Message, pat) || strings.Contains(f.Message, basicToken(pat)) {
				t.Errorf("the message holds the PAT: %q", f.Message)
			}
			if tc.timeout > 0 && time.Since(start) > 5*time.Second {
				t.Errorf("the clone took %v to give up, want it cut at %v", time.Since(start), tc.timeout)
			}
		})
	}

	t.Run("an attempt that is cancelled while it clones is a result that says so", func(t *testing.T) {
		e := cloneEnv(t, "main")
		e.hub.Inject("", "/git/acme.git", fakehub.Fault{Delay: 30 * time.Second})
		ctx, cancel := context.WithCancelCause(context.Background())
		time.AfterFunc(500*time.Millisecond, func() { cancel(jobs.ErrCancelled) })

		res, err := e.runner.Run(ctx, e.fixRequest())

		category, _, retryable, _ := errorOf(envelope(t, res))
		if err != nil || category != "cancelled" || retryable {
			t.Errorf("result %q, error %v, want an envelope of category cancelled and no error", res.Envelope, err)
		}
	})

	t.Run("an attempt that is shut down while it clones is its cause", func(t *testing.T) {
		e := cloneEnv(t, "main")
		e.hub.Inject("", "/git/acme.git", fakehub.Fault{Delay: 30 * time.Second})
		ctx, cancel := context.WithCancelCause(context.Background())
		time.AfterFunc(500*time.Millisecond, func() { cancel(jobs.ErrShutdown) })

		res, err := e.runner.Run(ctx, e.fixRequest())

		if !errors.Is(err, jobs.ErrShutdown) || jobs.IsTransient(err) || len(res.Envelope) != 0 {
			t.Errorf("result %q, error %v, want the shutdown and no envelope", res.Envelope, err)
		}
	})

	t.Run("what git prints is shown without the token", func(t *testing.T) {
		e := cloneEnv(t, "main")
		e.runner.gitWrap = func([]string) gitx.Runner {
			return func(context.Context, string, []string, ...string) (string, int, error) {
				return "fatal: unable to access: Authorization: Basic " + basicToken(pat) + " (" + pat + ")", 128, nil
			}
		}

		_, err := e.run(e.fixRequest())

		f := transient(err)
		if f == nil || strings.Contains(f.Message, pat) || strings.Contains(f.Message, basicToken(pat)) || !strings.Contains(f.Message, "***") {
			t.Errorf("failure = %+v, want the message with the token hidden", f)
		}
	})
}

// TestAKeptCloneOfAnEarlierDeliveryIsRemovedAndClonedAfresh_TS_13_40 verifies TS-13-40: the
// marker file and the stale commit of a clone/ that an earlier delivery kept are gone, and
// clone/ is a fresh clone of the base branch.
// Verifies: 13-REQ-5.8
func TestAKeptCloneOfAnEarlierDeliveryIsRemovedAndClonedAfresh_TS_13_40(t *testing.T) {
	e := cloneEnv(t, "main")
	clone := filepath.Join(e.dir, "clone")
	if err := os.Mkdir(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, clone, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(clone, "marker.txt"), []byte("an earlier delivery"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, clone, "add", "-A")
	gitIn(t, clone, "-c", "user.name=old", "-c", "user.email=old@example.com", "commit", "-q", "-m", "stale commit")

	e.through(t, "clone", e.fixRequest())

	if _, err := os.Stat(filepath.Join(clone, "marker.txt")); !os.IsNotExist(err) {
		t.Errorf("the marker file of the earlier delivery is still there: %v", err)
	}
	if log := gitIn(t, clone, "log", "--format=%s"); log != "chore: initial commit" {
		t.Errorf("the history of the clone = %q, want the history of the base branch only", log)
	}
	if head, want := gitIn(t, clone, "rev-parse", "HEAD"), gitIn(t, e.hub.GitDir(slug), "rev-parse", "main"); head != want {
		t.Errorf("HEAD = %s, want %s of the server", head, want)
	}
	if got := gitIn(t, clone, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Errorf("the clone is on %q", got)
	}
}

// reports is the reporter of the fix pipeline in a test: it keeps what it is told.
type reports struct {
	warnings []string
	next     []toolio.Next
	effects  []toolio.SideEffect
}

func (r *reports) Warn(code, _ string)            { r.warnings = append(r.warnings, code) }
func (r *reports) Artifact(toolio.Artifact)       {}
func (r *reports) SideEffect(s toolio.SideEffect) { r.effects = append(r.effects, s) }
func (r *reports) Next(n toolio.Next)             { r.next = append(r.next, n) }

// land lands the change of the clone of a through the fix pipeline, with the runner and the
// post-push hook of the worker, and a hub that publishes the branch.
func (e *env) land(t *testing.T, a *attempt) (fix.LandResult, *reports, error) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(a.cloneDir, "src", "greet.sh"), []byte("echo hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := gitx.New(a.cloneDir, a.git)
	g.SetSleep(func(time.Duration) {})
	head, err := g.RevParse(context.Background(), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	rep := &reports{}
	res, err := fix.Land(context.Background(), fix.LandParams{
		Git:   g,
		State: fix.State{RunID: "20261020_100000_aaaaaa", Base: "main", BaseHead: head, Issue: &fix.StateIssue{URL: issuex.IssueRef{Repo: a.repo, Number: 42}.URL(), Title: "Nil map", Number: 42}},
		Input: fix.Input{Kind: "issue", Body: "It panics."},
		Report: fix.Report{Type: "fix", Subject: "Nil map", Change: "x",
			Files:    []fix.File{{Path: "src/greet.sh", Why: "the word"}},
			Criteria: []fix.Criterion{{Criterion: "it says hello", Status: "met", Evidence: "the test"}}},
		Mode: fix.LandPR, Forge: a.forge, Target: a.repo, Reporter: rep,
		Measured: fix.Measurement{Verdict: checks.VerdictPass, Landable: true, Lands: true, Closes: true, Changed: []string{"src/greet.sh"}},
		PostPush: e.runner.publishBranch(a),
	})
	return res, rep, err
}

// TestTheBranchIsPublishedThroughHubAfterTheGitPushAndBeforeThePullRequest_TS_13_63 verifies
// TS-13-63: hub is asked to push the branch after the git push and before the pull request, is
// asked again while it is unavailable, three times in all, and a final failure ends the landing
// failed in category git, not retryable, with no pull request and the branch named in next.
// Verifies: 13-REQ-8.6
func TestTheBranchIsPublishedThroughHubAfterTheGitPushAndBeforeThePullRequest_TS_13_63(t *testing.T) {
	branch := "fix/42-nil-map"
	route := "/api/v1/workspaces/acme/branches/" + branch + "/push"
	pulls := "/api/v1/workspaces/acme/pulls"

	t.Run("hub is unavailable twice and then publishes", func(t *testing.T) {
		e := cloneEnv(t, "main")
		var slept []time.Duration
		e.runner.sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
		e.hub.Inject("POST", route, fakehub.Fault{Status: 503, Once: true})
		e.hub.Inject("POST", route, fakehub.Fault{Status: 503, Once: true})
		a := e.through(t, "clone", e.fixRequest())

		res, rep, err := e.land(t, a)

		if err != nil || res.Branch != branch || !res.Pushed || res.PullRequest == nil {
			t.Fatalf("landing = %+v, %v (%v), want the branch %s pushed and a pull request", res, err, rep.warnings, branch)
		}
		tries := e.hub.RequestsTo("POST", route)
		if len(tries) != 3 {
			t.Fatalf("hub was asked to push the branch %d times, want 3", len(tries))
		}
		var gitPush int
		for _, r := range e.hub.GitRequests() {
			if r.Method == "POST" && strings.HasSuffix(r.Path, "/git-receive-pack") {
				gitPush = r.Seq
			}
		}
		prs := e.hub.RequestsTo("POST", pulls)
		if len(prs) != 1 || gitPush == 0 || !(gitPush < tries[0].Seq && tries[2].Seq < prs[0].Seq) {
			t.Errorf("order: git push %d, branch push route %d..%d, pull request %v; want the git push, then the route, then the pull request", gitPush, tries[0].Seq, tries[2].Seq, prs)
		}
		if len(slept) != 2 || slept[0] >= slept[1] {
			t.Errorf("waited %v between the tries, want two growing waits", slept)
		}
		var pushed []string
		for _, p := range e.hub.BranchPushes() {
			if p.Status == 200 {
				pushed = append(pushed, p.Branch)
			}
		}
		if !slices.Equal(pushed, []string{branch}) {
			t.Errorf("hub pushed %v to the forge, want [%s]", pushed, branch)
		}
	})

	t.Run("hub stays unavailable", func(t *testing.T) {
		e := cloneEnv(t, "main")
		e.runner.sleep = func(context.Context, time.Duration) error { return nil }
		e.hub.Inject("POST", route, fakehub.Fault{Status: 503})
		a := e.through(t, "clone", e.fixRequest())

		_, rep, err := e.land(t, a)

		var f *toolio.Failure
		if !errors.As(err, &f) || f.Category != toolio.CategoryGit || f.Retryable {
			t.Fatalf("error = %v, want a failure of category git that is not retryable", err)
		}
		if n := len(e.hub.RequestsTo("POST", route)); n != 3 {
			t.Errorf("hub was asked %d times, want 3 in all", n)
		}
		if n := len(e.hub.RequestsTo("POST", pulls)); n != 0 {
			t.Errorf("%d pull requests were asked for after the branch was not published", n)
		}
		var named bool
		for _, n := range rep.next {
			named = named || slices.Contains(n.Argv, branch)
		}
		if !named || !strings.Contains(f.Message, branch) {
			t.Errorf("next = %v, message = %q, want both to name %s", rep.next, f.Message, branch)
		}
		if !strings.Contains(gitIn(t, e.hub.GitDir(slug), "branch", "--list", branch), branch) {
			t.Error("the branch is not kept on hub's git server")
		}
	})

	t.Run("an answer that is final is not asked again", func(t *testing.T) {
		e := cloneEnv(t, "main")
		e.runner.sleep = func(context.Context, time.Duration) error { t.Error("waited for an answer that is final"); return nil }
		e.hub.Inject("POST", route, fakehub.Fault{Status: 403})
		a := e.through(t, "clone", e.fixRequest())

		_, _, err := e.land(t, a)

		var f *toolio.Failure
		if !errors.As(err, &f) || f.Category != toolio.CategoryGit || f.Retryable {
			t.Fatalf("error = %v, want a failure of category git that is not retryable", err)
		}
		if n := len(e.hub.RequestsTo("POST", route)); n != 1 {
			t.Errorf("hub was asked %d times after it forbade the call, want 1", n)
		}
	})

	t.Run("the attempt ending stops the tries", func(t *testing.T) {
		e := cloneEnv(t, "main")
		ctx, cancel := context.WithCancelCause(context.Background())
		e.runner.sleep = func(ctx context.Context, _ time.Duration) error { cancel(jobs.ErrCancelled); return context.Cause(ctx) }
		e.hub.Inject("POST", route, fakehub.Fault{Status: 503})
		a := e.through(t, "clone", e.fixRequest())

		err := e.runner.publishBranch(a)(ctx, branch)

		if !errors.Is(err, jobs.ErrCancelled) || len(e.hub.RequestsTo("POST", route)) != 1 {
			t.Errorf("error = %v after %d calls, want the cancellation after the first", err, len(e.hub.RequestsTo("POST", route)))
		}
	})
}

// TestOnlyTheCommandsThatReachARemoteAreGivenTheToken pins which git commands carry the PAT.
func TestOnlyTheCommandsThatReachARemoteAreGivenTheToken(t *testing.T) {
	for argv, want := range map[string]bool{
		"git clone --quiet -- u clone": true, "git push -u origin b": true, "git fetch origin": true, "git pull origin main": true,
		"git ls-remote --heads origin b": true, "git -C dir push": true, "git -c a=b push": true, "git --no-pager push": true,
		"git commit -am x": false, "git checkout -b b": false, "git config --local user.name x": false, "git remote get-url origin": false,
		"git status --porcelain": false, "git": false, "push": false, "sh -c push": false, "git -C": false,
	} {
		if got := reachesRemote(strings.Fields(argv)); got != want {
			t.Errorf("reachesRemote(%q) = %v, want %v", argv, got, want)
		}
	}
}

// TestGitIsGivenOnlyWhatItNeedsOfTheEnvironmentOfTheProcess pins that the environment of a git
// command is built from a short list of what git needs of the process (where to find programs, the
// locale, the proxies and the certificates) and not from all of it less what looks like a secret: a
// setting that no list of names would call one, and the hooks of a repository, which run under it,
// do not see it.
// Verifies: 13-REQ-5.4, 13-REQ-10.5
func TestGitIsGivenOnlyWhatItNeedsOfTheEnvironmentOfTheProcess(t *testing.T) {
	for name, value := range map[string]string{
		"DATABASE_URL": "postgres://user:password@db.example.com/agentfox", "AWS_ACCESS_KEY_ID": "AKIAEXAMPLE", "SERVICE_DSN": "dsn-value",
		"SOMETHING_ELSE": "x", "GIT_SSH_COMMAND": "ssh -i /home/operator/key",
		"HTTPS_PROXY": "http://proxy.example.com:3128", "NO_PROXY": "localhost", "SSL_CERT_FILE": "/etc/ssl/certs/ca.pem", "LANG": "C.UTF-8",
		"LC_ALL": "C.UTF-8", "LC_MESSAGES": "C",
	} {
		t.Setenv(name, value)
	}
	e := cloneEnv(t, "main")
	spy := &gitSpy{}
	e.runner.gitWrap = spy.wrap

	a := e.through(t, "clone", e.fixRequest())
	fixPush(t, a, "fix/42-greeting")

	if len(spy.calls) == 0 {
		t.Fatal("no git command ran")
	}
	for _, c := range spy.calls {
		for _, name := range []string{"DATABASE_URL", "AWS_ACCESS_KEY_ID", "SERVICE_DSN", "SOMETHING_ELSE", "GIT_SSH_COMMAND"} {
			if v, ok := c.getenv(name); ok {
				t.Errorf("%v: %s = %q is in the environment of git", c.argv, name, v)
			}
		}
		for name, want := range map[string]string{"HTTPS_PROXY": "http://proxy.example.com:3128", "NO_PROXY": "localhost", "SSL_CERT_FILE": "/etc/ssl/certs/ca.pem", "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8", "LC_MESSAGES": "C"} {
			if v, _ := c.getenv(name); v != want {
				t.Errorf("%v: %s = %q, want %q: git needs it", c.argv, name, v, want)
			}
		}
		if v, _ := c.getenv("PATH"); v == "" {
			t.Errorf("%v: no PATH", c.argv)
		}
	}
}
