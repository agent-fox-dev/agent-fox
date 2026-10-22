package afcmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// The smoke tests of the seven paths of 09_af_fix. Each runs the commands the way a person
// types them, through the shell, against the real preflight, loop, apply, measure, land and
// park, the gate and the revert check of the project, real git with a local bare origin, the
// local box and the scripted brain: the only things that are not the product are the forge's
// server (an httptest server) and the model, which is not there.

// smokeFiles is the fixture gate with a script that fails its test, and a target that a
// brain can run to commit inside its box, which the tool gate of the brain does not let it do
// with git itself.
func smokeFiles() map[string]string {
	files := redFixFiles()
	files["Makefile"] += "box-commit:\n\tgit add src tests\n\tgit -c user.name=box -c user.email=box@example.com commit -q -m 'a commit made in the box'\n"
	return files
}

// boxCommit is the step of a script that commits what the brain wrote, in its box.
func boxCommit() map[string]any {
	return map[string]any{"tool": "execute", "args": map[string]any{"command": "make box-commit"}, "expect": "ok"}
}

// fixSteps is the round of a brain that fixes the greeting, proves it with a test of its
// own, commits in its box and hands over its report.
func fixSteps() []map[string]any {
	return []map[string]any{
		writeStep("src/greet.sh", "echo hello\n"),
		writeStep("tests/hello_test.sh", helloTest),
		boxCommit(),
		submitStep(spyReport, "accepted"),
	}
}

// scriptRounds is a script file of rounds: the k-th run of the brain replays the k-th.
func scriptRounds(t *testing.T, rounds ...[]map[string]any) string {
	t.Helper()
	var all []map[string]any
	for _, steps := range rounds {
		all = append(all, map[string]any{"steps": steps})
	}
	data, err := json.Marshal(map[string]any{"rounds": all})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// scripted is the flags of a run on the scripted brain and the local box.
func scripted(path string) []string {
	return []string{"--brain", "scripted", "--sandbox", "local", "--script", path}
}

// commitsAbove is the number of commits a branch has above a commit.
func (e *fixEnv) commitsAbove(t *testing.T, base, branch string) string {
	t.Helper()
	return strings.TrimSpace(gitDo(t, e.dir, "rev-list", "--count", base+".."+branch))
}

// TestAnIssueBecomesOneCommitOnItsBranchAndAPullRequest_TS_09_73 verifies TS-09-73
// (09-PATH-1): an issue URL, a brain that edits its box and commits in it, and a report with a
// proving test end as one commit on fix/<N>-<slug> that carries the trailer of the run and not
// the commit of the box, pushed, with a pull request that says Closes and the checkout back
// on main.
// Verifies: 09-PATH-1, 09-REQ-1.2, 09-REQ-5.5, 09-REQ-8.3
func TestAnIssueBecomesOneCommitOnItsBranchAndAPullRequest_TS_09_73(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, smokeFiles(), true)
	base := strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "HEAD"))

	code, env := e.fix(e.forge.IssueURL(7), scripted(script(t, fixSteps()...))...)

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	res := resultOf(t, env)
	branch, _ := res["branch"].(string)
	if !strings.HasPrefix(branch, "fix/7-") || res["outcome"] != "landed" || res["closes"] != true {
		t.Fatalf("result = %v", res)
	}
	if n := e.commitsAbove(t, base, branch); n != "1" {
		t.Errorf("%s commits above the base, want exactly 1", n)
	}
	message := strings.TrimSpace(gitDo(t, e.dir, "log", "-1", "--format=%B", branch))
	paragraphs := strings.Split(message, "\n\n")
	if last := paragraphs[len(paragraphs)-1]; last != "Agent-Fox-Run: "+env["run_id"].(string) {
		t.Errorf("the last paragraph of the message is %q, want the trailer of the run:\n%s", last, message)
	}
	if !strings.Contains(message, "\nCloses "+e.forge.IssueURL(7)+"\n") {
		t.Errorf("the message does not close the issue:\n%s", message)
	}
	if all := gitDo(t, e.dir, "log", "--all", "--format=%s"); strings.Contains(all, "a commit made in the box") {
		t.Errorf("the commit of the box is in the history of the checkout:\n%s", all)
	}
	if !slices.Contains(e.remoteBranches(t), branch) {
		t.Errorf("the origin has %v, want %s pushed", e.remoteBranches(t), branch)
	}
	for _, want := range []string{"branch", "commit", "pull_request", "report", "submission", "run_dir", "transcript"} {
		if !slices.Contains(artifactKinds(env), want) {
			t.Errorf("artifacts = %v, want %s", artifactKinds(env), want)
		}
	}
	prs := e.forge.PullRequests()
	if len(prs) != 1 || prs[0].Head != branch || prs[0].Draft || !strings.Contains(prs[0].Body, "Closes "+e.forge.IssueURL(7)) {
		t.Errorf("pull requests = %+v, want one for %s that says Closes", prs, branch)
	}
	e.onBase(t)
}

// TestAFileBecomesOneIssueAndAPullRequestThatRefersToIt_TS_09_74 verifies TS-09-74
// (09-PATH-2): a report in a file under --land pr makes exactly one issue, with the report
// under ## Original report, and a pull request whose body says Closes or Refs and that issue.
// Verifies: 09-PATH-2, 09-REQ-8.1
func TestAFileBecomesOneIssueAndAPullRequestThatRefersToIt_TS_09_74(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, smokeFiles(), true)
	bug := filepath.Join(t.TempDir(), "bug.md")
	if err := os.WriteFile(bug, []byte("The greeting should say hello.\n\nRun src/greet.sh.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, env := e.fix(bug, append(scripted(script(t, fixSteps()...)), "--repo", "o/r", "--land", "pr")...)

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	res := resultOf(t, env)
	created, _ := res["issue_created"].(map[string]any)
	pr, _ := res["pull_request"].(map[string]any)
	if created == nil || pr == nil || res["issue"] != nil {
		t.Fatalf("result = %v, want issue_created and pull_request set", res)
	}
	if n := e.forge.Count(fakeforge.OpCreate); n != 1 {
		t.Errorf("%d issues were created, want exactly 1", n)
	}
	number := int(created["number"].(float64))
	if body := e.forge.Issue(number).Body; !strings.Contains(body, "## Original report") || !strings.Contains(body, "The greeting should say hello.") {
		t.Errorf("the body of the issue is:\n%s", body)
	}
	url, _ := created["url"].(string)
	prs := e.forge.PullRequests()
	if len(prs) != 1 || !(strings.Contains(prs[0].Body, "Closes "+url) || strings.Contains(prs[0].Body, "Refs "+url)) {
		t.Errorf("pull requests = %+v, want a body that closes or refers to %s", prs, url)
	}
	if !slices.Contains(e.remoteBranches(t), res["branch"].(string)) {
		t.Errorf("the origin has %v, want %v pushed", e.remoteBranches(t), res["branch"])
	}
	e.onBase(t)
}

// TestARegressingChangeIsRefusedAndTheSecondRoundLands_TS_09_75 verifies TS-09-75
// (09-PATH-3): a change that breaks a green gate is refused, the second round's task block
// says regressed and shows the output of the failing check, the second round's change lands,
// and the result counts 2 rounds in 1 attempt.
// Verifies: 09-PATH-3, 09-REQ-6.7, 09-REQ-7.2
func TestARegressingChangeIsRefusedAndTheSecondRoundLands_TS_09_75(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, fixFiles, true) // the baseline is green
	pinned := `{"subject":"pin the greeting","root_cause":"nothing pinned the greeting","change":"add a test","files":[{"path":"tests/hello_test.sh","why":"the proof"}]}`
	path := scriptRounds(t,
		[]map[string]any{writeStep("src/greet.sh", ":\n"), submitStep(spyReport, "accepted")},
		[]map[string]any{writeStep("src/greet.sh", "echo hello\n"), writeStep("tests/hello_test.sh", helloTest), submitStep(pinned, "accepted")},
	)

	code, env := e.fix(e.forge.IssueURL(7), scripted(path)...)

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	res := resultOf(t, env)
	if res["outcome"] != "landed" || res["rounds"] != float64(2) || res["attempts"] != float64(1) || res["verdict"] != "pass" {
		t.Errorf("result = %v", res)
	}
	data, err := os.ReadFile(filepath.Join(runDirOf(e, env), "brief.2.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, task, _ := strings.Cut(string(data), "\n## Task\n")
	for _, want := range []string{"round 2", "verdict: regressed", "FAIL: greet printed"} {
		if !strings.Contains(task, want) {
			t.Errorf("the task block of round 2 lacks %q:\n%s", want, task)
		}
	}
	if prs := e.forge.PullRequests(); len(prs) != 1 {
		t.Errorf("pull requests = %+v, want 1", prs)
	}
	e.onBase(t)
}

// TestAStillRedResultIsParkedAsAWipDraftPullRequest_TS_09_76 verifies TS-09-76
// (09-PATH-4): a change that leaves the gate red, with one attempt, is parked: a wip commit on
// the fix branch, pushed, a draft pull request that says Refs, the status unverified at exit 4
// with fix_not_landed, and the base checked out clean.
// Verifies: 09-PATH-4, 09-REQ-9.2, 09-REQ-9.3
func TestAStillRedResultIsParkedAsAWipDraftPullRequest_TS_09_76(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	steps := []map[string]any{writeStep("src/greet.sh", ":\n"), submitStep(spyReport, "accepted")}

	code, env := e.fix(e.forge.IssueURL(7), append(scripted(script(t, steps...)), "--land", "pr", "--attempts", "1")...)

	if code != 4 || env["status"] != "unverified" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	if !slices.Contains(warningCodes(env), "fix_not_landed") {
		t.Errorf("warnings = %v, want fix_not_landed", warningCodes(env))
	}
	res := resultOf(t, env)
	branch, _ := res["branch"].(string)
	if res["outcome"] != "parked" || res["verdict"] != "still_failing" || res["pushed"] != true {
		t.Fatalf("result = %v", res)
	}
	if subject := strings.TrimSpace(gitDo(t, e.dir, "log", "-1", "--format=%s", branch)); !strings.HasPrefix(subject, "wip: ") {
		t.Errorf("the subject is %q, want a wip commit", subject)
	}
	if message := gitDo(t, e.dir, "log", "-1", "--format=%B", branch); !strings.Contains(message, "\nRefs "+e.forge.IssueURL(7)+"\n") || strings.Contains(message, "Closes") {
		t.Errorf("the message of the parked change is:\n%s\nwant it to refer to the issue and not close it", message)
	}
	if !slices.Contains(e.remoteBranches(t), branch) {
		t.Errorf("the origin has %v, want %s pushed", e.remoteBranches(t), branch)
	}
	prs := e.forge.PullRequests()
	if len(prs) != 1 || !prs[0].Draft || !strings.Contains(prs[0].Body, "Refs "+e.forge.IssueURL(7)) {
		t.Errorf("pull requests = %+v, want a draft that says Refs", prs)
	}
	e.onBase(t)
}

// TestAQuestionStopsTheRunAndTheAnswerResumesIt_TS_09_77 verifies TS-09-77 (09-PATH-5): a
// brain that asks a question ends the run at exit 3 with the options and the way to answer,
// and nothing is changed; the invocation the envelope offers, with --context B, renders a
// brief with the same prefix whose task block holds the question and the answer.
// Verifies: 09-PATH-5, 09-REQ-3.4, 09-REQ-3.1
func TestAQuestionStopsTheRunAndTheAnswerResumesIt_TS_09_77(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	before, branches, head := snapshot(t, e.dir), e.branches(), strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "HEAD"))
	question := `{"question":{"text":"Which greeting?","options":["hello","hi"]}}`

	code, first := e.fix(e.forge.IssueURL(7), scripted(script(t, writeStep("src/greet.sh", "echo half\n"), submitStep(question, "accepted")))...)

	if code != 3 || first["status"] != "needs_human" {
		t.Fatalf("exit code %d, status %v, error %v", code, first["status"], first["error"])
	}
	nh, _ := first["needs_human"].(map[string]any)
	var ids []string
	opts, _ := nh["options"].([]any)
	for _, o := range opts {
		ids = append(ids, o.(map[string]any)["id"].(string))
	}
	if !slices.Equal(ids, []string{"A", "B"}) || nh["question"] != "Which greeting?" {
		t.Errorf("needs_human = %v, want the question and the options A and B", nh)
	}
	if !equalMaps(before, snapshot(t, e.dir)) || !slices.Equal(e.branches(), branches) || strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "HEAD")) != head {
		t.Error("the checkout changed")
	}
	if n := len(e.forge.PullRequests()); n != 0 {
		t.Errorf("%d pull requests", n)
	}

	resume, _ := nh["resume"].(map[string]any)
	var argv []string
	for _, a := range resume["argv"].([]any) {
		argv = append(argv, a.(string))
	}
	if argv[0] == "af" {
		argv = argv[1:]
	}
	code, second := e.run("", append(argv, resume["flag"].(string), "B")...)

	if code != 3 { // the same script asks again; what is under test is the brief it was given
		t.Fatalf("exit code %d, status %v, error %v", code, second["status"], second["error"])
	}
	if readFixJSON(t, runDirOf(e, second))["prefix_sha256"] != readFixJSON(t, runDirOf(e, first))["prefix_sha256"] {
		t.Error("the prefix of the resumed run is not the prefix of the run it resumes")
	}
	task := fixTaskBlockOf(t, runDirOf(e, second))
	for _, want := range []string{"Which greeting?", "A. hello", "B. hi", "\n```\nB\n```\n"} {
		if !strings.Contains(task, want) {
			t.Errorf("the task block of the resumed run lacks %q:\n%s", want, task)
		}
	}
}

// TestADryRunSpendsTheRoundAndWritesNothing_TS_09_78 verifies TS-09-78 (09-PATH-6): a dry run
// runs the round and measures the change, sends no write to the forge, makes no branch and no
// commit, pushes nothing, lists the branch, the commit, the push and the pull request as
// planned, leaves the tree clean at base_head, and has the status the real run would have had.
// Verifies: 09-PATH-6, 09-REQ-9.5
func TestADryRunSpendsTheRoundAndWritesNothing_TS_09_78(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, smokeFiles(), true)
	head, remote := strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "HEAD")), e.remoteBranches(t)

	code, env := e.fix(e.forge.IssueURL(7), append(scripted(script(t, fixSteps()...)), "--dry-run")...)

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	res := resultOf(t, env)
	if res["outcome"] != "dry_run" || res["rounds"] != float64(1) || res["verdict"] != "pass_was_already_failing" || res["branch"] != nil || res["commit"] != nil || res["pushed"] != false {
		t.Errorf("result = %v: the round was spent and measured, and nothing was made", res)
	}
	if u, _ := env["usage"].(map[string]any); u["brain"] != "scripted" {
		t.Errorf("usage = %v, want the brain that ran", env["usage"])
	}
	if writes := e.forge.Writes(); len(writes) != 0 {
		t.Errorf("%d write requests reached the forge: %v", len(writes), writes)
	}
	for _, kind := range []string{"branch", "commit", "push", "create_pull_request"} {
		if got := effects(env)[kind]; got != "planned" {
			t.Errorf("side effect %s is %q, want planned (all: %v)", kind, got, effects(env))
		}
	}
	if !slices.Equal(e.branches(), []string{"main"}) || strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "HEAD")) != head || !slices.Equal(e.remoteBranches(t), remote) {
		t.Errorf("branches %v, remote %v: a dry run makes none", e.branches(), e.remoteBranches(t))
	}
	e.onBase(t)
}

// TestAPersonFixesAtTheirOwnCheckout_TS_09_79 verifies TS-09-79 (09-PATH-7): fix brief gives
// the brief and the command to submit with, a file edited by hand is reported with fix
// submit, and fix land measures once and makes the branch with one commit, pushes it, opens
// the pull request and goes back to the base.
// Verifies: 09-PATH-7, 09-REQ-3.8, 09-REQ-1.7
func TestAPersonFixesAtTheirOwnCheckout_TS_09_79(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	base := strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "HEAD"))

	code, brief := e.brief(e.forge.IssueURL(7))

	res := resultOf(t, brief)
	if code != 0 || brief["status"] != "done" {
		t.Fatalf("brief: exit code %d, status %v, error %v", code, brief["status"], brief["error"])
	}
	if path, _ := res["brief_path"].(string); path == "" || !strings.HasSuffix(path, "brief.md") {
		t.Errorf("brief_path = %v", res["brief_path"])
	} else if _, err := os.Stat(path); err != nil {
		t.Error(err)
	}
	if argv, _ := res["submit_argv"].([]any); len(argv) < 4 || argv[1] != "fix" || argv[2] != "submit" {
		t.Errorf("submit_argv = %v", res["submit_argv"])
	}

	e.writeFile("src/greet.sh", "echo hello\n")
	e.writeFile("tests/hello_test.sh", helloTest)
	code, submitted := e.submitReport(brief, helloReport2)
	if code != 0 || submitted["status"] != "done" {
		t.Fatalf("submit: exit code %d, status %v, error %v", code, submitted["status"], submitted["error"])
	}

	code, landed := e.land(brief)

	if code != 0 || landed["status"] != "done" {
		t.Fatalf("land: exit code %d, status %v, error %v", code, landed["status"], landed["error"])
	}
	lres := resultOf(t, landed)
	branch, _ := lres["branch"].(string)
	if !strings.HasPrefix(branch, "fix/7-") || lres["outcome"] != "landed" || e.commitsAbove(t, base, branch) != "1" {
		t.Errorf("land result = %v", lres)
	}
	if !slices.Contains(e.remoteBranches(t), branch) || len(e.forge.PullRequests()) != 1 {
		t.Errorf("remote %v, pull requests %+v", e.remoteBranches(t), e.forge.PullRequests())
	}
	if lres["attempts"] != float64(0) || lres["rounds"] != float64(0) || lres["box"] != nil {
		t.Errorf("land result = %v: a land runs no brain", lres)
	}
	if n := slices.Index(ledgerKinds(t, runDirOf(e, brief)), "measure"); n < 0 || slices.Index(ledgerKinds(t, runDirOf(e, brief))[n+1:], "measure") >= 0 {
		t.Errorf("ledger kinds = %v, want the change measured once", ledgerKinds(t, runDirOf(e, brief)))
	}
	e.onBase(t)
}
