package codefix

import (
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// summaryResult is the trimmed view of Result kept under --detail summary
// (06-REQ-3.2). Verification — the failing command's tail output — is
// included only when the run's verdict is not one Verdict.Landable()
// reports true for: a caller deciding whether to retry, repair or give up
// needs it; a caller whose run landed does not. Every other field of Result
// is still computed exactly as under --detail full, and is still written in
// full to the report file (06-REQ-3.5).
type summaryResult struct {
	Stage           string         `json:"stage" trust:"fact"`
	Branch          string         `json:"branch,omitempty" trust:"fact"`
	BaseBranch      string         `json:"base_branch,omitempty" trust:"fact"`
	Commit          string         `json:"commit,omitempty" trust:"fact"`
	ChangedFiles    []string       `json:"changed_files,omitempty" trust:"fact"`
	Verdict         string         `json:"verdict" trust:"fact"`
	CriteriaOutcome string         `json:"criteria_outcome,omitempty" trust:"fact"`
	PullRequestURL  string         `json:"pull_request_url,omitempty" trust:"fact"`
	DryRun          bool           `json:"dry_run"`
	Verification    *checks.Result `json:"verification,omitempty"`
	// Preflight and Estimate are what a --preflight run exists to report, so
	// they survive the default view.
	Preflight []toolio.PreflightCheck `json:"preflight,omitempty"`
	Estimate  *toolio.Estimate        `json:"estimate,omitempty"`
	Detail    string                  `json:"detail" trust:"fact"`
}

// SummaryView implements toolio.Summarizable.
func (r *Result) SummaryView() any {
	s := summaryResult{
		Stage:           r.Stage,
		Branch:          r.Branch,
		BaseBranch:      r.BaseBranch,
		Commit:          r.Commit,
		ChangedFiles:    r.ChangedFiles,
		Verdict:         r.Verdict,
		CriteriaOutcome: r.CriteriaOutcome,
		PullRequestURL:  r.PullRequestURL,
		DryRun:          r.DryRun,
		Preflight:       r.Preflight,
		Estimate:        r.Estimate,
		Detail:          "summary",
	}
	if !checks.Verdict(r.Verdict).Landable() {
		v := r.Verification
		s.Verification = &v
	}
	return s
}
