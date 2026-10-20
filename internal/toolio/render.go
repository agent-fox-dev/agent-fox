package toolio

import (
	"fmt"
	"strings"

	"github.com/agent-fox-dev/aflib/issuex"
)

// RenderThread renders an issue or pull request thread as the text a brief
// shows a model:
//
//	<GitHub|GitLab|Forge> <issue|pull request> <ref> (state: <state>)
//	Title: …
//	Author: …
//	Labels: …            (left out when there are none)
//
//	<the body, trimmed>
//
//	--- comment by <login> ---
//	<the comment>
//
//	[... further comments not read ...]   (when the thread was cut short)
//
// The forge in the first line comes from the host of ref: a host with
// "gitlab" in it is GitLab, one with "github" in it, or none, GitHub, and any
// other is a Forge.
func RenderThread(ref issuex.IssueRef, t issuex.IssueThread) string {
	kind := "issue"
	if ref.IsPullRequest {
		kind = "pull request"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s (state: %s)\n", forgeName(ref.Repo.Host), kind, ref, t.Issue.State)
	fmt.Fprintf(&b, "Title: %s\nAuthor: %s\n", t.Issue.Title, t.Issue.Author.Login)
	if len(t.Issue.Labels) > 0 {
		fmt.Fprintf(&b, "Labels: %s\n", strings.Join(t.Issue.Labels, ", "))
	}
	fmt.Fprintf(&b, "\n%s\n", strings.TrimSpace(t.Issue.Body))
	for _, c := range t.Comments {
		fmt.Fprintf(&b, "\n--- comment by %s ---\n%s\n", c.User.Login, c.Body)
	}
	if t.Truncated {
		b.WriteString("\n[... further comments not read ...]\n")
	}
	return b.String()
}

// forgeName is how the first line of a rendered thread names the forge.
func forgeName(host string) string {
	h := strings.ToLower(host)
	switch {
	case strings.Contains(h, "gitlab"):
		return "GitLab"
	case h == "" || strings.Contains(h, "github"):
		return "GitHub"
	}
	return "Forge"
}
