package ghapi

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Repo names one GitHub repository.
type Repo struct {
	Owner string
	Name  string
}

func (r Repo) String() string { return r.Owner + "/" + r.Name }

// Valid reports whether both halves are present.
func (r Repo) Valid() bool { return r.Owner != "" && r.Name != "" }

// ParseRepo parses an "owner/repo" pair, as written on a command line.
func ParseRepo(s string) (Repo, bool) {
	parts := strings.Split(strings.Trim(strings.TrimSpace(s), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Repo{}, false
	}
	return Repo{Owner: parts[0], Name: strings.TrimSuffix(parts[1], ".git")}, true
}

// IssueRef identifies one issue or pull request. GitHub numbers both from the
// same series, so an issue reference is enough to read either.
type IssueRef struct {
	Repo   Repo
	Number int
	// IsPullRequest records which URL form the reference was parsed from.
	// The issues API serves both, so this only affects how the reference is
	// rendered and whether the pull-request endpoints are worth calling.
	IsPullRequest bool
}

func (r IssueRef) String() string { return fmt.Sprintf("%s#%d", r.Repo, r.Number) }

// URL renders the canonical web URL for the reference.
func (r IssueRef) URL() string {
	kind := "issues"
	if r.IsPullRequest {
		kind = "pull"
	}
	return fmt.Sprintf("https://github.com/%s/%s/%s/%d", r.Repo.Owner, r.Repo.Name, kind, r.Number)
}

// ParseIssueURL recognizes a GitHub issue or pull-request URL. Anything else —
// including a github.com URL pointing at a file, a commit or a repository
// root — is not an issue reference, and the caller treats it as text.
//
// The host must be github.com or the Enterprise host GITHUB_API_URL names.
// Accepting an arbitrary host would let a URL on an unrelated forge be turned
// into a github.com owner/repo that happens to share the name, with the issue
// then read from — or filed against — the wrong project.
func ParseIssueURL(s string) (IssueRef, bool) {
	return parseIssueURL(s, GitHubHosts(os.Getenv("GITHUB_API_URL")))
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

// GitHubHosts is the set of hosts a GitHub URL or remote may live on:
// github.com, and the Enterprise host behind apiURL with or without its
// "api." prefix (api.ghe.example.com serves ghe.example.com).
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

// DetectRepo reads `git remote get-url origin` in dir and parses owner/repo
// out of it. A repository with no origin is not an error: it means the target
// has to be named explicitly, and saying that is more useful than failing
// here.
func DetectRepo(dir string) (Repo, bool) {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return Repo{}, false
	}
	return ParseRemote(strings.TrimSpace(string(out)))
}

// ParseRemote handles the three spellings git writes: git@host:owner/repo.git,
// https://host/owner/repo(.git) and ssh://git@host/owner/repo.git.
func ParseRemote(remote string) (Repo, bool) {
	return parseRemote(remote, GitHubHosts(os.Getenv("GITHUB_API_URL")))
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
