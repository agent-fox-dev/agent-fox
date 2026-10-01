package codefix

import (
	"strings"
	"testing"
)

func TestFixImplementPromptSaysTheProgramRunsTheFullChecks(t *testing.T) {
	for _, want := range []string{"targeted runs", "full check command itself", "repeats it"} {
		if !strings.Contains(implementSystemPrompt, want) {
			t.Errorf("the implement prompt lacks %q", want)
		}
	}
	if strings.Contains(implementSystemPrompt, "Run the project's checks yourself") {
		t.Error("the implement prompt still tells the agent to run the full checks")
	}
}

// The analyse phase gets the project instructions too (#66).
func TestAnalysisPromptCarriesTheProjectInstructions(t *testing.T) {
	in := analysisInput{Root: "/r", Instructions: "Layout: code is under internal/.\n"}
	p := analysisPrompt(in)
	for _, want := range []string{"BEGIN PROJECT INSTRUCTIONS", "Layout: code is under internal/."} {
		if !strings.Contains(p, want) {
			t.Errorf("the analysis prompt lacks %q:\n%s", want, p)
		}
	}
	if strings.Contains(analysisPrompt(analysisInput{Root: "/r"}), "PROJECT INSTRUCTIONS") {
		t.Error("an empty instruction file produced a block")
	}
	impl := implementPrompt(implementInput{Root: "/r", Instructions: "Be terse."})
	if !strings.Contains(impl, "Be terse.") || !strings.Contains(impl, "apply to the change") {
		t.Errorf("the implement prompt changed:\n%s", impl)
	}
}
