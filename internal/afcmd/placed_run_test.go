package afcmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/provider/faux"

	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// prefixText is the text of the prefix of a request to the model: the part of the
// brief that every request of a run carries ahead of the messages.
func prefixText(req core.Request) string {
	var parts []string
	for _, m := range req.Prefix {
		if u, ok := m.(core.UserMessage); ok {
			parts = append(parts, u.Content.Text())
		}
	}
	return strings.Join(parts, "")
}

// placeRun makes the run directory that brain.Execute places in a box: the brief, and
// a run.json with the attempt, the nonce and the length of the prefix, and no file of
// the host's run state. It returns the document and the directory.
func placeRun(t *testing.T, dir, id, kind string, attempt int, nonce, prefix, task string) (document, runDir string) {
	t.Helper()
	runDir = filepath.Join(dir, ".agent-fox", "run", id)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	document = prefix + task
	if err := os.WriteFile(filepath.Join(runDir, "brief.md"), []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}
	run, err := json.Marshal(submission.Run{RunID: id, Kind: kind, Attempt: attempt, Nonce: nonce, PrefixBytes: len(prefix)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), run, 0o644); err != nil {
		t.Fatal(err)
	}
	return document, runDir
}

// accepted is the submission file of a run directory, decoded.
func accepted(t *testing.T, runDir string) submission.File {
	t.Helper()
	var file submission.File
	if err := json.Unmarshal(readFile(t, filepath.Join(runDir, "submission.json")), &file); err != nil {
		t.Fatal(err)
	}
	return file
}

// TestTriageRunTakesTheBriefAndThePrefixFromRunJSON_TS_10_58 verifies the triage half of
// TS-10-58: over a run directory with brief.md and a run.json that has prefix_bytes,
// and no triage.json, af triage run takes the brief and where its prefix ends from
// run.json, and submits under the attempt and the nonce that run.json holds, so that
// the process that placed it accepts the submission.
// Verifies: 10-REQ-9.6
func TestTriageRunTakesTheBriefAndThePrefixFromRunJSON_TS_10_58(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.briefRun()
	runDir := filepath.Join(e.dir, ".agent-fox", "run", id)
	document := string(readFile(t, filepath.Join(runDir, "brief.md")))
	var facts struct{ Prefix struct{ Bytes int } }
	if err := json.Unmarshal(readFile(t, filepath.Join(runDir, "triage.json")), &facts); err != nil {
		t.Fatal(err)
	}
	prefix, task := document[:facts.Prefix.Bytes], document[facts.Prefix.Bytes:]
	for _, name := range []string{"triage.json", "submission.json"} {
		_ = os.Remove(filepath.Join(runDir, name))
	}
	placeRun(t, e.dir, id, "triage", 2, "0123456789abcdef", prefix, task)
	p := faux.New(fauxUsage(call("c1", "file_diagnosis", diagnosisPayload(nil)), 0.3, 900, 120))
	fauxBrains(t, p)

	code, env := e.run("", "triage", "run", "--run-id", id, "--dir", e.dir)

	if code != 0 || env["status"] != "done" || resultOf(t, env)["submitted"] != true {
		t.Fatalf("exit code %d, status %v, error %v, result %v", code, env["status"], env["error"], env["result"])
	}
	reqs := p.Requests()
	if len(reqs) != 1 || prefixText(reqs[0]) != prefix {
		t.Fatalf("%d requests, with the prefix %q; want one, with the first %d bytes of brief.md", len(reqs), prefixTextOf(reqs), len(prefix))
	}
	if file := accepted(t, runDir); file.Nonce != "0123456789abcdef" || file.Attempt != 2 || file.Kind != "triage" {
		t.Errorf("submission = %+v, want it made under the attempt and the nonce of run.json", file)
	}
	var placed submission.Run
	if err := json.Unmarshal(readFile(t, filepath.Join(runDir, "run.json")), &placed); err != nil || placed.Nonce != "0123456789abcdef" || placed.Attempt != 2 || placed.PrefixBytes != len(prefix) {
		t.Errorf("run.json = %+v, %v, want what was placed", placed, err)
	}
	if usage, _ := env["usage"].(map[string]any); usage["cost_source"] != "measured" || usage["turns"] != float64(1) {
		t.Errorf("usage = %v, want the measured cost of the turn: the process that placed the run reads it", usage)
	}
}

func prefixTextOf(reqs []core.Request) string {
	if len(reqs) == 0 {
		return ""
	}
	return prefixText(reqs[0])
}

// TestARunDirectoryWithNeitherStateNorAPrefixIsNoRun guards the refusal that stays:
// without triage.json, and a run.json that says where the prefix ends, there is no
// brief to split.
func TestARunDirectoryWithNeitherStateNorAPrefixIsNoRun(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.briefRun()
	runDir := filepath.Join(e.dir, ".agent-fox", "run", id)
	_ = os.Remove(filepath.Join(runDir, "triage.json"))
	fauxBrains(t, faux.New())
	for name, runJSON := range map[string]string{
		"no prefix_bytes":     `{"run_id":"` + id + `","kind":"triage","attempt":1,"nonce":"0123456789abcdef"}`,
		"a prefix beyond it":  `{"run_id":"` + id + `","kind":"triage","attempt":1,"nonce":"0123456789abcdef","prefix_bytes":99999999}`,
		"another kind":        `{"run_id":"` + id + `","kind":"fix_report","attempt":1,"nonce":"0123456789abcdef","prefix_bytes":10}`,
		"another run":         `{"run_id":"20260101_000000_abcdef","kind":"triage","attempt":1,"nonce":"0123456789abcdef","prefix_bytes":10}`,
		"attempt 0":           `{"run_id":"` + id + `","kind":"triage","attempt":0,"nonce":"0123456789abcdef","prefix_bytes":10}`,
		"a nonce that is not": `{"run_id":"` + id + `","kind":"triage","attempt":1,"nonce":"not hex","prefix_bytes":10}`,
	} {
		if err := os.WriteFile(filepath.Join(runDir, "run.json"), []byte(runJSON), 0o644); err != nil {
			t.Fatal(err)
		}

		code, env := e.run("", "triage", "run", "--run-id", id, "--dir", e.dir)

		errObj, _ := env["error"].(map[string]any)
		if code != 2 || errObj["category"] != "usage" {
			t.Errorf("%s: exit code %d, error %v, want a usage failure", name, code, env["error"])
		}
	}
}

// TestFixRunWithNoStateRunsTheDriverOnThePlacedBrief_TS_10_58 verifies the fix half of
// TS-10-58: over a run directory with brief.md and a run.json that has prefix_bytes, and
// no fix.json, af fix run is the driver of the box: it takes the brief and where its
// prefix ends from run.json, opens no box and applies nothing, submits under the
// attempt and the nonce that run.json holds, and says that it submitted.
// Verifies: 10-REQ-9.6
func TestFixRunWithNoStateRunsTheDriverOnThePlacedBrief_TS_10_58(t *testing.T) {
	requireMake(t)
	e := newFixEnv(t, fakeforge.GitHub)
	const id = "20261021_150000_abcdef"
	prefix := "PREFIX: the mandate, the skill and the report\n"
	task := "TASK: fix the greeting\n"
	_, runDir := placeRun(t, e.dir, id, "fix_report", 3, "fedcba9876543210", prefix, task)
	p := faux.New(fauxUsage(call("c1", "submit_fix", helloReport), 0.2, 700, 90))
	fixFauxBrains(t, p)
	before := snapshot(t, e.dir)

	code, env := e.run("", "fix", "run", "--run-id", id, "--dir", e.dir, "--sandbox", "local")

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	res := resultOf(t, env)
	if res["run_id"] != id || res["round"] != float64(3) || res["submission"] != "report" || res["submitted"] != true || res["apply"] != nil || res["question"] != nil {
		t.Errorf("result = %v, want round 3 (the attempt of run.json), a report, submitted, and nothing applied", res)
	}
	reqs := p.Requests()
	if len(reqs) != 1 || prefixText(reqs[0]) != prefix {
		t.Fatalf("%d requests, with the prefix %q; want one, with the first %d bytes of brief.md", len(reqs), prefixTextOf(reqs), len(prefix))
	}
	if !strings.Contains(allUserText(reqs[0]), "TASK: fix the greeting") {
		t.Errorf("the model was not given the task block of brief.md:\n%s", allUserText(reqs[0]))
	}
	file := accepted(t, runDir)
	if file.Nonce != "fedcba9876543210" || file.Attempt != 3 || file.Kind != "fix_report" {
		t.Errorf("submission = %+v, want it made under the attempt and the nonce of run.json", file)
	}
	if usage, _ := env["usage"].(map[string]any); usage["cost_source"] != "measured" || usage["turns"] != float64(1) {
		t.Errorf("usage = %v, want the measured cost of the turn", usage)
	}
	// Nothing was opened, applied or written outside the run directory: the box is the
	// process's own directory, and the round of the host is not this.
	if _, err := os.Stat(filepath.Join(runDir, "fix.json")); err == nil {
		t.Error("the driver wrote fix.json: it would make the next fix run in this directory a round of the host")
	}
	after := snapshot(t, e.dir)
	for path := range after {
		if _, ok := before[path]; !ok && !strings.HasPrefix(path, ".agent-fox/") {
			t.Errorf("%s appeared in the checkout", path)
		}
	}
	for path, content := range before {
		if after[path] != content {
			t.Errorf("%s changed", path)
		}
	}
}

// TestFixRunWithNoStateAndNoPrefixIsNoRun guards the refusal of a directory that is no
// fix run: neither fix.json nor a run.json that says where the prefix ends.
func TestFixRunWithNoStateAndNoPrefixIsNoRun(t *testing.T) {
	requireMake(t)
	e := newFixEnv(t, fakeforge.GitHub)
	const id = "20261021_150000_abcdef"
	_, runDir := placeRun(t, e.dir, id, "fix_report", 1, "fedcba9876543210", "P", "T")
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), []byte(`{"run_id":"`+id+`","kind":"fix_report","attempt":1,"nonce":"fedcba9876543210"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fixFauxBrains(t, faux.New())

	code, env := e.run("", "fix", "run", "--run-id", id, "--dir", e.dir, "--sandbox", "local")

	errObj, _ := env["error"].(map[string]any)
	if code != 2 || errObj["category"] != "usage" || !strings.Contains(errObj["message"].(string), "fix.json") {
		t.Errorf("exit code %d, error %v, want the usage failure that names fix.json", code, env["error"])
	}
}

// TestAFixRunWithStateIsTheRoundOfTheHostEvenIfRunJSONHasAPrefix guards the line between
// the two: under the none provider brain.Execute writes prefix_bytes into the run.json of
// the host's own run directory, and a run that has fix.json is still the one the host
// runs a round of, with a box of its own, whatever its run.json says.
func TestAFixRunWithStateIsTheRoundOfTheHostEvenIfRunJSONHasAPrefix(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	_, brief := e.brief(e.forge.IssueURL(7))
	runDir := runDirOf(e, brief)
	var run submission.Run
	if err := json.Unmarshal(readFile(t, filepath.Join(runDir, "run.json")), &run); err != nil {
		t.Fatal(err)
	}
	run.PrefixBytes = 10
	data, _ := json.Marshal(run)
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	code, env := e.fixRun(brief, "--brain", "scripted", "--script", script(t, writeStep("src/greet.sh", "echo hello\n"), submitStep(helloReport, "accepted")), "--sandbox", "local")

	if code != 0 {
		t.Fatalf("exit code %d, error %v", code, env["error"])
	}
	apply, _ := resultOf(t, env)["apply"].(map[string]any)
	if apply["written"] != float64(1) || resultOf(t, env)["submitted"] != true {
		t.Errorf("result = %v, want the round of the host, which applied the edit of the box", env["result"])
	}
	if got, _ := os.ReadFile(filepath.Join(e.dir, "src", "greet.sh")); string(got) != "echo hello\n" {
		t.Errorf("src/greet.sh = %q, want the edit applied to the checkout", got)
	}
}
