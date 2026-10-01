package issuetriage

import "github.com/agent-fox-dev/agentfox/internal/toolio"

// summaryResult is the trimmed view of Result kept under --detail summary
// (06-REQ-3.1): action, repo, url, number, title, severity, confidence,
// affected_files reduced to bare paths, labels, and rejected_path_calls.
// Every other field of Result is still computed exactly as under
// --detail full, and is still written in full to the report file
// (06-REQ-3.5) — this is a second, deliberately smaller view over the same
// facts, not a replacement for them.
type summaryResult struct {
	Action            string   `json:"action" trust:"fact"`
	Repo              string   `json:"repo,omitempty" trust:"fact"`
	URL               string   `json:"url,omitempty" trust:"fact"`
	Number            int      `json:"number,omitempty"`
	Title             string   `json:"title" trust:"model"`
	Severity          string   `json:"severity" trust:"model"`
	Confidence        string   `json:"confidence" trust:"model"`
	AffectedFiles     []string `json:"affected_files" trust:"fact"`
	Labels            []string `json:"labels,omitempty" trust:"fact"`
	RejectedPathCalls int      `json:"rejected_path_calls"`
	// Stage, Preflight and Estimate are the whole point of a --preflight
	// run, so the default view keeps them. They are absent on every
	// ordinary run.
	Stage     string                  `json:"stage,omitempty" trust:"fact"`
	Preflight []toolio.PreflightCheck `json:"preflight,omitempty"`
	Estimate  *toolio.Estimate        `json:"estimate,omitempty"`
	Detail    string                  `json:"detail" trust:"fact"`
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
		Stage:             r.Stage,
		Preflight:         r.Preflight,
		Estimate:          r.Estimate,
		Detail:            "summary",
	}
}
