package fix

import (
	"context"
	"crypto/rand"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/policy"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// roundState is the state of a run over the fixture gate.
func roundState() State {
	s := sampleState()
	s.Suite = [][]string{{"make", "check"}}
	s.Programs = []string{"make", "sh"}
	return s
}

// TestTheBoxFilesAreWhatTheirReadersTake guards 09-REQ-3.7 at the bytes: the gate.json is
// the pinned table as project.Detect reads it, and the guard.json is the suite, the
// protected paths and the programs as policy.LoadPolicy reads them.
func TestTheBoxFilesAreWhatTheirReadersTake(t *testing.T) {
	s := roundState()
	gate, err := GateFile(s)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := GuardFile(s)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{"gate.json": gate, "guard.json": guard} {
		if err := os.MkdirAll(filepath.Join(dir, ".agent-fox"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".agent-fox", name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	detected, err := project.Detect(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if detected.Gate.Source != project.SourceFile || len(detected.Gate.Checks) != 1 || detected.Gate.Checks[0].Name != "make check" || !slices.Equal(detected.Gate.Checks[0].Argv, []string{"make", "check"}) {
		t.Errorf("gate.json is read as %+v, want the pinned table", detected.Gate)
	}
	pol, err := policy.LoadPolicy(context.Background(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(pol.Suite) != 1 || !slices.Equal(pol.Suite[0], []string{"make", "check"}) || !slices.Equal(pol.ProtectedPaths, policy.DefaultProtectedPaths()) || !slices.Equal(pol.AllowPrograms, AllowPrograms([]string{"make", "sh"})) {
		t.Errorf("guard.json is read as %+v, want the suite, the default protected paths and the allowed programs", pol)
	}
}

// roundWorld is a checkout with the fixture gate, a run directory in it, and the state of
// the run.
type roundWorld struct {
	repo   *testRepo
	run    *toolio.RunDir
	state  State
	prefix string
	task   string
}

func newRoundWorld(t *testing.T) *roundWorld {
	t.Helper()
	r := newTestRepo(t, redGate(), false)
	run, err := toolio.NewRunDir(r.Dir, time.Now, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s := roundState()
	s.RunID = run.ID
	return &roundWorld{repo: r, run: run, state: s, prefix: "# Fix mandate\n", task: "## Task\n"}
}

// round runs one round of the brain over a box on the checkout.
func (w *roundWorld) round(t *testing.T, b brain.Brain, number int) (RoundResult, error) {
	t.Helper()
	var res RoundResult
	err := box.With(context.Background(), box.Request{Provider: "local", HostDir: w.repo.Dir, RunID: w.run.ID, Tool: "fix", Record: w.run.Record}, func(sess *box.Session) error {
		var err error
		res, err = RunRound(context.Background(), RoundParams{
			Dir: w.repo.Dir, Git: w.repo.Git, Session: sess, Name: "spy", Brain: b, Tally: &brain.Tally{}, RunDir: w.run,
			State: w.state, Prefix: w.prefix, Task: w.task, Round: number, MaxCostUSD: 3, MaxTurns: 7, Timeout: time.Minute,
		})
		return err
	})
	return res, err
}

// spy is a brain that does what a function says, and keeps the brief it was given.
type spy struct {
	run    func(ctx context.Context, b brain.Brief) (brain.Outcome, error)
	briefs []brain.Brief
}

func (s *spy) Run(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
	s.briefs = append(s.briefs, b)
	return s.run(ctx, b)
}

// submit submits the payload the way a brain's tool does, through the submit core.
func submit(ctx context.Context, b brain.Brief, payload string) (brain.Outcome, error) {
	target := submission.Target{Tree: b.Box.FS(ctx), Write: b.Box.WriteFile, RunID: b.RunID}
	if _, err := submission.Submit(ctx, target, b.Kind, []byte(payload), time.Now()); err != nil {
		return brain.Outcome{StopReason: brain.StopError}, err
	}
	return brain.Outcome{Submitted: true, StopReason: brain.StopSubmitted}, nil
}

const reportPayload = `{"subject":"say hello","root_cause":"the script says helo","change":"say hello","files":[{"path":"src/greet.sh","why":"the word"}]}`

// TestARoundPlacesTheFilesRunsTheBrainAndAppliesAnAcceptedTree guards 09-REQ-3.7,
// 09-REQ-5.1 and 09-REQ-5.5 at the round: the gate.json and the guard.json are in the box
// when the brain starts, the brief it is given is the run's, and the tree of its accepted
// report is on the checkout, uncommitted.
func TestARoundPlacesTheFilesRunsTheBrainAndAppliesAnAcceptedTree(t *testing.T) {
	requireMake(t)
	w := newRoundWorld(t)
	head := strings.TrimSpace(gitDo(t, w.repo.Dir, "rev-parse", "HEAD"))
	var seen = map[string]string{}
	b := &spy{run: func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		for _, name := range []string{".agent-fox/gate.json", ".agent-fox/guard.json"} {
			data, err := fs.ReadFile(b.Box.FS(ctx), name)
			if err != nil {
				t.Errorf("%s is not in the box when the brain starts: %v", name, err)
			}
			seen[name] = string(data)
		}
		if err := os.WriteFile(filepath.Join(b.Box.Spec().Workdir, "src", "greet.sh"), []byte("echo hello\n"), 0o644); err != nil {
			return brain.Outcome{}, err
		}
		return submit(ctx, b, reportPayload)
	}}

	res, err := w.round(t, b, 3)

	if err != nil {
		t.Fatal(err)
	}
	if res.Submission.Report == nil || res.Submission.Report.Subject != "say hello" || res.Apply == nil || res.Apply.Written != 1 || res.Apply.Deleted != 0 {
		t.Errorf("result = %+v (apply %+v), want the report and one file written", res, res.Apply)
	}
	if got := snapshotOf(t, w.repo.Dir, []string{"src/greet.sh"}); got["src/greet.sh"] != "file:644:echo hello\n" {
		t.Errorf("the checkout holds %v", got)
	}
	if strings.TrimSpace(gitDo(t, w.repo.Dir, "rev-parse", "HEAD")) != head {
		t.Error("the round moved HEAD")
	}
	gate, _ := GateFile(w.state)
	guard, _ := GuardFile(w.state)
	if seen[".agent-fox/gate.json"] != string(gate) || seen[".agent-fox/guard.json"] != string(guard) {
		t.Errorf("the box held %v, want the files the run's state makes", seen)
	}
	if len(b.briefs) != 1 {
		t.Fatalf("the brain ran %d times", len(b.briefs))
	}
	got := b.briefs[0]
	if got.Attempt != 3 || got.RunID != w.run.ID || got.Prefix != w.prefix || got.Task != w.task || got.MaxCostUSD != 3 || got.MaxTurns != 7 || got.Kind.Name != KindName {
		t.Errorf("brief = %+v, want the round's number, run, prefix, task and bounds", got)
	}
	if len(got.Tools) == 0 || !slices.Contains(got.Hook.AllowPrograms, "make") || !slices.Equal(got.Hook.ProtectedPaths[:5], policy.DefaultProtectedPaths()) {
		t.Errorf("tools %v, hook %+v", got.Tools, got.Hook)
	}
	var ledger []string
	if entries, err := w.run.Ledger(); err == nil {
		for _, e := range entries {
			ledger = append(ledger, e.Kind)
		}
	}
	if !slices.Contains(ledger, "apply") || slices.Contains(ledger, "measure") {
		t.Errorf("ledger kinds = %v, want apply and no measure", ledger)
	}
}

// TestARoundThatAsksAppliesNothing guards 09-REQ-4: a question is not a change, so the
// tree of the box is left where it is.
func TestARoundThatAsksAppliesNothing(t *testing.T) {
	requireMake(t)
	w := newRoundWorld(t)
	before := snapshotOf(t, w.repo.Dir, allPaths(t, w.repo.Dir))
	b := &spy{run: func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		if err := os.WriteFile(filepath.Join(b.Box.Spec().Workdir, "src", "greet.sh"), []byte("echo half done\n"), 0o644); err != nil {
			return brain.Outcome{}, err
		}
		return submit(ctx, b, `{"question":{"text":"Which greeting?","options":["hello","hi"]}}`)
	}}

	res, err := w.round(t, b, 1)

	if err != nil {
		t.Fatal(err)
	}
	if res.Submission.Question == nil || res.Submission.Question.Text != "Which greeting?" || res.Apply != nil {
		t.Errorf("result = %+v, want the question and nothing applied", res)
	}
	if after := snapshotOf(t, w.repo.Dir, allPaths(t, w.repo.Dir)); !equalSnapshots(before, after) {
		t.Errorf("the checkout changed: %v", after)
	}
}

// TestARoundWithoutAnAcceptedSubmissionAppliesNothing_TS_09_37 guards 09-REQ-5.7 at the
// round: a brain that edits and ends without submitting, and one whose report is refused,
// leave the checkout as it was.
func TestARoundWithoutAnAcceptedSubmissionAppliesNothing_TS_09_37(t *testing.T) {
	requireMake(t)
	for name, run := range map[string]func(context.Context, brain.Brief) (brain.Outcome, error){
		"it never submits": func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
			return brain.Outcome{StopReason: brain.StopEnded}, nil
		},
		"it is refused": func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
			return submit(ctx, b, `{"subject":"fix: wrong."}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newRoundWorld(t)
			before := snapshotOf(t, w.repo.Dir, allPaths(t, w.repo.Dir))
			edit := run
			b := &spy{run: func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
				if err := os.WriteFile(filepath.Join(b.Box.Spec().Workdir, "src", "greet.sh"), []byte("echo edited\n"), 0o644); err != nil {
					return brain.Outcome{}, err
				}
				return edit(ctx, b)
			}}

			res, err := w.round(t, b, 1)

			if err == nil || res.Apply != nil {
				t.Errorf("result = %+v, %v, want the failure of a round with no submission", res, err)
			}
			if after := snapshotOf(t, w.repo.Dir, allPaths(t, w.repo.Dir)); !equalSnapshots(before, after) {
				t.Errorf("the checkout changed: %v", after)
			}
		})
	}
}

// warnings is a report that keeps the warnings it is given.
type warnings struct{ codes []string }

func (*warnings) Emit(string, any)         {}
func (w *warnings) Warn(code, _ string)    { w.codes = append(w.codes, code) }
func (*warnings) Artifact(toolio.Artifact) {}
func (*warnings) SetUsage(toolio.Usage)    {}

// TestARoundWarnsOfALedgerItCannotWriteAndGoesOn guards that the notice of the apply gets
// to the report of the run, beside the one of the brain's own run.
func TestARoundWarnsOfALedgerItCannotWriteAndGoesOn(t *testing.T) {
	requireMake(t)
	w := newRoundWorld(t)
	if err := os.Mkdir(filepath.Join(w.run.Path, "ledger.jsonl"), 0o755); err != nil { // a ledger that cannot be written to
		t.Fatal(err)
	}
	report := &warnings{}
	b := &spy{run: func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		return submit(ctx, b, reportPayload)
	}}
	var res RoundResult
	err := box.With(context.Background(), box.Request{Provider: "local", HostDir: w.repo.Dir, RunID: w.run.ID, Tool: "fix"}, func(sess *box.Session) error {
		var err error
		res, err = RunRound(context.Background(), RoundParams{
			Dir: w.repo.Dir, Git: w.repo.Git, Session: sess, Name: "spy", Brain: b, Tally: &brain.Tally{}, RunDir: w.run, Report: report,
			State: w.state, Prefix: w.prefix, Task: w.task, Round: 1, MaxCostUSD: 1, MaxTurns: 1, Timeout: time.Minute,
		})
		return err
	})

	if err != nil || res.Apply == nil {
		t.Fatalf("RunRound = %+v, %v", res, err)
	}
	if n := strings.Count(strings.Join(report.codes, " "), "ledger_unwritable"); n != 2 {
		t.Errorf("warnings = %v, want ledger_unwritable from the brain's run and from the apply", report.codes)
	}
}
