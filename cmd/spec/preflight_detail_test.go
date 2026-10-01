package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/specgen"
)

// TS-11-41 (integration, spec's share): --detail summary keeps preflight and
// estimate in the trimmed result, and artifacts[] and side_effects[] stay
// empty.
//
// Verifies: 11-REQ-7.1, 11-REQ-7.2
func TestTS11_41_DetailSummaryKeepsPreflightAndEstimate_Spec(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv(specgen.SpecDirEnv, "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := newApp().Main(context.Background(), []string{"--preflight", "--detail", "summary", "--dir", dir, "a library that does a thing"},
		strings.NewReader(""), &stdout, &stderr)
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s\n%s", err, stdout.String(), stderr.String())
	}
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
	// Only the envelope's own report file: no spec package or comment.
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
