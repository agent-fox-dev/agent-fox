package toolio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/issuex"
)

type mockForgeClient struct {
	issuex.NoOpClient
	authenticated bool
	readErr       error
	thread        issuex.IssueThread
	readIssueFn   func(ctx context.Context, ref issuex.IssueRef) (issuex.IssueThread, error)
}

func (m *mockForgeClient) Authenticated() bool {
	return m.authenticated
}

func (m *mockForgeClient) ReadIssue(ctx context.Context, ref issuex.IssueRef) (issuex.IssueThread, error) {
	if m.readIssueFn != nil {
		return m.readIssueFn(ctx, ref)
	}
	if m.readErr != nil {
		return issuex.IssueThread{}, m.readErr
	}
	return m.thread, nil
}

func TestResolveClassifiesText(t *testing.T) {
	in, err := Resolve(context.Background(), "the widget counter double-counts on retry", nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if in.Kind != KindText || in.Origin != "argument" {
		t.Errorf("got %+v, want text/argument", in)
	}
	if in.Body != "the widget counter double-counts on retry" {
		t.Errorf("Body = %q", in.Body)
	}
}

func TestResolveReadsAFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crash.log")
	if err := os.WriteFile(path, []byte("panic: nil map\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := Resolve(context.Background(), path, nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if in.Kind != KindFile {
		t.Errorf("Kind = %s, want file", in.Kind)
	}
	if in.Body != "panic: nil map\n" {
		t.Errorf("Body = %q", in.Body)
	}
}

// A path that exists but is a directory is not an error: "src/" is a
// plausible thing to name in a bug report, so it falls through to text.
func TestResolveTreatsADirectoryAsText(t *testing.T) {
	in, err := Resolve(context.Background(), t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if in.Kind != KindText {
		t.Errorf("Kind = %s, want text", in.Kind)
	}
}

// A path that does NOT exist is text too. Refusing it as a missing file would
// reject "the widget/ package panics".
func TestResolveTreatsAMissingPathAsText(t *testing.T) {
	in, err := Resolve(context.Background(), "internal/nope/missing.go panics on empty input", nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if in.Kind != KindText {
		t.Errorf("Kind = %s, want text", in.Kind)
	}
}

func TestResolveReadsStdin(t *testing.T) {
	in, err := Resolve(context.Background(), "-", strings.NewReader("piped report\n"), nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if in.Kind != KindStdin || in.Origin != "stdin" {
		t.Errorf("got %+v", in)
	}
	if in.Body != "piped report\n" {
		t.Errorf("Body = %q", in.Body)
	}
}

func TestResolveRejectsEmptyInput(t *testing.T) {
	for _, arg := range []string{"", "   "} {
		if _, err := Resolve(context.Background(), arg, nil, nil); !errors.Is(err, ErrNoInput) {
			t.Errorf("Resolve(%q) = %v, want ErrNoInput", arg, err)
		}
	}
	if _, err := Resolve(context.Background(), "-", strings.NewReader("  \n"), nil); !errors.Is(err, ErrNoInput) {
		t.Errorf("empty stdin: want ErrNoInput, got %v", err)
	}
}

func TestResolveFetchesAnIssue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/comments") {
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"body": "here is the traceback", "user": map[string]any{"login": "maintainer"}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 42, "title": "it crashes", "body": "on save", "state": "open",
			"user": map[string]any{"login": "reporter"},
		})
	}))
	defer srv.Close()

	client, err := issuex.NewWithOptions(issuex.Options{
		BaseURL:   srv.URL,
		Repo:      issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"},
		Token:     "t",
		UserAgent: "test",
	})
	if err != nil {
		t.Fatalf("NewWithOptions: %v", err)
	}

	in, err := Resolve(context.Background(), "https://github.com/acme/widgets/issues/42", nil, client)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if in.Kind != KindIssue {
		t.Fatalf("Kind = %s, want issue", in.Kind)
	}
	if in.Issue == nil || in.Issue.Number != 42 {
		t.Fatalf("Issue = %+v", in.Issue)
	}
	for _, want := range []string{"it crashes", "on save", "here is the traceback", "maintainer"} {
		if !strings.Contains(in.Body, want) {
			t.Errorf("Body is missing %q:\n%s", want, in.Body)
		}
	}
}

// A truncated report must say so: one that ends mid-stack-trace with no
// marker reads to a model as a complete stack trace that had no more frames.
func TestTruncateMarksTheCut(t *testing.T) {
	long := strings.Repeat("a line of log output\n", MaxInputBytes/10)
	got, cut := Truncate(long)
	if !cut {
		t.Fatal("want truncated")
	}
	if len(got) > MaxInputBytes+200 {
		t.Errorf("len = %d, want about %d", len(got), MaxInputBytes)
	}
	if !strings.Contains(got, "truncated at") {
		t.Error("the cut must be visible in the text")
	}
	short := "one line\n"
	if got, cut := Truncate(short); cut || got != short {
		t.Errorf("short input was altered: %q, %v", got, cut)
	}
}

func TestRenderThreadIsDeterministic(t *testing.T) {
	ref := issuex.IssueRef{Repo: issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"}, Number: 7}
	thread := issuex.IssueThread{
		Issue: issuex.Issue{
			Title:  "t",
			Body:   "b",
			State:  "open",
			Author: issuex.User{Login: "u"},
			Labels: []string{"bug"},
		},
		Comments: []issuex.Comment{{Body: "c", User: issuex.User{Login: "v"}}},
	}
	first := RenderThread(ref, thread)
	if second := RenderThread(ref, thread); first != second {
		t.Error("RenderThread is not deterministic")
	}
	if !strings.Contains(first, "Labels: bug") {
		t.Errorf("labels missing:\n%s", first)
	}
}

// TS-04-1 (unit): toolio Input struct and KindIssue source kind adopt issuex types and value
// Verifies: 04-REQ-1.1
func TestTS0401_KindIssueAndInputFields(t *testing.T) {
	if got := string(KindIssue); got != "issue" {
		t.Errorf("string(KindIssue) = %q, want %q", got, "issue")
	}
	var in Input
	var _ *issuex.IssueRef = in.Issue
	var _ *issuex.IssueThread = in.Thread
}

// TS-04-2 (integration): Resolve parses GitHub and GitLab issue and pull/merge request URLs via issuex
// Verifies: 04-REQ-1.2, 04-REQ-10.1
func TestTS0402_ResolveParsesGitHubAndGitLabURLs(t *testing.T) {
	ctx := context.Background()
	client := &mockForgeClient{
		authenticated: true,
		readIssueFn: func(ctx context.Context, ref issuex.IssueRef) (issuex.IssueThread, error) {
			return issuex.IssueThread{
				Issue: issuex.Issue{
					Number: ref.Number,
					Title:  fmt.Sprintf("Issue %d", ref.Number),
					Body:   "Body content",
					State:  "open",
					Author: issuex.User{Login: "author1"},
					IsPR:   ref.IsPullRequest,
				},
				Comments: []issuex.Comment{
					{Body: "A comment", User: issuex.User{Login: "reviewer1"}},
				},
			}, nil
		},
	}

	// GitHub issue URL
	inGH, errGH := Resolve(ctx, "https://github.com/org/repo/issues/42", nil, client)
	if errGH != nil {
		t.Fatalf("Resolve GitHub issue: %v", errGH)
	}
	if inGH.Kind != KindIssue {
		t.Errorf("inGH.Kind = %s, want %s", inGH.Kind, KindIssue)
	}
	if inGH.Issue == nil {
		t.Fatal("inGH.Issue is nil")
	}
	if inGH.Issue.Repo.Owner != "org" || inGH.Issue.Repo.Name != "repo" || inGH.Issue.Number != 42 {
		t.Errorf("inGH.Issue = %+v, want org/repo#42", inGH.Issue)
	}
	if inGH.Thread == nil || inGH.Thread.Issue.Number != 42 {
		t.Errorf("inGH.Thread = %+v", inGH.Thread)
	}

	// GitLab merge request URL
	inGL, errGL := Resolve(ctx, "https://gitlab.com/group/sub/project/-/merge_requests/99", nil, client)
	if errGL != nil {
		t.Fatalf("Resolve GitLab MR: %v", errGL)
	}
	if inGL.Kind != KindIssue {
		t.Errorf("inGL.Kind = %s, want %s", inGL.Kind, KindIssue)
	}
	if inGL.Issue == nil {
		t.Fatal("inGL.Issue is nil")
	}
	if inGL.Issue.Repo.Owner != "group/sub" || inGL.Issue.Repo.Name != "project" || inGL.Issue.Number != 99 || !inGL.Issue.IsPullRequest {
		t.Errorf("inGL.Issue = %+v, want group/sub/project#99 IsPullRequest=true", inGL.Issue)
	}
	if inGL.Thread == nil || inGL.Thread.Issue.Number != 99 {
		t.Errorf("inGL.Thread = %+v", inGL.Thread)
	}
}

// TS-04-3 (unit): Resolve rejects issue URLs when no forge client is configured
// Verifies: 04-REQ-1.3
func TestTS0403_ResolveRejectsNilForgeClient(t *testing.T) {
	_, err := Resolve(context.Background(), "https://github.com/owner/repo/issues/10", nil, nil)
	if err == nil {
		t.Fatal("Resolve with nil forge client should return an error")
	}
	if !strings.Contains(err.Error(), "cannot read issue: no forge client configured") {
		t.Errorf("err = %q, want containing 'cannot read issue: no forge client configured'", err.Error())
	}
}

// TS-04-4 (unit): Resolve annotates unauthenticated 404 errors with private repository credential notice
// Verifies: 04-REQ-1.4
func TestTS0404_ResolveAnnotatesUnauthenticated404(t *testing.T) {
	client := &mockForgeClient{
		authenticated: false,
		readErr:       issuex.ErrNotFound,
	}
	_, err := Resolve(context.Background(), "https://github.com/private/repo/issues/1", nil, client)
	if err == nil {
		t.Fatal("Resolve should fail on 404")
	}
	if !errors.Is(err, issuex.ErrNotFound) {
		t.Errorf("err = %v, want wrapping issuex.ErrNotFound", err)
	}
	errStr := strings.ToLower(err.Error())
	if !strings.Contains(errStr, "credentials") && !strings.Contains(errStr, "private") {
		t.Errorf("err = %q, want mention of credentials or private", err.Error())
	}
}

// TS-04-5 (unit): Resolve updates pull request flag, truncates long threads, and populates Input fields
// Verifies: 04-REQ-1.5
func TestTS0405_ResolvePullRequestAndTruncation(t *testing.T) {
	makeLargeComments := func() []issuex.Comment {
		var comments []issuex.Comment
		// MaxInputBytes is 256KB, make comments exceeding that
		body := strings.Repeat("a line of lengthy traceback information\n", 8000)
		comments = append(comments, issuex.Comment{
			Body: body,
			User: issuex.User{Login: "bot"},
		})
		return comments
	}

	client := &mockForgeClient{
		authenticated: true,
		thread: issuex.IssueThread{
			Issue: issuex.Issue{
				Number: 5,
				Title:  "Big PR",
				State:  "open",
				Author: issuex.User{Login: "pr-author"},
				IsPR:   true,
			},
			Comments: makeLargeComments(),
		},
	}

	in, err := Resolve(context.Background(), "https://github.com/org/repo/issues/5", nil, client)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if in.Kind != KindIssue {
		t.Errorf("in.Kind = %s, want %s", in.Kind, KindIssue)
	}
	if in.Issue == nil || !in.Issue.IsPullRequest {
		t.Errorf("in.Issue = %+v, want IsPullRequest=true", in.Issue)
	}
	if in.Origin != in.Issue.URL() {
		t.Errorf("in.Origin = %q, want %q", in.Origin, in.Issue.URL())
	}
	if !in.Truncated {
		t.Error("in.Truncated = false, want true")
	}
	if len(in.Body) > MaxInputBytes+200 {
		t.Errorf("len(in.Body) = %d, want <= %d", len(in.Body), MaxInputBytes+200)
	}
	if !strings.Contains(in.Body, "truncated at") {
		t.Errorf("in.Body missing truncation notice: %s", in.Body[len(in.Body)-100:])
	}
	if in.Thread == nil {
		t.Error("in.Thread is nil")
	}
}

// TS-04-6 (unit): RenderThread renders distinct header prefix based on forge host and pull request flag
// Verifies: 04-REQ-2.1, 04-REQ-2.2, 04-REQ-2.3, 04-REQ-10.2
func TestTS0406_RenderThreadBrandingHeader(t *testing.T) {
	refGL := issuex.IssueRef{Repo: issuex.Repo{Host: "gitlab.com", Owner: "o", Name: "r"}, Number: 1, IsPullRequest: true}
	refGH := issuex.IssueRef{Repo: issuex.Repo{Host: "github.com", Owner: "o", Name: "r"}, Number: 2, IsPullRequest: false}
	refGHEmpty := issuex.IssueRef{Repo: issuex.Repo{Host: "", Owner: "o", Name: "r"}, Number: 3, IsPullRequest: false}
	refCustom := issuex.IssueRef{Repo: issuex.Repo{Host: "forgejo.example.com", Owner: "o", Name: "r"}, Number: 4, IsPullRequest: true}

	thread := issuex.IssueThread{Issue: issuex.Issue{State: "open"}}

	if got := RenderThread(refGL, thread); !strings.HasPrefix(got, "GitLab pull request o/r#1 (state: open)\n") {
		t.Errorf("GitLab PR header = %q", got)
	}
	if got := RenderThread(refGH, thread); !strings.HasPrefix(got, "GitHub issue o/r#2 (state: open)\n") {
		t.Errorf("GitHub issue header = %q", got)
	}
	if got := RenderThread(refGHEmpty, thread); !strings.HasPrefix(got, "GitHub issue o/r#3 (state: open)\n") {
		t.Errorf("GitHub empty host header = %q", got)
	}
	if got := RenderThread(refCustom, thread); !strings.HasPrefix(got, "Forge pull request o/r#4 (state: open)\n") {
		t.Errorf("Forge custom host header = %q", got)
	}
}

// TS-04-7 (property): RenderThread formats issue thread metadata and comments in deterministic order
// Verifies: 04-REQ-2.4
func TestTS0407_RenderThreadDeterministicOrder(t *testing.T) {
	testCases := []issuex.IssueThread{
		{
			Issue: issuex.Issue{
				Title:  "Crash in parser",
				Author: issuex.User{Login: "alice"},
				State:  "open",
				Body:   "Steps to reproduce:\n1. run app\n2. crash",
				Labels: []string{"bug", "critical"},
			},
			Comments: []issuex.Comment{
				{User: issuex.User{Login: "bob"}, Body: "Confirmed on macOS"},
				{User: issuex.User{Login: "carol"}, Body: "Fixed in PR #12"},
			},
		},
		{
			Issue: issuex.Issue{
				Title:  "Feature request: dark mode",
				Author: issuex.User{Login: "david"},
				State:  "closed",
				Body:   "Please add dark mode.",
				Labels: nil, // no labels
			},
			Comments: []issuex.Comment{
				{User: issuex.User{Login: "eve"}, Body: "Shipped in v2.0"},
			},
		},
		{
			Issue: issuex.Issue{
				Title:  "Empty issue",
				Author: issuex.User{Login: "frank"},
				State:  "open",
				Body:   "",
			},
			Comments: nil,
		},
	}

	for i, tc := range testCases {
		ref := issuex.IssueRef{Repo: issuex.Repo{Host: "github.com", Owner: "org", Name: "repo"}, Number: i + 1}
		out1 := RenderThread(ref, tc)
		out2 := RenderThread(ref, tc)

		if out1 != out2 {
			t.Fatalf("case %d: RenderThread is not deterministic: out1 != out2", i)
		}

		titleSub := "Title: " + tc.Issue.Title
		authorSub := "Author: " + tc.Issue.Author.Login
		idxTitle := strings.Index(out1, titleSub)
		if idxTitle == -1 {
			t.Errorf("case %d: missing %q", i, titleSub)
		}
		idxAuthor := strings.Index(out1, authorSub)
		if idxAuthor == -1 {
			t.Errorf("case %d: missing %q", i, authorSub)
		}
		if idxAuthor <= idxTitle {
			t.Errorf("case %d: Author appears before Title", i)
		}

		lastIdx := idxAuthor
		if len(tc.Issue.Labels) > 0 {
			labelsSub := "Labels: " + strings.Join(tc.Issue.Labels, ", ")
			idxLabels := strings.Index(out1, labelsSub)
			if idxLabels == -1 {
				t.Errorf("case %d: missing %q", i, labelsSub)
			}
			if idxLabels <= lastIdx {
				t.Errorf("case %d: Labels appear before Author", i)
			}
			lastIdx = idxLabels
		} else {
			if strings.Contains(out1, "Labels:") {
				t.Errorf("case %d: unexpected Labels line", i)
			}
		}

		if tc.Issue.Body != "" {
			idxBody := strings.Index(out1, strings.TrimSpace(tc.Issue.Body))
			if idxBody == -1 {
				t.Errorf("case %d: missing body", i)
			}
			if idxBody <= lastIdx {
				t.Errorf("case %d: Body appears before header/labels", i)
			}
			lastIdx = idxBody
		}

		for _, c := range tc.Comments {
			expectedComment := fmt.Sprintf("--- comment by %s ---\n%s", c.User.Login, strings.TrimSpace(c.Body))
			idxComment := strings.Index(out1, expectedComment)
			if idxComment == -1 {
				t.Errorf("case %d: missing comment block %q", i, expectedComment)
			}
			if idxComment <= lastIdx {
				t.Errorf("case %d: comment block appears out of order", i)
			}
			lastIdx = idxComment
		}
	}
}

// TS-04-8 (unit): RenderThread appends truncation notice when comments are truncated
// Verifies: 04-REQ-2.5
func TestTS0408_RenderThreadTruncationNotice(t *testing.T) {
	ref := issuex.IssueRef{Repo: issuex.Repo{Host: "gitlab.com", Owner: "g", Name: "p"}, Number: 10}
	thread := issuex.IssueThread{
		Issue:     issuex.Issue{Title: "Bug", State: "open", Author: issuex.User{Login: "alice"}},
		Truncated: true,
	}
	out := RenderThread(ref, thread)
	if !strings.HasSuffix(out, "\n[... further comments not read ...]\n") {
		t.Errorf("out = %q, want ending with truncation notice", out)
	}

	threadNotTruncated := issuex.IssueThread{
		Issue:     issuex.Issue{Title: "Bug", State: "open", Author: issuex.User{Login: "alice"}},
		Truncated: false,
	}
	outNotTruncated := RenderThread(ref, threadNotTruncated)
	if strings.Contains(outNotTruncated, "[... further comments not read ...]") {
		t.Errorf("outNotTruncated = %q, should not contain truncation notice", outNotTruncated)
	}
}
