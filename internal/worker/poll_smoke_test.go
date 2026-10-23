package worker

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/poll"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/triage"
)

// TestATriageLabelBecomesOneJobThatTheRealWorkerRunsToDoneEndToEnd_TS_15_65 verifies TS-15-65, 15-PATH-1 and 15-REQ-10.2:
// the label poll, the real jobs layer on a state file, the real worker with the fake hub, its git server, the in-process
// gateway over the local provider and the scripted brain, and the hub forge client between them. An open issue carries
// af:triage; one poll cycle makes one job whose source is a label and removes the label through hub; the worker runs the job
// to done, rewrites the issue body with a "## Original report" section that is the original body byte for byte, and sets
// af:ready; the next cycle makes no second job.
//
// It is in this package because the harness that runs the worker end to end, and the seam that chooses the scripted brain,
// which no variable may name, are this package's.
// Verifies: 15-PATH-1, 15-REQ-10.2
func TestATriageLabelBecomesOneJobThatTheRealWorkerRunsToDoneEndToEnd_TS_15_65(t *testing.T) {
	e := loopEnv(t, triageFiles, nil, nil, submitStep(probableDiagnosis))
	original := "The app crashes on submit.\n\nSteps: press the button."
	e.hub.AddIssue(slug, fakehub.Issue{Number: 44, Title: "App crashes", Body: original, Labels: []string{"af:triage"}})

	key, err := secrets.NewKey(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "agent-fox.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, _, err := st.Upsert(context.Background(), slug, "af:", pat, "u1", "u1", time.Now()); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc, err := jobs.New(jobs.Config{
		DB: st.DB(), Subscriptions: st, Logger: logger, Workers: 1, AttemptsDir: filepath.Join(t.TempDir(), "attempts"),
		Runner: e.runner, PollInterval: 10 * time.Millisecond, RetryDelay: 10 * time.Millisecond, Retain: 100 * 365 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close() })
	poller := poll.New(poll.Config{Subscriptions: st, Hub: hub.New(e.hub.URL, "1"), Jobs: svc, DB: st.DB(), Logger: logger})

	if _, stop := poller.Cycle(context.Background(), slug); stop {
		t.Fatal("the cycle stopped the loop")
	}

	var id string
	for deadline := time.Now().Add(30 * time.Second); id == ""; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no job was made")
		}
		if err := st.DB().QueryRow("SELECT id FROM af_jobs WHERE workspace = ?", slug).Scan(&id); err != nil {
			id = ""
		}
	}
	var done jobs.Doc
	for deadline := time.Now().Add(90 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if done, err = svc.Get(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		if done.State == jobs.StateDone || done.State == jobs.StateFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the job is %s after 90 seconds", done.State)
		}
	}

	if done.State != jobs.StateDone || done.Kind != jobs.KindTriage || done.Source.Kind != "label" || done.Source.By != "label af:triage" ||
		done.RequestLabel == nil || *done.RequestLabel != "af:triage" {
		t.Fatalf("the job is %s %s from %+v with the request label %v (result %+v), want a triage from the label af:triage, done",
			done.State, done.Kind, done.Source, done.RequestLabel, done.Result)
	}
	removed := false
	for _, r := range e.hub.RequestsTo("DELETE", "/api/v1/workspaces/"+slug+"/issues/44/labels/af:triage") {
		removed = removed || r.TokenHash == fakehub.TokenHash(pat)
	}
	if !removed {
		t.Error("hub saw no removal of af:triage from the issue 44")
	}
	issue, _ := e.hub.IssueOf(slug, 44)
	if kept, _, _ := triage.Extract(issue.Body); kept != original || issue.Body == original {
		t.Errorf("the body is\n%s\nwant a rewrite whose ## Original report is the original byte for byte", issue.Body)
	}
	if !slices.Contains(issue.Labels, "af:ready") || slices.Contains(issue.Labels, "af:triage") {
		t.Errorf("the issue has the labels %v, want af:ready set and af:triage removed", issue.Labels)
	}

	if _, stop := poller.Cycle(context.Background(), slug); stop {
		t.Fatal("the second cycle stopped the loop")
	}
	var n int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM af_jobs WHERE workspace = ?", slug).Scan(&n); err != nil || n != 1 {
		t.Errorf("%d jobs after the second cycle (%v), want still the one", n, err)
	}
}
