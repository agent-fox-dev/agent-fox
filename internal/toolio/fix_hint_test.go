package toolio_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/anthropic"
)

type totalBudgetErrHolder struct {
	total float64
}

func (t totalBudgetErrHolder) Error() string           { return "budget exceeded" }
func (t totalBudgetErrHolder) TotalBudgetUSD() float64 { return t.total }

// TS-05-27 (unit): fix_hint for the budget and max_turns categories names the flag, the resolved current bound, and a suggestion double it
func TestTS05_27_FixHintBudgetAndMaxTurns(t *testing.T) {
	bounds := agentrun.Bounds{MaxBudgetUSD: 2.0, MaxTurns: 20}

	hintB := toolio.FixHintFor("budget", "implement", bounds, nil)
	if hintB == nil {
		t.Fatal("expected non-nil FixHint for budget")
	}
	wantB := toolio.FixHint{Flag: "--budget", Current: 2.0, Suggest: 4.0}
	if !reflect.DeepEqual(*hintB, wantB) {
		t.Errorf("hintB = %+v, want %+v", *hintB, wantB)
	}

	hintT := toolio.FixHintFor("max_turns", "implement", bounds, nil)
	if hintT == nil {
		t.Fatal("expected non-nil FixHint for max_turns")
	}
	wantT := toolio.FixHint{Flag: "--max-turns", Current: 20.0, Suggest: 40.0}
	if !reflect.DeepEqual(*hintT, wantT) {
		t.Errorf("hintT = %+v, want %+v", *hintT, wantT)
	}
}

// TS-05-28 (unit): fix_hint for the no_result category takes the shape of whichever bound the phase actually stopped at
func TestTS05_28_FixHintNoResult(t *testing.T) {
	bounds := agentrun.Bounds{MaxBudgetUSD: 2.0, MaxTurns: 20}

	errBudget := agentrun.NoResultError("implement", "submit", agentrun.Result{StopReason: agentrun.RunStopBudgetExceeded})
	errTurns := agentrun.NoResultError("implement", "submit", agentrun.Result{StopReason: agentrun.RunStopMaxTurns})

	hintBudget := toolio.FixHintFor("no_result", "implement", bounds, errBudget)
	if hintBudget == nil {
		t.Fatal("expected non-nil FixHint for no_result (budget)")
	}
	if hintBudget.Flag != "--budget" {
		t.Errorf("hintBudget.Flag = %q, want %q", hintBudget.Flag, "--budget")
	}

	hintTurns := toolio.FixHintFor("no_result", "implement", bounds, errTurns)
	if hintTurns == nil {
		t.Fatal("expected non-nil FixHint for no_result (max_turns)")
	}
	if hintTurns.Flag != "--max-turns" {
		t.Errorf("hintTurns.Flag = %q, want %q", hintTurns.Flag, "--max-turns")
	}
}

// TS-05-29 (unit): fix_hint for the auth category names the vendor's credential variables, or the forge token names for a forge credential
func TestTS05_29_FixHintAuth(t *testing.T) {
	bounds := agentrun.Bounds{MaxBudgetUSD: 2.0, MaxTurns: 20}

	t.Setenv("ANTHROPIC_API_KEY", "")
	anthropicAuthErr := agentrun.CheckCredentials(&core.Model{Provider: "anthropic", API: anthropic.API})
	if anthropicAuthErr == nil {
		anthropicAuthErr = errors.New("no credential for vendor \"anthropic\": set one of ANTHROPIC_API_KEY")
	}
	githubForgeAuthErr := errors.New("github: missing authentication token")

	modelHint := toolio.FixHintFor("auth", "preflight", bounds, anthropicAuthErr)
	if modelHint == nil {
		t.Fatal("expected non-nil FixHint for model auth")
	}
	if !slices.Contains(modelHint.Env, "ANTHROPIC_API_KEY") {
		t.Errorf("modelHint.Env = %v, want it to contain ANTHROPIC_API_KEY", modelHint.Env)
	}

	forgeHint := toolio.FixHintFor("auth", "land", bounds, githubForgeAuthErr)
	if forgeHint == nil {
		t.Fatal("expected non-nil FixHint for forge auth")
	}
	wantForgeEnv := []string{"GITHUB_TOKEN", "GH_TOKEN"}
	if !reflect.DeepEqual(forgeHint.Env, wantForgeEnv) {
		t.Errorf("forgeHint.Env = %v, want %v", forgeHint.Env, wantForgeEnv)
	}
}

// TS-05-30 (unit): fix_hint for the model category is the static tier list, regardless of what model spec was asked for
func TestTS05_30_FixHintModel(t *testing.T) {
	bounds := agentrun.Bounds{MaxBudgetUSD: 2.0, MaxTurns: 20}
	unresolvableModelErr := errors.New("unknown model spec: nonexistent")

	hint := toolio.FixHintFor("model", "preflight", bounds, unresolvableModelErr)
	if hint == nil {
		t.Fatal("expected non-nil FixHint for model")
	}
	if hint.Flag != "--model" {
		t.Errorf("hint.Flag = %q, want %q", hint.Flag, "--model")
	}
	wantValid := []string{"SIMPLE", "STANDARD", "ADVANCED"}
	if !reflect.DeepEqual(hint.Valid, wantValid) {
		t.Errorf("hint.Valid = %v, want %v", hint.Valid, wantValid)
	}
}

// TS-05-31 (unit): fix_hint for a usage error naming exactly one culprit flag is present; one naming none or several is omitted
func TestTS05_31_FixHintUsage(t *testing.T) {
	bounds := agentrun.Bounds{MaxBudgetUSD: 2.0, MaxTurns: 20}

	h1 := toolio.FixHintFor("usage", "usage", bounds, errors.New("--overwrite cannot be combined with --repo or --label"))
	if h1 == nil {
		t.Fatal("expected non-nil FixHint for overwrite usage error")
	}
	if h1.Flag != "--overwrite" {
		t.Errorf("h1.Flag = %q, want %q", h1.Flag, "--overwrite")
	}

	h2 := toolio.FixHintFor("usage", "usage", bounds, errors.New("--no-verify cannot be combined with --verify"))
	if h2 == nil {
		t.Fatal("expected non-nil FixHint for no-verify usage error")
	}
	if h2.Flag != "--no-verify" {
		t.Errorf("h2.Flag = %q, want %q", h2.Flag, "--no-verify")
	}

	h3 := toolio.FixHintFor("usage", "usage", bounds, errors.New("expected one input, got 2"))
	if h3 != nil {
		t.Errorf("h3 = %+v, want nil", h3)
	}
}

// TS-05-32 (unit): A budget-category error whose stage is literally "budget" gets a --total-budget fix_hint instead of the per-phase --budget one
func TestTS05_32_FixHintTotalBudget(t *testing.T) {
	bounds := agentrun.Bounds{MaxBudgetUSD: 2.0, MaxTurns: 20}
	totalBudgetErr := totalBudgetErrHolder{total: 10.0}

	h1 := toolio.FixHintFor("budget", "budget", bounds, totalBudgetErr)
	if h1 == nil {
		t.Fatal("expected non-nil FixHint for total budget error")
	}
	if h1.Flag != "--total-budget" || h1.Current != 10.0 || h1.Suggest != 20.0 {
		t.Errorf("h1 = %+v, want Flag: --total-budget, Current: 10.0, Suggest: 20.0", h1)
	}

	h2 := toolio.FixHintFor("budget", "implement", bounds, nil)
	if h2 == nil {
		t.Fatal("expected non-nil FixHint for per-phase budget error")
	}
	if h2.Flag != "--budget" || h2.Current != 2.0 || h2.Suggest != 4.0 {
		t.Errorf("h2 = %+v, want Flag: --budget, Current: 2.0, Suggest: 4.0", h2)
	}
}

func TestEnvelopeAttachesFixHint(t *testing.T) {
	r := toolio.NewRun("fix", "v1")
	r.SetBounds(agentrun.Bounds{MaxBudgetUSD: 3.5, MaxTurns: 40})

	env := r.Envelope(toolio.ExitFailed, nil, &toolio.ErrorInfo{
		Stage:    "implement",
		Category: "budget",
		Message:  "budget exceeded",
	})
	if env.Error == nil {
		t.Fatal("expected non-nil Error")
	}
	if env.Error.FixHint == nil {
		t.Fatal("expected non-nil FixHint on envelope error")
	}
	want := toolio.FixHint{Flag: "--budget", Current: 3.5, Suggest: 7.0}
	if !reflect.DeepEqual(*env.Error.FixHint, want) {
		t.Errorf("FixHint = %+v, want %+v", *env.Error.FixHint, want)
	}
}
