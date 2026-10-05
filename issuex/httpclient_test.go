package issuex

import (
	"net/http"
	"testing"
	"time"
)

// TestSuppliedHTTPClientIsUsedAsIs verifies 02-REQ-1.2 and its GitLab twin: a
// caller's http.Client is the one the adapter uses, whatever its Timeout, so a
// caller that relies on context deadlines gets no hidden cap and its later
// changes to the client reach the adapter. The 30 s default is only for a
// caller that supplies none.
func TestSuppliedHTTPClientIsUsedAsIs(t *testing.T) {
	for _, v := range []string{"GITHUB_API_URL", "GITHUB_TOKEN", "GH_TOKEN", "GITLAB_API_URL", "GITLAB_TOKEN"} {
		t.Setenv(v, "")
	}
	clientOf := func(t *testing.T, c Client) *http.Client {
		t.Helper()
		switch c := c.(type) {
		case *githubClient:
			return c.httpClient
		case *gitlabClient:
			return c.httpClient
		}
		t.Fatalf("unexpected client type %T", c)
		return nil
	}
	constructors := map[string]func(Options) (Client, error){
		"NewGitHub":                        NewGitHub,
		"NewGitLab":                        NewGitLab,
		"NewWithOptions (GitHub base URL)": func(o Options) (Client, error) { o.BaseURL = "https://api.github.com"; return NewWithOptions(o) },
		"NewWithOptions (GitLab base URL)": func(o Options) (Client, error) { o.BaseURL = "https://gitlab.com/api/v4"; return NewWithOptions(o) },
	}
	for name, construct := range constructors {
		t.Run(name, func(t *testing.T) {
			for label, supplied := range map[string]*http.Client{
				"no timeout":    {},
				"own timeout":   {Timeout: 5 * time.Minute},
				"own transport": {Transport: http.DefaultTransport},
				"zero timeout":  {Timeout: 0},
			} {
				c, err := construct(Options{HTTPClient: supplied})
				if err != nil {
					t.Fatalf("%s: unexpected error: %v", label, err)
				}
				if got := clientOf(t, c); got != supplied {
					t.Errorf("%s: adapter uses a different *http.Client than the one supplied (timeout %v)", label, got.Timeout)
				}
			}

			supplied := &http.Client{}
			c, err := construct(Options{HTTPClient: supplied})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := clientOf(t, c).Timeout; got != 0 {
				t.Errorf("supplied client's Timeout became %v, want 0", got)
			}

			c, err = construct(Options{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := clientOf(t, c); got == nil || got.Timeout != 30*time.Second {
				t.Errorf("default client = %+v, want a 30 s timeout", got)
			}
		})
	}
}
