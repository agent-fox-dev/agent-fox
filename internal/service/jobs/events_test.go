package jobs

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// eventEnv is a store whose events go to a broker, a job to put them in, and a log.
type eventEnv struct {
	st  *Store
	br  *Broker
	log *testutil.Log
	id  string
}

func newEventEnv(t *testing.T) *eventEnv {
	t.Helper()
	st, _ := newStore(t)
	e := &eventEnv{st: st, br: NewBroker(), log: &testutil.Log{}}
	st.SetPublisher(e.br.Publish)
	t.Cleanup(e.br.Close)
	e.id = mustCreate(t, st, newJob(1))
	return e
}

func (e *eventEnv) recorder(attempt int) *Recorder {
	return newRecorder(e.st, e.log.Logger(), e.id, attempt)
}

// stored are the stored events of the job.
func (e *eventEnv) stored(t *testing.T) []Event {
	t.Helper()
	evs, err := e.st.EventsAfter(ctx, e.id, 0, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func (e *eventEnv) last(t *testing.T) Event {
	t.Helper()
	evs := e.stored(t)
	return evs[len(evs)-1]
}

// receive is the next event of the subscription; it fails the test when there is none waiting.
func receive(t *testing.T, sub *Subscription) Event {
	t.Helper()
	select {
	case ev, ok := <-sub.C:
		if !ok {
			t.Fatal("the subscription is closed")
		}
		return ev
	default:
		t.Fatal("no event was published")
		return Event{}
	}
}

func TestRecorderEmit_StoresTheTypeAndTheDataAsTheRuleHasThem(t *testing.T) {
	e := newEventEnv(t)
	sub := e.br.Subscribe(e.id)
	rec := e.recorder(1)
	given := map[string]any{"a": 1, "html": "<&>"}
	const data = `{"a":1,"html":"<&>"}`
	wrapped := func(typ, data string) string {
		return `{"type":` + string(testutil.JSON(t, typ)) + `,"data":` + data + `}`
	}
	cases := []struct {
		name, typ string
		data      any
		wantType  string
		wantData  string
	}{
		{"a word", "step", given, "step", data},
		{"letters, digits and underscores", "x1_y2", given, "x1_y2", data},
		{"64 KiB of JSON", "step", strings.Repeat("a", 64<<10-2), "step", `"` + strings.Repeat("a", 64<<10-2) + `"`},
		{"a byte more", "step", strings.Repeat("a", 64<<10-1), "step", `{"bytes":65537,"truncated":true}`},
		{"data that cannot be encoded", "step", make(chan int), "step", `{"error":"json: unsupported type: chan int"}`},
		{"an upper-case type", "Step", given, "invalid_event_type", wrapped("Step", data)},
		{"an empty type", "", given, "invalid_event_type", wrapped("", data)},
		{"a type with a dash", "a-b", given, "invalid_event_type", wrapped("a-b", data)},
		{"a type with a newline", "tool\ncall", given, "invalid_event_type", wrapped("tool\ncall", data)},
		{"a type that begins with a digit", "1step", given, "invalid_event_type", wrapped("1step", data)},
		{"job_state, the layer's own", EventJobState, given, "invalid_event_type", wrapped(EventJobState, data)},
		{"attempt_start, the layer's own", EventAttemptStart, given, "invalid_event_type", wrapped(EventAttemptStart, data)},
		{"attempt_end, the layer's own", EventAttemptEnd, given, "invalid_event_type", wrapped(EventAttemptEnd, data)},
		{"a wrong type and large data", "Bad", strings.Repeat("b", 70<<10), "invalid_event_type", wrapped("Bad", `{"bytes":71682,"truncated":true}`)},
	}
	for i, tc := range cases {
		rec.Emit(tc.typ, tc.data)
		ev := e.last(t)
		if ev.Seq != int64(i+2) || ev.Type != tc.wantType || string(ev.Data) != tc.wantData || ev.Attempt != 1 || ev.JobID != e.id {
			t.Errorf("%s: stored #%d %s %.100s, want #%d %s %.100s", tc.name, ev.Seq, ev.Type, ev.Data, i+2, tc.wantType, tc.wantData)
		}
		if got := receive(t, sub); got.Seq != ev.Seq || string(got.Data) != string(ev.Data) {
			t.Errorf("%s: published %+v, want the stored event", tc.name, got)
		}
	}
	if recs := e.log.Records(); len(recs) != 0 {
		t.Errorf("events that were replaced were logged: %v", recs)
	}
}

func TestRecorderEmit_AnEventThatCannotBeStoredIsWarnedOfAndDroppedWithoutAGap(t *testing.T) {
	e := newEventEnv(t)
	sub := e.br.Subscribe(e.id)
	rec := e.recorder(1)
	e.st.SetFault(func(op string) error {
		if op == OpInsertEvent {
			return errors.New("injected")
		}
		return nil
	})
	rec.Emit("step", 1)
	if n := len(e.stored(t)); n != 1 {
		t.Errorf("%d events stored, want only the creation's", n)
	}
	if warned := e.log.Find("WARN", "could not be stored"); len(warned) != 1 || warned[0]["job"] != e.id {
		t.Errorf("warnings = %v, want one naming the job", e.log.Records())
	}
	e.st.SetFault(nil)
	rec.Emit("step", 2)
	if ev := receive(t, sub); ev.Seq != 2 {
		t.Errorf("the next event is #%d, want #2", ev.Seq)
	}
}

func TestRecorder_StoresHubReferencesAndDropsEverythingOnceClosed(t *testing.T) {
	e := newEventEnv(t)
	sub := e.br.Subscribe(e.id)
	rec := e.recorder(1)
	hub := func() string {
		return testutil.Column(t, e.st.db, "SELECT COALESCE(hub_session, '-') || COALESCE(hub_run, '-') || COALESCE(hub_sandbox, '-') FROM af_attempts WHERE job_id = ?", e.id)[0]
	}
	rec.Hub("s0", "r0", "b0") // the attempt is not running: nothing is stored
	if got := hub(); got != "---" {
		t.Errorf("hub of a queued attempt = %s, want none", got)
	}
	testutil.Exec(t, e.st.db, "UPDATE af_attempts SET state = 'running' WHERE job_id = ?", e.id)
	rec.Hub("s1", "r1", "") // an empty reference is none
	if got := hub(); got != "s1r1-" {
		t.Errorf("hub = %s, want s1, r1 and none", got)
	}
	rec.close()
	rec.Emit("step", 1)
	rec.Hub("s2", "r2", "b2")
	if got := hub(); got != "s1r1-" || len(e.stored(t)) != 1 {
		t.Errorf("after the close: hub %s and %d events, want them as they were", got, len(e.stored(t)))
	}
	select {
	case ev := <-sub.C:
		t.Errorf("published after the close: %+v", ev)
	default:
	}
	// A store that fails is warned of, and the references are dropped.
	testutil.Exec(t, e.st.db, "ALTER TABLE af_attempts RENAME COLUMN hub_run TO hub_run_away")
	e.recorder(1).Hub("s3", "r3", "b3")
	if len(e.log.Find("WARN", "hub references")) != 1 {
		t.Errorf("warnings = %v, want one for the hub references", e.log.Records())
	}
}

func TestAppendEvent_NumbersEventsWithoutAGapAndPublishesEachOnceItIsInTheFile(t *testing.T) {
	e := newEventEnv(t)
	var early atomic.Int32
	var mu sync.Mutex
	var published []int64
	e.st.SetPublisher(func(ev Event) {
		// Read through another connection than the writer's: it shows what has been committed.
		if testutil.Count(t, e.st.db, "af_events", "job_id = ? AND seq = ?", ev.JobID, ev.Seq) != 1 {
			early.Add(1)
		}
		mu.Lock()
		published = append(published, ev.Seq)
		mu.Unlock()
	})
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			rec := e.recorder(1 + g%2)
			for i := range 25 {
				rec.Emit("step", i)
			}
		})
	}
	wg.Wait()
	evs := e.stored(t)
	if len(evs) != 1+8*25 {
		t.Fatalf("%d events, want %d", len(evs), 1+8*25)
	}
	for i, ev := range evs {
		if ev.Seq != int64(i+1) || ev.TS == "" {
			t.Fatalf("event %d is #%d at %q, want #%d", i, ev.Seq, ev.TS, i+1)
		}
	}
	if early.Load() != 0 || len(published) != 8*25 {
		t.Errorf("%d events published before they were in the file, %d published, want 0 and %d", early.Load(), len(published), 8*25)
	}
	for i, seq := range published {
		if seq != int64(i+2) {
			t.Fatalf("published in the order %v, want the order of the numbers", published)
		}
	}
}

func TestAppendEvent_HoldsTheJobUntilItsEventIsPublished(t *testing.T) {
	e := newEventEnv(t)
	entered, gate := make(chan struct{}), make(chan struct{})
	var order []int64
	e.st.SetPublisher(func(ev Event) {
		if ev.Seq == 2 {
			close(entered)
			<-gate
		}
		order = append(order, ev.Seq)
	})
	var wg sync.WaitGroup
	wg.Go(func() { e.recorder(1).Emit("step", "first") })
	<-entered
	wg.Go(func() { e.recorder(2).Emit("step", "second") })
	// The second waits for the lock of the job, which the first holds while it publishes.
	testutil.Eventually(t, "the second event to wait for the job", func() bool {
		e.st.locks.mu.Lock()
		defer e.st.locks.mu.Unlock()
		return e.st.locks.locks[e.id] != nil && e.st.locks.locks[e.id].users == 2
	})
	close(gate)
	wg.Wait()
	if len(order) != 2 || order[0] != 2 || order[1] != 3 {
		t.Errorf("published in the order %v, want [2 3]", order)
	}
	if len(e.st.locks.locks) != 0 {
		t.Errorf("%d locks are kept after their users let go", len(e.st.locks.locks))
	}
}

func TestBroker_ClosesASubscriberThatFallsBehindWithoutWaitingForIt(t *testing.T) {
	br := NewBroker()
	stalled, healthy, other := br.Subscribe("job_a"), br.Subscribe("job_a"), br.Subscribe("job_b")
	for seq := int64(1); seq <= 1000; seq++ {
		br.Publish(Event{JobID: "job_a", Seq: seq})
		if got := receive(t, healthy); got.Seq != seq {
			t.Fatalf("the subscriber that reads got #%d, want #%d", got.Seq, seq)
		}
	}
	br.Publish(Event{JobID: "job_b", Seq: 1})
	if !stalled.Slow() || healthy.Slow() {
		t.Errorf("slow: the stalled subscriber %v, the one that reads %v, want true and false", stalled.Slow(), healthy.Slow())
	}
	n := 0
	for range stalled.C {
		n++
	}
	if n != SubscriberBuffer {
		t.Errorf("the stalled subscriber had %d events, want the %d it had room for", n, SubscriberBuffer)
	}
	if got := receive(t, other); got.JobID != "job_b" {
		t.Errorf("the subscriber of another job got %+v", got)
	}
	healthy.Cancel()
	if _, ok := <-healthy.C; ok {
		t.Error("a cancelled subscription is open")
	}
	br.Close()
	if _, ok := <-other.C; ok || other.Slow() {
		t.Error("a subscription is open after the broker closed, or it is taken for slow")
	}
	late := br.Subscribe("job_a")
	br.Publish(Event{JobID: "job_a", Seq: 1})
	if _, ok := <-late.C; ok {
		t.Error("a subscription to a closed broker delivers")
	}
	late.Cancel()
}

func TestFollower_SeesEveryEventAfterItsStartOnceAndInOrder(t *testing.T) {
	e := newEventEnv(t) // the creation stored event 1
	const per = 60
	total := int64(1 + 2*per)
	var emitting sync.WaitGroup
	for g := range 2 {
		emitting.Go(func() {
			rec := e.recorder(g + 1)
			for i := range per {
				rec.Emit("step", i)
			}
		})
	}
	results := make(chan []int64, 8)
	var following sync.WaitGroup
	follow := func(since int64) {
		following.Go(func() {
			f := e.br.Follow(e.st, e.id, since)
			defer f.Close()
			got := []int64{since}
			if err := f.Replay(ctx, func(ev Event) error { got = append(got, ev.Seq); return nil }); err != nil {
				t.Error(err)
			}
			for f.Last() < total {
				ev, ok := <-f.Live()
				if !ok {
					t.Errorf("since %d: the live events ended at %d", since, f.Last())
					break
				}
				if f.Take(ev) {
					got = append(got, ev.Seq)
				}
			}
			results <- got
		})
	}
	// Followers that start while the events are stored, at whatever point the file has reached.
	for i := range 5 {
		follow(int64(min(i*20, len(e.stored(t)))))
	}
	emitting.Wait()
	follow(total - 1)
	follow(total)
	following.Wait()
	close(results)
	for got := range results {
		since := got[0]
		for i, seq := range got[1:] {
			if seq != since+int64(i)+1 {
				t.Fatalf("since %d: event %d is #%d, want #%d", since, i, seq, since+int64(i)+1)
			}
		}
		if int64(len(got)-1) != total-since {
			t.Errorf("since %d: %d events, want %d", since, len(got)-1, total-since)
		}
	}
}

func TestFollowerReplay_SendsMoreThanOneBatchOnceAndInOrder(t *testing.T) {
	e := newEventEnv(t)
	rec := e.recorder(1)
	const total = 2*replayBatch + 7
	for i := 1; i < total; i++ {
		rec.Emit("step", i)
	}
	for _, since := range []int64{0, replayBatch - 1, replayBatch, replayBatch + 1, total - 1, total} {
		f := e.br.Follow(e.st, e.id, since)
		next := since + 1
		err := f.Replay(ctx, func(ev Event) error {
			if ev.Seq != next {
				return errors.New("out of order")
			}
			next++
			return nil
		})
		if err != nil || next != total+1 || f.Last() != total {
			t.Errorf("since %d: %v, last %d, want every event to %d", since, err, f.Last(), total)
		}
		if f.Take(Event{Seq: total}) || !f.Take(Event{Seq: total + 1}) {
			t.Errorf("since %d: Take accepted an event that was sent, or refused the next", since)
		}
		f.Close()
	}
	// An error of send stops the replay.
	stop := errors.New("stop")
	f := e.br.Follow(e.st, e.id, 0)
	defer f.Close()
	if err := f.Replay(ctx, func(Event) error { return stop }); !errors.Is(err, stop) || f.Last() != 0 {
		t.Errorf("Replay = %v with last %d, want the error of send and nothing counted", err, f.Last())
	}
}

func TestEventFrame_IsTheNumberTheTypeAndTheEventOnOneLine(t *testing.T) {
	ev := Event{Seq: 7, TS: "2026-10-22T08:00:00.000Z", JobID: "job_x", Attempt: 2, Type: "step", Data: json.RawMessage(`{"a":"<b>"}`)}
	want := "id: 7\nevent: step\ndata: {\"seq\":7,\"ts\":\"2026-10-22T08:00:00.000Z\",\"job_id\":\"job_x\",\"attempt\":2,\"type\":\"step\",\"data\":{\"a\":\"<b>\"}}\n\n"
	if got := string(ev.Frame()); got != want {
		t.Errorf("Frame = %q, want %q", got, want)
	}
}
