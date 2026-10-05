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

// --dry-run is not nothing beside --preflight: a run that writes nothing to a
// forge needs no forge credential, so the forge checks are skipped under it.
func TestCLIDocPreflightDryRunParagraphIsTrue(t *testing.T) {
	doc := oneLine(readDoc(t, "cli.md"))
	if strings.Contains(doc, "`--dry-run` adds nothing to it") {
		t.Error("docs/cli.md still says --dry-run adds nothing to --preflight")
	}
	for _, want := range []string{"`--preflight --dry-run` answers whether the same run with `--dry-run` would start", "credential"} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/cli.md does not say %q about --preflight with --dry-run", want)
		}
	}
}

// Each tool's --detail summary table lists what the view keeps, preflight and
// estimate included, as docs/cli.md already says they survive.
func TestCLIDocSummaryTablesListPreflightEstimateAndStage(t *testing.T) {
	doc := readDoc(t, "cli.md")
	const marker = "Under `--detail summary` (the default) `result` keeps only:"
	parts := strings.Split(doc, marker)
	if len(parts) != 5 {
		t.Fatalf("found %d summary tables, want 4 (triage, fix, spec, impl)", len(parts)-1)
	}
	for i, tool := range []string{"triage", "fix", "spec", "impl"} {
		// The table runs from its first row to the next blank line.
		table := parts[i+1]
		table = table[strings.Index(table, "|"):]
		if cut := strings.Index(table, "\n\n"); cut >= 0 {
			table = table[:cut]
		}
		for _, field := range []string{"stage", "preflight", "estimate"} {
			if _, ok := tableRow(table, "| `"+field+"` |"); !ok {
				t.Errorf("%s's summary table has no `%s` row", tool, field)
			}
		}
	}
}

// impl's estimate counts no phase when nothing is pending, and the docs say so.
func TestCLIDocEstimateStatesTheZeroPendingRule(t *testing.T) {
	doc := oneLine(readDoc(t, "cli.md"))
	if !strings.Contains(doc, "no task is pending") || !strings.Contains(doc, "`estimate.phases` is `0`") {
		t.Error("docs/cli.md does not say impl's estimate.phases is 0 when no task is pending")
	}
}

// The deviations from spec 11 are recorded.
func TestSpec11ErratumRecordsTheDeviations(t *testing.T) {
	root := findWorkspaceRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docs", "errata", "11_preflight.md"))
	if err != nil {
		t.Fatalf("no erratum for the spec 11 deviations: %v", err)
	}
	for _, want := range []string{"11-REQ-1.4", "11-REQ-5.5", "TS-11-43", "check"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the erratum does not mention %q", want)
		}
	}
}

// The --effort default in the shared-flags table is the whole precedence below
// the flag: $AF_MODEL_EFFORT, else the tier's own effort, else unset
// (internal/toolio/cli.go, docs/configuration.md "Effort precedence").
func TestCLIDocEffortDefaultNamesTheTierStep(t *testing.T) {
	row, ok := tableRow(docSection(t, readDoc(t, "cli.md"), "Shared flags"), "| `--effort`")
	if !ok {
		t.Fatal("docs/cli.md has no --effort row in the shared-flags table")
	}
	if !strings.Contains(row, "`$AF_MODEL_EFFORT`, else the tier's effort, else unset") {
		t.Errorf("--effort row = %s, want its default to name the tier step", row)
	}
}

// The errata index describes the model-resolution erratum as it now reads: the
// variant layer was removed, so it no longer concerns an "extended-variant
// model".
func TestErrataIndexLineForModelResolutionIsCurrent(t *testing.T) {
	line, ok := tableRow(readDoc(t, "README.md"), "| [agentkit_model_resolution]")
	if !ok {
		t.Fatal("docs/README.md has no line for the agentkit_model_resolution erratum")
	}
	if strings.Contains(line, "extended-variant") || !strings.Contains(line, "variant layer was removed") {
		t.Errorf("index line = %s, want it to say the variant layer was removed", line)
	}
}

// impl's preflight checklist reports the repair phase's model and effort, and
// docs/cli.md says what the entry holds.
func TestCLIDocNamesTheRepairModelCredentialEntry(t *testing.T) {
	doc := oneLine(readDoc(t, "cli.md"))
	for _, want := range []string{"`repair_model_credential`", "names the model and the effort the repair phase will run at"} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/cli.md does not say %q about impl's repair_model_credential entry", want)
		}
	}
}
