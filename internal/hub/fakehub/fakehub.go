// Package fakehub is a hub for tests: an httptest server that speaks the two
// calls of hub's API that agent-fox makes (who a token belongs to, and whether a
// token sees a workspace), from a table of tokens, users and workspaces. It
// records every request, and can be told to fail a route with a status, to hang
// up on it, or to answer it late, so that a test can say exactly what the
// service sent to hub and how it behaves when hub misbehaves. It is used by the
// tests of internal/hub, internal/service and cmd/agent-fox, and by nothing else.
//
// Later specs extend it with the routes they call.
package fakehub

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	userPath       = "/api/v1/user"
	workspacePath  = "/api/v1/workspaces/"
	defaultHidden  = http.StatusNotFound
	bearerPrefix   = "bearer "
	failureMessage = "injected failure"
)

// Request is a request the hub received.
type Request struct {
	Method string
	// Path is the path of the request, decoded; URI is it as it was sent.
	Path string
	URI  string
	// TokenHash is the hex SHA-256 of the bearer token, empty when there was none.
	TokenHash string
	Header    http.Header
}

// rule makes a route fail.
type rule struct {
	path      string
	tokenHash string // when set, only requests with this token
	status    int
	hangup    bool
	once      bool
}

// Hub is a fake hub.
type Hub struct {
	// URL is the base of the server.
	URL string
	// HiddenStatus is what a token that is known gets for a workspace it may not
	// see: 404, as hub's own answer for a workspace that is missing or forbidden.
	// Set it before the first request.
	HiddenStatus int

	srv      *httptest.Server
	mu       sync.Mutex
	tokens   map[string]string          // token -> user id
	visible  map[string]map[string]bool // user id -> slugs
	bound    map[string]map[string]bool // token without a user -> slugs
	requests []Request
	rules    []rule
	delays   map[string]time.Duration
}

// New starts a hub and stops it when the test ends.
func New(t testing.TB) *Hub {
	t.Helper()
	h := &Hub{
		HiddenStatus: defaultHidden,
		tokens:       map[string]string{},
		visible:      map[string]map[string]bool{},
		bound:        map[string]map[string]bool{},
		delays:       map[string]time.Duration{},
	}
	h.srv = httptest.NewServer(http.HandlerFunc(h.serve))
	h.URL = h.srv.URL
	t.Cleanup(h.srv.Close)
	return h
}

// TokenHash is the hex SHA-256 of a token, the way the hub records tokens and
// the service keys its caches by them.
func TokenHash(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// AddUser makes token the token of the user, who may see the slugs and whatever
// else is granted to the user.
func (h *Hub) AddUser(token, userID string, slugs []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tokens[token] = userID
	h.grant(userID, slugs)
}

// AddWorkspaceToken makes token a token of the slugs that belongs to no user:
// /user refuses it while the workspace call finds the workspaces, as it does for
// a credential bound to a workspace.
func (h *Hub) AddWorkspaceToken(token string, slugs []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := map[string]bool{}
	for _, s := range slugs {
		set[s] = true
	}
	h.bound[token] = set
}

// Grant lets the user see more workspaces.
func (h *Hub) Grant(userID string, slugs ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.grant(userID, slugs)
}

func (h *Hub) grant(userID string, slugs []string) {
	if h.visible[userID] == nil {
		h.visible[userID] = map[string]bool{}
	}
	for _, s := range slugs {
		h.visible[userID][s] = true
	}
}

// Revoke makes hub forget the token, so that it is refused from now on.
func (h *Hub) Revoke(token string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.tokens, token)
	delete(h.bound, token)
}

// Requests are all the requests received, in order, those that failed included.
func (h *Hub) Requests() []Request {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Request(nil), h.requests...)
}

// RequestsFor are the requests made with the token whose hash is given.
func (h *Hub) RequestsFor(tokenHash string) []Request {
	var out []Request
	for _, r := range h.Requests() {
		if r.TokenHash == tokenHash {
			out = append(out, r)
		}
	}
	return out
}

// Total is the number of requests received.
func (h *Hub) Total() int { return len(h.Requests()) }

// CountFor is the number of requests made with the token whose hash is given.
func (h *Hub) CountFor(tokenHash string) int { return len(h.RequestsFor(tokenHash)) }

// Count is the number of requests for the path made with the token whose hash is
// given.
func (h *Hub) Count(path, tokenHash string) int {
	n := 0
	for _, r := range h.RequestsFor(tokenHash) {
		if r.Path == path {
			n++
		}
	}
	return n
}

// Fail makes every request for the path fail with the status, until Clear.
func (h *Hub) Fail(path string, status int) { h.add(rule{path: path, status: status}) }

// FailOnce makes the next request for the path fail with the status.
func (h *Hub) FailOnce(path string, status int) {
	h.add(rule{path: path, status: status, once: true})
}

// FailFor makes every request for the path made with the token fail with the
// status, until Clear.
func (h *Hub) FailFor(path, token string, status int) {
	h.add(rule{path: path, tokenHash: TokenHash(token), status: status})
}

// Hangup makes every request for the path end by closing the connection, until
// Clear.
func (h *Hub) Hangup(path string) { h.add(rule{path: path, hangup: true}) }

// Delay makes every answer for the path wait for d first, until Clear.
func (h *Hub) Delay(path string, d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.delays[path] = d
}

// Clear takes away every failure and every delay.
func (h *Hub) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rules = nil
	h.delays = map[string]time.Duration{}
}

func (h *Hub) add(r rule) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rules = append(h.rules, r)
}

// serve records the request, applies the first rule that matches it, and answers.
func (h *Hub) serve(w http.ResponseWriter, r *http.Request) {
	token := bearer(r)
	hash := TokenHash(token)

	h.mu.Lock()
	h.requests = append(h.requests, Request{Method: r.Method, Path: r.URL.Path, URI: r.RequestURI, TokenHash: hash, Header: r.Header.Clone()})
	failure := h.take(r.URL.Path, hash)
	delay := h.delays[r.URL.Path]
	h.mu.Unlock()

	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	if failure != nil {
		if failure.hangup {
			panic(http.ErrAbortHandler)
		}
		writeError(w, failure.status, "injected", failureMessage)
		return
	}

	switch {
	case r.Method != http.MethodGet:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	case r.URL.Path == userPath:
		h.user(w, token)
	case strings.HasPrefix(r.URL.Path, workspacePath) && len(r.URL.Path) > len(workspacePath):
		h.workspace(w, token, strings.TrimPrefix(r.URL.Path, workspacePath))
	default:
		writeError(w, http.StatusNotFound, "not_found", "not found")
	}
}

// take is the rule that applies to the request, if any: of those that match,
// the one added last, so that a test can fail a route and then make it hang up.
// A rule that fails only once is used up. The caller holds the lock.
func (h *Hub) take(path, tokenHash string) *rule {
	for i := len(h.rules) - 1; i >= 0; i-- {
		r := h.rules[i]
		if r.path != path || (r.tokenHash != "" && r.tokenHash != tokenHash) {
			continue
		}
		if r.once {
			h.rules = append(h.rules[:i:i], h.rules[i+1:]...)
		}
		return &r
	}
	return nil
}

// user answers /user: the user of the token, or 401 for a token that belongs to
// no user.
func (h *Hub) user(w http.ResponseWriter, token string) {
	h.mu.Lock()
	id, ok := h.tokens[token]
	h.mu.Unlock()
	if !ok || token == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "login": "login-" + id})
}

// workspace answers the workspace call: 200 for a token that sees the slug, 401
// for a token that hub does not know, and HiddenStatus for one that it does.
func (h *Hub) workspace(w http.ResponseWriter, token, slug string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var sees, known bool
	if id, ok := h.tokens[token]; ok && token != "" {
		known, sees = true, h.visible[id][slug]
	} else if set, ok := h.bound[token]; ok && token != "" {
		known, sees = true, set[slug]
	}
	switch {
	case !known:
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
	case !sees:
		writeError(w, h.HiddenStatus, "not_found", "workspace not found")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"slug": slug})
	}
}

// bearer is the token of the request, empty when it carries none.
func bearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if len(v) > len(bearerPrefix) && strings.EqualFold(v[:len(bearerPrefix)], bearerPrefix) {
		return strings.TrimSpace(v[len(bearerPrefix):])
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError answers with hub's error shape. It never repeats anything of the
// request, so that a token cannot come back in a body.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
