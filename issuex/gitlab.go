package issuex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

var _ Client = (*gitlabClient)(nil)

type gitlabClient struct {
	baseURL      string
	token        string
	userAgent    string
	httpClient   *http.Client
	repo         Repo
	sleep        func(time.Duration)
	mergeMethod  string
	squashOption string
}

// NewGitLab constructs a GitLab client adapter directly from Options.
func NewGitLab(o Options) (Client, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(o.BaseURL), "/")
	if baseURL == "" {
		if envURL := os.Getenv("GITLAB_API_URL"); envURL != "" {
			baseURL = strings.TrimRight(strings.TrimSpace(envURL), "/")
		} else {
			baseURL = "https://gitlab.com/api/v4"
		}
	}
	if !strings.HasSuffix(baseURL, "/api/v4") {
		baseURL += "/api/v4"
	}

	token := strings.TrimSpace(o.Token)
	if token == "" {
		token = os.Getenv("GITLAB_TOKEN")
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

	return &gitlabClient{
		baseURL:    baseURL,
		token:      token,
		userAgent:  ua,
		httpClient: hc,
		repo:       o.Repo,
		sleep:      time.Sleep,
	}, nil
}

func (c *gitlabClient) Authenticated() bool {
	return c != nil && c.token != ""
}

func (c *gitlabClient) Close() error {
	if c != nil && c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
	return nil
}

// SetSleep sets the rate-limit backoff sleep function on the client.
// If fn is nil, time.Sleep is used.
func (c *gitlabClient) SetSleep(fn func(time.Duration)) {
	if fn == nil {
		c.sleep = time.Sleep
	} else {
		c.sleep = fn
	}
}

// SetGitLabSleep sets the rate-limit sleep function on a GitLab Client.
// It is intended for deterministic testing of rate-limit backoff without delays.
func SetGitLabSleep(c Client, fn func(time.Duration)) {
	if gc, ok := c.(*gitlabClient); ok {
		gc.SetSleep(fn)
	}
}

func (c *gitlabClient) do(ctx context.Context, method, path string, reqBody, respTarget any) ([]byte, error) {
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
				return nil, fmt.Errorf("%s %s: reading request body: %w", method, path, err)
			}
			if len(b) > 0 {
				bodyBytes = b
			}
		default:
			b, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("%s %s: encoding request body: %w", method, path, err)
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
			return nil, fmt.Errorf("%s %s: %w", method, path, err)
		}

		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", c.userAgent)
		if c.Authenticated() {
			req.Header.Set("PRIVATE-TOKEN", c.token)
		}
		if len(bodyBytes) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", method, path, err)
		}

		raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBodyBytes))
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("%s %s: reading response: %w", method, path, err)
		}

		// 2xx response
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if respTarget != nil {
				if w, ok := respTarget.(io.Writer); ok {
					if _, err := w.Write(raw); err != nil {
						return nil, fmt.Errorf("%s %s: writing response: %w", method, path, err)
					}
				} else {
					if err := json.Unmarshal(raw, respTarget); err != nil {
						return nil, fmt.Errorf("%s %s: decoding response: %w", method, path, err)
					}
				}
			}
			return raw, nil
		}

		httpErr := newGitLabHTTPError(method, path, resp, raw)

		// Check rate limiting (429)
		wait, isRateLimit := httpErr.RetryAfter()
		if isRateLimit {
			if attempt == 0 && wait <= 120*time.Second {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
				}
				c.sleep(wait)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				default:
				}
				continue
			}
			return nil, fmt.Errorf("%w: %w", ErrRateLimited, httpErr)
		}

		// Map status codes to sentinels
		if resp.StatusCode == http.StatusNotFound {
			if !c.Authenticated() {
				return nil, fmt.Errorf("%w: resource may be private and require credentials: %w", ErrNotFound, httpErr)
			}
			return nil, fmt.Errorf("%w: %w", ErrNotFound, httpErr)
		}
		if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotAcceptable || resp.StatusCode == http.StatusConflict {
			return nil, fmt.Errorf("%w: %w", ErrConflict, httpErr)
		}

		return nil, httpErr
	}

	return nil, nil
}

func newGitLabHTTPError(method, path string, resp *http.Response, body []byte) *HTTPError {
	msg := parseGitLabErrorMessage(body, http.StatusText(resp.StatusCode))
	rateLimitReset := getHeader(resp.Header, "RateLimit-Reset")
	if rateLimitReset == "" {
		rateLimitReset = getHeader(resp.Header, "X-RateLimit-Reset")
	}
	rateLimitRemaining := getHeader(resp.Header, "RateLimit-Remaining")
	if rateLimitRemaining == "" {
		rateLimitRemaining = getHeader(resp.Header, "X-RateLimit-Remaining")
	}
	return &HTTPError{
		Method:             method,
		Path:               path,
		Status:             resp.StatusCode,
		Message:            msg,
		RetryAfterHeader:   getHeader(resp.Header, "Retry-After"),
		RateLimitReset:     rateLimitReset,
		RateLimitRemaining: rateLimitRemaining,
	}
}

func parseGitLabErrorMessage(body []byte, defaultMsg string) string {
	if len(bytes.TrimSpace(body)) == 0 {
		return defaultMsg
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		if s := strings.TrimSpace(string(body)); s != "" {
			return s
		}
		return defaultMsg
	}

	// Check error and error_description fields
	errVal, hasErr := raw["error"]
	errDescVal, hasErrDesc := raw["error_description"]
	if hasErr || hasErrDesc {
		errStr, _ := errVal.(string)
		errDescStr, _ := errDescVal.(string)
		if errStr != "" && errDescStr != "" {
			return fmt.Sprintf("%s: %s", errStr, errDescStr)
		}
		if errStr != "" {
			return errStr
		}
		if errDescStr != "" {
			return errDescStr
		}
	}

	// Check message field: string or validation object {"field": ["error1", ...]}
	if msgVal, ok := raw["message"]; ok {
		switch m := msgVal.(type) {
		case string:
			if s := strings.TrimSpace(m); s != "" {
				return s
			}
		case map[string]any:
			var parts []string
			var keys []string
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				v := m[k]
				switch val := v.(type) {
				case []any:
					var strVals []string
					for _, item := range val {
						if s, ok := item.(string); ok {
							strVals = append(strVals, s)
						} else {
							strVals = append(strVals, fmt.Sprint(item))
						}
					}
					parts = append(parts, fmt.Sprintf("%s: %s", k, strings.Join(strVals, ", ")))
				case string:
					parts = append(parts, fmt.Sprintf("%s: %s", k, val))
				default:
					parts = append(parts, fmt.Sprintf("%s: %v", k, val))
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, "; ")
			}
		}
	}

	if defaultMsg != "" {
		return defaultMsg
	}
	return strings.TrimSpace(string(body))
}

// Client interface method stubs (implemented in subsequent tasks)

func (c *gitlabClient) GetRepository(ctx context.Context, repo Repo) (Repository, error) {
	panic("not implemented")
}

func (c *gitlabClient) CreateIssue(ctx context.Context, repo Repo, req CreateIssueRequest) (Issue, error) {
	panic("not implemented")
}

func (c *gitlabClient) ReadIssue(ctx context.Context, ref IssueRef) (IssueThread, error) {
	panic("not implemented")
}

func (c *gitlabClient) UpdateIssue(ctx context.Context, ref IssueRef, req UpdateIssueRequest) (Issue, error) {
	panic("not implemented")
}

func (c *gitlabClient) CloseIssue(ctx context.Context, ref IssueRef, comment string) error {
	panic("not implemented")
}

func (c *gitlabClient) ListIssues(ctx context.Context, repo Repo, filter IssueFilter) (IssueList, error) {
	panic("not implemented")
}

func (c *gitlabClient) AddComment(ctx context.Context, ref IssueRef, body string) (string, error) {
	panic("not implemented")
}

func (c *gitlabClient) ListComments(ctx context.Context, ref IssueRef) (CommentList, error) {
	panic("not implemented")
}

func (c *gitlabClient) AddLabels(ctx context.Context, ref IssueRef, labels []string) error {
	panic("not implemented")
}

func (c *gitlabClient) RemoveLabel(ctx context.Context, ref IssueRef, label string) error {
	panic("not implemented")
}

func (c *gitlabClient) CreateLabel(ctx context.Context, repo Repo, label Label) error {
	panic("not implemented")
}

func (c *gitlabClient) CreatePullRequest(ctx context.Context, repo Repo, req CreatePullRequestRequest) (PullRequest, error) {
	panic("not implemented")
}

func (c *gitlabClient) ReadPullRequest(ctx context.Context, ref IssueRef) (PullRequest, error) {
	panic("not implemented")
}

func (c *gitlabClient) ReadChangedFiles(ctx context.Context, ref IssueRef) ([]ChangedFile, error) {
	panic("not implemented")
}

func (c *gitlabClient) GetPRState(ctx context.Context, ref IssueRef) (PRState, error) {
	panic("not implemented")
}

func (c *gitlabClient) GetCIChecks(ctx context.Context, ref IssueRef) ([]CheckRun, error) {
	panic("not implemented")
}

func (c *gitlabClient) GetPRReviews(ctx context.Context, ref IssueRef) ([]Review, error) {
	panic("not implemented")
}

func (c *gitlabClient) PostReviewComment(ctx context.Context, ref IssueRef, body string) error {
	panic("not implemented")
}

func (c *gitlabClient) MergePullRequest(ctx context.Context, ref IssueRef, opts MergeOptions) (MergeResult, error) {
	panic("not implemented")
}

func (c *gitlabClient) ClosePullRequest(ctx context.Context, ref IssueRef) error {
	panic("not implemented")
}
