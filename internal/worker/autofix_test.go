package worker

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// readyTriage makes the loop of a triage end done with the confidence, as the worker takes it.
func (e *env) readyTriage(confidence string) {
	e.endsWith(jobs.KindTriage, toolio.StatusDone, withResult(map[string]any{"action": "rewrote_body", "diagnosis": map[string]any{"confidence": confidence}}))
}

// warningsOf are the codes of the warnings of an envelope, with their messages.
func warningsOf(env map[string]any) map[string]string {
	out := map[string]string{}
	list, _ := env["warnings"].([]any)
	for _, w := range list {
		m, _ := w.(map[string]any)
		code, _ := m["code"].(string)
		message, _ := m["message"].(string)
		out[code] = message
	}
	return out
}

// fixAdds are the additions of af:fix to issue 42 that hub was asked for.
func (e *env) fixAdds() int {
	n := 0
	for _, r := range e.hub.RequestsTo("POST", "/api/v1/workspaces/acme/issues/42/labels") {
		if strings.Contains(string(r.Body), `"af:fix"`) {
			n++
		}
	}
	return n
}

// kinds is a runner of the jobs layer that ends every fix attempt blocked at once and runs every triage with the worker.
type kinds struct{ worker *Runner }

func (k kinds) Run(ctx context.Context, req jobs.Request) (jobs.Result, error) {
	if req.Kind == jobs.KindFix {
		return jobs.Result{Envelope: fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithTool("fix"), fakerunner.WithQuestion("Which?", "a"))}, nil
	}
	return k.worker.Run(ctx, req)
}

// withJobs runs the worker under the real jobs layer on a state file, which reports through the jobs layer's recorder of
// each attempt, and returns the layer and the state file.
func (e *env) withJobs(runner jobs.Runner) (*jobs.Service, *sql.DB) {
	e.t.Helper()
	key, err := secrets.NewKey(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		e.t.Fatal(err)
	}
	st, err := store.Open(context.Background(), filepath.Join(e.t.TempDir(), "agent-fox.db"), key)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { st.Close() })
	if _, _, err := st.Upsert(context.Background(), slug, "af:", pat, "u1", "u1", time.Now()); err != nil {
		e.t.Fatal(err)
	}
	e.runner.reporter = nil
	svc, err := jobs.New(jobs.Config{
		DB: st.DB(), Subscriptions: st, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Workers: 1,
		AttemptsDir: filepath.Join(e.t.TempDir(), "attempts"), Runner: runner,
		PollInterval: 10 * time.Millisecond, RetryDelay: 10 * time.Millisecond, Retain: 100 * 365 * 24 * time.Hour,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := svc.Start(); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { svc.Close() })
	return svc, st.DB()
}

// atRest waits until the job is at rest, and returns its document.
func atRest(t *testing.T, svc *jobs.Service, id string) jobs.Doc {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		doc, err := svc.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if doc.State == jobs.StateDone || doc.State == jobs.StateFailed || doc.State == jobs.StateBlocked || doc.State == jobs.StateCancelled {
			return doc
		}
		if time.Now().After(deadline) {
			t.Fatalf("the job is %s after 30 seconds", doc.State)
		}
	}
}

// labelTriage makes a triage job of issue 42 as the poll does.
func labelJob(t *testing.T, svc *jobs.Service, kind jobs.Kind) string {
	t.Helper()
	doc, err := svc.Create(context.Background(), jobs.CreateRequest{Workspace: slug, Kind: kind, Body: jobs.Body{Issue: json.RawMessage("42")},
		Source: jobs.Source{Kind: "label", By: "label af:" + string(kind)}, RequestLabel: "af:" + string(kind)})
	if err != nil {
		t.Fatal(err)
	}
	return doc.ID
}

// TestAReadyTriageWithAutoFixRecordsAPendingFollowUpAndThenAddsTheFixLabel_TS_19_38 verifies TS-19-38: under the real jobs
// layer, a confirmed triage of issue 42 records its follow-up before af:fix is written, and the triage job shows it pending,
// with af:fix in its labels.set and side effects; a triage of a text names the issue the loop created.
// Verifies: 19-REQ-7.3
func TestAReadyTriageWithAutoFixRecordsAPendingFollowUpAndThenAddsTheFixLabel_TS_19_38(t *testing.T) {
	e := mirrorEnv(t, []string{"af:triage"}, map[string]string{"AF_AUTO_FIX": "true"})
	e.readyTriage("confirmed")
	svc, db := e.withJobs(e.runner)
	var mu sync.Mutex
	recordedFirst := ""
	e.hub.OnRequest(func(r fakehub.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.Path, "/issues/42/labels") && strings.Contains(string(r.Body), `"af:fix"`) {
			var state string
			_ = db.QueryRow("SELECT state FROM af_followups").Scan(&state)
			mu.Lock()
			recordedFirst = state
			mu.Unlock()
		}
	})
	id := labelJob(t, svc, jobs.KindTriage)
	doc := atRest(t, svc, id)
	if doc.State != jobs.StateDone {
		t.Fatalf("the triage is %s: %+v", doc.State, doc.Result)
	}
	if f := doc.FollowUp; f == nil || f.Label != "af:fix" || f.Issue != 42 || f.State != jobs.FollowUpPending || f.JobID != nil {
		t.Errorf("the follow-up is %+v, want af:fix on 42, pending, with no job", doc.FollowUp)
	}
	mu.Lock()
	if recordedFirst != jobs.FollowUpPending {
		t.Errorf("when af:fix was written the follow-up was %q, want it recorded pending already", recordedFirst)
	}
	mu.Unlock()
	if doc.Labels == nil || !slices.Contains(doc.Labels.Set, "af:fix") {
		t.Errorf("labels.set is %+v, want af:fix in it", doc.Labels)
	}
	if !slices.ContainsFunc(doc.SideEffects, func(s toolio.SideEffect) bool { return s.Kind == "add_labels" && s.State == toolio.StateDone }) || e.fixAdds() != 1 {
		t.Errorf("the side effects are %+v and af:fix was added %d times, want it added once", doc.SideEffects, e.fixAdds())
	}

	t.Run("a text triage names the issue the loop created", func(t *testing.T) {
		e := mirrorEnv(t, nil, map[string]string{"AF_AUTO_FIX": "true"})
		e.hub.AddIssue(slug, fakehub.Issue{Number: 7, Title: "The loop made this", Body: "x"})
		e.endsWith(jobs.KindTriage, toolio.StatusDone, withResult(map[string]any{
			"action": "created_issue", "diagnosis": map[string]any{"confidence": "probable"},
			"issue": map[string]any{"repo": "Org/Repo", "host": "github.example.com", "number": 7, "url": forgeRepo + "/issues/7"},
		}))
		res, err := e.run(e.request(jobs.KindTriage, jobs.Input{Text: "The greeting is wrong."}))
		if err != nil || envelope(t, res)["status"] != "done" {
			t.Fatalf("result %s, error %v", res.Envelope, err)
		}
		if !slices.Equal(e.sink.followUps, []string{"7 af:fix"}) {
			t.Errorf("the follow-ups asked for are %v, want af:fix on 7", e.sink.followUps)
		}
		if created, _ := e.hub.IssueOf(slug, 7); !slices.Contains(created.Labels, "af:fix") {
			t.Errorf("the created issue carries %v, want af:fix", created.Labels)
		}
	})
}

// TestAnOpenFixJobAtAskTimeAbsorbsTheFollowUpAndNoLabelIsAdded_TS_19_39 verifies TS-19-39 under the real jobs layer.
// Verifies: 19-REQ-7.4
func TestAnOpenFixJobAtAskTimeAbsorbsTheFollowUpAndNoLabelIsAdded_TS_19_39(t *testing.T) {
	e := mirrorEnv(t, []string{"af:triage"}, map[string]string{"AF_AUTO_FIX": "true"})
	e.readyTriage("confirmed")
	svc, _ := e.withJobs(kinds{worker: e.runner})
	fix := labelJob(t, svc, jobs.KindFix)
	if d := atRest(t, svc, fix); d.State != jobs.StateBlocked {
		t.Fatalf("the fix is %s, want blocked", d.State)
	}
	triage := labelJob(t, svc, jobs.KindTriage)
	doc := atRest(t, svc, triage)
	if f := doc.FollowUp; f == nil || f.State != jobs.FollowUpAbsorbed || f.JobID == nil || *f.JobID != fix {
		t.Errorf("the follow-up is %+v, want absorbed by %s", doc.FollowUp, fix)
	}
	if n := e.fixAdds(); n != 0 {
		t.Errorf("af:fix was added %d times, want never", n)
	}
}

// TestTheSkipCasesRecordNothingAddNoLabelAndWarnWithTheReason_TS_19_40 verifies TS-19-40.
// Verifies: 19-REQ-7.5
func TestTheSkipCasesRecordNothingAddNoLabelAndWarnWithTheReason_TS_19_40(t *testing.T) {
	cases := map[string]struct {
		vars   map[string]string
		mode   string
		reason string
	}{
		"the label triggers off":  {map[string]string{"AF_POLL_INTERVAL": "0"}, "", "AF_POLL_INTERVAL"},
		"a carry_patch workspace": {nil, "carry_patch", "carry_patch"},
		"landing as a patch":      {map[string]string{"AF_FIX_LAND": "patch"}, "", "AF_FIX_LAND"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			vars := map[string]string{"AF_AUTO_FIX": "true"}
			for k, v := range tc.vars {
				vars[k] = v
			}
			e := mirrorEnv(t, nil, vars)
			if tc.mode != "" {
				e.hub.UpdateWorkspace(slug, func(w *fakehub.Workspace) { w.Mode = tc.mode })
			}
			e.readyTriage("confirmed")
			res, err := e.run(e.request(jobs.KindTriage, issueInput("42")))
			if err != nil {
				t.Fatal(err)
			}
			env := envelope(t, res)
			if env["status"] != "done" {
				t.Fatalf("the triage is %v: %s", env["status"], res.Envelope)
			}
			if msg, ok := warningsOf(env)["auto_fix_skipped"]; !ok || !strings.Contains(msg, tc.reason) {
				t.Errorf("the warnings are %v, want auto_fix_skipped naming %s", warningsOf(env), tc.reason)
			}
			if len(e.sink.followUps) != 0 || e.fixAdds() != 0 {
				t.Errorf("follow-ups %v and %d additions of af:fix, want none", e.sink.followUps, e.fixAdds())
			}
		})
	}
	t.Run("another AF_FIX_LAND does not fail the triage", func(t *testing.T) {
		e := mirrorEnv(t, nil, map[string]string{"AF_AUTO_FIX": "true", "AF_FIX_LAND": "bogus"})
		e.readyTriage("confirmed")
		res, err := e.run(e.request(jobs.KindTriage, issueInput("42")))
		if err != nil || envelope(t, res)["status"] != "done" || e.fixAdds() != 1 {
			t.Errorf("result %s, error %v, %d additions of af:fix: want done, and af:fix added", res.Envelope, err, e.fixAdds())
		}
	})
}

// TestAFollowUpThatCannotBeRecordedOrALabelThatCannotBeWrittenWarnsAutoFixFailed_TS_19_41 verifies TS-19-41.
// Verifies: 19-REQ-7.6
func TestAFollowUpThatCannotBeRecordedOrALabelThatCannotBeWrittenWarnsAutoFixFailed_TS_19_41(t *testing.T) {
	t.Run("the follow-up cannot be recorded", func(t *testing.T) {
		e := mirrorEnv(t, nil, map[string]string{"AF_AUTO_FIX": "true"})
		e.sink.followUp = func(int64, string) (string, string, error) { return "", "", errors.New("the state file is gone") }
		e.readyTriage("confirmed")
		res, err := e.run(e.request(jobs.KindTriage, issueInput("42")))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := warningsOf(envelope(t, res))["auto_fix_failed"]; !ok || e.fixAdds() != 0 {
			t.Errorf("the warnings are %v and af:fix was added %d times, want auto_fix_failed and no label", warningsOf(envelope(t, res)), e.fixAdds())
		}
	})
	t.Run("the label cannot be written", func(t *testing.T) {
		e := mirrorEnv(t, nil, map[string]string{"AF_AUTO_FIX": "true"})
		e.readyTriage("confirmed")
		// The in-progress mark and the status label go through; the addition of af:fix fails.
		e.hub.Inject("POST", "/api/v1/workspaces/acme/issues/42/labels", fakehub.Fault{Status: 503, Skip: 2})
		res, err := e.run(e.request(jobs.KindTriage, issueInput("42")))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := warningsOf(envelope(t, res))["auto_fix_failed"]; !ok {
			t.Errorf("the warnings are %v, want auto_fix_failed", warningsOf(envelope(t, res)))
		}
		if e.sink.withdrawn != 1 {
			t.Errorf("%d follow-ups withdrawn, want 1", e.sink.withdrawn)
		}
		failed := false
		effects, _ := envelope(t, res)["side_effects"].([]any)
		for _, s := range effects {
			m, _ := s.(map[string]any)
			failed = failed || m["kind"] == "add_labels" && m["state"] == string(toolio.StateFailed) && strings.Contains(m["detail"].(string), "503")
		}
		if !failed {
			t.Errorf("the side effects are %v, want a failed add_labels", effects)
		}
	})
}

// TestNoFollowUpIsAskedForWithoutAutoFixOrForATriageThatIsNotReady_TS_19_42 verifies TS-19-42.
// Verifies: 19-REQ-7.7
func TestNoFollowUpIsAskedForWithoutAutoFixOrForATriageThatIsNotReady_TS_19_42(t *testing.T) {
	cases := map[string]struct {
		vars map[string]string
		end  func(e *env)
	}{
		"AF_AUTO_FIX unset":         {nil, func(e *env) { e.readyTriage("confirmed") }},
		"AF_AUTO_FIX false":         {map[string]string{"AF_AUTO_FIX": "false"}, func(e *env) { e.readyTriage("confirmed") }},
		"a suspected triage":        {map[string]string{"AF_AUTO_FIX": "true"}, func(e *env) { e.readyTriage("suspected") }},
		"a not reproducible triage": {map[string]string{"AF_AUTO_FIX": "true"}, func(e *env) { e.readyTriage("not_reproducible") }},
		"a failed triage": {map[string]string{"AF_AUTO_FIX": "true"}, func(e *env) {
			e.endsWith(jobs.KindTriage, toolio.StatusFailed, fakerunner.WithError("brain", "gave up", false))
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := mirrorEnv(t, nil, tc.vars)
			tc.end(e)
			if _, err := e.run(e.request(jobs.KindTriage, issueInput("42"))); err != nil {
				t.Fatal(err)
			}
			if len(e.sink.followUps) != 0 || e.fixAdds() != 0 {
				t.Errorf("follow-ups %v and %d additions of af:fix, want none", e.sink.followUps, e.fixAdds())
			}
		})
	}
}

// TestAResumedMirrorAsksAgainAndAddsTheLabelOnlyWhilePending_TS_19_43 verifies TS-19-43: a delivery that finishes the mirror
// asks for the follow-up again, gets its state as recorded, and adds af:fix only while it is pending.
// Verifies: 19-REQ-7.8
func TestAResumedMirrorAsksAgainAndAddsTheLabelOnlyWhilePending_TS_19_43(t *testing.T) {
	for _, state := range []string{jobs.FollowUpPending, jobs.FollowUpMade, jobs.FollowUpAbsorbed, jobs.FollowUpWithdrawn} {
		t.Run(state, func(t *testing.T) {
			e := mirrorEnv(t, nil, map[string]string{"AF_AUTO_FIX": "true"})
			e.readyTriage("confirmed")
			// The first delivery stops once the labels of the mirror are written, the follow-up recorded and af:fix added.
			e.runner.abortAfter = "mirror_labels"
			_, err := e.run(e.request(jobs.KindTriage, issueInput("42")))
			e.runner.abortAfter = ""
			var stopped stageAbort
			if !errors.As(err, &stopped) || len(e.sink.followUps) != 1 || e.fixAdds() != 1 {
				t.Fatalf("the first delivery returned %v with the follow-ups %v and %d additions of af:fix, want it stopped after the follow-up and af:fix", err, e.sink.followUps, e.fixAdds())
			}
			e.hub.RemoveLabel(slug, 42, "af:fix") // the trigger took it
			adds := e.fixAdds()
			e.sink.followUp = func(int64, string) (string, string, error) { return state, "job_fix", nil }
			if _, err := e.run(e.request(jobs.KindTriage, issueInput("42"), func(r *jobs.Request) { r.Delivery = 2 })); err != nil {
				t.Fatal(err)
			}
			if len(e.sink.followUps) != 2 {
				t.Errorf("the follow-up was asked for %d times, want again by the resumed delivery", len(e.sink.followUps))
			}
			if added := e.fixAdds() - adds; added != map[bool]int{true: 1, false: 0}[state == jobs.FollowUpPending] {
				t.Errorf("the resumed delivery added af:fix %d times with the follow-up %s", added, state)
			}
		})
	}
}

// TestNoFollowUpIsAskedForWhenTheStatusLabelCannotBeSet verifies that the follow-up comes after the status label: a triage
// whose ready label hub refuses asks for no fix.
// Verifies: 19-REQ-7.3
func TestNoFollowUpIsAskedForWhenTheStatusLabelCannotBeSet(t *testing.T) {
	e := mirrorEnv(t, nil, map[string]string{"AF_AUTO_FIX": "true"})
	e.readyTriage("confirmed")
	e.hub.Inject("POST", "/api/v1/workspaces/acme/issues/42/labels", fakehub.Fault{Status: 503, Skip: 1}) // the mark goes through
	if _, err := e.run(e.request(jobs.KindTriage, issueInput("42"))); err != nil {
		t.Fatal(err)
	}
	if len(e.sink.followUps) != 0 {
		t.Errorf("the follow-ups asked for are %v, want none without the status label", e.sink.followUps)
	}
}

// TestAFailedLabelWriteWithdrawsTheFollowUpInTheJobsLayer verifies the second half of TS-19-41 through the real jobs layer:
// the follow-up of the triage reads withdrawn.
// Verifies: 19-REQ-7.6
func TestAFailedLabelWriteWithdrawsTheFollowUpInTheJobsLayer(t *testing.T) {
	e := mirrorEnv(t, []string{"af:triage"}, map[string]string{"AF_AUTO_FIX": "true"})
	e.readyTriage("confirmed")
	svc, _ := e.withJobs(e.runner)
	e.hub.Inject("POST", "/api/v1/workspaces/acme/issues/42/labels", fakehub.Fault{Status: 503, Skip: 2})
	doc := atRest(t, svc, labelJob(t, svc, jobs.KindTriage))
	if f := doc.FollowUp; f == nil || f.State != jobs.FollowUpWithdrawn {
		t.Errorf("the follow-up is %+v, want withdrawn", doc.FollowUp)
	}
}
