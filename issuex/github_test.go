package issuex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/quick"
	"time"
)

func newTestGitHubClient(o Options) *githubClient {
	c, err := NewGitHub(o)
	if err != nil {
		panic(err)
	}
	return c.(*githubClient)
}

// TestGitHub_Client_TS_02_1 verifies TS-02-1:
// GitHub client adapter implements Client interface, handles authentication state, and closes idle transport connections.
// Verifies: 02-REQ-1.1, 02-REQ-1.4
func TestGitHub_Client_TS_02_1(t *testing.T) {
	var _ Client = (*githubClient)(nil)

	c := newTestGitHubClient(Options{Token: "ghp_secret"})
	if !c.Authenticated() {
		t.Errorf("expected Authenticated() == true, got false")
	}
	if err := c.Close(); err != nil {
		t.Errorf("expected Close() == nil, got %v", err)
	}

	unauth := newTestGitHubClient(Options{})
	if unauth.Authenticated() {
		t.Errorf("expected Authenticated() == false, got true")
	}
}

// TestGitHub_Constructor_TS_02_2 verifies TS-02-2:
// NewGitHub constructor resolves base URL, token, HTTP client, and user agent from options and environment variables.
// Verifies: 02-REQ-1.2
func TestGitHub_Constructor_TS_02_2(t *testing.T) {
	c1, err := NewGitHub(Options{BaseURL: "https://ghe.example.com/api/v3/", Token: "tok1", UserAgent: "custom-ua"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gh1, ok := c1.(*githubClient)
	if !ok {
		t.Fatalf("expected *githubClient, got %T", c1)
	}
	if gh1.baseURL != "https://ghe.example.com/api/v3" {
		t.Errorf("expected baseURL https://ghe.example.com/api/v3, got %s", gh1.baseURL)
	}
	if gh1.token != "tok1" {
		t.Errorf("expected token tok1, got %s", gh1.token)
	}
	if gh1.userAgent != "custom-ua" {
		t.Errorf("expected userAgent custom-ua, got %s", gh1.userAgent)
	}

	t.Setenv("GITHUB_API_URL", "https://api.github.corp/")
	t.Setenv("GITHUB_TOKEN", "tok2")
	c2, err := NewGitHub(Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	gh2, ok := c2.(*githubClient)
	if !ok {
		t.Fatalf("expected *githubClient, got %T", c2)
	}
	if gh2.baseURL != "https://api.github.corp" {
		t.Errorf("expected baseURL https://api.github.corp, got %s", gh2.baseURL)
	}
	if gh2.token != "tok2" {
		t.Errorf("expected token tok2, got %s", gh2.token)
	}
	if gh2.userAgent != "agent-fox" {
		t.Errorf("expected userAgent agent-fox, got %s", gh2.userAgent)
	}
	if gh2.httpClient == nil || gh2.httpClient.Timeout != 30*time.Second {
		t.Errorf("expected httpClient timeout 30s, got %v", gh2.httpClient)
	}
}

// TestGitHub_Factory_TS_02_3 verifies TS-02-3:
// NewWithOptions factory constructs and returns GitHub client when forge type resolves to GitHub.
// Verifies: 02-REQ-1.3
func TestGitHub_Factory_TS_02_3(t *testing.T) {
	client, err := NewWithOptions(Options{
		Repo:      Repo{Owner: "agentfox", Name: "agent-fox"},
		RemoteURL: "https://github.com/agentfox/agent-fox.git",
		Token:     "dummy-token",
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if !client.Authenticated() {
		t.Errorf("expected Authenticated() == true, got false")
	}
	ghClient, ok := client.(*githubClient)
	if !ok {
		t.Fatalf("expected *githubClient, got %T", client)
	}
	if ghClient.repo.Owner != "agentfox" {
		t.Errorf("expected repo owner agentfox, got %s", ghClient.repo.Owner)
	}
}

// TestGitHub_Headers_TS_02_4 verifies TS-02-4:
// Outgoing HTTP requests consistently format GitHub API headers and authentication tokens.
// Verifies: 02-REQ-1.5
func TestGitHub_Headers_TS_02_4(t *testing.T) {
	var mu sync.Mutex
	var captured *http.Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		captured = r.Clone(r.Context())
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	methods := []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	f := func(methodIdx uint8, pathSeed uint16, rawToken string, body []byte) bool {
		method := methods[int(methodIdx)%len(methods)]
		path := fmt.Sprintf("/path/%d", pathSeed)

		// Sanitize token to valid HTTP header characters
		var tokenBuilder strings.Builder
		for _, r := range rawToken {
			if r >= 33 && r <= 126 {
				tokenBuilder.WriteRune(r)
			}
		}
		token := tokenBuilder.String()

		c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: token})
		_ = c.do(context.Background(), method, path, body, nil)

		mu.Lock()
		defer mu.Unlock()
		if captured == nil {
			return false
		}

		ok := captured.Header.Get("Accept") == "application/vnd.github+json" &&
			captured.Header.Get("X-GitHub-Api-Version") == "2022-11-28" &&
			captured.Header.Get("User-Agent") != ""

		if token != "" {
			ok = ok && captured.Header.Get("Authorization") == "Bearer "+token
		} else {
			ok = ok && captured.Header.Get("Authorization") == ""
		}

		if len(body) > 0 {
			ok = ok && captured.Header.Get("Content-Type") == "application/json"
		} else {
			ok = ok && captured.Header.Get("Content-Type") == ""
		}

		return ok
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatalf("property failed: %v", err)
	}
}

// TestGitHub_Transport_LimitReader_TS_02_21 verifies TS-02-21:
// Transport enforces 8 MB bounded reading on HTTP response bodies.
// Verifies: 02-REQ-7.1
func TestGitHub_Transport_LimitReader_TS_02_21(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		chunk := make([]byte, 1024*1024)
		for i := 0; i < 9; i++ {
			_, _ = w.Write(chunk)
		}
	}))
	defer srv.Close()

	c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"})
	var buf bytes.Buffer
	err := c.do(context.Background(), "GET", "/test", nil, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.Len() > 8*1024*1024 {
		t.Errorf("expected buffer len <= 8MB, got %d", buf.Len())
	}
	if buf.Len() != 8*1024*1024 {
		t.Errorf("expected buffer len == 8MB, got %d", buf.Len())
	}
}

// TestGitHub_Transport_RateLimit_TS_02_22 verifies TS-02-22:
// Transport executes single rate-limit retry with injectable sleep and returns ErrRateLimited if limit exceeded.
// Verifies: 02-REQ-7.2, 02-REQ-7.3, 02-REQ-7.6
func TestGitHub_Transport_RateLimit_TS_02_22(t *testing.T) {
	var slept time.Duration
	sleepFn := func(d time.Duration) { slept = d }

	// Case 1: 429 with 5-second backoff succeeds on retry
	attempts := 0
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"full_name": "o/r"}`))
	}))
	defer srv1.Close()

	c1 := newTestGitHubClient(Options{BaseURL: srv1.URL, Token: "tok"})
	c1.sleep = sleepFn
	_, err1 := c1.GetRepository(context.Background(), Repo{Owner: "o", Name: "r"})
	if err1 != nil {
		t.Errorf("expected err1 == nil, got %v", err1)
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
	if slept != 5*time.Second && slept != 6*time.Second {
		t.Errorf("expected slept around 5s-6s, got %v", slept)
	}

	// Case 2: 403 quota exhaustion with > 120s backoff aborts immediately
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(200*time.Second).Unix(), 10))
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv2.Close()

	c2 := newTestGitHubClient(Options{BaseURL: srv2.URL, Token: "tok"})
	c2.sleep = sleepFn
	_, err2 := c2.GetRepository(context.Background(), Repo{Owner: "o", Name: "r"})
	if !IsRateLimited(err2) {
		t.Errorf("expected IsRateLimited(err2) == true, got %v", err2)
	}
}

// TestGitHub_Transport_ErrorFormat_TS_02_23 verifies TS-02-23:
// GitHub REST error JSON payloads format into HTTPError message with field-level details.
// Verifies: 02-REQ-7.4
func TestGitHub_Transport_ErrorFormat_TS_02_23(t *testing.T) {
	type fieldError struct {
		Field string `json:"field"`
		Code  string `json:"code"`
	}

	cleanWord := func(s, def string) string {
		var sb strings.Builder
		for _, r := range s {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
				sb.WriteRune(r)
			}
		}
		if sb.Len() == 0 {
			return def
		}
		return sb.String()
	}

	f := func(rawMsg, f1, c1, f2, c2 string) bool {
		msg := cleanWord(rawMsg, "Validation Failed")
		fieldErrs := []fieldError{
			{Field: cleanWord(f1, "name"), Code: cleanWord(c1, "already_exists")},
			{Field: cleanWord(f2, "email"), Code: cleanWord(c2, "invalid")},
		}

		payload, err := json.Marshal(map[string]any{
			"message": msg,
			"errors":  fieldErrs,
		})
		if err != nil {
			return false
		}

		httpErr := parseGitHubError(http.StatusUnprocessableEntity, "POST", "/repos/o/r/labels", payload)
		expectedPrefix := msg
		if !strings.Contains(httpErr.Error(), expectedPrefix) {
			return false
		}
		for _, fe := range fieldErrs {
			if !strings.Contains(httpErr.Error(), fe.Field) || !strings.Contains(httpErr.Error(), fe.Code) {
				return false
			}
		}
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatalf("property failed: %v", err)
	}
}

// TestGitHub_Transport_SentinelErrors_TS_02_24 verifies TS-02-24:
// Transport maps HTTP 404 to ErrNotFound and 409 and 405 to ErrConflict in HTTPError.
// Verifies: 02-REQ-7.5
func TestGitHub_Transport_SentinelErrors_TS_02_24(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/404":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message": "Not Found"}`))
		case "/409":
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message": "Conflict"}`))
		case "/405":
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte(`{"message": "Method Not Allowed"}`))
		}
	}))
	defer srv.Close()

	c := newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"})

	err404 := c.do(context.Background(), "GET", "/404", nil, nil)
	if !IsNotFound(err404) {
		t.Errorf("expected IsNotFound(err404) == true, got %v", err404)
	}
	if !errors.Is(err404, ErrNotFound) {
		t.Errorf("expected errors.Is(err404, ErrNotFound), got %v", err404)
	}

	err409 := c.do(context.Background(), "POST", "/409", nil, nil)
	if !IsConflict(err409) {
		t.Errorf("expected IsConflict(err409) == true, got %v", err409)
	}
	if !errors.Is(err409, ErrConflict) {
		t.Errorf("expected errors.Is(err409, ErrConflict), got %v", err409)
	}

	err405 := c.do(context.Background(), "PUT", "/405", nil, nil)
	if !IsConflict(err405) {
		t.Errorf("expected IsConflict(err405) == true, got %v", err405)
	}
	if !errors.Is(err405, ErrConflict) {
		t.Errorf("expected errors.Is(err405, ErrConflict), got %v", err405)
	}
}
