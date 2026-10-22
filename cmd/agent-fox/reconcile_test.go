package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

// subscribe subscribes the workspace in the database of the setup, as the API does, before the service is started.
func (s *setup) subscribe(t *testing.T, slug, pat string) {
	t.Helper()
	key, err := secrets.LoadKey(s.Key)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, s.DB, key)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, _, err := st.Upsert(ctx, slug, "af:", pat, "usr_1", "usr_1", time.Now()); err != nil {
		t.Fatal(err)
	}
}

// TestTheServiceAcceptsRequestsWhileTheFirstReconciliationPassWaitsForHubAndStartsNoSecondPass_TS_14_50 verifies
// TS-14-50, the part of 14-REQ-8.1 that only the service can show: a pass is made when it starts, in a goroutine, so
// that the listener is bound and the requests are served while the pass waits for hub; the ticks of a short interval
// that come in the meantime start no other pass; and the service stops, with exit code 0, while the pass waits.
// Verifies: 14-REQ-8.1
func TestTheServiceAcceptsRequestsWhileTheFirstReconciliationPassWaitsForHubAndStartsNoSecondPass_TS_14_50(t *testing.T) {
	const pat = "pat-RECONCILE-0123456789"
	leases := "/api/v1/workspaces/widgets/sandboxes"
	hub := fakehub.New(t)
	hub.AddUser("tok", "usr_1", []string{"widgets"})
	hub.AddWorkspace(t, fakehub.Workspace{Slug: "widgets", PAT: pat})
	hub.Delay(leases, 400*time.Millisecond)
	s := newSetup(t, hub.URL)
	s.write(t)
	s.subscribe(t, "widgets", pat)
	previous := reconcileInterval
	reconcileInterval = 10 * time.Millisecond
	t.Cleanup(func() { reconcileInterval = previous })

	logs := &syncBuffer{}
	stop, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- serve(stop, s.Config, slog.New(slog.NewJSONHandler(logs, nil)), net.Listen) }()

	var addr string
	for deadline := time.Now().Add(20 * time.Second); addr == ""; time.Sleep(10 * time.Millisecond) {
		for _, rec := range logs.records(t) {
			if rec["msg"] == "listening" {
				addr, _ = rec["addr"].(string)
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no listening record")
		}
	}
	for deadline := time.Now().Add(10 * time.Second); len(hub.RequestsTo("GET", leases)) == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the pass never asked hub for the sandboxes of the workspace")
		}
	}

	started := time.Now()
	req, _ := http.NewRequest("GET", "http://"+addr+"/api/v1/workspaces", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || time.Since(started) > time.Second {
		t.Errorf("the request got %d after %s, want 200 at once, while the pass waits for hub", resp.StatusCode, time.Since(started))
	}
	time.Sleep(200 * time.Millisecond) // twenty ticks of the interval, inside the first pass
	if n := len(hub.RequestsTo("GET", leases)); n != 1 {
		t.Errorf("hub was asked for the sandboxes %d times while the first pass waited, want once: a pass is not started over another", n)
	}
	for deadline := time.Now().Add(5 * time.Second); len(hub.RequestsTo("GET", leases)) < 2; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no second pass was made after the first: the interval was not the one the service was given")
		}
	}

	stopped := time.Now()
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code %d, want 0", code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("serve did not return")
	}
	if took := time.Since(stopped); took > 350*time.Millisecond {
		t.Errorf("the service took %s to stop, want the pass that waited for hub to be stopped with it", took)
	}
}
