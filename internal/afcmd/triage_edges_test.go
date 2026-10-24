package afcmd

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/provider/faux"

	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// The edges of the commands that the review of the branch found: each of these
// is a case the spec's tests do not name.

// pullURL is the URL of a pull request of the fake forge.
func pullURL(e *triageEnv) string {
	return strings.Replace(e.forge.IssueURL(7), "/issues/", "/pull/", 1)
}

// TestAReportThatIsNotUTF8IsRefusedBeforeABriefOrAModel guards 08-REQ-2.1: text the
// box and the model cannot take is a usage failure of the preflight, and not a
// failure of the box after the brief is written.
func TestAReportThatIsNotUTF8IsRefusedBeforeABriefOrAModel(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	file := filepath.Join(t.TempDir(), "latin1.txt")
	if err := os.WriteFile(file, []byte("caf\xe9 crashes on submit"), 0o644); err != nil {
		t.Fatal(err)
	}
	spy := &spyBrain{}
	useSpy(t, spy)

	code, env := e.run("", "triage", file, "--repo", "o/r", "--dir", e.dir, "--brain", "spy", "--sandbox", "local")

	errObj, _ := env["error"].(map[string]any)
	if code != 2 || errObj["category"] != "usage" || !strings.Contains(errObj["message"].(string), "UTF-8") {
		t.Fatalf("exit code %d, error %v, want usage that names UTF-8", code, errObj)
	}
	if matches, _ := filepath.Glob(filepath.Join(e.dir, ".agent-fox", "run", "*", "brief.md")); len(matches) != 0 || spy.runs != 0 {
		t.Errorf("briefs %v, the brain ran %d times: nothing should have been spent", matches, spy.runs)
	}
}

// TestARefusedRunStillRecordsItsInput guards 08-REQ-1.4: triage.input is
// recorded when the input is resolved, also for a run that the preflight refuses.
func TestARefusedRunStillRecordsItsInput(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)

	code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--label", "a,b")

	if code != 2 {
		t.Fatalf("exit code %d", code)
	}
	if kinds := ledgerKinds(t, e.runDir(env)); !slices.Equal(kinds, []string{"triage.input"}) {
		t.Errorf("ledger = %v, want triage.input alone", kinds)
	}
}

// TestAnAbsolutePathInTheBoxIsMadeRelative guards 08-REQ-5.4 in a box: a brain
// that is told its workspace by an error of a tool cites a file by the absolute
// path it has there, and the path is the one of the repository.
func TestAnAbsolutePathInTheBoxIsMadeRelative(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	spy := &spyBrain{}
	spy.run = func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		payload := diagnosisPayload(func(m map[string]any) {
			m["affected_files"] = []any{map[string]any{"path": b.Box.Spec().Workdir + "/main.go", "line": 6, "note": "Run"}}
		})
		return submitting(payload)(ctx, b)
	}
	useSpy(t, spy)

	code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "spy", "--land", "none")

	if code != 0 {
		t.Fatalf("exit code %d, error %v", code, env["error"])
	}
	got := resultOf(t, env)
	files := got["diagnosis"].(map[string]any)["affected_files"].([]any)
	if files[0].(map[string]any)["path"] != "main.go" || got["rejected_path_calls"] != float64(0) {
		t.Errorf("result = %v, want the path main.go and no refused call", got)
	}
}

// TestALinkOutOfTheCheckoutCannotBeCited guards 08-REQ-5.5 and DD7 at the submit
// core over the checkout: a link is in the file view, and a diagnosis may not name a
// file that is not in the checkout.
func TestALinkOutOfTheCheckoutCannotBeCited(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("1\n2\n3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(e.dir, "linkout")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	if err := os.Symlink("main.go", filepath.Join(e.dir, "linkmain")); err != nil {
		t.Fatal(err)
	}
	id := e.briefRun()
	cite := func(path string) string {
		return diagnosisPayload(func(m map[string]any) {
			m["affected_files"] = []any{map[string]any{"path": path, "note": "n"}}
		})
	}

	err := e.inProcess(id, cite("linkout"))
	if err == nil || !strings.Contains(err.Error(), "/affected_files/0/path") || !strings.Contains(err.Error(), "cite a file") {
		t.Errorf("a link out of the checkout: %v, want the refusal of the path", err)
	}
	if err := e.inProcess(id, cite("linkmain")); err != nil {
		t.Errorf("a link to a file of the checkout: %v, want accepted", err)
	}
}

// TestRunInPlaceWarnsThatItReadsTheTreeAsItIs guards the honesty of `af triage
// run`: the tools reach what the process reaches, and the envelope says so.
func TestRunInPlaceWarnsThatItReadsTheTreeAsItIs(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.briefRun()
	fauxBrains(t, faux.New(call("c1", "file_diagnosis", diagnosisPayload(nil))))

	code, env := e.run("", "triage", "run", "--run-id", id, "--dir", e.dir)

	if code != 0 || !slices.Contains(warningCodes(env), "box_unconfined") {
		t.Errorf("exit code %d, warnings %v, want done with box_unconfined", code, warningCodes(env))
	}
	for _, w := range env["warnings"].([]any) {
		if m := w.(map[string]any); m["code"] == "box_unconfined" && !strings.Contains(m["message"].(string), ".git/config") {
			t.Errorf("the warning = %q, want it to say what is reachable", m["message"])
		}
	}
}

// TestNoBrainIsMadeBeforeThePreflightPasses guards 08-REQ-2.1: a refused run
// constructs no brain, not even one that would cost nothing until it ran.
func TestNoBrainIsMadeBeforeThePreflightPasses(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	spy := &spyBrain{}
	useSpy(t, spy)

	for name, args := range map[string][]string{
		"a bad label":    {e.forge.IssueURL(7), "--label", "a,b"},
		"a pull request": {pullURL(e)},
		"a bad budget":   {e.forge.IssueURL(7), "--budget", "0"},
	} {
		if code, _ := e.loop(append(args, "--dir", e.dir, "--brain", "spy")...); code != 2 {
			t.Errorf("%s: exit code %d, want 2", name, code)
		}
	}
	e.app.NewForge = e.forge.NewForge("")
	if code, _ := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "spy"); code != 1 {
		t.Errorf("no credential: exit code %d, want 1", code)
	}
	if spy.news != 0 || spy.runs != 0 {
		t.Errorf("the brain was made %d times and ran %d times by runs that were refused", spy.news, spy.runs)
	}

	e.app.NewForge = e.forge.NewForge("test-token")
	if code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "spy", "--land", "none"); code != 1 || spy.news != 1 {
		t.Errorf("a run that passes: exit code %d (%v), the brain made %d times, want it made once", code, env["error"], spy.news)
	}
}

// TestTheRefusalsOfTheLandingCheckAreTheEnvelopesTheySay guards 08-REQ-2.6 and 2.7
// through the loop: the category, whether to retry, the variable to set, and a
// brief that is not written.
func TestTheRefusalsOfTheLandingCheckAreTheEnvelopesTheySay(t *testing.T) {
	for _, tc := range []struct {
		name      string
		kind      string
		prepare   func(*triageEnv)
		category  string
		retryable bool
		hintEnv   string
		mention   string
	}{
		{"no credential on GitHub", fakeforge.GitHub, func(e *triageEnv) { e.app.NewForge = e.forge.NewForge("") }, "auth", false, "GITHUB_TOKEN", "needs a credential"},
		{"no credential on GitLab", fakeforge.GitLab, func(e *triageEnv) { e.app.NewForge = e.forge.NewForge("") }, "auth", false, "GITLAB_TOKEN", "needs a credential"},
		{"a credential the forge refuses", fakeforge.GitHub, func(e *triageEnv) {
			e.forge.Fail(fakeforge.OpRepo, fakeforge.Failure{Status: 401, Message: "Bad credentials"})
		}, "auth", false, "GITHUB_TOKEN", "cannot read"},
		{"a repository that is not found", fakeforge.GitHub, func(e *triageEnv) {
			e.forge.Fail(fakeforge.OpRepo, fakeforge.Failure{Status: 404, Message: "Not Found"})
		}, "forge", false, "", "not found"},
		{"a forge that is rate limited", fakeforge.GitHub, func(e *triageEnv) {
			e.forge.Fail(fakeforge.OpRepo, fakeforge.Failure{Status: 429, Message: "slow down"})
		}, "forge", true, "", "cannot read"},
		{"a forge that fails", fakeforge.GitHub, func(e *triageEnv) {
			e.forge.Fail(fakeforge.OpRepo, fakeforge.Failure{Status: 503, Message: "down"})
		}, "forge", true, "", "cannot read"},
		{"a connection that is lost", fakeforge.GitHub, func(e *triageEnv) {
			e.forge.Fail(fakeforge.OpRepo, fakeforge.Failure{Hangup: true})
		}, "forge", true, "", "cannot read"},
		{"an archived repository", fakeforge.GitHub, func(e *triageEnv) { e.forge.Archived = true }, "forge", false, "", "archived"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTriageEnv(t, tc.kind)
			tc.prepare(e)

			code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "scripted", "--script", script(t, submitStep(diagnosisPayload(nil), "accepted")))

			errObj := failedWith(t, code, env, 1, tc.category)
			hint, _ := errObj["fix_hint"].(map[string]any)
			if errObj["retryable"] != tc.retryable || (tc.hintEnv != "" && hint["env"] != tc.hintEnv) || !strings.Contains(errObj["message"].(string), tc.mention) {
				t.Errorf("error = %v, want retryable %v, hint %s, a message with %q", errObj, tc.retryable, tc.hintEnv, tc.mention)
			}
			if matches, _ := filepath.Glob(filepath.Join(e.dir, ".agent-fox", "run", "*", "brief.md")); len(matches) != 0 {
				t.Errorf("a brief was rendered: %v", matches)
			}
			if n := len(e.forge.Writes()); n != 0 {
				t.Errorf("%d writes to the forge", n)
			}
		})
	}
}

// TestTheLoopWritesTheBodyAlone guards 08-REQ-8.3 end to end: the update that
// replaces the body sends the body and nothing else.
func TestTheLoopWritesTheBodyAlone(t *testing.T) {
	for _, kind := range []string{fakeforge.GitHub, fakeforge.GitLab} {
		t.Run(kind, func(t *testing.T) {
			e := newTriageEnv(t, kind)

			code, env := scriptedLoop(e, e.forge.IssueURL(7), diagnosisPayload(nil), "--label", "triaged")

			if code != 0 {
				t.Fatalf("exit code %d, error %v", code, env["error"])
			}
			var bodies []fakeforge.Request
			for _, w := range e.forge.Writes() {
				if w.Op == fakeforge.OpBody {
					bodies = append(bodies, w)
				}
			}
			if len(bodies) != 1 {
				t.Fatalf("%d body writes, want 1", len(bodies))
			}
			if keys := mapKeys(bodies[0].JSON); len(keys) != 1 || (keys[0] != "body" && keys[0] != "description") {
				t.Errorf("the update sent %v, want the body alone", keys)
			}
		})
	}
}

func mapKeys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// TestTheLabelsGoWithTheIssueThatIsCreated guards 08-REQ-8.5: a text report's
// issue is created with the labels, in the order they were given, and the result
// says so.
func TestTheLabelsGoWithTheIssueThatIsCreated(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)

	code, env := scriptedLoop(e, "the app crashes on submit", diagnosisPayload(nil), "--repo", "o/r", "--label", "zeta", "--label", "alpha", "--label", "zeta")

	if code != 0 {
		t.Fatalf("exit code %d, error %v", code, env["error"])
	}
	if got := anyStrings(resultOf(t, env)["labels"]); !slices.Equal(got, []string{"zeta", "alpha"}) {
		t.Errorf("result labels = %v, want zeta and alpha in the order given", got)
	}
	creates := 0
	for _, w := range e.forge.Writes() {
		if w.Op == fakeforge.OpCreate {
			creates++
			if got := anyStrings(w.JSON["labels"]); !slices.Equal(got, []string{"zeta", "alpha"}) {
				t.Errorf("the create request sent the labels %v", w.JSON["labels"])
			}
		}
	}
	if creates != 1 {
		t.Errorf("%d creates, want 1", creates)
	}
}

// TestAFailedWriteIsRetryableOnlyWhereTheForgeMayRecover guards 08-REQ-8.7 at the
// envelope: the flag the caller reads to decide whether to try again.
func TestAFailedWriteIsRetryableOnlyWhereTheForgeMayRecover(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failure   fakeforge.Failure
		category  string
		retryable bool
	}{
		{"a refusal", fakeforge.Failure{Status: 422, Message: "invalid"}, "forge", false},
		{"a server error", fakeforge.Failure{Status: 502, Message: "bad gateway"}, "forge", false},
		{"a rate limit", fakeforge.Failure{Status: 429, Message: "slow down"}, "forge", true},
		{"a lost connection", fakeforge.Failure{Hangup: true}, "forge", true},
		{"a credential that is refused", fakeforge.Failure{Status: 401, Message: "bad credentials"}, "auth", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTriageEnv(t, fakeforge.GitHub)
			e.forge.Fail(fakeforge.OpBody, tc.failure)

			code, env := scriptedLoop(e, e.forge.IssueURL(7), diagnosisPayload(nil))

			errObj := failedWith(t, code, env, 1, tc.category)
			if errObj["retryable"] != tc.retryable {
				t.Errorf("retryable = %v, want %v (error %v)", errObj["retryable"], tc.retryable, errObj)
			}
		})
	}
}

// TestARunWithNoRepositoryNeverTriesToFindAForge guards 08-REQ-2.5 against the shell's
// own client: with no client injected, a repository that is not there is told as such,
// and not as a forge that cannot be detected.
func TestARunWithNoRepositoryNeverTriesToFindAForge(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	e.app.NewForge = nil // the shell's own: it detects the forge from the environment and the origin, and there are none

	code, env := scriptedLoop(e, "the app crashes on submit", diagnosisPayload(nil))

	errObj := failedWith(t, code, env, 2, "usage")
	if msg := errObj["message"].(string); !strings.Contains(msg, "--land none") || strings.Contains(msg, "forge") {
		t.Errorf("message = %q, want the one that names --repo and --land none", msg)
	}
}
