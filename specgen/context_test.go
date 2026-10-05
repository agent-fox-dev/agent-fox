package specgen

import (
	"context"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
)

// The caller's --context block reaches the PRD phase's prompt, after the
// input and independently of it (05-REQ-3.7). The real phase runs against a
// scripted provider that never submits a PRD; what is asserted is the prompt
// it was sent, so the run's failure is beside the point.
func TestCallerContextReachesThePRDPrompt(t *testing.T) {
	const block = "## Additional context from the caller\n\nTarget the v2 API only.\n"

	prompt := func(t *testing.T, context_ string) (body, sent string) {
		t.Helper()
		ws := goWorkspace(t)
		runner, p := fauxRunner(t, ws, faux.FauxAssistantMessage(core.StopReasonStop, faux.FauxText("no PRD")))
		o := newOptions(ws, nil)
		o.Runner = runner
		o.Input.Context = context_
		body = o.Input.Body
		_, _ = Run(context.Background(), o)
		reqs := p.Requests()
		if len(reqs) == 0 {
			t.Fatal("the PRD phase sent no request")
		}
		return body, transcript(reqs[0])
	}

	body, sent := prompt(t, block)
	if !strings.Contains(sent, "Target the v2 API only.") {
		t.Fatalf("the PRD prompt lacks the caller's context:\n%s", sent)
	}
	if !strings.Contains(sent, "--- BEGIN INPUT ---\n"+body+"\n--- END INPUT ---") {
		t.Errorf("the input block is not the body, unchanged:\n%s", sent)
	}
	if strings.Index(sent, "Target the v2 API only.") < strings.Index(sent, "--- END INPUT ---") {
		t.Error("the context should follow the input, not sit inside it")
	}

	_, sent = prompt(t, "")
	if strings.Contains(sent, "Additional context") {
		t.Errorf("a prompt with no --context mentions one:\n%s", sent)
	}
}
