package codeimpl

import (
	"encoding/json"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/checks"
)

// TS-06-17 (unit): impl's summary view reduces each task to
// {id, outcome, commit, verdict} and omits top-level verification when the
// stopping verdict is landable (06-REQ-3.3).
func TestTS06_17_SummaryViewReducesTasksAndOmitsVerificationWhenLandable(t *testing.T) {
	r := &Result{
		Stage:          "landed",
		SpecDir:        ".specs/09_thing",
		SpecID:         "09_thing",
		SpecName:       "thing",
		Title:          "Thing",
		Status:         "active",
		Branch:         "impl/09-thing",
		TasksTotal:     2,
		TasksDone:      2,
		TasksSkipped:   0,
		TasksRemaining: 0,
		Tasks: []TaskReport{
			{
				ID: 1, Kind: "implement", Title: "one", Outcome: OutcomeDone,
				Commit: "aaa1111", Verdict: string(checks.VerdictPass),
				ChangedFiles: []string{"a.go"}, DiffStat: "1 file changed",
			},
			{
				ID: 2, Kind: "implement", Title: "two", Outcome: OutcomeDone,
				Commit: "bbb2222", Verdict: string(checks.VerdictPass),
			},
		},
		Gate:           []string{"go vet ./...", "go test ./..."},
		Verdict:        string(checks.VerdictPass),
		PullRequestURL: "https://github.com/o/r/pull/1",
		Verification:   GateResult{Checks: []checks.Result{{OK: true, Output: "ok"}}},
	}
	if !landable(r.Verdict, len(r.Gate) == 0) {
		t.Fatalf("fixture verdict %q must be landable", r.Verdict)
	}

	sv := r.SummaryView()
	b, err := json.Marshal(sv)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if v, ok := m["verification"]; ok {
		t.Errorf("expected no top-level verification for a landable verdict, got %v", v)
	}

	tasksAny, ok := m["tasks"].([]any)
	if !ok || len(tasksAny) != 2 {
		t.Fatalf("tasks = %v", m["tasks"])
	}
	wantKeys := map[string]bool{"id": true, "outcome": true, "commit": true, "verdict": true}
	for _, ta := range tasksAny {
		task, ok := ta.(map[string]any)
		if !ok {
			t.Fatalf("task entry is not an object: %#v", ta)
		}
		for k := range task {
			if !wantKeys[k] {
				t.Errorf("unexpected key %q in task summary: %v", k, task)
			}
		}
		for k := range wantKeys {
			if _, ok := task[k]; !ok {
				t.Errorf("task summary is missing %q: %v", k, task)
			}
		}
	}

	for _, key := range []string{
		"stage", "spec_dir", "spec_id", "spec_name", "title", "status", "branch",
		"tasks_total", "tasks_done", "tasks_skipped", "tasks_remaining",
		"verdict", "pull_request_url", "detail",
	} {
		if _, ok := m[key]; !ok {
			t.Errorf("summary view is missing %q: %v", key, m)
		}
	}
}

// TS-06-18 (unit): impl's summary view includes the top-level verification
// when the stopping verdict is not landable (06-REQ-3.3).
func TestTS06_18_SummaryViewIncludesTopLevelVerificationWhenNotLandable(t *testing.T) {
	r := &Result{
		Stage:   "parked",
		Verdict: string(checks.VerdictRegressed),
		Gate:    []string{"go test ./..."},
		Verification: GateResult{
			Checks: []checks.Result{{OK: false, ExitCode: 1, Output: "FAIL: TestY"}},
		},
	}
	if landable(r.Verdict, len(r.Gate) == 0) {
		t.Fatalf("fixture verdict %q must not be landable", r.Verdict)
	}

	sv := r.SummaryView()
	b, err := json.Marshal(sv)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	vAny, ok := m["verification"]
	if !ok {
		t.Fatal("expected a top-level verification field for a non-landable verdict")
	}
	v, ok := vAny.(map[string]any)
	if !ok {
		t.Fatalf("verification is not an object: %T", vAny)
	}
	checksAny, ok := v["checks"].([]any)
	if !ok || len(checksAny) == 0 {
		t.Fatalf("expected verification.checks to carry the gate's checks, got %v", v["checks"])
	}
}
