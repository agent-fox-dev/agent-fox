package worker

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func mods(m ...func(*toolio.Envelope)) []func(*toolio.Envelope) { return m }

// A status label replaces the other four, but in-progress of a fix keeps ready.
func TestMirror_SetsTheLabelAndTheCommentOfEachOutcome(t *testing.T) {
	for name, tc := range map[string]struct {
		kind                     jobs.Kind
		vars                     map[string]string
		initial                  []string
		status                   toolio.Status
		mods                     []func(*toolio.Envelope)
		during, after            []string
		comment                  string
		says                     []string
		labelsSet, labelsRemoved []string
	}{
		"a triage that is probable": {kind: jobs.KindTriage, initial: []string{"af:ready"}, status: "done", mods: mods(withResult(triageResult("probable", "replaced_body"))),
			during: []string{"af:in-progress"}, after: []string{"af:ready"}, labelsRemoved: []string{"af:in-progress"}},
		"a triage with no diagnosis": {kind: jobs.KindTriage, status: "done", after: []string{"af:ready"}, labelsSet: []string{"af:ready"}, labelsRemoved: []string{"af:in-progress"}},
		"a triage that is suspected": {kind: jobs.KindTriage, status: "done", mods: mods(withResult(triageResult("suspected", "replaced_body"))),
			after: []string{"af:needs-human"}, comment: "reason", says: []string{"`suspected`", "in the body of this issue", "POST /api/v1/workspaces/acme/triage"}},
		"a triage that is not reproducible": {kind: jobs.KindTriage, status: "done", mods: mods(withResult(triageResult("not_reproducible", "commented"))),
			after: []string{"af:needs-human"}, comment: "reason", says: []string{"`not_reproducible`", "in a comment of the triage"}},
		"a triage that asks": {kind: jobs.KindTriage, status: "needs_human", mods: mods(fakerunner.WithQuestion("Which greeting?", "hello", "hi")),
			after: []string{"af:needs-human"}, comment: "question", says: []string{"Which greeting?\n\nA. hello\nB. hi\n", "af jobs answer " + jobID, "POST /api/v1/jobs/" + jobID + "/answer"}},
		"a fix that asks": {kind: jobs.KindFix, status: "needs_human", mods: mods(fakerunner.WithQuestion("Which greeting?")), after: []string{"af:needs-human"}, comment: "question"},
		"a fix of an issue that is ready": {kind: jobs.KindFix, initial: []string{"af:ready"}, status: "done", mods: mods(withResult(fixResult("landed", "pr", true))),
			during: []string{"af:in-progress", "af:ready"}, after: []string{"af:landed"}, comment: "fix_summary", says: []string{"landed", forgeRepo + "/pull/9", "Closes " + issueURL},
			labelsSet: []string{"af:landed"}, labelsRemoved: []string{"af:in-progress", "af:ready"}},
		"a fix that lands over every label": {kind: jobs.KindFix, initial: []string{"af:failed", "af:needs-human", "af:ready", "other"}, status: "done", mods: mods(withResult(fixResult("landed", "pr", true))),
			after: []string{"af:landed", "other"}, comment: "fix_summary", labelsSet: []string{"af:landed"}, labelsRemoved: []string{"af:failed", "af:in-progress", "af:needs-human", "af:ready"}},
		"a fix that is unverified": {kind: jobs.KindFix, status: "unverified", mods: mods(withResult(fixResult("landed", "pr", false))), after: []string{"af:landed"}, comment: "fix_summary", says: []string{"Refs " + issueURL}},
		"a fix that kept nothing": {kind: jobs.KindFix, vars: map[string]string{"AF_FIX_LAND": "none"}, initial: []string{"af:ready"}, status: "done", mods: mods(withResult(fixResult("landed", "none", true))),
			after: []string{"af:ready"}, comment: "fix_summary", says: []string{"nothing was pushed or kept"}},
		"a fix that is parked": {kind: jobs.KindFix, status: "unverified", mods: mods(withResult(fixResult("parked", "pr", false))), after: []string{"af:needs-human"}, comment: "fix_summary", says: []string{"parked"}},
		"a fix that fails an issue that failed": {kind: jobs.KindFix, initial: []string{"af:failed"}, status: "failed", mods: mods(fakerunner.WithError("git", "the branch could not be pushed", false)),
			during: []string{"af:in-progress"}, after: []string{"af:failed"}, comment: "failure", says: []string{"`git`", "the branch could not be pushed", "POST /api/v1/jobs/" + jobID + "/retry"},
			labelsRemoved: []string{"af:in-progress"}},
		"a usage failure": {kind: jobs.KindTriage, status: "usage", mods: mods(fakerunner.WithError("usage", "the input is empty", false)), after: []string{"af:failed"}, comment: "failure"},
	} {
		t.Run(name, func(t *testing.T) {
			e := varsEnv(t, tc.vars)
			e.hub.AddIssue(slug, fakehub.Issue{Number: 42, Title: "Greeting is wrong", Labels: tc.initial})
			e.endsWith(tc.status, tc.mods...)
			stand := e.runner.loop
			var during []string
			e.runner.loop = func(ctx context.Context, a *attempt) *outcome { during = e.issueNow().Labels; return stand(ctx, a) }

			res, err := e.run(e.request(tc.kind, issueInput("42")))

			if err != nil || envelope(t, res)["status"] != string(tc.status) {
				t.Fatalf("error %v, envelope %s: the mirror never changes the status", err, res.Envelope)
			}
			if issue := e.issueNow(); !slices.Equal(issue.Labels, tc.after) || (tc.during != nil && !slices.Equal(during, tc.during)) {
				t.Errorf("the issue carried %v while the loop ran and %v after, want %v and %v", during, issue.Labels, tc.during, tc.after)
			}
			if kinds := e.commentKinds(); (tc.comment == "" && len(kinds) != 0) || (tc.comment != "" && !slices.Equal(kinds, []string{tc.comment})) {
				t.Fatalf("comments %v, want one of the kind %q", kinds, tc.comment)
			}
			for _, part := range tc.says {
				if body := e.issueNow().Comments[0].Body; !strings.Contains(body, part) {
					t.Errorf("the comment lacks %q:\n%s", part, body)
				}
			}
			set, removed := slices.Sorted(slices.Values(res.LabelsSet)), slices.Sorted(slices.Values(res.LabelsRemoved))
			if (tc.labelsSet != nil || tc.labelsRemoved != nil) && (!slices.Equal(set, tc.labelsSet) || !slices.Equal(removed, tc.labelsRemoved)) {
				t.Errorf("the result sets %v and removes %v, want %v and %v", set, removed, tc.labelsSet, tc.labelsRemoved)
			}
		})
	}
}

func TestMirror_OnlyTakesInProgressOffWhenAnotherAttemptFollows(t *testing.T) {
	for name, tc := range map[string]struct {
		category  string
		retryable bool
		round     int
		max       string
		follows   bool
	}{
		"a transient error":                     {follows: true},
		"a brain failure at 1 of 2":             {"brain", true, 1, "2", true},
		"a no_result failure at 1 of 3":         {"no_result", true, 1, "3", true},
		"a brain failure at 2 of 3":             {"brain", true, 2, "3", true},
		"a brain failure at 2 of 2":             {"brain", true, 2, "2", false},
		"a brain failure that is not retryable": {"brain", false, 1, "2", false},
		"a git failure at 1 of 2":               {"git", true, 1, "2", false},
		"a brain failure with no round":         {"brain", true, 0, "", true},
		"a brain failure with no round of 1":    {"brain", true, 0, "1", false},
	} {
		t.Run(name, func(t *testing.T) {
			e := varsEnv(t, map[string]string{"AF_MAX_ATTEMPTS": tc.max})
			if tc.category == "" {
				e.runner.loop = boxLost
			} else {
				e.endsWith("failed", fakerunner.WithError(tc.category, "it failed", tc.retryable))
			}

			res, _ := e.run(e.fix(func(r *jobs.Request) { r.Round = tc.round }))

			labels, kinds := e.issueNow().Labels, e.commentKinds()
			if tc.follows && (len(labels) != 0 || len(kinds) != 0 || !slices.Equal(res.LabelsRemoved, []string{"af:in-progress"}) || len(res.LabelsSet) != 0) {
				t.Errorf("labels %v, comments %v, result %v %v: want in-progress taken off and nothing else", labels, kinds, res.LabelsSet, res.LabelsRemoved)
			}
			if !tc.follows && (!slices.Equal(labels, []string{"af:failed"}) || !slices.Equal(kinds, []string{"failure"})) {
				t.Errorf("labels %v and comments %v, want failed and its comment: this attempt ends the job", labels, kinds)
			}
		})
	}
}

func TestMirror_ATextTriageWritesOnlyToTheIssueItCreated(t *testing.T) {
	created := map[string]any{"action": "created_issue", "issue": map[string]any{"number": 7, "url": forgeRepo + "/issues/7"}}
	for name, tc := range map[string]struct {
		status toolio.Status
		result any
		ready  bool
	}{
		"done with the issue it created": {"done", created, true},
		"done without an issue":          {"done", map[string]any{"issue": "not an object"}, false},
		"needs_human, naming an issue":   {"needs_human", created, false},
	} {
		e := newEnv(t, fakehub.Workspace{})
		e.hub.AddIssue(slug, fakehub.Issue{Number: 7, Title: "The loop made this"})
		e.endsWith(tc.status, withResult(tc.result))

		res, err := e.run(e.request(jobs.KindTriage, jobs.Input{File: &jobs.File{Name: "bug.md", Content: "x"}}))

		issue7 := e.issue(slug, 7)
		if err != nil || len(e.issueNow().Labels) != 0 || len(issue7.Comments) != 0 || slices.Equal(issue7.Labels, []string{"af:ready"}) != tc.ready {
			t.Errorf("%s: error %v, issue 7 %v, issue 42 %v, want issue 7 ready: %v", name, err, issue7.Labels, e.issueNow().Labels, tc.ready)
		}
		if !tc.ready && (len(res.LabelsSet) != 0 || len(res.LabelsRemoved) != 0) {
			t.Errorf("%s: the result sets %v and removes %v", name, res.LabelsSet, res.LabelsRemoved)
		}
	}
}

func TestMirror_AFailedWriteIsAWarningAndNeverAChangeOfTheOutcome(t *testing.T) {
	for name, tc := range map[string]struct {
		initial             []string
		method, path        string
		status              int
		warning, effect     string
		after, set, removed []string
	}{
		"a label that cannot be set removes none of the others": {[]string{"af:failed", "af:ready"}, "POST", issuePath + "/labels", 500, "status_label_failed", "add_labels",
			[]string{"af:failed", "af:ready"}, nil, nil},
		"a label that cannot be removed": {[]string{"af:failed"}, "DELETE", issuePath + "/labels/*", 500, "status_label_failed", "remove_label",
			[]string{"af:failed", "af:in-progress", "af:landed"}, []string{"af:in-progress", "af:landed"}, nil},
		"a label that is gone already is removed": {[]string{"af:failed"}, "DELETE", issuePath + "/labels/*", 404, "", "",
			[]string{"af:failed", "af:in-progress", "af:landed"}, []string{"af:landed"}, []string{"af:failed", "af:in-progress"}},
		"a comment that cannot be posted": {nil, "POST", issuePath + "/comments", 500, "comment_failed", "add_comment",
			[]string{"af:landed"}, []string{"af:landed"}, []string{"af:in-progress"}},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, fakehub.Workspace{})
			e.hub.AddIssue(slug, fakehub.Issue{Number: 42, Title: "T", Labels: tc.initial})
			e.hub.Inject(tc.method, tc.path, fakehub.Fault{Status: tc.status})
			e.endsWith("done", withResult(fixResult("landed", "pr", true)))

			res, err := e.run(e.fix())

			env := envelope(t, res)
			if err != nil || env["status"] != "done" || env["exit_code"] != float64(0) {
				t.Fatalf("error %v, envelope %s: the outcome must not change", err, res.Envelope)
			}
			if got := codes(env); (tc.warning == "") != (len(got) == 0) || (tc.warning != "" && !slices.Contains(got, tc.warning)) {
				t.Errorf("warnings %v, want %q", got, tc.warning)
			}
			if got := failedWrites(env); (tc.effect == "") != (len(got) == 0) || (tc.effect != "" && !slices.Contains(got, tc.effect)) {
				t.Errorf("failed writes %v, want %q", got, tc.effect)
			}
			set, removed := slices.Sorted(slices.Values(res.LabelsSet)), slices.Sorted(slices.Values(res.LabelsRemoved))
			if labels := e.issueNow().Labels; !slices.Equal(labels, tc.after) || !slices.Equal(set, tc.set) || !slices.Equal(removed, tc.removed) {
				t.Errorf("labels %v, the result sets %v and removes %v, want %v, %v and %v", labels, set, removed, tc.after, tc.set, tc.removed)
			}
		})
	}

	t.Run("under a transient error the failure is logged", func(t *testing.T) {
		e := newEnv(t, fakehub.Workspace{})
		e.hub.Inject("DELETE", issuePath+"/labels/*", fakehub.Fault{Status: 500})
		e.runner.loop = boxLost

		res, err := e.run(e.fix())

		if !jobs.IsTransient(err) || len(res.Envelope) != 0 {
			t.Fatalf("result %s, error %v, want the transient error as it was", res.Envelope, err)
		}
		if logged := e.log.Find("WARN", "a write of the attempt failed"); len(logged) != 1 || logged[0]["code"] != "status_label_failed" || strings.Contains(e.log.String(), pat) {
			t.Errorf("logged %v, want the failed label write, and not the token", logged)
		}
	})
}

// Ready is left in place, nothing is said on the issue, and the envelope says why the attempt ended.
func TestMirror_ACancelPutsTheRequestLabelBack(t *testing.T) {
	for name, tc := range map[string]struct {
		cause  error
		reason string
		fails  bool
	}{
		"a cancel":                       {jobs.ErrCancelled, "was cancelled", false},
		"an unsubscribe":                 {jobs.ErrUnsubscribed, "was unsubscribed", false},
		"a request label that cannot be": {jobs.ErrCancelled, "was cancelled", true},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, fakehub.Workspace{})
			e.hub.AddIssue(slug, fakehub.Issue{Number: 42, Title: "T", Labels: []string{"af:ready"}})
			if tc.fails {
				e.hub.Inject("POST", issuePath+"/labels", fakehub.Fault{Status: 500, Skip: 1}) // the mark passes
			}
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			e.runner.loop = stopping(tc.cause)(cancel)

			res, err := e.runner.Run(ctx, e.fix(func(r *jobs.Request) { r.RequestLabel = "af:fix" }))

			env := envelope(t, res)
			category, message, retryable, _ := errorOf(env)
			if err != nil || env["status"] != "failed" || category != "cancelled" || retryable || !strings.Contains(message, tc.reason) {
				t.Fatalf("error %v, envelope %s, want a cancelled failure that says it %s", err, res.Envelope, tc.reason)
			}
			want, set := []string{"af:fix", "af:ready"}, []string{"af:fix"}
			if tc.fails {
				want, set = []string{"af:ready"}, nil
				if !slices.Contains(codes(env), "request_label_failed") || !slices.Contains(failedWrites(env), "add_labels") {
					t.Errorf("warnings %v, failed writes %v, want request_label_failed", codes(env), failedWrites(env))
				}
			}
			if issue := e.issueNow(); !slices.Equal(issue.Labels, want) || len(issue.Comments) != 0 {
				t.Errorf("issue %v with %d comments, want %v and no comment", issue.Labels, len(issue.Comments), want)
			}
			if !slices.Equal(res.LabelsSet, set) || !slices.Equal(res.LabelsRemoved, []string{"af:in-progress"}) {
				t.Errorf("the result sets %v and removes %v", res.LabelsSet, res.LabelsRemoved)
			}
		})
	}
}

// The labels stay for the attempt that is handed over again.
func TestMirror_AShutdownWritesNothing(t *testing.T) {
	e := newEnv(t, fakehub.Workspace{})
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	var before int
	e.runner.loop = func(ctx context.Context, a *attempt) *outcome {
		before = e.hub.Total()
		return stopping(jobs.ErrShutdown)(cancel)(ctx, a)
	}

	res, err := e.runner.Run(ctx, e.fix(func(r *jobs.Request) { r.RequestLabel = "af:fix" }))

	if !errors.Is(err, jobs.ErrShutdown) || len(res.Envelope) != 0 {
		t.Errorf("result %s, error %v, want the shutdown", res.Envelope, err)
	}
	for _, r := range e.hub.Requests()[before:] {
		if strings.Contains(r.Path, "/issues/") || strings.HasPrefix(r.Path, sessionsPath) {
			t.Errorf("%s %s after the shutdown", r.Method, r.Path)
		}
	}
	if issue := e.issueNow(); !slices.Equal(issue.Labels, []string{"af:in-progress"}) {
		t.Errorf("issue %v, want in-progress kept", issue.Labels)
	}
}

func TestMirror_ACancelDuringTheEndDoesNotStopItsWrites(t *testing.T) {
	e := newEnv(t, fakehub.Workspace{})
	e.endsWith("done", withResult(fixResult("landed", "pr", true)))
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	e.sink.hook = func(typ string, data json.RawMessage) {
		if typ == "step" && strings.Contains(string(data), `"mirror"`) {
			cancel(jobs.ErrCancelled)
		}
	}

	res, err := e.runner.Run(ctx, e.fix())

	if err != nil || len(codes(envelope(t, res))) != 0 {
		t.Errorf("error %v, warnings %v, want none", err, codes(envelope(t, res)))
	}
	if issue := e.issueNow(); !slices.Equal(issue.Labels, []string{"af:landed"}) || len(issue.Comments) != 1 || e.session().Status != "completed" || !e.hub.Leases()[0].Released {
		t.Errorf("issue %v with %d comments, session %q, lease %+v", issue.Labels, len(issue.Comments), e.session().Status, e.hub.Leases()[0])
	}
}

// A label taken off and put back is neither set nor removed; one set, taken off and set again is set.
func TestMirror_LabelsAreReportedNetOfTheAttempt(t *testing.T) {
	e := newEnv(t, fakehub.Workspace{})
	e.hub.AddIssue(slug, fakehub.Issue{Number: 42, Title: "T", Labels: []string{"bug"}})
	e.runner.loop = func(ctx context.Context, a *attempt) *outcome {
		ref := *a.issue
		a.removeLabel(ctx, ref, "bug")
		a.addLabel(ctx, ref, "bug", WarnRequestLabelFailed)
		a.addLabel(ctx, ref, "triaged", WarnRequestLabelFailed)
		a.removeLabel(ctx, ref, "triaged")
		a.addLabel(ctx, ref, "triaged", WarnRequestLabelFailed)
		return ended(a, toolio.StatusFailed, fakerunner.WithError("git", "no", false))
	}

	res, err := e.run(e.fix())

	if err != nil || slices.Contains(res.LabelsSet, "bug") || slices.Contains(res.LabelsRemoved, "bug") || !slices.Contains(res.LabelsSet, "triaged") {
		t.Errorf("error %v, the result sets %v and removes %v, want triaged set and bug in neither", err, res.LabelsSet, res.LabelsRemoved)
	}
}

func TestMirror_AnEnvelopeThatDoesNotDecodeOnlyTakesInProgressOff(t *testing.T) {
	e := newEnv(t, fakehub.Workspace{})
	e.runner.loop = func(context.Context, *attempt) *outcome {
		return &outcome{result: jobs.Result{Envelope: []byte("not an envelope")}}
	}

	res, err := e.run(e.fix())

	if err != nil || string(res.Envelope) != "not an envelope" {
		t.Errorf("result %s, error %v, want the envelope as it was", res.Envelope, err)
	}
	if issue := e.issueNow(); len(issue.Labels) != 0 || len(issue.Comments) != 0 || e.session().Status != "failed" {
		t.Errorf("issue %v with %d comments, session %q", issue.Labels, len(issue.Comments), e.session().Status)
	}
	if len(e.log.Find("WARN", "could not be amended")) == 0 {
		t.Error("the envelope that could not be amended was not logged")
	}
}
