package agentfox

import (
	"slices"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/tools"
)

// TS-18-10 (unit): docs/model-usage.md says seven read tools and lists all
// seven including find_references
// Verifies: 18-REQ-6.1, 18-REQ-6.2
func TestTS18_10_ModelUsageSaysSevenReadToolsAndListsAll(t *testing.T) {
	content := readDoc(t, "model-usage.md")
	if strings.Contains(content, "six read tools") {
		t.Error("docs/model-usage.md still says 'six read tools'")
	}
	if !strings.Contains(content, "seven read tools") {
		t.Error("docs/model-usage.md does not say 'seven read tools'")
	}
	if !strings.Contains(content, "find_references") {
		t.Error("docs/model-usage.md does not mention find_references")
	}
	allSeven := []string{"read_file", "list_files", "find_files", "search_files", "file_outline", "find_symbol", "find_references"}
	for _, n := range allSeven {
		if !strings.Contains(content, n) {
			t.Errorf("docs/model-usage.md does not mention %s", n)
		}
	}
	// The triage row lists all seven tools explicitly.
	triagePrefix := "| `triage` | `triage` |"
	idx := strings.Index(content, triagePrefix)
	if idx < 0 {
		t.Fatal("docs/model-usage.md has no triage row")
	}
	triageRow := content[idx:]
	if nl := strings.Index(triageRow, "\n"); nl >= 0 {
		triageRow = triageRow[:nl]
	}
	for _, n := range allSeven {
		if !strings.Contains(triageRow, "`"+n+"`") {
			t.Errorf("triage row does not list `%s`", n)
		}
	}
	// The defining paragraph lists all seven.
	defPrefix := `The "seven read tools"`
	defIdx := strings.Index(content, defPrefix)
	if defIdx < 0 {
		t.Fatal(`docs/model-usage.md has no "seven read tools" definition`)
	}
	defPara := content[defIdx:]
	if end := strings.Index(defPara, "\n\n"); end >= 0 {
		defPara = defPara[:end]
	}
	for _, n := range allSeven {
		if !strings.Contains(defPara, "`"+n+"`") {
			t.Errorf(`"seven read tools" definition does not list %s`, n)
		}
	}
}

// TS-18-11 (unit): docs/cli.md says seven read tools, lists all seven, and
// adds no new preflight check for find_references
// Verifies: 18-REQ-7.1, 18-REQ-7.2
func TestTS18_11_CLISaysSevenReadToolsAndNoNewPreflightCheck(t *testing.T) {
	content := readDoc(t, "cli.md")
	if strings.Contains(content, "six read tools") {
		t.Error("docs/cli.md still says 'six read tools'")
	}
	if !strings.Contains(content, "seven read tools") {
		t.Error("docs/cli.md does not say 'seven read tools'")
	}
	if !strings.Contains(content, "find_references") {
		t.Error("docs/cli.md does not mention find_references")
	}
	// No preflight check entry named find_references.
	if strings.Contains(content, `"check": "find_references"`) {
		t.Error("docs/cli.md has a preflight check entry for find_references")
	}
	// symbol_backend check still present.
	if !strings.Contains(content, "symbol_backend") {
		t.Error("docs/cli.md does not mention symbol_backend")
	}
}

// TS-18-12 (unit): docs/development.md baseline tables have a find_references
// column with dashes in every cell
// Verifies: 18-REQ-8.1, 18-REQ-8.2
func TestTS18_12_BaselineTablesHaveFindReferencesColumn(t *testing.T) {
	section := navigationBaseline(t)
	for _, tool := range []string{"triage", "fix", "spec", "impl"} {
		_, table, ok := strings.Cut(section, "\n#### "+tool+"\n")
		if !ok {
			t.Errorf("the baseline has no table for %s", tool)
			continue
		}
		if end := strings.Index(table, "\n#"); end >= 0 {
			table = table[:end]
		}
		// Check column order: find_references after find_symbol and before code_search.
		if !strings.Contains(table, "| find_symbol | find_references | code_search |") {
			t.Errorf("the %s table does not have find_references between find_symbol and code_search", tool)
		}
		// Every data row has a dash in the find_references column.
		lines := strings.Split(table, "\n")
		var headerCells []string
		frIdx := -1
		for _, line := range lines {
			if !strings.HasPrefix(line, "|") {
				continue
			}
			cells := strings.Split(strings.Trim(line, "|"), "|")
			for i := range cells {
				cells[i] = strings.TrimSpace(cells[i])
			}
			if headerCells == nil {
				headerCells = cells
				for i, c := range cells {
					if c == "find_references" {
						frIdx = i
						break
					}
				}
				if frIdx < 0 {
					t.Errorf("the %s table header has no find_references column", tool)
					break
				}
				continue
			}
			// Skip separator row.
			if len(cells) > 0 && strings.HasPrefix(cells[0], "---") {
				continue
			}
			if frIdx < len(cells) {
				if cells[frIdx] != "—" {
					t.Errorf("the %s table data row has %q in find_references column, want —: %s", tool, cells[frIdx], line)
				}
			}
		}
	}
}

// TS-18-13 (unit): docs/development.md jq snippet extracts find_references
// between find_symbol and code_search
// Verifies: 18-REQ-8.3
func TestTS18_13_JqSnippetExtractsFindReferences(t *testing.T) {
	section := navigationBaseline(t)
	// Find the jq code block.
	jqStart := strings.Index(section, "```sh\nfor f in")
	if jqStart < 0 {
		jqStart = strings.Index(section, "```\nfor f in")
	}
	if jqStart < 0 {
		t.Fatal("the navigation baseline has no jq code block")
	}
	jqBlock := section[jqStart:]
	if end := strings.Index(jqBlock[3:], "```"); end >= 0 {
		jqBlock = jqBlock[:end+3+3]
	}
	if !strings.Contains(jqBlock, ".tool_calls.find_references // 0") {
		t.Error("the jq snippet does not extract find_references")
	}
	idxFR := strings.Index(jqBlock, "find_references")
	idxFS := strings.Index(jqBlock, "find_symbol")
	idxCS := strings.Index(jqBlock, "code_search")
	if idxFS < 0 || idxFR < 0 || idxCS < 0 {
		t.Fatal("the jq snippet is missing one of find_symbol, find_references, code_search")
	}
	if !(idxFS < idxFR && idxFR < idxCS) {
		t.Errorf("find_references is not between find_symbol and code_search in the jq snippet (find_symbol=%d, find_references=%d, code_search=%d)", idxFS, idxFR, idxCS)
	}
}

// TS-18-14 (unit): docs/development.md introductory text says seven read-only
// file tools and names find_references
// Verifies: 18-REQ-8.4
func TestTS18_14_BaselineIntroSaysSevenAndNamesFindReferences(t *testing.T) {
	section := navigationBaseline(t)
	if strings.Contains(section, "six read-only file tools") {
		t.Error("the baseline intro still says 'six read-only file tools'")
	}
	if !strings.Contains(section, "seven read-only file tools") && !strings.Contains(section, "7 read-only file tools") {
		t.Error("the baseline intro does not say 'seven read-only file tools'")
	}
	if !strings.Contains(section, "find_references") {
		t.Error("the baseline intro does not name find_references")
	}
}

// TS-18-25 (smoke): Documentation reflects seven read tools throughout.
//
// Verifies: 18-PATH-4, 18-REQ-6.1, 18-REQ-7.1, 18-REQ-8.1
//
// Real components: docs/model-usage.md, docs/cli.md, docs/development.md.
func TestTS18_25_DocumentationReflectsSevenReadToolsThroughout(t *testing.T) {
	// docs/model-usage.md: no "six read tools", at least one "seven read tools".
	modelUsage := readDoc(t, "model-usage.md")
	if strings.Contains(modelUsage, "six read tools") {
		t.Error("docs/model-usage.md still says 'six read tools'")
	}
	if !strings.Contains(modelUsage, "seven read tools") {
		t.Error("docs/model-usage.md does not say 'seven read tools'")
	}
	if !strings.Contains(modelUsage, "find_references") {
		t.Error("docs/model-usage.md does not mention find_references")
	}

	// docs/cli.md: no "six read tools", at least one "seven read tools".
	cli := readDoc(t, "cli.md")
	if strings.Contains(cli, "six read tools") {
		t.Error("docs/cli.md still says 'six read tools'")
	}
	if !strings.Contains(cli, "seven read tools") {
		t.Error("docs/cli.md does not say 'seven read tools'")
	}
	if !strings.Contains(cli, "find_references") {
		t.Error("docs/cli.md does not mention find_references")
	}

	// docs/development.md: baseline tables contain a find_references column.
	section := navigationBaseline(t)
	for _, tool := range []string{"triage", "fix", "spec", "impl"} {
		_, table, ok := strings.Cut(section, "\n#### "+tool+"\n")
		if !ok {
			t.Errorf("the baseline has no table for %s", tool)
			continue
		}
		if end := strings.Index(table, "\n#"); end >= 0 {
			table = table[:end]
		}
		if !strings.Contains(table, "find_references") {
			t.Errorf("the %s baseline table does not have a find_references column", tool)
		}
	}
}

// findReferencesShipped reports whether AgentKit's tools.All returns
// find_references. Until it does, SelectTools drops the name and no phase's
// model sees the tool (docs/errata/18_find_references_not_yet_in_tools_all.md).
func findReferencesShipped(t *testing.T) bool {
	t.Helper()
	all, err := tools.All(tools.Options{Workspace: &tools.Workspace{Root: t.TempDir()}})
	if err != nil {
		t.Fatalf("tools.All: %v", err)
	}
	return slices.ContainsFunc(all, func(tl core.Tool) bool { return tl.Name == "find_references" })
}

// The docs name find_references among the seven read tools, so every
// paragraph that does must also say the model gets it only once AgentKit ships
// it, for as long as that is true, and must stop saying so once it is not.
func TestFindReferencesDocsMatchWhatTheModelSees(t *testing.T) {
	const caveat = "AgentKit ships"
	shipped := findReferencesShipped(t)
	for _, name := range []string{"cli.md", "model-usage.md"} {
		for i, para := range strings.Split(readDoc(t, name), "\n\n") {
			if !strings.Contains(para, "find_references") {
				continue
			}
			switch has := strings.Contains(para, caveat); {
			case !shipped && !has:
				t.Errorf("docs/%s paragraph %d names find_references without saying the model gets it only once AgentKit ships it:\n%s", name, i, para)
			case shipped && has:
				t.Errorf("docs/%s paragraph %d still says find_references waits on AgentKit, which now ships it:\n%s", name, i, para)
			}
		}
	}
}
