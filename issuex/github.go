package issuex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

var _ Client = (*githubClient)(nil)

type githubClient struct {
	baseURL          string
	token            string
	userAgent        string
	httpClient       *http.Client
	repo             Repo
	sleep            func(time.Duration)
	allowMergeCommit bool
	allowSquashMerge bool
	allowRebaseMerge bool
}

// NewGitHub constructs a GitHub client adapter directly from Options.
func NewGitHub(o Options) (Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(o.BaseURL), "/")
	if baseURL == "" {
		if envURL := os.Getenv("GITHUB_API_URL"); envURL != "" {
			baseURL = strings.TrimRight(strings.TrimSpace(envURL), "/")
		} else {
			baseURL = "https://api.github.com"
		}
	}

	token := strings.TrimSpace(o.Token)
	if token == "" && !o.withholdEnvToken {
		if envTok := os.Getenv("GITHUB_TOKEN"); envTok != "" {
			token = envTok
		} else if envTok := os.Getenv("GH_TOKEN"); envTok != "" {
			token = envTok
		}
	}

	// A supplied client is used as is: its timeout, or the lack of one, is
	// the caller's choice. The default is for a caller that supplies none.
	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}

	ua := strings.TrimSpace(o.UserAgent)
	if ua == "" {
		ua = "agent-fox"
	}

	return &githubClient{
		baseURL:    baseURL,
		token:      token,
		userAgent:  ua,
		httpClient: hc,
		repo:       o.Repo,
	}, nil
}

func (c *githubClient) Authenticated() bool {
	return c != nil && c.token != ""
}

func (c *githubClient) Close() error {
	if c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
	return nil
}

// SetSleep sets the rate-limit backoff sleep function on the client.
// If fn is nil, the default wait is used, which ends when the run is cancelled.
func (c *githubClient) SetSleep(fn func(time.Duration)) {
	// nil restores the default: a wait that ends when the run is cancelled.
	c.sleep = fn
}

// SetGitHubSleep sets the rate-limit sleep function on a GitHub Client.
// It is intended for deterministic testing of rate-limit backoff without delays.
func SetGitHubSleep(c Client, fn func(time.Duration)) {
	if gc, ok := c.(*githubClient); ok {
		gc.SetSleep(fn)
	}
}

func (c *githubClient) do(ctx context.Context, method, path string, reqBody, respTarget any) error {
	_, err := c.doWithResponse(ctx, method, path, reqBody, respTarget)
	return err
}

// doWithResponse is do, also returning the response of the successful
// request, whose body is already read, for its headers.
func (c *githubClient) doWithResponse(ctx context.Context, method, path string, reqBody, respTarget any) (*http.Response, error) {
	resp, _, err := c.forge().do(ctx, method, path, reqBody, respTarget)
	return resp, err
}

// forge describes GitHub to the shared request executor.
func (c *githubClient) forge() forgeHTTP {
	return forgeHTTP{
		baseURL:       c.baseURL,
		client:        c.httpClient,
		sleep:         c.sleep,
		authenticated: c.Authenticated(),
		header: func(req *http.Request) {
			req.Header.Set("Accept", "application/vnd.github+json")
			req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
			req.Header.Set("User-Agent", c.userAgent)
			if c.Authenticated() {
				req.Header.Set("Authorization", "Bearer "+c.token)
			}
		},
		newError: newGitHubHTTPError,
	}
}

// hasNextPage reports whether a response's Link header offers a rel="next"
// page, which GitHub omits on the last page.
func hasNextPage(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	for _, header := range resp.Header.Values("Link") {
		for _, link := range strings.Split(header, ",") {
			_, params, _ := strings.Cut(link, ";")
			for _, param := range strings.Split(params, ";") {
				name, value, _ := strings.Cut(strings.TrimSpace(param), "=")
				if strings.EqualFold(name, "rel") && slices.Contains(strings.Fields(strings.Trim(value, `"`)), "next") {
					return true
				}
			}
		}
	}
	return false
}

func parseGitHubError(status int, method, path string, body []byte) *HTTPError {
	msg := parseErrorMessage(body, http.StatusText(status))
	return &HTTPError{
		Method:  method,
		Path:    path,
		Status:  status,
		Message: msg,
	}
}

func newGitHubHTTPError(method, path string, resp *http.Response, body []byte) *HTTPError {
	httpErr := parseGitHubError(resp.StatusCode, method, path, body)
	httpErr.RetryAfterHeader = getHeader(resp.Header, "Retry-After")
	httpErr.RateLimitReset = getHeader(resp.Header, "X-RateLimit-Reset")
	httpErr.RateLimitRemaining = getHeader(resp.Header, "X-RateLimit-Remaining")
	return httpErr
}

func parseErrorMessage(body []byte, defaultMsg string) string {
	var payload struct {
		Message string `json:"message"`
		Errors  []struct {
			Resource string `json:"resource"`
			Field    string `json:"field"`
			Code     string `json:"code"`
			Message  string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || strings.TrimSpace(payload.Message) == "" {
		if defaultMsg != "" {
			return defaultMsg
		}
		return strings.TrimSpace(string(body))
	}

	msg := strings.TrimSpace(payload.Message)
	for _, e := range payload.Errors {
		var detail string
		if e.Field != "" && e.Code != "" {
			if e.Resource != "" {
				detail = e.Resource + "." + e.Field + "." + e.Code
			} else {
				detail = e.Field + "." + e.Code
			}
			if e.Message != "" {
				detail += ": " + e.Message
			}
		} else if e.Field != "" {
			detail = e.Field
			if e.Message != "" {
				detail += ": " + e.Message
			}
		} else if e.Code != "" {
			detail = e.Code
			if e.Message != "" {
				detail += ": " + e.Message
			}
		} else if e.Message != "" {
			detail = e.Message
		}
		if detail != "" {
			msg += " (" + detail + ")"
		}
	}
	return msg
}

// Repository operations

func (c *githubClient) GetRepository(ctx context.Context, repo Repo) (Repository, error) {
	if !repo.Valid() && c.repo.Valid() {
		repo = c.repo
	}
	var out Repository
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s", repo.Owner, repo.Name), nil, &out)
	if err != nil {
		return Repository{}, err
	}
	c.allowMergeCommit = out.AllowMergeCommit
	c.allowSquashMerge = out.AllowSquashMerge
	c.allowRebaseMerge = out.AllowRebaseMerge
	return out, nil
}
