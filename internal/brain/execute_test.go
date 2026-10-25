package brain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestExecute_RefusesARequestItCannotRun(t *testing.T) {
	w := newWorld(t)
	for _, tc := range []struct {
		name    string
		change  func(*Request)
		mention string
	}{
		{"no box", func(r *Request) { r.Session = nil }, "no box session"},
		{"no brain", func(r *Request) { r.Brain = nil }, "no brain"},
		{"no run directory", func(r *Request) { r.RunDir = nil }, "no run directory"},
		{"another run", func(r *Request) { r.Brief.RunID = "20260101_000000_000000" }, "the brief is for run"},
		{"attempt 0", func(r *Request) { r.Brief.Attempt = 0 }, "attempts count from 1"},
		{"a nonce that is not hex", func(r *Request) { r.Nonce = "not hex!" }, "is not 8 to 64"},
		{"an upper-case nonce", func(r *Request) { r.Nonce = "ABCDEF12" }, "is not 8 to 64"},
		{"a nonce that is too long", func(r *Request) { r.Nonce = strings.Repeat("a", 65) }, "is not 8 to 64"},
		{"no randomness", func(r *Request) { r.Rand = iotest.ErrReader(errors.New("drained")) }, "cannot draw a nonce"},
	} {
		req := w.request(w.brief(1), submits(`{"summary":"x"}`))
		tc.change(&req)
		if _, err := Execute(context.Background(), req); err == nil || !strings.Contains(err.Error(), tc.mention) {
			t.Errorf("%s: %v, want an error that says %q", tc.name, err, tc.mention)
		}
	}
	if _, err := w.boxFile(submission.RunPath(w.run.ID)); err == nil {
		t.Error("a request that was refused placed run.json")
	}
}

func TestExecute_PlacesTheBriefAndTheRunFilesBeforeTheBrainRuns(t *testing.T) {
	w := newWorld(t)
	dir := ".agent-fox/run/" + w.run.ID + "/"
	for i, tc := range []struct {
		hook               HookConfig
		protected, allowed []string
	}{
		{HookConfig{}, []string{".agent-fox/"}, []string{}},
		{HookConfig{ProtectedPaths: []string{".specs/"}, AllowPrograms: []string{"go"}}, []string{".specs/", ".agent-fox/"}, []string{"go"}},
		{HookConfig{ProtectedPaths: []string{".agent-fox/", "docs/"}}, []string{".agent-fox/", "docs/"}, []string{}},
	} {
		attempt := i + 1
		b := w.brief(attempt)
		b.RunID, b.Hook = "", tc.hook // the run of the run directory
		files := map[string]string{}
		var given Brief
		brain := stubBrain(func(_ context.Context, got Brief) (Outcome, error) {
			given = got
			for _, name := range []string{".agent-fox/.gitignore", dir + "brief.md", dir + "run.json", dir + "hook.json"} {
				files[name], _ = w.boxFile(name)
			}
			return Outcome{}, nil
		})

		_, _ = Execute(context.Background(), w.request(b, brain))

		if files[".agent-fox/.gitignore"] != "*" || files[dir+"brief.md"] != b.Document() || b.Document() != b.Prefix+b.Task {
			t.Errorf("attempt %d: .gitignore %q and brief.md %q, want * and the prefix then the task", attempt, files[".agent-fox/.gitignore"], files[dir+"brief.md"])
		}
		run := testutil.Decode[map[string]any](t, []byte(files[dir+"run.json"]))
		if run["run_id"] != w.run.ID || run["kind"] != "test" || run["attempt"] != float64(attempt) || run["nonce"] != "deadbeef01234567" || run["prefix_bytes"] != float64(len(b.Prefix)) {
			t.Errorf("attempt %d: run.json = %v, want the run, the kind, the attempt, the nonce drawn and the length of the prefix", attempt, run)
		}
		host := testutil.ReadFile(t, filepath.Join(w.run.Path, "hook.json"))
		hook := testutil.Decode[HookConfig](t, []byte(host))
		if host != files[dir+"hook.json"] || !slices.Equal(hook.ProtectedPaths, tc.protected) || !slices.Equal(hook.AllowPrograms, tc.allowed) || strings.Contains(host, "null") {
			t.Errorf("attempt %d: hook.json = %s (the box's %s), want %v protected and %v allowed", attempt, host, files[dir+"hook.json"], tc.protected, tc.allowed)
		}
		if !slices.Equal(given.Hook.ProtectedPaths, tc.protected) || given.Box != w.session || given.RunID != w.run.ID || given.RunDir != w.run.Path ||
			given.TranscriptPath != filepath.Join(w.run.Path, "transcript."+strconv.Itoa(attempt)+".jsonl") {
			t.Errorf("attempt %d: the brain was given %+v, want the hook that was placed, the box, the run and its transcript", attempt, given)
		}
		if got := testutil.ReadFile(t, filepath.Join(w.run.Path, "brief."+strconv.Itoa(attempt)+".md")); got != b.Document() {
			t.Errorf("attempt %d: the host's copy of the brief = %q", attempt, got)
		}
		placed := w.entry("brief_placed")
		if placed["attempt"] != float64(attempt) || placed["bytes"] != float64(len(b.Document())) || placed["sha256"] != testutil.SHA256(b.Document()) || placed["prefix_sha256"] != testutil.SHA256(b.Prefix) {
			t.Errorf("attempt %d: brief_placed = %v", attempt, placed)
		}
	}
}

func TestExecute_ReportsEachToolCallAsAnEventWithoutItsArguments(t *testing.T) {
	w := newWorld(t)
	calls := []ToolCall{
		{Brain: ScriptedName, Tool: "read_file", Decision: DecisionAllow, OK: true, DurationMS: 3},
		{Brain: ScriptedName, Tool: "write_file", Decision: DecisionDeny, Reason: "no", DurationMS: 0},
	}
	var heard []ToolCall
	b := w.brief(1)
	b.Observe = func(c ToolCall) { heard = append(heard, c) }

	_, _ = Execute(context.Background(), w.request(b, stubBrain(func(_ context.Context, b Brief) (Outcome, error) {
		for _, c := range calls {
			b.Observe(c)
		}
		return Outcome{}, nil
	})))

	events := w.report.eventsOf("tool_call")
	if len(events) != 2 || !slices.Equal(heard, calls) {
		t.Fatalf("events %v and the caller heard %v, want both calls", events, heard)
	}
	for i, e := range events {
		c := calls[i]
		want := map[string]any{"brain": c.Brain, "tool": c.Tool, "decision": c.Decision, "reason": c.Reason, "ok": c.OK, "duration_ms": float64(c.DurationMS)}
		if len(e) != len(want) || e["tool"] != want["tool"] || e["decision"] != want["decision"] || e["reason"] != want["reason"] || e["ok"] != want["ok"] || e["duration_ms"] != want["duration_ms"] || e["brain"] != want["brain"] {
			t.Errorf("event %d = %v, want %v and nothing else", i, e, want)
		}
	}
}

func TestExecute_SaysWhereTheRunIsAndRecordsTheBrainsStartAndEnd(t *testing.T) {
	w := newWorld(t)
	req := w.request(w.brief(1), stubBrain(func(context.Context, Brief) (Outcome, error) {
		testutil.Eventually(t, "two heartbeats", func() bool { return len(w.report.eventsOf("heartbeat")) >= 2 })
		return Outcome{Submitted: true, StopReason: StopEnded, CostUSD: 0.25, CostSource: CostMeasured, Turns: 4, InputTokens: 1200, OutputTokens: 340}, nil
	}))
	req.Heartbeat = 5 * time.Millisecond

	_, _ = Execute(context.Background(), req)

	var order []string
	for _, e := range w.report.events {
		if e.typ == "step" {
			order = append(order, e.data["name"].(string))
		} else if e.typ == "heartbeat" && (e.data["brain"] != ScriptedName || e.data["elapsed_ms"] == nil) {
			t.Errorf("heartbeat = %v, want the brain and the time elapsed", e.data)
		}
		if e.typ == "heartbeat" && slices.Contains(order, "submission") {
			t.Error("a heartbeat came after the brain had returned")
		}
	}
	if !slices.Equal(order, []string{"brief", "brain", "submission"}) {
		t.Errorf("steps = %v", order)
	}
	kinds := w.kinds()
	if i, j := slices.Index(kinds, "brain_start"), slices.Index(kinds, "brain_end"); i < 0 || j < i {
		t.Errorf("ledger = %v, want brain_start before brain_end", kinds)
	}
	end := w.entry("brain_end")
	if end["brain"] != ScriptedName || end["stop_reason"] != StopEnded || end["submitted_claim"] != true || end["cost_usd"] != 0.25 || end["cost_source"] != CostMeasured ||
		end["turns"] != 4.0 || end["input_tokens"] != 1200.0 || end["output_tokens"] != 340.0 || end["duration_ms"] == nil {
		t.Errorf("brain_end = %v, want the outcome of the brain", end)
	}
}

func TestExecute_BoundsTheBrainByTheTimeoutOfTheBrief(t *testing.T) {
	w := newWorld(t)
	var deadline time.Time
	var has bool
	brain := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
		deadline, has = ctx.Deadline()
		return Outcome{}, nil
	})

	start := time.Now()
	b := w.brief(1)
	b.Timeout = time.Minute
	_, _ = Execute(context.Background(), w.request(b, brain))
	if !has || deadline.Before(start.Add(time.Minute)) || deadline.After(time.Now().Add(time.Minute)) {
		t.Errorf("deadline %v (%v), want a minute from the start of Execute", deadline, has)
	}

	_, _ = Execute(context.Background(), w.request(w.brief(2), brain))
	if has {
		t.Errorf("a brief with no timeout: deadline %v", deadline)
	}

	caller, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	_, _ = Execute(caller, w.request(w.brief(3), brain))
	if want, _ := caller.Deadline(); !deadline.Equal(want) {
		t.Errorf("deadline %v, want the caller's %v", deadline, want)
	}
}

func TestExecute_RecoversAPanicOfTheBrain(t *testing.T) {
	w := newWorld(t)

	_, err := w.execute(context.Background(), w.brief(3), stubBrain(func(context.Context, Brief) (Outcome, error) { panic("boom") }))

	f := asFailure(t, err)
	if f.Category != toolio.CategoryBrain || f.Retryable || !strings.Contains(f.Message, "boom") || !strings.Contains(f.Message, "brain-panic.3.txt") {
		t.Errorf("failure = %+v, want brain, not retryable, with the panic and where its stack is", f)
	}
	if stack := testutil.ReadFile(t, filepath.Join(w.run.Path, "brain-panic.3.txt")); !strings.Contains(stack, "boom") || !strings.Contains(stack, "goroutine") {
		t.Errorf("brain-panic.3.txt = %q, want the panic and its stack", stack)
	}
	if w.report.lastUsage().Brain != ScriptedName || w.entry("brain_end")["stop_reason"] != StopError {
		t.Error("the run of a brain that panicked has no usage, or no brain_end that says it stopped on an error")
	}

	// The stack cannot be written where a directory has its name: the failure says so.
	testutil.WriteFile(t, w.run.Path, "brain-panic.4.txt/x", "")
	_, err = w.execute(context.Background(), w.brief(4), stubBrain(func(context.Context, Brief) (Outcome, error) { panic("boom") }))
	if f := asFailure(t, err); !strings.Contains(f.Message, "could not be written") {
		t.Errorf("failure = %+v, want it to say the stack could not be written", f)
	}
}

func TestExecute_TalliesWhatEveryAttemptCost(t *testing.T) {
	w := newWorld(t)
	tally := &Tally{}
	for i, step := range []struct {
		outcome Outcome
		err     error
	}{
		{Outcome{CostUSD: 0.25, Turns: 3, InputTokens: 100, OutputTokens: 50, CostSource: CostMeasured}, nil},
		{Outcome{CostUSD: 0.5, Turns: 2, InputTokens: 10, OutputTokens: 5, CostSource: CostMeasured}, errors.New("the model went away")},
	} {
		req := w.request(w.brief(i+1), stubBrain(func(context.Context, Brief) (Outcome, error) { return step.outcome, step.err }))
		req.Tally = tally
		if _, err := Execute(context.Background(), req); err == nil {
			t.Fatalf("attempt %d submitted", i+1)
		}
	}

	want := toolio.Usage{Brain: ScriptedName, CostUSD: 0.75, CostSource: CostMeasured, Turns: 5, InputTokens: 110, OutputTokens: 55}
	if u := w.report.lastUsage(); u != want {
		t.Errorf("usage = %+v, want the sum of both attempts %+v", u, want)
	}
}

func TestExecute_GoesOnWhenTheReportOrTheLedgerFails(t *testing.T) {
	w := newWorld(t)
	beat := make(chan struct{})
	req := w.request(w.brief(1), stubBrain(func(context.Context, Brief) (Outcome, error) {
		<-beat
		return Outcome{}, nil
	}))
	req.Report, req.Heartbeat = panicsOnHeartbeat{w.report, beat}, time.Millisecond
	ledger := filepath.Join(w.run.Path, "ledger.jsonl")
	if err := os.Remove(ledger); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(ledger, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Execute(context.Background(), req)

	if f := asFailure(t, err); f.Category != toolio.CategoryNoResult {
		t.Errorf("failure = %+v, want no_result for a brain that submitted nothing", f)
	}
	if got := w.report.warned("ledger_unwritable"); len(got) != 1 || !strings.Contains(got[0], "brief_placed") {
		t.Errorf("warnings = %+v, want one ledger_unwritable, for the first entry", w.report.warnings)
	}
}

// panicsOnHeartbeat is a Report whose heartbeat panics, once it has said so.
type panicsOnHeartbeat struct {
	*recorder
	beat chan struct{}
}

func (r panicsOnHeartbeat) Emit(typ string, data any) {
	if typ == "heartbeat" {
		close(r.beat)
		panic("the report is broken")
	}
	r.recorder.Emit(typ, data)
}

func TestExecute_RunsWithNoReportAndNoTally(t *testing.T) {
	w := newWorld(t)
	brain := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
		return Outcome{}, submitIn(ctx, b, `{"summary":"done"}`) // and claims nothing
	})

	res, err := Execute(context.Background(), Request{Session: w.session, Brain: brain, Brief: w.brief(1), RunDir: w.run})

	if err != nil || string(res.Payload) != `{"summary":"done"}` {
		t.Errorf("Execute = %+v, %v, want the submission", res, err)
	}
	run := testutil.Decode[submission.Run](t, []byte(must(w.boxFile(submission.RunPath(w.run.ID)))))
	if !ValidNonce(run.Nonce) || len(run.Nonce) != 16 {
		t.Errorf("run.json's nonce = %q, want 16 hex digits drawn", run.Nonce)
	}
}

func TestExecute_ACancelledRunIsTheErrorOfItsContext(t *testing.T) {
	w := newWorld(t)
	stale := testutil.WriteFile(t, w.run.Path, "submission.json", `{"stale":true}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := w.execute(ctx, w.brief(1), stubBrain(func(context.Context, Brief) (Outcome, error) {
		t.Error("the brain ran in a cancelled run")
		return Outcome{}, nil
	}))

	var f *toolio.Failure
	if !errors.Is(err, context.Canceled) || errors.As(err, &f) {
		t.Errorf("error = %v, want context.Canceled and no failure of the box that could not be written", err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("the submission of an earlier attempt is still in the run directory")
	}
}

func TestExecute_AdoptsTheNonceItIsGiven(t *testing.T) {
	w := newWorld(t)
	for i, nonce := range []string{"0123456789abcdef", "01234567"} {
		req := w.request(w.brief(i+1), submits(`{"summary":"x"}`))
		req.Nonce = nonce

		res, err := Execute(context.Background(), req)

		if err != nil || !res.Outcome.Submitted {
			t.Fatalf("nonce %s: %+v, %v", nonce, res, err)
		}
		host, _ := w.hostSubmission()
		run := testutil.Decode[submission.Run](t, []byte(must(w.boxFile(submission.RunPath(w.run.ID)))))
		if file := testutil.Decode[submission.File](t, []byte(host)); file.Nonce != nonce || run.Nonce != nonce {
			t.Errorf("nonce %s: run.json has %s and the submission %s, want the adopted one", nonce, run.Nonce, file.Nonce)
		}
	}
}

// must is the value of a call that cannot fail in the test.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
