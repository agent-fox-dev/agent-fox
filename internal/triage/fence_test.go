package triage

import (
	"math/rand"
	"strings"
	"testing"
)

// A fence is one backtick longer than the longest run in its content, at least
// three long, and no line of the content closes it.
func TestFence_IsLongerThanAnyRunOfBackticksInItsContent(t *testing.T) {
	rng := rand.New(rand.NewSource(18))
	pieces := []string{"", "a", "\n", "`", "``", "```", "````", "`````", "\n```\n", "\n````", "```go\n", " ", "text", "\r\n"}
	cases := []string{"", "plain", "```", "````", "a\n```\nb", "`````````"}
	for range 3000 {
		var b strings.Builder
		for range rng.Intn(10) {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		cases = append(cases, b.String())
	}
	for _, s := range cases {
		open, closing := Fence(s)

		longest := 0
		for _, run := range strings.FieldsFunc(s, func(r rune) bool { return r != '`' }) {
			longest = max(longest, len(run))
		}
		if open != closing || len(open) != max(3, longest+1) || strings.Trim(open, "`") != "" {
			t.Fatalf("Fence(%q) = %q, %q, want two lines of %d backticks", s, open, closing, max(3, longest+1))
		}
		for _, line := range strings.Split(s, "\n") {
			if line == closing {
				t.Fatalf("a line of %q closes its fence", s)
			}
		}
	}
}
