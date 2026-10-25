package reconcile_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/service/reconcile"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// TestPass_OverTheRealJobsLayerReleasesTheLeasesOfAttemptsNoRunnerHolds: with the one runner slot held, the attempts
// waiting behind it are known and in no runner, so their leases go; the held attempt's lease and a stranger's stay.
func TestPass_OverTheRealJobsLayerReleasesTheLeasesOfAttemptsNoRunnerHolds(t *testing.T) {
	w := newWorld(t)
	key, _ := secrets.NewKey(testutil.Key())
	dir := t.TempDir()
	st, err := store.Open(t.Context(), filepath.Join(dir, "agent-fox.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	for slug, pat := range map[string]string{acme: patAcme, beta: patBeta} {
		_, _, _ = st.Upsert(t.Context(), slug, pat, "usr_1", "usr_1", time.Now())
	}
	runner := fakerunner.New().Then(fakerunner.Block(fakerunner.Step{}))
	svc, err := jobs.New(jobs.Config{
		DB: st.DB(), Subscriptions: st, Logger: w.logs.Logger(), Clock: fakeclock.New(time.Now()), Workers: 1,
		AttemptsDir: filepath.Join(dir, "attempts"), Runner: runner, PollInterval: 5 * time.Millisecond, RetryDelay: 20 * time.Millisecond,
	})
	if err == nil {
		err = svc.Start()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { runner.ReleaseAll(); _ = svc.Close() })
	create := func(workspace string, issue int) string {
		body := jobs.Body{Issue: json.RawMessage(fmt.Sprint(issue))}
		doc, err := svc.Create(t.Context(), jobs.CreateRequest{Workspace: workspace, Kind: jobs.KindFix, Body: body, Source: jobs.Source{Kind: "api", By: "usr_1"}})
		if err != nil {
			t.Fatal(err)
		}
		return doc.ID
	}
	running := create(acme, 1)
	testutil.Eventually(t, "the first job in the runner", func() bool { return svc.Running(running, 1) && len(runner.Calls()) == 1 })
	behind, waiting := create(acme, 2), create(beta, 3)
	w.lease(acme, running)
	w.lease(acme, "job_00000000000000000000")
	orphanA, orphanB := w.lease(acme, behind), w.lease(beta, waiting)

	reconcile.New(reconcile.Config{Subscriptions: st, Hub: w.client, Jobs: svc, Logger: w.logs.Logger()}).Pass(t.Context())

	released(t, w, orphanA, orphanB)
	if len(runner.Calls()) != 1 || !svc.Running(running, 1) {
		t.Errorf("%d runner calls; want the blocked attempt alone in the runner, as the pass found it", len(runner.Calls()))
	}
}
