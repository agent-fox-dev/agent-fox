package main

import (
	"os"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/optin"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// The tests of this file are opt-in: they need a real Landlock kernel, or a container
// engine and the tools image, which make check does not have (see internal/optin).

// podmanImage is the image that a podman box of this environment runs.
func podmanImage() string {
	if v := os.Getenv("AF_SANDBOX_IMAGE"); v != "" {
		return v
	}
	return brain.DefaultImage
}

// fixScriptedFourChangesUnder runs af fix with the scripted brain in the box of the
// provider: it edits a file and adds one with write_file, deletes one and sets an executable
// bit with make (the one program of the hands that runs anything, since rm and chmod are not
// among the programs a fix may use), and tries a commit, which its hook refuses.
func fixScriptedFourChangesUnder(t *testing.T, provider string) {
	t.Helper()
	requireMake(t)
	files := boxFixFiles()
	files["Makefile"] += "tidy:\n\trm docs/old.txt\n\tchmod +x bin/run.sh\n"
	e := newFixEnvWith(t, fakeforge.GitHub, files, true)
	steps := script(t,
		writeStep("src/greet.sh", "echo hello\n"),
		writeStep("tests/hello_test.sh", helloTest),
		map[string]any{"tool": "execute", "args": map[string]any{"command": "make tidy"}, "expect": "ok"},
		map[string]any{"tool": "execute", "args": map[string]any{"command": "git add -A && git commit -q -m 'from the box'"}, "expect": "denied"},
		submitStep(spyReport, "accepted"),
	)

	code, env := e.fix(e.forge.IssueURL(7), "--brain", "scripted", "--script", steps, "--sandbox", provider)

	assertFourChangesLanded(t, e, code, env, "")
	if box, _ := resultOf(t, env)["box"].(map[string]any); box["provider"] != provider {
		t.Errorf("box = %v, want the %s provider", box, provider)
	}
}

// TestFixUnderTheRealBoxesLandsAllFourChanges_TS_10_63 verifies, for landlock and podman,
// what TS-10-63 asks: af fix end to end lands one commit with the trailer that holds an
// edit, an addition, a deletion and a change of the executable bit, owned by the host user,
// and not the commit a brain made in the box. It runs where AF_TEST_LANDLOCK or
// AF_TEST_PODMAN says so, and is skipped with the reason otherwise.
// Verifies: 10-REQ-10.3, 10-REQ-6.1
func TestFixUnderTheRealBoxesLandsAllFourChanges_TS_10_63(t *testing.T) {
	for _, tc := range []struct {
		provider string
		gate     func(*testing.T)
	}{
		{"landlock", func(t *testing.T) { optin.RequireLandlock(t) }},
		{"podman", func(t *testing.T) { optin.RequirePodman(t, podmanImage()) }},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			tc.gate(t)
			t.Run("scripted", func(t *testing.T) { fixScriptedFourChangesUnder(t, tc.provider) })
			t.Run("a commit in the box", func(t *testing.T) { requireMake(t); fixFourChangesUnder(t, tc.provider) })
		})
	}
}

// TestFixEndToEndWithTheScriptedBrainLandsAllFourChangesUnderLocal runs the scripted
// run of the opt-in test under the one box every machine has, so that make check proves
// the run: the steps, and the commit its hook refuses.
func TestFixEndToEndWithTheScriptedBrainLandsAllFourChangesUnderLocal(t *testing.T) {
	fixScriptedFourChangesUnder(t, "local")
}
