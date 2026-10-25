package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/afclient/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// request is a request the fake service saw.
type request struct {
	Method, Path, URI string
	Header            http.Header
	Body              []byte
}

// body is the request's JSON body, or nil when it has none.
func (r request) body(t *testing.T) any {
	t.Helper()
	if len(r.Body) == 0 {
		return nil
	}
	return testutil.Decode[any](t, r.Body)
}

// service is a stand-in for the agent-fox service that records the requests it answers.
type service struct {
	*httptest.Server
	mu   sync.Mutex
	seen []request
}

// newService starts a service that answers with handler, or with an empty list of workspaces.
func newService(t *testing.T, handler http.HandlerFunc) *service {
	t.Helper()
	if handler == nil {
		handler = answer(200, `{"workspaces":[]}`)
	}
	s := &service{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.seen = append(s.seen, request{r.Method, r.URL.Path, r.RequestURI, r.Header.Clone(), data})
		s.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// requests are the requests seen so far, in order.
func (s *service) requests() []request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.seen)
}

// lines are the requests seen so far as "METHOD request-URI".
func (s *service) lines() []string {
	var out []string
	for _, r := range s.requests() {
		out = append(out, r.Method+" "+r.URI)
	}
	return out
}

// last is the last request seen.
func (s *service) last(t *testing.T) request {
	t.Helper()
	reqs := s.requests()
	if len(reqs) == 0 {
		t.Fatal("the service saw no request")
	}
	return reqs[len(reqs)-1]
}

// serve starts a service with handler, and logs the test in to it: a home whose credential is T,
// AF_URL naming the service, and a clock on which the pauses of a following cost nothing.
func serve(t *testing.T, handler http.HandlerFunc) *service {
	t.Helper()
	s := newService(t, handler)
	home(t, "token = \"T\"\n")
	t.Setenv("AF_URL", s.URL)
	useClock(t)
	return s
}

// home makes a home directory for the test, with the credential file holding content unless it
// is empty, and returns the path of the credential file.
func home(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	path := filepath.Join(dir, ".af", "config.toml")
	if content != "" {
		testutil.WriteFile(t, dir, ".af/config.toml", content)
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// useClock makes the requests of the commands take their timers from a fake clock.
func useClock(t *testing.T) {
	old := clientClock
	clientClock = fakeclock.New(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC))
	t.Cleanup(func() { clientClock = old })
}

// answer is a handler that answers every request with status and a JSON body.
func answer(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// stream is a handler that sends the frames of an event stream.
func stream(frames ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, strings.Join(frames, ""))
	}
}

// routes is a handler that answers "METHOD /escaped/path" with the handler of that key, and any
// other request with a 404.
func routes(m map[string]http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h, ok := m[r.Method+" "+r.URL.EscapedPath()]; ok {
			h(w, r)
			return
		}
		answer(404, `{"error":{"code":"not_found","message":"no such route"}}`)(w, r)
	}
}

// eventDoc is the document of the event seq of the job j.
func eventDoc(seq int) string {
	return fmt.Sprintf(`{"seq":%d,"ts":"2026-10-23T12:00:00.000Z","job_id":"j","attempt":1,"type":"step","data":{"n":%d}}`, seq, seq)
}

// frame is the event seq of the job j as a frame of the stream.
func frame(seq int) string {
	return fmt.Sprintf("id: %d\nevent: step\ndata: %s\n\n", seq, eventDoc(seq))
}

// jobJSON is the document of the job j in state, with its result and blocked objects ("" is
// null) and an attempt in each of the states.
func jobJSON(state, result, blocked string, attempts ...string) string {
	null := func(s string) string {
		if s == "" {
			return "null"
		}
		return s
	}
	var list []string
	for i, s := range attempts {
		list = append(list, fmt.Sprintf(`{"n":%d,"state":%q}`, i+1, s))
	}
	return fmt.Sprintf(`{"id":"j","workspace":"widgets","state":%q,"future":{"kept":true},"result":%s,"blocked":%s,"attempts":[%s]}`,
		state, null(result), null(blocked), strings.Join(list, ","))
}

// doneJob is the job j done, with one attempt.
var doneJob = jobJSON("done", `{"status":"done","exit_code":0,"summary":"fixed","error":null}`, "", "done")

// atRest is a handler for the job j: its stream sends event 1, the envelope of its attempt 1 is
// answered by envelope (a 404 when it is nil), and any other request reads job.
func atRest(job string, envelope http.HandlerFunc) http.HandlerFunc {
	if envelope == nil {
		envelope = answer(404, `{"error":{"code":"not_found","message":"no such envelope"}}`)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/events"):
			stream(frame(1))(w, r)
		case r.URL.Path == "/api/v1/jobs/j/attempts/1/envelope":
			envelope(w, r)
		default:
			answer(200, job)(w, r)
		}
	}
}

// afResult is what one invocation of the binary's shell wrote.
type afResult struct {
	code                   int
	env                    map[string]any
	stdout, stderr, events string
}

// af runs the binary's shell with args and an empty stdin.
func af(t *testing.T, args ...string) afResult {
	t.Helper()
	return afWith(t, context.Background(), strings.NewReader(""), args...)
}

// afWith runs the binary's shell with args, stdin and ctx, and collects its events.
func afWith(t *testing.T, ctx context.Context, stdin io.Reader, args ...string) afResult {
	t.Helper()
	var out, errb, events bytes.Buffer
	var mu sync.Mutex
	app := newApp()
	app.EventSink = func(line []byte) {
		mu.Lock()
		defer mu.Unlock()
		events.Write(line)
		events.WriteByte('\n')
	}
	code := app.Main(ctx, append([]string{"af"}, args...), stdin, &out, &errb)
	res := afResult{code: code, stdout: out.String(), stderr: errb.String(), events: events.String()}
	if out.Len() > 0 {
		res.env = testutil.Decode[map[string]any](t, out.Bytes())
	}
	return res
}

// field follows keys through nested objects.
func field(v any, keys ...string) any {
	for _, k := range keys {
		obj, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = obj[k]
	}
	return v
}

func (r afResult) status() any    { return r.env["status"] }
func (r afResult) category() any  { return field(r.env, "error", "category") }
func (r afResult) retryable() any { return field(r.env, "error", "retryable") }
func (r afResult) hint() any      { return field(r.env, "error", "fix_hint") }
func (r afResult) result() any    { return r.env["result"] }
func (r afResult) output() string { return r.stdout + r.stderr + r.events }

func (r afResult) message() string { return str(field(r.env, "error", "message")) }
func (r afResult) summary() string { return str(r.env["summary"]) }

// warnings are the codes of the warnings of the envelope.
func (r afResult) warnings() []string {
	return each(r.env["warnings"], func(w any) string { return str(field(w, "code")) })
}

// next are the argv of the next entries of the envelope, each joined with spaces.
func (r afResult) next() []string {
	return each(r.env["next"], func(n any) string { return joined(field(n, "argv")) })
}

// artifacts are the names of the artifacts of the envelope.
func (r afResult) artifacts() []string {
	return each(r.env["artifacts"], func(a any) string { return str(field(a, "name")) })
}

// joined is a JSON array of strings joined with spaces.
func joined(v any) string { return strings.Join(each(v, str), " ") }

// each is f of each element of the JSON array v.
func each(v any, f func(any) string) []string {
	var out []string
	list, _ := v.([]any)
	for _, x := range list {
		out = append(out, f(x))
	}
	return out
}

// str is v if it is a string, else "".
func str(v any) string {
	s, _ := v.(string)
	return s
}

// value is a JSON text decoded, for comparing with what the envelope holds.
func value(t *testing.T, s string) any {
	t.Helper()
	return testutil.Decode[any](t, []byte(s))
}
