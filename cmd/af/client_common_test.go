package main

import (
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/afclient"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// roundTrip is a transport made of a function.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestServer_TheFlagWinsOverAFURL(t *testing.T) {
	a, b := newService(t, nil), serve(t, nil)
	if res := af(t, "workspace", "list", "--server", a.URL); res.code != 0 || len(a.requests()) != 1 || len(b.requests()) != 0 {
		t.Errorf("with both: exit %d, %d requests to the flag's and %d to AF_URL's, want one to the flag's", res.code, len(a.requests()), len(b.requests()))
	}
	if res := af(t, "workspace", "list"); res.code != 0 || len(b.requests()) != 1 {
		t.Errorf("with AF_URL alone: exit %d, %d requests to it, want one", res.code, len(b.requests()))
	}
}

func TestServer_RefusalsAreUsageErrorsHintingAtTheirSourceAndSendNothing(t *testing.T) {
	svc := serve(t, nil)
	host := strings.TrimPrefix(svc.URL, "http://")
	for _, c := range []struct {
		name, env string
		args      []string
		hint      map[string]any
	}{
		{"no server", "", nil, map[string]any{"flag": "--server", "env": "AF_URL"}},
		{"an empty --server", svc.URL, []string{"--server", ""}, map[string]any{"flag": "--server"}},
		{"a bad --server", "", []string{"--server", "ftp://" + host}, map[string]any{"flag": "--server"}},
		{"a bad AF_URL", "http://u:pw@" + host, nil, map[string]any{"env": "AF_URL"}},
	} {
		t.Setenv("AF_URL", c.env)
		res := af(t, append([]string{"workspace", "list"}, c.args...)...)
		if res.code != 2 || res.category() != "usage" || !reflect.DeepEqual(res.hint(), c.hint) {
			t.Errorf("%s: exit %d, category %v, fix_hint %v, want 2, usage and %v", c.name, res.code, res.category(), res.hint(), c.hint)
		}
	}
	if n := len(svc.requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestServer_PlainHTTPToAnotherMachineWarnsAndGoesOn(t *testing.T) {
	old := httpTransport
	httpTransport = roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"workspaces":[]}`)), Request: r}, nil
	})
	t.Cleanup(func() { httpTransport = old })
	home(t, "token = \"T\"\n")
	for u, warns := range map[string]bool{"http://example.com": true, "https://example.com": false, "http://localhost:1": false} {
		res := af(t, "workspace", "list", "--server", u)
		if res.code != 0 || slices.Contains(res.warnings(), "server_insecure") != warns {
			t.Errorf("%s: exit %d, warnings %v, want server_insecure %v", u, res.code, res.warnings(), warns)
		}
	}
}

func TestCredential_FailuresTakeTheCategoryOfTheirKindAndSendNothing(t *testing.T) {
	svc := newService(t, nil)
	t.Setenv("AF_URL", svc.URL)
	for _, c := range []struct {
		name, content string
		dir           bool
		category      string
		hint          any
	}{
		{"no file", "", false, "auth", map[string]any{"suggest": afclient.CredentialSuggestion}},
		{"no token", "other = 1\n", false, "auth", map[string]any{"suggest": afclient.CredentialSuggestion}},
		{"not TOML", "token = \"SENTINEL\n", false, "config", nil},
		{"a control character", "token = \"SENTINEL\\u0007\"\n", false, "config", nil},
		{"a directory", "", true, "io", nil},
	} {
		path := home(t, c.content)
		if c.dir {
			testutil.WriteFile(t, path, "inside", "")
		}
		res := af(t, "workspace", "list")
		if res.code != 1 || res.category() != c.category || !reflect.DeepEqual(res.hint(), c.hint) {
			t.Errorf("%s: exit %d, category %v, fix_hint %v, want 1, %s and %v", c.name, res.code, res.category(), res.hint(), c.category, c.hint)
		}
		if !strings.Contains(res.message(), path) || strings.Contains(res.output(), "SENTINEL") {
			t.Errorf("%s: message %q, want it to name the file and never the token", c.name, res.message())
		}
	}
	if n := len(svc.requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

// echoing is a service that repeats each request, with its Authorization header and its body, in
// what it answers: the message of an error for a status of 400 or more, else the summary and the
// error of the job, an event and the warnings of the envelope.
func echoing(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		echo := strings.ReplaceAll(fmt.Sprintf("%s %s with %s and %s", r.Method, r.URL.Path, r.Header.Get("Authorization"), body), `"`, `\"`)
		switch {
		case strings.HasSuffix(r.URL.Path, "/events"):
			stream(frame(1), `data: {"seq":2,"job_id":"j","type":"step","data":{"echo":"`+echo+`"}}`+"\n\n")(w, r)
		case strings.Contains(r.URL.Path, "/attempts/"):
			answer(200, `{"status":"done","warnings":[{"code":"echo","message":"`+echo+`"}]}`)(w, r)
		case status >= 400:
			answer(status, `{"error":{"code":"echo","message":"`+echo+`"}}`)(w, r)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/workspaces"):
			answer(200, `{"workspaces":[]}`)(w, r)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/jobs"):
			answer(200, `{"jobs":[],"next_cursor":null}`)(w, r)
		case r.Method == http.MethodPut:
			answer(201, view)(w, r)
		default:
			answer(200, jobJSON("failed", `{"status":"failed","summary":"`+echo+`","error":{"category":"brain","message":"`+echo+`"}}`, "", "failed"))(w, r)
		}
	}
}

func TestRedaction_TheCredentialAndTheWorkspaceTokenReachNoOutput(t *testing.T) {
	const cred, pat = "cred-sentinel-0123456789", "pat-sentinel-9876543210"
	patFile := testutil.WriteFile(t, t.TempDir(), "pat", pat+"\n")
	for _, status := range []int{200, 400, 502} {
		serve(t, echoing(status))
		home(t, "token = \""+cred+"\"\n")
		for _, args := range [][]string{
			{"workspace", "subscribe", "w", "--hub-token-file", patFile}, {"workspace", "list"}, {"workspace", "get", "w"},
			{"workspace", "unsubscribe", "w"}, {"jobs", "list"}, {"jobs", "get", "j"}, {"jobs", "cancel", "j"},
			{"jobs", "retry", "j", "--wait"}, {"jobs", "answer", "j", "text", "--wait"}, {"jobs", "watch", "j"},
			{"jobs", "submit", "triage", "some words", "--workspace", "w", "--wait"},
		} {
			res := af(t, args...)
			if res.stdout == "" || strings.Contains(res.output(), cred) || strings.Contains(res.output(), pat) {
				t.Errorf("%d %v: a secret reached the output:\n%s", status, args, res.output())
			}
		}
	}
}

func TestRequestFailure_IsTheCategoryMessageAndHintOfTheAnswer(t *testing.T) {
	for _, c := range []struct {
		status    int
		code      string
		exit      int
		category  string
		retryable bool
		hint      any
	}{
		{400, "invalid_request", 2, "usage", false, nil},
		{401, "unauthenticated", 1, "auth", false, map[string]any{"suggest": afclient.CredentialSuggestion}},
		{409, "invalid_state", 1, "conflict", false, nil},
		{409, "not_subscribed", 1, "conflict", false, map[string]any{"suggest": "af workspace subscribe <slug> --hub-token-file FILE"}},
		{502, "hub_unavailable", 1, "hub", true, nil},
	} {
		serve(t, answer(c.status, `{"error":{"code":"`+c.code+`","message":"the service says no"}}`))
		res := af(t, "jobs", "cancel", "j")
		if res.code != c.exit || res.category() != c.category || res.retryable() != c.retryable || !reflect.DeepEqual(res.hint(), c.hint) {
			t.Errorf("%d %s: exit %d, category %v, retryable %v, fix_hint %v, want %d, %s, %v, %v",
				c.status, c.code, res.code, res.category(), res.retryable(), res.hint(), c.exit, c.category, c.retryable, c.hint)
		}
		if want := "the service says no (" + c.code + ")"; res.message() != want {
			t.Errorf("%d %s: message %q, want %q", c.status, c.code, res.message(), want)
		}
	}
}
