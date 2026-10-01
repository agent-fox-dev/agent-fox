package checks

import (
	"regexp"
	"strings"
)

// A commit body or a comment may describe what a change does, and it may not
// assert that the project's checks pass: that is a result, and the only thing
// entitled to state a result is the run of the command itself. These two
// patterns find a sentence that does — a verification subject and a passing
// word together — so it can be dropped from text a model wrote.
var (
	claimSubject = regexp.MustCompile(`(?i)\b(make\s+(check|test|lint|build)|go\s+(test|vet|build)|` +
		`(all|every|the|full|whole)\s+(project'?s\s+)?(tests?|checks?|lint|suite)|tests?|test\s+suite|` +
		`checks?|linters?|lint|ci|build)\b`)
	claimPassed = regexp.MustCompile(`(?i)\b(pass(es|ed|ing)?|green|succeed(s|ed)?|clean|ok)\b`)
)

// StripClaims removes from text every sentence that asserts the checks pass,
// and returns what is left, trimmed. Sentences are split on line breaks and on
// ". " so a list of changes keeps its other lines. A sentence that merely
// mentions a test without a passing word is kept.
func StripClaims(text string) string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		var out []string
		for _, sent := range splitSentences(line) {
			if claimSubject.MatchString(sent) && claimPassed.MatchString(sent) {
				continue
			}
			out = append(out, sent)
		}
		kept = append(kept, strings.TrimSpace(strings.Join(out, " ")))
	}
	return strings.TrimSpace(collapseBlank(strings.Join(kept, "\n")))
}

func splitSentences(line string) []string {
	var out []string
	rest := line
	for {
		i := sentenceEnd(rest)
		if i < 0 {
			if strings.TrimSpace(rest) != "" {
				out = append(out, strings.TrimSpace(rest))
			}
			return out
		}
		out = append(out, strings.TrimSpace(rest[:i+1]))
		rest = rest[i+2:]
	}
}

// sentenceEnd is the index of the first ". " that ends a sentence, or -1. A
// period after another period or a slash is part of a path or an ellipsis
// (`go test ./... passed`), not a full stop.
func sentenceEnd(s string) int {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '.' && s[i+1] == ' ' && (i == 0 || (s[i-1] != '.' && s[i-1] != '/')) {
			return i
		}
	}
	return -1
}

func collapseBlank(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}
