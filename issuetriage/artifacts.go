package issuetriage

import "github.com/agent-fox-dev/agentfox/internal/toolio"

// Artifacts implements toolio.ArtifactsProvider (06-REQ-4). The single entry
// it can produce is read off Action, URL and Number — the facts Write set —
// never off the diagnosis fields (Problem, RootCause, SuggestedFix, ...),
// which are the model's own account of what it found.
func (r *Result) Artifacts() []toolio.Artifact {
	if r == nil {
		return nil
	}
	if r.Stage == "preflight" && len(r.Preflight) > 0 {
		// A passed --preflight run files nothing, not even hypothetically
		// (11-REQ-7.2).
		return nil
	}
	if r.Action == "none" && !r.DryRun {
		// Nothing was written, and nothing was going to be: there is
		// nothing to report, hypothetical or otherwise.
		return nil
	}
	a := toolio.Artifact{Kind: toolio.ArtifactIssue, URL: r.URL, Number: r.Number}
	if r.DryRun && r.Action == "none" {
		// --dry-run never reaches Write, so Action stays "none" and there
		// is no URL or number yet — the entry is hypothetical (06-REQ-4.3).
		a.DryRun = true
	}
	return []toolio.Artifact{a}
}
