package main

import (
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-11-41 (integration, impl's share): --detail summary keeps preflight and
// estimate in the trimmed result, and artifacts[] and side_effects[] stay
// empty (TS-11-42's share for impl).
//
// Verifies: 11-REQ-7.1, 11-REQ-7.2
func TestTS11_41_DetailSummaryKeepsPreflightAndEstimate_Impl(t *testing.T) {
	implEnv(t)
	dir, _ := preflightSpecRepo(t)

	code, env := runImpl(t, "--preflight", "--detail", "summary", "--dir", dir, "--land", "none", "09")
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
	// Only the envelope's own report file and events file: no branch
	// (none was created), commit or pull request.
	arts, _ := env["artifacts"].([]any)
	for _, a := range arts {
		kind := a.(map[string]any)["kind"]
		if kind != "report_file" && kind != "events_file" {
			t.Errorf("artifact %v, want only report_file or events_file", a)
		}
	}
	if v, ok := env["untrusted_fields"].([]any); ok && len(v) != 0 {
		t.Errorf("untrusted_fields = %v, want empty", v)
	}
}
