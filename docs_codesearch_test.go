package agentfox

import (
	"strings"
	"testing"
)

// codeSearchPreflightExample is the check every tool's --preflight example
// carries once the index is wired in (16-REQ-7).
const codeSearchPreflightExample = `{"check": "code_search_index", "ok": true, "detail": "built"}`

// TS-16-27 (unit): docs/cli.md lists code_search alongside the six read tools
// for each tool
// Verifies: 16-REQ-9.1
func TestTS16_27_CLIListsCodeSearchForEachTool(t *testing.T) {
	content := readDoc(t, "cli.md")
	if !strings.Contains(content, "code_search") {
		t.Fatal("docs/cli.md does not mention code_search")
	}
	// Each tool's section names code_search, with its condition.
	for _, heading := range []string{"## `triage`", "## `fix`", "## `spec`", "## `impl`"} {
		section := docSection(t, content, heading)
		if !strings.Contains(section, "`code_search`") {
			t.Errorf("%s does not mention `code_search`", heading)
		}
		if !strings.Contains(section, "index is built") {
			t.Errorf("%s does not say code_search is available when the index is built", heading)
		}
		for _, tool := range sixReadTools {
			if !strings.Contains(section, "`"+tool+"`") {
				t.Errorf("%s does not list `%s` alongside `code_search`", heading, tool)
			}
		}
	}
	// The sections that say what the model may do list it too.
	for _, heading := range []string{"## `fix`", "## `impl`"} {
		section := docSection(t, content, heading)
		_, may, ok := strings.Cut(section, "### What the model may and may not do")
		if !ok {
			t.Errorf("%s has no 'What the model may and may not do' section", heading)
			continue
		}
		if !strings.Contains(may, "`code_search`") {
			t.Errorf("%s 'What the model may and may not do' does not list `code_search`", heading)
		}
	}
	// Every --preflight example carries the check, and the prose explains it.
	if n := strings.Count(content, codeSearchPreflightExample); n != 4 {
		t.Errorf("docs/cli.md has %d code_search_index example entries, want 4 (fix, impl, spec, triage)", n)
	}
	preflight := docSection(t, content, "--preflight")
	if !strings.Contains(preflight, "`code_search_index`") {
		t.Error("the --preflight section does not mention the code_search_index check")
	}
}

// TS-16-28 (unit): docs/model-usage.md lists code_search in the phase table
// and describes the indexed search
// Verifies: 16-REQ-9.2
func TestTS16_28_ModelUsageListsCodeSearchAndDescribesIndex(t *testing.T) {
	content := readDoc(t, "model-usage.md")
	rows := 0
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, "| `") || !strings.Contains(line, "`") {
			continue
		}
		if strings.Contains(line, "the six read tools") ||
			strings.HasPrefix(line, "| `triage` | `triage` |") {
			rows++
			if !strings.Contains(line, "`code_search` (if indexed)") {
				t.Errorf("phase-table row does not list `code_search` with its condition: %s", line)
			}
		}
	}
	if rows == 0 {
		t.Error("docs/model-usage.md has no phase-table rows that name the read tools")
	}
	if !strings.Contains(content, "conditional on the index") {
		t.Error("docs/model-usage.md does not say code_search is conditional on the index")
	}

	section := docSection(t, content, "Reading the codebase")
	for _, want := range []string{"code_search", "built once per run", "invalidat", "search_files"} {
		if !strings.Contains(section, want) {
			t.Errorf("'Reading the codebase' does not mention %q", want)
		}
	}
	for _, tool := range []string{"`fix`", "`impl`"} {
		if !strings.Contains(section, tool) {
			t.Errorf("'Reading the codebase' does not say %s invalidates the index", tool)
		}
	}
}

// TS-16-29 (unit): docs/development.md navigation baseline tables show
// code_search's effect on search_files call counts
// Verifies: 16-REQ-9.3
func TestTS16_29_NavigationBaselineHasCodeSearchColumn(t *testing.T) {
	section := navigationBaseline(t)
	if !strings.Contains(section, "code_search") || !strings.Contains(section, "search_files") {
		t.Fatal("the navigation baseline mentions neither code_search nor search_files")
	}
	for _, tool := range []string{"triage", "fix", "spec", "impl"} {
		_, table, ok := strings.Cut(section, "\n#### "+tool+"\n")
		if !ok {
			t.Errorf("the baseline has no table for %s", tool)
			continue
		}
		if end := strings.Index(table, "\n#"); end >= 0 {
			table = table[:end]
		}
		if !strings.Contains(table, "| search_files | file_outline | find_symbol | code_search | all tools |") {
			t.Errorf("the %s table has no code_search column after find_symbol", tool)
		}
	}
	if !strings.Contains(section, ".tool_calls.code_search") {
		t.Error("the jq snippet does not extract code_search")
	}
}
