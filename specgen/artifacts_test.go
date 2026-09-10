package specgen

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
)

// The tests here drive the REAL generation phase — the agent loop, the tool
// schema converted from the format's own JSON Schema, the submit handler and
// its validation — with a scripted provider in the vendor's place.

func fauxRunner(t *testing.T, ws *tools.Workspace, turns ...faux.Turn) (*agentrun.Runner, *faux.Provider) {
	t.Helper()
	p := faux.New(turns...)
	r, err := agentrun.NewRunner(agentrun.Config{
		Model:         faux.Model(),
		Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
		Workspace:     ws,
		Bounds:        agentrun.Bounds{MaxTurns: 8, MaxBudgetUSD: 1, MaxAttempts: 1},
		SessionPrefix: "spec",
	})
	if err != nil {
		t.Fatal(err)
	}
	return r, p
}

func toolCall(id, name string, args any) faux.Turn {
	raw, err := json.Marshal(args)
	if err != nil {
		panic(err)
	}
	return faux.Turn{
		Blocks:     []core.ContentBlock{faux.FauxToolCall(id, name, string(raw))},
		StopReason: core.StopReasonToolUse,
	}
}

func goWorkspace(t *testing.T) *tools.Workspace {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := tools.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

// The repair loop IS the loop. A violation is a tool error the loop appends to
// the transcript the model is already holding, and the model corrects itself
// on the next turn — with the system prompt and tool schemas byte-identical
// across the attempt and its correction, so the provider's cache prefix
// survives the repair.
func TestARejectedArtifactIsRepairedInTheLoop(t *testing.T) {
	ws := goWorkspace(t)
	artifacts, _ := loadFixture(t, "01", "test_feature")

	// The first attempt cites a criterion that does not exist, breaking rule C3.
	broken := deepCopy(t, artifacts[afspec.StepTestSpec])
	first := broken["tests"].([]any)[0].(map[string]any)
	first["verifies"] = []any{"01-REQ-99.1"}

	runner, p := fauxRunner(t, ws,
		toolCall("c1", ArtifactToolName(afspec.StepTestSpec), broken),
		toolCall("c2", ArtifactToolName(afspec.StepTestSpec), artifacts[afspec.StepTestSpec]),
	)

	// Requirements have to be in place for the test-spec step's rules to have
	// anything to check against.
	partial := afspec.PartialSpec{SpecID: "01", SpecName: "test_feature"}
	if err := validateArtifactContent(artifacts[afspec.StepRequirements], afspec.StepRequirements, &partial); err != nil {
		t.Fatal(err)
	}

	a := &agentAuthor{runner: runner}
	got, res, err := a.GenerateArtifact(context.Background(), artifactRequest{
		Step: afspec.StepTestSpec, SpecID: "01", SpecName: "test_feature",
		Root: ws.Root, PRD: "## Intent\n\nx\n", Partial: &partial,
	})
	if err != nil {
		t.Fatalf("GenerateArtifact: %v", err)
	}
	if got == nil || partial.TestSpec == nil {
		t.Fatal("no artifact was recorded")
	}
	if len(partial.TestSpec.Tests) == 0 || partial.TestSpec.Tests[0].Verifies[0] == "01-REQ-99.1" {
		t.Errorf("the rejected artifact was recorded: %+v", partial.TestSpec.Tests[0].Verifies)
	}
	if res.Turns < 2 {
		t.Errorf("Turns = %d, want the rejection and the correction", res.Turns)
	}

	// The rejection reached the model as a tool error naming the rule.
	reqs := p.Requests()
	if len(reqs) < 2 {
		t.Fatalf("requests = %d, want at least two", len(reqs))
	}
	if !strings.Contains(transcript(reqs[1]), "C3") {
		t.Errorf("the second request should carry the C3 violation:\n%s", transcript(reqs[1]))
	}
	// The system prompt is byte-identical across the attempt and its repair,
	// which is what keeps the provider's cache prefix alive.
	if blocksText(reqs[0].System) != blocksText(reqs[1].System) {
		t.Error("the system prompt changed between the attempt and the repair")
	}
}

// A rejected submission must leave the accumulated state untouched, so the
// next attempt validates against the same upstream artifacts as the one
// before it.
func TestARejectedArtifactDoesNotPoisonTheAccumulatedState(t *testing.T) {
	artifacts, _ := loadFixture(t, "01", "test_feature")
	partial := afspec.PartialSpec{SpecID: "01", SpecName: "test_feature"}
	if err := validateArtifactContent(artifacts[afspec.StepRequirements], afspec.StepRequirements, &partial); err != nil {
		t.Fatal(err)
	}
	before := partial.Requirements

	broken := deepCopy(t, artifacts[afspec.StepTestSpec])
	broken["tests"] = []any{}
	if err := validateArtifactContent(broken, afspec.StepTestSpec, &partial); err == nil {
		t.Fatal("an empty test list was accepted")
	}
	if partial.TestSpec != nil {
		t.Error("a rejected artifact was recorded")
	}
	if partial.Requirements != before {
		t.Error("the rejection disturbed the artifact before it")
	}
}

// The tasks step is the one with a project check on top of the format's
// rules: a plan whose test commands belong to another ecosystem is refused
// before the file is written, with the real commands in the message.
func TestTheTasksStepRefusesAnotherEcosystemsCommands(t *testing.T) {
	ws := goWorkspace(t)
	artifacts, _ := loadFixture(t, "01", "test_feature")

	partial := afspec.PartialSpec{SpecID: "01", SpecName: "test_feature"}
	for _, step := range []afspec.GenerationStep{afspec.StepRequirements, afspec.StepTestSpec} {
		if err := validateArtifactContent(artifacts[step], step, &partial); err != nil {
			t.Fatal(err)
		}
	}

	pythonic := deepCopy(t, artifacts[afspec.StepTasks])
	pythonic["test_commands"] = map[string]any{"all_tests": "pytest -q", "linter": "ruff check ."}

	runner, p := fauxRunner(t, ws,
		toolCall("c1", ArtifactToolName(afspec.StepTasks), pythonic),
		toolCall("c2", ArtifactToolName(afspec.StepTasks), artifacts[afspec.StepTasks]),
	)
	a := &agentAuthor{runner: runner}
	if _, _, err := a.GenerateArtifact(context.Background(), artifactRequest{
		Step: afspec.StepTasks, SpecID: "01", SpecName: "test_feature",
		Root: ws.Root, PRD: "## Intent\n\nx\n",
		Profile: DetectProfile(ws.Root), Partial: &partial,
	}); err != nil {
		t.Fatalf("GenerateArtifact: %v", err)
	}
	reqs := p.Requests()
	if len(reqs) < 2 {
		t.Fatalf("requests = %d", len(reqs))
	}
	second := transcript(reqs[1])
	for _, want := range []string{"pytest", "go test ./... -count=1"} {
		if !strings.Contains(second, want) {
			t.Errorf("the repair instruction is missing %q:\n%s", want, second)
		}
	}
	if partial.Tasks == nil || partial.Tasks.TestCommands.AllTests != "go test ./... -count=1" {
		t.Errorf("the accepted plan's commands = %+v", partial.Tasks.TestCommands)
	}
}

// The tool schema is converted from the format's own JSON Schema rather than
// re-authored, so the two cannot disagree.
func TestArtifactSchemaCoversEveryStep(t *testing.T) {
	for _, step := range afspec.GenerationSteps {
		s, err := ArtifactSchema(step)
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		if s == nil || len(s.Properties) == 0 {
			t.Fatalf("%s: empty schema", step)
		}
		// The property names the v2 test object uses are exactly the ones an
		// earlier converter deleted by name at every depth.
		if step == afspec.StepTestSpec {
			raw, err := json.Marshal(s)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{`"title"`, `"then"`} {
				if !strings.Contains(string(raw), want) {
					t.Errorf("the test schema lost the %s property", want)
				}
			}
		}
	}
	if _, err := ArtifactSchema(afspec.GenerationStep("nope")); err == nil {
		t.Error("an unknown step should be an error")
	}
}

func deepCopy(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// transcript renders a request's messages, so a test can assert on what the
// model was actually shown.
func transcript(req core.Request) string {
	var b strings.Builder
	for _, m := range req.Messages {
		switch v := m.(type) {
		case core.UserMessage:
			b.WriteString(v.Content.Text())
		case core.AssistantMessage:
			b.WriteString(v.Content.Text())
		case core.ToolResultMessage:
			b.WriteString(v.Content.Text())
		}
		b.WriteString("\n")
	}
	return b.String()
}

func blocksText(blocks []core.ContentBlock) string {
	var b strings.Builder
	for _, block := range blocks {
		if t, ok := block.(core.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// The model is never asked for the artifact's $schema — a tool schema cannot
// declare a property by that name — so the submit handler writes it, and a
// submission that leaves it out is accepted and stored with the right URI.
func TestTheSubmitHandlerWritesTheArtifactsSchemaField(t *testing.T) {
	ws := goWorkspace(t)
	artifacts, _ := loadFixture(t, "01", "test_feature")

	// What the model can actually submit: the fixture without the one field
	// the tool schema does not declare.
	submitted := deepCopy(t, artifacts[afspec.StepRequirements])
	delete(submitted, "$schema")

	runner, _ := fauxRunner(t, ws,
		toolCall("c1", ArtifactToolName(afspec.StepRequirements), submitted))

	partial := afspec.PartialSpec{SpecID: "01", SpecName: "test_feature"}
	a := &agentAuthor{runner: runner}
	got, _, err := a.GenerateArtifact(context.Background(), artifactRequest{
		Step: afspec.StepRequirements, SpecID: "01", SpecName: "test_feature",
		Root: ws.Root, PRD: "## Intent\n\nx\n", Partial: &partial,
	})
	if err != nil {
		t.Fatalf("GenerateArtifact: %v", err)
	}
	want := "https://agent-fox.dev/schemas/requirements.v2.json"
	if got["$schema"] != want {
		t.Errorf("the submitted content carries $schema = %v, want %q", got["$schema"], want)
	}
	if partial.Requirements == nil || partial.Requirements.Schema != want {
		t.Errorf("the recorded artifact carries $schema = %+v", partial.Requirements)
	}

	// The tool the model was shown does not declare the field at all, so
	// there is nothing for it to get wrong.
	s, err := ArtifactSchema(afspec.StepRequirements)
	if err != nil {
		t.Fatal(err)
	}
	if s.Properties["$schema"] != nil {
		t.Error("the tool schema declares $schema; a vendor rejects the whole request over it")
	}
}
