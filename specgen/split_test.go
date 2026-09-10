package specgen

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The tests below drive the real pipeline over an input the PRD phase reports
// as three specs' worth of work. The scripted author answers the first PRD
// phase with a split and every follow-on phase with the fixture under the
// planned name, so what is exercised is the pipeline's own bookkeeping: the
// plan, the numbering, the landscape, the resume.

func threeScopes() []SplitScope {
	return []SplitScope{
		{Name: "widget_core", Scope: "The widget model and its loader."},
		{Name: "widget_github", Scope: "The GitHub-backed widget store."},
		{Name: "widget_adopt", Scope: "Switching the tools over to the new store."},
	}
}

func splitAuthor(t *testing.T) *scriptedAuthor {
	t.Helper()
	a := newAuthor(t, "01", "widget_core")
	a.prd.RecommendedSplit = threeScopes()
	return a
}

func fileOptions(ws *tools.Workspace, a author) Options {
	o := newOptions(ws, a)
	o.Input = toolio.Input{Kind: toolio.KindFile, Origin: "docs/drafts/widgets.md",
		Body: "widgets: a model, a store, and the switch-over"}
	return o
}

func specDirs(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".specs"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestASplitInputWritesEveryScope(t *testing.T) {
	ws := newWorkspace(t)
	a := splitAuthor(t)

	got, err := Run(context.Background(), fileOptions(ws, a))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Three packages, numbered in the plan's order, each valid and active.
	want := []string{"01_widget_core", "02_widget_github", "03_widget_adopt"}
	if dirs := specDirs(t, ws.Root); strings.Join(dirs, ",") != strings.Join(want, ",") {
		t.Fatalf("spec root holds %v, want %v and no plan file", dirs, want)
	}
	for _, dir := range want {
		loaded, err := afspec.LoadSpec(filepath.Join(ws.Root, ".specs", dir))
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		if res := loaded.Validate(); !res.Valid || loaded.Status != "active" {
			t.Errorf("%s: valid=%v status=%s", dir, res.Valid, loaded.Status)
		}
	}

	// The first package is at the top level, the rest follow, and the plan
	// reports every scope done.
	if got.SpecDir != filepath.Join(".specs", "01_widget_core") {
		t.Errorf("SpecDir = %q", got.SpecDir)
	}
	if len(got.FollowOnSpecs) != 2 || got.FollowOnSpecs[0].SpecID != "02" || got.FollowOnSpecs[1].SpecID != "03" {
		t.Errorf("FollowOnSpecs = %+v", got.FollowOnSpecs)
	}
	if len(got.Split) != 3 {
		t.Fatalf("Split = %+v", got.Split)
	}
	for i, s := range got.Split {
		if s.Status != ScopeDone || s.SpecDir == "" || s.Name != threeScopes()[i].Name {
			t.Errorf("Split[%d] = %+v", i, s)
		}
	}
	if got.SplitPlan != "" {
		t.Errorf("SplitPlan = %q after a complete split", got.SplitPlan)
	}

	// One PRD phase for the whole input, then one per remaining scope, each
	// told which scope it writes and shown the packages before it.
	if len(a.prdRequests) != 3 {
		t.Fatalf("%d PRD phases", len(a.prdRequests))
	}
	if a.prdRequests[0].Split != nil {
		t.Error("the first PRD phase must see the whole input, not a scope")
	}
	third := a.prdRequests[2]
	if third.Split == nil || third.Split.Index != 2 || third.Split.Scope().Name != "widget_adopt" {
		t.Fatalf("third PRD request: %+v", third.Split)
	}
	if !third.Split.Plan.Scopes[1].Written() {
		t.Error("the third scope's PRD phase should see the second scope as written")
	}
	var names []string
	for _, m := range third.Landscape {
		names = append(names, m.SpecID+"_"+m.SpecName)
	}
	if strings.Join(names, ",") != "01_widget_core,02_widget_github" {
		t.Errorf("the third scope's landscape lists %v", names)
	}
	block := splitBlock(third.Split)
	for _, want := range []string{"`01_widget_core`", "`02_widget_github`", "**this PRD**",
		"`spec_name` is `widget_adopt`"} {
		if !strings.Contains(block, want) {
			t.Errorf("the scope block lacks %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "not yet written") {
		t.Errorf("nothing follows the last scope, so the block must not say so:\n%s", block)
	}

	// The first package's fields are at the top level of the JSON, as they
	// are for an undivided input.
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]any
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatal(err)
	}
	if shape["spec_dir"] != got.SpecDir || shape["spec_id"] != "01" {
		t.Errorf("the top level of the result should describe the first package: %v", shape["spec_dir"])
	}
	if _, ok := shape["follow_on_specs"].([]any); !ok {
		t.Errorf("follow_on_specs missing: %s", raw)
	}
	if _, ok := shape["split"].([]any); !ok {
		t.Errorf("split missing: %s", raw)
	}
}

// A run that stops on a scope leaves the plan behind, and the next run on the
// same input continues from that scope without re-deciding the split.
func TestAFailedScopeLeavesAPlanTheNextRunResumesFrom(t *testing.T) {
	ws := newWorkspace(t)
	a := splitAuthor(t)
	a.followOnErr["widget_github"] = errors.New("budget exceeded")

	got, err := Run(context.Background(), fileOptions(ws, a))
	if err == nil {
		t.Fatal("want an error from the second scope")
	}
	var f *Failure
	if !errors.As(err, &f) || f.Stage != "prd" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "scope 2 of 3 (widget_github)") {
		t.Errorf("the error should name the scope: %v", err)
	}
	if got == nil || got.SpecID != "01" || !got.Validation.Valid {
		t.Fatalf("the first package should be reported: %+v", got)
	}
	planPath := filepath.Join(ws.Root, ".specs", "widget_core"+SplitPlanSuffix)
	if _, err := os.Stat(planPath); err != nil {
		t.Fatalf("the plan should remain: %v", err)
	}
	if got.SplitPlan != filepath.Join(".specs", "widget_core"+SplitPlanSuffix) {
		t.Errorf("SplitPlan = %q", got.SplitPlan)
	}
	states := []string{got.Split[0].Status, got.Split[1].Status, got.Split[2].Status}
	if strings.Join(states, ",") != "done,failed,pending" {
		t.Errorf("scope states = %v", states)
	}

	// The next run, same input: no first PRD phase, the remaining two
	// scopes written, the plan gone.
	b := splitAuthor(t)
	o := fileOptions(ws, b)
	o.Name = "ignored_on_resume"
	got, err = Run(context.Background(), o)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(b.prdRequests) != 2 || b.prdRequests[0].Split == nil || b.prdRequests[0].Split.Index != 1 {
		t.Fatalf("the resumed run should start at scope 2 with the plan; requests: %d", len(b.prdRequests))
	}
	want := []string{"01_widget_core", "02_widget_github", "03_widget_adopt"}
	if dirs := specDirs(t, ws.Root); strings.Join(dirs, ",") != strings.Join(want, ",") {
		t.Fatalf("spec root holds %v, want %v", dirs, want)
	}
	if got.SpecID != "02" || len(got.FollowOnSpecs) != 1 || got.FollowOnSpecs[0].SpecID != "03" {
		t.Errorf("the resumed run wrote %s and %+v", got.SpecID, got.FollowOnSpecs)
	}
	if got.Split[0].Status != ScopeDone || got.Split[0].SpecID != "01" {
		t.Errorf("the earlier run's package should be reported as done: %+v", got.Split[0])
	}
	if got.SplitPlan != "" {
		t.Error("a finished split should have no plan")
	}
	if warnings := strings.Join(o.Run.Warnings(), "\n"); !strings.Contains(warnings, "--name") {
		t.Errorf("--name should be reported as ignored on resume; warnings: %q", warnings)
	}
}

// An edited file resumes by origin; the plan names the package whatever the
// model called it.
func TestResumeMatchesByOriginAndNamesFromThePlan(t *testing.T) {
	ws := newWorkspace(t)
	a := splitAuthor(t)
	a.followOnErr["widget_github"] = errors.New("api down")
	if _, err := Run(context.Background(), fileOptions(ws, a)); err == nil {
		t.Fatal("want the first run to stop")
	}

	b := splitAuthor(t)
	renamed := b.prd
	renamed.SpecName, renamed.Title = "github_store", "Renamed by the model"
	b.followOn["widget_github"] = renamed
	o := fileOptions(ws, b)
	o.Input.Body += "\n\nedited after the first run"
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if got.SpecName != "widget_github" || got.SpecDir != filepath.Join(".specs", "02_widget_github") {
		t.Errorf("the plan's name must win: %q at %q", got.SpecName, got.SpecDir)
	}
	warnings := strings.Join(o.Run.Warnings(), "\n")
	if !strings.Contains(warnings, "input changed") || !strings.Contains(warnings, `"github_store"`) {
		t.Errorf("warnings = %q", warnings)
	}
}

// A text input has no origin worth matching, so it resumes by content only.
func TestATextInputResumesByContentOnly(t *testing.T) {
	plan := newSplitPlan(toolio.Input{Kind: toolio.KindText, Origin: "argument", Body: "one"}, threeScopes())
	if ok, _ := plan.matches(toolio.Input{Kind: toolio.KindText, Origin: "argument", Body: "two"}); ok {
		t.Error("a different text matched by its shared origin")
	}
	if ok, changed := plan.matches(toolio.Input{Kind: toolio.KindText, Origin: "argument", Body: "one"}); !ok || changed {
		t.Error("the same text did not match")
	}
	file := newSplitPlan(toolio.Input{Kind: toolio.KindFile, Origin: "a.md", Body: "one"}, threeScopes())
	if ok, _ := file.matches(toolio.Input{Kind: toolio.KindFile, Origin: "b.md", Body: "two"}); ok {
		t.Error("another file matched")
	}
	if ok, changed := file.matches(toolio.Input{Kind: toolio.KindFile, Origin: "a.md", Body: "two"}); !ok || !changed {
		t.Error("the edited file did not resume by origin")
	}
}

// A package that does not validate is on disk and stops nothing; the run
// still fails, naming it, and the plan does not write it again.
func TestAnInvalidScopeDoesNotStopTheSplit(t *testing.T) {
	ws := newWorkspace(t)
	a := splitAuthor(t)
	a.breakScope = "widget_github"

	got, err := Run(context.Background(), fileOptions(ws, a))
	var f *Failure
	if !errors.As(err, &f) || f.Category != CategoryInvalid {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "02_widget_github") {
		t.Errorf("the error should name the invalid package: %v", err)
	}
	want := []string{"01_widget_core", "02_widget_github", "03_widget_adopt"}
	if dirs := specDirs(t, ws.Root); strings.Join(dirs, ",") != strings.Join(want, ",") {
		t.Fatalf("spec root holds %v, want %v", dirs, want)
	}
	states := []string{got.Split[0].Status, got.Split[1].Status, got.Split[2].Status}
	if strings.Join(states, ",") != "done,invalid,done" {
		t.Errorf("scope states = %v", states)
	}
	if got.FollowOnSpecs[0].Validation.Valid || got.FollowOnSpecs[0].Status == "active" {
		t.Errorf("the invalid package must be reported as such and not activated: %+v", got.FollowOnSpecs[0])
	}
	if got.SplitPlan != "" {
		t.Error("every scope exists, so the plan should be gone")
	}
}

// A dry run generates every scope, numbers them as a real run would, and
// leaves neither packages nor a plan behind.
func TestDryRunOfASplitWritesNothing(t *testing.T) {
	ws := newWorkspace(t)
	o := fileOptions(ws, splitAuthor(t))
	o.DryRun = true

	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws.Root, ".specs")); !os.IsNotExist(err) {
		t.Error("--dry-run created the spec root")
	}
	ids := []string{got.SpecID, got.FollowOnSpecs[0].SpecID, got.FollowOnSpecs[1].SpecID}
	if strings.Join(ids, ",") != "01,02,03" {
		t.Errorf("ids = %v", ids)
	}
	if got.SplitPlan != "" || len(got.Split) != 3 {
		t.Errorf("SplitPlan = %q, Split = %+v", got.SplitPlan, got.Split)
	}
}

// Someone else's unfinished split is reported and left alone.
func TestAPlanForAnotherInputIsLeftAlone(t *testing.T) {
	ws := newWorkspace(t)
	other := newSplitPlan(toolio.Input{Kind: toolio.KindFile, Origin: "docs/other.md", Body: "other"},
		[]SplitScope{{Name: "other_core", Scope: "x"}, {Name: "other_rest", Scope: "y"}})
	if err := other.save(filepath.Join(ws.Root, ".specs")); err != nil {
		t.Fatal(err)
	}

	o := newOptions(ws, newAuthor(t, "01", "test_feature"))
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.SpecID != "01" || len(got.Split) != 0 {
		t.Errorf("a fresh run was expected: %+v", got)
	}
	if _, err := os.Stat(other.Path(filepath.Join(ws.Root, ".specs"))); err != nil {
		t.Error("the other plan was removed")
	}
	if warnings := strings.Join(o.Run.Warnings(), "\n"); !strings.Contains(warnings, "docs/other.md") {
		t.Errorf("the stray plan should be reported: %q", warnings)
	}
}
