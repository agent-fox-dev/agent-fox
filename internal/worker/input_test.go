package worker

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

func TestParseInput_AnIssueBecomesItsURLOnTheWorkspacesRepository(t *testing.T) {
	const notOn = "not on the workspace's repository"
	for remote, cases := range map[string][]struct{ raw, url, refusal string }{
		forgeRepo: {
			{`42`, issueURL, ""},
			{`"org/repo#42"`, issueURL, ""},
			{`"Org/Repo#42"`, issueURL, ""},
			{`"https://github.example.com/Org/Repo/issues/42"`, issueURL, ""},
			{`"https://github.example.com/org/REPO/issues/42"`, issueURL, ""},
			{`"other/repo#42"`, "", notOn},
			{`"Other/Repo#42"`, "", notOn},
			{`"https://github.example.com/Other/Repo/issues/42"`, "", notOn},
			{`"https://github.example.com/Org/Other/issues/42"`, "", notOn},
			{`"https://github.com/Org/Repo/issues/42"`, "", notOn},
			{`99999999999999999999`, "", "too large"},
			{`"org/repo#0"`, "", "no usable number"},
			{`"org/repo#99999999999999999999"`, "", "no usable number"},
			{`"the login fails"`, "", "none of an issue number"},
			{`true`, "", "none of an issue number"},
			{`-3`, "", "none of an issue number"},
		},
		"https://gitlab.example.com/group/sub/project": {
			{`7`, "https://gitlab.example.com/group/sub/project/-/issues/7", ""},
			{`"Group/Sub/Project#7"`, "https://gitlab.example.com/group/sub/project/-/issues/7", ""},
			{`"https://gitlab.example.com/group/sub/project/-/issues/7"`, "https://gitlab.example.com/group/sub/project/-/issues/7", ""},
			{`"project#7"`, "", notOn},
			{`"sub/project#7"`, "", notOn},
			{`"other/sub/project#7"`, "", notOn},
		},
	} {
		repo, ok := issuex.ParseRemote(remote)
		if !ok {
			t.Fatalf("%s is not a repository", remote)
		}
		for _, c := range cases {
			in, refused := parseInput(jobs.Input{Issue: json.RawMessage(c.raw)}, repo)
			switch {
			case c.url != "" && (refused != nil || in.kind != inputIssue || in.url != c.url || in.ref.URL() != c.url):
				t.Errorf("%s %s: %+v, %+v, want the issue %s", remote, c.raw, in, refused, c.url)
			case c.url == "" && (refused == nil || refused.category != "usage" || !strings.Contains(refused.message, c.refusal)):
				t.Errorf("%s %s: %+v, %+v, want a usage refusal that says %q", remote, c.raw, in, refused, c.refusal)
			}
		}
	}

	t.Run("a refusal names a repository without a host by its path", func(t *testing.T) {
		_, refused := parseInput(issueInput(`"other/repo#1"`), issuex.Repo{Owner: "o", Name: "r"})
		if refused == nil || !strings.HasSuffix(refused.message, "repository, o/r") {
			t.Errorf("refusal = %+v", refused)
		}
	})
}

func TestParseInput_TextAndAFileAreTakenAsTheyAre(t *testing.T) {
	file := &jobs.File{Name: "report.txt", Content: "body"}
	for name, tc := range map[string]struct {
		in   jobs.Input
		want loopInput
	}{
		"text with a title gets it as a heading": {jobs.Input{Title: "Login", Text: "the login fails"}, loopInput{kind: inputText, text: "# Login\n\nthe login fails"}},
		"text with no title":                     {jobs.Input{Text: "just text"}, loopInput{kind: inputText, text: "just text"}},
		"a file":                                 {jobs.Input{File: file}, loopInput{kind: inputFile, file: file}},
	} {
		got, refused := parseInput(tc.in, issuex.Repo{Owner: "Org", Name: "Repo"})
		if refused != nil || got != tc.want {
			t.Errorf("%s: %+v, %+v, want %+v", name, got, refused, tc.want)
		}
	}
}
