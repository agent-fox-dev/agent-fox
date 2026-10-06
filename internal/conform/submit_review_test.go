package conform

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
)

func submitTo(t *testing.T, tool core.Tool, r any) core.ToolResult {
	t.Helper()
	in, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Execute(context.Background(), in)
}

// reviewRepo is a repository with a file a basename can name unambiguously
// (phase.go) and one it cannot (main.go, in two packages).
func reviewRepo(t *testing.T) string {
	_, root, _ := newRepo(t, map[string]string{
		"internal/agentrun/phase.go": "package agentrun\n\nfunc run() {}\n",
		"cmd/a/main.go":              "package main\n\nfunc main() {}\n",
		"cmd/b/main.go":              "package main\n\nfunc main() {}\n",
	})
	return root
}

var reviewScope = ReviewScope{Requirements: []string{"16-REQ-1", "16-REQ-2", "16-REQ-3"}, Tests: []string{"TS-16-1"}}

func goodReview() Review {
	return Review{Summary: "The change meets the spec.",
		Requirements: []RequirementRow{
			{ID: "16-REQ-1", Status: StatusImplemented, Evidence: "internal/agentrun/phase.go:3 runs the phase"},
			{ID: "16-REQ-2", Status: StatusImplemented, Evidence: "cmd/a/main.go:3 is the entry point"},
			{ID: "16-REQ-3", Status: StatusImplemented, Evidence: "cmd/b/main.go:3 is the other entry point"},
		},
		Tests: []TestRow{{ID: "TS-16-1", Test: "internal/agentrun/phase.go:3", Assessment: AssessAssertsContract,
			Evidence: "it asserts the phase's observable outcome"}},
	}
}

// Issue #195: one refusal names every row that is wrong, not the first, and
// a resubmission that carries only the refused rows is merged with the rows
// already accepted.
func TestSubmitReviewNamesEveryBadRowAndAcceptsAPartialResubmission(t *testing.T) {
	root := reviewRepo(t)
	var sink reviewSink
	tool := SubmitReviewTool(root, reviewScope, &sink)

	bad := goodReview()
	bad.Requirements[1].Evidence = "WarnCodeSearchUnavailable is declared in warncode.go"
	bad.Requirements[2].Evidence = "cmd/b/main.go:300 is the other entry point"
	bad.Tests[0].Assessment = "mostly"
	res := submitTo(t, tool, bad)
	if res.OK {
		t.Fatal("a review with three bad rows was accepted")
	}
	for _, id := range []string{"16-REQ-2", "16-REQ-3", "TS-16-1"} {
		if !strings.Contains(res.Detail, id) {
			t.Errorf("the refusal does not name %s:\n%s", id, res.Detail)
		}
	}
	if strings.Contains(res.Detail, "16-REQ-1:") {
		t.Errorf("the refusal names a good row:\n%s", res.Detail)
	}

	fix := goodReview()
	partial := map[string]any{
		"requirements": fix.Requirements[1:],
		"tests":        fix.Tests,
	}
	res = submitTo(t, tool, partial)
	if !res.OK {
		t.Fatalf("the resubmission of the refused rows was refused: %s: %s", res.Error, res.Detail)
	}
	got, ok := sink.get()
	if !ok || got.Summary != "The change meets the spec." || len(got.Requirements) != 3 || len(got.Tests) != 1 ||
		got.Requirements[0].Evidence != fix.Requirements[0].Evidence || got.Tests[0].Assessment != AssessAssertsContract {
		t.Errorf("the merged review = %+v", got)
	}
}

// A basename that names exactly one tracked file is a citation; one that
// names two is refused, with the candidates.
func TestSubmitReviewResolvesAnUnambiguousBasename(t *testing.T) {
	root := reviewRepo(t)
	r := goodReview()
	r.Requirements[0].Evidence = "phase.go:3 runs the phase"
	var sink reviewSink
	if res := submitTo(t, SubmitReviewTool(root, reviewScope, &sink), r); !res.OK {
		t.Errorf("phase.go:3 was refused: %s", res.Detail)
	}
	r.Requirements[1].Evidence = "main.go:3 is the entry point"
	res := submitTo(t, SubmitReviewTool(root, reviewScope, &reviewSink{}), r)
	if res.OK || !strings.Contains(res.Detail, "cmd/a/main.go") || !strings.Contains(res.Detail, "cmd/b/main.go") {
		t.Errorf("an ambiguous basename: ok=%v detail=%s", res.OK, res.Detail)
	}
}
