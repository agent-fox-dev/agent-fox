package codefix

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
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/agentrun/indextest"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// preflightRunner builds a Runner whose ResolvedBounds are (turns, budget).
// Building a Runner makes no call; RunPreflight never runs a phase.
func preflightRunner(t *testing.T, turns int, budget float64) *agentrun.Runner {
	t.Helper()
	r, err := agentrun.NewRunner(agentrun.Config{
		Model:  &core.Model{ID: "test-model", Provider: "test"},
		Bounds: agentrun.Bounds{MaxTurns: turns, MaxBudgetUSD: budget},
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func preflightOptions(t *testing.T, ws *tools.Workspace, g *gitx.Git) Options {
	t.Helper()
	o := newOptions(ws, g, nil)
	o.Runner = preflightRunner(t, 10, 2.0)
	return o
}

func listBranches(t *testing.T, dir string) []string {
	t.Helper()
	out, code, err := gitx.ExecRunner(context.Background(), dir, []string{"git", "branch", "--format=%(refname:short)"})
	if err != nil || code != 0 {
		t.Fatalf("git branch: %v (%d) %s", err, code, out)
	}
	fields := strings.Fields(out)
	slices.Sort(fields)
	return fields
}

func skipIfNoFindReferences(t *testing.T, ws *tools.Workspace) {
	t.Helper()
	built, err := tools.All(tools.Options{Workspace: ws})
	if err != nil {
		t.Skipf("tools.All failed: %v", err)
	}
	for _, tl := range built {
		if tl.Name == "find_references" {
			return
		}
	}
	t.Skip("find_references not offered by the replace target")
}

func findCheck(checks []toolio.PreflightCheck, name string) (toolio.PreflightCheck, bool) {
	for _, c := range checks {
		if c.Check == name {
			return c, true
		}
	}
	return toolio.PreflightCheck{}, false
}

// recordingForge counts every write the pipeline attempts.
type recordingForge struct {
	issuex.NoOpClient
	authenticated bool
	writes        int
}

func (f *recordingForge) Authenticated() bool { return f.authenticated }
func (f *recordingForge) AddComment(context.Context, issuex.IssueRef, string) (string, error) {
	f.writes++
	return "", nil
}
func (f *recordingForge) CreatePullRequest(context.Context, issuex.Repo, issuex.CreatePullRequestRequest) (issuex.PullRequest, error) {
	f.writes++
	return issuex.PullRequest{}, nil
}
func (f *recordingForge) CreateIssue(context.Context, issuex.Repo, issuex.CreateIssueRequest) (issuex.Issue, error) {
	f.writes++
	return issuex.Issue{}, nil
}

// TS-11-16 (unit): codefix.Result carries Preflight and Estimate immediately
// after DryRun.
//
// Verifies: 11-REQ-3.4
func TestTS11_16_ResultCarriesPreflightAndEstimateAfterDryRun(t *testing.T) {
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

// TS-11-20 (unit): an ordinary run's result carries no preflight or estimate
// key, whether it succeeds or fails.
//
// Verifies: 11-REQ-3.5
func TestTS11_20_OrdinaryRunCarriesNoPreflightOrEstimate(t *testing.T) {
	check := func(t *testing.T, res *Result) {
		t.Helper()
		for name, v := range map[string]any{"result": res, "summary view": res.SummaryView()} {
			b, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			for _, k := range []string{"preflight", "estimate"} {
				if _, ok := m[k]; ok {
					t.Errorf("an ordinary run's %s carries %q: %s", name, k, b)
				}
			}
		}
	}

	t.Run("success", func(t *testing.T) {
		ws, g := newRepo(t, 0)
		res, err := Run(context.Background(), newOptions(ws, g, defaultBrain()))
		if err != nil {
			t.Fatal(err)
		}
		check(t, res)
	})
	t.Run("failure", func(t *testing.T) {
		ws, g := newRepo(t, 0)
		write(t, ws.Root, "dirty.txt", "x")
		res, err := Run(context.Background(), newOptions(ws, g, defaultBrain()))
		if err == nil {
			t.Fatal("want an error")
		}
		check(t, res)
	})
}

// TS-11-25 (integration): a failing preflight run creates no branch and makes
// no remote write.
//
// Verifies: 11-REQ-4.4
func TestTS11_25_FailingPreflightCreatesNoBranchAndNoWrite(t *testing.T) {
	ws, g := newRepo(t, 0)
	forge := &recordingForge{authenticated: false}
	o := preflightOptions(t, ws, g)
	o.Land = LandPR // needs a credential; the forge has none: refuses after the base branch
	o.Forge = forge
	before := listBranches(t, ws.Root)

	res, err := RunPreflight(context.Background(), o)
	if err == nil {
		t.Fatal("want the auth refusal")
	}
	var f *Failure
	if !errors.As(err, &f) || f.Stage != "preflight" || f.Category != "auth" {
		t.Fatalf("err = %v", err)
	}
	if after := listBranches(t, ws.Root); !slices.Equal(before, after) {
		t.Errorf("branches before %v, after %v", before, after)
	}
	if forge.writes != 0 {
		t.Errorf("forge writes = %d, want 0", forge.writes)
	}
	if res != nil && len(res.Preflight) != 0 {
		t.Errorf("a refused run carries a partial checklist: %v", res.Preflight)
	}

	// The refusal is the one the ordinary run reports.
	oo := newOptions(ws, g, defaultBrain())
	oo.Land, oo.Forge = LandPR, forge
	_, ordErr := Run(context.Background(), oo)
	if ordErr == nil || ordErr.Error() != err.Error() {
		t.Errorf("ordinary error = %v, preflight error = %v", ordErr, err)
	}
}

// TS-11-26 (integration): RunPreflight on full success runs the baseline and
// reports the fix checklist without creating a branch.
//
// Verifies: 11-REQ-5.1
func TestTS11_26_RunPreflightReportsChecklistWithoutBranch(t *testing.T) {
	ws, g := newRepo(t, 0) // a Makefile with a passing `test` target
	o := preflightOptions(t, ws, g)
	before := listBranches(t, ws.Root)

	res, err := RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	if res.Stage != "preflight" {
		t.Errorf("Stage = %q", res.Stage)
	}
	if !res.Baseline.Ran() || !res.Baseline.OK {
		t.Errorf("Baseline = %+v, want a passing run", res.Baseline)
	}
	for _, name := range []string{"git_repository", "clean_tree", "base_branch", "verify_command", "verify_baseline"} {
		c, ok := findCheck(res.Preflight, name)
		if !ok {
			t.Errorf("checklist lacks %q: %v", name, res.Preflight)
			continue
		}
		if !c.OK {
			t.Errorf("%s: OK = false (%s)", name, c.Detail)
		}
	}
	if c, _ := findCheck(res.Preflight, "base_branch"); c.Detail != "main" {
		t.Errorf("base_branch detail = %q", c.Detail)
	}
	if c, _ := findCheck(res.Preflight, "verify_command"); c.Detail != "make test" {
		t.Errorf("verify_command detail = %q", c.Detail)
	}
	for _, name := range []string{"pull", "forge_credential", "land_target", "remote_configured"} {
		if _, ok := findCheck(res.Preflight, name); ok {
			t.Errorf("checklist has %q although nothing asked for it", name)
		}
	}
	if after := listBranches(t, ws.Root); !slices.Equal(before, after) {
		t.Errorf("branches before %v, after %v", before, after)
	}
	if res.Branch != "" || res.Commit != "" {
		t.Errorf("a preflight run produced Branch=%q Commit=%q", res.Branch, res.Commit)
	}
	if len(res.Preflight) == 0 || res.Summary() != "fix: preflight passed ("+itoa(len(res.Preflight))+" checks)" {
		t.Errorf("Summary = %q", res.Summary())
	}
}

// The conditional entries appear when the run would need them.
func TestRunPreflightReportsConditionalChecks(t *testing.T) {
	ws, g := newRepo(t, 0)
	o := preflightOptions(t, ws, g)
	o.Land = LandBranch
	o.Forge = &recordingForge{authenticated: true}
	if out, code, err := gitx.ExecRunner(context.Background(), ws.Root,
		[]string{"git", "remote", "add", "origin", "https://github.com/acme/widgets.git"}); err != nil || code != 0 {
		t.Fatalf("git remote add: %v %s", err, out)
	}
	o.Input.Issue = &issuex.IssueRef{Repo: issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"}, Number: 3}

	res, err := RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	for _, name := range []string{"forge_credential", "remote_configured"} {
		if c, ok := findCheck(res.Preflight, name); !ok || !c.OK {
			t.Errorf("%s = %+v, %v", name, c, ok)
		}
	}
	if _, ok := findCheck(res.Preflight, "land_target"); ok {
		t.Error("land_target is only for --land=pr")
	}
}

// TS-11-30 (unit): the estimate reports exactly two phases, with bounds from
// ResolvedBounds.
//
// Verifies: 11-REQ-5.5
func TestTS11_30_EstimateIsTwoPhasesFromResolvedBounds(t *testing.T) {
	ws, g := newRepo(t, 0)
	res, err := RunPreflight(context.Background(), preflightOptions(t, ws, g))
	if err != nil {
		t.Fatal(err)
	}
	e := res.Estimate
	if e == nil {
		t.Fatal("no estimate")
	}
	if e.Phases != 2 || e.MaxTurnsPerPhase != 10 || e.MaxBudgetPerPhaseUSD != 2.0 || e.MaxTotalUSD != 4.0 {
		t.Errorf("Estimate = %+v, want {2 10 2 4}", *e)
	}
}

// TS-11-34 (unit), as amended by docs/errata/11_preflight.md: no detectable
// verify command is no longer advisory — an unverified change lands only with
// --no-verify — so --preflight refuses it as the ordinary run does (#215).
//
// Verifies: 11-REQ-5.6 (erratum)
func TestTS11_34_NoVerifyCommandIsAdvisory(t *testing.T) {
	ws, g := newRepo(t, 0)
	if err := os.Remove(filepath.Join(ws.Root, "Makefile")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CommitAll(context.Background(), "chore: drop the makefile\n"); err != nil {
		t.Fatal(err)
	}
	_, err := RunPreflight(context.Background(), preflightOptions(t, ws, g))
	var f *Failure
	if !errors.As(err, &f) || f.Stage != "preflight" || f.Category != "usage" ||
		!strings.Contains(f.Error(), "--no-verify") {
		t.Errorf("RunPreflight: %v, want a preflight usage refusal naming --no-verify", err)
	}
}

// A baseline that currently fails is advisory too, and --no-verify leaves the
// baseline entry out.
func TestRunPreflightFailingBaselineAndNoVerify(t *testing.T) {
	ws, g := newRepo(t, 1) // `make test` exits 1
	res, err := RunPreflight(context.Background(), preflightOptions(t, ws, g))
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	c, ok := findCheck(res.Preflight, "verify_baseline")
	if !ok || c.OK || !strings.HasPrefix(c.Detail, "failed (exit ") {
		t.Errorf("verify_baseline = %+v (present %v)", c, ok)
	}

	o := preflightOptions(t, ws, g)
	o.NoVerify = true
	res, err = RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatalf("RunPreflight --no-verify: %v", err)
	}
	if _, ok := findCheck(res.Preflight, "verify_baseline"); ok {
		t.Error("verify_baseline must be absent under --no-verify")
	}
	if c, ok := findCheck(res.Preflight, "verify_command"); !ok || c.OK {
		t.Errorf("verify_command under --no-verify = %+v (present %v)", c, ok)
	}
}

// --dry-run implies nothing to a preflight run: its result is the one the
// bare flag produces (11-REQ-1.4).
func TestRunPreflightDryRunDoesNotChangeASuccessfulResult(t *testing.T) {
	ws, g := newRepo(t, 0)
	plain, err := RunPreflight(context.Background(), preflightOptions(t, ws, g))
	if err != nil {
		t.Fatal(err)
	}
	o := preflightOptions(t, ws, g)
	o.DryRun = true
	dry, err := RunPreflight(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	// Timings differ between two runs of the same command.
	plain.Baseline.DurationMS, dry.Baseline.DurationMS = 0, 0
	a, _ := json.Marshal(plain)
	b, _ := json.Marshal(dry)
	if string(a) != string(b) {
		t.Errorf("--preflight and --preflight --dry-run differ:\n%s\n%s", a, b)
	}
}

// RunPreflight refuses without a runner or workspace, the way Run does.
func TestRunPreflightRequiresWorkspaceAndRunner(t *testing.T) {
	if _, err := RunPreflight(context.Background(), Options{}); err == nil {
		t.Error("want a refusal without a workspace")
	}
	ws, g := newRepo(t, 0)
	o := newOptions(ws, g, nil)
	if _, err := RunPreflight(context.Background(), o); err == nil {
		t.Error("want a refusal without a runner")
	}
}

// TS-11-36 (unit): Summary reports the checklist count when Stage is
// preflight and the checks passed.
//
// Verifies: 11-REQ-6.1
func TestTS11_36_SummaryReportsChecklistCount(t *testing.T) {
	r := Result{Stage: "preflight", Preflight: make([]toolio.PreflightCheck, 5)}
	if got, want := r.Summary(), "fix: preflight passed (5 checks)"; got != want {
		t.Errorf("Summary() = %q, want %q", got, want)
	}
	if r.Resumable() {
		t.Error("a preflight result is never resumable")
	}
	// An ordinary run that refused at its own preflight stage has no checklist.
	if got := (Result{Stage: "preflight"}).Summary(); strings.Contains(got, "preflight passed") {
		t.Errorf("a refused run reads %q", got)
	}
}

// TS-15-9 (unit): RunPreflight adds a symbol_backend check, OK true, with the
// detail "ctags" or "heuristics".
//
// Verifies: 15-REQ-4.1, 15-REQ-4.2
func TestTS15_9_PreflightReportsSymbolBackend(t *testing.T) {
	ws, g := newRepo(t, 0)
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

	ws, g := newRepo(t, 0)
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

// TS-15-18 (smoke): fix --preflight reports the symbol_backend check with OK
// true, through the real RunPreflight and the real detection.
//
// Verifies: 15-PATH-2, 15-REQ-4.1
//
// Real components: RunPreflight, agentrun.DetectSymbolBackend,
// toolio.PreflightCheck.
func TestTS15_18_FixPreflightReportsTheSymbolBackend(t *testing.T) {
	ws, g := newRepo(t, 0)
	res, err := RunPreflight(context.Background(), preflightOptions(t, ws, g))
	if err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	var found []toolio.PreflightCheck
	for _, c := range res.Preflight {
		if c.Check == "symbol_backend" {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one symbol_backend entry, got %+v", res.Preflight)
	}
	if !found[0].OK {
		t.Errorf("symbol_backend is not OK: %+v", found[0])
	}
	if d := found[0].Detail; d != "ctags" && d != "heuristics" {
		t.Errorf("symbol_backend detail = %q, want ctags or heuristics", d)
	}
}

// TS-15-17 (smoke): the real fix brain's analyse (read-only) and implement
// (writing) phases each declare the six read tools; analyse declares no write
// tool, implement declares write_file, edit_file and execute.
//
// Verifies: 15-PATH-1, 15-REQ-1.2, 15-REQ-1.3, 15-REQ-1.4
//
// Real components: agentBrain (the phases codefix really builds),
// agentrun.Runner, agentrun.SelectTools, tools.All. Only the model is
// scripted; neither phase has a valid submission, so the runs end in errors
// that are ignored: what is asserted is what reached the wire.
func TestTS15_17_FixPhasesDeclareTheSixReadTools(t *testing.T) {
	ws, _ := newRepo(t, 0)
	skipIfNoFindReferences(t, ws)
	text := func(s string) faux.Turn {
		return faux.Turn{Blocks: []core.ContentBlock{faux.FauxText(s)}, StopReason: core.StopReasonStop}
	}
	p := faux.New(text("a"), text("b"), text("c"), text("d"), text("e"), text("f"))
	r, err := agentrun.NewRunner(agentrun.Config{
		Model:         faux.Model(),
		Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
		Workspace:     ws,
		Bounds:        agentrun.Bounds{MaxTurns: 4, MaxBudgetUSD: 1, MaxAttempts: 1},
		SessionPrefix: "fix",
	})
	if err != nil {
		t.Fatal(err)
	}
	b := &agentBrain{runner: r}
	in := toolio.Input{Kind: toolio.KindText, Origin: "argument", Body: "crash on nil pointer"}
	_, _, _ = b.Analyze(context.Background(), analysisInput{Input: in, Root: ws.Root})
	nAnalyse := len(p.Requests())
	_, _, _ = b.Implement(context.Background(), implementInput{Input: in, Root: ws.Root})

	reqs := p.Requests()
	if nAnalyse == 0 || len(reqs) == nAnalyse {
		t.Fatalf("expected requests from both phases, got %d then %d", nAnalyse, len(reqs))
	}
	declared := func(req core.Request) map[string]bool {
		m := map[string]bool{}
		for _, tool := range req.Tools {
			m[tool.Name] = true
		}
		return m
	}
	analyse, implement := declared(reqs[0]), declared(reqs[nAnalyse])
	for _, n := range agentrun.ReadOnlyFileTools {
		if !analyse[n] {
			t.Errorf("analyse did not declare %s", n)
		}
		if !implement[n] {
			t.Errorf("implement did not declare %s", n)
		}
	}
	for _, n := range []string{"file_outline", "find_symbol"} {
		if !analyse[n] || !implement[n] {
			t.Errorf("%s missing from a phase (analyse %v, implement %v)", n, analyse[n], implement[n])
		}
	}
	for _, n := range []string{"write_file", "edit_file"} {
		if analyse[n] {
			t.Errorf("analyse declared %s", n)
		}
		if !implement[n] {
			t.Errorf("implement did not declare %s", n)
		}
	}
	if !implement["execute"] {
		t.Error("implement did not declare execute")
	}
	// analyse keeps a shell, but behind the read-only guard: no phase of fix
	// is without it.
	if !analyse["execute"] {
		t.Error("analyse did not declare execute")
	}
}

// TS-16-24 (unit): RunPreflight includes a code_search_index check with the
// detail "built" when the run has an index.
//
// Verifies: 16-REQ-7.1
func TestTS16_24_PreflightReportsCodeSearchIndexBuilt(t *testing.T) {
	ws, g := newRepo(t, 0)
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
		ws, g := newRepo(t, 0)
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
