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
