package reconcile_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

func TestPass_ReleasesTheOrphanAndLogsTheRelease(t *testing.T) {
	for name, status := range map[string]int{"hub releases it": 0, "hub does not find it": 404, "hub fails, and the next pass releases it": 500} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			orphan := w.lease(acme, "job_1")
			w.hub.Inject("DELETE", sandboxes+"/"+orphan, fakehub.Fault{Status: status, Once: true})
			for range 2 {
				w.rec.Pass(t.Context())
			}
			released(t, w, orphan)
			if warns := w.logs.Find("WARN", orphan); (len(warns) == 1) != (status == 500) {
				t.Errorf("warnings %v", warns)
			}
			infos := w.logs.Find("INFO", "released")
			if len(infos) == 0 || infos[0]["workspace"] != acme || infos[0]["sandbox_id"] != orphan || infos[0]["job"] != "job_1" || infos[0]["attempt"] != float64(1) {
				t.Errorf("the release is logged as %v, want the workspace, the sandbox, the job and the attempt", infos)
			}
		})
	}
}

func TestPass_KeepsEveryLeaseThatIsNotAnOrphan(t *testing.T) {
	for name, labels := range map[string]map[string]string{
		"no labels":                           nil,
		"labels of someone else":              {"owner": "someone else"},
		"a job the database does not have":    {"job": "job_unknown", "attempt": "1"},
		"an attempt in a runner":              {"job": "job_1", "attempt": "2"},
		"no attempt":                          {"job": "job_1"},
		"an attempt that is not a number":     {"job": "job_1", "attempt": "next"},
		"attempt 0":                           {"job": "job_1", "attempt": "0"},
		"a job the database cannot answer of": {"job": "job_err", "attempt": "1"},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.hub.SeedLease(acme, labels, "fix")
			w.rec.Pass(t.Context())
			released(t, w)
		})
	}
}

func TestPass_AFailingWorkspaceIsLoggedWithoutItsTokenAndTheNextPassCatchesUp(t *testing.T) {
	list := func(f fakehub.Fault) func(*world) { return func(w *world) { w.hub.Inject("GET", sandboxes, f) } }
	for name, spoil := range map[string]func(*world){
		"an unreadable token":  func(w *world) { w.subs.tokenErr[acme] = store.ErrUnreadable },
		"hub answers 500":      list(fakehub.Fault{Status: 500}),
		"hub echoes the token": list(fakehub.Fault{Status: 500, Body: `{"error":{"code":"internal","message":"cannot serve ` + patAcme + `"}}`}),
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			orphanA, orphanB := w.lease(acme, "job_1"), w.lease(beta, "job_1")
			spoil(w)
			w.rec.Pass(t.Context())
			released(t, w, orphanB)
			if warns := w.logs.Find("WARN", "next pass"); len(warns) != 1 || warns[0]["workspace"] != acme {
				t.Errorf("warnings %v, want one naming %s", warns, acme)
			}
			if s := w.logs.String(); strings.Contains(s, patAcme) || strings.Contains(s, patBeta) {
				t.Errorf("the log holds a token: %s", s)
			}
			w.hub.Clear()
			delete(w.subs.tokenErr, acme)
			w.rec.Pass(t.Context())
			released(t, w, orphanB, orphanA)
		})
	}
}

func TestPass_ThatIsStoppedAsksNothingAndWarnsOfNothing(t *testing.T) {
	w := newWorld(t)
	stopped, stop := context.WithCancel(t.Context())
	stop()
	w.rec.Pass(stopped)
	w.subs.listErr = errors.New("boom")
	w.rec.Pass(stopped)
	if n := w.hub.Total(); n != 0 || len(w.logs.Records()) != 0 {
		t.Errorf("stopped passes made %d requests and logged %s", n, w.logs)
	}
	w.rec.Pass(t.Context())
	if warns := w.logs.Find("WARN", "cannot be listed"); len(warns) != 1 || warns[0]["error"] != "boom" {
		t.Errorf("warnings %v, want one that the subscriptions cannot be listed", warns)
	}
}

func TestPass_WhileOneRunsReturnsWithoutASecond(t *testing.T) {
	w := newWorld(t)
	g := w.gate()
	rec := w.reconciler(g, 0)
	first, second := make(chan struct{}), make(chan struct{})
	go func() { defer close(first); rec.Pass(t.Context()) }()
	within(t, g.entered, "a listing")
	go func() { defer close(second); rec.Pass(t.Context()) }()
	within(t, second, "a pass made while another runs to return at once") // it would wait at the gate had it listed
	close(g.proceed)
	within(t, first, "the first pass to return")
}

func TestRun_PassesAtOnceThenOneAfterAnotherUntilStopped(t *testing.T) {
	for passes, interval := range map[int]time.Duration{1: time.Hour, 3: time.Millisecond} {
		w := newWorld(t)
		g := w.gate()
		run, stop := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { defer close(done); w.reconciler(g, interval).Run(run) }()
		for range 2*passes - 1 { // each pass lists acme and beta; the first is made at once, whatever the interval
			within(t, g.entered, "a listing")
			g.proceed <- struct{}{}
		}
		within(t, g.entered, "a listing") // the last listing waits for hub, and is stopped
		stop()
		within(t, done, "Run to return")
		if warns := w.logs.Find("WARN", ""); len(warns) != 0 {
			t.Errorf("the stopped pass warned: %v", warns)
		}
	}
}
