package brain_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/triage"
)

// TestTheClaudeEntryHasReportedCostAndTheCommandRoute_TS_10_46 verifies TS-10-46: the
// claude entry reports its cost and submits with the command, so that the triage brief
// and the fix brief rendered for it name af triage submit and af fix submit with the
// run, and not the tool of the driver.
// Verifies: 10-REQ-8.1
func TestTheClaudeEntryHasReportedCostAndTheCommandRoute_TS_10_46(t *testing.T) {
	entry, err := brain.Default.Lookup(brain.ClaudeName)
	if err != nil {
		t.Fatal(err)
	}
	if entry.CostSource != brain.CostReported || !entry.CommandRoute || !entry.OwnHands {
		t.Fatalf("entry = %+v, want reported cost, the command route and hands of its own", entry)
	}
	for _, name := range []string{brain.AgentkitName, brain.ScriptedName} {
		if other, _ := brain.Default.Lookup(name); other.CommandRoute {
			t.Errorf("%s has the command route: it submits through the tool of the driver", name)
		}
	}

	const id = "20261021_170000_abcdef"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	brief, err := triage.BuildBrief(context.Background(), triage.BriefParams{
		Dir: dir, Report: triage.Report{Kind: "text", Origin: "text", Body: "it crashes"}, RunID: id,
		InProcess: !entry.CommandRoute, Bounds: triage.DefaultBounds, TruncationMarker: "…",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(brief.Task, "af triage submit --run-id "+id) || strings.Contains(brief.Task, "Submit your diagnosis by calling the `file_diagnosis` tool") {
		t.Errorf("the triage task block for claude:\n%s\nwant the command and not the tool", brief.Task)
	}

	task := fix.TaskBlock(fix.TaskParams{RunID: id, Attempt: 1, Round: 1, CommandOnly: entry.CommandRoute})
	if !strings.Contains(task, "af fix submit --run-id "+id) || strings.Contains(task, "submit_fix") {
		t.Errorf("the fix task block for claude:\n%s\nwant the command and not the tool", task)
	}
	both := fix.TaskBlock(fix.TaskParams{RunID: id, Attempt: 1, Round: 1})
	if !strings.Contains(both, "af fix submit --run-id "+id) || !strings.Contains(both, "submit_fix") {
		t.Errorf("the fix task block for a brain that may use either:\n%s", both)
	}
}
