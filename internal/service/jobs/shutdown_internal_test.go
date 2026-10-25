package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

// funcRunner is a Runner that is a function.
type funcRunner func(ctx context.Context, req Request) (Result, error)

func (f funcRunner) Run(ctx context.Context, req Request) (Result, error) { return f(ctx, req) }

// anySubscription is subscriptions in which every workspace is subscribed.
type anySubscription struct{}

func (anySubscription) Get(_ context.Context, slug string) (store.Subscription, error) {
	return store.Subscription{Slug: slug, TokenState: store.TokenOK}, nil
}

func TestStopping_GrantsNoSlotAndCallsNoRunnerForAnAttemptClaimedAsItStops(t *testing.T) {
	calls := 0
	svc, err := New(Config{DB: openDB(t), Subscriptions: anySubscription{}, Workers: 1, AttemptsDir: filepath.Join(t.TempDir(), "attempts"),
		Runner: funcRunner(func(context.Context, Request) (Result, error) { calls++; return Result{}, nil })})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	if !svc.acquire() {
		t.Fatal("a running service granted no slot")
	}
	svc.release()
	id := mustCreate(t, svc.store, newJob(1))
	if _, err := svc.store.Claim(ctx, id, 1); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	for range 100 { // a select between a free slot and the stop would choose either
		if svc.acquire() {
			t.Fatal("a stopping service granted the slot that was free")
		}
	}
	_, out, cause, _, _, err := svc.invoke(ctx, attemptPayload{JobID: id, Attempt: 1})
	if err != nil || !errors.Is(cause, ErrShutdown) || !errors.Is(out.err, ErrShutdown) || calls != 0 {
		t.Errorf("invoke after the stop: cause %v, out %v, err %v, %d calls, want the shutdown and no call", cause, out.err, err, calls)
	}
}
