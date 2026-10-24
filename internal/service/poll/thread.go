package poll

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/agent-fox-dev/aflib/issuex"
)

// What a person wrote on an issue since the service asked: the comments of the thread that count as the answer to a
// blocked job's question, or as the context of a failed job's next attempt, and their text.

// serviceMarker is what every text the service writes to an issue carries: a comment that has it is the service's own,
// whoever the forge says wrote it, since hub's credential may be a person's.
const serviceMarker = "<!-- agent-fox"

// maxThreadText is the longest text the comments become: the API's limit of an answer, and of one context string.
const maxThreadText = 8 << 10

// cutLine is the last line of a text that was cut to maxThreadText.
const cutLine = "[… cut at 8192 bytes; the issue holds the rest …]"

// counted are the comments of the thread that count for the job's latest attempt: those after the comment that carries
// the attempt's marker (its question, its failure comment or a parked fix's summary), in the order hub lists them, or,
// when no comment carries it, those made at or after the attempt finished. The service's own comments and blank ones
// never count. The anchor is the first comment with the marker: the replies of the trigger carry it as well, and come
// after it.
func counted(comments []issuex.Comment, job string, attempt int, finished time.Time) []issuex.Comment {
	anchor := fmt.Sprintf("<!-- agent-fox job=%s attempt=%d kind=", job, attempt)
	from, marked := 0, false
	for i, c := range comments {
		if strings.Contains(c.Body, anchor) {
			from, marked = i+1, true
			break
		}
	}
	var out []issuex.Comment
	for _, c := range comments[from:] {
		if !marked && c.CreatedAt.Before(finished) {
			continue
		}
		if strings.Contains(c.Body, serviceMarker) || strings.TrimSpace(c.Body) == "" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// render is the text of the comments: each as "--- comment by <login> ---" and its body, trimmed, the blocks joined by a
// blank line in order, with NUL bytes dropped and invalid UTF-8 replaced.
func render(comments []issuex.Comment) string {
	blocks := make([]string, 0, len(comments))
	for _, c := range comments {
		blocks = append(blocks, "--- comment by "+sane(c.User.Login)+" ---\n"+strings.TrimSpace(sane(c.Body)))
	}
	return strings.Join(blocks, "\n\n")
}

// sane is the text with NUL bytes dropped and invalid UTF-8 replaced.
func sane(s string) string { return strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "�") }

// bound is the text as it is answered or given as context: unchanged when it is at most maxThreadText bytes, and
// otherwise cut at a rune boundary so that it ends with the cut line and is at most maxThreadText bytes with it.
func bound(text string) string {
	if len(text) <= maxThreadText {
		return text
	}
	keep := maxThreadText - len("\n"+cutLine)
	for keep > 0 && !utf8.RuneStart(text[keep]) {
		keep--
	}
	return text[:keep] + "\n" + cutLine
}
