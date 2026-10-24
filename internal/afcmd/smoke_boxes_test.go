package afcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/optin"
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

// TestSmokeADeveloperFixesInPlaceWithNoneAndAForeignCommitIsFolded_TS_10_67 verifies
// TS-10-67 (10-PATH-2): af fix under none, with a brain that edits the checkout and
// commits on main, lands one commit of af's on the fix branch that holds the change, takes
// the foreign commit off main, exports nothing, and says so.
// Verifies: 10-PATH-2, 10-REQ-6.5
func TestSmokeADeveloperFixesInPlaceWithNoneAndAForeignCommitIsFolded_TS_10_67(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	var foreign string
	watched := &sessions{}
	spyRounds(t, watchRound(watched, shellRound(t, `printf 'echo hello\n' > src/greet.sh
cat > tests/hello_test.sh <<'EOF'
[ "$(sh src/greet.sh)" = hello ] || { echo "FAIL: not hello"; exit 1; }
EOF`+commitTheWork, spyReport, &foreign)))

	code, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", "none")

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	res := resultOf(t, env)
	branch, _ := res["branch"].(string)
	base, _ := res["base_head"].(string)
	if res["outcome"] != "landed" || !strings.HasPrefix(branch, "fix/7-") {
		t.Fatalf("result = %v, want landed on fix/7-…", res)
	}
	if unconfined := warningMessages(env, "box_unconfined"); len(unconfined) != 1 || !strings.Contains(unconfined[0], "the hook is the only guard") {
		t.Errorf("box_unconfined = %q, want one that says the hook is the only guard", unconfined)
	}
	if n := len(ledgerEntries(t, runDirOf(e, env), "box_export")); n != 0 {
		t.Errorf("%d box_export entries, want none: Export is never called in place", n)
	}
	if calls, boxes := watched.exports(); calls != 0 || boxes == 0 {
		t.Errorf("Export was asked %d times of the %d boxes the brain was given, want none", calls, boxes)
	}
	if opened := ledgerEntries(t, runDirOf(e, env), "box_open"); len(opened) != 1 || opened[0]["provider"] != "none" || opened[0]["placement"] != "host" || opened[0]["network"] != "host" || opened[0]["image"] != "" {
		t.Errorf("box_open = %v, want none, placement host, network host", opened)
	}
	reset := warningMessages(env, "foreign_commits_reset")
	if len(reset) != 1 || !strings.Contains(reset[0], "1 commit") {
		t.Errorf("foreign_commits_reset = %q, want one that counts 1 commit", reset)
	}
	if n := len(strings.Fields(gitDo(t, e.dir, "rev-list", base+".."+branch))); n != 1 {
		t.Errorf("%d commits above the base, want af's one", n)
	}
	if msg := gitDo(t, e.dir, "log", "-1", "--format=%B", branch); !strings.Contains(msg, "Agent-Fox-Run: "+env["run_id"].(string)) {
		t.Errorf("message = %q, want the trailer", msg)
	}
	if show := gitDo(t, e.dir, "show", "--stat", "--format=", branch); !strings.Contains(show, "src/greet.sh") || !strings.Contains(show, "tests/hello_test.sh") {
		t.Errorf("af's commit does not hold the change of the foreign one:\n%s", show)
	}
	if foreign == "" {
		t.Fatal("the brain recorded no commit")
	}
	if on := gitDo(t, e.dir, "branch", "--contains", foreign); strings.Contains(on, "main") || strings.Contains(on, branch) {
		t.Errorf("the foreign commit %s is on %q", foreign, on)
	}
	if got := strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "main")); got != base {
		t.Errorf("main is at %s, want the base %s", got, base)
	}
	e.onBase(t)
}

// TestSmokeADeveloperFixesAnIssueInsideALandlockBox_TS_10_68 verifies TS-10-68
// (10-PATH-3): af fix in a landlock box lands an edit, an addition, a deletion and the
// executable bit in one commit with the trailer, not the commit made in the box, with files
// the host user owns. It runs on Linux with Landlock ABI 4 or newer, and where
// AF_TEST_LANDLOCK is 1; it is skipped, with the reason, otherwise.
// Verifies: 10-PATH-3, 10-REQ-6.1
func TestSmokeADeveloperFixesAnIssueInsideALandlockBox_TS_10_68(t *testing.T) {
	optin.RequireLandlock(t)
	t.Run("a brain that commits in the box", func(t *testing.T) { requireMake(t); fixFourChangesUnder(t, "landlock") })
	t.Run("the scripted brain", func(t *testing.T) { fixScriptedFourChangesUnder(t, "landlock") })
}

// TestSmokeWithTheDefaultBoxDownAfFailsWithSandboxAndNeverFallsBack_TS_10_69 verifies
// TS-10-69 (10-PATH-4): on macOS, with no --sandbox and no AF_SANDBOX, the default box is
// podman; with its engine down af fix fails as sandbox, not retryable, tells how to start
// the machine and how to choose another box, starts no brain, and opens neither local nor
// none.
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
