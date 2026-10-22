package afcmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/triage"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// diagnosisPayload is a diagnosis that is valid against the repository of
// newTriageEnv, as the JSON a brain would send.
func diagnosisPayload(mutate func(map[string]any)) string {
	m := map[string]any{
		"title": "Run starts the wrong thing", "severity": "high", "confidence": "probable",
		"root_cause":          "Run calls Help before it checks its arguments.",
		"affected_files":      []any{map[string]any{"path": "main.go", "line": 6, "note": "Run"}, map[string]any{"path": "util/util.go", "note": "Help"}},
		"suggested_fix":       "Check the arguments first.",
		"acceptance_criteria": []any{"Run with no arguments prints the usage"},
	}
	if mutate != nil {
		mutate(m)
	}
	data, _ := json.Marshal(m)
	return string(data)
}

// briefRun makes the run directory of an attended session: af triage brief on
// issue 7, and its run id.
func (e *triageEnv) briefRun() string {
	e.t.Helper()
	code, env := e.run("", "triage", "brief", e.forge.IssueURL(7), "--dir", e.dir)
	if code != 0 {
		e.t.Fatalf("triage brief: exit code %d, error %v", code, env["error"])
	}
	return env["run_id"].(string)
}

func (e *triageEnv) submit(id, payload string) (int, map[string]any) {
	e.t.Helper()
	return e.run(payload, "triage", "submit", "--run-id", id, "--dir", e.dir)
}

func (e *triageEnv) submissionFile(id string) string {
	return filepath.Join(e.dir, ".agent-fox", "run", id, "submission.json")
}

// inProcess submits the payload the way a brain's file_diagnosis tool does: the
// submit core over the tree, with the kind of the triage run.
func (e *triageEnv) inProcess(id, payload string) error {
	e.t.Helper()
	root, err := os.OpenRoot(e.dir)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	target := submission.Target{Tree: root.FS(), Write: submission.RootWriter(root), RunID: id}
	_, err = submission.Submit(context.Background(), target, triage.Kind(e.dir), []byte(payload), time.Now())
	return err
}

// problemsOf are the problems of a refusal, as the lines they print as.
func problemsOf(t *testing.T, err error) []string {
	t.Helper()
	refused, ok := err.(*submission.Refused)
	if !ok {
		t.Fatalf("error = %v (%T), want a refusal", err, err)
	}
	var out []string
	for _, p := range refused.Problems {
		out = append(out, p.String())
	}
	return out
}

// threeProblems is a payload with a bad severity, a path that is not there and a
// criterion that is empty.
func threeProblems() string {
	return diagnosisPayload(func(m map[string]any) {
		m["severity"] = "urgent"
		m["affected_files"] = []any{map[string]any{"path": "nowhere.go", "note": "gone"}}
		m["acceptance_criteria"] = []any{""}
	})
}

// TestTheSameProblemsComeFromEveryCaller_TS_08_31 verifies TS-08-31:
// the same payload and tree give the same problems through af triage submit, the
// in-process submit and the validation the host makes again.
// Verifies: 08-REQ-5.1
func TestTheSameProblemsComeFromEveryCaller_TS_08_31(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.briefRun()
	payload := threeProblems()

	code, env := e.submit(id, payload)
	inProcess := problemsOf(t, e.inProcess(id, payload))

	// The host's second look: the tree as the landing and the loop build it, and the function they ask.
	files, err := checks.FileView(context.Background(), e.dir)
	if err != nil {
		t.Fatal(err)
	}
	hostReason := triage.StaleReason([]byte(payload), triage.Tree{
		Roots: []string{e.dir}, Files: files,
		Regular: func(p string) bool { return triage.RegularFile(e.dir, p) }, Lines: triage.HostLines(e.dir),
	})
	if code != 1 || env["status"] != "failed" || len(inProcess) != 3 {
		t.Fatalf("exit code %d, status %v, in-process problems %v", code, env["status"], inProcess)
	}
	if want := strings.Join(inProcess, "; "); hostReason != want {
		t.Errorf("in-process problems:\n%v\nhost problems:\n%v", want, hostReason)
	}
	message := env["error"].(map[string]any)["message"].(string)
	at := 0
	for _, p := range inProcess {
		i := strings.Index(message[at:], p)
		if i < 0 {
			t.Errorf("the message of the command lacks the problem %q, or has it out of order:\n%s", p, message)
			break
		}
		at += i + len(p)
	}
}

// TestARefusalListsEveryProblemAndKeepsTheEarlierSubmission_TS_08_37 verifies TS-08-37:
// a payload with three problems is refused with all three, by the command as
// failed/submission and retryable and in-process as the error of the tool, and
// an earlier valid submission is as it was.
// Verifies: 08-REQ-5.7
func TestARefusalListsEveryProblemAndKeepsTheEarlierSubmission_TS_08_37(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.briefRun()
	if code, env := e.submit(id, diagnosisPayload(nil)); code != 0 {
		t.Fatalf("the first submission: exit code %d, error %v", code, env["error"])
	}
	before := readFile(t, e.submissionFile(id))

	code, env := e.submit(id, threeProblems())

	errObj, _ := env["error"].(map[string]any)
	if code != 1 || env["status"] != "failed" || errObj["category"] != "submission" || errObj["retryable"] != true || env["result"] != nil {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], errObj)
	}
	message := errObj["message"].(string)
	for _, pointer := range []string{"/severity", "/affected_files/0/path", "/acceptance_criteria/0"} {
		if !strings.Contains(message, pointer) {
			t.Errorf("the message lacks %s:\n%s", pointer, message)
		}
	}
	if !strings.Contains(message, `"nowhere.go"`) {
		t.Errorf("the message does not name the missing path:\n%s", message)
	}
	if got := problemsOf(t, e.inProcess(id, threeProblems())); len(got) != 3 {
		t.Errorf("in-process problems = %v, want all three", got)
	}
	if after := readFile(t, e.submissionFile(id)); string(after) != string(before) {
		t.Error("the earlier submission was changed by the refusals")
	}
}

// TestAnAcceptedSubmitStoresTheNormalizedFormAndReturnsItsResult_TS_08_38 verifies TS-08-38:
// af triage submit stores the diagnosis with its paths cleaned and path:N split,
// returns {run_id, accepted, submission, rejected_path_calls} and records
// triage.submission.
// Verifies: 08-REQ-5.8
func TestAnAcceptedSubmitStoresTheNormalizedFormAndReturnsItsResult_TS_08_38(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.briefRun()
	payload := diagnosisPayload(func(m map[string]any) {
		m["affected_files"] = []any{map[string]any{"path": "./main.go:6", "note": "Run"}, map[string]any{"path": "./util//util.go", "note": "Help"}}
	})

	code, env := e.submit(id, payload)

	if code != 0 || env["status"] != "done" || env["tool"] != "triage submit" {
		t.Fatalf("exit code %d, status %v, tool %v, error %v", code, env["status"], env["tool"], env["error"])
	}
	result := resultOf(t, env)
	if result["run_id"] != id || result["accepted"] != true || result["submission"] != e.submissionFile(id) || result["rejected_path_calls"] != float64(0) {
		t.Errorf("result = %v, want run_id %s, accepted, submission %s and 0 rejected path calls", result, id, e.submissionFile(id))
	}
	var file struct {
		Payload triage.Diagnosis
	}
	if err := json.Unmarshal(readFile(t, e.submissionFile(id)), &file); err != nil {
		t.Fatal(err)
	}
	if got := file.Payload.AffectedFiles; len(got) != 2 || got[0] != (triage.AffectedFile{Path: "main.go", Line: 6, Note: "Run"}) || got[1].Path != "util/util.go" {
		t.Errorf("affected files = %+v, want main.go:6 and util/util.go", got)
	}
	if arts := artifactsOf(env, "submission"); len(arts) != 1 || arts[0]["path"] != e.submissionFile(id) {
		t.Errorf("artifacts = %v", env["artifacts"])
	}
	if kinds := ledgerKinds(t, filepath.Join(e.dir, ".agent-fox", "run", id)); !slices.Contains(kinds, "triage.submission") {
		t.Errorf("ledger = %v, want triage.submission", kinds)
	}
	if _, err := os.Stat(filepath.Join(e.dir, ".agent-fox", "run", id, "run.json")); err != nil {
		t.Errorf("run.json: %v: an attended submit places the file the protocol reads", err)
	}
}

// TestEachRefusedCallAppendsOneLineBesideTheSubmission_TS_08_39 verifies TS-08-39:
// through the command and in-process, two refused calls add two lines of
// {ts, problems, unresolved_paths} to rejections.jsonl, an accepted one none; the
// count in the result is of the lines with an unresolved path.
// Verifies: 08-REQ-6.1
func TestEachRefusedCallAppendsOneLineBesideTheSubmission_TS_08_39(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.briefRun()
	rejections := filepath.Join(e.dir, ".agent-fox", "run", id, "rejections.jsonl")
	lines := func() []string {
		data, err := os.ReadFile(rejections)
		if err != nil {
			return nil
		}
		return strings.Split(strings.TrimSpace(string(data)), "\n")
	}

	e.submit(id, diagnosisPayload(func(m map[string]any) { m["severity"] = "urgent" }))
	_ = e.inProcess(id, threeProblems())
	if got := lines(); len(got) != 2 {
		t.Fatalf("rejections.jsonl has %d lines after two refused calls:\n%s", len(got), readFile(t, rejections))
	}
	for _, line := range lines() {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil || len(m) != 3 || m["ts"] == nil || m["problems"] == nil || m["unresolved_paths"] == nil {
			t.Errorf("line %q: want an object of ts, problems and unresolved_paths (%v)", line, err)
		}
	}

	code, env := e.submit(id, diagnosisPayload(nil))

	if code != 0 || len(lines()) != 2 {
		t.Fatalf("exit code %d, %d lines: an accepted call adds none", code, len(lines()))
	}
	if got := resultOf(t, env)["rejected_path_calls"]; got != float64(1) {
		t.Errorf("rejected_path_calls = %v, want 1: only the second call had a path that did not resolve", got)
	}
	if !strings.Contains(lines()[1], `"unresolved_paths":["nowhere.go"]`) || !strings.Contains(lines()[0], `"unresolved_paths":[]`) {
		t.Errorf("lines:\n%s", strings.Join(lines(), "\n"))
	}
	if dirOfSubmission, dirOfRejections := filepath.Dir(e.submissionFile(id)), filepath.Dir(rejections); dirOfSubmission != dirOfRejections {
		t.Errorf("rejections.jsonl is in %s, submission.json in %s", dirOfRejections, dirOfSubmission)
	}
}

// TestTheSubmitSchemaMatchesItsResult guards 08-REQ-10.4 for af triage submit.
func TestTheSubmitSchemaMatchesItsResult(t *testing.T) {
	checkSchema(t, "triage_submit", triageSubmitResult{})
}
