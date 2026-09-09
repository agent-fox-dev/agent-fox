package gitx

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// stopWords are dropped from a branch slug.
var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "for": true, "with": true, "of": true,
	"to": true, "in": true, "is": true, "fix": true, "add": true, "bug": true,
	"feature": true,
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slug turns a title into the tail of a branch name: lowercase,
// non-alphanumerics collapsed to hyphens, stop words dropped, the first five
// surviving words kept, truncated to 40 characters at a hyphen boundary.
//
// It never returns the empty string. A title made entirely of stop words
// ("Fix the bug") would otherwise produce "fix/issue-42-", which git accepts
// and no person can read.
func Slug(title string) string {
	words := strings.Split(nonSlug.ReplaceAllString(strings.ToLower(title), "-"), "-")
	kept := make([]string, 0, 5)
	for _, w := range words {
		if w == "" || stopWords[w] {
			continue
		}
		kept = append(kept, w)
		if len(kept) == 5 {
			break
		}
	}
	if len(kept) == 0 {
		return "issue"
	}
	slug := strings.Join(kept, "-")
	if len(slug) > 40 {
		slug = slug[:40]
		if i := strings.LastIndex(slug, "-"); i > 0 {
			slug = slug[:i]
		}
	}
	return strings.Trim(slug, "-")
}

// BranchName composes prefix/issue-N-slug, or prefix/slug when there is no
// issue number — which is the case when the input was text or a file rather
// than a GitHub URL.
func BranchName(prefix string, number int, title string) string {
	if number > 0 {
		return fmt.Sprintf("%s/issue-%d-%s", prefix, number, Slug(title))
	}
	return fmt.Sprintf("%s/%s", prefix, Slug(title))
}

// UniqueBranchName appends a numeric suffix until the name is free both
// locally and on origin.
//
// The skill this replaces halts here and asks whether to force-push over the
// existing branch — inside a workflow whose first paragraph promises not to
// stop for confirmation. A second attempt at the same issue is normal (the
// first run's fix was rejected in review, say), so the resolution that keeps
// the promise is a fresh name. Force-pushing over someone else's branch is
// never something to do on a guess.
func UniqueBranchName(ctx context.Context, g *Git, name string) string {
	candidate := name
	for i := 2; i < 50; i++ {
		if !g.LocalBranchExists(ctx, candidate) && !g.RemoteBranchExists(ctx, candidate) {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", name, i)
	}
	return fmt.Sprintf("%s-%d", name, time.Now().Unix())
}
