package issuex_test

import (
	"os/exec"
	"testing"

	"github.com/agent-fox-dev/agentfox/issuex"
)

// TestParseRepo_TS_01_7 verifies TS-01-7: ParseRepo parses standard owner/repo and nested group/subgroup/project paths.
// Verifies: 01-REQ-3.1
func TestParseRepo_TS_01_7(t *testing.T) {
	r1, ok1 := issuex.ParseRepo("octocat/Hello-World")
	if !ok1 {
		t.Fatalf("ParseRepo(\"octocat/Hello-World\") returned ok = false, want true")
	}
	if r1.Owner != "octocat" || r1.Name != "Hello-World" {
		t.Errorf("ParseRepo(\"octocat/Hello-World\") = %+v, want Owner: \"octocat\", Name: \"Hello-World\"", r1)
	}

	r2, ok2 := issuex.ParseRepo("gitlab-org/subgroup/project")
	if !ok2 {
		t.Fatalf("ParseRepo(\"gitlab-org/subgroup/project\") returned ok = false, want true")
	}
	if r2.Owner != "gitlab-org/subgroup" || r2.Name != "project" {
		t.Errorf("ParseRepo(\"gitlab-org/subgroup/project\") = %+v, want Owner: \"gitlab-org/subgroup\", Name: \"project\"", r2)
	}

	r3, ok3 := issuex.ParseRepo("   org/nested/deep/repo   ")
	if !ok3 || r3.Owner != "org/nested/deep" || r3.Name != "repo" {
		t.Errorf("ParseRepo with deep nested segments failed: %+v, ok=%v", r3, ok3)
	}
}

// TestParseRepoRejections_TS_01_8 verifies TS-01-8: ParseRepo rejects empty strings and inputs lacking slash-delimited segments.
// Verifies: 01-REQ-3.2
func TestParseRepoRejections_TS_01_8(t *testing.T) {
	tests := []struct {
		input string
	}{
		{""},
		{"singleword"},
		{"/leading"},
		{"trailing/"},
		{"a//b"},
	}

	for _, tc := range tests {
		r, ok := issuex.ParseRepo(tc.input)
		if ok || r != (issuex.Repo{}) {
			t.Errorf("ParseRepo(%q) = (%+v, %v), want (Repo{}, false)", tc.input, r, ok)
		}
	}
}

// TestParseRemote_TS_01_9 verifies TS-01-9: ParseRemote successfully parses HTTPS, SSH, and SCP-style git remote URLs.
// Verifies: 01-REQ-3.3
func TestParseRemote_TS_01_9(t *testing.T) {
	r1, ok1 := issuex.ParseRemote("https://github.com/octocat/Hello-World.git")
	if !ok1 {
		t.Fatalf("ParseRemote(\"https://github.com/octocat/Hello-World.git\") returned ok = false, want true")
	}
	if r1.Host != "github.com" || r1.Owner != "octocat" || r1.Name != "Hello-World" {
		t.Errorf("ParseRemote https mismatch = %+v, want Host: \"github.com\", Owner: \"octocat\", Name: \"Hello-World\"", r1)
	}

	r2, ok2 := issuex.ParseRemote("git@gitlab.com:group/subgroup/repo.git")
	if !ok2 {
		t.Fatalf("ParseRemote(\"git@gitlab.com:group/subgroup/repo.git\") returned ok = false, want true")
	}
	if r2.Host != "gitlab.com" || r2.Owner != "group/subgroup" || r2.Name != "repo" {
		t.Errorf("ParseRemote scp mismatch = %+v, want Host: \"gitlab.com\", Owner: \"group/subgroup\", Name: \"repo\"", r2)
	}

	r3, ok3 := issuex.ParseRemote("ssh://git@github.com/org/repo.git")
	if !ok3 {
		t.Fatalf("ParseRemote(\"ssh://git@github.com/org/repo.git\") returned ok = false, want true")
	}
	if r3.Host != "github.com" || r3.Owner != "org" || r3.Name != "repo" {
		t.Errorf("ParseRemote ssh mismatch = %+v, want Host: \"github.com\", Owner: \"org\", Name: \"repo\"", r3)
	}

	r4, ok4 := issuex.ParseRemote("git@gitlab.com:deep/sub1/sub2/project.git")
	if !ok4 || r4.Host != "gitlab.com" || r4.Owner != "deep/sub1/sub2" || r4.Name != "project" {
		t.Errorf("ParseRemote multi-segment scp mismatch = %+v, ok = %v", r4, ok4)
	}

	r5, ok5 := issuex.ParseRemote("https://github.com/octocat/Hello-World")
	if !ok5 || r5.Host != "github.com" || r5.Owner != "octocat" || r5.Name != "Hello-World" {
		t.Errorf("ParseRemote without .git mismatch = %+v, ok = %v", r5, ok5)
	}
}

// TestParseRemoteRejections_TS_01_10 verifies TS-01-10: ParseRemote rejects invalid remote URLs and unsupported formats.
// Verifies: 01-REQ-3.4
func TestParseRemoteRejections_TS_01_10(t *testing.T) {
	tests := []struct {
		input string
	}{
		{""},
		{"http://"},
		{"not-a-remote-url"},
		{"https://bitbucket.org/owner/repo.git"},
		{"https://github.com/"},
		{"https://github.com/onlyone"},
		{"git@github.com:"},
		{"git@:repo.git"},
	}

	for _, tc := range tests {
		r, ok := issuex.ParseRemote(tc.input)
		if ok || r != (issuex.Repo{}) {
			t.Errorf("ParseRemote(%q) = (%+v, %v), want (Repo{}, false)", tc.input, r, ok)
		}
	}
}

// setupTempGitRepoWithOrigin creates a temporary directory initialized as a git repository with an origin remote.
func setupTempGitRepoWithOrigin(t *testing.T, originURL string) string {
	t.Helper()
	dir := t.TempDir()
	initCmd := exec.Command("git", "init", dir)
	if err := initCmd.Run(); err != nil {
		t.Fatalf("git init %s failed: %v", dir, err)
	}
	remoteCmd := exec.Command("git", "-C", dir, "remote", "add", "origin", originURL)
	if err := remoteCmd.Run(); err != nil {
		t.Fatalf("git remote add origin %s failed: %v", originURL, err)
	}
	return dir
}

// TestDetectRepo_TS_01_11 verifies TS-01-11: DetectRepo extracts origin remote from valid git repo and returns false gracefully for non-git dirs.
// Verifies: 01-REQ-3.5, 01-REQ-3.6
func TestDetectRepo_TS_01_11(t *testing.T) {
	gitDir := setupTempGitRepoWithOrigin(t, "git@github.com:agentfox/agent-fox.git")
	r1, ok1 := issuex.DetectRepo(gitDir)
	if !ok1 {
		t.Fatalf("DetectRepo(%q) returned ok = false, want true", gitDir)
	}
	if r1.Host != "github.com" || r1.Owner != "agentfox" || r1.Name != "agent-fox" {
		t.Errorf("DetectRepo(%q) = %+v, want Host: \"github.com\", Owner: \"agentfox\", Name: \"agent-fox\"", gitDir, r1)
	}

	nonGitDir := t.TempDir()
	r2, ok2 := issuex.DetectRepo(nonGitDir)
	if ok2 || r2 != (issuex.Repo{}) {
		t.Errorf("DetectRepo(%q) = (%+v, %v), want (Repo{}, false)", nonGitDir, r2, ok2)
	}
}

// TestParseIssueURL_TS_01_12 verifies TS-01-12: ParseIssueURL parses GitHub and GitLab web issue and PR/MR URLs into IssueRef.
// Verifies: 01-REQ-3.7
func TestParseIssueURL_TS_01_12(t *testing.T) {
	ref1, ok1 := issuex.ParseIssueURL("https://github.com/owner/repo/issues/123")
	if !ok1 {
		t.Fatalf("ParseIssueURL github issue returned ok = false")
	}
	if ref1.Repo.Owner != "owner" || ref1.Repo.Name != "repo" || ref1.Number != 123 || ref1.IsPullRequest {
		t.Errorf("ParseIssueURL github issue mismatch = %+v", ref1)
	}

	ref2, ok2 := issuex.ParseIssueURL("https://github.com/owner/repo/pull/456")
	if !ok2 {
		t.Fatalf("ParseIssueURL github pull returned ok = false")
	}
	if ref2.Repo.Owner != "owner" || ref2.Repo.Name != "repo" || ref2.Number != 456 || !ref2.IsPullRequest {
		t.Errorf("ParseIssueURL github pull mismatch = %+v", ref2)
	}

	ref3, ok3 := issuex.ParseIssueURL("https://gitlab.com/group/sub/proj/-/issues/789")
	if !ok3 {
		t.Fatalf("ParseIssueURL gitlab issue returned ok = false")
	}
	if ref3.Repo.Owner != "group/sub" || ref3.Repo.Name != "proj" || ref3.Number != 789 || ref3.IsPullRequest {
		t.Errorf("ParseIssueURL gitlab issue mismatch = %+v", ref3)
	}

	ref4, ok4 := issuex.ParseIssueURL("https://gitlab.com/group/sub/proj/-/merge_requests/101")
	if !ok4 {
		t.Fatalf("ParseIssueURL gitlab merge request returned ok = false")
	}
	if ref4.Repo.Owner != "group/sub" || ref4.Repo.Name != "proj" || ref4.Number != 101 || !ref4.IsPullRequest {
		t.Errorf("ParseIssueURL gitlab merge request mismatch = %+v", ref4)
	}

	// URL with trailing slash, query, or fragment
	ref5, ok5 := issuex.ParseIssueURL("https://github.com/owner/repo/issues/123/?foo=bar#anchor")
	if !ok5 || ref5.Number != 123 || ref5.Repo.Owner != "owner" || ref5.Repo.Name != "repo" || ref5.IsPullRequest {
		t.Errorf("ParseIssueURL with query/fragment mismatch = %+v, ok = %v", ref5, ok5)
	}

	// GitLab with trailing slash
	ref6, ok6 := issuex.ParseIssueURL("https://gitlab.com/group/sub/proj/-/issues/789/")
	if !ok6 || ref6.Number != 789 || ref6.Repo.Owner != "group/sub" || ref6.Repo.Name != "proj" || ref6.IsPullRequest {
		t.Errorf("ParseIssueURL gitlab trailing slash mismatch = %+v, ok = %v", ref6, ok6)
	}
}

// TestParseIssueURLRejections_TS_01_13 verifies TS-01-13: ParseIssueURL rejects non-issue paths, malformed URLs, and non-numeric identifiers.
// Verifies: 01-REQ-3.8
func TestParseIssueURLRejections_TS_01_13(t *testing.T) {
	tests := []struct {
		input string
	}{
		{"https://github.com/owner/repo/issues/abc"},
		{"https://github.com/owner/repo/commits/123"},
		{"not-a-url"},
		{""},
		{"https://gitlab.com/group/sub/proj/-/issues/xyz"},
		{"https://gitlab.com/group/sub/proj/-/unknown/123"},
		{"https://github.com/owner/repo/issues/0"},
		{"https://github.com/owner/repo/issues/-5"},
		{"https://gitlab.com/proj/-/issues/123"},
		{"https://example.com/owner/repo/issues/123"},
	}

	for _, tc := range tests {
		ref, ok := issuex.ParseIssueURL(tc.input)
		if ok || ref != (issuex.IssueRef{}) {
			t.Errorf("ParseIssueURL(%q) = (%+v, %v), want (IssueRef{}, false)", tc.input, ref, ok)
		}
	}
}
