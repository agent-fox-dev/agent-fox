package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage"
)

// The smoke tests follow the six paths of the spec through the real code: the worker, the shell of
// the af commands and their loops in the process, the fake hub with its git server and its
// gateway over the local provider, the forge client through hub, the git binary, and the test
// binary as the af and the claude of the box. Nothing in the pipeline is a stand-in. Only the model
// is not there: the brain is scripted.

// The third fixture: a triage whose submission asks a question.
const questionDiagnosis = `{"title":"Which session?","severity":"high","confidence":"suspected","root_cause":"Run calls Help before it checks its arguments.","affected_files":[{"path":"main.go","line":6,"note":"Run"}],"suggested_fix":"Check the arguments first.","acceptance_criteria":["Run with no arguments prints the usage"],"question":{"text":"Which session?","options":["old","new"]}}`

// TestAFixAttemptOnIssue42LandsAPullRequestEndToEnd_TS_13_82 verifies TS-13-82 and 13-PATH-1:
// a session opens and reaches the recorder, the issue is marked, the clone comes from the git
// server, the sandbox is reported, the branch is pushed and published and the pull request opened,
// one fix_summary comment is posted and the issue is landed, the lease is released and then the
// usage, the events and the completion are posted, and the envelope is done with its artifacts and
// the labels.
// Verifies: 13-PATH-1, 13-REQ-8.1, 13-REQ-9.3
func TestAFixAttemptOnIssue42LandsAPullRequestEndToEnd_TS_13_82(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	req := e.request(jobs.KindFix, issueInput("42"), func(r *jobs.Request) { r.Round = 1 })

	res, err := e.run(req)

	if err != nil {
		t.Fatal(err)
	}
	env := envelope(t, res)
	if env["status"] != "done" || env["tool"] != "fix" || res.MaxAttempts != 2 {
		t.Fatalf("status %v, tool %v, max attempts %d, error %v, warnings %v", env["status"], env["tool"], res.MaxAttempts, env["error"], codes(env))
	}

	// The session and the sandbox reached the recorder.
	sessions, leases := e.hub.Sessions(), e.hub.Leases()
	if len(sessions) != 1 || len(leases) != 1 {
		t.Fatalf("%d sessions and %d leases", len(sessions), len(leases))
	}
	session, lease := sessions[0], leases[0]
	if len(e.sink.hubs) < 2 || e.sink.hubs[0] != [3]string{session.ID, session.RunID, ""} || e.sink.hubs[len(e.sink.hubs)-1] != [3]string{session.ID, session.RunID, lease.ID} {
		t.Errorf("the recorder was told %v", e.sink.hubs)
	}

	// The requests, in the order of the pipeline.
	var cloneSeq, pushSeq int
	for _, g := range e.hub.GitRequests() {
		if cloneSeq == 0 || g.Seq < cloneSeq {
			cloneSeq = g.Seq
		}
		if g.Method == "POST" && strings.HasSuffix(g.Path, "/git-receive-pack") {
			pushSeq = g.Seq
		}
		if g.Status >= 400 {
			t.Errorf("the git server answered %d to %s %s", g.Status, g.Method, g.Path)
		}
	}
	steps := []struct {
		what string
		seq  int
	}{
		{"open the session", session.Seq},
		{"mark the issue", seqOf(e, "POST", issuePath+"/labels", "")},
		{"clone", cloneSeq},
		{"lease", lease.Seq},
		{"push the branch to the git server", pushSeq},
		{"publish the branch", seqOf(e, "POST", "/api/v1/workspaces/acme/branches/", "/push")},
		{"open the pull request", seqOf(e, "POST", pullsPath, "")},
		{"comment", seqOf(e, "POST", issuePath+"/comments", "")},
		{"release the lease", lease.ReleasedSeq},
		{"post the usage", seqOf(e, "POST", "/api/v1/sessions/"+session.ID+"/usage", "")},
		{"post the events", seqOf(e, "POST", "/api/v1/workspaces/acme/runs/", "/events/batch")},
		{"complete the session", session.CompletedSeq},
	}
	for i, s := range steps {
		if s.seq == 0 {
			t.Errorf("%s: no such request", s.what)
		} else if i > 0 && steps[i-1].seq >= s.seq && steps[i-1].what != "post the usage" {
			t.Errorf("%s (%d) did not come after %s (%d)", s.what, s.seq, steps[i-1].what, steps[i-1].seq)
		}
	}
	if pulls := e.hub.Pulls(slug); len(pulls) != 1 || !strings.HasPrefix(pulls[0].Head, "fix/42-") {
		t.Errorf("pull requests = %+v", pulls)
	}

	// The issue.
	issue := e.issueNow()
	if !slices.Equal(issue.Labels, []string{"af:landed"}) || len(issue.Comments) != 1 || !strings.Contains(issue.Comments[0].Body, "kind=fix_summary") {
		t.Errorf("issue 42 has the labels %v and %d comments, want landed and one fix_summary", issue.Labels, len(issue.Comments))
	}
	if !slices.Equal(res.LabelsSet, []string{"af:landed"}) || !slices.Equal(res.LabelsRemoved, []string{"af:in-progress"}) {
		t.Errorf("the result sets %v and removes %v", res.LabelsSet, res.LabelsRemoved)
	}

	// The envelope and the report to hub.
	var kinds []string
	for _, a := range env["artifacts"].([]any) {
		m := a.(map[string]any)
		kinds = append(kinds, m["kind"].(string)+"="+asString(m["name"]))
	}
	for _, want := range []string{"hub_session=" + session.ID, "sandbox=" + lease.ID} {
		if !slices.Contains(kinds, want) {
			t.Errorf("artifacts = %v, want %s", kinds, want)
		}
	}
	if !slices.ContainsFunc(kinds, func(k string) bool { return strings.HasPrefix(k, "pull_request=") }) {
		t.Errorf("the artifacts name no pull request: %v", kinds)
	}
	if pr, _ := resultMap(t, res)["pull_request"].(map[string]any); pr == nil || pr["url"] == "" || pr["draft"] != false {
		t.Errorf("the result names no pull request that is not a draft: %v", resultMap(t, res)["pull_request"])
	}
	if session.Status != "completed" || len(session.Usage) != 1 || session.Usage[0].Model != session.Model {
		t.Errorf("session status %q, usage %+v", session.Status, session.Usage)
	}
	if res.Report == "" {
		t.Error("the result holds no report")
	}
	// The events of the loop reached the recorder, among the steps of the worker.
	var loop, stepEvents int
	for _, ev := range e.sink.emitted() {
		if ev.Type == "step" {
			stepEvents++
		} else if ev.Type == "run_start" || ev.Type == "run_end" {
			loop++
		}
	}
	if loop != 2 || stepEvents < 8 {
		t.Errorf("the recorder got %d events of the loop's run and %d steps, want run_start and run_end and at least 8 steps", loop, stepEvents)
	}
}

// TestATriageAttemptReplacesTheBodyAndSetsReadyEndToEnd_TS_13_83 verifies TS-13-83 and
// 13-PATH-2.
// Verifies: 13-PATH-2, 13-REQ-9.3
func TestATriageAttemptReplacesTheBodyAndSetsReadyEndToEnd_TS_13_83(t *testing.T) {
	e := loopEnv(t, triageFiles, nil, nil, submitStep(probableDiagnosis))
	original := "The app crashes on submit.\n\nSteps: press the button."
	e.hub.AddIssue(slug, fakehub.Issue{Number: 44, Title: "App crashes", Body: original})

	res, err := e.run(e.request(jobs.KindTriage, issueInput("44")))

	if err != nil {
		t.Fatal(err)
	}
	env := envelope(t, res)
	if env["status"] != "done" || env["tool"] != "triage" {
		t.Fatalf("status %v, tool %v, error %v", env["status"], env["tool"], env["error"])
	}
	issue, _ := e.hub.IssueOf(slug, 44)
	if issue.Body == original || !strings.Contains(issue.Body, "## Original report") {
		t.Fatalf("the body was not replaced:\n%s", issue.Body)
	}
	if kept, _, _ := triage.Extract(issue.Body); kept != original {
		t.Errorf("the original report is %q, want it byte for byte %q", kept, original)
	}
	if !slices.Equal(issue.Labels, []string{"af:ready"}) || len(issue.Comments) != 0 {
		t.Errorf("issue 44 has the labels %v and %d comments, want ready and none", issue.Labels, len(issue.Comments))
	}
	if !slices.Equal(res.LabelsSet, []string{"af:ready"}) || !slices.Equal(res.LabelsRemoved, []string{"af:in-progress"}) {
		t.Errorf("the result sets %v and removes %v", res.LabelsSet, res.LabelsRemoved)
	}
	leases, sessions := e.hub.Leases(), e.hub.Sessions()
	if len(leases) != 1 || !leases[0].Released || len(sessions) != 1 || sessions[0].Status != "completed" {
		t.Errorf("leases %+v, sessions %+v, want the lease released and the session completed", leases, sessions)
	}
}

// TestATriageWhoseSubmissionAsksAQuestionBlocksOnAPerson_TS_13_84 verifies TS-13-84 and
// 13-PATH-3.
// Verifies: 13-PATH-3, 13-REQ-9.5
func TestATriageWhoseSubmissionAsksAQuestionBlocksOnAPerson_TS_13_84(t *testing.T) {
	e := loopEnv(t, triageFiles, nil, nil, submitStep(questionDiagnosis))
	req := e.request(jobs.KindTriage, issueInput("42"))

	res, err := e.run(req)

	if err != nil {
		t.Fatal(err)
	}
	env := envelope(t, res)
	if env["status"] != "needs_human" {
		t.Fatalf("status %v, error %v, warnings %v", env["status"], env["error"], codes(env))
	}
	issue := e.issueNow()
	if !slices.Equal(issue.Labels, []string{"af:needs-human"}) || len(issue.Comments) != 1 {
		t.Fatalf("issue 42 has the labels %v and %d comments, want needs-human and one comment", issue.Labels, len(issue.Comments))
	}
	body := issue.Comments[0].Body
	for _, want := range []string{"Which session?", "A. old", "B. new", "af jobs answer " + req.JobID, "POST /api/v1/jobs/" + req.JobID + "/answer", "kind=question"} {
		if !strings.Contains(body, want) {
			t.Errorf("the comment lacks %q:\n%s", want, body)
		}
	}
	if sessions := e.hub.Sessions(); len(sessions) != 1 || sessions[0].Status != "completed" {
		t.Errorf("sessions = %+v: a question is a completed attempt", sessions)
	}
}

// TestAFixOnACarryPatchWorkspaceIsRefusedAtResolve_TS_13_85 verifies TS-13-85 and 13-PATH-4.
// Verifies: 13-PATH-4, 13-REQ-4.6
func TestAFixOnACarryPatchWorkspaceIsRefusedAtResolve_TS_13_85(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "the-brain-ran")
	e := loopEnv(t, blockFiles(marker), nil, func(w *fakehub.Workspace) { w.Mode = "carry_patch" }, blockStep())

	res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

	if err != nil {
		t.Fatal(err)
	}
	category, message, retryable, _ := errorOf(envelope(t, res))
	if envelope(t, res)["status"] != "failed" || category != "unsupported" || retryable || !strings.Contains(message, "later release") {
		t.Errorf("status %v, error %q %q retryable %v, want failed, unsupported, not retryable", envelope(t, res)["status"], category, message, retryable)
	}
	for _, r := range e.hub.Requests() {
		if r.Method != "GET" {
			t.Errorf("%s %s: a write of an attempt that was refused at resolve", r.Method, r.Path)
		}
	}
	if len(e.hub.GitRequests()) != 0 || len(e.hub.Leases()) != 0 || len(e.hub.Sessions()) != 0 || len(e.hub.Gateway().Created()) != 0 {
		t.Errorf("git requests %d, leases %d, sessions %d, boxes %d: want none", len(e.hub.GitRequests()), len(e.hub.Leases()), len(e.hub.Sessions()), len(e.hub.Gateway().Created()))
	}
	if issue := e.issueNow(); len(issue.Labels) != 0 || len(issue.Comments) != 0 {
		t.Errorf("issue 42 has %v and %d comments", issue.Labels, len(issue.Comments))
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the brain ran")
	}
}

// TestACancelledFixAttemptRestoresTheRequestLabelEndToEnd_TS_13_86 verifies TS-13-86 and
// 13-PATH-5.
// Verifies: 13-PATH-5, 13-REQ-9.7
func TestACancelledFixAttemptRestoresTheRequestLabelEndToEnd_TS_13_86(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "begun")
	e := loopEnv(t, blockFiles(marker), nil, nil, blockStep())
	e.hub.AddIssue(slug, fakehub.Issue{Number: 43, Title: "Greeting is wrong", Body: "x", Labels: []string{"af:ready"}})
	req := e.request(jobs.KindFix, issueInput("43"), func(r *jobs.Request) { r.RequestLabel = "af:fix" })
	begun := func() bool { _, err := os.Stat(marker); return err == nil }

	res, err, took, _ := runUntil(t, e, req, begun, jobs.ErrCancelled)

	if err != nil {
		t.Fatalf("error = %v, want a result", err)
	}
	if took > 60*time.Second {
		t.Errorf("the attempt returned %v after the cancel", took)
	}
	issue, _ := e.hub.IssueOf(slug, 43)
	slices.Sort(issue.Labels)
	if !slices.Equal(issue.Labels, []string{"af:fix", "af:ready"}) || len(issue.Comments) != 0 {
		t.Errorf("issue 43 has the labels %v and %d comments, want ready kept, the request label back and no comment", issue.Labels, len(issue.Comments))
	}
	if !slices.Equal(res.LabelsSet, []string{"af:fix"}) || !slices.Equal(res.LabelsRemoved, []string{"af:in-progress"}) {
		t.Errorf("the result sets %v and removes %v", res.LabelsSet, res.LabelsRemoved)
	}
	leases, sessions := e.hub.Leases(), e.hub.Sessions()
	if len(leases) != 1 || !leases[0].Released || len(sessions) != 1 || sessions[0].Status != "failed" {
		t.Errorf("leases %+v, sessions %+v, want the lease released and hub told that the session failed", leases, sessions)
	}
	if _, batches := e.postedEvents(t); len(batches) == 0 {
		t.Error("no events were posted to hub")
	}
	if category, _, _, _ := errorOf(envelope(t, res)); category != "cancelled" {
		t.Errorf("the envelope of the cancelled loop has the category %q", category)
	}
}

// TestALeaseRefusedWith503ComesBackAsATransientErrorAndIsRetried_TS_13_87 verifies TS-13-87 and
// 13-PATH-6.
// Verifies: 13-PATH-6, 13-REQ-8.2
func TestALeaseRefusedWith503ComesBackAsATransientErrorAndIsRetried_TS_13_87(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.hub.Inject("POST", sandboxesPath, fakehub.Fault{Status: 503, Once: true})
	req := e.request(jobs.KindFix, issueInput("42"))

	res, err := e.run(req)

	f := transient(err)
	if f == nil || f.Category != "sandbox" || !f.Retryable || len(res.Envelope) != 0 {
		t.Fatalf("result %q, error %v, want a transient sandbox error that is retryable and no envelope", res.Envelope, err)
	}
	if issue := e.issueNow(); len(issue.Labels) != 0 || len(issue.Comments) != 0 {
		t.Errorf("issue 42 has the labels %v and %d comments after a transient error", issue.Labels, len(issue.Comments))
	}
	// The delivery that is handed over again reports nothing and leaves the session open (14-REQ-1.6).
	session := e.hub.Sessions()[0]
	if session.Status != "open" || len(session.Usage) != 0 {
		t.Errorf("the session is %q with %d usage records, want it open with none", session.Status, len(session.Usage))
	}
	if _, batches := e.postedEvents(t); len(batches) != 0 {
		t.Errorf("%d batches of events were posted, want none", len(batches))
	}
	if _, err := os.Stat(filepath.Join(e.dir, "clone")); err != nil {
		t.Errorf("clone/ is not kept: %v", err)
	}

	// The jobs layer hands the same attempt over again, and this time it goes through.
	again := req
	again.Delivery = 2
	res, err = e.runner.Run(context.Background(), again)
	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("the second delivery: %s, %v, want a done envelope", res.Envelope, err)
	}
	// The second delivery reports to the session of the first, which was left open (14-REQ-2.5).
	if sessions := e.hub.Sessions(); len(sessions) != 1 || sessions[0].Status != "completed" || len(sessions[0].Usage) != 1 {
		t.Errorf("sessions = %+v, want the one session of the attempt, completed, with its one usage record", sessions)
	}
	if issue := e.issueNow(); !slices.Equal(issue.Labels, []string{"af:landed"}) {
		t.Errorf("after the second delivery issue 42 has the labels %v, want landed", issue.Labels)
	}
}

// The smoke tests of the recovery of attempts: each is a path of the spec through the same real parts. A process that
// dies is a delivery that the stage-abort seam stops, and the next delivery is the runner called again with the
// handover count, on the same attempt directory.

// TestACrashRightAfterTheApplyIsResumedReMeasuredAndLandedWithNoSuffix_TS_14_62 verifies TS-14-62 and 14-PATH-1.
// Verifies: 14-PATH-1, 14-REQ-3.2, 14-REQ-4.7, 14-REQ-2.6
func TestACrashRightAfterTheApplyIsResumedReMeasuredAndLandedWithNoSuffix_TS_14_62(t *testing.T) {
	e := loopEnv(t, fixFiles, map[string]string{"AF_MAX_BUDGET_USD": "1.00"}, nil, fixSteps()...)
	e.usePricing(scriptFile(t, fixSteps()...), map[int]pricedRun{1: {cost: 0.25, turns: 5, costSource: "reported"}})
	e.stopAfter("applied")
	spy := e.spyOnGit()
	leases, runs := len(e.hub.Leases()), e.brainRuns()

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if n := clonesOf(spy); n != 0 || e.brainRuns()-runs != 0 {
		t.Errorf("%d clones and %d runs of the brain, want none of either", n, e.brainRuns()-runs)
	}
	held := e.hub.Leases()
	if len(held) != leases+1 || !held[0].Released || !held[1].Released {
		t.Errorf("leases %+v, want the lease of delivery 1 given back and exactly one new one", held)
	}
	pulls := e.hub.Pulls(slug)
	if len(pulls) != 1 || pulls[0].Head != "fix/42-greeting-wrong" || len(e.issueNow().Comments) != 1 || !slices.Contains(e.issueNow().Labels, "af:landed") {
		t.Errorf("pull requests %+v, comments %d, labels %v, want one pull request from fix/42-greeting-wrong without a suffix, one comment, landed", pulls, len(e.issueNow().Comments), e.issueNow().Labels)
	}
	env := envelope(t, res)
	usage, _ := env["usage"].(map[string]any)
	if warningOf(env, WarnAttemptResumed) == nil || !near(usage["cost_usd"].(float64), 0.25) || usage["turns"] != float64(5) {
		t.Errorf("warning %v, usage %v, want attempt_resumed and the spend of delivery 1", warningOf(env, WarnAttemptResumed), usage)
	}
	s := e.hub.Sessions()[0]
	if o := storedOutcome(t, e); !o.Mirrored || s.Status != "completed" || len(s.Usage) != 1 || !near(s.Usage[0].CostUSD, 0.25) {
		t.Errorf("outcome mirrored %v, session %q with usage %+v, want the outcome stored, the session completed and one usage record of the attempt's total", o.Mirrored, s.Status, s.Usage)
	}
}

// TestAGracefulShutdownDuringTheLandIsResumedAfterTheRealJobsLayerIsRestartedOnTheSameDatabase_TS_14_63 verifies
// TS-14-63 and 14-PATH-2: the real jobs layer on a database file hands the worker an attempt, is stopped while the
// change is measured, and a jobs layer started on the same file hands the attempt over again.
// Verifies: 14-PATH-2
func TestAGracefulShutdownDuringTheLandIsResumedAfterTheRealJobsLayerIsRestartedOnTheSameDatabase_TS_14_63(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "measuring")
	steps := []map[string]any{
		writeStep("src/greet.sh", "echo hello\n"),
		// The first time the test runs it waits, so that the layer can be stopped while the change is measured.
		writeStep("tests/hello_test.sh", "if [ ! -e '"+marker+"' ]; then touch '"+marker+"'; sleep 120; fi\n"+helloTest),
		{"tool": "execute", "args": map[string]any{"command": "make box-commit"}, "expect": "ok"},
		submitStep(fixReport),
	}
	e := loopEnv(t, fixFiles, nil, nil, steps...)
	key, err := secrets.NewKey(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(t.TempDir(), "agent-fox.db")
	attempts := filepath.Join(t.TempDir(), "attempts")
	start := func() *jobs.Service {
		t.Helper()
		st, err := store.Open(context.Background(), database, key)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := st.Upsert(context.Background(), slug, "af:", pat, "u1", "u1", time.Now()); err != nil {
			t.Fatal(err)
		}
		svc, err := jobs.New(jobs.Config{
			DB: st.DB(), Subscriptions: st, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Workers: 1, AttemptsDir: attempts,
			Runner: e.runner, PollInterval: 10 * time.Millisecond, RetryDelay: 10 * time.Millisecond, Retain: 100 * 365 * 24 * time.Hour,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = svc.Close(); _ = st.Close() })
		return svc
	}

	first := start()
	doc, err := first.Create(context.Background(), jobs.CreateRequest{
		Workspace: slug, Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage("42")}, Source: jobs.Source{Kind: "api", By: "u1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the change was never measured")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := first.Close(); err != nil { // the shutdown: the attempt is told to stop, and has its grace
		t.Fatal(err)
	}

	session := e.hub.Sessions()[0]
	if session.Status != "open" || len(session.Usage) != 0 || len(e.hub.EventBatches(slug, session.RunID)) != 0 {
		t.Fatalf("the stopped delivery left the session %+v with %d batches of events, want it open, with no usage and no events", session, len(e.hub.EventBatches(slug, session.RunID)))
	}
	attemptDir := filepath.Join(attempts, doc.ID, "1")
	if branches := localBranchesIn(t, attemptDir); len(branches) != 1 {
		t.Fatalf("the clone has the branches %v after the shutdown, want the one the change was parked on", branches)
	}
	var branches []string
	e.onStep("release", func() { branches = localBranchesIn(t, attemptDir) })

	second := start()
	var final jobs.Doc
	for deadline := time.Now().Add(90 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if final, err = second.Get(context.Background(), doc.ID); err != nil {
			t.Fatal(err)
		}
		if final.State == jobs.StateDone || final.State == jobs.StateFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the job is %s after 90 seconds", final.State)
		}
	}

	if final.State != jobs.StateDone || final.CurrentAttempt != 1 || final.Attempts[0].Deliveries != 2 {
		t.Fatalf("the job is %s, attempt %d with %d deliveries, want done on its first attempt in the second delivery", final.State, final.CurrentAttempt, final.Attempts[0].Deliveries)
	}
	if pulls := e.hub.Pulls(slug); len(pulls) != 1 || pulls[0].Head != "fix/42-greeting-wrong" {
		t.Errorf("pull requests %+v, want one from the branch the change was parked on, with its name", pulls)
	}
	if !slices.Equal(branches, []string{"fix/42-greeting-wrong"}) {
		t.Errorf("the clone had the branches %v at the end, want the one of the landing: the parked branch was brought back and deleted", branches)
	}
	if sessions := e.hub.Sessions(); len(sessions) != 1 || sessions[0].Status != "completed" || len(sessions[0].Usage) != 1 || sessions[0].Usage[0].Turns != final.Usage.Turns {
		t.Errorf("sessions %+v, want one session, completed, with one usage record holding the attempt's total (%d turns)", sessions, final.Usage.Turns)
	}
}

// localBranchesIn are the branches of the clone of the attempt directory that start with fix/.
func localBranchesIn(t *testing.T, attemptDir string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", filepath.Join(attemptDir, "clone"), "branch", "--list", "fix/*", "--format=%(refname:short)").Output()
	if err != nil {
		t.Fatalf("git branch: %v", err)
	}
	return strings.Fields(string(out))
}

// TestACrashAfterThePullRequestWasOpenedFinishesTheLandingWithoutASecondPullRequest_TS_14_64 verifies TS-14-64 and
// 14-PATH-3.
// Verifies: 14-PATH-3, 14-REQ-5.2, 14-REQ-5.3
func TestACrashAfterThePullRequestWasOpenedFinishesTheLandingWithoutASecondPullRequest_TS_14_64(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.stopAfter("pull_request")
	plan := journalOf(t, e).Landing.Plan
	if plan == nil {
		t.Fatal("the journal has no plan")
	}
	first := e.hub.Pulls(slug)
	if len(first) != 1 {
		t.Fatalf("%d pull requests after delivery 1, want the one it opened", len(first))
	}
	spy := e.spyOnGit()
	leases, runs := len(e.hub.Leases()), e.brainRuns()

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	asked := false
	for _, c := range spy.with("ls-remote") {
		asked = asked || strings.Contains(strings.Join(c.argv, " "), plan.Branch)
	}
	if !asked {
		t.Errorf("the delivery did not read the branch %s from the remote: %v", plan.Branch, spy.with("ls-remote"))
	}
	if len(e.hub.Leases()) != leases || e.brainRuns() != runs || len(e.hub.Pulls(slug)) != 1 {
		t.Errorf("%d new leases, %d new runs of the brain, %d pull requests, want none, none and the one", len(e.hub.Leases())-leases, e.brainRuns()-runs, len(e.hub.Pulls(slug)))
	}
	if issue := e.issueNow(); len(issue.Comments) != 1 || !slices.Contains(issue.Labels, "af:landed") || slices.Contains(issue.Labels, "af:in-progress") {
		t.Errorf("issue 42 has the labels %v and %d comments, want landed, in-progress off, and one comment", issue.Labels, len(issue.Comments))
	}
	pr, _ := resultMap(t, res)["pull_request"].(map[string]any)
	if pr == nil || pr["number"] != float64(first[0].Number) || warningOf(envelope(t, res), WarnAttemptResumed) == nil {
		t.Errorf("the envelope reports the pull request %v and the warning %v, want #%d of delivery 1 and attempt_resumed", pr, warningOf(envelope(t, res), WarnAttemptResumed), first[0].Number)
	}
}

// TestACrashAfterTheOutcomeWasStoredReplaysOnlyTheBookkeeping_TS_14_65 verifies TS-14-65 and 14-PATH-4.
// Verifies: 14-PATH-4, 14-REQ-6.2, 14-REQ-6.4
func TestACrashAfterTheOutcomeWasStoredReplaysOnlyTheBookkeeping_TS_14_65(t *testing.T) {
	cases := map[string]struct {
		stage  string
		steps  []map[string]any
		mutate func(*jobs.Request)
	}{
		"after the mirror, before the report":    {stage: "mirrored", steps: fixSteps()},
		"a failed attempt, before the mirror":    {stage: "outcome_stored", mutate: func(r *jobs.Request) { r.Round = 2 }},
		"a landed attempt, before the mirror":    {stage: "outcome_stored", steps: fixSteps()},
		"a landed attempt, after the usage only": {stage: "usage_posted", steps: fixSteps()},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := loopEnv(t, fixFiles, nil, nil, c.steps...)
			var mutate []func(*jobs.Request)
			if c.mutate != nil {
				mutate = append(mutate, c.mutate)
			}
			e.stopAfter(c.stage, mutate...)
			stored := storedOutcome(t, e)
			leases, runs, opened := len(e.hub.Leases()), e.brainRuns(), sessionOpens(e)

			res, err := e.deliver(2, mutate...)

			if err != nil {
				t.Fatal(err)
			}
			if len(e.hub.Leases()) != leases || e.brainRuns() != runs || sessionOpens(e) != opened {
				t.Errorf("%d new leases, %d new runs, %d new sessions, want none", len(e.hub.Leases())-leases, e.brainRuns()-runs, sessionOpens(e)-opened)
			}
			if markedComments(e) > 1 {
				t.Errorf("%d comments with the marker, want at most one", markedComments(e))
			}
			s := e.hub.Sessions()[0]
			if s.Status == "open" || len(s.Usage) != 1 {
				t.Errorf("the session is %q with %d usage records, want it completed, with the usage posted once", s.Status, len(s.Usage))
			}
			got, n := withoutWarning(t, res.Envelope, WarnAttemptResumed)
			want, _ := withoutWarning(t, []byte(stored.Envelope), WarnAttemptResumed)
			if n != 1 || !reflect.DeepEqual(got, want) {
				t.Errorf("the envelope is not the stored one with attempt_resumed added")
			}
		})
	}
}

// TestATriageOfTextThatCrashedAfterTheIssueWasCreatedLandsIntoTheSameIssue_TS_14_66 verifies TS-14-66 and 14-PATH-5.
// Verifies: 14-PATH-5, 14-REQ-5.8
func TestATriageOfTextThatCrashedAfterTheIssueWasCreatedLandsIntoTheSameIssue_TS_14_66(t *testing.T) {
	e := loopEnv(t, triageFiles, nil, nil, submitStep(probableDiagnosis))
	input := jobs.Input{Title: "Run starts the wrong thing", Text: "The app crashes on submit."}
	e.runner.abortAfter = "created_issue"
	if _, err := e.runner.Run(context.Background(), e.request(jobs.KindTriage, input)); err == nil {
		t.Fatal("the first delivery ran to its end")
	}
	e.runner.abortAfter = ""
	created := journalOf(t, e).Landing.CreatedIssue
	if created == nil || len(e.hub.Issues(slug)) != 2 {
		t.Fatalf("the journal holds %v and the repository %d issues after delivery 1, want the issue that hub created, and issue 42", created, len(e.hub.Issues(slug)))
	}
	leases, runs, creates, edits := len(e.hub.Leases()), e.brainRuns(), issueCreates(e), issueEdits(e, created.Number)

	res, err := e.run(e.request(jobs.KindTriage, input, func(r *jobs.Request) { r.Delivery = 2 }))

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if issueCreates(e) != creates || issueEdits(e, created.Number) <= edits {
		t.Errorf("the delivery made %d issues and %d edits of #%d, want no issue made and the body of that one replaced", issueCreates(e)-creates, issueEdits(e, created.Number)-edits, created.Number)
	}
	if len(e.hub.Leases()) != leases || e.brainRuns() != runs {
		t.Errorf("%d new leases and %d new runs of the brain, want none: the accepted diagnosis is validated against the tree of the clone", len(e.hub.Leases())-leases, e.brainRuns()-runs)
	}
	issue, _ := e.hub.IssueOf(slug, created.Number)
	if !strings.Contains(issue.Body, "Run calls Help before it checks its arguments") || len(e.hub.Issues(slug)) != 2 {
		t.Errorf("the body of issue #%d is %q and the repository has %d issues, want the diagnosis in that issue, and two issues in all", created.Number, issue.Body, len(e.hub.Issues(slug)))
	}
}

// TestABoxLostDuringTheBrainIsReplacedWithinTheDeliveryAndTheAttemptLands_TS_14_67 verifies TS-14-67 and 14-PATH-6.
// Verifies: 14-PATH-6, 14-REQ-7.2, 14-REQ-9.2
func TestABoxLostDuringTheBrainIsReplacedWithinTheDeliveryAndTheAttemptLands_TS_14_67(t *testing.T) {
	e := loopEnv(t, fixFiles, map[string]string{"AF_MAX_BUDGET_USD": "1.00", "AF_MAX_TURNS": "20"}, nil, fixSteps()...)
	e.usePricing(scriptFile(t, fixSteps()...), map[int]pricedRun{1: {cost: 0.25, turns: 5, costSource: "reported"}})
	seen := e.settingsSeen()
	e.loseBoxes(1, func(fakehub.Lease) { e.hub.SetLeaseTTL(10 * time.Minute) })
	var tree string
	brains := 0
	e.onStep("brain", func() {
		if brains++; brains == 2 {
			tree = gitOut(t, filepath.Join(e.dir, cloneDirName), "status", "--porcelain")
		}
	})

	res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	leases := e.hub.Leases()
	if len(leases) != 2 || !leases[0].Released || !leases[1].Released {
		t.Fatalf("leases %+v, want the lost one given back and a new one", leases)
	}
	if seen.Timeout > 8*time.Minute || warningOf(envelope(t, res), WarnLeaseShortenedTimeout) == nil {
		t.Errorf("the loop was given %s and the warning %v, want the deadline cut to two minutes before the replacement expires", seen.Timeout, warningOf(envelope(t, res), WarnLeaseShortenedTimeout))
	}
	if w := warningOf(envelope(t, res), WarnBoxReplaced); w == nil || e.sink.lastHub()[2] != leases[1].ID {
		t.Errorf("the warning %v and the sandbox %q the recorder holds, want box_replaced and the new sandbox %s", w, e.sink.lastHub()[2], leases[1].ID)
	}
	if !near(seen.Budget, 0.75) || seen.MaxTurns != 15 || brains != 2 || tree != "" {
		t.Errorf("the loop ran with $%.2f and %d turns, %d runs of the brain, the tree at the second %q, want $0.75, 15 turns, a second run from a clean tree", seen.Budget, seen.MaxTurns, brains, tree)
	}
	if pulls := e.hub.Pulls(slug); len(pulls) != 1 || pulls[0].Head != "fix/42-greeting-wrong" {
		t.Errorf("pull requests %+v, want the one that landed, from a branch with no suffix", pulls)
	}
}

// TestACrashBeforeTheApplyRerunsTheBrainFromTheBaseWithTheRemainingBudget_TS_14_69 verifies TS-14-69 and 14-PATH-8.
// Verifies: 14-PATH-8, 14-REQ-3.1, 14-REQ-3.3, 14-REQ-4.1
func TestACrashBeforeTheApplyRerunsTheBrainFromTheBaseWithTheRemainingBudget_TS_14_69(t *testing.T) {
	e := loopEnv(t, fixFiles, map[string]string{"AF_MAX_BUDGET_USD": "1.00", "AF_MAX_TURNS": "20"}, nil, fixSteps()...)
	e.usePricing(scriptFile(t, fixSteps()...), map[int]pricedRun{1: {cost: 0.30, turns: 5, costSource: "reported"}})
	e.stopAfter("submission_accepted")
	seen := e.settingsSeen()
	clone := filepath.Join(e.dir, cloneDirName)
	if applied := ledgerHas(t, e, fix.LedgerApply); applied {
		t.Fatal("the ledger has an apply record: the delivery was stopped after the apply")
	}
	var tree string
	e.onStep("brain", func() { tree = gitOut(t, clone, "status", "--porcelain") })

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if !near(seen.Budget, 0.70) || seen.MaxTurns != 15 || tree != "" {
		t.Errorf("the brain was given $%.2f and %d turns, from a tree %q, want 0.70 and 15 from a tree with no change in it", seen.Budget, seen.MaxTurns, tree)
	}
	if pulls, pushes := e.hub.Pulls(slug), e.hub.RequestsTo("POST", "/api/v1/workspaces/acme/branches/fix/42-greeting-wrong/push"); len(pulls) != 1 || len(pushes) != 1 {
		t.Errorf("%d pull requests and %d pushes of the branch to the forge, want one of each", len(pulls), len(pushes))
	}
	usage, _ := envelope(t, res)["usage"].(map[string]any)
	s := e.hub.Sessions()[0]
	if len(s.Usage) != 1 || !near(s.Usage[0].CostUSD, 0.60) || s.Usage[0].Turns != 10 || !near(usage["cost_usd"].(float64), 0.60) {
		t.Errorf("usage records %+v and the envelope's %v, want one that covers both deliveries: $0.30 and 5 turns, twice", s.Usage, usage)
	}
}

// ledgerHas says whether the ledger of the run in the clone has a record of the kind.
func ledgerHas(t *testing.T, e *env, kind string) bool {
	t.Helper()
	rd, err := toolio.OpenRunDir(filepath.Join(e.dir, cloneDirName), e.hub.Sessions()[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := rd.Ledger()
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(entries, func(e toolio.LedgerEntry) bool { return e.Kind == kind })
}
