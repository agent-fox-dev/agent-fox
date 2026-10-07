package agentrun

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
)

// TS-15-1 / TS-18-1 (unit): ReadOnlyFileTools contains exactly the seven
// expected names in the correct order.
//
// Verifies: 15-REQ-1.1, 15-REQ-1.4, 18-REQ-1.1
func TestTS15_1_ReadOnlyFileToolsHasTheSevenReadTools(t *testing.T) {
	want := []string{"read_file", "list_files", "find_files", "search_files", "file_outline", "find_symbol", "find_references"}
	if !slices.Equal(ReadOnlyFileTools, want) {
		t.Errorf("ReadOnlyFileTools = %v, want %v", ReadOnlyFileTools, want)
	}
	if len(ReadOnlyFileTools) != 7 {
		t.Errorf("len = %d, want 7", len(ReadOnlyFileTools))
	}
	for _, n := range []string{"file_outline", "find_symbol", "find_references"} {
		if slices.Contains(MutatingTools, n) {
			t.Errorf("%s must not be a mutating tool", n)
		}
	}
}

// TS-18-2 (unit): find_references is in neither MutatingTools nor ShellTools,
// so AssertReadOnly passes for a set containing all seven read tools.
//
// Verifies: 18-REQ-1.2, 18-REQ-1.4
func TestTS18_2_FindReferencesNotInMutatingOrShellTools(t *testing.T) {
	if slices.Contains(MutatingTools, "find_references") {
		t.Error("find_references must not be in MutatingTools")
	}
	if slices.Contains(ShellTools, "find_references") {
		t.Error("find_references must not be in ShellTools")
	}
	resolved := []core.Tool{
		{Name: "read_file"}, {Name: "list_files"}, {Name: "find_files"},
		{Name: "search_files"}, {Name: "file_outline"}, {Name: "find_symbol"},
		{Name: "find_references"},
	}
	if err := AssertReadOnly(resolved); err != nil {
		t.Errorf("AssertReadOnly refused the seven read tools: %v", err)
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

// TS-18-15 (unit): WithCodeSearch with on=true returns a new slice of
// len(ReadOnlyFileTools)+1 ending with code_search, without mutating the
// original.
//
// Verifies: 18-REQ-9.1
func TestTS18_15_WithCodeSearchOnTrueReturnsNewSlice(t *testing.T) {
	before := len(ReadOnlyFileTools)
	got := WithCodeSearch(ReadOnlyFileTools, true)
	if len(ReadOnlyFileTools) != before {
		t.Errorf("ReadOnlyFileTools was mutated: len %d, want %d", len(ReadOnlyFileTools), before)
	}
	if len(got) != before+1 {
		t.Errorf("len(result) = %d, want %d", len(got), before+1)
	}
	if got[len(got)-1] != "code_search" {
		t.Errorf("last element = %q, want code_search", got[len(got)-1])
	}
	got[0] = "mutated"
	if ReadOnlyFileTools[0] == "mutated" {
		t.Error("mutating the result affected ReadOnlyFileTools")
	}
}

// TS-18-16 (unit): WithCodeSearch with on=false returns the input grant
// unchanged.
//
// Verifies: 18-REQ-9.2
func TestTS18_16_WithCodeSearchOnFalseReturnsUnchanged(t *testing.T) {
	same := WithCodeSearch(ReadOnlyFileTools, false)
	if len(same) != len(ReadOnlyFileTools) {
		t.Errorf("len = %d, want %d", len(same), len(ReadOnlyFileTools))
	}
	if slices.Contains(same, "code_search") {
		t.Error("result contains code_search when on=false")
	}
}

// TS-18-21 (property): WithCodeSearch never mutates its input slice for any
// grant length.
//
// Verifies: 18-REQ-9.1
func TestTS18_21_WithCodeSearchNeverMutatesInput(t *testing.T) {
	for n := 0; n <= 20; n++ {
		grant := make([]string, n)
		for i := range grant {
			grant[i] = fmt.Sprintf("tool_%d", i)
		}
		orig := slices.Clone(grant)
		result := WithCodeSearch(grant, true)
		if !slices.Equal(grant, orig) {
			t.Errorf("n=%d: grant was mutated", n)
		}
		if len(result) != n+1 {
			t.Errorf("n=%d: len(result) = %d, want %d", n, len(result), n+1)
		}
		if result[len(result)-1] != "code_search" {
			t.Errorf("n=%d: last element = %q, want code_search", n, result[len(result)-1])
		}
	}
}
