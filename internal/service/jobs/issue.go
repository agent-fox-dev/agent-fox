package jobs

import (
	"encoding/json"
	"regexp"
	"strconv"

	"github.com/agent-fox-dev/aflib/issuex"
)

// maxIssueBytes is the longest an issue given as a string may be.
const maxIssueBytes = 2048

var (
	// positiveInteger is a JSON number that is a positive integer.
	positiveInteger = regexp.MustCompile(`^[1-9][0-9]*$`)
	// pathAndNumber is <path>#<N>: two or more /-separated segments of letters, digits
	// and _ . -, then # and digits.
	pathAndNumber = regexp.MustCompile(`^[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)+#([0-9]+)$`)
)

// ParseIssue reads the issue of a request, as it was posted, and returns its
// number. An issue is accepted exactly when it is one of three things: a JSON
// number that is a positive integer; a string of at most 2048 bytes that
// issuex.ParseIssueURL accepts and does not mark as a pull or merge request; or a
// string <path>#<N>. Which repository the number belongs to is for the worker to
// check against the workspace.
func ParseIssue(raw json.RawMessage) (int64, bool) {
	if positiveInteger.Match(raw) {
		n, err := strconv.ParseInt(string(raw), 10, 64)
		return n, err == nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil || len(s) > maxIssueBytes {
		return 0, false
	}
	if ref, ok := issuex.ParseIssueURL(s); ok {
		return int64(ref.Number), !ref.IsPullRequest
	}
	m := pathAndNumber.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	return n, err == nil && n > 0
}
