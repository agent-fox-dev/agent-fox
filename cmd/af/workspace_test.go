package main

import (
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// view is the subscription view of the service, with a field af does not know.
const view = `{"slug":"widgets","token_user":null,"subscribed_by":"u","token_state":"ok","created_at":"2026-10-23T12:00:00Z","updated_at":"2026-10-23T12:00:00Z","future":{"a":[1,{"b":2}]}}`

// brokenReader is a stdin that cannot be read.
type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("the pipe is broken") }

func TestWorkspaceList_GivesTheDocumentAsReceived(t *testing.T) {
	for _, c := range []struct{ body, summary string }{
		{`{"workspaces":[` + view + `],"extra":[true]}`, "1 workspace subscribed"},
		{`{"workspaces":[` + view + `,` + view + `]}`, "2 workspaces subscribed"},
	} {
		serve(t, answer(200, c.body))
		res := af(t, "workspace", "list")
		if res.code != 0 || res.summary() != c.summary || !reflect.DeepEqual(res.result(), value(t, c.body)) {
			t.Errorf("exit %d, summary %q, result %v, want %q and the document", res.code, res.summary(), res.result(), c.summary)
		}
	}
	serve(t, answer(200, `{"jobs":[]}`))
	if res := af(t, "workspace", "list"); res.code != 1 || res.category() != "service" {
		t.Errorf("a document that is not a list: exit %d, category %v, want 1 and service", res.code, res.category())
	}
}

func TestWorkspaceSubscribe_PutsTheTokenAndSaysWhetherItReplaced(t *testing.T) {
	pat := testutil.WriteFile(t, t.TempDir(), "pat", "pat-sentinel\n")
	for _, c := range []struct {
		status        int
		file, summary string
	}{
		{201, pat, "subscribed the workspace widgets"},
		{200, "-", "replaced the subscription of the workspace widgets"},
	} {
		svc := serve(t, routes(map[string]http.HandlerFunc{"PUT /api/v1/workspaces/widgets": answer(c.status, view)}))
		res := afWith(t, t.Context(), strings.NewReader("pat-sentinel\n"), "workspace", "subscribe", "widgets", "--hub-token-file", c.file)
		if res.code != 0 || res.summary() != c.summary || !reflect.DeepEqual(res.result(), value(t, view)) {
			t.Errorf("%d: exit %d, summary %q, result %v, want %q and the view", c.status, res.code, res.summary(), res.result(), c.summary)
		}
		if got := svc.last(t).body(t); !reflect.DeepEqual(got, map[string]any{"hub_token": "pat-sentinel"}) || strings.Contains(res.output(), "pat-sentinel") {
			t.Errorf("%d: body %v, want the token, and an output without it", c.status, got)
		}
	}
}

func TestWorkspaceSubscribe_RefusesABadTokenFileBeforeAnyRequest(t *testing.T) {
	svc := serve(t, nil)
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	for _, c := range []struct {
		name     string
		args     []string
		code     int
		category string
	}{
		{"no flag", []string{"w"}, 2, "usage"},
		{"no slug", []string{"--hub-token-file", testutil.WriteFile(t, dir, "ok", "pat")}, 2, "usage"},
		{"a missing file", []string{"w", "--hub-token-file", missing}, 1, "io"},
		{"an empty file", []string{"w", "--hub-token-file", testutil.WriteFile(t, dir, "empty", "\n")}, 2, "usage"},
		{"two lines", []string{"w", "--hub-token-file", testutil.WriteFile(t, dir, "two", "pat-sentinel\nsecond\n")}, 2, "usage"},
	} {
		res := af(t, append([]string{"workspace", "subscribe"}, c.args...)...)
		if res.code != c.code || res.category() != c.category || strings.Contains(res.output(), "pat-sentinel") {
			t.Errorf("%s: exit %d, category %v, message %q, want %d and %s, never the token", c.name, res.code, res.category(), res.message(), c.code, c.category)
		}
	}
	res := afWith(t, t.Context(), brokenReader{}, "workspace", "subscribe", "w", "--hub-token-file", "-")
	if res.code != 1 || res.category() != "io" {
		t.Errorf("an unreadable stdin: exit %d, category %v, want 1 and io", res.code, res.category())
	}
	if n := len(svc.requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestWorkspaceGetAndUnsubscribe_UseTheEscapedSlug(t *testing.T) {
	slug := "a b/c"
	svc := serve(t, routes(map[string]http.HandlerFunc{
		"GET /api/v1/workspaces/a%20b%2Fc":    answer(200, view),
		"DELETE /api/v1/workspaces/a%20b%2Fc": answer(204, ""),
	}))
	if res := af(t, "workspace", "get", slug); res.code != 0 || !reflect.DeepEqual(res.result(), value(t, view)) {
		t.Errorf("get: exit %d, result %v, want the view as received", res.code, res.result())
	}
	res := af(t, "workspace", "unsubscribe", slug)
	if res.code != 0 || !reflect.DeepEqual(res.result(), map[string]any{"slug": slug}) || !strings.Contains(res.summary(), "open jobs were cancelled") {
		t.Errorf("unsubscribe: exit %d, result %v, summary %q, want the slug and the cancelled jobs", res.code, res.result(), res.summary())
	}
	if got := svc.lines(); !reflect.DeepEqual(got, []string{"GET /api/v1/workspaces/a%20b%2Fc", "DELETE /api/v1/workspaces/a%20b%2Fc"}) {
		t.Errorf("requests %v, want the escaped slug", got)
	}
}

func TestWorkspaceGetAndUnsubscribe_FailAsTheServiceAnswers(t *testing.T) {
	serve(t, answer(404, `{"error":{"code":"not_found","message":"workspace not found"}}`))
	for _, cmd := range []string{"get", "unsubscribe"} {
		if res := af(t, "workspace", cmd); res.code != 2 {
			t.Errorf("%s with no slug: exit %d, want 2", cmd, res.code)
		}
		if res := af(t, "workspace", cmd, "missing"); res.code != 2 || res.category() != "usage" {
			t.Errorf("%s of an unknown workspace: exit %d, category %v, want 2 and usage", cmd, res.code, res.category())
		}
	}
	serve(t, answer(200, `not json`))
	if res := af(t, "workspace", "get", "w"); res.code != 1 || res.category() != "service" {
		t.Errorf("a view that is not JSON: exit %d, category %v, want 1 and service", res.code, res.category())
	}
}
