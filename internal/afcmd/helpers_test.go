package afcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/provider/faux"
	"github.com/agent-fox-dev/agentkit-go/sandbox"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/fakeaf"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// The identity of the af of these tests, which the af of the boxes has too: the test binary.
const testVersion, testCommit = "v-test", "c-test"

// afBin is a directory whose af is the test binary run as the af of this package, for a box
// that finds af on its PATH.
var afBin string

// TestMain makes the test binary the af that the boxes run: run as af, it is the app of this
// package. The boxes run it through a script that gives it a GOCOVERDIR, since a binary built
// with -cover prints a warning without one, and a box hands that back with what af printed.
func TestMain(m *testing.M) {
	if fakeaf.Name() != "" {
		os.Exit(New(&Config{Version: testVersion, Commit: testCommit}).Main(context.Background(), os.Args, os.Stdin, os.Stdout, os.Stderr))
	}
	dir, af, err := fakeaf.LinkSelf()
	if err == nil {
		afBin = filepath.Join(dir, "bin")
		wrapper := fmt.Sprintf("#!/bin/sh\nGOCOVERDIR='%s' exec '%s' \"$@\"\n", filepath.Join(dir, "cover"), af)
		err = firstErr(os.MkdirAll(filepath.Join(dir, "cover"), 0o755), os.MkdirAll(afBin, 0o755), os.WriteFile(filepath.Join(afBin, "af"), []byte(wrapper), 0o755))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot make the af of the boxes: %v\n", err)
		os.Exit(1)
	}
	brain.DefaultAFPath = filepath.Join(afBin, "af")
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// out is what an invocation answered: the exit code and the envelope.
type out struct {
	code int
	toolio.Envelope
}

func (o out) res() map[string]any { m, _ := o.Result.(map[string]any); return m }

func (o out) category() string {
	if o.Error == nil {
		return ""
	}
	return o.Error.Category
}

func (o out) message() string {
	if o.Error == nil {
		return ""
	}
	return o.Error.Message
}

func (o out) hint() toolio.FixHint {
	if o.Error == nil || o.Error.FixHint == nil {
		return toolio.FixHint{}
	}
	return *o.Error.FixHint
}

func (o out) warned(code string) bool {
	return slices.ContainsFunc(o.Warnings, func(w toolio.Warning) bool { return w.Code == code })
}

// artifacts is how many artifacts of the kind the envelope lists.
func (o out) artifacts(kind string) int {
	n := 0
	for _, a := range o.Artifacts {
		if a.Kind == kind {
			n++
		}
	}
	return n
}

func (o out) String() string {
	return fmt.Sprintf("exit code %d, status %s, error %+v, warnings %v, result %v", o.code, o.Status, o.Error, o.Warnings, o.Result)
}

// invoke runs the command line on the app, and fails the test unless it answered one envelope.
func invoke(t *testing.T, app *toolio.App, stdin string, args ...string) out {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), append([]string{"af"}, args...), strings.NewReader(stdin), &stdout, &stderr)
	return out{code: code, Envelope: testutil.Decode[toolio.Envelope](t, stdout.Bytes())}
}

// env is a checkout on main, a fake forge with an issue 7, and the Config of the commands:
// that forge, a box registry whose local provider counts the boxes it opens, the brains of
// the release, and an environment of vars alone.
type env struct {
	t      *testing.T
	dir    string
	forge  *fakeforge.Forge
	vars   map[string]string
	cfg    Config
	opened atomic.Int32
}

// newEnv makes the environment over a checkout of files, with a bare origin when origin is set.
func newEnv(t *testing.T, files map[string]string, origin bool) *env {
	t.Helper()
	testutil.Require(t, "git", "sh", "make")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN"} { // a forge client reads them
		t.Setenv(name, "")
	}
	e := &env{t: t, dir: testutil.Repo(t, files), forge: fakeforge.New(t, fakeforge.GitHub, "o/r"), vars: map[string]string{}}
	for _, kv := range [][]string{{"user.name", "Test"}, {"user.email", "test@example.com"}, {"commit.gpgsign", "false"}} {
		testutil.Git(t, e.dir, "config", kv[0], kv[1])
	}
	if origin {
		bare := filepath.Join(t.TempDir(), "origin.git")
		testutil.Git(t, e.dir, "init", "-q", "--bare", "-b", "main", bare)
		testutil.Git(t, e.dir, "remote", "add", "origin", bare)
		testutil.Git(t, e.dir, "push", "-q", "-u", "origin", "main")
	}
	e.forge.AddIssue(fakeforge.Issue{Number: 7, Title: "Greeting is wrong", Body: "The greeting should say hello."})

	local, err := box.Default.Lookup("local")
	if err != nil {
		t.Fatal(err)
	}
	counted := local
	counted.New = func() sandbox.Provider { return countingProvider{Provider: local.New(), opened: &e.opened} }
	boxes := box.NewRegistry()
	boxes.Register(counted)
	brains := brain.NewRegistry()
	for _, name := range []string{brain.AgentkitName, brain.ScriptedName} {
		entry, err := brain.Default.Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		brains.Register(entry)
	}
	e.cfg = Config{
		Version: testVersion, Commit: testCommit, Forge: e.forge.NewForge("test-token"), Boxes: boxes, Brains: brains,
		Brain: brain.Options{Getenv: func(name string) (string, bool) { v, ok := e.vars[name]; return v, ok }},
	}
	return e
}

type countingProvider struct {
	sandbox.Provider
	opened *atomic.Int32
}

func (c countingProvider) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	c.opened.Add(1)
	return c.Provider.Create(ctx, spec)
}

// af runs the command line on an App made from the Config of the environment.
func (e *env) af(args ...string) out {
	e.t.Helper()
	return invoke(e.t, New(&e.cfg), "", args...)
}

// triage runs af triage on the input in the checkout, in the local box unless args say otherwise.
func (e *env) triage(input string, args ...string) out {
	e.t.Helper()
	return e.af(append([]string{"triage", input, "--dir", e.dir, "--sandbox", "local"}, args...)...)
}

// fix runs af fix on the input in the checkout, in the local box unless args say otherwise.
func (e *env) fix(input string, args ...string) out {
	e.t.Helper()
	return e.af(append([]string{"fix", input, "--dir", e.dir, "--sandbox", "local"}, args...)...)
}

// runDir is the run directory of the invocation.
func (e *env) runDir(o out) string {
	e.t.Helper()
	if o.RunID == nil {
		e.t.Fatalf("the envelope has no run id: %v", o)
	}
	return filepath.Join(e.dir, ".agent-fox", "run", *o.RunID)
}

// git runs git in the checkout and returns what it printed, trimmed.
func (e *env) git(args ...string) string {
	e.t.Helper()
	return strings.TrimSpace(testutil.Git(e.t, e.dir, args...))
}

// spyBrain is a brain that keeps the brief it was given and does what run says; with no run
// it ends with nothing.
type spyBrain struct {
	brief *brain.Brief
	runs  int
	made  int
	run   func(ctx context.Context, b brain.Brief) (brain.Outcome, error)
}

func (s *spyBrain) Run(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
	s.runs++
	s.brief = &b
	if s.run != nil {
		return s.run(ctx, b)
	}
	return brain.Outcome{StopReason: brain.StopEnded}, nil
}

// spy puts s in the brain registry of the environment as the brain "spy", and returns it.
func (e *env) spy(s *spyBrain) *spyBrain {
	e.cfg.Brains.Register(brain.Entry{Name: "spy", CostSource: brain.CostNone, New: func(brain.Options) (brain.Brain, error) { s.made++; return s, nil }})
	return s
}

// faux makes the agentkit brain of the environment talk to a faux provider of the turns.
func (e *env) faux(turns ...faux.Turn) *faux.Provider {
	p := faux.New(turns...)
	e.cfg.Brain.Model = "faux-1"
	e.cfg.Brain.NewProvider = func(string) (core.ProviderClient, error) { return p, nil }
	return p
}

// prefixOf is the text of the prefix of a request to the model.
func prefixOf(req core.Request) string {
	var parts []string
	for _, m := range req.Prefix {
		if u, ok := m.(core.UserMessage); ok {
			parts = append(parts, u.Content.Text())
		}
	}
	return strings.Join(parts, "")
}

// submitting is a run of a spy that submits the payload as a brain's tool does, in its box.
func submitting(payload string) func(context.Context, brain.Brief) (brain.Outcome, error) {
	return func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		target := submission.Target{Tree: b.Box.FS(ctx), Write: b.Box.WriteFile, RunID: b.RunID}
		if _, err := submission.Submit(ctx, target, b.Kind, []byte(payload), time.Now()); err != nil {
			return brain.Outcome{StopReason: brain.StopError}, err
		}
		return brain.Outcome{Submitted: true, StopReason: brain.StopSubmitted}, nil
	}
}

// repoFiles is a repository whose gate, make check, runs the tests under tests/ against a
// greeting that is wrong, and has a target that commits inside a box, which the tool gate of a
// brain does not let it do with git itself.
func repoFiles() map[string]string {
	return map[string]string{
		"Makefile":            "check:\n\tsh tests/run.sh\nbox-commit:\n\tgit add src tests\n\tgit -c user.name=box -c user.email=box@example.com commit -q -m 'a commit made in the box'\n",
		"tests/run.sh":        "set -e\nfor t in tests/*_test.sh; do sh \"$t\"; done\n",
		"tests/greet_test.sh": "[ \"$(sh src/greet.sh)\" = hello ] || { echo \"FAIL: greet printed $(sh src/greet.sh)\"; exit 1; }\n",
		"src/greet.sh":        "echo helo\n",
	}
}

const helloTest = "[ \"$(sh src/greet.sh)\" = hello ] || { echo \"FAIL: not hello\"; exit 1; }\n"

// fixReport is the report of the change that says hello and proves it.
const fixReport = `{"subject":"say hello","root_cause":"the script says helo","change":"say hello","files":[{"path":"src/greet.sh","why":"the word"},{"path":"tests/hello_test.sh","why":"the proof"}]}`

// script writes a script of the scripted brain with the steps, and returns its path.
func script(t *testing.T, steps ...map[string]any) string {
	t.Helper()
	return testutil.WriteFile(t, t.TempDir(), "script.json", string(testutil.JSON(t, map[string]any{"steps": steps})))
}

func writeStep(path, content string) map[string]any {
	return map[string]any{"tool": "write_file", "args": map[string]any{"path": path, "content": content}, "expect": "ok"}
}

func submitStep(payload, expect string) map[string]any {
	return map[string]any{"submit": json.RawMessage(payload), "expect": expect}
}

// fixSteps is a round that fixes the greeting, proves it with a test of its own, commits in its
// box and hands over the report.
func fixSteps() []map[string]any {
	return []map[string]any{
		writeStep("src/greet.sh", "echo hello\n"), writeStep("tests/hello_test.sh", helloTest),
		{"tool": "execute", "args": map[string]any{"command": "make box-commit"}, "expect": "ok"},
		submitStep(fixReport, "accepted"),
	}
}

// scripted are the flags of a run of the scripted brain on the steps.
func scripted(t *testing.T, steps ...map[string]any) []string {
	t.Helper()
	return []string{"--brain", "scripted", "--script", script(t, steps...)}
}

// diagnosis is a diagnosis of repoFiles, as a brain sends it, changed by mutate.
func diagnosis(mutate func(map[string]any)) string {
	m := map[string]any{
		"title": "The greeting is misspelt", "severity": "high", "confidence": "probable",
		"root_cause":          "src/greet.sh prints helo.",
		"affected_files":      []any{map[string]any{"path": "tests/run.sh", "line": 2, "note": "the loop"}, map[string]any{"path": "src/greet.sh", "note": "the word"}},
		"suggested_fix":       "Print hello.",
		"acceptance_criteria": []any{"sh src/greet.sh prints hello"},
	}
	if mutate != nil {
		mutate(m)
	}
	data, _ := json.Marshal(m)
	return string(data)
}

// ledger are the kinds of the ledger of a run directory, in order.
func ledger(t *testing.T, runDir string) []string {
	t.Helper()
	var kinds []string
	for _, rec := range testutil.JSONLines(t, filepath.Join(runDir, "ledger.jsonl")) {
		kind, _ := rec["kind"].(string)
		kinds = append(kinds, kind)
	}
	return kinds
}

// ledgerData is the data of the first ledger entry of the kind, or nil.
func ledgerData(t *testing.T, runDir, kind string) map[string]any {
	t.Helper()
	for _, rec := range testutil.JSONLines(t, filepath.Join(runDir, "ledger.jsonl")) {
		if rec["kind"] == kind {
			data, _ := rec["data"].(map[string]any)
			return data
		}
	}
	return nil
}

// placeRun makes a run directory as brain.Execute places one in a box: brief.md, the prefix
// then the task, and a run.json; it returns the directory.
func placeRun(t *testing.T, dir, id, kind string, attempt int, nonce, prefix, task string) string {
	t.Helper()
	runDir := filepath.Join(dir, ".agent-fox", "run", id)
	testutil.WriteFile(t, runDir, "brief.md", prefix+task)
	run := submission.Run{RunID: id, Kind: kind, Attempt: attempt, Nonce: nonce, PrefixBytes: len(prefix)}
	testutil.WriteFile(t, runDir, "run.json", string(testutil.JSON(t, run)))
	return runDir
}

// measure runs a command that measures a tree, on an App of the zero Config.
func measure(t *testing.T, args ...string) out {
	t.Helper()
	return invoke(t, New(&Config{}), "", args...)
}

// writeGate writes the gate.json of dir with the checks, each a name and its argv.
func writeGate(t *testing.T, dir string, checks ...[]string) {
	t.Helper()
	var list []map[string]any
	for _, c := range checks {
		list = append(list, map[string]any{"name": c[0], "argv": c[1:]})
	}
	testutil.WriteFile(t, dir, ".agent-fox/gate.json", string(testutil.JSON(t, map[string]any{"version": 1, "checks": list})))
}
