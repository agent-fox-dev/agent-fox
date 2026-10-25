package main

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// blockedJob is the job j blocked on a question with the options A and B.
var blockedJob = jobJSON("blocked", "", `{"reason":"needs_human","question":"Which?","options":[{"id":"A","label":"main"},{"id":"B","label":"release"}]}`, "needs_human")

// af jobs list picks the route by --workspace, sends --state and --cursor only when given, and
// gives the page as received, with the command for the next page in next.
func TestJobsList_GivesAPageAndTheCommandForTheNext(t *testing.T) {
	var page atomic.Value
	svc := serve(t, func(w http.ResponseWriter, r *http.Request) { answer(200, page.Load().(string))(w, r) })
	for _, c := range []struct {
		page, request, summary string
		args, next             []string
	}{
		{`{"jobs":[{"id":"j1","future":1}],"next_cursor":null}`, "GET /api/v1/jobs", "1 job", nil, nil},
		{`{"jobs":[],"next_cursor":"c2"}`, "GET /api/v1/jobs?state=s", "0 jobs; there are more", []string{"--state", "s"}, []string{"af jobs list --state s --cursor c2"}},
		{`{"jobs":[{},{}],"next_cursor":"c2"}`, "GET /api/v1/workspaces/a%20b/jobs?cursor=c1&state=s", "2 jobs; there are more",
			[]string{"--workspace", "a b", "--state", "s", "--cursor", "c1", "--server", svc.URL},
			[]string{"af jobs list --workspace a b --state s --server " + svc.URL + " --cursor c2"}},
	} {
		page.Store(c.page)
		res := af(t, append([]string{"jobs", "list"}, c.args...)...)
		if res.code != 0 || res.summary() != c.summary || !slices.Equal(res.next(), c.next) || !reflect.DeepEqual(res.result(), value(t, c.page)) {
			t.Errorf("%v: exit %d, summary %q, next %v, result %v, want %q, %v and the page", c.args, res.code, res.summary(), res.next(), res.result(), c.summary, c.next)
		}
		if got := svc.lines(); got[len(got)-1] != c.request {
			t.Errorf("%v: request %s, want %s", c.args, got[len(got)-1], c.request)
		}
	}
}

func TestJobsList_RefusesAnEmptyWorkspaceAndADocumentThatIsNotAPage(t *testing.T) {
	svc := serve(t, answer(200, `{"workspaces":[]}`))
	if res := af(t, "jobs", "list", "--workspace", ""); res.code != 2 || len(svc.requests()) != 0 {
		t.Errorf("an empty --workspace: exit %d, %d requests, want 2 and none", res.code, len(svc.requests()))
	}
	if res := af(t, "jobs", "list"); res.code != 1 || res.category() != "service" {
		t.Errorf("not a page: exit %d, category %v, want 1 and service", res.code, res.category())
	}
}

func TestJobsGetCancelRetryAnswer_SendTheirRequestAndGiveTheJob(t *testing.T) {
	svc := serve(t, routes(map[string]http.HandlerFunc{
		"GET /api/v1/jobs/j":         answer(200, doneJob),
		"POST /api/v1/jobs/j/cancel": answer(200, jobJSON("cancelled", "", "", "cancelled")),
		"POST /api/v1/jobs/j/retry":  answer(202, jobJSON("queued", "", "", "queued")),
		"POST /api/v1/jobs/j/answer": answer(202, jobJSON("queued", "", "", "queued")),
	}))
	for _, c := range []struct {
		args    []string
		request string
		body    any
		summary string
	}{
		{[]string{"get", "j"}, "GET /api/v1/jobs/j", nil, "showed the job j, which is done"},
		{[]string{"cancel", "j"}, "POST /api/v1/jobs/j/cancel", nil, "cancelled the job j, which is cancelled"},
		{[]string{"retry", "j"}, "POST /api/v1/jobs/j/retry", map[string]any{}, "queued another attempt of the job j, which is queued"},
		{[]string{"answer", "j", "--option", " b "}, "POST /api/v1/jobs/j/answer", map[string]any{"option": "B"}, "answered the job j, which is queued"},
	} {
		before := len(svc.requests())
		res := af(t, append([]string{"jobs"}, c.args...)...)
		reqs := svc.requests()[before:]
		if res.code != 0 || res.summary() != c.summary || len(res.artifacts()) != 0 {
			t.Errorf("%v: exit %d, summary %q, artifacts %v, want %q and none", c.args, res.code, res.summary(), res.artifacts(), c.summary)
		}
		if len(reqs) != 1 || reqs[0].Method+" "+reqs[0].URI != c.request || !reflect.DeepEqual(reqs[0].body(t), c.body) {
			t.Errorf("%v: requests %v, want %s with %v", c.args, svc.lines()[before:], c.request, c.body)
		}
		if result, _ := res.result().(map[string]any); len(result) != 2 || result["job"] == nil || result["envelope"] != nil {
			t.Errorf("%v: result %v, want the job and a null envelope", c.args, res.result())
		}
	}
	if got := field(af(t, "jobs", "get", "j").result(), "job"); !reflect.DeepEqual(got, value(t, doneJob)) {
		t.Errorf("get: job %v, want the document as received", got)
	}
	serve(t, answer(200, `not json`))
	if res := af(t, "jobs", "get", "j"); res.code != 1 || res.category() != "service" {
		t.Errorf("a job that is not JSON: exit %d, category %v, want 1 and service", res.code, res.category())
	}
}

func TestJobsRetryAndAnswer_WithWaitFollowTheJobToRest(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			answer(202, blockedJob)(w, r)
			return
		}
		atRest(doneJob, answer(200, `{"status":"done"}`))(w, r)
	})
	for _, args := range [][]string{{"retry", "j", "--wait"}, {"answer", "j", "--wait", "--context", "b"}} {
		res := af(t, append([]string{"jobs"}, args...)...)
		if res.code != 0 || res.status() != "done" || !slices.Equal(res.artifacts(), []string{"j"}) {
			t.Errorf("%v: exit %d, status %v, artifacts %v, want done and the job", args, res.code, res.status(), res.artifacts())
		}
		if !reflect.DeepEqual(field(res.result(), "envelope"), map[string]any{"status": "done"}) {
			t.Errorf("%v: result %v, want the envelope of the attempt", args, res.result())
		}
	}
}

func TestJobsAnswer_TakesExactlyOneAnswerAndSendsNothingOtherwise(t *testing.T) {
	svc := serve(t, nil)
	for _, args := range [][]string{{}, {"t", "--context", "u"}, {"--context", "u", "--option", "A"},
		{"--context", "u", "--context", "v"}, {"--option", " "}, {"  "}, {"--context", "\t"}} {
		res := af(t, append([]string{"jobs", "answer", "j"}, args...)...)
		if res.code != 2 || res.category() != "usage" {
			t.Errorf("%q: exit %d, category %v, want 2 and usage", args, res.code, res.category())
		}
	}
	if n := len(svc.requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestJobsAnswer_SendsTextThatNamesAnOptionAsTheOption(t *testing.T) {
	svc := serve(t, routes(map[string]http.HandlerFunc{
		"GET /api/v1/jobs/j":         answer(200, blockedJob),
		"POST /api/v1/jobs/j/answer": answer(202, blockedJob),
	}))
	for text, body := range map[string]any{
		"b":            map[string]any{"option": "B"},
		"free text":    map[string]any{"answer": "free text"},
		"  spaced  ":   map[string]any{"answer": "spaced"},
		"not an id: A": map[string]any{"answer": "not an id: A"},
	} {
		before := len(svc.requests())
		res := af(t, "jobs", "answer", "j", text)
		if got := svc.lines()[before:]; res.code != 0 || !slices.Equal(got, []string{"GET /api/v1/jobs/j", "POST /api/v1/jobs/j/answer"}) {
			t.Errorf("%q: exit %d, requests %v, want the job read and then the answer", text, res.code, got)
		}
		if got := svc.last(t).body(t); !reflect.DeepEqual(got, body) {
			t.Errorf("%q: body %v, want %v", text, got, body)
		}
	}
}

func TestJobsWatch_DeliversTheEventsAboveSinceToStderrAndAsJobEvents(t *testing.T) {
	serve(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/events") {
			stream(frame(4), frame(5), frame(6), frame(6), frame(7))(w, r)
			return
		}
		atRest(doneJob, nil)(w, r)
	})
	res := af(t, "jobs", "watch", "j", "--since", "5")
	if res.code != 0 || res.status() != "done" {
		t.Fatalf("exit %d, %s", res.code, res.stdout)
	}
	if want := eventDoc(6) + "\n" + eventDoc(7) + "\n"; res.stderr != want {
		t.Errorf("stderr %q, want one line for each event above 5: %q", res.stderr, want)
	}
	var events []any
	for _, line := range strings.Split(strings.TrimSpace(res.events), "\n") {
		if ev := value(t, line); field(ev, "type") == "job_event" {
			events = append(events, field(ev, "data"))
		}
	}
	if !reflect.DeepEqual(events, []any{value(t, eventDoc(6)), value(t, eventDoc(7))}) {
		t.Errorf("job events %v, want the documents of 6 and 7", events)
	}
	if !slices.Equal(res.artifacts(), []string{"j"}) {
		t.Errorf("artifacts %v, want the job", res.artifacts())
	}
}

func TestJobs_RefuseNoIdAndANegativeSinceBeforeAnyRequest(t *testing.T) {
	svc := serve(t, nil)
	for _, args := range [][]string{{"get"}, {"cancel"}, {"retry"}, {"answer"}, {"watch"}, {"watch", "j", "--since", "-1"}, {"watch", "j", "--since", "x"}} {
		if res := af(t, append([]string{"jobs"}, args...)...); res.code != 2 {
			t.Errorf("%v: exit %d, want 2", args, res.code)
		}
	}
	if n := len(svc.requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestJobsWatch_AFollowingThatGivesUpSuggestsWatchingFromTheLastEvent(t *testing.T) {
	var tries atomic.Int32
	svc := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case !strings.HasSuffix(r.URL.Path, "/events"):
			answer(200, jobJSON("running", "", "", "running"))(w, r)
		case tries.Add(1) == 1:
			stream(frame(1), frame(2))(w, r)
		default:
			answer(502, `{"error":{"code":"hub_unavailable","message":"hub is down"}}`)(w, r)
		}
	})
	for _, c := range []struct {
		args []string
		next string
	}{
		{nil, "af jobs watch j --since 2"},
		{[]string{"--server", svc.URL}, "af jobs watch j --since 2 --server " + svc.URL},
	} {
		tries.Store(0)
		res := af(t, append([]string{"jobs", "watch", "j"}, c.args...)...)
		if res.code != 1 || res.category() != "hub" || res.retryable() != true || !slices.Equal(res.next(), []string{c.next}) {
			t.Errorf("%v: exit %d, category %v, retryable %v, next %v, want 1, hub, retryable and %q", c.args, res.code, res.category(), res.retryable(), res.next(), c.next)
		}
	}
	serve(t, answer(404, `{"error":{"code":"not_found","message":"job not found"}}`))
	if res := af(t, "jobs", "watch", "j"); res.code != 2 || len(res.next()) != 0 {
		t.Errorf("an unknown job: exit %d, next %v, want 2 and nothing to watch", res.code, res.next())
	}
}
