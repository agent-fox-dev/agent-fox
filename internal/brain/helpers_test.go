package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/provider/faux"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/local"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/fakeaf"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TestMain acts as the fake af when the test binary is run under that name (see
// internal/fakeaf), and runs the tests otherwise.
func TestMain(m *testing.M) {
	if fakeaf.Name() != "" {
		os.Exit(fakeaf.Run(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// world is a repository, a run directory in it and a box on it, with a Report that
// remembers.
type world struct {
	t       *testing.T
	dir     string
	run     *toolio.RunDir
	session *box.Session
	report  *recorder
}

// newWorld opens a local box on a new repository.
func newWorld(t *testing.T) *world { return openWorld(t, box.Request{Provider: "local"}) }

// openWorld opens the box of req on a new repository with a new run directory, and
// closes it when the test ends.
func openWorld(t *testing.T, req box.Request) *world {
	t.Helper()
	testutil.Require(t, "sh")
	dir := testutil.Repo(t, map[string]string{"README.md": "# x\n", "src/main.go": "package main\n"})
	rd, err := toolio.NewRunDir(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.HostDir, req.RunID, req.Tool, req.Record, req.Warn = dir, rd.ID, "fix", rd.Record, func(string, string) {}
	s, err := box.Open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &world{t: t, dir: dir, run: rd, session: s, report: &recorder{}}
}

// suppliesCredentials is a registry with the local provider under the name "supplies",
// as a box that has the credentials of its brain in an environment of its own.
func suppliesCredentials() *box.Registry {
	reg := box.NewRegistry()
	reg.Register(box.Entry{Name: "supplies", Unconfined: true, SuppliesCredentials: true, New: func() sandbox.Provider { return local.Unisolated() }})
	return reg
}

// testKind is a kind whose payloads are any object.
func testKind() submission.Kind {
	return submission.Kind{Name: "test", ToolName: "submit_test", Description: "a test kind", Schema: json.RawMessage(`{"type":"object"}`)}
}

// citeKind refuses a payload whose cite is a path that is not in the tree.
func citeKind() submission.Kind {
	k := testKind()
	k.Validate = func(_ context.Context, tree fs.FS, payload json.RawMessage) (json.RawMessage, []submission.Problem) {
		var p struct {
			Cite string `json:"cite"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, []submission.Problem{{Message: err.Error()}}
		}
		if _, err := fs.Stat(tree, p.Cite); p.Cite != "" && err != nil {
			return nil, []submission.Problem{{Path: "cite", Message: p.Cite + " is not in the tree"}, {Path: "summary", Message: "must say what was done"}}
		}
		return payload, nil
	}
	return k
}

// brief is a brief for the attempt, with bounds and a hook.
func (w *world) brief(attempt int) Brief {
	return Brief{
		RunID: w.run.ID, Attempt: attempt, Kind: testKind(), Prefix: "PREFIX: the run-constant part\n", Task: "TASK: do the thing\n",
		Tools: []string{"read_file", "write_file"}, Hook: HookConfig{RunID: w.run.ID, ProtectedPaths: []string{".specs/"}},
		MaxCostUSD: 1, MaxTurns: 10,
	}
}

// request is the request of a brief, with a fixed nonce source and no heartbeat.
func (w *world) request(b Brief, brain Brain) Request {
	return Request{
		Session: w.session, Name: ScriptedName, Brain: brain, Brief: b, RunDir: w.run, Report: w.report, Tally: &Tally{},
		Heartbeat: time.Hour, Rand: bytes.NewReader([]byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x23, 0x45, 0x67}),
	}
}

// execute runs the brief through the brain, with a fresh report.
func (w *world) execute(ctx context.Context, b Brief, brain Brain) (Result, error) {
	w.report = &recorder{}
	return Execute(ctx, w.request(b, brain))
}

// ledger is the entries of the run's ledger, decoded.
func (w *world) ledger() []map[string]any {
	return testutil.JSONLines(w.t, filepath.Join(w.run.Path, "ledger.jsonl"))
}

// kinds are the kinds of the ledger's entries, in order.
func (w *world) kinds() []string {
	var kinds []string
	for _, e := range w.ledger() {
		kinds = append(kinds, e["kind"].(string))
	}
	return kinds
}

// entry is the data of the last ledger entry of the kind.
func (w *world) entry(kind string) map[string]any {
	w.t.Helper()
	entries := w.ledger()
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i]["kind"] == kind {
			return entries[i]["data"].(map[string]any)
		}
	}
	w.t.Fatalf("the ledger has no %s entry; it has %v", kind, w.kinds())
	return nil
}

// boxFile reads a file of the box's tree.
func (w *world) boxFile(rel string) (string, error) {
	data, err := fs.ReadFile(w.session.FS(context.Background()), rel)
	return string(data), err
}

// hostSubmission is the submission.json of the run directory, and whether there is one.
func (w *world) hostSubmission() (string, bool) {
	data, err := os.ReadFile(filepath.Join(w.run.Path, "submission.json"))
	return string(data), err == nil
}

// transcript is the lines of the transcript of the attempt.
func (w *world) transcript(attempt int) []map[string]any {
	return testutil.JSONLines(w.t, filepath.Join(w.run.Path, "transcript."+strconv.Itoa(attempt)+".jsonl"))
}

// recorder is a Report that remembers.
type recorder struct {
	mu        sync.Mutex
	events    []event
	warnings  []toolio.Warning
	artifacts []toolio.Artifact
	usages    []toolio.Usage
}

type event struct {
	typ  string
	data map[string]any
}

func (r *recorder) Emit(typ string, data any) {
	raw, _ := json.Marshal(data)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event{typ, m})
}

func (r *recorder) Warn(code, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnings = append(r.warnings, toolio.Warning{Code: code, Message: message})
}

func (r *recorder) Artifact(a toolio.Artifact) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.artifacts = append(r.artifacts, a)
}

func (r *recorder) SetUsage(u toolio.Usage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usages = append(r.usages, u)
}

func (r *recorder) eventsOf(typ string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for _, e := range r.events {
		if e.typ == typ {
			out = append(out, e.data)
		}
	}
	return out
}

func (r *recorder) warned(code string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, w := range r.warnings {
		if w.Code == code {
			out = append(out, w.Message)
		}
	}
	return out
}

func (r *recorder) lastUsage() toolio.Usage {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.usages) == 0 {
		return toolio.Usage{}
	}
	return r.usages[len(r.usages)-1]
}

// stubBrain is a brain made of a function.
type stubBrain func(ctx context.Context, b Brief) (Outcome, error)

func (f stubBrain) Run(ctx context.Context, b Brief) (Outcome, error) { return f(ctx, b) }

// idle is a brain that does nothing and ends.
var idle = stubBrain(func(context.Context, Brief) (Outcome, error) { return Outcome{StopReason: StopEnded}, nil })

// submitIn submits the payload from inside the run, as a brain does: through the
// submit core against the box.
func submitIn(ctx context.Context, b Brief, payload string) error {
	_, err := submission.Submit(ctx, submission.Target{Tree: b.Box.FS(ctx), Write: b.Box.WriteFile, RunID: b.RunID}, b.Kind, []byte(payload), time.Now())
	return err
}

// submits is a brain that submits the payload and says so.
func submits(payload string) stubBrain {
	return func(ctx context.Context, b Brief) (Outcome, error) {
		if err := submitIn(ctx, b, payload); err != nil {
			return Outcome{StopReason: StopError}, err
		}
		return Outcome{Submitted: true, StopReason: StopSubmitted}, nil
	}
}

// asFailure is err as a *toolio.Failure.
func asFailure(t *testing.T, err error) *toolio.Failure {
	t.Helper()
	var f *toolio.Failure
	if !errors.As(err, &f) {
		t.Fatalf("error = %v (%T), want a *toolio.Failure", err, err)
	}
	return f
}

// writeScript writes a script file and returns its path.
func writeScript(t *testing.T, content string) string {
	return testutil.WriteFile(t, t.TempDir(), "script.json", content)
}

// runScript runs a script through Execute as attempt 1, with the cite kind.
func runScript(t *testing.T, w *world, script string, mutate func(*Brief)) (Result, error) {
	t.Helper()
	brain, err := Default.New(ScriptedName, Options{Script: writeScript(t, script)})
	if err != nil {
		t.Fatal(err)
	}
	b := w.brief(1)
	b.Kind = citeKind()
	b.Tools = []string{"read_file", "write_file", "list_files", "execute"}
	if mutate != nil {
		mutate(&b)
	}
	return Execute(context.Background(), w.request(b, brain))
}

// noEnv is a host environment with nothing in it.
func noEnv(string) (string, bool) { return "", false }

// envOf is a host environment of the values.
func envOf(m map[string]string) Getenv {
	return func(name string) (string, bool) { v, ok := m[name]; return v, ok }
}

// fauxBrain is the agentkit brain over a faux provider that plays the turns.
func fauxBrain(t *testing.T, turns ...faux.Turn) (Brain, *faux.Provider) {
	t.Helper()
	p := faux.New(turns...)
	b, err := Default.New(AgentkitName, Options{Model: "faux-1", Getenv: noEnv, NewProvider: func(string) (core.ProviderClient, error) { return p, nil }})
	if err != nil {
		t.Fatal(err)
	}
	return b, p
}

// costs gives a turn a usage.
func costs(turn faux.Turn, cost float64, in, out int64) faux.Turn {
	turn.Usage.SetField(core.UsageInputTokens, in)
	turn.Usage.SetField(core.UsageOutputTokens, out)
	turn.Usage.SetCost(cost)
	return turn
}

// runFaux runs an agentkit brain through Execute as attempt 1, with the cite kind.
func runFaux(w *world, brain Brain, mutate func(*Brief)) (Result, error) {
	b := w.brief(1)
	b.Kind = citeKind()
	b.Tools = []string{"read_file", "list_files"}
	if mutate != nil {
		mutate(&b)
	}
	req := w.request(b, brain)
	req.Name = AgentkitName
	return Execute(context.Background(), req)
}

// toolResults are the texts of the tool results of a request, joined.
func toolResults(req core.Request) string {
	var out []string
	for _, m := range req.Messages {
		if tr, ok := m.(core.ToolResultMessage); ok {
			out = append(out, tr.Content.Text())
		}
	}
	return strings.Join(out, "\n")
}

// procWorld is a world with the fake af, and a runner that finds it.
type procWorld struct {
	*world
	af string
	p  *Process
}

// newProcWorld opens the box of req, with the fake af and a runner for the host
// environment get (nil is the process's).
func newProcWorld(t *testing.T, req box.Request, get Getenv) *procWorld {
	t.Helper()
	_, link := fakeaf.Install(t)
	// A test binary built with -cover warns on its standard error when it has no
	// GOCOVERDIR, and the box returns that with what af prints: the script gives it one.
	af := testutil.WriteFile(t, t.TempDir(), "af", "#!/bin/sh\nGOCOVERDIR="+t.TempDir()+" exec "+link+` "$@"`+"\n")
	if err := os.Chmod(af, 0o755); err != nil {
		t.Fatal(err)
	}
	w := &procWorld{world: openWorld(t, req), af: af}
	w.p = NewProcess(ProcessConfig{AFPath: af, Getenv: get, Version: "v1.0.0", Commit: "abc123"})
	return w
}

// script tells the fake af what to do.
func (w *procWorld) script(s fakeaf.Script) {
	w.t.Helper()
	if err := w.session.WriteFile(context.Background(), fakeaf.ScriptPath, testutil.JSON(w.t, s)); err != nil {
		w.t.Fatal(err)
	}
}

// processBrief is the brief of an attempt as Execute leaves it for a brain.
func (w *procWorld) processBrief(attempt int) Brief {
	b := w.brief(attempt)
	// Execute places the brief, in the directory of the run that a process writes to.
	if err := w.session.WriteFile(context.Background(), ".agent-fox/run/"+b.RunID+"/brief.md", []byte(b.Document())); err != nil {
		w.t.Fatal(err)
	}
	b.Box, b.RunDir, b.Artifact, b.Warn = w.session, w.run.Path, w.report.Artifact, w.report.Warn
	b.Record = func(kind string, data any) { _ = w.run.Record(kind, data) }
	return b
}

// runs are what the fake af noted of the processes that were not a probe of its version.
func (w *procWorld) runs() []fakeaf.Record {
	return w.records(false)
}

// records are what the fake af noted, of its probes or of the rest.
func (w *procWorld) records(probes bool) []fakeaf.Record {
	data, _ := w.boxFile(fakeaf.RecordPath)
	var out []fakeaf.Record
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		if line == "" {
			continue
		}
		r := testutil.Decode[fakeaf.Record](w.t, []byte(line))
		if (len(r.Argv) == 1 && r.Argv[0] == "--version") == probes {
			out = append(out, r)
		}
	}
	return out
}

// envOfRecord is the environment a process of the fake af saw, by name.
func envOfRecord(r fakeaf.Record) map[string]string {
	seen := map[string]string{}
	for _, kv := range r.Env {
		name, value, _ := strings.Cut(kv, "=")
		seen[name] = value
	}
	return seen
}
