package codeimpl

import (
	"strings"
	"testing"
)

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
