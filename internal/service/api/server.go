package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"runtime/debug"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

const (
	// readHeaderTimeout bounds how long a client may take to send its headers.
	readHeaderTimeout = 10 * time.Second
	// maxBody is the largest request body that is read.
	maxBody = 64 << 10
	// readyTimeout bounds the ping of the database that /readyz makes.
	readyTimeout = 5 * time.Second
)

// Deps is what the server is built from.
type Deps struct {
	Store  *store.Store
	Hub    *hub.Client
	Jobs   *jobs.Service
	Logger *slog.Logger
	// Version is the version document that GET /version answers.
	Version any
	// AuthCacheTTL is how long hub's answers about a caller's token are kept.
	AuthCacheTTL time.Duration
	// Now is the clock of the caches; time.Now when nil. The tests move it.
	Now func() time.Time
	// Routes registers more routes behind the same middleware, with the checks
	// of the API to wrap them in; the tests use it.
	Routes func(mux *http.ServeMux, s *server)
}

// server is the state the handlers share.
type server struct {
	deps Deps
	log  *slog.Logger
	auth *auth
}

// NewServer returns the HTTP server of the service, without an address: the
// caller serves it on the listener it has bound. Its Handler routes the probes
// and the API, answers every error in the envelope, recovers from the panic of a
// handler, and writes one log record for each request.
func NewServer(deps Deps) *http.Server {
	s := &server{deps: deps, log: deps.Logger}
	if s.log == nil {
		s.log = slog.Default()
	}
	s.auth = newAuth(deps.Hub, deps.AuthCacheTTL, deps.Now)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /version", s.version)
	mux.HandleFunc("GET /api/v1/workspaces", s.authed(s.listWorkspaces))
	mux.HandleFunc("GET /api/v1/workspaces/{slug}", s.inWorkspace(s.getWorkspace))
	mux.HandleFunc("PUT /api/v1/workspaces/{slug}", s.inWorkspace(s.subscribe))
	mux.HandleFunc("DELETE /api/v1/workspaces/{slug}", s.inWorkspace(s.deleteWorkspace))
	if deps.Jobs != nil {
		s.jobRoutes(mux)
	}
	if deps.Routes != nil {
		deps.Routes(mux, s)
	}
	return &http.Server{
		Handler:           s.logged(s.recovered(s.routed(mux))),
		ReadHeaderTimeout: readHeaderTimeout,
		// What net/http itself reports (a panic's stack, a bad request line) goes
		// to the log as records too, so that every line on stderr is one.
		ErrorLog: slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
}

// now is the time, by the clock of the dependencies.
func (s *server) now() time.Time {
	if s.deps.Now != nil {
		return s.deps.Now()
	}
	return time.Now()
}

func (s *server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyz is ready when the database answers a ping.
func (s *server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
	defer cancel()
	if err := s.deps.Store.Ping(ctx); err != nil {
		writeError(w, CodeNotReady, "the database does not answer")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *server) version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.deps.Version)
}

// routed sends a request to the mux, and answers in the envelope what the mux
// would answer in plain text: a route it does not have, a method the route does
// not take, and a path that is not in its canonical form, which the mux would
// redirect.
func (s *server) routed(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !canonical(r.URL.Path) {
			writeError(w, CodeNotFound, "not found")
			return
		}
		handler, pattern := mux.Handler(r)
		if pattern != "" {
			// Not handler.ServeHTTP: only the mux sets the values of the wildcards.
			mux.ServeHTTP(w, r)
			return
		}
		answer := &probe{header: http.Header{}}
		handler.ServeHTTP(answer, r)
		if answer.status == http.StatusMethodNotAllowed {
			if allow := answer.header.Get("Allow"); allow != "" {
				w.Header().Set("Allow", allow)
			}
			writeError(w, CodeMethodNotAllowed, "method not allowed")
			return
		}
		writeError(w, CodeNotFound, "not found")
	})
}

// canonical reports whether the path is already what path.Clean would make of it,
// a trailing slash aside.
func canonical(p string) bool {
	if !strings.HasPrefix(p, "/") {
		return true // "*", the target of OPTIONS: the mux has no route for it
	}
	clean := path.Clean(p)
	if strings.HasSuffix(p, "/") && clean != "/" {
		clean += "/"
	}
	return clean == p
}

// probe is the writer that the mux's own answer is run against, to learn which
// answer it is.
type probe struct {
	header http.Header
	status int
}

func (p *probe) Header() http.Header         { return p.header }
func (p *probe) Write(b []byte) (int, error) { return len(b), nil }
func (p *probe) WriteHeader(status int)      { p.status = status }

// statusWriter remembers the status of the answer for the log, and whether the
// answer has begun.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 && status >= 200 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the writer underneath, for flushing.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// callerKey is the key of the request's caller in its context.
type callerKey struct{}

// caller is the hub user who made the request, once the request has shown who
// that is. The log of the request carries it.
type caller struct{ id string }

// setCaller records the hub user who is calling, for the log of the request.
func setCaller(r *http.Request, id string) {
	if c, ok := r.Context().Value(callerKey{}).(*caller); ok {
		c.id = id
	}
}

// logged writes one record for each request: the method, the path (never the
// query), the status, how long it took and, when known, the caller. Nothing of
// the headers or the bodies is logged.
func (s *server) logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		c := &caller{}
		r = r.WithContext(context.WithValue(r.Context(), callerKey{}, c))
		defer func() {
			status := sw.status
			if status == 0 {
				status = http.StatusOK
			}
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Duration("duration", time.Since(start)),
			}
			if c.id != "" {
				attrs = append(attrs, slog.String("user", c.id))
			}
			s.log.LogAttrs(r.Context(), slog.LevelInfo, "request", attrs...)
		}()
		next.ServeHTTP(sw, r)
	})
}

// recovered answers the panic of a handler with 500 internal, logs it with its
// stack, and goes on serving. The panic is not in the answer.
func (s *server) recovered(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				panic(v) // the handler asked for the connection to be closed
			}
			s.log.LogAttrs(r.Context(), slog.LevelError, "panic in handler",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("panic", fmt.Sprint(v)),
				slog.String("stack", string(debug.Stack())),
			)
			// An answer that has begun cannot be replaced.
			if sw, ok := w.(*statusWriter); ok && sw.status != 0 {
				return
			}
			writeError(w, CodeInternal, "internal error")
		}()
		next.ServeHTTP(w, r)
	})
}

// decodeBody reads the JSON body of the request into v, at most 64 KiB of it; see
// decodeBodyLimit.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeBodyLimit(w, r, v, maxBody)
}

// decodeBodyLimit reads the JSON body of the request into v. The body must be
// application/json (parameters allowed), at most limit bytes, one JSON value and
// nothing after it, with no field that v does not have. Otherwise it answers the
// request with 415, 413 or 400, before the handler has written anything, and
// reports false.
func decodeBodyLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	data, ok := readBody(w, r, limit)
	return ok && decodeJSON(w, data, v)
}

// readBody reads the body of the request, which must be application/json
// (parameters allowed) and at most limit bytes; otherwise it answers 415 or 413 and
// reports false. The body is read whole before it is decoded, up to the limit: a
// decoder that fails on the first byte of a body of 100 KiB would otherwise answer
// 400 for a body that is too large, and one that has read a value would not look at
// the rest.
func readBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		writeError(w, CodeUnsupportedMedia, "the body must be application/json")
		return nil, false
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		bodyError(w, err, limit)
		return nil, false
	}
	return data, true
}

// decodeJSON decodes a body into v: one JSON value, nothing after it, and no field
// that v does not have. Otherwise it answers 400 and reports false.
func decodeJSON(w http.ResponseWriter, data []byte, v any) bool {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		bodyError(w, err, 0)
		return false
	}
	// Nothing may follow the value but white space.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		bodyError(w, errTrailingData, 0)
		return false
	}
	return true
}

// errTrailingData is the error of a body that holds more than one JSON value.
var errTrailingData = errors.New("data after the JSON value")

// bodyError answers a body that could not be read; limit is the largest body there
// was room for.
func bodyError(w http.ResponseWriter, err error, limit int64) {
	var tooLarge *http.MaxBytesError
	var wrongType *json.UnmarshalTypeError
	switch {
	case errors.As(err, &tooLarge):
		writeError(w, CodePayloadTooLarge, fmt.Sprintf("the body is larger than %d bytes", limit))
	case errors.Is(err, io.EOF):
		writeError(w, CodeInvalidRequest, "the body is empty")
	case errors.As(err, &wrongType):
		writeError(w, CodeInvalidRequest, fmt.Sprintf("the field %q has the wrong type", wrongType.Field))
	case errors.Is(err, errTrailingData):
		writeError(w, CodeInvalidRequest, "the body has data after the JSON value")
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		writeError(w, CodeInvalidRequest, "the body has a field that is not known: "+strings.TrimPrefix(err.Error(), "json: unknown field "))
	default:
		writeError(w, CodeInvalidRequest, "the body is not valid JSON")
	}
}
