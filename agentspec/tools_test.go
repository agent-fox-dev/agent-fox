package agentspec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// exec runs a tool's Execute handler the way the loop would.
func exec(t *testing.T, tool core.Tool, args any) core.ToolResult {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Execute(context.Background(), raw)
}

func TestSubmitAssessmentTerminatesOnlyOnAcceptance(t *testing.T) {
	// Terminate is the batch vote of REQ-TOOL-13: it ends the run when the
	// submission was accepted, and a rejected one has to leave the run going
	// or the model never gets the chance to correct it.
	var got Assessment
	var done bool
	tool := submitAssessmentTool(&got, &done)

	res := exec(t, tool, validAssessment("ready"))
	if !res.OK || !res.Terminate {
		t.Errorf("an accepted assessment did not terminate the run: %+v", res)
	}
	if !done || got.Quality != "ready" {
		t.Errorf("the assessment was not captured: done=%v %+v", done, got)
	}

	var got2 Assessment
	var done2 bool
	res = exec(t, submitAssessmentTool(&got2, &done2), map[string]any{"quality": "excellent"})
	if res.OK || res.Terminate {
		t.Errorf("a rejected assessment terminated the run: %+v", res)
	}
	if done2 {
		t.Error("a rejected assessment was recorded as submitted")
	}
}

func TestARejectedSubmissionLeavesThePreviousValueAlone(t *testing.T) {
	got := Assessment{Quality: "ready", Summary: "kept"}
	done := true
	res := exec(t, submitAssessmentTool(&got, &done), map[string]any{"quality": "nonsense"})
	if res.OK {
		t.Fatal("expected a rejection")
	}
	if got.Summary != "kept" {
		t.Errorf("the rejected call overwrote the previous value: %+v", got)
	}
}

func TestSubmitPRDUpdateNeedsBothHalves(t *testing.T) {
	var prd string
	var a Assessment
	var done bool
	tool := submitPRDUpdateTool(&prd, &a, &done)

	if res := exec(t, tool, map[string]any{"updated_prd": "# PRD"}); res.OK {
		t.Error("a submission with no assessment was accepted")
	}
	if res := exec(t, tool, map[string]any{"assessment": validAssessment("ready")}); res.OK {
		t.Error("a submission with no PRD was accepted")
	}
	res := exec(t, tool, map[string]any{
		"updated_prd": "# PRD", "assessment": validAssessment("ready")})
	if !res.OK || !res.Terminate {
		t.Errorf("a complete submission was not accepted: %+v", res)
	}
	if prd != "# PRD" || a.Quality != "ready" || !done {
		t.Errorf("the submission was not captured: prd=%q %+v done=%v", prd, a, done)
	}
}

func TestAnEmptyPRDRejectionSaysWhatToSubmitInstead(t *testing.T) {
	// The wording is the repair instruction, so it has to say what a correct
	// submission looks like rather than only that this one was wrong.
	var prd string
	var a Assessment
	var done bool
	res := exec(t, submitPRDUpdateTool(&prd, &a, &done), map[string]any{
		"updated_prd": "  \n ", "assessment": validAssessment("ready")})
	if res.OK {
		t.Fatal("an empty PRD was accepted")
	}
	if !strings.Contains(res.Detail, "complete") {
		t.Errorf("the rejection does not say to submit the complete PRD: %q", res.Detail)
	}
}

func TestSubmitArtifactValidatesBeforeItAccepts(t *testing.T) {
	s, err := ArtifactSchema(afspec.StepRequirements)
	if err != nil {
		t.Fatal(err)
	}
	partial := afspec.PartialSpec{SpecID: "01", SpecName: "widget_service"}
	var content map[string]any
	var done bool
	tool := submitArtifactTool(afspec.StepRequirements, s, &partial, &content, &done)

	broken := v2RequirementsArtifact("01", "widget_service")
	delete(broken, "requirements")
	res := exec(t, tool, broken)
	if res.OK || res.Terminate {
		t.Errorf("an invalid artifact was accepted: %+v", res)
	}
	if partial.Requirements != nil {
		t.Error("a rejected artifact was recorded in the partial spec")
	}

	res = exec(t, tool, v2RequirementsArtifact("01", "widget_service"))
	if !res.OK || !res.Terminate {
		t.Errorf("a valid artifact was not accepted: %+v", res)
	}
	if !done || content == nil {
		t.Error("the accepted artifact was not captured")
	}
	if partial.Requirements == nil {
		t.Error("the accepted artifact was not recorded in the partial spec")
	}
}

func TestArgumentsThatAreNotAnObjectAreRejectedNotPanicked(t *testing.T) {
	s, err := ArtifactSchema(afspec.StepRequirements)
	if err != nil {
		t.Fatal(err)
	}
	partial := afspec.PartialSpec{}
	var content map[string]any
	var done bool
	tool := submitArtifactTool(afspec.StepRequirements, s, &partial, &content, &done)

	res := tool.Execute(context.Background(), json.RawMessage(`["not","an","object"]`))
	if res.OK {
		t.Error("a JSON array was accepted as an artifact")
	}
}

func TestEverySubmitToolCarriesItsSchemaAndAGuideline(t *testing.T) {
	// PromptGuidelines are how a tool tells the model to use it rather than
	// writing prose, which is the failure the old ToolChoice "any" prevented
	// on one wire only.
	var a Assessment
	var prd string
	var done bool
	var content map[string]any
	partial := afspec.PartialSpec{}
	reqSchema, err := ArtifactSchema(afspec.StepRequirements)
	if err != nil {
		t.Fatal(err)
	}

	for _, tool := range []core.Tool{
		submitAssessmentTool(&a, &done),
		submitPRDUpdateTool(&prd, &a, &done),
		submitArtifactTool(afspec.StepRequirements, reqSchema, &partial, &content, &done),
	} {
		if tool.InputSchema == nil {
			t.Errorf("%s declares no schema", tool.Name)
		}
		if len(tool.PromptGuidelines) == 0 {
			t.Errorf("%s carries no prompt guideline", tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("%s has no description", tool.Name)
		}
		if tool.ConstrainedSampling == nil {
			t.Errorf("%s does not ask for constrained sampling", tool.Name)
		} else if tool.ConstrainedSampling.Strict != core.StrictPrefer {
			t.Errorf("%s requires strict sampling; that fails the whole request on a wire "+
				"that cannot emit strict schemas, and the handler validates anyway", tool.Name)
		}
	}
}

func TestArtifactToolNamesMatchTheSteps(t *testing.T) {
	for _, step := range afspec.GenerationSteps {
		if got, want := ArtifactToolName(step), "submit_"+string(step); got != want {
			t.Errorf("ArtifactToolName(%s) = %q, want %q", step, got, want)
		}
	}
}
