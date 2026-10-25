package triage

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

const testRun = "20261020_072649_a31f9c"

// plainTree is a directory of files. It is not a git repository, so the file
// view is a walk and the tests need no git.
func plainTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		testutil.WriteFile(t, dir, name, content)
	}
	return dir
}

func goTree(t *testing.T) string {
	return plainTree(t, map[string]string{
		"go.mod":       "module example.com/x\n",
		"main.go":      "package main\n\nfunc Run() {}\n",
		"util/util.go": "package util\n\n// Help helps.\nfunc Help() {}\n",
	})
}

func issueRef() issuex.IssueRef {
	return issuex.IssueRef{Repo: issuex.Repo{Owner: "o", Name: "r", Host: "github.com"}, Number: 7}
}

var day = time.Date(2026, 10, 1, 12, 30, 0, 0, time.UTC)

// issueReport is an open issue by alice with two comments, one by alice and one
// by bob.
func issueReport(body string) Report {
	return Report{
		Kind: KindIssue, Origin: issueRef().URL(), Ref: issueRef(),
		Thread: issuex.IssueThread{
			Issue: issuex.Issue{Number: 7, Title: "Login fails", Body: body, State: "open", Author: issuex.User{Login: "alice"}, CreatedAt: day},
			Comments: []issuex.Comment{
				{Body: "It also fails on Safari.", User: issuex.User{Login: "alice"}, CreatedAt: day.Add(24 * time.Hour)},
				{Body: "Works for me.", User: issuex.User{Login: "bob"}, CreatedAt: day.Add(48 * time.Hour)},
			},
		},
	}
}

// fullDiagnosis has every section: a path with a line and one without, and a
// question with options.
func fullDiagnosis() Diagnosis {
	return Diagnosis{
		Title: "Login fails on submit", Severity: "high", Confidence: "suspected",
		RootCause: "The handler dereferences a nil session.\nIt is created lazily.",
		AffectedFiles: []AffectedFile{
			{Path: "auth/login.go", Line: 42, Note: "nil session dereference"},
			{Path: "auth/session.go", Note: "lazy creation"},
		},
		SuggestedFix:       "Create the session before the handler runs.",
		AcceptanceCriteria: []string{"Submitting the login form with valid credentials returns 200", "No panic is logged"},
		Question:           &Question{Text: "Should an expired session log the user out?", Options: []string{"Yes", "No"}},
	}
}

// notReproducible has only what a diagnosis that found nothing must have.
func notReproducible() Diagnosis {
	return Diagnosis{Title: "Crash on submit", Severity: "low", Confidence: "not_reproducible", RootCause: "Could not reproduce with the steps given."}
}

// treeFiles are the files of the test tree and their line counts.
var treeFiles = map[string]int{
	"a/b.go": 20, "c:5": 3, "main.go": 10, "pkg/inner.go": 4,
	"w/util.go": 1, "x/util.go": 1, "y/util.go": 1, "z/util.go": 1,
	"README.md": 5, "empty.txt": 0,
}

const treeRoot = "/work/repo"

func testTree() Tree {
	var files []string
	for name := range treeFiles {
		files = append(files, name)
	}
	slices.Sort(files)
	return Tree{Roots: []string{treeRoot}, Files: files, Lines: func(path string) (int, error) {
		n, ok := treeFiles[path]
		if !ok {
			return 0, fmt.Errorf("%s is not a file", path)
		}
		return n, nil
	}}
}

// valid is a diagnosis of testTree that has no problem, as the map a brain
// would send.
func valid() map[string]any {
	return map[string]any{
		"title": "Login fails on submit", "severity": "high", "confidence": "probable",
		"root_cause": "The handler dereferences a nil session.",
		"affected_files": []any{
			map[string]any{"path": "main.go", "line": 3, "note": "the handler"},
			map[string]any{"path": "a/b.go", "note": "the session"},
			map[string]any{"path": "README.md", "line": 5, "note": "the docs"},
		},
		"suggested_fix":       "Create the session first.",
		"acceptance_criteria": []any{"The login form returns 200", "No panic is logged"},
	}
}

// with is valid() changed by f.
func with(f func(m map[string]any)) map[string]any {
	m := valid()
	f(m)
	return m
}

func payloadOf(t *testing.T, v any) []byte { return testutil.JSON(t, v) }

// citing is the payload of a valid diagnosis that cites one path.
func citing(t *testing.T, path string) []byte {
	return payloadOf(t, with(func(m map[string]any) {
		m["affected_files"] = []any{map[string]any{"path": path, "note": "the function"}}
	}))
}

// suspecting sets a question, which goes with the confidence suspected.
func suspecting(q map[string]any) func(m map[string]any) {
	return func(m map[string]any) { m["confidence"], m["question"] = "suspected", q }
}

func question(text string, options ...any) map[string]any {
	q := map[string]any{"text": text}
	if options != nil {
		q["options"] = options
	}
	return q
}

func pointers(problems []Problem) []string {
	var out []string
	for _, p := range problems {
		out = append(out, p.Pointer)
	}
	return out
}

func repeat(s string, n int) string { return strings.Repeat(s, n) }

// items are n copies of item.
func items(n int, item func(i int) any) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = item(i)
	}
	return out
}

func mainGo(int) any { return map[string]any{"path": "main.go", "note": "n"} }

func criterion(i int) any { return fmt.Sprintf("criterion %d", i) }

// noToken clears the environment a client takes its token from.
func noToken(t *testing.T) {
	t.Helper()
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "GITHUB_API_URL", "GITLAB_API_URL"} {
		t.Setenv(name, "")
	}
}

// forgeStub is a forge that answers the repository, and counts what it was
// asked.
type forgeStub struct {
	issuex.Client
	authenticated bool
	limit         int
	repo          issuex.Repository
	err           error
	asked         atomic.Int32
}

func newForgeStub() *forgeStub {
	return &forgeStub{Client: issuex.NewNoOp(), authenticated: true, limit: issuex.GitHubMaxBodyLength, repo: issuex.Repository{FullName: "o/r"}}
}

func (f *forgeStub) Authenticated() bool { return f.authenticated }
func (f *forgeStub) MaxBodyLength() int  { return f.limit }
func (f *forgeStub) GetRepository(context.Context, issuex.Repo) (issuex.Repository, error) {
	f.asked.Add(1)
	return f.repo, f.err
}
