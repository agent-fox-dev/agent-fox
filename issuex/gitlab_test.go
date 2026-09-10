package issuex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/quick"
	"time"
)

func newTestGitLabClient(o Options) *gitlabClient {
	c, _ := NewGitLab(o)
	if gc, ok := c.(*gitlabClient); ok {
		return gc
	}
	return nil
}

// TestGitLab_Client_Lifecycle_TS_03_1 verifies TS-03-1:
// GitLab client adapter implements Client interface, handles authentication state, and closes idle transport connections.
// Verifies: 03-REQ-1.1, 03-REQ-1.5
func TestGitLab_Client_Lifecycle_TS_03_1(t *testing.T) {
	var _ Client = (*gitlabClient)(nil)

	c := newTestGitLabClient(Options{Token: "glpat_secret"})
	if c == nil {
		t.Fatalf("expected non-nil client")
	}
	if !c.Authenticated() {
		t.Errorf("expected Authenticated() == true for authenticated client")
	}
	if err := c.Close(); err != nil {
		t.Errorf("expected Close() == nil, got %v", err)
	}

	unauth := newTestGitLabClient(Options{})
	if unauth == nil {
		t.Fatalf("expected non-nil client")
	}
	if unauth.Authenticated() {
		t.Errorf("expected Authenticated() == false for unauthenticated client")
	}
}

// TestGitLab_Constructor_TS_03_2 verifies TS-03-2:
// NewGitLab constructor resolves base URL, token, HTTP client, and user agent from options and environment variables.
// Verifies: 03-REQ-1.2
func TestGitLab_Constructor_TS_03_2(t *testing.T) {
	c1, err1 := NewGitLab(Options{
		BaseURL:   "https://gitlab.example.com/subpath/",
		Token:     "tok1",
		UserAgent: "custom-ua",
	})
	if err1 != nil {
		t.Fatalf("unexpected error: %v", err1)
	}
	gc1, ok := c1.(*gitlabClient)
	if !ok {
		t.Fatalf("expected *gitlabClient, got %T", c1)
	}
	if gc1.baseURL != "https://gitlab.example.com/subpath/api/v4" {
		t.Errorf("baseURL = %q, want %q", gc1.baseURL, "https://gitlab.example.com/subpath/api/v4")
	}
	if gc1.token != "tok1" {
		t.Errorf("token = %q, want %q", gc1.token, "tok1")
	}
	if gc1.userAgent != "custom-ua" {
		t.Errorf("userAgent = %q, want %q", gc1.userAgent, "custom-ua")
	}

	t.Setenv("GITLAB_API_URL", "https://gl.corp/api/v4/")
	t.Setenv("GITLAB_TOKEN", "tok2")
	c2, err2 := NewGitLab(Options{})
	if err2 != nil {
		t.Fatalf("unexpected error: %v", err2)
	}
	gc2, ok := c2.(*gitlabClient)
	if !ok {
		t.Fatalf("expected *gitlabClient, got %T", c2)
	}
	if gc2.baseURL != "https://gl.corp/api/v4" {
		t.Errorf("baseURL = %q, want %q", gc2.baseURL, "https://gl.corp/api/v4")
	}
	if gc2.token != "tok2" {
		t.Errorf("token = %q, want %q", gc2.token, "tok2")
	}
	if gc2.userAgent != "agent-fox" {
		t.Errorf("userAgent = %q, want agent-fox", gc2.userAgent)
	}
	if gc2.httpClient == nil || gc2.httpClient.Timeout != 30*time.Second {
		t.Errorf("expected 30s timeout on default httpClient")
	}
}

// TestGitLab_Factory_Wiring_TS_03_3 verifies TS-03-3:
// NewWithOptions factory constructs and returns GitLab client when forge type resolves to GitLab.
// Verifies: 03-REQ-1.3
func TestGitLab_Factory_Wiring_TS_03_3(t *testing.T) {
	client, err := NewWithOptions(Options{
		Repo:      Repo{Owner: "group/subgroup", Name: "project"},
		RemoteURL: "https://gitlab.com/group/subgroup/project.git",
		Token:     "glpat-token",
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if client == nil {
		t.Fatalf("expected non-nil client")
	}
	if !client.Authenticated() {
		t.Errorf("expected Authenticated() == true")
	}
	gc, ok := client.(*gitlabClient)
	if !ok {
		t.Fatalf("expected *gitlabClient, got %T", client)
	}
	if gc.repo.String() != "group/subgroup/project" {
		t.Errorf("repo = %q, want group/subgroup/project", gc.repo.String())
	}
}

// TestGitLab_Factory_AmbiguousHostProbe_TS_03_4 verifies TS-03-4:
// NewWithOptions probes ambiguous hosts with GET /api/v4/version to detect self-hosted GitLab instances before returning ErrAmbiguousForge.
// Verifies: 03-REQ-1.4
func TestGitLab_Factory_AmbiguousHostProbe_TS_03_4(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/version" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"version": "16.8.0-ee", "revision": "abc123"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client, err := NewWithOptions(Options{
		Repo:      Repo{Owner: "corp", Name: "app"},
		RemoteURL: srv.URL + "/corp/app.git",
		Token:     "glpat-token",
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if client == nil {
		t.Fatalf("expected non-nil client")
	}
	gc, ok := client.(*gitlabClient)
	if !ok {
		t.Fatalf("expected *gitlabClient, got %T", client)
	}
	if gc.baseURL != srv.URL+"/api/v4" {
		t.Errorf("baseURL = %q, want %q", gc.baseURL, srv.URL+"/api/v4")
	}

	// Negative case: ambiguous host that is not GitLab returns ErrAmbiguousForge
	srvNonGitLab := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srvNonGitLab.Close()

	_, errNonGitLab := NewWithOptions(Options{
		Repo:      Repo{Owner: "corp", Name: "app"},
		RemoteURL: srvNonGitLab.URL + "/corp/app.git",
		Token:     "glpat-token",
	})
	if !errors.Is(errNonGitLab, ErrAmbiguousForge) && !strings.Contains(fmt.Sprint(errNonGitLab), "ambiguous") {
		t.Errorf("expected ambiguous forge error for non-GitLab host, got %v", errNonGitLab)
	}
}

// TestGitLab_Headers_Property_TS_03_5 verifies TS-03-5:
// Outgoing HTTP requests consistently format GitLab API headers and authentication tokens.
// Verifies: 03-REQ-1.6
func TestGitLab_Headers_Property_TS_03_5(t *testing.T) {
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

		var tokenBuilder strings.Builder
		for _, r := range rawToken {
			if r >= 33 && r <= 126 {
				tokenBuilder.WriteRune(r)
			}
		}
		token := tokenBuilder.String()

		c := newTestGitLabClient(Options{BaseURL: srv.URL, Token: token})
		_, _ = c.do(context.Background(), method, path, body, nil)

		mu.Lock()
		defer mu.Unlock()
		if captured == nil {
			return false
		}

		ok := captured.Header.Get("Accept") == "application/json" &&
			captured.Header.Get("User-Agent") != ""

		if token != "" {
			ok = ok && captured.Header.Get("PRIVATE-TOKEN") == token
		} else {
			ok = ok && captured.Header.Get("PRIVATE-TOKEN") == ""
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

// TestGitLab_Transport_LimitReader_TS_03_25 verifies TS-03-25:
// Transport limits response body reading to 8 MB via io.LimitReader.
// Verifies: 03-REQ-7.1
func TestGitLab_Transport_LimitReader_TS_03_25(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		chunk := make([]byte, 1024*1024)
		for i := 0; i < 9; i++ {
			_, _ = w.Write(chunk)
		}
	}))
	defer srv.Close()

	c := newTestGitLabClient(Options{BaseURL: srv.URL, Token: "tok"})
	body, err := c.do(context.Background(), "GET", "/test", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(body) != 8*1024*1024 {
		t.Errorf("len(body) = %d, want %d", len(body), 8*1024*1024)
	}
}

// TestGitLab_Transport_RateLimit_TS_03_26 verifies TS-03-26:
// Rate-limit backoff sleeps via injectable function and retries once, failing with ErrRateLimited if duration exceeds 120s or retry fails.
// Verifies: 03-REQ-7.2, 03-REQ-7.3, 03-REQ-7.6
func TestGitLab_Transport_RateLimit_TS_03_26(t *testing.T) {
	var mu sync.Mutex
	var sleepDurations []time.Duration
	mockSleep := func(d time.Duration) {
		mu.Lock()
		sleepDurations = append(sleepDurations, d)
		mu.Unlock()
	}

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		c := calls
		mu.Unlock()

		p := strings.TrimPrefix(r.URL.Path, "/api/v4")
		if p == "/retry-ok" && c == 1 {
			w.Header().Set("Retry-After", "10")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message": "Rate limit exceeded"}`))
			return
		}
		if p == "/retry-fail" {
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message": "Rate limit exceeded"}`))
			return
		}
		if p == "/retry-excessive" {
			w.Header().Set("Retry-After", "125")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message": "Rate limit exceeded"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status": "ok"}`))
	}))
	defer srv.Close()

	c := newTestGitLabClient(Options{BaseURL: srv.URL, Token: "tok"})
	c.sleep = mockSleep

	// Case 1: Retry-After <= 120s retries once and succeeds
	mu.Lock()
	calls = 0
	sleepDurations = nil
	mu.Unlock()

	_, err1 := c.do(context.Background(), "GET", "/retry-ok", nil, nil)
	if err1 != nil {
		t.Fatalf("expected err1 == nil, got %v", err1)
	}
	if len(sleepDurations) != 1 || (sleepDurations[0] != 10*time.Second && sleepDurations[0] != 11*time.Second) {
		t.Errorf("expected sleep duration 10s-11s, got %v", sleepDurations)
	}

	// Case 2: Retry-After > 120s aborts immediately with ErrRateLimited
	_, err2 := c.do(context.Background(), "GET", "/retry-excessive", nil, nil)
	if !IsRateLimited(err2) {
		t.Errorf("expected IsRateLimited(err2) == true, got %v", err2)
	}

	// Case 3: retried request fails, returns ErrRateLimited
	_, err3 := c.do(context.Background(), "GET", "/retry-fail", nil, nil)
	if !IsRateLimited(err3) {
		t.Errorf("expected IsRateLimited(err3) == true, got %v", err3)
	}
}

// TestGitLab_Transport_ErrorPayloads_TS_03_27 verifies TS-03-27:
// Error response payloads format HTTPError message from string, validation object, or error description.
// Verifies: 03-REQ-7.4
func TestGitLab_Transport_ErrorPayloads_TS_03_27(t *testing.T) {
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

	f := func(msgChoice uint8, w1, w2, w3 string) bool {
		str1 := cleanWord(w1, "error_one")
		str2 := cleanWord(w2, "error_two")
		str3 := cleanWord(w3, "error_three")

		var payload []byte
		var expectedMessage string

		switch msgChoice % 3 {
		case 0:
			// Plain string message
			expectedMessage = str1 + " " + str2
			payload, _ = json.Marshal(map[string]string{
				"message": expectedMessage,
			})
		case 1:
			// Nested validation object
			expectedMessage = str1 + ": " + str2
			payload, _ = json.Marshal(map[string]any{
				"message": map[string][]string{
					str1: {str2},
				},
			})
		case 2:
			// error and error_description
			expectedMessage = str1 + ": " + str3
			payload, _ = json.Marshal(map[string]string{
				"error":             str1,
				"error_description": str3,
			})
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(payload)
		}))
		defer srv.Close()

		c := newTestGitLabClient(Options{BaseURL: srv.URL, Token: "tok"})
		_, err := c.do(context.Background(), "GET", "/err", nil, nil)
		return err != nil && strings.Contains(err.Error(), expectedMessage)
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Fatalf("property failed: %v", err)
	}
}

// TestGitLab_Transport_SentinelErrors_TS_03_28 verifies TS-03-28:
// HTTP transport wraps sentinel errors ErrNotFound for 404 and ErrConflict for 405, 406, and 409.
// Verifies: 03-REQ-7.5
func TestGitLab_Transport_SentinelErrors_TS_03_28(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/api/v4")
		switch p {
		case "/404":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message": "not found"}`))
		case "/405":
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte(`{"message": "method not allowed"}`))
		case "/406":
			w.WriteHeader(http.StatusNotAcceptable)
			_, _ = w.Write([]byte(`{"message": "not acceptable"}`))
		case "/409":
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message": "conflict"}`))
		}
	}))
	defer srv.Close()

	c := newTestGitLabClient(Options{BaseURL: srv.URL, Token: "tok"})

	_, err404 := c.do(context.Background(), "GET", "/404", nil, nil)
	if !IsNotFound(err404) {
		t.Errorf("expected IsNotFound(err404) == true, got %v", err404)
	}

	_, err405 := c.do(context.Background(), "GET", "/405", nil, nil)
	if !IsConflict(err405) {
		t.Errorf("expected IsConflict(err405) == true, got %v", err405)
	}

	_, err406 := c.do(context.Background(), "GET", "/406", nil, nil)
	if !IsConflict(err406) {
		t.Errorf("expected IsConflict(err406) == true, got %v", err406)
	}

	_, err409 := c.do(context.Background(), "GET", "/409", nil, nil)
	if !IsConflict(err409) {
		t.Errorf("expected IsConflict(err409) == true, got %v", err409)
	}
}

// TestGitLab_Path_Property_TS_03_6 verifies TS-03-6:
// Project-scoped REST endpoints format project path as url.PathEscape(repo.String()) with slashes replaced by %2F.
// Verifies: 03-REQ-2.1
func TestGitLab_Path_Property_TS_03_6(t *testing.T) {
	f := func(owner, name string) bool {
		r := Repo{Owner: owner, Name: name}
		expected := url.PathEscape(r.String())
		expected = strings.ReplaceAll(expected, "/", "%2F")
		return projectPath(r) == expected && !strings.Contains(projectPath(r), "/")
	}
	if err := quick.Check(f, nil); err != nil {
		t.Fatalf("projectPath property failed: %v", err)
	}

	// Specific test cases for nested paths and special characters
	cases := []struct {
		repo Repo
		want string
	}{
		{Repo{Owner: "group", Name: "project"}, "group%2Fproject"},
		{Repo{Owner: "group/subgroup", Name: "project"}, "group%2Fsubgroup%2Fproject"},
		{Repo{Owner: "a/b/c", Name: "d"}, "a%2Fb%2Fc%2Fd"},
	}
	for _, tc := range cases {
		if got := projectPath(tc.repo); got != tc.want {
			t.Errorf("projectPath(%+v) = %q, want %q", tc.repo, got, tc.want)
		}
	}
}

// TestGitLab_GetRepository_TS_03_7 verifies TS-03-7:
// GetRepository retrieves project metadata, calculates permissions from effective access level, and records merge policy settings.
// Verifies: 03-REQ-2.2, 03-REQ-2.3
func TestGitLab_GetRepository_TS_03_7(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.EscapedPath() != "/api/v4/projects/group%2Fproject" {
			t.Errorf("escaped path = %s, want /api/v4/projects/group%%2Fproject", r.URL.EscapedPath())
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"path_with_namespace": "group/project",
			"default_branch": "main",
			"visibility": "private",
			"archived": false,
			"permissions": {
				"project_access": {"access_level": 30},
				"group_access": {"access_level": 20}
			},
			"merge_method": "merge",
			"squash_option": "default_on"
		}`))
	}))
	defer srv.Close()

	c := newTestGitLabClient(Options{BaseURL: srv.URL, Token: "glpat-tok"})
	repo, err := c.GetRepository(context.Background(), Repo{Owner: "group", Name: "project"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.FullName != "group/project" {
		t.Errorf("FullName = %q, want %q", repo.FullName, "group/project")
	}
	if repo.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %q, want %q", repo.DefaultBranch, "main")
	}
	if !repo.Private {
		t.Errorf("Private = %v, want true", repo.Private)
	}
	if repo.Archived {
		t.Errorf("Archived = %v, want false", repo.Archived)
	}
	if !repo.Permissions.Push || !repo.Permissions.Pull || repo.Permissions.Admin {
		t.Errorf("Permissions = %+v, want Push=true, Pull=true, Admin=false", repo.Permissions)
	}
	if c.mergeMethod != "merge" || c.squashOption != "default_on" {
		t.Errorf("mergeMethod = %q, squashOption = %q, want merge / default_on", c.mergeMethod, c.squashOption)
	}

	// Test fallback to c.repo when called with empty Repo{}
	cWithRepo := newTestGitLabClient(Options{
		BaseURL: srv.URL,
		Token:   "glpat-tok",
		Repo:    Repo{Owner: "group", Name: "project"},
	})
	repo2, err2 := cWithRepo.GetRepository(context.Background(), Repo{})
	if err2 != nil {
		t.Fatalf("unexpected error on fallback repo: %v", err2)
	}
	if repo2.FullName != "group/project" {
		t.Errorf("repo2.FullName = %q, want group/project", repo2.FullName)
	}

	// Test permissions calculation: public project with no explicit access level
	srvPublic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"path_with_namespace": "open/repo",
			"default_branch": "master",
			"visibility": "public",
			"archived": true,
			"permissions": null,
			"merge_method": "rebase_merge",
			"squash_option": "never"
		}`))
	}))
	defer srvPublic.Close()

	cPub := newTestGitLabClient(Options{BaseURL: srvPublic.URL})
	repoPub, errPub := cPub.GetRepository(context.Background(), Repo{Owner: "open", Name: "repo"})
	if errPub != nil {
		t.Fatalf("unexpected error: %v", errPub)
	}
	if repoPub.Private {
		t.Errorf("expected Private == false for public project")
	}
	if !repoPub.Archived {
		t.Errorf("expected Archived == true")
	}
	if !repoPub.Permissions.Pull {
		t.Errorf("expected Pull == true for public project")
	}
	if repoPub.Permissions.Push || repoPub.Permissions.Admin {
		t.Errorf("expected Push == false, Admin == false for unauthenticated public project, got %+v", repoPub.Permissions)
	}
	if cPub.mergeMethod != "rebase_merge" || cPub.squashOption != "never" {
		t.Errorf("cPub.mergeMethod = %q, squashOption = %q", cPub.mergeMethod, cPub.squashOption)
	}

	// Test admin permissions calculation (access_level >= 40)
	srvAdmin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"path_with_namespace": "corp/admin-proj",
			"default_branch": "main",
			"visibility": "internal",
			"permissions": {
				"group_access": {"access_level": 40}
			}
		}`))
	}))
	defer srvAdmin.Close()

	cAdmin := newTestGitLabClient(Options{BaseURL: srvAdmin.URL, Token: "glpat-tok"})
	repoAdmin, errAdmin := cAdmin.GetRepository(context.Background(), Repo{Owner: "corp", Name: "admin-proj"})
	if errAdmin != nil {
		t.Fatalf("unexpected error: %v", errAdmin)
	}
	if !repoAdmin.Private {
		t.Errorf("expected Private == true for internal project")
	}
	if !repoAdmin.Permissions.Pull || !repoAdmin.Permissions.Push || !repoAdmin.Permissions.Admin {
		t.Errorf("expected Pull, Push, Admin all true for access_level 40, got %+v", repoAdmin.Permissions)
	}
}

// TestGitLab_GetRepository_NotFound_TS_03_8 verifies TS-03-8:
// GetRepository maps HTTP 404 to ErrNotFound, adding private project guidance when unauthenticated.
// Verifies: 03-REQ-2.4, 03-REQ-2.5
func TestGitLab_GetRepository_NotFound_TS_03_8(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message": "404 Project Not Found"}`))
	}))
	defer srv.Close()

	unauth := newTestGitLabClient(Options{BaseURL: srv.URL})
	_, err1 := unauth.GetRepository(context.Background(), Repo{Owner: "org", Name: "secret"})
	if !IsNotFound(err1) {
		t.Errorf("expected IsNotFound(err1) == true, got %v", err1)
	}
	if !strings.Contains(err1.Error(), "GITLAB_TOKEN") {
		t.Errorf("expected guidance mentioning GITLAB_TOKEN, got %q", err1.Error())
	}

	auth := newTestGitLabClient(Options{BaseURL: srv.URL, Token: "glpat-tok"})
	_, err2 := auth.GetRepository(context.Background(), Repo{Owner: "org", Name: "missing"})
	if !IsNotFound(err2) {
		t.Errorf("expected IsNotFound(err2) == true, got %v", err2)
	}
}
