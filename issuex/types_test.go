package issuex_test

import (
	"fmt"
	"testing"
	"testing/quick"

	"github.com/agent-fox-dev/agentfox/issuex"
)

// TestRepoValid verifies TS-01-2: Repo Valid method reports true only when both Owner and Name are non-empty.
// Verifies: 01-REQ-2.1
func TestRepoValid_TS_01_2(t *testing.T) {
	tests := []struct {
		repo issuex.Repo
		want bool
	}{
		{issuex.Repo{Owner: "foo", Name: "bar"}, true},
		{issuex.Repo{Owner: "group/sub", Name: "bar"}, true},
		{issuex.Repo{Owner: "", Name: "bar"}, false},
		{issuex.Repo{Owner: "foo", Name: ""}, false},
		{issuex.Repo{}, false},
	}

	for _, tc := range tests {
		if got := tc.repo.Valid(); got != tc.want {
			t.Errorf("Repo{%q, %q}.Valid() = %v, want %v", tc.repo.Owner, tc.repo.Name, got, tc.want)
		}
	}
}

// TestRepoStringProperty verifies TS-01-3: Repo String returns owner joined with name by slash for any non-empty segments.
// Verifies: 01-REQ-2.2
func TestRepoStringProperty_TS_01_3(t *testing.T) {
	f := func(owner, name string) bool {
		r := issuex.Repo{Owner: owner, Name: name}
		return r.String() == fmt.Sprintf("%s/%s", owner, name)
	}
	if err := quick.Check(f, nil); err != nil {
		t.Errorf("Repo.String() property failed: %v", err)
	}
}

// TestIssueRefStringProperty verifies TS-01-4: IssueRef String returns repo string and issue number joined by hash.
// Verifies: 01-REQ-2.3
func TestIssueRefStringProperty_TS_01_4(t *testing.T) {
	f := func(owner, name string, num int) bool {
		r := issuex.Repo{Owner: owner, Name: name}
		ref := issuex.IssueRef{Repo: r, Number: num}
		return ref.String() == fmt.Sprintf("%s#%d", r.String(), num)
	}
	if err := quick.Check(f, nil); err != nil {
		t.Errorf("IssueRef.String() property failed: %v", err)
	}
}

func TestIssueRefURL(t *testing.T) {
	tests := []struct {
		ref  issuex.IssueRef
		want string
	}{
		{
			ref:  issuex.IssueRef{Repo: issuex.Repo{Owner: "foo", Name: "bar"}, Number: 42, IsPullRequest: false},
			want: "https://github.com/foo/bar/issues/42",
		},
		{
			ref:  issuex.IssueRef{Repo: issuex.Repo{Owner: "foo", Name: "bar"}, Number: 42, IsPullRequest: true},
			want: "https://github.com/foo/bar/pull/42",
		},
		{
			ref:  issuex.IssueRef{Repo: issuex.Repo{Host: "gitlab.com", Owner: "group/sub", Name: "proj"}, Number: 10, IsPullRequest: false},
			want: "https://gitlab.com/group/sub/proj/-/issues/10",
		},
		{
			ref:  issuex.IssueRef{Repo: issuex.Repo{Host: "gitlab.com", Owner: "group/sub", Name: "proj"}, Number: 10, IsPullRequest: true},
			want: "https://gitlab.com/group/sub/proj/-/merge_requests/10",
		},
	}

	for _, tc := range tests {
		if got := tc.ref.URL(); got != tc.want {
			t.Errorf("IssueRef.URL() = %q, want %q", got, tc.want)
		}
	}
}

// TestDomainModelsInstantiation verifies TS-01-5: Issue, thread, comment, pull request, file, check, and review domain models instantiate properly.
// Verifies: 01-REQ-2.4, 01-REQ-2.5
func TestDomainModelsInstantiation_TS_01_5(t *testing.T) {
	iss := issuex.Issue{Number: 42, Title: "Bug", State: "open", Author: issuex.User{Login: "dev"}}
	if iss.Number != 42 || iss.Title != "Bug" || iss.State != "open" || iss.Author.Login != "dev" {
		t.Errorf("Issue instantiation mismatch: %+v", iss)
	}

	thread := issuex.IssueThread{Issue: iss, Comments: []issuex.Comment{{Body: "cmt"}}, Truncated: false, CommentsErr: nil}
	if len(thread.Comments) != 1 || thread.Comments[0].Body != "cmt" || thread.Truncated || thread.CommentsErr != nil {
		t.Errorf("IssueThread instantiation mismatch: %+v", thread)
	}

	pr := issuex.PullRequest{Number: 10, State: "open", HeadBranch: "feat", BaseBranch: "main"}
	if pr.Number != 10 || pr.State != "open" || pr.HeadBranch != "feat" || pr.BaseBranch != "main" {
		t.Errorf("PullRequest instantiation mismatch: %+v", pr)
	}

	file := issuex.ChangedFile{Filename: "a.go", Status: "modified", Additions: 3, Deletions: 1}
	if file.Filename != "a.go" || file.Status != "modified" || file.Additions != 3 || file.Deletions != 1 {
		t.Errorf("ChangedFile instantiation mismatch: %+v", file)
	}

	check := issuex.CheckRun{Name: "ci/test", Status: "completed", Conclusion: "success"}
	if check.Name != "ci/test" || check.Status != "completed" || check.Conclusion != "success" {
		t.Errorf("CheckRun instantiation mismatch: %+v", check)
	}

	rev := issuex.Review{Author: issuex.User{Login: "rev"}, State: "approved"}
	if rev.Author.Login != "rev" || rev.State != "approved" {
		t.Errorf("Review instantiation mismatch: %+v", rev)
	}
}

// TestConfigurationAndRequestModels verifies TS-01-6: Repository, label, filter, issue list, and request parameter models instantiate with default constants.
// Verifies: 01-REQ-2.6, 01-REQ-2.7
func TestConfigurationAndRequestModels_TS_01_6(t *testing.T) {
	repo := issuex.Repository{FullName: "o/r", DefaultBranch: "main", Permissions: issuex.RepoPermissions{Push: true}}
	if repo.FullName != "o/r" || repo.DefaultBranch != "main" || !repo.Permissions.Push {
		t.Errorf("Repository instantiation mismatch: %+v", repo)
	}

	lbl := issuex.Label{Name: "bug", Color: "ff0000"}
	if lbl.Name != "bug" || lbl.Color != "ff0000" {
		t.Errorf("Label instantiation mismatch: %+v", lbl)
	}

	filt := issuex.IssueFilter{State: "open", Limit: 100}
	if filt.State != "open" || filt.Limit != 100 {
		t.Errorf("IssueFilter instantiation mismatch: %+v", filt)
	}

	list := issuex.IssueList{Issues: []issuex.Issue{{Number: 1}}, Incomplete: false}
	if len(list.Issues) != 1 || list.Issues[0].Number != 1 || list.Incomplete {
		t.Errorf("IssueList instantiation mismatch: %+v", list)
	}

	clist := issuex.CommentList{Comments: []issuex.Comment{{Body: "ok"}}, Truncated: false}
	if len(clist.Comments) != 1 || clist.Comments[0].Body != "ok" || clist.Truncated {
		t.Errorf("CommentList instantiation mismatch: %+v", clist)
	}

	req := issuex.CreateIssueRequest{Title: "T", Body: "B", Labels: []string{"bug"}}
	if req.Title != "T" || req.Body != "B" || len(req.Labels) != 1 || req.Labels[0] != "bug" {
		t.Errorf("CreateIssueRequest instantiation mismatch: %+v", req)
	}

	upReq := issuex.UpdateIssueRequest{Title: "T2", Body: "B2"}
	if upReq.Title != "T2" || upReq.Body != "B2" {
		t.Errorf("UpdateIssueRequest instantiation mismatch: %+v", upReq)
	}

	prReq := issuex.CreatePullRequestRequest{Title: "PR", Body: "PR body", Head: "feat", Base: "main", Draft: true}
	if prReq.Title != "PR" || prReq.Head != "feat" || !prReq.Draft {
		t.Errorf("CreatePullRequestRequest instantiation mismatch: %+v", prReq)
	}

	mOpt := issuex.MergeOptions{Method: issuex.MergeMethodSquash, CommitTitle: "Squash merge"}
	if mOpt.Method != issuex.MergeMethodSquash || mOpt.CommitTitle != "Squash merge" {
		t.Errorf("MergeOptions instantiation mismatch: %+v", mOpt)
	}

	mRes := issuex.MergeResult{Merged: true, SHA: "abcdef"}
	if !mRes.Merged || mRes.SHA != "abcdef" {
		t.Errorf("MergeResult instantiation mismatch: %+v", mRes)
	}

	// Verify enum constants
	if issuex.MergeMethodDefault != "" ||
		issuex.MergeMethodMerge != "merge" ||
		issuex.MergeMethodSquash != "squash" ||
		issuex.MergeMethodRebase != "rebase" {
		t.Errorf("MergeMethod enum constants mismatch")
	}
}
