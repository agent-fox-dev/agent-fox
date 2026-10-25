package jobs_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// stream follows the job's events after since in the background: each event it sends, and each keepalive as an
// event of the type "keepalive", arrive on the channel, and its end on the other.
func (e *env) stream(sctx context.Context, id string, since int64) (<-chan jobs.Event, <-chan error) {
	frames, end := make(chan jobs.Event, 4096), make(chan error, 1)
	go func() {
		end <- e.Svc.Stream(sctx, id, since, func(ev jobs.Event) error { frames <- ev; return nil },
			func() error { frames <- jobs.Event{Type: "keepalive"}; return nil })
	}()
	return frames, end
}

// drain is everything the stream sent once it has ended, and how it ended.
func drain(t *testing.T, frames <-chan jobs.Event, end <-chan error) ([]jobs.Event, error) {
	t.Helper()
	var err error
	select {
	case err = <-end:
	case <-time.After(testutil.WaitTimeout):
		t.Fatal("the stream did not end")
	}
	var evs []jobs.Event
	for {
		select {
		case ev := <-frames:
			evs = append(evs, ev)
		default:
			return evs, err
		}
	}
}

// streamAll follows the job's events after since until the stream ends.
func (e *env) streamAll(id string, since int64) ([]jobs.Event, error) {
	e.t.Helper()
	frames, end := e.stream(ctx, id, since)
	return drain(e.t, frames, end)
}

// next is the next thing the stream sent.
func next(t *testing.T, frames <-chan jobs.Event) jobs.Event {
	t.Helper()
	select {
	case ev := <-frames:
		return ev
	case <-time.After(testutil.WaitTimeout):
		t.Fatal("the stream sent nothing")
		return jobs.Event{}
	}
}

func seqsOf(evs []jobs.Event) []int64 {
	var out []int64
	for _, ev := range evs {
		out = append(out, ev.Seq)
	}
	return out
}

func TestStream_OfAJobAtRestIsItsReplayAfterSince(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(question())))
	job := e.create("widgets", 1, "")
	e.start()
	e.waitState(job.ID, jobs.StateBlocked)
	all := e.events(job.ID)
	for _, since := range []int64{0, 2, int64(len(all))} {
		evs, err := e.streamAll(job.ID, since)
		if err != nil || !slices.Equal(seqsOf(evs), seqsOf(all[since:])) {
			t.Errorf("since %d: %v, %v, want the events after it and the end", since, seqsOf(evs), err)
		}
	}
}

func TestStream_StaysOpenUntilALiveJobStateAtRestAndKeepsAliveMeanwhile(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(question(), fakerunner.BlockIgnoringContext(fakerunner.Step{}))))
	job := e.create("widgets", 1, "")
	e.start()
	e.waitState(job.ID, jobs.StateBlocked)
	if _, err := e.Svc.Answer(ctx, job.ID, "u", jobs.AnswerRequest{Option: ptr("A")}); err != nil {
		t.Fatal(err)
	}
	e.waitCalls(2)
	stored := len(e.events(job.ID)) // the replay holds the job_state at rest of the question
	tickers := e.Clock.ActiveTickers()
	frames, end := e.stream(ctx, job.ID, 0)
	for range stored {
		next(t, frames)
	}
	testutil.Eventually(t, "the stream's ticker", func() bool { return e.Clock.ActiveTickers() > tickers })
	e.Clock.Advance(14 * time.Second)
	e.Clock.Advance(time.Second)
	if ev := next(t, frames); ev.Type != "keepalive" {
		t.Fatalf("after 15 s the stream sent %+v, want a keepalive", ev)
	}
	if _, err := e.Svc.Cancel(ctx, job.ID); err != nil { // a job_state that is not at rest: the runner is held
		t.Fatal(err)
	}
	if ev := next(t, frames); ev.Type != jobs.EventJobState || e.lastState(job.ID) != "cancelled:false" {
		t.Fatalf("after the cancel the stream sent %+v, want the job_state of the cancel", ev)
	}
	select {
	case err := <-end:
		t.Fatalf("the stream ended (%v) on a job_state that is not at rest", err)
	default:
	}
	e.Runner.ReleaseAll()
	evs, err := drain(t, frames, end)
	if err != nil || len(evs) < 2 || evs[len(evs)-1].Type != jobs.EventJobState || e.lastState(job.ID) != "cancelled:true" {
		t.Errorf("after the release: %v, %v, want attempt_end and the job_state at rest last", evs, err)
	}
}

func TestStream_ASubscriberThatFallsBehindIsCutWithoutHoldingUpTheRunnerAndResumes(t *testing.T) {
	start := make(chan struct{})
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Do(func(_ context.Context, req jobs.Request) (jobs.Result, error) {
		<-start
		for i := range 1000 {
			req.Recorder.Emit("tick", i)
		}
		return jobs.Result{Envelope: fakerunner.Env(toolio.StatusDone)}, nil
	}))))
	e.openAtEnd(start)
	job := e.create("widgets", 1, "")
	e.start()
	e.waitCalls(1)
	gate, live := make(chan struct{}), make(chan struct{})
	var seen []int64
	end := make(chan error, 1)
	tickers := e.Clock.ActiveTickers()
	go func() {
		end <- e.Svc.Stream(ctx, job.ID, 0, func(ev jobs.Event) error {
			seen = append(seen, ev.Seq)
			if ev.Type == "tick" && len(seen) == 4 {
				close(live)
				<-gate // the client stops reading
			}
			return nil
		}, func() error { return nil })
	}()
	testutil.Eventually(t, "the stream to be live", func() bool { return e.Clock.ActiveTickers() > tickers })
	close(start)
	<-live
	e.waitState(job.ID, jobs.StateDone) // the runner was not held up
	close(gate)
	if err := <-end; !errors.Is(err, jobs.ErrSlowSubscriber) {
		t.Fatalf("the stream of a client that fell behind ended with %v, want ErrSlowSubscriber", err)
	}
	last := seen[len(seen)-1]
	if int64(len(seen)) != last || len(seen) < 4+jobs.SubscriberBuffer || len(seen) > 5+jobs.SubscriberBuffer {
		t.Errorf("the client took %d events up to #%d, want a prefix of what it had room for", len(seen), last)
	}
	evs, err := e.streamAll(job.ID, last)
	total := int64(len(e.events(job.ID)))
	if err != nil || int64(len(evs)) != total-last || evs[0].Seq != last+1 {
		t.Errorf("following again from #%d: %d events, %v, want the %d it missed", last, len(evs), err, total-last)
	}
}

func TestStream_EndsForClosedStreamsForItsContextAndForAFailingSend(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(fakerunner.Block(fakerunner.Step{}))))
	job := e.create("widgets", 1, "")
	e.start()
	e.waitCalls(1)
	cut, cancel := context.WithCancel(ctx)
	frames, end := e.stream(cut, job.ID, 0)
	next(t, frames)
	cancel()
	if _, err := drain(t, frames, end); !errors.Is(err, context.Canceled) {
		t.Errorf("a stream whose context ended: %v", err)
	}
	refused := errors.New("the client went away")
	if err := e.Svc.Stream(ctx, job.ID, 0, func(jobs.Event) error { return refused }, nil); !errors.Is(err, refused) {
		t.Errorf("a stream whose send fails: %v", err)
	}
	tickers := e.Clock.ActiveTickers()
	gone := make(chan error, 1)
	go func() {
		gone <- e.Svc.Stream(ctx, job.ID, 0, func(jobs.Event) error { return nil }, func() error { return refused })
	}()
	testutil.Eventually(t, "the stream to be live", func() bool { return e.Clock.ActiveTickers() > tickers })
	e.Clock.Advance(jobs.KeepaliveEvery)
	if err := <-gone; !errors.Is(err, refused) {
		t.Errorf("a stream whose keepalive fails: %v", err)
	}
	frames, end = e.stream(ctx, job.ID, 0)
	next(t, frames)
	e.Svc.CloseStreams()
	if _, err := drain(t, frames, end); !errors.Is(err, jobs.ErrStreamsClosed) {
		t.Errorf("an open stream when the streams close: %v", err)
	}
	// One begun afterwards sends its replay and ends.
	evs, err := e.streamAll(job.ID, 0)
	if !errors.Is(err, jobs.ErrStreamsClosed) || len(evs) != len(e.events(job.ID)) {
		t.Errorf("a stream after the close: %d events, %v, want the replay and the end", len(evs), err)
	}
}
