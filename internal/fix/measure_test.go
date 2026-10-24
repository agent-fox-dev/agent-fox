package fix

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// measuredFiles is the fixture gate, with a script that prints something and a test that
// wants something, and files for the protected paths and for a change.
func measuredFiles() map[string]string {
	files := map[string]string{}
	for k, v := range gateFiles {
		files[k] = v
	}
	files["src/greet.sh"] = "echo helo\n"
	files["tests/greet_test.sh"] = "[ -n \"$(sh src/greet.sh)\" ] || { echo \"FAIL: greet printed nothing\"; exit 1; }\n"
	files[".specs/a.md"] = "# spec\n"
	files["docs/readme.md"] = "readme\n"
	return files
}

// measureWorld is a checkout whose preflight has run, so that its baseline is measured,
// and the counters of the gate and of the revert check.
type measureWorld struct {
	repo       *testRepo
	state      State
	gateRuns   int
	conformRun int
	warnings   []string
	// gateOptions are the options of the last gate run and conformSince the commit of
	// the last revert check.
	gateOptions  checks.Options
	conformSince string
}

func newMeasureWorld(t *testing.T, files map[string]string) *measureWorld {
	t.Helper()
	return newMeasureWorldWith(t, files, false)
}

// newMeasureWorldWith is a measure world whose checkout has a bare origin when withOrigin
// is set.
func newMeasureWorldWith(t *testing.T, files map[string]string, withOrigin bool) *measureWorld {
	t.Helper()
	requireMake(t)
	r := newTestRepo(t, files, withOrigin)
	p, _ := params(r)
	p.Land = ""
	pre, err := preflightAll(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	return &measureWorld{repo: r, state: State{
		Base: pre.Base, BaseHead: pre.BaseHead, Table: pre.Detection.Gate, BaselineGateID: pre.Baseline.ID, BaselinePassed: pre.Baseline.Passed,
		BaselineMS: GateMS(pre.Baseline),
	}}
}

func (w *measureWorld) edit(t *testing.T, path, content string) {
	t.Helper()
	full := filepath.Join(w.repo.Dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (w *measureWorld) read(path string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(w.repo.Dir, filepath.FromSlash(path)))
	return string(data), err == nil
}

// gateSeam and conformSeam are the seams of the world, which count the runs.
func (w *measureWorld) gateSeam() func(ctx context.Context, dir string, o checks.Options) (checks.Result, error) {
	return func(ctx context.Context, dir string, o checks.Options) (checks.Result, error) {
		w.gateRuns++
		w.gateOptions = o
		return checks.Run(ctx, dir, o)
	}
}

func (w *measureWorld) conformSeam() func(ctx context.Context, dir, since string, o conform.Options) (conform.Result, error) {
	return func(ctx context.Context, dir, since string, o conform.Options) (conform.Result, error) {
		w.conformRun++
		w.conformSince = since
		return conform.Check(ctx, dir, since, o)
	}
}

// measure measures the change on the checkout, counting the runs of the gate and of the
// revert check, and keeping the codes of the warnings.
func (w *measureWorld) measure(report *Report) (Measurement, error) {
	w.warnings = nil
	return Measure(context.Background(), MeasureParams{
		Dir: w.repo.Dir, Git: w.repo.Git, State: w.state, Report: report,
		Warn: func(code, _ string) { w.warnings = append(w.warnings, code) },
		Gate: func(ctx context.Context, dir string, o checks.Options) (checks.Result, error) {
			w.gateRuns++
			w.gateOptions = o
			return checks.Run(ctx, dir, o)
		},
		Conform: func(ctx context.Context, dir, since string, o conform.Options) (conform.Result, error) {
			w.conformRun++
			w.conformSince = since
			return conform.Check(ctx, dir, since, o)
		},
	})
}

func count(list []string, code string) int {
	n := 0
	for _, c := range list {
		if c == code {
			n++
		}
	}
	return n
}

// TestProtectedPathsAreRestoredAndReported_TS_09_39 verifies TS-09-39: a protected file
// that was changed is back to its content at the base, one that was added is gone, both
// are listed with a warning each, and the change to the sources stays.
// Verifies: 09-REQ-6.2
func TestProtectedPathsAreRestoredAndReported_TS_09_39(t *testing.T) {
	w := newMeasureWorld(t, measuredFiles())
	w.edit(t, ".specs/a.md", "# spec, rewritten\n")
	w.edit(t, ".github/workflows/ci.yml", "on: push\n")
	w.edit(t, "docs/readme.md", "readme, changed\n")

	m, err := w.measure(nil)

	if err != nil {
		t.Fatal(err)
	}
	if got, _ := w.read(".specs/a.md"); got != "# spec\n" {
		t.Errorf(".specs/a.md = %q, want its content at the base", got)
	}
	if _, there := w.read(".github/workflows/ci.yml"); there {
		t.Error("the workflow that was added is still there")
	}
	if got, _ := w.read("docs/readme.md"); got != "readme, changed\n" {
		t.Errorf("docs/readme.md = %q, want the change to stay", got)
	}
	if !slices.Equal(m.Reverted, []string{".github/workflows/ci.yml", ".specs/a.md"}) || !slices.Equal(m.Changed, []string{"docs/readme.md"}) {
		t.Errorf("reverted %v, changed %v, want the two protected paths and the one change", m.Reverted, m.Changed)
	}
	if count(w.warnings, "protected_path_reverted") != 2 {
		t.Errorf("warnings = %v, want protected_path_reverted for each", w.warnings)
	}
	if status := gitDo(t, w.repo.Dir, "status", "--porcelain"); strings.TrimSpace(status) != "M docs/readme.md" {
		t.Errorf("git status = %q, want the one change alone", status)
	}
	if _, err := os.Stat(filepath.Join(w.repo.Dir, ".github")); err == nil {
		t.Error(".github, which the removed workflow left empty, is still there")
	}
}

// TestNoChangeAfterTheRevertIsARefusalAndTheGateIsNotRun_TS_09_40 verifies TS-09-40.
// Verifies: 09-REQ-6.3
func TestNoChangeAfterTheRevertIsARefusalAndTheGateIsNotRun_TS_09_40(t *testing.T) {
	w := newMeasureWorld(t, measuredFiles())
	w.edit(t, ".specs/a.md", "# spec, rewritten\n")

	m, err := w.measure(nil)

	if err != nil {
		t.Fatal(err)
	}
	if m.Refusal == nil || m.Refusal.Kind != RefusalNoChange || !slices.Equal(m.Refusal.Reverted, []string{".specs/a.md"}) || m.Lands || m.Landable {
		t.Errorf("measurement = %+v, want the refusal no_change with the path that was put back", m)
	}
	if w.gateRuns != 0 || w.conformRun != 0 || m.After != nil {
		t.Errorf("the gate ran %d times and the revert check %d times, want neither", w.gateRuns, w.conformRun)
	}
	if m.Verdict != "no_change" {
		t.Errorf("verdict = %q, want no_change", m.Verdict)
	}
}

// TestTheGateAfterIsComparedWithTheBaselineByID_TS_09_42 verifies TS-09-42: the gate is
// run with the pinned table and compared with the baseline, and under --no-verify it is
// not run and the verdict is unverified, and the change lands all the same.
// Verifies: 09-REQ-6.5
func TestTheGateAfterIsComparedWithTheBaselineByID_TS_09_42(t *testing.T) {
	w := newMeasureWorld(t, measuredFiles())
	w.edit(t, "docs/readme.md", "readme, changed\n")

	m, err := w.measure(nil)

	if err != nil {
		t.Fatal(err)
	}
	if m.Verdict != checks.VerdictPass || !m.Landable || !m.Lands || m.Refusal != nil || m.After == nil || m.After.GateID == "" || m.After.GateID == w.state.BaselineGateID || !m.After.Passed {
		t.Errorf("measurement = %+v, want a passing gate of its own that lands", m)
	}
	if w.gateRuns != 1 {
		t.Errorf("the gate ran %d times, want once", w.gateRuns)
	}
	if o := w.gateOptions; !reflect.DeepEqual(o.Table, w.state.Table) || len(o.Table.Checks) == 0 {
		t.Errorf("options = %+v, want the pinned table", o)
	}

	w2 := newMeasureWorld(t, measuredFiles())
	w2.state.NoVerify = true
	w2.edit(t, "docs/readme.md", "readme, changed\n")
	m2, err := w2.measure(nil)
	if err != nil {
		t.Fatal(err)
	}
	if m2.Verdict != checks.VerdictUnverified || m2.After != nil || m2.Landable || !m2.Lands || m2.Refusal != nil || m2.Closes || m2.RevertCheck != nil {
		t.Errorf("under --no-verify: %+v, want unverified, no gate, no revert check, and a change that lands", m2)
	}
	if w2.gateRuns != 0 || w2.conformRun != 0 {
		t.Errorf("under --no-verify the gate ran %d times and the revert check %d", w2.gateRuns, w2.conformRun)
	}
	if count(w2.warnings, "fix_not_proven") != 0 {
		t.Errorf("warnings = %v", w2.warnings)
	}
}

// provenFiles is a checkout whose script prints something wrong and whose test is happy
// with anything.
func provenFiles() map[string]string { return measuredFiles() }

// TestTheRevertCheckRunsOnLandableVerdictsAndOnlyProvenCloses_TS_09_43 verifies TS-09-43.
// Verifies: 09-REQ-6.6
func TestTheRevertCheckRunsOnLandableVerdictsAndOnlyProvenCloses_TS_09_43(t *testing.T) {
	t.Run("proven", func(t *testing.T) {
		w := newMeasureWorld(t, provenFiles())
		w.edit(t, "src/greet.sh", "echo hello\n")
		w.edit(t, "tests/hello_test.sh", "[ \"$(sh src/greet.sh)\" = hello ] || { echo \"FAIL: not hello\"; exit 1; }\n")

		m, err := w.measure(nil)

		if err != nil {
			t.Fatal(err)
		}
		if !m.Lands || !m.Closes || m.RevertCheck == nil || m.RevertCheck.Outcome != conform.OutcomeProven || m.RevertCheck.Reason == "" || w.conformRun != 1 {
			t.Errorf("measurement = %+v (revert check ran %d times), want proven, which closes", m, w.conformRun)
		}
		if count(w.warnings, "fix_not_proven") != 0 || w.conformSince != w.state.BaseHead {
			t.Errorf("warnings = %v, the revert check went back to %q, want no warning and the base %s", w.warnings, w.conformSince, w.state.BaseHead)
		}
		if got, _ := w.read("src/greet.sh"); got != "echo hello\n" {
			t.Errorf("src/greet.sh = %q: the revert check must put the tree back", got)
		}
	})
	t.Run("not proven", func(t *testing.T) {
		w := newMeasureWorld(t, provenFiles())
		w.edit(t, "src/greet.sh", "echo hello\n")
		w.edit(t, "tests/nonempty_test.sh", "[ -n \"$(sh src/greet.sh)\" ] || exit 1\n")

		m, err := w.measure(nil)

		if err != nil {
			t.Fatal(err)
		}
		if !m.Lands || !m.Landable || m.Closes || m.RevertCheck == nil || m.RevertCheck.Outcome != conform.OutcomeNotProven || m.Refusal != nil {
			t.Errorf("measurement = %+v, want landable, not proven, and no Closes", m)
		}
		if count(w.warnings, "fix_not_proven") != 1 {
			t.Errorf("warnings = %v, want fix_not_proven", w.warnings)
		}
	})
	t.Run("regressed", func(t *testing.T) {
		w := newMeasureWorld(t, provenFiles())
		w.edit(t, "src/greet.sh", ":\n") // prints nothing, which the test of the checkout does not take
		w.edit(t, ".specs/a.md", "# spec, rewritten\n")

		m, err := w.measure(nil)

		if err != nil {
			t.Fatal(err)
		}
		if m.Verdict != checks.VerdictRegressed || m.Lands || m.RevertCheck != nil || m.Closes || w.conformRun != 0 {
			t.Errorf("measurement = %+v (revert check ran %d times), want regressed, refused and no revert check", m, w.conformRun)
		}
		if m.Refusal == nil || m.Refusal.Kind != RefusalGate || m.Refusal.Verdict != checks.VerdictRegressed || len(m.Refusal.Failing) != 1 ||
			m.Refusal.Failing[0].Name != "make check" || !strings.Contains(m.Refusal.Failing[0].Tail, "FAIL: greet printed nothing") {
			t.Errorf("refusal = %+v, want the verdict and the failing check with its output", m.Refusal)
		}
		if m.After == nil || m.After.Passed {
			t.Errorf("after = %+v, want a gate that failed", m.After)
		}
		if !slices.Equal(m.Refusal.Reverted, []string{".specs/a.md"}) {
			t.Errorf("refusal reverted = %v, want the protected path that was put back", m.Refusal.Reverted)
		}
	})
}

// TestTheTailsOfTheFailingChecksAreCapped guards the refusal that the brain reads: the end
// of the output of each failing check, at most 4 KiB of it.
func TestTheTailsOfTheFailingChecksAreCapped(t *testing.T) {
	files := measuredFiles()
	w := newMeasureWorld(t, files)
	w.edit(t, "tests/greet_test.sh", "i=0; while [ $i -lt 3000 ]; do echo \"line $i of the noise\"; i=$((i+1)); done; echo \"FAIL: the last word\"; exit 1\n")

	m, err := w.measure(nil)

	if err != nil {
		t.Fatal(err)
	}
	if m.Refusal == nil || len(m.Refusal.Failing) != 1 {
		t.Fatalf("measurement = %+v, want a refusal with the failing check", m)
	}
	if tail := m.Refusal.Failing[0].Tail; len(tail) > MaxTail || !strings.Contains(tail, "FAIL: the last word") || strings.Contains(tail, "line 0 of the noise") {
		t.Errorf("tail is %d bytes and ends %q, want at most %d bytes from the end of the output", len(tail), tail[max(0, len(tail)-40):], MaxTail)
	}

	// Whatever a check keeps of its output, the refusal keeps the end of at most 4 KiB.
	long := checks.Result{Checks: []checks.CheckResult{
		{Name: "pass", Status: checks.StatusPass, Tail: "fine"},
		{Name: "big", Status: checks.StatusFail, Tail: strings.Repeat("x", 10000) + "THE END\n"},
		{Name: "timed out", Status: checks.StatusTimeout, Tail: "waiting"},
		{Name: "error", Status: checks.StatusError},
	}}
	got := failing(long)
	if len(got) != 3 || got[0].Name != "big" || len(got[0].Tail) > MaxTail || !strings.HasSuffix(got[0].Tail, "THE END") || got[1].Name != "timed out" || got[2].Name != "error" {
		t.Errorf("failing = %+v, want the three that did not pass, the big one cut to its end", got)
	}
}

// TestAGateThatCannotBeComparedIsARefusalWithTheReason guards the checks that ran not
// being the checks of the baseline: the change took a program the checks need away.
func TestAGateThatCannotBeComparedIsARefusalWithTheReason(t *testing.T) {
	requireMake(t)
	r := newTestRepo(t, map[string]string{"README.md": "x\n"}, false)
	table := project.Table{Source: project.SourceFile, Checks: []project.Check{
		{Name: "first", Argv: []string{"sh", "-c", "true"}, Kind: project.KindCheck, Source: "test", Available: true},
		{Name: "second", Argv: []string{"sh", "-c", "true"}, Kind: project.KindCheck, Source: "test", Available: true},
	}}
	baseline, err := checks.Run(context.Background(), r.Dir, checks.Options{Table: table})
	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(gitDo(t, r.Dir, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(r.Dir, "new.txt"), []byte("n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var warned []string

	m, err := Measure(context.Background(), MeasureParams{
		Dir: r.Dir, Git: r.Git, Warn: func(code, _ string) { warned = append(warned, code) },
		State: State{Base: "main", BaseHead: head, Table: table, BaselineGateID: baseline.ID, BaselinePassed: true},
		Gate: func(ctx context.Context, dir string, o checks.Options) (checks.Result, error) {
			o.Table.Checks = slices.Clone(o.Table.Checks)
			o.Table.Checks[1].Available = false // the program of the second check is gone
			return checks.Run(ctx, dir, o)
		},
	})

	if err != nil {
		t.Fatal(err)
	}
	if m.Verdict != checks.VerdictUnverified || m.Lands || m.Refusal == nil || m.Refusal.Kind != RefusalGate || m.Refusal.Detail == "" {
		t.Errorf("measurement = %+v, want an unverified refusal that says why", m)
	}
}

// TestAGateThatCannotRunIsAFailureOfTheRun guards the errors of the gate: a stopped run is
// the stop, and anything else is an io failure.
func TestAGateThatCannotRunIsAFailureOfTheRun(t *testing.T) {
	w := newMeasureWorld(t, measuredFiles())
	w.edit(t, "docs/readme.md", "readme, changed\n")
	run := func(gate func(context.Context, string, checks.Options) (checks.Result, error), conformFn func(context.Context, string, string, conform.Options) (conform.Result, error)) error {
		_, err := Measure(context.Background(), MeasureParams{Dir: w.repo.Dir, Git: w.repo.Git, State: w.state, Gate: gate, Conform: conformFn})
		return err
	}

	err := run(func(context.Context, string, checks.Options) (checks.Result, error) {
		return checks.Result{}, errors.New("disk full")
	}, nil)
	if f := failure(t, err); f.Category != toolio.CategoryIO || !strings.Contains(f.Message, "disk full") {
		t.Errorf("a gate that cannot run: %v", err)
	}
	err = run(nil, func(context.Context, string, string, conform.Options) (conform.Result, error) {
		return conform.Result{}, &conform.RestoreError{Dir: "/x", Cause: errors.New("busy")}
	})
	if f := failure(t, err); f.Category != toolio.CategoryIO || !strings.Contains(f.Message, "/x") {
		t.Errorf("a tree that cannot be put back: %v, want the backup named", err)
	}
	err = run(nil, func(context.Context, string, string, conform.Options) (conform.Result, error) {
		return conform.Result{}, &conform.GitError{Err: errors.New("bad object")}
	})
	if f := failure(t, err); f.Category != toolio.CategoryGit {
		t.Errorf("a revert check that git failed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = Measure(ctx, MeasureParams{Dir: w.repo.Dir, Git: w.repo.Git, State: w.state, Gate: func(context.Context, string, checks.Options) (checks.Result, error) {
		cancel()
		return checks.Result{}, errors.New("killed")
	}})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("a stopped run: %v, want the stop", err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	_, err = Measure(ctx2, MeasureParams{Dir: w.repo.Dir, Git: w.repo.Git, State: w.state, Conform: func(context.Context, string, string, conform.Options) (conform.Result, error) {
		cancel2()
		return conform.Result{}, errors.New("killed")
	}})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("a stopped revert check: %v, want the stop", err)
	}
}

// TestACommitOrABranchThatIsNotTheBaseStopsTheMeasuring guards 09-REQ-6.1: af makes the
// commit that counts, so a checkout that has moved is not measured, and the commits are named.
func TestACommitOrABranchThatIsNotTheBaseStopsTheMeasuring(t *testing.T) {
	for name, tc := range map[string]struct {
		move    func(t *testing.T, w *measureWorld)
		mention string
	}{
		"a foreign commit": {func(t *testing.T, w *measureWorld) {
			w.edit(t, "docs/readme.md", "committed by someone\n")
			gitDo(t, w.repo.Dir, "commit", "-q", "-am", "chore: someone else's commit")
		}, "someone else's commit"},
		"another branch": {func(t *testing.T, w *measureWorld) {
			gitDo(t, w.repo.Dir, "checkout", "-q", "-b", "elsewhere")
		}, "elsewhere"},
		"a detached head": {func(t *testing.T, w *measureWorld) {
			gitDo(t, w.repo.Dir, "checkout", "-q", "--detach")
		}, "detached"},
	} {
		t.Run(name, func(t *testing.T) {
			w := newMeasureWorld(t, measuredFiles())
			tc.move(t, w)
			w.edit(t, "docs/other.md", "a change\n")

			m, err := w.measure(nil)

			if f := failure(t, err); f.Category != toolio.CategoryGit || f.Retryable || !strings.Contains(f.Message, tc.mention) || !strings.Contains(f.Message, w.state.BaseHead[:7]) {
				t.Errorf("failure = %+v, want git, naming %q and the base", f, tc.mention)
			}
			if w.gateRuns != 0 || m.Refusal != nil || len(w.warnings) != 0 {
				t.Errorf("gate ran %d times, refusal %v, warnings %v: nothing is measured", w.gateRuns, m.Refusal, w.warnings)
			}
			if _, there := w.read("docs/other.md"); !there {
				t.Error("the checkout was touched")
			}
		})
	}
}

// TestAStateWithNoBaselineAndNoNoVerifyIsAnInternalFailure guards a state that has a gate
// to run and none to compare with.
func TestAStateWithNoBaselineAndNoNoVerifyIsAnInternalFailure(t *testing.T) {
	w := newMeasureWorld(t, measuredFiles())
	w.state.BaselineGateID = ""
	w.edit(t, "docs/readme.md", "readme, changed\n")

	_, err := w.measure(nil)

	if f := failure(t, err); f.Category != toolio.CategoryInternal || w.gateRuns != 0 {
		t.Errorf("failure %v, the gate ran %d times, want an internal failure before the gate", err, w.gateRuns)
	}
}

// TestAGateThatChangesTheTreeIsWarnedOf guards the tree the checks run on: a check that
// writes files changes what is committed, and the run says so.
func TestAGateThatChangesTheTreeIsWarnedOf(t *testing.T) {
	w := newMeasureWorld(t, measuredFiles())
	w.edit(t, "tests/run.sh", "set -e\necho generated > generated.txt\nfor t in tests/*_test.sh; do sh \"$t\"; done\n")

	m, err := w.measure(nil)

	if err != nil {
		t.Fatal(err)
	}
	if count(w.warnings, "tree_changed_during_gate") != 1 || !m.Lands {
		t.Errorf("warnings = %v, lands %v, want the warning of a tree the checks changed, and a change that lands", w.warnings, m.Lands)
	}
}
