package issuex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
	if token == "" {
		if envTok := os.Getenv("GITHUB_TOKEN"); envTok != "" {
			token = envTok
		} else if envTok := os.Getenv("GH_TOKEN"); envTok != "" {
			token = envTok
		}
	}

	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	} else if hc.Timeout == 0 {
		clientCopy := *hc
		clientCopy.Timeout = 30 * time.Second
		hc = &clientCopy
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
		sleep:      time.Sleep,
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
// If fn is nil, time.Sleep is used.
func (c *githubClient) SetSleep(fn func(time.Duration)) {
	if fn == nil {
		c.sleep = time.Sleep
	} else {
		c.sleep = fn
	}
}

// SetGitHubSleep sets the rate-limit sleep function on a GitHub Client.
// It is intended for deterministic testing of rate-limit backoff without delays.
func SetGitHubSleep(c Client, fn func(time.Duration)) {
	if gc, ok := c.(*githubClient); ok {
		gc.SetSleep(fn)
	}
}

func (c *githubClient) do(ctx context.Context, method, path string, reqBody, respTarget any) error {
	var bodyBytes []byte
	if reqBody != nil {
		switch v := reqBody.(type) {
		case []byte:
			if len(v) > 0 {
				bodyBytes = v
			}
		case string:
			if len(v) > 0 {
				bodyBytes = []byte(v)
			}
		case io.Reader:
			b, err := io.ReadAll(v)
			if err != nil {
				return fmt.Errorf("%s %s: reading request body: %w", method, path, err)
			}
			if len(b) > 0 {
				bodyBytes = b
			}
		default:
			b, err := json.Marshal(v)
			if err != nil {
				return fmt.Errorf("%s %s: encoding request body: %w", method, path, err)
			}
			bodyBytes = b
		}
	}

	fullURL := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		p := path
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		fullURL = strings.TrimRight(c.baseURL, "/") + p
	}

	for attempt := 0; attempt < 2; attempt++ {
		var bodyReader io.Reader
		if len(bodyBytes) > 0 {
			bodyReader = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
		if err != nil {
			return fmt.Errorf("%s %s: %w", method, path, err)
		}

		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", c.userAgent)
		if c.Authenticated() {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		if len(bodyBytes) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("%s %s: %w", method, path, err)
		}

		raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBodyBytes))
		_ = resp.Body.Close()
		if err != nil {
			return fmt.Errorf("%s %s: reading response: %w", method, path, err)
		}

		// 2xx response
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if respTarget != nil {
				if w, ok := respTarget.(io.Writer); ok {
					if _, err := w.Write(raw); err != nil {
						return fmt.Errorf("%s %s: writing response: %w", method, path, err)
					}
				} else {
					if err := json.Unmarshal(raw, respTarget); err != nil {
						return fmt.Errorf("%s %s: decoding response: %w", method, path, err)
					}
				}
			}
			return nil
		}

		httpErr := newGitHubHTTPError(method, path, resp, raw)

		// Check rate limiting
		wait, isRateLimit := httpErr.RetryAfter()
		if isRateLimit {
			if attempt == 0 && wait <= 120*time.Second {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				c.sleep(wait)
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				continue
			}
			return fmt.Errorf("%w: %w", ErrRateLimited, httpErr)
		}

		// Map status codes to sentinels
		if resp.StatusCode == http.StatusNotFound {
			if !c.Authenticated() {
				return fmt.Errorf("%w: resource may be private and require credentials: %w", ErrNotFound, httpErr)
			}
			return fmt.Errorf("%w: %w", ErrNotFound, httpErr)
		}
		if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusMethodNotAllowed {
			return fmt.Errorf("%w: %w", ErrConflict, httpErr)
		}

		return httpErr
	}

	return nil
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
