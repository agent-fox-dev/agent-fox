package toolio_test

import (
	"encoding/json"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-06-26 (unit): side_effects lists every remote write in order and is
// omitted when there were none.
func TestTS06_26_SideEffectsListedAndOmittedWhenEmpty(t *testing.T) {
	issueRun := toolio.NewRun("issue", "v1")
	issueRun.RecordSideEffect("create_issue", "acme/widgets", true, "")
	env := issueRun.Envelope(toolio.ExitOK, nil, nil)
	if len(env.SideEffects) != 1 || env.SideEffects[0].Action != "create_issue" {
		t.Fatalf("SideEffects = %+v, want one create_issue entry", env.SideEffects)
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	list, _ := doc["side_effects"].([]any)
	if len(list) != 1 {
		t.Fatalf("side_effects = %v", doc["side_effects"])
	}
	e := list[0].(map[string]any)
	if e["action"] != "create_issue" || e["target"] != "acme/widgets" || e["ok"] != true {
		t.Errorf("entry = %v", e)
	}
	if _, has := e["warning"]; has {
		t.Errorf("an ok entry carries a warning: %v", e)
	}

	specRun := toolio.NewRun("spec", "v1")
	sb, _ := json.Marshal(specRun.Envelope(toolio.ExitOK, nil, nil))
	var sdoc map[string]any
	_ = json.Unmarshal(sb, &sdoc)
	if _, has := sdoc["side_effects"]; has {
		t.Errorf("a run with no remote write has a side_effects field: %s", sb)
	}
}

// TS-06-27 (unit): actions are drawn from the closed set and the envelope
// keeps the order the writes were recorded in.
func TestTS06_27_SideEffectsKeepRecordedOrderAndClosedActions(t *testing.T) {
	run := toolio.NewRun("fix", "v1")
	run.RecordSideEffect("comment", "acme/widgets#42", true, "")
	run.RecordSideEffect("push", "origin fix/x", true, "")
	run.RecordSideEffect("open_pr", "acme/widgets#7", true, "")
	run.RecordSideEffect("update_issue", "acme/widgets#42", true, "")
	got := run.SideEffects()
	want := []string{"comment", "push", "open_pr", "update_issue"}
	closed := map[string]bool{"create_issue": true, "update_issue": true, "comment": true, "push": true, "open_pr": true}
	if len(got) != len(want) {
		t.Fatalf("SideEffects = %+v", got)
	}
	for i, e := range got {
		if e.Action != want[i] || !closed[e.Action] {
			t.Errorf("entry %d action = %q, want %q", i, e.Action, want[i])
		}
	}
	env := run.Envelope(toolio.ExitOK, nil, nil)
	if len(env.SideEffects) != 4 {
		t.Errorf("envelope SideEffects = %+v", env.SideEffects)
	}
	got[0].Action = "mutated"
	if run.SideEffects()[0].Action != "comment" {
		t.Error("SideEffects returned the run's own slice, not a copy")
	}
}

// TS-06-28 (unit): a failed write keeps the warning code the call site
// recorded, and a successful one never carries a warning.
func TestTS06_28_FailedWriteKeepsWarnCodeAndOKNeverHasOne(t *testing.T) {
	run := toolio.NewRun("fix", "v1")
	run.Warn(toolio.WarnPullRequestNotOpened, "high", "no PR")
	run.RecordSideEffect("open_pr", "acme/widgets", false, toolio.WarnPullRequestNotOpened)
	run.RecordSideEffect("comment", "acme/widgets#1", true, toolio.WarnCommentNotPosted)
	se := run.SideEffects()
	if se[0].OK || se[0].Warning != toolio.WarnPullRequestNotOpened {
		t.Errorf("failed entry = %+v", se[0])
	}
	if se[0].Warning != run.Warnings()[0].Code {
		t.Errorf("entry warning %q disagrees with the recorded warning %q", se[0].Warning, run.Warnings()[0].Code)
	}
	if !se[1].OK || se[1].Warning != "" {
		t.Errorf("an ok entry must not carry a warning: %+v", se[1])
	}
}

// A nil Run records nothing and does not panic, like Warn.
func TestSideEffectsNilRunIsSafe(t *testing.T) {
	var r *toolio.Run
	r.RecordSideEffect("push", "origin x", true, "")
	if r.SideEffects() != nil {
		t.Error("nil run returned side effects")
	}
}
