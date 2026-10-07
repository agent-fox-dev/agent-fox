package issuex

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
)

// ForgeType identifies a supported Git forge platform.
type ForgeType string

const (
	// ForgeTypeUnknown represents an unknown or unresolved forge type.
	ForgeTypeUnknown ForgeType = ""
	// ForgeTypeGitHub represents the GitHub platform.
	ForgeTypeGitHub ForgeType = "github"
	// ForgeTypeGitLab represents the GitLab platform.
	ForgeTypeGitLab ForgeType = "gitlab"
)

var (
	adaptersMu sync.RWMutex
	adapters   = make(map[ForgeType]func(Options) (Client, error))
)

// RegisterAdapter registers a client factory function for a specific ForgeType.
// This allows provider packages (such as issuex_github or issuex_gitlab) to register their implementations.
func RegisterAdapter(forge ForgeType, factory func(Options) (Client, error)) {
	adaptersMu.Lock()
	defer adaptersMu.Unlock()
	adapters[forge] = factory
}

// DetectForge resolves the ForgeType from explicit configuration,
// environment variables, or the local git origin remote.
func DetectForge(o Options) (ForgeType, error) {
	ft, _, err := detectForge(o)
	return ft, err
}

// detectForge resolves the ForgeType and effective Options from explicit configuration,
// environment variables, or local git origin remote.
func detectForge(o Options) (ForgeType, Options, error) {
	opts := o
	opts.BaseURL = strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")

	if opts.BaseURL != "" {
		u := strings.ToLower(opts.BaseURL)
		hasGH := strings.Contains(u, "github")
		hasGL := strings.Contains(u, "gitlab")
		if hasGH && !hasGL {
			return ForgeTypeGitHub, opts, nil
		}
		if hasGL && !hasGH {
			return ForgeTypeGitLab, opts, nil
		}
		if hasGH && hasGL {
			return ForgeTypeUnknown, opts, fmt.Errorf("%w: cannot determine forge from base URL %q", ErrAmbiguousForge, opts.BaseURL)
		}
		// BaseURL contains neither "github" nor "gitlab" (e.g. test mock server or GHE custom domain)
		if opts.RemoteURL != "" {
			if repo, ok := ParseRemote(opts.RemoteURL); ok {
				host := strings.ToLower(repo.Host)
				if strings.Contains(host, "github") && !strings.Contains(host, "gitlab") {
					if !opts.Repo.Valid() {
						opts.Repo = repo
					}
					return ForgeTypeGitHub, opts, nil
				}
				if strings.Contains(host, "gitlab") && !strings.Contains(host, "github") {
					if !opts.Repo.Valid() {
						opts.Repo = repo
					}
					return ForgeTypeGitLab, opts, nil
				}
			}
		}
		if opts.Repo.Valid() {
			host := strings.ToLower(opts.Repo.Host)
			if strings.Contains(host, "github") && !strings.Contains(host, "gitlab") {
				return ForgeTypeGitHub, opts, nil
			}
			if strings.Contains(host, "gitlab") && !strings.Contains(host, "github") {
				return ForgeTypeGitLab, opts, nil
			}
		}
		return ForgeTypeUnknown, opts, fmt.Errorf("%w: cannot determine forge from base URL %q", ErrAmbiguousForge, opts.BaseURL)
	}

	if opts.RemoteURL != "" {
		if repo, ok := ParseRemote(opts.RemoteURL); ok {
			if ft := classifyHost(repo.Host); ft != ForgeTypeUnknown {
				opts = hostEndpoint(ft, repo.Host, opts)
				if !opts.Repo.Valid() {
					opts.Repo = repo
				}
				return ft, opts, nil
			}
		} else if host := extractHostFromRemote(opts.RemoteURL); host != "" && classifyHost(host) == ForgeTypeUnknown {
			return ForgeTypeUnknown, opts, fmt.Errorf("%w: ambiguous forge from remote URL host %q", ErrAmbiguousForge, host)
		}
	}

	// A repository whose host is known decides the forge by that host, and
	// nothing else: a host that names neither forge is not sent a request
	// meant for whichever forge the environment happens to configure.
	if opts.Repo.Host != "" {
		ft := classifyHost(opts.Repo.Host)
		if ft == ForgeTypeUnknown {
			return ForgeTypeUnknown, opts, fmt.Errorf("%w: cannot tell which forge serves %q; set GITHUB_API_URL "+
				"or GITLAB_API_URL to its API URL", ErrAmbiguousForge, opts.Repo.Host)
		}
		return ft, hostEndpoint(ft, opts.Repo.Host, opts), nil
	}

	ghEnv := os.Getenv("GITHUB_API_URL") != "" || os.Getenv("GITHUB_TOKEN") != "" || os.Getenv("GH_TOKEN") != ""
	glEnv := os.Getenv("GITLAB_API_URL") != "" || os.Getenv("GITLAB_TOKEN") != ""

	if ghEnv && !glEnv {
		if opts.BaseURL == "" {
			if apiURL := os.Getenv("GITHUB_API_URL"); apiURL != "" {
				opts.BaseURL = apiURL
			} else {
				opts.BaseURL = "https://api.github.com"
			}
		}
		if opts.Token == "" {
			opts.Token = envToken(ForgeTypeGitHub)
		}
		return ForgeTypeGitHub, opts, nil
	}

	if glEnv && !ghEnv {
		if opts.BaseURL == "" {
			if apiURL := os.Getenv("GITLAB_API_URL"); apiURL != "" {
				opts.BaseURL = apiURL
			} else {
				opts.BaseURL = "https://gitlab.com/api/v4"
			}
		}
		if opts.Token == "" {
			opts.Token = envToken(ForgeTypeGitLab)
		}
		return ForgeTypeGitLab, opts, nil
	}

	// Environment variables are either both present or both absent: the
	// origin remote of the working tree decides — of opts.Dir (--dir), not
	// of wherever the process happens to run.
	dir := opts.Dir
	if dir == "" {
		dir = "."
	}
	repo, ok := DetectRepo(dir)
	if !ok {
		return ForgeTypeUnknown, opts, fmt.Errorf("%w: unable to detect forge from environment or git origin remote", ErrAmbiguousForge)
	}
	ft := classifyHost(repo.Host)
	if ft == ForgeTypeUnknown {
		return ForgeTypeUnknown, opts, fmt.Errorf("%w: ambiguous forge from git remote host %q", ErrAmbiguousForge, repo.Host)
	}
	return ft, hostEndpoint(ft, repo.Host, opts), nil
}

// normHost is a host name compared case-insensitively and without "www.".
func normHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	return strings.TrimPrefix(h, "www.")
}

// apiURLNames reports whether apiURL is the API of the web host host: the
// same host, or api.<host> (api.github.com for github.com).
func apiURLNames(apiURL, host string) bool {
	if strings.TrimSpace(apiURL) == "" {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(apiURL))
	if err != nil || u.Hostname() == "" {
		return false
	}
	uh := normHost(u.Hostname())
	return uh == host || uh == "api."+host
}

// classifyHost is the forge a web host belongs to. A configured
// GITHUB_API_URL or GITLAB_API_URL that names the host decides first; then
// the host's own name ("github" or "gitlab" in it). Unknown otherwise.
func classifyHost(host string) ForgeType {
	h := normHost(host)
	if h == "" {
		return ForgeTypeUnknown
	}
	gh := apiURLNames(os.Getenv("GITHUB_API_URL"), h)
	gl := apiURLNames(os.Getenv("GITLAB_API_URL"), h)
	switch {
	case gh && !gl:
		return ForgeTypeGitHub
	case gl && !gh:
		return ForgeTypeGitLab
	case gh && gl:
		return ForgeTypeUnknown
	}
	hasGH, hasGL := strings.Contains(h, "github"), strings.Contains(h, "gitlab")
	switch {
	case hasGH && !hasGL:
		return ForgeTypeGitHub
	case hasGL && !hasGH:
		return ForgeTypeGitLab
	}
	return ForgeTypeUnknown
}

// envToken is the forge's token from the environment.
func envToken(ft ForgeType) string {
	if ft == ForgeTypeGitLab {
		return os.Getenv("GITLAB_TOKEN")
	}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		return tok
	}
	return os.Getenv("GH_TOKEN")
}

// hostEndpoint fills in the API base and the token for a forge whose web host
// is known. The environment's token goes only where the environment points
// it: to the host of GITHUB_API_URL / GITLAB_API_URL when that names this
// host, or to the public forge when no API URL is configured. Any other host
// — a self-hosted instance with no API URL configured, or the public forge
// while the API URL names an enterprise one — gets its own API address and no
// token: a credential must not leave for a host it was not configured for.
// An explicit BaseURL is the caller's choice and is left alone.
func hostEndpoint(ft ForgeType, host string, opts Options) Options {
	if opts.BaseURL != "" {
		return opts
	}
	h := normHost(host)
	envVar, public, publicAPI, suffix := "GITHUB_API_URL", "github.com", "https://api.github.com", "/api/v3"
	if ft == ForgeTypeGitLab {
		envVar, public, publicAPI, suffix = "GITLAB_API_URL", "gitlab.com", "https://gitlab.com/api/v4", "/api/v4"
	}
	envURL := strings.TrimSpace(os.Getenv(envVar))
	withhold := false
	switch {
	case envURL != "" && apiURLNames(envURL, h):
		opts.BaseURL = strings.TrimRight(envURL, "/")
	case h == public:
		opts.BaseURL = publicAPI
		withhold = envURL != "" && !apiURLNames(envURL, public)
	default:
		opts.BaseURL = "https://" + h + suffix
		withhold = true
	}
	if opts.Token == "" {
		if withhold {
			opts.withholdEnvToken = true
		} else {
			opts.Token = envToken(ft)
		}
	}
	return opts
}

func extractHostFromRemote(remote string) string {
	s := strings.TrimSpace(remote)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil {
			return strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
		}
	} else if strings.Contains(s, ":") {
		userHost, _, found := strings.Cut(s, ":")
		if found {
			if at := strings.Index(userHost, "@"); at >= 0 {
				userHost = userHost[at+1:]
			}
			h := strings.ToLower(strings.TrimPrefix(userHost, "www."))
			if i := strings.IndexByte(h, ':'); i >= 0 {
				h = h[:i]
			}
			return h
		}
	}
	return ""
}
