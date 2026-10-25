package fix

import (
	"context"
	"crypto/rand"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/policy"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestBoxFiles_AreTheTableAndTheRulesAsTheirReadersTakeThem(t *testing.T) {
	s := sampleState()
	gate, err := GateFile(s)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := GuardFile(s)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	testutil.WriteFile(t, dir, ".agent-fox/gate.json", string(gate))
	testutil.WriteFile(t, dir, ".agent-fox/guard.json", string(guard))

	detected, err := project.Detect(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	pol, err := policy.LoadPolicy(context.Background(), dir, "")
	if err != nil {
		t.Fatal(err)
	}

	if c := detected.Gate.Checks; detected.Gate.Source != project.SourceFile || len(c) != 1 || c[0].Name != "make check" || !slices.Equal(c[0].Argv, []string{"make", "check"}) {
		t.Errorf("gate.json is read as %+v, want the pinned table", detected.Gate)
	}
	if len(pol.Suite) != 1 || !slices.Equal(pol.Suite[0], []string{"make", "check"}) || !slices.Equal(pol.ProtectedPaths, policy.DefaultProtectedPaths()) || !slices.Equal(pol.AllowPrograms, AllowPrograms(s.Programs)) {
		t.Errorf("guard.json is read as %+v, want the suite, the default protected paths and the allowed programs", pol)
	}
}

// The run directory of the box gets no fix.json: with one, the af fix run of a driver in the
// box would take itself for the round of the host.
func TestWriteBoxFiles_PlacesTheGateAndTheGuardAndNotTheState(t *testing.T) {
	sess := openLocalBox(t, newRepo(t, nil, false).Dir)
	ctx := context.Background()

	if err := WriteBoxFiles(ctx, sess, sampleState()); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{".agent-fox/gate.json", ".agent-fox/guard.json"} {
		if _, ok := boxFile(ctx, sess, rel); !ok {
			t.Errorf("%s is not in the box", rel)
		}
	}
	if _, ok := boxFile(ctx, sess, ".agent-fox/run/"+runID+"/"+StateFile); ok {
		t.Error("fix.json is in the box")
	}
}

// roundWorld is a checkout over the gate fixture with a run directory in it.
type roundWorld struct {
	repo  *repo
	run   *toolio.RunDir
	state State
}

func newRoundWorld(t *testing.T) *roundWorld {
	t.Helper()
	r := newRepo(t, measuredFiles(), false)
	run, err := toolio.NewRunDir(r.Dir, time.Now, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s := sampleState()
	s.RunID = run.ID
	return &roundWorld{repo: r, run: run, state: s}
}

// round runs one round of the brain over a local box on the checkout.
func (w *roundWorld) round(b brain.Brain, number int, report brain.Report) (RoundResult, error) {
	var res RoundResult
	err := box.With(context.Background(), box.Request{Provider: "local", HostDir: w.repo.Dir, RunID: w.run.ID, Tool: "fix", Record: w.run.Record}, func(sess *box.Session) error {
		var err error
		res, err = RunRound(context.Background(), RoundParams{
			Dir: w.repo.Dir, Git: w.repo.Git, Session: sess, Name: "spy", Brain: b, Tally: &brain.Tally{}, RunDir: w.run, Report: report,
			State: w.state, Prefix: "# Fix mandate\n", Task: "## Task\n", Round: number, MaxCostUSD: 3, MaxTurns: 7, Timeout: time.Minute,
		})
		return err
	})
	return res, err
}

func TestRunRound_AppliesTheTreeOfAnAcceptedReportToTheCheckout(t *testing.T) {
	w := newRoundWorld(t)
	head := w.repo.git(t, "rev-parse", "HEAD")
	seen := map[string]string{}
	var tools []string
	b := &spy{rounds: []func(context.Context, brain.Brief) (brain.Outcome, error){func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		for _, tool := range b.Box.Tools() {
			tools = append(tools, tool.Name)
		}
		for _, rel := range []string{".agent-fox/gate.json", ".agent-fox/guard.json"} {
			seen[rel], _ = boxFile(ctx, b.Box, rel)
		}
		return round(t, map[string]string{"src/greet.sh": "echo hello\n"}, reportPayload, 0, 1)(ctx, b)
	}}}

	res, err := w.round(b, 3, nil)

	if err != nil {
		t.Fatal(err)
	}
	if res.Submission.Report == nil || res.Submission.Report.Subject != "say hello" || res.Apply == nil || *res.Apply != (ApplyResult{Written: 1}) {
		t.Errorf("result = %+v (apply %+v), want the report and one file written", res, res.Apply)
	}
	if w.repo.read(t, "src/greet.sh") != "echo hello\n" || w.repo.git(t, "rev-parse", "HEAD") != head {
		t.Error("the tree of the box is not in the working tree of the checkout, or HEAD moved")
	}
	gate, _ := GateFile(w.state)
	guard, _ := GuardFile(w.state)
	if seen[".agent-fox/gate.json"] != string(gate) || seen[".agent-fox/guard.json"] != string(guard) {
		t.Errorf("the box held %v when the brain started, want the files of the run's state", seen)
	}
	got := b.briefs[0]
	if got.Attempt != 3 || got.RunID != w.run.ID || got.Prefix != "# Fix mandate\n" || got.Task != "## Task\n" || got.MaxCostUSD != 3 || got.MaxTurns != 7 || got.Timeout != time.Minute || got.Kind.Name != KindName {
		t.Errorf("brief = %+v, want the round's number, run, prefix, task and bounds", got)
	}
	if len(tools) < 2 || !slices.Equal(got.Tools, tools) || !slices.Contains(got.Hook.AllowPrograms, "make") {
		t.Errorf("the brief allows %v and the programs %v, the box has %v: a round gives its brain every tool and the gate's programs", got.Tools, got.Hook.AllowPrograms, tools)
	}
	if kinds := ledgerKinds(t, w.run); !slices.Contains(kinds, LedgerApply) || slices.Contains(kinds, LedgerMeasure) {
		t.Errorf("ledger kinds = %v, want apply and no measure", kinds)
	}
}

func TestRunRound_AppliesNothingWithoutAnAcceptedReport(t *testing.T) {
	for name, tc := range map[string]struct {
		end     func(context.Context, brain.Brief) (brain.Outcome, error)
		success bool
	}{
		"a question": {func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
			return submit(ctx, b, `{"question":{"text":"Which greeting?","options":["hello","hi"]}}`)
		}, true},
		"no submission": {func(context.Context, brain.Brief) (brain.Outcome, error) {
			return brain.Outcome{StopReason: brain.StopEnded}, nil
		}, false},
		"a refused report": {func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
			return submit(ctx, b, `{"subject":"fix: wrong."}`)
		}, false},
	} {
		t.Run(name, func(t *testing.T) {
			w := newRoundWorld(t)
			before := w.repo.files(t)
			b := &spy{rounds: []func(context.Context, brain.Brief) (brain.Outcome, error){func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
				edits(t, b, map[string]string{"src/greet.sh": "echo half done\n"})
				return tc.end(ctx, b)
			}}}

			res, err := w.round(b, 1, nil)

			if (err == nil) != tc.success || res.Apply != nil {
				t.Errorf("result = %+v, %v, want nothing applied and success %v", res, err, tc.success)
			}
			if tc.success && (res.Submission.Question == nil || res.Submission.Question.Text != "Which greeting?") {
				t.Errorf("submission = %+v, want the question", res.Submission)
			}
			if after := w.repo.files(t); !maps.Equal(before, after) {
				t.Errorf("the checkout changed: %v", after)
			}
		})
	}
}

func TestRunRound_WarnsOfALedgerItCannotWriteAndGoesOn(t *testing.T) {
	w := newRoundWorld(t)
	if err := os.Mkdir(filepath.Join(w.run.Path, "ledger.jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	report := &warnings{}
	b := &spy{rounds: []func(context.Context, brain.Brief) (brain.Outcome, error){func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		return submit(ctx, b, reportPayload)
	}}}

	res, err := w.round(b, 1, report)

	if err != nil || res.Apply == nil {
		t.Fatalf("RunRound = %+v, %v", res, err)
	}
	// One warning is the brain's run's, and one the apply's.
	if n := countOf(report.codes, "ledger_unwritable"); n != 2 {
		t.Errorf("warnings = %v, want ledger_unwritable twice", report.codes)
	}
}
