package specgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// Issue #223 (1): a name the directory rule refuses is refused at every
// check that sees it, before a phase is paid for: --name, submit_prd and the
// split.
func TestANameTheDirectoryRuleRefusesIsRefusedUpFront(t *testing.T) {
	ws := newWorkspace(t)
	a := newAuthor(t, "01", "test_feature")
	o := newOptions(ws, a)
	o.Name = "widget__cache"
	_, err := Run(context.Background(), o)
	var f *Failure
	if !errors.As(err, &f) || f.Category != "usage" || len(a.prdRequests) != 0 {
		t.Errorf("--name widget__cache: %v (phases=%d), want a usage refusal before any phase", err, len(a.prdRequests))
	}

	var sink prdSink
	tool := submitPRDTool(&sink, ws)
	submit := func(prd map[string]any) bool {
		b, _ := json.Marshal(prd)
		return tool.Execute(context.Background(), b).OK
	}
	base := map[string]any{"spec_name": "issuex_", "title": "Issuex", "body": "## Intent\n\nWhy.\n"}
	if submit(base) {
		t.Error("submit_prd accepted spec_name issuex_")
	}
	if err := checkSplit("issuex_core", []SplitScope{{Name: "issuex_core", Scope: "a"}, {Name: "issuex_", Scope: "b"}}); err == nil {
		t.Error("checkSplit accepted the scope name issuex_")
	}
}

// Issue #223 (2): a PRD whose intent heading afspec cannot hash is refused by
// submit_prd, where the model can fix it, not found at activation.
func TestSubmitPRDRefusesAnIntentHeadingActivationWouldRefuse(t *testing.T) {
	var sink prdSink
	tool := submitPRDTool(&sink, newWorkspace(t))
	b, _ := json.Marshal(map[string]any{"spec_name": "widget", "title": "Widget", "body": "## intent\n\nWhy.\n"})
	if res := tool.Execute(context.Background(), b); res.OK {
		t.Error("submit_prd accepted '## intent'")
	}
}

// Issue #223 (4): one unreadable spec directory is warned about; the specs
// that can be read are still the landscape every phase is shown.
func TestAnUnreadableSpecDirDoesNotHideTheOthers(t *testing.T) {
	ws := newWorkspace(t)
	specs := filepath.Join(ws.Root, ".specs")
	good := filepath.Join(specs, "01_test_feature")
	if err := os.MkdirAll(good, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"prd.md", "requirements.json", "test_spec.json", "tasks.json"} {
		b, err := os.ReadFile(filepath.Join("..", "testdata", "valid_spec", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(good, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(specs, "07_scratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := newAuthor(t, "08", "test_feature")
	o := newOptions(ws, a)
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	land := a.prdRequests[0].Landscape
	if len(land) != 1 || land[0].SpecID != "01" {
		t.Errorf("landscape = %+v, want the readable 01 spec", land)
	}
	warned := false
	for _, w := range o.Run.Warnings() {
		warned = warned || w.Code == toolio.WarnSpecsDirUnreadable
	}
	if !warned {
		t.Error("the unreadable directory was not warned about")
	}
}

// Issue #223 (smaller): the package's number is reserved on disk when it is
// allocated, so a run that starts while this one's phases run cannot take it.
func TestTheSpecNumberIsReservedWhileThePhasesRun(t *testing.T) {
	ws := newWorkspace(t)
	a := newAuthor(t, "01", "test_feature")
	reserved := false
	a.onGenerate = func(req artifactRequest) {
		dir := filepath.Join(ws.Root, ".specs", req.SpecID+"_"+req.SpecName)
		_, err := os.Stat(dir)
		reserved = reserved || err == nil
	}
	if _, err := Run(context.Background(), newOptions(ws, a)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !reserved {
		t.Error("the package directory did not exist while the generation phases ran")
	}

	// A run that fails before writing gives the number back.
	ws2 := newWorkspace(t)
	b := newAuthor(t, "01", "test_feature")
	b.artifactErr["tasks"] = errors.New("budget exceeded")
	if _, err := Run(context.Background(), newOptions(ws2, b)); err == nil {
		t.Fatal("want an error")
	}
	if entries, _ := os.ReadDir(filepath.Join(ws2.Root, ".specs")); len(entries) != 0 {
		t.Errorf("the reservation was left behind: %v", entries)
	}
}

// Issue #223 (smaller): runs reserving at the same time each get a number of
// their own.
func TestConcurrentReservationsGetDistinctNumbers(t *testing.T) {
	specs := filepath.Join(t.TempDir(), ".specs")
	var mu sync.Mutex
	var ids []string
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := reserveSpecDir(specs, fmt.Sprintf("spec_%c", 'a'+i), 0)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			ids = append(ids, id)
			mu.Unlock()
		}()
	}
	wg.Wait()
	slices.Sort(ids)
	if len(slices.Compact(slices.Clone(ids))) != 8 {
		t.Errorf("ids = %v, want 8 distinct numbers", ids)
	}
}

// Issue #223 (smaller): a dry run writes no package, so later scopes are not
// told to read packages that do not exist, next[] suggests no impl on a
// missing directory, and every spec_package artifact is marked dry_run.
func TestADryRunReportsItsPackagesAsHypothetical(t *testing.T) {
	ws := newWorkspace(t)
	a := splitAuthor(t)
	o := fileOptions(ws, a)
	o.DryRun = true
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, req := range a.prdRequests[1:] {
		if len(req.Landscape) != 0 {
			t.Errorf("a later scope was shown dry-run packages as existing: %+v", req.Landscape)
		}
	}
	for _, n := range got.Next() {
		if n.Tool == "impl" {
			t.Errorf("next suggests impl on %s, which a dry run did not write", n.Input)
		}
	}
	arts := got.Artifacts()
	if len(arts) == 0 {
		t.Fatal("no artifacts")
	}
	for _, art := range arts {
		if art.Kind == toolio.ArtifactSpecPackage && !art.DryRun {
			t.Errorf("artifact %+v is not marked dry_run", art)
		}
	}
}

// Issue #223 (smaller): a dry run, and --preflight --dry-run, still report
// another input's plan.
func TestADryRunStillLooksAtTheSplitPlans(t *testing.T) {
	ws := newWorkspace(t)
	specs := filepath.Join(ws.Root, ".specs")
	other := newSplitPlan(toolio.Input{Kind: toolio.KindFile, Origin: "docs/other.md", Body: "other"},
		[]SplitScope{{Name: "other_core", Scope: "x"}, {Name: "other_rest", Scope: "y"}})
	if err := other.save(specs); err != nil {
		t.Fatal(err)
	}
	o := newOptions(ws, newAuthor(t, "01", "test_feature"))
	o.DryRun = true
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if w := strings.Join(toolio.WarningMessages(o.Run.Warnings()), "\n"); !strings.Contains(w, "docs/other.md") {
		t.Errorf("a dry run did not report the other input's plan: %q", w)
	}

	mine := newSplitPlan(fileOptions(ws, nil).Input, threeScopes())
	if err := mine.save(specs); err != nil {
		t.Fatal(err)
	}
	po := fileOptions(ws, newAuthor(t, "01", "widget_core"))
	po.DryRun = true
	po.Run = toolio.NewRun("spec", "test")
	if _, err := os.Stat(other.Path(specs)); err != nil {
		t.Fatal(err)
	}
	if _, err := RunPreflight(context.Background(), po); err != nil {
		t.Fatalf("RunPreflight: %v", err)
	}
	if w := strings.Join(toolio.WarningMessages(po.Run.Warnings()), "\n"); !strings.Contains(w, "docs/other.md") {
		t.Errorf("--preflight --dry-run did not report the other input's plan: %q", w)
	}
}

// Issue #223 (smaller): a scope is recorded in the plan as soon as its
// package is on disk, before anything after the write, so a run killed then
// does not write the package a second time.
func TestAScopeIsRecordedOnceItsPackageIsWritten(t *testing.T) {
	ws := newWorkspace(t)
	specs := filepath.Join(ws.Root, ".specs")
	o := fileOptions(ws, splitAuthor(t))
	issue := issuex.IssueRef{Repo: issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"}, Number: 7}
	o.Input = toolio.Input{Kind: toolio.KindIssue, Origin: issue.URL(), Issue: &issue, Body: o.Input.Body}
	o.Comment = true
	// Each comment is posted after its own scope's package is written; by
	// then the plan must already record that scope.
	calls, recorded := 0, 0
	o.Forge = &hookForge{mockForgeClient: mockForgeClient{authenticated: true, commentURL: "u"}, hook: func() {
		calls++
		matches, _ := filepath.Glob(filepath.Join(specs, "*"+SplitPlanSuffix))
		if len(matches) != 1 {
			return
		}
		if p, err := loadSplitPlan(matches[0]); err == nil && p.Scopes[calls-1].Written() {
			recorded++
		}
	}}
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 3 || recorded != 3 {
		t.Errorf("comments=%d, scopes recorded before their comment=%d; want every scope recorded once written", calls, recorded)
	}
}

type hookForge struct {
	mockForgeClient
	hook func()
}

func (f *hookForge) AddComment(ctx context.Context, ref issuex.IssueRef, body string) (string, error) {
	f.hook()
	return f.mockForgeClient.AddComment(ctx, ref, body)
}

// Issue #223 (smaller): --comment on a package that does not validate is not
// skipped silently.
func TestACommentSkippedForAnInvalidPackageIsWarned(t *testing.T) {
	ws := newWorkspace(t)
	a := newAuthor(t, "01", "test_feature")
	a.skipStepValidation = true
	dropOneOwnedTest(a.artifacts["tasks"])
	o := newOptions(ws, a)
	issue := issuex.IssueRef{Repo: issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"}, Number: 7}
	o.Input = toolio.Input{Kind: toolio.KindIssue, Issue: &issue, Body: "x"}
	o.Comment = true
	forge := &mockForgeClient{authenticated: true}
	o.Forge = forge
	_, _ = Run(context.Background(), o)
	warned := false
	for _, w := range o.Run.Warnings() {
		warned = warned || (w.Code == toolio.WarnCommentNotPosted && strings.Contains(w.Message, "validate"))
	}
	if !warned || forge.commentCalls != 0 {
		t.Errorf("warned=%v calls=%d, want a comment_not_posted warning and no comment", warned, forge.commentCalls)
	}
}

// Issue #223 (smaller): a --total-budget stop in a split names the scope it
// stopped on as failed, like every other stop.
func TestABudgetStopMarksTheScopeItStoppedOn(t *testing.T) {
	ws := newWorkspace(t)
	a := splitAuthor(t)
	a.prdCost = 3
	o := fileOptions(ws, a)
	o.TotalBudgetUSD = 2
	got, err := Run(context.Background(), o)
	if err == nil {
		t.Fatal("want a budget stop")
	}
	if len(got.Split) != 3 || got.Split[1].Status != ScopeFailed {
		t.Errorf("split = %+v, want scope 2 failed", got.Split)
	}
}

// Issue #223 (smaller): the requirements phase ends only by its tool, so it
// is not told to answer in prose; and every prompt states the format's task
// limit.
func TestThePromptsAgreeWithTheFormat(t *testing.T) {
	req, err := templates.ReadFile("templates/generation_user_requirements.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(req), "say so in your response") {
		t.Error("the requirements prompt asks for a prose answer the phase cannot end with")
	}
	prd, err := templates.ReadFile("templates/prd_system.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(prd), "at most 8 tasks") || !strings.Contains(string(prd), "at most 12 tasks") {
		t.Error("the PRD prompt's task limit is not the format's 12")
	}
}
