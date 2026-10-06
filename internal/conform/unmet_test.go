package conform

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A table cell longer than the cap is cut at the cap and marked, and one
// within it is left whole: the full text is in the JSON report.
func TestCellCapsLongText(t *testing.T) {
	long := strings.Repeat("evidence ", 200)
	got := cell(long)
	if utf8.RuneCountInString(got) > MaxCellLength || !strings.HasSuffix(got, "…") {
		t.Errorf("cell(long) = %d runes, ends %q", utf8.RuneCountInString(got), got[len(got)-8:])
	}
	if got := cell("short | text"); got != `short \| text` {
		t.Errorf("cell(short) = %q", got)
	}
	// A cut never leaves a dangling escape or a split rune.
	edge := strings.Repeat("é", MaxCellLength-2) + "|||"
	if got := cell(edge); !utf8.ValidString(got) || strings.HasSuffix(strings.TrimSuffix(got, "…"), `\`) {
		t.Errorf("cell(edge) = %q", got)
	}
	review := Review{Requirements: []RequirementRow{{ID: "1-REQ-1", Status: StatusImplemented, Evidence: long}}}
	if out := RenderReview(review, nil); strings.Contains(out, long) {
		t.Error("RenderReview kept the evidence whole")
	}
}
