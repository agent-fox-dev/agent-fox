package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/schematest"
	"github.com/agent-fox-dev/agentfox/issuetriage"
)

// sse renders (event, data) pairs as an Anthropic text/event-stream body.
func sse(pairs ...[2]string) string {
	var b strings.Builder
	for _, p := range pairs {
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", p[0], p[1])
	}
	return b.String()
}

// scriptedAnthropic is a scripted stand-in for the Anthropic API: its first
// reply is one file_issue tool call carrying a valid triage, every later one a
// plain end of turn. It speaks the vendor's wire protocol over a real socket,
// so the binary under test resolves a real model and drives a real provider.
func scriptedAnthropic(t *testing.T) *httptest.Server {
	t.Helper()
	args, err := json.Marshal(map[string]any{
		"title":          "widget: Count() double counts on retry",
		"problem":        "Count() returns 1 where it should return 2",
		"reproduction":   "Call Count() after a retry",
		"confidence":     "Confirmed",
		"root_cause":     "Count() returns a constant",
		"affected_files": []any{map[string]any{"path": "widget.go", "role": "fault location"}},
		"suggested_fix": map[string]any{
			"approach": "Return 2",
			"files":    []any{map[string]any{"path": "widget.go", "role": "change Count()"}},
			"risks":    "None",
		},
		"acceptance_criteria": []any{"Given a retry, Count() returns 2"},
		"severity":            "High",
		"severity_rationale":  "Wrong answer",
	})
	if err != nil {
		t.Fatal(err)
	}
	partial, _ := json.Marshal(string(args))
	start := [2]string{"message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-test","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":1}}}`}
	toolTurn := sse(start,
		[2]string{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"` + issuetriage.ToolFileIssue + `","input":{}}}`},
		[2]string{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":` + string(partial) + `}}`},
		[2]string{"content_block_stop", `{"type":"content_block_stop","index":0}`},
		[2]string{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":20}}`},
		[2]string{"message_stop", `{"type":"message_stop"}`},
	)
	endTurn := sse(start,
		[2]string{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		[2]string{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`},
		[2]string{"content_block_stop", `{"type":"content_block_stop","index":0}`},
		[2]string{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":2}}`},
		[2]string{"message_stop", `{"type":"message_stop"}`},
	)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			fmt.Fprint(w, toolTurn)
			return
		}
		fmt.Fprint(w, endTurn)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runIssue runs the built issue binary and parses its stdout, which must be
// exactly one JSON object.
func runIssue(t *testing.T, bin, dir string, env []string, args ...string) (map[string]any, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("issue %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("issue %v: stdout is not a JSON object: %v\nstdout: %s\nstderr: %s", args, err, stdout.String(), stderr.String())
	}
	if dec.More() {
		t.Fatalf("issue %v: stdout holds more than one JSON document:\n%s", args, stdout.String())
	}
	t.Logf("issue %v: exit %d\nstderr: %s", args, code, stderr.String())
	return doc, code
}

// TS-09-46 (smoke): An ordinary run of issue reports the same schema_version
// its own --schema advertised.
//
// Verifies: 09-PATH-3, 09-REQ-5.1, 09-REQ-5.2, 09-REQ-5.3
// Real components: the built cmd/issue binary, toolio.App, toolio.Run.Envelope,
// issuetriage.Run, the Anthropic provider (over a scripted HTTP server)
func TestTS09_46_OrdinaryRunReportsTheSchemaVersionSchemaAdvertised_Smoke(t *testing.T) {
	bin := schematest.Build(t, "issue")
	api := scriptedAnthropic(t)

	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "widget.go"), []byte("package widget\nfunc Count() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", ws, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"XDG_STATE_HOME=" + t.TempDir(),
		"ANTHROPIC_API_KEY=test-key",
		"ANTHROPIC_BASE_URL=" + api.URL,
	}

	described, code := runIssue(t, bin, ws, env, "--schema")
	if code != 0 {
		t.Fatalf("issue --schema exited %d", code)
	}
	advertised, _ := described["schema_version"].(string)
	if advertised != "3.0.0" {
		t.Fatalf("--schema advertised schema_version %q, want 3.0.0", advertised)
	}

	ordinary, code := runIssue(t, bin, ws, env, "--dir", ws, "--dry-run",
		"Count() in widget.go returns 1 where it should return 2 after a retry")
	if code != 0 {
		t.Fatalf("the ordinary run exited %d, want 0; envelope: %v", code, ordinary)
	}
	if ordinary["ok"] != true || ordinary["tool"] != "issue" {
		t.Fatalf("the ordinary run did not complete: %v", ordinary)
	}
	got, present := ordinary["schema_version"].(string)
	if !present {
		t.Fatalf("the ordinary envelope carries no schema_version: %v", ordinary)
	}
	if got != "3.0.0" {
		t.Errorf("ordinary envelope schema_version = %q, want 3.0.0", got)
	}
	if got != advertised {
		t.Errorf("ordinary envelope schema_version %q != --schema's %q", got, advertised)
	}
}
