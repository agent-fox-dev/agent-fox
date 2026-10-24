package afcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/provider/faux"

	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// noEnv is a host environment with nothing in it.
func noEnv(string) (string, bool) { return "", false }

// fauxBrains makes the brain of the commands that run one a brain on the faux
// provider, for the rest of the test.
func fauxBrains(t *testing.T, p *faux.Provider) {
	t.Helper()
	old := testConfig.Brain
	testConfig.Brain = brain.Options{Model: "faux-1", Getenv: noEnv, NewProvider: func(string) (core.ProviderClient, error) { return p, nil }}
	t.Cleanup(func() { testConfig.Brain = old })
}

// fauxUsage is a faux turn's usage with a cost.
func fauxUsage(turn faux.Turn, cost float64, in, out int64) faux.Turn {
	var u core.Usage
	u.SetField(core.UsageInputTokens, in)
	u.SetField(core.UsageOutputTokens, out)
	u.SetCost(cost)
	turn.Usage = u
	return turn
}

// call is a faux turn that calls the tool.
func call(id, name, args string) faux.Turn { return faux.ToolCall(id, name, args) }

// userText is the text of the first user message of a request.
func userText(req core.Request) string {
	for _, m := range req.Messages {
		if u, ok := m.(core.UserMessage); ok {
			return u.Content.Text()
		}
	}
	return ""
}

// TestRunExecutesTheAgentkitBrainInProcessOverDir_TS_08_30 verifies TS-08-30:
// af triage run runs the agentkit brain in this process over --dir, with no
// box, on the run's brief, and file_diagnosis submits into the run directory.
// Verifies: 08-REQ-4.7
func TestRunExecutesTheAgentkitBrainInProcessOverDir_TS_08_30(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.briefRun()
	p := faux.New(
		fauxUsage(call("c1", "read_file", `{"path":"main.go"}`), 0.1, 800, 100),
		fauxUsage(call("c2", "file_diagnosis", diagnosisPayload(nil)), 0.4, 1200, 300),
	)
	fauxBrains(t, p)
	status := git2(t, e.dir, "status", "--porcelain")

	code, env := e.run("", "triage", "run", "--run-id", id, "--dir", e.dir)

	if code != 0 || env["status"] != "done" || env["tool"] != "triage run" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	result := resultOf(t, env)
	if result["run_id"] != id || result["submitted"] != true {
		t.Errorf("result = %v, want run_id %s and submitted", result, id)
	}
	if _, err := os.Stat(e.submissionFile(id)); err != nil {
		t.Errorf("submission.json: %v", err)
	}
	usage, _ := env["usage"].(map[string]any)
	if usage["brain"] != "agentkit" || usage["turns"] != float64(2) || usage["cost_source"] != "measured" || usage["input_tokens"] != float64(2000) {
		t.Errorf("usage = %v, want the agentkit brain's measured cost of two turns", usage)
	}
	reqs := p.Requests()
	if len(reqs) != 2 {
		t.Fatalf("%d requests to the model, want 2", len(reqs))
	}
	var names []string
	for _, tool := range reqs[0].Tools {
		names = append(names, tool.Name)
	}
	for _, want := range []string{"read_file", "list_files", "find_files", "search_files", "file_outline", "find_symbol", "find_references", "file_diagnosis"} {
		if !contains(names, want) {
			t.Errorf("the model was offered %v, want %s", names, want)
		}
	}
	for _, banned := range []string{"write_file", "edit_file", "execute", "run_command"} {
		if contains(names, banned) {
			t.Errorf("the model was offered %s", banned)
		}
	}
	task := userText(reqs[0])
	if !strings.Contains(task, "## Task") || !strings.Contains(task, "file_diagnosis") || strings.Contains(task[strings.LastIndex(task, "## Task"):], "af triage submit") {
		t.Errorf("the task block the model got should name the tool and not the command:\n%s", task[strings.LastIndex(task, "## Task"):])
	}
	if got := git2(t, e.dir, "status", "--porcelain"); got != status {
		t.Errorf("git status changed:\n%q\n%q", status, got)
	}
	if kinds := ledgerKinds(t, filepath.Join(e.dir, ".agent-fox", "run", id)); !contains(kinds, "triage.submission") || !contains(kinds, "brain_start") {
		t.Errorf("ledger = %v, want triage.submission and the ledger of the brain", kinds)
	}
	if n := len(e.forge.Writes()); n != 0 {
		t.Errorf("%d writes to the forge", n)
	}
}

// TestARefusedPathCallIsTheBrainsNextTurn guards 08-REQ-6.2 through the command: a
// refusal that cites a path that is not there is the brain's next turn.
func TestARefusedPathCallIsTheBrainsNextTurn(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.briefRun()
	missing := diagnosisPayload(func(m map[string]any) {
		m["affected_files"] = []any{map[string]any{"path": "nowhere.go", "note": "x"}}
	})
	p := faux.New(call("c1", "file_diagnosis", missing), call("c2", "file_diagnosis", diagnosisPayload(nil)))
	fauxBrains(t, p)

	code, env := e.run("", "triage", "run", "--run-id", id, "--dir", e.dir)

	if code != 0 || resultOf(t, env)["submitted"] != true {
		t.Fatalf("exit code %d, result %v, error %v", code, env["result"], env["error"])
	}
	reqs := p.Requests()
	if len(reqs) != 2 {
		t.Fatalf("%d requests, want 2", len(reqs))
	}
	var results []string
	for _, m := range reqs[1].Messages {
		if tr, ok := m.(core.ToolResultMessage); ok {
			results = append(results, tr.Content.Text())
		}
	}
	if len(results) != 1 || !strings.Contains(results[0], "/affected_files/0/path") || !strings.Contains(results[0], `"nowhere.go"`) {
		t.Errorf("the second request's tool result = %v, want the refusal that names the path", results)
	}
}

// TestRunWithoutASubmissionFails guards 08-REQ-4.6 for the command: a brain that
// ends with nothing fails as no_result and leaves no submission.
func TestRunWithoutASubmissionFails(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	id := e.briefRun()
	fauxBrains(t, faux.New(faux.Text("I could not decide.")))

	code, env := e.run("", "triage", "run", "--run-id", id, "--dir", e.dir)

	errObj, _ := env["error"].(map[string]any)
	if code != 1 || env["status"] != "failed" || errObj["category"] != "no_result" || env["result"] != nil {
		t.Errorf("exit code %d, status %v, error %v", code, env["status"], errObj)
	}
	if _, err := os.Stat(e.submissionFile(id)); err == nil {
		t.Error("a submission exists")
	}
}

// TestRunWantsABriefAndValidBounds guards the arguments of af triage run.
func TestRunWantsABriefAndValidBounds(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	fauxBrains(t, faux.New(faux.Text("unused")))
	id := e.briefRun()
	for name, args := range map[string][]string{
		"a budget of 0":      {"triage", "run", "--run-id", id, "--dir", e.dir, "--budget", "0"},
		"no turns":           {"triage", "run", "--run-id", id, "--dir", e.dir, "--max-turns", "0"},
		"no run id":          {"triage", "run", "--dir", e.dir},
		"a run id of a path": {"triage", "run", "--run-id", "../x", "--dir", e.dir},
	} {
		t.Run(name, func(t *testing.T) {
			code, env := e.run("", args...)
			if code != 2 || env["status"] != "usage" {
				t.Errorf("exit code %d, status %v, error %v, want usage", code, env["status"], env["error"])
			}
		})
	}
	if err := os.Remove(filepath.Join(e.dir, ".agent-fox", "run", id, "brief.md")); err != nil {
		t.Fatal(err)
	}
	code, env := e.run("", "triage", "run", "--run-id", id, "--dir", e.dir)
	if code != 2 || env["status"] != "usage" {
		t.Errorf("a run with no brief: exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// TestRunHoldsTheBrainToTheDefaultBounds guards 08-REQ-4.2 for `af triage run`: with
// no flag the brain is told USD 2 and 100 turns, and given them; with flags, those.
func TestRunHoldsTheBrainToTheDefaultBounds(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"the defaults": {nil, "You have at most 100 turns, $2.00 of model cost and "},
		"the flags":    {[]string{"--budget", "3.5", "--max-turns", "40"}, "You have at most 40 turns, $3.50 of model cost and "},
	} {
		t.Run(name, func(t *testing.T) {
			e := newTriageEnv(t, fakeforge.GitHub)
			id := e.briefRun()
			p := faux.New(call("c1", "file_diagnosis", diagnosisPayload(nil)))
			fauxBrains(t, p)

			code, env := e.run("", append([]string{"triage", "run", "--run-id", id, "--dir", e.dir}, tc.args...)...)

			if code != 0 {
				t.Fatalf("exit code %d, error %v", code, env["error"])
			}
			if task := userText(p.Requests()[0]); !strings.Contains(task, tc.want) {
				t.Errorf("the task block does not hold %q:\n%s", tc.want, task[strings.LastIndex(task, "## Task"):])
			}
		})
	}
}
