package afcmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// land runs af fix land on the run of an envelope.
func (e *fixEnv) land(brief map[string]any, args ...string) (int, map[string]any) {
	e.t.Helper()
	return e.run("", append([]string{"fix", "land", "--run-id", brief["run_id"].(string), "--dir", e.dir}, args...)...)
}

// submitReport submits the report on the run of an envelope.
func (e *fixEnv) submitReport(brief map[string]any, payload string) (int, map[string]any) {
	e.t.Helper()
	return e.run(payload, "fix", "submit", "--run-id", brief["run_id"].(string), "--dir", e.dir)
}

// writeFile writes a file of the checkout.
func (e *fixEnv) writeFile(path, content string) {
	e.t.Helper()
	full := filepath.Join(e.dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

const helloReport2 = `{"subject":"say hello","root_cause":"the script says helo","change":"say hello","files":[{"path":"src/greet.sh","why":"the word"},{"path":"tests/hello_test.sh","why":"the proof"}]}`

const helloTest = "[ \"$(sh src/greet.sh)\" = hello ] || { echo \"FAIL: not hello\"; exit 1; }\n"

// TestRunSubmitAndLandRefuseARunThatIsNoRun_TS_09_6 verifies TS-09-6: a run id that is a
// path, one that names no run, and one whose directory has no fix.json are usage failures of
// the three commands that carry a run on, and the checkout is as it was.
// Verifies: 09-REQ-1.6
func TestRunSubmitAndLandRefuseARunThatIsNoRun_TS_09_6(t *testing.T) {
	requireMake(t)
	e := newFixEnv(t, fakeforge.GitHub)
	bare := "20260101_000000_abcdef"
	if err := os.MkdirAll(filepath.Join(e.dir, ".agent-fox", "run", bare), 0o755); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, e.dir)
	branches, head := e.branches(), gitDo(t, e.dir, "rev-parse", "HEAD")

	for _, command := range [][]string{{"fix", "run", "--brain", "scripted", "--script", "x.json"}, {"fix", "submit"}, {"fix", "land"}} {
		for _, id := range []string{"../x", "nope", "20260101_000000_ffffff", bare} {
			t.Run(strings.Join(command[:2], " ")+" "+id, func(t *testing.T) {
				args := append(slices.Clone(command), "--run-id", id, "--dir", e.dir)

				code, env := e.run(helloReport2, args...)

				refusedBy(t, code, env, 2, "usage")
				if env["status"] != "usage" {
					t.Errorf("status = %v", env["status"])
				}
			})
		}
	}
	if !equalMaps(before, snapshot(t, e.dir)) || !slices.Equal(e.branches(), branches) || gitDo(t, e.dir, "rev-parse", "HEAD") != head {
		t.Error("a refused run changed the checkout")
	}
	if _, err := os.Stat(filepath.Join(e.dir, ".agent-fox", "run", bare, "submission.json")); err == nil {
		t.Error("fix submit wrote a submission for a run that is no fix run")
	}
}

// TestTheFixResultsMatchTheirSchemas_TS_09_67 verifies TS-09-67 at the commands: the
// schemas of fix and of fix land describe the result type, a landed and a parked result of
// fix land carry every field they require, and the box of a land is null.
// Verifies: 09-REQ-10.1
func TestTheFixResultsMatchTheirSchemas_TS_09_67(t *testing.T) {
	checkSchema(t, "fix", fix.Result{})
	checkSchema(t, "fix_land", fix.Result{})

	requireMake(t)
	required := func(name string) []string {
		var schema struct {
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(resultSchema(name), &schema); err != nil {
			t.Fatal(err)
		}
		return schema.Required
	}
	keysOf := func(m map[string]any) []string {
		var keys []string
		for k := range m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		return keys
	}

	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	_, brief := e.brief(e.forge.IssueURL(7))
	e.writeFile("src/greet.sh", "echo hello\n")
	e.writeFile("tests/hello_test.sh", helloTest)
	e.submitReport(brief, helloReport2)
	code, env := e.land(brief, "--land", "branch")
	if code != 0 {
		t.Fatalf("exit code %d, error %v", code, env["error"])
	}
	landed := resultOf(t, env)
	if want := required("fix_land"); !slices.Equal(keysOf(landed), slices.Sorted(slices.Values(want))) {
		t.Errorf("landed result keys %v, schema requires %v", keysOf(landed), want)
	}
	if landed["outcome"] != "landed" || landed["box"] != nil || landed["issue"] != e.forge.IssueURL(7) {
		t.Errorf("landed = %v", landed)
	}
	if commit, _ := landed["commit"].(string); len(commit) != 40 || commit != strings.TrimSpace(gitDo(t, e.dir, "rev-parse", landed["branch"].(string))) {
		t.Errorf("commit = %v, want the full hash of the branch", landed["commit"])
	}

	e2 := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	_, brief2 := e2.brief(e2.forge.IssueURL(7))
	e2.writeFile("src/greet.sh", ":\n")
	e2.submitReport(brief2, helloReport2)
	code, env = e2.land(brief2, "--land", "branch")
	if code != 4 {
		t.Fatalf("exit code %d, error %v", code, env["error"])
	}
	if want := required("fix_land"); !slices.Equal(keysOf(resultOf(t, env)), slices.Sorted(slices.Values(want))) || resultOf(t, env)["outcome"] != "parked" {
		t.Errorf("parked result = %v", resultOf(t, env))
	}
}

// TestFixLandLandsAndParksFromTheCommandLine guards 09-REQ-6.8 and the exits of the command:
// a hand-made change that is good lands with exit 0, and one that regresses the gate is parked
// with exit 4, and neither starts a brain.
func TestFixLandLandsAndParksFromTheCommandLine(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	_, brief := e.brief(e.forge.IssueURL(7))
	e.writeFile("src/greet.sh", ":\n")
	e.submitReport(brief, helloReport2)

	code, env := e.land(brief, "--land", "pr")

	if code != 4 || env["status"] != "unverified" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	res := resultOf(t, env)
	if res["outcome"] != "parked" || res["verdict"] != "still_failing" || res["landable"] != false {
		t.Errorf("result = %v", res)
	}
	prs := e.forge.PullRequests()
	if len(prs) != 1 || !prs[0].Draft || !strings.HasPrefix(prs[0].Title, "wip: ") || !strings.Contains(prs[0].Body, "Refs "+e.forge.IssueURL(7)) {
		t.Errorf("pull requests = %+v, want a draft that says Refs", prs)
	}
	if pr, _ := res["pull_request"].(map[string]any); pr["draft"] != true {
		t.Errorf("pull_request = %v", res["pull_request"])
	}
	if codes := warningCodes(env); !slices.Contains(codes, "fix_not_landed") {
		t.Errorf("warnings = %v", codes)
	}
	var switchTo bool
	for _, n := range env["next"].([]any) {
		argv := n.(map[string]any)["argv"].([]any)
		if len(argv) == 3 && argv[0] == "git" && argv[1] == "switch" && argv[2] == res["branch"] {
			switchTo = true
		}
	}
	if !switchTo {
		t.Errorf("next = %v, want git switch to the branch", env["next"])
	}
	if now := strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "--abbrev-ref", "HEAD")); now != "main" {
		t.Errorf("checked out %q", now)
	}
	kinds := ledgerKinds(t, runDirOf(e, brief))
	if !slices.Contains(kinds, "measure") || !slices.Contains(kinds, "refusal") {
		t.Errorf("ledger kinds = %v", kinds)
	}
	var artifacts []string
	for _, a := range env["artifacts"].([]any) {
		artifacts = append(artifacts, a.(map[string]any)["kind"].(string))
	}
	for _, want := range []string{"branch", "commit", "pull_request", "run_dir", "submission"} {
		if !slices.Contains(artifacts, want) {
			t.Errorf("artifacts = %v, want %s", artifacts, want)
		}
	}
}

// TestFixLandLandsAGoodChangeAsAPullRequestWithItsReport guards the landing from the command
// line: the pull request, report.md, and the issue of the text it was made for.
func TestFixLandLandsAGoodChangeAsAPullRequestWithItsReport(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	_, brief := e.brief("the greeting says helo", "--repo", "o/r")
	e.writeFile("src/greet.sh", "echo hello\n")
	e.writeFile("tests/hello_test.sh", helloTest)
	e.submitReport(brief, helloReport2)

	code, env := e.land(brief)

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	res := resultOf(t, env)
	issue, _ := res["issue_created"].(map[string]any)
	if res["outcome"] != "landed" || res["closes"] != true || issue["number"] == nil || res["land"] != "pr" || res["pushed"] != true {
		t.Errorf("result = %v", res)
	}
	prs := e.forge.PullRequests()
	if len(prs) != 1 || prs[0].Draft || !strings.Contains(prs[0].Body, "Closes ") || !strings.HasPrefix(prs[0].Title, "fix: say hello") {
		t.Fatalf("pull requests = %+v", prs)
	}
	report, err := os.ReadFile(filepath.Join(runDirOf(e, brief), "report.md"))
	if err != nil || string(report) != prs[0].Body {
		t.Errorf("report.md is not the body of the pull request: %v", err)
	}
	var reported bool
	for _, a := range env["artifacts"].([]any) {
		reported = reported || a.(map[string]any)["kind"] == "report"
	}
	if !reported {
		t.Errorf("artifacts = %v, want the report", env["artifacts"])
	}
	if sent := e.forge.Issue(int(issue["number"].(float64))); !strings.Contains(sent.Body, "the greeting says helo") {
		t.Errorf("issue = %+v, want the report the run was given", sent)
	}
}

// TestFixLandTellsAboutAQuestionBeforeItAsksForARepository guards 09-REQ-4.6 at the command: a
// run on a text, whose checkout names no repository, that ended on a question, is refused
// for the question, which is what the person has to act on, and not for the repository that a
// pull request would need.
func TestFixLandTellsAboutAQuestionBeforeItAsksForARepository(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	_, brief := e.brief("the greeting says helo")
	e.submitReport(brief, `{"question":{"text":"Which greeting?","options":["hello","hi"]}}`)

	code, env := e.land(brief)

	msg := refusedBy(t, code, env, 1, "submission")["message"].(string)
	if !strings.Contains(msg, "Which greeting?") || !strings.Contains(msg, "--context") {
		t.Errorf("message = %q, want the question and the way to answer it", msg)
	}
}

// TestFixLandRefusesWhatItCannotLand guards the refusals of the command: a question
// submission points at fix brief, a foreign commit is told, a pull request without a
// repository says so, and none of them makes a branch.
func TestFixLandRefusesWhatItCannotLand(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	_, brief := e.brief(e.forge.IssueURL(7))
	e.writeFile("src/greet.sh", "echo hello\n")

	code, env := e.land(brief)
	if msg := refusedBy(t, code, env, 2, "usage")["message"].(string); !strings.Contains(msg, "af fix submit") {
		t.Errorf("no submission: %q", msg)
	}

	e.submitReport(brief, `{"question":{"text":"Which greeting?","options":["hello","hi"]}}`)
	code, env = e.land(brief)
	if msg := refusedBy(t, code, env, 1, "submission")["message"].(string); !strings.Contains(msg, "af fix brief "+e.forge.IssueURL(7)+" --context") {
		t.Errorf("question: %q", msg)
	}

	e.submitReport(brief, helloReport2)
	e.writeFile("docs/foreign.md", "x\n")
	gitDo(t, e.dir, "add", "docs/foreign.md")
	gitDo(t, e.dir, "commit", "-q", "-m", "chore: made outside af", "docs/foreign.md")
	code, env = e.land(brief)
	if msg := refusedBy(t, code, env, 1, "git")["message"].(string); !strings.Contains(msg, "made outside af") {
		t.Errorf("foreign commit: %q", msg)
	}
	if !slices.Equal(e.branches(), []string{"main"}) {
		t.Errorf("branches = %v", e.branches())
	}

	// A run of a text with no repository cannot open a pull request.
	e2 := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	_, brief2 := e2.brief("the greeting says helo")
	e2.writeFile("src/greet.sh", "echo hello\n")
	e2.submitReport(brief2, helloReport2)
	code, env = e2.land(brief2)
	if msg := refusedBy(t, code, env, 2, "usage")["message"].(string); !strings.Contains(msg, "--land branch") || !strings.Contains(msg, "the brief again with --repo") {
		t.Errorf("no repository: %q, want it to point at --land branch, or at the brief made again with --repo", msg)
	}
	if code, _ := e2.land(brief2, "--land", "bogus"); code != 2 {
		t.Errorf("a land mode that is none of three: exit code %d", code)
	}
	if n := len(e2.forge.Writes()); n != 0 {
		t.Errorf("%d writes to the forge", n)
	}
	// A branch needs no repository: the same run lands with --land branch.
	e2.writeFile("tests/hello_test.sh", helloTest)
	if code, env := e2.land(brief2, "--land", "branch"); code != 0 || resultOf(t, env)["outcome"] != "landed" || resultOf(t, env)["issue"] != nil {
		t.Errorf("--land branch with no repository: exit code %d, result %v, error %v", code, env["result"], env["error"])
	}
}

// TestTheHelpOfFixLandShowsItsFlags guards 09-REQ-1.3 for land.
func TestTheHelpOfFixLandShowsItsFlags(t *testing.T) {
	e := newFixEnv(t, fakeforge.GitHub)
	var out strings.Builder
	e.app.Main(t.Context(), []string{"af", "help", "fix", "land"}, strings.NewReader(""), &out, &out)
	for _, want := range []string{"--run-id", "--land mode", "(default pr)", "--dir", "--timeout", "(default 45m0s)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("af help fix land does not show %q:\n%s", want, out.String())
		}
	}
	for _, not := range []string{"--dry-run", "--repo", "--brain", "--attempts", "--no-verify", "--context"} {
		if strings.Contains(out.String(), not) {
			t.Errorf("af help fix land shows %q, which it does not take", not)
		}
	}
}

// TestAFixJSONThatCannotBeReadIsNoRunEither guards the refusal of a run directory whose fix.json
// is damaged: it is a usage failure that names the run, for the three commands.
func TestAFixJSONThatCannotBeReadIsNoRunEither(t *testing.T) {
	requireMake(t)
	e := newFixEnv(t, fakeforge.GitHub)
	_, brief := e.brief(e.forge.IssueURL(7))
	if err := os.WriteFile(filepath.Join(runDirOf(e, brief), "fix.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, command := range [][]string{{"fix", "run", "--brain", "scripted", "--script", "x.json"}, {"fix", "submit"}, {"fix", "land"}} {
		code, env := e.run(helloReport2, append(slices.Clone(command), "--run-id", brief["run_id"].(string), "--dir", e.dir)...)
		if msg := refusedBy(t, code, env, 2, "usage")["message"].(string); !strings.Contains(msg, brief["run_id"].(string)) || !strings.Contains(msg, "fix.json") {
			t.Errorf("%v: %q, want it to name the run and the file", command[:2], msg)
		}
	}
}

// TestFixRunKeepsWhatTheRoundCost guards the footer of the pull request: the brain and the
// cost of a round are in fix.json, added to what the rounds before it cost.
func TestFixRunKeepsWhatTheRoundCost(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	_, brief := e.brief(e.forge.IssueURL(7))

	code, env := e.fixRun(brief, "--brain", "scripted", "--sandbox", "local", "--script", script(t, submitStep(helloReport, "accepted")))

	if code != 0 {
		t.Fatalf("exit code %d, error %v", code, env["error"])
	}
	fj := readFixJSON(t, runDirOf(e, brief))
	if fj["brain"] != "scripted" || fj["cost_usd"] != float64(0) || fj["cost_source"] != "none" {
		t.Errorf("fix.json brain %v, cost %v (%v), want the scripted brain, at no cost", fj["brain"], fj["cost_usd"], fj["cost_source"])
	}
}
