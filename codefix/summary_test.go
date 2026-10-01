package codefix

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-06-15 (unit): fix's summary view omits verification when the run's
// verdict is landable (06-REQ-3.2).
func TestTS06_15_SummaryViewOmitsVerificationWhenLandable(t *testing.T) {
	r := &Result{
		Stage:           "landed",
		Branch:          "fix/stop-double-counting",
		BaseBranch:      "main",
		Commit:          "abc1234",
		ChangedFiles:    []string{"count.go"},
		Verdict:         string(checks.VerdictPass),
		CriteriaOutcome: "pass",
		PullRequestURL:  "https://github.com/o/r/pull/1",
		DryRun:          false,
		Verification:    checks.Result{OK: true, Output: "all tests passed"},
	}
	if !checks.Verdict(r.Verdict).Landable() {
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
		t.Errorf("expected no verification field for a landable verdict, got %v", v)
	}
	for _, key := range []string{
		"stage", "branch", "base_branch", "commit", "changed_files",
		"verdict", "criteria_outcome", "pull_request_url", "dry_run",
	} {
		if _, ok := m[key]; !ok {
			t.Errorf("summary view is missing %q: %v", key, m)
		}
	}
}

// TS-06-16 (unit): fix's summary view includes verification, tail output and
// all, when the run's verdict is not landable (06-REQ-3.2).
func TestTS06_16_SummaryViewIncludesVerificationWhenNotLandable(t *testing.T) {
	r := &Result{
		Stage:        "unverified",
		Branch:       "fix/stop-double-counting",
		Verdict:      string(checks.VerdictRegressed),
		Verification: checks.Result{Command: "make test", OK: false, ExitCode: 1, Output: "FAIL: TestX\nassertion failed"},
	}
	if checks.Verdict(r.Verdict).Landable() {
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
		t.Fatal("expected a verification field for a non-landable verdict")
	}
	v, ok := vAny.(map[string]any)
	if !ok {
		t.Fatalf("verification is not an object: %T", vAny)
	}
	out, _ := v["output"].(string)
	if len(out) == 0 {
		t.Error("expected the failing command's tail output to be present in verification.output")
	}
}

// TS-06-20 (unit): fields trimmed from the summary view are still computed
// in full, and the value the report file carries is identical to what the
// pipeline actually computed — trimming for stdout never touches it
// (06-REQ-3.5).
func TestTS06_20_TrimmedFieldsStillComputedAndPresentInReportFile(t *testing.T) {
	ws, g := newRepo(t, 0)
	b := defaultBrain()

	got, err := Run(t.Context(), newOptions(ws, g, b))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !checks.Verdict(got.Verdict).Landable() {
		t.Fatalf("this test needs a landable verdict, got %q", got.Verdict)
	}

	// What --detail summary emits on stdout: verification is trimmed.
	summary := toolio.ApplyDetail("summary", got)
	sb, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("Marshal summary: %v", err)
	}
	var sm map[string]any
	if err := json.Unmarshal(sb, &sm); err != nil {
		t.Fatalf("Unmarshal summary: %v", err)
	}
	if v, ok := sm["verification"]; ok {
		t.Fatalf("expected stdout's summary view to omit verification, got %v", v)
	}

	// What the report file carries: the full view, computed in full
	// regardless of what --detail asked for on stdout.
	full := toolio.FullView(got)
	fb, err := json.Marshal(full)
	if err != nil {
		t.Fatalf("Marshal full: %v", err)
	}
	var fm map[string]any
	if err := json.Unmarshal(fb, &fm); err != nil {
		t.Fatalf("Unmarshal full: %v", err)
	}
	fv, ok := fm["verification"]
	if !ok {
		t.Fatal("expected the report file's full view to carry a computed verification field")
	}

	// Round-trip both sides through the same map shape before comparing:
	// a struct and the map decoded from its own JSON marshal in a
	// different, but equally valid, key order otherwise.
	wantBytes, err := json.Marshal(got.Verification)
	if err != nil {
		t.Fatalf("Marshal got.Verification: %v", err)
	}
	var wantVerification map[string]any
	if err := json.Unmarshal(wantBytes, &wantVerification); err != nil {
		t.Fatalf("Unmarshal wantBytes: %v", err)
	}
	gotVerification, ok := fv.(map[string]any)
	if !ok {
		t.Fatalf("report file's verification is not an object: %T", fv)
	}
	wantJSON, _ := json.Marshal(wantVerification)
	gotJSON, _ := json.Marshal(gotVerification)
	if string(wantJSON) != string(gotJSON) {
		t.Errorf("report file's verification diverges from the computed one:\nwant %s\ngot  %s",
			wantJSON, gotJSON)
	}
}

// A run that failed before verification ran has no verdict and no
// verification: the zero-valued check would read as a failed one (#70).
func TestSummaryViewOmitsVerdictAndVerificationWhenNoCheckRan(t *testing.T) {
	for _, stage := range []string{"analyse", "branch", "implement"} {
		r := &Result{Stage: stage}
		b, err := json.Marshal(r.SummaryView())
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"verdict", "verification"} {
			if _, ok := m[field]; ok {
				t.Errorf("stage %s: the summary view carries %q though no check ran: %s", stage, field, b)
			}
		}
	}

	// A skipped verification is an outcome, and stays visible.
	r := &Result{Stage: "committed", Verdict: string(checks.VerdictUnverified),
		Verification: checks.Result{Skipped: true}}
	b, _ := json.Marshal(r.SummaryView())
	if !strings.Contains(string(b), `"verification"`) || !strings.Contains(string(b), `"unverified"`) {
		t.Errorf("a skipped verification vanished from the summary view: %s", b)
	}
}
