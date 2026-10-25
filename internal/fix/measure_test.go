package fix

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestMeasure_PutsTheProtectedPathsBackAndSaysWhich(t *testing.T) {
	w := newMeasureWorld(t)
	w.edit(t, ".specs/a.md", "# spec, rewritten\n")
	w.edit(t, ".github/workflows/ci.yml", "on: push\n")
	w.edit(t, "docs/readme.md", "readme, changed\n")

	m, err := w.measure()

	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.Reverted, []string{".github/workflows/ci.yml", ".specs/a.md"}) || !slices.Equal(m.Changed, []string{"docs/readme.md"}) || countOf(w.warnings, WarnProtectedPathReverted) != 2 {
		t.Errorf("reverted %v, changed %v, warnings %v, want the two protected paths, each warned of, and the one change", m.Reverted, m.Changed, w.warnings)
	}
	if status := w.repo.git(t, "status", "--porcelain"); status != "M docs/readme.md" || w.repo.read(t, ".specs/a.md") != "# spec\n" {
		t.Errorf("git status = %q, want the one change alone and the protected file as it was", status)
	}
	if _, err := os.Stat(filepath.Join(w.repo.Dir, ".github")); err == nil {
		t.Error(".github, which the removed workflow left empty, is still there")
	}
}

func TestMeasure_AChangeThatIsNothingOnceThePathsAreBackIsRefusedUnmeasured(t *testing.T) {
	w := newMeasureWorld(t)
	w.edit(t, ".specs/a.md", "# spec, rewritten\n")

	m, err := w.measure()

	if err != nil || m.Verdict != VerdictNoChange || m.Lands || m.After != nil || m.Refusal == nil || m.Refusal.Kind != RefusalNoChange || !slices.Equal(m.Refusal.Reverted, []string{".specs/a.md"}) {
		t.Errorf("Measure = %+v, %v, want the refusal no_change with the path that was put back", m, err)
	}
	if w.gateRuns != 0 || w.conformRuns != 0 {
		t.Errorf("the gate ran %d times and the revert check %d times, want neither", w.gateRuns, w.conformRuns)
	}
}

// Only a revert check that proves the change closes the issue; a change it does not prove
// lands with a warning, and one the gate refuses gets no revert check.
func TestMeasure_TheGateDecidesWhetherItLandsAndTheRevertCheckWhetherItCloses(t *testing.T) {
	for name, tc := range map[string]struct {
		edits                    map[string]string
		verdict, outcome         string
		lands, closes, warnProof bool
	}{
		"proven": {map[string]string{"src/greet.sh": "echo hello\n", "tests/hello_test.sh": "[ \"$(sh src/greet.sh)\" = hello ] || { echo \"FAIL: not hello\"; exit 1; }\n"},
			checks.VerdictPass, conform.OutcomeProven, true, true, false},
		"not proven": {map[string]string{"src/greet.sh": "echo hello\n", "tests/nonempty_test.sh": "[ -n \"$(sh src/greet.sh)\" ] || exit 1\n"},
			checks.VerdictPass, conform.OutcomeNotProven, true, false, true},
		"regressed": {map[string]string{"src/greet.sh": ":\n"}, checks.VerdictRegressed, "", false, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			w := newMeasureWorld(t)
			for name, content := range tc.edits {
				w.edit(t, name, content)
			}

			m, err := w.measure()

			if err != nil || m.Verdict != tc.verdict || m.Lands != tc.lands || m.Landable != tc.lands || m.Closes != tc.closes || (m.Refusal == nil) != tc.lands {
				t.Fatalf("Measure = %+v, %v", m, err)
			}
			if m.After == nil || m.After.GateID == "" || m.After.GateID == w.state.BaselineGateID || m.After.Passed != tc.lands {
				t.Errorf("after = %+v, want a gate of its own", m.After)
			}
			if tc.outcome == "" {
				if m.RevertCheck != nil || w.conformRuns != 0 {
					t.Errorf("a refused change got the revert check %+v", m.RevertCheck)
				}
				if f := m.Refusal.Failing; m.Refusal.Kind != RefusalGate || len(f) != 1 || f[0].Name != "make check" || !strings.Contains(f[0].Tail, "FAIL: greet printed nothing") {
					t.Errorf("refusal = %+v, want the failing check with its output", m.Refusal)
				}
				return
			}
			if m.RevertCheck == nil || m.RevertCheck.Outcome != tc.outcome || w.conformSince != w.state.BaseHead || (countOf(w.warnings, WarnFixNotProven) == 1) != tc.warnProof {
				t.Errorf("revert check %+v since %q, warnings %v", m.RevertCheck, w.conformSince, w.warnings)
			}
			if w.repo.read(t, "src/greet.sh") != "echo hello\n" {
				t.Error("the revert check did not put the tree back")
			}
		})
	}
}

func TestMeasure_NoVerifyRunsNothingAndLands(t *testing.T) {
	w := newMeasureWorld(t)
	w.state.NoVerify = true
	w.edit(t, "docs/readme.md", "readme, changed\n")

	m, err := w.measure()

	if err != nil || m.Verdict != checks.VerdictUnverified || !m.Lands || m.Landable || m.After != nil || m.RevertCheck != nil || m.Refusal != nil || w.gateRuns+w.conformRuns != 0 {
		t.Errorf("Measure = %+v, %v, the gate and the revert check ran %d times, want unverified, nothing run, and a change that lands", m, err, w.gateRuns+w.conformRuns)
	}
}

func TestMeasure_ARefusalCarriesTheEndOfTheOutputOfEachFailingCheck(t *testing.T) {
	w := newMeasureWorld(t)
	w.edit(t, "tests/greet_test.sh", "i=0; while [ $i -lt 3000 ]; do echo \"line $i of the noise\"; i=$((i+1)); done; echo \"FAIL: the last word\"; exit 1\n")

	m, err := w.measure()

	if err != nil || m.Refusal == nil || len(m.Refusal.Failing) != 1 {
		t.Fatalf("Measure = %+v, %v, want a refusal with the failing check", m, err)
	}
	if tail := m.Refusal.Failing[0].Tail; len(tail) > MaxTail || !strings.Contains(tail, "FAIL: the last word") || strings.Contains(tail, "line 0 of") {
		t.Errorf("tail is %d bytes and ends %q, want at most %d bytes from the end of the output", len(tail), tail[max(0, len(tail)-40):], MaxTail)
	}

	got := failing(checks.Result{Checks: []checks.CheckResult{
		{Name: "pass", Status: checks.StatusPass, Tail: "fine"},
		{Name: "big", Status: checks.StatusFail, Tail: strings.Repeat("é", 5000) + "THE END\n"},
		{Name: "timed out", Status: checks.StatusTimeout},
	}})
	if len(got) != 2 || got[0].Name != "big" || len(got[0].Tail) > MaxTail || !strings.HasSuffix(got[0].Tail, "THE END") || !strings.HasPrefix(got[0].Tail, "é") || got[1].Name != "timed out" {
		t.Errorf("failing = %+v, want the two that did not pass, the big one cut at a character", got)
	}
}

// When the checks that ran are not the baseline's, there is nothing to compare: the change
// took a program a check needs away.
func TestMeasure_AGateThatCannotBeComparedIsARefusalThatSaysWhy(t *testing.T) {
	r := newRepo(t, nil, false)
	table := project.Table{Source: project.SourceFile, Checks: []project.Check{
		{Name: "first", Argv: []string{"sh", "-c", "true"}, Kind: project.KindCheck, Available: true},
		{Name: "second", Argv: []string{"sh", "-c", "true"}, Kind: project.KindCheck, Available: true},
	}}
	baseline, err := checks.Run(context.Background(), r.Dir, checks.Options{Table: table})
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, r.Dir, "new.txt", "n\n")
	for name, gone := range map[string][]int{"a check that cannot run": {1}, "no check that can run": {0, 1}} {
		m, err := Measure(context.Background(), MeasureParams{
			Dir: r.Dir, Git: r.Git, State: State{Base: "main", BaseHead: r.git(t, "rev-parse", "HEAD"), Table: table, BaselineGateID: baseline.ID},
			Gate: func(ctx context.Context, dir string, o checks.Options) (checks.Result, error) {
				o.Table.Checks = slices.Clone(o.Table.Checks)
				for _, i := range gone {
					o.Table.Checks[i].Available = false
				}
				return checks.Run(ctx, dir, o)
			},
		})

		if err != nil || m.Verdict != checks.VerdictUnverified || m.Lands || m.Refusal == nil || m.Refusal.Kind != RefusalGate || m.Refusal.Detail == "" {
			t.Errorf("%s: Measure = %+v, %v, want an unverified refusal that says why", name, m, err)
		}
	}
}

func TestMeasure_FailsWhenTheGateOrTheRevertCheckCannotRun(t *testing.T) {
	w := newMeasureWorld(t)
	w.edit(t, "docs/readme.md", "readme, changed\n")
	gate := func(err error) func(context.Context, string, checks.Options) (checks.Result, error) {
		return func(context.Context, string, checks.Options) (checks.Result, error) { return checks.Result{}, err }
	}
	revert := func(err error) func(context.Context, string, string, conform.Options) (conform.Result, error) {
		return func(context.Context, string, string, conform.Options) (conform.Result, error) {
			return conform.Result{}, err
		}
	}
	sandbox := &toolio.Failure{Category: toolio.CategorySandbox, Retryable: true, Message: "lost"}
	for name, tc := range map[string]struct {
		p        MeasureParams
		category string
		says     string
	}{
		"a gate that cannot run":           {MeasureParams{Gate: gate(errors.New("disk full"))}, toolio.CategoryIO, "disk full"},
		"a box lost in the revert check":   {MeasureParams{Conform: revert(sandbox)}, toolio.CategorySandbox, "lost"},
		"a tree that cannot be put back":   {MeasureParams{Conform: revert(&conform.RestoreError{Patch: "/x", Cause: errors.New("busy")})}, toolio.CategoryRestore, "/x"},
		"a revert check that git failed":   {MeasureParams{Conform: revert(&conform.GitError{Err: errors.New("bad object")})}, toolio.CategoryGit, "bad object"},
		"a revert check refused its table": {MeasureParams{Conform: revert(&checks.UsageError{Message: "no table"})}, toolio.CategoryUsage, "no table"},
		"a revert check that breaks":       {MeasureParams{Conform: revert(errors.New("boom"))}, toolio.CategoryIO, "boom"},
	} {
		tc.p.Dir, tc.p.Git, tc.p.State = w.repo.Dir, w.repo.Git, w.state
		_, err := Measure(context.Background(), tc.p)
		if f := failure(t, err); f.Category != tc.category || !strings.Contains(f.Message, tc.says) {
			t.Errorf("%s: %+v, want %s saying %q", name, f, tc.category, tc.says)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopGate := func(context.Context, string, checks.Options) (checks.Result, error) {
		cancel()
		return checks.Result{}, errors.New("killed")
	}
	if _, err := Measure(ctx, MeasureParams{Dir: w.repo.Dir, Git: w.repo.Git, State: w.state, Gate: stopGate}); !errors.Is(err, context.Canceled) {
		t.Errorf("a run stopped in the gate: %v, want the stop", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	stopRevert := func(context.Context, string, string, conform.Options) (conform.Result, error) {
		cancel()
		return conform.Result{}, errors.New("killed")
	}
	if _, err := Measure(ctx, MeasureParams{Dir: w.repo.Dir, Git: w.repo.Git, State: w.state, Conform: stopRevert}); !errors.Is(err, context.Canceled) {
		t.Errorf("a run stopped in the revert check: %v, want the stop", err)
	}
}

// af makes the commit that counts, so a checkout that moved is not measured, and the commits
// made since are named.
func TestMeasure_ACheckoutThatIsNotWhereTheRunBeganIsNotMeasured(t *testing.T) {
	for name, tc := range map[string]struct {
		move func(t *testing.T, w *measureWorld)
		says string
	}{
		"a foreign commit": {func(t *testing.T, w *measureWorld) {
			w.edit(t, "docs/readme.md", "committed by someone\n")
			testutil.Commit(t, w.repo.Dir, "someone else's commit")
		}, "1 commit(s) since the run began"},
		"another branch":  {func(t *testing.T, w *measureWorld) { w.repo.git(t, "checkout", "-q", "-b", "elsewhere") }, "elsewhere is checked out"},
		"a detached head": {func(t *testing.T, w *measureWorld) { w.repo.git(t, "checkout", "-q", "--detach") }, "HEAD is detached"},
	} {
		t.Run(name, func(t *testing.T) {
			w := newMeasureWorld(t)
			tc.move(t, w)
			w.edit(t, "docs/other.md", "a change\n")

			_, err := w.measure()

			if f := failure(t, err); f.Category != toolio.CategoryGit || f.Retryable || !strings.Contains(f.Message, tc.says) || !strings.Contains(f.Message, w.state.BaseHead[:12]) {
				t.Errorf("failure = %+v, want git naming %q and the base", f, tc.says)
			}
			if w.gateRuns != 0 || len(w.warnings) != 0 || w.repo.read(t, "docs/other.md") == "" {
				t.Errorf("the gate ran %d times, warnings %v: nothing is measured or touched", w.gateRuns, w.warnings)
			}
		})
	}
}

func TestMeasure_AVerifiedRunWithNoBaselineIsAnInternalFailure(t *testing.T) {
	w := newMeasureWorld(t)
	w.state.BaselineGateID = ""
	w.edit(t, "docs/readme.md", "readme, changed\n")

	_, err := w.measure()

	if f := failure(t, err); f.Category != toolio.CategoryInternal || w.gateRuns != 0 {
		t.Errorf("failure %v, the gate ran %d times, want an internal failure before the gate", err, w.gateRuns)
	}
}

// A check that writes files changes what is committed, and the run says so.
func TestMeasure_AGateThatChangesTheTreeIsWarnedOf(t *testing.T) {
	w := newMeasureWorld(t)
	w.edit(t, "tests/run.sh", "set -e\necho generated > generated.txt\nfor t in tests/*_test.sh; do sh \"$t\"; done\n")

	m, err := w.measure()

	if err != nil || countOf(w.warnings, WarnTreeChangedDuringGate) != 1 || !m.Lands {
		t.Errorf("Measure = %+v, %v, warnings %v, want the warning, and a change that lands", m, err, w.warnings)
	}
}
