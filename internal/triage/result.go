package triage

// FixSuggested says whether the next step of a done run is to fix the problem:
// the diagnosis is confirmed or probable, and there is an issue to point at.
func FixSuggested(d Diagnosis, issue *IssueInfo) bool {
	return issue != nil && (d.Confidence == "confirmed" || d.Confidence == "probable")
}

// LandAgain is the command that lands a run again, after a landing that failed.
// repo is the repository to name with --repo, or empty.
func LandAgain(runID, dir, repo string) []string {
	argv := []string{"af", "triage", "land", "--run-id", runID, "--dir", dir}
	if repo != "" {
		argv = append(argv, "--repo", repo)
	}
	return argv
}
