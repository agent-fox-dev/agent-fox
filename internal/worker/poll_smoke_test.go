package worker

import (
	"bytes"
	"context"
	"encoding/json"
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

// pollRig is the poll, the real jobs layer and its store on a state file, over the worker of the environment, which reports
// through the jobs layer's recorder of each attempt.
func pollRig(t *testing.T, e *env) (*store.Store, *jobs.Service, *poll.Supervisor) {
	t.Helper()
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
	e.runner.reporter = nil
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
	return st, svc, poll.New(poll.Config{Subscriptions: st, Hub: hub.New(e.hub.URL, "1"), Jobs: svc, DB: st.DB(), Logger: logger})
}

// rest waits until the job is done or failed, and returns it.
func rest(t *testing.T, svc *jobs.Service, id string) jobs.Doc {
	t.Helper()
	var doc jobs.Doc
	eventually(t, "the job "+id+" to end", 90*time.Second, func() bool {
		var err error
		doc, err = svc.Get(context.Background(), id)
		return err == nil && (doc.State == jobs.StateDone || doc.State == jobs.StateFailed)
	})
	return doc
}

// onlyJob is the one job of the kind on the workspace, waited for.
func onlyJob(t *testing.T, st *store.Store, kind jobs.Kind, n int) string {
	t.Helper()
	var ids []string
	eventually(t, "the "+string(kind)+" job", 30*time.Second, func() bool {
		rows, err := st.DB().Query("SELECT id FROM af_jobs WHERE workspace = ? AND kind = ? ORDER BY created_at, rowid", slug, string(kind))
		if err != nil {
			return false
		}
		defer rows.Close()
		ids = nil
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			ids = append(ids, id)
		}
		return len(ids) >= n
	})
	if len(ids) != n {
		t.Fatalf("%d %s jobs, want %d", len(ids), kind, n)
	}
	return ids[n-1]
}

// TestAutoFixChainsATriageIntoALinkedFixJobEndToEnd_TS_19_62 verifies TS-19-62 and 19-PATH-5: issue 44 carries af:triage on a
// workspace with AF_AUTO_FIX=true; one cycle makes the triage job, the real worker runs it with the scripted brain to
// af:ready, records its follow-up pending through the jobs layer and adds af:fix; the next cycle makes the fix job linked
// to the triage, and the triage's follow-up is made with it.
// Verifies: 19-PATH-5, 19-REQ-9.1, 19-REQ-7.3
func TestAutoFixChainsATriageIntoALinkedFixJobEndToEnd_TS_19_62(t *testing.T) {
	e := loopEnv(t, triageFiles, map[string]string{"AF_AUTO_FIX": "true"}, nil, submitStep(probableDiagnosis))
	e.hub.AddIssue(slug, fakehub.Issue{Number: 44, Title: "App crashes", Body: "The app crashes on submit.", Labels: []string{"af:triage"}})
	st, svc, poller := pollRig(t, e)

	poller.Cycle(context.Background(), slug)
	triage := onlyJob(t, st, jobs.KindTriage, 1)
	doc := rest(t, svc, triage)
	if doc.State != jobs.StateDone {
		t.Fatalf("the triage is %s: %+v", doc.State, doc.Result)
	}
	if f := doc.FollowUp; f == nil || f.Label != "af:fix" || f.Issue != 44 || f.State != jobs.FollowUpPending {
		t.Fatalf("the triage's follow-up is %+v, want af:fix on 44, pending", doc.FollowUp)
	}
	if issue, _ := e.hub.IssueOf(slug, 44); !slices.Contains(issue.Labels, "af:ready") || !slices.Contains(issue.Labels, "af:fix") {
		t.Fatalf("issue 44 carries %v, want af:ready and af:fix", issue.Labels)
	}

	poller.Cycle(context.Background(), slug)
	fix := onlyJob(t, st, jobs.KindFix, 1)
	fd, err := svc.Get(context.Background(), fix)
	if err != nil {
		t.Fatal(err)
	}
	if fd.Source != (jobs.Source{Kind: "triage", By: triage}) || fd.ParentJobID == nil || *fd.ParentJobID != triage {
		t.Errorf("the fix job is from %+v with the parent %v, want the triage %s", fd.Source, fd.ParentJobID, triage)
	}
	td, _ := svc.Get(context.Background(), triage)
	if f := td.FollowUp; f == nil || f.State != jobs.FollowUpMade || f.JobID == nil || *f.JobID != fix {
		t.Errorf("the triage's follow-up is %+v, want made with %s", td.FollowUp, fix)
	}
	rest(t, svc, fix) // the fix runs to its end before the state file closes
}

// TestARelabelledTriagedIssueGetsANewTriageThatKeepsTheEditedReport_TS_19_63 verifies TS-19-63 and 19-PATH-6: issue 44's
// latest triage job is done and its body carries the diagnosis after the original report; a person edits the original
// report and adds af:triage; one cycle makes a new triage job of the label and leaves the done one as it was, and the real
// worker replaces the diagnosis and keeps the edited report.
// Verifies: 19-PATH-6, 19-REQ-1.6
func TestARelabelledTriagedIssueGetsANewTriageThatKeepsTheEditedReport_TS_19_63(t *testing.T) {
	e := loopEnv(t, triageFiles, nil, nil, submitStep(probableDiagnosis))
	original := "The app crashes on submit.\n\nSteps: press the button."
	e.hub.AddIssue(slug, fakehub.Issue{Number: 44, Title: "App crashes", Body: original, Labels: []string{"af:triage"}})
	st, svc, poller := pollRig(t, e)
	poller.Cycle(context.Background(), slug)
	first := onlyJob(t, st, jobs.KindTriage, 1)
	if d := rest(t, svc, first); d.State != jobs.StateDone {
		t.Fatalf("the first triage is %s", d.State)
	}
	issue, _ := e.hub.IssueOf(slug, 44)
	if kept, _, _ := triage.Extract(issue.Body); kept != original {
		t.Fatalf("the first triage kept %q, want the original report", kept)
	}
	before, _ := svc.Get(context.Background(), first)

	edited := "The app crashes on submit when it is offline.\n\nSteps: press the button."
	firstBody := issue.Body
	issue.Body = strings.Replace(issue.Body, original, edited, 1)
	issue.Labels = append(issue.Labels, "af:triage")
	e.hub.AddIssue(slug, issue)
	poller.Cycle(context.Background(), slug)
	second := onlyJob(t, st, jobs.KindTriage, 2)
	sd := rest(t, svc, second)
	if sd.State != jobs.StateDone || sd.Source != (jobs.Source{Kind: "label", By: "label af:triage"}) {
		t.Fatalf("the second triage is %s from %+v, want done, from the label", sd.State, sd.Source)
	}
	if after, _ := svc.Get(context.Background(), first); !jsonEqualDocs(before, after) {
		t.Errorf("the done triage changed:\n%+v\n%+v", before, after)
	}
	now, _ := e.hub.IssueOf(slug, 44)
	kept, _, _ := triage.Extract(now.Body)
	if kept != edited {
		t.Errorf("the second triage kept %q, want the edited report %q", kept, edited)
	}
	if firstLine(now.Body) == firstLine(firstBody) || strings.Count(now.Body, triage.BeginMarker) != 1 {
		t.Errorf("the diagnosis was not replaced:\n%s", now.Body)
	}
}

// jsonEqualDocs says whether the two documents are the same as JSON.
func jsonEqualDocs(a, b jobs.Doc) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// firstLine is the first line of the text.
func firstLine(s string) string { first, _, _ := strings.Cut(s, "\n"); return first }
