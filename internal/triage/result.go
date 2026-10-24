package triage

// FixSuggested says whether the next step of a done run is to fix the problem:
// the diagnosis is confirmed or probable, and there is an issue to point at.
func FixSuggested(d Diagnosis, issue *IssueInfo) bool {
	return issue != nil && (d.Confidence == "confirmed" || d.Confidence == "probable")
}
