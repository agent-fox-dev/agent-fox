package agentspec

import (
	"context"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/provider/faux"

	"github.com/agent-fox-dev/agentfox/afspec"
)

const (
	genSpecID   = "01"
	genSpecName = "widget_service"
)

func TestGenerateArtifactsProducesAllThree(t *testing.T) {
	p := generationScript(genSpecID, genSpecName)

	got, err := newFauxAgent(p).GenerateArtifacts(
		context.Background(), "# Widget service", genSpecID, genSpecName)
	if err != nil {
		t.Fatalf("GenerateArtifacts: %v", err)
	}
	for _, step := range afspec.GenerationSteps {
		if got[string(step)] == nil {
			t.Errorf("no %s artifact was returned", step)
		}
	}
}

func TestGenerationIsSequentialInTheOrderTheFormatMandates(t *testing.T) {
	// §12.1: requirements, then test_spec, then tasks. The order is what lets
	// each step see the complete artifact before it, and the previous format's
	// concurrent test_spec/tasks pass is the reason the rule exists.
	p := generationScript(genSpecID, genSpecName)
	if _, err := newFauxAgent(p).GenerateArtifacts(
		context.Background(), "# PRD", genSpecID, genSpecName); err != nil {
		t.Fatal(err)
	}
	if p.Calls() != len(afspec.GenerationSteps) {
		t.Fatalf("the provider saw %d calls, want %d", p.Calls(), len(afspec.GenerationSteps))
	}
	for i, step := range afspec.GenerationSteps {
		names := toolNamesOf(t, p, i)
		if len(names) != 1 || names[0] != ArtifactToolName(step) {
			t.Errorf("call %d declared %v, want only %s", i, names, ArtifactToolName(step))
		}
	}
}

func TestTheTaskStepSeesEveryTestID(t *testing.T) {
	// A generator that cannot see a test ID cannot own it, which is how the
	// previous format ended up with specs whose edge-case and property tests
	// belonged to no task at all.
	p := generationScript(genSpecID, genSpecName)
	if _, err := newFauxAgent(p).GenerateArtifacts(
		context.Background(), "# PRD", genSpecID, genSpecName); err != nil {
		t.Fatal(err)
	}
	tasksPrompt := userTextOf(t, p, 2)
	for _, id := range []string{"TS-01-1", "TS-01-2", "TS-01-3"} {
		if !strings.Contains(tasksPrompt, id) {
			t.Errorf("the tasks prompt does not carry test %s", id)
		}
	}
}

func TestTheTestStepSeesTheFullRequirements(t *testing.T) {
	p := generationScript(genSpecID, genSpecName)
	if _, err := newFauxAgent(p).GenerateArtifacts(
		context.Background(), "# PRD", genSpecID, genSpecName); err != nil {
		t.Fatal(err)
	}
	testPrompt := userTextOf(t, p, 1)
	for _, want := range []string{"01-REQ-1.1", "01-REQ-1.2", "01-PATH-1"} {
		if !strings.Contains(testPrompt, want) {
			t.Errorf("the test_spec prompt does not carry %s", want)
		}
	}
}

func TestAnInvalidArtifactIsReturnedToTheModelAsAToolError(t *testing.T) {
	// The repair path, end to end. The rejected submission never becomes a
	// second conversation assembled by hand: the loop appends the tool error
	// to the transcript the model is already holding, so the system prompt and
	// the tool schema are byte-identical across the two attempts and the
	// provider's cache prefix survives the repair.
	broken := v2TestSpecArtifact(genSpecID, genSpecName)
	tests := broken["tests"].([]any)
	tests[0].(map[string]any)["verifies"] = []any{genSpecID + "-REQ-9.9"} // no such criterion

	p := faux.New(
		toolCallTurn("r", ArtifactToolName(afspec.StepRequirements),
			v2RequirementsArtifact(genSpecID, genSpecName)),
		toolCallTurn("t1", ArtifactToolName(afspec.StepTestSpec), broken),
		toolCallTurn("t2", ArtifactToolName(afspec.StepTestSpec),
			v2TestSpecArtifact(genSpecID, genSpecName)),
		toolCallTurn("k", ArtifactToolName(afspec.StepTasks),
			v2TasksArtifact(genSpecID, genSpecName)),
	)

	got, err := newFauxAgent(p).GenerateArtifacts(
		context.Background(), "# PRD", genSpecID, genSpecName)
	if err != nil {
		t.Fatalf("GenerateArtifacts: %v", err)
	}
	if got["test_spec"] == nil {
		t.Fatal("the repaired test_spec was not returned")
	}
	if p.Calls() != 4 {
		t.Errorf("the provider saw %d calls, want 4 (the rejection costs one)", p.Calls())
	}

	// The rejection has to reach the model, and it has to name the rule.
	repair := toolResultTextOf(t, p, 2)
	if !strings.Contains(repair, "validation error") {
		t.Errorf("the second test_spec turn does not carry the validation failure:\n%s", repair)
	}
	if !strings.Contains(repair, "REQ-9.9") {
		t.Errorf("the rejection does not name what was wrong:\n%s", repair)
	}
}

func TestASystemPromptIsIdenticalAcrossARepair(t *testing.T) {
	// The reason the repair goes through the loop rather than through a
	// hand-assembled transcript: the cached prefix is only worth anything if
	// it does not change between the attempt and its correction.
	broken := v2TestSpecArtifact(genSpecID, genSpecName)
	broken["tests"].([]any)[0].(map[string]any)["verifies"] = []any{"01-REQ-9.9"}

	p := faux.New(
		toolCallTurn("r", ArtifactToolName(afspec.StepRequirements),
			v2RequirementsArtifact(genSpecID, genSpecName)),
		toolCallTurn("t1", ArtifactToolName(afspec.StepTestSpec), broken),
		toolCallTurn("t2", ArtifactToolName(afspec.StepTestSpec),
			v2TestSpecArtifact(genSpecID, genSpecName)),
		toolCallTurn("k", ArtifactToolName(afspec.StepTasks),
			v2TasksArtifact(genSpecID, genSpecName)),
	)
	if _, err := newFauxAgent(p).GenerateArtifacts(
		context.Background(), "# PRD", genSpecID, genSpecName); err != nil {
		t.Fatal(err)
	}
	if a, b := systemPromptOf(t, p, 1), systemPromptOf(t, p, 2); a != b {
		t.Error("the system prompt changed between an attempt and its repair, which invalidates the cache prefix")
	}
}

func TestGenerationStopsAtTheFirstStepThatNeverValidates(t *testing.T) {
	// §12.2: an invalid spec is a failed generation, not a partial result.
	broken := v2RequirementsArtifact(genSpecID, genSpecName)
	delete(broken, "requirements")

	turns := make([]faux.Turn, 0, 8)
	for i := 0; i < 8; i++ {
		turns = append(turns, toolCallTurn("r", ArtifactToolName(afspec.StepRequirements), broken))
	}
	p := faux.New(turns...)

	agent := NewSpecAgentWith("STANDARD", "", withMaxTurns(fauxRun(p), 3))
	got, err := agent.GenerateArtifacts(context.Background(), "# PRD", genSpecID, genSpecName)
	if err == nil {
		t.Fatal("expected a failed generation")
	}
	if got != nil {
		t.Errorf("a failed generation returned a partial result: %v", got)
	}
	if !strings.Contains(err.Error(), "requirements") {
		t.Errorf("the error does not name the step that failed: %v", err)
	}
}

func TestAFailedStepStopsTheOnesAfterIt(t *testing.T) {
	broken := v2RequirementsArtifact(genSpecID, genSpecName)
	delete(broken, "requirements")

	turns := make([]faux.Turn, 0, 4)
	for i := 0; i < 4; i++ {
		turns = append(turns, toolCallTurn("r", ArtifactToolName(afspec.StepRequirements), broken))
	}
	p := faux.New(turns...)

	var produced []string
	agent := NewSpecAgentWith("STANDARD", "", withMaxTurns(fauxRun(p), 2))
	_, err := agent.GenerateArtifacts(context.Background(), "# PRD", genSpecID, genSpecName,
		WithOnArtifact(func(name string, _ any) { produced = append(produced, name) }))
	if err == nil {
		t.Fatal("expected a failed generation")
	}
	if len(produced) != 0 {
		t.Errorf("artifacts were emitted after the first step failed: %v", produced)
	}
}

func TestGenerateArtifactsHonoursContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := generationScript(genSpecID, genSpecName)
	if _, err := newFauxAgent(p).GenerateArtifacts(ctx, "# PRD", genSpecID, genSpecName); err == nil {
		t.Fatal("expected a cancellation error")
	}
}

func TestACallbackPanicBecomesAnError(t *testing.T) {
	p := generationScript(genSpecID, genSpecName)
	_, err := newFauxAgent(p).GenerateArtifacts(context.Background(), "# PRD", genSpecID, genSpecName,
		WithOnArtifact(func(string, any) { panic("boom") }))
	if err == nil {
		t.Fatal("expected the panic to surface as an error")
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Errorf("the error does not say what happened: %v", err)
	}
}

func TestNoCallbackDoesNotPanic(t *testing.T) {
	p := generationScript(genSpecID, genSpecName)
	if _, err := newFauxAgent(p).GenerateArtifacts(
		context.Background(), "# PRD", genSpecID, genSpecName); err != nil {
		t.Fatal(err)
	}
}

// ------------------------------------------------------- validation itself

func TestValidateArtifactContentAcceptsTheValidArtifacts(t *testing.T) {
	partial := afspec.PartialSpec{SpecID: genSpecID, SpecName: genSpecName}
	for _, step := range afspec.GenerationSteps {
		if _, err := validateArtifactContent(v2Artifact(step, genSpecID, genSpecName), step, &partial); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}
}

func TestValidateArtifactContentReportsTheRuleThatFailed(t *testing.T) {
	// The message is the repair instruction the model reads, so naming the
	// rule is the whole of its value.
	partial := afspec.PartialSpec{SpecID: genSpecID, SpecName: genSpecName}
	if _, err := validateArtifactContent(
		v2RequirementsArtifact(genSpecID, genSpecName), afspec.StepRequirements, &partial); err != nil {
		t.Fatal(err)
	}
	broken := v2TestSpecArtifact(genSpecID, genSpecName)
	broken["tests"].([]any)[0].(map[string]any)["verifies"] = []any{"01-REQ-9.9"}

	_, err := validateArtifactContent(broken, afspec.StepTestSpec, &partial)
	if err == nil {
		t.Fatal("expected a validation failure")
	}
	if !strings.Contains(err.Error(), "validation error") {
		t.Errorf("the message does not read as a validation failure: %v", err)
	}
}

func TestAFailedStepLeavesThePartialSpecUntouched(t *testing.T) {
	// A rejected artifact must not become the state the next attempt is
	// validated against, or a repair would be judged against the thing it is
	// repairing.
	partial := afspec.PartialSpec{SpecID: genSpecID, SpecName: genSpecName}
	broken := v2RequirementsArtifact(genSpecID, genSpecName)
	delete(broken, "requirements")

	if _, err := validateArtifactContent(broken, afspec.StepRequirements, &partial); err == nil {
		t.Fatal("expected a validation failure")
	}
	if partial.Requirements != nil {
		t.Error("the rejected artifact was recorded in the partial spec")
	}
}

// withMaxTurns is how a test bounds the repair budget of one phase.
func withMaxTurns(o RunOptions, n int) RunOptions {
	o.MaxTurns = n
	return o
}
