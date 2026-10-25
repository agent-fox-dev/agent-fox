package main

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/api"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The person's credential and the token of the workspace on the fake hub: long, so that a leak
// of either is found in an output.
const (
	personToken    = "caller-credential-sentinel-0123456789"
	workspaceToken = "workspace-token-sentinel-9876543210"
)

// startService starts the agent-fox service in process, its routes, store and jobs layer over a
// fake hub, with a fake runner that takes the steps, and returns its URL and the runner.
func startService(t *testing.T, steps ...fakerunner.Step) (string, *fakerunner.Runner) {
	t.Helper()
	dir := t.TempDir()
	key, err := secrets.NewKey(testutil.Key())
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), filepath.Join(dir, "agent-fox.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fh := fakehub.New(t)
	fh.AddUser(personToken, "usr_1", []string{"widgets"})
	fh.AddUser(workspaceToken, "usr_9", []string{"widgets"})
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	runner := fakerunner.New(steps...)
	svc, err := jobs.New(jobs.Config{
		DB: st.DB(), Subscriptions: st, Logger: logger, Workers: 2, AttemptsDir: filepath.Join(dir, "attempts"), Runner: runner,
		PollInterval: 5 * time.Millisecond, RetryDelay: time.Millisecond, Retain: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		runner.ReleaseAll()
		if err := svc.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	version := (&toolio.App{Name: "agent-fox", Version: "1.2.3"}).VersionDocument()
	srv := api.NewServer(api.Deps{Store: st, Hub: hub.New(fh.URL, "test"), Logger: logger, Version: version, Jobs: svc})
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	return ts.URL, runner
}

// A person subscribes a workspace, queues a triage that stops on a question, and answers it
// through the resume argv, against the real service.
func TestService_SubscribeQueueAndAnswerAJob(t *testing.T) {
	url, runner := startService(t,
		fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which branch?", "main", "release"))),
		fakerunner.Done(fakerunner.WithSummary("triaged on release")),
	)
	home(t, "token = \""+personToken+"\"\n")
	t.Setenv("AF_URL", url)
	useClock(t)
	pat := testutil.WriteFile(t, t.TempDir(), "pat", workspaceToken+"\n")
	var outputs strings.Builder

	for _, summary := range []string{"subscribed the workspace widgets", "replaced the subscription of the workspace widgets"} {
		res := af(t, "workspace", "subscribe", "widgets", "--hub-token-file", pat)
		outputs.WriteString(res.output())
		if res.code != 0 || res.summary() != summary || field(res.result(), "token_state") != "ok" {
			t.Fatalf("subscribe: exit %d, summary %q, result %v, want %q", res.code, res.summary(), res.result(), summary)
		}
	}

	res := af(t, "jobs", "submit", "triage", "42", "--workspace", "widgets", "--wait")
	outputs.WriteString(res.output())
	if res.code != 3 || res.status() != "needs_human" || len(res.artifacts()) != 1 {
		t.Fatalf("submit: exit %d, status %v, artifacts %v, %s; want 3, needs_human and the job", res.code, res.status(), res.artifacts(), res.message())
	}
	id := res.artifacts()[0]
	resume := field(res.env, "needs_human", "resume")
	if argv := joined(field(resume, "argv")); argv != "af jobs answer "+id+" --wait" || field(resume, "flag") != "--context" {
		t.Fatalf("resume %v, want af jobs answer %s --wait with --context", resume, id)
	}

	res = af(t, "jobs", "answer", id, "--wait", "--context", "B")
	outputs.WriteString(res.output())
	if res.code != 0 || res.summary() != "triaged on release" || field(res.result(), "job", "attempt_count") != float64(2) {
		t.Fatalf("answer: exit %d, summary %q, %s; want the second attempt done", res.code, res.summary(), res.message())
	}
	if calls := runner.Calls(); len(calls) != 2 || len(calls[1].Answers) != 1 || calls[1].Answers[0].Text != "release" || calls[1].Answers[0].Option == nil || *calls[1].Answers[0].Option != "B" {
		t.Errorf("the runner was called %d times, the second with %+v; want the option B and its label", len(calls), calls[len(calls)-1].Answers)
	}

	for _, secret := range []string{personToken, workspaceToken} {
		if strings.Contains(outputs.String(), secret) {
			t.Errorf("an output holds %q", secret)
		}
	}
}
