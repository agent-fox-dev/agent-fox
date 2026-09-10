package issuex_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/issuex"
)

// TestNoOpClientLifecycleAndRepository_TS_01_27 verifies TS-01-27:
// NoOpClient returns unauthenticated status, nil close error, and zero repository.
// Verifies: 01-REQ-7.1, 01-REQ-7.2, 01-REQ-7.3
func TestNoOpClientLifecycleAndRepository_TS_01_27(t *testing.T) {
	client := issuex.NewNoOp()
	if client == nil {
		t.Fatal("expected non-nil Client from NewNoOp")
	}

	if client.Authenticated() {
		t.Errorf("expected Authenticated() to be false, got true")
	}

	if err := client.Close(); err != nil {
		t.Errorf("expected Close() to return nil, got %v", err)
	}

	ctx := context.Background()
	repo, err := client.GetRepository(ctx, issuex.Repo{Owner: "a", Name: "b"})
	if err != nil {
		t.Errorf("expected GetRepository() to return nil error, got %v", err)
	}
	if !reflect.DeepEqual(repo, issuex.Repository{}) {
		t.Errorf("expected zero-value Repository, got %+v", repo)
	}
}

// TestNoOpClientIssueOperations_TS_01_28 verifies TS-01-28:
// NoOpClient issue read and write methods succeed silently with zero values.
// Verifies: 01-REQ-7.4, 01-REQ-7.5
func TestNoOpClientIssueOperations_TS_01_28(t *testing.T) {
	ctx := context.Background()
	client := issuex.NewNoOp()
	if client == nil {
		t.Fatal("expected non-nil Client from NewNoOp")
	}
	ref := issuex.IssueRef{Repo: issuex.Repo{Owner: "a", Name: "b"}, Number: 1}

	// Reads
	thread, err := client.ReadIssue(ctx, ref)
	if err != nil {
		t.Errorf("ReadIssue: expected nil error, got %v", err)
	}
	if !reflect.DeepEqual(thread, issuex.IssueThread{}) {
		t.Errorf("ReadIssue: expected zero-value IssueThread, got %+v", thread)
	}

	list, err := client.ListIssues(ctx, ref.Repo, issuex.IssueFilter{})
	if err != nil {
		t.Errorf("ListIssues: expected nil error, got %v", err)
	}
	if !reflect.DeepEqual(list, issuex.IssueList{}) {
		t.Errorf("ListIssues: expected zero-value IssueList, got %+v", list)
	}

	clist, err := client.ListComments(ctx, ref)
	if err != nil {
		t.Errorf("ListComments: expected nil error, got %v", err)
	}
	if !reflect.DeepEqual(clist, issuex.CommentList{}) {
		t.Errorf("ListComments: expected zero-value CommentList, got %+v", clist)
	}

	// Writes
	iss, err := client.CreateIssue(ctx, ref.Repo, issuex.CreateIssueRequest{})
	if err != nil {
		t.Errorf("CreateIssue: expected nil error, got %v", err)
	}
	if !reflect.DeepEqual(iss, issuex.Issue{}) {
		t.Errorf("CreateIssue: expected zero-value Issue, got %+v", iss)
	}

	uiss, err := client.UpdateIssue(ctx, ref, issuex.UpdateIssueRequest{})
	if err != nil {
		t.Errorf("UpdateIssue: expected nil error, got %v", err)
	}
	if !reflect.DeepEqual(uiss, issuex.Issue{}) {
		t.Errorf("UpdateIssue: expected zero-value Issue, got %+v", uiss)
	}

	if err := client.CloseIssue(ctx, ref, ""); err != nil {
		t.Errorf("CloseIssue: expected nil error, got %v", err)
	}

	commentID, err := client.AddComment(ctx, ref, "")
	if err != nil {
		t.Errorf("AddComment: expected nil error, got %v", err)
	}
	if commentID != "" {
		t.Errorf("AddComment: expected empty string, got %q", commentID)
	}

	if err := client.AddLabels(ctx, ref, nil); err != nil {
		t.Errorf("AddLabels: expected nil error, got %v", err)
	}

	if err := client.RemoveLabel(ctx, ref, ""); err != nil {
		t.Errorf("RemoveLabel: expected nil error, got %v", err)
	}

	if err := client.CreateLabel(ctx, ref.Repo, issuex.Label{}); err != nil {
		t.Errorf("CreateLabel: expected nil error, got %v", err)
	}
}

// TestNoOpClientPullRequestOperationsRejected_TS_01_29 verifies TS-01-29:
// NoOpClient pull request methods return explicit errors requiring an active forge client.
// Verifies: 01-REQ-7.6
func TestNoOpClientPullRequestOperationsRejected_TS_01_29(t *testing.T) {
	ctx := context.Background()
	client := issuex.NewNoOp()
	if client == nil {
		t.Fatal("expected non-nil Client from NewNoOp")
	}
	ref := issuex.IssueRef{Repo: issuex.Repo{Owner: "a", Name: "b"}, Number: 1, IsPullRequest: true}
	const expectedErrSubstr = "pull request operations require an active forge client"

	checkErr := func(method string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: expected non-nil error, got nil", method)
			return
		}
		if !strings.Contains(err.Error(), expectedErrSubstr) {
			t.Errorf("%s: expected error containing %q, got %q", method, expectedErrSubstr, err.Error())
		}
	}

	_, err1 := client.CreatePullRequest(ctx, ref.Repo, issuex.CreatePullRequestRequest{})
	checkErr("CreatePullRequest", err1)

	_, err2 := client.ReadPullRequest(ctx, ref)
	checkErr("ReadPullRequest", err2)

	_, err3 := client.ReadChangedFiles(ctx, ref)
	checkErr("ReadChangedFiles", err3)

	_, err4 := client.GetPRState(ctx, ref)
	checkErr("GetPRState", err4)

	_, err5 := client.GetCIChecks(ctx, ref)
	checkErr("GetCIChecks", err5)

	_, err6 := client.GetPRReviews(ctx, ref)
	checkErr("GetPRReviews", err6)

	err7 := client.PostReviewComment(ctx, ref, "")
	checkErr("PostReviewComment", err7)

	_, err8 := client.MergePullRequest(ctx, ref, issuex.MergeOptions{})
	checkErr("MergePullRequest", err8)

	err9 := client.ClosePullRequest(ctx, ref)
	checkErr("ClosePullRequest", err9)
}
