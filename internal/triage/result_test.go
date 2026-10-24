package triage

import (
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
