package agentfox

import (
	"strconv"
	"strings"
	"testing"
)

// sevenReadTools is declared in docs_findrefs_test.go as agentrun.ReadOnlyFileTools.

// TS-15-12 (unit): docs/model-usage.md lists all seven read tools in the phase table
// Verifies: 15-REQ-6.1
func TestTS15_12_ModelUsagePhaseTableListsSevenReadTools(t *testing.T) {
	content := readDoc(t, "model-usage.md")
	if strings.Contains(content, "the four read tools") {
		t.Error("docs/model-usage.md still says 'the four read tools'")
	}
	if !strings.Contains(content, "the seven read tools") {
		t.Error("docs/model-usage.md does not say 'the seven read tools'")
	}
	for _, name := range []string{"file_outline", "find_symbol", "find_references"} {
		if !strings.Contains(content, name) {
			t.Errorf("docs/model-usage.md does not mention %s", name)
		}
	}
	// The triage row lists the tools explicitly, and the definition sentence
	// names all seven.
	for _, prefix := range []string{"| `triage` | `triage` |", "The \"seven read tools\""} {
		idx := strings.Index(content, prefix)
		if idx < 0 {
			t.Errorf("docs/model-usage.md has no %q", prefix)
			continue
		}
		rest := content[idx:]
		if end := strings.Index(rest, "\n\n"); end >= 0 {
			rest = rest[:end]
		}
		if nl := strings.Index(rest, "\n|"); nl >= 0 {
			rest = rest[:nl]
		}
		for _, tool := range sevenReadTools {
			if !strings.Contains(rest, "`"+tool+"`") {
				t.Errorf("%q does not list `%s`", prefix, tool)
			}
		}
	}
	// Every row that names the read tools is a phase-table row; none says
	// "the read tools" without the count.
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "| `") && strings.Contains(line, "the read tools") {
			t.Errorf("phase-table row does not say 'the seven read tools': %s", line)
		}
	}
}

// TS-15-13 (unit): docs/model-usage.md describes the symbol tools in the
// 'Reading the codebase' section
// Verifies: 15-REQ-6.2
func TestTS15_13_ModelUsageReadingTheCodebaseDescribesSymbolTools(t *testing.T) {
	section := docSection(t, readDoc(t, "model-usage.md"), "Reading the codebase")
	for _, want := range []string{"file_outline", "find_symbol", "ctags", "heuristics", "symbol table"} {
		if !strings.Contains(section, want) {
			t.Errorf("'Reading the codebase' does not mention %q", want)
		}
	}
	if !strings.Contains(section, "own symbol table") {
		t.Error("'Reading the codebase' does not say each phase holds its own symbol table")
	}
}

// TS-15-14 (unit): docs/cli.md lists seven read tools and includes
// symbol_backend in the preflight examples
// Verifies: 15-REQ-7.1, 15-REQ-7.2
func TestTS15_14_CLIListsSevenReadToolsAndSymbolBackend(t *testing.T) {
	content := readDoc(t, "cli.md")
	for _, name := range []string{"file_outline", "find_symbol", "find_references"} {
		if n := strings.Count(content, name); n < 4 {
			t.Errorf("docs/cli.md mentions %s %d times, want at least 4", name, n)
		}
	}
	// Every --preflight example carries the check.
	if n := strings.Count(content, `{"check": "symbol_backend", "ok": true, "detail": "ctags"}`); n != 4 {
		t.Errorf("docs/cli.md has %d symbol_backend example entries, want 4 (fix, impl, spec, triage)", n)
	}
	// The sections that say what the model may do name all seven read tools.
	for _, heading := range []string{"## `fix`", "## `impl`"} {
		section := docSection(t, content, heading)
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
	}
}

// TS-15-15 (unit): docs/cli.md --preflight prose mentions ctags detection
// Verifies: 15-REQ-7.3
func TestTS15_15_CLIPreflightProseMentionsCtags(t *testing.T) {
	section := docSection(t, readDoc(t, "cli.md"), "--preflight")
	if !strings.Contains(section, "universal-ctags") {
		t.Error("the --preflight section does not mention universal-ctags")
	}
	if !strings.Contains(section, "symbol backend") {
		t.Error("the --preflight section does not mention the symbol backend")
	}
	if !strings.Contains(section, "symbol_backend") {
		t.Error("the --preflight section does not mention the symbol_backend check")
	}
}

// TS-15-16 (unit): docs/development.md navigation baseline tables include
// file_outline and find_symbol columns, and show before-and-after counts per
// tool: every phase has a before row and an after row, every count cell is
// a dash or a number, and the dashes are consistent with the "Measured at"
// line — all dashes while nothing was measured, none once a commit is named.
// Verifies: 15-REQ-8.1, 15-REQ-8.2
func TestTS15_16_NavigationBaselineHasSymbolColumns(t *testing.T) {
	section := navigationBaseline(t)
	_, measured, ok := strings.Cut(section, "\nMeasured at: ")
	if !ok {
		t.Fatal("the baseline has no \"Measured at:\" line")
	}
	measured, _, _ = strings.Cut(measured, "\n")
	unmeasured := strings.HasPrefix(measured, "— (commit)")
	for _, tool := range []string{"triage", "fix", "spec", "impl"} {
		_, table, ok := strings.Cut(section, "\n#### "+tool+"\n")
		if !ok {
			t.Errorf("the baseline has no table for %s", tool)
			continue
		}
		if end := strings.Index(table, "\n#"); end >= 0 {
			table = table[:end]
		}
		if !strings.Contains(table, "| file_outline | find_symbol |") {
			t.Errorf("the %s table has no file_outline and find_symbol columns", tool)
		}
		if !strings.Contains(table, "| Input | Phase | Run |") {
			t.Errorf("the %s table has no Run column for the before and after rows", tool)
		}
		var want int
		runs := map[string]map[string]bool{} // input+phase -> run -> seen
		for _, line := range strings.Split(table, "\n") {
			if !strings.HasPrefix(line, "|") {
				continue
			}
			cells := strings.Split(strings.Trim(line, "|"), "|")
			for i := range cells {
				cells[i] = strings.TrimSpace(cells[i])
			}
			if want == 0 {
				want = len(cells)
				continue // the header
			}
			if len(cells) != want {
				t.Errorf("the %s table row has %d cells, want %d: %s", tool, len(cells), want, line)
				continue
			}
			if strings.HasPrefix(cells[0], "---") {
				continue // the separator
			}
			key, run := cells[0]+"/"+cells[1], cells[2]
			if run != "before" && run != "after" {
				t.Errorf("the %s table row %q has run %q, want before or after", tool, key, run)
			}
			if runs[key] == nil {
				runs[key] = map[string]bool{}
			}
			runs[key][run] = true
			for _, c := range cells[3:] {
				if c == "—" {
					if !unmeasured {
						t.Errorf("the %s table is measured at %q but %s/%s still has a dash", tool, measured, key, run)
					}
					continue
				}
				if _, err := strconv.Atoi(c); err != nil {
					t.Errorf("the %s table cell %q in %s/%s is neither a dash nor a count", tool, c, key, run)
				} else if unmeasured {
					t.Errorf("the %s table has a count in %s/%s while nothing was measured", tool, key, run)
				}
			}
		}
		if len(runs) == 0 {
			t.Errorf("the %s table has no rows", tool)
		}
		for key, seen := range runs {
			if !seen["before"] || !seen["after"] {
				t.Errorf("the %s table row %s lacks a before or an after row", tool, key)
			}
		}
	}
	for _, name := range []string{"file_outline", "find_symbol"} {
		if !strings.Contains(section, ".tool_calls."+name) {
			t.Errorf("the jq snippet does not extract %s", name)
		}
	}
	if strings.Contains(section, "four read-only file tools") {
		t.Error("the section still says 'four read-only file tools'")
	}
}
