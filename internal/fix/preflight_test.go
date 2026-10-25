package fix

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// countingBrain is a NewBrain that counts how many brains it made.
func countingBrain(made *int) func() (brain.Brain, error) {
	return func() (brain.Brain, error) { *made++; return nil, nil }
}

// The checks of the host come in order and the first that fails stops the run; the brain,
// which needs a credential, is made last of them, and the gate, which is the repository's, is
// checked after them, where it runs.
func TestPreflight_StopsAtTheFirstCheckThatFails(t *testing.T) {
	r := newRepo(t, nil, false)
	made := 0
	p := preflightParams(r)
	p.Land, p.NewBrain = LandBranch, countingBrain(&made)
	testutil.WriteFile(t, r.Dir, "dirty.txt", "x")
	ctx := context.Background()

	r.git(t, "checkout", "-q", "--detach")
	if _, err := preflightAll(ctx, p); failure(t, err).Category != toolio.CategoryUsage || !strings.Contains(err.Error(), "detached") {
		t.Errorf("detached, dirty, no origin, no gate: %v, want the branch", err)
	}
	r.git(t, "checkout", "-q", "main")
	if _, err := preflightAll(ctx, p); failure(t, err).Category != toolio.CategoryPreflight || !strings.Contains(err.Error(), "dirty.txt") {
		t.Errorf("dirty, no origin, no gate: %v, want the dirty tree", err)
	}
	r.git(t, "clean", "-fq")
	if _, err := preflightAll(ctx, p); failure(t, err).Category != toolio.CategoryPreflight || !strings.Contains(err.Error(), "no origin") || made != 0 {
		t.Errorf("no origin, no gate: %v (brain made %d times), want the origin and no brain", err, made)
	}
	p.Land = LandNone
	if _, err := preflightAll(ctx, p); failure(t, err).FixHint == nil || failure(t, err).FixHint.Flag != "--no-verify" || made != 1 {
		t.Errorf("no gate: %v (brain made %d times), want the gate, checked once the brain was made", err, made)
	}
	p.NoVerify = true
	if pre, err := preflightAll(ctx, p); err != nil || pre.Base != "main" || pre.BaseHead != r.git(t, "rev-parse", "HEAD") || pre.Git == nil || pre.Baseline != nil {
		t.Errorf("--no-verify: %+v, %v, want the base recorded and no baseline", pre, err)
	}
}

func TestPreflight_TheDirectoryMustBeTheTopLevelOfAWorkTreeWithACommit(t *testing.T) {
	r := newRepo(t, map[string]string{"sub/a.txt": "a\n"}, false)
	empty := t.TempDir()
	testutil.Git(t, empty, "init", "-q")
	for name, tc := range map[string]struct{ dir, says string }{
		"no work tree":   {t.TempDir(), "not inside a git work tree"},
		"a subdirectory": {filepath.Join(r.Dir, "sub"), "not the top level"},
		"no commit":      {empty, "no commit to fix from"},
	} {
		_, err := Preflight(context.Background(), PreflightParams{Dir: tc.dir})
		if f := failure(t, err); f.Category != toolio.CategoryUsage || !strings.Contains(f.Message, tc.says) {
			t.Errorf("%s: %v, want usage saying %q", name, err, tc.says)
		}
	}
	if _, err := Preflight(context.Background(), PreflightParams{Dir: r.Dir, Base: "release"}); err == nil || !strings.Contains(err.Error(), "check out release first") {
		t.Errorf("another base: %v, want it to be checked out first", err)
	}
}

func TestPreflight_ADirtyTreeNamesTwentyPathsAndCountsTheRest(t *testing.T) {
	r := newRepo(t, map[string]string{".gitignore": "ignored.log\n", "src/greet.sh": "echo hello\n"}, false)
	testutil.WriteFile(t, r.Dir, "ignored.log", "x")
	if _, err := Preflight(context.Background(), preflightParams(r)); err != nil {
		t.Fatalf("an ignored file made the tree dirty: %v", err)
	}
	for i := 1; i <= 25; i++ {
		testutil.WriteFile(t, r.Dir, fmt.Sprintf("f%02d.txt", i), "x")
	}
	testutil.WriteFile(t, r.Dir, "src/greet.sh", "echo changed\n")

	_, err := Preflight(context.Background(), preflightParams(r))

	// git lists the change to the script first, then the 25 new files.
	f := failure(t, err)
	if f.Category != toolio.CategoryPreflight || !strings.Contains(f.Message, "src/greet.sh, f01.txt") || !strings.Contains(f.Message, "f19.txt, and 6 more") || strings.Contains(f.Message, "f20.txt") || strings.Contains(f.Message, "?? ") {
		t.Errorf("message = %q, want the paths of the first 20 without their status, and the 6 more counted", f.Message)
	}
}

func TestPreflightMeasure_PinsTheTableAndRunsTheBaselineOnce(t *testing.T) {
	requireMake(t)
	red := gateFiles()
	red["src/greet.sh"] = "echo helo\n"
	for name, tc := range map[string]struct {
		files map[string]string
		warn  []string
	}{"a red baseline": {red, []string{WarnBaselineFailing}}, "a green baseline": {gateFiles(), nil}} {
		t.Run(name, func(t *testing.T) {
			r := newRepo(t, tc.files, false)
			p := preflightParams(r)
			var runs []checks.Options
			var warned []string
			p.Gate = func(ctx context.Context, dir string, o checks.Options) (checks.Result, error) {
				runs = append(runs, o)
				return checks.Run(ctx, dir, o)
			}
			p.Warn = func(code, _ string) { warned = append(warned, code) }

			pre, err := preflightAll(context.Background(), p)

			if err != nil || len(runs) != 1 || !reflect.DeepEqual(runs[0], checks.Options{Table: pre.Detection.Gate}) || len(pre.Detection.Gate.Checks) == 0 {
				t.Fatalf("Preflight = %+v, %v, the gate ran with %+v, want once, with the pinned table and nothing else", pre, err, runs)
			}
			if pre.Baseline == nil || pre.Baseline.ID == "" || pre.Baseline.Passed != (tc.warn == nil) || !reflect.DeepEqual(warned, tc.warn) || len(pre.Files) == 0 {
				t.Errorf("baseline %+v, warnings %v, want %v", pre.Baseline, warned, tc.warn)
			}
		})
	}
}

func TestPreflightMeasure_FailsWithWhatTheGateOrTheDetectionSays(t *testing.T) {
	r := newRepo(t, nil, false)
	gate := func(err error) func(context.Context, string, checks.Options) (checks.Result, error) {
		return func(context.Context, string, checks.Options) (checks.Result, error) { return checks.Result{}, err }
	}
	available := func(context.Context, string, []string) (project.Detection, error) {
		return project.Detection{Gate: project.Table{Checks: []project.Check{{Name: "c", Argv: []string{"true"}, Available: true}}}}, nil
	}
	detect := func(err error) func(context.Context, string, []string) (project.Detection, error) {
		return func(context.Context, string, []string) (project.Detection, error) { return project.Detection{}, err }
	}
	sandbox := &toolio.Failure{Category: toolio.CategorySandbox, Retryable: true, Message: "the box is gone"}
	for name, tc := range map[string]struct {
		p        PreflightParams
		category string
	}{
		"a gate that cannot run":        {PreflightParams{Detect: available, Gate: gate(errors.New("disk full"))}, toolio.CategoryIO},
		"a gate refused its options":    {PreflightParams{Detect: available, Gate: gate(&checks.UsageError{Message: "label not allowed"})}, toolio.CategoryUsage},
		"a gate whose box is gone":      {PreflightParams{Detect: available, Gate: gate(sandbox)}, toolio.CategorySandbox},
		"a detection whose box is gone": {PreflightParams{Detect: detect(sandbox)}, toolio.CategorySandbox},
		"a gate.json that is wrong":     {PreflightParams{Detect: detect(fmt.Errorf("bad: %w", project.ErrGateConfig))}, toolio.CategoryConfig},
		"a detection that breaks":       {PreflightParams{Detect: detect(errors.New("boom"))}, toolio.CategoryInternal},
	} {
		tc.p.Dir, tc.p.Warn = r.Dir, func(string, string) {}
		pre := &Preflighted{}
		if f := failure(t, pre.Measure(context.Background(), tc.p)); f.Category != tc.category || (tc.category == toolio.CategorySandbox && !f.Retryable) {
			t.Errorf("%s: %+v, want %s as the box marked it", name, f, tc.category)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopping := func(context.Context, string, checks.Options) (checks.Result, error) {
		cancel()
		return checks.Result{}, errors.New("killed")
	}
	if err := (&Preflighted{}).Measure(ctx, PreflightParams{Dir: r.Dir, Detect: available, Gate: stopping}); !errors.Is(err, context.Canceled) {
		t.Errorf("a run stopped in the baseline: %v, want the stop", err)
	}
}

// A brain that cannot be made is refused before the baseline, and a command that lands
// nothing and runs no brain makes neither check.
func TestPreflight_TheLandingAndTheBrainAreCheckedOnlyWhenTheCommandHasThem(t *testing.T) {
	r := newRepo(t, nil, false)
	p := preflightParams(r)
	p.NewBrain = func() (brain.Brain, error) {
		return nil, &toolio.Failure{Category: toolio.CategoryAuth, Message: "no key"}
	}
	if _, err := Preflight(context.Background(), p); failure(t, err).Category != toolio.CategoryAuth {
		t.Errorf("a brain that cannot be made: %v", err)
	}
	p.Land, p.NewBrain = "", nil
	if pre, err := Preflight(context.Background(), p); err != nil || pre.Brain != nil {
		t.Errorf("no landing and no brain: %+v, %v, want it to pass with no origin and no brain", pre, err)
	}
}

func TestCheckLanding_NeedsAnOriginAndForAPullRequestARepositoryThatTakesOne(t *testing.T) {
	for _, v := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN"} {
		t.Setenv(v, "") // a client with no token takes none from the environment
	}
	withOrigin, without := newRepo(t, nil, true), newRepo(t, nil, false)
	ctx := context.Background()
	forge := func(kind string, fail *fakeforge.Failure, archived bool, token string) issuex.Client {
		f := fakeforge.New(t, kind, "o/r")
		f.Archived = archived
		if fail != nil {
			f.Fail(fakeforge.OpRepo, *fail)
		}
		c, err := f.NewForge(token)(issuex.Options{Repo: issuex.Repo{Owner: "o", Name: "r"}})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	target := issuex.Repo{Owner: "o", Name: "r", Host: "github.com"}
	for name, tc := range map[string]struct {
		r         *repo
		p         PreflightParams
		category  string // "" for none
		retryable bool
		env       string
	}{
		"none needs nothing":            {without, PreflightParams{Land: LandNone}, "", false, ""},
		"branch needs an origin":        {without, PreflightParams{Land: LandBranch}, toolio.CategoryPreflight, false, ""},
		"branch with an origin":         {withOrigin, PreflightParams{Land: LandBranch}, "", false, ""},
		"pr needs a repository":         {withOrigin, PreflightParams{Land: LandPR, Forge: noForge{}}, toolio.CategoryUsage, false, ""},
		"pr needs a credential":         {withOrigin, PreflightParams{Land: LandPR, Target: target, Forge: forge(fakeforge.GitHub, nil, false, "")}, toolio.CategoryAuth, false, "GITHUB_TOKEN"},
		"pr on gitlab needs its own":    {withOrigin, PreflightParams{Land: LandPR, Target: issuex.Repo{Owner: "o", Name: "r", Host: "gitlab.example.com"}, Forge: forge(fakeforge.GitLab, nil, false, "")}, toolio.CategoryAuth, false, "GITLAB_TOKEN"},
		"pr in a repository":            {withOrigin, PreflightParams{Land: LandPR, Target: target, Forge: forge(fakeforge.GitHub, nil, false, "t")}, "", false, ""},
		"pr in an archived repository":  {withOrigin, PreflightParams{Land: LandPR, Target: target, Forge: forge(fakeforge.GitHub, nil, true, "t")}, toolio.CategoryForge, false, ""},
		"pr in no repository":           {withOrigin, PreflightParams{Land: LandPR, Target: target, Forge: forge(fakeforge.GitHub, &fakeforge.Failure{Status: 404, Message: "Not Found"}, false, "t")}, toolio.CategoryForge, false, ""},
		"pr with a refused credential":  {withOrigin, PreflightParams{Land: LandPR, Target: target, Forge: forge(fakeforge.GitHub, &fakeforge.Failure{Status: 401, Message: "Bad credentials"}, false, "t")}, toolio.CategoryAuth, false, "GITHUB_TOKEN"},
		"pr on a forge that is down":    {withOrigin, PreflightParams{Land: LandPR, Target: target, Forge: forge(fakeforge.GitHub, &fakeforge.Failure{Status: 502, Message: "Bad Gateway"}, false, "t")}, toolio.CategoryForge, true, ""},
		"pr on a forge that refuses it": {withOrigin, PreflightParams{Land: LandPR, Target: target, Forge: forge(fakeforge.GitHub, &fakeforge.Failure{Status: 422, Message: "Unprocessable"}, false, "t")}, toolio.CategoryForge, false, ""},
	} {
		err := CheckLanding(ctx, tc.r.Git, tc.p)
		if tc.category == "" {
			if err != nil {
				t.Errorf("%s: %v, want it to pass", name, err)
			}
			continue
		}
		var f *toolio.Failure
		if !errors.As(err, &f) {
			t.Errorf("%s: %v, want a failure", name, err)
			continue
		}
		if f.Category != tc.category || f.Retryable != tc.retryable || (tc.env != "" && (f.FixHint == nil || f.FixHint.Env != tc.env)) {
			t.Errorf("%s: %+v (hint %+v), want %s, retryable %v, env %q", name, f, f.FixHint, tc.category, tc.retryable, tc.env)
		}
	}
}
