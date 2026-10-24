package fix

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// failure is the *toolio.Failure of an error.
func failure(t *testing.T, err error) *toolio.Failure {
	t.Helper()
	var f *toolio.Failure
	if !errors.As(err, &f) {
		t.Fatalf("error = %v (%T), want a *toolio.Failure", err, err)
	}
	return f
}

// noForge is the client of a run that has no forge: it is never asked.
type noForge struct{ issuex.Client }

func (noForge) Authenticated() bool { return false }

func params(r *testRepo) (PreflightParams, *int) {
	built := new(int)
	return PreflightParams{
		Dir: r.Dir, Land: LandNone, Forge: noForge{},
		NewBrain: func() (brain.Brain, error) { *built++; return nil, nil },
		Warn:     func(string, string) {},
	}, built
}

// TestPreflightStopsAtTheFirstFailingCheckInOrder_TS_09_9 verifies the order of
// TS-09-9 at the function: a repository that is detached, dirty, without an origin and
// without a gate fails at the earliest check; the brain is made last of the checks of the
// host, and the gate, which is the repository's and is checked where it runs, after them.
// Verifies: 09-REQ-2.1
func TestPreflightStopsAtTheFirstFailingCheckInOrder_TS_09_9(t *testing.T) {
	r := newTestRepo(t, map[string]string{"README.md": "# x\n"}, false)
	p, built := params(r)
	p.Land = LandBranch
	if err := os.WriteFile(filepath.Join(r.Dir, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Dirty, no gate and no origin: the tree is the first of them.
	if _, err := preflightAll(context.Background(), p); failure(t, err).Category != toolio.CategoryPreflight || !strings.Contains(err.Error(), "dirty.txt") {
		t.Errorf("dirty, no gate, no origin: %v, want the dirty tree", err)
	}
	// Detached as well: the branch comes before the tree.
	gitDo(t, r.Dir, "checkout", "-q", "--detach")
	if _, err := preflightAll(context.Background(), p); failure(t, err).Category != toolio.CategoryUsage {
		t.Errorf("detached and dirty: %v, want the usage failure of the branch", err)
	}
	gitDo(t, r.Dir, "checkout", "-q", "main")
	if err := os.Remove(filepath.Join(r.Dir, "dirty.txt")); err != nil {
		t.Fatal(err)
	}
	// Clean, no origin, no gate: the origin.
	if _, err := preflightAll(context.Background(), p); failure(t, err).Category != toolio.CategoryPreflight || !strings.Contains(err.Error(), "origin") {
		t.Errorf("no origin, no gate: %v, want the origin", err)
	}
	if *built != 0 {
		t.Errorf("the brain was made %d times by runs that were refused before it", *built)
	}
	// Nothing to land: the gate, after the brain was made.
	p.Land = LandNone
	if _, err := preflightAll(context.Background(), p); failure(t, err).FixHint == nil || failure(t, err).FixHint.Flag != "--no-verify" || *built != 1 {
		t.Errorf("no gate: %v (brain made %d times), want the gate, checked once the brain was made", err, *built)
	}

	p.NoVerify = true
	if res, err := preflightAll(context.Background(), p); err != nil || res.Base != "main" || res.Git == nil || *built != 2 {
		t.Errorf("nothing wrong: %+v, %v (brain made %d times), want it to pass and make the brain again", res, err, *built)
	}
}

// TestADirtyTreeNamesAtMostTwentyPaths_TS_09_11 verifies TS-09-11 at the function.
// Verifies: 09-REQ-2.3
func TestADirtyTreeNamesAtMostTwentyPaths_TS_09_11(t *testing.T) {
	r := newTestRepo(t, gateFiles, false)
	p, _ := params(r)
	for i := 1; i <= 25; i++ {
		if err := os.WriteFile(filepath.Join(r.Dir, fmt.Sprintf("f%02d.txt", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(r.Dir, "src", "greet.sh"), []byte("echo changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Dir, ".gitignore"), []byte("ignored.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := preflightAll(context.Background(), p)

	f := failure(t, err)
	if f.Category != toolio.CategoryPreflight || f.Retryable {
		t.Fatalf("failure = %+v", f)
	}
	// 27 paths are dirty: the 25 files, the change to the script and the new .gitignore.
	// The first 20 in the order git lists them are named, and the others counted.
	all := []string{"src/greet.sh", ".gitignore"}
	for i := 1; i <= 25; i++ {
		all = append(all, fmt.Sprintf("f%02d.txt", i))
	}
	named := 0
	for _, path := range all {
		if strings.Contains(f.Message, path) {
			named++
		}
	}
	if strings.Contains(f.Message, "?? ") || strings.Contains(f.Message, " M ") {
		t.Errorf("message = %q, want the paths, and not the status codes git lists them with", f.Message)
	}
	if named != 20 || !strings.Contains(f.Message, "f18.txt") || strings.Contains(f.Message, "f19.txt") || !strings.Contains(f.Message, "and 7 more") {
		t.Errorf("%d paths are named (f18 yes, f19 no, and 7 more): %s", named, f.Message)
	}

	// An ignored file alone does not make the tree dirty.
	clean := newTestRepo(t, gateFiles, false)
	if err := os.WriteFile(filepath.Join(clean.Dir, ".gitignore"), []byte("ignored.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDo(t, clean.Dir, "add", "-A")
	gitDo(t, clean.Dir, "commit", "-q", "-m", "chore: ignore")
	if err := os.WriteFile(filepath.Join(clean.Dir, "ignored.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cp, _ := params(clean)
	requireMake(t)
	if _, err := preflightAll(context.Background(), cp); err != nil {
		t.Errorf("an ignored file made the tree dirty: %v", err)
	}
}

// redGate is the fixture gate with a script that fails the test at HEAD.
func redGate() map[string]string {
	files := map[string]string{}
	for k, v := range gateFiles {
		files[k] = v
	}
	files["src/greet.sh"] = "echo helo\n"
	return files
}

// gateSpy counts the runs of the baseline and keeps their options.
type gateSpy struct{ options []checks.Options }

func (g *gateSpy) run(ctx context.Context, dir string, o checks.Options) (checks.Result, error) {
	g.options = append(g.options, o)
	return checks.Run(ctx, dir, o)
}

// TestThePreflightRecordsTheBaseAndRunsTheBaselineOnce_TS_09_16 verifies TS-09-16 at
// the function: the full hash of the base, the pinned table, one run of the gate in
// the default mode with no label, and the warning of a red baseline.
// Verifies: 09-REQ-2.7
func TestThePreflightRecordsTheBaseAndRunsTheBaselineOnce_TS_09_16(t *testing.T) {
	requireMake(t)
	r := newTestRepo(t, redGate(), false)
	p, _ := params(r)
	spy := &gateSpy{}
	p.Gate = spy.run
	var warned []string
	p.Warn = func(code, _ string) { warned = append(warned, code) }

	res, err := preflightAll(context.Background(), p)

	if err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(gitDo(t, r.Dir, "rev-parse", "HEAD"))
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(res.BaseHead) || res.BaseHead != head {
		t.Errorf("BaseHead = %q, want the full hash %s", res.BaseHead, head)
	}
	if len(spy.options) != 1 {
		t.Fatalf("the baseline ran %d times, want once", len(spy.options))
	}
	if o := spy.options[0]; o.Label != "" || o.Hermetic || o.NoCache || !reflect.DeepEqual(o.Table, res.Detection.Gate) || len(o.Table.Checks) == 0 {
		t.Errorf("options = %+v, want the pinned table in the default mode with no label", o)
	}
	if res.Baseline == nil || res.Baseline.Passed || res.Baseline.ID == "" {
		t.Errorf("Baseline = %+v, want a red gate result with an id", res.Baseline)
	}
	if !slices.Equal(warned, []string{WarnBaselineFailing}) {
		t.Errorf("warnings = %v, want baseline_failing alone", warned)
	}

	// A green baseline raises nothing.
	green := newTestRepo(t, gateFiles, false)
	gp, _ := params(green)
	gp.Gate, gp.Warn = spy.run, func(code, _ string) { t.Errorf("warning %s on a green baseline", code) }
	if res, err := preflightAll(context.Background(), gp); err != nil || res.Baseline == nil || !res.Baseline.Passed {
		t.Errorf("green: %+v, %v, want a passed baseline", res.Baseline, err)
	}
}

// TestNoVerifyRunsNoBaselineAndALaterFailureRunsNone_TS_09_16 guards the order of
// 09-REQ-2.7: the baseline comes after every check, the brain's included, so a run
// that is refused spends no gate, and --no-verify spends none either.
func TestNoVerifyRunsNoBaselineAndALaterFailureRunsNone(t *testing.T) {
	r := newTestRepo(t, redGate(), false)
	p, _ := params(r)
	spy := &gateSpy{}
	p.Gate = spy.run

	p.NewBrain = func() (brain.Brain, error) {
		return nil, &toolio.Failure{Category: toolio.CategoryAuth, Message: "no key"}
	}
	if _, err := preflightAll(context.Background(), p); failure(t, err).Category != toolio.CategoryAuth || len(spy.options) != 0 {
		t.Errorf("a brain that cannot be made: %v, %d baseline runs, want the failure and none", err, len(spy.options))
	}

	p, _ = params(r)
	p.Gate, p.NoVerify = spy.run, true
	p.Warn = func(code, _ string) { t.Errorf("warning %s under --no-verify", code) }
	if res, err := preflightAll(context.Background(), p); err != nil || res.Baseline != nil || len(spy.options) != 0 || len(res.BaseHead) != 40 {
		t.Errorf("--no-verify: %+v, %v, %d baseline runs, want no baseline and the base recorded", res.Baseline, err, len(spy.options))
	}

	// A gate that fails to run is a failure of the run, and a stopped one is the stop.
	p, _ = params(r)
	p.Gate = func(context.Context, string, checks.Options) (checks.Result, error) {
		return checks.Result{}, errors.New("disk full")
	}
	if _, err := preflightAll(context.Background(), p); failure(t, err).Category != toolio.CategoryIO || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("a gate that cannot run: %v, want an io failure", err)
	}
	p.Gate = func(context.Context, string, checks.Options) (checks.Result, error) {
		return checks.Result{}, &checks.UsageError{Message: "label not allowed"}
	}
	if _, err := preflightAll(context.Background(), p); failure(t, err).Category != toolio.CategoryUsage || !strings.Contains(err.Error(), "label not allowed") {
		t.Errorf("a gate that is refused its options: %v, want a usage failure", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Gate = func(ctx context.Context, _ string, _ checks.Options) (checks.Result, error) {
		cancel() // the run is stopped while the gate is running
		return checks.Result{}, errors.New("killed")
	}
	if _, err := preflightAll(ctx, p); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled run: %v, want the cancellation", err)
	}
}

// TestACommandThatLandsAndRunsNoBrainSkipsThoseChecks guards fix brief: no landing
// and no brain make the preflight of the work tree, the gate and the baseline.
func TestACommandThatLandsAndRunsNoBrainSkipsThoseChecks(t *testing.T) {
	requireMake(t)
	r := newTestRepo(t, gateFiles, false)
	p, _ := params(r)
	p.Land, p.NewBrain = "", nil
	p.Gate = (&gateSpy{}).run

	res, err := preflightAll(context.Background(), p)

	if err != nil || res.Brain != nil || res.Baseline == nil {
		t.Errorf("Preflight = %+v, %v, want the baseline and no brain, though there is no origin", res, err)
	}
}

// preflightAll is the whole of the checks before a run: those of the host, then the gate and
// the baseline.
func preflightAll(ctx context.Context, p PreflightParams) (Preflighted, error) {
	pre, err := Preflight(ctx, p)
	if err != nil {
		return pre, err
	}
	return pre, pre.Measure(ctx, p)
}
