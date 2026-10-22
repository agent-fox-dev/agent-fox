package jobs

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// logBuffer is a log destination that goroutines may write to.
type logBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

// records are the log records written so far.
func (l *logBuffer) records() []map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(l.b.String()), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) == nil {
			out = append(out, rec)
		}
	}
	return out
}

// eventEnv is a store whose events go to a broker, and a job to put them in.
type eventEnv struct {
	st   *Store
	br   *Broker
	logs *logBuffer
	log  *slog.Logger
	id   string
}

func newEventEnv(t *testing.T) *eventEnv {
	t.Helper()
	db := openDB(t)
	e := &eventEnv{st: NewStore(db, newTestClock().Now), br: NewBroker(), logs: &logBuffer{}}
	e.log = slog.New(slog.NewJSONHandler(e.logs, nil))
	e.st.SetPublisher(e.br.Publish)
	t.Cleanup(e.br.Close)
	id, err := e.st.CreateJob(ctx, newJob(KindFix, 1))
	if err != nil {
		t.Fatal(err)
	}
	e.id = id
	return e
}

// stored are all the stored events of the job.
func (e *eventEnv) stored(t *testing.T) []Event {
	t.Helper()
	evs, err := e.st.EventsAfter(ctx, e.id, 0, 100000)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

// last is the last stored event.
func (e *eventEnv) last(t *testing.T) Event {
	t.Helper()
	evs := e.stored(t)
	if len(evs) == 0 {
		t.Fatal("no event is stored")
	}
	return evs[len(evs)-1]
}

// receive reads the next event of a subscription, or fails the test.
func receive(t *testing.T, sub *Subscription) Event {
	t.Helper()
	select {
	case ev, ok := <-sub.C:
		if !ok {
			t.Fatal("the subscription is closed")
		}
		return ev
	case <-time.After(10 * time.Second):
		t.Fatal("no event arrived within 10s")
		return Event{}
	}
}

// TestStoredSeqsAreGapFreeAndStoredBeforeTheyArePublished_TS_12_64 verifies TS-12-64:
// the events of a job, from the jobs layer and from concurrent runners of several
// attempts, are numbered 1 to k without a gap; each has ts, job_id, attempt, type
// and data; and a subscriber that looks at the file when an event reaches it finds
// the event there.
// Verifies: 12-REQ-10.1
func TestStoredSeqsAreGapFreeAndStoredBeforeTheyArePublished_TS_12_64(t *testing.T) {
	rng := rand.New(rand.NewPCG(12, 64))
	types := []string{"step", "tool_call", "heartbeat", "x", "usage_note"}
	for run := range 12 {
		e := newEventEnv(t)
		sub := e.br.Subscribe(e.id)
		attempts := 1 + rng.IntN(4)

		// The store hands each event to the publisher once it has committed it: the
		// file, read through another connection, shows the event at that moment.
		var early atomic.Int64
		e.st.SetPublisher(func(ev Event) {
			var n int
			if err := e.st.db.QueryRow("SELECT count(*) FROM af_events WHERE job_id = ? AND seq = ?", ev.JobID, ev.Seq).Scan(&n); err != nil || n != 1 {
				early.Add(1)
			}
			e.br.Publish(ev)
		})
		defer func() {
			if n := early.Load(); n != 0 {
				t.Errorf("run %d: %d events were published before they were in the file", run, n)
			}
		}()

		type delivery struct {
			ev     Event
			stored bool
		}
		var (
			mu        sync.Mutex
			delivered []delivery
			done      = make(chan struct{})
		)
		go func() {
			defer close(done)
			for ev := range sub.C {
				var n int
				// The file is read through another connection than the writer's: it shows
				// what has been committed.
				if err := e.st.db.QueryRow("SELECT count(*) FROM af_events WHERE job_id = ? AND seq = ?", ev.JobID, ev.Seq).Scan(&n); err != nil {
					t.Error(err)
				}
				mu.Lock()
				delivered = append(delivered, delivery{ev, n == 1})
				mu.Unlock()
			}
		}()

		var wg sync.WaitGroup
		total := 1 // the job_state of the creation
		for n := 1; n <= attempts; n++ {
			count := rng.IntN(40)
			total += count
			rec := newRecorder(e.st, e.log, e.id, n)
			kinds := make([]string, count)
			for i := range kinds {
				kinds[i] = types[rng.IntN(len(types))]
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i, k := range kinds {
					rec.Emit(k, map[string]any{"attempt": n, "i": i})
				}
			}()
		}
		// Meanwhile the jobs layer moves the job on, and says so in the log.
		for n := 2; n <= attempts; n++ {
			force(t, e.st.db, e.st, e.id, StateFailed)
			if _, err := e.st.Requeue(ctx, e.id, StateFailed, Trigger{Kind: TriggerRetry, By: "u"}, nil); err != nil {
				t.Fatal(err)
			}
			total++
		}
		wg.Wait()
		e.br.Close()
		<-done

		evs := e.stored(t)
		if len(evs) != total {
			t.Fatalf("run %d: %d events stored, want %d", run, len(evs), total)
		}
		for i, ev := range evs {
			if ev.Seq != int64(i+1) {
				t.Fatalf("run %d: the event at position %d has seq %d, want %d", run, i, ev.Seq, i+1)
			}
			if ev.TS == "" || ev.JobID != e.id || ev.Attempt < 1 || ev.Attempt > attempts || ev.Type == "" || len(ev.Data) == 0 {
				t.Fatalf("run %d: event %+v lacks a field", run, ev)
			}
			if !timeShape.MatchString(ev.TS) {
				t.Errorf("run %d: ts %q is not RFC 3339 UTC with milliseconds", run, ev.TS)
			}
		}
		// The subscriber came after the creation of the job, so it gets events 2 to total.
		mu.Lock()
		if len(delivered) != total-1 {
			t.Errorf("run %d: %d events delivered, want %d", run, len(delivered), total-1)
		}
		for i, d := range delivered {
			if !d.stored {
				t.Errorf("run %d: event %d reached the subscriber before it was in the file", run, d.ev.Seq)
			}
			if d.ev.Seq != int64(i+2) {
				t.Errorf("run %d: the subscriber's event %d has seq %d, want %d: they arrive in order", run, i, d.ev.Seq, i+2)
				break
			}
		}
		mu.Unlock()
	}
}

// TestMalformedAndReservedTypesLargeAndUnencodableDataAreStoredAsSaid_TS_12_65 verifies
// TS-12-65: an event whose type does not match ^[a-z][a-z0-9_]*$, or is one of the
// layer's own, is stored as invalid_event_type with the type and the data it was
// given; data over 64 KiB as JSON becomes {"truncated":true,"bytes":N} and data
// that cannot be encoded {"error":…}; every event is streamed as stored and the
// calls return.
// Verifies: 12-REQ-10.2
func TestMalformedAndReservedTypesLargeAndUnencodableDataAreStoredAsSaid_TS_12_65(t *testing.T) {
	e := newEventEnv(t)
	sub := e.br.Subscribe(e.id)
	rec := newRecorder(e.st, e.log, e.id, 1)

	given := map[string]any{"a": 1, "list": []int{1, 2}}
	wantGiven := `{"a":1,"list":[1,2]}`
	for _, typ := range []string{"Bad-Type", "", "job_state", "attempt_start", "attempt_end", "1step", "has space", "Step", "a-b", "tool\ncall", "é"} {
		rec.Emit(typ, given)
		ev := e.last(t)
		var wrapped struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if ev.Type != "invalid_event_type" || json.Unmarshal(ev.Data, &wrapped) != nil || wrapped.Type != typ || string(wrapped.Data) != wantGiven {
			t.Errorf("type %q: stored as %q with %s, want invalid_event_type with the type and %s", typ, ev.Type, ev.Data, wantGiven)
		}
		if got := receive(t, sub); got.Seq != ev.Seq || got.Type != "invalid_event_type" {
			t.Errorf("type %q: streamed as %+v, want the stored event", typ, got)
		}
	}

	// Types that are in the form stay as they are.
	for _, typ := range []string{"step", "tool_call", "a", "x1_y2", "heartbeat"} {
		rec.Emit(typ, given)
		if ev := e.last(t); ev.Type != typ || string(ev.Data) != wantGiven {
			t.Errorf("type %q: stored as %q with %s, want it unchanged", typ, ev.Type, ev.Data)
		}
		receive(t, sub)
	}

	// 64 KiB of JSON is kept, a byte more is replaced by its size.
	for _, tc := range []struct {
		name     string
		chars    int
		wantSize int
		replaced bool
	}{
		{"70 KiB", 70 * 1024, 70*1024 + 2, true},
		{"exactly 64 KiB of JSON", 64*1024 - 2, 64 * 1024, false},
		{"64 KiB and a byte of JSON", 64*1024 - 1, 64*1024 + 1, true},
	} {
		rec.Emit("step", strings.Repeat("a", tc.chars))
		ev := e.last(t)
		var marker struct {
			Truncated bool `json:"truncated"`
			Bytes     int  `json:"bytes"`
		}
		if tc.replaced {
			if err := json.Unmarshal(ev.Data, &marker); err != nil || !marker.Truncated || marker.Bytes != tc.wantSize || len(ev.Data) > 100 {
				t.Errorf("%s: stored as %.80s, want {\"truncated\":true,\"bytes\":%d}", tc.name, ev.Data, tc.wantSize)
			}
		} else if len(ev.Data) != tc.wantSize {
			t.Errorf("%s: stored %d bytes, want the data kept at %d", tc.name, len(ev.Data), tc.wantSize)
		}
		if got := receive(t, sub); got.Seq != ev.Seq || len(got.Data) != len(ev.Data) {
			t.Errorf("%s: streamed %d bytes, stored %d", tc.name, len(got.Data), len(ev.Data))
		}
	}

	// A type that is wrong and data that is large: the data is replaced before it is wrapped.
	rec.Emit("Bad", strings.Repeat("b", 70*1024))
	if ev := e.last(t); ev.Type != "invalid_event_type" || len(ev.Data) > 200 || !strings.Contains(string(ev.Data), `"truncated":true`) || !strings.Contains(string(ev.Data), `"type":"Bad"`) {
		t.Errorf("a wrong type with large data: stored as %q with %.120s", ev.Type, ev.Data)
	}
	receive(t, sub)

	// Data that cannot be encoded.
	rec.Emit("step", make(chan int))
	ev := e.last(t)
	var failure struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(ev.Data, &failure); err != nil || ev.Type != "step" || failure.Error == "" {
		t.Errorf("unencodable data: stored as %q with %s, want step with {\"error\":…}", ev.Type, ev.Data)
	}
	receive(t, sub)
	rec.Emit("Wrong", func() {})
	if ev := e.last(t); ev.Type != "invalid_event_type" || !strings.Contains(string(ev.Data), `"error"`) || !strings.Contains(string(ev.Data), `"type":"Wrong"`) {
		t.Errorf("a wrong type and unencodable data: stored as %q with %s", ev.Type, ev.Data)
	}
	receive(t, sub)

	// The attempt goes on: every call returned, and the next event takes the next number.
	rec.Emit("step", "after")
	evs := e.stored(t)
	for i, ev := range evs {
		if ev.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d, want %d", i, ev.Seq, i+1)
		}
	}
	if len(e.logs.records()) != 0 {
		t.Errorf("events that were replaced were logged: %v", e.logs.records())
	}
}

// TestAnEventThatCannotBeStoredIsLoggedAsAWarningAndDropped_TS_12_66 verifies TS-12-66:
// when the insert of an event fails, Emit returns, a WARN is logged, nothing is
// published, and the next event takes the number the failed one did not.
// Verifies: 12-REQ-10.3
func TestAnEventThatCannotBeStoredIsLoggedAsAWarningAndDropped_TS_12_66(t *testing.T) {
	e := newEventEnv(t)
	sub := e.br.Subscribe(e.id)
	rec := newRecorder(e.st, e.log, e.id, 1)

	e.st.SetFault(func(op string) error {
		if op == OpInsertEvent {
			return fmt.Errorf("injected failure of %s", op)
		}
		return nil
	})
	before := len(e.stored(t))
	rec.Emit("step", map[string]int{"n": 1})
	if got := len(e.stored(t)); got != before {
		t.Errorf("%d events stored, want the %d there were", got, before)
	}
	select {
	case ev := <-sub.C:
		t.Errorf("the subscriber got %+v of an event that was not stored", ev)
	default:
	}
	warned := false
	for _, rec := range e.logs.records() {
		if rec["level"] == "WARN" && strings.Contains(fmt.Sprint(rec["msg"]), "event") && rec["job"] == e.id {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no WARN names the job and the event: %v", e.logs.records())
	}

	e.st.SetFault(nil)
	rec.Emit("step", map[string]int{"n": 2})
	ev := e.last(t)
	if ev.Seq != int64(before+1) || ev.Type != "step" {
		t.Errorf("the next event is %+v, want number %d: no gap where the failed one was", ev, before+1)
	}
	if got := receive(t, sub); got.Seq != ev.Seq {
		t.Errorf("the subscriber got %d, want %d", got.Seq, ev.Seq)
	}
}

// TestARecorderThatHasEndedDropsWhatIsSentAfterwards verifies the closed flag of the
// recorder (12-REQ-5.5, which the end of an attempt in task 5 applies): after the
// recorder is closed, Emit and Hub store and publish nothing, and a call that is in
// progress when it is closed is finished before the close returns.
// Verifies: 12-REQ-5.3
func TestARecorderThatHasEndedDropsWhatIsSentAfterwards(t *testing.T) {
	e := newEventEnv(t)
	sub := e.br.Subscribe(e.id)
	rec := newRecorder(e.st, e.log, e.id, 1)
	rec.Emit("step", 1)
	receive(t, sub)
	before := len(e.stored(t))

	// The references of a running attempt are stored; an empty one is none.
	if _, err := e.st.db.Exec("UPDATE af_attempts SET state = 'running' WHERE job_id = ? AND n = 1", e.id); err != nil {
		t.Fatal(err)
	}
	hub := func() (session, run, sandbox sql.NullString) {
		t.Helper()
		if err := e.st.db.QueryRow("SELECT hub_session, hub_run, hub_sandbox FROM af_attempts WHERE job_id = ? AND n = 1", e.id).Scan(&session, &run, &sandbox); err != nil {
			t.Fatal(err)
		}
		return
	}
	rec.Hub("s1", "r1", "")
	if session, run, sandbox := hub(); session.String != "s1" || run.String != "r1" || sandbox.Valid {
		t.Errorf("hub references = %v %v %v, want s1, r1 and none", session, run, sandbox)
	}

	rec.close()
	rec.Emit("step", 2)
	rec.Hub("s2", "r2", "b2")
	if session, run, sandbox := hub(); session.String != "s1" || run.String != "r1" || sandbox.Valid {
		t.Errorf("hub references = %v %v %v after the close, want them as they were", session, run, sandbox)
	}
	if got := len(e.stored(t)); got != before {
		t.Errorf("%d events stored after the close, want %d", got, before)
	}
	select {
	case ev := <-sub.C:
		t.Errorf("an event was published after the close: %+v", ev)
	default:
	}
}

// TestABrokerNeverBlocksAndDisconnectsASubscriberMoreThan256Behind verifies the broker
// of 12-REQ-10.7: a subscriber holds 256 events; the 257th it has not read
// closes its channel and flags it slow, without a wait, while the other subscribers
// of the job and the subscribers of other jobs get everything; a closed broker
// closes every subscription.
// Verifies: 12-REQ-10.7
func TestABrokerNeverBlocksAndDisconnectsASubscriberMoreThan256Behind(t *testing.T) {
	br := NewBroker()
	stalled, healthy, other := br.Subscribe("job_a"), br.Subscribe("job_a"), br.Subscribe("job_b")

	// The subscriber that reads is kept up with, so that it is never behind by more than
	// one event: it is the one that does not read that is behind.
	var healthyGot []int64
	var read atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range healthy.C {
			healthyGot = append(healthyGot, ev.Seq)
			read.Store(ev.Seq)
		}
	}()

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for seq := int64(1); seq <= 1000; seq++ {
			br.Publish(Event{JobID: "job_a", Seq: seq, Type: "step"})
			for read.Load() < seq {
				runtime.Gosched()
			}
		}
		br.Publish(Event{JobID: "job_b", Seq: 1, Type: "step"})
	}()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("Publish waited for a subscriber that does not read")
	}

	if !stalled.Slow() {
		t.Error("the subscriber that did not read is not flagged slow")
	}
	n := 0
	for range stalled.C {
		n++
	}
	if n != SubscriberBuffer {
		t.Errorf("the stalled subscriber could read %d events, want the %d it held", n, SubscriberBuffer)
	}
	if got := receive(t, other); got.Seq != 1 || got.JobID != "job_b" {
		t.Errorf("a subscriber of another job got %+v", got)
	}

	br.Close()
	<-done
	if len(healthyGot) != 1000 {
		t.Fatalf("the subscriber that read got %d events, want 1000", len(healthyGot))
	}
	for i, seq := range healthyGot {
		if seq != int64(i+1) {
			t.Fatalf("the healthy subscriber's event %d has seq %d", i, seq)
		}
	}
	if healthy.Slow() {
		t.Error("the subscriber that read is flagged slow")
	}
	// After the close, a subscription is closed at once, and publishing is harmless.
	late := br.Subscribe("job_a")
	if _, ok := <-late.C; ok {
		t.Error("a subscription to a closed broker delivers")
	}
	br.Publish(Event{JobID: "job_a", Seq: 1})
	late.Cancel()
}

// TestAFollowerSeesEveryEventAfterItsStartOnceAndInOrder verifies the primitive that
// the event stream is made of (12-REQ-10.5): for any starting point, and however the
// moment it starts falls among events that are being stored and published, the
// stored events after it and then the live ones are exactly the events k+1 to n,
// each once and in order; and a follower that starts again from the last number it
// saw gets exactly the ones it missed.
// Verifies: 12-REQ-10.5
func TestAFollowerSeesEveryEventAfterItsStartOnceAndInOrder(t *testing.T) {
	for run := range 15 {
		rng := rand.New(rand.NewPCG(12, uint64(680+run)))
		e := newEventEnv(t) // the creation stored event 1
		per := [2]int{20 + rng.IntN(40), 20 + rng.IntN(40)}
		total := int64(1 + per[0] + per[1])

		var emitting sync.WaitGroup
		for g := range 2 {
			rec := newRecorder(e.st, e.log, e.id, g+1)
			pause := rand.New(rand.NewPCG(12, uint64(6800+run*2+g)))
			emitting.Add(1)
			go func() {
				defer emitting.Done()
				for i := range per[g] {
					rec.Emit("step", i)
					if pause.IntN(3) == 0 {
						time.Sleep(time.Duration(pause.IntN(300)) * time.Microsecond)
					}
				}
			}()
		}

		type outcome struct {
			since int64
			got   []int64
		}
		results := make(chan outcome, 16)
		var following sync.WaitGroup
		follow := func(since int64) {
			following.Add(1)
			go func() {
				defer following.Done()
				f := e.br.Follow(e.st, e.id, since)
				defer f.Close()
				var got []int64
				if err := f.Replay(ctx, func(ev Event) error { got = append(got, ev.Seq); return nil }); err != nil {
					t.Error(err)
					return
				}
				timeout := time.After(10 * time.Second)
				for f.Last() < total {
					select {
					case ev, ok := <-f.Live():
						if !ok {
							t.Errorf("run %d since %d: the live events ended at %d of %d (slow: %v)", run, since, f.Last(), total, f.Slow())
							results <- outcome{since, got}
							return
						}
						if f.Take(ev) {
							got = append(got, ev.Seq)
						}
					case <-timeout:
						t.Errorf("run %d since %d: no event %d within 10s", run, since, f.Last()+1)
						results <- outcome{since, got}
						return
					}
				}
				results <- outcome{since, got}
			}()
		}
		for range 6 {
			time.Sleep(time.Duration(rng.IntN(1500)) * time.Microsecond)
			follow(int64(rng.IntN(len(e.stored(t)) + 1)))
		}
		follow(0)
		emitting.Wait()
		follow(total - 1)
		follow(total) // nothing is missing: it waits for nothing
		following.Wait()
		close(results)

		n := 0
		for res := range results {
			n++
			want := total - res.since
			if int64(len(res.got)) != want {
				t.Errorf("run %d since %d: %d events, want %d", run, res.since, len(res.got), want)
				continue
			}
			for i, seq := range res.got {
				if seq != res.since+int64(i)+1 {
					t.Errorf("run %d since %d: event %d is number %d, want %d", run, res.since, i, seq, res.since+int64(i)+1)
					break
				}
			}
		}
		if n != 9 {
			t.Errorf("run %d: %d followers finished, want 9", run, n)
		}
	}
}

// TestAReplayOfMoreThanOneBatchSendsEveryEventOnceAndInOrder verifies that Replay reads
// the stored events in batches without losing or repeating the ones at the edges of a
// batch, and continues from the last event it sent.
// Verifies: 12-REQ-10.5
func TestAReplayOfMoreThanOneBatchSendsEveryEventOnceAndInOrder(t *testing.T) {
	e := newEventEnv(t)
	rec := newRecorder(e.st, e.log, e.id, 1)
	const total = 2*replayBatch + 7
	for i := 1; i < total; i++ { // the creation stored event 1
		rec.Emit("step", i)
	}
	for _, since := range []int64{0, 1, replayBatch - 1, replayBatch, replayBatch + 1, 2*replayBatch - 1, total - 1, total} {
		f := e.br.Follow(e.st, e.id, since)
		var got []int64
		if err := f.Replay(ctx, func(ev Event) error { got = append(got, ev.Seq); return nil }); err != nil {
			t.Fatal(err)
		}
		if int64(len(got)) != total-since || f.Last() != total {
			t.Errorf("since %d: %d events, last %d, want %d and %d", since, len(got), f.Last(), total-since, total)
		}
		for i, seq := range got {
			if seq != since+int64(i)+1 {
				t.Errorf("since %d: event %d is number %d, want %d", since, i, seq, since+int64(i)+1)
				break
			}
		}
		// A second replay has nothing more to send, and what Replay sent is not taken again.
		if err := f.Replay(ctx, func(ev Event) error { t.Errorf("since %d: replayed %d again", since, ev.Seq); return nil }); err != nil {
			t.Fatal(err)
		}
		if f.Take(Event{Seq: total}) || (since+1 <= total && f.Take(Event{Seq: since + 1})) {
			t.Errorf("since %d: Take accepted an event that was sent already", since)
		}
		if !f.Take(Event{Seq: total + 1}) || f.Last() != total+1 {
			t.Errorf("since %d: Take refused the event after the last", since)
		}
		f.Close()
	}
}

// TestEventsOfAJobArePublishedInTheOrderOfTheirNumbers verifies that the store holds
// the lock of a job from the moment an event is numbered until it is published: an
// event whose publication is held up keeps the next event of the job from being
// stored, so that no subscriber sees number 3 before number 2.
// Verifies: 12-REQ-10.1
func TestEventsOfAJobArePublishedInTheOrderOfTheirNumbers(t *testing.T) {
	e := newEventEnv(t)
	var (
		mu      sync.Mutex
		order   []int64
		entered = make(chan struct{})
		gate    = make(chan struct{})
	)
	e.st.SetPublisher(func(ev Event) {
		if ev.Seq == 2 {
			close(entered)
			<-gate
		}
		mu.Lock()
		order = append(order, ev.Seq)
		mu.Unlock()
	})

	var emitters sync.WaitGroup
	emitters.Add(2)
	go func() { defer emitters.Done(); newRecorder(e.st, e.log, e.id, 1).Emit("step", "first") }()
	<-entered // the first event is stored and is being published
	go func() { defer emitters.Done(); newRecorder(e.st, e.log, e.id, 2).Emit("step", "second") }()
	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	if len(order) != 0 {
		t.Errorf("events %v were published while the publication of event 2 was held up", order)
	}
	mu.Unlock()
	close(gate)
	emitters.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != 2 || order[1] != 3 {
		t.Errorf("events were published in the order %v, want [2 3]", order)
	}
}
