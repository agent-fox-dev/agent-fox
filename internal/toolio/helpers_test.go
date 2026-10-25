package toolio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// TestMain keeps the tests on the loopback interface and independent of the
// environment: the default HTTP transport refuses any other address, and the
// variables the forge clients read are unset, so a test that wants one sets
// it itself.
func TestMain(m *testing.M) {
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "GITHUB_API_URL", "GITLAB_API_URL"} {
		_ = os.Unsetenv(name)
	}
	http.DefaultTransport = &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("refusing to dial %s: the tests reach only the loopback interface", addr)
		}
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}}
	os.Exit(m.Run())
}

// newApp is an app with a fixed version.
func newApp() *App { return &App{Version: "v0.0.0-test"} }

// result is what one invocation of Main returned and wrote.
type result struct {
	code           int
	stdout, stderr string
}

// env is stdout decoded as one JSON object.
func (r result) env(t *testing.T) map[string]any {
	t.Helper()
	return testutil.Decode[map[string]any](t, []byte(r.stdout))
}

// run runs the app with "af" as argv[0], an empty stdin and no deadline.
func run(t *testing.T, app *App, args ...string) result {
	t.Helper()
	return runWith(t, context.Background(), app, strings.NewReader(""), args...)
}

// runWith is run under ctx with stdin.
func runWith(t *testing.T, ctx context.Context, app *App, stdin io.Reader, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := app.Main(ctx, append([]string{"af"}, args...), stdin, &out, &errb)
	return result{code, out.String(), errb.String()}
}

// at follows path through nested JSON objects and returns what is there, or
// nil.
func at(v any, path ...string) any {
	for _, p := range path {
		obj, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = obj[p]
	}
	return v
}

// strs is a JSON array of strings as a slice.
func strs(v any) []string {
	var out []string
	list, _ := v.([]any)
	for _, s := range list {
		out = append(out, fmt.Sprint(s))
	}
	return out
}

// warningCodes are the codes of an envelope's warnings, in order.
func warningCodes(env map[string]any) []string {
	var codes []string
	list, _ := env["warnings"].([]any)
	for _, w := range list {
		codes = append(codes, fmt.Sprint(at(w, "code")))
	}
	return codes
}

// topLevelKeys are the keys of the JSON object raw, in document order.
func topLevelKeys(t *testing.T, raw string) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not a JSON object: %v %v", tok, err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

// returning is a command that ends with out and err.
func returning(path string, out Outcome, err error) Command {
	return Command{Path: path, Run: func(*Run) (Outcome, error) { return out, err }}
}

// recorder makes commands that record the Run they were given and then end
// with then, or done when then is nil.
type recorder struct {
	ran  int
	last *Run
	then func(*Run) (Outcome, error)
}

func (r *recorder) command(c Command) Command {
	c.Run = func(run *Run) (Outcome, error) {
		r.ran++
		r.last = run
		if r.then != nil {
			return r.then(run)
		}
		return Outcome{Status: StatusDone, Summary: "ok"}, nil
	}
	return c
}

// agentFoxDir is where the shell keeps its local state under dir.
func agentFoxDir(dir string) string { return filepath.Join(dir, ".agent-fox") }

// stubForge is a forge client that counts its closes.
type stubForge struct {
	issuex.Client
	closes int
}

func newStubForge() *stubForge { return &stubForge{Client: issuex.NewNoOp()} }

func (s *stubForge) Close() error {
	s.closes++
	return nil
}

// MaxBodyLength is the body limit of the client the stub wraps.
func (s *stubForge) MaxBodyLength() int { return issuex.MaxBodyLength(s.Client) }

// forgeApp is an app whose NewForge records the options it is given and
// returns stub.
func forgeApp(stub *stubForge) (*App, *[]issuex.Options) {
	var got []issuex.Options
	app := newApp()
	app.NewForge = func(o issuex.Options) (issuex.Client, error) {
		got = append(got, o)
		return stub, nil
	}
	return app, &got
}

// closeProbe is a stdout that notes how often the stub had been closed when
// the envelope was first written.
type closeProbe struct {
	stub          *stubForge
	closesAtWrite int
	wrote         bool
}

func (p *closeProbe) Write(b []byte) (int, error) {
	if !p.wrote {
		p.wrote, p.closesAtWrite = true, p.stub.closes
	}
	return len(b), nil
}

// githubEnv points the environment at a GitHub forge at url, with no token.
func githubEnv(t *testing.T, url string) {
	t.Helper()
	for _, name := range []string{"GITLAB_API_URL", "GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN"} {
		t.Setenv(name, "")
	}
	t.Setenv("GITHUB_API_URL", url)
}

// gitlabEnv points the environment at a GitLab forge at url, with no token.
func gitlabEnv(t *testing.T, url string) {
	t.Helper()
	githubEnv(t, "")
	t.Setenv("GITLAB_API_URL", url)
}

// githubForge answers the reads of issue 7 of o/r, a pull request when
// asPullRequest, and its comments with comments (one comment when nil).
func githubForge(t *testing.T, asPullRequest bool, comments http.HandlerFunc) *httptest.Server {
	t.Helper()
	pr := ""
	if asPullRequest {
		pr = `,"pull_request":{"url":"x"}`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/issues/7/comments") && comments != nil:
			comments(w, r)
		case strings.HasSuffix(r.URL.Path, "/issues/7/comments"):
			fmt.Fprint(w, `[{"body":"me too","user":{"login":"bob"}}]`)
		case strings.HasSuffix(r.URL.Path, "/issues/7"):
			fmt.Fprint(w, `{"number":7,"title":"Login fails","body":"  It crashes on submit.  ","state":"open",`+
				`"user":{"login":"alice"},"labels":[{"name":"bug"}]`+pr+`}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// recordingForge is a forge that answers the reads of both forges with
// documents that are empty but valid, and notes every request.
type recordingForge struct {
	*httptest.Server
	mu       sync.Mutex
	requests []string // "METHOD path"
}

func newRecordingForge(t *testing.T) *recordingForge {
	t.Helper()
	f := &recordingForge{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/changes"):
			fmt.Fprint(w, `{"changes":[]}`)
		case strings.HasSuffix(p, "/approvals"):
			fmt.Fprint(w, `{"approved_by":[]}`)
		case strings.Contains(p, "/check-runs"):
			fmt.Fprint(w, `{"total_count":0,"check_runs":[]}`)
		case strings.HasSuffix(p, "/comments"), strings.HasSuffix(p, "/notes"), strings.HasSuffix(p, "/files"),
			strings.HasSuffix(p, "/reviews"), strings.HasSuffix(p, "/pipelines"), strings.HasSuffix(p, "/issues"):
			fmt.Fprint(w, `[]`)
		case strings.HasSuffix(p, "/pulls/7"):
			fmt.Fprint(w, `{"number":7,"state":"open","head":{"sha":"abc123","ref":"fix"},"base":{"ref":"main"}}`)
		case strings.HasSuffix(p, "/merge_requests/7"):
			fmt.Fprint(w, `{"iid":7,"state":"opened","sha":"abc123"}`)
		case strings.HasSuffix(p, "/issues/7"):
			fmt.Fprint(w, `{"number":7,"iid":7,"title":"T","state":"open"}`)
		default:
			fmt.Fprint(w, `{"full_name":"o/r","path_with_namespace":"o/r","default_branch":"main"}`)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// seen are the requests so far.
func (f *recordingForge) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// writes counts the requests that are not reads.
func (f *recordingForge) writes() int {
	n := 0
	for _, r := range f.seen() {
		if !strings.HasPrefix(r, "GET ") {
			n++
		}
	}
	return n
}

// sinkEvents collects, decoded, the event lines the app hands its EventSink.
func sinkEvents(t *testing.T, app *App) *[]map[string]any {
	t.Helper()
	var mu sync.Mutex
	events := []map[string]any{}
	app.EventSink = func(line []byte) {
		var e map[string]any
		if err := json.Unmarshal(line, &e); err != nil { // the sink may be called from the command's goroutine
			t.Errorf("%q is not a JSON line: %v", line, err)
		}
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	}
	return &events
}

// eventTypes are the types of events, in order.
func eventTypes(events []map[string]any) []string {
	var types []string
	for _, e := range events {
		types = append(types, fmt.Sprint(e["type"]))
	}
	return types
}
