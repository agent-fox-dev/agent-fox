package codefix

import "github.com/agent-fox-dev/agentfox/internal/toolio"

// Artifacts implements toolio.ArtifactsProvider (06-REQ-4). Every entry is
// read off a fact this package already established on Result — Branch,
// Commit, PullRequestURL, Comments — never from Implementation, which is
// the model's own account of its work.
func (r *Result) Artifacts() []toolio.Artifact {
	if r == nil {
		return nil
	}
	var out []toolio.Artifact
	if r.Branch != "" {
		// The branch and its commit (landed or, when the run parked, the
		// wip: one — the same field holds either) happened locally, dry
		// run or not, so neither carries a dry_run marker (06-REQ-4.4).
		out = append(out, toolio.Artifact{Kind: toolio.ArtifactBranch, Name: r.Branch, Base: r.BaseBranch})
	}
	if r.Commit != "" {
		out = append(out, toolio.Artifact{Kind: toolio.ArtifactCommit, SHA: r.Commit, Branch: r.Branch})
	}
	if r.PullRequestURL != "" {
		out = append(out, toolio.Artifact{Kind: toolio.ArtifactPullRequest, URL: r.PullRequestURL, Number: r.PullRequestNumber})
	} else if r.DryRun && r.Land == string(LandPR) {
		// --land=pr under --dry-run never pushes, so no pull request was
		// opened; the entry reports what would have happened (06-REQ-4.3).
		out = append(out, toolio.Artifact{Kind: toolio.ArtifactPullRequest, DryRun: true})
	}
	for _, url := range r.Comments {
		out = append(out, toolio.Artifact{Kind: toolio.ArtifactComment, URL: url})
	}
	return out
}
