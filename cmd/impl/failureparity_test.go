package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-11-22 (property, impl's share): for every scenario that fails before the
// first model call, adding --preflight changes neither the error nor the exit
// code. The other tools' share is in cmd/fix, cmd/spec and cmd/issue.
//
// Verifies: 11-REQ-4.1
func TestTS11_22_PreflightDoesNotChangeAPreModelFailure(t *testing.T) {
	dirty := func(t *testing.T, dir, _ string) {
		if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	invalidSpec := func(t *testing.T, dir, specDir string) {
		if err := os.WriteFile(filepath.Join(specDir, "tasks.json"), []byte("{ not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-q", "-m", "chore: break the spec")
	}
	base := map[string]string{
		"ANTHROPIC_API_KEY": "test-key", "GITHUB_TOKEN": "test-token", "GH_TOKEN": "", "GITLAB_TOKEN": "",
	}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for key, val := range base {
			m[key] = val
		}
		m[k] = v
		return m
	}

	cases := []struct {
		name  string
		env   map[string]string
		setup func(t *testing.T, dir, specDir string)
		argv  []string
	}{
		{"dirty tree", base, dirty, []string{"--land", "none"}},
		{"invalid spec package", base, invalidSpec, []string{"--land", "none"}},
		{"unresolvable model", base, nil, []string{"--land", "none", "--model", "no-such-tier/at-all"}},
		{"missing model credential", with("ANTHROPIC_API_KEY", ""), nil, []string{"--land", "none"}},
		{"missing forge credential", with("GITHUB_TOKEN", ""), nil, []string{"--repo", "acme/widgets"}},
		{"no target repository", base, nil, []string{"--land", "pr"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			dir, specDir := preflightSpecRepo(t)
			if tc.setup != nil {
				tc.setup(t, dir, specDir)
			}
			args := append(append([]string{"--dir", dir}, tc.argv...), "09")

			codeA, envA := runImpl(t, args...)
			codeB, envB := runImpl(t, append([]string{"--preflight"}, args...)...)
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
