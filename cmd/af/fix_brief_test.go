package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// redFixFiles is the fixture gate with a script that fails its test at HEAD.
func redFixFiles() map[string]string {
	files := map[string]string{}
	for k, v := range fixFiles {
		files[k] = v
	}
	files["src/greet.sh"] = "echo helo\n"
	return files
}

// result is the result object of an envelope.
func result(t *testing.T, env map[string]any) map[string]any {
	t.Helper()
	res, _ := env["result"].(map[string]any)
	if res == nil {
		t.Fatalf("no result in %v", env)
	}
	return res
}

// runDirOf is the run directory of an envelope's run.
func runDirOf(e *fixEnv, env map[string]any) string {
	id, _ := env["run_id"].(string)
	if id == "" {
		e.t.Fatalf("no run id in the envelope %v", env)
	}
	return filepath.Join(e.dir, ".agent-fox", "run", id)
}

// countGate makes the baseline of a run count the gate runs and keep their options.
func countGate(t *testing.T) *[]checks.Options {
	t.Helper()
	var seen []checks.Options
	old := fixGate
	fixGate = func(ctx context.Context, dir string, o checks.Options) (checks.Result, error) {
		seen = append(seen, o)
		return checks.Run(ctx, dir, o)
	}
	t.Cleanup(func() { fixGate = old })
	return &seen
}

// brief runs af fix brief on the input, in the checkout.
func (e *fixEnv) brief(input string, args ...string) (int, map[string]any) {
	e.t.Helper()
	return e.run("", append([]string{"fix", "brief", input, "--dir", e.dir}, args...)...)
}

// TestFixBriefRecordsTheBaseTheBaselineAndTheWarnings_TS_09_16 verifies TS-09-16:
// the full hash of the base, one run of the gate in the default mode with no label,
// the warnings of a red baseline and a closed issue, and the baseline in the result.
// Verifies: 09-REQ-2.7
func TestFixBriefRecordsTheBaseTheBaselineAndTheWarnings_TS_09_16(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	e.forge.AddIssue(fakeforge.Issue{Number: 9, Title: "Old", Body: "The greeting is wrong.", State: "closed"})
	seen := countGate(t)

	code, env := e.brief(e.forge.IssueURL(9))

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	res := result(t, env)
	head := strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "HEAD"))
	if got, _ := res["base_head"].(string); !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(got) || got != head || res["base"] != "main" {
		t.Errorf("base %v, base_head %v, want main at the full hash %s", res["base"], res["base_head"], head)
	}
	if len(*seen) != 1 || (*seen)[0].Label != "" || (*seen)[0].Hermetic || (*seen)[0].NoCache {
		t.Errorf("the baseline gate ran %d times with options %+v, want once in the default mode with no label", len(*seen), *seen)
	}
	baseline, _ := res["baseline"].(map[string]any)
	if baseline["gate_id"] == "" || baseline["gate_id"] == nil || baseline["passed"] != false {
		t.Errorf("baseline = %v, want a gate id and passed false", baseline)
	}
	if got := warningCodes(env); !slices.Contains(got, "baseline_failing") || !slices.Contains(got, "issue_closed") {
		t.Errorf("warnings = %v, want baseline_failing and issue_closed", got)
	}
}

// TestFixJSONHasTheInputKeyOfEveryKindOfInput_TS_09_17 verifies TS-09-17 at the
// command: fix.json holds every field, the key of an issue is its URL, of a file
// its absolute path and of a text the SHA-256 of the text, and no temporary file is
// left beside it.
// Verifies: 09-REQ-2.8
func TestFixJSONHasTheInputKeyOfEveryKindOfInput_TS_09_17(t *testing.T) {
	requireMake(t)
	e := newFixEnv(t, fakeforge.GitHub)
	report := filepath.Join(t.TempDir(), "bug.md")
	if err := os.WriteFile(report, []byte("The greeting says helo.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const text = "the greeting says helo"
	sum := sha256.Sum256([]byte(text))

	for name, tc := range map[string]struct{ input, kind, origin, key string }{
		"issue": {e.forge.IssueURL(7), "issue", e.forge.IssueURL(7), e.forge.IssueURL(7)},
		"file":  {report, "file", report, report},
		"text":  {text, "text", "", hex.EncodeToString(sum[:])},
	} {
		t.Run(name, func(t *testing.T) {
			args := []string{"--context", "A", "--context", "B"}
			if tc.kind != "issue" {
				args = append(args, "--repo", "o/r") // an issue names its repository, a report does not
			}
			code, env := e.brief(tc.input, args...)
			if code != 0 {
				t.Fatalf("exit code %d, error %v", code, env["error"])
			}
			dir := runDirOf(e, env)
			data, err := os.ReadFile(filepath.Join(dir, "fix.json"))
			if err != nil {
				t.Fatal(err)
			}
			var fj map[string]any
			if err := json.Unmarshal(data, &fj); err != nil {
				t.Fatal(err)
			}
			for _, k := range []string{"run_id", "input", "issue", "target", "base", "base_head", "table", "baseline_gate_id", "no_verify", "prefix_sha256", "context", "rounds"} {
				if _, ok := fj[k]; !ok {
					t.Errorf("fix.json has no %s: %s", k, data)
				}
			}
			input, _ := fj["input"].(map[string]any)
			if input["kind"] != tc.kind || input["origin"] != tc.origin || input["key"] != tc.key {
				t.Errorf("input = %v, want kind %s, origin %q, key %q", input, tc.kind, tc.origin, tc.key)
			}
			if fj["run_id"] != env["run_id"] || fj["base"] != "main" || fj["no_verify"] != false || fj["rounds"] != float64(0) {
				t.Errorf("fix.json = %s", data)
			}
			// An issue names the host of its repository, --repo leaves it to the default.
			if target, _ := fj["target"].(map[string]any); target["repo"] != "o/r" || (tc.kind == "issue") == (target["host"] == "") {
				t.Errorf("target = %v, want the repository o/r, with its host for an issue", fj["target"])
			}
			if ctx, _ := fj["context"].([]any); len(ctx) != 2 || ctx[0] != "A" || ctx[1] != "B" {
				t.Errorf("context = %v, want A and B", fj["context"])
			}
			if tc.kind == "issue" {
				issue, _ := fj["issue"].(map[string]any)
				if issue["url"] != tc.origin || issue["title"] != "Greeting is wrong" {
					t.Errorf("issue = %v, want its URL and title", issue)
				}
			} else if fj["issue"] != nil {
				t.Errorf("issue = %v, want null for %s input", fj["issue"], tc.kind)
			}
			if ms, _ := fj["baseline_ms"].(float64); ms <= 0 || fj["baseline_passed"] != true || fj["baseline_gate_id"] == "" {
				t.Errorf("baseline_gate_id %v, baseline_passed %v, baseline_ms %v, want the gate that was run, passed, and how long it took", fj["baseline_gate_id"], fj["baseline_passed"], fj["baseline_ms"])
			}
			if table, _ := fj["table"].(map[string]any); len(table["checks"].([]any)) == 0 {
				t.Errorf("table = %v, want the pinned table", fj["table"])
			}
			entries, _ := os.ReadDir(dir)
			for _, entry := range entries {
				if strings.Contains(entry.Name(), "tmp") {
					t.Errorf("%s was left in the run directory", entry.Name())
				}
			}
		})
	}
}

// snapshot is the names and contents of the files of a checkout other than .git and
// .agent-fox.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() && (rel == ".git" || rel == ".agent-fox") {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			data, err := os.ReadFile(path)
			files[rel] = string(data)
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestFixBriefWritesTheBriefAndRunJSONAndNothingElse_TS_09_25 verifies TS-09-25.
// Verifies: 09-REQ-3.8
func TestFixBriefWritesTheBriefAndRunJSONAndNothingElse_TS_09_25(t *testing.T) {
	requireMake(t)
	e := newFixEnv(t, fakeforge.GitHub)
	before := snapshot(t, e.dir)
	branches := e.branches()
	head := gitDo(t, e.dir, "rev-parse", "HEAD")

	code, env := e.brief(e.forge.IssueURL(7))

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	dir := runDirOf(e, env)
	var rj map[string]any
	data, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil || json.Unmarshal(data, &rj) != nil {
		t.Fatalf("run.json: %v, %s", err, data)
	}
	if rj["run_id"] != env["run_id"] || rj["kind"] != "fix_report" || rj["attempt"] != float64(1) || rj["nonce"] == nil || rj["nonce"] == "" {
		t.Errorf("run.json = %s, want the run id, kind fix_report, attempt 1 and a nonce", data)
	}
	briefMD, err := os.ReadFile(filepath.Join(dir, "brief.md"))
	if err != nil {
		t.Fatal(err)
	}
	res := result(t, env)
	if res["brief_path"] != filepath.Join(dir, "brief.md") {
		t.Errorf("brief_path = %v, want %s", res["brief_path"], filepath.Join(dir, "brief.md"))
	}
	fj := readFixJSON(t, dir)
	if res["prefix_sha256"] != fj["prefix_sha256"] || len(res["prefix_sha256"].(string)) != 64 {
		t.Errorf("prefix_sha256 = %v, fix.json has %v", res["prefix_sha256"], fj["prefix_sha256"])
	}
	// The prefix of the brief is the prefix whose hash was reported.
	if !strings.HasPrefix(string(briefMD), "# Fix mandate") || !strings.Contains(string(briefMD), "\n## Task\n") || !strings.Contains(string(briefMD), "af fix submit --run-id "+env["run_id"].(string)) {
		t.Errorf("brief.md does not hold the mandate and the task block of the run:\n%.600s", briefMD)
	}
	prefix, _, _ := strings.Cut(string(briefMD), "## Task\n")
	if sum := sha256.Sum256([]byte(prefix)); hex.EncodeToString(sum[:]) != res["prefix_sha256"] {
		t.Errorf("the hash of the prefix of brief.md is not prefix_sha256")
	}
	table, _ := res["table"].(map[string]any)
	if list, _ := table["checks"].([]any); len(list) == 0 {
		t.Errorf("table = %v, want the pinned table", res["table"])
	}
	submit, _ := res["submit_argv"].([]any)
	if len(submit) < 5 || submit[0] != "af" || submit[1] != "fix" || submit[2] != "submit" || !slices.Contains(submit, any(env["run_id"])) {
		t.Errorf("submit argv = %v, want af fix submit --run-id <id> …", submit)
	}
	baseline, _ := res["baseline"].(map[string]any)
	if baseline["passed"] != true || baseline["gate_id"] == "" {
		t.Errorf("baseline = %v, want a passed gate with an id", baseline)
	}

	if after := snapshot(t, e.dir); !equalMaps(before, after) {
		t.Errorf("the checkout changed outside .agent-fox: %v, now %v", before, after)
	}
	if status := gitDo(t, e.dir, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("git status = %q, want a clean tree: .agent-fox is ignored", status)
	}
	if !slices.Equal(e.branches(), branches) || gitDo(t, e.dir, "rev-parse", "HEAD") != head {
		t.Errorf("branches %v and HEAD changed", e.branches())
	}
	if n := len(e.forge.Writes()); n != 0 {
		t.Errorf("%d writes to the forge", n)
	}

	// The steps that follow are offered, and the brief is listed and noted.
	next, _ := env["next"].([]any)
	var steps []string
	for _, n := range next {
		argv, _ := n.(map[string]any)["argv"].([]any)
		if len(argv) > 3 {
			steps = append(steps, argv[1].(string)+" "+argv[2].(string)+" "+argv[3].(string)+" "+argv[4].(string))
		}
	}
	id := env["run_id"].(string)
	if !slices.Contains(steps, "fix submit --run-id "+id) || !slices.Contains(steps, "fix land --run-id "+id) {
		t.Errorf("next = %v, want fix submit and fix land of this run", steps)
	}
	var listed bool
	for _, a := range env["artifacts"].([]any) {
		if art := a.(map[string]any); art["kind"] == "brief" && art["path"] == filepath.Join(dir, "brief.md") {
			listed = true
		}
	}
	if ledger, _ := os.ReadFile(filepath.Join(dir, "ledger.jsonl")); !listed || !strings.Contains(string(ledger), `"fix.brief"`) {
		t.Errorf("the brief is listed: %v, and the ledger holds %s", listed, ledger)
	}
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// readFixJSON is the fix.json of a run directory.
func readFixJSON(t *testing.T, dir string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "fix.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fj map[string]any
	if err := json.Unmarshal(data, &fj); err != nil {
		t.Fatal(err)
	}
	return fj
}

// needsHuman puts an envelope that ends in a question in the run directory of an
// earlier run, as the loop will leave one.
func needsHuman(t *testing.T, dir, question string, options ...string) {
	t.Helper()
	var opts []map[string]string
	for i, o := range options {
		opts = append(opts, map[string]string{"id": string(rune('A' + i)), "label": o})
	}
	env := map[string]any{"status": "needs_human", "needs_human": map[string]any{"question": question, "options": opts}}
	data, _ := json.Marshal(env)
	if err := os.WriteFile(filepath.Join(dir, "envelope.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixTaskBlockOf is the part of brief.md after the prefix.
func fixTaskBlockOf(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "brief.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, task, ok := strings.Cut(string(data), "\n## Task\n")
	if !ok {
		t.Fatalf("brief.md has no task block:\n%s", data)
	}
	return task
}

// TestTheAnswerComesBackWithItsQuestionAndTheSamePrefix_TS_09_21 verifies TS-09-21
// and the prefix half of TS-09-77 at the command: the brief of a run with an answer
// holds the question an earlier run of the same input asked, with the answer, and has
// the prefix of that run.
// Verifies: 09-REQ-3.4, 09-REQ-3.1
func TestTheAnswerComesBackWithItsQuestionAndTheSamePrefix_TS_09_21(t *testing.T) {
	requireMake(t)
	e := newFixEnv(t, fakeforge.GitHub)
	url := e.forge.IssueURL(7)
	_, first := e.brief(url)
	needsHuman(t, runDirOf(e, first), "Which session?", "the old one", "the new one")
	// A question of the same input that an answer was already given to, and one of
	// another issue.
	e.forge.AddIssue(fakeforge.Issue{Number: 8, Title: "Another", Body: "Another problem."})
	_, other := e.brief(e.forge.IssueURL(8))
	needsHuman(t, runDirOf(e, other), "A question about another issue?", "yes", "no")

	code, env := e.brief(url, "--context", "B")

	if code != 0 {
		t.Fatalf("exit code %d, error %v", code, env["error"])
	}
	task := fixTaskBlockOf(t, runDirOf(e, env))
	for _, want := range []string{"Which session?", "A. the old one", "B. the new one", "\n```\nB\n```\n"} {
		if !strings.Contains(task, want) {
			t.Errorf("the task block lacks %q:\n%s", want, task)
		}
	}
	if strings.Contains(task, "another issue") {
		t.Errorf("the question of another issue is in the task block:\n%s", task)
	}
	if slices.Contains(warningCodes(env), "question_not_found") {
		t.Errorf("warnings = %v: the question was found", warningCodes(env))
	}
	if result(t, env)["prefix_sha256"] != result(t, first)["prefix_sha256"] {
		t.Errorf("prefix_sha256 %v, want the %v of the run it resumes", result(t, env)["prefix_sha256"], result(t, first)["prefix_sha256"])
	}
}

// TestAnAnswerWithNoQuestionIsRenderedAloneWithAWarning_TS_09_22 verifies TS-09-22.
// Verifies: 09-REQ-3.5
func TestAnAnswerWithNoQuestionIsRenderedAloneWithAWarning_TS_09_22(t *testing.T) {
	requireMake(t)
	e := newFixEnv(t, fakeforge.GitHub)

	code, env := e.brief(e.forge.IssueURL(7), "--context", "B")

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v: the run goes on", code, env["status"], env["error"])
	}
	task := fixTaskBlockOf(t, runDirOf(e, env))
	if !strings.Contains(task, "\n```\nB\n```\n") || strings.Contains(task, "The question you asked") {
		t.Errorf("the task block should hold the answer alone:\n%s", task)
	}
	if !slices.Contains(warningCodes(env), "question_not_found") {
		t.Errorf("warnings = %v, want question_not_found", warningCodes(env))
	}
}

// TestFixBriefRefusesWhatFixRefuses guards 09-REQ-1.5 and 09-REQ-2.2 for the brief:
// a pull or merge request, a base that is not checked out, and the flags of the loop
// that a brief does not take.
func TestFixBriefRefusesWhatFixRefuses(t *testing.T) {
	requireMake(t)
	for kind, urlOf := range map[string]func(*fixEnv) string{fakeforge.GitHub: (*fixEnv).pullURL, fakeforge.GitLab: (*fixEnv).mergeURL} {
		e := newFixEnv(t, kind)
		code, env := e.brief(urlOf(e))
		errObj := refusedBy(t, code, env, 2, "usage")
		if !strings.Contains(errObj["message"].(string), "pull or merge request") || len(e.forge.Writes()) != 0 {
			t.Errorf("%s: %v, %d writes", kind, errObj, len(e.forge.Writes()))
		}
	}

	e := newFixEnv(t, fakeforge.GitHub)
	code, env := e.brief(e.forge.IssueURL(7), "--base", "develop")
	if msg := refusedBy(t, code, env, 2, "usage")["message"].(string); !strings.Contains(msg, "check out develop first") {
		t.Errorf("message = %q, want it to say to check out develop first", msg)
	}
	for _, flag := range []string{"--land", "--attempts", "--no-verify", "--brain", "--dry-run"} {
		args := []string{flag}
		if flag != "--no-verify" && flag != "--dry-run" {
			args = append(args, "x")
		}
		if code, env := e.brief(e.forge.IssueURL(7), args...); code != 2 {
			t.Errorf("%s: exit code %d (%v), want a usage failure: a brief does not take it", flag, code, env["error"])
		}
	}
	dirty := newFixEnv(t, fakeforge.GitHub)
	if err := os.WriteFile(filepath.Join(dirty.dir, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, env = dirty.brief(dirty.forge.IssueURL(7))
	if msg := refusedBy(t, code, env, 1, "preflight")["message"].(string); !strings.Contains(msg, "dirty.txt") {
		t.Errorf("message = %q, want the dirty path", msg)
	}
}

// TestFixBriefNeedsNoBrainAndNoOrigin guards that the brief is made with no model
// key and no remote: it lands nothing and runs nothing.
func TestFixBriefNeedsNoBrainAndNoOrigin(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, fixFiles, false)

	code, env := e.brief(e.forge.IssueURL(7))

	if code != 0 || env["status"] != "done" {
		t.Errorf("exit code %d, status %v, error %v, want a brief with no origin and no key", code, env["status"], env["error"])
	}
}

// TestFixSubmitValidatesWithTheFixReportKindAndWritesTheNormalisedPayload_TS_09_7
// verifies TS-09-7.
// Verifies: 09-REQ-1.7
func TestFixSubmitValidatesWithTheFixReportKindAndWritesTheNormalisedPayload_TS_09_7(t *testing.T) {
	requireMake(t)
	e := newFixEnv(t, fakeforge.GitHub)
	_, brief := e.brief(e.forge.IssueURL(7))
	id := brief["run_id"].(string)
	// Files out of order, a CRLF in the change, and no type.
	report := `{"subject":"say hello","root_cause":"the script says helo","change":"line one\r\nline two","files":[{"path":"src/greet.sh","why":"fixes the word"},{"path":"Makefile","why":"nothing"}]}`

	code, env := e.run(report, "fix", "submit", "--run-id", id, "--dir", e.dir)

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	data, err := os.ReadFile(filepath.Join(runDirOf(e, brief), "submission.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Kind    string          `json:"kind"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &file); err != nil || file.Kind != "fix_report" {
		t.Fatalf("submission.json = %s (%v)", data, err)
	}
	var s struct {
		Type   string `json:"type"`
		Change string `json:"change"`
		Files  []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(file.Payload, &s); err != nil {
		t.Fatal(err)
	}
	if s.Type != "fix" || strings.Contains(s.Change, "\r") || s.Change != "line one\nline two" || len(s.Files) != 2 || s.Files[0].Path != "Makefile" || s.Files[1].Path != "src/greet.sh" {
		t.Errorf("payload = %s, want type fix, LF line ends and sorted files", file.Payload)
	}
	res := result(t, env)
	if res["run_id"] != id || res["kind"] != "fix_report" {
		t.Errorf("result = %v", res)
	}

	// A payload that is not valid writes no submission.
	_, other := e.brief(e.forge.IssueURL(7))
	code, env = e.run(`{"subject":"fix: say hello.","files":[]}`, "fix", "submit", "--run-id", other["run_id"].(string), "--dir", e.dir)
	errObj := refusedBy(t, code, env, 1, "submission")
	if errObj["retryable"] != true {
		t.Errorf("error = %v, want a refusal the brain can correct", errObj)
	}
	if _, err := os.Stat(filepath.Join(runDirOf(e, other), "submission.json")); err == nil {
		t.Error("an invalid payload wrote a submission")
	}
}
