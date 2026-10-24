package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// snapshot is what one statement reads of the linked fix job of the triage job: how many jobs name it as their parent,
// and the state of its follow-up.
func snapshot(t *testing.T, svc *Service, triage string) (jobs int, state string) {
	t.Helper()
	err := svc.store.db.QueryRowContext(context.Background(),
		"SELECT (SELECT COUNT(*) FROM af_jobs WHERE parent_job_id = ?), (SELECT state FROM af_followups WHERE triage_job_id = ?)",
		triage, triage).Scan(&jobs, &state)
	if err != nil {
		t.Fatal(err)
	}
	return jobs, state
}

// TestAFixJobFromAFollowUpAndTheFollowUpsMadeStateAreNeverSeenApart_TS_19_49 verifies TS-19-49, a property: a failure at any
// step of making the linked fix job leaves the follow-up pending and no job, and a reader that looks while it is made sees
// both or neither.
// Verifies: 19-REQ-8.6
func TestAFixJobFromAFollowUpAndTheFollowUpsMadeStateAreNeverSeenApart_TS_19_49(t *testing.T) {
	ctx := context.Background()
	svc := internalService(t, funcRunner(func(ctx context.Context, req Request) (Result, error) { <-ctx.Done(); return Result{}, ctx.Err() }))
	triage, err := svc.Create(ctx, CreateRequest{
		Workspace: "widgets", Kind: KindTriage, Body: Body{Issue: json.RawMessage("42")},
		Source: Source{Kind: "label", By: "label af:triage"}, RequestLabel: "af:triage",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RecordFollowUp(ctx, triage.ID, 42, "af:fix"); err != nil {
		t.Fatal(err)
	}
	req := CreateRequest{
		Workspace: "widgets", Kind: KindFix, Body: Body{Issue: json.RawMessage("42")},
		Source: Source{Kind: "triage", By: triage.ID}, ParentJobID: triage.ID, RequestLabel: "af:fix",
	}

	injected := errors.New("injected")
	for _, step := range []string{OpInsertJob, OpInsertAttempt, OpInsertEvent, OpFollowUp} {
		svc.store.SetFault(func(op string) error {
			if op == step {
				return injected
			}
			return nil
		})
		if _, err := svc.CreateLinked(ctx, triage.ID, req); !errors.Is(err, injected) {
			t.Errorf("a failure at %s: %v, want it returned", step, err)
		}
		if jobs, state := snapshot(t, svc, triage.ID); jobs != 0 || state != "pending" {
			t.Errorf("after a failure at %s: %d jobs and the follow-up %s, want none and pending", step, jobs, state)
		}
	}
	svc.store.SetFault(nil)

	var apart atomic.Int32
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if jobs, state := snapshot(t, svc, triage.ID); (jobs == 1) != (state == "made") {
					apart.Add(1)
				}
			}
		}()
	}
	fix, err := svc.CreateLinked(ctx, triage.ID, req)
	close(stop)
	readers.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if n := apart.Load(); n != 0 {
		t.Errorf("readers saw the job and the follow-up apart %d times", n)
	}
	if jobs, state := snapshot(t, svc, triage.ID); jobs != 1 || state != "made" {
		t.Errorf("after the make: %d jobs and the follow-up %s, want one and made", jobs, state)
	}
	if doc, _ := svc.Get(ctx, triage.ID); doc.FollowUp == nil || doc.FollowUp.JobID == nil || *doc.FollowUp.JobID != fix.ID {
		t.Errorf("the follow-up is %+v, want made with %s", doc.FollowUp, fix.ID)
	}
	// A follow-up that is no longer pending makes no job.
	if _, err := svc.CreateLinked(ctx, triage.ID, req); err == nil {
		t.Error("a second make from a made follow-up was not refused")
	}
}
