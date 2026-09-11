package ghapi

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/agent-fox-dev/agentfox/issuex"
)

// Repo names one repository across supported forges.
//
// Deprecated: use github.com/agent-fox-dev/agentfox/issuex.Repo instead.
type Repo = issuex.Repo

// IssueRef identifies one issue or pull request.
//
// Deprecated: use github.com/agent-fox-dev/agentfox/issuex.IssueRef instead.
type IssueRef = issuex.IssueRef

// ParseRepo parses a repository path into a Repo.
//
// Deprecated: use github.com/agent-fox-dev/agentfox/issuex.ParseRepo instead.
func ParseRepo(s string) (Repo, bool) {
	return issuex.ParseRepo(s)
}

// ParseIssueURL recognizes an issue or pull-request URL across supported forges.
//
// Deprecated: use github.com/agent-fox-dev/agentfox/issuex.ParseIssueURL instead.
func ParseIssueURL(s string) (IssueRef, bool) {
	return issuex.ParseIssueURL(s)
}

// DetectRepo reads `git remote get-url origin` in dir and parses owner/repo out of it.
//
// Deprecated: use github.com/agent-fox-dev/agentfox/issuex.DetectRepo instead.
func DetectRepo(dir string) (Repo, bool) {
	return issuex.DetectRepo(dir)
}

// ParseRemote parses git remote URLs matching a supported forge host into a Repo.
//
// Deprecated: use github.com/agent-fox-dev/agentfox/issuex.ParseRemote instead.
func ParseRemote(remote string) (Repo, bool) {
	return issuex.ParseRemote(remote)
}

// GitHubHosts is the set of hosts a GitHub URL or remote may live on:
// github.com, and the Enterprise host behind apiURL with or without its
// "api." prefix (api.ghe.example.com serves ghe.example.com).
//
// Deprecated: use github.com/agent-fox-dev/agentfox/issuex instead.
func GitHubHosts(apiURL string) map[string]bool {
	hosts := map[string]bool{"github.com": true}
	u, err := url.Parse(strings.TrimSpace(apiURL))
	if err != nil || u.Host == "" {
		return hosts
	}
	h := strings.ToLower(u.Hostname())
	if h == "api.github.com" {
		return hosts
	}
	hosts[h] = true
	hosts[strings.TrimPrefix(h, "api.")] = true
	return hosts
}

func parseIssueURL(s string, hosts map[string]bool) (IssueRef, bool) {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return IssueRef{}, false
	}
	host := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
	if !hosts[host] {
		return IssueRef{}, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 {
		return IssueRef{}, false
	}
	var pull bool
	switch parts[2] {
	case "issues":
	case "pull", "pulls":
		pull = true
	default:
		return IssueRef{}, false
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n <= 0 {
		return IssueRef{}, false
	}
	if parts[0] == "" || parts[1] == "" {
		return IssueRef{}, false
	}
	return IssueRef{Repo: Repo{Owner: parts[0], Name: parts[1]}, Number: n, IsPullRequest: pull}, true
}

func parseRemote(remote string, hosts map[string]bool) (Repo, bool) {
	s := strings.TrimSuffix(strings.TrimSpace(remote), ".git")
	switch {
	case strings.Contains(s, "://"):
		s = s[strings.Index(s, "://")+3:]
		if at := strings.Index(s, "@"); at >= 0 {
			s = s[at+1:]
		}
	case strings.Contains(s, "@") && strings.Contains(s, ":"):
		// scp-like: git@host:owner/repo
		s = s[strings.Index(s, "@")+1:]
		s = strings.Replace(s, ":", "/", 1)
	}
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if len(parts) < 3 {
		return Repo{}, false
	}
	host := strings.ToLower(strings.TrimPrefix(parts[0], "www."))
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i] // a port
	}
	if !hosts[host] {
		return Repo{}, false
	}
	r := Repo{Owner: parts[len(parts)-2], Name: parts[len(parts)-1]}
	if !r.Valid() {
		return Repo{}, false
	}
	return r, true
}
