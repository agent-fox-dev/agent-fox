package gitx

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestSlug(t *testing.T) {
	for _, tc := range []struct{ title, want string }{
		{"session: token refresh skips expiry check", "session-token-refresh-skips-expiry"},
		{"Add support for UTF-8 in the parser!!", "support-utf-8-parser"},
		{"one two three four five six seven", "one-two-three-four-five"},
		{strings.Repeat("verylongword-", 10), "verylongword-verylongword-verylongword"}, // cut to 40 at a hyphen
		{strings.Repeat("x", 50), strings.Repeat("x", 40)},
		// Never the empty string, which would end the branch name in a hyphen.
		{"Fix the bug", "issue"},
		{"", "issue"},
		{"!!!", "issue"},
	} {
		if got := Slug(tc.title); got != tc.want {
			t.Errorf("Slug(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}
}

func TestBranchName(t *testing.T) {
	for _, tc := range []struct {
		prefix string
		number int
		title  string
		want   string
	}{
		{"fix", 42, "counter double counts", "fix/issue-42-counter-double-counts"},
		{"feature", 0, "add a widget cache", "feature/widget-cache"}, // text or a file: no issue number
	} {
		if got := BranchName(tc.prefix, tc.number, tc.title); got != tc.want {
			t.Errorf("BranchName(%q, %d, %q) = %q, want %q", tc.prefix, tc.number, tc.title, got, tc.want)
		}
	}
}

// A second attempt at an issue is normal, so a name that is taken here or on
// origin gets a numeric suffix rather than a force-push.
func TestUniqueBranchNameAvoidsTheNamesInUse(t *testing.T) {
	for _, tc := range []struct {
		name          string
		local, remote []string
		all           bool
		want          string
	}{
		{"a free name", nil, nil, false, "fix/x"},
		{"taken here", []string{"fix/x"}, nil, false, "fix/x-2"},
		{"taken on origin", nil, []string{"fix/x"}, false, "fix/x-2"},
		{"taken in both places", []string{"fix/x", "fix/x-3"}, []string{"fix/x-2"}, false, "fix/x-4"},
		{"every suffix taken", nil, nil, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{answer: func(argv []string) (string, int, error) {
				name := argv[len(argv)-1]
				switch {
				case argv[1] == "rev-parse" && (tc.all || slices.Contains(tc.local, strings.TrimPrefix(name, "refs/heads/"))):
					return "", 0, nil
				case argv[1] == "ls-remote" && slices.Contains(tc.remote, name):
					return "abc\trefs/heads/" + name, 0, nil
				}
				return "", 1, nil
			}}

			got := UniqueBranchName(t.Context(), New("/repo", f.run), "fix/x")

			if tc.all {
				// The last resort is a suffix no attempt has used: the time.
				if n, err := strconv.ParseInt(strings.TrimPrefix(got, "fix/x-"), 10, 64); err != nil || n < 1e9 {
					t.Errorf("UniqueBranchName = %q, want fix/x- and a Unix time", got)
				}
				return
			}
			if got != tc.want {
				t.Errorf("UniqueBranchName = %q, want %q", got, tc.want)
			}
		})
	}
}
