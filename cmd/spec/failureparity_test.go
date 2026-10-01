package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/specgen"
)

// runSpecApp drives the real App, as main does, and returns the exit code and
// the decoded envelope.
func runSpecApp(t *testing.T, argv ...string) (int, map[string]any) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := newApp().Main(context.Background(), argv, strings.NewReader(""), &stdout, &stderr)
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	return code, env
}

// TS-11-22 (property, spec's share): for every scenario that fails before the
// first model call, adding --preflight changes neither the error nor the exit
// code. The other tools' share is in cmd/fix, cmd/impl and cmd/issue.
//
// Verifies: 11-REQ-4.1
func TestTS11_22_PreflightDoesNotChangeAPreModelFailure(t *testing.T) {
	const idea = "a library that does a thing"
	// ambiguousPlans leaves two unfinished splits that both match the input,
	// which the run refuses at its preflight stage.
	ambiguousPlans := func(t *testing.T, dir string) {
		sum := sha256.Sum256([]byte(idea))
		for _, name := range []string{"one", "two"} {
			plan := map[string]any{
				"input":      map[string]any{"kind": "text", "origin": "argument", "sha256": hex.EncodeToString(sum[:])},
				"created_at": "2025-01-01T00:00:00Z",
				"updated_at": "2025-01-01T00:00:00Z",
				"scopes":     []map[string]any{{"name": name, "scope": "s"}, {"name": name + "_b", "scope": "t"}},
			}
			raw, _ := json.Marshal(plan)
			root := filepath.Join(dir, ".specs")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, name+".split.json"), raw, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	cases := []struct {
		name  string
		env   map[string]string
		setup func(*testing.T, string)
		argv  []string
	}{
		{
			name: "unresolvable model",
			env:  map[string]string{"ANTHROPIC_API_KEY": "test-key"},
			argv: []string{"--model", "no-such-tier/at-all", idea},
		},
		{
			name: "missing model credential",
			env:  map[string]string{"ANTHROPIC_API_KEY": ""},
			argv: []string{idea},
		},
		{
			name:  "ambiguous split plan",
			env:   map[string]string{"ANTHROPIC_API_KEY": "test-key"},
			setup: ambiguousPlans,
			argv:  []string{idea},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			t.Setenv(specgen.SpecDirEnv, "")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.setup != nil {
				tc.setup(t, dir)
			}
			base := append([]string{"--dir", dir}, tc.argv...)

			codeA, envA := runSpecApp(t, base...)
			codeB, envB := runSpecApp(t, append([]string{"--preflight"}, base...)...)
			if codeA == toolio.ExitOK {
				t.Fatalf("the scenario does not fail: %v", envA)
			}
			if codeA != codeB {
				t.Errorf("exit code %d without --preflight, %d with", codeA, codeB)
			}
			a, _ := json.Marshal(envA["error"])
			b, _ := json.Marshal(envB["error"])
			if len(a) == 0 || string(a) == "null" {
				t.Fatalf("no error object: %v", envA)
			}
			if !bytes.Equal(a, b) {
				t.Errorf("error objects differ:\n%s\n%s", a, b)
			}
			if envA["status"] != envB["status"] {
				t.Errorf("status = %v vs %v", envA["status"], envB["status"])
			}
			if res, ok := envB["result"].(map[string]any); ok {
				for _, k := range []string{"preflight", "estimate"} {
					if _, has := res[k]; has {
						t.Errorf("a refused --preflight run carries %q: %v", k, res)
					}
				}
			}
		})
	}
}
