package hub

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

var ctx = context.Background()

// pat is the PAT of the workspaces of the tests.
const pat = "pat-SECRET-xyz"

// env is a fake hub with a workspace that pat sees, and a client of it.
type env struct {
	hub    *fakehub.Hub
	client *Client
	slug   string
}

func newEnv(t *testing.T, slug string, w fakehub.Workspace) *env {
	t.Helper()
	h := fakehub.New(t)
	w.Slug, w.PAT = slug, pat
	h.AddWorkspace(t, w)
	return &env{hub: h, client: New(h.URL, "9.9.9"), slug: slug}
}

// path is the path of a route of the workspace, decoded, as the hub matches a fault by it.
func (e *env) path(rest string) string { return "/api/v1/workspaces/" + e.slug + rest }

// answer makes the hub answer the requests with the method for the path with the fault, and nothing
// else that was injected before.
func (e *env) answer(method, path string, f fakehub.Fault) {
	e.hub.Clear()
	e.hub.Inject(method, path, f)
}

// sent is the JSON body of the last request the hub received with the method for the path.
func (e *env) sent(t *testing.T, method, path string) any {
	t.Helper()
	reqs := e.hub.RequestsTo(method, path)
	if len(reqs) == 0 {
		t.Fatalf("no request %s %s", method, path)
	}
	return testutil.Decode[any](t, reqs[len(reqs)-1].Body)
}

// hubError is hub's error object.
func hubError(status int, kind, message string) string {
	return fmt.Sprintf(`{"error":{"code":%d,"message":%q,"error_type":%q}}`, status, message, kind)
}

func isUnavailable(err error) bool {
	_, ok := errors.AsType[*UnavailableError](err)
	return ok
}

// kind names the one class of the error of a call, and fails the test when it is of more than one.
func kind(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		return "ok"
	}
	var found []string
	for name, is := range map[string]bool{
		"refused": errors.Is(err, ErrRefused), "forbidden": errors.Is(err, ErrForbidden), "not found": errors.Is(err, ErrNotFound),
		"rejected": errors.Is(err, ErrRejected), "unavailable": isUnavailable(err),
	} {
		if is {
			found = append(found, name)
		}
	}
	if len(found) != 1 {
		t.Errorf("error %q is of the classes %v, want exactly one", err, found)
		return "?"
	}
	return found[0]
}

// holdsSecret fails the test when err, or an error it wraps, holds the PAT or the URL of the hub.
func holdsSecret(t *testing.T, what string, err error, hubURL string) {
	t.Helper()
	for e := err; e != nil; e = errors.Unwrap(e) {
		text := fmt.Sprintf("%+v %v %#v", e, e.Error(), e)
		if strings.Contains(text, pat) || strings.Contains(text, hubURL) {
			t.Errorf("%s: an error holds the PAT or the URL of the call: %s", what, text)
		}
	}
}

// forgeEnv is an env whose workspace "acme" has the forge repository acme/widgets on a GitHub host,
// with issue 42 (two comments), a pull request 43 and an issue 44, and the forge client of it.
type forgeEnv struct {
	*env
	repo  issuex.Repo
	forge *ForgeClient
}

const forgeHost = "github.example.com"

func newForgeEnv(t *testing.T) *forgeEnv {
	t.Helper()
	e := newEnv(t, "acme", fakehub.Workspace{ForgeURL: "https://" + forgeHost + "/acme/widgets"})
	e.hub.AddIssue("acme", fakehub.Issue{Number: 42, Title: "Nil map", Body: "It panics.", Labels: []string{"af:ready"},
		Comments: []fakehub.Comment{{Author: "bob", Body: "me too"}, {Author: "eve", Body: "same"}}})
	e.hub.AddIssue("acme", fakehub.Issue{Number: 43, Title: "A pull request", IsPR: true})
	e.hub.AddIssue("acme", fakehub.Issue{Number: 44, Title: "Plain"})
	repo := issuex.Repo{Host: forgeHost, Owner: "acme", Name: "widgets"}
	return &forgeEnv{env: e, repo: repo, forge: e.client.Forge(pat, "acme", repo)}
}

func (e *forgeEnv) ref(n int) issuex.IssueRef { return issuex.IssueRef{Repo: e.repo, Number: n} }

// faultPath is the decoded path of a request-URI, without its query, as the hub matches a fault by it.
func faultPath(t *testing.T, uri string) string {
	t.Helper()
	path, _, _ := strings.Cut(uri, "?")
	p, err := url.PathUnescape(path)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
