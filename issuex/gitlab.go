package issuex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
	if token == "" && !o.withholdEnvToken {
		token = os.Getenv("GITLAB_TOKEN")
	}

	// A supplied client is used as is: its timeout, or the lack of one, is
	// the caller's choice. The default is for a caller that supplies none.
	//
	// The default client does not carry PRIVATE-TOKEN across a redirect to
	// another host: it is a header of GitLab's own, which Go does not strip
	// there the way it strips Authorization. A supplied client's redirect
	// policy is the caller's.
	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second, CheckRedirect: dropTokenAcrossHosts}
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
// If fn is nil, the default wait is used, which ends when the run is cancelled.
func (c *gitlabClient) SetSleep(fn func(time.Duration)) {
	// nil restores the default: a wait that ends when the run is cancelled.
	c.sleep = fn
}

// SetGitLabSleep sets the rate-limit sleep function on a GitLab Client.
// It is intended for deterministic testing of rate-limit backoff without delays.
func SetGitLabSleep(c Client, fn func(time.Duration)) {
	if gc, ok := c.(*gitlabClient); ok {
		gc.SetSleep(fn)
	}
}

func (c *gitlabClient) do(ctx context.Context, method, path string, reqBody, respTarget any) ([]byte, error) {
	_, body, err := c.doWithResponse(ctx, method, path, reqBody, respTarget)
	return body, err
}

func (c *gitlabClient) doWithResponse(ctx context.Context, method, path string, reqBody, respTarget any) (*http.Response, []byte, error) {
	return c.forge().do(ctx, method, path, reqBody, respTarget)
}

// forge describes GitLab to the shared request executor. GitLab reports a
// merge request that cannot be accepted as 406 as well as 405 and 409.
func (c *gitlabClient) forge() forgeHTTP {
	return forgeHTTP{
		baseURL:       c.baseURL,
		client:        c.httpClient,
		sleep:         c.sleep,
		authenticated: c.Authenticated(),
		header: func(req *http.Request) {
			req.Header.Set("Accept", "application/json")
			req.Header.Set("User-Agent", c.userAgent)
			if c.Authenticated() {
				req.Header.Set("PRIVATE-TOKEN", c.token)
			}
		},
		newError:  newGitLabHTTPError,
		conflicts: []int{http.StatusNotAcceptable},
	}
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

// projectPath formats a Repo into a URL-encoded project path with slashes replaced by %2F.
func projectPath(repo Repo) string {
	escaped := url.PathEscape(repo.String())
	return strings.ReplaceAll(escaped, "/", "%2F")
}

type gitlabProjectPermissions struct {
	ProjectAccess *gitlabAccessLevel `json:"project_access"`
	GroupAccess   *gitlabAccessLevel `json:"group_access"`
}

type gitlabAccessLevel struct {
	AccessLevel int `json:"access_level"`
}

type gitlabProjectResponse struct {
	PathWithNamespace string                    `json:"path_with_namespace"`
	DefaultBranch     string                    `json:"default_branch"`
	Visibility        string                    `json:"visibility"`
	Archived          bool                      `json:"archived"`
	Permissions       *gitlabProjectPermissions `json:"permissions"`
	MergeMethod       string                    `json:"merge_method"`
	SquashOption      string                    `json:"squash_option"`
}

func (c *gitlabClient) GetRepository(ctx context.Context, repo Repo) (Repository, error) {
	if !repo.Valid() && c.repo.Valid() {
		repo = c.repo
	}

	path := fmt.Sprintf("/projects/%s", projectPath(repo))
	var project gitlabProjectResponse
	_, err := c.do(ctx, http.MethodGet, path, nil, &project)
	if err != nil {
		if IsNotFound(err) && !c.Authenticated() {
			return Repository{}, fmt.Errorf("%w: repository may be private and require setting GITLAB_TOKEN: %w", ErrNotFound, err)
		}
		return Repository{}, err
	}

	isPrivate := project.Visibility == "private" || project.Visibility == "internal"

	var effectiveLevel int
	if project.Permissions != nil {
		if project.Permissions.ProjectAccess != nil && project.Permissions.ProjectAccess.AccessLevel > effectiveLevel {
			effectiveLevel = project.Permissions.ProjectAccess.AccessLevel
		}
		if project.Permissions.GroupAccess != nil && project.Permissions.GroupAccess.AccessLevel > effectiveLevel {
			effectiveLevel = project.Permissions.GroupAccess.AccessLevel
		}
	}

	pull := effectiveLevel >= 20 || !isPrivate
	push := effectiveLevel >= 30
	admin := effectiveLevel >= 40

	c.mergeMethod = project.MergeMethod
	c.squashOption = project.SquashOption

	return Repository{
		FullName:      project.PathWithNamespace,
		DefaultBranch: project.DefaultBranch,
		Private:       isPrivate,
		Archived:      project.Archived,
		Permissions: RepoPermissions{
			Push:  push,
			Pull:  pull,
			Admin: admin,
		},
		AllowMergeCommit: project.MergeMethod == "" || project.MergeMethod == "merge",
		AllowSquashMerge: project.SquashOption != "never",
		AllowRebaseMerge: project.MergeMethod == "rebase_merge" || project.MergeMethod == "ff",
	}, nil
}

// dropTokenAcrossHosts is the default GitLab client's redirect policy: Go's
// own, plus PRIVATE-TOKEN removed when the redirect leaves the host the
// request was sent to.
func dropTokenAcrossHosts(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if len(via) > 0 && req.URL.Host != via[0].URL.Host {
		req.Header.Del("PRIVATE-TOKEN")
	}
	return nil
}
