package worker

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/triage"
)

// The smoke tests follow the six paths of the spec through the real code: the worker, the shell of
// the af commands and their loops in the process, the fake hub with its git server and its
// gateway over the local provider, the forge client through hub, the git binary, and the test
// binary as the af of the box. Nothing in the pipeline is a stand-in. Only the model
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
	// The jobs layer hands the same attempt over again, with the session the first delivery reported, and this time
	// it goes through.
	again := req
	again.Delivery = 2
	again.SessionID, again.RunID = session.ID, session.RunID
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
