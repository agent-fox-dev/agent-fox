package jobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestCreate_StoresAQueuedJobAndQueuesItsAttemptOnceItIsCommitted(t *testing.T) {
	var mu sync.Mutex
	var queued []string
	var e *env
	e = newEnv(t, func(c *jobs.Config) {
		c.Enqueue = func(_ context.Context, id string, n int) error {
			rows := testutil.Count(t, e.DB, "af_jobs", "id = ?", id) + testutil.Count(t, e.DB, "af_attempts", "job_id = ?", id) +
				testutil.Count(t, e.DB, "af_events", "job_id = ?", id)
			mu.Lock()
			defer mu.Unlock()
			queued = append(queued, fmt.Sprintf("%s:%d:%d", id, n, rows))
			return nil
		}
	})
	doc, err := e.Svc.Create(ctx, jobs.CreateRequest{
		Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage(`9`), Context: []string{"from the poll"}},
		Source: jobs.Source{Kind: "label", By: "poll"}, RequestLabel: "af:fix",
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.State != jobs.StateQueued || doc.Kind != jobs.KindFix || doc.Workspace != "widgets" || *doc.Issue != 9 || doc.AttemptCount != 1 || doc.CurrentAttempt != 1 {
		t.Errorf("job = %+v, want a queued fix of widgets#9 with attempt 1", doc)
	}
	if doc.Source != (jobs.Source{Kind: "label", By: "poll"}) || doc.RequestLabel == nil || *doc.RequestLabel != "af:fix" || doc.Attempts != nil {
		t.Errorf("source %+v, request label %v, attempts %v, want the poll's, af:fix and none listed", doc.Source, doc.RequestLabel, doc.Attempts)
	}
	mu.Lock()
	if want := doc.ID + ":1:3"; len(queued) != 1 || queued[0] != want {
		t.Errorf("queued %v, want %s: attempt 1, asked for once its three rows were there", queued, want)
	}
	mu.Unlock()
	api := e.create("widgets", 10, "")
	if api.RequestLabel != nil || api.Source != (jobs.Source{Kind: "api", By: "user-1"}) {
		t.Errorf("a job of the API: source %+v, request label %v", api.Source, api.RequestLabel)
	}
}

func TestCreate_RefusesARequestTheWorkspaceOrTheIssueCannotTake(t *testing.T) {
	e := newEnv(t)
	open := e.create("widgets", 42, "")
	e.Subs.set("locked", store.TokenUnreadable)
	e.Subs.set("leaving", store.TokenOK)
	clear := e.Svc.MarkUnsubscribing("leaving")
	again := e.Svc.MarkUnsubscribing("leaving") // the marks count
	clear()
	clear() // clearing twice clears once
	defer again()
	req := func(workspace string, issue json.RawMessage) jobs.CreateRequest {
		return jobs.CreateRequest{Workspace: workspace, Kind: jobs.KindFix, Body: jobs.Body{Issue: issue}, Source: jobs.Source{Kind: "api", By: "user-1"}}
	}
	for name, tc := range map[string]struct {
		req  jobs.CreateRequest
		want error
	}{
		"a workspace that is not subscribed": {req("nowhere", json.RawMessage(`1`)), jobs.ErrNotSubscribed},
		"a workspace being unsubscribed":     {req("leaving", json.RawMessage(`1`)), jobs.ErrNotSubscribed},
		"a token that does not open":         {req("locked", json.RawMessage(`1`)), jobs.ErrTokenUnreadable},
		"the issue of an open fix":           {req("widgets", json.RawMessage(`42`)), jobs.ErrDuplicate},
		"the issue of an open fix, by URL":   {req("widgets", json.RawMessage(`"https://github.example.com/acme/widgets/issues/42"`)), jobs.ErrDuplicate},
		"the issue of an open fix, by path":  {req("widgets", json.RawMessage(`"acme/widgets#42"`)), jobs.ErrDuplicate},
	} {
		_, err := e.Svc.Create(ctx, tc.req)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
		var dup *jobs.DuplicateError
		if errors.As(err, &dup) && (dup.JobID != open.ID || err.Error() != "an open job of this kind exists for the issue: "+open.ID) {
			t.Errorf("%s: %v, want it to name %s", name, err, open.ID)
		}
	}
	var invalid *jobs.ValidationError
	if _, err := e.Svc.Create(ctx, req("widgets", json.RawMessage(`"nope"`))); !errors.As(err, &invalid) || invalid.Field != "issue" {
		t.Errorf("an issue that is not one: %v, want a *ValidationError of issue", err)
	}
	for name, r := range map[string]jobs.CreateRequest{
		"an unknown kind":           {Workspace: "widgets", Kind: "rebuild", Body: jobs.Body{Issue: json.RawMessage(`1`)}, Source: jobs.Source{Kind: "api", By: "u"}},
		"an unknown source":         {Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage(`1`)}, Source: jobs.Source{Kind: "poll", By: "u"}},
		"a source with nobody":      {Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage(`1`)}, Source: jobs.Source{Kind: "api", By: " "}},
		"a request label from api ": {Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage(`1`)}, Source: jobs.Source{Kind: "api", By: "u"}, RequestLabel: "af:fix"},
	} {
		if _, err := e.Svc.Create(ctx, r); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	e.Subs.mu.Lock()
	e.Subs.fail = errors.New("the state file is locked")
	e.Subs.mu.Unlock()
	if _, err := e.Svc.Create(ctx, req("widgets", json.RawMessage(`5`))); err == nil || errors.Is(err, jobs.ErrNotSubscribed) {
		t.Errorf("a subscription that cannot be read: %v, want that error", err)
	}
	// What is not refused: the other kind for the issue, another issue.
	e.Subs.mu.Lock()
	e.Subs.fail = nil
	e.Subs.mu.Unlock()
	triage := req("widgets", json.RawMessage(`42`))
	triage.Kind = jobs.KindTriage
	if _, err := e.Svc.Create(ctx, triage); err != nil {
		t.Errorf("a triage beside the fix: %v", err)
	}
	e.create("widgets", 43, "")
	if n := testutil.Count(t, e.DB, "af_jobs", ""); n != 3 {
		t.Errorf("%d jobs, want the three that were accepted", n)
	}
}

func TestCreate_AFailedInsertStoresNothingAndQueuesNothing(t *testing.T) {
	var fail string
	e := newEnv(t, withFault(func(op string) error {
		if op == fail {
			return errors.New("injected " + op)
		}
		return nil
	}))
	for _, op := range []string{jobs.OpInsertJob, jobs.OpInsertAttempt, jobs.OpInsertEvent} {
		fail = op
		if _, err := e.Svc.Create(ctx, jobs.CreateRequest{Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage(`1`)},
			Source: jobs.Source{Kind: "api", By: "u"}}); err == nil {
			t.Errorf("%s failed and Create succeeded", op)
		}
	}
	for _, table := range []string{"af_jobs", "af_attempts", "af_events", "jobs"} {
		if n := testutil.Count(t, e.DB, table, ""); n != 0 {
			t.Errorf("%d rows in %s", n, table)
		}
	}
}

func TestCreate_ALoserToAJobThatEndsBeforeItIsLookedUpMakesItsOwn(t *testing.T) {
	var e *env
	var once sync.Once
	var winner jobs.Doc
	e = newEnv(t, withFault(func(op string) error {
		if op == jobs.OpDuplicate {
			once.Do(func() { // the job that won the issue ends after the insert was refused, before it is looked up
				if _, err := e.Svc.Cancel(ctx, winner.ID); err != nil {
					t.Errorf("cancel: %v", err)
				}
			})
		}
		return nil
	}))
	winner = e.create("widgets", 42, "af:fix")
	loser := e.create("widgets", 42, "af:fix")
	if loser.ID == winner.ID || loser.State != jobs.StateQueued {
		t.Errorf("the second creator got %+v, want a job of its own", loser)
	}
	if n := testutil.Count(t, e.DB, "af_jobs", "issue = 42 AND state = 'queued'"); n != 1 {
		t.Errorf("%d open jobs for the issue, want one", n)
	}
}

func TestOpenJobAndLatestJob_FindAJobOfAKindForAnIssue(t *testing.T) {
	e := newEnv(t)
	first := e.create("widgets", 42, "")
	if doc, err := e.Svc.OpenJob(ctx, "widgets", jobs.KindFix, 42); err != nil || doc.ID != first.ID {
		t.Errorf("OpenJob = %s, %v, want %s", doc.ID, err, first.ID)
	}
	for _, kind := range []jobs.Kind{jobs.KindTriage, jobs.KindFix} {
		if _, err := e.Svc.OpenJob(ctx, "widgets", kind, 99); !errors.Is(err, jobs.ErrNotFound) {
			t.Errorf("OpenJob of %s#99: %v, want ErrNotFound", kind, err)
		}
	}
	if _, err := e.Svc.Cancel(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Svc.OpenJob(ctx, "widgets", jobs.KindFix, 42); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("OpenJob of a cancelled job: %v, want ErrNotFound", err)
	}
	if doc, err := e.Svc.LatestJob(ctx, "widgets", jobs.KindFix, 42); err != nil || doc.ID != first.ID || doc.State != jobs.StateCancelled {
		t.Errorf("LatestJob = %s %s, %v, want the cancelled %s", doc.ID, doc.State, err, first.ID)
	}
	e.Clock.Advance(1)
	second := e.create("widgets", 42, "")
	if doc, err := e.Svc.LatestJob(ctx, "widgets", jobs.KindFix, 42); err != nil || doc.ID != second.ID {
		t.Errorf("LatestJob = %s, %v, want the newer %s", doc.ID, err, second.ID)
	}
	if _, err := e.Svc.LatestJob(ctx, "widgets", jobs.KindTriage, 42); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("LatestJob of no job: %v, want ErrNotFound", err)
	}
}
