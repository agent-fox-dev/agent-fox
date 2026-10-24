package afcmd

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/sandbox"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// A fixture gate: a Makefile whose check target runs every test under tests/
// against a script that is not a test.
var fixFiles = map[string]string{
	"Makefile":            "check:\n\tsh tests/run.sh\n",
	"tests/run.sh":        "set -e\nfor t in tests/*_test.sh; do sh \"$t\"; done\n",
	"tests/greet_test.sh": "[ \"$(sh src/greet.sh)\" = hello ] || { echo \"FAIL: greet printed $(sh src/greet.sh)\"; exit 1; }\n",
	"src/greet.sh":        "echo hello\n",
}

// fixEnv is a checkout with a gate and a local bare origin, a fake forge with an
// issue 7 in it, and the shell wired to the forge.
type fixEnv struct {
	t      *testing.T
	dir    string
	origin string
	forge  *fakeforge.Forge
	app    *toolio.App
}

// requireMake skips the test where the fixture gate cannot run.
func requireMake(t *testing.T) {
	t.Helper()
	requireSh(t)
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}
}

// hermeticGit makes git ignore the configuration of the machine for the rest of the test,
// and the environment hold no token and no key.
func hermeticGit(t *testing.T) {
	t.Helper()
	requireGit(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "GITHUB_API_URL", "GITLAB_API_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "AF_MODEL"} {
		t.Setenv(name, "")
	}
}

// fixRepo makes a repository of files on main, with an identity of its own, and a
// bare origin that main is pushed to when withOrigin is set.
func fixRepo(t *testing.T, files map[string]string, withOrigin bool) (dir, origin string) {
	t.Helper()
	hermeticGit(t)
	dir = repoWith(t, files)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.com"}, {"config", "commit.gpgsign", "false"},
		{"add", "-A"}, {"commit", "-q", "--allow-empty", "-m", "chore: initial commit"},
	} {
		gitDo(t, dir, args...)
	}
	if withOrigin {
		origin = filepath.Join(t.TempDir(), "origin.git")
		gitDo(t, t.TempDir(), "init", "-q", "--bare", "-b", "main", origin)
		gitDo(t, dir, "remote", "add", "origin", origin)
		gitDo(t, dir, "push", "-q", "-u", "origin", "main")
	}
	return dir, origin
}

// newFixEnv makes the environment of a forge of the kind, over a repository with
// the fixture gate and an origin.
func newFixEnv(t *testing.T, kind string) *fixEnv {
	t.Helper()
	return newFixEnvWith(t, kind, fixFiles, true)
}

func newFixEnvWith(t *testing.T, kind string, files map[string]string, withOrigin bool) *fixEnv {
	t.Helper()
	dir, origin := fixRepo(t, files, withOrigin)
	e := &fixEnv{t: t, dir: dir, origin: origin, forge: fakeforge.New(t, kind, "o/r")}
	e.forge.AddIssue(fakeforge.Issue{Number: 7, Title: "Greeting is wrong", Body: "The greeting should say hello."})
	e.app = newApp()
	e.app.NewForge = e.forge.NewForge("test-token")
	return e
}

func (e *fixEnv) run(stdin string, args ...string) (int, map[string]any) {
	e.t.Helper()
	var out, errb bytes.Buffer
	code := e.app.Main(context.Background(), append([]string{"af"}, args...), strings.NewReader(stdin), &out, &errb)
	return code, oneDocument(e.t, out.String())
}

// fix runs af fix on the input, in the checkout, in the local box: the tests that
// are about another provider say so after it, and the last --sandbox is the one that counts.
func (e *fixEnv) fix(input string, args ...string) (int, map[string]any) {
	e.t.Helper()
	return e.run("", append([]string{"fix", input, "--dir", e.dir, "--sandbox", "local"}, args...)...)
}

// branches are the local branches of the checkout.
func (e *fixEnv) branches() []string {
	e.t.Helper()
	return strings.Fields(gitDo(e.t, e.dir, "for-each-ref", "--format=%(refname:short)", "refs/heads"))
}

// useFixSpy makes the brain "spy" the one the fix commands find in the registry,
// beside the brains of the release.
func useFixSpy(t *testing.T, s *spyBrain) {
	t.Helper()
	old := testConfig.Brains
	testConfig.Brains = spyRegistry(t, s)
	t.Cleanup(func() { testConfig.Brains = old })
}

// countBoxes registers a box provider that is the local one and counts the boxes
// it creates.
func countBoxes(t *testing.T) *atomic.Int32 {
	t.Helper()
	local, err := box.Default.Lookup("local")
	if err != nil {
		t.Fatal(err)
	}
	var created atomic.Int32
	entry := local
	entry.Name = "countbox"
	entry.New = func() sandbox.Provider { return countingProvider{Provider: local.New(), created: &created} }
	box.Default.Register(entry)
	return &created
}

type countingProvider struct {
	sandbox.Provider
	created *atomic.Int32
}

func (c countingProvider) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	c.created.Add(1)
	return c.Provider.Create(ctx, spec)
}

// TestFlagsOutOfRangeAreRefusedBeforeAnythingIsAsked_TS_09_4 verifies TS-09-4:
// --attempts outside 1 to 5 and a --land that is not one of three are usage
// failures that the shell finds before it reads the issue, so the forge sees no
// request and the brain never runs.
// Verifies: 09-REQ-1.4
func TestFlagsOutOfRangeAreRefusedBeforeAnythingIsAsked_TS_09_4(t *testing.T) {
	requireMake(t)
	e := newFixEnv(t, fakeforge.GitHub)
	spy := &spyBrain{}
	useFixSpy(t, spy)

	for _, flags := range [][]string{{"--attempts", "0"}, {"--attempts", "6"}, {"--attempts", "x"}, {"--land", "bogus"}} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			code, env := e.fix(e.forge.IssueURL(7), append(flags, "--brain", "spy")...)

			errObj, _ := env["error"].(map[string]any)
			if code != 2 || env["status"] != "usage" || errObj["category"] != "usage" {
				t.Errorf("exit code %d, status %v, error %v, want usage", code, env["status"], errObj)
			}
			if n := len(e.forge.Requests()); n != 0 {
				t.Errorf("%d requests to the forge: %v", n, e.forge.Requests())
			}
			if spy.runs != 0 || spy.news != 0 {
				t.Errorf("the brain was made %d times and ran %d times", spy.news, spy.runs)
			}
		})
	}
	for _, ok := range [][]string{{"--attempts", "1"}, {"--attempts", "5"}, {"--land", "pr"}, {"--land", "branch"}, {"--land", "none"}} {
		if code, env := e.fix(e.forge.IssueURL(7), append(ok, "--brain", "spy", "--dry-run")...); code == 2 {
			t.Errorf("%v: exit code %d, error %v, want the flag accepted", ok, code, env["error"])
		}
	}
}

// pullURL is the URL of a pull request of the fake forge, and mergeURL the one of a
// merge request.
func (e *fixEnv) pullURL() string {
	return strings.Replace(e.forge.IssueURL(7), "/issues/", "/pull/", 1)
}

func (e *fixEnv) mergeURL() string {
	return strings.Replace(e.forge.IssueURL(7), "/issues/", "/merge_requests/", 1) // the URL of a GitLab issue already has its "/-"
}

// TestAPullOrMergeRequestIsNoInput_TS_09_5 verifies TS-09-5: a GitHub pull
// request and a GitLab merge request are refused as usage, with no model call and
// no write to the forge.
// Verifies: 09-REQ-1.5
func TestAPullOrMergeRequestIsNoInput_TS_09_5(t *testing.T) {
	requireMake(t)
	for kind, urlOf := range map[string]func(*fixEnv) string{fakeforge.GitHub: (*fixEnv).pullURL, fakeforge.GitLab: (*fixEnv).mergeURL} {
		t.Run(kind, func(t *testing.T) {
			e := newFixEnv(t, kind)
			spy := &spyBrain{}
			useFixSpy(t, spy)

			code, env := e.fix(urlOf(e), "--brain", "spy")

			errObj, _ := env["error"].(map[string]any)
			if code != 2 || env["status"] != "usage" || !strings.Contains(errObj["message"].(string), "pull or merge request") {
				t.Errorf("exit code %d, status %v, error %v, want usage that says fix takes an issue", code, env["status"], errObj)
			}
			if n := len(e.forge.Writes()); n != 0 || spy.news != 0 {
				t.Errorf("%d writes to the forge, the brain made %d times", n, spy.news)
			}
		})
	}
}

// refusedBy asserts a failed envelope of the category with the exit code, and
// returns its error object.
func refusedBy(t *testing.T, code int, env map[string]any, wantCode int, category string) map[string]any {
	t.Helper()
	errObj, _ := env["error"].(map[string]any)
	if code != wantCode || errObj["category"] != category {
		t.Fatalf("exit code %d, status %v, error %v; want exit code %d and category %s", code, env["status"], errObj, wantCode, category)
	}
	return errObj
}

// TestPreflightChecksRunInOrderAndAFailureCostsNothing_TS_09_9 verifies TS-09-9:
// of several things wrong with the repository the earliest check is the one that
// is reported, and a run that the checks of the host refuse makes no brain, no box, no
// branch and no write to the forge. (The gate is the repository's, and is checked in the
// box: see TestARunWithNoGateIsRefusedInTheBoxAndRunsNoBrain.)
// Verifies: 09-REQ-2.1
func TestPreflightChecksRunInOrderAndAFailureCostsNothing_TS_09_9(t *testing.T) {
	requireMake(t)
	spy := &spyBrain{}
	useFixSpy(t, spy)
	boxes := countBoxes(t)

	for name, tc := range map[string]struct {
		files      map[string]string
		withOrigin bool
		prepare    func(e *fixEnv)
		args       []string
		code       int
		category   string
		mention    string
		// input is the report, and brain the brain: the issue 7 and the spy when they are empty.
		input func(e *fixEnv) string
		brain string
	}{
		"dirty, no gate, no origin: the tree": {map[string]string{"README.md": "x\n"}, false, func(e *fixEnv) {
			e.writeFile("dirty.txt", "x")
		}, []string{"--land", "branch"}, 1, "preflight", "dirty.txt", nil, ""},
		"detached and dirty: the branch": {fixFiles, true, func(e *fixEnv) {
			gitDo(e.t, e.dir, "checkout", "-q", "--detach")
			e.writeFile("dirty.txt", "x")
		}, nil, 2, "usage", "detached", nil, ""},
		"no gate and no origin: the origin": {map[string]string{"README.md": "x\n"}, false, nil, []string{"--land", "branch"}, 1, "preflight", "origin", nil, ""},
		"no origin: the origin":             {fixFiles, false, nil, []string{"--land", "branch"}, 1, "preflight", "origin", nil, ""},
		// The landing is checked before the brain is made, and the parts of the landing in order.
		"no origin and a brain with no credential: the origin": {fixFiles, false, nil, []string{"--land", "pr"}, 1, "preflight", "origin", nil, "agentkit"},
		"no token and a brain with no credential: the token": {fixFiles, true, func(e *fixEnv) { e.app.NewForge = e.forge.NewForge("") }, []string{"--land", "pr"},
			1, "auth", "GITHUB_TOKEN", nil, "agentkit"},
		"a text, no repository and no token: the repository": {fixFiles, true, func(e *fixEnv) { e.app.NewForge = e.forge.NewForge("") }, []string{"--land", "pr"},
			2, "usage", "--repo", func(*fixEnv) string { return "the greeting says helo" }, ""},
	} {
		t.Run(name, func(t *testing.T) {
			e := newFixEnvWith(t, fakeforge.GitHub, tc.files, tc.withOrigin)
			if tc.prepare != nil {
				tc.prepare(e)
			}
			before, head := e.branches(), strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "HEAD"))
			input, brain := e.forge.IssueURL(7), cmp.Or(tc.brain, "spy")
			if tc.input != nil {
				input = tc.input(e)
			}

			code, env := e.fix(input, append(tc.args, "--brain", brain, "--sandbox", "countbox")...)

			errObj := refusedBy(t, code, env, tc.code, tc.category)
			if !strings.Contains(errObj["message"].(string), tc.mention) {
				t.Errorf("message = %q, want it to mention %q", errObj["message"], tc.mention)
			}
			if spy.news != 0 || spy.runs != 0 || boxes.Load() != 0 {
				t.Errorf("the brain was made %d times, ran %d times, and %d boxes were opened", spy.news, spy.runs, boxes.Load())
			}
			if !slices.Equal(e.branches(), before) || strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "HEAD")) != head {
				t.Errorf("branches %v, were %v, or HEAD moved: a refused run makes no branch and no commit", e.branches(), before)
			}
			if n := len(e.forge.Writes()); n != 0 {
				t.Errorf("%d writes to the forge", n)
			}
		})
	}
}

// TestARunWithNoGateIsRefusedInTheBoxAndRunsNoBrain guards the last check of the preflight, which
// is made where the checks of the repository run: a repository with no gate is refused as
// preflight, with the hint --no-verify, once its box is open and before the brain runs, and
// makes no branch and no write to the forge.
func TestARunWithNoGateIsRefusedInTheBoxAndRunsNoBrain(t *testing.T) {
	spy := &spyBrain{}
	useFixSpy(t, spy)
	boxes := countBoxes(t)
	e := newFixEnvWith(t, fakeforge.GitHub, map[string]string{"README.md": "x\n"}, true)
	before := e.branches()

	code, env := e.fix(e.forge.IssueURL(7), "--land", "none", "--brain", "spy", "--sandbox", "countbox")

	errObj := refusedBy(t, code, env, 1, "preflight")
	if hint, _ := errObj["fix_hint"].(map[string]any); hint["flag"] != "--no-verify" || !strings.Contains(errObj["message"].(string), "gate") {
		t.Errorf("error = %v, want one that names the gate and the hint --no-verify", errObj)
	}
	if spy.runs != 0 || boxes.Load() != 1 {
		t.Errorf("the brain ran %d times and %d boxes were opened, want no run in the one box", spy.runs, boxes.Load())
	}
	if !slices.Equal(e.branches(), before) || len(e.forge.Writes()) != 0 {
		t.Errorf("branches %v, were %v, and %d writes: a refused run makes neither", e.branches(), before, len(e.forge.Writes()))
	}
}

// TestADirOrBaseThatIsNotRightIsUsage_TS_09_10 verifies TS-09-10.
// Verifies: 09-REQ-2.2
func TestADirOrBaseThatIsNotRightIsUsage_TS_09_10(t *testing.T) {
	requireMake(t)
	e := newFixEnv(t, fakeforge.GitHub)
	sub := filepath.Join(e.dir, "src")

	code, env := e.run("", "fix", e.forge.IssueURL(7), "--dir", sub, "--land", "none", "--sandbox", "local")
	refusedBy(t, code, env, 2, "usage")

	code, env = e.run("", "fix", e.forge.IssueURL(7), "--dir", t.TempDir(), "--land", "none", "--sandbox", "local")
	refusedBy(t, code, env, 2, "usage")

	code, env = e.fix(e.forge.IssueURL(7), "--base", "other", "--land", "none")
	if msg := refusedBy(t, code, env, 2, "usage")["message"].(string); !strings.Contains(msg, "check out other first") {
		t.Errorf("message = %q, want 'check out other first'", msg)
	}

	if code, env := e.fix(e.forge.IssueURL(7), "--base", "main", "--land", "none", "--dry-run"); code == 2 {
		t.Errorf("--base main on main: exit code %d, error %v, want it accepted", code, env["error"])
	}

	gitDo(t, e.dir, "checkout", "-q", "--detach")
	code, env = e.fix(e.forge.IssueURL(7), "--land", "none")
	refusedBy(t, code, env, 2, "usage")
}

// TestADirtyTreeFailsPreflightAndNamesTwentyPaths_TS_09_11 verifies TS-09-11.
// Verifies: 09-REQ-2.3
func TestADirtyTreeFailsPreflightAndNamesTwentyPaths_TS_09_11(t *testing.T) {
	requireMake(t)
	e := newFixEnv(t, fakeforge.GitHub)
	for i := 1; i <= 25; i++ {
		e.writeFile(fmt.Sprintf("f%02d.txt", i), "x")
	}
	e.writeFile("src/greet.sh", "echo changed\n")

	code, env := e.fix(e.forge.IssueURL(7), "--land", "none")

	msg := refusedBy(t, code, env, 1, "preflight")["message"].(string)
	// 26 paths are dirty: the change to the script is listed first, and the first 20 are named.
	n := len(regexp.MustCompile(`f\d\d\.txt`).FindAllString(msg, -1))
	if !strings.Contains(msg, "src/greet.sh") || n != 19 || strings.Contains(msg, "f20.txt") || !strings.Contains(msg, "and 6 more") {
		t.Errorf("%d of the new files are named, want 19 and the script (20 paths) and 'and 6 more': %s", n, msg)
	}
}

// TestNoGateFailsPreflightUnlessNoVerify_TS_09_12 verifies TS-09-12.
// Verifies: 09-REQ-2.4
func TestNoGateFailsPreflightUnlessNoVerify_TS_09_12(t *testing.T) {
	e := newFixEnvWith(t, fakeforge.GitHub, map[string]string{"README.md": "# no gate\n"}, true)
	useFixSpy(t, &spyBrain{})

	code, env := e.fix(e.forge.IssueURL(7), "--land", "none", "--brain", "spy")

	errObj := refusedBy(t, code, env, 1, "preflight")
	hint, _ := errObj["fix_hint"].(map[string]any)
	if hint["flag"] != "--no-verify" {
		t.Errorf("fix_hint = %v, want the flag --no-verify", hint)
	}

	code, env = e.fix(e.forge.IssueURL(7), "--land", "none", "--no-verify", "--brain", "spy")
	if errObj, _ := env["error"].(map[string]any); errObj["category"] == "preflight" {
		t.Errorf("with --no-verify: exit code %d, error %v, want the missing gate not to stop the run", code, errObj)
	}
}

// TestLandingPrerequisitesOfTheRepositoryAndTheInvocation_TS_09_13 verifies TS-09-13.
// Verifies: 09-REQ-2.5
func TestLandingPrerequisitesOfTheRepositoryAndTheInvocation_TS_09_13(t *testing.T) {
	requireMake(t)
	useFixSpy(t, &spyBrain{})

	noOrigin := newFixEnvWith(t, fakeforge.GitHub, fixFiles, false)
	code, env := noOrigin.fix(noOrigin.forge.IssueURL(7), "--land", "branch", "--brain", "spy")
	if msg := refusedBy(t, code, env, 1, "preflight")["message"].(string); !strings.Contains(msg, "--land branch") || !strings.Contains(msg, "origin") {
		t.Errorf("message = %q, want it to say what --land branch needs", msg)
	}
	for _, args := range [][]string{{"--land", "none"}, {"--land", "branch", "--dry-run"}} {
		if code, env := noOrigin.fix(noOrigin.forge.IssueURL(7), append(args, "--brain", "spy")...); code != 0 {
			if errObj, _ := env["error"].(map[string]any); errObj["category"] == "preflight" {
				t.Errorf("%v: %v, want a repository with no origin to do", args, errObj)
			}
		}
	}

	e := newFixEnv(t, fakeforge.GitHub)
	code, env = e.fix("the greeting says helo", "--land", "pr", "--brain", "spy")
	errObj := refusedBy(t, code, env, 2, "usage")
	hint, _ := errObj["fix_hint"].(map[string]any)
	if hint["flag"] != "--repo" {
		t.Errorf("fix_hint = %v, want the flag --repo", hint)
	}
	if code, env := e.fix("the greeting says helo", "--land", "pr", "--repo", "o/r", "--brain", "spy"); code == 2 {
		t.Errorf("with --repo: exit code %d, error %v", code, env["error"])
	}
}

// TestAForgeThatCannotTakeAPullRequest_TS_09_14 verifies TS-09-14.
// Verifies: 09-REQ-2.5
func TestAForgeThatCannotTakeAPullRequest_TS_09_14(t *testing.T) {
	requireMake(t)
	useFixSpy(t, &spyBrain{})

	for kind, variable := range map[string]string{fakeforge.GitHub: "GITHUB_TOKEN", fakeforge.GitLab: "GITLAB_TOKEN"} {
		t.Run("no credential, "+kind, func(t *testing.T) {
			e := newFixEnv(t, kind)
			e.app.NewForge = e.forge.NewForge("")

			code, env := e.fix(e.forge.IssueURL(7), "--land", "pr", "--brain", "spy")

			errObj := refusedBy(t, code, env, 1, "auth")
			if hint, _ := errObj["fix_hint"].(map[string]any); hint["env"] != variable {
				t.Errorf("fix_hint = %v, want env %s", hint, variable)
			}

			code, env = e.fix(e.forge.IssueURL(7), "--land", "pr", "--dry-run", "--brain", "spy")
			if !slices.Contains(warningCodes(env), "no_forge_token") {
				t.Errorf("dry run: exit code %d, warnings %v, want no_forge_token", code, warningCodes(env))
			}
			if errObj, _ := env["error"].(map[string]any); errObj["category"] == "auth" {
				t.Errorf("dry run: %v, want the missing token not to stop the run", errObj)
			}
		})
	}

	t.Run("a repository that cannot be read", func(t *testing.T) {
		e := newFixEnv(t, fakeforge.GitHub)
		e.forge.Fail(fakeforge.OpRepo, fakeforge.Failure{Status: 404, Message: "Not Found"})
		code, env := e.fix(e.forge.IssueURL(7), "--land", "pr", "--brain", "spy")
		refusedBy(t, code, env, 1, "forge")
	})
	for name, failure := range map[string]fakeforge.Failure{
		"a rate limit":      {Status: 429, Message: "slow down"},
		"a server error":    {Status: 503, Message: "down"},
		"a connection lost": {Hangup: true},
	} {
		t.Run(name+" is worth trying again", func(t *testing.T) {
			e := newFixEnv(t, fakeforge.GitHub)
			e.forge.Fail(fakeforge.OpRepo, failure)
			code, env := e.fix(e.forge.IssueURL(7), "--land", "pr", "--brain", "spy")
			if errObj := refusedBy(t, code, env, 1, "forge"); errObj["retryable"] != true {
				t.Errorf("error = %v, want retryable", errObj)
			}
		})
	}
	t.Run("a repository that is not found is not worth it", func(t *testing.T) {
		e := newFixEnv(t, fakeforge.GitHub)
		e.forge.Fail(fakeforge.OpRepo, fakeforge.Failure{Status: 404, Message: "Not Found"})
		code, env := e.fix(e.forge.IssueURL(7), "--land", "pr", "--brain", "spy")
		if errObj := refusedBy(t, code, env, 1, "forge"); errObj["retryable"] != false {
			t.Errorf("error = %v, want not retryable", errObj)
		}
	})
	t.Run("an archived repository", func(t *testing.T) {
		e := newFixEnv(t, fakeforge.GitHub)
		e.forge.Archived = true
		code, env := e.fix(e.forge.IssueURL(7), "--land", "pr", "--brain", "spy")
		if msg := refusedBy(t, code, env, 1, "forge")["message"].(string); !strings.Contains(msg, "archived") {
			t.Errorf("message = %q, want 'archived'", msg)
		}
	})
	t.Run("a credential the forge refuses", func(t *testing.T) {
		e := newFixEnv(t, fakeforge.GitHub)
		e.forge.Fail(fakeforge.OpRepo, fakeforge.Failure{Status: 401, Message: "Bad credentials"})
		code, env := e.fix(e.forge.IssueURL(7), "--land", "pr", "--brain", "spy")
		refusedBy(t, code, env, 1, "auth")
	})
	t.Run("--land branch needs no forge", func(t *testing.T) {
		e := newFixEnv(t, fakeforge.GitHub)
		e.app.NewForge = e.forge.NewForge("")
		code, env := e.fix(e.forge.IssueURL(7), "--land", "branch", "--brain", "spy")
		if errObj, _ := env["error"].(map[string]any); errObj["category"] == "auth" || errObj["category"] == "forge" {
			t.Errorf("exit code %d, error %v, want the forge not to be asked", code, errObj)
		}
	})
}

// TestABrainThatCannotBeMadeFailsAuthBeforeABoxIsOpened_TS_09_15 verifies TS-09-15.
// Verifies: 09-REQ-2.6
func TestABrainThatCannotBeMadeFailsAuthBeforeABoxIsOpened_TS_09_15(t *testing.T) {
	requireMake(t)
	e := newFixEnv(t, fakeforge.GitHub)
	boxes := countBoxes(t)

	code, env := e.fix(e.forge.IssueURL(7), "--brain", "agentkit", "--sandbox", "countbox", "--land", "none")

	errObj := refusedBy(t, code, env, 1, "auth")
	if hint, _ := errObj["fix_hint"].(map[string]any); hint["env"] != "ANTHROPIC_API_KEY" {
		t.Errorf("fix_hint = %v, want ANTHROPIC_API_KEY", hint)
	}
	if boxes.Load() != 0 {
		t.Errorf("%d boxes were opened", boxes.Load())
	}
	_ = brain.AgentkitName
}

// TestAGateThatCannotBeUsedIsAConfigFailure guards 09-REQ-2.4: a gate.json that does
// not parse is not "no gate": it is named as a configuration to correct, with or
// without --no-verify.
func TestAGateThatCannotBeUsedIsAConfigFailure(t *testing.T) {
	e := newFixEnv(t, fakeforge.GitHub)
	useFixSpy(t, &spyBrain{})
	if err := os.MkdirAll(filepath.Join(e.dir, ".agent-fox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.dir, ".agent-fox", "gate.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, extra := range [][]string{nil, {"--no-verify"}} {
		code, env := e.fix(e.forge.IssueURL(7), append([]string{"--land", "none", "--brain", "spy"}, extra...)...)
		errObj := refusedBy(t, code, env, 1, "config")
		if !strings.Contains(errObj["message"].(string), "gate.json") {
			t.Errorf("message = %q, want it to name the file", errObj["message"])
		}
	}
}

// TestAClosedIssueIsWarnedOfAndTheRunGoesOn guards 09-REQ-2.7: the warning
// issue_closed, and no stop.
func TestAClosedIssueIsWarnedOfAndTheRunGoesOn(t *testing.T) {
	e := newFixEnv(t, fakeforge.GitHub)
	e.forge.AddIssue(fakeforge.Issue{Number: 9, Title: "Old", Body: "It broke.", State: "closed"})
	useFixSpy(t, &spyBrain{})

	_, env := e.fix(e.forge.IssueURL(9), "--land", "none", "--brain", "spy")

	if !slices.Contains(warningCodes(env), "issue_closed") {
		t.Errorf("warnings = %v, want issue_closed", warningCodes(env))
	}
	if errObj, _ := env["error"].(map[string]any); errObj["category"] == "preflight" || errObj["category"] == "usage" {
		t.Errorf("error = %v: a closed issue does not stop the run", errObj)
	}
}

// TestTheHelpOfFixShowsItsDefaults guards 09-REQ-1.2 and 1.3 at the registration:
// the defaults of the loop and the flags it takes.
func TestTheHelpOfFixShowsItsDefaults(t *testing.T) {
	e := newFixEnv(t, fakeforge.GitHub)
	var out strings.Builder
	e.app.Main(context.Background(), []string{"af", "help", "fix"}, strings.NewReader(""), &out, &out)
	help := out.String()

	for flag, def := range map[string]string{"brain": "agentkit", "budget": "10", "max-turns": "150", "attempts": "2", "land": "pr", "timeout": "45m0s"} {
		if !regexp.MustCompile(`(?m)^\s+--` + flag + ` .*\(default ` + regexp.QuoteMeta(def) + `\)$`).MatchString(help) {
			t.Errorf("af help fix does not show --%s with the default %s:\n%s", flag, def, help)
		}
	}
	for _, want := range []string{"--base branch", "--no-verify", "--dry-run", "--context", "--repo"} {
		if !strings.Contains(help, want) {
			t.Errorf("af help fix does not show %q:\n%s", want, help)
		}
	}
	// The box has no default of its own that the help could print: it is chosen by AF_SANDBOX,
	// and without that it is podman.
	sandbox := regexp.MustCompile(`(?m)^\s+--sandbox .*$`).FindString(help)
	for _, want := range []string{"podman or local", "AF_SANDBOX", "else podman"} {
		if !strings.Contains(sandbox, want) {
			t.Errorf("the --sandbox line of af help fix = %q, want it to say %q", sandbox, want)
		}
	}
	if strings.Contains(sandbox, "(default") {
		t.Errorf("the --sandbox line of af help fix = %q, want no default printed", sandbox)
	}
}
