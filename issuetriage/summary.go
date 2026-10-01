package issuetriage

// summaryResult is the trimmed view of Result kept under --detail summary
// (06-REQ-3.1): action, repo, url, number, title, severity, confidence,
// affected_files reduced to bare paths, labels, and rejected_path_calls.
// Every other field of Result is still computed exactly as under
// --detail full, and is still written in full to the report file
// (06-REQ-3.5) — this is a second, deliberately smaller view over the same
// facts, not a replacement for them.
type summaryResult struct {
	Action            string   `json:"action"`
	Repo              string   `json:"repo,omitempty"`
	URL               string   `json:"url,omitempty"`
	Number            int      `json:"number,omitempty"`
	Title             string   `json:"title"`
	Severity          string   `json:"severity"`
	Confidence        string   `json:"confidence"`
	AffectedFiles     []string `json:"affected_files"`
	Labels            []string `json:"labels,omitempty"`
	RejectedPathCalls int      `json:"rejected_path_calls"`
	Detail            string   `json:"detail"`
}

// SummaryView implements toolio.Summarizable.
func (r *Result) SummaryView() any {
	paths := make([]string, 0, len(r.AffectedFiles))
	for _, f := range r.AffectedFiles {
		paths = append(paths, f.Path)
	}
	return summaryResult{
		Action:            r.Action,
		Repo:              r.Repo,
		URL:               r.URL,
		Number:            r.Number,
		Title:             r.Title,
		Severity:          r.Severity,
		Confidence:        r.Confidence,
		AffectedFiles:     paths,
		Labels:            r.Labels,
		RejectedPathCalls: r.RejectedPathCalls,
		Detail:            "summary",
	}
}
