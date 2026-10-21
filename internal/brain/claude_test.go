package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// withPathOf sets PATH to the directories, and nothing else.
func withPathOf(t *testing.T, dirs ...string) {
	t.Helper()
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))
}

// hostWith is a host environment of the variables.
func hostWith(m map[string]string) Getenv {
	return func(name string) (string, bool) { v, ok := m[name]; return v, ok }
}

// TestClaudeNeedsTheVendorKeyExceptUnderNone_TS_10_47 verifies TS-10-47: without
// ANTHROPIC_API_KEY, claude fails as auth, with the variable in the hint, under local,
// landlock and podman, before any box opens; under none it is made.
// Verifies: 10-REQ-8.2
func TestClaudeNeedsTheVendorKeyExceptUnderNone_TS_10_47(t *testing.T) {
	bin, _, _ := fakeBin(t)
	withPathOf(t, bin)
	for _, provider := range []string{"local", "landlock", "podman", "none"} {
		b, err := Default.New(ClaudeName, Options{Provider: provider, Getenv: hostWith(nil)})

		if provider == "none" {
			if err != nil || b == nil {
				t.Errorf("none: %v, %v, want the brain: a person may be logged in", b, err)
			}
			continue
		}
		f := asFailure(t, err)
		if b != nil || f.Category != toolio.CategoryAuth || f.FixHint == nil || f.FixHint.Env != "ANTHROPIC_API_KEY" {
			t.Errorf("%s: %v, %+v, want failed/auth with the variable in the hint", provider, b, f)
		}
	}
	if _, err := Default.New(ClaudeName, Options{Provider: "local", Getenv: hostWith(map[string]string{"ANTHROPIC_API_KEY": ""})}); err == nil {
		t.Error("an empty key was taken for one")
	}
	if _, err := Default.New(ClaudeName, Options{Provider: "local", Getenv: hostWith(map[string]string{"ANTHROPIC_API_KEY": "sk"})}); err != nil {
		t.Errorf("with a key: %v", err)
	}
}

// TestClaudeMustBeOnThePathOfTheHostExceptUnderPodman_TS_10_48 verifies TS-10-48:
// without claude on PATH, local, landlock and none fail as brain, not retryable, naming
// claude, and podman, whose claude is the image's, does not need it.
// Verifies: 10-REQ-8.3
func TestClaudeMustBeOnThePathOfTheHostExceptUnderPodman_TS_10_48(t *testing.T) {
	withPathOf(t, t.TempDir())
	env := hostWith(map[string]string{"ANTHROPIC_API_KEY": "sk"})
	for _, provider := range []string{"local", "landlock", "none"} {
		b, err := Default.New(ClaudeName, Options{Provider: provider, Getenv: env})

		f := asFailure(t, err)
		if b != nil || f.Category != toolio.CategoryBrain || f.Retryable || !strings.Contains(f.Message, "claude") {
			t.Errorf("%s: %v, %+v, want failed/brain, not retryable, naming claude", provider, b, f)
		}
	}
	if b, err := Default.New(ClaudeName, Options{Provider: "podman", Getenv: env}); err != nil || b == nil {
		t.Errorf("podman: %v, %v, want the brain made without claude on the PATH of the host", b, err)
	}

	bin, _, _ := fakeBin(t)
	withPathOf(t, bin)
	if b, err := Default.New(ClaudeName, Options{Provider: "local", Getenv: env}); err != nil || b == nil {
		t.Errorf("with claude on PATH: %v, %v", b, err)
	}
}

// runClaude runs the brain on the brief of the attempt, with the script of the fake.
func (w *claudeWorld) runClaude(attempt int, mutate func(*Brief)) (Outcome, error) {
	w.t.Helper()
	b := w.processBrief(attempt)
	if mutate != nil {
		mutate(&b)
	}
	return w.brain.Run(context.Background(), b)
}

// TestTheGuardAndSettingsFilesArePlacedInTheBoxWithHostCopies_TS_10_49 verifies TS-10-49:
// guard.json (version 1, the protected paths and the programs of the hook, no suite)
// and claude-settings.json (one PreToolUse hook that matches every tool and runs af
// guard check on that policy, with a timeout of 30) are written into the run
// directory of the box, and copied to the one on the host.
// Verifies: 10-REQ-8.4
func TestTheGuardAndSettingsFilesArePlacedInTheBoxWithHostCopies_TS_10_49(t *testing.T) {
	for _, provider := range []string{"local", "none"} {
		t.Run(provider, func(t *testing.T) {
			w := newClaudeWorld(t, provider, map[string]string{"ANTHROPIC_API_KEY": "sk-test"}, "", false)

			_, err := w.runClaude(2, func(b *Brief) {
				b.Hook = HookConfig{RunID: w.run.ID, ProtectedPaths: []string{".agent-fox/", ".specs/"}, AllowPrograms: []string{"go", "git"}}
			})
			if err != nil {
				t.Fatal(err)
			}

			root := w.session.Root()
			dir := ".agent-fox/run/" + w.run.ID
			guard, err := w.boxFile(dir + "/guard.json")
			if err != nil {
				t.Fatalf("guard.json is not in the box: %v", err)
			}
			var policy map[string]any
			if err := json.Unmarshal([]byte(guard), &policy); err != nil {
				t.Fatal(err)
			}
			if len(policy) != 3 || policy["version"] != float64(1) || fmt.Sprint(policy["protected_paths"]) != "[.agent-fox/ .specs/]" || fmt.Sprint(policy["allow_programs"]) != "[go git]" {
				t.Errorf("guard.json = %v, want version 1 with the protected paths and the programs of the hook and no suite", policy)
			}
			settings, err := w.boxFile(dir + "/claude-settings.json")
			if err != nil {
				t.Fatalf("claude-settings.json is not in the box: %v", err)
			}
			var s struct {
				Hooks struct {
					PreToolUse []struct {
						Matcher string `json:"matcher"`
						Hooks   []struct {
							Type    string `json:"type"`
							Command string `json:"command"`
							Timeout int    `json:"timeout"`
						} `json:"hooks"`
					} `json:"PreToolUse"`
				} `json:"hooks"`
			}
			if err := json.Unmarshal([]byte(settings), &s); err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("%s guard check --host claude --dir %s --policy %s/%s/guard.json", w.af, root, root, dir)
			if len(s.Hooks.PreToolUse) != 1 || s.Hooks.PreToolUse[0].Matcher != "*" || len(s.Hooks.PreToolUse[0].Hooks) != 1 {
				t.Fatalf("claude-settings.json = %s, want one PreToolUse hook that matches every tool", settings)
			}
			if h := s.Hooks.PreToolUse[0].Hooks[0]; h.Type != "command" || h.Command != want || h.Timeout != 30 {
				t.Errorf("hook = %+v, want the command %q with a timeout of 30", h, want)
			}

			for _, name := range []string{"guard.json", "claude-settings.json"} {
				host, err := os.ReadFile(filepath.Join(w.run.Path, name))
				boxSide, _ := w.boxFile(dir + "/" + name)
				if err != nil || string(host) != boxSide {
					t.Errorf("the host copy of %s = %q, %v, want the bytes that are in the box", name, host, err)
				}
			}
		})
	}
}

// argValues are the arguments that follow the flag, up to the next flag.
func argValues(argv []string, flag string) []string {
	for i, a := range argv {
		if a == flag {
			var out []string
			for _, v := range argv[i+1:] {
				if strings.HasPrefix(v, "-") {
					break
				}
				out = append(out, v)
			}
			return out
		}
	}
	return nil
}

// TestTheClaudeCommandLineCarriesTheFlagsAndTheNote_TS_10_50 verifies TS-10-50: claude
// is run with -p, JSON output, the turn bound, dontAsk, the settings file, strict MCP
// config, the allowed and the disallowed tools and the fixed note as the appended system
// prompt; --model only when there is a model; and the brief is on its stdin and not
// among its arguments.
// Verifies: 10-REQ-8.5
func TestTheClaudeCommandLineCarriesTheFlagsAndTheNote_TS_10_50(t *testing.T) {
	for _, model := range []string{"", "m1"} {
		t.Run("model="+model, func(t *testing.T) {
			w := newClaudeWorld(t, "local", map[string]string{"ANTHROPIC_API_KEY": "sk-test"}, model, false)

			if _, err := w.runClaude(1, func(b *Brief) { b.MaxTurns = 17 }); err != nil {
				t.Fatal(err)
			}

			runs := w.runsOf(ProgramClaude)
			if len(runs) != 1 {
				t.Fatalf("claude ran %d times", len(runs))
			}
			argv := runs[0].Argv
			settings := w.session.Root() + "/.agent-fox/run/" + w.run.ID + "/claude-settings.json"
			if argv[0] != "-p" || fmt.Sprint(argValues(argv, "--output-format")) != "[json]" || fmt.Sprint(argValues(argv, "--max-turns")) != "[17]" ||
				fmt.Sprint(argValues(argv, "--permission-mode")) != "[dontAsk]" || fmt.Sprint(argValues(argv, "--settings")) != "["+settings+"]" ||
				!slices.Contains(argv, "--strict-mcp-config") {
				t.Errorf("argv = %q, want -p, JSON output, 17 turns, dontAsk, the settings file %s and --strict-mcp-config", argv, settings)
			}
			if len(argValues(argv, "--allowedTools")) == 0 || len(argValues(argv, "--disallowedTools")) == 0 {
				t.Errorf("argv = %q, want allowed and disallowed tools", argv)
			}
			note := argValues(argv, "--append-system-prompt")
			if len(note) != 1 || note[0] != claudeNote {
				t.Errorf("the appended system prompt = %q, want the fixed note", note)
			}
			for _, phrase := range []string{"headless", "one submission", "never commit", "push", "forge", "refusal", "next turn"} {
				if !strings.Contains(claudeNote, phrase) {
					t.Errorf("the note %q does not say %q", claudeNote, phrase)
				}
			}
			if got := argValues(argv, "--model"); model == "" && got != nil || model != "" && fmt.Sprint(got) != "["+model+"]" {
				t.Errorf("--model = %v with the model %q", got, model)
			}
			brief := w.processBrief(1).Document()
			if runs[0].StdinBytes != len(brief) {
				t.Errorf("claude read %d bytes on its stdin, want the brief of %d", runs[0].StdinBytes, len(brief))
			}
			for _, a := range argv {
				if strings.Contains(a, "PREFIX:") {
					t.Errorf("the brief is among the arguments: %q", a)
				}
			}
		})
	}
}

// wantLists is the allow and disallow lists of the spec for a set of tools.
func wantLists(tools []string, agent string) (allowed, disallowed []string) {
	table := map[string][]string{
		"read_file": {"Read"}, "list_files": {"LS", "Glob"}, "find_files": {"Glob"}, "search_files": {"Grep"},
		"write_file": {"Write"}, "edit_file": {"Edit", "MultiEdit"}, "execute": {"Bash"},
	}
	writer := false
	for _, tool := range tools {
		for _, c := range table[tool] {
			if !slices.Contains(allowed, c) {
				allowed = append(allowed, c)
			}
		}
		writer = writer || tool == "write_file" || tool == "edit_file"
	}
	allowed = append(allowed, "Bash(af "+agent+" submit:*)")
	disallowed = []string{"WebFetch", "WebSearch"}
	if !writer {
		disallowed = append(disallowed, "Write", "Edit", "MultiEdit", "NotebookEdit")
	}
	return allowed, disallowed
}

// TestTheToolTableDrivesTheAllowAndDisallowLists_TS_10_51 verifies TS-10-51: for every
// subset of the seven box tools, with and without tools that have no equivalent, the
// allowed tools are the union of the mapped ones plus the submit command, the web is
// always disallowed, the writers are disallowed when there is none, and what has no
// equivalent is dropped.
// Verifies: 10-REQ-8.6
func TestTheToolTableDrivesTheAllowAndDisallowLists_TS_10_51(t *testing.T) {
	mapped := []string{"read_file", "list_files", "find_files", "search_files", "write_file", "edit_file", "execute"}
	unmapped := []string{"file_outline", "find_symbol", "find_references", "code_search", "run_command"}
	for _, agent := range []string{"triage", "fix"} {
		for mask := range 1 << len(mapped) {
			var tools []string
			for i, name := range mapped {
				if mask&(1<<i) != 0 {
					tools = append(tools, name)
				}
			}
			for _, extras := range [][]string{nil, unmapped} {
				got := claudeToolLists(append(slices.Clone(tools), extras...), agent)
				wantAllowed, wantDisallowed := wantLists(tools, agent)
				if !sameSet(got.Allowed, wantAllowed) || !sameSet(got.Disallowed, wantDisallowed) {
					t.Fatalf("%s %v + %v: allowed %v disallowed %v, want %v and %v", agent, tools, extras, got.Allowed, got.Disallowed, wantAllowed, wantDisallowed)
				}
				if !slices.Contains(got.Allowed, "Bash(af "+agent+" submit:*)") || !slices.Contains(got.Disallowed, "WebFetch") || !slices.Contains(got.Disallowed, "WebSearch") {
					t.Fatalf("%v: allowed %v disallowed %v, want the submit command allowed and the web disallowed", tools, got.Allowed, got.Disallowed)
				}
				if !sameSet(got.Dropped, extras) {
					t.Fatalf("%v + %v: dropped %v, want %v", tools, extras, got.Dropped, extras)
				}
				for _, a := range got.Allowed {
					if slices.Contains(got.Disallowed, a) {
						t.Fatalf("%v: %s is allowed and disallowed", tools, a)
					}
				}
			}
		}
	}
}

func sameSet(a, b []string) bool {
	a, b = slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b))
	return slices.Equal(a, b)
}

// TestEachToolWithNoEquivalentIsDroppedInTheLedger verifies the record of 10-REQ-8.6: a
// run whose tools include some that Claude Code has no tool for notes each one.
func TestEachToolWithNoEquivalentIsDroppedInTheLedger(t *testing.T) {
	w := newClaudeWorld(t, "local", map[string]string{"ANTHROPIC_API_KEY": "sk-test"}, "", false)

	_, err := w.runClaude(1, func(b *Brief) {
		b.Tools = []string{"read_file", "file_outline", "find_symbol", "execute"}
	})
	if err != nil {
		t.Fatal(err)
	}

	kinds, data := w.ledger()
	var dropped []string
	for i, k := range kinds {
		if k == "claude_tool_dropped" {
			var m map[string]any
			_ = json.Unmarshal(data[i], &m)
			dropped = append(dropped, fmt.Sprint(m["tool"]))
		}
	}
	if !sameSet(dropped, []string{"file_outline", "find_symbol"}) {
		t.Errorf("claude_tool_dropped entries = %v, want file_outline and find_symbol", dropped)
	}
	runs := w.runsOf(ProgramClaude)
	if len(runs) != 1 {
		t.Fatalf("claude ran %d times", len(runs))
	}
	argv := runs[0].Argv
	if allowed := argValues(argv, "--allowedTools"); !sameSet(allowed, []string{"Read", "Bash", "Bash(af fix submit:*)"}) {
		t.Errorf("--allowedTools = %v, want Read, Bash and the submit command of fix", allowed)
	}
}

// TestTheHookCommandOfTheSettingsDeniesGoTestAndSubmissionWritesAndAllowsGitDiff_TS_10_52
// verifies TS-10-52: the PreToolUse command of the settings file, run with the payloads
// of Claude Code, denies go test ./... naming af gate run, denies a write to
// submission.json, and answers {} to git diff, each with exit code 0.
// Verifies: 10-REQ-8.7
func TestTheHookCommandOfTheSettingsDeniesGoTestAndSubmissionWritesAndAllowsGitDiff_TS_10_52(t *testing.T) {
	w := newClaudeWorld(t, "local", map[string]string{"ANTHROPIC_API_KEY": "sk-test"}, "", true)
	for name, content := range map[string]string{"go.mod": "module example.test/m\n\ngo 1.22\n", "m.go": "package m\n", "m_test.go": "package m\n\nimport \"testing\"\n\nfunc TestM(t *testing.T) {}\n"} {
		if err := os.WriteFile(filepath.Join(w.dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, w.dir, "add", ".")
	git(t, w.dir, "commit", "-q", "-m", "a go module")
	if err := w.session.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	root := w.session.Root()
	payload := func(tool string, input map[string]any) json.RawMessage {
		data, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": input, "cwd": root})
		return data
	}
	w.script(fakeScript{HookPayloads: []json.RawMessage{
		payload("Bash", map[string]any{"command": "go test ./..."}),
		payload("Write", map[string]any{"file_path": root + "/.agent-fox/run/" + w.run.ID + "/submission.json", "content": "{}"}),
		payload("Bash", map[string]any{"command": "git diff"}),
	}})

	_, err := w.runClaude(1, func(b *Brief) {
		b.Hook = HookConfig{RunID: w.run.ID, ProtectedPaths: []string{".agent-fox/"}, AllowPrograms: []string{}}
	})
	if err != nil {
		t.Fatal(err)
	}

	answers := w.answers(fakeHooksPath)
	if len(answers) != 3 {
		t.Fatalf("%d answers of the hook, want 3: %v", len(answers), answers)
	}
	for i, a := range answers {
		if a.Exit != 0 {
			t.Errorf("answer %d ended with %d, want 0", i, a.Exit)
		}
	}
	for i, names := range []string{"af gate run", ""} {
		var doc struct {
			HookSpecificOutput struct {
				PermissionDecision       string `json:"permissionDecision"`
				PermissionDecisionReason string `json:"permissionDecisionReason"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal([]byte(answers[i].Stdout), &doc); err != nil || doc.HookSpecificOutput.PermissionDecision != "deny" || !strings.Contains(doc.HookSpecificOutput.PermissionDecisionReason, names) {
			t.Errorf("answer %d = %q, want a deny that names %q", i, answers[i].Stdout, names)
		}
	}
	if answers[2].Stdout != "{}" {
		t.Errorf("answer 2 = %q, want {}", answers[2].Stdout)
	}
}

// TestOutputAndErrorsAreCopiedToTheHostWithTheirCaps_TS_10_44 verifies TS-10-44: the
// brief is on the stdin of the process, its output and errors go to
// .agent-fox/run/<id>/claude.<attempt>.json and .stderr, the copy of the output is at
// most 1 MiB, the copy of the errors is the last 64 KiB, and both are brain_output
// artifacts; and no submission is made of what the output says.
// Verifies: 10-REQ-7.7
func TestOutputAndErrorsAreCopiedToTheHostWithTheirCaps_TS_10_44(t *testing.T) {
	for _, provider := range []string{"local", "none"} {
		t.Run(provider, func(t *testing.T) {
			w := newClaudeWorld(t, provider, map[string]string{"ANTHROPIC_API_KEY": "sk-test"}, "", false)
			w.script(fakeScript{StdoutBytes: 2 << 20, StderrBytes: 200 << 10, Stderr: "TAIL-MARK"})

			if _, err := w.runClaude(3, nil); err != nil {
				t.Fatal(err)
			}

			runs := w.runsOf(ProgramClaude)
			if len(runs) != 1 || runs[0].StdinBytes != len(w.processBrief(3).Document()) {
				t.Fatalf("claude ran %d times, and read %v bytes on stdin", len(runs), runs)
			}
			dir := ".agent-fox/run/" + w.run.ID
			// In a box of its own the file stays as the process wrote it; in place it is the
			// host's copy, and the copy replaces it.
			boxSize := 2 << 20
			if provider == "none" {
				boxSize = 1 << 20
			}
			if data, err := w.boxFile(dir + "/claude.3.json"); err != nil || len(data) != boxSize {
				t.Errorf("the output file in the box has %d bytes (%v), want %d", len(data), err, boxSize)
			}
			jsonCopy, err := os.ReadFile(filepath.Join(w.run.Path, "claude.3.json"))
			if err != nil || len(jsonCopy) != 1<<20 {
				t.Errorf("the host copy of the output has %d bytes (%v), want 1 MiB", len(jsonCopy), err)
			}
			errCopy, err := os.ReadFile(filepath.Join(w.run.Path, "claude.3.stderr"))
			if err != nil || len(errCopy) != 64<<10 || !strings.HasSuffix(string(errCopy), "TAIL-MARK") {
				t.Errorf("the host copy of the errors has %d bytes ending %q (%v), want the last 64 KiB", len(errCopy), string(errCopy[max(0, len(errCopy)-12):]), err)
			}
			var outputs []string
			for _, a := range w.report.artifacts {
				if a.Kind == "brain_output" {
					outputs = append(outputs, filepath.Base(a.Path))
				}
			}
			if !sameSet(outputs, []string{"claude.3.json", "claude.3.stderr"}) {
				t.Errorf("brain_output artifacts = %v, want the two copies", outputs)
			}
		})
	}
}

// TestWhatTheOutputSaysDoesNotMakeASubmission verifies the other half of TS-10-44: a
// process whose output says it submitted, and that submitted nothing, has no
// submission, and the run fails for that.
func TestWhatTheOutputSaysDoesNotMakeASubmission(t *testing.T) {
	w := newClaudeWorld(t, "local", map[string]string{"ANTHROPIC_API_KEY": "sk-test"}, "", false)
	w.script(fakeScript{OutputJSON: `{"type":"result","subtype":"success","is_error":false,"result":"I submitted it","num_turns":2,"total_cost_usd":0.5}`})
	req := w.request(w.brief(1), w.brain)
	req.Name = ClaudeName
	req.Heartbeat = 0

	_, err := Execute(context.Background(), req)

	f := asFailure(t, err)
	if f.Category != toolio.CategoryNoResult {
		t.Errorf("failure = %+v, want no_result: the file decides, not the output", f)
	}
}

// TestTheEnvironmentOfTheProcessIsTheVendorKeyAndNoForgeTokenUnderLocalAndNone_TS_10_43
// verifies TS-10-43: for any host environment with GITHUB_TOKEN, GH_TOKEN, GITLAB_TOKEN
// and ANTHROPIC_API_KEY set, and any other variables, the claude process under local and
// under none sees the key with the host's value, no forge token, and nothing outside
// the allowlist.
// Verifies: 10-REQ-7.6
func TestTheEnvironmentOfTheProcessIsTheVendorKeyAndNoForgeTokenUnderLocalAndNone_TS_10_43(t *testing.T) {
	rng := rand.New(rand.NewPCG(10, 43))
	for i := range 4 {
		host := map[string]string{
			"GITHUB_TOKEN": fmt.Sprintf("ghp-%d", i), "GH_TOKEN": fmt.Sprintf("gh-%d", i), "GITLAB_TOKEN": fmt.Sprintf("glpat-%d", i),
			"ANTHROPIC_API_KEY": fmt.Sprintf("sk-vendor-%d", i),
		}
		for j := range rng.IntN(6) {
			host[fmt.Sprintf("EXTRA_%d_%d", i, j)] = fmt.Sprintf("extra-%d-%d", i, j)
		}
		for _, name := range []string{"HTTPS_PROXY", "SSL_CERT_FILE", "AWS_SECRET_ACCESS_KEY", "SSH_AUTH_SOCK"} {
			if rng.IntN(2) == 0 {
				host[name] = "set-" + name
			}
		}
		for _, provider := range []string{"local", "none"} {
			w := newClaudeWorld(t, provider, host, "", false)

			if _, err := w.runClaude(1, nil); err != nil {
				t.Fatal(err)
			}

			runs := w.runsOf(ProgramClaude)
			if len(runs) != 1 {
				t.Fatalf("#%d %s: claude ran %d times", i, provider, len(runs))
			}
			seen := map[string]string{}
			for _, kv := range runs[0].Env {
				name, value, _ := strings.Cut(kv, "=")
				seen[name] = value
			}
			if seen["ANTHROPIC_API_KEY"] != host["ANTHROPIC_API_KEY"] {
				t.Fatalf("#%d %s: the process saw the key %q, want the host's %q", i, provider, seen["ANTHROPIC_API_KEY"], host["ANTHROPIC_API_KEY"])
			}
			for name, value := range seen {
				if slices.Contains([]string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN"}, name) {
					t.Fatalf("#%d %s: the process saw %s", i, provider, name)
				}
				if !slices.Contains([]string{"PATH", "HOME", "ANTHROPIC_API_KEY", "LANG", "TMPDIR", "TZ", "LC_ALL", "HTTPS_PROXY", "SSL_CERT_FILE"}, name) {
					t.Fatalf("#%d %s: the process saw %s=%s, which is not on the list", i, provider, name, value)
				}
			}
		}
	}
}
