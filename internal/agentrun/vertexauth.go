package agentrun

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/agentfox/agentkit-go/provider"
	"github.com/agentfox/agentkit-go/provider/anthropic"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// Claude on Vertex AI authenticates with a Google OAuth access token. AgentKit
// selects the deployment and drops an Anthropic key on that path, but it has
// no dependency to mint the token with: it leaves the credential to the
// embedder's transport. This file is that transport, minting the token from
// Application Default Credentials the way Claude Code does, so a machine on
// which `gcloud auth application-default login` makes Claude Code work makes
// agent-fox work too.

// vertexScope is the OAuth scope Vertex AI accepts.
const vertexScope = "https://www.googleapis.com/auth/cloud-platform"

// findADC returns a caching token source for Application Default Credentials.
// It is a variable so a test can stand in for the credential lookup.
var findADC = func(ctx context.Context) (oauth2.TokenSource, error) {
	creds, err := google.FindDefaultCredentials(ctx, vertexScope)
	if err != nil {
		return nil, err
	}
	return oauth2.ReuseTokenSource(nil, creds.TokenSource), nil
}

// vertexNeedsADC reports whether the environment selects Claude on Vertex AI
// and supplies no token of its own.
//
// A token in ANTHROPIC_AUTH_TOKEN or ANTHROPIC_OAUTH_TOKEN is the operator's
// choice and is sent as is; ADC is only the fallback, as it is for Claude
// Code.
func vertexNeedsADC() bool {
	if !anthropic.VertexSelected(provider.Env{}) {
		return false
	}
	return os.Getenv(anthropic.AuthTokenVar) == "" && os.Getenv(anthropic.OAuthTokenVar) == ""
}

// checkVertexADC fails when Claude on Vertex AI is selected with no token and
// no Application Default Credentials can be found.
//
// Without it the preflight passes (AgentKit reports the deployment as an
// ambient credential) and the run dies on its first request with a Google 401
// that names nothing an operator can act on.
func checkVertexADC() error {
	if !vertexNeedsADC() {
		return nil
	}
	if _, err := findADC(context.Background()); err != nil {
		return adcError(err)
	}
	return nil
}

func adcError(err error) error {
	return newError("", CategoryAuth, err,
		"Claude on Vertex AI is selected but no Google credential was found: run "+
			"`gcloud auth application-default login`, set GOOGLE_APPLICATION_CREDENTIALS, or put "+
			"an access token in %s (%v)", anthropic.AuthTokenVar, err)
}

// adcTransport adds an ADC access token to every request that carries no
// Authorization header of its own.
//
// The credential lookup is deferred to the first request and done once; the
// token source refreshes the token before it expires, so a run longer than a
// token's hour keeps working.
type adcTransport struct {
	// base is the round-tripper requests go through; nil means
	// http.DefaultTransport at the time of the request.
	base http.RoundTripper

	once sync.Once
	src  oauth2.TokenSource
	err  error
}

func (t *adcTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if req.Header.Get("Authorization") != "" {
		return base.RoundTrip(req)
	}
	t.once.Do(func() { t.src, t.err = findADC(context.Background()) })
	if t.err != nil {
		return nil, adcError(t.err)
	}
	tok, err := t.src.Token()
	if err != nil {
		return nil, fmt.Errorf("minting a Google access token for Claude on Vertex AI: %w", err)
	}
	// A RoundTripper must not modify the request it was handed.
	r := req.Clone(req.Context())
	tok.SetAuthHeader(r)
	return base.RoundTrip(r)
}

// anthropicOptions configures the Anthropic provider: with an ADC-minting
// transport when the environment selects Claude on Vertex AI and supplies no
// token, and with AgentKit's defaults otherwise.
func anthropicOptions() anthropic.Options {
	if !vertexNeedsADC() {
		return anthropic.Options{}
	}
	return anthropic.Options{HTTPClient: &http.Client{Transport: &adcTransport{}}}
}
