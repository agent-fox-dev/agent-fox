package afcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// The smoke tests of this file run a path of the spec from the command to the files it
// leaves, with the components of the path real: the commands of cmd/af, the box
// providers, brain.Execute, real git. Only the model is faked.

// warningMessages are the messages of the warnings of an envelope with the code.
func warningMessages(env map[string]any, code string) []string {
	var out []string
	list, _ := env["warnings"].([]any)
	for _, w := range list {
		if m, ok := w.(map[string]any); ok && m["code"] == code {
			out = append(out, m["message"].(string))
		}
	}
	return out
}

// TestSmokeWithTheDefaultBoxDownAfFailsWithSandboxAndNeverFallsBack_TS_10_69 verifies
// TS-10-69 (10-PATH-4): on macOS, with no --sandbox and no AF_SANDBOX, the default box is
// podman; with its engine down af fix fails as sandbox, not retryable, tells how to start
// the machine and how to choose another box, starts no brain, and does not open local.
// Verifies: 10-PATH-4, 10-REQ-1.5
func TestSmokeWithTheDefaultBoxDownAfFailsWithSandboxAndNeverFallsBack_TS_10_69(t *testing.T) {
	requireMake(t)
	oldGOOS := box.GOOS
	box.GOOS = "darwin"
	t.Cleanup(func() { box.GOOS = oldGOOS })
	t.Setenv("AF_SANDBOX", "")
	// A podman whose engine is down: the CLI that the provider runs answers as the real one does.
	bin := t.TempDir()
	// Its error says nothing of starting a machine: the hint is af's, from the OS it runs on.
	down := "#!/bin/sh\necho 'Cannot connect to Podman: connection refused' >&2\nexit 125\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(down), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	// The default brain runs in the box, so it needs the key of the vendor before one opens.
	t.Setenv("ANTHROPIC_API_KEY", "sk-dummy-key")

	code, env := e.run("", "fix", e.forge.IssueURL(7), "--dir", e.dir, "--land", "none")

	errObj, _ := env["error"].(map[string]any)
	hint, _ := errObj["fix_hint"].(map[string]any)
	message, _ := errObj["message"].(string)
	if code != 1 || env["status"] != "failed" || errObj["category"] != "sandbox" || errObj["retryable"] != false {
		t.Fatalf("exit code %d, status %v, error %v, want failed/sandbox, not retryable", code, env["status"], errObj)
	}
	if !strings.Contains(message, "connection refused") || !strings.Contains(message, "podman machine start") {
		t.Errorf("message = %q, want the error of the provider and a mention of podman machine start", message)
	}
	if hint["flag"] != "--sandbox" || hint["env"] != "AF_SANDBOX" {
		t.Errorf("fix_hint = %v, want one that names --sandbox and AF_SANDBOX", hint)
	}
	if started := ledgerEntries(t, runDirOf(e, env), "brain_start"); len(started) != 0 {
		t.Errorf("brain_start = %v, want no brain started", started)
	}
	if n := len(warningMessages(env, "box_unconfined")); n != 0 {
		t.Errorf("box_unconfined was raised %d times: a box without isolation was opened", n)
	}
	if opened := ledgerEntries(t, runDirOf(e, env), "box_open"); len(opened) != 0 {
		t.Errorf("box_open = %v, want no box opened at all", opened)
	}
}
