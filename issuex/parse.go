package issuex

import (
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ParseRepo parses a slash-delimited repository path (e.g. "owner/repo" or "group/subgroup/project") into a Repo.
// The last segment is assigned to Name, and all preceding segments joined by slash are assigned to Owner.
// Returns (Repo{}, false) if the input is empty or lacks slash-delimited segments.
func ParseRepo(s string) (Repo, bool) {
	s = strings.TrimSpace(s)
	if s == "" || !strings.Contains(s, "/") {
		return Repo{}, false
	}
	parts := strings.Split(s, "/")
	for _, p := range parts {
		if p == "" {
			return Repo{}, false
		}
	}
	name := strings.TrimSuffix(parts[len(parts)-1], ".git")
	if name == "" {
		return Repo{}, false
	}
	owner := strings.Join(parts[:len(parts)-1], "/")
	return Repo{Owner: owner, Name: name}, true
}

// isSupportedHost reports whether host belongs to a recognized GitHub or GitLab forge.
func isSupportedHost(host string) bool {
	h := strings.ToLower(host)
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	h = strings.TrimPrefix(h, "www.")
	if h == "" {
		return false
	}
	if strings.Contains(h, "github") || strings.Contains(h, "gitlab") {
		return true
	}
	if ghAPI := os.Getenv("GITHUB_API_URL"); ghAPI != "" {
		if u, err := url.Parse(ghAPI); err == nil && u.Hostname() != "" {
			ghHost := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
			if ghHost == h || strings.TrimPrefix(ghHost, "api.") == h {
				return true
			}
		}
	}
	if glAPI := os.Getenv("GITLAB_API_URL"); glAPI != "" {
		if u, err := url.Parse(glAPI); err == nil && u.Hostname() != "" {
			glHost := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
			if glHost == h || strings.TrimPrefix(glHost, "api.") == h {
				return true
			}
		}
	}
	return false
}

// ParseRemote parses git remote URLs across HTTPS, SSH, and SCP-style syntax matching a supported forge host into a Repo.
// It supports multi-segment owner paths (e.g. for GitLab) and strips the .git suffix.
// Returns (Repo{}, false) for invalid remote URLs, missing hosts, or unsupported formats.
func ParseRemote(remote string) (Repo, bool) {
	s := strings.TrimSpace(remote)
	if s == "" {
		return Repo{}, false
	}

	var host, path string

	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return Repo{}, false
		}
		scheme := strings.ToLower(u.Scheme)
		if scheme != "https" && scheme != "http" && scheme != "ssh" && scheme != "git" {
			return Repo{}, false
		}
		host = u.Hostname()
		path = u.Path
	} else if strings.Contains(s, ":") {
		// SCP-like syntax: [user@]host:path
		userHost, repoPath, found := strings.Cut(s, ":")
		if !found {
			return Repo{}, false
		}
		if at := strings.Index(userHost, "@"); at >= 0 {
			userHost = userHost[at+1:]
		}
		host = userHost
		path = repoPath
	} else {
		return Repo{}, false
	}

	host = strings.ToLower(strings.TrimPrefix(host, "www."))
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	if host == "" || !isSupportedHost(host) {
		return Repo{}, false
	}

	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	if path == "" {
		return Repo{}, false
	}

	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return Repo{}, false
	}
	for _, p := range parts {
		if p == "" {
			return Repo{}, false
		}
	}

	name := parts[len(parts)-1]
	owner := strings.Join(parts[:len(parts)-1], "/")

	return Repo{
		Host:  host,
		Owner: owner,
		Name:  name,
	}, true
}

// DetectRepo executes "git -C <dir> remote get-url origin" and parses the output with ParseRemote.
// If the directory is not a git repository or origin remote is not configured, it returns (Repo{}, false) without error.
func DetectRepo(dir string) (Repo, bool) {
	if dir == "" {
		dir = "."
	}
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return Repo{}, false
	}
	return ParseRemote(strings.TrimSpace(string(out)))
}

// ParseIssueURL parses GitHub web issue/PR URLs and GitLab web issue/MR URLs into an IssueRef.
// Returns (IssueRef{}, false) if the input is malformed, non-numeric, or a non-issue path.
func ParseIssueURL(s string) (IssueRef, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return IssueRef{}, false
	}

	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return IssueRef{}, false
	}

	host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	if host == "" || !isSupportedHost(host) {
		return IssueRef{}, false
	}

	path := strings.Trim(u.Path, "/")
	if path == "" {
		return IssueRef{}, false
	}

	// Check GitLab syntax with "/-/" infix:
	// e.g. /group/sub/proj/-/issues/123 or /group/sub/proj/-/merge_requests/123
	if idx := strings.Index(path, "/-/"); idx >= 0 {
		repoPath := path[:idx]
		actionPath := path[idx+3:]

		parts := strings.Split(repoPath, "/")
		if len(parts) < 2 {
			return IssueRef{}, false
		}
		for _, p := range parts {
			if p == "" {
				return IssueRef{}, false
			}
		}
		name := parts[len(parts)-1]
		owner := strings.Join(parts[:len(parts)-1], "/")

		actionParts := strings.Split(actionPath, "/")
		if len(actionParts) != 2 {
			return IssueRef{}, false
		}

		var isPR bool
		switch actionParts[0] {
		case "issues", "issue":
			isPR = false
		case "merge_requests", "merge_request":
			isPR = true
		default:
			return IssueRef{}, false
		}

		num, err := strconv.Atoi(actionParts[1])
		if err != nil || num <= 0 {
			return IssueRef{}, false
		}

		return IssueRef{
			Repo: Repo{
				Host:  host,
				Owner: owner,
				Name:  name,
			},
			Number:        num,
			IsPullRequest: isPR,
		}, true
	}

	// GitHub syntax: /owner/repo/issues/123 or /owner/repo/pull/123
	parts := strings.Split(path, "/")
	if len(parts) != 4 {
		return IssueRef{}, false
	}
	if parts[0] == "" || parts[1] == "" {
		return IssueRef{}, false
	}

	var isPR bool
	switch parts[2] {
	case "issues", "issue":
		isPR = false
	case "pull", "pulls":
		isPR = true
	default:
		return IssueRef{}, false
	}

	num, err := strconv.Atoi(parts[3])
	if err != nil || num <= 0 {
		return IssueRef{}, false
	}

	return IssueRef{
		Repo: Repo{
			Host:  host,
			Owner: parts[0],
			Name:  parts[1],
		},
		Number:        num,
		IsPullRequest: isPR,
	}, true
}
