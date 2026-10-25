package fakehub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/gateway"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// pat is the token of the workspace acme.
const pat = "pat-acme"

// newHub is a hub with the workspace acme of w, whose token is pat.
func newHub(t *testing.T, w Workspace) *Hub {
	t.Helper()
	h := New(t)
	w.Slug, w.PAT = "acme", pat
	h.AddWorkspace(t, w)
	return h
}

// do sends a request to the hub with the bearer token, none when it is empty.
func do(t *testing.T, h *Hub, method, path, token string, body any) testutil.Response {
	t.Helper()
	var headers [][]string
	if token != "" {
		headers = append(headers, testutil.Bearer(token))
	}
	return testutil.Request(t, method, h.URL+path, body, headers...)
}

// try sends a GET with the bearer token and returns the status, or the error of the transport.
func try(h *Hub, path, token string) (int, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, h.URL+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// errorType is the kind of hub's error object {error: {code, message, error_type}} in the answer, whose
// code is the status; it fails the test when the body is not that object.
func errorType(t *testing.T, r testutil.Response) string {
	t.Helper()
	var body struct {
		Error struct {
			Code      int    `json:"code"`
			Message   string `json:"message"`
			ErrorType string `json:"error_type"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(r.Body), &body) != nil || body.Error.Code != r.Status || body.Error.Message == "" || body.Error.ErrorType == "" {
		t.Errorf("answer %d %s, want hub's error object", r.Status, r.Body)
	}
	return body.Error.ErrorType
}

// decode is the JSON of the body.
func decode(t *testing.T, body string) any { return testutil.Decode[any](t, []byte(body)) }

func basic(user, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}

// lease grants a lease of acme and returns its id and token.
func lease(t *testing.T, h *Hub) (id, token string) {
	t.Helper()
	r := do(t, h, "POST", "/api/v1/workspaces/acme/sandboxes", pat, map[string]any{"purpose": "fix"})
	body := r.Object(t)
	id, _ = body["id"].(string)
	token, _ = body["token"].(string)
	if r.Status != http.StatusCreated || id == "" || token == "" {
		t.Fatalf("the lease: %d %s", r.Status, r.Body)
	}
	return id, token
}

// dial dials the gateway of the hub with the token, and closes the provider when the test ends.
func dial(t *testing.T, h *Hub, token string) (*gateway.Provider, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p, err := gateway.Dial(ctx, h.Gateway().URL(), gateway.Options{Token: token})
	if err == nil {
		t.Cleanup(func() { _ = p.Close() })
	}
	return p, err
}

// box creates a box through the provider.
func box(t *testing.T, p sandbox.Provider) (sandbox.Sandbox, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return p.Create(ctx, sandbox.Spec{Labels: map[string]string{"af.job": "job_1"}, Network: sandbox.Network{Mode: sandbox.NetworkUnrestricted}})
}

// execute is the execute tool of the box, as a function of the command.
func execute(t *testing.T, sb sandbox.Sandbox) func(command string) core.ToolResult {
	t.Helper()
	tools, err := sb.Tools(context.Background(), sandbox.ToolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools {
		if tool.Name == "execute" {
			return func(command string) core.ToolResult {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				return tool.Execute(ctx, testutil.JSON(t, map[string]string{"command": command}))
			}
		}
	}
	t.Fatal("the box has no execute tool")
	return nil
}
