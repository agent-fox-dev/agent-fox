package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// lateService is a service over a file with the runner and the clock, started, and the
// directory of its attempts.
func lateService(t *testing.T, runner Runner, clock Clock) (*Service, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "attempts")
	svc, err := New(Config{
		DB: openDB(t), Subscriptions: anySubscription{}, Logger: slog.New(slog.DiscardHandler), Clock: clock, Workers: 1, Runner: runner,
		AttemptsDir: dir, PollInterval: 5 * time.Millisecond, RetryDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close() })
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	return svc, dir
}

// lateSnapshot is what a report that comes late must not change: the document of the job, its
// events and the files of its attempts.
func lateSnapshot(t *testing.T, svc *Service, dir, id string) string {
	t.Helper()
	doc, err := svc.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(doc)
	out := string(raw)
	events, err := svc.store.EventsAfter(ctx, id, 0, 100000)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		out += fmt.Sprintf("|%d:%s:%s", ev.Seq, ev.Type, ev.Data)
	}
	for n := 1; n <= doc.AttemptCount; n++ {
		for _, name := range []string{"envelope.json", "report.md"} {
			data, _ := os.ReadFile(filepath.Join(dir, id, fmt.Sprint(n), name))
			out += fmt.Sprintf("|%s:%x", name, data)
		}
	}
	return out
}

// TestEventsHubReferencesAndAResultSentAfterTheAttemptEndedAreDropped_TS_12_33 verifies TS-12-33:
// a runner that reports after its attempt has ended, one whose attempt finished and has kept
// its recorder, and one that overran the cancel grace and returns a done envelope when it
// finally returns, change nothing: af_events has no new row, nothing is published to a
// subscriber of the job, the attempt, its files and the job are as they were, and the hub
// references of the job stay null.
// Verifies: 12-REQ-5.5
func TestEventsHubReferencesAndAResultSentAfterTheAttemptEndedAreDropped_TS_12_33(t *testing.T) {
	done, err := json.Marshal(toolio.Envelope{SchemaVersion: toolio.SchemaVersion, Tool: "fix", Status: toolio.StatusDone, Summary: "done"})
	if err != nil {
		t.Fatal(err)
	}
	newJob := func(t *testing.T, svc *Service) string {
		t.Helper()
		doc, err := svc.Create(ctx, CreateRequest{
			Workspace: "widgets", Kind: KindFix, Body: Body{Issue: json.RawMessage("1")}, Source: Source{Kind: "api", By: "user-1"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return doc.ID
	}
	// dropped checks that a late report changes nothing.
	dropped := func(t *testing.T, svc *Service, dir, id string, report func()) {
		t.Helper()
		sub := svc.broker.Subscribe(id)
		defer sub.Cancel()
		before := lateSnapshot(t, svc, dir, id)
		report()
		if after := lateSnapshot(t, svc, dir, id); after != before {
			t.Errorf("a report after the end of the attempt changed the job:\nbefore %s\nafter  %s", before, after)
		}
		select {
		case ev := <-sub.C:
			t.Errorf("an event was published after the end of the attempt: %+v", ev)
		default:
		}
		if doc, err := svc.Get(ctx, id); err != nil || doc.Hub != nil {
			t.Errorf("hub = %+v (%v) after a late report, want null", doc.Hub, err)
		}
	}

	t.Run("a finished attempt whose recorder handle was kept", func(t *testing.T) {
		kept := make(chan *Recorder, 1)
		svc, dir := lateService(t, funcRunner(func(_ context.Context, req Request) (Result, error) {
			req.Recorder.Emit("step", map[string]int{"i": 1})
			kept <- req.Recorder
			return Result{Envelope: done, Report: "# Done\n"}, nil
		}), nil)
		id := newJob(t, svc)
		recorder := <-kept
		restOf(t, svc, id)
		dropped(t, svc, dir, id, func() {
			recorder.Emit("late", map[string]string{"after": "the end"})
			recorder.Emit("job_state", "a type that is the layer's own")
			recorder.Hub("session", "run", "sandbox")
		})
	})

	t.Run("a runner that overran the grace and returned a done envelope", func(t *testing.T) {
		clk := fakeclock.New(time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC))
		hold, returned := make(chan struct{}), make(chan struct{})
		kept := make(chan *Recorder, 1)
		svc, dir := lateService(t, funcRunner(func(_ context.Context, req Request) (Result, error) {
			defer close(returned)
			kept <- req.Recorder
			<-hold // it does not look at its context
			req.Recorder.Emit("late", 1)
			req.Recorder.Hub("session", "run", "sandbox")
			return Result{Envelope: done, Report: "# Late\n", LabelsSet: []string{"af:late"}}, nil
		}), clk)
		id := newJob(t, svc)
		recorder := <-kept
		if _, err := svc.Cancel(ctx, id); err != nil {
			t.Fatal(err)
		}
		if !clk.WaitForWaits(1, 10*time.Second) {
			t.Fatal("the cancel started no grace")
		}
		clk.Advance(61 * time.Second)
		restOf(t, svc, id) // the attempt is recorded cancelled without its runner

		dropped(t, svc, dir, id, func() {
			close(hold)
			<-returned
			deadline := time.Now().Add(10 * time.Second)
			for svc.runnerInProgress(id) {
				if time.Now().After(deadline) {
					t.Fatal("the late runner was never forgotten")
				}
				time.Sleep(time.Millisecond)
			}
			recorder.Emit("later still", 2)
		})
		if doc, _ := svc.Get(ctx, id); doc.State != StateCancelled || doc.Labels != nil && len(doc.Labels.Set) > 0 {
			t.Errorf("the job is %s with labels %+v, want cancelled and none of the labels the late runner returned", doc.State, doc.Labels)
		}
	})
}
