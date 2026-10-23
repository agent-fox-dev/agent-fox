package main

import (
	"context"
	"log/slog"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

// The tests that do not test the poll do not want it to poll: a workspace that is subscribed before the service starts
// would otherwise be listed within 30 seconds, at a time the test does not choose.
func init() { pollFirstDelay = func() time.Duration { return time.Hour } }

// msgAt is where in the records the first record with the message is, or -1.
func msgAt(records []map[string]any, msg string) int {
	return slices.IndexFunc(records, func(r map[string]any) bool { return r["msg"] == msg })
}

// runService runs the service in the background and returns the records it writes, the function that stops it and
// the channel that gets its exit code.
func runService(t *testing.T, s *setup) (logs *syncBuffer, stop func(), done chan int) {
	t.Helper()
	logs = &syncBuffer{}
	stopCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	done = make(chan int, 1)
	go func() { done <- serve(stopCtx, s.Config, slog.New(slog.NewJSONHandler(logs, nil)), net.Listen) }()
	for deadline := time.Now().Add(20 * time.Second); msgAt(logs.records(t), "listening") < 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no listening record")
		}
	}
	return logs, cancel, done
}

// exitOf waits for the exit code of the service.
func exitOf(t *testing.T, done chan int) int {
	t.Helper()
	select {
	case code := <-done:
		return code
	case <-time.After(30 * time.Second):
		t.Fatal("serve did not return")
		return -1
	}
}

// TestThePollStartsAfterTheJobsLayerWithOneLoopForEachSubscribedWorkspace_TS_15_1 verifies the part of TS-15-1 that only the
// service can show: the poll starts after the jobs layer has started, with exactly one loop for each workspace that is
// subscribed, and it is wired in the production start.
// Verifies: 15-REQ-1.1
func TestThePollStartsAfterTheJobsLayerWithOneLoopForEachSubscribedWorkspace_TS_15_1(t *testing.T) {
	hub := fakehub.New(t)
	hub.AddUser("tok", "usr_1", []string{"widgets", "gadgets"})
	hub.AddWorkspace(t, fakehub.Workspace{Slug: "widgets", PAT: "pat-WIDGETS-0123456789"})
	hub.AddWorkspace(t, fakehub.Workspace{Slug: "gadgets", PAT: "pat-GADGETS-0123456789"})
	s := newSetup(t, hub.URL)
	s.subscribe(t, "widgets", "pat-WIDGETS-0123456789")
	s.subscribe(t, "gadgets", "pat-GADGETS-0123456789")

	logs, stop, done := runService(t, s)
	defer stop()

	records := logs.records(t)
	queue, poll := msgAt(records, "queue started"), msgAt(records, "the label poll started")
	if queue < 0 || poll < 0 || poll < queue {
		t.Errorf("the jobs layer started at record %d and the poll at record %d, want the poll after it", queue, poll)
	}
	loops := map[any]int{}
	for _, rec := range records {
		if rec["msg"] == "the poll started for a workspace" {
			loops[rec["workspace"]]++
		}
	}
	if loops["widgets"] != 1 || loops["gadgets"] != 1 || len(loops) != 2 {
		t.Errorf("loops by workspace %v, want exactly one for widgets and one for gadgets", loops)
	}
	stop()
	if code := exitOf(t, done); code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
}

// TestShutdownStopsThePollAndCancelsItsCycleBeforeTheJobsLayerStops_TS_15_7 verifies the part of TS-15-7 that only the
// service can show: a cycle that is in flight, held on hub, is cancelled when the service is told to stop, the poll's stop
// returns before the jobs layer's stop is called, and hub is asked nothing more.
// Verifies: 15-REQ-1.7
func TestShutdownStopsThePollAndCancelsItsCycleBeforeTheJobsLayerStops_TS_15_7(t *testing.T) {
	const pat = "pat-WIDGETS-0123456789"
	hub := fakehub.New(t)
	hub.AddUser("tok", "usr_1", []string{"widgets"})
	hub.AddWorkspace(t, fakehub.Workspace{Slug: "widgets", PAT: pat})
	hub.Delay("/api/v1/workspaces/widgets/issues", time.Hour)
	s := newSetup(t, hub.URL)
	s.subscribe(t, "widgets", pat)
	previous := pollFirstDelay
	pollFirstDelay = func() time.Duration { return 0 }
	t.Cleanup(func() { pollFirstDelay = previous })

	logs, stop, done := runService(t, s)
	defer stop()
	for deadline := time.Now().Add(20 * time.Second); len(hub.RequestsTo("GET", "/api/v1/workspaces/widgets/issues")) == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the cycle never listed the issues")
		}
	}

	began := time.Now()
	stop()
	if code := exitOf(t, done); code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
	if took := time.Since(began); took > 10*time.Second {
		t.Errorf("the service took %s to stop with a cycle held on hub, want the cycle to be cancelled", took)
	}
	records := logs.records(t)
	pollStopped, queueStopped := msgAt(records, "the label poll stopped"), msgAt(records, "queue stopped")
	if pollStopped < 0 || queueStopped < 0 || queueStopped < pollStopped {
		t.Errorf("the poll stopped at record %d and the jobs layer at record %d, want the poll first", pollStopped, queueStopped)
	}
	n := hub.Total()
	time.Sleep(200 * time.Millisecond)
	if hub.Total() != n {
		t.Errorf("hub had %d requests after the service stopped", hub.Total()-n)
	}
}
