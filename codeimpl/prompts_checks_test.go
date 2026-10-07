package codeimpl

import (
	"regexp"
	"strings"
	"testing"
)

// TS-18-6 (unit): The surveySystemPrompt contains find_references in the
// callers step and retains the callers instruction.
//
// Verifies: 18-REQ-3.2, 18-REQ-3.4
func TestTS18_6_SurveyPromptContainsFindReferences(t *testing.T) {
	if !strings.Contains(surveySystemPrompt, "find_references") {
		t.Error("surveySystemPrompt does not contain find_references")
	}
	if !strings.Contains(surveySystemPrompt, "caller") {
		t.Error("surveySystemPrompt lost the callers instruction")
	}
	steps := regexp.MustCompile(`(?m)^\d+\.`).FindAllString(surveySystemPrompt, -1)
	if len(steps) != 5 {
		t.Errorf("surveySystemPrompt has %d method steps, want 5", len(steps))
	}
}

// The harness runs the full check command after every task, so the prompt
// must not also tell the agent to run it (#48).
func TestImplementPromptSaysTheProgramRunsTheFullChecks(t *testing.T) {
	for _, want := range []string{"targeted runs", "full check command itself", "repeats it"} {
		if !strings.Contains(implementSystemPrompt, want) {
			t.Errorf("the implement prompt lacks %q", want)
		}
	}
	if strings.Contains(implementSystemPrompt, "Run the checks yourself") {
		t.Error("the implement prompt still tells the agent to run the full checks")
	}
}
