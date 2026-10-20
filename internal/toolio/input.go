package toolio

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/issuex"
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

// WarnInputTruncated is raised when an input's body was cut, or the thread of
// an issue was cut short.
const WarnInputTruncated = "input_truncated"

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

	in.Body = strings.TrimSpace(in.Body)
	if in.Body == "" {
		return Input{}, &Failure{Category: CategoryUsage, Message: "the input is empty"}
	}
	body, cut := Truncate(in.Body)
	in.Body = body
	in.Truncated = in.Truncated || cut
	return in, nil
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

// resolveIssue reads the thread of ref with forge.
func resolveIssue(ctx context.Context, ref issuex.IssueRef, forge issuex.Client) (Input, error) {
	return Input{}, &Failure{Category: CategoryInternal, Message: "reading an issue or pull request is not implemented"}
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
