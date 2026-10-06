package codeimpl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// sentence is model prose of about n characters.
func sentence(n int) string {
	const s = "The handler at internal/search/index.go:142 builds the trigram set before it writes the segment. "
	return strings.Repeat(s, n/len(s)+1)[:n]
}

// largeResult is a run on the scale of issue #190's: 22 blocking findings,
// 18 unmet items, 30 requirements, 40 tests and 20 decisions answered with
// evidence of realistic length, and 9 landed tasks with full reports.
func largeResult() *Result {
	r := &Result{SpecDir: ".specs/16_search", Title: "Indexed code search", TasksDone: 9, TasksTotal: 9,
		Gate:         []string{"make check"},
		Baseline:     GateResult{Checks: []checks.Result{{Command: "make check", OK: true, DurationMS: 4000}}},
		Verification: GateResult{Checks: []checks.Result{{Command: "make check", OK: true, DurationMS: 4100}}},
		Review:       &conform.Review{Summary: sentence(800)},
	}
	for i := 1; i <= 22; i++ {
		r.Blocking = append(r.Blocking, conform.Blocker{Key: fmt.Sprintf("16-REQ-%d.1", i), What: sentence(300)})
	}
	for i := 1; i <= 18; i++ {
		r.Unmet = append(r.Unmet, conform.Unmet{Source: conform.SourceReview,
			Requirement: fmt.Sprintf("16-REQ-%d.2", i), What: sentence(500)})
	}
	for i := 1; i <= 30; i++ {
		r.Review.Requirements = append(r.Review.Requirements, conform.RequirementRow{
			ID: fmt.Sprintf("16-REQ-%d.3", i), Status: conform.StatusImplemented, Evidence: sentence(700)})
	}
	for i := 1; i <= 40; i++ {
		r.Review.Tests = append(r.Review.Tests, conform.TestRow{ID: fmt.Sprintf("TS-16-%d", i),
			Test: "internal/search/index_test.go:88", Assessment: conform.AssessAssertsContract, Evidence: sentence(700)})
	}
	for i := 1; i <= 20; i++ {
		r.Review.Decisions = append(r.Review.Decisions, conform.DecisionRow{
			ID: fmt.Sprintf("D-%d", i), Status: conform.DecisionFollowed, Evidence: sentence(600)})
	}
	for i := 1; i <= 9; i++ {
		sub := &Submission{Summary: sentence(1500), Notes: sentence(800)}
		for j := 1; j <= 5; j++ {
			sub.TestVerdicts = append(sub.TestVerdicts, Verdict{ID: fmt.Sprintf("TS-16-%d", i*5+j),
				Verdict: "pass", Evidence: sentence(400), RedEvidence: sentence(300)})
		}
		r.Tasks = append(r.Tasks, TaskReport{ID: i, Title: fmt.Sprintf("Task %d", i), Outcome: OutcomeDone,
			Commit: "abc1234", Submission: sub})
	}
	return r
}

func landLarge(t *testing.T, forge *mockAuthClient, r *Result) (Options, issuex.Repo) {
	t.Helper()
	target := issuex.Repo{Owner: "agent-fox-dev", Name: "agent-fox", Host: "github.com"}
	st := &RunState{Target: target, target: target, branch: "impl/16-search", base: "main",
		spec: &afspec.Spec{SpecID: "16", Title: "Indexed code search"}}
	opts := Options{Land: LandPR, Run: toolio.NewRun("impl", "test"), Forge: forge}
	if _, err := LandPRChanges(context.Background(), opts, st, r); err != nil {
		t.Fatalf("LandPRChanges: %v", err)
	}
	return opts, target
}

// Issue #190: a run with many requirements, tests and tasks rendered a body
// GitHub refused (over 65536 characters), so no pull request was opened.
// The body must fit the forge's limit, keep what a reviewer must read first,
// and carry the rest as comments on the pull request, each within the limit.
func TestLandPRChangesKeepsTheBodyWithinTheForgeLimit(t *testing.T) {
	r := largeResult()
	if full := pullRequestBody(r); len(full) <= issuex.GitHubMaxBodyLength {
		t.Fatalf("the fixture is too small to test the limit: %d bytes", len(full))
	}
	forge := &mockAuthClient{authenticated: true,
		createdPR: issuex.PullRequest{URL: "https://github.com/agent-fox-dev/agent-fox/pull/7", Number: 7}}
	_, target := landLarge(t, forge, r)

	body := forge.capturedReq.Body
	if len(body) > issuex.GitHubMaxBodyLength {
		t.Fatalf("body is %d bytes, over the %d limit", len(body), issuex.GitHubMaxBodyLength)
	}
	for _, want := range []string{"Not ready: blocking findings", "Unmet requirements", "## Summary",
		"## Tasks", "## Verification", "`make check` passes", footer, "on this pull request"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q", want)
		}
	}
	if strings.Contains(body, "## Conformance review") || strings.Contains(body, "### Task 1:") {
		t.Error("body still carries the detail that was moved to comments")
	}

	if len(forge.comments) == 0 {
		t.Fatal("no comment carried the detail")
	}
	all := strings.Join(forge.comments, "\n")
	for _, c := range forge.comments {
		if len(c) > issuex.GitHubMaxBodyLength {
			t.Errorf("a comment is %d bytes, over the limit", len(c))
		}
	}
	for _, want := range []string{"## Conformance review", "### Task 1:", "### Task 9:", "TS-16-40", "D-20"} {
		if !strings.Contains(all, want) {
			t.Errorf("the comments lack %q", want)
		}
	}
	for _, ref := range forge.commentRefs {
		if ref.Repo != target || ref.Number != 7 {
			t.Errorf("comment posted to %+v, want %s#7", ref, target)
		}
	}
}

// A body within the limit is sent whole, and nothing is commented.
func TestLandPRChangesSendsASmallBodyWhole(t *testing.T) {
	r := largeResult()
	r.Review, r.Blocking, r.Unmet, r.Tasks = nil, nil, nil, r.Tasks[:1]
	forge := &mockAuthClient{authenticated: true, createdPR: issuex.PullRequest{Number: 7}}
	landLarge(t, forge, r)
	if forge.capturedReq.Body != pullRequestBody(r) {
		t.Error("a body within the limit was changed")
	}
	if len(forge.comments) != 0 {
		t.Errorf("%d comment(s) posted for a body that fit", len(forge.comments))
	}
}

// A detail comment that cannot be posted is a warning, not a failed landing:
// the pull request is open and the JSON report holds the full account.
func TestLandPRChangesWarnsWhenTheDetailCannotBePosted(t *testing.T) {
	forge := &mockAuthClient{authenticated: true, commentErr: errors.New("403"),
		createdPR: issuex.PullRequest{URL: "https://github.com/agent-fox-dev/agent-fox/pull/7", Number: 7}}
	r := largeResult()
	opts, _ := landLarge(t, forge, r)
	if r.PullRequestURL == "" || r.Stage != "landed" {
		t.Errorf("PullRequestURL=%q Stage=%q", r.PullRequestURL, r.Stage)
	}
	var warned bool
	for _, w := range opts.Run.Warnings() {
		warned = warned || w.Code == toolio.WarnCommentNotPosted
	}
	if !warned {
		t.Errorf("warnings = %+v, want %s", opts.Run.Warnings(), toolio.WarnCommentNotPosted)
	}
	se := opts.Run.SideEffects()
	if last := se[len(se)-1]; last.Action != "comment" || last.Kind != "detail" || last.OK ||
		last.Warning != toolio.WarnCommentNotPosted {
		t.Errorf("last side effect = %+v", last)
	}
}

// packSections loses and reorders nothing, and no piece exceeds the limit,
// even when one section is larger than the limit or has a line that is.
func TestPackSectionsSplitsOversizedSections(t *testing.T) {
	sections := []string{"short\n", strings.Repeat("a line of the review table\n", 40),
		strings.Repeat("é", 300) + "\n", "tail\n"}
	const max = 200
	pieces := packSections(sections, max)
	if got := strings.Join(pieces, ""); got != strings.Join(sections, "") {
		t.Fatal("packing changed the text")
	}
	for _, p := range pieces {
		if len(p) > max || !utf8.ValidString(p) {
			t.Errorf("piece of %d bytes (valid UTF-8: %v)", len(p), utf8.ValidString(p))
		}
	}
}
