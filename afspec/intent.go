package afspec

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
)

// intentSectionPattern extracts the content between "## Intent" and the next
// heading (## ...) or end of string. The (?m) flag enables multiline mode
// so ^ matches the start of each line.
var intentSectionPattern = regexp.MustCompile(`(?m)^## Intent\s*\n([\s\S]*?)(?:^## |\z)`)

// ComputeIntentHash extracts the ## Intent section from a PRD body string,
// normalizes it, computes its SHA-256 hash, and returns the hex-encoded
// hash string. The hash is stable: identical intent text always produces
// the same hash.
//
// Returns an IntentError if the body does not contain a ## Intent section
// or if the body is empty.
func ComputeIntentHash(body string) (string, error) {
	if body == "" {
		return "", &IntentError{
			Msg: "no ## Intent section found in empty body",
			Err: &SpecError{Msg: "no ## Intent section found in empty body"},
		}
	}

	matches := intentSectionPattern.FindStringSubmatch(body)
	if matches == nil {
		return "", &IntentError{
			Msg: "no ## Intent section found in body",
			Err: &SpecError{Msg: "no ## Intent section found in body"},
		}
	}

	// Normalize: trim leading/trailing whitespace from the extracted section.
	intentText := strings.TrimSpace(matches[1])

	hash := sha256.Sum256([]byte(intentText))
	return fmt.Sprintf("%x", hash), nil
}
