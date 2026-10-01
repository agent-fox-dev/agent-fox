package codeimpl

import "github.com/agent-fox-dev/agentfox/internal/toolio"

// Artifacts implements toolio.ArtifactsProvider (06-REQ-4). Every entry is
// read off a fact this package already established — Branch, each task's
// own Commit, a repair's Commit, PullRequestURL — never from Submission or
// Survey, which are the model's own account of its work.
func (r *Result) Artifacts() []toolio.Artifact {
	if r == nil {
		return nil
	}
	if r.Stage == "preflight" && len(r.Preflight) > 0 {
		// A passed --preflight run created nothing: Branch is the name the
		// branch would have, not a branch this run made (11-REQ-7.2).
		return nil
	}
	var out []toolio.Artifact
	if r.Branch != "" {
		// The branch and every commit below happened locally, dry run or
		// not, so none of them carries a dry_run marker (06-REQ-4.4).
		out = append(out, toolio.Artifact{Kind: toolio.ArtifactBranch, Name: r.Branch, Base: r.BaseBranch})
	}
	if r.Repair != nil && r.Repair.Commit != "" {
		out = append(out, toolio.Artifact{Kind: toolio.ArtifactCommit, SHA: r.Repair.Commit, Branch: r.Branch})
	}
	for _, t := range r.Tasks {
		if t.Commit != "" {
			out = append(out, toolio.Artifact{Kind: toolio.ArtifactCommit, SHA: t.Commit, Branch: r.Branch})
		}
		if t.Repair != nil && t.Repair.Commit != "" {
			out = append(out, toolio.Artifact{Kind: toolio.ArtifactCommit, SHA: t.Repair.Commit, Branch: r.Branch})
		}
	}
	if r.PullRequestURL != "" {
		out = append(out, toolio.Artifact{Kind: toolio.ArtifactPullRequest, URL: r.PullRequestURL, Number: r.PullRequestNumber})
	} else if r.DryRun && r.Land == string(LandPR) {
		// --land=pr under --dry-run never pushes, so no pull request was
		// opened; the entry reports what would have happened (06-REQ-4.3).
		out = append(out, toolio.Artifact{Kind: toolio.ArtifactPullRequest, DryRun: true})
	}
	return out
}
