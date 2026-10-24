package fix

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/checks"
)

// resultKeys are the fields of a result, in the order of the spec.
var resultKeys = []string{
	"run_id", "issue", "base", "base_head", "rounds", "outcome", "verdict", "landable", "closes",
	"baseline", "after", "revert_check", "changed_files", "reverted_paths", "land", "branch", "commit", "pushed", "pull_request", "box",
}

// asMap is a result as the generic JSON a consumer reads.
func asMap(t *testing.T, r Result) map[string]any {
	t.Helper()
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestAResultCarriesEveryFieldWhateverTheRunDid_TS_09_67 verifies TS-09-67 for the
// builder: a landed result, a parked one and one of a run that measured nothing each have
// all the fields, the lists are lists and not null, and the box is host for a provider that
// puts no boundary around its tools.
// Verifies: 09-REQ-10.1
func TestAResultCarriesEveryFieldWhateverTheRunDid_TS_09_67(t *testing.T) {
	state := sampleState()
	info := sessionBoxInfo(t, "local")
	if info != (BoxInfo{Provider: "local", Network: "host"}) {
		t.Errorf("NewBoxInfo(local) = %+v, want network host", info)
	}

	landed := NewResult(ResultInput{
		State: state, Rounds: 2, Outcome: OutcomeLanded, Land: LandPR,
		Measured: &Measurement{
			Verdict: checks.VerdictPass, Landable: true, Lands: true, Closes: true, After: &GateRef{GateID: "after", Passed: true},
			RevertCheck: &RevertRef{Outcome: "proven", Reason: "r"}, Changed: []string{"a.go"}, Reverted: []string{".specs/x.md"},
		},
		Branch: "fix/7-greeting", Commit: "0123456789abcdef0123456789abcdef01234567", Pushed: true,
		PullRequest: &PullRequestRef{Number: 3, URL: "https://x/3", Draft: false}, Box: &info,
	})
	m := asMap(t, landed)
	for _, key := range resultKeys {
		if _, ok := m[key]; !ok {
			t.Errorf("a landed result lacks %s: %v", key, m)
		}
	}
	if len(m) != len(resultKeys) {
		t.Errorf("a landed result has %d fields, want %d", len(m), len(resultKeys))
	}
	if m["run_id"] != state.RunID || m["issue"] != state.Issue.URL || m["base"] != "main" || m["base_head"] != state.BaseHead || m["outcome"] != "landed" || m["verdict"] != "pass" ||
		m["landable"] != true || m["closes"] != true || m["land"] != "pr" || m["branch"] != "fix/7-greeting" || m["commit"] != "0123456789abcdef0123456789abcdef01234567" || m["pushed"] != true ||
		m["rounds"] != float64(2) {
		t.Errorf("result = %v", m)
	}
	if b, _ := m["baseline"].(map[string]any); b["gate_id"] != state.BaselineGateID || b["passed"] != state.BaselinePassed {
		t.Errorf("baseline = %v, want the gate of the state", m["baseline"])
	}
	if a, _ := m["after"].(map[string]any); a["gate_id"] != "after" || a["passed"] != true {
		t.Errorf("after = %v", m["after"])
	}
	if r, _ := m["revert_check"].(map[string]any); r["outcome"] != "proven" {
		t.Errorf("revert_check = %v", m["revert_check"])
	}
	if bx, _ := m["box"].(map[string]any); bx["provider"] != "local" || bx["network"] != "host" || bx["image"] != "" {
		t.Errorf("box = %v", m["box"])
	}
	if got := m["changed_files"].([]any); len(got) != 1 || got[0] != "a.go" {
		t.Errorf("changed_files = %v", m["changed_files"])
	}
	if got := m["reverted_paths"].([]any); len(got) != 1 || got[0] != ".specs/x.md" {
		t.Errorf("reverted_paths = %v", m["reverted_paths"])
	}
	if pr, _ := m["pull_request"].(map[string]any); pr["number"] != float64(3) || pr["draft"] != false {
		t.Errorf("pull_request = %v", m["pull_request"])
	}

}

// TestAResultOfARunThatParkedOrAskedCarriesEveryField_TS_09_67 verifies the other half of
// TS-09-67: a parked result, one of a failed run that measured something with no verdict,
// and one of a run that asked a question and measured nothing have all the fields, with the
// lists as lists and the rest null.
// Verifies: 09-REQ-10.1
func TestAResultOfARunThatParkedOrAskedCarriesEveryField_TS_09_67(t *testing.T) {
	state := sampleState()
	info := sessionBoxInfo(t, "local")

	// A run that parked.
	state.NoVerify = false
	parked := asMap(t, NewResult(ResultInput{
		State: state, Outcome: OutcomeParked, Land: LandBranch,
		Measured: &Measurement{Verdict: checks.VerdictStillFailing, After: &GateRef{GateID: "a2"}, Changed: []string{"a.go"}},
		Branch:   "fix/x", Commit: "0123456789abcdef0123456789abcdef01234567", Box: &info,
	}))
	for _, key := range resultKeys {
		if _, ok := parked[key]; !ok {
			t.Errorf("a parked result lacks %s", key)
		}
	}
	if parked["issue"] != state.Issue.URL || parked["outcome"] != "parked" || parked["landable"] != false || parked["closes"] != false || parked["pull_request"] != nil || parked["pushed"] != false {
		t.Errorf("parked = %v", parked)
	}
	for _, key := range []string{"changed_files", "reverted_paths"} {
		if list, ok := parked[key].([]any); !ok {
			t.Errorf("%s = %v, want a list and not null", key, parked[key])
		} else if key == "reverted_paths" && len(list) != 0 {
			t.Errorf("%s = %v", key, list)
		}
	}

	// A run that asked a question measured nothing, and had no baseline when it was not
	// verified.
	state.BaselineGateID = ""
	if got := asMap(t, NewResult(ResultInput{State: state, Outcome: OutcomeFailed, Land: LandPR, Measured: &Measurement{Changed: []string{"a.go"}}})); got["verdict"] != nil {
		t.Errorf("a measurement with no verdict: verdict = %v, want null", got["verdict"])
	}
	asked := asMap(t, NewResult(ResultInput{State: state, Outcome: OutcomeNeedsHuman, Land: LandPR}))
	if len(asked) != len(resultKeys) || asked["verdict"] != nil || asked["after"] != nil || asked["baseline"] != nil || asked["branch"] != nil || asked["commit"] != nil || asked["box"] != nil || asked["revert_check"] != nil {
		t.Errorf("a run that measured nothing = %v", asked)
	}
	if list, ok := asked["changed_files"].([]any); !ok || len(list) != 0 {
		t.Errorf("changed_files = %v, want []", asked["changed_files"])
	}
	if keys := slices.Sorted(func(yield func(string) bool) {
		for k := range asked {
			if !yield(k) {
				return
			}
		}
	}); !slices.Equal(keys, slices.Sorted(slices.Values(resultKeys))) {
		t.Errorf("keys = %v", keys)
	}
}

// sessionBoxInfo is the box info of an open session of the provider over a new checkout.
func sessionBoxInfo(t *testing.T, provider string) BoxInfo {
	t.Helper()
	r := newTestRepo(t, map[string]string{"a.txt": "a\n"}, false)
	sess, err := box.Open(context.Background(), box.Request{Provider: provider, HostDir: r.Dir, RunID: "20261021_180000_abcdef", Tool: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return NewBoxInfo(sess)
}
