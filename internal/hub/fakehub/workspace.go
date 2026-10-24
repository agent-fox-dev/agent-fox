package fakehub

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Workspace is a workspace that a test adds to the hub.
type Workspace struct {
	Slug string
	// Branch is the base branch, "main" when empty. Mode is "standard", or "carry_patch".
	Branch, Mode string
	// ForgeURL is the URL of the forge repository (hub's git_url), https://github.example.com/acme/widgets
	// when empty.
	ForgeURL string
	// PAT is the token of the workspace: the bearer token that hub's API takes for it, and
	// the password of the Basic credential that its git server takes. Every call needs it.
	PAT string
	// Vars are the variables the workspace resolves, served as an object {"vars": {…}} when
	// VarsForm is "object", as a list of {name, value} when it is "namelist", and as hub does,
	// a list of {key, value, origin}, otherwise.
	Vars     map[string]string
	VarsForm string
	// Files are the files of the first commit of the git repository that the clone URL leads
	// to; none means the workspace has no repository.
	Files map[string]string
}

// workspace is a Workspace with its state.
type workspace struct {
	Workspace
	gitDir   string // the bare repository the git server serves
	issues   map[int]*issueState
	pulls    []Pull
	nextItem int
	lists    listState
}

// AddWorkspace adds a workspace: the token PAT sees it, and the hub answers for it. If it has
// files it gets a bare git repository with them on its base branch, which the clone URL
// leads to (the test is skipped where git is missing).
func (h *Hub) AddWorkspace(t interface {
	Helper()
	Skip(...any)
	Fatalf(string, ...any)
	TempDir() string
	Cleanup(func())
}, w Workspace) {
	t.Helper()
	if w.Branch == "" {
		w.Branch = "main"
	}
	if w.Mode == "" {
		w.Mode = "standard"
	}
	if w.ForgeURL == "" {
		w.ForgeURL = "https://github.example.com/acme/widgets"
	}
	ws := &workspace{Workspace: w, issues: map[int]*issueState{}, nextItem: 100}
	if len(w.Files) > 0 {
		ws.gitDir = makeRepository(t, w)
	}
	if w.PAT != "" {
		h.AddWorkspaceToken(w.PAT, []string{w.Slug})
	}
	h.mu.Lock()
	h.workspaces[w.Slug] = ws
	h.mu.Unlock()
}

// CloneURL is the clone URL of the workspace on the hub's git server.
func (h *Hub) CloneURL(slug string) string { return h.URL + gitPrefix + url.PathEscape(slug) + ".git" }

// GitDir is the bare repository that the git server of the hub serves for the workspace, or ""
// when it has none.
func (h *Hub) GitDir(slug string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ws := h.workspaces[slug]; ws != nil {
		return ws.gitDir
	}
	return ""
}

// makeRepository makes a bare repository with the files of the workspace committed on its base
// branch, that git http-backend may serve, and receives pushes.
func makeRepository(t interface {
	Helper()
	Skip(...any)
	Fatalf(string, ...any)
	TempDir() string
}, w Workspace) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	work, bare := t.TempDir(), filepath.Join(t.TempDir(), url.PathEscape(w.Slug)+".git")
	for name, content := range w.Files {
		path := filepath.Join(work, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("fakehub: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("fakehub: %v", err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", w.Branch},
		{"add", "-A"},
		{"-c", "user.name=hub", "-c", "user.email=hub@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "chore: initial commit"},
	} {
		gitRun(t, work, args...)
	}
	gitRun(t, filepath.Dir(bare), "clone", "-q", "--bare", work, bare)
	gitRun(t, bare, "config", "http.receivepack", "true")
	gitRun(t, bare, "config", "http.uploadpack", "true")
	return bare
}

// gitRun runs git in dir with none of the configuration of the machine.
func gitRun(t interface {
	Helper()
	Fatalf(string, ...any)
}, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fakehub: git %v: %v\n%s", args, err, out)
	}
}

// workspaceRoute serves the routes under /api/v1/workspaces/{slug}.
func (h *Hub) workspaceRoute(w http.ResponseWriter, r *http.Request, token, slug string, rest []string) {
	if len(rest) == 0 && r.Method == http.MethodGet {
		h.workspaceRecord(w, token, slug)
		return
	}
	if len(rest) == 0 {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if h.refuse(w, token, slug) {
		return
	}
	// The label route serves any slug the token is granted, added or not, so that a subscribe can create its labels.
	if slices.Equal(rest, []string{"labels"}) && r.Method == http.MethodPost {
		h.createLabel(w, r, slug)
		return
	}
	h.mu.Lock()
	ws := h.workspaces[slug]
	h.mu.Unlock()
	if ws == nil {
		writeError(w, http.StatusNotFound, "not_found", "workspace not found")
		return
	}
	switch {
	case slices.Equal(rest, []string{"vars", "resolved"}) && r.Method == http.MethodGet:
		h.resolvedVars(w, ws)
	case len(rest) == 4 && rest[0] == "runs" && rest[2] == "events" && rest[3] == "batch" && r.Method == http.MethodPost:
		h.eventsBatch(w, r, ws, rest[1])
	case rest[0] == "sandboxes":
		h.sandboxRoute(w, r, ws, rest[1:])
	case len(rest) == 3 && rest[0] == "branches" && rest[2] == "push" && r.Method == http.MethodPost:
		h.branchPush(w, ws, rest[1])
	default:
		h.forgeRoute(w, r, ws, rest)
	}
}

// workspaceRecord answers the workspace call: the record of a workspace of AddWorkspace, and
// otherwise the bare object that the core's call needs.
func (h *Hub) workspaceRecord(w http.ResponseWriter, token, slug string) {
	if h.refuse(w, token, slug) {
		return
	}
	h.mu.Lock()
	ws := h.workspaces[slug]
	h.mu.Unlock()
	if ws == nil {
		writeJSON(w, http.StatusOK, map[string]any{"slug": slug})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"slug": slug, "git_url": ws.ForgeURL, "hub_url": h.CloneURL(slug), "branch": ws.Branch, "workspace_mode": ws.Mode,
		"status": "active", "clone_status": "ready",
	})
}

// resolvedVars answers the resolved variables in the form the workspace was added with, as they are when it is asked:
// a test may change them meanwhile (UpdateWorkspace).
func (h *Hub) resolvedVars(w http.ResponseWriter, ws *workspace) {
	h.mu.Lock()
	vars, form := maps.Clone(ws.Vars), ws.VarsForm
	h.mu.Unlock()
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	slices.Sort(names)
	switch form {
	case "object":
		writeJSON(w, http.StatusOK, map[string]any{"vars": vars})
	case "namelist":
		list := []map[string]string{}
		for _, name := range names {
			list = append(list, map[string]string{"name": name, "value": vars[name]})
		}
		writeJSON(w, http.StatusOK, list)
	default:
		list := []map[string]string{}
		for _, name := range names {
			list = append(list, map[string]string{"key": name, "value": vars[name], "origin": "workspace"})
		}
		writeJSON(w, http.StatusOK, list)
	}
}

// BranchPush is a call of the branch push route.
type BranchPush struct {
	Seq            int
	Slug, Branch   string
	Status         int
	BranchWasThere bool
}

// branchPush answers the branch push route: 200 when the branch is in the repository of the
// workspace, 404 branch_not_found when it is not.
func (h *Hub) branchPush(w http.ResponseWriter, ws *workspace, branch string) {
	push := BranchPush{Seq: h.next(), Slug: ws.Slug, Branch: branch}
	if ws.gitDir != "" {
		cmd := exec.Command("git", "-C", ws.gitDir, "rev-parse", "--verify", "-q", "refs/heads/"+branch)
		push.BranchWasThere = cmd.Run() == nil
	} else {
		push.BranchWasThere = true
	}
	push.Status = http.StatusOK
	if !push.BranchWasThere {
		push.Status = http.StatusNotFound
	}
	h.mu.Lock()
	h.pushes = append(h.pushes, push)
	h.mu.Unlock()
	if !push.BranchWasThere {
		writeError(w, http.StatusNotFound, "branch_not_found", "branch_not_found: "+branch)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"branch": branch, "sha": strings.Repeat("0", 40), "remote": "origin", "remote_url": ws.ForgeURL, "result": "pushed"})
}

// BranchPushes are the calls of the branch push route, in order.
func (h *Hub) BranchPushes() []BranchPush {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]BranchPush(nil), h.pushes...)
}

// EventRecord is an event as the hub stored it.
type EventRecord struct {
	ID, RunID, Type, Timestamp string
	Payload                    json.RawMessage
}

// eventsBatch answers the events batch route: the body is a bare array of events.
func (h *Hub) eventsBatch(w http.ResponseWriter, r *http.Request, ws *workspace, runID string) {
	var items []struct {
		ID        string          `json:"id"`
		RunID     string          `json:"run_id"`
		EventType string          `json:"event_type"`
		Timestamp string          `json:"timestamp"`
		Payload   json.RawMessage `json:"payload"`
	}
	if !readJSON(w, r, &items) {
		return
	}
	if len(items) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "the array is empty")
		return
	}
	if len(items) > 1000 {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "more than 1000 items")
		return
	}
	batch := make([]EventRecord, 0, len(items))
	for _, it := range items {
		batch = append(batch, EventRecord{ID: it.ID, RunID: it.RunID, Type: it.EventType, Timestamp: it.Timestamp, Payload: it.Payload})
	}
	key := ws.Slug + "/" + runID
	h.mu.Lock()
	h.events[key] = append(h.events[key], batch)
	h.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"accepted": len(items), "duplicates": 0, "errors": []any{}})
}

// EventBatches are the batches of events that were posted for the run of the workspace, in
// order.
func (h *Hub) EventBatches(slug, runID string) [][]EventRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([][]EventRecord(nil), h.events[slug+"/"+runID]...)
}

// number is the number in a path segment, or 0.
func number(seg string) int {
	n, err := strconv.Atoi(seg)
	if err != nil || n < 1 {
		return 0
	}
	return n
}

// stamp is a time as hub writes it.
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// UpdateWorkspace changes the workspace as hub holds it: its variables, its mode, its base branch or its
// forge repository, as a person would between two deliveries of an attempt.
func (h *Hub) UpdateWorkspace(slug string, change func(*Workspace)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ws := h.workspaces[slug]; ws != nil {
		change(&ws.Workspace)
	}
}

// Label is a label of the repository of a workspace, as the label route keeps it.
type Label struct{ Name, Color, Description string }

// createLabel answers POST …/labels, hub's ensureForgeLabel: a label with a name the slug holds is answered 200 as it is
// held, and left unchanged; a new one is kept and answered 201.
func (h *Hub) createLabel(w http.ResponseWriter, r *http.Request, slug string) {
	var req struct {
		Name        string `json:"name"`
		Color       string `json:"color"`
		Description string `json:"description"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "name must not be blank")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, l := range h.labels[slug] {
		if l.Name == req.Name {
			writeJSON(w, http.StatusOK, labelJSON(l))
			return
		}
	}
	l := Label{Name: req.Name, Color: strings.TrimPrefix(req.Color, "#"), Description: req.Description}
	h.labels[slug] = append(h.labels[slug], l)
	writeJSON(w, http.StatusCreated, labelJSON(l))
}

// labelJSON is a label as hub's forge API writes it.
func labelJSON(l Label) map[string]any {
	return map[string]any{"name": l.Name, "color": l.Color, "description": l.Description}
}

// Labels are the labels the slug holds, in the order they were created.
func (h *Hub) Labels(slug string) []Label {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.labels[slug])
}

// PutLabel sets a label of the slug, as a person would change it on the forge.
func (h *Hub) PutLabel(slug string, l Label) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.labels[slug] {
		if h.labels[slug][i].Name == l.Name {
			h.labels[slug][i] = l
			return
		}
	}
	h.labels[slug] = append(h.labels[slug], l)
}
