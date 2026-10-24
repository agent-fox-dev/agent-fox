package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
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
	if _, _, err := st.Upsert(context.Background(), slug, pat, "u1", "u1", time.Now()); err != nil {
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

// eventually waits until ok says so, and fails the test after the limit.
func eventually(t *testing.T, what string, limit time.Duration, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(limit); !ok(); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("waited %v for %s", limit, what)
		}
	}
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
	if _, _, err := st.Upsert(context.Background(), slug, pat, "u1", "u1", time.Now()); err != nil {
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
