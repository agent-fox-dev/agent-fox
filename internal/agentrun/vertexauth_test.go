package agentrun

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/anthropic"
	"golang.org/x/oauth2"
)

// clearAnthropicEnv removes every variable that selects or authenticates an
// Anthropic deployment, so a developer's shell cannot change the outcome.
func clearAnthropicEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN",
		"ANTHROPIC_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL", "ANTHROPIC_VERTEX_PROJECT_ID",
		"CLAUDE_CODE_USE_VERTEX", "CLOUD_ML_REGION",
	} {
		t.Setenv(v, "")
		_ = os.Unsetenv(v)
	}
}

// selectVertex selects Claude on Vertex AI the way an operator does.
func selectVertex(t *testing.T) {
	t.Helper()
	clearAnthropicEnv(t)
	t.Setenv("CLAUDE_CODE_USE_VERTEX", "1")
	t.Setenv("ANTHROPIC_VERTEX_PROJECT_ID", "test-project")
}

// stubADC replaces the credential lookup for one test and counts its calls.
func stubADC(t *testing.T, src oauth2.TokenSource, err error) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	old := findADC
	findADC = func(context.Context) (oauth2.TokenSource, error) {
		calls.Add(1)
		return src, err
	}
	t.Cleanup(func() { findADC = old })
	return &calls
}

func static(token string) oauth2.TokenSource {
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token, TokenType: "Bearer"})
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func okResponse(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: http.NoBody}, nil
}

func TestADCTransportAddsABearerTokenAndLooksCredentialsUpOnce(t *testing.T) {
	calls := stubADC(t, static("adc-token"), nil)
	var seen []string
	tr := &adcTransport{base: rtFunc(func(r *http.Request) (*http.Response, error) {
		seen = append(seen, r.Header.Get("Authorization"))
		return okResponse(r)
	})}
	for range 2 {
		req, _ := http.NewRequest(http.MethodPost, "https://us-east5-aiplatform.googleapis.com/v1/x", nil)
		if _, err := tr.RoundTrip(req); err != nil {
			t.Fatal(err)
		}
		if req.Header.Get("Authorization") != "" {
			t.Error("the caller's request was modified")
		}
	}
	if len(seen) != 2 || seen[0] != "Bearer adc-token" || seen[1] != "Bearer adc-token" {
		t.Errorf("Authorization headers sent = %q", seen)
	}
	if calls.Load() != 1 {
		t.Errorf("credential lookups = %d, want 1", calls.Load())
	}
}

// A token the operator supplied is theirs to choose; ADC is the fallback.
func TestADCTransportLeavesAnExistingAuthorizationAlone(t *testing.T) {
	calls := stubADC(t, static("adc-token"), nil)
	var got string
	tr := &adcTransport{base: rtFunc(func(r *http.Request) (*http.Response, error) {
		got = r.Header.Get("Authorization")
		return okResponse(r)
	})}
	req, _ := http.NewRequest(http.MethodPost, "https://aiplatform.googleapis.com/v1/x", nil)
	req.Header.Set("Authorization", "Bearer mine")
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer mine" || calls.Load() != 0 {
		t.Errorf("Authorization = %q, lookups = %d", got, calls.Load())
	}
}

func TestADCTransportFailsWithoutSendingWhenNoCredentialIsFound(t *testing.T) {
	stubADC(t, nil, errors.New("could not find default credentials"))
	tr := &adcTransport{base: rtFunc(func(*http.Request) (*http.Response, error) {
		t.Error("a request was sent without a credential")
		return nil, errors.New("unreachable")
	})}
	req, _ := http.NewRequest(http.MethodPost, "https://aiplatform.googleapis.com/v1/x", nil)
	_, err := tr.RoundTrip(req)
	if err == nil || !strings.Contains(err.Error(), "gcloud auth application-default login") {
		t.Errorf("err = %v; it must name the way to log in", err)
	}
}

func TestAnthropicOptionsMintsFromADCOnlyForAVertexDeploymentWithNoToken(t *testing.T) {
	t.Run("direct API", func(t *testing.T) {
		clearAnthropicEnv(t)
		t.Setenv("ANTHROPIC_API_KEY", "k")
		if anthropicOptions().HTTPClient != nil {
			t.Error("the direct API must not get a Google transport")
		}
	})
	t.Run("Vertex with no token", func(t *testing.T) {
		selectVertex(t)
		if anthropicOptions().HTTPClient == nil {
			t.Error("Vertex with no token must mint one from ADC")
		}
	})
	t.Run("Vertex with ANTHROPIC_AUTH_TOKEN", func(t *testing.T) {
		selectVertex(t)
		t.Setenv("ANTHROPIC_AUTH_TOKEN", "tok")
		if anthropicOptions().HTTPClient != nil {
			t.Error("an explicit token must be used as is")
		}
	})
	t.Run("Vertex switched off", func(t *testing.T) {
		selectVertex(t)
		t.Setenv("CLAUDE_CODE_USE_VERTEX", "0")
		t.Setenv("ANTHROPIC_API_KEY", "k")
		if anthropicOptions().HTTPClient != nil {
			t.Error("CLAUDE_CODE_USE_VERTEX=0 must not get a Google transport")
		}
	})
}

func TestCheckCredentialsRefusesVertexWithNoGoogleCredential(t *testing.T) {
	selectVertex(t)
	stubADC(t, nil, errors.New("could not find default credentials"))
	m := &core.Model{ID: "claude-test", API: anthropic.API, Provider: "anthropic"}
	err := CheckCredentials(m)
	if err == nil {
		t.Fatal("the preflight passed with no credential")
	}
	var e *Error
	if !errors.As(err, &e) || e.Category() != CategoryAuth || e.Model != m {
		t.Errorf("err = %#v; want an auth error carrying the model", err)
	}
	if !strings.Contains(err.Error(), "gcloud auth application-default login") {
		t.Errorf("err = %v; it must name the way to log in", err)
	}
}

func TestCheckCredentialsPassesVertexWithADC(t *testing.T) {
	selectVertex(t)
	stubADC(t, static("adc-token"), nil)
	m := &core.Model{ID: "claude-test", API: anthropic.API, Provider: "anthropic"}
	if err := CheckCredentials(m); err != nil {
		t.Fatal(err)
	}
}

// End to end through AgentKit's Anthropic provider: with Vertex selected and
// no token in the environment, the request that reaches Vertex carries the
// ADC token. This is the failure an operator reported: a Google 401
// CREDENTIALS_MISSING from a machine on which Claude Code works.
func TestTheVertexRequestCarriesTheADCToken(t *testing.T) {
	var auth, path atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		path.Store(r.URL.Path)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	selectVertex(t)
	t.Setenv("ANTHROPIC_API_KEY", "leftover-direct-key")
	t.Setenv("ANTHROPIC_VERTEX_BASE_URL", srv.URL)
	stubADC(t, static("adc-token"), nil)

	m := &core.Model{ID: "claude-test", API: anthropic.API, Provider: "anthropic", MaxTokens: 1024, ContextWindow: 200000}
	req := core.Request{Messages: core.Messages{core.UserMessage{Content: core.Content{core.TextBlock{Text: "hi"}}}}}
	p, ok := DefaultProviders().Get(anthropic.API)
	if !ok {
		t.Fatal("no Anthropic provider registered")
	}
	p.Stream(context.Background(), m, req, core.ProviderStreamOptions{}).Result()

	if got, _ := auth.Load().(string); got != "Bearer adc-token" {
		t.Errorf("Authorization = %q, want the ADC token", got)
	}
	if got, _ := path.Load().(string); !strings.Contains(got, "/projects/test-project/") {
		t.Errorf("path = %q, want a Vertex path", got)
	}
}
