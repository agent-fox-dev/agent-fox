package fix

import (
	"fmt"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/checks"
)

// sampleFacts are the facts of a verified change that is proven.
func sampleFacts() BodyFacts {
	return BodyFacts{
		RunID:   runID,
		Verdict: checks.VerdictPassWasAlreadyFailing,
		Rows: []checks.Row{
			{Name: "make check", Before: "fail", After: "pass", Verdict: checks.VerdictPassWasAlreadyFailing},
			{Name: "vet", Before: "pass", After: "pass", Verdict: checks.VerdictPass},
		},
		RevertCheck: &RevertRef{Outcome: "proven", Reason: "tests_fail_without_the_change"},
		Reverted:    []string{".specs/a.md"},
		Report: Report{
			Type: "fix", Subject: "say hello", RootCause: "the script says helo", Change: "say hello",
			Files:     []File{{Path: "src/greet.sh", Why: "the word"}},
			Tests:     []Test{{Path: "tests/hello_test.sh", Name: "hello", Proves: "it says hello"}},
			Criteria:  []Criterion{{Criterion: "greets", Status: "met", Evidence: "the test"}},
			Decisions: []string{"kept the script"},
		},
		Changed:  []string{"tests/hello_test.sh", "src/greet.sh", "src/greet.sh"},
		IssueURL: "https://github.com/o/r/issues/7", Closes: true,
		Brain: "agentkit", CostUSD: 0.1234, CostSource: "measured",
	}
}

const unlimited = 1 << 30

func TestRenderBody_SaysWhatAfMeasuredThenWhatWasReportedThenTheFiles(t *testing.T) {
	body := RenderBody(sampleFacts(), issuex.GitHubMaxBodyLength)

	if !strings.HasPrefix(body, "<!-- agent-fox:run "+runID+" -->\n") {
		t.Errorf("the body starts %.40q, want the marker of the run", body)
	}
	inOrder(t, body, headingVerification, "Verdict: **pass_was_already_failing**",
		"| make check | fail | pass | pass_was_already_failing |", "| vet | pass | pass | pass |",
		"Revert check: proven (tests_fail_without_the_change)", "- `.specs/a.md`",
		headingRootCause, "> the script says helo", headingChange, headingTests, "> - `tests/hello_test.sh` (hello) — it says hello",
		headingCriteria, "> - [met] greets — the test", headingDecisions, "> - kept the script",
		headingFiles, "- `src/greet.sh` — the word\n- `tests/hello_test.sh` — "+NotDeclared+"\n",
		"\nCloses https://github.com/o/r/issues/7\n", "Run `"+runID+"` · brain `agentkit` · cost $0.1234 (measured)")
	if strings.Count(body, "`src/greet.sh` —") != 1 {
		t.Errorf("a changed file is listed twice:\n%s", body)
	}
}

func TestRenderBody_ClosesOnlyWhatWasProvenAndSaysWhatWasNotVerified(t *testing.T) {
	notProven, noIssue, noVerify := sampleFacts(), sampleFacts(), sampleFacts()
	notProven.Closes = false
	noIssue.IssueURL = ""
	noVerify.NoVerify, noVerify.Rows, noVerify.RevertCheck, noVerify.Verdict = true, nil, nil, checks.VerdictUnverified

	if b := RenderBody(notProven, unlimited); !strings.Contains(b, "\nRefs https://github.com/o/r/issues/7\n") || strings.Contains(b, "Closes ") {
		t.Errorf("not proven:\n%s", b)
	}
	if b := RenderBody(noIssue, unlimited); strings.Contains(b, "Refs ") || strings.Contains(b, "Closes ") {
		t.Errorf("no issue:\n%s", b)
	}
	if v := between(t, RenderBody(noVerify, unlimited), headingVerification, headingRootCause); !strings.Contains(v, "Not verified") || strings.Contains(v, "Verdict") || !strings.Contains(v, ".specs/a.md") {
		t.Errorf("--no-verify:\n%s", v)
	}
}

// What the brain reported is quoted: a heading in it is no section of the body, and a comment in
// it hides nothing.
func TestRenderBody_WhatTheBrainReportedCannotPassForWhatAfMeasured(t *testing.T) {
	f := sampleFacts()
	f.Report.RootCause = "line one\n\n## Verification (measured by af)\n\nVerdict: pass\n\n<!-- agent-fox:run 99 --> and <!-- hidden"

	body := RenderBody(f, unlimited)

	if strings.Count(body, "\n"+headingVerification+"\n") != 1 || !strings.Contains(body, "> ## Verification (measured by af)") || strings.Count(body, "<!--") != 1 {
		t.Errorf("a reported heading or comment is in the clear:\n%s", body)
	}
}

func TestRenderBody_NamesCannotBreakTheTableOrACodeSpan(t *testing.T) {
	f := sampleFacts()
	f.Rows = []checks.Row{{Name: "a | b\nc", Before: "pass", After: "fail", Verdict: checks.VerdictRegressed}}
	f.Changed = []string{"we`ird.go"}

	body := RenderBody(f, unlimited)

	if !strings.Contains(body, `| a \| b?c | pass | fail | regressed |`) || !strings.Contains(body, "- `we'ird.go` — "+NotDeclared) {
		t.Errorf("a name broke a cell or a code span:\n%s", body)
	}
}

func TestRenderBody_ASectionWithNothingReportedSaysSo(t *testing.T) {
	f := sampleFacts()
	f.Report.Tests, f.Report.Criteria, f.Report.Decisions, f.Changed = nil, nil, nil, nil

	body := RenderBody(f, unlimited)

	for _, h := range []string{headingTests, headingCriteria, headingDecisions, headingFiles} {
		if !strings.Contains(body, h+"\n\n"+none+"\n") {
			t.Errorf("%q is not followed by %q:\n%s", h, none, body)
		}
	}
	if strings.Contains(body, BodyCutMarker) {
		t.Error("an empty section was taken for a cut one")
	}
}

// words is text of n bytes or a little more, in paragraphs.
func words(n int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "word%d ", i%1000)
		if i%12 == 11 {
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

func TestRenderBody_FitsTheLimitAndNeverCutsWhatAfMeasured(t *testing.T) {
	for _, size := range []struct{ root, change, decision, tests, criteria, files int }{
		{0, 0, 0, 0, 0, 0},
		{10, 10, 10, 3, 3, 10},
		{70000, 10, 10, 0, 0, 5},
		{10, 300000, 3000, 20, 10, 50},
		{1500000, 1500000, 1500000, 60, 40, 500},
		{3000, 3000, 70000, 0, 0, 0},
		{0, 0, 0, 60, 40, 3000},
	} {
		f := sampleFacts()
		f.Report.RootCause, f.Report.Change, f.Report.Decisions = words(size.root), words(size.change), []string{words(size.decision)}
		f.Report.Tests, f.Report.Criteria, f.Changed = nil, nil, nil
		for i := range size.tests {
			f.Report.Tests = append(f.Report.Tests, Test{Path: fmt.Sprintf("tests/t%d_test.go", i), Proves: words(1500)})
		}
		for range size.criteria {
			f.Report.Criteria = append(f.Report.Criteria, Criterion{Criterion: words(400), Status: "met", Evidence: words(1200)})
		}
		for i := range size.files {
			f.Changed = append(f.Changed, fmt.Sprintf("pkg%d/file%04d.go", i%50, i))
		}
		whole := RenderBody(f, unlimited)
		verification := between(t, whole, headingVerification, headingRootCause)
		for _, limit := range []int{issuex.GitHubMaxBodyLength, issuex.GitLabMaxBodyLength} {
			body := RenderBody(f, limit)

			if len(body) > limit {
				t.Fatalf("%+v: %d bytes, over the limit of %d", size, len(body), limit)
			}
			if !strings.HasPrefix(body, "<!-- agent-fox:run ") || !strings.Contains(body, verification) || !strings.Contains(body, "\nCloses https://github.com/o/r/issues/7\n") {
				t.Fatalf("%+v at %d: the marker, what af measured or the Closes line was cut", size, limit)
			}
			inOrder(t, body, headingVerification, headingRootCause, headingChange, headingTests, headingCriteria, headingDecisions, headingFiles, "\nCloses ")
			if cut := len(whole) > limit; cut != strings.Contains(body, BodyCutMarker) || (!cut && body != whole) {
				t.Fatalf("%+v at %d: a body of %d bytes is cut: %v, and says so: %v", size, limit, len(whole), cut, strings.Contains(body, BodyCutMarker))
			}
		}
	}
}

func TestRenderBody_FactsOverTheLimitByThemselvesAreNotCut(t *testing.T) {
	body := RenderBody(sampleFacts(), 200)

	for _, want := range []string{"tests_fail_without_the_change", "Closes https://github.com/o/r/issues/7", "| make check | fail | pass |"} {
		if !strings.Contains(body, want) {
			t.Errorf("the body lacks %q:\n%s", want, body)
		}
	}
}

// The reported sections are cut the least important first, each only as far as it must, and
// the list of files only once they are all gone.
func TestRenderBody_CutsTheLeastImportantFirst(t *testing.T) {
	big := strings.Repeat("0123456789 ", 2000)
	f := sampleFacts()
	f.Report.Decisions = []string{big}
	whole := RenderBody(f, unlimited)

	body := RenderBody(f, len(whole)-5000)

	if decisions := between(t, body, headingDecisions, headingFiles); !strings.Contains(decisions, BodyCutMarker) || len(decisions) < 10000 {
		t.Errorf("the decisions are %d bytes, want most of them kept behind a marker", len(decisions))
	}
	inOrder(t, body, "the script says helo", "it says hello", "[met] greets")

	f.Report.Criteria = []Criterion{{Criterion: big, Status: "met"}}
	f.Report.Tests = []Test{{Path: "tests/t_test.go", Proves: big}}
	f.Report.Change, f.Report.RootCause = big, big
	whole = RenderBody(f, unlimited)

	body = RenderBody(f, len(whole)-len(big)-5000)

	if strings.Contains(between(t, body, headingDecisions, headingFiles), "0123456789") || !strings.Contains(between(t, body, headingCriteria, headingDecisions), BodyCutMarker) ||
		strings.Contains(between(t, body, headingTests, headingCriteria), BodyCutMarker) {
		t.Errorf("want the decisions gone, the criteria cut and the tests whole:\n%.2000s", body)
	}

	f = sampleFacts()
	for i := range 400 {
		f.Changed = append(f.Changed, fmt.Sprintf("pkg/file%03d.go", i))
	}
	whole = RenderBody(f, unlimited)

	body = RenderBody(f, len(whole)-3000)

	files := between(t, body, headingFiles, "Closes ")
	if !strings.Contains(files, "pkg/file000.go") || strings.Contains(files, "pkg/file399.go") || !strings.Contains(files, BodyCutMarker+" (") || !strings.Contains(files, "more files)") {
		t.Errorf("files = %.300s…, want the end of the list cut behind a marker", files)
	}
	if strings.Contains(body, "the script says helo") || strings.Count(body, BodyCutMarker) != 6 {
		t.Errorf("%d markers, want the five reported sections cut entirely before a file was", strings.Count(body, BodyCutMarker))
	}
}

// The line that says how many files are not shown is part of the body, so the cut of the list
// fits every limit, and takes no more than it must, whatever the number of digits.
func TestRenderBody_CutsTheListOfFilesTight(t *testing.T) {
	for _, n := range []int{500, 2500} {
		f := sampleFacts()
		f.Report, f.Changed = Report{}, nil
		for i := range n {
			f.Changed = append(f.Changed, fmt.Sprintf("pkg%d/file%04d.go", i%7, i))
		}
		whole := RenderBody(f, unlimited)
		for _, limit := range []int{len(whole) - 1, len(whole) - 700, len(whole) - 5000, 40000, 40001, 40002, 40003, 40017, 40031} {
			if limit >= len(whole) {
				continue
			}
			body := RenderBody(f, limit)
			if len(body) > limit || limit-len(body) > 120 {
				t.Errorf("%d files, a limit of %d: the body is %d bytes", n, limit, len(body))
			}
		}
	}
}
