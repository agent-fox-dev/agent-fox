package main

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/envtest"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// runTriage drives the real App, as main does, and returns the exit code and
// the decoded envelope.
func runIssueApp(t *testing.T, argv ...string) (int, map[string]any) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := newApp().Main(context.Background(), argv, strings.NewReader(""), &stdout, &stderr)
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	return code, env
}

// TS-11-11 (unit): triage's Exec and PreflightExec closures build identical
// issuetriage.Options from the same flags, because both call triageOptions.
//
// Verifies: 11-REQ-2.4
func TestTS11_11_IssueOptionsIsOneFunctionForBothClosures(t *testing.T) {
	f := &triageFlags{repo: "acme/widgets", labels: "af:fix, bug", overwrite: false}
	d := toolio.Deps{
		Common: &toolio.Common{DryRun: true},
		Input:  toolio.Input{Kind: toolio.KindText, Origin: "argument", Body: "a report"},
	}

	optsA, optsB := f.triageOptions(d), f.triageOptions(d)
	if !reflect.DeepEqual(optsA, optsB) {
		t.Errorf("two builds differ:\n%+v\n%+v", optsA, optsB)
	}
	want := issuex.Repo{Owner: "acme", Name: "widgets"}
	if got := optsA.Repo; got.Owner != want.Owner || got.Name != want.Name {
		t.Errorf("Repo = %+v", got)
	}
	if !reflect.DeepEqual(optsA.Labels, []string{"af:fix", "bug"}) || !optsA.DryRun || optsA.Overwrite || optsA.Input.Body != "a report" {
		t.Errorf("flags did not reach the options: %+v", optsA)
	}

	app := newApp()
	if app.Exec == nil || app.PreflightExec == nil {
		t.Errorf("Exec = %v, PreflightExec = %v: triage must register both", app.Exec != nil, app.PreflightExec != nil)
	}
}

// triage --preflight through the real shell: exit 0, stage preflight, the
// checklist and estimate in the default view, no usage.
func TestIssuePreflightThroughTheShell(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("GITHUB_TOKEN", "test-token")

	code, env := runIssueApp(t, "--preflight", "--dir", t.TempDir(), "--repo", "acme/widgets", "a report")
	if code != toolio.ExitOK {
		t.Fatalf("code = %d: %v", code, env)
	}
	res, _ := env["result"].(map[string]any)
	if res["stage"] != "preflight" {
		t.Errorf("stage = %v", res["stage"])
	}
	if list, ok := res["preflight"].([]any); !ok || len(list) != 3 {
		t.Errorf("result.preflight = %v, want target_repository, forge_credential and symbol_backend", res["preflight"])
	}
	if est, ok := res["estimate"].(map[string]any); !ok || est["phases"] != float64(1) {
		t.Errorf("estimate = %v", res["estimate"])
	}
	if _, ok := env["usage"]; ok {
		t.Error("no phase ran, so the envelope must carry no usage")
	}
	if _, ok := env["error"]; ok {
		t.Errorf("unexpected error: %v", env["error"])
	}
}

// TS-11-22 (property, triage's share): for every scenario that fails before
// the first model call, adding --preflight changes neither the error nor the
// exit code. The corpus here is the pre-model failures triage can have; the
// other tools' share is in cmd/fix, cmd/impl and cmd/spec.
//
// Verifies: 11-REQ-4.1
func TestTS11_22_PreflightDoesNotChangeAPreModelFailure(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		argv []string
	}{
		{
			name: "unresolvable model",
			env:  map[string]string{"ANTHROPIC_API_KEY": "test-key", "GITHUB_TOKEN": "test-token"},
			argv: []string{"--model", "no-such-tier/at-all", "--repo", "acme/widgets", "a report"},
		},
		{
			name: "missing model credential",
			env:  map[string]string{"ANTHROPIC_API_KEY": "", "GITHUB_TOKEN": "test-token"},
			argv: []string{"--repo", "acme/widgets", "a report"},
		},
		{
			name: "missing forge credential",
			env:  map[string]string{"ANTHROPIC_API_KEY": "test-key", "GITHUB_TOKEN": "", "GH_TOKEN": "", "GITLAB_TOKEN": ""},
			argv: []string{"--repo", "acme/widgets", "a report"},
		},
		{
			name: "no target repository",
			env:  map[string]string{"ANTHROPIC_API_KEY": "test-key", "GITHUB_TOKEN": "test-token"},
			argv: []string{"a report"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Hermetic: a machine that carries a gateway, a token or a forge
			// credential of its own must not change which scenarios fail.
			envtest.Clean(t)
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			dir := t.TempDir() // no git, so no origin remote to detect
			base := append([]string{"--dir", dir}, tc.argv...)

			codeA, envA := runIssueApp(t, base...)
			codeB, envB := runIssueApp(t, append([]string{"--preflight"}, base...)...)
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

// 11-REQ-6.3, 05-REQ-2: a summary is in the tool's own vocabulary, so it begins
// with the tool's name as the envelope reports it. The expectation is read from
// the envelope's own `tool`, so a rename that misses the summary strings fails
// here.
func TestSummaryBeginsWithTheToolsName(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	code, env := runIssueApp(t, "--preflight", "--dry-run", "--dir", t.TempDir(), "--repo", "acme/widgets", "a report")
	if code != toolio.ExitOK {
		t.Fatalf("exit = %d: %v", code, env)
	}
	tool, _ := env["tool"].(string)
	summary, _ := env["summary"].(string)
	if tool == "" || !strings.HasPrefix(summary, tool+": ") {
		t.Errorf("summary = %q, want it to begin with %q", summary, tool+": ")
	}
}
