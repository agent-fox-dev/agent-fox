package afclient

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/afclient/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestFollow_DeliversTheEventsAboveSinceInOrderAndOnce(t *testing.T) {
	s := &script{t: t, events: []step{sends(frame(4, "step"), frame(5, "step"), frame(6, "job_state"), frame(5, "step"), frame(2, "step"), frame(7, "step"))},
		jobs: []step{finished}}
	var events []Event

	rest, err := s.client(nil).Follow(context.Background(), "j", 4, func(e Event) { events = append(events, e) })

	if err != nil || rest == nil || rest.LastSeq != 7 || !strings.Contains(string(rest.Job), `"extra":{"kept":true}`) {
		t.Fatalf("Follow = %+v, %v, want the job at rest as the service sent it, after event 7", rest, err)
	}
	var got []string
	for _, e := range events {
		got = append(got, strconv.FormatInt(e.Seq, 10)+" "+e.Type)
		if string(e.Document) != eventDoc(int(e.Seq), e.Type) {
			t.Errorf("event %d: document %s, want it as sent", e.Seq, e.Document)
		}
	}
	if want := []string{"5 step", "6 job_state", "7 step"}; !slices.Equal(got, want) {
		t.Errorf("delivered %v, want %v", got, want)
	}
	if got := s.requests(); !slices.Equal(got, []string{"events since=4", "job"}) {
		t.Errorf("requests = %v, want the stream from 4 and the read of the job", got)
	}
}

func TestFollow_ReadsTheJobAfterEveryEndOfAStreamAndGoesOnUntilItIsAtRest(t *testing.T) {
	for name, first := range map[string]step{
		"an end of file":      sends(frame(1, "a")),
		"a dropped stream":    dropsAfter(frame(1, "a")),
		"a frame of no event": sends(frame(1, "a"), "data: {oops\n\n", frame(2, "ignored")),
		"an event of no seq":  sends(frame(1, "a"), "data: {\"type\":\"x\"}\n\n"),
		"a line too long":     sends(frame(1, "a"), "data: "+strings.Repeat("x", maxSSELine)+"\n\n"),
	} {
		for jobName, notAtRest := range map[string]step{
			"running":                     stillRunning,
			"done with a running attempt": job("done", "done", "running"),
			"queued":                      job("queued", "queued"),
		} {
			s := &script{t: t, events: []step{first, sends(frame(2, "b"))}, jobs: []step{notAtRest, finished}}

			seqs, rest, err := s.follow(fakeclock.New(epoch), 0)

			if err != nil || rest == nil || !slices.Equal(seqs, []int64{1, 2}) {
				t.Errorf("%s, %s: delivered %v, %+v, %v, want 1 and 2 and the job at rest", name, jobName, seqs, rest, err)
			}
			if got, want := s.requests(), []string{"events since=0", "job", "events since=1", "job"}; !slices.Equal(got, want) {
				t.Errorf("%s, %s: requests = %v, want %v", name, jobName, got, want)
			}
		}
	}
}

func TestFollow_CutsAStreamThatSendsNoByteForSixtySeconds(t *testing.T) {
	more := make(chan struct{})
	quietAfterTwo := func(w http.ResponseWriter, r *http.Request) {
		f := startStream(w)
		_, _ = io.WriteString(w, frame(1, "a"))
		f.Flush()
		<-more
		_, _ = io.WriteString(w, frame(2, "b"))
		f.Flush()
		<-r.Context().Done()
	}
	clk := fakeclock.New(epoch)
	s := &script{t: t, events: []step{quietAfterTwo}, jobs: []step{finished}}
	c := s.client(clk)
	delivered := make(chan int64, 2)
	done := make(chan error, 1)
	go func() {
		_, err := c.Follow(context.Background(), "j", 0, func(e Event) { delivered <- e.Seq })
		done <- err
	}()
	<-delivered
	if !clk.WaitForWait(IdleCut, 1, testutil.WaitTimeout) {
		t.Fatal("no watch of sixty seconds was started")
	}
	clk.Advance(30 * time.Second)
	close(more)
	<-delivered

	// The byte at 30 seconds moves the cut to 90 seconds.
	clk.Advance(30 * time.Second)
	if !clk.WaitForWait(30*time.Second, 1, testutil.WaitTimeout) {
		t.Fatalf("the watch did not wait again for what was left: %v", clk.Waits())
	}
	clk.Advance(30 * time.Second)

	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := s.requests(); !slices.Equal(got, []string{"events since=0", "job"}) {
		t.Errorf("requests = %v, want the cut stream then the read of the job", got)
	}
}

func TestFollow_PausesOneSecondAfterAnEventAnd1248SecondsAfterFailedTries(t *testing.T) {
	s := &script{t: t,
		events: []step{
			sends(frame(1, "a")), sends(), dropsAfter(), answers(503, ``), sends(),
			sends(frame(2, "b")), sends(), sends(),
		},
		jobs: []step{stillRunning, stillRunning, stillRunning, answers(500, ``), stillRunning, stillRunning, finished}}
	clk := fakeclock.New(epoch)

	seqs, rest, err := s.follow(clk, 0)

	if err != nil || rest == nil || !slices.Equal(seqs, []int64{1, 2}) {
		t.Fatalf("Follow = %+v (%v), delivered %v", rest, err, seqs)
	}
	if got, want := clk.Sleeps(), seconds(1, 1, 2, 4, 8, 1, 1); !slices.Equal(got, want) {
		t.Errorf("pauses = %v, want %v: an event clears the count of failed tries (%v)", got, want, s.requests())
	}
}

func TestFollow_EveryWayATryEndsWithoutAnEventIsAFailedTry(t *testing.T) {
	for name, f := range map[string]struct {
		events, job step // job is nil when the stream was not opened, and the job is not read
	}{
		"an end of file":                  {sends(), stillRunning},
		"a drop":                          {dropsAfter(), stillRunning},
		"a frame of no event":             {sends("data: {oops\n\n"), stillRunning},
		"a 503 on the stream":             {answers(503, ``), nil},
		"a 500 on the stream":             {answers(500, ``), nil},
		"a transport error on the stream": {hangsUp(), nil},
		"a 502 on the read of the job":    {sends(), answers(502, ``)},
		"a transport error on the read":   {sends(), hangsUp()},
	} {
		jobs := []step{finished}
		if f.job != nil {
			jobs = []step{f.job, f.job, finished}
		}
		s := &script{t: t, events: []step{f.events, f.events, sends(frame(1, "a"))}, jobs: jobs}
		clk := fakeclock.New(epoch)

		if _, _, err := s.follow(clk, 0); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if got := clk.Sleeps(); !slices.Equal(got, seconds(1, 2)) {
			t.Errorf("%s: pauses = %v, want 1 and 2 seconds (%v)", name, got, s.requests())
		}
	}
}

func TestFollow_GivesUpAfterTheFifthFailedTryInARow(t *testing.T) {
	for name, c := range map[string]struct {
		events   step
		category string
		says     string
	}{
		"a 502":               {answers(502, `{"error":{"code":"hub_unavailable","message":"hub is down"}}`), CategoryHub, "hub is down (hub_unavailable)"},
		"a 503":               {answers(503, ``), CategoryService, "the service answered 503"},
		"a transport error":   {hangsUp(), CategoryService, "cannot be reached"},
		"an end of file":      {sends(), CategoryService, "the event stream ended with no new event"},
		"a frame of no event": {sends("data: []\n\n"), CategoryService, "a frame that is not an event"},
	} {
		s := &script{t: t, events: []step{sends(frame(1, "a"), frame(2, "b"), frame(3, "c")), c.events}, jobs: []step{stillRunning}}
		clk := fakeclock.New(epoch)

		seqs, rest, err := s.follow(clk, 0)

		var gu *GaveUp
		var ae *Error
		if !errors.As(err, &gu) || !errors.As(err, &ae) || rest != nil {
			t.Errorf("%s: Follow = %+v, %v, want a give-up that unwraps to an *Error", name, rest, err)
			continue
		}
		if gu.Err.Category != c.category || !gu.Err.Retryable || gu.LastSeq != 3 || gu.JobID != "j" {
			t.Errorf("%s: %+v, want category %s, retryable, last seq 3, job j", name, gu, c.category)
		}
		want := "stopped following the job j after 5 failed tries in a row; the job may still be running on the service: "
		if !strings.HasPrefix(gu.Error(), want) || !strings.Contains(gu.Error(), c.says) {
			t.Errorf("%s: message %q, want %q and %q", name, gu.Error(), want, c.says)
		}
		if s.tries() != 6 || len(seqs) != 3 || !slices.Equal(clk.Sleeps(), seconds(1, 1, 2, 4, 8)) {
			t.Errorf("%s: %d tries, %d events, pauses %v, want 6, 3 and no pause after the fifth failure", name, s.tries(), len(seqs), clk.Sleeps())
		}
	}
}

func TestFollow_StopsAtOnceOnA3xxOrA4xx(t *testing.T) {
	for _, status := range []int{401, 404, 302, 400, 418} {
		for _, where := range []string{"stream", "job"} {
			s := &script{t: t, events: []step{answers(status, ``)}, jobs: []step{finished}}
			if where == "job" {
				s.events, s.jobs = []step{sends(frame(1, "a"))}, []step{answers(status, ``)}
			}
			clk := fakeclock.New(epoch)

			_, rest, err := s.follow(clk, 0)

			var gu *GaveUp
			if errors.As(err, &gu) || rest != nil {
				t.Errorf("%d on the %s: Follow = %+v, %v, want it to stop at once", status, where, rest, err)
				continue
			}
			if ae := asError(t, err); ae.Status != status || s.tries() != 1 || len(clk.Sleeps()) != 0 {
				t.Errorf("%d on the %s: %+v after %d tries and the pauses %v, want the error of the answer after one", status, where, ae, s.tries(), clk.Sleeps())
			}
		}
	}
}

func TestFollow_AJobThatIsNotTheJSONExpectedEndsIt(t *testing.T) {
	s := &script{t: t, events: []step{sends(frame(1, "a"))}, jobs: []step{answers(200, `[]`)}}

	_, rest, err := s.follow(fakeclock.New(epoch), 0)

	if e := asError(t, err); rest != nil || e.Category != CategoryService || e.Retryable {
		t.Errorf("Follow = %+v, %+v, want the service error of the decode", rest, e)
	}
}

// stoppingClock is a fake clock that stops the context of the test at the first pause, and
// never ends that pause.
type stoppingClock struct {
	*fakeclock.Clock
	stop context.CancelFunc
}

func (c stoppingClock) After(d time.Duration) <-chan time.Time {
	if d > fakeclock.Quick {
		return c.Clock.After(d)
	}
	c.stop()
	return nil
}

func TestFollow_ReturnsTheErrorOfAStoppedContext(t *testing.T) {
	t.Run("while it reads the stream", func(t *testing.T) {
		s := &script{t: t, events: []step{goesQuiet(frame(1, "a"))}, jobs: []step{stillRunning}}
		ctx, cancel := context.WithCancel(context.Background())

		_, err := s.client(fakeclock.New(epoch)).Follow(ctx, "j", 0, func(Event) { cancel() })

		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want the error of the context", err)
		}
	})
	t.Run("in a pause", func(t *testing.T) {
		s := &script{t: t, events: []step{sends()}, jobs: []step{stillRunning}}
		ctx, cancel := context.WithCancel(context.Background())
		c := s.client(nil)
		c.Clock = stoppingClock{fakeclock.New(epoch), cancel}

		_, err := c.Follow(ctx, "j", 0, func(Event) {})

		if !errors.Is(err, context.Canceled) || s.tries() != 1 {
			t.Errorf("err = %v after %d tries, want the error of the context after one", err, s.tries())
		}
	})
}

// TestFollow_DeliversEveryEventOnceWhateverTheCuts runs a service that holds the events 1 to n,
// cuts the stream at random bytes, repeats older frames, and honours since.
func TestFollow_DeliversEveryEventOnceWhateverTheCuts(t *testing.T) {
	for seed := int64(0); seed < 100; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 1 + rng.Intn(50)
		var delivered atomic.Int64
		var allSent atomic.Bool
		fruitless := 0 // the cuts in a row before a whole new event: five would end the following
		events := func(w http.ResponseWriter, r *http.Request) {
			since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
			if last := delivered.Load(); since != last {
				t.Errorf("seed %d: a request asked for since=%d, but the last event delivered was %d", seed, since, last)
			}
			var body strings.Builder
			for k := since + 1; k <= int64(n); k++ {
				body.WriteString(frame(int(k), "step"))
				for rng.Intn(4) == 0 {
					body.WriteString(frame(1+rng.Intn(int(k)), "step"))
				}
			}
			full := body.String()
			cut := len(full)
			if rng.Intn(3) != 0 {
				cut = rng.Intn(len(full) + 1)
			}
			if first := len(frame(int(since)+1, "step")); cut < first && since < int64(n) {
				if fruitless++; fruitless >= 4 {
					cut, fruitless = first+rng.Intn(len(full)-first+1), 0
				}
			} else {
				fruitless = 0
			}
			f := startStream(w)
			_, _ = io.WriteString(w, full[:cut])
			f.Flush()
			if cut == len(full) {
				allSent.Store(true)
				return
			}
			hangUp(w)
		}
		jobRead := func(w http.ResponseWriter, r *http.Request) {
			if allSent.Load() {
				finished(w, r)
				return
			}
			stillRunning(w, r)
		}
		s := &script{t: t, events: []step{events}, jobs: []step{jobRead}}
		var got []int64

		_, err := s.client(fakeclock.New(epoch)).Follow(context.Background(), "j", 0, func(e Event) {
			got = append(got, e.Seq)
			delivered.Store(e.Seq)
		})

		if err != nil {
			t.Fatalf("seed %d, n %d: %v", seed, n, err)
		}
		for i, seq := range got {
			if seq != int64(i+1) || len(got) != n {
				t.Fatalf("seed %d: delivered %v, want 1 to %d once each in order", seed, got, n)
			}
		}
	}
}
