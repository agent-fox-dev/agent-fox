package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	afclock "github.com/agent-fox-dev/agentfox/internal/afclient/fakeclock"
)

// eventDocument is the document of event seq of the job j, as the service sends it.
func eventDocument(seq int) string {
	return fmt.Sprintf(`{"seq":%d,"ts":"2026-10-23T12:00:00.000Z","job_id":"j","attempt":1,"type":"step","data":{"n":%d}}`, seq, seq)
}

// eventFrame is event seq as one frame of the stream.
func eventFrame(seq int) string {
	return fmt.Sprintf("id: %d\nevent: step\ndata: %s\n\n", seq, eventDocument(seq))
}

// doneJob is the document of the job j at rest.
const doneJob = `{"id":"j","state":"done","workspace":"widgets","future":{"kept":[1,2]},"attempts":[{"n":1,"state":"done"}]}`

// streaming is a handler that serves the stream of the job j with the frames, and the job
// document for every other request.
func streaming(frames []string, job string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/events") {
			w.Header().Set("Content-Type", "text/event-stream")
			for _, f := range frames {
				_, _ = io.WriteString(w, f)
			}
			return
		}
		if strings.Contains(r.URL.Path, "/attempts/") { // the attempt has no stored envelope
			answering(404, `{"error":{"code":"not_found","message":"no such envelope"}}`)(w, r)
			return
		}
		answering(200, job)(w, r)
	}
}

// watch runs af jobs watch for the job j with a clock whose pauses cost nothing.
func watch(t *testing.T, args ...string) (afResult, *afclock.Clock) {
	t.Helper()
	clk := afclock.New(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC))
	useClock(t, clk)
	return afRun(t, "", append([]string{"jobs", "watch", "j"}, args...)...), clk
}

// TestWatchStartsFromSinceAndDeliversTheEventsAboveItAsJobEvents_TS_16_61 verifies TS-16-61
// and TS-16-62 for the command: the first request asks for the stream from --since, the events
// at or below it are not delivered, and --emit-events holds one job_event for each delivered
// event with the document of the service as its data.
// Verifies: 16-REQ-9.1, 16-REQ-9.2
func TestWatchStartsFromSinceAndDeliversTheEventsAboveItAsJobEvents_TS_16_61(t *testing.T) {
	svc := ready(t, streaming([]string{eventFrame(4), eventFrame(5), eventFrame(6), eventFrame(6), eventFrame(7), eventFrame(5)}, doneJob))
	emitted := filepath.Join(t.TempDir(), "events.jsonl")
	res, _ := watch(t, "--since", "5", "--emit-events", emitted)
	if res.code != 0 {
		t.Fatalf("exit %d, %s", res.code, res.stdout)
	}
	if reqs := svc.Requests(); len(reqs) < 2 || reqs[0].RequestURI != "/api/v1/jobs/j/events?since=5" {
		t.Errorf("requests = %+v, want the stream from since=5 first", reqs)
	}
	data, err := os.ReadFile(emitted)
	if err != nil {
		t.Fatal(err)
	}
	var seqs []float64
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var ev struct {
			Type string `json:"type"`
			Data any    `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		if ev.Type != "job_event" {
			continue
		}
		doc, _ := ev.Data.(map[string]any)
		seqs = append(seqs, doc["seq"].(float64))
		var want map[string]any
		if err := json.Unmarshal([]byte(eventDocument(int(doc["seq"].(float64)))), &want); err != nil || !reflect.DeepEqual(doc, want) {
			t.Errorf("the data of job_event %v is %v, want the document of the service: %v", doc["seq"], doc, want)
		}
	}
	if !slices.Equal(seqs, []float64{6, 7}) {
		t.Errorf("job_event seqs = %v, want 6 and 7 once each", seqs)
	}
}

// TestWatchWritesOneCompactJSONLineForEachEventToStderrAndRecordsTheJob_TS_16_63 verifies
// TS-16-63 for watch and the result of a job that is at rest, so far: the artifact of the job,
// status done and the job document as received.
// Verifies: 16-REQ-9.3
func TestWatchWritesOneCompactJSONLineForEachEventToStderrAndRecordsTheJob_TS_16_63(t *testing.T) {
	svc := ready(t, streaming([]string{eventFrame(1), eventFrame(2), eventFrame(3)}, doneJob))
	res, _ := watch(t)
	if res.code != 0 || field(res.env, "status") != "done" {
		t.Fatalf("exit %d, status %v, %s", res.code, field(res.env, "status"), res.message())
	}
	var want []string
	for i := 1; i <= 3; i++ {
		var buf bytes.Buffer
		if err := json.Compact(&buf, []byte(eventDocument(i))); err != nil {
			t.Fatal(err)
		}
		want = append(want, buf.String())
	}
	if got := strings.Split(strings.TrimSuffix(res.stderr, "\n"), "\n"); !slices.Equal(got, want) {
		t.Errorf("stderr = %q, want one compact line for each event in order: %q", got, want)
	}
	artifacts, _ := res.env["artifacts"].([]any)
	if len(artifacts) != 1 || !reflect.DeepEqual(artifacts[0], map[string]any{"kind": "job", "name": "j", "url": svc.URL + "/api/v1/jobs/j"}) {
		t.Errorf("artifacts = %v, want the job", artifacts)
	}
	result, _ := res.env["result"].(map[string]any)
	var job any
	if err := json.Unmarshal([]byte(doneJob), &job); err != nil {
		t.Fatal(err)
	}
	if env, present := result["envelope"]; !present || env != nil || !reflect.DeepEqual(result["job"], job) {
		t.Errorf("result = %v, want {job: the document as received, envelope: null}", result)
	}
}

// TestWatchNeedsAJobIdAndANonNegativeSince_TS_16_61 verifies the arguments of watch.
// Verifies: 16-REQ-9.1
func TestWatchNeedsAJobIdAndANonNegativeSince_TS_16_61(t *testing.T) {
	svc := ready(t, nil)
	if res := afRun(t, "", "jobs", "watch"); res.code != 2 {
		t.Errorf("no id: exit %d, want 2", res.code)
	}
	if res := afRun(t, "", "jobs", "watch", "j", "--since", "-1"); res.code != 2 || !strings.Contains(res.message(), "--since") {
		t.Errorf("--since -1: exit %d, message %q, want 2 naming --since", res.code, res.message())
	}
	if res := afRun(t, "", "jobs", "watch", "j", "--since", "x"); res.code != 2 {
		t.Errorf("--since x: exit %d, want 2", res.code)
	}
	if n := len(svc.Requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

// TestAFollowingThatGivesUpIsRetryableAndNextWatchesFromTheLastEvent_TS_16_66 verifies
// TS-16-66 for the command: the failure, and the next argv with --server only when the server
// came from the flag.
// Verifies: 16-REQ-9.6
func TestAFollowingThatGivesUpIsRetryableAndNextWatchesFromTheLastEvent_TS_16_66(t *testing.T) {
	var asked int
	svc := ready(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/events") {
			answering(200, `{"id":"j","state":"running","attempts":[{"n":1,"state":"running"}]}`)(w, r)
			return
		}
		asked++
		if asked == 1 { // the first try delivers two events
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, eventFrame(1)+eventFrame(2))
			return
		}
		answering(502, `{"error":{"code":"hub_unavailable","message":"hub is down"}}`)(w, r)
	})

	for _, c := range []struct {
		name string
		args []string
		argv []string
	}{
		{"from AF_URL", nil, []string{"af", "jobs", "watch", "j", "--since", "2"}},
		{"from the flag", []string{"--server", svc.URL}, []string{"af", "jobs", "watch", "j", "--since", "2", "--server", svc.URL}},
	} {
		asked = 0
		res, clk := watch(t, c.args...)
		if res.code != 1 || res.category() != "hub" || !res.retryable() {
			t.Errorf("%s: exit %d, category %q, retryable %v, message %q, want 1, hub, retryable", c.name, res.code, res.category(), res.retryable(), res.message())
		}
		if m := res.message(); !strings.Contains(m, "j") || !strings.Contains(m, "may still be running") {
			t.Errorf("%s: message %q, want it to name the job and say it may still be running", c.name, m)
		}
		next, _ := res.env["next"].([]any)
		if len(next) != 1 {
			t.Fatalf("%s: next = %v, want one entry", c.name, next)
		}
		argv, _ := next[0].(map[string]any)["argv"].([]any)
		var got []string
		for _, a := range argv {
			got = append(got, a.(string))
		}
		if !slices.Equal(got, c.argv) {
			t.Errorf("%s: next argv = %v, want %v", c.name, got, c.argv)
		}
		if got, want := clk.Sleeps(), []time.Duration{time.Second, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}; !slices.Equal(got, want) {
			t.Errorf("%s: sleeps = %v, want %v", c.name, got, want)
		}
	}
}

// TestA404WhileFollowingStopsAtOnceAndNamesTheJob_TS_16_67 verifies TS-16-67 for the command:
// the answer is mapped as the table says, and the message names the job.
// Verifies: 16-REQ-9.7
func TestA404WhileFollowingStopsAtOnceAndNamesTheJob_TS_16_67(t *testing.T) {
	svc := ready(t, answering(404, `{"error":{"code":"not_found","message":"job not found"}}`))
	res, clk := watch(t)
	if res.code != 2 || res.category() != "usage" || !strings.Contains(res.message(), `"j"`) {
		t.Errorf("exit %d, category %q, message %q, want 2, usage, naming the job", res.code, res.category(), res.message())
	}
	if n := len(svc.Requests()); n != 1 || len(clk.Sleeps()) != 0 {
		t.Errorf("%d requests and sleeps %v, want one try and no wait", n, clk.Sleeps())
	}
}
