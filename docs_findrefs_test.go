package agentfox

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

// TS-17-42 (unit): The Navigation baseline intro in docs/development.md says
// seven read tools plus code_search, and the sentence after the jq snippet
// counts eight navigation columns.
// Verifies: 17-REQ-6.6
func TestTS17_42_NavigationBaselineIntroSaysSevenReadTools(t *testing.T) {
	sec := navigationBaseline(t)
	intro, _, _ := strings.Cut(sec, "### Inputs")
	if !strings.Contains(intro, "seven read") {
		t.Error("the intro does not say 'seven read'")
	}
	if !strings.Contains(intro, "code_search") {
		t.Error("the intro does not mention code_search")
	}
	if strings.Contains(intro, "six read") {
		t.Error("the intro still says 'six read'")
	}
	// The sentence starting with 'The last column counts every tool'.
	var para string
	for _, line := range strings.Split(sec, "\n") {
		if strings.HasPrefix(line, "The last column counts every tool") {
			para = line
			break
		}
	}
	if para == "" {
		t.Fatal("no paragraph starting with 'The last column counts every tool'")
	}
	if !strings.Contains(para, "eight") {
		t.Error("the last-column sentence does not say 'eight'")
	}
	if strings.Contains(para, "seven before it") {
		t.Error("the last-column sentence still says 'seven before it'")
	}
}

// TS-17-43 (unit): docs/cli.md, docs/model-usage.md and docs/development.md
// contain none of 'six read tools', 'six read-only file tools' or 'A seventh
// tool'.
// Verifies: 17-REQ-6.7
func TestTS17_43_NoSixReadToolsOrSeventhTool(t *testing.T) {
	for _, doc := range []string{"cli.md", "model-usage.md", "development.md"} {
		text := strings.ToLower(readDoc(t, doc))
		for _, phrase := range []string{"six read tools", "six read-only file tools", "a seventh tool"} {
			if strings.Contains(text, phrase) {
				t.Errorf("docs/%s contains %q", doc, phrase)
			}
		}
	}
}

// TS-17-44 (unit): PRD 09's status line is no longer 'proposed', and its row
// in docs/prds/README.md is outside the Proposed section.
// Verifies: 17-REQ-6.8
func TestTS17_44_PRD09StatusNotProposed(t *testing.T) {
	root := findWorkspaceRoot(t)
	prd, err := os.ReadFile(filepath.Join(root, "docs", "prds", "09-add-a-find-references-tool.md"))
	if err != nil {
		t.Fatalf("reading PRD 09: %v", err)
	}
	var status string
	for _, line := range strings.Split(string(prd), "\n") {
		if strings.HasPrefix(line, "Status:") {
			status = line
			break
		}
	}
	if status == "" {
		t.Fatal("PRD 09 has no Status: line")
	}
	if strings.Contains(strings.ToLower(status), "proposed") {
		t.Errorf("PRD 09 Status line still says proposed: %s", status)
	}

	readme, err := os.ReadFile(filepath.Join(root, "docs", "prds", "README.md"))
	if err != nil {
		t.Fatalf("reading docs/prds/README.md: %v", err)
	}
	readmeStr := string(readme)
	// Find the row linking PRD 09.
	var row string
	for _, line := range strings.Split(readmeStr, "\n") {
		if strings.Contains(line, "09-add-a-find-references-tool.md") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatal("docs/prds/README.md has no row linking 09-add-a-find-references-tool.md")
	}
	// Find the heading of the section that contains the row.
	lines := strings.Split(readmeStr, "\n")
	var sectionHeading string
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			sectionHeading = line
		}
		if strings.Contains(line, "09-add-a-find-references-tool.md") {
			break
		}
	}
	if sectionHeading == "## Proposed" {
		t.Errorf("PRD 09 row is still in the '## Proposed' section")
	}
}

// TestTS17_45_BaselineTablesHaveFindReferencesColumn checks that each of the
// four baseline tables has the find_references header, a before and an after
// row per phase with the header's cell count, and only dashes while the
// Measured at line names no commit.
// TS-17-45 (unit)
// Verifies: 17-REQ-7.1, 17-REQ-7.4
func TestTS17_45_BaselineTablesHaveFindReferencesColumn(t *testing.T) {
	sec := navigationBaseline(t)
	hdr := "| Input | Phase | Run | read_file | list_files | find_files | search_files | file_outline | find_symbol | find_references | code_search | all tools |"

	// Check Measured at line.
	_, measured, ok := strings.Cut(sec, "\nMeasured at: ")
	if !ok {
		t.Fatal("the baseline has no 'Measured at:' line")
	}
	measured, _, _ = strings.Cut(measured, "\n")
	if !strings.HasPrefix(measured, "— (commit), — (model)") {
		t.Errorf("Measured at line does not begin with '— (commit), — (model)': %s", measured)
	}

	for _, tool := range []string{"triage", "fix", "spec", "impl"} {
		_, table, ok := strings.Cut(sec, "\n#### "+tool+"\n")
		if !ok {
			t.Errorf("the baseline has no table for %s", tool)
			continue
		}
		if end := strings.Index(table, "\n#"); end >= 0 {
			table = table[:end]
		}
		// Check header.
		lines := strings.Split(table, "\n")
		var headerLine string
		for _, l := range lines {
			if strings.HasPrefix(l, "| Input") {
				headerLine = l
				break
			}
		}
		if headerLine != hdr {
			t.Errorf("the %s table header does not match:\n got: %s\nwant: %s", tool, headerLine, hdr)
		}
		// Count header cells.
		headerCells := strings.Split(strings.Trim(headerLine, "|"), "|")
		wantCells := len(headerCells) // 12
		if wantCells != 12 {
			t.Errorf("the %s table header has %d cells, want 12", tool, wantCells)
		}
		// Check data rows.
		runs := map[string]map[string]bool{}
		for _, line := range lines {
			if !strings.HasPrefix(line, "|") {
				continue
			}
			cells := strings.Split(strings.Trim(line, "|"), "|")
			for i := range cells {
				cells[i] = strings.TrimSpace(cells[i])
			}
			if cells[0] == "Input" || strings.HasPrefix(cells[0], "---") {
				continue
			}
			if len(cells) != wantCells {
				t.Errorf("the %s table row has %d cells, want %d: %s", tool, len(cells), wantCells, line)
				continue
			}
			// Every count cell is a dash.
			for _, c := range cells[3:] {
				if strings.TrimSpace(c) != "—" {
					t.Errorf("the %s table has a non-dash count cell %q", tool, c)
				}
			}
			key := cells[0] + "/" + cells[1]
			run := cells[2]
			if runs[key] == nil {
				runs[key] = map[string]bool{}
			}
			runs[key][run] = true
		}
		for key, seen := range runs {
			if !seen["before"] || !seen["after"] {
				t.Errorf("the %s table row %s lacks a before or an after row", tool, key)
			}
		}
	}
}

// TS-17-46 (unit): The jq snippet extracts find_references between find_symbol
// and code_search, and the procedure text says what the before and after pair
// measures.
// Verifies: 17-REQ-7.2, 17-REQ-7.3
func TestTS17_46_JqSnippetAndProcedureText(t *testing.T) {
	sec := navigationBaseline(t)
	a := strings.Index(sec, "(.tool_calls.find_symbol // 0)")
	b := strings.Index(sec, "(.tool_calls.find_references // 0)")
	c := strings.Index(sec, "(.tool_calls.code_search // 0)")
	if a < 0 || b < 0 || c < 0 {
		t.Fatalf("the jq snippet is missing one of find_symbol (%d), find_references (%d), code_search (%d)", a, b, c)
	}
	if !(a < b && b < c) {
		t.Errorf("the jq snippet does not order find_symbol (%d) < find_references (%d) < code_search (%d)", a, b, c)
	}
	// The procedure text about before and after.
	for _, w := range []string{"commit before", "does not exist", "after", "search_files", "read_file", "analyse", "survey"} {
		if !strings.Contains(sec, w) {
			t.Errorf("the procedure text does not mention %q", w)
		}
	}
	if !strings.Contains(sec, "fall") && !strings.Contains(sec, "drop") {
		t.Error("the procedure text does not say 'fall' or 'drop'")
	}
}

// TS-17-47 (unit): docs/errata/17_navigation_baseline.md exists in the form of
// errata 15 and 16, names the test that checks the column, tells the first
// runner to fill both rows and remove it, and docs/README.md lists it.
// Verifies: 17-REQ-7.5
func TestTS17_47_ErratumExists(t *testing.T) {
	root := findWorkspaceRoot(t)
	e, err := os.ReadFile(filepath.Join(root, "docs", "errata", "17_navigation_baseline.md"))
	if err != nil {
		t.Fatalf("reading erratum: %v", err)
	}
	erratum := string(e)
	if !strings.HasPrefix(erratum, "# Erratum:") {
		t.Error("the erratum's first line does not begin with '# Erratum:'")
	}
	for _, w := range []string{"find_references", "docs/development.md", "first person to run the procedure"} {
		if !strings.Contains(erratum, w) {
			t.Errorf("the erratum does not mention %q", w)
		}
	}
	// It names a Test function that exists in a root-package docs_*_test.go file.
	re := regexp.MustCompile(`Test[A-Za-z0-9_]+`)
	names := re.FindAllString(erratum, -1)
	var found bool
	for _, name := range names {
		// Check if this function exists in a docs_*_test.go file.
		matches, _ := filepath.Glob(filepath.Join(root, "docs_*_test.go"))
		for _, m := range matches {
			b, err := os.ReadFile(m)
			if err != nil {
				continue
			}
			src := string(b)
			if strings.Contains(src, "func "+name) && strings.Contains(src, "| find_symbol | find_references | code_search |") {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		t.Error("the erratum does not name a Test function in a docs_*_test.go file whose body asserts the find_references column header")
	}
	// docs/README.md lists it.
	readme, err := os.ReadFile(filepath.Join(root, "docs", "README.md"))
	if err != nil {
		t.Fatalf("reading docs/README.md: %v", err)
	}
	if !strings.Contains(string(readme), "errata/17_navigation_baseline.md") {
		t.Error("docs/README.md does not list errata/17_navigation_baseline.md")
	}
}

// TS-17-48 (unit): The root-package docs tests use a read-tool list equal to
// agentrun.ReadOnlyFileTools element for element and select phase-table rows
// by 'the seven read tools'.
// Verifies: 17-REQ-8.2
func TestTS17_48_DocsTestsUseSevenReadTools(t *testing.T) {
	if !slices.Equal(sevenReadTools, agentrun.ReadOnlyFileTools) {
		t.Errorf("sevenReadTools != agentrun.ReadOnlyFileTools: %v vs %v", sevenReadTools, agentrun.ReadOnlyFileTools)
	}
	root := findWorkspaceRoot(t)
	for _, f := range []string{"docs_symbols_test.go", "docs_codesearch_test.go"} {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			t.Fatalf("reading %s: %v", f, err)
		}
		src := string(b)
		if strings.Contains(src, "sixReadTools") {
			t.Errorf("%s still contains the identifier sixReadTools", f)
		}
		if strings.Contains(src, "the six read tools") {
			t.Errorf("%s still contains 'the six read tools'", f)
		}
		if !strings.Contains(src, "the seven read tools") {
			t.Errorf("%s does not contain 'the seven read tools'", f)
		}
	}
	// docs_codesearch_test.go pins the header with the find_references column.
	b, err := os.ReadFile(filepath.Join(root, "docs_codesearch_test.go"))
	if err != nil {
		t.Fatalf("reading docs_codesearch_test.go: %v", err)
	}
	if !strings.Contains(string(b), "| search_files | file_outline | find_symbol | find_references | code_search | all tools |") {
		t.Error("docs_codesearch_test.go does not pin the header with the find_references column after find_symbol")
	}
}
