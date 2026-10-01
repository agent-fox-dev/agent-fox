package main

import (
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-11-41 (integration, issue's share): --detail summary keeps preflight and
// estimate in the trimmed result, and artifacts[] and side_effects[] stay
// empty.
//
// Verifies: 11-REQ-7.1, 11-REQ-7.2
func TestTS11_41_DetailSummaryKeepsPreflightAndEstimate_Issue(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("GITHUB_TOKEN", "test-token")

	code, env := runIssueApp(t, "--preflight", "--detail", "summary", "--dir", t.TempDir(), "--repo", "acme/widgets", "a report")
	if code != toolio.ExitOK {
		t.Fatalf("code = %d: %v", code, env)
	}
	res, _ := env["result"].(map[string]any)
	if list, ok := res["preflight"].([]any); !ok || len(list) == 0 {
		t.Errorf("--detail summary dropped result.preflight: %v", res)
	}
	if est, ok := res["estimate"].(map[string]any); !ok || len(est) == 0 {
		t.Errorf("--detail summary dropped result.estimate: %v", res)
	}
	if list, _ := env["side_effects"].([]any); len(list) != 0 {
		t.Errorf("side_effects = %v, want empty", list)
	}
	// Only the envelope's own report file: no issue was filed.
	arts, _ := env["artifacts"].([]any)
	for _, a := range arts {
		if kind := a.(map[string]any)["kind"]; kind != "report_file" {
			t.Errorf("artifact %v, want only the report_file", a)
		}
	}
	if v, ok := env["untrusted_fields"].([]any); ok && len(v) != 0 {
		t.Errorf("untrusted_fields = %v, want empty", v)
	}
}
