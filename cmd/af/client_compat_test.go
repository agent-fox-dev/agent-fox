package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/afcmd"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// localEnv is a repository, a fake forge that serves its issue 7, and a shell with the forge in it:
// what a local triage needs to run to a diagnosis with the scripted brain.
type localEnv struct {
	t     *testing.T
	dir   string
	forge *fakeforge.Forge
	app   *toolio.App
}

const originalReport = "The login button does nothing.\nIt used to work."

// newLocalEnv makes a local environment whose shell is app (nil is the binary's).
func newLocalEnv(t *testing.T, app *toolio.App) *localEnv {
	t.Helper()
	requireGit(t)
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "GITHUB_API_URL", "GITLAB_API_URL", "ANTHROPIC_API_KEY"} {
		t.Setenv(name, "")
	}
	e := &localEnv{t: t, forge: fakeforge.New(t, fakeforge.GitHub, "o/r")}
	e.dir = gitRepo(t, map[string]string{
		"go.mod":       "module example.com/x\n",
		"main.go":      "package main\n\nimport \"example.com/x/util\"\n\n// Run starts the program.\nfunc Run() {\n\tutil.Help()\n}\n\nfunc main() { Run() }\n",
		"util/util.go": "package util\n\n// Help helps.\nfunc Help() {}\n",
	})
	e.forge.AddIssue(fakeforge.Issue{Number: 7, Title: "Login fails", Body: originalReport})
	if app == nil {
		app = newApp()
	}
	e.app = app
	e.app.NewForge = e.forge.NewForge("test-token")
	return e
}

// run runs the shell of the environment with args and returns the exit code and the envelope.
func (e *localEnv) run(stdin string, args ...string) (int, map[string]any) {
	e.t.Helper()
	var out, errb bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	code := e.app.Main(ctx, append([]string{"af"}, args...), strings.NewReader(stdin), &out, &errb)
	return code, oneDocument(e.t, out.String())
}

// diagnosis is a payload of the scripted brain: a diagnosis of the confidence.
func diagnosis(confidence string) string {
	data, _ := json.Marshal(map[string]any{
		"title": "Run starts the wrong thing", "severity": "high", "confidence": confidence,
		"root_cause":          "Run calls Help before it checks its arguments.",
		"affected_files":      []any{map[string]any{"path": "main.go", "line": 6, "note": "Run"}},
		"suggested_fix":       "Check the arguments first.",
		"acceptance_criteria": []any{"Run with no arguments prints the usage"},
	})
	return string(data)
}

// scriptOf writes a script of the scripted brain that submits the payload and returns its path.
func scriptOf(t *testing.T, payload string) string {
	t.Helper()
	var p any
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"steps": []any{map[string]any{"submit": p, "expect": "accepted"}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// loopArgs are the arguments of a local triage of issue 7 with the scripted brain.
func (e *localEnv) loopArgs(confidence string, extra ...string) []string {
	return append([]string{"triage", e.forge.IssueURL(7), "--dir", e.dir, "--sandbox", "local", "--brain", "scripted", "--script", scriptOf(e.t, diagnosis(confidence))}, extra...)
}

// nextArgvs are the argv of the next entries of an envelope.
func nextArgvs(env map[string]any) [][]string {
	var out [][]string
	next, _ := env["next"].([]any)
	for _, n := range next {
		var argv []string
		for _, a := range n.(map[string]any)["argv"].([]any) {
			argv = append(argv, a.(string))
		}
		out = append(out, argv)
	}
	return out
}

// normalized is the envelope without what changes from one run to the next: the run id, the times,
// the durations, and the directories, which differ between two environments.
func normalized(t *testing.T, env map[string]any, dirs ...string) string {
	t.Helper()
	var scrub func(v any) any
	scrub = func(v any) any {
		switch v := v.(type) {
		case map[string]any:
			out := map[string]any{}
			for k, x := range v {
				if k == "duration_ms" || k == "ts" || strings.HasSuffix(k, "_at") {
					continue
				}
				out[k] = scrub(x)
			}
			return out
		case []any:
			out := make([]any, len(v))
			for i, x := range v {
				out[i] = scrub(x)
			}
			return out
		}
		return v
	}
	data, err := json.Marshal(scrub(env))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if id, _ := env["run_id"].(string); id != "" {
		text = strings.ReplaceAll(text, id, "<run>")
	}
	for _, d := range dirs {
		text = strings.ReplaceAll(text, d, "<dir>")
	}
	return text
}

// runFiles are the files of the run directory of an envelope, by name.
func (e *localEnv) runFiles(env map[string]any) []string {
	e.t.Helper()
	id, _ := env["run_id"].(string)
	entries, err := os.ReadDir(filepath.Join(e.dir, ".agent-fox", "run", id))
	if err != nil {
		e.t.Fatal(err)
	}
	var names []string
	for _, ent := range entries {
		names = append(names, ent.Name())
	}
	return names
}

// eventLines are the events of the run directory of an envelope, in order, without what changes from one run
// to the next (times, the run id, the directory) and without the argv of run_start.
func (e *localEnv) eventLines(env map[string]any) []string {
	e.t.Helper()
	id, _ := env["run_id"].(string)
	data, err := os.ReadFile(filepath.Join(e.dir, ".agent-fox", "run", id, "events.jsonl"))
	if err != nil {
		e.t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			e.t.Fatal(err)
		}
		if ev["type"] == "run_start" {
			delete(ev["data"].(map[string]any), "argv")
		}
		lines = append(lines, normalized(e.t, ev, e.dir))
	}
	return lines
}

// eventTypes are the types of the events of the run directory of an envelope, in order.
func (e *localEnv) eventTypes(env map[string]any) []string {
	e.t.Helper()
	id, _ := env["run_id"].(string)
	data, err := os.ReadFile(filepath.Join(e.dir, ".agent-fox", "run", id, "events.jsonl"))
	if err != nil {
		e.t.Fatal(err)
	}
	var types []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var ev struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			e.t.Fatal(err)
		}
		types = append(types, ev.Type)
	}
	return types
}

// countingListener listens on the loopback interface and counts the connections made to it.
func countingListener(t *testing.T) (addr string, connections *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var n atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			n.Add(1)
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String(), &n
}

// TestTheBinaryRunsTriageAsTheShellOfAfcmdDoesEvenWithAFURLNamingAService_TS_16_12 verifies
// TS-16-12 as it stands: the same triage, run through the shell of afcmd (the way the worker runs
// it) and through the binary's shell, gives the same statuses, exit codes, results, events,
// warnings and run directory, and calls no service.
// Verifies: 16-REQ-2.4
func TestTheBinaryRunsTriageAsTheShellOfAfcmdDoesEvenWithAFURLNamingAService_TS_16_12(t *testing.T) {
	addr, connections := countingListener(t)
	t.Setenv("AF_URL", "http://"+addr)

	before := newLocalEnv(t, afcmd.New(&afcmd.Config{}))
	beforeCode, beforeEnv := before.run("", before.loopArgs("confirmed")...)
	after := newLocalEnv(t, nil)
	afterCode, afterEnv := after.run("", after.loopArgs("confirmed")...)

	if beforeCode != 0 || afterCode != beforeCode {
		t.Fatalf("exit codes %d before and %d after, want both 0 (%v)", beforeCode, afterCode, afterEnv["error"])
	}
	if len(nextArgvs(afterEnv)) == 0 {
		t.Fatalf("next = %v, want the suggestion of a fix", nextArgvs(afterEnv))
	}
	if b, a := normalized(t, beforeEnv, before.dir), normalized(t, afterEnv, after.dir); b != a {
		t.Errorf("the envelope differs:\nbefore: %s\nafter:  %s", b, a)
	}
	if b, a := before.runFiles(beforeEnv), after.runFiles(afterEnv); !slices.Equal(b, a) {
		t.Errorf("the run directory holds %v before and %v after", b, a)
	}
	if b, a := before.eventLines(beforeEnv), after.eventLines(afterEnv); !slices.Equal(b, a) {
		t.Errorf("the events differ:\nbefore: %q\nafter:  %q", b, a)
	}
	if types := after.eventTypes(afterEnv); len(types) < 3 || types[0] != "run_start" || types[len(types)-1] != "run_end" {
		t.Errorf("the events are %v, want a run from run_start to run_end", types)
	}
	if n := connections.Load(); n != 0 {
		t.Errorf("%d connections were made to the service named by AF_URL", n)
	}
}

// TestTriagesFixSuggestionRunsAsGiven_TS_16_13 verifies TS-16-13 as it stands: the loop suggests
// af fix of the issue in the checkout, and the binary takes the suggestion as the loop.
// Verifies: 16-REQ-2.5
func TestTriagesFixSuggestionRunsAsGiven_TS_16_13(t *testing.T) {
	hasFix := func(env map[string]any, url, dir string) []string {
		for _, argv := range nextArgvs(env) {
			if slices.Equal(argv, []string{"af", "fix", url, "--dir", dir}) {
				return argv
			}
		}
		t.Errorf("next = %v, want af fix %s --dir %s", nextArgvs(env), url, dir)
		return nil
	}

	var suggestion []string
	for _, confidence := range []string{"confirmed", "probable"} {
		e := newLocalEnv(t, nil)
		code, env := e.run("", e.loopArgs(confidence)...)
		if code != 0 {
			t.Fatalf("%s: exit %d, %v", confidence, code, env["error"])
		}
		suggestion = hasFix(env, e.forge.IssueURL(7), e.dir)
	}

	e := newLocalEnv(t, nil)
	// The suggestion runs as given: the binary takes it as the loop, with no flag it does not know.
	if suggestion != nil {
		res := e.run2(suggestion[1:]...)
		if strings.Contains(res, "unknown flag") || strings.Contains(res, "unexpected argument") {
			t.Errorf("running the suggestion gave a usage error of its words: %s", res)
		}
	}
}

// run2 runs the shell of the environment and returns the message of the error of its envelope.
func (e *localEnv) run2(args ...string) string {
	e.t.Helper()
	_, env := e.run("", args...)
	msg, _ := field(env, "error", "message").(string)
	return msg
}

// TestTheLoopsRunTheSameInProcessAndThroughTheBinaryAndNeverContactAService_TS_16_14 verifies
// TS-16-14 as it stands.
// Verifies: 16-REQ-2.6
func TestTheLoopsRunTheSameInProcessAndThroughTheBinaryAndNeverContactAService_TS_16_14(t *testing.T) {
	addr, connections := countingListener(t)
	t.Setenv("AF_URL", "http://"+addr)
	dir := inDir(t)
	adir := filepath.Join(dir, "adir")
	if err := os.Mkdir(adir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"fix", "triage"} {
		// The way the worker makes its shell: afcmd.New, with no hook of the binary.
		var out bytes.Buffer
		app := afcmd.New(&afcmd.Config{})
		code := app.Main(context.Background(), []string{"af", cmd, adir, "--dir", dir}, strings.NewReader(""), &out, &bytes.Buffer{})
		env := oneDocument(t, out.String())
		msg, _ := field(env, "error", "message").(string)
		if code != 2 || !strings.Contains(msg, "adir") {
			t.Errorf("%s: exit %d, message %q, want the loop's own refusal of the directory", cmd, code, msg)
		}
		// The binary's shell refuses the same words the same way.
		if res := afRun(t, "", cmd, adir, "--dir", dir); res.code != 2 || res.message() != msg {
			t.Errorf("%s through the binary: exit %d, message %q, want %q", cmd, res.code, res.message(), msg)
		}
	}
	if n := connections.Load(); n != 0 {
		t.Errorf("%d connections were made to the service named by AF_URL", n)
	}
}

// TestAFixThroughTheBinaryIsTheLoopOfAfcmd_TS_16_12 verifies TS-16-12 for af fix: the same fix, run
// through the shell of afcmd and through the binary's shell, ends the same way, with the
// same envelope and run directory. The fix is refused by its preflight (the working tree is not clean), which
// is the first thing the loop does after the forge has been read.
// Verifies: 16-REQ-2.4
func TestAFixThroughTheBinaryIsTheLoopOfAfcmd_TS_16_12(t *testing.T) {
	addr, connections := countingListener(t)
	t.Setenv("AF_URL", "http://"+addr)
	run := func(app *toolio.App, extra ...string) (*localEnv, int, map[string]any) {
		e := newLocalEnv(t, app)
		if err := os.WriteFile(filepath.Join(e.dir, "dirty.txt"), []byte("not committed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		args := append([]string{"fix", e.forge.IssueURL(7), "--dir", e.dir, "--sandbox", "local", "--brain", "scripted", "--script", scriptOf(t, diagnosis("confirmed"))}, extra...)
		code, env := e.run("", args...)
		return e, code, env
	}
	before, beforeCode, beforeEnv := run(afcmd.New(&afcmd.Config{}))
	after, afterCode, afterEnv := run(nil)
	if beforeCode != 1 || afterCode != beforeCode || field(beforeEnv, "error", "category") != "preflight" {
		t.Fatalf("exit codes %d before and %d after, category %v, want both 1 and preflight", beforeCode, afterCode, field(beforeEnv, "error", "category"))
	}
	if b, a := normalized(t, beforeEnv, before.dir), normalized(t, afterEnv, after.dir); b != a {
		t.Errorf("the envelope differs:\nbefore: %s\nafter:  %s", b, a)
	}
	if b, a := before.eventLines(beforeEnv), after.eventLines(afterEnv); !slices.Equal(b, a) {
		t.Errorf("the events differ:\nbefore: %q\nafter:  %q", b, a)
	}
	if b, a := before.runFiles(beforeEnv), after.runFiles(afterEnv); !slices.Equal(b, a) {
		t.Errorf("the run directory holds %v before and %v after", b, a)
	}
	if n := connections.Load(); n != 0 {
		t.Errorf("%d connections were made to the service named by AF_URL", n)
	}
}
