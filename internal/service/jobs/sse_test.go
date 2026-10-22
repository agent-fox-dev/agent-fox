package jobs_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// frame is a block of an event stream as it was sent: a comment, or the three lines of an
// event.
type frame struct {
	ID, Event, Data string
	Comment         string
}

func (f frame) isComment() bool { return f.Comment != "" }

// event decodes the data of a frame, and checks that the id and the event line say what the
// data says.
func (f frame) event(t *testing.T) jobs.Event {
	t.Helper()
	var ev jobs.Event
	if err := json.Unmarshal([]byte(f.Data), &ev); err != nil {
		t.Fatalf("the data of a frame is not an event: %v\n%s", err, f.Data)
	}
	if f.ID != strconv.FormatInt(ev.Seq, 10) || f.Event != ev.Type {
		t.Errorf("frame id %q, event %q, but the data says seq %d, type %q", f.ID, f.Event, ev.Seq, ev.Type)
	}
	return ev
}

// atRest reports whether the frame is a job_state that says the job is at rest.
func (f frame) atRest(t *testing.T) bool {
	t.Helper()
	if f.Event != jobs.EventJobState {
		return false
	}
	var data struct {
		AtRest bool `json:"at_rest"`
	}
	if err := json.Unmarshal([]byte(f.event(t).Data), &data); err != nil {
		t.Fatal(err)
	}
	return data.AtRest
}

// A line of data is the event on one line, with its keys in the order the spec names them.
var dataLine = regexp.MustCompile(`^\{"seq":[0-9]+,"ts":"[^"\n]+","job_id":"job_[0-9a-f]{20}","attempt":[0-9]+,"type":"[a-z][a-z0-9_]*","data":.*\}$`)

// stream is an open event stream, read a frame at a time.
type stream struct {
	t      *testing.T
	Status int
	Header http.Header
	Body   []byte // what was not a stream: the body of an answer that was not 200
	frames chan frame
	cancel context.CancelFunc
	mu     sync.Mutex
	odd    []string // blocks of the stream that are neither a comment nor an event
	cut    bool     // the stream ended in the middle of a block
	done   chan struct{}
}

var errTimeout = errors.New("no frame in time")

// open opens the event stream of the job at the url, with the headers, as the caller.
func (e *env) open(url string, headers map[string]string) *stream {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		cancel()
		e.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+callerToken)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		e.t.Fatalf("GET %s: %v", url, err)
	}
	s := &stream{t: e.t, Status: res.StatusCode, Header: res.Header, frames: make(chan frame, 1<<16), cancel: cancel, done: make(chan struct{})}
	e.t.Cleanup(cancel)
	go s.read(res.Body)
	return s
}

// stream opens the event stream of the job on the API of the test, with since if it is not "".
func (e *env) stream(id, since string, headers map[string]string) *stream {
	path := e.URL + "/api/v1/jobs/" + id + "/events"
	if since != "" {
		path += "?since=" + since
	}
	return e.open(path, headers)
}

// read parses the body into frames until it ends.
func (s *stream) read(body io.ReadCloser) {
	defer close(s.done)
	defer close(s.frames)
	defer body.Close()
	if !strings.HasPrefix(s.Header.Get("Content-Type"), "text/event-stream") {
		s.Body, _ = io.ReadAll(body)
		return
	}
	br := bufio.NewReader(body)
	var block []string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			s.mu.Lock()
			s.cut = len(block) > 0 || line != ""
			s.mu.Unlock()
			return
		}
		line = strings.TrimSuffix(line, "\n")
		if line != "" {
			block = append(block, line)
			continue
		}
		switch {
		case len(block) == 1 && strings.HasPrefix(block[0], ": "):
			s.frames <- frame{Comment: block[0]}
		case len(block) == 3 && strings.HasPrefix(block[0], "id: ") && strings.HasPrefix(block[1], "event: ") && strings.HasPrefix(block[2], "data: "):
			s.frames <- frame{ID: block[0][4:], Event: block[1][7:], Data: block[2][6:]}
		default:
			s.mu.Lock()
			s.odd = append(s.odd, strings.Join(block, "\\n"))
			s.mu.Unlock()
		}
		block = nil
	}
}

// next is the next frame, or io.EOF when the stream has ended, or errTimeout.
func (s *stream) next(d time.Duration) (frame, error) {
	select {
	case f, ok := <-s.frames:
		if !ok {
			return frame{}, io.EOF
		}
		return f, nil
	case <-time.After(d):
		return frame{}, errTimeout
	}
}

// all reads the stream to its end, which has to come within d, and returns the frames. A
// stream that was cut in the middle of a frame, or had a block that is no frame, fails the test.
func (s *stream) all(d time.Duration) []frame {
	s.t.Helper()
	var out []frame
	deadline := time.After(d)
	for {
		select {
		case f, ok := <-s.frames:
			if !ok {
				s.check()
				return out
			}
			out = append(out, f)
		case <-deadline:
			s.t.Fatalf("the stream did not end within %v; it had %d frames", d, len(out))
		}
	}
}

// check fails the test if the stream held anything but comments and events.
func (s *stream) check() {
	s.t.Helper()
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.odd) > 0 || s.cut {
		s.t.Errorf("the stream has blocks that are not frames: %q (cut in the middle of one: %v)", s.odd, s.cut)
	}
}

// close ends the stream from the client's side.
func (s *stream) close() { s.cancel() }

// events are the events among the frames, each checked against its own id line.
func events(t *testing.T, frames []frame) []jobs.Event {
	t.Helper()
	var out []jobs.Event
	for _, f := range frames {
		if !f.isComment() {
			out = append(out, f.event(t))
		}
	}
	return out
}

// seqs are the numbers of the events.
func seqs(evs []jobs.Event) []int64 {
	out := make([]int64, 0, len(evs))
	for _, ev := range evs {
		out = append(out, ev.Seq)
	}
	return out
}

// rangeOf is the numbers from..to, both included.
func rangeOf(from, to int64) []int64 {
	out := []int64{}
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

// finishedJob makes a job whose attempt reports four events of its own and is done, and
// returns it once it is at rest.
func finishedJob(t *testing.T, e *env, issue int) jobs.Doc {
	t.Helper()
	job := e.create("widgets", jobs.KindFix, issue)
	e.Start()
	return e.waitRest(job.ID)
}

// emitting is a step of a runner that reports n events, "step" with the number in the data, and
// is done.
func emitting(n int) fakerunner.Step {
	return fakerunner.Do(func(_ context.Context, req jobs.Request) (jobs.Result, error) {
		for i := 1; i <= n; i++ {
			req.Recorder.Emit("step", map[string]int{"i": i})
		}
		return jobs.Result{Envelope: fakerunner.Env(toolio.StatusDone)}, nil
	})
}

// TestTheEventsRouteStreamsTheStoredEventsAfterSinceOrLastEventIDOrZero_TS_12_67 verifies
// TS-12-67: for a job that is done and has its events 1..8, the stream answers 200 text/event-stream
// with Cache-Control: no-cache, in frames of id, event and data on one line; it starts after
// ?since, else after Last-Event-ID, else at the beginning; since that is not an integer of at
// least 0 is 400; an unknown job and a job the caller cannot see are the 404 of 12-REQ-9.7.
// Verifies: 12-REQ-10.4
func TestTheEventsRouteStreamsTheStoredEventsAfterSinceOrLastEventIDOrZero_TS_12_67(t *testing.T) {
	e := newEnv(t, withRunner(fakerunner.New(emitting(3))))
	job := finishedJob(t, e, 1)
	stored := e.events(job.ID)
	k := int64(len(stored))
	if k != 8 {
		t.Fatalf("the job has %d events (%v), want the 8 of the spec: queued, running, attempt_start, 3 steps, attempt_end, job_state done", k, seqs(stored))
	}

	for _, tc := range []struct {
		name    string
		since   string
		headers map[string]string
		want    []int64
	}{
		{"since=3", "3", nil, rangeOf(4, k)},
		{"Last-Event-ID: 5", "", map[string]string{"Last-Event-ID": "5"}, rangeOf(6, k)},
		{"since=2 beats Last-Event-ID: 6", "2", map[string]string{"Last-Event-ID": "6"}, rangeOf(3, k)},
		{"neither", "", nil, rangeOf(1, k)},
		{"since=0", "0", nil, rangeOf(1, k)},
		{"since=0 beats Last-Event-ID: 6", "0", map[string]string{"Last-Event-ID": "6"}, rangeOf(1, k)},
		{"since the last", fmt.Sprint(k), nil, []int64{}},
		{"since past the last", "100", nil, []int64{}},
	} {
		s := e.stream(job.ID, tc.since, tc.headers)
		if s.Status != 200 {
			t.Errorf("%s: status %d", tc.name, s.Status)
			continue
		}
		if ct, cc := s.Header.Get("Content-Type"), s.Header.Get("Cache-Control"); ct != "text/event-stream" || cc != "no-cache" {
			t.Errorf("%s: Content-Type %q, Cache-Control %q, want text/event-stream and no-cache", tc.name, ct, cc)
		}
		frames := s.all(10 * time.Second)
		got := events(t, frames)
		if !slices.Equal(seqs(got), tc.want) {
			t.Errorf("%s: the stream carried %v, want %v", tc.name, seqs(got), tc.want)
		}
		if len(frames) != len(got) {
			t.Errorf("%s: %d frames for %d events: a stream that ends at the end of the replay has nothing else", tc.name, len(frames), len(got))
		}
		for i, f := range frames {
			ev := stored[got[i].Seq-1]
			if !dataLine.MatchString(f.Data) || strings.Contains(f.Data, "\n") {
				t.Errorf("%s: the data %q is not one line of {seq, ts, job_id, attempt, type, data}", tc.name, f.Data)
			}
			if got[i].TS != ev.TS || got[i].JobID != job.ID || got[i].Attempt != ev.Attempt || got[i].Type != ev.Type || string(got[i].Data) != string(ev.Data) {
				t.Errorf("%s: event %d = %+v, want the stored %+v", tc.name, ev.Seq, got[i], ev)
			}
		}
	}

	// A since that is not an integer of at least 0 is refused, and so is a header like it.
	for _, query := range []string{"-1", "x", "", "1.5", "%2B1", "01", "99999999999999999999", "1&since=2", "0x10", "%201"} {
		r := e.call("GET", "/api/v1/jobs/"+job.ID+"/events?since="+query, nil)
		if code, message := r.errorOf(t); r.Status != 400 || code != "invalid_request" || !strings.Contains(message, "since") {
			t.Errorf("since=%s: %d %s %q, want 400 invalid_request naming since", query, r.Status, code, message)
		}
	}
	req := func(header string) response {
		return e.callWith("GET", "/api/v1/jobs/"+job.ID+"/events", map[string]string{"Last-Event-ID": header})
	}
	for _, header := range []string{"abc", "-1", "1.5", "5 5"} {
		if code, message := req(header).errorOf(t); !strings.Contains(message, "Last-Event-ID") || code != "invalid_request" {
			t.Errorf("Last-Event-ID %q: %s %q, want invalid_request naming the header", header, code, message)
		}
	}

	// 404, the same as for any file of a job; the guard comes before the parameters.
	e.subscribe("secret")
	hidden := e.createDirect("secret", jobs.KindFix, 7)
	unknown := e.call("GET", "/api/v1/jobs/job_00000000000000000000/events", nil)
	invisible := e.call("GET", "/api/v1/jobs/"+hidden.ID+"/events", nil)
	badSince := e.call("GET", "/api/v1/jobs/"+hidden.ID+"/events?since=x", nil)
	for name, r := range map[string]response{"unknown": unknown, "invisible": invisible, "invisible with a bad since": badSince} {
		if code, _ := r.errorOf(t); r.Status != 404 || code != "not_found" || string(r.Body) != string(unknown.Body) {
			t.Errorf("%s job: %d %s, want the 404 of an unknown job: %s", name, r.Status, r.Body, unknown.Body)
		}
	}
	if same := e.call("GET", "/api/v1/jobs/job_00000000000000000000", nil); string(same.Body) != string(unknown.Body) {
		t.Errorf("the 404 of the events is %s, and of the job %s, want one answer", unknown.Body, same.Body)
	}
	if r := e.callAs("", "GET", "/api/v1/jobs/"+job.ID+"/events", nil); r.Status != 401 {
		t.Errorf("without a token: %d, want 401", r.Status)
	}
	if r := e.call("POST", "/api/v1/jobs/"+job.ID+"/events", nil); r.Status != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d, want 405", r.Status)
	}
}

// callWith makes a request as the caller with more headers.
func (e *env) callWith(method, path string, headers map[string]string) response {
	e.t.Helper()
	req, err := http.NewRequest(method, e.URL+path, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+callerToken)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return response{Status: res.StatusCode, Header: res.Header, Body: raw}
}

// TestAStreamHasNoMissingAndNoRepeatedEventWhateverTheInterleavingAndAReconnectResumes_TS_12_68
// verifies TS-12-68: while a runner reports events, subscribers connect at seeded times and with
// seeded starting points, and some of them are cut and reconnect with the last id they saw; each
// receives exactly the events after its starting point, once and in order, and a cut stream and
// its reconnection together are the whole tail of the log.
// Verifies: 12-REQ-10.5
func TestAStreamHasNoMissingAndNoRepeatedEventWhateverTheInterleavingAndAReconnectResumes_TS_12_68(t *testing.T) {
	for seed := int64(1); seed <= 4; seed++ {
		t.Run(fmt.Sprintf("seed %d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed * 6812))
			n := 25 + rng.Intn(25)
			permits := make(chan struct{})
			r := fakerunner.New(fakerunner.Do(func(ctx context.Context, req jobs.Request) (jobs.Result, error) {
				for i := 1; i <= n; i++ {
					select {
					case <-permits:
					case <-ctx.Done():
						return jobs.Result{}, context.Cause(ctx)
					}
					req.Recorder.Emit("step", map[string]int{"i": i})
				}
				return jobs.Result{Envelope: fakerunner.Env(toolio.StatusDone)}, nil
			}))
			e := newEnv(t, withRunner(r))
			job := e.create("widgets", jobs.KindFix, 1)
			e.Start()
			waitCalls(t, r, 1)

			type outcome struct {
				since int64
				cut   int // frames read before the cut; 0: not cut
				got   []int64
				again []int64
			}
			var (
				wg      sync.WaitGroup
				mu      sync.Mutex
				results []*outcome
			)
			subscribe := func() {
				stored := int64(len(e.events(job.ID)))
				o := &outcome{since: rng.Int63n(stored + 1)}
				if rng.Intn(2) == 0 {
					o.cut = 1 + rng.Intn(10)
				}
				mu.Lock()
				results = append(results, o)
				mu.Unlock()
				wg.Add(1)
				go func() {
					defer wg.Done()
					s := e.stream(job.ID, fmt.Sprint(o.since), nil)
					if o.cut == 0 {
						o.got = seqs(events(t, s.all(30*time.Second)))
						return
					}
					// Read some of it, then go away, and come back with the last id that was seen.
					last := o.since
					for i := 0; i < o.cut; i++ {
						f, err := s.next(10 * time.Second)
						if err != nil {
							break // the stream was shorter than the cut: it ended, or the job did
						}
						if !f.isComment() {
							o.got = append(o.got, f.event(t).Seq)
							last = o.got[len(o.got)-1]
						}
					}
					s.close()
					again := e.stream(job.ID, "", map[string]string{"Last-Event-ID": fmt.Sprint(last)})
					o.again = seqs(events(t, again.all(30*time.Second)))
				}()
			}

			for i := 1; i <= n; i++ {
				for range rng.Intn(3) {
					subscribe()
				}
				select {
				case permits <- struct{}{}:
				case <-time.After(10 * time.Second):
					t.Fatalf("the runner did not take the permit to report event %d of %d", i, n)
				}
				if rng.Intn(3) == 0 {
					time.Sleep(time.Duration(rng.Intn(3)) * time.Millisecond)
				}
			}
			e.waitRest(job.ID)
			subscribe() // after the end: the replay alone
			wg.Wait()

			total := int64(len(e.events(job.ID)))
			for _, o := range results {
				// An early close may drop frames that were read from the wire but not by the test, so the
				// reconnection starts at the last id the test took; the cut stream's own events are a
				// prefix of the tail, and with the reconnection they are all of it.
				all := append(slices.Clone(o.got), o.again...)
				if o.cut == 0 {
					all = o.got
				}
				if want := rangeOf(o.since+1, total); !slices.Equal(all, want) {
					t.Errorf("since %d (cut after %d frames): received %v, want %v", o.since, o.cut, all, want)
				}
			}
		})
	}
}

// TestAStreamEndsAtTheEndOfTheReplayWhenTheJobIsAtRestElseAfterTheFirstLiveAtRestJobStateAndKeepsAlive_TS_12_69
// verifies TS-12-69: the stream of a done job ends at the end of its replay with a job_state at
// rest as its last frame; the stream of a job that is not at rest stays open past an old job_state
// at rest in its replay and past a live one that is not at rest, sends ": keepalive" every 15 s of
// the clock, and ends after the first live job_state that is at rest and not before.
// Verifies: 12-REQ-10.6
func TestAStreamEndsAtTheEndOfTheReplayWhenTheJobIsAtRestElseAfterTheFirstLiveAtRestJobStateAndKeepsAlive_TS_12_69(t *testing.T) {
	clk := fakeclock.New(epoch)
	r := fakerunner.New(
		emitting(2), // job 1: done
		question(),  // job 2, attempt 1: blocked
		fakerunner.BlockIgnoringContext(fakerunner.Step{}), // job 2, attempt 2: held
	)
	e := newEnv(t, withRunner(r), withClock(clk))
	first := e.create("widgets", jobs.KindFix, 1)
	second := e.create("widgets", jobs.KindFix, 2)
	e.Start()
	e.waitRest(first.ID)
	e.waitState(second.ID, jobs.StateBlocked)

	// A job at rest: the replay, and the end.
	done := e.stream(first.ID, "", nil).all(10 * time.Second)
	if len(done) == 0 || !done[len(done)-1].atRest(t) {
		t.Fatalf("the last frame of a done job's stream = %+v, want a job_state with at_rest true", done)
	}
	for _, f := range done {
		if f.isComment() {
			t.Errorf("a stream that ended at the end of its replay has a comment: %s", f.Comment)
		}
	}
	// A blocked job is at rest as well.
	if blocked := e.stream(second.ID, "", nil).all(10 * time.Second); !blocked[len(blocked)-1].atRest(t) {
		t.Errorf("the last frame of a blocked job's stream is not a job_state at rest")
	}

	// Answered: the log has a job_state at rest in the middle of it, and the job is running.
	if resp := e.answer(second.ID, map[string]string{"option": "A"}); resp.Status != 202 {
		t.Fatalf("answer: %d %s", resp.Status, resp.Body)
	}
	e.waitState(second.ID, jobs.StateRunning)
	tickers := clk.ActiveTickers()
	s := e.stream(second.ID, "", nil)
	replayed := 0
	var sawRest bool
	for {
		f, err := s.next(2 * time.Second)
		if err != nil {
			t.Fatalf("the replay stopped after %d frames: %v", replayed, err)
		}
		replayed++
		sawRest = sawRest || f.atRest(t)
		if ev := f.event(t); ev.Type == jobs.EventAttemptStart && ev.Attempt == 2 {
			break
		}
	}
	if !sawRest {
		t.Fatal("the replay holds no job_state at rest (the question of attempt 1), so the test proves nothing")
	}
	if _, err := s.next(300 * time.Millisecond); !errors.Is(err, errTimeout) {
		t.Fatalf("after the replay: %v, want the stream to stay open although it holds a job_state at rest", err)
	}

	// Keepalive: every 15 s of the clock, and not before.
	if !clk.WaitForActiveTickers(tickers+1, 5*time.Second) {
		t.Fatal("the stream made no ticker")
	}
	clk.Advance(14 * time.Second)
	if f, err := s.next(200 * time.Millisecond); !errors.Is(err, errTimeout) {
		t.Fatalf("after 14 s: %+v %v, want nothing", f, err)
	}
	clk.Advance(time.Second)
	if f, err := s.next(5 * time.Second); err != nil || f.Comment != ": keepalive" {
		t.Fatalf("after 15 s: %+v %v, want ': keepalive'", f, err)
	}
	clk.Advance(15 * time.Second)
	if f, err := s.next(5 * time.Second); err != nil || f.Comment != ": keepalive" {
		t.Fatalf("after 30 s: %+v %v, want a second ': keepalive'", f, err)
	}

	// A live job_state that is not at rest does not end it: the job is cancelled, the runner is held.
	if resp := e.call("POST", "/api/v1/jobs/"+second.ID+"/cancel", nil); resp.Status != 200 {
		t.Fatalf("cancel: %d %s", resp.Status, resp.Body)
	}
	f, err := s.next(5 * time.Second)
	if err != nil || f.Event != jobs.EventJobState || f.atRest(t) {
		t.Fatalf("after the cancel: %+v %v, want a job_state that is not at rest", f, err)
	}
	if f, err := s.next(300 * time.Millisecond); !errors.Is(err, errTimeout) {
		t.Fatalf("after a job_state that is not at rest: %+v %v, want the stream open", f, err)
	}
	// The runner returns: the attempt ends, and the job_state at rest ends the stream.
	r.ReleaseAll()
	var rest []frame
	for {
		f, err := s.next(5 * time.Second)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("after the release: %v", err)
			}
			break
		}
		rest = append(rest, f)
	}
	if len(rest) == 0 || !rest[len(rest)-1].atRest(t) {
		t.Fatalf("the frames after the release = %+v, want them to end with a job_state at rest", rest)
	}
	for _, f := range rest[:len(rest)-1] {
		if !f.isComment() && f.atRest(t) {
			t.Errorf("a job_state at rest came before the end of the stream: %+v", f)
		}
	}
	s.check()
}

// stalledWriter is the response of a client that does not read: while it is gated, a write
// does not return. What it has taken is what the client has seen.
type stalledWriter struct {
	mu     sync.Mutex
	cond   *sync.Cond
	gated  bool
	header http.Header
	status int
	body   bytes.Buffer
}

func newStalledWriter() *stalledWriter {
	w := &stalledWriter{header: http.Header{}}
	w.cond = sync.NewCond(&w.mu)
	return w
}

func (w *stalledWriter) Header() http.Header { return w.header }
func (w *stalledWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = code
}
func (w *stalledWriter) Flush() {}
func (w *stalledWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for w.gated {
		w.cond.Wait()
	}
	w.body.Write(p)
	return len(p), nil
}

// gate makes the next write wait, and open lets the writes through.
func (w *stalledWriter) gate() { w.mu.Lock(); w.gated = true; w.mu.Unlock() }
func (w *stalledWriter) open() { w.mu.Lock(); w.gated = false; w.mu.Unlock(); w.cond.Broadcast() }

// seen are the events and the comments taken so far.
func (w *stalledWriter) seen() (events []int64, keepalives int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, block := range strings.Split(w.body.String(), "\n\n") {
		switch {
		case strings.HasPrefix(block, ": keepalive"):
			keepalives++
		case strings.HasPrefix(block, "id: "):
			id, _, _ := strings.Cut(strings.TrimPrefix(block, "id: "), "\n")
			n, _ := strconv.ParseInt(id, 10, 64)
			events = append(events, n)
		}
	}
	return events, keepalives
}

// TestASubscriberMoreThan256EventsBehindIsDisconnectedWithoutBlockingTheRunnerAndRecovers_TS_12_70
// verifies TS-12-70: a client that does not read, a second that does, and a runner that reports 1000
// events; the runner is never held up and the second client receives every event; the first client's
// stream is closed, after the events it had room for and without the end; and reconnecting with the
// last id it took receives exactly the events it missed.
// Verifies: 12-REQ-10.7
func TestASubscriberMoreThan256EventsBehindIsDisconnectedWithoutBlockingTheRunnerAndRecovers_TS_12_70(t *testing.T) {
	clk := fakeclock.New(epoch)
	start := make(chan struct{})
	r := fakerunner.New(fakerunner.Do(func(ctx context.Context, req jobs.Request) (jobs.Result, error) {
		select {
		case <-start:
		case <-ctx.Done():
			return jobs.Result{}, context.Cause(ctx)
		}
		for i := 1; i <= 1000; i++ {
			req.Recorder.Emit("tick", map[string]int{"i": i})
		}
		return jobs.Result{Envelope: fakerunner.Env(toolio.StatusDone)}, nil
	}))
	e := newEnv(t, withRunner(r), withClock(clk))
	job := e.create("widgets", jobs.KindFix, 1)
	e.Start()
	waitCalls(t, r, 1)

	healthy := e.stream(job.ID, "", nil)
	stalled := newStalledWriter()
	req := httptest.NewRequest("GET", "/api/v1/jobs/"+job.ID+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+callerToken)
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		e.Srv.Handler.ServeHTTP(stalled, req)
	}()

	// The stalled client is in the live part of its stream once a keepalive has reached it.
	waitFor(t, "a keepalive to reach the client", func() bool {
		clk.Advance(15 * time.Second)
		time.Sleep(5 * time.Millisecond)
		_, keepalives := stalled.seen()
		return keepalives > 0
	})
	before, _ := stalled.seen()
	stalled.gate()

	began := time.Now()
	close(start)
	deadline := time.Now().Add(2 * time.Minute)
	for e.get(job.ID).State != jobs.StateDone {
		if time.Now().After(deadline) {
			t.Fatal("the runner was held up: the job is not done 2 minutes after it began to report")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Logf("1000 events reported in %v with a client that does not read", time.Since(began))

	stored := e.events(job.ID)
	total := int64(len(stored))
	if got := seqs(events(t, healthy.all(30*time.Second))); !slices.Equal(got, rangeOf(1, total)) {
		t.Errorf("the client that reads received %d events, want all %d", len(got), total)
	}

	stalled.open()
	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("the stream of the client that fell behind was not closed")
	}
	taken, _ := stalled.seen()
	if len(taken) == 0 || int64(len(taken)) >= total || !slices.Equal(taken, rangeOf(1, int64(len(taken)))) {
		t.Fatalf("the client that fell behind took %d events %v.. of %d, want a prefix of the log that stops short", len(taken), taken[:min(len(taken), 5)], total)
	}
	// It took the 256 that were waiting for it and the one it had in hand (or the keepalive that it
	// had in hand), and was cut after those.
	if got, low := len(taken), len(before)+jobs.SubscriberBuffer; got < low || got > low+1 {
		t.Errorf("the client that fell behind took %d events, want %d or %d: the %d it had before it stopped reading and %d more", got, low, low+1, len(before), jobs.SubscriberBuffer)
	}
	if last := stalled.body.String(); strings.Contains(last[max(0, len(last)-400):], `"at_rest":true`) {
		t.Error("the stream that was cut ends with the job_state at rest: it was not cut")
	}

	// The cut is logged, once, as the service letting a client go and not as a failure.
	if logged := e.Logs.find("INFO", "fell behind"); len(logged) != 1 || logged[0]["job"] != job.ID {
		t.Errorf("records of a client that fell behind = %v, want one at INFO naming the job", logged)
	}
	if bad := append(e.Logs.find("WARN", "event stream"), e.Logs.find("ERROR", "event stream")...); len(bad) != 0 {
		t.Errorf("the streams that ended logged %v, want nothing above INFO", bad)
	}

	last := taken[len(taken)-1]
	again := e.stream(job.ID, "", map[string]string{"Last-Event-ID": fmt.Sprint(last)})
	if got := seqs(events(t, again.all(30*time.Second))); !slices.Equal(got, rangeOf(last+1, total)) {
		t.Errorf("reconnecting with %d received %d events, want exactly the %d that were missed", last, len(got), total-last)
	}
}

// TestShuttingDownTheServerOrTheServiceClosesEveryStream_TS_12_70 verifies the second half of
// TS-12-70: with streams open on a job that is running, the shutdown of the HTTP server returns
// at once and every stream is closed; so does the close of the jobs service.
// Verifies: 12-REQ-10.7
func TestShuttingDownTheServerOrTheServiceClosesEveryStream_TS_12_70(t *testing.T) {
	setup := func(t *testing.T) (*env, string) {
		r := fakerunner.New(fakerunner.Block(fakerunner.Step{}))
		e := newEnv(t, withRunner(r))
		job := e.create("widgets", jobs.KindFix, 1)
		e.Start()
		waitCalls(t, r, 1)
		e.waitState(job.ID, jobs.StateRunning)
		return e, job.ID
	}
	expectClosed := func(t *testing.T, streams []*stream) {
		t.Helper()
		for i, s := range streams {
			for {
				_, err := s.next(5 * time.Second)
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Errorf("stream %d: %v, want it closed", i, err)
					break
				}
			}
		}
	}
	open := func(e *env, base, id string, n int) []*stream {
		var streams []*stream
		for range n {
			s := e.open(base+"/api/v1/jobs/"+id+"/events", nil)
			if s.Status != 200 {
				t.Fatalf("stream: %d", s.Status)
			}
			// It has its replay, and is waiting for more.
			for {
				f, err := s.next(5 * time.Second)
				if err != nil {
					t.Fatalf("replay: %v", err)
				}
				if f.Event == jobs.EventAttemptStart {
					break
				}
			}
			streams = append(streams, s)
		}
		return streams
	}

	t.Run("the HTTP server shuts down", func(t *testing.T) {
		e, id := setup(t)
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		served := make(chan error, 1)
		go func() { served <- e.Srv.Serve(ln) }()
		streams := open(e, "http://"+ln.Addr().String(), id, 3)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		began := time.Now()
		if err := e.Srv.Shutdown(ctx); err != nil {
			t.Fatalf("Shutdown with three streams open: %v after %v", err, time.Since(began))
		}
		expectClosed(t, streams)
		if err := <-served; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("Serve = %v", err)
		}
	})
	t.Run("the jobs service closes", func(t *testing.T) {
		e, id := setup(t)
		streams := open(e, e.URL, id, 3)
		if err := e.Svc.Close(); err != nil {
			t.Fatal(err)
		}
		expectClosed(t, streams)
		// A stream opened afterwards has its replay and ends: there is nothing to wait for.
		late := e.stream(id, "", nil)
		if late.Status != 200 {
			t.Fatalf("a stream after the close: %d", late.Status)
		}
		late.all(5 * time.Second)
	})
}
