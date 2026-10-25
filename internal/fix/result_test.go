package fix

import (
	"slices"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestNewResult_CarriesWhatTheRunMeasuredAndDid(t *testing.T) {
	s := sampleState()
	info := BoxInfo{Provider: "local", Network: "host"}
	pr := &PullRequestRef{Number: 3, URL: "https://x/3"}

	r := NewResult(ResultInput{
		State: s, Rounds: 2, Outcome: OutcomeLanded, Land: LandPR,
		Measured: &Measurement{
			Verdict: checks.VerdictPass, Landable: true, Lands: true, Closes: true, After: &GateRef{GateID: "after", Passed: true},
			RevertCheck: &RevertRef{Outcome: "proven", Reason: "r"}, Changed: []string{"a.go"}, Reverted: []string{".specs/x.md"},
		},
		Branch: "fix/7-greeting", Commit: "0123", Pushed: true, PullRequest: pr, Box: &info,
	})

	if r.RunID != s.RunID || r.Base != "main" || r.BaseHead != s.BaseHead || r.Rounds != 2 || r.Outcome != OutcomeLanded || r.Land != LandPR || !r.Pushed {
		t.Errorf("result = %+v", r)
	}
	if r.Issue == nil || *r.Issue != s.Issue.URL || r.Verdict == nil || *r.Verdict != checks.VerdictPass || !r.Landable || !r.Closes {
		t.Errorf("issue %v, verdict %v, landable %v, closes %v", r.Issue, r.Verdict, r.Landable, r.Closes)
	}
	if r.Baseline == nil || *r.Baseline != (GateRef{GateID: s.BaselineGateID, Passed: true}) || r.After.GateID != "after" || r.RevertCheck.Outcome != "proven" {
		t.Errorf("baseline %+v, after %+v, revert check %+v", r.Baseline, r.After, r.RevertCheck)
	}
	if r.Branch == nil || *r.Branch != "fix/7-greeting" || r.Commit == nil || *r.Commit != "0123" || r.PullRequest != pr || r.Box != &info {
		t.Errorf("branch %v, commit %v, pull request %v, box %v", r.Branch, r.Commit, r.PullRequest, r.Box)
	}
	if !slices.Equal(r.ChangedFiles, []string{"a.go"}) || !slices.Equal(r.RevertedPaths, []string{".specs/x.md"}) {
		t.Errorf("changed %v, reverted %v", r.ChangedFiles, r.RevertedPaths)
	}
}

func TestNewResult_WhatTheRunDidNotDoIsNullAndTheListsAreEmpty(t *testing.T) {
	s := sampleState()
	s.Issue, s.BaselineGateID = nil, ""

	asked := testutil.Decode[map[string]any](t, testutil.JSON(t, NewResult(ResultInput{State: s, Outcome: OutcomeNeedsHuman, Land: LandPR})))
	noVerdict := NewResult(ResultInput{State: s, Outcome: OutcomeFailed, Measured: &Measurement{Changed: []string{"a.go"}}})

	for _, key := range []string{"issue", "verdict", "baseline", "after", "revert_check", "branch", "commit", "pull_request", "box"} {
		if v, ok := asked[key]; !ok || v != nil {
			t.Errorf("%s = %v (present %v), want null", key, v, ok)
		}
	}
	for _, key := range []string{"changed_files", "reverted_paths"} {
		if list, ok := asked[key].([]any); !ok || len(list) != 0 {
			t.Errorf("%s = %v, want []", key, asked[key])
		}
	}
	if noVerdict.Verdict != nil || !slices.Equal(noVerdict.ChangedFiles, []string{"a.go"}) {
		t.Errorf("a measurement with no verdict: verdict %v, changed %v, want null and the paths", noVerdict.Verdict, noVerdict.ChangedFiles)
	}
}

func TestNewBoxInfo_DescribesTheBoxOfASession(t *testing.T) {
	sess := openLocalBox(t, newRepo(t, nil, false).Dir)

	if got := NewBoxInfo(sess); got != (BoxInfo{Provider: "local", Network: "host"}) {
		t.Errorf("NewBoxInfo = %+v, want the local provider with the network of the host", got)
	}
}
