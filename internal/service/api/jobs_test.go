package api

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// jobsEnv is a server with the jobs layer on which the caller A (usr_1) sees widgets and
// gadgets, which are subscribed, and the caller B sees nothing.
func jobsEnv(t *testing.T, runner *fakerunner.Runner) *env {
	t.Helper()
	e := newEnv(t, withJobs(runner))
	e.Hub.AddUser("A", "usr_1", []string{"widgets", "gadgets", "unsubscribed", "unreadable"})
	e.Hub.AddUser("B", "usr_2", nil)
	e.seed(t, "widgets", "pat")
	e.seed(t, "gadgets", "pat")
	e.seedUnreadable(t, "unreadable")
	return e
}

// createJob makes a job of the kind on the workspace as the caller A and returns its id.
func createJob(t *testing.T, e *env, slug, kind string, body any) string {
	t.Helper()
	r := e.call(t, "POST", "/api/v1/workspaces/"+slug+"/"+kind, body, "A")
	if r.Status != 202 {
		t.Fatalf("POST %s on %s = %d %s, want 202", kind, slug, r.Status, r.Body)
	}
	return r.Object(t)["id"].(string)
}

// waitFor waits until the job is in the state.
func waitFor(t *testing.T, e *env, id string, state jobs.State) {
	t.Helper()
	testutil.Eventually(t, "the job "+string(state), func() bool {
		doc, err := e.Jobs.Get(bg, id)
		return err == nil && doc.State == state
	})
}

func TestCreateJob_IsAQueuedJobOfTheCaller(t *testing.T) {
	e := jobsEnv(t, nil)
	for kind, body := range map[string]any{
		"triage": map[string]any{"text": "it breaks", "title": "Breaks", "context": []string{"see the log"}},
		"fix":    map[string]any{"issue": 42, "context": []string{"be careful"}},
	} {
		r := e.call(t, "POST", "/api/v1/workspaces/widgets/"+kind, body, "A")
		doc := r.Object(t)
		source, _ := doc["source"].(map[string]any)
		if r.Status != 202 || doc["kind"] != kind || doc["workspace"] != "widgets" || doc["state"] != "queued" || source["kind"] != "api" || source["by"] != "usr_1" {
			t.Errorf("POST %s = %d %s, want 202, a queued job of usr_1 from the API", kind, r.Status, r.Body)
		}
		got := e.get(t, "/api/v1/jobs/"+doc["id"].(string), "A")
		if attempts, _ := got.Object(t)["attempts"].([]any); got.Status != 200 || got.Object(t)["id"] != doc["id"] || len(attempts) != 1 {
			t.Errorf("GET the job = %d %s, want it with its attempt", got.Status, got.Body)
		}
	}
}

func TestCreateJob_RefusesWhatCannotBeAJob(t *testing.T) {
	e := jobsEnv(t, nil)
	first := createJob(t, e, "widgets", "fix", map[string]any{"issue": 7})
	text := func(n int) string { return `{"text":"` + strings.Repeat("x", n) + `"}` }
	for name, tc := range map[string]struct {
		path, caller string
		body         any
		headers      []string
		status       int
		code         Code
		says         string
	}{
		"no form of input":            {"widgets/triage", "A", `{}`, jsonType, 400, CodeInvalidRequest, "issue"},
		"a fix of a text":             {"widgets/fix", "A", `{"text":"x"}`, jsonType, 400, CodeInvalidRequest, "text"},
		"a field that is not known":   {"widgets/triage", "A", `{"text":"x","x":1}`, jsonType, 400, CodeInvalidRequest, "x"},
		"bytes that are not UTF-8":    {"widgets/triage", "A", "{\"text\":\"\xff\"}", jsonType, 400, CodeInvalidRequest, "text: must be valid UTF-8"},
		"a triage of 300 KiB":         {"widgets/triage", "A", text(300 << 10), jsonType, 400, CodeInvalidRequest, "text"},
		"a fix of 300 KiB":            {"widgets/fix", "A", text(300 << 10), jsonType, 413, CodePayloadTooLarge, ""},
		"a triage of more than 1 MiB": {"widgets/triage", "A", text(1 << 20), jsonType, 413, CodePayloadTooLarge, ""},
		"text/plain":                  {"widgets/triage", "A", `{"text":"x"}`, []string{"Content-Type", "text/plain"}, 415, CodeUnsupportedMedia, ""},
		"a workspace not subscribed":  {"unsubscribed/triage", "A", `{"text":"x"}`, jsonType, 404, CodeNotFound, ""},
		"a token that does not open":  {"unreadable/triage", "A", `{"text":"x"}`, jsonType, 409, CodeTokenUnreadable, "subscribe the workspace again"},
		"an open fix of the issue":    {"widgets/fix", "A", `{"issue":7}`, jsonType, 409, CodeDuplicateJob, first},
		"a workspace not seen":        {"widgets/triage", "B", `{"text":"x"}`, jsonType, 404, CodeNotFound, ""},
		"no caller":                   {"widgets/triage", "", `{"text":"x"}`, jsonType, 401, CodeUnauthenticated, ""},
	} {
		r := e.call(t, "POST", "/api/v1/workspaces/"+tc.path, tc.body, tc.caller, tc.headers)
		failed(t, name, r, tc.status, tc.code)
		if !strings.Contains(message(t, r), tc.says) {
			t.Errorf("%s: the message %q does not say %q", name, message(t, r), tc.says)
		}
	}
	if n := testutil.Count(t, e.Store.DB(), "af_jobs", ""); n != 1 {
		t.Errorf("%d jobs, want the first only", n)
	}
}

func TestInvalidUTF8Field_NamesTheFirstFieldWithBadBytes(t *testing.T) {
	for body, want := range map[string]string{
		`{"text":"fine"}`:                                  "",
		"{\"title\":\"ok\",\"text\":\"\xff\"}":             "text",
		"{\"b\":\"\xff\",\"a\":\"\xfe\"}":                  "a",
		"{\"file\":{\"name\":\"\xff\",\"content\":\"x\"}}": "file.name",
		"{\"file\":{\"name\":\"n\",\"content\":\"\xff\"}}": "file.content",
		"{\"file\":\"\xff\"}":                              "file",
		"{\"\xff\":\"x\"}":                                 "body",
		"[\"\xff\"]":                                       "body",
	} {
		if got := invalidUTF8Field([]byte(body)); got != want {
			t.Errorf("invalidUTF8Field(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestJobRoutes_AJobTheCallerCannotSeeIsNotFound(t *testing.T) {
	e := jobsEnv(t, nil)
	id := createJob(t, e, "widgets", "triage", map[string]any{"text": "x"})
	for _, route := range []struct{ method, path string }{
		{"GET", ""}, {"POST", "/cancel"}, {"POST", "/answer"}, {"POST", "/retry"},
		{"GET", "/attempts/1/envelope"}, {"GET", "/attempts/1/report"}, {"GET", "/events"},
	} {
		hidden := e.call(t, route.method, "/api/v1/jobs/"+id+route.path, nil, "B")
		missing := e.call(t, route.method, "/api/v1/jobs/job_00000000000000000000"+route.path, nil, "A")
		failed(t, route.path+" hidden", hidden, 404, CodeNotFound)
		if hidden.Body != missing.Body {
			t.Errorf("%s: the 404s differ: %q and %q", route.path, hidden.Body, missing.Body)
		}
		failed(t, route.path+" without a caller", e.call(t, route.method, "/api/v1/jobs/"+id+route.path, nil, ""), 401, CodeUnauthenticated)
	}
	e.Hub.AddUser("C", "usr_3", []string{"widgets"})
	e.Hub.Fail("/api/v1/workspaces/widgets", 500)
	failed(t, "hub down", e.get(t, "/api/v1/jobs/"+id, "C"), 502, CodeHubUnavailable)
	if doc, _ := e.Jobs.Get(bg, id); doc.State != jobs.StateQueued {
		t.Errorf("the job is %s, want it queued still", doc.State)
	}
}

func TestCancelJob_AnOpenJobIsCancelledOnce(t *testing.T) {
	e := jobsEnv(t, nil)
	id := createJob(t, e, "widgets", "triage", map[string]any{"text": "x"})
	path := "/api/v1/jobs/" + id + "/cancel"
	failed(t, "a body with a field", e.call(t, "POST", path, `{"x":1}`, "A", jsonType), 400, CodeInvalidRequest)
	failed(t, "a body that is not JSON", e.call(t, "POST", path, `x`, "A", []string{"Content-Type", "text/plain"}), 415, CodeUnsupportedMedia)
	failed(t, "a body too large", e.call(t, "POST", path, strings.Repeat(" ", maxBody+1), "A", jsonType), 413, CodePayloadTooLarge)
	if r := e.call(t, "POST", path, `{}`, "A", jsonType); r.Status != 200 || r.Object(t)["state"] != "cancelled" {
		t.Errorf("the cancel = %d %s, want 200 and the job cancelled", r.Status, r.Body)
	}
	r := e.call(t, "POST", path, nil, "A")
	failed(t, "a second cancel", r, 409, CodeInvalidState)
	if message(t, r) != "the job is cancelled" {
		t.Errorf("the message is %q, want the state", message(t, r))
	}
}

func TestAnswerAndRetry_AreForABlockedAndAFailedJob(t *testing.T) {
	runner := fakerunner.New(
		fakerunner.Return(jobs.Result{Envelope: fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which?", "a", "b")), LabelsSet: []string{"af:needs-human"}}),
		fakerunner.Return(jobs.Result{Envelope: fakerunner.Env(toolio.StatusDone), Report: "# The report\n"}),
		fakerunner.Failed("brain", false),
	)
	e := jobsEnv(t, runner)
	blocked := createJob(t, e, "widgets", "fix", map[string]any{"issue": 1})
	waitFor(t, e, blocked, jobs.StateBlocked)
	answer := "/api/v1/jobs/" + blocked + "/answer"
	failed(t, "retry a blocked job", e.call(t, "POST", "/api/v1/jobs/"+blocked+"/retry", nil, "A"), 409, CodeInvalidState)
	for name, body := range map[string]string{
		"no body": "", "both": `{"answer":"x","option":"A"}`, "an option it does not offer": `{"option":"Z"}`,
		"bytes that are not UTF-8": "{\"answer\":\"\xff\"}", "a field that is not known": `{"x":1}`,
	} {
		failed(t, name, e.call(t, "POST", answer, body, "A", jsonType), 400, CodeInvalidRequest)
	}
	failed(t, "an answer that is not JSON", e.call(t, "POST", answer, `B`, "A", []string{"Content-Type", "text/plain"}), 415, CodeUnsupportedMedia)
	if r := e.call(t, "POST", answer, map[string]string{"option": "B"}, "A"); r.Status != 202 || r.Object(t)["state"] != "queued" {
		t.Fatalf("the answer = %d %s, want 202 and the job queued", r.Status, r.Body)
	}
	waitFor(t, e, blocked, jobs.StateDone)
	failed(t, "answer a done job", e.call(t, "POST", answer, `{"option":"B"}`, "A", jsonType), 409, CodeInvalidState)

	failing := createJob(t, e, "gadgets", "fix", map[string]any{"issue": 2})
	waitFor(t, e, failing, jobs.StateFailed)
	retry := "/api/v1/jobs/" + failing + "/retry"
	failed(t, "a retry with a body", e.call(t, "POST", retry, `{"x":1}`, "A", jsonType), 400, CodeInvalidRequest)
	if _, err := e.Store.Delete(bg, "gadgets"); err != nil {
		t.Fatal(err)
	}
	failed(t, "a retry of a workspace no longer subscribed", e.call(t, "POST", retry, nil, "A"), 409, CodeNotSubscribed)
	e.seed(t, "gadgets", "pat")
	if r := e.call(t, "POST", retry, nil, "A"); r.Status != 202 || r.Object(t)["state"] != "queued" {
		t.Errorf("the retry = %d %s, want 202 and the job queued", r.Status, r.Body)
	}
	waitFor(t, e, failing, jobs.StateDone)
}

func TestAttemptFiles_AreServedAsTheAttemptStoredThem(t *testing.T) {
	envelope := fakerunner.Env(toolio.StatusDone, fakerunner.WithSummary("all good"))
	e := jobsEnv(t, fakerunner.New(fakerunner.Return(jobs.Result{Envelope: envelope, Report: "# The report\n"})))
	id := createJob(t, e, "widgets", "fix", map[string]any{"issue": 1})
	waitFor(t, e, id, jobs.StateDone)
	base := "/api/v1/jobs/" + id + "/attempts/"
	if r := e.get(t, base+"1/envelope", "A"); r.Status != 200 || r.Body != string(envelope) || r.Header.Get("Content-Type") != "application/json" {
		t.Errorf("the envelope = %d %q %s, want the bytes stored", r.Status, r.Header.Get("Content-Type"), r.Body)
	}
	if r := e.get(t, base+"1/report", "A"); r.Status != 200 || r.Body != "# The report\n" || r.Header.Get("Content-Type") != "text/markdown; charset=utf-8" {
		t.Errorf("the report = %d %q %q, want the bytes stored", r.Status, r.Header.Get("Content-Type"), r.Body)
	}
	for _, n := range []string{"2", "0", "01", "+1", "x"} {
		failed(t, "attempt "+n, e.get(t, base+n+"/envelope", "A"), 404, CodeNotFound)
	}
}

func TestListJobs_TheJobsOfTheWorkspacesTheCallerSees(t *testing.T) {
	e := jobsEnv(t, nil)
	e.Hub.AddUser("W", "usr_4", []string{"widgets"})
	w := createJob(t, e, "widgets", "fix", map[string]any{"issue": 1})
	g := createJob(t, e, "gadgets", "triage", map[string]any{"text": "x"})
	ids := func(r testutil.Response) []string {
		t.Helper()
		if r.Status != 200 {
			t.Fatalf("the list = %d %s", r.Status, r.Body)
		}
		var out []string
		for _, j := range r.Object(t)["jobs"].([]any) {
			out = append(out, j.(map[string]any)["id"].(string))
		}
		return out
	}
	for name, tc := range map[string]struct {
		path, caller string
		want         []string
	}{
		"every workspace":    {"/api/v1/jobs", "A", []string{g, w}},
		"of a kind":          {"/api/v1/jobs?kind=fix", "A", []string{w}},
		"seen by another":    {"/api/v1/jobs", "W", []string{w}},
		"seen by nobody":     {"/api/v1/jobs", "B", nil},
		"of one workspace":   {"/api/v1/workspaces/gadgets/jobs", "A", []string{g}},
		"of an unsubscribed": {"/api/v1/workspaces/unsubscribed/jobs", "A", nil},
	} {
		if got := ids(e.get(t, tc.path, tc.caller)); strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
		}
	}
	for _, path := range []string{"/api/v1/jobs?state=nope", "/api/v1/jobs?limit=0", "/api/v1/workspaces/widgets/jobs?kind=x", "/api/v1/jobs?%zz"} {
		failed(t, path, e.get(t, path, "A"), 400, CodeInvalidRequest)
	}
	e.Clock.Advance(cacheTTL)
	e.Hub.FailOnce("/api/v1/workspaces/widgets", 500)
	failed(t, "hub down for one workspace", e.get(t, "/api/v1/jobs", "W"), 502, CodeHubUnavailable)
}

func TestJobRoutes_AStoreThatFailsIsAnInternalError(t *testing.T) {
	e := jobsEnv(t, nil)
	id := createJob(t, e, "widgets", "triage", map[string]any{"text": "x"})
	e.get(t, "/api/v1/jobs/"+id, "A") // A's visibility of widgets is kept
	if err := e.Store.Close(); err != nil {
		t.Fatal(err)
	}
	for what, r := range map[string]testutil.Response{
		"a job":               e.get(t, "/api/v1/jobs/"+id, "A"),
		"the list":            e.get(t, "/api/v1/jobs", "A"),
		"the list of widgets": e.get(t, "/api/v1/workspaces/widgets/jobs", "A"),
		"a job that is made":  e.call(t, "POST", "/api/v1/workspaces/widgets/triage", map[string]string{"text": "x"}, "A"),
	} {
		failed(t, what, r, 500, CodeInternal)
	}
}

// frames reads the frames of an event stream until it ends, and returns the types of the events.
func frames(t *testing.T, resp *http.Response) []string {
	t.Helper()
	var types []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if typ, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
			types = append(types, typ)
		}
	}
	return types
}

// stream opens the event stream of the job as the caller A.
func stream(t *testing.T, e *env, id, query string, headers ...string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), "GET", e.URL+"/api/v1/jobs/"+id+"/events"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer A")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestStreamEvents_TheStoredEventsAndThenTheLiveOnesUntilTheJobIsAtRest(t *testing.T) {
	e := jobsEnv(t, nil)
	id := createJob(t, e, "widgets", "triage", map[string]any{"text": "x"})
	resp := stream(t, e, id, "")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("the stream = %d %v", resp.StatusCode, resp.Header)
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "id: 1\n" {
		t.Fatalf("the first line = %q, %v; want the stored event 1", line, err)
	}
	if _, err := e.Jobs.Cancel(bg, id); err != nil {
		t.Fatal(err)
	}
	// The stream ends once the job is at rest: the replay of an ended job is all of it.
	if got := frames(t, stream(t, e, id, "")); len(got) < 2 || got[0] != "job_state" || got[len(got)-1] != "job_state" {
		t.Errorf("the events of the cancelled job are %v, want its states", got)
	}
	all := len(frames(t, stream(t, e, id, "")))
	if n := len(frames(t, stream(t, e, id, "?since=1"))); n != all-1 {
		t.Errorf("since=1 gave %d events, want the %d after the first", n, all-1)
	}
	if n := len(frames(t, stream(t, e, id, "", "Last-Event-ID", "1"))); n != all-1 {
		t.Errorf("Last-Event-ID 1 gave %d events, want the %d after the first", n, all-1)
	}
	if n := len(frames(t, stream(t, e, id, "?since=0", "Last-Event-ID", "1"))); n != all {
		t.Errorf("since=0 with Last-Event-ID 1 gave %d events, want since to win with all %d", n, all)
	}
}

func TestStreamEvents_RefusesAStartingPointThatIsNotANumber(t *testing.T) {
	e := jobsEnv(t, nil)
	id := createJob(t, e, "widgets", "triage", map[string]any{"text": "x"})
	path := "/api/v1/jobs/" + id + "/events"
	for name, tc := range map[string]struct {
		query, lastEventID, says string
	}{
		"a negative since":      {"?since=-1", "", "since"},
		"a since with a zero":   {"?since=01", "", "since"},
		"since twice":           {"?since=1&since=2", "", "since"},
		"an empty since":        {"?since=", "", "since"},
		"a query that is wrong": {"?%zz", "", "query"},
		"a Last-Event-ID word":  {"", "x", "Last-Event-ID"},
		"a huge Last-Event-ID":  {"", "99999999999999999999", "Last-Event-ID"},
	} {
		var headers [][]string
		if tc.lastEventID != "" {
			headers = append(headers, []string{"Last-Event-ID", tc.lastEventID})
		}
		r := e.call(t, "GET", path+tc.query, nil, "A", headers...)
		failed(t, name, r, 400, CodeInvalidRequest)
		if !strings.Contains(message(t, r), tc.says) {
			t.Errorf("%s: the message %q does not name %s", name, message(t, r), tc.says)
		}
	}
}

func TestStreamEvents_EndAtTheShutdownOfTheServer(t *testing.T) {
	e := jobsEnv(t, nil)
	id := createJob(t, e, "widgets", "triage", map[string]any{"text": "x"})
	resp := stream(t, e, id, "")
	ctx, cancel := context.WithTimeout(bg, testutil.WaitTimeout)
	defer cancel()
	if err := e.HTTP.Shutdown(ctx); err != nil {
		t.Fatalf("the shutdown waited for the stream: %v", err)
	}
	if got := frames(t, resp); len(got) != 1 {
		t.Errorf("the stream sent %v, want the stored event and its end", got)
	}
	if n := len(e.Log.Find("ERROR", "")); n != 0 {
		t.Errorf("%d ERROR records, want none for a stream the server let go:\n%s", n, e.Log)
	}
}
