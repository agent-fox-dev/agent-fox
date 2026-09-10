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
			host := strings.ToLower(repo.Host)
			hasHostGH := strings.Contains(host, "github")
			hasHostGL := strings.Contains(host, "gitlab")
			if hasHostGH && !hasHostGL {
				if opts.BaseURL == "" {
					if apiURL := os.Getenv("GITHUB_API_URL"); apiURL != "" {
						opts.BaseURL = apiURL
					} else {
						opts.BaseURL = "https://api.github.com"
					}
				}
				if opts.Token == "" {
					if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
						opts.Token = tok
					} else {
						opts.Token = os.Getenv("GH_TOKEN")
					}
				}
				if !opts.Repo.Valid() {
					opts.Repo = repo
				}
				return ForgeTypeGitHub, opts, nil
			}
			if hasHostGL && !hasHostGH {
				if opts.BaseURL == "" {
					if apiURL := os.Getenv("GITLAB_API_URL"); apiURL != "" {
						opts.BaseURL = apiURL
					} else {
						opts.BaseURL = "https://gitlab.com/api/v4"
					}
				}
				if opts.Token == "" {
					opts.Token = os.Getenv("GITLAB_TOKEN")
				}
				if !opts.Repo.Valid() {
					opts.Repo = repo
				}
				return ForgeTypeGitLab, opts, nil
			}
		}
	}

	if opts.Repo.Host != "" {
		host := strings.ToLower(opts.Repo.Host)
		hasHostGH := strings.Contains(host, "github")
		hasHostGL := strings.Contains(host, "gitlab")
		if hasHostGH && !hasHostGL {
			if opts.BaseURL == "" {
				if apiURL := os.Getenv("GITHUB_API_URL"); apiURL != "" {
					opts.BaseURL = apiURL
				} else {
					opts.BaseURL = "https://api.github.com"
				}
			}
			if opts.Token == "" {
				if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
					opts.Token = tok
				} else {
					opts.Token = os.Getenv("GH_TOKEN")
				}
			}
			return ForgeTypeGitHub, opts, nil
		}
		if hasHostGL && !hasHostGH {
			if opts.BaseURL == "" {
				if apiURL := os.Getenv("GITLAB_API_URL"); apiURL != "" {
					opts.BaseURL = apiURL
				} else {
					opts.BaseURL = "https://gitlab.com/api/v4"
				}
			}
			if opts.Token == "" {
				opts.Token = os.Getenv("GITLAB_TOKEN")
			}
			return ForgeTypeGitLab, opts, nil
		}
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
			if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
				opts.Token = tok
			} else {
				opts.Token = os.Getenv("GH_TOKEN")
			}
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
			opts.Token = os.Getenv("GITLAB_TOKEN")
		}
		return ForgeTypeGitLab, opts, nil
	}

	// Environment variables are either both present or both absent.
	// Inspect git origin remote in current directory.
	repo, ok := DetectRepo(".")
	if !ok {
		return ForgeTypeUnknown, opts, fmt.Errorf("%w: unable to detect forge from environment or git origin remote", ErrAmbiguousForge)
	}

	host := strings.ToLower(repo.Host)
	hasHostGH := strings.Contains(host, "github")
	hasHostGL := strings.Contains(host, "gitlab")

	if !hasHostGH && !hasHostGL {
		if ghAPI := os.Getenv("GITHUB_API_URL"); ghAPI != "" {
			if u, err := url.Parse(ghAPI); err == nil && u.Hostname() != "" {
				ghHost := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
				if ghHost == host || strings.TrimPrefix(ghHost, "api.") == host {
					hasHostGH = true
				}
			}
		}
		if glAPI := os.Getenv("GITLAB_API_URL"); glAPI != "" {
			if u, err := url.Parse(glAPI); err == nil && u.Hostname() != "" {
				glHost := strings.ToLower(strings.TrimPrefix(u.Hostname(), "www."))
				if glHost == host || strings.TrimPrefix(glHost, "api.") == host {
					hasHostGL = true
				}
			}
		}
	}

	if hasHostGH && !hasHostGL {
		if opts.BaseURL == "" {
			if apiURL := os.Getenv("GITHUB_API_URL"); apiURL != "" {
				opts.BaseURL = apiURL
			} else {
				opts.BaseURL = "https://api.github.com"
			}
		}
		if opts.Token == "" {
			if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
				opts.Token = tok
			} else {
				opts.Token = os.Getenv("GH_TOKEN")
			}
		}
		return ForgeTypeGitHub, opts, nil
	}

	if hasHostGL && !hasHostGH {
		if opts.BaseURL == "" {
			if apiURL := os.Getenv("GITLAB_API_URL"); apiURL != "" {
				opts.BaseURL = apiURL
			} else {
				opts.BaseURL = "https://gitlab.com/api/v4"
			}
		}
		if opts.Token == "" {
			opts.Token = os.Getenv("GITLAB_TOKEN")
		}
		return ForgeTypeGitLab, opts, nil
	}

	return ForgeTypeUnknown, opts, fmt.Errorf("%w: ambiguous forge from git remote host %q", ErrAmbiguousForge, repo.Host)
}
