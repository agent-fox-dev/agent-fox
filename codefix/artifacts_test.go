package codefix

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// findKind returns the JSON-decoded object of the first artifact of kind k,
// or nil when there is none.
func findKind(t *testing.T, artifacts []toolio.Artifact, k toolio.ArtifactKind) map[string]any {
	t.Helper()
	for _, a := range artifacts {
		if a.Kind != k {
			continue
		}
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
	return nil
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TS-06-21 (unit): artifacts entries carry exactly the fields fixed for
// their kind (06-REQ-4.1).
func TestTS0621_ArtifactEntriesCarryExactlyTheirKindsFields(t *testing.T) {
	r := &Result{
		Branch:            "fix/stop-double-counting",
		BaseBranch:        "main",
		Commit:            "abc1234",
		PullRequestURL:    "https://github.com/o/r/pull/5",
		PullRequestNumber: 5,
	}
	artifacts := r.Artifacts()

	branch := findKind(t, artifacts, toolio.ArtifactBranch)
	if branch == nil {
		t.Fatal("expected a branch artifact")
	}
	if got, want := keysOf(branch), []string{"base", "kind", "name"}; !equalStrings(got, want) {
		t.Errorf("branch artifact keys = %v, want %v", got, want)
	}

	pr := findKind(t, artifacts, toolio.ArtifactPullRequest)
	if pr == nil {
		t.Fatal("expected a pull_request artifact")
	}
	if got, want := keysOf(pr), []string{"kind", "number", "url"}; !equalStrings(got, want) {
		t.Errorf("pull_request artifact keys = %v, want %v", got, want)
	}

	commit := findKind(t, artifacts, toolio.ArtifactCommit)
	if commit == nil {
		t.Fatal("expected a commit artifact")
	}
	if got, want := keysOf(commit), []string{"branch", "kind", "sha"}; !equalStrings(got, want) {
		t.Errorf("commit artifact keys = %v, want %v", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TS-06-22 (unit): artifacts is built only from the Result's
// already-established fact fields, never from the model's own report
// (06-REQ-4.2).
func TestTS0622_ArtifactsUnaffectedByMutatingTheModelsReport(t *testing.T) {
	r := &Result{
		Branch:         "fix/stop-double-counting",
		BaseBranch:     "main",
		Commit:         "abc1234",
		PullRequestURL: "https://github.com/o/r/pull/5",
		Implementation: &Implementation{Summary: "the original report"},
	}
	before, err := json.Marshal(r.Artifacts())
	if err != nil {
		t.Fatalf("Marshal before: %v", err)
	}

	r.Implementation = &Implementation{Summary: "anything else entirely"}

	after, err := json.Marshal(r.Artifacts())
	if err != nil {
		t.Fatalf("Marshal after: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("Artifacts() changed after mutating Implementation:\nbefore: %s\nafter:  %s", before, after)
	}
}

// TS-06-23 (unit, fix half): under --dry-run --land=pr, no pull request was
// actually opened, and the hypothetical pull_request artifact carries
// dry_run: true (06-REQ-4.3).
func TestTS0623_DryRunPullRequestMarkedHypothetical(t *testing.T) {
	r := &Result{
		Stage:  "committed",
		Branch: "fix/stop-double-counting",
		Commit: "abc1234",
		DryRun: true,
		Land:   string(LandPR),
		// PullRequestURL stays empty: --dry-run never opens one.
	}
	pr := findKind(t, r.Artifacts(), toolio.ArtifactPullRequest)
	if pr == nil {
		t.Fatal("expected a hypothetical pull_request artifact")
	}
	if dr, _ := pr["dry_run"].(bool); !dr {
		t.Errorf("pull_request artifact dry_run = %v, want true", pr["dry_run"])
	}
}

// TS-06-24 (unit): under --dry-run, the branch and commit entries carry no
// dry_run marker, because the branch and the commit happened locally
// (06-REQ-4.4).
func TestTS0624_BranchAndCommitCarryNoDryRunMarkerUnderDryRun(t *testing.T) {
	r := &Result{
		Stage:  "committed",
		Branch: "fix/stop-double-counting",
		Commit: "abc1234",
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

	commit := findKind(t, artifacts, toolio.ArtifactCommit)
	if commit == nil {
		t.Fatal("expected a commit artifact")
	}
	if _, ok := commit["dry_run"]; ok {
		t.Errorf("commit artifact carries a dry_run marker: %v", commit)
	}
}

// TS-06-25 (unit): the existing result fields are unchanged alongside the
// new artifacts array (06-REQ-4.5).
func TestTS0625_ExistingResultFieldsUnchangedAlongsideArtifacts(t *testing.T) {
	r := &Result{
		Branch:            "fix/stop-double-counting",
		BaseBranch:        "main",
		Commit:            "abc1234",
		PullRequestURL:    "https://github.com/o/r/pull/5",
		PullRequestNumber: 5,
	}
	pr := findKind(t, r.Artifacts(), toolio.ArtifactPullRequest)
	if pr == nil {
		t.Fatal("expected a pull_request artifact")
	}
	if got, want := pr["url"], r.PullRequestURL; got != want {
		t.Errorf("artifacts pull_request.url = %v, want result.pull_request_url = %v", got, want)
	}
	if r.PullRequestURL != "https://github.com/o/r/pull/5" {
		t.Errorf("result.pull_request_url changed: %v", r.PullRequestURL)
	}
}

// TS-06-23 (comment case): under --dry-run on an issue input, the comment the
// run would have posted is reported as a hypothetical comment artifact with
// dry_run: true, and nothing reached the forge (06-REQ-4.3).
func TestTS0623_DryRunCommentMarkedHypothetical(t *testing.T) {
	o, forgeLog := sideEffectFixture(t, http.StatusOK)
	o.DryRun = true
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(*forgeLog) != 0 {
		t.Fatalf("dry run reached the forge: %v", *forgeLog)
	}

	var roles []string
	for _, a := range got.Artifacts() {
		if a.Kind != toolio.ArtifactComment {
			continue
		}
		if !a.DryRun {
			t.Errorf("comment artifact %+v does not carry dry_run", a)
		}
		if a.URL != "" {
			t.Errorf("a comment that was not posted has a URL: %+v", a)
		}
		roles = append(roles, a.Role)
	}
	want := []string{"analysis", "summary"}
	sort.Strings(roles)
	if !reflect.DeepEqual(roles, want) {
		t.Errorf("hypothetical comment roles = %v, want %v", roles, want)
	}
	if len(got.Comments) != 0 || len(got.CommentRefs) != 0 {
		t.Errorf("a dry run reported posted comments: %v %v", got.Comments, got.CommentRefs)
	}
}

// A comment that was posted carries no dry_run marker.
func TestPostedCommentCarriesNoDryRunMarker(t *testing.T) {
	r := &Result{
		Comments:    []string{"https://github.com/o/r/issues/1#c"},
		CommentRefs: []CommentRef{{Kind: "summary", URL: "https://github.com/o/r/issues/1#c"}},
	}
	c := findKind(t, r.Artifacts(), toolio.ArtifactComment)
	if c == nil {
		t.Fatal("expected a comment artifact")
	}
	if _, ok := c["dry_run"]; ok {
		t.Errorf("a posted comment carries a dry_run marker: %v", c)
	}
}
