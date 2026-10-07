package codeimpl

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/agentrun/indextest"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// preflightOptions is newOptions plus a Runner whose ResolvedBounds are
// (10 turns, $2). Building a Runner makes no call; RunPreflight never runs a
// phase, so the scripted brain must never be asked for one.
func preflightOptions(t *testing.T, ws *tools.Workspace, g *gitx.Git) Options {
	t.Helper()
	o := newOptions(ws, g, &scriptedBrain{})
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

func branches(t *testing.T, dir string) []string {
	t.Helper()
	f := strings.Fields(gitOut(t, dir, "branch", "--format=%(refname:short)"))
	slices.Sort(f)
	return f
}

// TS-11-17 (unit): codeimpl.Result carries Preflight and Estimate immediately
// after DryRun.
//
// Verifies: 11-REQ-3.4
func TestTS11_17_ResultCarriesPreflightAndEstimateAfterDryRun(t *testing.T) {
	typ := reflect.TypeOf(Result{})
	var names []string
	for i := 0; i < typ.NumField(); i++ {
		names = append(names, typ.Field(i).Name)
	}
	i := slices.Index(names, "DryRun")
	if i < 0 || i+2 >= len(names) {
		t.Fatalf("fields = %v", names)
	}
	if names[i+1] != "Preflight" || names[i+2] != "Estimate" {
		t.Fatalf("fields after DryRun = %v, want Preflight then Estimate", names[i+1:i+3])
	}
	pf, _ := typ.FieldByName("Preflight")
	if pf.Type != reflect.TypeOf([]toolio.PreflightCheck(nil)) {
		t.Errorf("Preflight type = %v", pf.Type)
	}
	if got := pf.Tag.Get("json"); got != "preflight,omitempty" {
		t.Errorf("Preflight json tag = %q", got)
	}
	est, _ := typ.FieldByName("Estimate")
	if est.Type != reflect.TypeOf((*toolio.Estimate)(nil)) {
		t.Errorf("Estimate type = %v", est.Type)
	}
	if got := est.Tag.Get("json"); got != "estimate,omitempty" {
		t.Errorf("Estimate json tag = %q", got)
	}
}

// TS-11-27 (integration): RunPreflight on full success reports Tasks and the
// impl checklist without creating a branch.
//
// Verifies: 11-REQ-5.2
func TestTS11_27_RunPreflightReportsChecklistWithoutBranch(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	o := preflightOptions(t, ws, g)
	before := branches(t, ws.Root)

	res, err := RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	if res.Stage != "preflight" {
		t.Errorf("Stage = %q", res.Stage)
	}
	if len(res.Tasks) == 0 || res.TasksRemaining != 3 || res.TasksTotal != 3 {
		t.Errorf("Tasks = %d, remaining %d, total %d", len(res.Tasks), res.TasksRemaining, res.TasksTotal)
	}
	for _, task := range res.Tasks {
		if task.Outcome != OutcomePending {
			t.Errorf("task %d outcome = %s", task.ID, task.Outcome)
		}
	}
	for _, name := range []string{"git_repository", "clean_tree", "spec_resolved", "branch", "spec_valid",
		"spec_status", "test_commands", "dependencies", "verify_baseline"} {
		c, ok := findCheck(res.Preflight, name)
		if !ok {
			t.Errorf("checklist lacks %q: %v", name, res.Preflight)
			continue
		}
		if !c.OK {
			t.Errorf("%s: OK = false (%s)", name, c.Detail)
		}
	}
	for _, name := range []string{"pull", "forge_credential", "land_target", "remote_configured", "repair_model_credential"} {
		if _, ok := findCheck(res.Preflight, name); ok {
			t.Errorf("checklist has %q although nothing asked for it", name)
		}
	}
	if c, _ := findCheck(res.Preflight, "spec_resolved"); !strings.HasSuffix(c.Detail, "09_agent_mode") {
		t.Errorf("spec_resolved detail = %q", c.Detail)
	}
	if c, _ := findCheck(res.Preflight, "spec_status"); c.Detail != "active" {
		t.Errorf("spec_status detail = %q", c.Detail)
	}
	if c, _ := findCheck(res.Preflight, "branch"); !strings.Contains(c.Detail, res.Branch) || !strings.Contains(c.Detail, "created") {
		t.Errorf("branch detail = %q (branch %q)", c.Detail, res.Branch)
	}
	if res.Branch == "" {
		t.Fatal("Branch is empty")
	}
	if got := branches(t, ws.Root); !slices.Equal(before, got) || slices.Contains(got, res.Branch) {
		t.Errorf("branches before %v, after %v: %s must not exist", before, got, res.Branch)
	}
	if !res.Baseline.Ran() {
		t.Errorf("the baseline gate did not run: %+v", res.Baseline)
	}
	if res.Summary() != "impl: preflight passed; 3 of 3 tasks remain" {
		t.Errorf("Summary = %q", res.Summary())
	}
	if res.Resumable() {
		t.Error("a preflight result is never resumable")
	}
	if b := o.brain.(*scriptedBrain); b.surveys != 0 || len(b.inputs) != 0 {
		t.Error("a model phase ran")
	}
}

// An existing continuation branch is reported as continuing.
func TestRunPreflightReportsAContinuationBranch(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	o := preflightOptions(t, ws, g)
	first, err := RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	gitOut(t, ws.Root, "branch", first.Branch)

	res, err := RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := findCheck(res.Preflight, "branch"); !ok || !strings.Contains(c.Detail, "continuing") {
		t.Errorf("branch = %+v", c)
	}
}

// TS-11-31 (unit): the estimate counts pending tasks plus a survey phase
// unless --no-survey was given.
//
// Verifies: 11-REQ-5.5
//
// The conformance review after the last task is a phase the plan decides too,
// so it is counted; --no-review takes it out.
func TestTS11_31_EstimateCountsPendingTasksPlusSurvey(t *testing.T) {
	ws, g, _ := newSpecRepo(t) // three pending tasks
	o := preflightOptions(t, ws, g)

	res, err := RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	e := res.Estimate
	if e == nil {
		t.Fatal("no estimate")
	}
	if e.Phases != 5 || e.MaxTurnsPerPhase != 10 || e.MaxBudgetPerPhaseUSD != 2.0 || e.MaxTotalUSD != 10.0 {
		t.Errorf("Estimate = %+v, want {5 10 2 10}: three tasks, the survey and the review", *e)
	}

	o.NoReview = true
	res, err = RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if e := res.Estimate; e == nil || e.Phases != 4 || e.MaxTotalUSD != 8.0 {
		t.Errorf("--no-review Estimate = %+v, want 4 phases and $8", e)
	}

	o.NoSurvey = true
	res, err = RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if e := res.Estimate; e == nil || e.Phases != 3 || e.MaxTotalUSD != 6.0 {
		t.Errorf("--no-survey Estimate = %+v, want 3 phases and $6", e)
	}

	o.Task = 1
	res, err = RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if e := res.Estimate; e == nil || e.Phases != 1 {
		t.Errorf("--task Estimate = %+v, want 1 phase", e)
	}
}

// TS-11-37 (unit): Summary reports the remaining tasks when Stage is
// preflight and the checks passed.
//
// Verifies: 11-REQ-6.2
func TestTS11_37_SummaryReportsRemainingTasks(t *testing.T) {
	r := Result{Stage: "preflight", TasksRemaining: 2, TasksTotal: 5}
	if got, want := r.Summary(), "impl: preflight passed; 2 of 5 tasks remain"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
	// Branch is the name the branch WOULD have, so it is nothing to resume.
	r.Branch = "impl/09-agent-mode"
	if r.Resumable() {
		t.Error("Resumable() = true for a preflight result with a Branch")
	}
	// A preflight result with a checklist is a pass whatever the counts.
	done := Result{Stage: "preflight", TasksTotal: 3, Preflight: make([]toolio.PreflightCheck, 1)}
	if got, want := done.Summary(), "impl: preflight passed; 0 of 3 tasks remain"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
	// An ordinary run that refused during its own preflight stage is not a pass.
	refused := Result{Stage: "preflight", TasksTotal: 3, TasksRemaining: 3,
		Baseline: GateResult{Checks: []checks.Result{{Command: "make test", ExitCode: -1}}}}
	if got := refused.Summary(); strings.Contains(got, "preflight passed") {
		t.Errorf("a refused run reads %q", got)
	}
	if refused.Resumable() {
		t.Error("a run that stopped at its preflight stage is not resumable")
	}
	if !(Result{Stage: "implementing", Branch: "impl/x"}).Resumable() {
		t.Error("an ordinary run with a branch is still resumable")
	}
}

// A refusal is the ordinary run's, with no checklist (11-REQ-4.1, 4.3).
func TestRunPreflightRefusesLikeRun(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	write(t, ws.Root, "loose.txt", "x")
	o := preflightOptions(t, ws, g)

	res, err := RunPreflight(context.Background(), o)
	var pf *Failure
	if !errors.As(err, &pf) {
		t.Fatalf("err = %v", err)
	}
	if res != nil && (len(res.Preflight) != 0 || res.Estimate != nil) {
		t.Errorf("a refused run carries a checklist or an estimate: %+v", res)
	}
	_, ordErr := Run(context.Background(), o)
	var of *Failure
	if !errors.As(ordErr, &of) || of.Stage != pf.Stage || of.Category != pf.Category || ordErr.Error() != err.Error() {
		t.Errorf("ordinary error = %v, preflight error = %v", ordErr, err)
	}

	// The ordinary run's own failing result carries neither key (11-REQ-3.5).
	ordRes, _ := Run(context.Background(), o)
	for _, v := range []any{ordRes, ordRes.SummaryView()} {
		b, _ := json.Marshal(v)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		for _, k := range []string{"preflight", "estimate"} {
			if _, ok := m[k]; ok {
				t.Errorf("an ordinary run's result carries %q: %s", k, b)
			}
		}
	}
}

// RunPreflight refuses without a runner or a workspace, as Run does.
func TestRunPreflightRequiresWorkspaceAndRunner(t *testing.T) {
	if _, err := RunPreflight(context.Background(), Options{}); err == nil {
		t.Error("want a refusal without a workspace")
	}
	ws, g, _ := newSpecRepo(t)
	if _, err := RunPreflight(context.Background(), newOptions(ws, g, &scriptedBrain{})); err == nil {
		t.Error("want a refusal without a runner")
	}
}

// A baseline that fails is advisory; --no-verify leaves verify_baseline out.
func TestRunPreflightBaselineIsAdvisoryAndAbsentUnderNoVerify(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	write(t, ws.Root, "FAIL", "x")
	if _, err := g.CommitAll(context.Background(), "chore: make the suite fail\n"); err != nil {
		t.Fatal(err)
	}
	o := preflightOptions(t, ws, g)
	res, err := RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatalf("a failing baseline refused the run: %v", err)
	}
	c, ok := findCheck(res.Preflight, "verify_baseline")
	if !ok || c.OK || !strings.Contains(c.Detail, "make test") {
		t.Errorf("verify_baseline = %+v (present %v)", c, ok)
	}

	o.NoVerify = true
	res, err = RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findCheck(res.Preflight, "verify_baseline"); ok {
		t.Error("verify_baseline must be absent under --no-verify")
	}
	if res.Estimate == nil {
		t.Error("no estimate")
	}
}

// --repair-model's resolution is a check of its own.
func TestRunPreflightReportsTheRepairModel(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	o := preflightOptions(t, ws, g)
	rr, err := agentrun.NewRunner(agentrun.Config{Model: &core.Model{ID: "repair-model", Provider: "test"}})
	if err != nil {
		t.Fatal(err)
	}
	o.RepairRunner = rr
	res, err := RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	c, ok := findCheck(res.Preflight, "repair_model_credential")
	if !ok || !c.OK || !strings.Contains(c.Detail, "repair-model") {
		t.Errorf("repair_model_credential = %+v (present %v)", c, ok)
	}
}

// 11-REQ-7.6: the dependencies entry's detail is a fact. An upstream spec the
// run could not find is not reported as sealed or done; the entry says it
// could not be checked, beside the upstream_missing warning, and stays ok
// because the ordinary run tolerates it.
func TestPreflightDependenciesDetailSaysWhatWasNotChecked(t *testing.T) {
	ws, g, specDir := newSpecRepo(t)

	tasksFile := filepath.Join(specDir, "tasks.json")
	raw, err := os.ReadFile(tasksFile)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["dependencies"] = []map[string]any{{"spec": "08", "reason": "needs the upstream foundation"}}
	raw, err = json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	write(t, specDir, "tasks.json", string(raw))
	if _, err := g.CommitAll(context.Background(), "chore: depend on a spec that is not there\n"); err != nil {
		t.Fatal(err)
	}

	o := preflightOptions(t, ws, g)
	res, err := RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	c, ok := findCheck(res.Preflight, "dependencies")
	if !ok {
		t.Fatalf("checklist lacks dependencies: %v", res.Preflight)
	}
	if !c.OK {
		t.Errorf("dependencies: OK = false (%s); an unchecked upstream is tolerated", c.Detail)
	}
	if strings.Contains(c.Detail, "sealed or done") && !strings.Contains(c.Detail, "could not be checked") {
		t.Errorf("detail claims what was never verified: %q", c.Detail)
	}
	if !strings.Contains(c.Detail, "1 could not be checked") || !strings.Contains(c.Detail, "0 verified") {
		t.Errorf("detail = %q, want it to say 0 verified and 1 could not be checked", c.Detail)
	}
	var warned bool
	for _, w := range o.Run.Warnings() {
		if w.Code == toolio.WarnUpstreamMissing {
			warned = true
		}
	}
	if !warned {
		t.Error("no upstream_missing warning beside the entry")
	}
}

// TS-15-9 (unit): RunPreflight adds a symbol_backend check, OK true, with the
// detail "ctags" or "heuristics".
//
// Verifies: 15-REQ-4.1, 15-REQ-4.2
func TestTS15_9_PreflightReportsSymbolBackend(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	res, err := RunPreflight(context.Background(), preflightOptions(t, ws, g))
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

	ws, g, _ := newSpecRepo(t)
	res, err := RunPreflight(context.Background(), preflightOptions(t, ws, g))
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	if _, ok := findCheck(res.Preflight, "symbol_backend"); ok {
		t.Errorf("symbol_backend present after a failed detection: %+v", res.Preflight)
	}
	if _, ok := findCheck(res.Preflight, "git_repository"); !ok {
		t.Errorf("other checks missing: %+v", res.Preflight)
	}
}

// TS-16-24 (unit): RunPreflight includes a code_search_index check with the
// detail "built" when the run has an index.
//
// Verifies: 16-REQ-7.1
func TestTS16_24_PreflightReportsCodeSearchIndexBuilt(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	o := preflightOptions(t, ws, g)
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
		ws, g, _ := newSpecRepo(t)
		o := preflightOptions(t, ws, g)
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

// TS-17-29 (unit): impl's RunPreflight puts go_typecheck between
// symbol_backend and code_search_index with ok true, and omits it without
// touching any other check when detection fails.
//
// Verifies: 17-REQ-4.1, 17-REQ-4.5
func TestTS17_29_ImplPreflightReportsGoTypecheck(t *testing.T) {
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
		ws, g, _ := newSpecRepo(t)
		res, err := RunPreflight(context.Background(), preflightOptions(t, ws, g))
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
		ws, g, _ := newSpecRepo(t)
		res, err := RunPreflight(context.Background(), preflightOptions(t, ws, g))
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

		ws, g, _ := newSpecRepo(t)
		res, err := RunPreflight(context.Background(), preflightOptions(t, ws, g))
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
		ws, g, _ := newSpecRepo(t)
		ok, err := RunPreflight(context.Background(), preflightOptions(t, ws, g))
		if err != nil {
			t.Fatalf("RunPreflight (ok): %v", err)
		}

		// Now fail detection.
		inject("", errors.New("probe timed out"))
		ws2, g2, _ := newSpecRepo(t)
		res, err := RunPreflight(context.Background(), preflightOptions(t, ws2, g2))
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
}
