package toolio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/agent-fox-dev/aflib/issuex"
)

const (
	// MaxInputBytes is how much of an input's body is kept.
	MaxInputBytes = 131072

	// truncationMarker ends a body that was cut at MaxInputBytes.
	truncationMarker = "\n[... input truncated at 131072 bytes ...]\n"

	// binarySniffBytes is how much of a file is looked at for a NUL byte.
	binarySniffBytes = 8 << 10

	// maxReadBytes bounds what is read from stdin or a file before the body
	// is trimmed and cut: a guard on memory, not a limit anyone meets, since
	// MaxInputBytes is far below it.
	maxReadBytes = 16 << 20
)

// The warnings the input raises.
const (
	// WarnInputTruncated: the body was cut, or the thread of an issue was cut
	// short.
	WarnInputTruncated = "input_truncated"
	// WarnCommentsUnreadable: the issue was read, its comments were not.
	WarnCommentsUnreadable = "comments_unreadable"
)

// InputMode says whether a command takes the positional input, the
// <issue|file|text|-> argument.
type InputMode int

// The input modes of a command.
const (
	// InputNone: the command takes no input; its positional arguments are
	// its own and are never read.
	InputNone InputMode = iota
	// InputOptional: the command works with or without an input.
	InputOptional
	// InputRequired: the command refuses to run without an input.
	InputRequired
)

// Input is the positional argument, resolved.
type Input struct {
	Kind      InputKind
	Origin    string
	Body      string
	Truncated bool
	Issue     *issuex.IssueRef
	Thread    *issuex.IssueThread

	cut bool // the body was cut at MaxInputBytes
}

// Resolve classifies arg, in this order, and reads it:
//
//  1. "-" is stdin, read until EOF.
//  2. A GitHub or GitLab issue, pull request or merge request URL is an
//     issue, read with forge.
//  3. Any other http:// or https:// URL is a usage error.
//  4. The name of an existing regular file, relative to the working
//     directory, is a file.
//  5. Anything else is text.
//
// The body is trimmed, must not be empty, and is cut at MaxInputBytes.
func Resolve(ctx context.Context, arg string, stdin io.Reader, forge issuex.Client) (Input, error) {
	var in Input
	var err error
	switch ref, isIssue := issuex.ParseIssueURL(arg); {
	case arg == "-":
		in, err = readStdin(ctx, stdin)
	case isIssue:
		in, err = resolveIssue(ctx, ref, forge)
	case isHTTPURL(arg):
		err = notAnIssueURL(arg)
	default:
		var isFile bool
		in, isFile, err = readFile(arg)
		if !isFile && err == nil {
			in = Input{Kind: KindText, Body: arg}
		}
	}
	if err != nil {
		return Input{}, err
	}

	if in.Kind != KindIssue { // a rendered thread is left as it is
		in.Body = strings.TrimSpace(in.Body)
	}
	if strings.TrimSpace(in.Body) == "" {
		return Input{}, &Failure{Category: CategoryUsage, Message: "the input is empty"}
	}
	in.Body, in.cut = Truncate(in.Body)
	in.Truncated = in.Truncated || in.cut
	return in, nil
}

// warnings are what the shell tells the caller about the input: a body that
// was cut, a thread that was cut short, comments that could not be read.
func (in Input) warnings() []Warning {
	var ws []Warning
	if in.cut {
		ws = append(ws, Warning{WarnInputTruncated, fmt.Sprintf("the input was cut at %d bytes", MaxInputBytes)})
	}
	if in.Thread != nil {
		if in.Thread.Truncated {
			ws = append(ws, Warning{WarnInputTruncated, "the thread has more comments than were read"})
		}
		if err := in.Thread.CommentsErr; err != nil {
			ws = append(ws, Warning{WarnCommentsUnreadable, fmt.Sprintf("the comments could not be read: %v", err)})
		}
	}
	return ws
}

// resolveInput resolves the first positional argument of a command that
// declares an input, or finds there is none to resolve.
func resolveInput(ctx context.Context, mode InputMode, positional []string, stdin io.Reader, forge issuex.Client) (Input, error) {
	if len(positional) == 0 {
		if mode == InputRequired {
			return Input{}, &Failure{Category: CategoryUsage, Message: "no input given: pass an issue or pull request URL, a file, text, or - to read stdin"}
		}
		return Input{Kind: KindNone}, nil
	}
	return Resolve(ctx, positional[0], stdin, forge)
}

// Truncate cuts body to MaxInputBytes at the last UTF-8 boundary at or below
// that, and ends it with a marker. It reports whether it cut.
func Truncate(body string) (string, bool) {
	if len(body) <= MaxInputBytes {
		return body, false
	}
	cut := MaxInputBytes
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return body[:cut] + truncationMarker, true
}

// resolveIssue reads the thread of ref with forge and renders it. The thread
// of a pull request that was named by an issue URL makes ref a pull request.
func resolveIssue(ctx context.Context, ref issuex.IssueRef, forge issuex.Client) (Input, error) {
	if forge == nil {
		return Input{}, &Failure{Category: CategoryUsage, Message: "this command does not read issues or pull requests from a forge; pass text or a file"}
	}
	thread, err := forge.ReadIssue(ctx, ref)
	if err != nil {
		return Input{}, readFailure(ctx, ref, forge, err)
	}
	ref.IsPullRequest = ref.IsPullRequest || thread.Issue.IsPR
	return Input{
		Kind:      KindIssue,
		Origin:    ref.URL(),
		Body:      RenderThread(ref, thread),
		Truncated: thread.Truncated,
		Issue:     &ref,
		Thread:    &thread,
	}, nil
}

// readFailure is the failure for an issue that could not be read. A run that
// was cancelled is cancelled, whatever the forge client made of it; a 404
// without a token may be a private issue; a refusal retrying cannot undo is
// not retryable.
func readFailure(ctx context.Context, ref issuex.IssueRef, forge issuex.Client, err error) *Failure {
	switch {
	case ctx.Err() != nil:
		return &Failure{Category: CategoryCancelled, Message: fmt.Sprintf("cancelled while reading %s", ref.URL())}
	case issuex.IsNotFound(err) && !forge.Authenticated():
		return &Failure{
			Category: CategoryAuth,
			Message:  fmt.Sprintf("%s was not found; it may be private. Set GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN to read it", ref.URL()),
			FixHint:  &FixHint{Env: tokenVariable(ref)},
		}
	case issuex.IsNotFound(err):
		return &Failure{Category: CategoryForge, Message: fmt.Sprintf("%s was not found: %v", ref.URL(), err)}
	}
	return &Failure{Category: CategoryForge, Message: fmt.Sprintf("cannot read %s: %v", ref.URL(), err), Retryable: retryable(err)}
}

// tokenVariable is the environment variable that holds the token of the
// forge ref lives on.
func tokenVariable(ref issuex.IssueRef) string {
	// The host decides, and an API URL in the environment can name a host
	// that has neither forge in its name.
	forge, err := issuex.DetectForge(issuex.Options{Repo: ref.Repo})
	if err != nil {
		forge = issuex.ForgeTypeUnknown
		if forgeName(ref.Repo.Host) == "GitLab" {
			forge = issuex.ForgeTypeGitLab
		}
	}
	if forge == issuex.ForgeTypeGitLab {
		return "GITLAB_TOKEN"
	}
	return "GITHUB_TOKEN"
}

// retryable reports whether trying a failed read again may succeed: it may
// after a rate limit, a server error or a lost connection, and may not after
// the forge refused the request.
func retryable(err error) bool {
	if issuex.IsRateLimited(err) {
		return true
	}
	var he *issuex.HTTPError
	if errors.As(err, &he) && he.Status >= 400 && he.Status < 500 && he.Status != http.StatusRequestTimeout {
		return false
	}
	return true
}

// isHTTPURL reports whether arg looks like a web URL.
func isHTTPURL(arg string) bool {
	s := strings.ToLower(strings.TrimSpace(arg))
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// notAnIssueURL is the usage failure for a web URL that names no issue.
func notAnIssueURL(arg string) *Failure {
	return &Failure{
		Category: CategoryUsage,
		Message: fmt.Sprintf("%q is not an issue or pull request URL; accepted are "+
			"https://github.com/<owner>/<repo>/issues/<number>, https://github.com/<owner>/<repo>/pull/<number>, "+
			"https://gitlab.com/<group>/<project>/-/issues/<number> and https://gitlab.com/<group>/<project>/-/merge_requests/<number>", arg),
		FixHint: &FixHint{
			Env:     "GITHUB_API_URL",
			Suggest: "for a self-hosted forge, set GITHUB_API_URL (GitHub) or GITLAB_API_URL (GitLab) to its API URL",
		},
	}
}

// readStdin reads stdin until EOF. It gives up when ctx is cancelled, so that
// an interrupt is not lost on a terminal nobody types into.
func readStdin(ctx context.Context, stdin io.Reader) (Input, error) {
	type read struct {
		data []byte
		err  error
	}
	done := make(chan read, 1)
	go func() {
		data, err := io.ReadAll(io.LimitReader(stdin, maxReadBytes))
		done <- read{data, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return Input{}, &Failure{Category: CategoryIO, Message: fmt.Sprintf("cannot read stdin: %v", r.err)}
		}
		return Input{Kind: KindStdin, Body: string(r.data)}, nil
	case <-ctx.Done():
		return Input{}, &Failure{Category: CategoryCancelled, Message: "cancelled while reading stdin"}
	}
}

// readFile reads arg if it names an existing regular file. It reports
// whether it does: a name that is no file is text. A directory is a usage
// error; a file that cannot be read, or is not text, is an error too.
func readFile(arg string) (Input, bool, error) {
	info, err := os.Stat(arg)
	if err != nil {
		return Input{}, false, nil
	}
	if info.IsDir() {
		return Input{}, true, &Failure{Category: CategoryUsage, Message: fmt.Sprintf("%s is a directory; pass a file, text, an issue URL or - for stdin", arg)}
	}
	if !info.Mode().IsRegular() {
		return Input{}, false, nil
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return Input{}, true, &Failure{Category: CategoryIO, Message: fmt.Sprintf("cannot resolve %s: %v", arg, err)}
	}
	f, err := os.Open(abs)
	if err != nil {
		return Input{}, true, &Failure{Category: CategoryIO, Message: fmt.Sprintf("cannot read %s: %v", abs, err)}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxReadBytes))
	if err != nil {
		return Input{}, true, &Failure{Category: CategoryIO, Message: fmt.Sprintf("cannot read %s: %v", abs, err)}
	}
	if bytes.IndexByte(data[:min(len(data), binarySniffBytes)], 0) >= 0 {
		return Input{}, true, &Failure{Category: CategoryUsage, Message: fmt.Sprintf("%s is not a text file", abs)}
	}
	return Input{Kind: KindFile, Origin: abs, Body: string(data)}, true, nil
}
