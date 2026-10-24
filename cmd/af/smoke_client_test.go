package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	afclock "github.com/agent-fox-dev/agentfox/internal/afclient/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/api"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The smoke tests run the commands of the client through the shell of the binary against the service
// of specs 11 and 12 as it is: the HTTP handler and its routes, the store over a file, the jobs layer with
// its queue and its event stream, and the fake hub; only the runner of the jobs is the fake one.

// The users of the fake hub: the person who logs in, and the token of a workspace. They are long, so
// that a leak of either is found in an output.
const (
	smokeCredential = "caller-credential-sentinel-0123456789"
	smokeWorkspace  = "workspace-token-sentinel-9876543210"
)

// realService is the service of specs 11 and 12 in process, over a fake hub, with a fake runner.
type realService struct {
	URL    string
	Hub    *fakehub.Hub
	Runner *fakerunner.Runner
	Store  *store.Store
}

// startRealService starts the service. The caller is the person who holds smokeCredential and sees
// the workspace widgets, as does the holder of smokeWorkspace.
func startRealService(t *testing.T, steps ...fakerunner.Step) *realService {
	t.Helper()
	dir := t.TempDir()
	key, err := secrets.NewKey(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), filepath.Join(dir, "agent-fox.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fh := fakehub.New(t)
	fh.AddUser(smokeCredential, "usr_1", []string{"widgets"})
	fh.AddUser(smokeWorkspace, "usr_9", []string{"widgets"})
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	runner := fakerunner.New(steps...)
	svc, err := jobs.New(jobs.Config{
		DB: st.DB(), Subscriptions: st, Logger: logger, Workers: 2, AttemptsDir: filepath.Join(dir, "attempts"), Runner: runner,
		PollInterval: 5 * time.Millisecond, RetryDelay: time.Millisecond, Retain: 100 * 365 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		runner.ReleaseAll()
		if err := svc.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	version := (&toolio.App{Name: "agent-fox", Version: "1.2.3"}).VersionDocument()
	srv := api.NewServer(api.Deps{Store: st, Hub: hub.New(fh.URL, "test"), Logger: logger, Version: version, Jobs: svc})
	ts := httptest.NewUnstartedServer(srv.Handler)
	ts.Config.ErrorLog, ts.Config.ReadHeaderTimeout = srv.ErrorLog, srv.ReadHeaderTimeout
	ts.Start()
	t.Cleanup(ts.Close)
	return &realService{URL: ts.URL, Hub: fh, Runner: runner, Store: st}
}

// subscribed puts the workspace widgets in the store, as a person's subscription would.
func (s *realService) subscribed(t *testing.T) {
	t.Helper()
	if _, _, err := s.Store.Upsert(context.Background(), "widgets", smokeWorkspace, "usr_1", "usr_1", time.Now()); err != nil {
		t.Fatal(err)
	}
}

// smokeEnv gives the invocations of the test a home that holds the credential of the caller, and
// AF_URL naming the service; the pauses of a following cost nothing.
func smokeEnv(t *testing.T, s *realService) (home string) {
	t.Helper()
	home, _ = clientHome(t, str("token = \""+smokeCredential+"\"\n"))
	t.Setenv("AF_URL", s.URL)
	useClockNow(t)
	return home
}

// eventDocuments are the events of the job as the service sends them, in order.
func eventDocuments(t *testing.T, s *realService, id string) []string {
	t.Helper()
	req, err := http.NewRequest("GET", s.URL+"/api/v1/jobs/"+id+"/events?since=0", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+smokeCredential)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var docs []string
	for _, line := range strings.Split(string(data), "\n") {
		if doc, ok := strings.CutPrefix(line, "data: "); ok {
			docs = append(docs, doc)
		}
	}
	return docs
}

// TestAPersonWithACredentialSubscribesAWorkspaceAgainstTheRealService_TS_16_76 verifies TS-16-76
// and 16-PATH-1 as they stand: the credential is in the file, and the person subscribes a workspace.
// Verifies: 16-PATH-1, 16-REQ-5.7, 16-REQ-8.1
func TestAPersonWithACredentialSubscribesAWorkspaceAgainstTheRealService_TS_16_76(t *testing.T) {
	s := startRealService(t)
	clientHome(t, str("token = \""+smokeCredential+"\"\n"))
	t.Setenv("AF_URL", s.URL)
	all := ""

	pat := tokenFile(t, smokeWorkspace+"\n")
	res := afRun(t, "", "workspace", "subscribe", "widgets", "--hub-token-file", pat)
	all += res.output()
	view, _ := res.env["result"].(map[string]any)
	if res.code != 0 || view["slug"] != "widgets" || view["token_state"] != "ok" || !strings.Contains(res.env["summary"].(string), "subscribed") {
		t.Fatalf("subscribe: exit %d, result %v, summary %v, want the view of a new subscription", res.code, res.env["result"], res.env["summary"])
	}
	res = afRun(t, "", "workspace", "subscribe", "widgets", "--hub-token-file", pat)
	all += res.output()
	view, _ = res.env["result"].(map[string]any)
	if res.code != 0 || view["slug"] != "widgets" || !strings.Contains(res.env["summary"].(string), "replaced") {
		t.Errorf("the repeated subscribe: exit %d, result %v, summary %v, want the replaced subscription", res.code, res.env["result"], res.env["summary"])
	}
	if res := afRun(t, "", "workspace", "list"); res.code != 0 || len(res.result()["workspaces"].([]any)) != 1 {
		t.Errorf("workspace list: exit %d, result %v", res.code, res.result())
	}
	for _, secret := range []string{smokeCredential, smokeWorkspace} {
		if strings.Contains(all, secret) {
			t.Errorf("an output holds %q", secret)
		}
	}
}

// TestAFixQueuedWithWaitEndsWithTheRunnersOutcomeAgainstTheRealService_TS_16_77 verifies
// TS-16-77 and 16-PATH-2.
// Verifies: 16-PATH-2, 16-REQ-7.7, 16-REQ-10.3, 16-REQ-10.5
func TestAFixQueuedWithWaitEndsWithTheRunnersOutcomeAgainstTheRealService_TS_16_77(t *testing.T) {
	done := fakerunner.Env(toolio.StatusDone, fakerunner.WithSummary("fixed it"), fakerunner.WithArtifacts(toolio.Artifact{Kind: "pull_request", URL: "https://github.com/o/r/pull/9", Number: 9}))
	var jobID string
	t.Run("done", func(t *testing.T) {
		s := startRealService(t, fakerunner.Return(jobs.Result{Envelope: done}))
		s.subscribed(t)
		smokeEnv(t, s)
		res := afRun(t, "", "jobs", "submit", "fix", "42", "--workspace", "widgets", "--wait")
		if res.code != 0 || field(res.env, "status") != "done" {
			t.Fatalf("exit %d, %s", res.code, res.message())
		}
		job, _ := res.result()["job"].(map[string]any)
		if job["state"] != "done" {
			t.Errorf("result.job.state = %v, want done", job["state"])
		}
		var want any
		_ = json.Unmarshal(done, &want)
		if !reflect.DeepEqual(res.result()["envelope"], want) {
			t.Errorf("result.envelope = %v, want attempt 1's stored envelope", res.result()["envelope"])
		}
		jobID, _ = job["id"].(string)
		if artifacts, _ := res.env["artifacts"].([]any); len(artifacts) != 1 || artifacts[0].(map[string]any)["name"] != jobID {
			t.Errorf("artifacts = %v, want the job", artifacts)
		}

		// af jobs watch writes every event of the job to stderr, one JSON line each, in the order of seq.
		watched := afRun(t, "", "jobs", "watch", jobID)
		if watched.code != 0 {
			t.Fatalf("watch: exit %d, %s", watched.code, watched.message())
		}
		var lines []string
		for _, l := range strings.Split(strings.TrimSuffix(watched.stderr, "\n"), "\n") {
			var buf bytes.Buffer
			if err := json.Compact(&buf, []byte(l)); err != nil {
				t.Fatalf("stderr line %q: %v", l, err)
			}
			lines = append(lines, buf.String())
		}
		var want2 []string
		for _, d := range eventDocuments(t, s, jobID) {
			var buf bytes.Buffer
			_ = json.Compact(&buf, []byte(d))
			want2 = append(want2, buf.String())
		}
		if len(want2) < 3 || !slices.Equal(lines, want2) {
			t.Errorf("stderr holds %d lines, the job has %d events; want the same, in the same order:\n%q\n%q", len(lines), len(want2), lines, want2)
		}
		var seqs []int
		for _, l := range lines {
			var ev struct {
				Seq int `json:"seq"`
			}
			_ = json.Unmarshal([]byte(l), &ev)
			seqs = append(seqs, ev.Seq)
		}
		if !slices.IsSorted(seqs) {
			t.Errorf("seqs = %v, want ascending", seqs)
		}
	})
	t.Run("unverified, parked", func(t *testing.T) {
		s := startRealService(t, fakerunner.Envelope(fakerunner.Env(toolio.StatusUnverified, fakerunner.WithOutcome("parked"))))
		s.subscribed(t)
		smokeEnv(t, s)
		res := afRun(t, "", "jobs", "submit", "fix", "42", "--workspace", "widgets", "--wait")
		if res.code != 4 || field(res.env, "status") != "unverified" {
			t.Errorf("exit %d, status %v, %s, want 4 and unverified", res.code, field(res.env, "status"), res.message())
		}
	})
	t.Run("failed and not retryable", func(t *testing.T) {
		s := startRealService(t, fakerunner.Failed("sandbox", false))
		s.subscribed(t)
		smokeEnv(t, s)
		res := afRun(t, "", "jobs", "submit", "fix", "42", "--workspace", "widgets", "--wait")
		if res.code != 1 || res.category() != "sandbox" || res.retryable() {
			t.Errorf("exit %d, category %q, retryable %v, %s, want 1 and the category of the envelope", res.code, res.category(), res.retryable(), res.message())
		}
	})
}

// TestABlockedTriageJobIsAnsweredThroughItsResumeArgvAndAttemptTwoGetsTheOptionLabel_TS_16_78
// verifies TS-16-78 and 16-PATH-3.
// Verifies: 16-PATH-3, 16-REQ-10.4, 16-REQ-8.7
func TestABlockedTriageJobIsAnsweredThroughItsResumeArgvAndAttemptTwoGetsTheOptionLabel_TS_16_78(t *testing.T) {
	s := startRealService(t,
		fakerunner.Envelope(fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which branch?", "main", "release"))),
		fakerunner.Done(fakerunner.WithSummary("triaged on release")),
	)
	s.subscribed(t)
	smokeEnv(t, s)

	res := afRun(t, "", "jobs", "submit", "triage", "42", "--workspace", "widgets", "--wait")
	if res.code != 3 || field(res.env, "status") != "needs_human" {
		t.Fatalf("exit %d, status %v, %s, want 3 and needs_human", res.code, field(res.env, "status"), res.message())
	}
	artifacts, _ := res.env["artifacts"].([]any)
	if len(artifacts) != 1 {
		t.Fatalf("artifacts = %v, want the job", artifacts)
	}
	id := artifacts[0].(map[string]any)["name"].(string)
	resume, _ := field(res.env, "needs_human", "resume").(map[string]any)
	var argv []string
	for _, a := range resume["argv"].([]any) {
		argv = append(argv, a.(string))
	}
	if !slices.Equal(argv, []string{"af", "jobs", "answer", id, "--wait"}) || resume["flag"] != "--context" {
		t.Fatalf("resume = %v, want af jobs answer %s --wait with --context", resume, id)
	}

	second := afRun(t, "", append(argv[1:], "--context", "B")...)
	if second.code != 0 || field(second.env, "status") != "done" {
		t.Fatalf("the resume: exit %d, %s", second.code, second.message())
	}
	if !s.Runner.Wait(2, 10*time.Second) {
		t.Fatal("attempt 2 never reached the runner")
	}
	calls := s.Runner.Calls()
	if len(calls) != 2 || calls[1].Attempt != 2 || len(calls[1].Answers) != 1 || calls[1].Answers[0].Text != "release" || calls[1].Answers[0].Option == nil || *calls[1].Answers[0].Option != "B" {
		t.Errorf("attempt 2 was given %+v, want the answer B with the label of option B", calls[len(calls)-1].Answers)
	}
	if job, _ := second.result()["job"].(map[string]any); job["attempt_count"] != float64(2) {
		t.Errorf("result.job.attempt_count = %v, want 2", job["attempt_count"])
	}
}

// TestATriageRunsOnThisMachineAndSuggestsAFix_TS_16_80 verifies TS-16-80 and 16-PATH-5 as they
// stand: af triage runs the loop here, and suggests af fix of the issue.
// Verifies: 16-PATH-5, 16-REQ-2.4, 16-REQ-2.5
func TestATriageRunsOnThisMachineAndSuggestsAFix_TS_16_80(t *testing.T) {
	addr, connections := countingListener(t)
	t.Setenv("AF_URL", "http://"+addr)
	e := newLocalEnv(t, nil)
	code, env := e.run("", e.loopArgs("confirmed")...)
	if code != 0 || field(env, "status") != "done" {
		t.Fatalf("exit %d, %v", code, env["error"])
	}
	if !slices.ContainsFunc(nextArgvs(env), func(argv []string) bool {
		return slices.Equal(argv, []string{"af", "fix", e.forge.IssueURL(7), "--dir", e.dir})
	}) {
		t.Errorf("next = %v, want af fix <issue url> --dir <dir>", nextArgvs(env))
	}
	if !slices.Contains(e.runFiles(env), "events.jsonl") {
		t.Errorf("the run directory holds %v, want it created", e.runFiles(env))
	}
	if reads := e.forge.Issue(7); reads.Body == originalReport {
		t.Error("the issue was not read and written by the loop")
	}
	if n := connections.Load(); n != 0 {
		t.Errorf("%d connections were made to the service named by AF_URL", n)
	}
}

// droppedStreamService is a service of the job j whose stream is cut after the events 1 to 3, repeats the
// event 3 first when it is asked from there, and ends with the job at rest at event 5. It returns the service
// and a function that gives the values of since that its streams were asked for.
func droppedStreamService(t *testing.T) (*fakeService, func() []string) {
	t.Helper()
	frame := func(seq int) string {
		typ := "step"
		data := `{"n":` + strconv.Itoa(seq) + `}`
		if seq == 5 {
			typ, data = "job_state", `{"state":"done","at_rest":true}`
		}
		doc := `{"seq":` + strconv.Itoa(seq) + `,"ts":"2026-10-23T12:00:00.000Z","job_id":"j","attempt":1,"type":"` + typ + `","data":` + data + `}`
		return "id: " + strconv.Itoa(seq) + "\nevent: " + typ + "\ndata: " + doc + "\n\n"
	}
	var mu sync.Mutex
	var asked []string
	streams := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(asked)
	}
	svc := ready(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/events"):
			since := r.URL.Query().Get("since")
			mu.Lock()
			asked = append(asked, since)
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			if since == "0" { // events 1 to 3, then the connection is cut
				_, _ = io.WriteString(w, frame(1)+frame(2)+frame(3))
				w.(http.Flusher).Flush()
				conn, _, _ := w.(http.Hijacker).Hijack()
				_ = conn.Close()
				return
			}
			_, _ = io.WriteString(w, frame(3)+frame(4)+frame(5)) // a duplicate first, then the rest
		case strings.Contains(r.URL.Path, "/attempts/1/envelope"):
			answering(200, `{"status":"done","warnings":[]}`)(w, r)
		default:
			state, attempt := "running", "running"
			if streams() >= 2 {
				state, attempt = "done", "done"
			}
			answering(200, `{"id":"j","state":"`+state+`","result":`+map[bool]string{true: `{"status":"done","exit_code":0,"summary":"ok","error":null}`, false: `null`}[state == "done"]+`,"attempts":[{"n":1,"state":"`+attempt+`"}]}`)(w, r)
		}
	})
	return svc, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(asked)
	}
}

// TestWatchingAJobResumesAfterADroppedStreamAndDeliversEveryEventOnce_TS_16_81 verifies TS-16-81
// and 16-PATH-6.
// Verifies: 16-PATH-6, 16-REQ-9.8, 16-REQ-9.3
func TestWatchingAJobResumesAfterADroppedStreamAndDeliversEveryEventOnce_TS_16_81(t *testing.T) {
	svc, asked := droppedStreamService(t)
	clk := afclock.New(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC))
	useClock(t, clk)
	res, data := afRunEvents(t, "jobs", "watch", "j")
	if res.code != 0 || field(res.env, "status") != "done" {
		t.Fatalf("exit %d, %s", res.code, res.message())
	}
	if got := asked(); !slices.Equal(got, []string{"0", "3"}) {
		t.Errorf("the streams were asked for since=%v, want 0 and then 3", got)
	}
	if got := clk.Sleeps(); !slices.Equal(got, []time.Duration{time.Second}) {
		t.Errorf("sleeps = %v, want one second before the second try", got)
	}
	lines := strings.Split(strings.TrimSuffix(res.stderr, "\n"), "\n")
	var seqs []int
	for _, l := range lines {
		var ev struct {
			Seq int `json:"seq"`
		}
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			t.Fatalf("stderr line %q: %v", l, err)
		}
		seqs = append(seqs, ev.Seq)
	}
	if !slices.Equal(seqs, []int{1, 2, 3, 4, 5}) {
		t.Errorf("stderr holds the events %v, want 1 to 5 once each in order", seqs)
	}
	var emittedSeqs []int
	for _, l := range strings.Split(strings.TrimSpace(data), "\n") {
		var ev struct {
			Type string `json:"type"`
			Data struct {
				Seq int `json:"seq"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(l), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Type == "job_event" {
			emittedSeqs = append(emittedSeqs, ev.Data.Seq)
		}
	}
	if !slices.Equal(emittedSeqs, []int{1, 2, 3, 4, 5}) {
		t.Errorf("the events hold the job events %v, want 1 to 5 once each in order", emittedSeqs)
	}
	var fetched bool
	for _, r := range svc.Requests() {
		fetched = fetched || strings.HasSuffix(r.Path, "/attempts/1/envelope")
	}
	if !fetched {
		t.Error("the envelope of the final attempt was not fetched")
	}
}
