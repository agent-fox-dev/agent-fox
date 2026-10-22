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
	"github.com/agent-fox-dev/agentfox/internal/triage"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// The edges of the commands that the review of the branch found: each of these
// is a case the spec's tests do not name.

// pullURL is the URL of a pull request of the fake forge.
func pullURL(e *triageEnv) string {
	return strings.Replace(e.forge.IssueURL(7), "/issues/", "/pull/", 1)
}

// TestAPullRequestIsRefusedByTheStepsAsByTheLoop guards 08-REQ-2.3 (and DD24):
// brief refuses a pull or merge request as the loop does, so that submit and land
// have no run of one to rewrite the description of.
func TestAPullRequestIsRefusedByTheStepsAsByTheLoop(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)

	code, env := e.run("", "triage", "brief", pullURL(e), "--dir", e.dir)

	errObj, _ := env["error"].(map[string]any)
	if code != 2 || errObj["category"] != "usage" || !strings.Contains(errObj["message"].(string), "pull or merge request") {
		t.Fatalf("exit code %d, error %v, want usage that says triage takes an issue", code, errObj)
	}
	if matches, _ := filepath.Glob(filepath.Join(e.dir, ".agent-fox", "run", "*", "brief.md")); len(matches) != 0 {
		t.Errorf("a brief was rendered: %v", matches)
	}
	if n := len(e.forge.Writes()); n != 0 {
		t.Errorf("%d writes to the forge", n)
	}
}

// TestAClosedIssueWarnsInTheBriefToo guards 08-REQ-2.4 for the step commands.
func TestAClosedIssueWarnsInTheBriefToo(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	e.forge.AddIssue(fakeforge.Issue{Number: 9, Title: "Old", Body: "It broke.", State: "closed"})

	code, env := e.run("", "triage", "brief", e.forge.IssueURL(9), "--dir", e.dir)

	if code != 0 || !slices.Contains(warningCodes(env), triage.WarnIssueClosed) {
		t.Errorf("exit code %d, warnings %v, want done with issue_closed", code, warningCodes(env))
	}
}

// TestAReportThatIsNotUTF8IsRefusedBeforeABriefOrAModel guards 08-REQ-2.1: text the
// box and the model cannot take is a usage failure of the preflight, in the loop
// and in brief, and not a failure of the box after the brief is written.
func TestAReportThatIsNotUTF8IsRefusedBeforeABriefOrAModel(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	file := filepath.Join(t.TempDir(), "latin1.txt")
	if err := os.WriteFile(file, []byte("caf\xe9 crashes on submit"), 0o644); err != nil {
		t.Fatal(err)
	}
	spy := &spyBrain{}
	useSpy(t, spy)

	for name, args := range map[string][]string{
		"the loop": {"triage", file, "--repo", "o/r", "--dir", e.dir, "--brain", "spy"},
		"brief":    {"triage", "brief", file, "--dir", e.dir},
	} {
		t.Run(name, func(t *testing.T) {
			code, env := e.run("", args...)

			errObj, _ := env["error"].(map[string]any)
			if code != 2 || errObj["category"] != "usage" || !strings.Contains(errObj["message"].(string), "UTF-8") {
				t.Fatalf("exit code %d, error %v, want usage that names UTF-8", code, errObj)
			}
		})
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

// TestLandOfARunWithNoRepositoryAsksForOne guards 08-REQ-2.5 for land: the
// message is the one that names --repo and --land none, and not the shell's about
// a forge it cannot find.
func TestLandOfARunWithNoRepositoryAsksForOne(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	code, env := e.run("", "triage", "brief", "the app crashes on submit", "--dir", e.dir)
	if code != 0 {
		t.Fatalf("brief: exit code %d, error %v", code, env["error"])
	}
	id := env["run_id"].(string)
	if code, env := e.submit(id, diagnosisPayload(nil)); code != 0 {
		t.Fatalf("submit: exit code %d, error %v", code, env["error"])
	}

	code, env = e.land(id)

	errObj := failedWith(t, code, env, 2, "usage")
	if msg := errObj["message"].(string); !strings.Contains(msg, "--repo") || !strings.Contains(msg, "--land none") || strings.Contains(msg, "ambiguous") {
		t.Errorf("message = %q, want the one that names --repo and --land none", msg)
	}
}

// TestLaterLandingsOfATextRunNeedNotNameTheRepositoryAgain guards 08-REQ-8.5: the
// issue the first landing created is in a repository, and landing again finds it.
func TestLaterLandingsOfATextRunNeedNotNameTheRepositoryAgain(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	code, env := e.run("", "triage", "brief", "the app crashes on submit", "--dir", e.dir)
	if code != 0 {
		t.Fatalf("brief: exit code %d, error %v", code, env["error"])
	}
	id := env["run_id"].(string)
	if code, env := e.submit(id, diagnosisPayload(nil)); code != 0 {
		t.Fatalf("submit: exit code %d, error %v", code, env["error"])
	}
	if code, env := e.land(id, "--repo", "o/r"); code != 0 || resultOf(t, env)["action"] != "created_issue" {
		t.Fatalf("first landing: exit code %d, error %v", code, env["error"])
	}

	code, env = e.land(id)

	if code != 0 || resultOf(t, env)["action"] != "replaced_body" {
		t.Fatalf("second landing: exit code %d, error %v, result %v", code, env["error"], env["result"])
	}
	if n := e.forge.Count(fakeforge.OpCreate); n != 1 {
		t.Errorf("%d creates, want 1", n)
	}
}

// TestLandingAgainOnGitLabReportsTheSameIssueURL guards the result of a repeated
// landing of a text run made with --repo, whose host is not recorded: the URL is
// the one the forge gave, also the second time.
func TestLandingAgainOnGitLabReportsTheSameIssueURL(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitLab)
	code, env := e.run("", "triage", "brief", "the app crashes on submit", "--repo", "o/r", "--dir", e.dir)
	if code != 0 {
		t.Fatalf("brief: exit code %d, error %v", code, env["error"])
	}
	id := env["run_id"].(string)
	if code, env := e.submit(id, diagnosisPayload(nil)); code != 0 {
		t.Fatalf("submit: exit code %d, error %v", code, env["error"])
	}

	var urls []string
	var hosts []any
	for i := range 2 {
		code, env := e.land(id)
		if code != 0 {
			t.Fatalf("landing %d: exit code %d, error %v", i+1, code, env["error"])
		}
		issue, _ := resultOf(t, env)["issue"].(map[string]any)
		urls = append(urls, issue["url"].(string))
		hosts = append(hosts, issue["host"])
	}
	if urls[0] != urls[1] || strings.Contains(urls[1], "github.com") || hosts[0] == "" || hosts[0] != hosts[1] {
		t.Errorf("urls %v, hosts %v: want the URL the forge gave, twice, and its host", urls, hosts)
	}
}

// TestAQuestionNeedsNoCredentialToBeAsked guards 08-REQ-9.1 for land: a submission
// with a question lands nothing, so landing it without a credential is the
// question and not a refusal of the credential.
func TestAQuestionNeedsNoCredentialToBeAsked(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.briefRun()
	if code, env := e.submit(id, questionPayload()); code != 0 {
		t.Fatalf("submit: exit code %d, error %v", code, env["error"])
	}
	e.app.NewForge = e.forge.NewForge("")

	code, env := e.land(id)

	if code != 3 || env["status"] != "needs_human" {
		t.Errorf("exit code %d, status %v, error %v, want needs_human", code, env["status"], env["error"])
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

// TestALinkOutOfTheCheckoutCannotBeCited guards 08-REQ-5.5 and DD7 through the
// command: a link is in the file view, and a diagnosis may not name a file that is
// not in the checkout.
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

	code, env := e.submit(id, cite("linkout"))
	errObj := failedWith(t, code, env, 1, "submission")
	if msg := errObj["message"].(string); !strings.Contains(msg, "/affected_files/0/path") || !strings.Contains(msg, "cite a file") {
		t.Errorf("message = %q", msg)
	}
	if code, env := e.submit(id, cite("linkmain")); code != 0 {
		t.Errorf("a link to a file of the checkout: exit code %d, error %v, want accepted", code, env["error"])
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

// TestARejectionsFileThatCannotBeReadIsAWarningInEveryEnvelope guards 08-REQ-6.3 at
// the envelope: submit and land raise rejections_unreadable and count 0.
func TestARejectionsFileThatCannotBeReadIsAWarningInEveryEnvelope(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.briefRun()
	dir := filepath.Join(e.dir, ".agent-fox", "run", id)
	if err := os.WriteFile(filepath.Join(dir, "rejections.jsonl"), []byte("this is not a refusal\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, submit := e.submit(id, diagnosisPayload(nil))
	if code != 0 || !slices.Contains(warningCodes(submit), triage.WarnRejectionsUnreadable) || resultOf(t, submit)["rejected_path_calls"] != float64(0) {
		t.Errorf("submit: exit code %d, warnings %v, result %v", code, warningCodes(submit), submit["result"])
	}
	code, land := e.land(id)
	got := resultOf(t, land)
	if code != 0 || !slices.Contains(warningCodes(land), triage.WarnRejectionsUnreadable) || got["rejected_path_calls"] != float64(0) || got["rejected_submissions"] != float64(0) {
		t.Errorf("land: exit code %d, warnings %v, result %v", code, warningCodes(land), got)
	}
}

// TestTheLoopWritesTheBodyAloneAndItsResultIsTheSchemasOwn guards 08-REQ-8.3 and
// 9.3 end to end: the update that replaces the body sends the body and nothing
// else, and the result of the loop validates against the schema of the command.
func TestTheLoopWritesTheBodyAloneAndItsResultIsTheSchemasOwn(t *testing.T) {
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
			valid(t, "triage", resultOf(t, env))
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

// TestLandWithNoCredentialAsksNothingOfTheForge guards 08-REQ-2.6 for land: the
// refusal is the one that names the credential, and comes before any request,
// not the failure of the first write.
func TestLandWithNoCredentialAsksNothingOfTheForge(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.landedRun()
	e.app.NewForge = e.forge.NewForge("")
	before := len(e.forge.Requests())

	code, env := e.land(id)

	errObj := failedWith(t, code, env, 1, "auth")
	hint, _ := errObj["fix_hint"].(map[string]any)
	if !strings.Contains(errObj["message"].(string), "needs a credential") || hint["env"] != "GITHUB_TOKEN" {
		t.Errorf("error = %v, want the refusal that names the credential", errObj)
	}
	if n := len(e.forge.Requests()) - before; n != 0 {
		t.Errorf("%d requests to the forge before the refusal", n)
	}
}

// TestLandOfARunWithNoRepositoryNeverTriesToFindAForge guards 08-REQ-2.5 against the
// shell's own client: with no client injected, a repository that is not there is
// told as such, and not as a forge that cannot be detected.
func TestLandOfARunWithNoRepositoryNeverTriesToFindAForge(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	code, env := e.run("", "triage", "brief", "the app crashes on submit", "--dir", e.dir)
	if code != 0 {
		t.Fatalf("brief: exit code %d, error %v", code, env["error"])
	}
	id := env["run_id"].(string)
	if code, env := e.submit(id, diagnosisPayload(nil)); code != 0 {
		t.Fatalf("submit: exit code %d, error %v", code, env["error"])
	}
	e.app.NewForge = nil // the shell's own: it detects the forge from the environment and the origin, and there are none

	code, env = e.land(id)

	errObj := failedWith(t, code, env, 2, "usage")
	if msg := errObj["message"].(string); !strings.Contains(msg, "--land none") || strings.Contains(msg, "forge") {
		t.Errorf("message = %q, want the one that names --repo and --land none", msg)
	}
}

// TestLandChecksAgainTheFilesOfTheCheckoutAsTheHostHasThem guards 08-REQ-4.5 for a
// submission written by something other than af: a link out of the checkout is no
// file to cite, and a pipe is never read.
func TestLandChecksAgainTheFilesOfTheCheckoutAsTheHostHasThem(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("1\n2\n3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(e.dir, "linkout")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	id := e.briefRun()
	submission := `{"kind":"triage","run_id":"` + id + `","attempt":1,"nonce":"n","submitted_at":"2026-10-20T08:00:00Z","payload_sha256":"x","payload":` +
		diagnosisPayload(func(m map[string]any) {
			m["affected_files"] = []any{map[string]any{"path": "linkout", "line": 2, "note": "n"}}
		}) + `}`
	if err := os.WriteFile(filepath.Join(e.dir, ".agent-fox", "run", id, "submission.json"), []byte(submission), 0o644); err != nil {
		t.Fatal(err)
	}

	code, env := e.land(id)

	errObj := failedWith(t, code, env, 1, "no_result")
	if !strings.Contains(errObj["message"].(string), "cite a file") {
		t.Errorf("message = %q", errObj["message"])
	}
	if n := len(e.forge.Writes()); n != 0 {
		t.Errorf("%d writes to the forge", n)
	}
}

// TestALedgerThatCannotBeWrittenIsAWarningOfTheLanding guards the landing's record
// of itself: triage.land is noted through the shell's, and a ledger it cannot write
// is the warning ledger_unwritable and not silence.
func TestALedgerThatCannotBeWrittenIsAWarningOfTheLanding(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.landedRun()
	ledger := filepath.Join(e.dir, ".agent-fox", "run", id, "ledger.jsonl")
	if err := os.Remove(ledger); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(ledger, 0o755); err != nil {
		t.Fatal(err)
	}

	code, env := e.land(id)

	if code != 0 || !slices.Contains(warningCodes(env), "ledger_unwritable") {
		t.Errorf("exit code %d, warnings %v, want a landing that went on and the warning ledger_unwritable", code, warningCodes(env))
	}
}
