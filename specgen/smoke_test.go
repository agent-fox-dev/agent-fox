package specgen

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/repomap"
)

// smokeRepo is a real git repository holding Go source files with
// declarations, so the map has something to show.
func smokeRepo(t *testing.T) *tools.Workspace {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	files := map[string]string{
		"go.mod":                        "module example.com/x\n\ngo 1.26\n",
		"internal/widget/model.go":      "package widget\n\ntype Widget struct{}\n\nfunc NewWidget() *Widget { return nil }\n",
		"internal/widget/store.go":      "package widget\n\ntype Store interface{}\n\nfunc (w *Widget) Save() {}\n",
		"internal/gadget/gadget.go":     "package gadget\n\ntype Gadget struct{}\n",
		"cmd/app/main.go":               "package main\n\nfunc main() {}\n",
		"internal/widget/model_test.go": "package widget\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := tools.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

// smokePRDArgs is a submit_prd payload carrying the fixture's PRD body, which
// the package's validation accepts.
func smokePRDArgs(t *testing.T, name string, split ...string) map[string]any {
	t.Helper()
	_, body := loadFixture(t, "01", name)
	args := map[string]any{"spec_name": name, "title": "Test Feature", "body": body}
	if len(split) > 0 {
		var scopes []map[string]any
		for _, s := range split {
			scopes = append(scopes, map[string]any{"name": s, "scope": "Covers " + s + "."})
		}
		args["recommended_split"] = scopes
	}
	return args
}

// smokeGenerationTurns scripts the three generation phases of one package.
func smokeGenerationTurns(t *testing.T, id, name string) []faux.Turn {
	t.Helper()
	artifacts, _ := loadFixture(t, id, name)
	var turns []faux.Turn
	for _, step := range afspec.GenerationSteps {
		turns = append(turns, toolCall("g-"+id+"-"+string(step), ArtifactToolName(step), artifacts[step]))
	}
	return turns
}

// requestUserText is the text of a request's first user message: the phase's
// user prompt as it reached the wire.
func requestUserText(req core.Request) string {
	for _, m := range req.Messages {
		um, ok := m.(core.UserMessage)
		if !ok {
			continue
		}
		var b strings.Builder
		for _, blk := range um.Content {
			if tb, ok := blk.(core.TextBlock); ok {
				b.WriteString(tb.Text)
			}
		}
		return b.String()
	}
	return ""
}

// mapSection is the repository map block of a prompt: the heading, the
// opening sentence and the fenced map, or "" when the prompt has none. What
// follows the block differs from phase to phase and is not part of it.
func mapSection(prompt string) string {
	i := strings.Index(prompt, "## Repository map")
	if i < 0 {
		return ""
	}
	open := strings.Index(prompt[i:], "```\n")
	if open < 0 {
		return prompt[i:]
	}
	body := i + open + len("```\n")
	closing := strings.Index(prompt[body:], "```")
	if closing < 0 {
		return prompt[i:]
	}
	return prompt[i : body+closing+len("```")]
}

// TS-14-38 (smoke): --repo-map-tokens 0 disables the map. Build returns ""
// without walking, no phase's user prompt has a map block, and the PRD prompt
// is byte-identical to the template with no map placeholder.
//
// Verifies: 14-PATH-3, 14-REQ-1.2, 14-REQ-6.3
//
// Real components: repomap.Build, prdUserPrompt, generationUserPrompt,
// agentAuthor, agentrun.Phase and Runner. Only the model is scripted.
func TestTS14_38_BudgetZeroDisablesTheMapAndKeepsPromptsIdentical(t *testing.T) {
	ws := smokeRepo(t)

	// Build with a budget of 0 returns before it walks: a cancelled context
	// would fail a walk, and it does not fail this.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if m, err := repomap.Build(cancelled, ws, 0, nil); m != "" || err != nil {
		t.Fatalf("Build with budget 0 = (%q, %v), want (\"\", nil) without walking", m, err)
	}

	turns := []faux.Turn{toolCall("p1", ToolSubmitPRD, smokePRDArgs(t, "test_feature"))}
	turns = append(turns, smokeGenerationTurns(t, "01", "test_feature")...)
	runner, prov := fauxRunner(t, ws, turns...)

	o := newOptions(ws, &agentAuthor{runner: runner, ws: ws})
	o.RepoMapTokens = 0
	var builds int
	var built string
	o.buildMap = func(ctx context.Context, w *tools.Workspace, b int, in []string) (string, error) {
		builds++
		m, err := repomap.Build(ctx, w, b, in)
		built = m
		return m, err
	}

	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if built != "" {
		t.Errorf("the map for budget 0 = %q, want empty", built)
	}
	reqs := prov.Requests()
	if want := 1 + len(afspec.GenerationSteps); len(reqs) != want {
		t.Fatalf("%d phases reached the model, want %d", len(reqs), want)
	}
	for i, req := range reqs {
		prompt := requestUserText(req)
		if strings.Contains(prompt, "## Repository map") || strings.Contains(prompt, "{{") {
			t.Errorf("phase %d has a map block or an unfilled placeholder:\n%s", i, prompt)
		}
	}

	// The PRD prompt equals the template without the placeholder, filled the
	// way the pipeline fills it.
	want := fill(strings.ReplaceAll(template("prd_user.md"), "{{repo_map_block}}", ""), map[string]string{
		"root": ws.Root, "source_kind": "text", "source_origin": "argument", "input": o.Input.Body,
		"context_block": "", "project_block": project.DetectProfile(ws.Root).LanguageBlock(),
		"landscape_block": "", "steering_block": "", "split_block": "",
	})
	if got := requestUserText(reqs[0]); got != want {
		t.Errorf("the PRD prompt differs from the one built without the map:\n got: %q\nwant: %q", got, want)
	}
}

// TS-14-40 (smoke): spec builds the map once and every PRD and generation
// phase of both scopes of a split carries the same map block.
//
// Verifies: 14-PATH-5, 14-REQ-8.2
//
// Real components: repomap.Build, prdUserPrompt, generationUserPrompt,
// agentAuthor, agentrun.Phase and Runner. Only the model is scripted.
func TestTS14_40_SpecReusesTheMapAcrossAllScopesOfASplit(t *testing.T) {
	ws := smokeRepo(t)

	// The PRD phase recommends a split into two scopes; scope 2 gets its own
	// PRD phase, then its own three generation phases.
	turns := []faux.Turn{toolCall("p1", ToolSubmitPRD, smokePRDArgs(t, "widget_core", "widget_core", "widget_gadget"))}
	turns = append(turns, smokeGenerationTurns(t, "01", "widget_core")...)
	turns = append(turns, toolCall("p2", ToolSubmitPRD, smokePRDArgs(t, "widget_gadget")))
	turns = append(turns, smokeGenerationTurns(t, "02", "widget_gadget")...)
	runner, prov := fauxRunner(t, ws, turns...)

	o := fileOptions(ws, &agentAuthor{runner: runner, ws: ws})
	o.RepoMapTokens = 6000
	o.Input.Body = "widgets: a model in internal/widget/model.go, and a gadget"
	var builds, budget int
	var built string
	o.buildMap = func(ctx context.Context, w *tools.Workspace, b int, in []string) (string, error) {
		builds++
		budget = b
		m, err := repomap.Build(ctx, w, b, in)
		built = m
		return m, err
	}

	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if builds != 1 || budget != 6000 {
		t.Errorf("Build ran %d times with budget %d, want once with 6000", builds, budget)
	}
	if !strings.Contains(built, "func NewWidget") || !strings.Contains(built, "gadget.go") {
		t.Fatalf("the real map lacks the repository's declarations:\n%s", built)
	}

	reqs := prov.Requests()
	if want := 2 * (1 + len(afspec.GenerationSteps)); len(reqs) != want {
		t.Fatalf("%d phases reached the model, want %d (a PRD and %d generation phases per scope)",
			len(reqs), want, len(afspec.GenerationSteps))
	}
	first := mapSection(requestUserText(reqs[0]))
	if first == "" {
		t.Fatalf("the first PRD prompt has no map block:\n%s", requestUserText(reqs[0]))
	}
	for i, req := range reqs {
		prompt := requestUserText(req)
		sec := mapSection(prompt)
		if sec != first {
			t.Errorf("phase %d's map block differs from the first phase's:\n%s", i, sec)
		}
		if !strings.Contains(sec, "The map below lists the repository") || !strings.Contains(sec, strings.TrimSpace(built)) {
			t.Errorf("phase %d's map block lacks the opening sentence or the built map", i)
		}
	}
}
