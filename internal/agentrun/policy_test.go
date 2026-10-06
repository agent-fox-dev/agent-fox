package agentrun

import (
	"slices"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
)

// TS-15-1 (unit): ReadOnlyFileTools contains exactly the six expected names.
//
// Verifies: 15-REQ-1.1, 15-REQ-1.4
func TestTS15_1_ReadOnlyFileToolsHasTheSixReadTools(t *testing.T) {
	want := []string{"read_file", "list_files", "find_files", "search_files", "file_outline", "find_symbol"}
	got := slices.Clone(ReadOnlyFileTools)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("ReadOnlyFileTools = %v, want the six %v", ReadOnlyFileTools, want)
	}
	if len(ReadOnlyFileTools) != 6 {
		t.Errorf("len = %d, want 6", len(ReadOnlyFileTools))
	}
	for _, n := range []string{"file_outline", "find_symbol"} {
		if slices.Contains(MutatingTools, n) {
			t.Errorf("%s must not be a mutating tool", n)
		}
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
