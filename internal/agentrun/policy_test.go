package agentrun

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
)

// TS-17-1 (unit): ReadOnlyFileTools holds exactly the seven read tools in
// order with find_references last, and its doc comment names the new tool.
//
// Verifies: 17-REQ-1.1
func TestTS17_1_ReadOnlyFileToolsHasTheSevenReadTools(t *testing.T) {
	want := []string{"read_file", "list_files", "find_files", "search_files", "file_outline", "find_symbol", "find_references"}
	if !slices.Equal(ReadOnlyFileTools, want) {
		t.Errorf("ReadOnlyFileTools = %v, want %v", ReadOnlyFileTools, want)
	}
	if len(ReadOnlyFileTools) != 7 {
		t.Errorf("len = %d, want 7", len(ReadOnlyFileTools))
	}
	if len(ReadOnlyFileTools) > 6 && ReadOnlyFileTools[6] != "find_references" {
		t.Errorf("last element = %q, want find_references", ReadOnlyFileTools[6])
	} else if len(ReadOnlyFileTools) <= 6 {
		t.Error("ReadOnlyFileTools has fewer than 7 elements; cannot check last")
	}

	// The doc comment on the declaration names find_references.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "policy.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse policy.go: %v", err)
	}
	var doc string
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, s := range gd.Specs {
			vs, ok := s.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, n := range vs.Names {
				if n.Name == "ReadOnlyFileTools" {
					if gd.Doc != nil {
						doc = gd.Doc.Text()
					}
				}
			}
		}
	}
	if !strings.Contains(doc, "find_references") {
		t.Errorf("doc comment does not mention find_references: %q", doc)
	}
}

// TS-17-2 (unit): find_references stays out of MutatingTools and ShellTools,
// code_search stays out of ReadOnlyFileTools, and WithCodeSearch leaves the
// shared slice unmodified.
//
// Verifies: 17-REQ-1.3
func TestTS17_2_FindReferencesNotInMutatingOrShellAndCodeSearchCopy(t *testing.T) {
	if slices.Contains(MutatingTools, "find_references") {
		t.Error("find_references must not be in MutatingTools")
	}
	if slices.Contains(ShellTools, "find_references") {
		t.Error("find_references must not be in ShellTools")
	}
	if slices.Contains(ReadOnlyFileTools, "code_search") {
		t.Error("code_search must not be in ReadOnlyFileTools")
	}

	before := slices.Clone(ReadOnlyFileTools)
	g := WithCodeSearch(ReadOnlyFileTools, true)
	if len(g) != 8 {
		t.Errorf("WithCodeSearch(true) len = %d, want 8", len(g))
	}
	if g[len(g)-1] != "code_search" {
		t.Errorf("WithCodeSearch(true) last = %q, want code_search", g[len(g)-1])
	}
	// Mutating the returned slice must not change ReadOnlyFileTools.
	g[0] = "x"
	if !slices.Equal(ReadOnlyFileTools, before) {
		t.Errorf("ReadOnlyFileTools was mutated: %v", ReadOnlyFileTools)
	}
	if !slices.Equal(WithCodeSearch(ReadOnlyFileTools, false), before) {
		t.Errorf("WithCodeSearch(false) != ReadOnlyFileTools")
	}
}

// TS-16-8 (unit): code_search is in neither ReadOnlyFileTools nor
// MutatingTools.
//
// Verifies: 16-REQ-2.5, 16-REQ-6.1
func TestTS16_8_CodeSearchInNeitherToolList(t *testing.T) {
	if slices.Contains(ReadOnlyFileTools, "code_search") {
		t.Error("code_search must not be in ReadOnlyFileTools")
	}
	if slices.Contains(MutatingTools, "code_search") {
		t.Error("code_search must not be in MutatingTools")
	}
}

// describedExecute is the execute description SelectTools gives a phase with
// the named tools.
func describedExecute(readOnly bool, programs []string, names ...string) string {
	all := []core.Tool{{Name: "execute", Description: "Run a command."}, {Name: "read_file", Description: "Read."},
		{Name: "search_files", Description: "Search."}, {Name: "write_file", Description: "Write."}}
	for _, tl := range SelectTools(all, readOnly, programs, names...) {
		if tl.Name == "execute" {
			return tl.Description
		}
	}
	return ""
}

// TS-17-14 (unit): with read_file or search_files selected, the execute
// description tells the model not to read or search files through the shell,
// naming only the built-in tools it has.
//
// Verifies: 17-REQ-5.1
func TestTS17_14_ExecuteDiscouragesShellReads(t *testing.T) {
	for _, ro := range []bool{true, false} {
		both := describedExecute(ro, ReadOnlyPrograms, "execute", "read_file", "search_files")
		for _, w := range []string{"cat", "head", "tail", "grep", "rg", "`read_file`", "`search_files`", ".gitignore"} {
			if !strings.Contains(both, w) {
				t.Errorf("readOnly=%v: execute lacks %q: %s", ro, w, both)
			}
		}
		one := describedExecute(ro, ReadOnlyPrograms, "execute", "read_file")
		if !strings.Contains(one, "`read_file`") || strings.Contains(one, "`search_files`") {
			t.Errorf("readOnly=%v, read_file only: %s", ro, one)
		}
	}
}

// TS-17-15 (unit): without read_file and search_files, the execute
// description is the phase's rules alone.
//
// Verifies: 17-REQ-5.2
func TestTS17_15_NoDiscouragementWithoutTheFileTools(t *testing.T) {
	for _, ro := range []bool{true, false} {
		got := describedExecute(ro, ReadOnlyPrograms, "execute", "write_file")
		want := describeForPhase(core.Tool{Name: "execute", Description: "Run a command."}, ro, ReadOnlyPrograms)
		if got != want || strings.Contains(got, ".gitignore") {
			t.Errorf("readOnly=%v: execute = %q, want %q", ro, got, want)
		}
	}
}

// TS-17-16 (unit): the discouraging sentence leaves the allowlist the guard
// enforces stated in full.
//
// Verifies: 17-REQ-5.3
func TestTS17_16_TheAllowlistIsStillStated(t *testing.T) {
	progs := []string{"git", "ls", "cat", "grep", "go"}
	for _, ro := range []bool{true, false} {
		if d := describedExecute(ro, progs, "execute", "read_file", "search_files"); !strings.Contains(d,
			"The only programs allowed are: "+strings.Join(progs, ", ")) {
			t.Errorf("readOnly=%v: %s", ro, d)
		}
	}
}
