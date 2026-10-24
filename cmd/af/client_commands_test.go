package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	afclock "github.com/agent-fox-dev/agentfox/internal/afclient/fakeclock"
)

// reply is the answer of a routed service.
type reply struct {
	status int
	body   string
	stream bool // the body is frames of the event stream
}

// routes is a handler that answers "METHOD /escaped/path" with the reply of that key, and every
// other request with a 404.
func routes(m map[string]reply) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rep, ok := m[r.Method+" "+r.URL.EscapedPath()]
		if !ok {
			answering(404, `{"error":{"code":"not_found","message":"no such route"}}`)(w, r)
			return
		}
		if rep.stream {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(rep.status)
		_, _ = io.WriteString(w, rep.body)
	}
}

// useClockNow makes the pauses of a following cost nothing for the test.
func useClockNow(t *testing.T) {
	t.Helper()
	useClock(t, afclock.New(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)))
}

// bodyJSON is the JSON body of a recorded request.
func (r recordedRequest) bodyJSON(t *testing.T) any {
	t.Helper()
	var v any
	if len(r.Body) == 0 {
		return nil
	}
	if err := json.Unmarshal(r.Body, &v); err != nil {
		t.Fatalf("the body %q of %s %s is not JSON: %v", r.Body, r.Method, r.Path, err)
	}
	return v
}

// jsonValue is a JSON text as a value.
func jsonValue(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// requestLines are the requests the service has seen, as "METHOD path" lines.
func (s *fakeService) requestLines() []string {
	var out []string
	for _, r := range s.Requests() {
		out = append(out, r.Method+" "+r.Path)
	}
	return out
}

const subscriptionView = `{"slug":"widgets","token_user":null,"subscribed_by":"u","token_state":"ok","created_at":"2026-10-23T12:00:00Z","updated_at":"2026-10-23T12:00:00Z","future":{"a":[1,{"b":2}]}}`

// TestSubscribeSendsThePATAndThePrefixOnlyWhenGivenAndBothAnswersAreSuccess_TS_16_53 verifies
// TS-16-53.
// Verifies: 16-REQ-8.1
func TestSubscribeSendsThePATAndThePrefixOnlyWhenGivenAndBothAnswersAreSuccess_TS_16_53(t *testing.T) {
	pat := tokenFile(t, "pat-sentinel\n")
	for _, c := range []struct {
		status int
		word   string
		args   []string
		body   map[string]any
	}{
		{201, "subscribed", nil, map[string]any{"hub_token": "pat-sentinel"}},
		{200, "replaced", nil, map[string]any{"hub_token": "pat-sentinel"}},
	} {
		svc := ready(t, routes(map[string]reply{"PUT /api/v1/workspaces/widgets": {c.status, subscriptionView, false}}))
		res := afRun(t, "", append([]string{"workspace", "subscribe", "widgets", "--hub-token-file", pat}, c.args...)...)
		if res.code != 0 || field(res.env, "status") != "done" {
			t.Errorf("%d %v: exit %d, %s", c.status, c.args, res.code, res.message())
			continue
		}
		reqs := svc.Requests()
		if len(reqs) != 1 || reqs[0].Method != "PUT" || reqs[0].Path != "/api/v1/workspaces/widgets" || !reflect.DeepEqual(reqs[0].bodyJSON(t), any(c.body)) {
			t.Errorf("%d %v: requests %+v, want one PUT with the body %v", c.status, c.args, reqs, c.body)
		}
		if s, _ := res.env["summary"].(string); !strings.Contains(s, c.word) || !strings.Contains(s, "widgets") {
			t.Errorf("%d: summary %q, want it to say %s for widgets", c.status, s, c.word)
		}
		if !reflect.DeepEqual(res.env["result"], jsonValue(t, subscriptionView)) {
			t.Errorf("%d: result = %v, want the view as received", c.status, res.env["result"])
		}
		if strings.Contains(res.output(), "pat-sentinel") {
			t.Errorf("%d: the output holds the PAT", c.status)
		}
	}
}

// TestBadPATFilesFailBeforeAnyRequestIOForReadsAndUsageForTheContent_TS_16_54 verifies TS-16-54.
// Verifies: 16-REQ-8.2
func TestBadPATFilesFailBeforeAnyRequestIOForReadsAndUsageForTheContent_TS_16_54(t *testing.T) {
	svc := ready(t, nil)
	missing := filepath.Join(t.TempDir(), "missing")
	res := afRun(t, "", "workspace", "subscribe", "w", "--hub-token-file", missing)
	if res.code != 1 || res.category() != "io" || !strings.Contains(res.message(), missing) {
		t.Errorf("a missing file: exit %d, category %q, message %q, want 1, io, naming the file", res.code, res.category(), res.message())
	}
	res = afRunReader(t, failingReader{}, "workspace", "subscribe", "w", "--hub-token-file", "-")
	if res.code != 1 || res.category() != "io" || !strings.Contains(res.message(), "stdin") {
		t.Errorf("an unreadable stdin: exit %d, category %q, message %q, want 1, io, naming stdin", res.code, res.category(), res.message())
	}
	for name, content := range map[string]string{
		"empty": "\n", "multi-line": "pat-sentinel\nsecond\n", "control": "pat\x01-sentinel", "oversized": "pat-sentinel" + strings.Repeat("x", 4090),
	} {
		res := afRun(t, "", "workspace", "subscribe", "w", "--hub-token-file", tokenFile(t, content))
		if res.code != 2 || !strings.Contains(res.message(), "--hub-token-file") {
			t.Errorf("%s: exit %d, message %q, want 2 naming --hub-token-file", name, res.code, res.message())
		}
		if strings.Contains(res.output(), "pat-sentinel") || strings.Contains(res.output(), "pat\x01") {
			t.Errorf("%s: the output echoes the PAT:\n%s", name, res.output())
		}
	}
	if res := afRun(t, "", "workspace", "subscribe", "w"); res.code != 2 || !strings.Contains(res.message(), "--hub-token-file") {
		t.Errorf("no --hub-token-file: exit %d, message %q, want 2 naming the flag", res.code, res.message())
	}
	if res := afRun(t, "", "workspace", "subscribe", "--hub-token-file", tokenFile(t, "pat")); res.code != 2 {
		t.Errorf("no slug: exit %d, want 2", res.code)
	}
	if n := len(svc.Requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

// TestWorkspaceListGetAndUnsubscribeUseTheirRoutesAndReturnTheirResults_TS_16_55 verifies
// TS-16-55.
// Verifies: 16-REQ-8.3
func TestWorkspaceListGetAndUnsubscribeUseTheirRoutesAndReturnTheirResults_TS_16_55(t *testing.T) {
	list := `{"workspaces":[` + subscriptionView + `],"future":true}`
	svc := ready(t, routes(map[string]reply{
		"GET /api/v1/workspaces":            {200, list, false},
		"GET /api/v1/workspaces/w":          {200, subscriptionView, false},
		"DELETE /api/v1/workspaces/w":       {204, ``, false},
		"GET /api/v1/workspaces/BAD%21":     {400, `{"error":{"code":"invalid_request","message":"the slug is not valid"}}`, false},
		"DELETE /api/v1/workspaces/missing": {404, `{"error":{"code":"not_found","message":"workspace not found"}}`, false},
	}))

	if res := afRun(t, "", "workspace", "list"); res.code != 0 || !reflect.DeepEqual(res.env["result"], jsonValue(t, list)) {
		t.Errorf("list: exit %d, result %v, want the document as received", res.code, res.env["result"])
	}
	if res := afRun(t, "", "workspace", "get", "w"); res.code != 0 || !reflect.DeepEqual(res.env["result"], jsonValue(t, subscriptionView)) {
		t.Errorf("get: exit %d, result %v, want the view as received", res.code, res.env["result"])
	}
	res := afRun(t, "", "workspace", "unsubscribe", "w")
	if res.code != 0 || !reflect.DeepEqual(res.env["result"], map[string]any{"slug": "w"}) {
		t.Errorf("unsubscribe: exit %d, result %v, want {slug: w}", res.code, res.env["result"])
	}
	if s, _ := res.env["summary"].(string); !strings.Contains(s, "open jobs") || !strings.Contains(s, "cancelled") {
		t.Errorf("unsubscribe: summary %q, want it to say that the open jobs of the workspace were cancelled", s)
	}
	if res := afRun(t, "", "workspace", "get", "BAD!"); res.code != 2 || res.category() != "usage" {
		t.Errorf("a bad slug: exit %d, category %q, want 2 and usage", res.code, res.category())
	}
	res = afRun(t, "", "workspace", "unsubscribe", "missing")
	if res.code != 2 || !strings.Contains(res.message(), `"missing"`) {
		t.Errorf("an unknown workspace: exit %d, message %q, want 2 naming it", res.code, res.message())
	}
	if h := res.hint(); h["suggest"] != "af workspace subscribe missing --hub-token-file FILE" {
		t.Errorf("fix_hint = %v, want the subscribe command with the real slug", h)
	}
	for _, args := range [][]string{{"workspace", "get"}, {"workspace", "unsubscribe"}} {
		if res := afRun(t, "", args...); res.code != 2 {
			t.Errorf("%v with no slug: exit %d, want 2", args, res.code)
		}
	}
	var escaped bool
	for _, r := range svc.Requests() {
		if strings.Contains(r.RequestURI, "BAD!") {
			t.Errorf("the slug was not escaped: %s", r.RequestURI)
		}
		escaped = escaped || strings.HasSuffix(r.RequestURI, "/BAD%21")
	}
	if !escaped {
		t.Errorf("no request carried the escaped slug: %v", svc.requestLines())
	}
}

// TestJobsListPicksTheRoutePassesStateAndCursorOnlyWhenGivenAndNextRepeatsTheCommand_TS_16_56
// verifies TS-16-56.
// Verifies: 16-REQ-8.4
func TestJobsListPicksTheRoutePassesStateAndCursorOnlyWhenGivenAndNextRepeatsTheCommand_TS_16_56(t *testing.T) {
	page := `{"jobs":[{"id":"j1","future":{"a":1}},{"id":"j2"}],"next_cursor":"c2","extra":[true]}`
	last := `{"jobs":[],"next_cursor":null}`
	svc := ready(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Query().Get("cursor") == "bad":
			answering(400, `{"error":{"code":"invalid_request","message":"bad cursor"}}`)(w, r)
		case r.URL.Query().Get("cursor") == "c2":
			answering(200, last)(w, r)
		default:
			answering(200, page)(w, r)
		}
	})
	nextArgv := func(res afResult) []string {
		next, _ := res.env["next"].([]any)
		if len(next) != 1 {
			return nil
		}
		var out []string
		for _, a := range next[0].(map[string]any)["argv"].([]any) {
			out = append(out, a.(string))
		}
		return out
	}

	res := afRun(t, "", "jobs", "list")
	if res.code != 0 || !reflect.DeepEqual(res.env["result"], jsonValue(t, page)) {
		t.Fatalf("exit %d, result %v, want the page as received", res.code, res.env["result"])
	}
	if got, want := nextArgv(res), []string{"af", "jobs", "list", "--cursor", "c2"}; !slices.Equal(got, want) {
		t.Errorf("next = %v, want %v", got, want)
	}
	res = afRun(t, "", "jobs", "list", "--workspace", "S", "--state", "blocked", "--cursor", "c1", "--server", svc.URL)
	if res.code != 0 {
		t.Fatalf("exit %d, %s", res.code, res.message())
	}
	if got, want := nextArgv(res), []string{"af", "jobs", "list", "--workspace", "S", "--state", "blocked", "--server", svc.URL, "--cursor", "c2"}; !slices.Equal(got, want) {
		t.Errorf("next = %v, want the command with the next cursor last: %v", got, want)
	}
	res = afRun(t, "", "jobs", "list", "--cursor", "c2")
	if res.code != 0 || len(res.env["next"].([]any)) != 0 || !reflect.DeepEqual(res.env["result"], jsonValue(t, last)) {
		t.Errorf("the last page: exit %d, next %v, result %v, want no next", res.code, res.env["next"], res.env["result"])
	}
	if res := afRun(t, "", "jobs", "list", "--cursor", "bad"); res.code != 2 || res.category() != "usage" {
		t.Errorf("a bad cursor: exit %d, category %q, want 2 and usage", res.code, res.category())
	}

	var queries []string
	var paths []string
	for _, r := range svc.Requests() {
		paths = append(paths, r.Path)
		queries = append(queries, r.RequestURI[len(r.Path):])
	}
	if want := []string{"/api/v1/jobs", "/api/v1/workspaces/S/jobs", "/api/v1/jobs", "/api/v1/jobs"}; !slices.Equal(paths, want) {
		t.Errorf("routes = %v, want %v", paths, want)
	}
	if want := []string{"", "?cursor=c1&state=blocked", "?cursor=c2", "?cursor=bad"}; !slices.Equal(queries, want) {
		t.Errorf("queries = %v, want %v: state and cursor only when given", queries, want)
	}
}

// TestJobsGetCancelRetryAndAnswerSendTheirRequestsAndWaitFollowsTheRecordedJob_TS_16_57
// verifies TS-16-57.
// Verifies: 16-REQ-8.5
func TestJobsGetCancelRetryAndAnswerSendTheirRequestsAndWaitFollowsTheRecordedJob_TS_16_57(t *testing.T) {
	queued := jobDocument("queued", attemptList("queued"), "", "")
	done := jobDocument("done", attemptList("done"), `{"status":"done","exit_code":0,"summary":"s","error":null}`, "")
	svc := ready(t, routes(map[string]reply{
		"GET /api/v1/jobs/j":                     {200, done, false},
		"POST /api/v1/jobs/j/cancel":             {200, jobDocument("cancelled", attemptList("cancelled"), "", ""), false},
		"POST /api/v1/jobs/j/retry":              {202, queued, false},
		"POST /api/v1/jobs/j/answer":             {202, queued, false},
		"GET /api/v1/jobs/j/events":              {200, eventFrame(1), true},
		"GET /api/v1/jobs/j/attempts/1/envelope": {200, `{"status":"done","warnings":[]}`, false},
	}))
	useClockNow(t)

	for _, c := range []struct {
		args []string
		want string
		body any
	}{
		{[]string{"jobs", "get", "j"}, "GET /api/v1/jobs/j", nil},
		{[]string{"jobs", "cancel", "j"}, "POST /api/v1/jobs/j/cancel", nil},
		{[]string{"jobs", "retry", "j"}, "POST /api/v1/jobs/j/retry", map[string]any{}},
		{[]string{"jobs", "answer", "j", "--option", "a"}, "POST /api/v1/jobs/j/answer", map[string]any{"option": "A"}},
	} {
		before := len(svc.Requests())
		res := afRun(t, "", c.args...)
		reqs := svc.Requests()[before:]
		if res.code != 0 || field(res.env, "status") != "done" {
			t.Errorf("%v: exit %d, %s", c.args, res.code, res.message())
			continue
		}
		if len(reqs) != 1 || reqs[0].Method+" "+reqs[0].Path != c.want || !reflect.DeepEqual(reqs[0].bodyJSON(t), c.body) {
			t.Errorf("%v: requests %+v, want %s with the body %v", c.args, reqs, c.want, c.body)
		}
		result := res.result()
		if env, present := result["envelope"]; !present || env != nil || result["job"] == nil {
			t.Errorf("%v: result = %v, want {job, envelope: null}", c.args, result)
		}
		if artifacts, _ := res.env["artifacts"].([]any); len(artifacts) != 0 {
			t.Errorf("%v: artifacts = %v, want none without --wait", c.args, artifacts)
		}
	}
	if got := afRun(t, "", "jobs", "get", "j").result()["job"]; !reflect.DeepEqual(got, jsonValue(t, done)) {
		t.Errorf("jobs get: job = %v, want the document as received", got)
	}

	// With --wait the job is recorded and followed to rest.
	for _, args := range [][]string{{"jobs", "retry", "j", "--wait"}, {"jobs", "answer", "j", "text", "--wait"}} {
		res := afRun(t, "", args...)
		artifacts, _ := res.env["artifacts"].([]any)
		if res.code != 0 || len(artifacts) != 1 || artifacts[0].(map[string]any)["name"] != "j" {
			t.Errorf("%v: exit %d, artifacts %v, %s", args, res.code, artifacts, res.message())
		}
		if job, _ := res.result()["job"].(map[string]any); job["state"] != "done" || res.result()["envelope"] == nil {
			t.Errorf("%v: result = %v, want the job at rest and its envelope", args, res.result())
		}
	}

	// cancel takes no --wait.
	if res := afRun(t, "", "jobs", "cancel", "j", "--wait"); res.code != 2 || !strings.Contains(res.message(), "wait") {
		t.Errorf("cancel --wait: exit %d, message %q, want 2 refusing the flag", res.code, res.message())
	}
}

// TestAnswerNeedsExactlyOneAnswerAndSendsNothingOtherwise_TS_16_58 verifies TS-16-58.
// Verifies: 16-REQ-8.6
func TestAnswerNeedsExactlyOneAnswerAndSendsNothingOtherwise_TS_16_58(t *testing.T) {
	svc := ready(t, nil)
	for _, args := range [][]string{
		{"jobs", "answer", "j"},
		{"jobs", "answer", "j", "t", "--context", "u"},
		{"jobs", "answer", "j", "--context", "u", "--option", "A"},
		{"jobs", "answer", "j", "t", "--option", "A"},
		{"jobs", "answer", "j", "--context", "u", "--context", "v"},
		{"jobs", "answer", "j", "--option", ""},
		{"jobs", "answer"},
	} {
		res := afRun(t, "", args...)
		if res.code != 2 || field(res.env, "status") != "usage" || strings.Contains(res.message(), "unknown command") {
			t.Errorf("%v: exit %d, status %v, message %q, want a usage error about the answer", args, res.code, field(res.env, "status"), res.message())
		}
	}
	if n := len(svc.Requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

// TestAnswerUppercasesTheOptionAndSendsTextThatMatchesAnOptionIgnoringCaseAsTheOption_TS_16_59
// verifies TS-16-59.
// Verifies: 16-REQ-8.7
func TestAnswerUppercasesTheOptionAndSendsTextThatMatchesAnOptionIgnoringCaseAsTheOption_TS_16_59(t *testing.T) {
	blocked := jobDocument("blocked", attemptList("needs_human"), "", `{"reason":"needs_human","question":"Which?","options":[{"id":"A","label":"main"},{"id":"B","label":"release"}],"answer_with":"x"}`)
	svc := ready(t, routes(map[string]reply{
		"GET /api/v1/jobs/j":         {200, blocked, false},
		"POST /api/v1/jobs/j/answer": {202, blocked, false},
	}))
	for _, c := range []struct {
		args  []string
		reads bool
		body  map[string]any
	}{
		{[]string{"--option", "b"}, false, map[string]any{"option": "B"}},
		{[]string{"b"}, true, map[string]any{"option": "B"}},
		{[]string{"--context", "B"}, true, map[string]any{"option": "B"}},
		{[]string{"free text"}, true, map[string]any{"answer": "free text"}},
		{[]string{"  spaced  "}, true, map[string]any{"answer": "spaced"}},
	} {
		before := len(svc.Requests())
		res := afRun(t, "", append([]string{"jobs", "answer", "j"}, c.args...)...)
		reqs := svc.Requests()[before:]
		if res.code != 0 {
			t.Errorf("%v: exit %d, %s", c.args, res.code, res.message())
			continue
		}
		wantLines := []string{"POST /api/v1/jobs/j/answer"}
		if c.reads {
			wantLines = []string{"GET /api/v1/jobs/j", "POST /api/v1/jobs/j/answer"}
		}
		var lines []string
		for _, r := range reqs {
			lines = append(lines, r.Method+" "+r.Path)
		}
		if !slices.Equal(lines, wantLines) || !reflect.DeepEqual(reqs[len(reqs)-1].bodyJSON(t), any(c.body)) {
			t.Errorf("%v: requests %v with the body %v, want %v with %v", c.args, lines, reqs[len(reqs)-1].bodyJSON(t), wantLines, c.body)
		}
	}
	before := len(svc.Requests())
	for _, blank := range []string{"  ", "\t"} {
		if res := afRun(t, "", "jobs", "answer", "j", blank); res.code != 2 {
			t.Errorf("blank text %q: exit %d, want 2", blank, res.code)
		}
	}
	if res := afRun(t, "", "jobs", "answer", "j", "--context", "  "); res.code != 2 {
		t.Errorf("blank --context: exit %d, want 2", res.code)
	}
	if n := len(svc.Requests()) - before; n != 0 {
		t.Errorf("%d requests were sent for blank answers", n)
	}
}

// TestTheResumeArgvOfABlockedJobParsesAndSendsTheOption_TS_16_59 verifies the resume argv that
// TS-16-72 hands out: af jobs answer <id> --wait with --context B appended sends {option: B}, and
// follows the job to rest.
// Verifies: 16-REQ-8.7, 16-REQ-10.4
func TestTheResumeArgvOfABlockedJobParsesAndSendsTheOption_TS_16_59(t *testing.T) {
	blocked := jobDocument("blocked", attemptList("needs_human"), "", `{"reason":"needs_human","question":"Which?","options":[{"id":"A","label":"main"},{"id":"B","label":"release"}],"answer_with":"x"}`)
	done := jobDocument("done", attemptList("needs_human", "done"), `{"status":"done","exit_code":0,"summary":"attempt 2","error":null}`, "")
	var answered atomic.Bool
	svc := ready(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/answer"):
			answered.Store(true)
			answering(202, blocked)(w, r)
		case strings.HasSuffix(r.URL.Path, "/events"):
			routes(map[string]reply{"GET " + r.URL.Path: {200, eventFrame(1), true}})(w, r)
		case strings.Contains(r.URL.Path, "/attempts/"):
			answering(200, `{"status":"done"}`)(w, r)
		case answered.Load():
			answering(200, done)(w, r)
		default:
			answering(200, blocked)(w, r)
		}
	})
	useClockNow(t)
	resume := []string{"jobs", "answer", "j", "--wait"}
	res := afRun(t, "", append(resume, "--context", "B")...)
	if res.code != 0 || field(res.env, "status") != "done" {
		t.Fatalf("exit %d, status %v, %s", res.code, field(res.env, "status"), res.message())
	}
	var posted any
	for _, r := range svc.Requests() {
		if r.Method == "POST" {
			posted = r.bodyJSON(t)
		}
	}
	if !reflect.DeepEqual(posted, any(map[string]any{"option": "B"})) {
		t.Errorf("the answer posted was %v, want {option: B}", posted)
	}
}

// TestAConflictOnAJobCommandFailsConflictWithTheServicesMessageAndTheHint_TS_16_60 verifies
// TS-16-60.
// Verifies: 16-REQ-8.8
func TestAConflictOnAJobCommandFailsConflictWithTheServicesMessageAndTheHint_TS_16_60(t *testing.T) {
	for _, code := range []string{"invalid_state", "not_subscribed", "token_unreadable"} {
		ready(t, answering(409, `{"error":{"code":"`+code+`","message":"the service says no"}}`))
		for _, args := range [][]string{{"jobs", "cancel", "j"}, {"jobs", "retry", "j"}, {"jobs", "answer", "j", "--option", "A"}} {
			res := afRun(t, "", args...)
			if res.code != 1 || res.category() != "conflict" || res.retryable() {
				t.Errorf("%s %v: exit %d, category %q, retryable %v, want 1, conflict, not retryable", code, args, res.code, res.category(), res.retryable())
			}
			if m := res.message(); !strings.Contains(m, "the service says no") || !strings.Contains(m, "("+code+")") {
				t.Errorf("%s %v: message %q, want the service's text and code", code, args, m)
			}
			h := res.hint()
			if code == "invalid_state" {
				if res.env["error"].(map[string]any)["fix_hint"] != nil {
					t.Errorf("%s %v: fix_hint = %v, want none", code, args, h)
				}
			} else if h["suggest"] != "af workspace subscribe <slug> --hub-token-file FILE" {
				t.Errorf("%s %v: fix_hint = %v, want the subscribe hint", code, args, h)
			}
		}
	}
}

// TestTheEscapedSlugReachesTheRouteOfEveryWorkspaceCommand_TS_16_55 verifies, for the commands
// of the slug, that a slug is path-escaped.
// Verifies: 16-REQ-8.3
func TestTheEscapedSlugReachesTheRouteOfEveryWorkspaceCommand_TS_16_55(t *testing.T) {
	slug := "a b/c"
	svc := ready(t, routes(map[string]reply{
		"GET /api/v1/workspaces/" + url.PathEscape(slug):    {200, subscriptionView, false},
		"DELETE /api/v1/workspaces/" + url.PathEscape(slug): {204, ``, false},
	}))
	if res := afRun(t, "", "workspace", "get", slug); res.code != 0 {
		t.Errorf("get: exit %d, %s", res.code, res.message())
	}
	if res := afRun(t, "", "workspace", "unsubscribe", slug); res.code != 0 || !reflect.DeepEqual(res.env["result"], map[string]any{"slug": slug}) {
		t.Errorf("unsubscribe: exit %d, result %v", res.code, res.env["result"])
	}
	if got := svc.Requests(); len(got) != 2 || got[0].RequestURI != "/api/v1/workspaces/a%20b%2Fc" {
		t.Errorf("requests %+v, want the escaped slug", got)
	}
}
