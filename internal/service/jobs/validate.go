package jobs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The limits of the fields of a request.
const (
	maxTitleBytes   = 256
	maxFileName     = 255
	maxContextItems = 10
	maxContextItem  = 8 << 10
	maxContextTotal = 32 << 10
)

// Body is what a triage or fix request asks, as it is posted. A triage takes exactly
// one of Issue, Text (with a Title, if it likes) and File; a fix takes an Issue.
type Body struct {
	Issue   json.RawMessage `json:"issue"`
	Text    *string         `json:"text"`
	Title   *string         `json:"title"`
	File    *File           `json:"file"`
	Context []string        `json:"context"`
}

// ValidationError is a field of a request that breaks its rule. Its message names
// the field.
type ValidationError struct {
	Field, Message string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

func invalid(field, format string, args ...any) error {
	return &ValidationError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// Parsed is a body that passed: the input without the context, the number of the
// issue it is about (nil for a text or a file), and the context.
type Parsed struct {
	Input   Input
	Issue   *int64
	Context []string
}

// Validate checks the body of a request for a job of the kind, against the rules of
// the fields. The first field that breaks its rule is a *ValidationError.
func (b Body) Validate(kind Kind) (Parsed, error) {
	hasIssue := len(b.Issue) > 0 && !bytes.Equal(bytes.TrimSpace(b.Issue), []byte("null"))
	hasText, hasFile := b.Text != nil, b.File != nil

	if kind == KindFix {
		switch {
		case hasText:
			return Parsed{}, invalid("text", "a fix takes an issue only")
		case b.Title != nil:
			return Parsed{}, invalid("title", "a fix takes an issue only")
		case hasFile:
			return Parsed{}, invalid("file", "a fix takes an issue only")
		case !hasIssue:
			return Parsed{}, invalid("issue", "is required")
		}
	} else {
		forms := 0
		for _, present := range []bool{hasIssue, hasText, hasFile} {
			if present {
				forms++
			}
		}
		if forms != 1 {
			return Parsed{}, invalid("issue", "exactly one of issue, text and file is required")
		}
		if b.Title != nil && !hasText {
			return Parsed{}, invalid("title", "is allowed with text only")
		}
	}

	var p Parsed
	switch {
	case hasIssue:
		n, ok := ParseIssue(b.Issue)
		if !ok {
			return Parsed{}, invalid("issue", "must be a positive integer, an issue URL that is not a pull or merge request, or owner/repo#N")
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, b.Issue); err != nil {
			return Parsed{}, invalid("issue", "is not valid JSON")
		}
		p.Issue, p.Input.Issue = &n, json.RawMessage(compact.Bytes())
	case hasText:
		if err := checkText("text", *b.Text); err != nil {
			return Parsed{}, err
		}
		p.Input.Text = *b.Text
		if b.Title != nil {
			if err := checkTitle(*b.Title); err != nil {
				return Parsed{}, err
			}
			p.Input.Title = *b.Title
		}
	default:
		if err := checkFile(*b.File); err != nil {
			return Parsed{}, err
		}
		p.Input.File = &File{Name: b.File.Name, Content: b.File.Content}
	}

	if err := checkContext(b.Context); err != nil {
		return Parsed{}, err
	}
	p.Context = append([]string{}, b.Context...)
	return p, nil
}

// checkText is the rule of a text and of the content of a file.
func checkText(field, s string) error {
	switch {
	case !utf8.ValidString(s):
		return invalid(field, "must be valid UTF-8")
	case strings.ContainsRune(s, 0):
		return invalid(field, "must not contain a NUL byte")
	case strings.TrimSpace(s) == "":
		return invalid(field, "must not be empty")
	case len(s) > toolio.MaxInputBytes:
		return invalid(field, "must be at most %d bytes, and is %d", toolio.MaxInputBytes, len(s))
	}
	return nil
}

// checkTitle is the rule of a title: one line of at most 256 bytes.
func checkTitle(s string) error {
	switch {
	case !utf8.ValidString(s):
		return invalid("title", "must be valid UTF-8")
	case strings.ContainsAny(s, "\n\r\x00"):
		return invalid("title", "must be one line")
	case len(s) > maxTitleBytes:
		return invalid("title", "must be at most %d bytes, and is %d", maxTitleBytes, len(s))
	}
	return nil
}

// checkFile is the rule of a file: a base name of 1 to 255 bytes, and a content that
// follows the rule of a text.
func checkFile(f File) error {
	switch {
	case !utf8.ValidString(f.Name):
		return invalid("file.name", "must be valid UTF-8")
	case len(f.Name) < 1 || len(f.Name) > maxFileName:
		return invalid("file.name", "must be 1 to %d bytes, and is %d", maxFileName, len(f.Name))
	case strings.ContainsAny(f.Name, "/\x00"):
		return invalid("file.name", "must be a base name, with no / and no NUL byte")
	}
	return checkText("file.content", f.Content)
}

// checkContext is the rule of the context: at most 10 strings, each not empty after
// trimming and of at most 8 KiB, and together at most 32 KiB.
func checkContext(notes []string) error {
	if len(notes) > maxContextItems {
		return invalid("context", "must have at most %d strings, and has %d", maxContextItems, len(notes))
	}
	total := 0
	for i, note := range notes {
		switch {
		case strings.TrimSpace(note) == "":
			return invalid("context", "string %d must not be empty", i+1)
		case len(note) > maxContextItem:
			return invalid("context", "string %d must be at most %d bytes, and is %d", i+1, maxContextItem, len(note))
		}
		total += len(note)
	}
	if total > maxContextTotal {
		return invalid("context", "must be at most %d bytes in all, and is %d", maxContextTotal, total)
	}
	return nil
}
