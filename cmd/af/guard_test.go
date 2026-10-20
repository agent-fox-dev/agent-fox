package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/policy"
)

// runWithStdin runs the shell of the binary with stdin and returns what it
// wrote.
func runWithStdin(t *testing.T, stdin io.Reader, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = newApp().Main(context.Background(), append([]string{"af"}, args...), stdin, &out, &errb)
	return code, out.String(), errb.String()
}

// jsonOf is v as JSON.
func jsonOf(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// claude is the payload Claude Code gives a PreToolUse hook.
func claude(t *testing.T, event, tool string, input map[string]any, cwd string) string {
	t.Helper()
	return jsonOf(t, map[string]any{
		"session_id": "abc", "transcript_path": "/x/y.jsonl", "cwd": cwd,
		"hook_event_name": event, "tool_name": tool, "tool_input": input,
	})
}

// neutral is the payload of af's own form.
func neutral(t *testing.T, tool string, input map[string]any) string {
	t.Helper()
	return jsonOf(t, map[string]any{"tool": tool, "input": input})
}

// goRepo is a directory that detection says runs go test ./... as its suite.
func goRepo(t *testing.T) string {
	t.Helper()
	return repoWith(t, map[string]string{"go.mod": "module x\n", "a.go": "package x\n"})
}

// hookDeny parses a --host claude output, which must be a deny.
func hookDeny(t *testing.T, stdout string) string {
	t.Helper()
	var doc struct {
		Hook struct {
			Event    string `json:"hookEventName"`
			Decision string `json:"permissionDecision"`
			Reason   string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	dec := json.NewDecoder(strings.NewReader(stdout))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("not a hook document: %v\n%s", err, stdout)
	}
	if dec.More() {
		t.Fatalf("more than one document:\n%s", stdout)
	}
	if doc.Hook.Event != "PreToolUse" || doc.Hook.Decision != "deny" || doc.Hook.Reason == "" {
		t.Fatalf("hook = %+v, want a PreToolUse denial with a reason\n%s", doc.Hook, stdout)
	}
	return doc.Hook.Reason
}

// TestGuardCheckPayloadForms_TS_06_58 verifies TS-06-58:
// Claude hook payloads and the neutral form give the same decision, and a
// non-PreToolUse event is allowed with no opinion.
// Verifies: 06-REQ-8.4
func TestGuardCheckPayloadForms_TS_06_58(t *testing.T) {
	dir := goRepo(t)
	command := map[string]any{"command": "go test ./..."}
	payloads := map[string]string{
		"claude":  claude(t, "PreToolUse", "Bash", command, dir),
		"neutral": neutral(t, "Bash", command),
	}

	for name, payload := range payloads {
		code, stdout, _ := runWithStdin(t, strings.NewReader(payload), "guard", "check", "--dir", dir)
		env := oneDocument(t, stdout)
		if code != 0 || field(env, "result", "decision") != "deny" || field(env, "result", "rule") != "suite_command" {
			t.Errorf("%s, envelope: exit code %d, result %v; want a denial by suite_command", name, code, env["result"])
		}

		code, stdout, _ = runWithStdin(t, strings.NewReader(payload), "guard", "check", "--dir", dir, "--host", "claude")
		if reason := hookDeny(t, stdout); code != 0 || !strings.Contains(reason, "af gate run") {
			t.Errorf("%s, --host claude: exit code %d, reason %q; want a denial that names af gate run", name, code, reason)
		}
	}

	post := claude(t, "PostToolUse", "Bash", command, dir)
	code, stdout, _ := runWithStdin(t, strings.NewReader(post), "guard", "check", "--dir", dir)
	env := oneDocument(t, stdout)
	if code != 0 || field(env, "result", "decision") != "allow" || field(env, "result", "rule") != "" {
		t.Errorf("PostToolUse, envelope: exit code %d, result %v; want an allowance with no rule", code, env["result"])
	}
	code, stdout, _ = runWithStdin(t, strings.NewReader(post), "guard", "check", "--dir", dir, "--host", "claude")
	if code != 0 || stdout != "{}" {
		t.Errorf("PostToolUse, --host claude: exit code %d, stdout %q; want {}", code, stdout)
	}

	// A hook that fires for an event it does not know has no opinion either,
	// and one that does not say what event it is is judged.
	other := claude(t, "SessionStart", "Bash", command, dir)
	if _, stdout, _ := runWithStdin(t, strings.NewReader(other), "guard", "check", "--dir", dir, "--host", "claude"); stdout != "{}" {
		t.Errorf("SessionStart: stdout %q, want {}", stdout)
	}
	absent := jsonOf(t, map[string]any{"tool_name": "Bash", "tool_input": command, "cwd": dir})
	if _, stdout, _ := runWithStdin(t, strings.NewReader(absent), "guard", "check", "--dir", dir, "--host", "claude"); stdout == "{}" {
		t.Error("a payload with no hook_event_name was allowed with no opinion; it is judged")
	}
}

// TestGuardCheckCwd guards 06-REQ-8.4: the directory a Claude Code session is
// in decides what its relative paths mean.
func TestGuardCheckCwd(t *testing.T) {
	dir := goRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := map[string]any{"file_path": "../.specs/x.md", "content": "x"}

	_, stdout, _ := runWithStdin(t, strings.NewReader(claude(t, "PreToolUse", "Write", write, filepath.Join(dir, "sub"))), "guard", "check", "--dir", dir)

	env := oneDocument(t, stdout)
	if field(env, "result", "decision") != "deny" || field(env, "result", "rule") != "protected_path" {
		t.Errorf("a write to ../.specs from a subdirectory: %v", env["result"])
	}
}

// TestGuardCheckEnvelope_TS_06_59 verifies TS-06-59:
// without --host the verdict is an envelope with decision, rule, reason, tool
// and segment, done and exit 0 for allow and deny.
// Verifies: 06-REQ-8.5
func TestGuardCheckEnvelope_TS_06_59(t *testing.T) {
	dir := goRepo(t)

	code, stdout, stderr := runWithStdin(t, strings.NewReader(neutral(t, "Bash", map[string]any{"command": "git commit -m x"})), "guard", "check", "--dir", dir)
	deny := oneDocument(t, stdout)
	if code != 0 || stderr != "" || deny["status"] != "done" || deny["tool"] != "guard check" || deny["run_id"] != nil {
		t.Fatalf("exit code %d, status %v, tool %v, run_id %v; want 0, done, guard check, null", code, deny["status"], deny["tool"], deny["run_id"])
	}
	result, _ := deny["result"].(map[string]any)
	if result["decision"] != "deny" || result["rule"] != "git_mutation" || result["tool"] != "Bash" || result["segment"] != "git commit -m x" ||
		!strings.Contains(result["reason"].(string), "af makes every commit") || len(result) != 5 {
		t.Errorf("result = %v, want decision, rule, reason, tool and segment of a denied git commit", result)
	}
	if deny["summary"] == "" {
		t.Error("the envelope has no summary")
	}

	code, stdout, _ = runWithStdin(t, strings.NewReader(neutral(t, "Bash", map[string]any{"command": "git diff"})), "guard", "check", "--dir", dir)
	allow := oneDocument(t, stdout)
	if code != 0 || allow["status"] != "done" || field(allow, "result", "decision") != "allow" || field(allow, "result", "rule") != "" ||
		field(allow, "result", "tool") != "Bash" || field(allow, "result", "segment") != "" || field(allow, "result", "reason") != "" {
		t.Errorf("exit code %d, result %v; want an allowance of Bash with nothing else", code, allow["result"])
	}
	if _, err := os.Stat(filepath.Join(dir, ".agent-fox", "run")); err == nil {
		t.Error("guard check made a run directory")
	}
}

// TestGuardCheckRefusals_TS_06_60 verifies TS-06-60:
// without --host a malformed or oversized payload is usage and an unparseable
// policy is failed/config.
// Verifies: 06-REQ-8.6
func TestGuardCheckRefusals_TS_06_60(t *testing.T) {
	dir := goRepo(t)
	good := neutral(t, "Bash", map[string]any{"command": "git diff"})
	oversized := neutral(t, "Read", map[string]any{"pad": strings.Repeat("x", 1<<20+10)})
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, stdin string
		args        []string
		code        int
		status      string
		category    string
	}{
		{"malformed", "{not json", nil, 2, "usage", "usage"},
		{"empty", "", nil, 2, "usage", "usage"},
		{"not an object", "[1,2]", nil, 2, "usage", "usage"},
		{"two documents", good + good, nil, 2, "usage", "usage"},
		{"no tool", `{"input":{}}`, nil, 2, "usage", "usage"},
		{"an input that is no object", `{"tool":"Bash","input":"git diff"}`, nil, 2, "usage", "usage"},
		{"an event that is no string", `{"hook_event_name":7,"tool_name":"Bash"}`, nil, 2, "usage", "usage"},
		{"oversized", oversized, nil, 2, "usage", "usage"},
		{"a policy that does not parse", good, []string{"--policy", bad}, 1, "failed", "config"},
		{"a policy that is not there", good, []string{"--policy", filepath.Join(dir, "missing.json")}, 1, "failed", "config"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, _ := runWithStdin(t, strings.NewReader(tc.stdin), append([]string{"guard", "check", "--dir", dir}, tc.args...)...)

			env := oneDocument(t, stdout)
			if code != tc.code || env["status"] != tc.status || field(env, "error", "category") != tc.category {
				t.Errorf("exit code %d, status %v, category %v; want %d, %s, %s", code, env["status"], field(env, "error", "category"), tc.code, tc.status, tc.category)
			}
			if env["result"] != nil {
				t.Errorf("result = %v: a decision was emitted", env["result"])
			}
		})
	}

	code, stdout, _ := runWithStdin(t, strings.NewReader(good), "guard", "check", "--dir", dir, "--host", "nosuch")
	if env := oneDocument(t, stdout); code != 2 || env["status"] != "usage" || !strings.Contains(field(env, "error", "message").(string), "claude") {
		t.Errorf("an unknown host: exit code %d, envelope %v; want usage naming claude", code, env)
	}
}

// TestGuardCheckHookOutput_TS_06_61 verifies TS-06-61 on the built binary:
// with --host claude a deny is a single hookSpecificOutput document, an allow
// is {} and permissionDecision allow is never printed, exit 0.
// Verifies: 06-REQ-8.7
func TestGuardCheckHookOutput_TS_06_61(t *testing.T) {
	binary := buildBinary(t)
	dir := goRepo(t)

	runHook := func(payload string) (int, string, string) {
		cmd := exec.Command(binary, "guard", "check", "--host", "claude", "--dir", dir)
		cmd.Stdin = strings.NewReader(payload)
		var out, errb strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return code, out.String(), errb.String()
	}

	code, stdout, stderr := runHook(claude(t, "PreToolUse", "Bash", map[string]any{"command": "af fix land"}, dir))
	reason := hookDeny(t, stdout)
	var asMap map[string]any
	if err := json.Unmarshal([]byte(stdout), &asMap); err != nil {
		t.Fatal(err)
	}
	if code != 0 || stderr != "" || len(asMap) != 1 || !strings.Contains(reason, "loop") {
		t.Errorf("deny: exit code %d, stderr %q, document %v", code, stderr, asMap)
	}
	if _, isEnvelope := asMap["status"]; isEnvelope || strings.Contains(stdout, `"allow"`) {
		t.Errorf("deny stdout is an envelope or says allow:\n%s", stdout)
	}

	code, stdout, stderr = runHook(claude(t, "PreToolUse", "Bash", map[string]any{"command": "git diff"}, dir))
	if code != 0 || stderr != "" || stdout != "{}" {
		t.Errorf("allow: exit code %d, stdout %q, stderr %q; want 0 and exactly {}", code, stdout, stderr)
	}

	code, stdout, _ = runHook(claude(t, "PreToolUse", "Write", map[string]any{"file_path": ".specs/x.md", "content": "x"}, dir))
	if reason := hookDeny(t, stdout); code != 0 || !strings.Contains(reason, ".specs/x.md") {
		t.Errorf("a Write to .specs: exit code %d, reason %q", code, reason)
	}
}

// hostFailsClosed runs guard check --host claude and requires a denial that
// carries the text and exit 0.
func hostFailsClosed(t *testing.T, stdin io.Reader, mention string, args ...string) {
	t.Helper()
	code, stdout, stderr := runWithStdin(t, stdin, append([]string{"guard", "check", "--host", "claude"}, args...)...)

	reason := hookDeny(t, stdout)
	if code != 0 || stderr != "" {
		t.Errorf("exit code %d, stderr %q; want 0 and nothing", code, stderr)
	}
	if !strings.Contains(reason, mention) {
		t.Errorf("reason %q does not carry %q", reason, mention)
	}
}

// TestGuardCheckHostFailsClosed_TS_06_62 verifies TS-06-62:
// with --host claude every internal error fails closed as a denial that
// carries the error, exit 0.
// Verifies: 06-REQ-8.8
func TestGuardCheckHostFailsClosed_TS_06_62(t *testing.T) {
	dir := goRepo(t)
	good := neutral(t, "Bash", map[string]any{"command": "git diff"})
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("a malformed payload", func(t *testing.T) {
		hostFailsClosed(t, strings.NewReader("{not json"), "not a JSON", "--dir", dir)
	})
	t.Run("an empty payload", func(t *testing.T) {
		hostFailsClosed(t, strings.NewReader(""), "empty", "--dir", dir)
	})
	t.Run("an oversized payload", func(t *testing.T) {
		hostFailsClosed(t, strings.NewReader(neutral(t, "Read", map[string]any{"pad": strings.Repeat("x", 1<<20+10)})), "1 MiB", "--dir", dir)
	})
	t.Run("a policy that does not parse", func(t *testing.T) {
		hostFailsClosed(t, strings.NewReader(good), bad, "--dir", dir, "--policy", bad)
	})
	t.Run("a panic of the policy engine", func(t *testing.T) {
		old := decide
		decide = func(policy.Policy, string, string, string, map[string]any) policy.Decision { panic("test hook") }
		t.Cleanup(func() { decide = old })
		hostFailsClosed(t, strings.NewReader(good), "test hook", "--dir", dir)
	})
	t.Run("a stdin that never ends", func(t *testing.T) {
		pr, pw := io.Pipe()
		t.Cleanup(func() { pw.Close() })
		start := time.Now()
		hostFailsClosed(t, pr, "in time", "--dir", dir, "--timeout", "2s")
		if time.Since(start) > 2*time.Second {
			t.Errorf("the hook took %s with a 2s timeout: the shell would have discarded its answer", time.Since(start))
		}
	})
}

// TestGuardCheckEnvelopeDoesNotFailClosed guards 06-REQ-8.6: without --host a
// panic of the policy engine is an internal failure, as for any command.
func TestGuardCheckEnvelopeDoesNotFailClosed(t *testing.T) {
	old := decide
	decide = func(policy.Policy, string, string, string, map[string]any) policy.Decision { panic("test hook") }
	t.Cleanup(func() { decide = old })

	code, stdout, _ := runWithStdin(t, strings.NewReader(neutral(t, "Bash", map[string]any{"command": "ls"})), "guard", "check", "--dir", goRepo(t))

	env := oneDocument(t, stdout)
	if code != 1 || env["status"] != "failed" || field(env, "error", "category") != "internal" {
		t.Errorf("exit code %d, envelope status %v, category %v", code, env["status"], field(env, "error", "category"))
	}
}

// TestOnlyGuardCheckHostPrintsADocument_TS_06_73 verifies TS-06-73:
// only af guard check --host claude emits a non-envelope stdout document, and
// it reads raw stdin.
// Verifies: 06-REQ-10.5
func TestOnlyGuardCheckHostPrintsADocument_TS_06_73(t *testing.T) {
	requireSh(t)
	dir := gitRepo(t, map[string]string{"go.mod": "module x\n"})
	writeGate(t, dir, []string{"ok", "sh", "-c", "exit 0"})
	base := baseOf(t, dir)
	payload := neutral(t, "Bash", map[string]any{"command": "git diff"})

	for _, args := range [][]string{
		{"lang", "detect", "--dir", dir},
		{"gate", "run", "--dir", dir},
		{"gate", "compare", "--dir", dir, "--before", "nosuch", "--after", "nosuch"},
		{"revert-check", "--dir", dir, "--since", base},
		{"guard", "check", "--dir", dir},
	} {
		_, stdout, _ := runWithStdin(t, strings.NewReader(payload), args...)
		if env := oneDocument(t, stdout); env["status"] == nil || env["schema_version"] == nil {
			t.Errorf("%v does not print an envelope: %s", args, stdout)
		}
	}
	_, stdout, _ := runWithStdin(t, strings.NewReader(payload), "guard", "check", "--dir", dir, "--host", "claude")
	if doc := oneDocument(t, stdout); doc["status"] != nil || len(doc) != 0 {
		t.Errorf("guard check --host claude printed %v, want {}", doc)
	}

	// Stdin is read as it is: not as an input that is trimmed and cut at 128
	// KiB. A payload of 400 KiB is read to its end.
	big := neutral(t, "Write", map[string]any{"file_path": "src/a.go", "content": strings.Repeat("y", 400<<10)})
	code, stdout, _ := runWithStdin(t, strings.NewReader(big), "guard", "check", "--dir", dir)
	env := oneDocument(t, stdout)
	if code != 0 || field(env, "result", "decision") != "allow" || field(env, "input", "kind") != "none" {
		t.Errorf("a payload of 400 KiB: exit code %d, result %v, input %v; want an allowance and input.kind none", code, env["result"], env["input"])
	}
	if warnings, _ := env["warnings"].([]any); len(warnings) != 0 {
		t.Errorf("warnings = %v: the payload was treated as an input", warnings)
	}
	padded := "   \n\n" + neutral(t, "Bash", map[string]any{"command": "go test ./..."}) + "\n\n  "
	if _, stdout, _ := runWithStdin(t, strings.NewReader(padded), "guard", "check", "--dir", dir); field(oneDocument(t, stdout), "result", "decision") != "deny" {
		t.Errorf("a payload with whitespace around it was not read: %s", stdout)
	}
}
