package agentfox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// oneLine joins a wrapped passage into one line, so a phrase is found wherever
// the wrap fell.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// docs/cli.md says what `resumable` means for each tool the way the code
// computes it: a result at stage "preflight" is never resumable (11-REQ-6.4),
// including an ordinary run that refused there, because it wrote nothing.
func TestCLIDocResumableStatesThePreflightRule(t *testing.T) {
	doc := readDoc(t, "cli.md")
	i := strings.Index(doc, "`resumable` says whether re-running the *same input* continues")
	if i < 0 {
		t.Fatal("docs/cli.md has no sentence defining `resumable`")
	}
	sentence := oneLine(doc[i : i+strings.Index(doc[i:], "`fix_hint`")])
	for _, want := range []string{"preflight", "wrote nothing"} {
		if !strings.Contains(sentence, want) {
			t.Errorf("the `resumable` sentence does not mention %q:\n%s", want, sentence)
		}
	}
	if strings.Contains(sentence, "whenever a branch was named,") && !strings.Contains(sentence, "except") {
		t.Errorf("the `resumable` sentence states the rule without its preflight exception:\n%s", sentence)
	}
}

// The --context bound is described in the order the code checks it: before
// anything is fetched for text given as the argument, after the read for a
// file, an issue URL or stdin (internal/toolio/app.go).
func TestCLIDocContextBoundOrderMatchesTheCode(t *testing.T) {
	doc := readDoc(t, "cli.md")
	i := strings.Index(doc, "`--context` does not change `input.bytes`")
	if i < 0 {
		t.Fatal("docs/cli.md has no paragraph on --context and input.bytes")
	}
	para := oneLine(doc[i : i+strings.Index(doc[i:], "\n\n")])
	for _, want := range []string{"before anything is fetched", "a file, an issue URL or stdin", "after", "truncated"} {
		if !strings.Contains(para, want) {
			t.Errorf("the --context paragraph does not mention %q:\n%s", want, para)
		}
	}
}

// The spec summary view's traceability keeps the covered counts, so a package
// with no gaps does not print {} (specgen/summary.go, issue #57).
func TestCLIDocSummaryTraceabilityKeepsTheCoveredCounts(t *testing.T) {
	row, ok := tableRow(readDoc(t, "cli.md"), "| `traceability` | `{")
	if !ok {
		t.Fatal("docs/cli.md has no summary-view traceability row")
	}
	for _, want := range []string{"criteria_covered", "paths_covered", "criteria_uncovered", "paths_uncovered", "tests_unowned"} {
		if !strings.Contains(row, want) {
			t.Errorf("the traceability row does not name %q: %s", want, row)
		}
	}
	if strings.Contains(row, "dropped") {
		t.Errorf("the traceability row still says something is dropped: %s", row)
	}
}

// spec's next[] suggests impl on every package that validates, in split order
// (specgen/next.go), not only the first.
func TestCLIDocNextTableSuggestsImplOnEveryValidPackage(t *testing.T) {
	row, ok := tableRow(readDoc(t, "cli.md"), "| `spec` | `impl` on")
	if !ok {
		t.Fatal("docs/cli.md has no spec row in the next[] table")
	}
	if strings.Contains(row, "the first package") || !strings.Contains(row, "every package that validates") {
		t.Errorf("spec next[] row = %s, want impl on every package that validates", row)
	}
}

// triage was called issue until d8d8db5; no sentence still folds "like issue".
func TestCLIDocUsesTheToolsCurrentName(t *testing.T) {
	if doc := readDoc(t, "cli.md"); strings.Contains(doc, "folds like `issue`") {
		t.Error("docs/cli.md still says an unsplit input folds like `issue`")
	}
}

// A pull request that could not be opened is recorded on <owner>/<repo> with no
// number (06-REQ-5.3), and the docs say so.
func TestCLIDocSaysWhereAFailedOpenPRIsRecorded(t *testing.T) {
	doc := oneLine(readDoc(t, "cli.md"))
	if !strings.Contains(doc, "pull request that could not be opened") || !strings.Contains(doc, "`<owner>/<repo>` with no number") {
		t.Error("docs/cli.md does not say a pull request that could not be opened is recorded on `<owner>/<repo>` with no number")
	}
}

// The deliberate divergences from spec 06 are recorded, so the spec package
// does not read as unmet.
func TestSpec06ErratumRecordsTheDeviations(t *testing.T) {
	root := findWorkspaceRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docs", "errata", "06_summary_and_next.md"))
	if err != nil {
		t.Fatalf("no erratum for the spec 06 deviations: %v", err)
	}
	for _, want := range []string{"06-REQ-3.4", "06-REQ-6.2", "06-REQ-6.6", "criteria_covered", "Command()"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the erratum does not mention %q", want)
		}
	}
}
