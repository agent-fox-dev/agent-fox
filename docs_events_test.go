package agentfox

import (
	"strings"
	"testing"
)

// TS-07-42 (unit): docs/cli.md documents --emit-events, the eleven-row
// type table and a worked multi-line JSONL example
// Verifies: 07-REQ-8.1
func TestDocsCliEventTypes_TS_07_42(t *testing.T) {
	doc := readDoc(t, "cli.md")
	if !strings.Contains(doc, "event types") {
		t.Fatal(`docs/cli.md has no "event types" section`)
	}
	sec := docSection(t, doc, "event types")
	if !strings.Contains(sec, "--emit-events") {
		t.Errorf("event types section does not document --emit-events")
	}
	for _, typ := range []string{"run_start", "step", "phase_start", "turn", "tool_call", "check", "phase_end", "warning", "heartbeat", "run_end", "text"} {
		if _, ok := tableRow(sec, "| `"+typ+"`"); !ok {
			t.Errorf("event types table has no row for %s", typ)
		}
	}
	// A worked example: at least three JSON lines.
	jsonLines := 0
	for _, l := range strings.Split(sec, "\n") {
		if strings.HasPrefix(l, `{"ts":`) {
			jsonLines++
		}
	}
	if jsonLines < 3 {
		t.Errorf("worked example has %d JSONL lines, want at least 3", jsonLines)
	}
}

// TS-07-43 (unit): docs/model-usage.md points at turn and phase_end as the way
// to watch spend live
// Verifies: 07-REQ-8.2
func TestDocsModelUsageWatchSpend_TS_07_43(t *testing.T) {
	doc := readDoc(t, "model-usage.md")
	for _, want := range []string{"`turn`", "`phase_end`", "usage.phases"} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/model-usage.md does not mention %s", want)
		}
	}
	// The pointer sits next to the usage.phases[] paragraph.
	i := strings.Index(doc, "usage.phases")
	j := strings.Index(doc, "`phase_end`")
	if i < 0 || j < 0 || j < i || j-i > 1500 {
		t.Errorf("phase_end pointer (offset %d) is not next to usage.phases (offset %d)", j, i)
	}
}
