package afclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/afclient/fakeclock"
)

// step answers one request of a scripted service.
type step func(w http.ResponseWriter, r *http.Request)

// script is a service that answers the event stream and the reading of the job j with the
// step of the number of the request, and the last step again once it has run out.
type script struct {
	t      *testing.T
	mu     sync.Mutex
	events []step
	jobs   []step
	log    []string
}

// serve starts the service and returns a client for it that reads its time from clk.
func (s *script) serve(clk *fakeclock.Clock) *Client {
	s.t.Helper()
	nEvents, nJobs := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		var run step
		switch r.URL.Path {
		case "/api/v1/jobs/j/events":
			s.log = append(s.log, "events since="+r.URL.Query().Get("since"))
			run = s.events[min(nEvents, len(s.events)-1)]
			nEvents++
			if got := r.Header.Get("Accept"); got != "text/event-stream" {
				s.t.Errorf("the events request has Accept %q, want text/event-stream", got)
			}
		case "/api/v1/jobs/j":
			s.log = append(s.log, "job")
			run = s.jobs[min(nJobs, len(s.jobs)-1)]
			nJobs++
			if got := r.Header.Get("Accept"); got != "application/json" {
				s.t.Errorf("the job request has Accept %q, want application/json", got)
			}
		default:
			s.t.Errorf("unexpected request %s", r.URL.Path)
			http.NotFound(w, r)
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		run(w, r)
	}))
	s.t.Cleanup(srv.Close)
	server, err := ParseServer(srv.URL, true, "")
	if err != nil {
		s.t.Fatal(err)
	}
	// Without keep-alive, a hung up connection is a failed request, not one that the transport tries again.
	c := New(server, "tok", "af/test", &http.Transport{DisableKeepAlives: true})
	c.Clock = clk
	return c
}

func (s *script) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.log)
}

// eventDoc is the document of event seq, as the service sends it.
func eventDoc(seq int, typ string) string {
	return fmt.Sprintf(`{"seq":%d,"ts":"2026-10-23T12:00:00.000Z","job_id":"j","attempt":1,"type":%q,"data":{"n":%d}}`, seq, typ, seq)
}

// frame is event seq as one frame of the stream.
func frame(seq int, typ string) string {
	return fmt.Sprintf("id: %d\nevent: %s\ndata: %s\n\n", seq, typ, eventDoc(seq, typ))
}

func startStream(w http.ResponseWriter) http.Flusher {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	f := w.(http.Flusher)
	f.Flush()
	return f
}

// sends writes the frames and ends the stream cleanly.
func sends(frames ...string) step {
	return func(w http.ResponseWriter, r *http.Request) {
		f := startStream(w)
		for _, fr := range frames {
			_, _ = io.WriteString(w, fr)
		}
		f.Flush()
	}
}

// dropsAfter writes the frames and then drops the connection.
func dropsAfter(frames ...string) step {
	return func(w http.ResponseWriter, r *http.Request) {
		f := startStream(w)
		for _, fr := range frames {
			_, _ = io.WriteString(w, fr)
		}
		f.Flush()
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = conn.Close()
		}
	}
}

// hangsUp drops the connection before any answer.
func hangsUp() step {
	return func(w http.ResponseWriter, r *http.Request) {
		if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
			_ = conn.Close()
		}
	}
}

// answers is an answer with a status and a body.
func answers(status int, body string) step {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// goesQuiet writes the frames and then sends nothing until the request ends.
func goesQuiet(frames ...string) step {
	return func(w http.ResponseWriter, r *http.Request) {
		f := startStream(w)
		for _, fr := range frames {
			_, _ = io.WriteString(w, fr)
		}
		f.Flush()
		<-r.Context().Done()
	}
}

// job is the job j in the state, with attempts of the states.
func job(state string, attempts ...string) step {
	var list []string
	for i, a := range attempts {
		list = append(list, fmt.Sprintf(`{"n":%d,"state":%q}`, i+1, a))
	}
	return answers(200, fmt.Sprintf(`{"id":"j","state":%q,"extra":{"kept":true},"attempts":[%s]}`, state, strings.Join(list, ",")))
}

var (
	stillRunning = job("running", "running")
	finished     = job("done", "done")
)

// follow follows the job j of the script from since, and returns what was delivered and how it ended.
func follow(t *testing.T, s *script, clk *fakeclock.Clock, since int64) (events []Event, rest *Rest, err error) {
	t.Helper()
	c := s.serve(clk)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rest, err = c.Follow(ctx, "j", since, func(e Event) { events = append(events, e) })
	return events, rest, err
}

func seqs(events []Event) []int64 {
	var out []int64
	for _, e := range events {
		out = append(out, e.Seq)
	}
	return out
}

var epoch = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

// sameJSON says whether two documents are the same JSON.
func sameJSON(a, b string) bool {
	var x, y any
	return json.Unmarshal([]byte(a), &x) == nil && json.Unmarshal([]byte(b), &y) == nil && reflect.DeepEqual(x, y)
}

// TestTheReaderFramesIDEventAndDataIgnoresCommentsAndUnknownFieldsAndAcceptsMegabyteLines_TS_16_61
// verifies TS-16-61.
// Verifies: 16-REQ-9.1
func TestTheReaderFramesIDEventAndDataIgnoresCommentsAndUnknownFieldsAndAcceptsMegabyteLines_TS_16_61(t *testing.T) {
	pad := strings.Repeat("x", 1<<20-300)
	big := fmt.Sprintf(`{"seq":4,"ts":"t","job_id":"j","attempt":1,"type":"step","data":{"pad":%q}}`, pad)
	split := "data: {\"seq\":3,\"ts\":\"t\",\ndata: \"job_id\":\"j\",\"attempt\":1,\"type\":\"step\",\"data\":{}}\n\n"
	stream := []string{
		": keepalive\n\n",
		"retry: 3000\nid: 1\nevent: job_state\nfoo: bar\ndata: " + eventDoc(1, "job_state") + "\n\n",
		"data: " + eventDoc(2, "step") + "\r\nid: 2\r\nevent: step\r\n\r\n", // CRLF, the data line first
		split,
		": a comment between frames\n\ndata: " + big + "\n\n",
		"event: lonely\nid: 9\n\n",                                           // no data: not an event
		"id: 5\nevent: job_state\ndata:" + eventDoc(5, "job_state") + "\n\n", // no space after the colon
	}
	s := &script{t: t, events: []step{sends(stream...)}, jobs: []step{finished}}
	events, rest, err := follow(t, s, fakeclock.New(epoch), 0)
	if err != nil || rest == nil {
		t.Fatalf("Follow = %v, %v", rest, err)
	}
	if got := seqs(events); !slices.Equal(got, []int64{1, 2, 3, 4, 5}) {
		t.Fatalf("delivered %v, want 1 to 5", got)
	}
	want := []string{eventDoc(1, "job_state"), eventDoc(2, "step"), `{"seq":3,"ts":"t","job_id":"j","attempt":1,"type":"step","data":{}}`, big, eventDoc(5, "job_state")}
	for i, e := range events {
		if !sameJSON(string(e.Document), want[i]) {
			t.Errorf("event %d: document %.80q, want %.80q", i+1, e.Document, want[i])
		}
	}
	if events[3].Type != "step" || events[0].Type != "job_state" {
		t.Errorf("types = %q, %q, want step and job_state", events[3].Type, events[0].Type)
	}
	if got := s.requests(); !slices.Equal(got, []string{"events since=0", "job"}) {
		t.Errorf("requests = %v, want the stream from 0 and the read of the job", got)
	}

	// From --since 5, the first request asks for 5 and the events up to it are not delivered.
	s = &script{t: t, events: []step{sends(frame(4, "step"), frame(5, "step"), frame(6, "step"))}, jobs: []step{finished}}
	events, _, err = follow(t, s, fakeclock.New(epoch), 5)
	if err != nil || !slices.Equal(seqs(events), []int64{6}) || s.requests()[0] != "events since=5" {
		t.Errorf("from 5: delivered %v (%v), requests %v, want 6 only and since=5", seqs(events), err, s.requests())
	}
}

// TestEventsNotAboveTheLastDeliveredAreDropped_TS_16_62 verifies TS-16-62 for the follower.
// Verifies: 16-REQ-9.2
func TestEventsNotAboveTheLastDeliveredAreDropped_TS_16_62(t *testing.T) {
	s := &script{t: t, events: []step{sends(frame(1, "a"), frame(2, "b"), frame(2, "b"), frame(1, "a"), frame(3, "c"))}, jobs: []step{finished}}
	events, _, err := follow(t, s, fakeclock.New(epoch), 0)
	if err != nil || !slices.Equal(seqs(events), []int64{1, 2, 3}) {
		t.Errorf("delivered %v (%v), want 1, 2, 3 once each", seqs(events), err)
	}
}

// TestAStreamEndOfAnyKindIsFollowedByAJobReadAndFollowingEndsOnlyAtRest_TS_16_64 verifies
// TS-16-64.
// Verifies: 16-REQ-9.4
func TestAStreamEndOfAnyKindIsFollowedByAJobReadAndFollowingEndsOnlyAtRest_TS_16_64(t *testing.T) {
	want := []string{"events since=0", "job", "events since=1", "job"}
	for name, first := range map[string]step{
		"end of file":         sends(frame(1, "a")),
		"a dropped stream":    dropsAfter(frame(1, "a")),
		"an unreadable frame": sends(frame(1, "a"), "data: {oops\n\n", frame(2, "ignored")),
	} {
		for jobName, notAtRest := range map[string]step{
			"running":                     stillRunning,
			"done with a running attempt": job("done", "done", "running"),
			"queued":                      job("queued", "queued"),
		} {
			s := &script{t: t, events: []step{first, sends(frame(2, "b"))}, jobs: []step{notAtRest, finished}}
			events, rest, err := follow(t, s, fakeclock.New(epoch), 0)
			if err != nil || rest == nil || !slices.Equal(seqs(events), []int64{1, 2}) {
				t.Errorf("%s, %s: delivered %v, %v, want 1 and 2 (%v)", name, jobName, seqs(events), rest, err)
			}
			if got := s.requests(); !slices.Equal(got, want) {
				t.Errorf("%s, %s: requests = %v, want %v", name, jobName, got, want)
			}
		}
	}

	t.Run("a stream that goes quiet for 60 seconds", func(t *testing.T) {
		clk := fakeclock.New(epoch)
		delivered := make(chan struct{}, 1)
		s := &script{t: t, events: []step{goesQuiet(frame(1, "a")), sends(frame(2, "b"))}, jobs: []step{stillRunning, finished}}
		c := s.serve(clk)
		done := make(chan error, 1)
		var events []Event
		go func() {
			_, err := c.Follow(context.Background(), "j", 0, func(e Event) {
				events = append(events, e)
				if e.Seq == 1 {
					delivered <- struct{}{}
				}
			})
			done <- err
		}()
		select {
		case <-delivered:
		case <-time.After(10 * time.Second):
			t.Fatal("the first event was not delivered")
		}
		clk.Advance(59 * time.Second)
		select {
		case err := <-done:
			t.Fatalf("following ended after 59 seconds of silence: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
		clk.Advance(time.Second)
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("the stream was not cut after 60 seconds without a byte")
		}
		if got := s.requests(); !slices.Equal(got, want) || !slices.Equal(seqs(events), []int64{1, 2}) {
			t.Errorf("requests = %v, delivered %v, want %v and 1, 2", got, seqs(events), want)
		}
	})
}

// failures are the ways a try ends without the job at rest and without a new event: a script of
// the events request and of the job read for each.
var failures = map[string]struct {
	events, job step
	opened      bool // the request for the stream was answered, so the job is read after it
}{
	"end of file":            {sends(), stillRunning, true},
	"a drop":                 {dropsAfter(), stillRunning, true},
	"an unreadable frame":    {sends("data: {oops\n\n"), stillRunning, true},
	"a 5xx on the events":    {answers(503, ``), nil, false},
	"a 500 on the events":    {answers(500, ``), nil, false},
	"a transport error":      {hangsUp(), nil, false},
	"a 5xx on the job read":  {sends(), answers(502, ``), true},
	"a transport error read": {sends(), hangsUp(), true},
}

// TestFailedTriesCountByCauseAndTheDelayBeforeATryIs1_2_4_8SecondsOr1AfterAnEvent_TS_16_65
// verifies TS-16-65.
// Verifies: 16-REQ-9.5
func TestFailedTriesCountByCauseAndTheDelayBeforeATryIs1_2_4_8SecondsOr1AfterAnEvent_TS_16_65(t *testing.T) {
	sec := func(n ...int) []time.Duration {
		var out []time.Duration
		for _, v := range n {
			out = append(out, time.Duration(v)*time.Second)
		}
		return out
	}
	// A try that delivers an event waits 1 s; failed tries wait 1, 2, 4, 8 s; an event resets the count.
	s := &script{t: t,
		events: []step{
			sends(frame(1, "a")), sends(), dropsAfter(), answers(503, ``), sends(),
			sends(frame(2, "b")), sends(), sends(),
		},
		jobs: []step{stillRunning, stillRunning, stillRunning, answers(500, ``), stillRunning, stillRunning, finished}}
	clk := fakeclock.New(epoch)
	events, rest, err := follow(t, s, clk, 0)
	if err != nil || rest == nil || !slices.Equal(seqs(events), []int64{1, 2}) {
		t.Fatalf("Follow = %v (%v), delivered %v", rest, err, seqs(events))
	}
	if got, want := clk.Sleeps(), sec(1, 1, 2, 4, 8, 1, 1); !slices.Equal(got, want) {
		t.Errorf("sleeps = %v, want %v: 1 s after an event, 1, 2, 4, 8 after the failed tries, 1 after the event that reset the count, 1 after the next failure (%v)", got, want, s.requests())
	}

	// Every cause counts as a failed try: two of them in a row wait 1 and 2 seconds.
	for name, f := range failures {
		jobs := []step{finished}
		if f.opened {
			jobs = []step{f.job, f.job, finished}
		}
		s := &script{t: t, events: []step{f.events, f.events, sends(frame(1, "a"))}, jobs: jobs}
		clk := fakeclock.New(epoch)
		if _, _, err := follow(t, s, clk, 0); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if got, want := clk.Sleeps(), sec(1, 2); !slices.Equal(got, want) {
			t.Errorf("%s: sleeps = %v, want %v", name, got, want)
		}
	}

	t.Run("a stream that goes quiet", func(t *testing.T) {
		clk := fakeclock.New(epoch)
		s := &script{t: t, events: []step{goesQuiet(), goesQuiet(), sends(frame(1, "a"))}, jobs: []step{stillRunning, stillRunning, finished}}
		c := s.serve(clk)
		done := make(chan error, 1)
		go func() {
			_, err := c.Follow(context.Background(), "j", 0, func(Event) {})
			done <- err
		}()
		for i := 1; i <= 2; i++ {
			if !clk.WaitForWait(IdleCut, i, 10*time.Second) {
				t.Fatalf("try %d: no idle watch was started", i)
			}
			for deadline := time.Now().Add(10 * time.Second); len(s.requests()) < 2*i-1 && time.Now().Before(deadline); { // the request reached the service
				time.Sleep(time.Millisecond)
			}
			clk.Advance(IdleCut)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("following did not end")
		}
		if got, want := clk.Sleeps(), sec(1, 2); !slices.Equal(got, want) {
			t.Errorf("sleeps = %v, want %v", got, want)
		}
	})
}

// TestTheFifthFailedTryInARowEndsFollowingWithTheLastFailuresCategory_TS_16_66 verifies
// TS-16-66.
// Verifies: 16-REQ-9.6
func TestTheFifthFailedTryInARowEndsFollowingWithTheLastFailuresCategory_TS_16_66(t *testing.T) {
	for name, c := range map[string]struct {
		events   step
		category string
	}{
		"a 502":             {answers(502, `{"error":{"code":"hub_unavailable","message":"hub is down"}}`), CategoryHub},
		"a 503":             {answers(503, ``), CategoryService},
		"a transport error": {hangsUp(), CategoryService},
		"an end of file":    {sends(), CategoryService},
	} {
		s := &script{t: t, events: []step{sends(frame(1, "a"), frame(2, "b"), frame(3, "c")), c.events}, jobs: []step{stillRunning}}
		clk := fakeclock.New(epoch)
		events, rest, err := follow(t, s, clk, 0)
		var gu *GaveUp
		if !errors.As(err, &gu) || rest != nil {
			t.Errorf("%s: Follow = %v, %v, want the end of the following", name, rest, err)
			continue
		}
		if gu.Err.Category != c.category || !gu.Err.Retryable || gu.LastSeq != 3 || gu.JobID != "j" {
			t.Errorf("%s: %+v, want category %s, retryable, last seq 3, job j", name, gu.Err, c.category)
		}
		if m := gu.Err.Message; !strings.Contains(m, "j") || !strings.Contains(m, "may still be running") {
			t.Errorf("%s: message %q, want it to name the job and say it may still be running", name, m)
		}
		var ae *Error
		if !errors.As(err, &ae) {
			t.Errorf("%s: the end of the following does not unwrap to an *Error", name)
		}
		// One delivering try, then five failed ones, and no wait after the fifth.
		requests := s.requests()
		eventsRequests := 0
		for _, r := range requests {
			if strings.HasPrefix(r, "events") {
				eventsRequests++
			}
		}
		if eventsRequests != 6 || len(events) != 3 {
			t.Errorf("%s: %d events requests and %d events, want 6 and 3 (%v)", name, eventsRequests, len(events), requests)
		}
		if got, want := clk.Sleeps(), []time.Duration{time.Second, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}; !slices.Equal(got, want) {
			t.Errorf("%s: sleeps = %v, want %v and none after the fifth failure", name, got, want)
		}
	}
}

// TestA401A404A3xxOrAnother4xxStopsFollowingAtOnce_TS_16_67 verifies TS-16-67.
// Verifies: 16-REQ-9.7
func TestA401A404A3xxOrAnother4xxStopsFollowingAtOnce_TS_16_67(t *testing.T) {
	for _, status := range []int{401, 404, 302, 400, 418} {
		for _, where := range []string{"events", "job"} {
			s := &script{t: t}
			if where == "events" {
				s.events, s.jobs = []step{answers(status, ``)}, []step{finished}
			} else {
				s.events, s.jobs = []step{sends(frame(1, "a"))}, []step{answers(status, ``)}
			}
			clk := fakeclock.New(epoch)
			_, rest, err := follow(t, s, clk, 0)
			var ae *Error
			if !errors.As(err, &ae) || rest != nil || ae.Status != status {
				t.Errorf("%d on the %s: Follow = %v, %v, want the *Error of the answer", status, where, rest, err)
				continue
			}
			var gu *GaveUp
			if errors.As(err, &gu) {
				t.Errorf("%d on the %s: following gave up instead of stopping at once", status, where)
			}
			eventsRequests := 0
			for _, r := range s.requests() {
				if strings.HasPrefix(r, "events") {
					eventsRequests++
				}
			}
			if eventsRequests != 1 || len(clk.Sleeps()) != 0 {
				t.Errorf("%d on the %s: %d tries and sleeps %v, want one try and no wait", status, where, eventsRequests, clk.Sleeps())
			}
		}
	}
}

// TestForAnyPatternOfCutsAndDuplicateFramesEveryEventIsDeliveredExactlyOnceInOrder_TS_16_68
// verifies TS-16-68: a service that holds the events 1 to n cuts the stream at random bytes,
// repeats and reorders frames it has sent, and honours since=k.
// Verifies: 16-REQ-9.8, 16-REQ-9.2
func TestForAnyPatternOfCutsAndDuplicateFramesEveryEventIsDeliveredExactlyOnceInOrder_TS_16_68(t *testing.T) {
	for seed := int64(0); seed < 120; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 1 + rng.Intn(50)
		var delivered atomic.Int64 // the last event the client has delivered
		var allSent atomic.Bool    // the service has sent the last event whole
		var mu sync.Mutex
		var sinces []int64
		fruitless := 0 // the cuts in a row that came before a whole new event

		events := func(w http.ResponseWriter, r *http.Request) {
			since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
			if last := delivered.Load(); since != last {
				t.Errorf("seed %d: a request asked for since=%d, but the last event delivered was %d", seed, since, last)
			}
			mu.Lock()
			sinces = append(sinces, since)
			mu.Unlock()
			var body strings.Builder
			for k := since + 1; k <= int64(n); k++ {
				body.WriteString(frame(int(k), "step"))
				// repeat or reorder frames that are older than the one just written
				for rng.Intn(4) == 0 {
					body.WriteString(frame(1+rng.Intn(int(k)), "step"))
				}
			}
			full := body.String()
			cut := len(full)
			if rng.Intn(3) != 0 { // cut at a random byte, mid-frame as likely as not
				cut = rng.Intn(len(full) + 1)
			}
			// Five failed tries in a row end the following, which is right, so the service lets one of every four through.
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
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = conn.Close()
			}
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
		c := s.serve(fakeclock.New(epoch))
		_, err := c.Follow(context.Background(), "j", 0, func(e Event) {
			got = append(got, e.Seq)
			delivered.Store(e.Seq)
		})
		if err != nil {
			t.Fatalf("seed %d, n %d: %v", seed, n, err)
		}
		want := make([]int64, n)
		for i := range want {
			want[i] = int64(i + 1)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("seed %d: delivered %v, want 1 to %d once each in order", seed, got, n)
		}
		// Every request but the first asks for what the client had delivered by then.
		if sinces[0] != 0 {
			t.Fatalf("seed %d: the first request asked for since=%d", seed, sinces[0])
		}
		if !slices.IsSorted(sinces) {
			t.Fatalf("seed %d: the requests asked for since=%v, want them not to go back", seed, sinces)
		}
	}
}
