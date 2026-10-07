package agentfox

import (
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
)

// sevenReadTools is the seven read tools every phase registers (17-REQ-1).
var sevenReadTools = agentrun.ReadOnlyFileTools

// TS-17-37 (unit): docs/cli.md lists the seven read tools in each tool's
// section and in the fix and impl 'What the model may and may not do'
// sections, and says what find_references does there.
// Verifies: 17-REQ-6.1
func TestTS17_37_CLIListsSevenReadToolsInEachSection(t *testing.T) {
	cli := readDoc(t, "cli.md")
	for _, heading := range []string{"## `triage`", "## `fix`", "## `spec`", "## `impl`"} {
		section := docSection(t, cli, heading)
		for _, tool := range sevenReadTools {
			if !strings.Contains(section, "`"+tool+"`") {
				t.Errorf("%s does not list `%s`", heading, tool)
			}
		}
	}
	for _, heading := range []string{"## `fix`", "## `impl`"} {
		section := docSection(t, cli, heading)
		_, may, ok := strings.Cut(section, "### What the model may and may not do")
		if !ok {
			t.Errorf("%s has no 'What the model may and may not do' section", heading)
			continue
		}
		for _, tool := range sevenReadTools {
			if !strings.Contains(may, "`"+tool+"`") {
				t.Errorf("%s 'What the model may and may not do' does not list `%s`", heading, tool)
			}
		}
		// Check that find_references is described with one of the expected phrases.
		found := false
		for _, line := range strings.Split(may, "\n") {
			if strings.Contains(line, "`find_references`") {
				if strings.Contains(line, "who uses") || strings.Contains(line, "call sites") || strings.Contains(line, "reference sites") {
					found = true
					break
				}
			}
		}
		// Also check across sentences (may span lines).
		if !found {
			for _, phrase := range []string{"who uses", "call sites", "reference sites"} {
				if strings.Contains(may, "`find_references`") && strings.Contains(may, phrase) {
					found = true
					break
				}
			}
		}
		if !found {
			t.Errorf("%s 'What the model may and may not do' does not describe find_references with 'who uses', 'call sites' or 'reference sites'", heading)
		}
	}
}

// TS-17-38 (unit): The --preflight prose of docs/cli.md says 'three
// informational checks' and describes go_typecheck beside symbol_backend and
// code_search_index.
// Verifies: 17-REQ-6.2
func TestTS17_38_CLIPreflightProseSaysThreeInformationalChecks(t *testing.T) {
	s := docSection(t, readDoc(t, "cli.md"), "--preflight")
	if !strings.Contains(s, "three informational checks") {
		t.Error("the --preflight section does not say 'three informational checks'")
	}
	if strings.Contains(s, "two informational checks") {
		t.Error("the --preflight section still says 'two informational checks'")
	}
	for _, w := range []string{"`go_typecheck`", "packages checked", "errors", ", partial", "stub"} {
		if !strings.Contains(s, w) {
			t.Errorf("the --preflight section does not mention %q", w)
		}
	}
	// go_typecheck must be described between symbol_backend and code_search_index.
	idxSB := strings.Index(s, "`symbol_backend`")
	idxGT := strings.Index(s, "`go_typecheck`")
	idxCS := strings.Index(s, "`code_search_index`")
	if idxSB < 0 || idxGT < 0 || idxCS < 0 {
		t.Fatal("the --preflight section is missing one of symbol_backend, go_typecheck, code_search_index")
	}
	if !(idxSB < idxGT && idxGT < idxCS) {
		t.Errorf("go_typecheck is not between symbol_backend and code_search_index: sb=%d gt=%d cs=%d", idxSB, idxGT, idxCS)
	}
	// ok is always true.
	if !strings.Contains(s, "always `true`") && !strings.Contains(s, "always true") {
		t.Error("the --preflight section does not say go_typecheck ok is always true")
	}
}

// TS-17-39 (unit): Each of the four preflight examples in docs/cli.md carries
// the go_typecheck entry between symbol_backend and code_search_index.
// Verifies: 17-REQ-6.3
func TestTS17_39_CLIPreflightExamplesCarryGoTypecheck(t *testing.T) {
	cli := readDoc(t, "cli.md")
	entry := `{"check": "go_typecheck", "ok": true, "detail": "12 packages checked, 0 errors"}`
	if n := strings.Count(cli, entry); n != 4 {
		t.Errorf("docs/cli.md has %d go_typecheck example entries, want 4", n)
	}
	lines := strings.Split(cli, "\n")
	var sbLines []int
	for i, l := range lines {
		if strings.Contains(l, `"check": "symbol_backend"`) {
			sbLines = append(sbLines, i)
		}
	}
	if len(sbLines) != 4 {
		t.Fatalf("docs/cli.md has %d symbol_backend lines, want 4", len(sbLines))
	}
	for _, i := range sbLines {
		if i+1 >= len(lines) || !strings.Contains(lines[i+1], entry) {
			t.Errorf("the line after symbol_backend at line %d is not the go_typecheck entry", i+1)
		}
		if i+2 >= len(lines) || !strings.Contains(lines[i+2], `"check": "code_search_index"`) {
			t.Errorf("the line two after symbol_backend at line %d is not code_search_index", i+1)
		}
	}
}

// TS-17-40 (unit): docs/model-usage.md says 'seven read tools' in every
// phase-table row and the definition sentence, lists find_references in the
// triage row, and calls code_search the eighth tool.
// Verifies: 17-REQ-6.4
func TestTS17_40_ModelUsageSaysSevenReadTools(t *testing.T) {
	md := readDoc(t, "model-usage.md")
	// Count phase-table rows that name the read tools.
	var rows int
	for _, line := range strings.Split(md, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		if strings.Contains(line, "the seven read tools") ||
			strings.HasPrefix(line, "| `triage` | `triage` |") {
			rows++
		}
	}
	if rows < 9 {
		t.Errorf("docs/model-usage.md has %d phase-table rows naming the read tools, want at least 9", rows)
	}
	// The triage row lists all seven tools by name.
	triageIdx := strings.Index(md, "| `triage` | `triage` |")
	if triageIdx < 0 {
		t.Fatal("docs/model-usage.md has no triage row")
	}
	triageRow := md[triageIdx:]
	if nl := strings.Index(triageRow, "\n"); nl >= 0 {
		triageRow = triageRow[:nl]
	}
	for _, tool := range sevenReadTools {
		if !strings.Contains(triageRow, "`"+tool+"`") {
			t.Errorf("the triage row does not list `%s`", tool)
		}
	}
	// The definition sentence.
	defIdx := strings.Index(md, `The "seven read tools"`)
	if defIdx < 0 {
		t.Fatal(`docs/model-usage.md has no 'The "seven read tools"' sentence`)
	}
	defBlock := md[defIdx:]
	if end := strings.Index(defBlock, "\n\n"); end >= 0 {
		defBlock = defBlock[:end]
	}
	for _, tool := range sevenReadTools {
		if !strings.Contains(defBlock, "`"+tool+"`") {
			t.Errorf(`the "seven read tools" definition does not list %q`, tool)
		}
	}
	// "An eighth tool, `code_search`".
	if !strings.Contains(md, "An eighth tool, `code_search`") {
		t.Error("docs/model-usage.md does not say 'An eighth tool, `code_search`'")
	}
	if strings.Contains(md, "A seventh tool") {
		t.Error("docs/model-usage.md still says 'A seventh tool'")
	}
}

// TS-17-41 (unit): 'Reading the codebase' describes find_references: grouping,
// the three confidence labels, ranking and limits, the per-phase table, the
// bounds and partial, mostly-lexical AgentKit sites, and go_typecheck.
// Verifies: 17-REQ-6.5
func TestTS17_41_ReadingTheCodebaseDescribesFindReferences(t *testing.T) {
	s := docSection(t, readDoc(t, "model-usage.md"), "Reading the codebase")
	for _, w := range []string{
		"`find_references`",
		"enclosing declaration",
		"grouped by file",
		"`resolved`",
		"`lexical`",
		"`text`",
		"go/types",
		"stub",
		"30",
		"100",
		"own reference table",
		"bounded",
		"partial",
		"go_typecheck",
	} {
		if !strings.Contains(s, w) {
			t.Errorf("'Reading the codebase' does not mention %q", w)
		}
	}
	// AgentKit sites are mostly lexical.
	if !strings.Contains(s, "mostly `lexical`") {
		t.Error("'Reading the codebase' does not say AgentKit sites are mostly `lexical`")
	}
	if !strings.Contains(s, "AgentKit") {
		t.Error("'Reading the codebase' does not mention AgentKit in the lexical context")
	}
	// No longer opens with 'Six tools read the tree'.
	if strings.Contains(s, "Six tools read the tree") {
		t.Error("'Reading the codebase' still opens with 'Six tools read the tree'")
	}
}
