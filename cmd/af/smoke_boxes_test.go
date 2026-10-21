package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/optin"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// The smoke tests of this file run a path of the spec from the command to the files it
// leaves, with the components of the path real: the commands of cmd/af, the box
// providers, brain.Execute, the process runner, af guard check and af <agent> submit (the
// test binary linked as af), real git. Only the model is faked, by a fake claude.

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

// allowedEnvironment are the names a process brain may find in its environment: the ones of
// the allowlist of the process runner, and the key of the vendor.
var allowedEnvironment = []string{
	"PATH", "HOME", "ANTHROPIC_API_KEY", "LANG", "LC_ALL", "TZ", "TMPDIR",
	"HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "https_proxy", "http_proxy", "no_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS",
}

// TestSmokeADeveloperTriagesAnIssueWithTheClaudeBrainInALocalBox_TS_10_66 verifies TS-10-66
// (10-PATH-1): af triage on an issue, with the claude brain in the local box, from the
// command to the diagnosis. The fake claude asks the hook of its settings file about three
// calls, and submits through af triage submit.
// Verifies: 10-PATH-1, 10-REQ-9.1
func TestSmokeADeveloperTriagesAnIssueWithTheClaudeBrainInALocalBox_TS_10_66(t *testing.T) {
	requireGit(t)
	requireSh(t)
	e := newTriageEnv(t, fakeforge.GitHub)
	for name, value := range map[string]string{"GITHUB_TOKEN": "ghp-host-token", "GH_TOKEN": "gh-host-token", "GITLAB_TOKEN": "gl-host-token"} {
		t.Setenv(name, value)
	}
	hook := func(tool string, input map[string]any) json.RawMessage {
		data, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": input, "cwd": "{root}"})
		return data
	}
	script := claudeSubmits(diagnosisPayload(nil))
	script.NotesToStderr = true
	placedInTheBox := []string{"brief.md", "run.json", "guard.json", "claude-settings.json"}
	for _, name := range placedInTheBox {
		script.ShowFiles = append(script.ShowFiles, "{root}/.agent-fox/run/{run_id}/"+name)
	}
	script.HookPayloads = []json.RawMessage{
		hook("Bash", map[string]any{"command": "go test ./..."}),
		hook("Write", map[string]any{"file_path": "{root}/.agent-fox/run/{run_id}/submission.json", "content": "{}"}),
		hook("Bash", map[string]any{"command": "git diff"}),
	}
	useFakeClaude(t, script)

	code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--land", "none", "--brain", "claude", "--sandbox", "local")

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	runDir := e.runDir(env)

	// The box: unconfined once, the brain in it, nothing enforced on the network.
	if n := len(warningMessages(env, "box_unconfined")); n != 1 {
		t.Errorf("box_unconfined was raised %d times, want once", n)
	}
	opened := ledgerEntries(t, runDir, "box_open")
	if len(opened) != 1 || opened[0]["provider"] != "local" || opened[0]["placement"] != "box" || opened[0]["network"] != "host" || opened[0]["image"] != "" {
		t.Errorf("box_open = %v, want local, placement box, network host", opened)
	}

	// What was placed in the box for the brain, as the fake saw it; and what was kept on the host.
	stderr := string(readFile(t, filepath.Join(runDir, "claude.1.stderr")))
	var hookLine string
	names := map[string]string{}
	seen := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		if rest, ok := strings.CutPrefix(line, "hooks: "); ok {
			hookLine = rest
		} else if rest, ok := strings.CutPrefix(line, "file: "); ok {
			var f struct {
				Path, Content string
				Missing       bool
			}
			if err := json.Unmarshal([]byte(rest), &f); err != nil || f.Missing {
				t.Errorf("a file the fake looked for in the box: %q, %v, want it there", rest, err)
			}
			seen[filepath.Base(f.Path)] = f.Content
		} else if name, value, ok := strings.Cut(line, "="); ok {
			names[name] = value
		}
	}
	for _, name := range placedInTheBox {
		if seen[name] == "" {
			t.Errorf("%s was not placed in the box", name)
		}
	}
	var placed struct {
		PrefixBytes int `json:"prefix_bytes"`
	}
	if err := json.Unmarshal([]byte(seen["run.json"]), &placed); err != nil || placed.PrefixBytes <= 0 || placed.PrefixBytes >= len(seen["brief.md"]) {
		t.Errorf("run.json in the box = %q: prefix_bytes %d, %v, want the length of the prefix of the brief of %d bytes", seen["run.json"], placed.PrefixBytes, err, len(seen["brief.md"]))
	}
	for _, name := range []string{"brief.md", "brief.1.md", "guard.json", "claude-settings.json"} {
		if _, err := os.Stat(filepath.Join(runDir, name)); err != nil {
			t.Errorf("the run directory lacks %s: %v", name, err)
		}
	}
	if settings := string(readFile(t, filepath.Join(runDir, "claude-settings.json"))); !strings.Contains(settings, "guard check --host claude") || !strings.Contains(settings, "guard.json") {
		t.Errorf("claude-settings.json = %s, want the hook af guard check --host claude with the policy", settings)
	}
	var outputs []string
	for _, a := range artifactsOf(env, "brain_output") {
		p, _ := a["path"].(string)
		outputs = append(outputs, filepath.Base(p))
	}
	slices.Sort(outputs)
	if !slices.Equal(outputs, []string{"claude.1.json", "claude.1.stderr"}) {
		t.Errorf("brain_output artifacts = %v, want the output and the errors of the process", outputs)
	}

	// The process: under env -i, with the key and nothing else of the host.
	for name := range names {
		if !slices.Contains(allowedEnvironment, name) {
			t.Errorf("the process found %s in its environment: only %v may be there", name, allowedEnvironment)
		}
	}
	if names["ANTHROPIC_API_KEY"] != "sk-fake-key" || names["PATH"] == "" || names["HOME"] == "" {
		t.Errorf("the environment of the process = %v, want the key of the vendor, PATH and HOME", names)
	}

	// The hook answered like af guard check: a deny that names af gate run, a deny for the
	// submission, and {} for git diff.
	var answers []struct {
		Stdout string `json:"stdout"`
		Exit   int    `json:"exit"`
	}
	if err := json.Unmarshal([]byte(hookLine), &answers); err != nil || len(answers) != 3 {
		t.Fatalf("the answers of the hook = %q, %v, want three", hookLine, err)
	}
	if !strings.Contains(answers[0].Stdout, `"permissionDecision":"deny"`) || !strings.Contains(answers[0].Stdout, "af gate run") ||
		!strings.Contains(answers[1].Stdout, `"permissionDecision":"deny"`) || answers[2].Stdout != "{}" || answers[0].Exit+answers[1].Exit+answers[2].Exit != 0 {
		t.Errorf("answers of the hook = %+v, want deny (af gate run), deny, {} and exit 0", answers)
	}

	// The result: the accepted diagnosis, and the cost as the vendor reported it.
	if sub := submissionOf(t, runDir); !strings.Contains(string(sub.Payload), "Run starts the wrong thing") {
		t.Errorf("submission.json holds %s, want the diagnosis", sub.Payload)
	}
	if u, _ := env["usage"].(map[string]any); u["cost_source"] != "reported" || u["cost_usd"] != 0.42 || u["turns"] != float64(5) {
		t.Errorf("usage = %v, want the cost and the turns of the fake, as reported", u)
	}
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
