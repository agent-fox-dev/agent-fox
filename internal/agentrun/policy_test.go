package agentrun

import (
	"slices"
	"testing"
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
