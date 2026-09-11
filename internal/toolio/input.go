package toolio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/agent-fox-dev/agentfox/issuex"
)

// SourceKind is what the single argument turned out to be.
//
// Every agent-fox tool takes exactly one input and it is one of these four.
// The classification is done here, in Go, and never delegated to a model:
// it decides whether a network call happens at all, which is a decision an
// operator must be able to audit without reading a transcript; a wrong guess
// costs a round trip and an issue filed about the wrong thing; and the rules
// are dull — "does this path exist on disk" is not a judgement call.
type SourceKind string

const (
	// KindText is literal text given on the command line.
	KindText SourceKind = "text"
	// KindFile is a readable regular file whose contents are the input.
	KindFile SourceKind = "file"
	// KindStdin is the input piped in, selected with the argument "-".
	KindStdin SourceKind = "stdin"
	// KindIssue is an issue or pull/merge request on any supported forge.
	KindIssue SourceKind = "issue"
)

func (k SourceKind) String() string { return string(k) }

// MaxInputBytes bounds what one input can contribute to a prompt. A 40MB log
// pasted at a coding agent is a bill, not an input, and truncating here —
// deterministically, at a line boundary, with a visible marker — is better
// than discovering the context window at request time.
const MaxInputBytes = 256 << 10

// Input is the classified argument: the text, plus where it came from.
type Input struct {
	Kind SourceKind
	// Origin is the path, the URL, or "argument"/"stdin".
	Origin string
	// Body is the text the tool works from, already truncated to
	// MaxInputBytes.
	Body string
	// Truncated reports that Body is shorter than the source.
	Truncated bool

	// Issue is set for KindIssue: the reference and the thread it was read
	// from. It is what lets a tool comment back on the issue it was given.
	Issue  *issuex.IssueRef
	Thread *issuex.IssueThread
}

// ErrNoInput is the "halt until input is received" branch of the skills these
// tools replace, except that a program cannot block on a conversational turn:
// it exits with a usage error before a token is spent.
var ErrNoInput = errors.New("no input given")

// Resolve classifies arg and loads it.
//
// The order is deliberate. "-" is stdin, unambiguously. An issue URL is
// recognized before the filesystem is touched, so a URL is never stat'ed. A
// path that exists and is a regular file is read. Everything else is text —
// including a path that does not exist, because "the widget/ package panics"
// is a plausible thing to say and refusing it as a missing file would be
// wrong.
func Resolve(ctx context.Context, arg string, stdin io.Reader, forge issuex.Client) (Input, error) {
	arg = strings.TrimSpace(arg)
	switch {
	case arg == "":
		return Input{}, ErrNoInput

	case arg == "-":
		b, err := io.ReadAll(io.LimitReader(stdin, MaxInputBytes+1))
		if err != nil {
			return Input{}, fmt.Errorf("reading stdin: %w", err)
		}
		body, cut := Truncate(string(b))
		if strings.TrimSpace(body) == "" {
			return Input{}, ErrNoInput
		}
		return Input{Kind: KindStdin, Origin: "stdin", Body: body, Truncated: cut}, nil
	}

	if ref, ok := issuex.ParseIssueURL(arg); ok {
		return resolveIssue(ctx, ref, forge)
	}
	if body, cut, ok, err := readIfFile(arg); err != nil {
		return Input{}, err
	} else if ok {
		return Input{Kind: KindFile, Origin: filepath.Clean(arg), Body: body, Truncated: cut}, nil
	}
	body, cut := Truncate(arg)
	return Input{Kind: KindText, Origin: "argument", Body: body, Truncated: cut}, nil
}

// readIfFile reports whether arg names a readable regular file and returns
// its contents when it does. A path that exists but is a directory is not an
// error: it falls through to the text branch, because "src/" is a plausible
// thing to name in a bug report.
func readIfFile(arg string) (body string, truncated, ok bool, err error) {
	if strings.ContainsAny(arg, "\n") {
		return "", false, false, nil // multi-line input is a report, not a path
	}
	info, statErr := os.Stat(arg)
	if statErr != nil || !info.Mode().IsRegular() {
		return "", false, false, nil
	}
	f, openErr := os.Open(arg)
	if openErr != nil {
		return "", false, false, fmt.Errorf("reading %s: %w", arg, openErr)
	}
	defer func() { _ = f.Close() }()
	b, readErr := io.ReadAll(io.LimitReader(f, MaxInputBytes+1))
	if readErr != nil {
		return "", false, false, fmt.Errorf("reading %s: %w", arg, readErr)
	}
	body, truncated = Truncate(string(b))
	return body, truncated, true, nil
}

// resolveIssue renders an issue and its comments as one input document.
//
// The comments are included because the useful part of a report usually is
// not in the opening post — it is in the third reply, where someone pasted
// the traceback.
func resolveIssue(ctx context.Context, ref issuex.IssueRef, forge issuex.Client) (Input, error) {
	if forge == nil {
		return Input{}, fmt.Errorf("cannot read issue: no forge client configured")
	}
	thread, err := forge.ReadIssue(ctx, ref)
	if err != nil {
		if (errors.Is(err, issuex.ErrNotFound) || issuex.IsNotFound(err)) && !forge.Authenticated() {
			return Input{}, fmt.Errorf("reading issue %s: repository or issue may be private and require credentials: %w", ref, err)
		}
		return Input{}, err
	}
	ref.IsPullRequest = ref.IsPullRequest || thread.Issue.IsPR

	body, cut := Truncate(RenderThread(ref, thread))
	return Input{
		Kind:      KindIssue,
		Origin:    ref.URL(),
		Body:      body,
		Truncated: cut || thread.Truncated,
		Issue:     &ref,
		Thread:    &thread,
	}, nil
}

// RenderThread flattens an issue and its comments into the text a model
// reads. It is a pure function so two runs on the same thread produce the
// same prompt, which is what lets a provider's cache prefix survive.
func RenderThread(ref issuex.IssueRef, t issuex.IssueThread) string {
	var b strings.Builder
	kind := "issue"
	if ref.IsPullRequest {
		kind = "pull request"
	}
	var brand string
	host := strings.ToLower(ref.Repo.Host)
	switch {
	case strings.Contains(host, "gitlab"):
		brand = "GitLab"
	case strings.Contains(host, "github") || host == "":
		brand = "GitHub"
	default:
		brand = "Forge"
	}
	fmt.Fprintf(&b, "%s %s %s (state: %s)\n", brand, kind, ref, t.Issue.State)
	fmt.Fprintf(&b, "Title: %s\n", t.Issue.Title)
	fmt.Fprintf(&b, "Author: %s\n", t.Issue.Author.Login)
	if len(t.Issue.Labels) > 0 {
		fmt.Fprintf(&b, "Labels: %s\n", strings.Join(t.Issue.Labels, ", "))
	}
	fmt.Fprintf(&b, "\n%s\n", strings.TrimSpace(t.Issue.Body))
	for _, c := range t.Comments {
		fmt.Fprintf(&b, "\n--- comment by %s ---\n%s\n", c.User.Login, strings.TrimSpace(c.Body))
	}
	if t.Truncated {
		b.WriteString("\n[... further comments not read ...]\n")
	}
	return b.String()
}

// Truncate cuts at a line boundary and says so, because an input that ends
// mid-stack-trace with no marker reads to a model as a complete stack trace
// that simply had no more frames.
func Truncate(s string) (string, bool) {
	if len(s) <= MaxInputBytes {
		return s, false
	}
	cut := s[:MaxInputBytes]
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		cut = cut[:i]
	}
	return cut + "\n\n[... truncated at " + strconv.Itoa(MaxInputBytes) + " bytes ...]", true
}
