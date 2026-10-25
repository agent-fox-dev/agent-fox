package toolio

import (
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"
)

func TestRenderThread_NamesTheForgeOfTheHostAndListsTheComments(t *testing.T) {
	thread := func(labels []string, comments []issuex.Comment, truncated bool) issuex.IssueThread {
		return issuex.IssueThread{
			Issue:     issuex.Issue{Title: "T", Body: "\n  body text \n\n", State: "open", Author: issuex.User{Login: "a"}, Labels: labels},
			Comments:  comments,
			Truncated: truncated,
		}
	}
	ref := func(host string, pr bool) issuex.IssueRef {
		return issuex.IssueRef{Repo: issuex.Repo{Owner: "o", Name: "r", Host: host}, Number: 7, IsPullRequest: pr}
	}
	bob := issuex.Comment{Body: "hello", User: issuex.User{Login: "bob"}}
	carol := issuex.Comment{Body: "agreed", User: issuex.User{Login: "carol"}}
	const head = "Title: T\nAuthor: a\n"
	for _, tc := range []struct {
		name string
		ref  issuex.IssueRef
		t    issuex.IssueThread
		want string
	}{
		{"github with labels", ref("github.com", false), thread([]string{"bug", "ui"}, nil, false),
			"GitHub issue o/r#7 (state: open)\n" + head + "Labels: bug, ui\n\nbody text\n"},
		{"a gitlab merge request", ref("gitlab.example", true), thread(nil, nil, false),
			"GitLab pull request o/r#7 (state: open)\n" + head + "\nbody text\n"},
		{"another host", ref("git.example.com", false), thread(nil, nil, false),
			"Forge issue o/r#7 (state: open)\n" + head + "\nbody text\n"},
		{"no host", ref("", false), thread(nil, nil, false),
			"GitHub issue o/r#7 (state: open)\n" + head + "\nbody text\n"},
		{"comments", ref("github.com", false), thread(nil, []issuex.Comment{bob, carol}, false),
			"GitHub issue o/r#7 (state: open)\n" + head + "\nbody text\n\n--- comment by bob ---\nhello\n\n--- comment by carol ---\nagreed\n"},
		{"a thread cut short", ref("github.com", false), thread(nil, []issuex.Comment{bob}, true),
			"GitHub issue o/r#7 (state: open)\n" + head + "\nbody text\n\n--- comment by bob ---\nhello\n\n[... further comments not read ...]\n"},
	} {
		if got := RenderThread(tc.ref, tc.t); got != tc.want {
			t.Errorf("%s:\n%q\nwant\n%q", tc.name, got, tc.want)
		}
	}
}
