package worker

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

// The kinds of input of the loop.
const (
	inputIssue = "issue"
	inputText  = "text"
	inputFile  = "file"
)

// loopInput is what the loop is asked about: an issue of the workspace's repository, as the URL
// of it; text, as what goes to the loop on stdin; or a file.
type loopInput struct {
	kind string
	// ref and url are the issue, for an issue.
	ref issuex.IssueRef
	url string
	// text is the standard input of the loop for text, with the title as a Markdown heading.
	text string
	// file is the file to write under input/, for a file.
	file *jobs.File
}

var (
	// positiveNumber is a JSON number that is a positive integer.
	positiveNumber = regexp.MustCompile(`^[1-9][0-9]*$`)
	// pathAndNumber is <path>#<N>, with a path of one or more segments: any path that is not the
	// workspace's is refused as not being on its repository.
	pathAndNumber = regexp.MustCompile(`^([A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*)#([0-9]+)$`)
)

// notOnRepository is the refusal of an issue that is not on the workspace's repository.
func notOnRepository(given string, repo issuex.Repo) *refusal {
	return usageRefusal("the issue %s is not on the workspace's repository, %s", given, repoName(repo))
}

// repoName is a repository as the refusals say it.
func repoName(r issuex.Repo) string {
	if r.Host == "" {
		return r.String()
	}
	return r.Host + "/" + r.String()
}

// parseInput turns the input of the job into the input of the loop. An issue becomes its URL on
// the repository of the workspace: a number is one of that repository, a path and a number are
// checked against it without case (on GitLab the whole namespace), and a URL must be on it, host
// included. Nothing else is accepted. Text and a file are taken as they are.
func parseInput(in jobs.Input, repo issuex.Repo) (loopInput, *refusal) {
	switch {
	case in.File != nil:
		return loopInput{kind: inputFile, file: in.File}, nil
	case len(in.Issue) == 0:
		text := in.Text
		if in.Title != "" {
			text = "# " + in.Title + "\n\n" + in.Text
		}
		return loopInput{kind: inputText, text: text}, nil
	}

	n, refused := issueNumber(in.Issue, repo)
	if refused != nil {
		return loopInput{}, refused
	}
	ref := issuex.IssueRef{Repo: repo, Number: n}
	return loopInput{kind: inputIssue, ref: ref, url: ref.URL()}, nil
}

// issueNumber is the number of the issue as the job gave it, once it is known to be on the
// repository.
func issueNumber(raw json.RawMessage, repo issuex.Repo) (int, *refusal) {
	if positiveNumber.Match(raw) {
		n, err := strconv.Atoi(string(raw))
		if err != nil {
			return 0, usageRefusal("the issue number %s is too large", raw)
		}
		return n, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, usageRefusal("the issue %s is none of an issue number, an issue URL and owner/repo#N", raw)
	}
	if ref, ok := issuex.ParseIssueURL(s); ok {
		if !sameRepository(ref.Repo, repo) {
			return 0, notOnRepository(s, repo)
		}
		return ref.Number, nil
	}
	if m := pathAndNumber.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[2])
		if err != nil || n < 1 {
			return 0, usageRefusal("the issue %q has no usable number", s)
		}
		if !strings.EqualFold(m[1], repo.Owner+"/"+repo.Name) {
			return 0, notOnRepository(s, repo)
		}
		return n, nil
	}
	return 0, usageRefusal("the issue %q is none of an issue number, an issue URL and owner/repo#N", s)
}

// sameRepository compares host, owner and name without case.
func sameRepository(a, b issuex.Repo) bool {
	return strings.EqualFold(a.Host, b.Host) && strings.EqualFold(a.Owner, b.Owner) && strings.EqualFold(a.Name, b.Name)
}
