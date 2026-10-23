package afclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/afclient/fakeclock"
)

// TestTheClientFollowsAJobThroughACutAndFetchesItsEnvelope_TS_16_81 verifies, for the library, the
// path of TS-16-81 and 16-PATH-6 end to end with a real HTTP server, the real framing, the real client and
// the clock of the tests: the stream is cut after three events, the job is read and is not at rest, the
// follower pauses one second and asks again from the third event, a duplicate is dropped, the job is read
// at rest, and the envelope of its last finished attempt is fetched and returned as stored.
// Verifies: 16-PATH-6, 16-REQ-9.8, 16-REQ-10.1
func TestTheClientFollowsAJobThroughACutAndFetchesItsEnvelope_TS_16_81(t *testing.T) {
	var mu sync.Mutex
	var log []string
	streams := 0
	const stored = `{"status":"done","result":{"fixed":true},"warnings":[]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		log = append(log, r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/events"):
			mu.Lock()
			streams++
			n := streams
			mu.Unlock()
			f := startStream(w)
			if n == 1 {
				_, _ = w.Write([]byte(frame(1, "step") + frame(2, "step") + frame(3, "step")))
				f.Flush()
				if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
					_ = conn.Close()
				}
				return
			}
			_, _ = w.Write([]byte(frame(3, "step") + frame(4, "step") + frame(5, "job_state")))
		case strings.HasSuffix(r.URL.Path, "/attempts/2/envelope"):
			_, _ = w.Write([]byte(stored))
		default:
			mu.Lock()
			n := streams
			mu.Unlock()
			if n < 2 {
				job("running", "running", "running")(w, r)
				return
			}
			job("done", "done", "done")(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	server, err := ParseServer(srv.URL, true, "")
	if err != nil {
		t.Fatal(err)
	}
	clk := fakeclock.New(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC))
	c := New(server, "tok", "af/test", &http.Transport{DisableKeepAlives: true})
	c.Clock = clk

	var seqs []int64
	rest, err := c.Follow(context.Background(), "j", 0, func(e Event) { seqs = append(seqs, e.Seq) })
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seqs, []int64{1, 2, 3, 4, 5}) || rest.LastSeq != 5 {
		t.Errorf("delivered %v, last %d, want 1 to 5 once each", seqs, rest.LastSeq)
	}
	if got := clk.Sleeps(); !slices.Equal(got, []time.Duration{time.Second}) {
		t.Errorf("sleeps = %v, want one second between the tries", got)
	}
	envelope, unavailable, err := c.Envelope(context.Background(), "j", rest.Job)
	if err != nil || unavailable != "" || !sameJSON(string(envelope), stored) {
		t.Errorf("Envelope = %s, %q, %v, want the envelope of attempt 2 as stored", envelope, unavailable, err)
	}
	var doc struct {
		State string `json:"state"`
	}
	if json.Unmarshal(rest.Job, &doc) != nil || doc.State != "done" {
		t.Errorf("the job is %s, want done", rest.Job)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"/api/v1/jobs/j/events?since=0", "/api/v1/jobs/j?", "/api/v1/jobs/j/events?since=3", "/api/v1/jobs/j?", "/api/v1/jobs/j/attempts/2/envelope?"}
	if !slices.Equal(log, want) {
		t.Errorf("requests = %v, want %v", log, want)
	}
}
