package jobs_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// attemptPath is the path of a file of an attempt on the API.
func attemptPath(id string, n any, file string) string {
	return "/api/v1/jobs/" + id + "/attempts/" + toString(n) + "/" + file
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return mustJSON(v)
}

// TestTheEnvelopeRouteServesTheStoredBytesAndTheReportRouteServesTheMarkdown_TS_12_62
// verifies TS-12-62 (and the route half of TS-12-36): of a job with two attempts, each
// envelope route answers 200 with the bytes of its envelope.json as they were stored, with
// the content type application/json, though they are not the canonical JSON of what they
// say; the report route answers the bytes of report.md as text/markdown; charset=utf-8.
// Verifies: 12-REQ-9.6
func TestTheEnvelopeRouteServesTheStoredBytesAndTheReportRouteServesTheMarkdown_TS_12_62(t *testing.T) {
	// The first envelope has a field that toolio.Envelope does not know, and an order of its
	// own; the second is indented the way a command writes it.
	asked := bytes.Replace(
		fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which? — ünï", "one", "two")),
		[]byte(`"status": "needs_human",`), []byte(`"x_extension": {"z": 1, "a": [1, 2]},   "status": "needs_human",`), 1)
	asked = append([]byte("  "), asked...) // leading white space, which no encoder writes
	done := fakerunner.Env(toolio.StatusDone, fakerunner.WithSummary("fixed it"))
	r := fakerunner.New(
		fakerunner.Return(jobs.Result{Envelope: asked, Report: "# Question\n\nWhich?\n"}),
		fakerunner.Return(jobs.Result{Envelope: done, Report: "# Done\n\n- fixed *it*\n\tand a tab\n"}),
	)
	e := newEnv(t, withRunner(r))
	job := e.create("widgets", jobs.KindFix, 1)
	e.Start()
	e.waitState(job.ID, jobs.StateBlocked)
	if resp := e.answer(job.ID, map[string]string{"option": "B"}); resp.Status != 202 {
		t.Fatalf("answer: %d %s", resp.Status, resp.Body)
	}
	e.waitState(job.ID, jobs.StateDone)

	for _, tc := range []struct {
		n      int
		file   string
		want   string
		stored []byte
		ct     string
	}{
		{1, "envelope", "envelope.json", asked, "application/json"},
		{2, "envelope", "envelope.json", done, "application/json"},
		{1, "report", "report.md", []byte("# Question\n\nWhich?\n"), "text/markdown; charset=utf-8"},
		{2, "report", "report.md", []byte("# Done\n\n- fixed *it*\n\tand a tab\n"), "text/markdown; charset=utf-8"},
	} {
		got := e.call("GET", attemptPath(job.ID, tc.n, tc.file), nil)
		onDisk, err := os.ReadFile(e.fileOf(job.ID, tc.n, tc.want))
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != 200 || string(got.Body) != string(onDisk) || string(onDisk) != string(tc.stored) {
			t.Errorf("attempt %d %s: status %d, %d bytes served, %d on disk, %d given; want the same bytes", tc.n, tc.file, got.Status, len(got.Body), len(onDisk), len(tc.stored))
		}
		if ct := got.Header.Get("Content-Type"); ct != tc.ct {
			t.Errorf("attempt %d %s: Content-Type %q, want %q", tc.n, tc.file, ct, tc.ct)
		}
	}
	// The two attempts are not one another's.
	if one, two := e.call("GET", attemptPath(job.ID, 1, "envelope"), nil), e.call("GET", attemptPath(job.ID, 2, "envelope"), nil); string(one.Body) == string(two.Body) {
		t.Error("the envelopes of attempts 1 and 2 are the same bytes")
	}
	// The bytes are not canonical JSON: decoded and encoded they would differ, so a server that
	// went through toolio.Envelope or a map would fail the comparison above.
	var round map[string]any
	if json.Unmarshal(asked, &round) == nil {
		if again, _ := json.Marshal(round); string(again) == string(asked) {
			t.Error("the test's envelope is canonical JSON, so it proves nothing about verbatim")
		}
	}
	// Who is calling comes first.
	if r := e.callAs("", "GET", attemptPath(job.ID, 1, "envelope"), nil); r.Status != 401 {
		t.Errorf("without a token: %d, want 401", r.Status)
	}
}

// TestEveryWayToNothingToServeIsTheSame404_TS_12_63 verifies TS-12-63: an unknown job, a job
// on a workspace the caller cannot see, an attempt number of 0, 9, -1, 01, 1.0 or text, the
// envelope of an attempt that is running, and the report of an attempt that has none all
// answer 404 with one body, for the envelope route and the report route; the document of an
// unknown or an invisible job is the same 404.
// Verifies: 12-REQ-9.7
func TestEveryWayToNothingToServeIsTheSame404_TS_12_63(t *testing.T) {
	r := fakerunner.New(
		fakerunner.Done(),                   // job 1, attempt 1: an envelope, and no report
		fakerunner.Block(fakerunner.Step{}), // job 2, attempt 1: running
		fakerunner.Return(jobs.Result{Envelope: fakerunner.Env(toolio.StatusDone), Report: "x"}), // job 3 (the hidden one)
	)
	e := newEnv(t, withRunner(r))
	e.subscribe("secret")
	finished := e.create("widgets", jobs.KindFix, 1)
	e.Start()
	e.waitState(finished.ID, jobs.StateDone)
	running := e.create("widgets", jobs.KindFix, 2)
	waitCalls(t, r, 2)
	e.waitState(running.ID, jobs.StateRunning)
	// The hidden job runs beside the running one, on the second slot; it is told apart from the
	// others by having an envelope and a report, which an outsider must not be able to learn of.
	hidden := e.createDirect("secret", jobs.KindFix, 3)
	e.waitState(hidden.ID, jobs.StateDone)

	type probe struct{ name, path string }
	var probes []probe
	for _, file := range []string{"envelope", "report"} {
		probes = append(probes,
			probe{"unknown job " + file, attemptPath("job_00000000000000000000", 1, file)},
			probe{"malformed id " + file, attemptPath("not-a-job", 1, file)},
			probe{"hidden job " + file, attemptPath(hidden.ID, 1, file)},
			probe{"attempt 0 " + file, attemptPath(finished.ID, 0, file)},
			probe{"attempt 9 " + file, attemptPath(finished.ID, 9, file)},
			probe{"attempt -1 " + file, attemptPath(finished.ID, "-1", file)},
			probe{"attempt 01 " + file, attemptPath(finished.ID, "01", file)},
			probe{"attempt 1.0 " + file, attemptPath(finished.ID, "1.0", file)},
			probe{"attempt text " + file, attemptPath(finished.ID, "latest", file)},
			probe{"attempt too big " + file, attemptPath(finished.ID, "99999999999999999999", file)},
		)
	}
	probes = append(probes,
		probe{"running attempt's envelope", attemptPath(running.ID, 1, "envelope")},
		probe{"running attempt's report", attemptPath(running.ID, 1, "report")},
		probe{"finished attempt without a report", attemptPath(finished.ID, 1, "report")},
		probe{"unknown job's document", "/api/v1/jobs/job_00000000000000000000"},
		probe{"hidden job's document", "/api/v1/jobs/" + hidden.ID},
	)
	var want string
	for _, p := range probes {
		got := e.call("GET", p.path, nil)
		code, _ := got.errorOf(t)
		if got.Status != 404 || code != "not_found" {
			t.Errorf("%s: %d %s, want 404 not_found", p.name, got.Status, got.Body)
			continue
		}
		if want == "" {
			want = string(got.Body)
		} else if string(got.Body) != want {
			t.Errorf("%s: %s, want the body every other 404 has: %s", p.name, got.Body, want)
		}
	}
	// What is there is served: the finished attempt's envelope, and the hidden job's to the
	// one who may see it.
	if got := e.call("GET", attemptPath(finished.ID, 1, "envelope"), nil); got.Status != 200 {
		t.Errorf("the envelope of the finished attempt: %d, want 200", got.Status)
	}
	if got := e.call("GET", attemptPath(finished.ID, 1, "envelope"), nil); len(got.Body) == 0 {
		t.Error("the envelope served is empty")
	}
	// The hidden job's files are there on disk; the answer for the caller gave nothing of them.
	if _, err := os.Stat(e.fileOf(hidden.ID, 1, "report.md")); err != nil {
		t.Errorf("the hidden job's report: %v", err)
	}
	// Only GET reads a file.
	if got := e.call("POST", attemptPath(finished.ID, 1, "envelope"), nil); got.Status != http.StatusMethodNotAllowed {
		t.Errorf("POST to the envelope: %d, want 405", got.Status)
	}
}

// TestTheRoutesServeTheDocumentsOfJobsAsTheyAre_TS_12_9_TS_12_10_TS_12_14 verifies the route
// half of TS-12-9, TS-12-10 and TS-12-14: GET /jobs/:id is the document of the job with its
// attempts, in order of their numbers, and an entry of GET /jobs and of GET /workspaces/:slug/jobs
// is the same document without attempts; fields, result, artifacts and side effects, and the
// attempts' triggers, answer, deliveries and counts are those of the layer's own view.
// Verifies: 12-REQ-2.1, 12-REQ-2.2, 12-REQ-2.4, 12-REQ-2.7
func TestTheRoutesServeTheDocumentsOfJobsAsTheyAre_TS_12_9_TS_12_10_TS_12_14(t *testing.T) {
	clk := fakeclock.New(epoch)
	artifacts := []toolio.Artifact{{Kind: "pull_request", URL: "https://github.example.com/acme/widgets/pull/9", Number: 9}}
	effects := []toolio.SideEffect{{Kind: "label", Target: "acme/widgets#2", State: toolio.StateDone}}
	r := fakerunner.New(
		question(), // job 1, attempt 1: blocked
		fakerunner.TransientFailure(sandboxFailure), // job 1, attempt 2: a transient failure,
		fakerunner.Done(fakerunner.WithSummary("fixed"), fakerunner.WithArtifacts(artifacts...), fakerunner.WithSideEffects(effects...)), // and then done
		fakerunner.Block(fakerunner.Step{}), // job 2: running
	)
	e := newEnv(t, withRunner(r), withClock(clk), withWorkers(1))
	blocked := e.create("widgets", jobs.KindFix, 1)
	e.Start()
	e.waitState(blocked.ID, jobs.StateBlocked)
	if resp := e.answer(blocked.ID, map[string]string{"option": "B"}); resp.Status != 202 {
		t.Fatalf("answer: %d", resp.Status)
	}
	clk.WaitForWaits(1, 10*time.Second)
	clk.Advance(30 * time.Second)
	e.waitState(blocked.ID, jobs.StateDone)
	clk.Advance(time.Second) // the jobs are made a second apart, so that their order is the order of their times
	running := e.create("widgets", jobs.KindFix, 2)
	waitCalls(t, r, 4)
	e.waitState(running.ID, jobs.StateRunning)
	clk.Advance(time.Second)
	queued := e.create("widgets", jobs.KindTriage, 7) // behind the running one, on the same workspace

	asMap := func(v any) map[string]any {
		var m map[string]any
		if err := json.Unmarshal([]byte(mustJSON(v)), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	fields := []string{"artifacts", "attempt_count", "blocked", "cancel_cause", "created_at", "current_attempt", "finished_at", "follow_up", "hub", "id", "input", "issue",
		"kind", "labels", "parent_job_id", "request_label", "result", "side_effects", "source", "started_at", "state", "usage", "workspace"}

	for name, id := range map[string]string{"done": blocked.ID, "running": running.ID, "queued": queued.ID} {
		single := e.call("GET", "/api/v1/jobs/"+id, nil)
		if single.Status != 200 || single.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("%s: GET /jobs/:id = %d %q", name, single.Status, single.Header.Get("Content-Type"))
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(single.Body, &raw); err != nil {
			t.Fatal(err)
		}
		if got := keysOf(raw); !slices.Equal(got, sortedWith(fields, "attempts")) {
			t.Errorf("%s: the fields of GET /jobs/:id are %v, want the 23 and attempts", name, got)
		}
		if want := asMap(e.get(id)); !reflect.DeepEqual(asMap(single.Doc(t)), want) {
			t.Errorf("%s: GET /jobs/:id is not the document of the layer\n got  %s\n want %s", name, single.Body, mustJSON(want))
		}
	}

	// A list entry is the document without attempts.
	list := e.call("GET", "/api/v1/jobs", nil).page(t)
	if len(list.Jobs) != 3 {
		t.Fatalf("the list has %d jobs, want 3", len(list.Jobs))
	}
	for _, entry := range list.Jobs {
		want := e.get(entry.ID)
		want.Attempts = nil
		if !reflect.DeepEqual(asMap(entry), asMap(want)) {
			t.Errorf("entry %s of GET /jobs is not the document without attempts\n got  %s\n want %s", entry.ID, mustJSON(entry), mustJSON(want))
		}
		if keys := keysOf(asMap(entry)); !slices.Equal(keys, fields) {
			t.Errorf("entry %s has the fields %v, want the 23", entry.ID, keys)
		}
	}
	if perWorkspace := e.call("GET", "/api/v1/workspaces/widgets/jobs", nil).page(t); !slices.Equal(perWorkspace.ids(), list.ids()) {
		t.Errorf("the list of widgets = %v, want the order of GET /jobs: %v", perWorkspace.ids(), list.ids())
	}
	// Newest first: the job made last is first.
	if list.Jobs[0].ID != queued.ID || list.Jobs[1].ID != running.ID || list.Jobs[2].ID != blocked.ID {
		t.Errorf("order = %v, want %s %s %s", list.ids(), queued.ID, running.ID, blocked.ID)
	}

	// TS-12-10 on the route: the result and what it carries are those of the envelope.
	done := e.call("GET", "/api/v1/jobs/"+blocked.ID, nil).Doc(t)
	if done.Result == nil || done.Result.Status != toolio.StatusDone || done.Result.ExitCode != 0 || done.Result.Summary != "fixed" || done.Result.Error != nil {
		t.Errorf("result = %+v, want done, exit code 0, \"fixed\"", done.Result)
	}
	if mustJSON(done.Artifacts) != mustJSON(artifacts) || mustJSON(done.SideEffects) != mustJSON(effects) {
		t.Errorf("artifacts %s and side effects %s, want those of the envelope", mustJSON(done.Artifacts), mustJSON(done.SideEffects))
	}
	// TS-12-14 on the route: two attempts, in order, with their triggers, answer and counts.
	if len(done.Attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", len(done.Attempts))
	}
	one, two := done.Attempts[0], done.Attempts[1]
	if one.N != 1 || two.N != 2 || one.State != jobs.AttemptNeedsHuman || two.State != jobs.AttemptDone {
		t.Errorf("attempts = %d %s, %d %s", one.N, one.State, two.N, two.State)
	}
	if one.Trigger.Kind != jobs.TriggerCreate || two.Trigger.Kind != jobs.TriggerAnswer || two.Trigger.By != callerID {
		t.Errorf("triggers = %+v and %+v", one.Trigger, two.Trigger)
	}
	if one.Answer != nil || two.Answer == nil || two.Answer.Option == nil || *two.Answer.Option != "B" || two.Answer.Text != "two" {
		t.Errorf("answers = %+v and %+v, want none and option B (\"two\")", one.Answer, two.Answer)
	}
	if one.Deliveries != 1 || one.TransientFailures != 0 || two.Deliveries != 2 || two.TransientFailures != 1 {
		t.Errorf("deliveries %d and %d, transient failures %d and %d, want 1 and 2, 0 and 1", one.Deliveries, two.Deliveries, one.TransientFailures, two.TransientFailures)
	}
	// The running job's document carries the attempt that is running, and nothing of a result.
	live := e.call("GET", "/api/v1/jobs/"+running.ID, nil).Doc(t)
	if live.State != jobs.StateRunning || live.Result != nil || len(live.Attempts) != 1 || live.Attempts[0].State != jobs.AttemptRunning || live.Attempts[0].Status != nil {
		t.Errorf("the running job = %s %+v", live.State, live.Attempts)
	}
	r.ReleaseAll()
}

// TestAFileOnDiskThatTheStoreDoesNotListIsNotServed_TS_12_63 verifies 12-REQ-9.7 further than
// TS-12-63 goes: the attempt that is running has an envelope.json and a report.md in its
// directory, which is what a runner that has begun to write them would leave, and its rows do
// not list them; the routes answer the 404, and the files of attempt 1, which the rows list,
// are served.
// Verifies: 12-REQ-9.7
func TestAFileOnDiskThatTheStoreDoesNotListIsNotServed_TS_12_63(t *testing.T) {
	r := fakerunner.New(
		fakerunner.Return(jobs.Result{Envelope: fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which?", "one", "two")), Report: "# Asked\n"}),
		fakerunner.Block(fakerunner.Step{}),
	)
	e := newEnv(t, withRunner(r))
	job := e.create("widgets", jobs.KindFix, 1)
	e.Start()
	e.waitState(job.ID, jobs.StateBlocked)
	if resp := e.answer(job.ID, map[string]string{"option": "A"}); resp.Status != 202 {
		t.Fatalf("answer: %d %s", resp.Status, resp.Body)
	}
	waitCalls(t, r, 2)
	e.waitState(job.ID, jobs.StateRunning)
	for name, content := range map[string]string{"envelope.json": "{}", "report.md": "# Early\n"} {
		if err := os.WriteFile(e.fileOf(job.ID, 2, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, file := range []string{"envelope", "report"} {
		if got := e.call("GET", attemptPath(job.ID, 2, file), nil); got.Status != 404 {
			t.Errorf("attempt 2 %s, which the rows do not list: %d %s, want 404", file, got.Status, got.Body)
		}
		if got := e.call("GET", attemptPath(job.ID, 1, file), nil); got.Status != 200 {
			t.Errorf("attempt 1 %s, which the rows list: %d, want 200", file, got.Status)
		}
	}
	// A file that the rows list and that is gone is the same answer, and the log says so.
	if err := os.Remove(e.fileOf(job.ID, 1, "report.md")); err != nil {
		t.Fatal(err)
	}
	if got := e.call("GET", attemptPath(job.ID, 1, "report"), nil); got.Status != 404 {
		t.Errorf("a report that is not on disk: %d, want 404", got.Status)
	}
	if len(e.Logs.find("WARN", "not on disk")) != 1 {
		t.Errorf("warnings = %v, want one that names the missing file", e.Logs.records())
	}
}
