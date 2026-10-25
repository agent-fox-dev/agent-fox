package afclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/afclient/fakeclock"
)

// token is the credential of the clients of the tests: long enough to be kept out of messages.
const token = "tok-0123456789"

var epoch = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

// clientAt is a client of the service at url, given by --server, that sends token and the
// User-Agent af/test, and reads its time from clk when clk is not nil. Without keep-alive, a hung
// up connection is a failed request and not one that the transport tries again.
func clientAt(t *testing.T, url string, clk *fakeclock.Clock) *Client {
	t.Helper()
	server, err := ParseServer(url, true, "")
	if err != nil {
		t.Fatal(err)
	}
	c := New(server, token, "af/test", &http.Transport{DisableKeepAlives: true})
	if clk != nil {
		c.Clock = clk
	}
	return c
}

// serve starts a service that h answers and returns a client of it.
func serve(t *testing.T, h step, clk *fakeclock.Clock) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(h))
	t.Cleanup(srv.Close)
	return clientAt(t, srv.URL, clk)
}

// roundTrip is a transport that a function is.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// step answers one request of a scripted service.
type step func(w http.ResponseWriter, r *http.Request)

// answers is an answer with a status and a JSON body.
func answers(status int, body string) step {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// hangUp drops the connection of the request.
func hangUp(w http.ResponseWriter) {
	if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
		_ = conn.Close()
	}
}

// hangsUp drops the connection before any answer.
func hangsUp() step { return func(w http.ResponseWriter, _ *http.Request) { hangUp(w) } }

// startStream answers 200 with the type of an event stream.
func startStream(w http.ResponseWriter) http.Flusher {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	f := w.(http.Flusher)
	f.Flush()
	return f
}

// streams writes the frames, and then ends the stream cleanly, drops the connection, or sends
// nothing more until the request ends.
func streams(end string, frames ...string) step {
	return func(w http.ResponseWriter, r *http.Request) {
		f := startStream(w)
		_, _ = io.WriteString(w, strings.Join(frames, ""))
		f.Flush()
		switch end {
		case "drop":
			hangUp(w)
		case "quiet":
			<-r.Context().Done()
		}
	}
}

func sends(frames ...string) step      { return streams("eof", frames...) }
func dropsAfter(frames ...string) step { return streams("drop", frames...) }
func goesQuiet(frames ...string) step  { return streams("quiet", frames...) }

// eventDoc is the document of event seq of the job j, as the service sends it.
func eventDoc(seq int, typ string) string {
	return fmt.Sprintf(`{"seq":%d,"ts":"2026-10-23T12:00:00.000Z","job_id":"j","attempt":1,"type":%q,"data":{"n":%d}}`, seq, typ, seq)
}

// frame is event seq as one frame of the stream.
func frame(seq int, typ string) string {
	return fmt.Sprintf("id: %d\nevent: %s\ndata: %s\n\n", seq, typ, eventDoc(seq, typ))
}

// job is the job j in the state, with attempts 1, 2, ... in the states.
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

// script is a service that answers the nth request for the event stream of the job j, and the
// nth read of the job, with the nth step of each, and the last step again once they run out.
type script struct {
	t            *testing.T
	events, jobs []step

	mu  sync.Mutex
	log []string
}

// client starts the service and returns a client of it that reads its time from clk.
func (s *script) client(clk *fakeclock.Clock) *Client {
	nEvents, nJobs := 0, 0
	return serve(s.t, func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		var run step
		accept := "application/json"
		switch r.URL.Path {
		case "/api/v1/jobs/j/events":
			s.log = append(s.log, "events since="+r.URL.Query().Get("since"))
			run, accept = s.events[min(nEvents, len(s.events)-1)], "text/event-stream"
			nEvents++
		case "/api/v1/jobs/j":
			s.log = append(s.log, "job")
			run = s.jobs[min(nJobs, len(s.jobs)-1)]
			nJobs++
		default:
			run = answers(404, ``)
			s.t.Errorf("unexpected request %s", r.URL.Path)
		}
		s.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("Accept") != accept {
			s.t.Errorf("%s carries Authorization %q and Accept %q, want the bearer and %s", r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Accept"), accept)
		}
		run(w, r)
	}, clk)
}

// requests are the requests the service has seen, in order.
func (s *script) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.log)
}

// tries is how many times the stream was asked for.
func (s *script) tries() int {
	n := 0
	for _, r := range s.requests() {
		if strings.HasPrefix(r, "events") {
			n++
		}
	}
	return n
}

// follow follows the job j of the script from since, and returns the numbers of the events
// delivered and how it ended.
func (s *script) follow(clk *fakeclock.Clock, since int64) ([]int64, *Rest, error) {
	var seqs []int64
	rest, err := s.client(clk).Follow(context.Background(), "j", since, func(e Event) { seqs = append(seqs, e.Seq) })
	return seqs, rest, err
}

// seconds are the durations of n seconds.
func seconds(n ...int) []time.Duration {
	var out []time.Duration
	for _, v := range n {
		out = append(out, time.Duration(v)*time.Second)
	}
	return out
}

// asError is err as an *Error, and fails the test when it is not one.
func asError(t *testing.T, err error) *Error {
	t.Helper()
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("err = %v (%T), want an *Error", err, err)
	}
	return e
}
