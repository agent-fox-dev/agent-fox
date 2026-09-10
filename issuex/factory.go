package issuex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func init() {
	RegisterAdapter(ForgeTypeGitHub, func(o Options) (Client, error) {
		return NewGitHub(o)
	})
	RegisterAdapter(ForgeTypeGitLab, func(o Options) (Client, error) {
		return NewGitLab(o)
	})
}

// NewWithOptions instantiates a Client based on the provided Options.
func NewWithOptions(o Options) (Client, error) {
	if o.NoOp {
		return NewNoOp(), nil
	}

	if o.UserAgent == "" {
		o.UserAgent = "agent-fox"
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	} else if o.HTTPClient.Timeout == 0 {
		clientCopy := *o.HTTPClient
		clientCopy.Timeout = 30 * time.Second
		o.HTTPClient = &clientCopy
	}

	forgeType, resolvedOpts, err := detectForge(o)
	if err != nil {
		if errors.Is(err, ErrAmbiguousForge) {
			if client, probeErr := probeGitLabHost(o); probeErr == nil {
				return client, nil
			}
		}
		return nil, err
	}

	if forgeType == ForgeTypeGitHub {
		return NewGitHub(resolvedOpts)
	}
	if forgeType == ForgeTypeGitLab {
		return NewGitLab(resolvedOpts)
	}

	adaptersMu.RLock()
	factory, ok := adapters[forgeType]
	adaptersMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s provider adapter is not registered", ErrUnsupportedForge, forgeType)
	}

	return factory(resolvedOpts)
}

func probeGitLabHost(o Options) (Client, error) {
	var probeURL string
	var apiBaseURL string

	if o.BaseURL != "" {
		uStr := strings.TrimRight(strings.TrimSpace(o.BaseURL), "/")
		lower := strings.ToLower(uStr)
		if strings.Contains(lower, "github") || strings.Contains(lower, "gitlab") {
			return nil, errors.New("not an ambiguous host")
		}
		if strings.HasSuffix(uStr, "/api/v4") {
			probeURL = uStr + "/version"
			apiBaseURL = uStr
		} else {
			probeURL = uStr + "/api/v4/version"
			apiBaseURL = uStr + "/api/v4"
		}
	} else if o.RemoteURL != "" {
		s := strings.TrimSpace(o.RemoteURL)
		if strings.Contains(s, "://") {
			u, err := url.Parse(s)
			if err != nil || u.Host == "" {
				return nil, errors.New("cannot parse remote URL")
			}
			lowerHost := strings.ToLower(u.Host)
			if strings.Contains(lowerHost, "github") || strings.Contains(lowerHost, "gitlab") {
				return nil, errors.New("not an ambiguous host")
			}
			scheme := u.Scheme
			if scheme != "http" && scheme != "https" {
				scheme = "https"
			}
			apiBaseURL = fmt.Sprintf("%s://%s/api/v4", scheme, u.Host)
			probeURL = apiBaseURL + "/version"
		} else if strings.Contains(s, ":") {
			userHost, _, found := strings.Cut(s, ":")
			if !found {
				return nil, errors.New("cannot parse SCP remote URL")
			}
			if at := strings.Index(userHost, "@"); at >= 0 {
				userHost = userHost[at+1:]
			}
			lowerHost := strings.ToLower(userHost)
			if strings.Contains(lowerHost, "github") || strings.Contains(lowerHost, "gitlab") {
				return nil, errors.New("not an ambiguous host")
			}
			apiBaseURL = fmt.Sprintf("https://%s/api/v4", userHost)
			probeURL = apiBaseURL + "/version"
		}
	} else if o.Repo.Host != "" {
		lowerHost := strings.ToLower(o.Repo.Host)
		if strings.Contains(lowerHost, "github") || strings.Contains(lowerHost, "gitlab") {
			return nil, errors.New("not an ambiguous host")
		}
		apiBaseURL = fmt.Sprintf("https://%s/api/v4", o.Repo.Host)
		probeURL = apiBaseURL + "/version"
	}

	if probeURL == "" {
		return nil, errors.New("no host to probe")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if o.UserAgent != "" {
		req.Header.Set("User-Agent", o.UserAgent)
	} else {
		req.Header.Set("User-Agent", "agent-fox")
	}
	token := strings.TrimSpace(o.Token)
	if token == "" {
		token = os.Getenv("GITLAB_TOKEN")
	}
	if token != "" {
		req.Header.Set("PRIVATE-TOKEN", token)
	}

	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 2 * time.Second}
	}

	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("probe status %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}

	var payload struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || strings.TrimSpace(payload.Version) == "" {
		return nil, fmt.Errorf("missing version in probe response")
	}

	optsCopy := o
	optsCopy.BaseURL = apiBaseURL
	return NewGitLab(optsCopy)
}
