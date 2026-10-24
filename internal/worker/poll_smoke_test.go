package worker

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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

// TestClosingAnIssueCancelsItsRunningJobAndTheWorkerTakesInProgressOff_TS_18_68 verifies TS-18-68 and 18-PATH-2: the
// supervisor runs with its event stream on the fake hub, an event of af:fix on 43 makes a fix job that the real worker runs
// until its loop is under way, with af:in-progress set; then the issue is closed and hub sends the event of the close. The
// job reads cancelled because the issue was closed, with no label to put back, the lease is released, af:in-progress is
// taken off, no other label is added and no comment posted, one INFO names the workspace, the issue and the job, and the id
// of the event is recorded.
// Verifies: 18-PATH-2, 18-REQ-9.1
func TestClosingAnIssueCancelsItsRunningJobAndTheWorkerTakesInProgressOff_TS_18_68(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "begun")
	e := loopEnv(t, blockFiles(marker), nil, nil, blockStep())
	e.hub.AddIssue(slug, fakehub.Issue{Number: 43, Title: "Greeting is wrong", Body: "x", Labels: []string{"af:fix"}})

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
	var logs syncBuffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
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
	poller := poll.New(poll.Config{Subscriptions: st, Hub: hub.New(e.hub.URL, "1"), Jobs: svc, DB: st.DB(), Logger: logger,
		FirstDelay: func() time.Duration { return 1000 * time.Hour }})
	poller.Start(context.Background())
	t.Cleanup(poller.Stop)
	eventually(t, "the stream to connect", 30*time.Second, func() bool { return e.hub.EventConns(slug) == 1 })

	e.hub.SendEvent(slug, fakehub.HubEvent{ID: "evt-fix", Type: fakehub.EventIssueLabeled, Issue: 43, Label: "af:fix"})
	var id string
	eventually(t, "the job of the event", 30*time.Second, func() bool {
		return st.DB().QueryRow("SELECT id FROM af_jobs WHERE workspace = ?", slug).Scan(&id) == nil
	})
	eventually(t, "the loop to begin", 90*time.Second, func() bool { _, err := os.Stat(marker); return err == nil })
	if issue, _ := e.hub.IssueOf(slug, 43); !slices.Contains(issue.Labels, "af:in-progress") {
		t.Fatalf("issue 43 carries %v while its job runs, want af:in-progress", issue.Labels)
	}

	e.hub.SetIssueState(slug, 43, "closed")
	e.hub.SendEvent(slug, fakehub.HubEvent{ID: "evt-closed", Type: fakehub.EventIssueClosed, Issue: 43})
	var doc jobs.Doc
	eventually(t, "the job to end", 90*time.Second, func() bool {
		doc, err = svc.Get(context.Background(), id)
		return err == nil && doc.State == jobs.StateCancelled && len(e.hub.Leases()) == 1 && e.hub.Leases()[0].Released
	})

	if doc.CancelCause == nil || *doc.CancelCause != "issue_closed" || (doc.Labels != nil && doc.Labels.Restore != nil) {
		t.Errorf("the job is %+v, want it cancelled because its issue was closed, with labels.restore null", doc)
	}
	eventually(t, "af:in-progress to be taken off", 30*time.Second, func() bool {
		issue, _ := e.hub.IssueOf(slug, 43)
		return !slices.Contains(issue.Labels, "af:in-progress")
	})
	issue, _ := e.hub.IssueOf(slug, 43)
	if len(issue.Labels) != 0 || len(issue.Comments) != 0 {
		t.Errorf("issue 43 has the labels %v and %d comments, want none", issue.Labels, len(issue.Comments))
	}
	for _, r := range e.hub.RequestsTo("POST", issue43Path+"/labels") {
		if !strings.Contains(string(r.Body), "af:in-progress") {
			t.Errorf("a label other than the mark was added: %s", r.Body)
		}
	}
	taken, err := poll.NewRecords(st.DB()).EventTaken(context.Background(), slug, "evt-closed")
	if err != nil || !taken {
		t.Errorf("the id of the close is recorded %v (%v), want it recorded", taken, err)
	}
	infos := 0
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, `"level":"INFO"`) && strings.Contains(line, "the issue was closed") {
			infos++
			if !strings.Contains(line, `"workspace":"`+slug+`"`) || !strings.Contains(line, `"issue":43`) || !strings.Contains(line, id) {
				t.Errorf("the INFO is %s, want it to name %s, 43 and %s", line, slug, id)
			}
		}
	}
	if infos != 1 {
		t.Errorf("%d INFO records of the close, want 1", infos)
	}
}

// eventually waits until ok says so, and fails the test after the limit.
func eventually(t *testing.T, what string, limit time.Duration, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(limit); !ok(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("waited %v for %s", limit, what)
		}
	}
}

// syncBuffer is a buffer that a logger may write while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
