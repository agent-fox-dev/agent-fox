package worker

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

const (
	pat       = "pat-of-the-acme-subscription"
	slug      = "acme"
	forgeRepo = "https://github.example.com/Org/Repo"
)

// subs is the store of the subscriptions of a test: a token and a label prefix by slug, and the
// errors the store gives for a slug that is not there or whose token does not open.
type subs struct {
	mu       sync.Mutex
	tokens   map[string]string
	prefixes map[string]string
	getErr   map[string]error
	tokenErr map[string]error
	reads    int // how many times a token was read
}

func (s *subs) Get(_ context.Context, slug string) (store.Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.getErr[slug]; err != nil {
		return store.Subscription{}, err
	}
	if _, ok := s.tokens[slug]; !ok {
		return store.Subscription{}, store.ErrNotSubscribed
	}
	return store.Subscription{Slug: slug, LabelPrefix: s.prefixes[slug]}, nil
}

func (s *subs) Token(_ context.Context, slug string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if err := s.tokenErr[slug]; err != nil {
		return "", err
	}
	tok, ok := s.tokens[slug]
	if !ok {
		return "", store.ErrNotSubscribed
	}
	return tok, nil
}

// sink is a reporter that keeps what it is told.
type sink struct {
	mu     sync.Mutex
	events []sunk
	hubs   [][3]string
}

type sunk struct {
	Type string
	Data json.RawMessage
}

func (s *sink) Emit(typ string, data any) {
	raw, _ := json.Marshal(data)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, sunk{typ, raw})
}

func (s *sink) Hub(sessionID, runID, sandboxID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hubs = append(s.hubs, [3]string{sessionID, runID, sandboxID})
}

func (s *sink) lastHub() [3]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.hubs) == 0 {
		return [3]string{}
	}
	return s.hubs[len(s.hubs)-1]
}

// env is a fake hub with the workspace "acme" and a worker over it.
type env struct {
	t      *testing.T
	hub    *fakehub.Hub
	subs   *subs
	sink   *sink
	runner *Runner
	dir    string
}

// newEnv makes the environment. The workspace is on a GitHub repository Org/Repo, with the
// variables and the mode of w.
func newEnv(t *testing.T, w fakehub.Workspace) *env {
	t.Helper()
	e := &env{t: t, hub: fakehub.New(t), sink: &sink{}, dir: t.TempDir()}
	if w.Slug == "" {
		w.Slug = slug
	}
	if w.PAT == "" {
		w.PAT = pat
	}
	if w.ForgeURL == "" {
		w.ForgeURL = forgeRepo
	}
	e.hub.AddWorkspace(t, w)
	e.subs = &subs{
		tokens: map[string]string{w.Slug: w.PAT}, prefixes: map[string]string{w.Slug: "af:"},
		getErr: map[string]error{}, tokenErr: map[string]error{},
	}
	e.runner = New(Config{Hub: hub.New(e.hub.URL, "1"), Subscriptions: e.subs, Version: "v-test", Commit: "c-test"})
	e.runner.reporter = e.sink
	return e
}

// request is the request of an attempt of a job of the kind on the input.
func (e *env) request(kind jobs.Kind, input jobs.Input, mutate ...func(*jobs.Request)) jobs.Request {
	req := jobs.Request{
		JobID: "job_0123456789abcdef0123", Attempt: 1, Delivery: 1, Workspace: slug, Kind: kind, Input: input,
		Context: []string{}, Answers: []jobs.Answer{}, Source: jobs.Source{Kind: "api", By: "u1"}, Dir: e.dir,
	}
	for _, m := range mutate {
		m(&req)
	}
	return req
}

// issueInput is the input of an issue given as the JSON.
func issueInput(raw string) jobs.Input { return jobs.Input{Issue: json.RawMessage(raw)} }

// run runs the attempt.
func (e *env) run(req jobs.Request) (jobs.Result, error) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return e.runner.Run(ctx, req)
}

// envelope decodes the envelope of a result.
func envelope(t *testing.T, res jobs.Result) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(res.Envelope, &env); err != nil {
		t.Fatalf("the result holds no envelope: %v\n%s", err, res.Envelope)
	}
	return env
}

// errorOf is the error object of an envelope.
func errorOf(env map[string]any) (category, message string, retryable bool, hint map[string]any) {
	obj, _ := env["error"].(map[string]any)
	category, _ = obj["category"].(string)
	message, _ = obj["message"].(string)
	retryable, _ = obj["retryable"].(bool)
	hint, _ = obj["fix_hint"].(map[string]any)
	return
}

// transient is the failure of a transient error, or nil when err is none.
func transient(err error) *toolio.Failure {
	if !jobs.IsTransient(err) {
		return nil
	}
	var failure *toolio.Failure
	if !errors.As(err, &failure) {
		return nil
	}
	return failure
}

// writes are the requests of the fake hub that changed the forge or took a resource: a label, a
// comment, an issue, a pull request, a lease, a clone or a session.
func (e *env) writes() []fakehub.Request {
	var out []fakehub.Request
	for _, r := range e.hub.Requests() {
		if r.Method != "GET" {
			out = append(out, r)
		}
	}
	return out
}
