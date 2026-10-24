package triage

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestStaleReasonHoldsWhileTheHostHasTheFiles verifies the host half of
// 08-REQ-4.5: the submission is checked against the host's tree once more.
func TestStaleReasonHoldsWhileTheHostHasTheFiles(t *testing.T) {
	payload := json.RawMessage(`{"title":"T","severity":"low","confidence":"confirmed","root_cause":"R",` +
		`"affected_files":[{"path":"a.go","line":3,"note":"n"}],"suggested_fix":"F","acceptance_criteria":["C"]}`)
	lines := func(p string) (int, error) { return 5, nil }

	if got := StaleReason(payload, Tree{Files: []string{"a.go"}, Lines: lines}); got != "" {
		t.Errorf("StaleReason of a tree that has the file = %q, want none", got)
	}

	for name, tree := range map[string]Tree{
		"a file the host no longer has": {Files: []string{"b.go"}, Lines: lines},
		"a file that became shorter":    {Files: []string{"a.go"}, Lines: func(string) (int, error) { return 2, nil }},
		"a file that cannot be counted": {Files: []string{"a.go"}, Lines: func(string) (int, error) { return 0, errors.New("gone") }},
	} {
		t.Run(name, func(t *testing.T) {
			got := StaleReason(payload, tree)
			if got == "" || !strings.Contains(got, "/affected_files/0") {
				t.Errorf("StaleReason = %q, want the problem with its pointer", got)
			}
		})
	}
}

func TestStaleReasonOfAPayloadThatIsNoDiagnosisSaysSo(t *testing.T) {
	if got := StaleReason(json.RawMessage(`{"title":1}`), Tree{}); got == "" {
		t.Error("StaleReason of a payload that does not validate is empty")
	}
}

func TestStaleReasonListsEveryProblem(t *testing.T) {
	payload := json.RawMessage(`{"title":"T","severity":"low","confidence":"confirmed","root_cause":"R",` +
		`"affected_files":[{"path":"a.go","note":"n"},{"path":"b.go","note":"n"}],"suggested_fix":"F","acceptance_criteria":["C"]}`)

	got := StaleReason(payload, Tree{Files: []string{"c.go"}})

	if !strings.Contains(got, "/affected_files/0") || !strings.Contains(got, "/affected_files/1") || !strings.Contains(got, "; /affected_files/1") {
		t.Errorf("StaleReason = %q, want both problems, one after the other", got)
	}
}
