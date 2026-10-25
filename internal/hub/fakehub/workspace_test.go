package fakehub

import (
	"reflect"
	"strings"
	"testing"
)

func TestWorkspaceRecord_IsTheRecordOfTheWorkspaceAsItIsNow(t *testing.T) {
	h := newHub(t, Workspace{})
	want := map[string]any{
		"slug": "acme", "git_url": "https://github.example.com/acme/widgets", "hub_url": h.URL + "/git/acme.git", "branch": "main",
		"workspace_mode": "standard", "status": "active", "clone_status": "ready",
	}
	if got := do(t, h, "GET", "/api/v1/workspaces/acme", pat, nil).Object(t); !reflect.DeepEqual(got, want) {
		t.Errorf("the record = %v, want %v", got, want)
	}
	h.UpdateWorkspace("acme", func(w *Workspace) {
		w.Branch, w.Mode, w.ForgeURL = "trunk", "carry_patch", "https://gitlab.example.com/g/p"
	})
	h.UpdateWorkspace("missing", func(*Workspace) { t.Error("a workspace that is not there was changed") })
	got := do(t, h, "GET", "/api/v1/workspaces/acme", pat, nil).Object(t)
	if got["branch"] != "trunk" || got["workspace_mode"] != "carry_patch" || got["git_url"] != "https://gitlab.example.com/g/p" {
		t.Errorf("the record after the change = %v", got)
	}
	if h.CloneURL("a/b") != h.URL+"/git/a%2Fb.git" || h.GitDir("acme") != "" || h.GitDir("missing") != "" {
		t.Errorf("CloneURL %s, GitDir %q: want the escaped slug and no repository", h.CloneURL("a/b"), h.GitDir("acme"))
	}
}

func TestResolvedVars_AreServedInTheFormOfTheWorkspace(t *testing.T) {
	vars := map[string]string{"B": "2", "A": "1"}
	for form, want := range map[string]any{
		"":         []any{map[string]any{"key": "A", "value": "1", "origin": "workspace"}, map[string]any{"key": "B", "value": "2", "origin": "workspace"}},
		"namelist": []any{map[string]any{"name": "A", "value": "1"}, map[string]any{"name": "B", "value": "2"}},
		"object":   map[string]any{"vars": map[string]any{"A": "1", "B": "2"}},
	} {
		h := newHub(t, Workspace{Vars: vars, VarsForm: form})
		if r := do(t, h, "GET", "/api/v1/workspaces/acme/vars/resolved", pat, nil); r.Status != 200 || !reflect.DeepEqual(decode(t, r.Body), want) {
			t.Errorf("form %q: %d %s, want %v", form, r.Status, r.Body, want)
		}
	}
	h := newHub(t, Workspace{Vars: vars})
	h.UpdateWorkspace("acme", func(w *Workspace) { w.Vars = map[string]string{"C": "3"} })
	if r := do(t, h, "GET", "/api/v1/workspaces/acme/vars/resolved", pat, nil); !strings.Contains(r.Body, `"C"`) || strings.Contains(r.Body, `"A"`) {
		t.Errorf("the variables after the change: %s", r.Body)
	}
}

// Every route of a workspace takes only a token that sees it, and only the label route serves a
// slug that was granted and never added.
func TestWorkspaceRoutes_TakeOnlyATokenThatSeesTheWorkspace(t *testing.T) {
	h := newHub(t, Workspace{})
	h.AddUser("other", "usr_2", []string{"granted"})
	routes := [][2]string{
		{"GET", "/vars/resolved"}, {"POST", "/runs/r1/events/batch"}, {"POST", "/sandboxes"}, {"GET", "/sandboxes"},
		{"DELETE", "/sandboxes/sbx_1"}, {"POST", "/branches/main/push"}, {"GET", "/issues"}, {"POST", "/labels"}, {"GET", "/nowhere"},
	}
	for _, r := range routes {
		for token, want := range map[string]int{"unknown": 401, "other": 404} {
			if got := do(t, h, r[0], "/api/v1/workspaces/acme"+r[1], token, map[string]string{}); got.Status != want {
				t.Errorf("%s %s with %s: %d, want %d", r[0], r[1], token, got.Status, want)
			}
		}
		want := 404
		if r[1] == "/labels" {
			want = 400 // the route is served, and the label has no name
		}
		if got := do(t, h, r[0], "/api/v1/workspaces/granted"+r[1], "other", map[string]string{}); got.Status != want {
			t.Errorf("%s %s of a granted slug that was not added: %d, want %d", r[0], r[1], got.Status, want)
		}
	}
	if got := do(t, h, "GET", "/api/v1/workspaces/acme/nowhere", pat, nil); got.Status != 404 || errorType(t, got) != "not_found" {
		t.Errorf("a route that is not hub's: %d %s", got.Status, got.Body)
	}
}

func TestEventsBatch_KeepsEachBatchOfTheRun(t *testing.T) {
	h := newHub(t, Workspace{})
	const path = "/api/v1/workspaces/acme/runs/r1/events/batch"
	for i := range 2 {
		r := do(t, h, "POST", path, pat, []map[string]any{{"id": "r1-1", "run_id": "r1", "event_type": "step", "timestamp": "2026-01-01T00:00:00Z", "payload": map[string]any{"n": i}}})
		if r.Status != 200 || r.Object(t)["accepted"] != 1.0 {
			t.Errorf("a batch: %d %s", r.Status, r.Body)
		}
	}
	batches := h.EventBatches("acme", "r1")
	if len(batches) != 2 || !reflect.DeepEqual(batches[1][0], EventRecord{ID: "r1-1", RunID: "r1", Type: "step", Timestamp: "2026-01-01T00:00:00Z", Payload: []byte(`{"n":1}`)}) {
		t.Errorf("batches = %+v", batches)
	}
	if len(h.EventBatches("acme", "r2")) != 0 {
		t.Error("another run has batches")
	}
	for name, c := range map[string]struct {
		body any
		want int
	}{
		"an empty batch":   {[]any{}, 400},
		"a malformed body": {"{", 400},
		"1001 events":      {make([]map[string]any, 1001), 413},
	} {
		if r := do(t, h, "POST", path, pat, c.body); r.Status != c.want {
			t.Errorf("%s: %d, want %d", name, r.Status, c.want)
		}
	}
}

// A label is kept once per slug, with its first colour and description, for any slug the token is
// granted, so that a subscribe can create its labels.
func TestLabels_AreCreatedOnceForAnyGrantedSlug(t *testing.T) {
	h := New(t)
	h.AddUser("pat", "usr_1", []string{"widgets"})
	h.AddUser("other", "usr_2", nil)
	h.AddWorkspaceToken("bound", []string{"gadgets"})
	label := func(slug, token, body string) int {
		return do(t, h, "POST", "/api/v1/workspaces/"+slug+"/labels", token, body).Status
	}
	if got := label("widgets", "pat", `{"name":"af:fix","color":"#0e8a16","description":"Fix it"}`); got != 201 {
		t.Errorf("a new label: %d, want 201", got)
	}
	if r := do(t, h, "POST", "/api/v1/workspaces/widgets/labels", "pat", `{"name":"af:fix","color":"ff0000"}`); r.Status != 200 || !strings.Contains(r.Body, `"0e8a16"`) {
		t.Errorf("the same name again: %d %s, want 200 with the label as it is kept", r.Status, r.Body)
	}
	for name, c := range map[string]struct {
		slug, token, body string
		want              int
	}{
		"a workspace token":       {"gadgets", "bound", `{"name":"af:triage"}`, 201},
		"a slug not granted":      {"widgets", "other", `{"name":"af:ready"}`, 404},
		"an unknown token":        {"widgets", "unknown", `{"name":"af:ready"}`, 401},
		"a blank name":            {"widgets", "pat", `{"name":" "}`, 400},
		"a body that is no label": {"widgets", "pat", `[`, 400},
	} {
		if got := label(c.slug, c.token, c.body); got != c.want {
			t.Errorf("%s: %d, want %d", name, got, c.want)
		}
	}
	if got := h.Labels("widgets"); !reflect.DeepEqual(got, []Label{{Name: "af:fix", Color: "0e8a16", Description: "Fix it"}}) {
		t.Errorf("Labels = %v, want the first label alone, unchanged", got)
	}
	h.PutLabel("widgets", Label{Name: "af:fix", Color: "000000"})
	h.PutLabel("widgets", Label{Name: "af:new", Color: "111111"})
	if got := h.Labels("widgets"); len(got) != 2 || got[0].Color != "000000" || got[1].Name != "af:new" {
		t.Errorf("Labels after PutLabel = %v", got)
	}
}
