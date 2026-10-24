package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

const (
	issueURL    = forgeRepo + "/issues/42"
	pullURL     = forgeRepo + "/pull/9"
	issue43Path = "/api/v1/workspaces/acme/issues/43"
)

// mirrorEnv is an environment whose issue 42 carries the labels, with a lease that outlives the
// timeouts; the loop is a stand-in that a test fills with endsWith.
func mirrorEnv(t *testing.T, labels []string, vars map[string]string) *env {
	t.Helper()
	e := leaseEnv(t, vars)
	e.hub.SetLeaseTTL(3 * time.Hour)
	e.hub.AddIssue(slug, fakehub.Issue{Number: 42, Title: "Greeting is wrong", Body: "The greeting says helo.", Labels: labels})
	return e
}

// withResult sets the result of an envelope.
func withResult(result any) func(*toolio.Envelope) {
	return func(e *toolio.Envelope) { e.Result = result }
}

// endsWith makes the loop end with the envelope of the tool, as the worker takes it.
func (e *env) endsWith(kind jobs.Kind, status toolio.Status, mods ...func(*toolio.Envelope)) {
	e.runner.loop = func(_ context.Context, a *attempt) *outcome {
		all := append([]func(*toolio.Envelope){fakerunner.WithTool(string(kind))}, mods...)
		return &outcome{result: jobs.Result{Envelope: fakerunner.Env(status, all...), MaxAttempts: a.maxAttempts}}
	}
}

// issueNow is issue 42 as the hub holds it.
func (e *env) issueNow() fakehub.Issue {
	e.t.Helper()
	issue, ok := e.hub.IssueOf(slug, 42)
	if !ok {
		e.t.Fatal("the hub has no issue 42")
	}
	slices.Sort(issue.Labels)
	return issue
}

var markerKind = regexp.MustCompile(`kind=([a-z_]+)`)

// commentKinds are the kinds of the comments of issue 42, by their markers.
func (e *env) commentKinds() []string {
	var kinds []string
	for _, c := range e.issueNow().Comments {
		if m := markerKind.FindStringSubmatch(c.Body); m != nil {
			kinds = append(kinds, m[1])
		} else {
			kinds = append(kinds, "no-marker")
		}
	}
	return kinds
}

// fixResult is the result of a fix that landed.
func fixResult(outcome, land string, closes bool) map[string]any {
	res := map[string]any{
		"outcome": outcome, "branch": "fix/42-greeting-is-wrong", "verdict": "pass", "closes": closes, "land": land,
		"revert_check": map[string]any{"outcome": "proven", "reason": "tests_fail_without_the_change"},
	}
	if land == "pr" {
		res["pull_request"] = map[string]any{"number": 9, "url": pullURL, "draft": false}
	}
	if !closes {
		res["revert_check"] = map[string]any{"outcome": "not_proven", "reason": "tests_pass_without_the_change"}
	}
	return res
}

// triageResult is the result of a triage of the confidence.
func triageResult(confidence, action string) map[string]any {
	return map[string]any{
		"action": action, "diagnosis": map[string]any{"confidence": confidence, "title": "x"},
		"issue": map[string]any{"repo": "Org/Repo", "host": "github.example.com", "number": 42, "url": issueURL},
	}
}

// TestATextTriageThatEndsNeedsHumanWritesNothingAndADoneOneMirrorsToTheIssueItCreated_TS_13_66
// verifies TS-13-66.
// Verifies: 13-REQ-9.1
func TestATextTriageThatEndsNeedsHumanWritesNothingAndADoneOneMirrorsToTheIssueItCreated_TS_13_66(t *testing.T) {
	forgeWrites := func(e *env) []string {
		var out []string
		for _, r := range e.writes() {
			if strings.Contains(r.Path, "/issues") || strings.HasSuffix(r.Path, "/pulls") {
				out = append(out, r.Method+" "+r.Path)
			}
		}
		return out
	}

	t.Run("a question", func(t *testing.T) {
		e := mirrorEnv(t, nil, nil)
		e.hub.AddIssue(slug, fakehub.Issue{Number: 7, Title: "Another issue", Body: "x"})
		e.endsWith(jobs.KindTriage, toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which greeting?", "hello", "hi"),
			withResult(map[string]any{"issue": map[string]any{"repo": "Org/Repo", "number": 7, "url": forgeRepo + "/issues/7"}})) // even if it names one

		res, err := e.run(e.request(jobs.KindTriage, jobs.Input{Text: "The greeting is wrong."}))

		if err != nil || envelope(t, res)["status"] != "needs_human" {
			t.Fatalf("result %s, error %v", res.Envelope, err)
		}
		if writes := forgeWrites(e); len(writes) != 0 {
			t.Errorf("forge writes = %v, want none: there is no issue", writes)
		}
		if len(res.LabelsSet) != 0 || len(res.LabelsRemoved) != 0 {
			t.Errorf("labels set %v removed %v, want none", res.LabelsSet, res.LabelsRemoved)
		}
	})

	t.Run("a file that ends done with an issue the loop created", func(t *testing.T) {
		e := mirrorEnv(t, nil, nil)
		e.hub.AddIssue(slug, fakehub.Issue{Number: 7, Title: "The loop made this", Body: "x"})
		e.endsWith(jobs.KindTriage, toolio.StatusDone, withResult(map[string]any{
			"action": "created_issue", "diagnosis": map[string]any{"confidence": "confirmed"},
			"issue": map[string]any{"repo": "Org/Repo", "host": "github.example.com", "number": 7, "url": forgeRepo + "/issues/7"},
		}))

		res, err := e.run(e.request(jobs.KindTriage, jobs.Input{File: &jobs.File{Name: "bug.md", Content: "The greeting is wrong.\n"}}))

		if err != nil || envelope(t, res)["status"] != "done" {
			t.Fatalf("result %s, error %v", res.Envelope, err)
		}
		created, _ := e.hub.IssueOf(slug, 7)
		if !slices.Equal(created.Labels, []string{"af:ready"}) || len(created.Comments) != 0 {
			t.Errorf("the issue the loop created has labels %v and %d comments, want ready and none", created.Labels, len(created.Comments))
		}
		if other := e.issueNow(); len(other.Labels) != 0 {
			t.Errorf("issue 42 got the labels %v", other.Labels)
		}
	})

	t.Run("a text through the real loop", func(t *testing.T) {
		e := loopEnv(t, triageFiles, nil, nil, submitStep(probableDiagnosis))

		res, err := e.run(e.request(jobs.KindTriage, jobs.Input{Title: "Run starts the wrong thing", Text: "The app crashes on submit."}))

		if err != nil || envelope(t, res)["status"] != "done" {
			t.Fatalf("result %s, error %v", res.Envelope, err)
		}
		made := resultMap(t, res)["issue"].(map[string]any)["number"].(float64)
		created, ok := e.hub.IssueOf(slug, int(made))
		if !ok || !slices.Equal(created.Labels, []string{"af:ready"}) {
			t.Errorf("the issue the loop created (%v) has %+v, want it ready", made, created)
		}
	})
}

// TestSettingAStatusLabelRemovesTheOtherFourExceptThatAFixsInProgressKeepsReady_TS_13_67 verifies
// TS-13-67.
// Verifies: 13-REQ-9.2
func TestSettingAStatusLabelRemovesTheOtherFourExceptThatAFixsInProgressKeepsReady_TS_13_67(t *testing.T) {
	for name, tc := range map[string]struct {
		kind    jobs.Kind
		initial []string
		during  []string // the labels while the loop runs: after the mark
		after   []string
		set     []string // what the result says the attempt set, and removed: the net of the attempt
		removed []string
		status  toolio.Status
		result  map[string]any
	}{
		"a fix that marks an issue that is ready": {jobs.KindFix, []string{"af:ready"}, []string{"af:in-progress", "af:ready"}, []string{"af:landed"},
			[]string{"af:landed"}, []string{"af:in-progress", "af:ready"}, toolio.StatusDone, fixResult("landed", "pr", true)},
		"a triage that marks an issue that is ready": {jobs.KindTriage, []string{"af:ready"}, []string{"af:in-progress"}, []string{"af:ready"},
			nil, []string{"af:in-progress"}, toolio.StatusDone, triageResult("probable", "replaced_body")},
		"a fix that marks an issue that needs a person": {jobs.KindFix, []string{"af:needs-human"}, []string{"af:in-progress"}, []string{"af:landed"},
			[]string{"af:landed"}, []string{"af:in-progress", "af:needs-human"}, toolio.StatusDone, fixResult("landed", "pr", true)},
		"a fix that lands over every other label": {jobs.KindFix, []string{"af:ready", "af:failed", "af:needs-human", "other"}, []string{"af:in-progress", "af:ready", "other"}, []string{"af:landed", "other"},
			[]string{"af:landed"}, []string{"af:failed", "af:in-progress", "af:needs-human", "af:ready"}, toolio.StatusDone, fixResult("landed", "pr", true)},
		"a fix that fails again an issue that failed": {jobs.KindFix, []string{"af:failed"}, []string{"af:in-progress"}, []string{"af:failed"},
			nil, []string{"af:in-progress"}, toolio.StatusFailed, nil},
	} {
		t.Run(name, func(t *testing.T) {
			e := mirrorEnv(t, tc.initial, nil)
			var during []string
			e.runner.loop = func(_ context.Context, a *attempt) *outcome {
				during = e.issueNow().Labels
				mods := []func(*toolio.Envelope){fakerunner.WithTool(string(tc.kind))}
				if tc.result != nil {
					mods = append(mods, withResult(tc.result))
				}
				if tc.status == toolio.StatusFailed {
					mods = append(mods, fakerunner.WithError("git", "it failed", false))
				}
				return &outcome{result: jobs.Result{Envelope: fakerunner.Env(tc.status, mods...), MaxAttempts: a.maxAttempts}}
			}

			res, err := e.run(e.request(tc.kind, issueInput("42")))
			if err != nil {
				t.Fatal(err)
			}

			want := slices.Clone(tc.during)
			slices.Sort(want)
			if !slices.Equal(during, want) {
				t.Errorf("while the loop ran the issue carried %v, want %v", during, want)
			}
			wantAfter := slices.Clone(tc.after)
			slices.Sort(wantAfter)
			if got := e.issueNow().Labels; !slices.Equal(got, wantAfter) {
				t.Errorf("after the attempt the issue carries %v, want %v", got, wantAfter)
			}
			set, removed := slices.Clone(res.LabelsSet), slices.Clone(res.LabelsRemoved)
			slices.Sort(set)
			slices.Sort(removed)
			if !slices.Equal(set, tc.set) && (len(set) != 0 || len(tc.set) != 0) || !slices.Equal(removed, tc.removed) {
				t.Errorf("the result sets %v and removes %v, want %v and %v: a label is not both set and removed", set, removed, tc.set, tc.removed)
			}
			for _, label := range set {
				if slices.Contains(removed, label) {
					t.Errorf("%s is both set and removed", label)
				}
			}
		})
	}
}

// TestEachOutcomeMapsToItsStatusLabelAndComment_TS_13_68 verifies TS-13-68.
// Verifies: 13-REQ-9.3
func TestEachOutcomeMapsToItsStatusLabelAndComment_TS_13_68(t *testing.T) {
	for name, tc := range map[string]struct {
		kind     jobs.Kind
		vars     map[string]string
		status   toolio.Status
		mods     []func(*toolio.Envelope)
		labels   []string
		comment  string
		contains []string
	}{
		"a triage that is confirmed":             {kind: jobs.KindTriage, status: toolio.StatusDone, mods: []func(*toolio.Envelope){withResult(triageResult("confirmed", "replaced_body"))}, labels: []string{"af:ready"}},
		"a triage that is probable":              {kind: jobs.KindTriage, status: toolio.StatusDone, mods: []func(*toolio.Envelope){withResult(triageResult("probable", "replaced_body"))}, labels: []string{"af:ready"}},
		"a triage that is suspected":             {kind: jobs.KindTriage, status: toolio.StatusDone, mods: []func(*toolio.Envelope){withResult(triageResult("suspected", "replaced_body"))}, labels: []string{"af:needs-human"}, comment: "reason", contains: []string{"suspected"}},
		"a triage that is not reproducible":      {kind: jobs.KindTriage, status: toolio.StatusDone, mods: []func(*toolio.Envelope){withResult(triageResult("not_reproducible", "replaced_body"))}, labels: []string{"af:needs-human"}, comment: "reason", contains: []string{"not_reproducible"}},
		"a triage that asks":                     {kind: jobs.KindTriage, status: toolio.StatusNeedsHuman, mods: []func(*toolio.Envelope){fakerunner.WithQuestion("Which greeting?", "hello", "hi")}, labels: []string{"af:needs-human"}, comment: "question", contains: []string{"Which greeting?", "A. hello", "B. hi"}},
		"a fix that asks":                        {kind: jobs.KindFix, status: toolio.StatusNeedsHuman, mods: []func(*toolio.Envelope){fakerunner.WithQuestion("Which greeting?", "hello", "hi")}, labels: []string{"af:needs-human"}, comment: "question", contains: []string{"Which greeting?"}},
		"a fix that is done with a pull request": {kind: jobs.KindFix, status: toolio.StatusDone, mods: []func(*toolio.Envelope){withResult(fixResult("landed", "pr", true))}, labels: []string{"af:landed"}, comment: "fix_summary", contains: []string{"landed", pullURL, "fix/42-greeting-is-wrong", "Closes " + issueURL}},
		"a fix that is unverified and landed":    {kind: jobs.KindFix, status: toolio.StatusUnverified, mods: []func(*toolio.Envelope){withResult(fixResult("landed", "pr", false))}, labels: []string{"af:landed"}, comment: "fix_summary", contains: []string{"Refs " + issueURL, "not proven"}},
		"a fix that landed as a branch":          {kind: jobs.KindFix, vars: map[string]string{"AF_FIX_LAND": "branch"}, status: toolio.StatusDone, mods: []func(*toolio.Envelope){withResult(fixResult("landed", "branch", true))}, labels: []string{"af:landed"}, comment: "fix_summary", contains: []string{"no pull request"}},
		"a fix that kept nothing":                {kind: jobs.KindFix, vars: map[string]string{"AF_FIX_LAND": "none"}, status: toolio.StatusDone, mods: []func(*toolio.Envelope){withResult(fixResult("landed", "none", true))}, labels: []string{"af:ready"}, comment: "fix_summary", contains: []string{"measured", "nothing was pushed or kept"}},
		"a fix that is parked": {kind: jobs.KindFix, status: toolio.StatusUnverified, mods: []func(*toolio.Envelope){withResult(fixResult("parked", "pr", false)), func(e *toolio.Envelope) {
			e.Result.(map[string]any)["verdict"] = "still_failing"
		}}, labels: []string{"af:needs-human"}, comment: "fix_summary", contains: []string{"parked", "still_failing", "af jobs retry job_0123456789abcdef0123", "POST /api/v1/jobs/job_0123456789abcdef0123/retry"}},
		"a failure":       {kind: jobs.KindFix, status: toolio.StatusFailed, mods: []func(*toolio.Envelope){fakerunner.WithError("git", "the branch could not be pushed", false)}, labels: []string{"af:failed"}, comment: "failure", contains: []string{"git", "the branch could not be pushed", "POST /api/v1/jobs/job_0123456789abcdef0123/retry"}},
		"a usage failure": {kind: jobs.KindTriage, status: toolio.StatusUsage, mods: []func(*toolio.Envelope){fakerunner.WithError("usage", "the input is empty", false)}, labels: []string{"af:failed"}, comment: "failure", contains: []string{"usage", "the input is empty"}},
	} {
		t.Run(name, func(t *testing.T) {
			// A fix that kept nothing leaves a triaged issue as it was: ready.
			initial := []string(nil)
			if strings.Contains(name, "kept nothing") {
				initial = []string{"af:ready"}
			}
			e := mirrorEnv(t, initial, tc.vars)
			e.endsWith(tc.kind, tc.status, tc.mods...)

			res, err := e.run(e.request(tc.kind, issueInput("42")))

			if err != nil {
				t.Fatal(err)
			}
			want := slices.Clone(tc.labels)
			slices.Sort(want)
			if got := e.issueNow().Labels; !slices.Equal(got, want) {
				t.Errorf("labels = %v, want %v", got, want)
			}
			kinds := e.commentKinds()
			switch {
			case tc.comment == "" && len(kinds) != 0:
				t.Errorf("comments = %v, want none", kinds)
			case tc.comment != "" && !slices.Equal(kinds, []string{tc.comment}):
				t.Errorf("comments = %v, want one of the kind %s", kinds, tc.comment)
			}
			if tc.comment != "" && len(e.issueNow().Comments) > 0 {
				body := e.issueNow().Comments[0].Body
				for _, want := range tc.contains {
					if !strings.Contains(body, want) {
						t.Errorf("the comment lacks %q:\n%s", want, body)
					}
				}
			}
			if !slices.Contains(res.LabelsRemoved, "af:in-progress") {
				t.Errorf("the result removes %v, want in-progress among them", res.LabelsRemoved)
			}
			if status := envelope(t, res)["status"]; status != string(tc.status) {
				t.Errorf("the status of the envelope is %v, want %s: the mirror never changes it", status, tc.status)
			}
		})
	}
}

// TestATransientErrorOrAFailureAnAutomaticAttemptFollowsOnlyRemovesInProgress_TS_13_69 verifies
// TS-13-69.
// Verifies: 13-REQ-9.4
func TestATransientErrorOrAFailureAnAutomaticAttemptFollowsOnlyRemovesInProgress_TS_13_69(t *testing.T) {
	failed := func(category string, retryable bool) []func(*toolio.Envelope) {
		return []func(*toolio.Envelope){fakerunner.WithError(category, "it failed: "+category, retryable)}
	}
	for name, tc := range map[string]struct {
		transient bool
		category  string
		retryable bool
		round     int
		max       string
		follows   bool // an automatic attempt follows: nothing is set and nothing is said
	}{
		"a transient error":                                  {transient: true, round: 1, follows: true},
		"a failed brain at position 1 of 2":                  {category: "brain", retryable: true, round: 1, max: "2", follows: true},
		"a failed no_result at position 1 of 3":              {category: "no_result", retryable: true, round: 1, max: "3", follows: true},
		"a failed brain at position 2 of 3":                  {category: "brain", retryable: true, round: 2, max: "3", follows: true},
		"a failed brain at position 2 of 2":                  {category: "brain", retryable: true, round: 2, max: "2"},
		"a failed brain that is not retryable":               {category: "brain", retryable: false, round: 1, max: "2"},
		"a failed git at position 1 of 2":                    {category: "git", retryable: true, round: 1, max: "2"},
		"a failed brain, the default maximum":                {category: "brain", retryable: true, round: 1, follows: true},
		"a failed brain, the default maximum, at position 2": {category: "brain", retryable: true, round: 2},
		"a failed brain, no round position given":            {category: "brain", retryable: true, round: 0, follows: true},
		"a failed brain, no round position, a maximum of 1":  {category: "brain", retryable: true, round: 0, max: "1"},
	} {
		t.Run(name, func(t *testing.T) {
			vars := map[string]string{}
			if tc.max != "" {
				vars["AF_MAX_ATTEMPTS"] = tc.max
			}
			e := mirrorEnv(t, nil, vars)
			if tc.transient {
				e.runner.loop = func(context.Context, *attempt) *outcome {
					return transientFailure(toolio.CategorySandbox, "the box was lost")
				}
			} else {
				e.endsWith(jobs.KindFix, toolio.StatusFailed, failed(tc.category, tc.retryable)...)
			}

			res, err := e.run(e.request(jobs.KindFix, issueInput("42"), func(r *jobs.Request) { r.Round = tc.round }))

			if tc.transient != jobs.IsTransient(err) {
				t.Fatalf("error = %v, want transient: %v", err, tc.transient)
			}
			labels, kinds := e.issueNow().Labels, e.commentKinds()
			if tc.follows {
				if len(labels) != 0 || len(kinds) != 0 {
					t.Errorf("labels %v and comments %v, want in-progress removed and nothing else", labels, kinds)
				}
				if !slices.Equal(res.LabelsRemoved, []string{"af:in-progress"}) || len(res.LabelsSet) != 0 {
					t.Errorf("the result sets %v and removes %v, want in-progress removed and none set", res.LabelsSet, res.LabelsRemoved)
				}
			} else if !slices.Equal(labels, []string{"af:failed"}) || !slices.Equal(kinds, []string{"failure"}) {
				t.Errorf("labels %v and comments %v, want failed and one comment: this attempt ends the job", labels, kinds)
			}
		})
	}
}

// leakedPath finds an absolute path of a worker.
var leakedPath = regexp.MustCompile(`(^|[\s'"(\x60])/(tmp|var|private|Users|home|root|opt|workspace)/[^\s'")\x60]*`)

// TestCommentsCarryMarkersAndTheirPartsAndNoPathsAndAreNotReposted_TS_13_70 verifies TS-13-70.
// Verifies: 13-REQ-9.5
func TestCommentsCarryMarkersAndTheirPartsAndNoPathsAndAreNotReposted_TS_13_70(t *testing.T) {
	jobID := "job_0123456789abcdef0123"
	for name, tc := range map[string]struct {
		kind   jobs.Kind
		status toolio.Status
		mods   func(e *env) []func(*toolio.Envelope)
		want   string // the kind of the comment
		parts  []string
	}{
		"a question": {jobs.KindTriage, toolio.StatusNeedsHuman, func(*env) []func(*toolio.Envelope) {
			return []func(*toolio.Envelope){fakerunner.WithQuestion("Which greeting?", "hello", "hi", "hey")}
		}, "question", []string{"Which greeting?", "A. hello", "B. hi", "C. hey", "af jobs answer " + jobID, "POST /api/v1/jobs/" + jobID + "/answer"}},
		"a suspected diagnosis": {jobs.KindTriage, toolio.StatusDone, func(*env) []func(*toolio.Envelope) {
			return []func(*toolio.Envelope){withResult(triageResult("suspected", "replaced_body"))}
		}, "reason", []string{"suspected", "in the body of this issue", "POST /api/v1/workspaces/acme/triage", "context"}},
		"a diagnosis posted as a comment": {jobs.KindTriage, toolio.StatusDone, func(*env) []func(*toolio.Envelope) {
			return []func(*toolio.Envelope){withResult(triageResult("not_reproducible", "commented"))}
		}, "reason", []string{"not_reproducible", "in a comment", "POST /api/v1/workspaces/acme/triage"}},
		"a landed fix that is not proven": {jobs.KindFix, toolio.StatusUnverified, func(*env) []func(*toolio.Envelope) {
			return []func(*toolio.Envelope){withResult(fixResult("landed", "pr", false))}
		}, "fix_summary", []string{"landed", "fix/42-greeting-is-wrong", pullURL, "pass", "Refs " + issueURL, "What was not proven", "tests_pass_without_the_change"}},
		"a parked fix": {jobs.KindFix, toolio.StatusUnverified, func(*env) []func(*toolio.Envelope) {
			return []func(*toolio.Envelope){withResult(fixResult("parked", "pr", false))}
		}, "fix_summary", []string{"parked", "af jobs retry " + jobID, "POST /api/v1/jobs/" + jobID + "/retry"}},
		"a failure that names a path": {jobs.KindFix, toolio.StatusFailed, func(e *env) []func(*toolio.Envelope) {
			return []func(*toolio.Envelope){fakerunner.WithError("git", "cannot commit in "+filepath.Join(e.dir, "clone")+"/src and "+e.dir+"/envelope.json", false)}
		}, "failure", []string{"git", "cannot commit in", "POST /api/v1/jobs/" + jobID + "/retry"}},
	} {
		t.Run(name, func(t *testing.T) {
			e := mirrorEnv(t, nil, nil)
			e.endsWith(tc.kind, tc.status, tc.mods(e)...)
			req := e.request(tc.kind, issueInput("42"))

			if _, err := e.run(req); err != nil {
				t.Fatal(err)
			}
			comments := e.issueNow().Comments
			if len(comments) != 1 {
				t.Fatalf("%d comments, want 1", len(comments))
			}
			body := comments[0].Body
			if want := fmt.Sprintf("job=%s attempt=%d kind=%s", jobID, req.Attempt, tc.want); !strings.Contains(body, want) {
				t.Errorf("the comment has no marker %q:\n%s", want, body)
			}
			for _, part := range tc.parts {
				if !strings.Contains(body, part) {
					t.Errorf("the comment lacks %q:\n%s", part, body)
				}
			}
			if m := leakedPath.FindString(body); m != "" || strings.Contains(body, e.dir) || strings.Contains(body, os.TempDir()) {
				t.Errorf("the comment holds a path of the worker (%q):\n%s", m, body)
			}

			// The same attempt again, as after a restart: the marker is on the issue, and nothing is posted twice.
			if _, err := e.run(req); err != nil {
				t.Fatal(err)
			}
			if got := len(e.issueNow().Comments); got != 1 {
				t.Errorf("%d comments after the same attempt ran again, want 1", got)
			}
		})
	}

	t.Run("another attempt of the same job posts its own", func(t *testing.T) {
		e := mirrorEnv(t, nil, nil)
		e.endsWith(jobs.KindFix, toolio.StatusFailed, fakerunner.WithError("git", "no", false))
		for attempt := 1; attempt <= 2; attempt++ {
			if _, err := e.run(e.request(jobs.KindFix, issueInput("42"), func(r *jobs.Request) { r.Attempt = attempt })); err != nil {
				t.Fatal(err)
			}
		}
		if got := len(e.issueNow().Comments); got != 2 {
			t.Errorf("%d comments for two attempts, want 2", got)
		}
	})
}

// failedWrites are the side effects of an envelope whose state is failed, as kind.
func failedWrites(env map[string]any) []string {
	var out []string
	list, _ := env["side_effects"].([]any)
	for _, s := range list {
		m := s.(map[string]any)
		if m["state"] == "failed" {
			out = append(out, m["kind"].(string))
		}
	}
	return out
}

// TestAFailedForgeWriteIsAWarningAndASideEffectAndNeverAStatusChange_TS_13_71 verifies TS-13-71.
// Verifies: 13-REQ-9.6
func TestAFailedForgeWriteIsAWarningAndASideEffectAndNeverAStatusChange_TS_13_71(t *testing.T) {
	t.Run("the label write of the mark and of the mirror", func(t *testing.T) {
		e := mirrorEnv(t, nil, nil)
		e.hub.Inject("POST", issuePath+"/labels", fakehub.Fault{Status: 500})
		e.endsWith(jobs.KindFix, toolio.StatusDone, withResult(fixResult("landed", "pr", true)))

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		env := envelope(t, res)
		if err != nil || env["status"] != "done" || env["exit_code"] != float64(0) {
			t.Fatalf("status %v, exit code %v, error %v: the status must not change", env["status"], env["exit_code"], err)
		}
		if got := codes(env); !slices.Contains(got, "status_label_failed") {
			t.Errorf("warnings = %v, want status_label_failed", got)
		}
		if got := failedWrites(env); !slices.Contains(got, "add_labels") {
			t.Errorf("failed side effects = %v, want add_labels", got)
		}
		if len(res.LabelsSet) != 0 {
			t.Errorf("the result says it set %v: a label that was not written is not set", res.LabelsSet)
		}
	})

	t.Run("a label that cannot be set removes none of the others", func(t *testing.T) {
		e := mirrorEnv(t, []string{"af:failed", "af:ready"}, nil)
		e.hub.Inject("POST", issuePath+"/labels", fakehub.Fault{Status: 500})
		e.endsWith(jobs.KindFix, toolio.StatusDone, withResult(fixResult("landed", "pr", true)))

		res, _ := e.run(e.request(jobs.KindFix, issueInput("42")))

		if got := e.issueNow().Labels; !slices.Equal(got, []string{"af:failed", "af:ready"}) {
			t.Errorf("labels = %v: labels were removed though the one that replaces them was not set", got)
		}
		if len(res.LabelsRemoved) != 0 {
			t.Errorf("the result removes %v", res.LabelsRemoved)
		}
	})

	t.Run("a label that is gone already is removed", func(t *testing.T) {
		e := mirrorEnv(t, []string{"af:failed"}, nil)
		e.hub.Inject("DELETE", issuePath+"/labels/*", fakehub.Fault{Status: 404})
		e.endsWith(jobs.KindFix, toolio.StatusDone, withResult(fixResult("landed", "pr", true)))

		res, _ := e.run(e.request(jobs.KindFix, issueInput("42")))

		if env := envelope(t, res); slices.Contains(codes(env), "status_label_failed") || len(failedWrites(env)) != 0 {
			t.Errorf("warnings %v, failed writes %v: a label that was not there is not a failure", codes(env), failedWrites(env))
		}
		if !slices.Contains(res.LabelsRemoved, "af:failed") {
			t.Errorf("the result removes %v, want af:failed among them", res.LabelsRemoved)
		}
	})

	t.Run("a label that cannot be removed", func(t *testing.T) {
		e := mirrorEnv(t, []string{"af:failed"}, nil)
		e.hub.Inject("DELETE", issuePath+"/labels/*", fakehub.Fault{Status: 500})
		e.endsWith(jobs.KindFix, toolio.StatusDone, withResult(fixResult("landed", "pr", true)))

		res, _ := e.run(e.request(jobs.KindFix, issueInput("42")))

		env := envelope(t, res)
		if !slices.Contains(codes(env), "status_label_failed") || !slices.Contains(failedWrites(env), "remove_label") || env["status"] != "done" {
			t.Errorf("warnings %v, failed writes %v, status %v", codes(env), failedWrites(env), env["status"])
		}
		if slices.Contains(res.LabelsRemoved, "af:failed") {
			t.Errorf("the result says it removed %v", res.LabelsRemoved)
		}
	})

	t.Run("the comment", func(t *testing.T) {
		e := mirrorEnv(t, nil, nil)
		e.hub.Inject("POST", issuePath+"/comments", fakehub.Fault{Status: 500})
		e.endsWith(jobs.KindFix, toolio.StatusFailed, fakerunner.WithError("git", "the branch could not be pushed", false))

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		env := envelope(t, res)
		if err != nil || env["status"] != "failed" || !slices.Contains(codes(env), "comment_failed") || !slices.Contains(failedWrites(env), "add_comment") {
			t.Errorf("status %v, warnings %v, failed writes %v, error %v", env["status"], codes(env), failedWrites(env), err)
		}
		if !slices.Equal(e.issueNow().Labels, []string{"af:failed"}) {
			t.Errorf("labels = %v: the status label is set though the comment failed", e.issueNow().Labels)
		}
	})

	t.Run("the request label put back by a cancellation", func(t *testing.T) {
		e := mirrorEnv(t, nil, nil)
		e.hub.Inject("POST", issuePath+"/labels", fakehub.Fault{Status: 500, Skip: 1}) // the mark passes
		ctx, cancel := context.WithCancelCause(context.Background())
		defer cancel(nil)
		e.runner.loop = func(ctx context.Context, a *attempt) *outcome {
			cancel(jobs.ErrCancelled)
			return &outcome{err: context.Cause(ctx)}
		}

		res, err := e.runner.Run(ctx, e.request(jobs.KindFix, issueInput("42"), func(r *jobs.Request) { r.RequestLabel = "af:fix" }))

		env := envelope(t, res)
		category, _, _, _ := errorOf(env)
		if err != nil || category != "cancelled" || !slices.Contains(codes(env), "request_label_failed") || !slices.Contains(failedWrites(env), "add_labels") {
			t.Errorf("error %v, category %q, warnings %v, failed writes %v", err, category, codes(env), failedWrites(env))
		}
		if slices.Contains(res.LabelsSet, "af:fix") {
			t.Errorf("the result says it put back %v", res.LabelsSet)
		}
	})

	t.Run("under a transient error the failure is logged", func(t *testing.T) {
		e := mirrorEnv(t, nil, nil)
		var logged bytes.Buffer
		e.runner.cfg.Logger = slog.New(slog.NewTextHandler(&logged, nil))
		e.hub.Inject("DELETE", issuePath+"/labels/*", fakehub.Fault{Status: 500})
		e.runner.loop = func(context.Context, *attempt) *outcome {
			return transientFailure(toolio.CategorySandbox, "the box was lost")
		}

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		if !jobs.IsTransient(err) || len(res.Envelope) != 0 {
			t.Fatalf("result %q, error %v, want the transient error as it was", res.Envelope, err)
		}
		if out := logged.String(); !strings.Contains(out, "status_label_failed") || strings.Contains(out, pat) {
			t.Errorf("log = %q, want the failure of the label write, and not the token", out)
		}
	})
}

// blockFiles is the repository of the fixtures with a target, block, that notes that it has begun
// by making the file, and then waits.
func blockFiles(marker string) map[string]string {
	files := map[string]string{}
	for name, content := range fixFiles {
		files[name] = content
	}
	files["Makefile"] += "block:\n\ttouch '" + marker + "'\n\tsleep 120\n"
	return files
}

// blockStep is the step of the scripted brain that runs it.
func blockStep() map[string]any {
	return map[string]any{"tool": "execute", "args": map[string]any{"command": "make block"}, "expect": "ok"}
}

// runUntil runs the attempt until ready says the loop is under way, cancels it with the cause, and
// returns what the attempt returned, how long it took after the cancel, and how many requests
// the hub had when it was cancelled.
func runUntil(t *testing.T, e *env, req jobs.Request, ready func() bool, cause error) (res jobs.Result, err error, took time.Duration, before int) {
	t.Helper()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		res, err = e.runner.Run(ctx, req)
	}()
	deadline := time.Now().Add(60 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			cancel(errors.New("the test gave up"))
			<-done
			t.Fatal("the loop never got under way")
		}
		select {
		case <-done:
			t.Fatalf("the attempt ended before the loop was under way: %s, %v", res.Envelope, err)
		case <-time.After(20 * time.Millisecond):
		}
	}
	before = e.hub.Total()
	start := time.Now()
	cancel(cause)
	select {
	case <-done:
	case <-time.After(90 * time.Second):
		t.Fatal("the attempt did not return after it was cancelled")
	}
	return res, err, time.Since(start), before
}

// TestACancelledRunningAttemptRestoresTheRequestLabelWithinTheGrace_TS_13_72 verifies TS-13-72.
// Verifies: 13-REQ-9.7
func TestACancelledRunningAttemptRestoresTheRequestLabelWithinTheGrace_TS_13_72(t *testing.T) {
	for name, cause := range map[string]error{"a cancel": jobs.ErrCancelled, "an unsubscribe": jobs.ErrUnsubscribed} {
		t.Run(name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "begun")
			e := loopEnv(t, blockFiles(marker), nil, nil, blockStep())
			e.hub.AddIssue(slug, fakehub.Issue{Number: 43, Title: "Greeting is wrong", Body: "x", Labels: []string{"af:ready"}})
			req := e.request(jobs.KindFix, issueInput("43"), func(r *jobs.Request) { r.RequestLabel = "af:fix" })
			begun := func() bool { _, err := os.Stat(marker); return err == nil }
			go func() { // the subscription is gone by the time the attempt is cancelled
				for !begun() {
					time.Sleep(10 * time.Millisecond)
				}
				e.subs.mu.Lock()
				delete(e.subs.tokens, slug)
				e.subs.mu.Unlock()
			}()

			res, err, took, _ := runUntil(t, e, req, begun, cause)

			if err != nil {
				t.Fatalf("error = %v, want a result", err)
			}
			if took > 60*time.Second {
				t.Errorf("the attempt returned %v after the cancel, want within the grace of 60s", took)
			}
			issue, _ := e.hub.IssueOf(slug, 43)
			slices.Sort(issue.Labels)
			if !slices.Equal(issue.Labels, []string{"af:fix", "af:ready"}) || len(issue.Comments) != 0 {
				t.Errorf("issue 43 has the labels %v and %d comments, want ready and the request label back, and no comment", issue.Labels, len(issue.Comments))
			}
			if !slices.Equal(res.LabelsSet, []string{"af:fix"}) || !slices.Equal(res.LabelsRemoved, []string{"af:in-progress"}) {
				t.Errorf("the result sets %v and removes %v, want af:fix set and af:in-progress removed", res.LabelsSet, res.LabelsRemoved)
			}
			env := envelope(t, res)
			if env["tool"] != "fix" || env["run_id"] == nil {
				t.Errorf("the result does not carry the envelope of the loop: %s", res.Envelope)
			}
			if leases := e.hub.Leases(); len(leases) != 1 || !leases[0].Released {
				t.Errorf("leases = %+v, want the one that was held released", leases)
			}
			if _, err := os.Stat(filepath.Join(e.dir, "clone")); !os.IsNotExist(err) {
				t.Errorf("clone/ is kept after a cancel: %v", err)
			}
		})
	}

	t.Run("a cancel before the loop starts", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		e.hub.AddIssue(slug, fakehub.Issue{Number: 43, Title: "Greeting is wrong", Body: "x", Labels: []string{"af:ready"}})
		e.hub.Inject("", "/git/acme.git", fakehub.Fault{Delay: 60 * time.Second})
		req := e.request(jobs.KindFix, issueInput("43"), func(r *jobs.Request) { r.RequestLabel = "af:fix" })
		marked := false
		cloning := func() bool { // the issue is marked, and the clone has begun to wait
			if !marked && len(e.hub.RequestsTo("POST", issue43Path+"/labels")) > 0 {
				time.Sleep(300 * time.Millisecond)
				marked = true
			}
			return marked
		}

		res, err, _, _ := runUntil(t, e, req, cloning, jobs.ErrCancelled)

		if err != nil {
			t.Fatalf("error = %v, want a result", err)
		}
		category, _, retryable, _ := errorOf(envelope(t, res))
		if envelope(t, res)["status"] != "failed" || category != "cancelled" || retryable {
			t.Errorf("envelope %s, want a failed one of category cancelled", res.Envelope)
		}
		issue, _ := e.hub.IssueOf(slug, 43)
		slices.Sort(issue.Labels)
		if !slices.Equal(issue.Labels, []string{"af:fix", "af:ready"}) {
			t.Errorf("issue 43 has the labels %v, want ready and the request label back", issue.Labels)
		}
	})
}

// TestAShutdownWritesNothingToTheForgeKeepsTheCloneAndReleasesTheLease_TS_13_73 verifies TS-13-73.
// Verifies: 13-REQ-9.8
func TestAShutdownWritesNothingToTheForgeKeepsTheCloneAndReleasesTheLease_TS_13_73(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "begun")
	e := loopEnv(t, blockFiles(marker), nil, nil, blockStep())
	req := e.request(jobs.KindFix, issueInput("42"), func(r *jobs.Request) { r.RequestLabel = "af:fix" })
	begun := func() bool { _, err := os.Stat(marker); return err == nil }

	res, err, _, before := runUntil(t, e, req, begun, jobs.ErrShutdown)

	if !errors.Is(err, jobs.ErrShutdown) || len(res.Envelope) != 0 {
		t.Errorf("result %q, error %v, want the shutdown", res.Envelope, err)
	}
	var after []string
	for _, r := range e.hub.Requests() {
		if r.Seq > e.hub.Requests()[before-1].Seq && r.Method != "GET" {
			after = append(after, r.Method+" "+r.Path)
		}
	}
	for _, w := range after {
		if strings.Contains(w, "/issues/") || strings.HasSuffix(w, "/pulls") {
			t.Errorf("a write to the forge after the shutdown: %s", w)
		}
	}
	issue := e.issueNow()
	if !slices.Equal(issue.Labels, []string{"af:in-progress"}) || len(issue.Comments) != 0 {
		t.Errorf("issue 42 has the labels %v and %d comments, want in-progress kept for the resumed attempt", issue.Labels, len(issue.Comments))
	}
	if _, err := os.Stat(filepath.Join(e.dir, "clone")); !os.IsNotExist(err) {
		t.Errorf("clone/ is kept after a shutdown: %v; the next delivery begins afresh", err)
	}
	leases := e.hub.Leases()
	if len(leases) != 1 || !leases[0].Released {
		t.Fatalf("leases = %+v", leases)
	}
	if n := len(e.hub.RequestsTo("DELETE", sandboxesPath+"/"+leases[0].ID)); n != 1 {
		t.Errorf("%d releases, want one", n)
	}
}

// TestThePathsOfTheWorkerAreTakenOutOfWhatGoesToAnIssue pins what is taken out of a text that goes
// into a comment, and what is not.
// Verifies: 13-REQ-9.5
func TestThePathsOfTheWorkerAreTakenOutOfWhatGoesToAnIssue(t *testing.T) {
	a := &attempt{cloneDir: "/srv/state/attempts/job_1/2/clone", req: jobs.Request{Dir: "/srv/state/attempts/job_1/2"}}
	home, _ := os.UserHomeDir()
	for name, tc := range map[string]struct {
		text string
		gone []string
		kept []string
	}{
		"the clone":           {"cannot read /srv/state/attempts/job_1/2/clone/src/greet.sh", []string{"/srv/state"}, []string{"src/greet.sh"}},
		"the attempt":         {"wrote /srv/state/attempts/job_1/2/envelope.json", []string{"/srv/state"}, []string{"envelope.json"}},
		"the temporary files": {"in " + os.TempDir() + "/x/y", []string{os.TempDir()}, nil},
		"the home":            {"in " + home + "/go/pkg", []string{home}, nil},
		"another path":        {"saw /opt/tools/bin/make and '/var/lib/x/y'", []string{"/opt/tools", "/var/lib"}, []string{"saw "}},
		"a route":             {"replace the token with PUT /api/v1/workspaces/acme", nil, []string{"PUT /api/v1/workspaces/acme"}},
		"a URL":               {"see https://github.example.com/var/lib/x/issues/1", nil, []string{"https://github.example.com/var/lib/x/issues/1"}},
		"a text with no path": {"the greeting says helo", nil, []string{"the greeting says helo"}},
	} {
		got := a.scrub(tc.text)
		for _, gone := range tc.gone {
			if len(gone) > 1 && strings.Contains(got, gone) {
				t.Errorf("%s: %q still holds %q", name, got, gone)
			}
		}
		for _, kept := range tc.kept {
			if !strings.Contains(got, kept) {
				t.Errorf("%s: %q lost %q", name, got, kept)
			}
		}
	}
}

// TestTheEnvelopeOfAnAttemptCancelledBeforeTheLoopSaysWhy pins the two causes that read as a cancel.
// Verifies: 13-REQ-9.7
func TestTheEnvelopeOfAnAttemptCancelledBeforeTheLoopSaysWhy(t *testing.T) {
	e := leaseEnv(t, nil)
	a := &attempt{req: e.fixRequest(), maxAttempts: 2, started: time.Now()}
	for cause, want := range map[error]string{jobs.ErrCancelled: "was cancelled", jobs.ErrUnsubscribed: "was unsubscribed"} {
		out := e.runner.cancelled(a, cause)

		category, message, retryable, _ := errorOf(envelope(t, out.result))
		if category != "cancelled" || retryable || !strings.Contains(message, want) {
			t.Errorf("%v: category %q, message %q, retryable %v, want a cancelled failure that says it %s", cause, category, message, retryable, want)
		}
	}
}

// TestACancelThatComesWhileTheAttemptIsBeingEndedDoesNotBreakTheWritesThatEndIt pins that the
// writes of the mirror, the release and the report are made on a context that a cancel does not
// reach, also when the cancel comes after the loop has returned: an attempt that was ended is
// ended in full.
// Verifies: 13-REQ-9.7, 13-REQ-6.7, 13-REQ-10.3
func TestACancelThatComesWhileTheAttemptIsBeingEndedDoesNotBreakTheWritesThatEndIt(t *testing.T) {
	e := mirrorEnv(t, nil, nil)
	e.endsWith(jobs.KindFix, toolio.StatusDone, withResult(fixResult("landed", "pr", true)))
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	e.sink.hook = func(typ string, data json.RawMessage) {
		if typ == "step" && strings.Contains(string(data), `"mirror"`) {
			cancel(jobs.ErrCancelled) // the loop has returned; the mirror is about to write
		}
	}

	res, err := e.runner.Run(ctx, e.request(jobs.KindFix, issueInput("42")))

	if err != nil {
		t.Fatal(err)
	}
	env := envelope(t, res)
	if got := codes(env); len(got) != 0 {
		t.Errorf("warnings = %v: a write failed for a context that was cancelled", got)
	}
	issue := e.issueNow()
	if !slices.Equal(issue.Labels, []string{"af:landed"}) || len(issue.Comments) != 1 {
		t.Errorf("issue 42 has the labels %v and %d comments, want landed and the summary", issue.Labels, len(issue.Comments))
	}
	if leases := e.hub.Leases(); len(leases) != 1 || !leases[0].Released {
		t.Errorf("leases = %+v", leases)
	}
	if session, _ := e.postedEvents(t); session.Status != "completed" {
		t.Errorf("the session was completed %q, want completed", session.Status)
	}
}

// TestALabelTakenOffAndPutBackIsNetOfTheAttempt pins the bookkeeping of the labels the result
// reports: a label the issue carried that the attempt took off and put back is neither set nor
// removed, and a label the attempt set, took off and set again is set, since the issue did not
// have it before.
// Verifies: 13-REQ-8.8
func TestALabelTakenOffAndPutBackIsNetOfTheAttempt(t *testing.T) {
	e := mirrorEnv(t, []string{"bug"}, nil)
	e.runner.loop = func(ctx context.Context, a *attempt) *outcome {
		ref := *a.issue
		a.removeLabel(ctx, ref, "bug")
		a.addLabel(ctx, ref, "bug", WarnRequestLabelFailed)
		a.addLabel(ctx, ref, "triaged", WarnRequestLabelFailed)
		a.removeLabel(ctx, ref, "triaged")
		a.addLabel(ctx, ref, "triaged", WarnRequestLabelFailed)
		return &outcome{result: jobs.Result{Envelope: fakerunner.Env(toolio.StatusFailed, fakerunner.WithTool(string(jobs.KindFix))), MaxAttempts: a.maxAttempts}}
	}

	res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if slices.Contains(res.LabelsSet, "bug") || slices.Contains(res.LabelsRemoved, "bug") {
		t.Errorf("the result sets %v and removes %v: bug was taken off and put back, and the issue has it as it had it", res.LabelsSet, res.LabelsRemoved)
	}
	if !slices.Contains(res.LabelsSet, "triaged") {
		t.Errorf("the result sets %v, want triaged among them: the issue did not have it before the attempt", res.LabelsSet)
	}
	if !slices.Contains(e.issueNow().Labels, "bug") || !slices.Contains(e.issueNow().Labels, "triaged") {
		t.Errorf("labels = %v, want bug and triaged on the issue", e.issueNow().Labels)
	}
}

// The sentences of the forge path.
const (
	answerPath = "Or reply on this issue, then add the label `af:fix` again: agent-fox takes the comments after this one as the answer."
	retryPath  = "Or add the label `%s` again; comments after this one go with the new attempt."
)

// composer is an attempt of the kind on issue 42 of acme, with the label triggers on or off, as far as composing its
// comments goes.
func composer(t *testing.T, kind jobs.Kind, triggersOn bool, src jobs.Source) (*attempt, issuex.IssueRef) {
	ref := issuex.IssueRef{Repo: issuex.Repo{Host: "github.example.com", Owner: "Org", Name: "Repo"}, Number: 42}
	a := &attempt{
		req:      jobs.Request{JobID: "job_1", Attempt: 1, Workspace: slug, Kind: kind, Source: src, Dir: t.TempDir()},
		settings: Settings{TriggersOn: triggersOn, Land: "pr"},
		issue:    &ref,
	}
	return a, ref
}

// parked is the result of a fix that was parked.
func parkedResult() loopResult {
	branch, verdict := "af/fix-42", "fail"
	return loopResult{Outcome: "parked", Branch: &branch, Verdict: &verdict}
}

// TestTheQuestionCommentNamesTheForgePathAndKeepsItsInstructions_TS_19_32 verifies TS-19-32.
// Verifies: 19-REQ-6.1
func TestTheQuestionCommentNamesTheForgePathAndKeepsItsInstructions_TS_19_32(t *testing.T) {
	a, ref := composer(t, jobs.KindFix, true, jobs.Source{Kind: "label", By: "label af:fix"})
	env := toolio.Envelope{Status: toolio.StatusNeedsHuman, NeedsHuman: &toolio.NeedsHuman{Question: "Which greeting?"}}
	c := a.compose(commentQuestion, ref, env, loopResult{})
	for _, want := range []string{answerPath, "af jobs answer job_1", "POST /api/v1/jobs/job_1/answer"} {
		if !strings.Contains(c, want) {
			t.Errorf("the question comment lacks %q:\n%s", want, c)
		}
	}
}

// TestTheFailureCommentAndTheParkedSummaryNameTheForgePath_TS_19_33 verifies TS-19-33.
// Verifies: 19-REQ-6.2
func TestTheFailureCommentAndTheParkedSummaryNameTheForgePath_TS_19_33(t *testing.T) {
	failed := toolio.Envelope{Status: toolio.StatusFailed, Error: &toolio.ErrorInfo{Category: "sandbox", Message: "gone"}}
	for _, kind := range []jobs.Kind{jobs.KindFix, jobs.KindTriage} {
		a, ref := composer(t, kind, true, jobs.Source{Kind: "api", By: "u"})
		c := a.compose(commentFailure, ref, failed, loopResult{})
		if want := fmt.Sprintf(retryPath, "af:"+string(kind)); !strings.Contains(c, want) || !strings.Contains(c, "POST /api/v1/jobs/job_1/retry") {
			t.Errorf("the failure comment of a %s lacks %q or the retry route:\n%s", kind, want, c)
		}
	}
	a, ref := composer(t, jobs.KindFix, true, jobs.Source{Kind: "api", By: "u"})
	c := a.compose(commentFixSummary, ref, toolio.Envelope{Status: toolio.StatusDone}, parkedResult())
	for _, want := range []string{fmt.Sprintf(retryPath, "af:fix"), "af jobs retry job_1", "POST /api/v1/jobs/job_1/retry"} {
		if !strings.Contains(c, want) {
			t.Errorf("the parked summary lacks %q:\n%s", want, c)
		}
	}
}

// TestWithTheLabelTriggersOffNoCommentNamesTheForgePath_TS_19_34 verifies TS-19-34.
// Verifies: 19-REQ-6.3
func TestWithTheLabelTriggersOffNoCommentNamesTheForgePath_TS_19_34(t *testing.T) {
	s, _ := resolveSettings(jobs.KindFix, map[string]string{"AF_POLL_INTERVAL": "0s"})
	a, ref := composer(t, jobs.KindFix, s.TriggersOn, jobs.Source{Kind: "api", By: "u"})
	comments := map[string]string{
		"af jobs answer job_1":          a.compose(commentQuestion, ref, toolio.Envelope{Status: toolio.StatusNeedsHuman, NeedsHuman: &toolio.NeedsHuman{Question: "Q?"}}, loopResult{}),
		"POST /api/v1/jobs/job_1/retry": a.compose(commentFailure, ref, toolio.Envelope{Status: toolio.StatusFailed, Error: &toolio.ErrorInfo{Category: "sandbox"}}, loopResult{}),
		"af jobs retry job_1":           a.compose(commentFixSummary, ref, toolio.Envelope{Status: toolio.StatusDone}, parkedResult()),
	}
	for old, c := range comments {
		if strings.Contains(c, "add the label") || !strings.Contains(c, old) {
			t.Errorf("with AF_POLL_INTERVAL=0s the comment names the forge path, or lacks %q:\n%s", old, c)
		}
	}
}
