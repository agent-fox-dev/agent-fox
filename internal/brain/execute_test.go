package brain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// git runs git in dir; the test fails if it fails.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo is a temporary repository with one commit and a local identity.
func newRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is not installed: the box runs its commands with it")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "config", "commit.gpgsign", "false")
	for name, content := range map[string]string{"README.md": "# x\n", "src/main.go": "package main\n"} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "initial")
	return dir
}

// world is a repository, a run directory in it and a box on it.
type world struct {
	t       *testing.T
	dir     string
	run     *toolio.RunDir
	session *box.Session
	report  *recorder
}

// newWorld opens a real local box on a new repository, with a real run
// directory, and closes the box when the test ends.
func newWorld(t *testing.T) *world {
	t.Helper()
	dir := newRepo(t)
	rd, err := toolio.NewRunDir(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := box.Open(context.Background(), box.Request{
		Provider: "local", HostDir: dir, RunID: rd.ID, Tool: "fix", Record: rd.Record, Warn: func(string, string) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &world{t: t, dir: dir, run: rd, session: s, report: &recorder{}}
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

func (r *recorder) eventsOf(typ string) []event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []event
	for _, e := range r.events {
		if e.typ == typ {
			out = append(out, e)
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

// testKind is a kind whose payloads are any object.
func testKind() submission.Kind {
	return submission.Kind{Name: "test", ToolName: "submit_test", Description: "a test kind", Schema: json.RawMessage(`{"type":"object"}`)}
}

// brief is a brief for the attempt, with a hook that protects .agent-fox/.
func (w *world) brief(attempt int) Brief {
	return Brief{
		RunID: w.run.ID, Attempt: attempt, Kind: testKind(), Prefix: "PREFIX: the run-constant part\n", Task: "TASK: do the thing\n",
		Tools: []string{"read_file", "write_file"}, Hook: HookConfig{RunID: w.run.ID, ProtectedPaths: []string{".agent-fox/", ".specs/"}},
		MaxCostUSD: 1, MaxTurns: 10,
	}
}

// request is a request for a stub brain, with a nonce source that is fixed.
func (w *world) request(b Brief, brain Brain) Request {
	return Request{
		Session: w.session, Name: "scripted", Brain: brain, Brief: b, RunDir: w.run, Report: w.report, Tally: &Tally{},
		Heartbeat: time.Hour, Rand: bytes.NewReader([]byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x23, 0x45, 0x67}),
	}
}

// ledger is the kinds and data of the run's ledger.
func (w *world) ledger() ([]string, []json.RawMessage) {
	w.t.Helper()
	entries, err := w.run.Ledger()
	if err != nil {
		w.t.Fatal(err)
	}
	var kinds []string
	var data []json.RawMessage
	for _, e := range entries {
		kinds = append(kinds, e.Kind)
		data = append(data, e.Data)
	}
	return kinds, data
}

// entry is the data of the last ledger entry of the kind.
func (w *world) entry(kind string) map[string]any {
	w.t.Helper()
	kinds, data := w.ledger()
	for i := len(kinds) - 1; i >= 0; i-- {
		if kinds[i] == kind {
			var m map[string]any
			if err := json.Unmarshal(data[i], &m); err != nil {
				w.t.Fatal(err)
			}
			return m
		}
	}
	w.t.Fatalf("the ledger has no %s entry; it has %v", kind, kinds)
	return nil
}

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
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

// boxFile reads a file of the box's tree.
func (w *world) boxFile(rel string) (string, error) {
	data, err := fs.ReadFile(w.session.FS(context.Background()), rel)
	return string(data), err
}

// TestExecutePlacesTheRunFiles_TS_07_40 verifies TS-07-40:
// Execute places .gitignore, brief.md, run.json and hook.json in the box before
// Brain.Run.
// Verifies: 07-REQ-6.1
func TestExecutePlacesTheRunFiles_TS_07_40(t *testing.T) {
	w := newWorld(t)
	b := w.brief(1)
	files := map[string]string{}
	brain := stubBrain(func(ctx context.Context, got Brief) (Outcome, error) {
		for name, rel := range map[string]string{
			"gitignore": ".agent-fox/.gitignore", "brief": ".agent-fox/run/" + w.run.ID + "/brief.md",
			"run": ".agent-fox/run/" + w.run.ID + "/run.json", "hook": ".agent-fox/run/" + w.run.ID + "/hook.json",
		} {
			files[name], _ = w.boxFile(rel)
		}
		return Outcome{}, nil
	})

	_, _ = Execute(context.Background(), w.request(b, brain))

	if files["gitignore"] != "*" {
		t.Errorf(".agent-fox/.gitignore = %q, want *", files["gitignore"])
	}
	if files["brief"] != b.Document() {
		t.Errorf("brief.md = %q, want the document %q", files["brief"], b.Document())
	}
	var run submission.Run
	if err := json.Unmarshal([]byte(files["run"]), &run); err != nil {
		t.Fatalf("run.json = %q: %v", files["run"], err)
	}
	if run.RunID != w.run.ID || run.Kind != "test" || run.Attempt != 1 || !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(run.Nonce) || run.Nonce != "deadbeef01234567" {
		t.Errorf("run.json = %+v, want the run, the kind, attempt 1 and the nonce drawn from Rand", run)
	}
	var hook HookConfig
	if err := json.Unmarshal([]byte(files["hook"]), &hook); err != nil || !slices.Equal(hook.ProtectedPaths, b.Hook.ProtectedPaths) || hook.RunID != w.run.ID {
		t.Errorf("hook.json = %q (%v), want the HookConfig", files["hook"], err)
	}
}

// TestHookAlwaysProtectsAgentFox_TS_07_41 verifies TS-07-41:
// for any HookConfig the placed hook.json lists .agent-fox/ in protected_paths.
// Verifies: 07-REQ-6.2
func TestHookAlwaysProtectsAgentFox_TS_07_41(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 41))
	pool := []string{".agent-fox/", ".specs/", "docs/", ".git/", "LICENSE", ".agent-fox/", "src/gen/"}
	programs := []string{"go", "make", "ls", "cat"}
	w := newWorld(t)
	for i := range 25 {
		var hc HookConfig
		switch rng.IntN(4) {
		case 0: // nil
		case 1:
			hc.ProtectedPaths = []string{}
		default:
			for range 1 + rng.IntN(5) {
				hc.ProtectedPaths = append(hc.ProtectedPaths, pool[rng.IntN(len(pool))])
			}
		}
		for range rng.IntN(3) {
			hc.AllowPrograms = append(hc.AllowPrograms, programs[rng.IntN(len(programs))])
		}
		hc.RunID = w.run.ID
		b := w.brief(i + 1)
		b.Hook = hc

		_, _ = Execute(context.Background(), w.request(b, stubBrain(func(context.Context, Brief) (Outcome, error) { return Outcome{}, nil })))

		boxHook, err := w.boxFile(".agent-fox/run/" + w.run.ID + "/hook.json")
		if err != nil {
			t.Fatal(err)
		}
		hostHook, err := os.ReadFile(filepath.Join(w.run.Path, "hook.json"))
		if err != nil {
			t.Fatal(err)
		}
		for name, raw := range map[string]string{"box": boxHook, "host": string(hostHook)} {
			var got HookConfig
			if err := json.Unmarshal([]byte(raw), &got); err != nil {
				t.Fatalf("#%d %s hook.json = %q: %v", i, name, raw, err)
			}
			for _, p := range hc.ProtectedPaths {
				if !slices.Contains(got.ProtectedPaths, p) {
					t.Fatalf("#%d %s: %q lost from %v (was %v)", i, name, p, got.ProtectedPaths, hc.ProtectedPaths)
				}
			}
			if !slices.Contains(got.ProtectedPaths, ".agent-fox/") || !slices.Equal(got.AllowPrograms, append([]string{}, hc.AllowPrograms...)) {
				t.Fatalf("#%d %s: hook = %+v from %+v", i, name, got, hc)
			}
			if strings.Contains(raw, "null") {
				t.Fatalf("#%d %s: hook.json has a null: %s", i, name, raw)
			}
		}
	}
}

// TestExecuteWritesHostCopiesAndWiresTheBrief_TS_07_42 verifies TS-07-42:
// Execute writes host copies, records brief_placed with the prefix hash and
// wires TranscriptPath and Observe.
// Verifies: 07-REQ-6.3
func TestExecuteWritesHostCopiesAndWiresTheBrief_TS_07_42(t *testing.T) {
	w := newWorld(t)
	b := w.brief(2)
	var seen Brief
	brain := stubBrain(func(ctx context.Context, got Brief) (Outcome, error) {
		seen = got
		got.Observe(ToolCall{Brain: "scripted", Tool: "read_file", Decision: DecisionAllow, OK: true, DurationMS: 3})
		return Outcome{}, nil
	})

	_, _ = Execute(context.Background(), w.request(b, brain))

	if got, err := os.ReadFile(filepath.Join(w.run.Path, "brief.2.md")); err != nil || string(got) != b.Document() {
		t.Errorf("brief.2.md = %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(w.run.Path, "hook.json")); err != nil || !strings.Contains(string(got), `"protected_paths"`) {
		t.Errorf("hook.json = %q, %v", got, err)
	}
	placed := w.entry("brief_placed")
	if placed["attempt"] != float64(2) || placed["bytes"] != float64(len(b.Document())) || placed["sha256"] != sha(b.Document()) || placed["prefix_sha256"] != sha(b.Prefix) {
		t.Errorf("brief_placed = %v, want attempt 2, %d bytes, sha256 %s and prefix_sha256 %s", placed, len(b.Document()), sha(b.Document()), sha(b.Prefix))
	}
	if want := filepath.Join(w.run.Path, "transcript.2.jsonl"); seen.TranscriptPath != want {
		t.Errorf("TranscriptPath = %q, want %q", seen.TranscriptPath, want)
	}
	if seen.Box != w.session || seen.Document() != b.Document() {
		t.Errorf("the brain got another brief than the one it was given: %+v", seen)
	}
	calls := w.report.eventsOf("tool_call")
	if len(calls) != 1 || calls[0].data["tool"] != "read_file" {
		t.Errorf("tool_call events = %v, want one for read_file", calls)
	}
}

// TestExecuteEmitsStepsAndHeartbeats_TS_07_43 verifies TS-07-43:
// Execute emits step and heartbeat events and records brain_start and
// brain_end.
// Verifies: 07-REQ-6.4
func TestExecuteEmitsStepsAndHeartbeats_TS_07_43(t *testing.T) {
	w := newWorld(t)
	req := w.request(w.brief(1), stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
		time.Sleep(120 * time.Millisecond)
		return Outcome{Submitted: true, StopReason: StopEnded, CostUSD: 0.25, CostSource: CostMeasured, Turns: 4}, nil
	}))
	req.Heartbeat = 10 * time.Millisecond

	_, _ = Execute(context.Background(), req)

	var steps []string
	for _, e := range w.report.eventsOf("step") {
		steps = append(steps, fmt.Sprint(e.data["name"]))
	}
	if !slices.Equal(steps, []string{"brief", "brain", "submission"}) {
		t.Errorf("steps = %v, want brief, brain, submission", steps)
	}
	beats := w.report.eventsOf("heartbeat")
	if len(beats) < 2 {
		t.Fatalf("%d heartbeats, want at least 2", len(beats))
	}
	for _, e := range beats {
		if e.data["brain"] != "scripted" || e.data["elapsed_ms"] == nil {
			t.Errorf("heartbeat = %v, want brain and elapsed_ms", e.data)
		}
	}
	kinds, _ := w.ledger()
	start, end := slices.Index(kinds, "brain_start"), slices.Index(kinds, "brain_end")
	if start < 0 || end < 0 || start > end {
		t.Fatalf("ledger = %v, want brain_start before brain_end", kinds)
	}
	ended := w.entry("brain_end")
	for _, key := range []string{"brain", "stop_reason", "submitted_claim", "cost_usd", "cost_source", "turns", "duration_ms"} {
		if _, ok := ended[key]; !ok {
			t.Errorf("brain_end = %v lacks %s", ended, key)
		}
	}
	if ended["brain"] != "scripted" || ended["stop_reason"] != StopEnded || ended["submitted_claim"] != true || ended["cost_usd"] != 0.25 || ended["cost_source"] != CostMeasured || ended["turns"] != float64(4) {
		t.Errorf("brain_end = %v", ended)
	}
	// No heartbeat after Execute returned.
	n := len(w.report.eventsOf("heartbeat"))
	time.Sleep(50 * time.Millisecond)
	if after := len(w.report.eventsOf("heartbeat")); after != n {
		t.Errorf("%d heartbeats after Execute returned", after-n)
	}
}

// TestExecuteAppliesTheTimeout_TS_07_44 verifies TS-07-44:
// Brief.Timeout sets Brain.Run's deadline to start plus Timeout, and zero leaves
// only the caller's deadline.
// Verifies: 07-REQ-6.5
func TestExecuteAppliesTheTimeout_TS_07_44(t *testing.T) {
	w := newWorld(t)
	var deadline time.Time
	var has bool
	brain := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
		deadline, has = ctx.Deadline()
		return Outcome{}, nil
	})

	start := time.Now()
	b := w.brief(1)
	b.Timeout = 5 * time.Second
	_, _ = Execute(context.Background(), w.request(b, brain))
	if !has || deadline.Before(start.Add(5*time.Second)) || deadline.After(start.Add(5*time.Second+2*time.Second)) {
		t.Errorf("deadline %v (%v), want about %v", deadline, has, start.Add(5*time.Second))
	}

	_, _ = Execute(context.Background(), w.request(w.brief(2), brain))
	if has {
		t.Errorf("a run with no timeout has the deadline %v", deadline)
	}

	caller, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Hour))
	defer cancel()
	_, _ = Execute(caller, w.request(w.brief(3), brain))
	if want, _ := caller.Deadline(); !has || !deadline.Equal(want) {
		t.Errorf("deadline %v, want the caller's %v", deadline, want)
	}
}

// TestExecuteRecoversAPanic_TS_07_45 verifies TS-07-45:
// a panic inside Brain.Run is recovered as failed/brain and the stack is
// written.
// Verifies: 07-REQ-6.6
func TestExecuteRecoversAPanic_TS_07_45(t *testing.T) {
	w := newWorld(t)

	_, err := Execute(context.Background(), w.request(w.brief(3), stubBrain(func(context.Context, Brief) (Outcome, error) { panic("boom") })))

	f := asFailure(t, err)
	if f.Category != "brain" {
		t.Errorf("failure = %+v, want category brain", f)
	}
	stack, rerr := os.ReadFile(filepath.Join(w.run.Path, "brain-panic.3.txt"))
	if rerr != nil || !strings.Contains(string(stack), "boom") || !strings.Contains(string(stack), "goroutine") {
		t.Errorf("brain-panic.3.txt = %q, %v, want the panic and a stack", stack, rerr)
	}
	if !strings.Contains(f.Message, "brain-panic.3.txt") {
		t.Errorf("the message %q does not say where the stack is", f.Message)
	}
	if u := w.report.lastUsage(); u.Brain != "scripted" {
		t.Errorf("usage after a panic = %+v, want it set", u)
	}
}

// TestExecuteAddsUsageEvenWhenRunFails_TS_07_46 verifies TS-07-46:
// outcomes accumulate in the Tally across attempts and SetUsage gets the total,
// even when Run errors.
// Verifies: 07-REQ-6.7
func TestExecuteAddsUsageEvenWhenRunFails_TS_07_46(t *testing.T) {
	w := newWorld(t)
	tally := &Tally{}
	first := w.request(w.brief(1), stubBrain(func(context.Context, Brief) (Outcome, error) {
		return Outcome{CostUSD: 0.10, Turns: 3, InputTokens: 100, OutputTokens: 50, CostSource: CostMeasured}, nil
	}))
	first.Tally = tally
	second := w.request(w.brief(2), stubBrain(func(context.Context, Brief) (Outcome, error) {
		return Outcome{CostUSD: 0.20, Turns: 2, InputTokens: 10, OutputTokens: 5, CostSource: CostMeasured}, errors.New("the model went away")
	}))
	second.Tally = tally

	_, _ = Execute(context.Background(), first)
	if u := w.report.lastUsage(); u.Brain != "scripted" || u.CostUSD != 0.10 || u.Turns != 3 || u.InputTokens != 100 || u.OutputTokens != 50 || u.CostSource != CostMeasured {
		t.Errorf("usage after attempt 1 = %+v", u)
	}
	_, err := Execute(context.Background(), second)
	if err == nil {
		t.Fatal("the failed attempt did not fail")
	}

	u := w.report.lastUsage()
	if u.Brain != "scripted" || u.Turns != 5 || u.InputTokens != 110 || u.OutputTokens != 55 || u.CostUSD < 0.2999 || u.CostUSD > 0.3001 {
		t.Errorf("usage after attempt 2 = %+v, want the sum: 0.30, 5 turns, 110 and 55 tokens", u)
	}
}

// TestToolCallEventsCarryNoArguments_TS_07_64 verifies TS-07-64:
// for any tool call the tool_call event carries only brain, tool, decision,
// reason, ok and duration_ms.
// Verifies: 07-REQ-8.8
func TestToolCallEventsCarryNoArguments_TS_07_64(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 64))
	w := newWorld(t)
	var calls []ToolCall
	for range 60 {
		calls = append(calls, ToolCall{
			Brain: []string{"scripted", "agentkit", ""}[rng.IntN(3)], Tool: []string{"read_file", "write_file", "execute", "x y", ""}[rng.IntN(5)],
			Decision: []string{DecisionAllow, DecisionDeny}[rng.IntN(2)], Reason: randomString(rng), OK: rng.IntN(2) == 0, DurationMS: rng.Int64N(100000),
		})
	}
	brain := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
		for _, c := range calls {
			b.Observe(c)
		}
		return Outcome{}, nil
	})
	var caller []ToolCall
	b := w.brief(1)
	b.Observe = func(c ToolCall) { caller = append(caller, c) }

	_, _ = Execute(context.Background(), w.request(b, brain))

	events := w.report.eventsOf("tool_call")
	if len(events) != len(calls) || len(caller) != len(calls) {
		t.Fatalf("%d events and %d observed for %d calls", len(events), len(caller), len(calls))
	}
	allowed := []string{"brain", "tool", "decision", "reason", "ok", "duration_ms"}
	for i, e := range events {
		for key := range e.data {
			if !slices.Contains(allowed, key) {
				t.Errorf("event %d has the key %q", i, key)
			}
		}
		c := calls[i]
		if e.data["brain"] != c.Brain || e.data["tool"] != c.Tool || e.data["decision"] != c.Decision || e.data["reason"] != c.Reason || e.data["ok"] != c.OK || e.data["duration_ms"] != float64(c.DurationMS) {
			t.Errorf("event %d = %v, want %+v", i, e.data, c)
		}
	}
}
