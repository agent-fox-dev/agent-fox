package fakehub

import (
	"fmt"
	"net/http"
	"time"
)

// Usage is a usage record as the hub stored it. Hub keeps the model and the tokens; the rest
// is what the worker sent beside them.
type Usage struct {
	Model                     string
	InputTokens, OutputTokens int64
	CostUSD                   float64
	CostSource, Brain         string
	Turns                     int
}

// Session is a session as the hub holds it.
type Session struct {
	ID, Slug, Archetype, Model, RunID string
	// Status is "open" until the session is completed, then the status it was completed with.
	Status  string
	Message string
	Usage   []Usage
	// Seq and CompletedSeq are where the opening and the completion are in the order of the
	// requests.
	Seq, CompletedSeq int
	// Token is the hash of the token that opened the session.
	Token string
}

// session is a Session that the hub keeps.
type session struct{ Session }

// Sessions are the sessions that were opened, in order.
func (h *Hub) Sessions() []Session {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Session, len(h.sessions))
	for i, s := range h.sessions {
		out[i] = s.Session
		out[i].Usage = append([]Usage(nil), s.Usage...)
	}
	return out
}

// sessionRoute serves the routes under /api/v1/sessions.
func (h *Hub) sessionRoute(w http.ResponseWriter, r *http.Request, token string, rest []string) {
	if !h.known(token) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	switch {
	case len(rest) == 0 && r.Method == http.MethodPost:
		h.openSession(w, r, token)
	case len(rest) == 2 && rest[1] == "usage" && r.Method == http.MethodPost:
		h.sessionAction(w, r, token, rest[0], h.reportUsage)
	case len(rest) == 2 && rest[1] == "complete" && r.Method == http.MethodPost:
		h.sessionAction(w, r, token, rest[0], h.completeSession)
	default:
		writeError(w, http.StatusNotFound, "not_found", "not found")
	}
}

// openSession answers POST /api/v1/sessions.
func (h *Hub) openSession(w http.ResponseWriter, r *http.Request, token string) {
	var req struct {
		WorkspaceSlug string `json:"workspace_slug"`
		Archetype     string `json:"archetype"`
		Model         string `json:"model"`
		RunID         string `json:"run_id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.WorkspaceSlug == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "workspace_slug is missing")
		return
	}
	if h.refuse(w, token, req.WorkspaceSlug) {
		return
	}
	h.mu.Lock()
	h.nextID++
	s := &session{Session{
		ID: fmt.Sprintf("ses_%04d", h.nextID), Slug: req.WorkspaceSlug, Archetype: req.Archetype, Model: req.Model, RunID: req.RunID,
		Status: "open", Token: TokenHash(token),
	}}
	h.seq++
	s.Seq = h.seq
	h.sessions = append(h.sessions, s)
	h.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": s.ID, "workspace_slug": s.Slug, "run_id": s.RunID, "archetype": s.Archetype, "model": s.Model, "status": "running",
		"credential_type": "pat", "started_at": stamp(time.Now()),
	})
}

// sessionAction runs act on the session named by id, for a token that sees its workspace.
func (h *Hub) sessionAction(w http.ResponseWriter, r *http.Request, token, id string, act func(http.ResponseWriter, *http.Request, *session)) {
	h.mu.Lock()
	var found *session
	for _, s := range h.sessions {
		if s.ID == id {
			found = s
		}
	}
	h.mu.Unlock()
	if found == nil {
		writeError(w, http.StatusNotFound, "not_found", "session not found")
		return
	}
	if h.refuse(w, token, found.Slug) {
		return
	}
	act(w, r, found)
}

// reportUsage answers POST /api/v1/sessions/{id}/usage.
func (h *Hub) reportUsage(w http.ResponseWriter, r *http.Request, s *session) {
	var req struct {
		Model        string  `json:"model"`
		InputTokens  int64   `json:"input_tokens"`
		OutputTokens int64   `json:"output_tokens"`
		CostUSD      float64 `json:"cost_usd"`
		CostSource   string  `json:"cost_source"`
		Brain        string  `json:"brain"`
		Turns        int     `json:"turns"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "model is missing")
		return
	}
	h.mu.Lock()
	s.Usage = append(s.Usage, Usage{Model: req.Model, InputTokens: req.InputTokens, OutputTokens: req.OutputTokens, CostUSD: req.CostUSD, CostSource: req.CostSource, Brain: req.Brain, Turns: req.Turns})
	h.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"model": req.Model, "input_tokens": req.InputTokens, "output_tokens": req.OutputTokens})
}

// completeSession answers POST /api/v1/sessions/{id}/complete: the status is completed,
// failed or timeout, as hub takes it.
func (h *Hub) completeSession(w http.ResponseWriter, r *http.Request, s *session) {
	var req struct {
		Status       string `json:"status"`
		ErrorMessage string `json:"error_message"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	switch req.Status {
	case "completed", "failed", "timeout":
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "status must be completed, failed or timeout")
		return
	}
	h.mu.Lock()
	s.Status, s.Message = req.Status, req.ErrorMessage
	h.seq++
	s.CompletedSeq = h.seq
	h.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"id": s.ID, "status": req.Status})
}
