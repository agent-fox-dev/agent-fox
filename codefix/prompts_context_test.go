package codefix

import (
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// 05-REQ-3.7: the caller's --context block is appended to what each phase's
// prompt shows the model, after the report and independently of Input.Body, and
// a run without --context sends the prompt it always did.
func TestContextBlockFollowsTheReportInEveryPhasePrompt(t *testing.T) {
	const note = "It only happens on Windows."
	const block = "## Additional context from the caller\n\n" + note + "\n"
	const body = "the counter double-counts"
	in := toolio.Input{Kind: toolio.KindText, Origin: "argument", Body: body, Context: block}
	plain := in
	plain.Context = ""

	prompts := map[string][2]string{
		"analysis":  {analysisPrompt(analysisInput{Input: in, Root: "/r"}), analysisPrompt(analysisInput{Input: plain, Root: "/r"})},
		"implement": {implementPrompt(implementInput{Input: in, Root: "/r", Branch: "fix/x"}), implementPrompt(implementInput{Input: plain, Root: "/r", Branch: "fix/x"})},
	}
	for name, p := range prompts {
		with, without := p[0], p[1]
		if !strings.Contains(with, note) {
			t.Errorf("%s prompt lacks the caller's context:\n%s", name, with)
			continue
		}
		if strings.Index(with, note) < strings.Index(with, "--- END REPORT ---") {
			t.Errorf("%s prompt puts the context inside the report fence", name)
		}
		if !strings.Contains(with, "--- BEGIN REPORT ---\n"+body+"\n--- END REPORT ---") {
			t.Errorf("%s prompt's report block is not the body, unchanged", name)
		}
		if strings.Contains(without, "Additional context") {
			t.Errorf("%s prompt with no --context mentions one", name)
		}
	}
	if in.Body != body {
		t.Errorf("Input.Body was changed to %q", in.Body)
	}
}
