package brain

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/provider/faux"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/local"
)

// smoke is what an agent command does with these pieces, in the order it does
// it: parse the shared flags, make the brain from the registry, open the box for
// the run with the brain's environment, and run the brief in it. The tests of
// this file are that command, with the parts of it that are not in this scope (the
// brief, which is the agent's) written out.
type smoke struct {
	t        *testing.T
	dir      string
	run      *toolio.RunDir
	report   *recorder
	settings *Settings
	brain    Brain
	entry    Entry
	registry *box.Registry
	tally    *Tally
	opened   func(*box.Session)
}

func newSmoke(t *testing.T, args []string, opts Options) *smoke {
	t.Helper()
	dir := newRepo(t)
	rd, err := toolio.NewRunDir(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	fs := flag.NewFlagSet("smoke", flag.ContinueOnError)
	settings := Flags(fs, Defaults{Sandbox: "local", Budget: 2, MaxTurns: 8})
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	if err := settings.Validate(); err != nil {
		t.Fatal(err)
	}
	opts.Script = settings.Script
	if opts.Getenv == nil {
		opts.Getenv = os.LookupEnv
	}
	brain, err := Default.New(settings.Brain, opts)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := Default.Lookup(settings.Brain)
	if err != nil {
		t.Fatal(err)
	}
	return &smoke{t: t, dir: dir, run: rd, report: &recorder{}, settings: settings, brain: brain, entry: entry, tally: &Tally{}}
}

// attempt runs one attempt of the brief in the box of the run, opening it with
// box.With, as the loop does for a run.
func (s *smoke) attempt(n int, mutate func(*Brief)) (Result, error) {
	return s.inBox(func(sess *box.Session) (Result, error) { return s.execute(sess, n, mutate) })
}

func (s *smoke) inBox(fn func(*box.Session) (Result, error)) (Result, error) {
	s.t.Helper()
	env := BuildBoxEnv(s.entry, PlacementHost, s.envOf)
	var res Result
	var err error
	werr := box.With(context.Background(), box.Request{
		Provider: s.settings.Sandbox, HostDir: s.dir, RunID: s.run.ID, Tool: "fix", Env: env,
		Record: s.run.Record, Warn: s.report.Warn, Registry: s.registry,
	}, func(sess *box.Session) error {
		if s.opened != nil {
			s.opened(sess)
		}
		res, err = fn(sess)
		return err
	})
	if werr != nil && err == nil {
		s.t.Fatalf("box.With: %v", werr)
	}
	return res, err
}

func (s *smoke) envOf(name string) (string, bool) { return os.LookupEnv(name) }

// execute runs an attempt in a box that is open.
func (s *smoke) execute(sess *box.Session, n int, mutate func(*Brief)) (Result, error) {
	b := Brief{
		RunID: s.run.ID, Attempt: n, Kind: contractKind(), Prefix: "PREFIX\n", Task: "TASK\n", Tools: []string{"read_file", "list_files"},
		Hook: HookConfig{RunID: s.run.ID, ProtectedPaths: []string{".agent-fox/", ".specs/"}}, MaxCostUSD: s.settings.Budget, MaxTurns: s.settings.MaxTurns,
		Env: BuildBoxEnv(s.entry, PlacementHost, s.envOf),
	}
	if mutate != nil {
		mutate(&b)
	}
	return Execute(context.Background(), Request{Session: sess, Name: s.settings.Brain, Brain: s.brain, Brief: b, RunDir: s.run, Report: s.report, Tally: s.tally})
}

// kinds are the kinds of the ledger of the run.
func (s *smoke) kinds() []string {
	entries, err := s.run.Ledger()
	if err != nil {
		s.t.Fatal(err)
	}
	var kinds []string
	for _, e := range entries {
		kinds = append(kinds, e.Kind)
	}
	return kinds
}

// inOrder says whether the wanted kinds are in the ledger in that order.
func inOrder(have []string, want ...string) bool {
	i := 0
	for _, k := range have {
		if i < len(want) && k == want[i] {
			i++
		}
	}
	return i == len(want)
}

// TestSmokeAScriptedBrainInALocalBox_TS_07_80 verifies TS-07-80 (07-PATH-1):
// a scripted brain runs a brief in a local box and its submission reaches the
// run directory.
// Verifies: 07-PATH-1, 07-REQ-1.7, 07-REQ-6.1, 07-REQ-7.2
func TestSmokeAScriptedBrainInALocalBox_TS_07_80(t *testing.T) {
	script := writeScript(t, `{"steps":[
		{"tool":"list_files","args":{"path":"."},"expect":"ok"},
		{"submit":{"summary":"done","cite":"README.md"},"expect":"accepted"}]}`)
	s := newSmoke(t, []string{"--brain", "scripted", "--script", script}, Options{})

	res, err := s.attempt(1, nil)

	if err != nil || !res.Outcome.Submitted {
		t.Fatalf("Execute = %+v, %v", res, err)
	}
	want := []string{"box_open", "box_sync", "brief_placed", "brain_start", "brain_end", "submission_accepted", "box_close"}
	if kinds := s.kinds(); !inOrder(kinds, want...) || kinds[0] != "box_open" || kinds[len(kinds)-1] != "box_close" {
		t.Errorf("ledger = %v, want %v in order, the box opened first and closed last", kinds, want)
	}
	if warned := s.report.warned("box_unconfined"); len(warned) != 1 {
		t.Errorf("box_unconfined warnings = %+v, want exactly one", s.report.warnings)
	}
	for _, name := range []string{"submission.json", "brief.1.md", "hook.json"} {
		if _, err := os.Stat(filepath.Join(s.run.Path, name)); err != nil {
			t.Errorf("the run directory has no %s: %v", name, err)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(s.run.Path, "submission.json")); !strings.Contains(string(got), `"summary":"done"`) {
		t.Errorf("submission.json = %s", got)
	}
	if !slices.Contains(s.report.artifacts, toolio.Artifact{Kind: "submission", Path: filepath.Join(s.run.Path, "submission.json")}) {
		t.Errorf("artifacts = %+v, want the submission", s.report.artifacts)
	}
	// What the box held stays in the box: the run's files in the tree are not
	// in the host's run directory, and the checkout is as it was.
	for _, name := range []string{"run.json", "brief.md"} {
		if _, err := os.Stat(filepath.Join(s.run.Path, name)); err == nil {
			t.Errorf("%s of the box is in the host's run directory", name)
		}
	}
	if status := git(t, s.dir, "status", "--porcelain"); status != "" {
		t.Errorf("the checkout is not clean: %s", status)
	}
	if u := s.report.lastUsage(); u.Brain != "scripted" || u.CostSource != CostNone {
		t.Errorf("usage = %+v", u)
	}
}

// TestSmokeAnAgentkitBrainRecovers_TS_07_81 verifies TS-07-81 (07-PATH-2): the
// agentkit brain on the faux provider recovers from a denied tool and a refused
// submission.
// Verifies: 07-PATH-2, 07-REQ-10.5
func TestSmokeAnAgentkitBrainRecovers_TS_07_81(t *testing.T) {
	p := faux.New(
		withUsage(call("c1", "write_file", `{"path":"a.txt","content":"x"}`), usage(0.01, 100, 20)),
		withUsage(call("c2", "submit_test", `{"cite":"nowhere.go"}`), usage(0.01, 120, 20)),
		withUsage(call("c3", "submit_test", `{"cite":"README.md","summary":"fixed"}`), usage(0.01, 140, 20)),
		faux.Text("never requested"))
	s := newSmoke(t, []string{"--brain", "agentkit"}, Options{
		Model: "faux-1", NewProvider: func(string) (core.ProviderClient, error) { return p, nil }, Getenv: noEnv,
	})
	s.settings.Brain = AgentkitName

	res, err := s.attempt(1, nil)

	if err != nil || !res.Outcome.Submitted || res.Outcome.StopReason != StopSubmitted {
		t.Fatalf("Execute = %+v, %v", res, err)
	}
	reqs := p.Requests()
	if len(reqs) != 3 {
		t.Fatalf("%d requests, want 3", len(reqs))
	}
	if got := lastToolResult(t, reqs[1]); !strings.Contains(got, "tool write_file is not in this run's allowlist") {
		t.Errorf("after the first turn the model read %q, want the denial", got)
	}
	if got := lastToolResult(t, reqs[2]); !strings.Contains(got, `"nowhere.go" is not in the tree`) {
		t.Errorf("after the second turn the model read %q, want the problem of the submission", got)
	}
	if got, _ := os.ReadFile(filepath.Join(s.run.Path, "submission.json")); !strings.Contains(string(got), `"cite":"README.md"`) {
		t.Errorf("submission.json = %s", got)
	}
	if u := s.report.lastUsage(); u.CostSource != CostMeasured || u.Brain != AgentkitName || u.Turns != 3 {
		t.Errorf("usage = %+v, want the measured cost of 3 turns", u)
	}
	if kinds := s.kinds(); kinds[len(kinds)-1] != "box_close" {
		t.Errorf("ledger = %v, want the box closed last", kinds)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "a.txt")); err == nil {
		t.Error("the denied write reached the host checkout")
	}
}

// countingRegistry has the real local provider under the name `local`, counted.
func countingRegistry() (*box.Registry, *atomic.Int32, *atomic.Int32) {
	creates, closes := &atomic.Int32{}, &atomic.Int32{}
	r := box.NewRegistry()
	r.Register(box.Entry{Name: "local", Unconfined: true, New: func() sandbox.Provider {
		return &countedProvider{Provider: local.Unisolated(), creates: creates, closes: closes}
	}})
	return r, creates, closes
}

type countedProvider struct {
	sandbox.Provider
	creates, closes *atomic.Int32
}

func (p *countedProvider) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	sb, err := p.Provider.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	p.creates.Add(1)
	return &countedSandbox{Sandbox: sb, closes: p.closes}, nil
}

type countedSandbox struct {
	sandbox.Sandbox
	closes *atomic.Int32
}

func (s *countedSandbox) Close(ctx context.Context) error {
	s.closes.Add(1)
	return s.Sandbox.Close(ctx)
}

// TestSmokeAStaleSubmissionIsNoResult_TS_07_83 verifies TS-07-83 (07-PATH-4): a
// run with only a stale submission fails as no_result, still records usage and
// closes the box.
// Verifies: 07-PATH-4, 07-REQ-7.4, 07-REQ-7.8
func TestSmokeAStaleSubmissionIsNoResult_TS_07_83(t *testing.T) {
	first := writeScript(t, `{"steps":[{"submit":{"summary":"attempt one","cite":"README.md"},"expect":"accepted"}]}`)
	s := newSmoke(t, []string{"--brain", "scripted", "--script", first}, Options{})
	registry, creates, closes := countingRegistry()
	s.registry = registry
	second, err := Default.New(ScriptedName, Options{Script: writeScript(t, `{"steps":[{"tool":"list_files","args":{"path":"."},"expect":"ok"}]}`)})
	if err != nil {
		t.Fatal(err)
	}

	var resolved Result
	var attemptTwo error
	_, err = s.inBox(func(sess *box.Session) (Result, error) {
		// Attempt 1 leaves a valid submission in the box.
		if _, err := s.execute(sess, 1, nil); err != nil {
			t.Fatalf("attempt 1: %v", err)
		}
		// Attempt 2, in the same box: its steps end without a submission.
		s.brain = second
		resolved, attemptTwo = s.execute(sess, 2, nil)
		return resolved, attemptTwo
	})

	f := asFailure(t, err)
	if f.Category != toolio.CategoryNoResult || !f.Retryable {
		t.Errorf("attempt 2: %+v, want failed/no_result, retryable", f)
	}
	if resolved.Payload != nil {
		t.Errorf("the stale submission of attempt 1 was taken for attempt 2's: %s", resolved.Payload)
	}
	kinds := s.kinds()
	if !slices.Contains(kinds, "submission_stale") {
		t.Errorf("ledger = %v, want submission_stale", kinds)
	}
	if _, err := os.Stat(filepath.Join(s.run.Path, "submission.json")); err == nil {
		t.Error("a submission file was written for attempt 2")
	}
	if u := s.report.lastUsage(); u.Brain != "scripted" || u.Turns != 2 {
		t.Errorf("usage = %+v, want the total of both attempts (1 + 1 turns)", u)
	}
	if kinds[len(kinds)-1] != "box_close" || creates.Load() != 1 || closes.Load() != 1 {
		t.Errorf("ledger ends %q; %d sandboxes created and %d closed, want the box closed on the error path", kinds[len(kinds)-1], creates.Load(), closes.Load())
	}
}
