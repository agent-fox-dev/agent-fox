package issuetriage

import (
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The caller's --context block reaches the analyse prompt, after the report
// and independently of it (05-REQ-3.7).
func TestCallerContextReachesTheTriagePrompt(t *testing.T) {
	const block = "## Additional context from the caller\n\nIt only happens on Windows.\n"
	in := toolio.Input{Kind: toolio.KindText, Origin: "argument", Body: "save crashes", Context: block}

	got := taskPrompt(in, "/repo")
	if !strings.Contains(got, "It only happens on Windows.") {
		t.Fatalf("the triage prompt lacks the caller's context:\n%s", got)
	}
	if !strings.Contains(got, "--- BEGIN REPORT ---\nsave crashes\n--- END REPORT ---") {
		t.Errorf("the report block is not the body, unchanged:\n%s", got)
	}
	if strings.Index(got, "It only happens on Windows.") < strings.Index(got, "--- END REPORT ---") {
		t.Error("the context should follow the report, not sit inside it")
	}

	in.Context = ""
	if got := taskPrompt(in, "/repo"); strings.Contains(got, "Additional context") {
		t.Errorf("a prompt with no --context mentions one:\n%s", got)
	}
}
