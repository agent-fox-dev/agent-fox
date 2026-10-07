package specgen

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/agentrun/indextest"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// preflightOptions is newOptions with a real Runner (10 turns, $2): building
// one makes no call, and RunPreflight never runs a phase, so the scripted
// author must never be asked for one.
func preflightOptions(t *testing.T) Options {
	t.Helper()
	o := newOptions(newWorkspace(t), newAuthor(t, "01", "test_feature"))
	r, err := agentrun.NewRunner(agentrun.Config{
		Model:  &core.Model{ID: "test-model", Provider: "test"},
		Bounds: agentrun.Bounds{MaxTurns: 10, MaxBudgetUSD: 2.0},
	})
	if err != nil {
		t.Fatal(err)
	}
	o.Runner = r
	return o
}

func findCheck(list []toolio.PreflightCheck, name string) (toolio.PreflightCheck, bool) {
	for _, c := range list {
		if c.Check == name {
			return c, true
		}
	}
	return toolio.PreflightCheck{}, false
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// TS-11-19 (unit): specgen.Result carries Preflight and Estimate fields
// immediately after DryRun.
//
// Verifies: 11-REQ-3.4
func TestTS11_19_ResultCarriesPreflightAndEstimateAfterDryRun(t *testing.T) {
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

// TS-11-28 (integration): RunPreflight on full success reports the spec
// checklist and writes no package to disk.
//
// Verifies: 11-REQ-5.3
func TestTS11_28_RunPreflightReportsTheChecklistAndWritesNothing(t *testing.T) {
	o := preflightOptions(t)
	specsDir := resolveSpecsDir(o, o.Workspace.Root)
	before := dirNames(t, specsDir)

	res, err := RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	if res.Stage != "preflight" {
		t.Errorf("stage = %q", res.Stage)
	}
	if c, ok := findCheck(res.Preflight, "schemas_valid"); !ok || !c.OK {
		t.Errorf("schemas_valid = %+v (present %v)", c, ok)
	}
	if c, ok := findCheck(res.Preflight, "split_plan"); !ok || !c.OK {
		t.Errorf("split_plan = %+v (present %v)", c, ok)
	}
	for _, absent := range []string{"name_flag", "comment_target", "comment_credential"} {
		if _, ok := findCheck(res.Preflight, absent); ok {
			t.Errorf("%s is present without its flag", absent)
		}
	}
	if !reflect.DeepEqual(dirNames(t, specsDir), before) {
		t.Errorf("the spec root changed: %v -> %v", before, dirNames(t, specsDir))
	}
	if got := res.Package.SpecDir; got != "" {
		t.Errorf("a package was reported: %q", got)
	}
	if env := o.Run.Envelope(0, res, nil); env.Usage != nil {
		t.Errorf("a phase was recorded: %+v", env.Usage)
	}
	if res.Estimate == nil || res.Estimate.MaxTurnsPerPhase != 10 || res.Estimate.MaxBudgetPerPhaseUSD != 2.0 {
		t.Errorf("estimate bounds = %+v", res.Estimate)
	}
}

// The conditional entries: --name, and --comment on an issue input, appear
// only when given; --dry-run drops split_plan and the comment entries.
func TestRunPreflightConditionalEntries(t *testing.T) {
	o := preflightOptions(t)
	o.Name = "my_spec"
	res, err := RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := findCheck(res.Preflight, "name_flag"); !ok || !c.OK || c.Detail != "my_spec" {
		t.Errorf("name_flag = %+v (present %v)", c, ok)
	}

	o = preflightOptions(t)
	o.DryRun = true
	res, err = RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findCheck(res.Preflight, "split_plan"); ok {
		t.Error("split_plan is present under --dry-run")
	}
	if _, ok := findCheck(res.Preflight, "schemas_valid"); !ok {
		t.Error("schemas_valid is always present")
	}

	o = preflightOptions(t)
	o.Comment = true
	o.Input = toolio.Input{Kind: toolio.KindIssue, Origin: "https://github.com/a/b/issues/1", Body: "an idea",
		Issue: &issuex.IssueRef{Repo: issuex.Repo{Owner: "a", Name: "b"}, Number: 1}}
	o.Forge = &mockForgeClient{authenticated: true}
	res, err = RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := findCheck(res.Preflight, "comment_target"); !ok || !c.OK || c.Detail == "" {
		t.Errorf("comment_target = %+v (present %v)", c, ok)
	}
	if c, ok := findCheck(res.Preflight, "comment_credential"); !ok || !c.OK {
		t.Errorf("comment_credential = %+v (present %v)", c, ok)
	}
}

// A refusal is the pipeline's own Preflight failure, verbatim, with no
// result: the same stage, category and message as the ordinary run.
func TestRunPreflightRefusalIsPreflightsFailure(t *testing.T) {
	o := preflightOptions(t)
	o.Name = "Bad Name"
	_, want := Preflight(context.Background(), o)
	res, err := RunPreflight(context.Background(), o)
	var got *Failure
	if !errors.As(err, &got) || want == nil {
		t.Fatalf("err = %v, want a *Failure", err)
	}
	if got.Stage != want.Stage || got.Category != want.Category || got.Error() != want.Error() {
		t.Errorf("failure = %+v, want %+v", got, want)
	}
	if res != nil {
		t.Errorf("a refusal carries a result: %+v", res)
	}

	// An ambiguous split plan refuses the run, not a checklist entry.
	o = preflightOptions(t)
	specsDir := resolveSpecsDir(o, o.Workspace.Root)
	for _, name := range []string{"one", "two"} {
		plan := newSplitPlan(o.Input, []SplitScope{{Name: name, Scope: "s"}, {Name: name + "_b", Scope: "t"}})
		if err := plan.save(specsDir); err != nil {
			t.Fatal(err)
		}
	}
	_, err = RunPreflight(context.Background(), o)
	if !errors.As(err, &got) || got.Stage != "preflight" || got.Category != "usage" {
		t.Errorf("two matching plans: err = %v", err)
	}
}

// TS-11-32 (unit): the estimate counts the PRD phase plus one per generation
// step, adds one for --architecture, and multiplies by a resumed split plan's
// pending scope count.
//
// Verifies: 11-REQ-5.5
func TestTS11_32_EstimateCountsPhases(t *testing.T) {
	perPackage := 1 + len(afspec.GenerationSteps)
	if len(afspec.GenerationSteps) != 3 {
		t.Fatalf("GenerationSteps has %d entries; the spec assumes 3", len(afspec.GenerationSteps))
	}

	resA, err := RunPreflight(context.Background(), preflightOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	if resA.Estimate.Phases != perPackage || perPackage != 4 {
		t.Errorf("fresh input: phases = %d, want 4", resA.Estimate.Phases)
	}
	if resA.Estimate.MaxTotalUSD != float64(resA.Estimate.Phases)*2.0 {
		t.Errorf("max_total_usd = %v", resA.Estimate.MaxTotalUSD)
	}

	oB := preflightOptions(t)
	oB.Architecture = true
	resB, err := RunPreflight(context.Background(), oB)
	if err != nil {
		t.Fatal(err)
	}
	if resB.Estimate.Phases != resA.Estimate.Phases+1 {
		t.Errorf("--architecture: phases = %d, want %d", resB.Estimate.Phases, resA.Estimate.Phases+1)
	}

	// A plan with 3 scopes of which 1 is written leaves 2 pending.
	oC := preflightOptions(t)
	specsDir := resolveSpecsDir(oC, oC.Workspace.Root)
	plan := newSplitPlan(oC.Input, []SplitScope{{Name: "first", Scope: "a"}, {Name: "second", Scope: "b"}, {Name: "third", Scope: "c"}})
	plan.Scopes[0].Dir, plan.Scopes[0].SpecID = "01_first", "01"
	if err := plan.save(specsDir); err != nil {
		t.Fatal(err)
	}
	if plan.Pending() != 2 {
		t.Fatalf("pending = %d", plan.Pending())
	}
	before := dirNames(t, specsDir)
	resC, err := RunPreflight(context.Background(), oC)
	if err != nil {
		t.Fatal(err)
	}
	if resC.Estimate.Phases != resA.Estimate.Phases*2 {
		t.Errorf("resuming 2 pending scopes: phases = %d, want %d", resC.Estimate.Phases, resA.Estimate.Phases*2)
	}
	if c, ok := findCheck(resC.Preflight, "split_plan"); !ok || !c.OK || c.Detail == "" {
		t.Errorf("split_plan = %+v (present %v)", c, ok)
	}
	if !reflect.DeepEqual(dirNames(t, specsDir), before) {
		t.Errorf("RunPreflight changed the spec root: %v -> %v", before, dirNames(t, specsDir))
	}
	if _, err := os.Stat(filepath.Join(specsDir, "first"+SplitPlanSuffix)); err != nil {
		t.Errorf("the plan was removed: %v", err)
	}
}

// TS-11-38 (unit): Summary() reports the checklist count when Stage is
// preflight and checks passed; Resumable() is false.
//
// Verifies: 11-REQ-6.3, 11-REQ-6.4
func TestTS11_38_SummaryUnderAPreflightStage(t *testing.T) {
	r := Result{Stage: "preflight", Preflight: make([]toolio.PreflightCheck, 3)}
	if got, want := r.Summary(), "spec: preflight passed (3 checks)"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
	r.SplitPlan = ".specs/x.split.json"
	if r.Resumable() {
		t.Error("a preflight result is not resumable, even with a split plan named")
	}
	if !(Result{SplitPlan: "x"}).Resumable() {
		t.Error("an ordinary result with a split plan is still resumable")
	}
}

// The default (summary) view keeps the checklist and the estimate.
func TestSummaryViewKeepsPreflightAndEstimate(t *testing.T) {
	r := &Result{Stage: "preflight", Preflight: []toolio.PreflightCheck{{Check: "schemas_valid", OK: true}},
		Estimate: &toolio.Estimate{Phases: 4}}
	v, ok := r.SummaryView().(summaryResult)
	if !ok {
		t.Fatalf("SummaryView is %T", r.SummaryView())
	}
	if v.Stage != "preflight" || len(v.Preflight) != 1 || v.Estimate == nil || v.Estimate.Phases != 4 {
		t.Errorf("view = %+v", v)
	}
}

// TS-15-9 (unit): RunPreflight adds a symbol_backend check, OK true, with the
// detail "ctags" or "heuristics".
//
// Verifies: 15-REQ-4.1, 15-REQ-4.2
func TestTS15_9_PreflightReportsSymbolBackend(t *testing.T) {
	res, err := RunPreflight(context.Background(), preflightOptions(t))
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

	res, err := RunPreflight(context.Background(), preflightOptions(t))
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	if _, ok := findCheck(res.Preflight, "symbol_backend"); ok {
		t.Errorf("symbol_backend present after a failed detection: %+v", res.Preflight)
	}
	if _, ok := findCheck(res.Preflight, "schemas_valid"); !ok {
		t.Errorf("other checks missing: %+v", res.Preflight)
	}
}

// TS-16-24 (unit): RunPreflight includes a code_search_index check with the
// detail "built" when the run has an index.
//
// Verifies: 16-REQ-7.1
func TestTS16_24_PreflightReportsCodeSearchIndexBuilt(t *testing.T) {

	o := preflightOptions(t)
	o.Index = &indextest.Index{}
	res, err := RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	c, ok := findCheck(res.Preflight, "code_search_index")
	if !ok {
		t.Fatalf("no code_search_index check in %+v", res.Preflight)
	}
	if !c.OK || c.Detail != "built" {
		t.Errorf("code_search_index = %+v, want OK with detail built", c)
	}
}

// TS-16-25 (unit): with no index the check is still OK and says why.
//
// Verifies: 16-REQ-7.2, 16-REQ-7.3
func TestTS16_25_PreflightReportsCodeSearchIndexUnavailable(t *testing.T) {
	for _, tc := range []struct{ reason, want string }{
		{"", "unavailable: index not built"},
		{"unsupported platform", "unavailable: unsupported platform"},
	} {

		o := preflightOptions(t)
		o.Index = nil
		o.IndexUnavailable = tc.reason
		res, err := RunPreflight(context.Background(), o)
		if err != nil {
			t.Fatalf("RunPreflight: %v", err)
		}
		c, ok := findCheck(res.Preflight, "code_search_index")
		if !ok {
			t.Fatalf("no code_search_index check in %+v", res.Preflight)
		}
		if !c.OK || c.Detail != tc.want {
			t.Errorf("code_search_index = %+v, want OK with detail %q", c, tc.want)
		}
	}
}

// TS-17-30 (unit): spec's RunPreflight puts go_typecheck between
// symbol_backend and code_search_index with ok true, and omits it without
// touching any other check when detection fails.
//
// Verifies: 17-REQ-4.1, 17-REQ-4.5
func TestTS17_30_SpecPreflightReportsGoTypecheck(t *testing.T) {
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
		res, err := RunPreflight(context.Background(), preflightOptions(t))
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
	})

	// Subtest 2: partial detail.
	t.Run("partial", func(t *testing.T) {
		inject("3 packages checked, 41 errors, partial", nil)
		res, err := RunPreflight(context.Background(), preflightOptions(t))
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

		res, err := RunPreflight(context.Background(), preflightOptions(t))
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
		ok, err := RunPreflight(context.Background(), preflightOptions(t))
		if err != nil {
			t.Fatalf("RunPreflight (ok): %v", err)
		}

		// Now fail detection.
		inject("", errors.New("no workspace configured"))
		res, err := RunPreflight(context.Background(), preflightOptions(t))
		if err != nil {
			t.Fatalf("RunPreflight (fail): %v", err)
		}
		if _, found := findCheck(res.Preflight, "go_typecheck"); found {
			t.Error("go_typecheck should be absent when detection fails")
		}
		if !reflect.DeepEqual(strip(ok.Preflight), strip(res.Preflight)) {
			t.Errorf("other checks differ:\nok:   %+v\nfail: %+v", strip(ok.Preflight), strip(res.Preflight))
		}
		if !reflect.DeepEqual(res.Estimate, ok.Estimate) {
			t.Errorf("estimate differs: %+v vs %+v", res.Estimate, ok.Estimate)
		}
	})
}
