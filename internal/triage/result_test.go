package triage

import (
	"slices"
	"testing"
)

// TestFixIsSuggestedForAConfirmedOrProbableDiagnosisOnAnIssue verifies the
// decision of TS-08-61 (08-REQ-9.5): only a diagnosis that is confirmed or
// probable, and an issue to point at, suggest a fix.
func TestFixIsSuggestedForAConfirmedOrProbableDiagnosisOnAnIssue(t *testing.T) {
	issue := &IssueInfo{Repo: "o/r", Number: 7, URL: "https://example.com/o/r/issues/7"}
	for confidence, want := range map[string]bool{"confirmed": true, "probable": true, "suspected": false, "not_reproducible": false, "": false} {
		if got := FixSuggested(Diagnosis{Confidence: confidence}, issue); got != want {
			t.Errorf("FixSuggested(%q, an issue) = %v, want %v", confidence, got, want)
		}
		if FixSuggested(Diagnosis{Confidence: confidence}, nil) {
			t.Errorf("FixSuggested(%q, no issue) = true: there is nothing to fix by", confidence)
		}
	}
}

func TestLandAgainNamesTheRunAndTheDirectoryAndTheRepoWhenGiven(t *testing.T) {
	got := LandAgain("20240101_000000_abcdef", "/work/x", "")
	if want := []string{"af", "triage", "land", "--run-id", "20240101_000000_abcdef", "--dir", "/work/x"}; !slices.Equal(got, want) {
		t.Errorf("LandAgain = %v, want %v", got, want)
	}
	got = LandAgain("20240101_000000_abcdef", "/work/x", "o/r")
	if want := []string{"af", "triage", "land", "--run-id", "20240101_000000_abcdef", "--dir", "/work/x", "--repo", "o/r"}; !slices.Equal(got, want) {
		t.Errorf("LandAgain with a repo = %v, want %v", got, want)
	}
}
