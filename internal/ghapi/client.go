package ghapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the public GitHub REST endpoint.
const DefaultBaseURL = "https://api.github.com"

// maxResponseBytes bounds one response body. An issue thread with a
// megabyte-long comment is a real thing, and reading it into memory
// unbounded because a server said so is not.
const maxResponseBytes = 8 << 20

// commentPageLimit bounds how many pages of comments are read. A thread with
// more than this is not going to be read usefully by a model either way, and
// the truncation is reported rather than silent.
const commentPageLimit = 5

// Client is the GitHub REST client.
//
// The zero value is not usable; build one with New or NewWithOptions. It is
// safe for concurrent use.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
	agent   string
	// now and sleep are injected so the rate-limit backoff is testable
	// without waiting.
	sleep func(time.Duration)
}

// Options configures a Client.
type Options struct {
	// BaseURL is the REST root. Empty means GITHUB_API_URL, else
	// DefaultBaseURL.
	BaseURL string
	// Token authenticates the calls. Empty means GITHUB_TOKEN, else
	// GH_TOKEN. Reading a public issue needs no token; every write does.
	Token string
	// HTTPClient overrides the transport. Empty means a client with a
	// 30-second timeout.
	HTTPClient *http.Client
	// UserAgent identifies the tool making the call. GitHub requires one.
	UserAgent string
}

// New returns a Client configured from the environment.
func New(userAgent string) *Client { return NewWithOptions(Options{UserAgent: userAgent}) }

// NewWithOptions returns a Client, filling unset fields from the environment.
func NewWithOptions(o Options) *Client {
	base := o.BaseURL
	if base == "" {
		base = envOr("GITHUB_API_URL", DefaultBaseURL)
	}
	token := o.Token
	if token == "" {
		token = envOr("GITHUB_TOKEN", os.Getenv("GH_TOKEN"))
	}
	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	ua := o.UserAgent
	if ua == "" {
		ua = "agent-fox"
	}
	return &Client{
		baseURL: strings.TrimSuffix(base, "/"),
		token:   token,
		http:    hc,
		agent:   ua,
		sleep:   time.Sleep,
	}
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// Authenticated reports whether a token was found. A tool that will write
// checks this before it spends money on a model run, so the failure arrives
// in the first second rather than the tenth minute.
func (c *Client) Authenticated() bool { return c != nil && c.token != "" }

// ErrNoToken is returned by every write when no credential was found.
var ErrNoToken = errors.New("no GitHub token: set GITHUB_TOKEN or GH_TOKEN")

// User is the subset of a GitHub account this package reads.
type User struct {
	Login string `json:"login"`
}

// Label is a GitHub issue label.
type Label struct {
	Name string `json:"name"`
}

// Issue is the subset of an issue or pull request this package reads.
type Issue struct {
	Number      int     `json:"number"`
	Title       string  `json:"title"`
	Body        string  `json:"body"`
	State       string  `json:"state"`
	HTMLURL     string  `json:"html_url"`
	User        User    `json:"user"`
	Labels      []Label `json:"labels"`
	PullRequest *struct {
		HTMLURL string `json:"html_url"`
	} `json:"pull_request,omitempty"`
}

// IsPullRequest reports whether the issues endpoint returned a pull request.
func (i Issue) IsPullRequest() bool { return i.PullRequest != nil }

// LabelNames flattens the labels.
func (i Issue) LabelNames() []string {
	out := make([]string, 0, len(i.Labels))
	for _, l := range i.Labels {
		if l.Name != "" {
			out = append(out, l.Name)
		}
	}
	return out
}

// Comment is one issue comment.
type Comment struct {
	Body      string    `json:"body"`
	User      User      `json:"user"`
	CreatedAt time.Time `json:"created_at"`
	HTMLURL   string    `json:"html_url"`
}

// Thread is an issue together with its comments.
type Thread struct {
	Issue    Issue
	Comments []Comment
	// Truncated reports that the comment list was cut at commentPageLimit
	// pages, so the thread the caller holds is not the whole thread.
	Truncated bool
	// CommentsErr records a failure to read the comments. An issue whose
	// comments could not be read is still a usable report, so this is
	// reported rather than returned.
	CommentsErr error
}

// PullRequest is the subset of a pull request this package reads and writes.
type PullRequest struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
	Draft   bool   `json:"draft"`
	Head    struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

// ChangedFile is one entry of a pull request's file list.
type ChangedFile struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// Repository is the subset of a repository this package reads.
type Repository struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	Archived      bool   `json:"archived"`
	Permissions   struct {
		Push  bool `json:"push"`
		Pull  bool `json:"pull"`
		Admin bool `json:"admin"`
	} `json:"permissions"`
}

// GetRepository reads a repository.
func (c *Client) GetRepository(ctx context.Context, r Repo) (Repository, error) {
	var out Repository
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s", r.Owner, r.Name), nil, &out)
	return out, err
}

// ReadIssue fetches an issue and its comments.
//
// Reading a public issue needs no token; a private one needs `repo` scope,
// and the 404 GitHub returns in that case is indistinguishable from a
// genuinely missing issue — so the error names both possibilities rather than
// guessing at one.
func (c *Client) ReadIssue(ctx context.Context, ref IssueRef) (Thread, error) {
	var t Thread
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", ref.Repo.Owner, ref.Repo.Name, ref.Number)
	if err := c.do(ctx, http.MethodGet, path, nil, &t.Issue); err != nil {
		var he *HTTPError
		if errors.As(err, &he) && he.Status == http.StatusNotFound && !c.Authenticated() {
			return t, fmt.Errorf("%w (the issue may be private: set GITHUB_TOKEN)", err)
		}
		return t, err
	}
	t.Comments, t.Truncated, t.CommentsErr = c.readComments(ctx, path)
	return t, nil
}

func (c *Client) readComments(ctx context.Context, issuePath string) ([]Comment, bool, error) {
	var all []Comment
	for page := 1; page <= commentPageLimit; page++ {
		var batch []Comment
		p := fmt.Sprintf("%s/comments?per_page=100&page=%d", issuePath, page)
		if err := c.do(ctx, http.MethodGet, p, nil, &batch); err != nil {
			return all, false, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			return all, false, nil
		}
	}
	return all, true, nil
}

// CreateIssue files an issue and returns it.
func (c *Client) CreateIssue(ctx context.Context, r Repo, title, body string, labels []string) (Issue, error) {
	if !c.Authenticated() {
		return Issue{}, fmt.Errorf("creating an issue in %s: %w", r, ErrNoToken)
	}
	payload := map[string]any{"title": title, "body": body}
	if len(labels) > 0 {
		payload["labels"] = labels
	}
	var out Issue
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/issues", r.Owner, r.Name), payload, &out)
	return out, err
}

// UpdateIssue replaces an issue's title and body. An empty title leaves the
// existing one.
func (c *Client) UpdateIssue(ctx context.Context, ref IssueRef, title, body string) (Issue, error) {
	if !c.Authenticated() {
		return Issue{}, fmt.Errorf("updating %s: %w", ref, ErrNoToken)
	}
	payload := map[string]any{"body": body}
	if strings.TrimSpace(title) != "" {
		payload["title"] = title
	}
	var out Issue
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", ref.Repo.Owner, ref.Repo.Name, ref.Number)
	err := c.do(ctx, http.MethodPatch, path, payload, &out)
	return out, err
}

// AddComment posts a comment on an issue or pull request and returns its URL.
func (c *Client) AddComment(ctx context.Context, ref IssueRef, body string) (string, error) {
	if !c.Authenticated() {
		return "", fmt.Errorf("commenting on %s: %w", ref, ErrNoToken)
	}
	var out Comment
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", ref.Repo.Owner, ref.Repo.Name, ref.Number)
	if err := c.do(ctx, http.MethodPost, path, map[string]any{"body": body}, &out); err != nil {
		return "", err
	}
	return out.HTMLURL, nil
}

// AddLabels adds labels to an issue, leaving the ones already on it.
func (c *Client) AddLabels(ctx context.Context, ref IssueRef, labels []string) error {
	if len(labels) == 0 {
		return nil
	}
	if !c.Authenticated() {
		return fmt.Errorf("labelling %s: %w", ref, ErrNoToken)
	}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/labels", ref.Repo.Owner, ref.Repo.Name, ref.Number)
	return c.do(ctx, http.MethodPost, path, map[string]any{"labels": labels}, nil)
}

// ReadPullRequest reads a pull request and the files it changes.
func (c *Client) ReadPullRequest(ctx context.Context, ref IssueRef) (PullRequest, []ChangedFile, error) {
	var pr PullRequest
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", ref.Repo.Owner, ref.Repo.Name, ref.Number)
	if err := c.do(ctx, http.MethodGet, path, nil, &pr); err != nil {
		return pr, nil, err
	}
	var files []ChangedFile
	if err := c.do(ctx, http.MethodGet, path+"/files?per_page=100", nil, &files); err != nil {
		// The pull request itself is the context that matters; a missing
		// file list narrows the analysis rather than preventing it.
		return pr, nil, nil
	}
	return pr, files, nil
}

// CreatePullRequest opens a pull request from head into base.
func (c *Client) CreatePullRequest(ctx context.Context, r Repo, title, body, head, base string, draft bool) (PullRequest, error) {
	if !c.Authenticated() {
		return PullRequest{}, fmt.Errorf("opening a pull request in %s: %w", r, ErrNoToken)
	}
	payload := map[string]any{"title": title, "body": body, "head": head, "base": base, "draft": draft}
	var out PullRequest
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/pulls", r.Owner, r.Name), payload, &out)
	return out, err
}

// ------------------------------------------------------------- transport --

// do performs one request, decoding a JSON body into out when out is non-nil.
//
// A 403 or 429 carrying a rate-limit reset is retried once, because the
// alternative — failing a ten-minute autonomous run on a secondary rate limit
// that clears in twenty seconds — is a worse answer than waiting. Nothing
// else is retried: a 401 does not become a 200 on the second attempt, and a
// write that is retried blindly is how an issue gets filed twice.
func (c *Client) do(ctx context.Context, method, path string, payload, out any) error {
	for attempt := 0; ; attempt++ {
		err := c.attempt(ctx, method, path, payload, out)
		if err == nil {
			return nil
		}
		var he *HTTPError
		if attempt > 0 || !errors.As(err, &he) {
			return err
		}
		wait, ok := he.RetryAfter()
		if !ok || wait > 2*time.Minute {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		c.sleep(wait)
	}
}

func (c *Client) attempt(ctx context.Context, method, path string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("%s %s: encoding the request: %w", method, path, err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", c.agent)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("%s %s: reading the response: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newHTTPError(method, path, resp, raw)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s %s: decoding the response: %w", method, path, err)
	}
	return nil
}

// HTTPError carries the status as a number, so a caller that wants to know
// about a 404 asks for the 404 rather than searching the message for the
// digits — which would also match a body that happens to mention them.
type HTTPError struct {
	Method             string
	Path               string
	Status             int
	Message            string
	RetryAfterHeader   string
	RateLimitReset     string
	RateLimitRemaining string
}

func newHTTPError(method, path string, resp *http.Response, body []byte) *HTTPError {
	return &HTTPError{
		Method:             method,
		Path:               path,
		Status:             resp.StatusCode,
		Message:            apiMessage(body, resp.Status),
		RetryAfterHeader:   resp.Header.Get("Retry-After"),
		RateLimitReset:     resp.Header.Get("X-RateLimit-Reset"),
		RateLimitRemaining: resp.Header.Get("X-RateLimit-Remaining"),
	}
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.Status, e.Message)
}

// RetryAfter reports how long to wait before retrying, when the response says
// the request was rate limited and when it says when the limit clears.
func (e *HTTPError) RetryAfter() (time.Duration, bool) {
	if e.Status != http.StatusTooManyRequests &&
		!(e.Status == http.StatusForbidden && e.RateLimitRemaining == "0") {
		return 0, false
	}
	if s := strings.TrimSpace(e.RetryAfterHeader); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			return time.Duration(n)*time.Second + time.Second, true
		}
	}
	if s := strings.TrimSpace(e.RateLimitReset); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			d := time.Until(time.Unix(n, 0))
			if d > 0 {
				return d + time.Second, true
			}
			return time.Second, true
		}
	}
	return 0, false
}

// apiMessage pulls GitHub's own error message out of the body, which is more
// useful than the status text: "Validation Failed" plus the field that failed
// beats "422 Unprocessable Entity".
func apiMessage(body []byte, status string) string {
	var payload struct {
		Message string `json:"message"`
		Errors  []struct {
			Resource string `json:"resource"`
			Field    string `json:"field"`
			Code     string `json:"code"`
			Message  string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Message == "" {
		return firstLine(status)
	}
	msg := payload.Message
	for _, e := range payload.Errors {
		detail := e.Message
		if detail == "" {
			detail = strings.TrimSpace(e.Resource + "." + e.Field + " " + e.Code)
		}
		if detail != "" {
			msg += " (" + detail + ")"
		}
	}
	return firstLine(msg)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
