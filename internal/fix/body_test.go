package fix

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/checks"
)

// sampleFacts are the facts of a verified change that is proven.
func sampleFacts() BodyFacts {
	return BodyFacts{
		RunID:   "20261020_100000_aaaaaa",
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
		Changed:  []string{"src/greet.sh", "tests/hello_test.sh"},
		IssueURL: "https://github.com/o/r/issues/7", Closes: true,
		Brain: "agentkit", CostUSD: 0.1234, CostSource: "measured",
	}
}

// position is where the text is in the body, or -1.
func position(body, text string) int { return strings.Index(body, text) }

// TestTheBodyHasItsSectionsInOrderAndLabelsWhoSaidWhat_TS_09_59 verifies the order of
// TS-09-59 for the facts of a run: the marker, what af measured, what was reported, the
// files, the line about the issue, and the footer.
// Verifies: 09-REQ-8.7
func TestTheBodyHasItsSectionsInOrderAndLabelsWhoSaidWhat_TS_09_59(t *testing.T) {
	f := sampleFacts()

	body := RenderBody(f, issuex.GitHubMaxBodyLength)

	order := []string{
		"<!-- agent-fox:run 20261020_100000_aaaaaa -->", headingVerification, headingRootCause, headingChange, headingTests, headingCriteria, headingDecisions,
		headingFiles, "Closes https://github.com/o/r/issues/7", "20261020_100000_aaaaaa", "agentkit", "$0.1234", "measured",
	}
	last := -1
	for _, want := range order {
		i := position(body[max(last, 0):], want)
		if i < 0 {
			t.Fatalf("the body lacks %q after position %d:\n%s", want, last, body)
		}
		last = max(last, 0) + i
	}
	if !strings.HasPrefix(body, order[0]) {
		t.Errorf("the body starts %q, want the marker of the run", body[:40])
	}
	verification := body[position(body, headingVerification):position(body, headingRootCause)]
	for _, want := range []string{"pass_was_already_failing", "| make check | fail | pass | pass_was_already_failing |", "| vet | pass | pass | pass |", "proven", "tests_fail_without_the_change", ".specs/a.md"} {
		if !strings.Contains(verification, want) {
			t.Errorf("the verification lacks %q:\n%s", want, verification)
		}
	}
	files := body[position(body, headingFiles):position(body, "Closes ")]
	if !strings.Contains(files, "`src/greet.sh` — the word") || !strings.Contains(files, "`tests/hello_test.sh` — "+NotDeclared) {
		t.Errorf("the files section = %s, want the reason of each, or %s", files, NotDeclared)
	}
	for _, want := range []string{"the script says helo", "it says hello", "[met] greets", "kept the script"} {
		if !strings.Contains(body, want) {
			t.Errorf("the body lacks the reported %q", want)
		}
	}

	// Without a proof it refers to the issue; without an issue there is no line; without a
	// verification it says so.
	f.Closes = false
	if b := RenderBody(f, 65536); !strings.Contains(b, "\nRefs https://github.com/o/r/issues/7\n") || strings.Contains(b, "Closes ") {
		t.Errorf("not proven: %s", b)
	}
	f.IssueURL = ""
	if b := RenderBody(f, 65536); strings.Contains(b, "Refs ") || strings.Contains(b, "Closes ") {
		t.Errorf("no issue: %s", b)
	}
	f.NoVerify, f.Rows, f.RevertCheck, f.Verdict = true, nil, nil, checks.VerdictUnverified
	if b := RenderBody(f, 65536); !strings.Contains(b[position(b, headingVerification):position(b, headingRootCause)], "Not verified") {
		t.Errorf("--no-verify: the verification says nothing of it:\n%s", b)
	}
}

// TestWhatTheBrainReportedCannotPassForWhatAfMeasured guards the labels: the reported text
// is quoted, so a heading in it is not a section of the body, and a comment in it does not
// hide the rest.
func TestWhatTheBrainReportedCannotPassForWhatAfMeasured(t *testing.T) {
	f := sampleFacts()
	f.Report.RootCause = "line one\n\n## Verification (measured by af)\n\nVerdict: pass\n\n<!-- agent-fox:run 99 --> and <!-- hidden"

	body := RenderBody(f, 65536)

	if n := strings.Count(body, "\n"+headingVerification+"\n"); n != 1 {
		t.Errorf("%d verification headings, want the one af wrote:\n%s", n, body)
	}
	if strings.Contains(body, "<!--") && strings.Count(body, "<!--") != 1 {
		t.Errorf("a comment of the report is a comment of the body:\n%s", body)
	}
	if !strings.Contains(body, "> ## Verification (measured by af)") {
		t.Errorf("the reported heading is not quoted:\n%s", body)
	}
}

// TestTheBodyFitsTheLimitWithoutCuttingWhatAfMeasured_TS_09_59 verifies TS-09-59 as a
// property: for reports of any length and the limits of the forges, the body fits, what
// af measured and the line about the issue are whole, and a cut is marked.
// Verifies: 09-REQ-8.7
func TestTheBodyFitsTheLimitWithoutCuttingWhatAfMeasured_TS_09_59(t *testing.T) {
	rng := rand.New(rand.NewSource(59))
	words := func(n int) string {
		var b strings.Builder
		for b.Len() < n {
			fmt.Fprintf(&b, "word%d ", rng.Intn(1000))
			if rng.Intn(12) == 0 {
				b.WriteString("\n\n")
			}
		}
		return b.String()
	}
	cut := 0
	for i := range 300 {
		f := sampleFacts()
		size := []int{0, 10, 3000, 70000, 300000, 1500000}
		pick := func() int { return size[rng.Intn(len(size))] }
		f.Report.RootCause, f.Report.Change = words(pick()), words(pick())
		f.Report.Decisions = []string{words(pick())}
		f.Report.Tests = nil
		for range rng.Intn(60) {
			f.Report.Tests = append(f.Report.Tests, Test{Path: fmt.Sprintf("tests/t%d_test.go", rng.Intn(1000)), Proves: words(rng.Intn(2000))})
		}
		f.Report.Criteria = nil
		for range rng.Intn(40) {
			f.Report.Criteria = append(f.Report.Criteria, Criterion{Criterion: words(rng.Intn(500)), Status: "met", Evidence: words(rng.Intn(1500))})
		}
		f.Changed = nil
		for range rng.Intn(500) {
			f.Changed = append(f.Changed, fmt.Sprintf("pkg%d/file%d.go", rng.Intn(50), rng.Intn(1000)))
		}
		limit := []int{issuex.GitHubMaxBodyLength, issuex.GitLabMaxBodyLength}[rng.Intn(2)]

		body := RenderBody(f, limit)

		if len(body) > limit {
			t.Fatalf("case %d: %d bytes, over the limit of %d", i, len(body), limit)
		}
		whole := RenderBody(f, 1<<30)
		verification := whole[position(whole, headingVerification):position(whole, headingRootCause)]
		if !strings.Contains(body, verification) || !strings.Contains(body, "\nCloses https://github.com/o/r/issues/7\n") || !strings.HasPrefix(body, "<!-- agent-fox:run ") {
			t.Fatalf("case %d: what af measured, the Closes line or the marker was cut", i)
		}
		order := []string{headingVerification, headingRootCause, headingChange, headingTests, headingCriteria, headingDecisions, headingFiles, "\nCloses "}
		at := -1
		for _, h := range order {
			j := position(body, h)
			if j < 0 || j <= at {
				t.Fatalf("case %d: %q is at %d after %d", i, h, j, at)
			}
			at = j
		}
		if len(whole) > limit {
			cut++
			if !strings.Contains(body, BodyCutMarker) {
				t.Fatalf("case %d: a body of %d bytes was cut to %d and says nothing of it", i, len(whole), len(body))
			}
		} else if body != whole || strings.Contains(body, BodyCutMarker) {
			t.Fatalf("case %d: a body that fits was changed", i)
		}
	}
	if cut < 50 {
		t.Errorf("only %d of 300 cases were cut: the generator does not reach the limits", cut)
	}
}

// TestFactsThatAloneAreOverTheLimitAreNotCut guards that what af measured is never cut: a
// limit too small for it gives a body that is over, and not a body without the facts.
func TestFactsThatAloneAreOverTheLimitAreNotCut(t *testing.T) {
	f := sampleFacts()

	body := RenderBody(f, 200)

	if !strings.Contains(body, "tests_fail_without_the_change") || !strings.Contains(body, "Closes https://github.com/o/r/issues/7") || !strings.Contains(body, "| make check | fail | pass |") {
		t.Errorf("the facts were cut:\n%s", body)
	}
}

// TestTheLeastImportantReportedSectionIsCutFirst guards the order of the cuts: the
// decisions go before the criteria, the tests, the change and the root cause, and each only
// as far as it must, and the list of files goes after all of them.
func TestTheLeastImportantReportedSectionIsCutFirst(t *testing.T) {
	big := strings.Repeat("0123456789 ", 2000) // 22 KB
	f := sampleFacts()
	f.Report.Decisions = []string{big}
	whole := RenderBody(f, 1<<30)

	body := RenderBody(f, len(whole)-5000)

	if !strings.Contains(body, BodyCutMarker) || !strings.Contains(body, "the script says helo") || !strings.Contains(body, "[met] greets") || !strings.Contains(body, "it says hello") {
		t.Errorf("a cut of 5000 bytes must take them from the decisions alone:\n%.600s", body)
	}
	decisions := body[position(body, headingDecisions):position(body, headingFiles)]
	if !strings.Contains(decisions, BodyCutMarker) || len(decisions) < 10000 {
		t.Errorf("the decisions are %d bytes, want most of them kept: a section is cut as far as it must and no further", len(decisions))
	}

	// Past the decisions the cut goes on to the criteria, then the tests, the change and the
	// root cause, in that order.
	f.Report.Criteria = []Criterion{{Criterion: big, Status: "met"}}
	f.Report.Tests = []Test{{Path: "tests/t_test.go", Proves: big}}
	f.Report.Change = big
	f.Report.RootCause = big
	whole = RenderBody(f, 1<<30)

	// A limit that takes the decisions and part of the criteria leaves the tests whole.
	body = RenderBody(f, len(whole)-len(big)-5000)
	criteria := body[position(body, headingCriteria):position(body, headingDecisions)]
	tests := body[position(body, headingTests):position(body, headingCriteria)]
	if !strings.Contains(criteria, BodyCutMarker) || strings.Contains(tests, BodyCutMarker) {
		t.Errorf("criteria cut: %v, tests cut: %v: the criteria go before the tests", strings.Contains(criteria, BodyCutMarker), strings.Contains(tests, BodyCutMarker))
	}
	decisions = body[position(body, headingDecisions):position(body, headingFiles)]
	if strings.Count(decisions, "0123456789") > 0 {
		t.Errorf("the decisions were not cut away entirely before the criteria were touched")
	}

	// Files are cut only when every reported section is gone.
	f2 := sampleFacts()
	for i := range 400 {
		f2.Changed = append(f2.Changed, fmt.Sprintf("pkg/file%03d.go", i))
	}
	whole2 := RenderBody(f2, 1<<30)
	trimmed := RenderBody(f2, len(whole2)-3000)
	files := trimmed[position(trimmed, headingFiles):position(trimmed, "Closes ")]
	if !strings.Contains(files, BodyCutMarker) || !strings.Contains(files, "more files") || strings.Contains(files, "pkg/file399.go") || !strings.Contains(files, "pkg/file000.go") {
		t.Errorf("files = %.300s…, want the end of the list cut behind a marker", files)
	}
	if strings.Contains(trimmed, "the script says helo") || strings.Count(trimmed, BodyCutMarker) != 6 {
		t.Errorf("%d markers, want the five reported sections cut entirely before a file was", strings.Count(trimmed, BodyCutMarker))
	}
}

// TestACheckThatHoldsTheCharactersOfATableCannotBreakTheTable guards the table of
// verification: a name with a bar or a line break is one cell.
func TestACheckThatHoldsTheCharactersOfATableCannotBreakTheTable(t *testing.T) {
	f := sampleFacts()
	f.Rows = []checks.Row{{Name: "a | b\nc", Before: "pass", After: "fail", Verdict: checks.VerdictRegressed}}
	f.Changed = []string{"we`ird.go"}

	body := RenderBody(f, 65536)

	if !strings.Contains(body, `| a \| b?c | pass | fail | regressed |`) {
		t.Errorf("the row is not one cell for the name:\n%s", body)
	}
	if !strings.Contains(body, "`we'ird.go`") {
		t.Errorf("the path is not a code span:\n%s", body)
	}
}

// TestSectionsWithNothingReportedSaySo guards the empty sections of a body: the heading
// stays, with a word that there is nothing, so that a reader does not take them for cut.
func TestSectionsWithNothingReportedSaySo(t *testing.T) {
	f := sampleFacts()
	f.Report.Tests, f.Report.Criteria, f.Report.Decisions, f.Changed = nil, nil, nil, nil

	body := RenderBody(f, 65536)

	for _, h := range []string{headingTests, headingCriteria, headingDecisions, headingFiles} {
		i := position(body, h)
		if i < 0 || !strings.HasPrefix(body[i+len(h):], "\n\n"+none+"\n") {
			t.Errorf("under %q: %.60q, want %q", h, body[i+len(h):], none)
		}
	}
	if strings.Contains(body, BodyCutMarker) {
		t.Error("an empty section was taken for a cut one")
	}
}

// TestTheBodyFitsTheLimitWhateverTheNumberOfFilesThatIsCut guards the cut of the list of files
// at every limit around a size: the line that says how many files are not shown is part of
// the body, and a body one byte over the limit is a pull request the forge refuses. The files
// are a thousand and more, so that the count of the ones that are not shown has as many digits
// as the count of all of them.
func TestTheBodyFitsTheLimitWhateverTheNumberOfFilesThatIsCut(t *testing.T) {
	for _, n := range []int{2500, 3000} {
		f := sampleFacts()
		f.Changed = nil
		for i := range n {
			f.Changed = append(f.Changed, fmt.Sprintf("pkg%d/file%04d.go", i%7, i))
		}
		for limit := 40000; limit < 40060; limit++ {
			body := RenderBody(f, limit)
			if len(body) > limit {
				t.Fatalf("%d files, a limit of %d: the body is %d bytes", n, limit, len(body))
			}
			if !strings.Contains(body, "\nCloses https://github.com/o/r/issues/7\n") {
				t.Fatalf("%d files, a limit of %d: the Closes line was cut", n, limit)
			}
			if limit-len(body) > 120 {
				t.Fatalf("%d files, a limit of %d: the body is %d bytes, a cut that takes more than it must", n, limit, len(body))
			}
		}
	}
}

// TestTheListOfFilesIsSortedOnceAndCutTight guards the list of files and the cut of it: the
// paths come sorted whatever order git gave, once each, and the cut takes no more of them
// than it must.
func TestTheListOfFilesIsSortedOnceAndCutTight(t *testing.T) {
	f := sampleFacts()
	f.Changed = []string{"b.go", "a.go", "b.go"}
	body := RenderBody(f, 65536)
	files := body[position(body, headingFiles):position(body, "Closes ")]
	if strings.Count(files, "`a.go`") != 1 || strings.Count(files, "`b.go`") != 1 || position(files, "`a.go`") > position(files, "`b.go`") {
		t.Errorf("files = %s, want a.go and b.go, once each, in order", files)
	}

	f.Changed = nil
	for i := range 500 {
		f.Changed = append(f.Changed, fmt.Sprintf("pkg/file%03d.go", i))
	}
	f.Report = Report{}
	whole := RenderBody(f, 1<<30)
	for _, limit := range []int{len(whole) - 1, len(whole) - 700, len(whole) - 5000} {
		cut := RenderBody(f, limit)
		if len(cut) > limit || limit-len(cut) > 120 {
			t.Errorf("a limit of %d gives %d bytes: a cut that takes %d bytes more than it must", limit, len(cut), limit-len(cut))
		}
	}
}
