package issuetriage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/agentrun/gotypechecktest"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// countingForge records every write call. RunPreflight must make none.
type countingForge struct {
	issuex.NoOpClient
	authenticated bool
	creates       int
	updates       int
}

func (c *countingForge) Authenticated() bool { return c.authenticated }
func (c *countingForge) CreateIssue(context.Context, issuex.Repo, issuex.CreateIssueRequest) (issuex.Issue, error) {
	c.creates++
	return issuex.Issue{}, nil
}
func (c *countingForge) UpdateIssue(context.Context, issuex.IssueRef, issuex.UpdateIssueRequest) (issuex.Issue, error) {
	c.updates++
	return issuex.Issue{}, nil
}

// boundedRunner is a faux-provider Runner with the given ceilings, plus the
// provider so a test can count the model calls it saw.
func boundedRunner(t *testing.T, turns int, budget float64) (*agentrun.Runner, *faux.Provider) {
	t.Helper()
	p := faux.New()
	r, err := agentrun.NewRunner(agentrun.Config{
		Model:     faux.Model(),
		Providers: core.ProviderRegistry{faux.API: p.APIProvider()},
		Bounds:    agentrun.Bounds{MaxTurns: turns, MaxBudgetUSD: budget, MaxAttempts: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	return r, p
}

func findCheck(list []toolio.PreflightCheck, name string) (toolio.PreflightCheck, bool) {
	for _, c := range list {
		if c.Check == name {
			return c, true
		}
	}
	return toolio.PreflightCheck{}, false
}

// preflightOptions is a passing, writing (not --dry-run) run against an
// authenticated forge.
func preflightOptions(t *testing.T) (Options, *countingForge, *faux.Provider) {
	t.Helper()
	ws := newWorkspace(t)
	runner, p := boundedRunner(t, 6, 1.5)
	o := newOptions(t, ws, runner)
	f := &countingForge{authenticated: true}
	o.Forge = f
	o.DryRun = false
	return o, f, p
}

// TS-11-18 (unit): issuetriage.Result carries Preflight and Estimate fields
// immediately after DryRun.
//
// Verifies: 11-REQ-3.4
func TestTS11_18_ResultCarriesPreflightAndEstimateAfterDryRun(t *testing.T) {
	typ := reflect.TypeOf(Result{})
	idx := -1
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Name == "DryRun" {
			idx = i
		}
	}
	if idx < 0 || idx+2 >= typ.NumField() {
		t.Fatalf("DryRun is at %d of %d fields", idx, typ.NumField())
	}
	pf, es := typ.Field(idx+1), typ.Field(idx+2)
	if pf.Name != "Preflight" || pf.Type != reflect.TypeOf([]toolio.PreflightCheck(nil)) || pf.Tag.Get("json") != "preflight,omitempty" {
		t.Errorf("field after DryRun = %s %s %q", pf.Name, pf.Type, pf.Tag.Get("json"))
	}
	if es.Name != "Estimate" || es.Type != reflect.TypeOf((*toolio.Estimate)(nil)) || es.Tag.Get("json") != "estimate,omitempty" {
		t.Errorf("field after Preflight = %s %s %q", es.Name, es.Type, es.Tag.Get("json"))
	}
	for _, f := range []reflect.StructField{pf, es} {
		if f.Tag.Get("description") == "" {
			t.Errorf("%s has no description tag", f.Name)
		}
	}
}

// TS-11-29 (integration): RunPreflight on full success reports the resolved
// target and the credential, and creates no issue.
//
// Verifies: 11-REQ-5.4
func TestTS11_29_RunPreflightReportsTargetAndCreatesNoIssue(t *testing.T) {
	o, forge, _ := preflightOptions(t)

	res, err := RunPreflight(o)
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	if res.Stage != "preflight" {
		t.Errorf("Stage = %q", res.Stage)
	}
	if c, ok := findCheck(res.Preflight, "target_repository"); !ok || !c.OK || c.Detail != o.Repo.String() {
		t.Errorf("target_repository = %+v (present %v), want detail %q", c, ok, o.Repo.String())
	}
	if c, ok := findCheck(res.Preflight, "forge_credential"); !ok || !c.OK {
		t.Errorf("forge_credential = %+v (present %v)", c, ok)
	}
	if forge.creates != 0 || forge.updates != 0 {
		t.Errorf("the forge saw %d creates, %d updates", forge.creates, forge.updates)
	}
	if res.Action != "" || res.URL != "" || res.Number != 0 {
		t.Errorf("a preflight run reports a write: %+v", res)
	}
}

// Under --dry-run no credential is needed, and with no target resolvable the
// entry says so.
func TestRunPreflightDryRunWithNoTarget(t *testing.T) {
	o, _, _ := preflightOptions(t)
	o.DryRun = true
	o.Repo = issuex.Repo{}

	res, err := RunPreflight(o)
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	if c, ok := findCheck(res.Preflight, "target_repository"); !ok || !c.OK || c.Detail != "none (dry run)" {
		t.Errorf("target_repository = %+v (present %v)", c, ok)
	}
	if _, ok := findCheck(res.Preflight, "forge_credential"); ok {
		t.Error("forge_credential is present under --dry-run")
	}
	if !res.DryRun {
		t.Error("DryRun was not echoed")
	}
}

// A refusal is Preflight's failure, verbatim, with no result.
func TestRunPreflightRefusalIsPreflightsFailure(t *testing.T) {
	o, _, _ := preflightOptions(t)
	o.Forge = &countingForge{authenticated: false}

	_, want := Preflight(o)
	res, err := RunPreflight(o)
	var got *Failure
	if want == nil || !errors.As(err, &got) {
		t.Fatalf("err = %v, want a *Failure", err)
	}
	if got.Stage != want.Stage || got.Category != want.Category || got.Error() != want.Error() {
		t.Errorf("failure = %+v, want %+v", got, want)
	}
	if got.Category != "auth" {
		t.Errorf("category = %q, want auth", got.Category)
	}
	if res != nil {
		t.Errorf("a refusal carries a result: %+v", res)
	}

	// And exactly Run's own failure, for the same options.
	_, runErr := Run(context.Background(), o)
	var runFail *Failure
	if !errors.As(runErr, &runFail) || runFail.Stage != got.Stage || runFail.Category != got.Category || runFail.Error() != got.Error() {
		t.Errorf("Run failed with %v, RunPreflight with %v", runErr, err)
	}
}

func TestPreflightOrderIsTargetThenCredential(t *testing.T) {
	// No target and no credential: the target refuses first, as in Run.
	o, _, _ := preflightOptions(t)
	o.Repo = issuex.Repo{}
	o.Forge = &countingForge{authenticated: false}
	_, f := Preflight(o)
	if f == nil || f.Category != "usage" {
		t.Fatalf("Preflight = %v, want the usage refusal for the missing target", f)
	}

	target, f := Preflight(func() Options { o, _, _ := preflightOptions(t); return o }())
	if f != nil || !target.Valid() {
		t.Errorf("Preflight = %v, %v on a passing run", target, f)
	}
}

func TestRunPreflightRequiresWorkspaceAndRunner(t *testing.T) {
	o, _, _ := preflightOptions(t)
	o.Runner = nil
	if _, err := RunPreflight(o); err == nil {
		t.Error("no runner: want a refusal")
	}
	o, _, _ = preflightOptions(t)
	o.Workspace = nil
	if _, err := RunPreflight(o); err == nil {
		t.Error("no workspace: want a refusal")
	}
}

// TS-11-33 (unit): issue's estimate is exactly one phase, with the bounds
// from ResolvedBounds.
//
// Verifies: 11-REQ-5.5
func TestTS11_33_EstimateIsOnePhaseFromResolvedBounds(t *testing.T) {
	o, _, _ := preflightOptions(t) // ResolvedBounds are (6, 1.5)
	if turns, budget := o.Runner.ResolvedBounds(); turns != 6 || budget != 1.5 {
		t.Fatalf("ResolvedBounds = (%d, %v)", turns, budget)
	}
	res, err := RunPreflight(o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Estimate == nil {
		t.Fatal("no estimate")
	}
	if res.Estimate.Phases != 1 {
		t.Errorf("Phases = %d, want 1", res.Estimate.Phases)
	}
	if res.Estimate.MaxTurnsPerPhase != 6 || res.Estimate.MaxBudgetPerPhaseUSD != 1.5 {
		t.Errorf("bounds = %+v, want 6 turns and $1.5", res.Estimate)
	}
	if res.Estimate.MaxTotalUSD != res.Estimate.MaxBudgetPerPhaseUSD {
		t.Errorf("MaxTotalUSD = %v, want %v", res.Estimate.MaxTotalUSD, res.Estimate.MaxBudgetPerPhaseUSD)
	}
}

// TS-11-35 (unit, issue's share): RunPreflight never asks the model for
// anything, so no phase is recorded and the envelope carries no usage.
//
// Verifies: 11-REQ-5.7
func TestTS11_35_RunPreflightNeverCallsTheRunner(t *testing.T) {
	o, _, provider := preflightOptions(t)

	res, err := RunPreflight(o)
	if err != nil {
		t.Fatal(err)
	}
	if n := provider.Calls(); n != 0 {
		t.Errorf("the provider saw %d model calls", n)
	}
	if env := o.Run.Envelope(0, res, nil); env.Usage != nil {
		t.Errorf("a phase was recorded: %+v", env.Usage)
	}
}

// TS-11-39 (unit): Summary() reports the checklist count when Stage is
// preflight and the checks passed.
//
// Verifies: 11-REQ-6.3
func TestTS11_39_SummaryReportsChecklistCount(t *testing.T) {
	r := Result{Stage: "preflight", Preflight: make([]toolio.PreflightCheck, 2)}
	if got, want := r.Summary(), "triage: preflight passed (2 checks)"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
	// An ordinary run's summary is unchanged.
	if got := (Result{Action: "created", Repo: "a/b", Number: 3, Severity: "high"}).Summary(); got != "triage: created a/b#3 (high severity, 0 files cited)" {
		t.Errorf("ordinary Summary() = %q", got)
	}
}

// TS-11-40 (unit, issue's share): Resumable() is false under a preflight
// stage.
//
// Verifies: 11-REQ-6.4
func TestTS11_40_ResumableIsFalseUnderPreflight(t *testing.T) {
	if (Result{Stage: "preflight"}).Resumable() {
		t.Error("a preflight result is resumable")
	}
}

// The default (summary) view keeps the stage, the checklist and the estimate.
func TestSummaryViewKeepsPreflightAndEstimate(t *testing.T) {
	r := &Result{Stage: "preflight", Preflight: []toolio.PreflightCheck{{Check: "target_repository", OK: true}},
		Estimate: &toolio.Estimate{Phases: 1}}
	v, ok := r.SummaryView().(summaryResult)
	if !ok {
		t.Fatalf("SummaryView is %T", r.SummaryView())
	}
	if v.Stage != "preflight" || len(v.Preflight) != 1 || v.Estimate == nil || v.Estimate.Phases != 1 {
		t.Errorf("view = %+v", v)
	}
}

// TS-15-9 (unit): RunPreflight adds a symbol_backend check, OK true, with the
// detail "ctags" or "heuristics".
//
// Verifies: 15-REQ-4.1, 15-REQ-4.2
func TestTS15_9_PreflightReportsSymbolBackend(t *testing.T) {
	o, _, _ := preflightOptions(t)
	res, err := RunPreflight(o)
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	c, ok := findCheck(res.Preflight, "symbol_backend")
	if !ok {
		t.Fatalf("no symbol_backend check in %+v", res.Preflight)
	}
	if !c.OK || (c.Detail != "ctags" && c.Detail != "heuristics") {
		t.Errorf("symbol_backend = %+v, want OK with ctags or heuristics", c)
	}
}

// TS-15-10 (unit): a failing detection omits the check and leaves the rest.
//
// Verifies: 15-REQ-4.3
func TestTS15_10_PreflightOmitsSymbolBackendWhenDetectionFails(t *testing.T) {
	orig := agentrun.DetectSymbolBackend
	agentrun.DetectSymbolBackend = func(*tools.Workspace) (string, error) {
		return "", errors.New("detection failed")
	}
	t.Cleanup(func() { agentrun.DetectSymbolBackend = orig })

	o, _, _ := preflightOptions(t)
	res, err := RunPreflight(o)
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	if _, ok := findCheck(res.Preflight, "symbol_backend"); ok {
		t.Errorf("symbol_backend present after a failed detection: %+v", res.Preflight)
	}
	if _, ok := findCheck(res.Preflight, "target_repository"); !ok {
		t.Errorf("other checks missing: %+v", res.Preflight)
	}
}

// TS-17-31 (unit): triage's RunPreflight puts go_typecheck between
// symbol_backend and code_search_index with ok true, and omits it without
// touching any other check when detection fails.
//
// Verifies: 17-REQ-4.1, 17-REQ-4.5
func TestTS17_31_TriagePreflightReportsGoTypecheck(t *testing.T) {
	inject := func(detail string, err error) {
		orig := agentrun.DetectGoTypecheck
		agentrun.DetectGoTypecheck = func(*tools.Workspace) (string, error) {
			return detail, err
		}
		t.Cleanup(func() { agentrun.DetectGoTypecheck = orig })
	}

	checkNames := func(list []toolio.PreflightCheck) []string {
		names := make([]string, len(list))
		for i, c := range list {
			names[i] = c.Check
		}
		return names
	}

	strip := func(list []toolio.PreflightCheck) []toolio.PreflightCheck {
		var out []toolio.PreflightCheck
		for _, c := range list {
			if c.Check != "go_typecheck" {
				out = append(out, c)
			}
		}
		return out
	}

	// Subtest 1: normal detail.
	t.Run("normal", func(t *testing.T) {
		inject("12 packages checked, 0 errors", nil)
		o, _, _ := preflightOptions(t)
		res, err := RunPreflight(o)
		if err != nil {
			t.Fatalf("RunPreflight: %v", err)
		}
		names := checkNames(res.Preflight)
		n := len(names)
		if n < 3 || names[n-3] != "symbol_backend" || names[n-2] != "go_typecheck" || names[n-1] != "code_search_index" {
			t.Fatalf("last 3 checks = %v, want [symbol_backend go_typecheck code_search_index]", names)
		}
		c, ok := findCheck(res.Preflight, "go_typecheck")
		if !ok || !c.OK || c.Detail != "12 packages checked, 0 errors" {
			t.Errorf("go_typecheck = %+v, want OK with detail %q", c, "12 packages checked, 0 errors")
		}
		// target_repository and forge_credential come first.
		if names[0] != "target_repository" {
			t.Errorf("first check = %q, want target_repository", names[0])
		}
	})

	// Subtest 2: partial detail.
	t.Run("partial", func(t *testing.T) {
		inject("3 packages checked, 41 errors, partial", nil)
		o, _, _ := preflightOptions(t)
		res, err := RunPreflight(o)
		if err != nil {
			t.Fatalf("RunPreflight: %v", err)
		}
		c, ok := findCheck(res.Preflight, "go_typecheck")
		if !ok || !c.OK || c.Detail != "3 packages checked, 41 errors, partial" {
			t.Errorf("go_typecheck = %+v, want OK with detail %q", c, "3 packages checked, 41 errors, partial")
		}
	})

	// Subtest 3: symbol_backend failing → go_typecheck is still present,
	// last two are go_typecheck, code_search_index.
	t.Run("symbol_backend_fails", func(t *testing.T) {
		inject("12 packages checked, 0 errors", nil)
		orig := agentrun.DetectSymbolBackend
		agentrun.DetectSymbolBackend = func(*tools.Workspace) (string, error) {
			return "", errors.New("detection failed")
		}
		t.Cleanup(func() { agentrun.DetectSymbolBackend = orig })

		o, _, _ := preflightOptions(t)
		res, err := RunPreflight(o)
		if err != nil {
			t.Fatalf("RunPreflight: %v", err)
		}
		names := checkNames(res.Preflight)
		n := len(names)
		if n < 2 || names[n-2] != "go_typecheck" || names[n-1] != "code_search_index" {
			t.Fatalf("last 2 checks = %v, want [go_typecheck code_search_index]", names)
		}
		if _, ok := findCheck(res.Preflight, "symbol_backend"); ok {
			t.Error("symbol_backend should be absent")
		}
	})

	// Subtest 4: go_typecheck detection fails → omitted, other checks unchanged.
	t.Run("detection_fails", func(t *testing.T) {
		// First get a successful run for comparison.
		inject("12 packages checked, 0 errors", nil)
		o, _, _ := preflightOptions(t)
		ok, err := RunPreflight(o)
		if err != nil {
			t.Fatalf("RunPreflight (ok): %v", err)
		}

		// Now fail detection.
		inject("", errors.New("timeout"))
		o2, _, _ := preflightOptions(t)
		res, err := RunPreflight(o2)
		if err != nil {
			t.Fatalf("RunPreflight (fail): %v", err)
		}
		if _, found := findCheck(res.Preflight, "go_typecheck"); found {
			t.Error("go_typecheck should be absent when detection fails")
		}
		if !reflect.DeepEqual(strip(ok.Preflight), strip(res.Preflight)) {
			t.Errorf("other checks differ:\nok:   %+v\nfail: %+v", strip(ok.Preflight), strip(res.Preflight))
		}
		if res.Stage != ok.Stage {
			t.Errorf("stage = %q, want %q", res.Stage, ok.Stage)
		}
		if !reflect.DeepEqual(res.Estimate, ok.Estimate) {
			t.Errorf("estimate differs: %+v vs %+v", res.Estimate, ok.Estimate)
		}
	})

	// Subtest 5: DryRun mode.
	t.Run("dry_run", func(t *testing.T) {
		inject("12 packages checked, 0 errors", nil)
		o, _, _ := preflightOptions(t)
		o.DryRun = true
		res, err := RunPreflight(o)
		if err != nil {
			t.Fatalf("RunPreflight: %v", err)
		}
		c, ok := findCheck(res.Preflight, "go_typecheck")
		if !ok || !c.OK || c.Detail != "12 packages checked, 0 errors" {
			t.Errorf("go_typecheck = %+v, want OK with detail %q", c, "12 packages checked, 0 errors")
		}
	})
}

// TS-17-32 (property): For any workspace, the go_typecheck check leaves
// stage and estimate unchanged, makes no model call or forge write, and
// writes nothing to the tree.
//
// Verifies: 17-REQ-4.2, 17-REQ-4.5
func TestTS17_32_GoTypecheckIsInertProperty(t *testing.T) {
	strip := func(list []toolio.PreflightCheck) []toolio.PreflightCheck {
		var out []toolio.PreflightCheck
		for _, c := range list {
			if c.Check != "go_typecheck" {
				out = append(out, c)
			}
		}
		return out
	}

	for seed := int64(0); seed < 15; seed++ {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			root, _, snapshot := gotypechecktest.Generate(t, seed)
			before := snapshot()

			ws, err := tools.NewWorkspace(root)
			if err != nil {
				t.Fatalf("NewWorkspace: %v", err)
			}

			runner, p := boundedRunner(t, 6, 1.5)
			forge := &countingForge{authenticated: true}

			makeOpts := func() Options {
				return Options{
					Input:     toolio.Input{Kind: toolio.KindText, Origin: "argument", Body: "a report"},
					Workspace: ws,
					Repo:      issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"},
					Runner:    runner,
					Forge:     forge,
					Run:       toolio.NewRun("triage", "test"),
					Progress:  toolio.NewProgress(io.Discard, "triage", false, true),
					DryRun:    true,
				}
			}

			// Run with the real DetectGoTypecheck.
			real, e1 := RunPreflight(makeOpts())
			if e1 != nil {
				t.Fatalf("RunPreflight (real): %v", e1)
			}

			// Run with a failing DetectGoTypecheck.
			orig := agentrun.DetectGoTypecheck
			agentrun.DetectGoTypecheck = func(*tools.Workspace) (string, error) {
				return "", errors.New("injected failure")
			}
			failed, e2 := RunPreflight(makeOpts())
			agentrun.DetectGoTypecheck = orig
			if e2 != nil {
				t.Fatalf("RunPreflight (failed): %v", e2)
			}

			// Both return the same stage and estimate.
			if real.Stage != failed.Stage {
				t.Errorf("stage: %q vs %q", real.Stage, failed.Stage)
			}
			if !reflect.DeepEqual(real.Estimate, failed.Estimate) {
				t.Errorf("estimate differs: %+v vs %+v", real.Estimate, failed.Estimate)
			}

			// Entries equal except go_typecheck.
			if !reflect.DeepEqual(strip(real.Preflight), failed.Preflight) {
				t.Errorf("entries differ (stripped):\nreal:   %+v\nfailed: %+v", strip(real.Preflight), failed.Preflight)
			}

			// No model calls.
			if n := len(p.Requests()); n != 0 {
				t.Errorf("provider received %d requests, want 0", n)
			}

			// No forge writes.
			if forge.creates+forge.updates != 0 {
				t.Errorf("forge writes = %d, want 0", forge.creates+forge.updates)
			}

			// Tree unchanged.
			after := snapshot()
			if before != after {
				t.Errorf("workspace tree changed")
			}
		})
	}
}
