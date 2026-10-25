package reconcile_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/reconcile"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

const (
	acme, beta       = "acme", "beta"
	patAcme, patBeta = "pat-ACME-0123456789", "pat-BETA-9876543210"
	sandboxes        = "/api/v1/workspaces/acme/sandboxes"
)

// fakeJobs knows job_1, whose attempt 2 is in a runner, and cannot say whether it knows job_err.
type fakeJobs struct{}

func (fakeJobs) Known(_ context.Context, id string) (bool, error) {
	return id == "job_1", map[bool]error{true: errors.New("boom")}[id == "job_err"]
}
func (fakeJobs) Running(id string, n int) bool { return id == "job_1" && n == 2 }

type fakeSubs struct {
	tokenErr map[string]error
	listErr  error
}

func (f *fakeSubs) List(context.Context) ([]store.Subscription, error) {
	return []store.Subscription{{Slug: acme}, {Slug: beta}}, f.listErr
}

func (f *fakeSubs) Token(_ context.Context, slug string) (string, error) {
	return map[string]string{acme: patAcme, beta: patBeta}[slug], f.tokenErr[slug]
}

// world is a fake hub with acme and beta, and a reconciler over it.
type world struct {
	hub    *fakehub.Hub
	client *hub.Client
	subs   *fakeSubs
	logs   *testutil.Log
	rec    *reconcile.Reconciler
}

func newWorld(t *testing.T) *world {
	w := &world{hub: fakehub.New(t), logs: &testutil.Log{}, subs: &fakeSubs{tokenErr: map[string]error{}}}
	w.hub.AddWorkspace(t, fakehub.Workspace{Slug: acme, PAT: patAcme})
	w.hub.AddWorkspace(t, fakehub.Workspace{Slug: beta, PAT: patBeta})
	w.client = hub.New(w.hub.URL, "1")
	w.rec = w.reconciler(w.client, 0)
	return w
}

func (w *world) reconciler(h reconcile.Hub, interval time.Duration) *reconcile.Reconciler {
	return reconcile.New(reconcile.Config{Subscriptions: w.subs, Hub: h, Jobs: fakeJobs{}, Logger: w.logs.Logger(), Interval: interval})
}

func (w *world) lease(slug, job string) string {
	return w.hub.SeedLease(slug, map[string]string{"job": job, "attempt": "1"}, "fix").ID
}

func released(t *testing.T, w *world, ids ...string) {
	t.Helper()
	if got := w.hub.Deleted(); !slices.Equal(got, ids) {
		t.Errorf("the hub released %v, want %v", got, ids)
	}
}

// gate is a hub whose listings wait until the test lets them go.
type gate struct {
	*hub.Client
	entered, proceed chan struct{}
}

func (w *world) gate() *gate {
	return &gate{Client: w.client, entered: make(chan struct{}, 16), proceed: make(chan struct{})}
}

func (g *gate) ListSandboxes(c context.Context, pat, slug string) ([]hub.LeaseInfo, error) {
	g.entered <- struct{}{}
	select {
	case <-g.proceed:
		return g.Client.ListSandboxes(c, pat, slug)
	case <-c.Done():
		return nil, c.Err()
	}
}

// within waits for a value of ch, and fails the test naming what it waited for when none comes.
func within[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(testutil.WaitTimeout):
		t.Fatal("gave up waiting for " + what)
	}
}
