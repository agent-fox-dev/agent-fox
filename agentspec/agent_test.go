package agentspec

import (
	"context"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
)

// newFauxAgent builds a SpecAgent that talks to a scripted provider.
func newFauxAgent(p *faux.Provider) *SpecAgent {
	return NewSpecAgentWith("STANDARD", "", fauxRun(p))
}

// ------------------------------------------------------------------ assess

func TestAssessPRDReturnsTheSubmittedAssessment(t *testing.T) {
	p := faux.New(toolCallTurn("c1", ToolSubmitAssessment, validAssessment("needs_refinement")))

	got, err := newFauxAgent(p).AssessPRD(context.Background(), "# Widget service", "01_widgets")
	if err != nil {
		t.Fatalf("AssessPRD: %v", err)
	}
	if got.Quality != "needs_refinement" {
		t.Errorf("quality = %q, want needs_refinement", got.Quality)
	}
	if len(got.Gaps) != 1 || len(got.Questions) != 1 {
		t.Errorf("gaps=%v questions=%v; both should have survived the tool call", got.Gaps, got.Questions)
	}
}

func TestAssessPRDDeclaresItsSubmitToolAndNothingElse(t *testing.T) {
	// With no workspace the model has exactly one thing it can do. The old
	// pipeline forced that with ToolChoice "any", which is an Anthropic-wire
	// spelling; a single-tool declaration says the same thing on every wire.
	p := faux.New(toolCallTurn("c1", ToolSubmitAssessment, validAssessment("ready")))
	if _, err := newFauxAgent(p).AssessPRD(context.Background(), "# PRD", "01_x"); err != nil {
		t.Fatal(err)
	}
	names := toolNamesOf(t, p, 0)
	if len(names) != 1 || names[0] != ToolSubmitAssessment {
		t.Errorf("declared tools = %v, want only %s", names, ToolSubmitAssessment)
	}
}

func TestAssessPRDSendsTheSystemPromptAndThePRD(t *testing.T) {
	p := faux.New(toolCallTurn("c1", ToolSubmitAssessment, validAssessment("ready")))
	if _, err := newFauxAgent(p).AssessPRD(context.Background(), "PRD-MARKER-TEXT", "01_widgets"); err != nil {
		t.Fatal(err)
	}
	if sys := systemPromptOf(t, p, 0); sys == "" {
		t.Error("the assembled system prompt was empty")
	}
	user := userTextOf(t, p, 0)
	if !strings.Contains(user, "PRD-MARKER-TEXT") {
		t.Error("the PRD text did not reach the model")
	}
	if !strings.Contains(user, "01_widgets") {
		t.Error("the spec name did not reach the model")
	}
}

func TestAnInvalidQualityComesBackToTheModelAsAToolError(t *testing.T) {
	// This is the repair loop in its smallest form: the handler rejects, the
	// loop appends the rejection, the model corrects itself on the next turn.
	// No second conversation is assembled anywhere.
	p := faux.New(
		toolCallTurn("c1", ToolSubmitAssessment, validAssessment("excellent")),
		toolCallTurn("c2", ToolSubmitAssessment, validAssessment("ready")),
	)
	got, err := newFauxAgent(p).AssessPRD(context.Background(), "# PRD", "01_x")
	if err != nil {
		t.Fatalf("AssessPRD: %v", err)
	}
	if got.Quality != "ready" {
		t.Errorf("quality = %q, want the corrected value", got.Quality)
	}
	if p.Calls() != 2 {
		t.Errorf("the provider saw %d calls; the rejection should have produced a second turn", p.Calls())
	}
}

func TestAssessmentQualityIsRejectedWithTheListOfValidValues(t *testing.T) {
	if _, err := decodeAssessment([]byte(`{"quality":"excellent"}`)); err == nil {
		t.Fatal("expected a rejection")
	} else if !strings.Contains(err.Error(), "needs_refinement") {
		t.Errorf("the rejection does not name the valid values: %v", err)
	}
	if _, err := decodeAssessment([]byte(`{"summary":"x"}`)); err == nil {
		t.Fatal("expected a rejection for a missing quality")
	}
	for _, q := range []string{"ready", "needs_refinement", "incomplete"} {
		if _, err := decodeAssessment([]byte(`{"quality":"` + q + `"}`)); err != nil {
			t.Errorf("quality %q was rejected: %v", q, err)
		}
	}
}

func TestARunThatEndsWithoutSubmittingNamesTheStopReason(t *testing.T) {
	// A model that answers in prose instead of calling the tool, a model that
	// exhausted the turn budget and a model that blew the cost cap all leave
	// the caller with no assessment, and each wants a different response.
	p := faux.New(textTurn("The PRD looks fine to me."))
	_, err := newFauxAgent(p).AssessPRD(context.Background(), "# PRD", "01_x")
	if err == nil {
		t.Fatal("expected an error")
	}
	var agentErr *AgentError
	if !asAgentError(err, &agentErr) {
		t.Fatalf("error is not an *AgentError: %T", err)
	}
	if !strings.Contains(err.Error(), ToolSubmitAssessment) {
		t.Errorf("the error does not name the tool that was not called: %v", err)
	}
	if !strings.Contains(err.Error(), string(core.RunStopEndTurn)) {
		t.Errorf("the error does not name the stop reason: %v", err)
	}
	if !strings.Contains(err.Error(), "The PRD looks fine to me") {
		t.Errorf("the error does not quote what the model said instead: %v", err)
	}
}

func TestAssessPRDHonoursContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := faux.New(toolCallTurn("c1", ToolSubmitAssessment, validAssessment("ready")))
	if _, err := newFauxAgent(p).AssessPRD(ctx, "# PRD", "01_x"); err == nil {
		t.Fatal("expected a cancellation error")
	}
	if p.Calls() != 0 {
		t.Errorf("the provider was called %d times on a cancelled context", p.Calls())
	}
}

// ------------------------------------------------------------------ refine

func TestRefinePRDReturnsTheRewrittenPRDAndItsAssessment(t *testing.T) {
	p := faux.New(toolCallTurn("c1", ToolSubmitPRDUpdate, map[string]any{
		"updated_prd": "# Widget service\n\nNow with an error budget.",
		"assessment":  validAssessment("ready"),
	}))

	prd, assessment, err := newFauxAgent(p).RefinePRD(
		context.Background(), "# Widget service", map[string]string{"q1": "99.9%"}, Assessment{Quality: "needs_refinement"})
	if err != nil {
		t.Fatalf("RefinePRD: %v", err)
	}
	if !strings.Contains(prd, "error budget") {
		t.Errorf("the rewritten PRD did not come back: %q", prd)
	}
	if assessment.Quality != "ready" {
		t.Errorf("quality = %q, want ready", assessment.Quality)
	}
}

func TestRefinementCarriesThePRDAndTheAssessmentInOneCall(t *testing.T) {
	// Both used to have to appear in one response and a hand-written check
	// enforced it. Making them two fields of one tool means the schema says
	// it, and a model that submits only one is refused by its own tool call
	// rather than after the response has been parsed.
	p := faux.New(
		toolCallTurn("c1", ToolSubmitPRDUpdate, map[string]any{"updated_prd": "# Only the PRD"}),
		toolCallTurn("c2", ToolSubmitPRDUpdate, map[string]any{
			"updated_prd": "# Both this time",
			"assessment":  validAssessment("ready"),
		}),
	)
	prd, _, err := newFauxAgent(p).RefinePRD(context.Background(), "# PRD", nil, Assessment{})
	if err != nil {
		t.Fatalf("RefinePRD: %v", err)
	}
	if !strings.Contains(prd, "Both this time") {
		t.Errorf("the corrected submission did not win: %q", prd)
	}
}

func TestAnEmptyRewrittenPRDIsRefusedWithAnInstruction(t *testing.T) {
	// Silently writing an empty PRD over a real one is the worst outcome
	// available here, and the wording of the refusal is the whole repair
	// instruction the model gets.
	p := faux.New(
		toolCallTurn("c1", ToolSubmitPRDUpdate, map[string]any{
			"updated_prd": "   ", "assessment": validAssessment("ready"),
		}),
		toolCallTurn("c2", ToolSubmitPRDUpdate, map[string]any{
			"updated_prd": "# A real PRD", "assessment": validAssessment("ready"),
		}),
	)
	prd, _, err := newFauxAgent(p).RefinePRD(context.Background(), "# PRD", nil, Assessment{})
	if err != nil {
		t.Fatalf("RefinePRD: %v", err)
	}
	if prd != "# A real PRD" {
		t.Errorf("prd = %q, want the second submission", prd)
	}
}

func TestRefinePRDSendsTheAnswersAndThePreviousAssessment(t *testing.T) {
	p := faux.New(toolCallTurn("c1", ToolSubmitPRDUpdate, map[string]any{
		"updated_prd": "# x", "assessment": validAssessment("ready"),
	}))
	prev := Assessment{Quality: "needs_refinement", Summary: "PRIOR-SUMMARY-MARKER"}
	if _, _, err := newFauxAgent(p).RefinePRD(
		context.Background(), "# PRD", map[string]string{"ANSWER-KEY-MARKER": "yes"}, prev); err != nil {
		t.Fatal(err)
	}
	user := userTextOf(t, p, 0)
	for _, want := range []string{"ANSWER-KEY-MARKER", "PRIOR-SUMMARY-MARKER"} {
		if !strings.Contains(user, want) {
			t.Errorf("%q did not reach the model", want)
		}
	}
}

// ------------------------------------------------------------------ options

func TestAgentOptionsCompose(t *testing.T) {
	landscape := []map[string]any{{"spec_id": "01"}}
	interfaces := []map[string]any{{"name": "Store"}}
	var got string

	o := applyOptions([]AgentOption{
		WithSpecLandscape(landscape),
		WithDependentInterfaces(interfaces),
		WithProjectDir("/tmp/project"),
		WithOnArtifact(func(name string, _ any) { got = name }),
	})

	if len(o.specLandscape) != 1 || len(o.dependentInterfaces) != 1 {
		t.Error("the landscape or the interfaces were lost")
	}
	if o.projectDir != "/tmp/project" {
		t.Errorf("projectDir = %q", o.projectDir)
	}
	o.onArtifact("requirements", nil)
	if got != "requirements" {
		t.Errorf("the callback was not the one set: %q", got)
	}
}

func TestZeroAgentOptionsAreTheZeroValue(t *testing.T) {
	o := applyOptions(nil)
	if o.specLandscape != nil || o.dependentInterfaces != nil || o.projectDir != "" || o.onArtifact != nil {
		t.Errorf("applyOptions(nil) = %+v, want the zero value", o)
	}
}

func TestTheSpecLandscapeReachesTheModel(t *testing.T) {
	p := faux.New(toolCallTurn("c1", ToolSubmitAssessment, validAssessment("ready")))
	landscape := []map[string]any{{"spec_id": "07", "title": "LANDSCAPE-MARKER", "status": "active"}}
	if _, err := newFauxAgent(p).AssessPRD(
		context.Background(), "# PRD", "01_x", WithSpecLandscape(landscape)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(userTextOf(t, p, 0), "LANDSCAPE-MARKER") {
		t.Error("the spec landscape did not reach the model")
	}
}

func TestNewSpecAgentKeepsItsTierAndVariant(t *testing.T) {
	a := NewSpecAgent("ADVANCED", "extended")
	if a.modelTier != "ADVANCED" || a.modelVariant != "extended" {
		t.Errorf("NewSpecAgent kept %q/%q", a.modelTier, a.modelVariant)
	}
	if NewSpecAgent("SIMPLE").modelVariant != "" {
		t.Error("an omitted variant should stay empty")
	}
}

func asAgentError(err error, target **AgentError) bool {
	for err != nil {
		if ae, ok := err.(*AgentError); ok {
			*target = ae
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
