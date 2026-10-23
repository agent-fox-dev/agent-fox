package fakehub

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/gateway"
)

const pat = "pat-for-the-worker"

// call makes a request to the hub with the bearer token and returns the status and the decoded
// body.
func call(t *testing.T, h *Hub, method, path, token string, body any) (int, any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, h.URL+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out any
	_ = json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

func worker(t *testing.T) *Hub {
	t.Helper()
	h := New(t)
	h.AddWorkspace(t, Workspace{
		Slug: "acme", PAT: pat, Vars: map[string]string{"AF_MODEL": "m1"},
		Files: map[string]string{"README.md": "hello\n"},
	})
	h.AddIssue("acme", Issue{Number: 42, Title: "Nil map", Body: "It panics.", Labels: []string{"af:ready"}})
	h.AddIssue("acme", Issue{Number: 43, Title: "A pull request", IsPR: true})
	return h
}

// TestFakehubServesTheEndpointsOfTheWorkerAndItsGitServerTakesOnlyTheBasicPAT_TS_13_80 verifies
// TS-13-80: the endpoints serve what a test set and are recorded and counted, failures are
// injected by route, the git server takes only Authorization: Basic agent-fox:<PAT>, and a lease
// points at an in-process gateway that records the specs it creates.
// Verifies: 13-REQ-10.7
func TestFakehubServesTheEndpointsOfTheWorkerAndItsGitServerTakesOnlyTheBasicPAT_TS_13_80(t *testing.T) {
	h := worker(t)

	t.Run("the endpoints serve the state that was set and every request is recorded", func(t *testing.T) {
		status, body := call(t, h, "GET", "/api/v1/workspaces/acme", pat, nil)
		rec, _ := body.(map[string]any)
		if status != 200 || rec["workspace_mode"] != "standard" || rec["branch"] != "main" || rec["git_url"] != "https://github.example.com/acme/widgets" || rec["hub_url"] != h.CloneURL("acme") {
			t.Errorf("the record: %d %v", status, body)
		}
		status, body = call(t, h, "GET", "/api/v1/workspaces/acme/vars/resolved", pat, nil)
		vars, _ := body.([]any)
		if status != 200 || len(vars) != 1 || vars[0].(map[string]any)["key"] != "AF_MODEL" || vars[0].(map[string]any)["value"] != "m1" {
			t.Errorf("the variables: %d %v", status, body)
		}
		status, body = call(t, h, "GET", "/api/v1/workspaces/acme/issues/42", pat, nil)
		thread, _ := body.(map[string]any)
		if issue, _ := thread["issue"].(map[string]any); status != 200 || issue["title"] != "Nil map" || issue["is_pr"] != false {
			t.Errorf("the issue: %d %v", status, body)
		}
		if _, body = call(t, h, "GET", "/api/v1/workspaces/acme/issues/43", pat, nil); body.(map[string]any)["issue"].(map[string]any)["is_pr"] != true {
			t.Errorf("a pull request is not marked: %v", body)
		}
		if status, _ = call(t, h, "GET", "/api/v1/workspaces/acme/issues/99", pat, nil); status != 404 {
			t.Errorf("an issue that is not there: %d", status)
		}

		call(t, h, "POST", "/api/v1/workspaces/acme/issues/42/labels", pat, []string{"af:in-progress"})
		call(t, h, "DELETE", "/api/v1/workspaces/acme/issues/42/labels/af:ready", pat, nil)
		call(t, h, "POST", "/api/v1/workspaces/acme/issues/42/comments", pat, map[string]string{"body": "a comment"})
		call(t, h, "PATCH", "/api/v1/workspaces/acme/issues/42", pat, map[string]string{"body": "a new body"})
		call(t, h, "POST", "/api/v1/workspaces/acme/pulls", pat, map[string]any{"title": "Fix", "head": "fix/42-nil-map", "base": "main", "draft": false})
		issue, _ := h.IssueOf("acme", 42)
		if !slices.Equal(issue.Labels, []string{"af:in-progress"}) || len(issue.Comments) != 1 || issue.Comments[0].Body != "a comment" || issue.Body != "a new body" {
			t.Errorf("the issue after the writes: %+v", issue)
		}
		if pulls := h.Pulls("acme"); len(pulls) != 1 || pulls[0].Head != "fix/42-nil-map" {
			t.Errorf("pulls = %+v", pulls)
		}

		status, body = call(t, h, "POST", "/api/v1/sessions", pat, map[string]string{"workspace_slug": "acme", "archetype": "fix", "model": "m1", "run_id": "20250102_030405_abc123"})
		id, _ := body.(map[string]any)["id"].(string)
		if status != 201 || id == "" {
			t.Fatalf("a session: %d %v", status, body)
		}
		call(t, h, "POST", "/api/v1/sessions/"+id+"/usage", pat, map[string]any{"model": "m1", "input_tokens": 5, "output_tokens": 6})
		call(t, h, "POST", "/api/v1/workspaces/acme/runs/20250102_030405_abc123/events/batch", pat, []map[string]any{{"event_type": "step", "payload": map[string]any{}}})
		if status, _ = call(t, h, "POST", "/api/v1/sessions/"+id+"/complete", pat, map[string]string{"status": "bogus"}); status != 400 {
			t.Errorf("a status that hub does not take: %d", status)
		}
		call(t, h, "POST", "/api/v1/sessions/"+id+"/complete", pat, map[string]string{"status": "completed"})
		sessions := h.Sessions()
		if len(sessions) != 1 || sessions[0].Status != "completed" || len(sessions[0].Usage) != 1 || sessions[0].Usage[0].OutputTokens != 6 || sessions[0].RunID != "20250102_030405_abc123" {
			t.Errorf("sessions = %+v", sessions)
		}
		if batches := h.EventBatches("acme", "20250102_030405_abc123"); len(batches) != 1 || batches[0][0].Type != "step" {
			t.Errorf("event batches = %+v", batches)
		}
		if status, _ = call(t, h, "POST", "/api/v1/workspaces/acme/branches/nothing/push", pat, nil); status != 404 {
			t.Errorf("the push of a branch that is not there: %d", status)
		}

		if got := len(h.RequestsTo("POST", "/api/v1/sessions")); got != 1 {
			t.Errorf("%d requests to open a session are recorded, want 1", got)
		}
		if h.Total() == 0 || h.CountFor(TokenHash(pat)) != h.Total() {
			t.Errorf("%d requests, %d with the PAT: every one of them has it", h.Total(), h.CountFor(TokenHash(pat)))
		}
		for _, r := range h.Requests() {
			if r.Seq == 0 {
				t.Errorf("request %s %s has no sequence number", r.Method, r.Path)
			}
		}
		if status, _ = call(t, h, "GET", "/api/v1/workspaces/acme", "someone-else", nil); status != 401 {
			t.Errorf("an unknown token: %d, want 401", status)
		}
		h.AddUser("other", "u2", nil)
		if status, _ = call(t, h, "GET", "/api/v1/workspaces/acme/vars/resolved", "other", nil); status != 404 {
			t.Errorf("a token that does not see the workspace: %d, want 404", status)
		}
	})

	t.Run("failures are injected by route and method", func(t *testing.T) {
		h.Inject("POST", "/api/v1/sessions", Fault{Status: 503})
		if status, _ := call(t, h, "POST", "/api/v1/sessions", pat, map[string]string{"workspace_slug": "acme"}); status != 503 {
			t.Errorf("the injected route: %d, want 503", status)
		}
		if status, _ := call(t, h, "GET", "/api/v1/workspaces/acme", pat, nil); status != 200 {
			t.Errorf("another route: %d, want 200", status)
		}
		h.Clear()
		h.Inject("GET", "/api/v1/workspaces/acme", Fault{Status: 502, Once: true})
		if status, _ := call(t, h, "GET", "/api/v1/workspaces/acme", pat, nil); status != 502 {
			t.Errorf("once: %d, want 502", status)
		}
		if status, _ := call(t, h, "GET", "/api/v1/workspaces/acme", pat, nil); status != 200 {
			t.Errorf("after once: %d, want 200", status)
		}
		h.Inject("GET", "/api/v1/workspaces/acme", Fault{Delay: 60 * time.Millisecond})
		start := time.Now()
		if status, _ := call(t, h, "GET", "/api/v1/workspaces/acme", pat, nil); status != 200 || time.Since(start) < 60*time.Millisecond {
			t.Errorf("a delay: %d after %v, want 200 after 60ms", status, time.Since(start))
		}
		h.Clear()

		h.Inject("GET", "/api/v1/workspaces/acme/issues/42", Fault{Status: 500})
		if status, _ := call(t, h, "GET", "/api/v1/workspaces/acme/issues/42", pat, nil); status != 500 {
			t.Errorf("the method of the rule: %d, want 500", status)
		}
		if status, _ := call(t, h, "PATCH", "/api/v1/workspaces/acme/issues/42", pat, map[string]string{"title": "t"}); status != 200 {
			t.Errorf("another method of the same route: %d, want 200", status)
		}
		h.Clear()
	})

	t.Run("the git server takes only the Basic credential of the PAT", func(t *testing.T) {
		url := h.CloneURL("acme") + "/info/refs?service=git-upload-pack"
		basic := func(user, password string) string {
			return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
		}
		for name, tc := range map[string]struct {
			header string
			want   int
		}{
			"no credential":  {"", 401},
			"a bearer token": {"Bearer " + pat, 401},
			"a wrong user":   {basic("someone", pat), 401},
			"a wrong PAT":    {basic("agent-fox", "not-the-pat"), 401},
			"an empty PAT":   {basic("agent-fox", ""), 401},
			"the credential": {basic("agent-fox", pat), 200},
		} {
			req, _ := http.NewRequest("GET", url, nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Errorf("%s: %d, want %d", name, resp.StatusCode, tc.want)
			}
		}
		// A workspace with no PAT has no credential that opens its git server.
		h.AddWorkspace(t, Workspace{Slug: "open", Files: map[string]string{"a": "b"}})
		open, _ := http.NewRequest("GET", h.CloneURL("open")+"/info/refs?service=git-upload-pack", nil)
		open.Header.Set("Authorization", basic("agent-fox", ""))
		if resp, err := http.DefaultClient.Do(open); err != nil || resp.StatusCode != 401 {
			t.Errorf("the empty credential at a workspace with no PAT: %v, %v, want 401", resp, err)
		}
		var accepted, refused int
		for _, entry := range h.GitRequests() {
			switch {
			case entry.Status == 200 && entry.Authorization == basic("agent-fox", pat):
				accepted++
			case entry.Status == 401:
				refused++
			}
		}
		if accepted != 1 || refused != 6 {
			t.Errorf("the git server accepted %d requests and refused %d, want 1 and 6: %+v", accepted, refused, h.GitRequests())
		}

		// A clone with the header git sends for it works, and one with a bearer token does not.
		dir := t.TempDir()
		cloneInto := func(target, header string) error {
			cmd := exec.Command("git", "-c", "http.extraHeader=Authorization: "+header, "clone", "-q", h.CloneURL("acme"), target)
			cmd.Env = append(os.Environ(), "HOME="+dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Logf("git clone: %s\n%+v", out, h.GitRequests())
			}
			return err
		}
		if err := cloneInto(filepath.Join(dir, "ok"), basic("agent-fox", pat)); err != nil {
			t.Errorf("a clone with the Basic credential: %v", err)
		} else if data, err := os.ReadFile(filepath.Join(dir, "ok", "README.md")); err != nil || string(data) != "hello\n" {
			t.Errorf("the clone holds %q (%v)", data, err)
		}
		if err := cloneInto(filepath.Join(dir, "bad"), "Bearer "+pat); err == nil {
			t.Error("a clone with a bearer token worked")
		}
	})

	t.Run("a lease points at an in-process gateway whose backend records the specs", func(t *testing.T) {
		status, body := call(t, h, "POST", "/api/v1/workspaces/acme/sandboxes", pat, map[string]any{"purpose": "fix", "labels": map[string]string{"job": "job_1", "attempt": "1"}})
		lease, _ := body.(map[string]any)
		if status != 201 || !strings.HasPrefix(lease["id"].(string), "sbx_") || !strings.HasPrefix(lease["token"].(string), "sbl_") || lease["gateway_url"] != h.Gateway().URL() {
			t.Fatalf("a lease: %d %v", status, body)
		}
		if status, _ := call(t, h, "POST", "/api/v1/workspaces/acme/sandboxes", pat, map[string]any{"ttl": "1h"}); status != 400 {
			t.Errorf("a lease with a field hub does not take: %d, want 400", status)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := gateway.Dial(ctx, h.Gateway().URL(), gateway.Options{Token: "sbl_not-a-lease-token"}); err == nil {
			t.Error("the gateway took a token that is no lease's")
		}
		provider, err := gateway.Dial(ctx, lease["gateway_url"].(string), gateway.Options{Token: lease["token"].(string)})
		if err != nil {
			t.Fatalf("dialling the gateway with the token of the lease: %v", err)
		}
		defer provider.Close()
		sb, err := provider.Create(ctx, sandbox.Spec{Labels: map[string]string{"af.run_id": "r", "af.job": "job_1"}, Network: sandbox.Network{Mode: sandbox.NetworkUnrestricted}})
		if err != nil {
			t.Fatalf("creating a box: %v", err)
		}
		created := h.Gateway().Created()
		if len(created) != 1 || created[0].Spec.Labels["af.job"] != "job_1" || len(created[0].Spec.Env) != 0 || created[0].Lease != lease["id"] {
			t.Errorf("the backend recorded %+v", created)
		}
		if err := sb.Close(ctx); err != nil {
			t.Errorf("closing the box: %v", err)
		}
		closed := h.Gateway().Closed()
		if len(closed) != 1 || closed[0].ID != created[0].ID || closed[0].Seq <= created[0].Seq {
			t.Errorf("closed = %+v, want the box, after its creation", closed)
		}

		if status, _ := call(t, h, "DELETE", "/api/v1/workspaces/acme/sandboxes/"+lease["id"].(string), pat, nil); status != 200 {
			t.Errorf("the release: %d", status)
		}
		leases := h.Leases()
		if len(leases) != 1 || !leases[0].Released || leases[0].ReleasedSeq <= closed[0].Seq || leases[0].Labels["job"] != "job_1" {
			t.Errorf("leases = %+v", leases)
		}
		if _, err := gateway.Dial(ctx, h.Gateway().URL(), gateway.Options{Token: lease["token"].(string)}); err == nil {
			t.Error("the gateway took the token of a lease that was released")
		}
		if status, _ := call(t, h, "DELETE", "/api/v1/workspaces/acme/sandboxes/sbx_00000000000000000000000000000000", pat, nil); status != 404 {
			t.Errorf("the release of a lease that is not there: %d, want 404", status)
		}

		h.SetLeaseTTL(90 * time.Second)
		_, body = call(t, h, "POST", "/api/v1/workspaces/acme/sandboxes", pat, map[string]any{})
		expires, err := time.Parse(time.RFC3339, body.(map[string]any)["expires_at"].(string))
		if err != nil || time.Until(expires) > 90*time.Second || time.Until(expires) < 80*time.Second {
			t.Errorf("expires_at = %v (%v), want about 90s from now", expires, err)
		}
	})

	t.Run("the hub records the branch pushes", func(t *testing.T) {
		if status, _ := call(t, h, "POST", "/api/v1/workspaces/acme/branches/main/push", pat, nil); status != 200 {
			t.Errorf("the push of main: %d", status)
		}
		if pushes := h.BranchPushes(); len(pushes) < 1 || pushes[len(pushes)-1].Branch != "main" || pushes[len(pushes)-1].Slug != "acme" {
			t.Errorf("pushes = %+v", pushes)
		}
	})
}

// TestTheGitServerAppliesTheFaultsInjectedForItsRepository checks the harness: a fault injected
// for the repository's root applies to every request under it, before the credential is looked
// at, is recorded with the status it answered, and goes away with Clear.
func TestTheGitServerAppliesTheFaultsInjectedForItsRepository(t *testing.T) {
	h := worker(t)
	root := "/git/acme.git"
	get := func() (int, error) {
		req, _ := http.NewRequest("GET", h.CloneURL("acme")+"/info/refs?service=git-upload-pack", nil)
		req.SetBasicAuth(GitUser, pat)
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		return resp.StatusCode, nil
	}

	for _, status := range []int{401, 403, 503} {
		h.Inject("", root, Fault{Status: status})
		if got, err := get(); err != nil || got != status {
			t.Errorf("a fault of %d answered %d, %v", status, got, err)
		}
	}
	if log := h.GitRequests(); len(log) != 3 || log[2].Status != 503 {
		t.Errorf("the requests of the git server = %+v, want the three that were refused, with their statuses", log)
	}

	h.Inject("", root, Fault{Hangup: true})
	if got, err := get(); err == nil {
		t.Errorf("a hangup answered %d, want the connection closed", got)
	}

	h.Inject("", root, Fault{Status: 503, Once: true})
	if got, _ := get(); got != 503 {
		t.Errorf("a fault that applies once answered %d the first time", got)
	}
	h.Clear()
	if got, err := get(); err != nil || got != 200 {
		t.Errorf("after Clear the git server answered %d, %v, want 200", got, err)
	}

	h.Inject("", root, Fault{Delay: 200 * time.Millisecond})
	start := time.Now()
	if got, err := get(); err != nil || got != 200 || time.Since(start) < 200*time.Millisecond {
		t.Errorf("a delayed answer was %d, %v after %v, want 200 after at least 200ms", got, err, time.Since(start))
	}
}

// TestAFaultForARouteWithAWildcardAppliesToEveryPathUnderIt checks the harness: a rule whose path
// ends in "/*" applies to the paths under it, which is how a test fails the release of a lease
// whose id it does not know, and a rule for the exact path wins when it was added last.
func TestAFaultForARouteWithAWildcardAppliesToEveryPathUnderIt(t *testing.T) {
	h := worker(t)
	_, body := call(t, h, "POST", "/api/v1/workspaces/acme/sandboxes", pat, map[string]any{})
	id := body.(map[string]any)["id"].(string)

	h.Inject("DELETE", "/api/v1/workspaces/acme/sandboxes/*", Fault{Status: 500})
	if status, _ := call(t, h, "DELETE", "/api/v1/workspaces/acme/sandboxes/"+id, pat, nil); status != 500 {
		t.Errorf("the release under a wildcard rule: %d, want 500", status)
	}
	if status, _ := call(t, h, "POST", "/api/v1/workspaces/acme/sandboxes", pat, map[string]any{}); status != 201 {
		t.Errorf("another method under the same prefix: %d, want 201", status)
	}
	if leases := h.Leases(); leases[0].Released {
		t.Error("a release that failed released the lease")
	}
	h.Inject("DELETE", "/api/v1/workspaces/acme/sandboxes/"+id, Fault{Status: 502})
	if status, _ := call(t, h, "DELETE", "/api/v1/workspaces/acme/sandboxes/"+id, pat, nil); status != 502 {
		t.Errorf("a rule for the exact path added after the wildcard: %d, want 502", status)
	}
	h.Clear()
	if status, _ := call(t, h, "DELETE", "/api/v1/workspaces/acme/sandboxes/"+id, pat, nil); status != 200 {
		t.Errorf("after Clear: %d, want 200", status)
	}
}

// TestTheGatewayCanBeStoppedAndMadeToRefuseBoxes checks the harness: a stopped gateway cannot
// be dialled, and one that refuses boxes answers the creation of a box with the reason, and
// records none.
func TestTheGatewayCanBeStoppedAndMadeToRefuseBoxes(t *testing.T) {
	h := worker(t)
	_, body := call(t, h, "POST", "/api/v1/workspaces/acme/sandboxes", pat, map[string]any{})
	lease := body.(map[string]any)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dial := func() (*gateway.Provider, error) {
		return gateway.Dial(ctx, lease["gateway_url"].(string), gateway.Options{Token: lease["token"].(string)})
	}

	h.Gateway().RefuseBoxes("no room for another box")
	provider, err := dial()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Create(ctx, sandbox.Spec{Network: sandbox.Network{Mode: sandbox.NetworkUnrestricted}}); err == nil || !strings.Contains(err.Error(), "no room for another box") {
		t.Errorf("creating a box on a gateway that refuses: %v, want the reason", err)
	}
	if n := len(h.Gateway().Created()); n != 0 {
		t.Errorf("%d boxes were recorded, want none", n)
	}
	provider.Close()

	h.Gateway().RefuseBoxes("")
	provider, err = dial()
	if err != nil {
		t.Fatal(err)
	}
	if sb, err := provider.Create(ctx, sandbox.Spec{Network: sandbox.Network{Mode: sandbox.NetworkUnrestricted}}); err != nil {
		t.Errorf("creating a box once the gateway takes them again: %v", err)
	} else {
		_ = sb.Close(ctx)
	}
	provider.Close()

	h.Gateway().Stop()
	if p, err := dial(); err == nil {
		// A dial that is lazy fails when the box is asked for.
		if _, err = p.Create(ctx, sandbox.Spec{Network: sandbox.Network{Mode: sandbox.NetworkUnrestricted}}); err == nil {
			t.Error("a box was created on a gateway that is stopped")
		}
		p.Close()
	}
}

// TestAFaultCanLetTheFirstRequestsThrough checks the harness: a fault with a Skip lets that many
// requests of its route through, and applies to the ones after them.
func TestAFaultCanLetTheFirstRequestsThrough(t *testing.T) {
	h := worker(t)
	h.Inject("GET", "/api/v1/workspaces/acme/issues/42", Fault{Status: 429, Skip: 2})
	var got []int
	for range 4 {
		status, _ := call(t, h, "GET", "/api/v1/workspaces/acme/issues/42", pat, nil)
		got = append(got, status)
	}
	if !slices.Equal(got, []int{200, 200, 429, 429}) {
		t.Errorf("statuses = %v, want two requests let through and the rest failed", got)
	}
}

// TestAnInstrumentedGatewayRecordsTheCommandsAndCanBeKilledOnOrAfterOne checks the harness: the
// commands that a box runs through an instrumented provider are recorded in order, a gateway that
// is killed on a command goes before the command runs, and one that is killed after it goes once
// the command has returned its result.
func TestAnInstrumentedGatewayRecordsTheCommandsAndCanBeKilledOnOrAfterOne(t *testing.T) {
	h := worker(t)
	_, body := call(t, h, "POST", "/api/v1/workspaces/acme/sandboxes", pat, map[string]any{})
	lease := body.(map[string]any)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	provider, err := gateway.Dial(ctx, lease["gateway_url"].(string), gateway.Options{Token: lease["token"].(string)})
	if err != nil {
		t.Fatal(err)
	}
	instrumented := h.Gateway().Instrument(provider)
	sb, err := instrumented.Create(ctx, sandbox.Spec{Network: sandbox.Network{Mode: sandbox.NetworkUnrestricted}})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := sb.Tools(ctx, sandbox.ToolOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var execute func(ctx context.Context, raw json.RawMessage) string
	for _, tool := range tools {
		if tool.Name == "execute" {
			execute = func(ctx context.Context, raw json.RawMessage) string {
				return fmt.Sprint(tool.Execute(ctx, raw))
			}
		}
	}
	if execute == nil {
		t.Fatal("the box has no execute tool")
	}

	_ = execute(ctx, json.RawMessage(`{"command":"echo first"}`))
	_ = execute(ctx, json.RawMessage(`{"command":"echo second"}`))
	commands := h.Gateway().Commands()
	if len(commands) != 2 || !strings.Contains(commands[0].Command, "first") || !strings.Contains(commands[1].Command, "second") || commands[1].Seq <= commands[0].Seq {
		t.Fatalf("commands = %+v, want the two, in order", commands)
	}

	h.Gateway().KillAfter("third")
	result := execute(ctx, json.RawMessage(`{"command":"echo third"}`))
	if !strings.Contains(result, "third") {
		t.Errorf("the command that the gateway was killed after returned %q, want its result", result)
	}
	if _, err := gateway.Dial(ctx, lease["gateway_url"].(string), gateway.Options{Token: lease["token"].(string)}); err == nil {
		if _, err := instrumented.Create(ctx, sandbox.Spec{Network: sandbox.Network{Mode: sandbox.NetworkUnrestricted}}); err == nil {
			t.Error("a box was made on a gateway that was killed after a command")
		}
	}
}

// TestTheFakeHubListsLabelledLeasesTakesSeededOnesKeepsSessionStateAndCanKillASandbox_TS_14_61
// verifies TS-14-61: GET …/sandboxes lists the leases that are held with their id, labels, purpose and
// end, the ones a test seeded among them; a lease that was deleted is observable as deleted and is
// absent from the next listing; the sessions keep what was opened, posted and completed; and a sandbox
// that was killed answers as gone to the gateway while its lease is still held.
// Verifies: 14-REQ-10.3
func TestTheFakeHubListsLabelledLeasesTakesSeededOnesKeepsSessionStateAndCanKillASandbox_TS_14_61(t *testing.T) {
	h := worker(t)
	seeded := []Lease{
		h.SeedLease("acme", map[string]string{"job": "job_1", "attempt": "1"}, "fix"),
		h.SeedLease("acme", map[string]string{"job": "job_unknown", "attempt": "2"}, "triage"),
		h.SeedLease("acme", nil, "fix"),
		h.SeedLease("acme", map[string]string{"job": "job_2", "attempt": "1"}, "fix"),
	}

	t.Run("the listing carries id, labels, purpose and end of the leases that are held", func(t *testing.T) {
		status, body := call(t, h, "GET", "/api/v1/workspaces/acme/sandboxes", pat, nil)
		list, _ := body.([]any)
		if status != 200 || len(list) != 4 {
			t.Fatalf("GET sandboxes = %d with %d leases, want 200 with 4: %v", status, len(list), body)
		}
		byID := map[string]map[string]any{}
		for _, item := range list {
			m := item.(map[string]any)
			byID[m["id"].(string)] = m
		}
		for _, l := range seeded {
			m := byID[l.ID]
			if m == nil {
				t.Fatalf("the listing lacks %s: %v", l.ID, list)
			}
			labels, _ := m["labels"].(map[string]any)
			if m["purpose"] != l.Purpose || len(labels) != len(l.Labels) || m["expires_at"] == "" || m["expires_at"] == nil {
				t.Errorf("lease %s listed as %v, want purpose %q, labels %v and an end", l.ID, m, l.Purpose, l.Labels)
			}
			for k, v := range l.Labels {
				if labels[k] != v {
					t.Errorf("lease %s: label %s = %v, want %s", l.ID, k, labels[k], v)
				}
			}
		}
		if status, _ := call(t, h, "GET", "/api/v1/workspaces/acme/sandboxes", "no-such-token", nil); status != 401 {
			t.Errorf("the listing for an unknown token = %d, want 401", status)
		}
	})

	t.Run("a lease that was deleted is observable as deleted and is not listed again", func(t *testing.T) {
		if status, _ := call(t, h, "DELETE", "/api/v1/workspaces/acme/sandboxes/"+seeded[0].ID, pat, nil); status/100 != 2 {
			t.Fatalf("DELETE = %d", status)
		}
		if status, _ := call(t, h, "DELETE", "/api/v1/workspaces/acme/sandboxes/sbx_00000000000000000000000000000000", pat, nil); status != 404 {
			t.Errorf("DELETE of a lease that is not there = %d, want 404", status)
		}
		if got := h.Deleted(); !slices.Equal(got, []string{seeded[0].ID}) {
			t.Errorf("Deleted() = %v, want only %s", got, seeded[0].ID)
		}
		_, body := call(t, h, "GET", "/api/v1/workspaces/acme/sandboxes", pat, nil)
		list, _ := body.([]any)
		if len(list) != 3 {
			t.Errorf("the listing after the delete holds %d leases, want 3", len(list))
		}
		for _, item := range list {
			if item.(map[string]any)["id"] == seeded[0].ID {
				t.Errorf("the deleted lease %s is still listed", seeded[0].ID)
			}
		}
	})

	t.Run("the sessions keep what was opened, posted and completed", func(t *testing.T) {
		_, body := call(t, h, "POST", "/api/v1/sessions", pat, map[string]any{"workspace_slug": "acme", "archetype": "fix", "model": "m1", "run_id": "20261022_101010_abcdef"})
		id := body.(map[string]any)["id"].(string)
		if s := h.Sessions(); len(s) != 1 || s[0].ID != id || s[0].Status != "open" || s[0].RunID != "20261022_101010_abcdef" {
			t.Errorf("sessions after the open = %+v, want one open session of the run", s)
		}
		call(t, h, "POST", "/api/v1/sessions/"+id+"/usage", pat, map[string]any{"model": "m1", "input_tokens": 10, "output_tokens": 5, "cost_usd": 0.5, "brain": "claude", "turns": 3})
		call(t, h, "POST", "/api/v1/sessions/"+id+"/complete", pat, map[string]any{"status": "completed"})
		s := h.Sessions()
		if len(s) != 1 || len(s[0].Usage) != 1 || s[0].Usage[0].CostUSD != 0.5 || s[0].Status != "completed" || s[0].CompletedSeq == 0 {
			t.Errorf("sessions after the report = %+v, want the usage and the completion", s)
		}
	})

	t.Run("a killed sandbox answers as gone to the gateway, and its lease is still held", func(t *testing.T) {
		lease := seeded[1]
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		provider, err := gateway.Dial(ctx, h.Gateway().URL(), gateway.Options{Token: lease.Token})
		if err != nil {
			t.Fatal(err)
		}
		defer provider.Close()
		sb, err := provider.Create(ctx, sandbox.Spec{Network: sandbox.Network{Mode: sandbox.NetworkUnrestricted}})
		if err != nil {
			t.Fatal(err)
		}
		tools, err := sb.Tools(ctx, sandbox.ToolOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var execute func(string) core.ToolResult
		for _, tool := range tools {
			if tool.Name == "execute" {
				execute = func(command string) core.ToolResult {
					return tool.Execute(ctx, json.RawMessage(fmt.Sprintf(`{"command":%q}`, command)))
				}
			}
		}
		if r := execute("echo alive"); !r.OK {
			t.Fatalf("a command in a box that was not killed: %+v", r)
		}

		h.KillSandbox(lease.ID)

		if r := execute("echo alive"); r.OK || r.Error != "sandbox_unavailable" {
			t.Errorf("a command in the killed box = %+v, want the tool error sandbox_unavailable", r)
		}
		if _, err := sb.Sync(ctx, sandbox.Tar{R: strings.NewReader(""), Path: "."}); !errors.Is(err, sandbox.ErrUnavailable) {
			t.Errorf("a sync to the killed box = %v, want sandbox.ErrUnavailable", err)
		}
		for _, l := range h.Leases() {
			if l.ID == lease.ID && l.Released {
				t.Error("the killed sandbox's lease was released: only the box is gone")
			}
		}
	})
}
