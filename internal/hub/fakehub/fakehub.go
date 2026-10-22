// Package fakehub is a hub for tests: an httptest server that speaks the calls of hub's API
// that agent-fox makes, from state that a test sets and reads. It records every request
// and can be told to fail a route, to hang up on it, to answer it late, to redirect it or to
// answer more than a client may read, so that a test can say exactly what the service sent
// to hub and how it behaves when hub misbehaves. It is used by the tests of internal/hub,
// internal/service, internal/worker and cmd/agent-fox, and by nothing else.
//
// What it serves:
//
//   - who a token belongs to and whether it sees a workspace (the core's two calls);
//   - the workspaces of AddWorkspace: the record with the clone URL, the base branch, the
//     mode and the URL of the forge repository, the resolved variables, a git smart-HTTP
//     server for the clone URL that accepts only the Basic credential agent-fox:<PAT>, the
//     branch push route, and the forge API (issues, comments, labels, pull requests);
//   - sessions with their usage, events and completion;
//   - sandbox leases, which point at an in-process gateway (see Gateway) over the local
//     provider, and their release.
//
// The shapes are those of hub's OpenAPI document. Every test runs offline.
package fakehub

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	userPath       = "/api/v1/user"
	workspacePath  = "/api/v1/workspaces/"
	sessionsPath   = "/api/v1/sessions"
	gitPrefix      = "/git/"
	defaultHidden  = http.StatusNotFound
	bearerPrefix   = "bearer "
	failureMessage = "injected failure"
	// maxRecordedBody is how much of a request body a Request keeps.
	maxRecordedBody = 1 << 20
)

// Request is a request the hub received.
type Request struct {
	// Seq is the order of the request among everything the hub and its gateway recorded.
	Seq    int
	Method string
	// Path is the path of the request, decoded; URI is it as it was sent.
	Path string
	URI  string
	// TokenHash is the hex SHA-256 of the bearer token, empty when there was none.
	TokenHash string
	Header    http.Header
	// Body is what the request carried, up to 1 MiB.
	Body []byte
}

// Fault is what a route does instead of answering, or before it answers.
type Fault struct {
	// Status, when it is not 0, is the status the route answers with, and Body its body (an
	// error object of hub's shape when empty).
	Status int
	Body   string
	// Hangup closes the connection without an answer.
	Hangup bool
	// Delay waits before the route answers or fails.
	Delay time.Duration
	// Redirect answers 302 to this location.
	Redirect string
	// Oversize answers 200 with a body of about this many bytes.
	Oversize int
	// Once makes the fault apply to the next request only.
	Once bool
	// Skip lets this many requests through before the fault applies.
	Skip int
}

// rule makes a route fail.
type rule struct {
	method    string // any method when empty
	path      string
	tokenHash string // when set, only requests with this token
	fault     Fault
	let       int // how many requests the rule has let through, for Fault.Skip
}

// matches says whether the rule is for the path: the path itself, or any path under the prefix
// when the rule's path ends in "/*".
func (r rule) matches(path string) bool {
	if prefix, ok := strings.CutSuffix(r.path, "*"); ok && strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(path, prefix)
	}
	return r.path == path
}

// Hub is a fake hub.
type Hub struct {
	// URL is the base of the server.
	URL string
	// HiddenStatus is what a token that is known gets for a workspace it may not
	// see: 404, as hub's own answer for a workspace that is missing or forbidden.
	// Set it before the first request.
	HiddenStatus int

	t       testing.TB
	srv     *httptest.Server
	mu      sync.Mutex
	seq     int
	tokens  map[string]string          // token -> user id
	visible map[string]map[string]bool // user id -> slugs
	bound   map[string]map[string]bool // token without a user -> slugs
	rules   []rule
	delays  map[string]time.Duration

	requests []Request

	workspaces map[string]*workspace
	sessions   []*session
	events     map[string][][]EventRecord // slug/run id -> batches
	leases     []*Lease
	pushes     []BranchPush
	gitLog     []GitRequest
	gateway    *Gateway
	leaseTTL   time.Duration
	nextID     int
}

// New starts a hub and stops it when the test ends.
func New(t testing.TB) *Hub {
	t.Helper()
	h := &Hub{
		HiddenStatus: defaultHidden,
		t:            t,
		tokens:       map[string]string{},
		visible:      map[string]map[string]bool{},
		bound:        map[string]map[string]bool{},
		delays:       map[string]time.Duration{},
		workspaces:   map[string]*workspace{},
		events:       map[string][][]EventRecord{},
		leaseTTL:     30 * time.Minute,
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

// RequestsTo are the requests with the method (any when empty) for the path, decoded.
func (h *Hub) RequestsTo(method, path string) []Request {
	var out []Request
	for _, r := range h.Requests() {
		if r.Path == path && (method == "" || r.Method == method) {
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
func (h *Hub) Fail(path string, status int) { h.add(rule{path: path, fault: Fault{Status: status}}) }

// FailOnce makes the next request for the path fail with the status.
func (h *Hub) FailOnce(path string, status int) {
	h.add(rule{path: path, fault: Fault{Status: status, Once: true}})
}

// FailFor makes every request for the path made with the token fail with the
// status, until Clear.
func (h *Hub) FailFor(path, token string, status int) {
	h.add(rule{path: path, tokenHash: TokenHash(token), fault: Fault{Status: status}})
}

// Hangup makes every request for the path end by closing the connection, until
// Clear.
func (h *Hub) Hangup(path string) { h.add(rule{path: path, fault: Fault{Hangup: true}}) }

// Delay makes every answer for the path wait for d first, until Clear.
func (h *Hub) Delay(path string, d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.delays[path] = d
}

// Inject makes the requests with the method (any when empty) for the path, decoded, do what
// the fault says, until Clear. A path that ends in "/*" is every path under the prefix. The rule
// added last that matches a request is the one that applies.
func (h *Hub) Inject(method, path string, f Fault) { h.add(rule{method: method, path: path, fault: f}) }

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

// next is the next number of the order of events.
func (h *Hub) next() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	return h.seq
}

// serve records the request, applies the first rule that matches it, and answers.
func (h *Hub) serve(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, gitPrefix) {
		h.serveGit(w, r)
		return
	}
	token := bearer(r)
	hash := TokenHash(token)
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(r.Body, maxRecordedBody))
		r.Body = io.NopCloser(bytes.NewReader(body))
	}

	h.mu.Lock()
	h.seq++
	h.requests = append(h.requests, Request{Seq: h.seq, Method: r.Method, Path: r.URL.Path, URI: r.RequestURI, TokenHash: hash, Header: r.Header.Clone(), Body: body})
	fault := h.take(r.Method, r.URL.Path, hash)
	delay := h.delays[r.URL.Path]
	h.mu.Unlock()

	if fault != nil {
		delay += fault.Delay
	}
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
	}
	if fault != nil && applies(*fault) {
		answerFault(w, *fault)
		return
	}
	h.route(w, r, token)
}

// applies says whether the fault answers in place of the route.
func applies(f Fault) bool {
	return f.Status != 0 || f.Hangup || f.Redirect != "" || f.Oversize > 0 || f.Body != ""
}

// answerFault answers as the fault says.
func answerFault(w http.ResponseWriter, f Fault) {
	switch {
	case f.Hangup:
		panic(http.ErrAbortHandler)
	case f.Redirect != "":
		w.Header().Set("Location", f.Redirect)
		w.WriteHeader(http.StatusFound)
	case f.Oversize > 0:
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"pad":"`+strings.Repeat("x", f.Oversize)+`"}`)
	case f.Body != "":
		status := f.Status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, f.Body)
	default:
		writeError(w, f.Status, "injected", failureMessage)
	}
}

// take is the fault that applies to the request, if any: of the rules that match,
// the one added last, so that a test can fail a route and then make it hang up.
// A rule that applies once is used up. The caller holds the lock.
func (h *Hub) take(method, path, tokenHash string) *Fault {
	for i := len(h.rules) - 1; i >= 0; i-- {
		r := h.rules[i]
		if !r.matches(path) || (r.method != "" && r.method != method) || (r.tokenHash != "" && r.tokenHash != tokenHash) {
			continue
		}
		if r.let < r.fault.Skip {
			h.rules[i].let++
			continue
		}
		if r.fault.Once {
			h.rules = append(h.rules[:i:i], h.rules[i+1:]...)
		}
		f := r.fault
		return &f
	}
	return nil
}

// route answers the request from the state of the hub.
func (h *Hub) route(w http.ResponseWriter, r *http.Request, token string) {
	segs := segments(r)
	switch {
	case r.URL.Path == userPath && r.Method == http.MethodGet:
		h.user(w, token)
	case len(segs) >= 4 && segs[0] == "api" && segs[1] == "v1" && segs[2] == "workspaces":
		h.workspaceRoute(w, r, token, segs[3], segs[4:])
	case len(segs) >= 3 && segs[0] == "api" && segs[1] == "v1" && segs[2] == "sessions":
		h.sessionRoute(w, r, token, segs[3:])
	case strings.HasPrefix(r.URL.Path, workspacePath):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	default:
		writeError(w, http.StatusNotFound, "not_found", "not found")
	}
}

// segments are the segments of the path, each decoded on its own, so that an escaped slash
// stays inside its segment.
func segments(r *http.Request) []string {
	var out []string
	for _, raw := range strings.Split(strings.Trim(r.URL.EscapedPath(), "/"), "/") {
		seg, err := url.PathUnescape(raw)
		if err != nil {
			seg = raw
		}
		out = append(out, seg)
	}
	return out
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

// access is how a token stands with a workspace: 0 when it sees it, 401 when hub does not
// know the token, and HiddenStatus when it does and the token may not see the workspace.
func (h *Hub) access(token, slug string) int {
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
		return http.StatusUnauthorized
	case !sees:
		return h.HiddenStatus
	}
	return 0
}

// known says whether hub knows the token at all.
func (h *Hub) known(token string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if token == "" {
		return false
	}
	if _, ok := h.tokens[token]; ok {
		return true
	}
	_, ok := h.bound[token]
	return ok
}

// refuse answers a token that does not have access, and says whether it did.
func (h *Hub) refuse(w http.ResponseWriter, token, slug string) bool {
	switch status := h.access(token, slug); status {
	case 0:
		return false
	case http.StatusUnauthorized:
		writeError(w, status, "unauthorized", "unauthorized")
	default:
		writeError(w, status, "not_found", "workspace not found")
	}
	return true
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

// readJSON decodes the body of the request into v, and answers 400 and says so when it
// cannot.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "the body is malformed")
		return false
	}
	return true
}
