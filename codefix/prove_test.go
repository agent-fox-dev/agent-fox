package codefix

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// issueOptions is a run on issue #1, made locally: --dry-run posts nothing
// and --land=none pushes nothing, and the commit and the rendered bodies
// still say what a real run would.
func issueOptions(t *testing.T, o Options, body string) Options {
	t.Helper()
	ref := issuex.IssueRef{Repo: issuex.Repo{Owner: "a", Name: "b", Host: "github.com"}, Number: 1}
	o.Input = toolio.Input{Kind: toolio.KindIssue, Origin: ref.URL(), Issue: &ref, Body: body}
	o.DryRun = true
	return o
}

func headMessage(t *testing.T, dir string) string {
	t.Helper()
	out, _, err := gitx.ExecRunner(context.Background(), dir, []string{"git", "log", "-1", "--format=%B"})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The default fix writes a regression test that fails without it: taken out,
// the checks fail, so the fix is proven and the issue is closed.
func TestAProvenFixClosesTheIssue(t *testing.T) {
	ws, g := newRepo(t, 0)
	o := issueOptions(t, newOptions(ws, g, defaultBrain()), "the counter double counts")
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rc := got.RevertCheck
	if rc == nil || !rc.Ran || !rc.Proves || strings.Join(rc.Reverted, ",") != "count.go" ||
		strings.Join(rc.Tests, ",") != "count_test.go" {
		t.Fatalf("RevertCheck = %+v", rc)
	}
	if !got.ClosesIssue || !strings.Contains(headMessage(t, ws.Root), "Closes #1") {
		t.Errorf("ClosesIssue=%v, commit:\n%s", got.ClosesIssue, headMessage(t, ws.Root))
	}
	if b, _ := os.ReadFile(filepath.Join(ws.Root, "count.go")); string(b) != "package x // fixed\n" {
		t.Errorf("the fix was not restored after the revert check: %q", b)
	}
	if body := pullRequestBody(got); !strings.Contains(body, "Closes #1") || !strings.Contains(body, "the tests depend on the fix") {
		t.Errorf("PR body:\n%s", body)
	}
}

// A fix whose tests pass without it proves nothing about it: the change still
// lands — it is verified — but the issue is referenced, not closed.
func TestAFixWhoseTestsPassWithoutItOnlyReferencesTheIssue(t *testing.T) {
	ws, g := newRepo(t, 0)
	b := defaultBrain()
	b.edit = func(root string) error {
		return os.WriteFile(filepath.Join(root, "count.go"), []byte("package x // fixed\n"), 0o644)
	}
	o := issueOptions(t, newOptions(ws, g, b), "the counter double counts")
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.ClosesIssue || got.RevertCheck == nil || got.RevertCheck.Proves {
		t.Fatalf("ClosesIssue=%v RevertCheck=%+v", got.ClosesIssue, got.RevertCheck)
	}
	msg := headMessage(t, ws.Root)
	if !strings.Contains(msg, "Refs #1") || strings.Contains(msg, "Closes") {
		t.Errorf("commit:\n%s", msg)
	}
	if !hasWarn(o.Run, toolio.WarnFixNotProven) {
		t.Error("no fix_not_proven warning")
	}
	for _, s := range []string{pullRequestBody(got), summaryComment(got)} {
		if !strings.Contains(s, "does not close the issue") || !strings.Contains(s, "still pass") {
			t.Errorf("the body does not say why the issue stays open:\n%s", s)
		}
	}
	if !strings.Contains(got.Summary(), "does not close #1") {
		t.Errorf("Summary() = %q", got.Summary())
	}
	if view, _ := json.Marshal(got.SummaryView()); !strings.Contains(string(view), `"closes_issue":false`) {
		t.Errorf("the default view hides that the issue stays open: %s", view)
	}
}

// A report that cites a spec's requirement and test ids is answered by a fix
// an independent review checks against them. What it finds unmet keeps the
// issue open, and the run says so.
func TestTheRequirementsAReportCitesAreReviewed(t *testing.T) {
	ws, g := newRepo(t, 0)
	specDir := filepath.Join(ws.Root, ".specs", "09_agent_mode")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"prd.md", "requirements.json", "test_spec.json", "tasks.json"} {
		b, err := os.ReadFile(filepath.Join("..", "testdata", "v2_example", name))
		if err != nil {
			t.Fatal(err)
		}
		write(t, specDir, name, string(b))
	}
	if _, err := g.CommitAll(context.Background(), "chore: add the spec\n"); err != nil {
		t.Fatal(err)
	}
	report := "TS-09-1 shows 09-REQ-1 is not met. (TS-77-1 is a typo.)"

	b := defaultBrain()
	b.review = func(in conform.ReviewInput) (conform.Review, error) {
		return conform.Review{Summary: "The flag is still ignored.",
			Requirements: []conform.RequirementRow{{ID: "09-REQ-1", Status: conform.StatusMissing,
				Evidence: "nothing in the diff reads the flag"}},
			Tests: []conform.TestRow{{ID: "TS-09-1", Test: "count_test.go:1", Assessment: conform.AssessNoAssertions,
				Evidence: "count_test.go has no test function"}}}, nil
	}
	o := issueOptions(t, newOptions(ws, g, b), report)
	got, err := Run(context.Background(), o)
	var f *Failure
	if !errors.As(err, &f) || f.Category != CategoryNonconformant || toolio.ExitCodeFor(f.Category) != toolio.ExitUnverified {
		t.Fatalf("err = %v", err)
	}
	if len(b.reviewIns) != 1 {
		t.Fatalf("review ran %d time(s)", len(b.reviewIns))
	}
	in := b.reviewIns[0]
	if strings.Join(in.Scope.Requirements, ",") != "09-REQ-1" || strings.Join(in.Scope.Tests, ",") != "TS-09-1" {
		t.Errorf("scope = %+v; an id no spec defines is dropped", in.Scope)
	}
	if in.Base != "HEAD" || !strings.Contains(in.Context, report) || !strings.Contains(in.Spec, "09-REQ-1") {
		t.Errorf("review input: base %q, context %q", in.Base, in.Context)
	}
	if got.ClosesIssue || len(got.Blocking) != 2 || !strings.Contains(headMessage(t, ws.Root), "Refs #1") {
		t.Errorf("ClosesIssue=%v Blocking=%+v", got.ClosesIssue, got.Blocking)
	}
	if body := pullRequestBody(got); !strings.HasPrefix(body, "## ❌ Not ready") || !strings.Contains(body, "## Conformance review") {
		t.Errorf("PR body:\n%s", body)
	}

	// --no-review leaves the decision to the revert check alone.
	ws, g = newRepo(t, 0)
	b = defaultBrain()
	o = issueOptions(t, newOptions(ws, g, b), report)
	o.NoReview = true
	if got, err = Run(context.Background(), o); err != nil || !got.ClosesIssue || len(b.reviewIns) != 0 {
		t.Errorf("--no-review: err=%v closes=%v reviews=%d", err, got.ClosesIssue, len(b.reviewIns))
	}
}

func hasWarn(r *toolio.Run, code toolio.WarnCode) bool {
	for _, w := range r.Warnings() {
		if w.Code == code {
			return true
		}
	}
	return false
}
