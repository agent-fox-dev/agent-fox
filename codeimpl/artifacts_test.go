package codeimpl

import (
	"encoding/json"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func artifactAsMap(t *testing.T, a toolio.Artifact) map[string]any {
	t.Helper()
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("Marshal artifact: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal artifact: %v", err)
	}
	return m
}

func findKind(t *testing.T, artifacts []toolio.Artifact, k toolio.ArtifactKind) map[string]any {
	t.Helper()
	for _, a := range artifacts {
		if a.Kind == k {
			return artifactAsMap(t, a)
		}
	}
	return nil
}

// 06-REQ-4: codeimpl.Result.Artifacts() builds a branch entry, one commit
// entry per task (plus a repair's, when one landed) and a pull_request
// entry from PullRequestURL, never from Submission or Survey.
func TestArtifactsBuildsBranchPerTaskCommitsAndPullRequest(t *testing.T) {
	r := &Result{
		Branch:            "impl/06-trim-results",
		BaseBranch:        "main",
		PullRequestURL:    "https://github.com/o/r/pull/9",
		PullRequestNumber: 9,
		Repair:            &RepairReport{Outcome: OutcomeDone, Commit: "repair123"},
		Tasks: []TaskReport{
			{ID: 1, Outcome: OutcomeDone, Commit: "aaa111"},
			{ID: 2, Outcome: OutcomeSkipped},
			{ID: 3, Outcome: OutcomeDone, Commit: "bbb222", Repair: &RepairReport{Outcome: OutcomeDone, Commit: "ccc333"}},
		},
		Survey: &Survey{Summary: "the model's own account, never read by Artifacts"},
	}

	artifacts := r.Artifacts()

	branch := findKind(t, artifacts, toolio.ArtifactBranch)
	if branch == nil || branch["name"] != r.Branch || branch["base"] != r.BaseBranch {
		t.Errorf("branch artifact = %v", branch)
	}

	var shas []string
	for _, a := range artifacts {
		if a.Kind == toolio.ArtifactCommit {
			shas = append(shas, a.SHA)
		}
	}
	for _, want := range []string{"repair123", "aaa111", "bbb222", "ccc333"} {
		found := false
		for _, s := range shas {
			if s == want {
				found = true
			}
		}
		if !found {
			t.Errorf("commit %q missing from artifacts, got %v", want, shas)
		}
	}

	pr := findKind(t, artifacts, toolio.ArtifactPullRequest)
	if pr == nil || pr["url"] != r.PullRequestURL {
		t.Errorf("pull_request artifact = %v", pr)
	}
	if _, ok := pr["dry_run"]; ok {
		t.Errorf("an opened pull request should carry no dry_run marker: %v", pr)
	}
}

// Under --dry-run with --land=pr, no pull request was opened; the entry
// reports it as hypothetical, and the branch stays unmarked since it
// happened locally either way (06-REQ-4.3, 06-REQ-4.4).
func TestArtifactsMarksDryRunPullRequestHypotheticalAndLeavesBranchUnmarked(t *testing.T) {
	r := &Result{
		Branch: "impl/06-trim-results",
		DryRun: true,
		Land:   string(LandPR),
	}
	artifacts := r.Artifacts()

	branch := findKind(t, artifacts, toolio.ArtifactBranch)
	if branch == nil {
		t.Fatal("expected a branch artifact")
	}
	if _, ok := branch["dry_run"]; ok {
		t.Errorf("branch artifact carries a dry_run marker: %v", branch)
	}

	pr := findKind(t, artifacts, toolio.ArtifactPullRequest)
	if pr == nil {
		t.Fatal("expected a hypothetical pull_request artifact")
	}
	if dr, _ := pr["dry_run"].(bool); !dr {
		t.Errorf("pull_request dry_run = %v, want true", pr["dry_run"])
	}
}
