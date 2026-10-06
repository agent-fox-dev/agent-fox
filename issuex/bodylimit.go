package issuex

// The largest body, in characters, each forge accepts for an issue, a pull
// or merge request, or a comment. A body over the limit is refused outright
// (GitHub answers 422), so a caller that renders an unbounded body measures
// it against MaxBodyLength first.
const (
	GitHubMaxBodyLength = 65536
	GitLabMaxBodyLength = 1048576
)

// BodyLimiter is a client that reports its forge's body limit.
type BodyLimiter interface {
	MaxBodyLength() int
}

// MaxBodyLength is the body limit of the forge c talks to. A client that does
// not report one gets the smallest known limit, GitHub's, so a body measured
// against it fits every forge.
//
// The limits are in characters; a caller measuring in bytes (len) is safe,
// because a body never has more characters than bytes.
func MaxBodyLength(c Client) int {
	if l, ok := c.(BodyLimiter); ok {
		if n := l.MaxBodyLength(); n > 0 {
			return n
		}
	}
	return GitHubMaxBodyLength
}

// MaxBodyLength is GitHub's limit on an issue, pull request or comment body.
func (c *githubClient) MaxBodyLength() int { return GitHubMaxBodyLength }

// MaxBodyLength is GitLab's limit on a description or note.
func (c *gitlabClient) MaxBodyLength() int { return GitLabMaxBodyLength }
