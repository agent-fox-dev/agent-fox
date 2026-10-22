package jobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TestEveryValidBodyCreatesAQueuedJobFromTheApi_TS_12_15 verifies TS-12-15: a valid
// triage body (an issue, a text with a title, a file, each with context) and a
// valid fix body answer 202 with a queued job whose source is the api and the
// hub user, and whose request label is null.
// Verifies: 12-REQ-3.1
func TestEveryValidBodyCreatesAQueuedJobFromTheApi_TS_12_15(t *testing.T) {
	e := newEnv(t)
	for name, tc := range map[string]struct {
		path  string
		body  string
		kind  jobs.Kind
		issue *int64
		input string
		notes []string
	}{
		"triage of an issue":  {"/api/v1/workspaces/widgets/triage", `{"issue":7}`, jobs.KindTriage, ptr(7), `{"issue":7}`, nil},
		"triage of an URL":    {"/api/v1/workspaces/widgets/triage", `{"issue":"https://github.example.com/acme/widgets/issues/8"}`, jobs.KindTriage, ptr(8), `{"issue":"https://github.example.com/acme/widgets/issues/8"}`, nil},
		"triage of a text":    {"/api/v1/workspaces/widgets/triage", `{"text":"the build is broken","title":"broken","context":["see the CI","and the log"]}`, jobs.KindTriage, nil, `{"text":"the build is broken","title":"broken"}`, []string{"see the CI", "and the log"}},
		"triage of a file":    {"/api/v1/workspaces/widgets/triage", `{"file":{"name":"report.txt","content":"it fails"},"context":["a note"]}`, jobs.KindTriage, nil, `{"file":{"name":"report.txt","content":"it fails"}}`, []string{"a note"}},
		"fix of an issue":     {"/api/v1/workspaces/widgets/fix", `{"issue":42,"context":["be careful"]}`, jobs.KindFix, ptr(42), `{"issue":42}`, []string{"be careful"}},
		"fix of owner/repo#N": {"/api/v1/workspaces/widgets/fix", `{"issue":"acme/widgets#43"}`, jobs.KindFix, ptr(43), `{"issue":"acme/widgets#43"}`, nil},
	} {
		t.Run(name, func(t *testing.T) {
			r := e.call("POST", tc.path, tc.body)
			if r.Status != http.StatusAccepted {
				t.Fatalf("status %d, want 202\n%s", r.Status, r.Body)
			}
			doc := r.Doc(t)
			if _, has := r.JSON(t)["attempts"]; has {
				t.Errorf("the answer lists the attempts, which only the view of one job does")
			}
			if doc.State != jobs.StateQueued || doc.Kind != tc.kind || doc.Workspace != "widgets" {
				t.Errorf("job = %s %s on %s, want a queued %s on widgets", doc.State, doc.Kind, doc.Workspace, tc.kind)
			}
			if doc.Source != (jobs.Source{Kind: "api", By: callerID}) || doc.RequestLabel != nil {
				t.Errorf("source %+v, request_label %v, want {api %s} and null", doc.Source, doc.RequestLabel, callerID)
			}
			if (tc.issue == nil) != (doc.Issue == nil) || (tc.issue != nil && *tc.issue != *doc.Issue) {
				t.Errorf("issue = %v, want %v", doc.Issue, tc.issue)
			}
			if got, _ := json.Marshal(doc.Input); string(got) != tc.input {
				t.Errorf("input = %s, want %s, as posted and without the context", got, tc.input)
			}
			if doc.AttemptCount != 1 || doc.CurrentAttempt != 1 || doc.StartedAt != nil || doc.FinishedAt != nil {
				t.Errorf("attempt_count %d, current_attempt %d, started_at %v, finished_at %v, want 1, 1, null, null", doc.AttemptCount, doc.CurrentAttempt, doc.StartedAt, doc.FinishedAt)
			}
			var notes string
			if err := e.DB.QueryRow("SELECT context FROM af_jobs WHERE id = ?", doc.ID).Scan(&notes); err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(append([]string{}, tc.notes...))
			if notes != string(want) {
				t.Errorf("stored context = %s, want %s", notes, want)
			}
		})
	}
}

func ptr(n int64) *int64 { return &n }

// TestEveryInvalidFieldIsRefusedNamingItAndStoresNothing_TS_12_16 verifies TS-12-16:
// each field that breaks its rule answers 400 invalid_request with a message that
// names the field, and no job row is written.
// Verifies: 12-REQ-3.2
func TestEveryInvalidFieldIsRefusedNamingItAndStoresNothing_TS_12_16(t *testing.T) {
	e := newEnv(t)
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	long := func(n int) string { return strings.Repeat("a", n) }
	notes := func(n int, each string) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = q(each)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	cases := []struct{ name, body, field string }{
		{"an empty text", `{"text":""}`, "text"},
		{"a text of white space", `{"text":" \n\t "}`, "text"},
		{"a text that is not UTF-8", "{\"text\":\"a\xffb\"}", "text"},
		{"a title that is not UTF-8", "{\"text\":\"x\",\"title\":\"a\xffb\"}", "title"},
		{"a file name that is not UTF-8", "{\"file\":{\"name\":\"a\xff\",\"content\":\"x\"}}", "file.name"},
		{"a file content that is not UTF-8", "{\"file\":{\"name\":\"a\",\"content\":\"\xff\"}}", "file.content"},
		{"a text with a NUL byte", `{"text":"a\u0000b"}`, "text"},
		{"a text over the limit", `{"text":` + q(long(toolio.MaxInputBytes+1)) + `}`, "text"},
		{"a title with a newline", `{"text":"x","title":"a\nb"}`, "title"},
		{"a title with a return", `{"text":"x","title":"a\rb"}`, "title"},
		{"a title of 257 bytes", `{"text":"x","title":` + q(long(257)) + `}`, "title"},
		{"a title with an issue", `{"issue":1,"title":"t"}`, "title"},
		{"a file name with a slash", `{"file":{"name":"a/b","content":"x"}}`, "file.name"},
		{"a file name with a NUL byte", `{"file":{"name":"a\u0000b","content":"x"}}`, "file.name"},
		{"an empty file name", `{"file":{"name":"","content":"x"}}`, "file.name"},
		{"a file name of 256 bytes", `{"file":{"name":` + q(long(256)) + `,"content":"x"}}`, "file.name"},
		{"an empty file content", `{"file":{"name":"a","content":"  "}}`, "file.content"},
		{"a file content with a NUL byte", `{"file":{"name":"a","content":"x\u0000"}}`, "file.content"},
		{"a file content over the limit", `{"file":{"name":"a","content":` + q(long(toolio.MaxInputBytes+1)) + `}}`, "file.content"},
		{"eleven context strings", `{"issue":1,"context":` + notes(11, "n") + `}`, "context"},
		{"an empty context string", `{"issue":1,"context":["a",""]}`, "context"},
		{"a context string of white space", `{"issue":1,"context":["  "]}`, "context"},
		{"a context string of 8 KiB and a byte", `{"issue":1,"context":[` + q(long(8193)) + `]}`, "context"},
		{"a context of 35 KiB", `{"issue":1,"context":` + notes(5, long(7*1024)) + `}`, "context"},
		{"a context that is not an array", `{"issue":1,"context":"one"}`, "context"},
		{"none of issue, text and file", `{}`, "exactly one of issue, text and file"},
		{"an issue and a text", `{"issue":1,"text":"x"}`, "exactly one of issue, text and file"},
		{"a text and a file", `{"text":"x","file":{"name":"a","content":"x"}}`, "exactly one of issue, text and file"},
		{"an unknown field", `{"issue":1,"bogus":2}`, "bogus"},
		{"an unknown field of the file", `{"file":{"name":"a","content":"x","mode":1}}`, "mode"},
		{"an issue of zero", `{"issue":0}`, "issue"},
		{"an issue that is a pull request", `{"issue":"https://github.example.com/acme/widgets/pull/3"}`, "issue"},
		{"an issue that is a word", `{"issue":"forty-two"}`, "issue"},
		{"an issue that is a fraction", `{"issue":4.2}`, "issue"},
		{"an issue that is null", `{"issue":null}`, "exactly one of issue, text and file"},
		{"a text of the wrong type", `{"text":5}`, "text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := e.call("POST", "/api/v1/workspaces/widgets/triage", tc.body)
			code, message := r.errorOf(t)
			if r.Status != http.StatusBadRequest || code != "invalid_request" {
				t.Fatalf("status %d code %q, want 400 invalid_request\n%s", r.Status, code, r.Body)
			}
			if !strings.Contains(message, tc.field) {
				t.Errorf("message %q does not name %q", message, tc.field)
			}
		})
	}
	// A fix takes an issue and a context, and nothing else.
	for name, tc := range map[string]struct{ body, field string }{
		"a fix without an issue": {`{"context":["a"]}`, "issue"},
		"a fix with a text":      {`{"issue":1,"text":"x"}`, "text"},
		"a fix with a file":      {`{"file":{"name":"a","content":"x"}}`, "file"},
		"a fix with a title":     {`{"issue":1,"title":"x"}`, "title"},
		"a fix of a pull":        {`{"issue":"https://github.example.com/acme/widgets/pull/3"}`, "issue"},
		"a fix with null":        {`{"issue":null}`, "issue"},
	} {
		t.Run(name, func(t *testing.T) {
			r := e.call("POST", "/api/v1/workspaces/widgets/fix", tc.body)
			code, message := r.errorOf(t)
			if r.Status != http.StatusBadRequest || code != "invalid_request" || !strings.Contains(message, tc.field) {
				t.Errorf("status %d, %s %q, want 400 invalid_request naming %q", r.Status, code, message, tc.field)
			}
		})
	}
	if n := e.count("SELECT count(*) FROM af_jobs"); n != 0 {
		t.Errorf("%d jobs were stored by requests that were all refused", n)
	}
	if n := e.count("SELECT count(*) FROM af_attempts") + e.count("SELECT count(*) FROM af_events"); n != 0 {
		t.Errorf("%d attempts and events were stored by requests that were all refused", n)
	}
}

// TestFieldsAtTheirLimitsAreAccepted_TS_12_16 is the other side of TS-12-16: a field
// that is exactly as large as its rule allows is accepted, so that each limit is
// where the rule puts it.
// Verifies: 12-REQ-3.2
func TestFieldsAtTheirLimitsAreAccepted_TS_12_16(t *testing.T) {
	e := newEnv(t)
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	long := func(n int) string { return strings.Repeat("a", n) }
	four := q(long(8192)) + "," + q(long(8192)) + "," + q(long(8192)) + "," + q(long(8192)) // 32 KiB in all
	ten := strings.TrimSuffix(strings.Repeat(q("n")+",", 10), ",")
	for name, body := range map[string]string{
		"a text of the largest size":      `{"text":` + q(long(toolio.MaxInputBytes)) + `}`,
		"a title of 256 bytes":            `{"text":"x","title":` + q(long(256)) + `}`,
		"a file name of 255 bytes":        `{"file":{"name":` + q(long(255)) + `,"content":"x"}}`,
		"a file content of the largest":   `{"file":{"name":"a","content":` + q(long(toolio.MaxInputBytes)) + `}}`,
		"ten context strings":             `{"issue":1,"context":[` + ten + `]}`,
		"a context of 4 strings of 8 KiB": `{"issue":2,"context":[` + four + `]}`,
		"an issue string of 2048 bytes":   `{"issue":` + q("a/b"+long(2042)+"#99") + `}`, // 2048 bytes
	} {
		t.Run(name, func(t *testing.T) {
			if r := e.call("POST", "/api/v1/workspaces/widgets/triage", body); r.Status != http.StatusAccepted {
				t.Errorf("status %d, want 202\n%.300s", r.Status, r.Body)
			}
		})
	}
	// One byte more in the total of the context is refused although no string is too long.
	over := q(long(8192)) + "," + q(long(8192)) + "," + q(long(8192)) + "," + q(long(8191)) + "," + q("ab")
	r := e.call("POST", "/api/v1/workspaces/widgets/triage", `{"issue":3,"context":[`+over+`]}`)
	if _, message := r.errorOf(t); r.Status != http.StatusBadRequest || !strings.Contains(message, "context") {
		t.Errorf("a context of 32 KiB and a byte: status %d %q, want 400 naming context", r.Status, message)
	}
}

// TestAnIssueThatIsNotValidIsRefusedByTheRouteNamingIssue_TS_12_17 is the route's
// half of TS-12-17: a value the parser refuses answers 400 naming issue.
// Verifies: 12-REQ-3.3
func TestAnIssueThatIsNotValidIsRefusedByTheRouteNamingIssue_TS_12_17(t *testing.T) {
	e := newEnv(t)
	for _, issue := range []string{`0`, `-3`, `4.5`, `"https://gitlab.example.com/acme/widgets/-/merge_requests/5"`, `"https://example.com/x/y/issues/1"`, `"nope"`, `"a#1"`, `[1]`} {
		for _, path := range []string{"triage", "fix"} {
			r := e.call("POST", "/api/v1/workspaces/widgets/"+path, `{"issue":`+issue+`}`)
			if _, message := r.errorOf(t); r.Status != http.StatusBadRequest || !strings.Contains(message, "issue") {
				t.Errorf("%s with issue %s: status %d %q, want 400 naming issue", path, issue, r.Status, message)
			}
		}
	}
	// The number is taken from the URL and from owner/repo#N, and kept as posted.
	r := e.call("POST", "/api/v1/workspaces/widgets/fix", `{"issue":"https://gitlab.example.com/g/s/widgets/-/issues/77"}`)
	if doc := r.Doc(t); r.Status != 202 || doc.Issue == nil || *doc.Issue != 77 {
		t.Errorf("an issue URL of GitLab: status %d issue %v, want 202 and 77", r.Status, doc.Issue)
	}
}

// TestRequestsForWorkspacesThatCannotTakeAJobAreRefusedWithTheirCodes_TS_12_19
// verifies TS-12-19: a workspace the caller does not see, one that is not
// subscribed and one that is being unsubscribed are 404, a token that does not open
// is 409 token_unreadable, and a second open job of a kind for an issue is 409
// duplicate_job naming the first, while the other kind is accepted.
// Verifies: 12-REQ-3.5
func TestRequestsForWorkspacesThatCannotTakeAJobAreRefusedWithTheirCodes_TS_12_19(t *testing.T) {
	e := newEnv(t)
	e.Hub.Grant(callerID, "unsubscribed", "unsubscribing", "locked")
	e.subscribe("gadgets") // subscribed, but hub does not show it to the caller
	e.subscribe("unsubscribing")
	e.subscribe("locked")
	if _, err := e.DB.Exec("UPDATE af_subscriptions SET sealed_token = x'0102' WHERE slug = 'locked'"); err != nil {
		t.Fatal(err)
	}
	clear := e.Svc.MarkUnsubscribing("unsubscribing")
	defer clear()

	first := e.call("POST", "/api/v1/workspaces/widgets/fix", `{"issue":42}`)
	if first.Status != 202 {
		t.Fatalf("the first job: status %d\n%s", first.Status, first.Body)
	}
	id := first.Doc(t).ID

	for name, tc := range map[string]struct {
		path    string
		status  int
		code    string
		message string
	}{
		"a workspace hub does not show":      {"gadgets", 404, "not_found", ""},
		"a workspace that is not subscribed": {"unsubscribed", 404, "not_found", ""},
		"a workspace being unsubscribed":     {"unsubscribing", 404, "not_found", ""},
		"a token that does not open":         {"locked", 409, "token_unreadable", ""},
	} {
		t.Run(name, func(t *testing.T) {
			r := e.call("POST", "/api/v1/workspaces/"+tc.path+"/fix", `{"issue":42}`)
			code, _ := r.errorOf(t)
			if r.Status != tc.status || code != tc.code {
				t.Errorf("status %d %s, want %d %s\n%s", r.Status, code, tc.status, tc.code, r.Body)
			}
		})
	}
	for _, issue := range []string{`42`, `"https://github.example.com/acme/widgets/issues/42"`, `"acme/widgets#42"`} {
		r := e.call("POST", "/api/v1/workspaces/widgets/fix", `{"issue":`+issue+`}`)
		code, message := r.errorOf(t)
		if r.Status != 409 || code != "duplicate_job" || !strings.Contains(message, id) {
			t.Errorf("issue %s again: status %d %s %q, want 409 duplicate_job naming %s", issue, r.Status, code, message, id)
		}
	}
	if r := e.call("POST", "/api/v1/workspaces/widgets/triage", `{"issue":42}`); r.Status != 202 {
		t.Errorf("a triage of issue 42 beside the fix: status %d, want 202\n%s", r.Status, r.Body)
	}
	if r := e.call("POST", "/api/v1/workspaces/widgets/fix", `{"issue":43}`); r.Status != 202 {
		t.Errorf("a fix of issue 43: status %d, want 202", r.Status)
	}
	if n := e.count("SELECT count(*) FROM af_jobs"); n != 3 {
		t.Errorf("%d jobs stored, want 3: the fix, the triage and the other fix", n)
	}

	// The answer for a workspace that is hidden says nothing of the others.
	hidden := e.call("POST", "/api/v1/workspaces/gadgets/fix", `{"issue":1}`)
	absent := e.call("POST", "/api/v1/workspaces/never-heard-of/fix", `{"issue":1}`)
	if string(hidden.Body) != string(absent.Body) || hidden.Status != 404 {
		t.Errorf("a hidden workspace is answered %d %s and a missing one %d %s, want the same answer", hidden.Status, hidden.Body, absent.Status, absent.Body)
	}
}

// TestTheJobItsAttemptAndItsFirstEventAreStoredTogetherAndTheQueueIsAskedAfter_TS_12_20
// verifies TS-12-20: creation stores the job, attempt 1 (trigger create) and event 1
// (job_state queued) in one transaction, and enqueues only once it has committed; a
// failure of the third insert leaves nothing and enqueues nothing.
// Verifies: 12-REQ-3.6
func TestTheJobItsAttemptAndItsFirstEventAreStoredTogetherAndTheQueueIsAskedAfter_TS_12_20(t *testing.T) {
	var (
		mu        sync.Mutex
		failOp    string
		enqueued  []string
		committed []bool
		e         *env
	)
	e = newEnv(t, func(c *jobs.Config) {
		c.StoreFault = func(op string) error {
			mu.Lock()
			defer mu.Unlock()
			if op == failOp {
				return errors.New("injected " + op)
			}
			return nil
		}
		c.Enqueue = func(_ context.Context, id string, attempt int) error {
			// What the hook can read from the file is what has been committed.
			var rows int
			if err := e.DB.QueryRow(`SELECT (SELECT count(*) FROM af_jobs WHERE id = ?)
				+ (SELECT count(*) FROM af_attempts WHERE job_id = ?) + (SELECT count(*) FROM af_events WHERE job_id = ?)`, id, id, id).Scan(&rows); err != nil {
				t.Error(err)
			}
			mu.Lock()
			defer mu.Unlock()
			enqueued = append(enqueued, fmt.Sprintf("%s:%d", id, attempt))
			committed = append(committed, rows == 3)
			return nil
		}
	})

	r := e.call("POST", "/api/v1/workspaces/widgets/fix", `{"issue":1}`)
	if r.Status != 202 {
		t.Fatalf("status %d\n%s", r.Status, r.Body)
	}
	id := r.Doc(t).ID
	var trigger string
	var n int
	if err := e.DB.QueryRow("SELECT trigger_kind, n FROM af_attempts WHERE job_id = ?", id).Scan(&trigger, &n); err != nil || trigger != "create" || n != 1 {
		t.Errorf("attempt: %s %d (%v), want create 1", trigger, n, err)
	}
	var seq int
	var typ, data string
	if err := e.DB.QueryRow("SELECT seq, type, data FROM af_events WHERE job_id = ?", id).Scan(&seq, &typ, &data); err != nil || seq != 1 || typ != "job_state" {
		t.Fatalf("event: %d %s (%v), want 1 job_state", seq, typ, err)
	}
	var state struct {
		State   string
		Attempt int
		AtRest  bool `json:"at_rest"`
	}
	if err := json.Unmarshal([]byte(data), &state); err != nil || state.State != "queued" || state.Attempt != 1 || state.AtRest {
		t.Errorf("event data %s, want job_state queued of attempt 1, not at rest", data)
	}
	mu.Lock()
	if len(enqueued) != 1 || enqueued[0] != id+":1" || !committed[0] {
		t.Errorf("enqueued %v (everything committed when asked: %v), want [%s:1] asked once it was", enqueued, committed, id)
	}
	mu.Unlock()

	// A failure of each of the three inserts leaves nothing behind and asks nothing of the queue.
	for _, op := range []string{jobs.OpInsertJob, jobs.OpInsertAttempt, jobs.OpInsertEvent} {
		mu.Lock()
		failOp = op
		mu.Unlock()
		r := e.call("POST", "/api/v1/workspaces/widgets/fix", `{"issue":2}`)
		if r.Status != 500 {
			t.Errorf("a failed %s: status %d, want 500\n%s", op, r.Status, r.Body)
		}
		if jobsN, attemptsN, eventsN := e.count("SELECT count(*) FROM af_jobs"), e.count("SELECT count(*) FROM af_attempts"), e.count("SELECT count(*) FROM af_events"); jobsN != 1 || attemptsN != 1 || eventsN != 1 {
			t.Errorf("a failed %s left %d jobs, %d attempts and %d events, want only the first job's 1, 1 and 1", op, jobsN, attemptsN, eventsN)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(enqueued) != 1 {
		t.Errorf("the queue was asked %d times, want once, for the job that was stored", len(enqueued))
	}
	if len(e.Logs.find("ERROR", "")) == 0 {
		t.Errorf("the failed creations were not logged")
	}
}

// TestTheGoEntryPointsCreateAJobFromATriggerAndLookItUp_TS_12_22 verifies TS-12-22:
// Create takes a source and the request label the trigger consumed and applies the
// refusals of the routes, and OpenJob finds the open job of a kind for an issue.
// Verifies: 12-REQ-3.8
func TestTheGoEntryPointsCreateAJobFromATriggerAndLookItUp_TS_12_22(t *testing.T) {
	e := newEnv(t)
	issue := func(n int) json.RawMessage { return json.RawMessage(strconv.Itoa(n)) }

	existing, err := e.Svc.Create(ctx, jobs.CreateRequest{Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: issue(42)}, Source: jobs.Source{Kind: "api", By: callerID}})
	if err != nil {
		t.Fatal(err)
	}

	got, err := e.Svc.Create(ctx, jobs.CreateRequest{
		Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: issue(9), Context: []string{"from the poll"}},
		Source: jobs.Source{Kind: "label", By: "poll"}, RequestLabel: "af:fix",
	})
	if err != nil {
		t.Fatalf("Create from a label: %v", err)
	}
	if got.Source != (jobs.Source{Kind: "label", By: "poll"}) || got.RequestLabel == nil || *got.RequestLabel != "af:fix" || got.State != jobs.StateQueued || got.Kind != jobs.KindFix {
		t.Errorf("job = source %+v, request_label %v, state %s, want {label poll}, af:fix, queued", got.Source, got.RequestLabel, got.State)
	}
	if again, err := e.Svc.Create(ctx, jobs.CreateRequest{Workspace: "widgets", Kind: jobs.KindTriage, Body: jobs.Body{Issue: issue(9)}, Source: jobs.Source{Kind: "event", By: "hub"}}); err != nil || again.Source.Kind != "event" {
		t.Errorf("Create from an event: %+v, %v", again.Source, err)
	}

	_, err = e.Svc.Create(ctx, jobs.CreateRequest{Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: issue(42)}, Source: jobs.Source{Kind: "event", By: "hub"}})
	var dup *jobs.DuplicateError
	if !errors.Is(err, jobs.ErrDuplicate) || !errors.As(err, &dup) || dup.JobID != existing.ID {
		t.Errorf("a duplicate: err = %v, want ErrDuplicate naming %s", err, existing.ID)
	}
	if _, err = e.Svc.Create(ctx, jobs.CreateRequest{Workspace: "gadgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: issue(1)}, Source: jobs.Source{Kind: "label", By: "poll"}}); !errors.Is(err, jobs.ErrNotSubscribed) {
		t.Errorf("an unsubscribed workspace: err = %v, want ErrNotSubscribed", err)
	}
	var invalid *jobs.ValidationError
	if _, err = e.Svc.Create(ctx, jobs.CreateRequest{Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage(`"nope"`)}, Source: jobs.Source{Kind: "label", By: "poll"}}); !errors.As(err, &invalid) || invalid.Field != "issue" {
		t.Errorf("an issue that is not one: err = %v, want a *ValidationError for issue", err)
	}
	text := "a text"
	if _, err = e.Svc.Create(ctx, jobs.CreateRequest{Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: issue(5), Text: &text}, Source: jobs.Source{Kind: "label", By: "poll"}}); !errors.As(err, &invalid) || invalid.Field != "text" {
		t.Errorf("a fix with a text: err = %v, want a *ValidationError for text", err)
	}
	if _, err = e.Svc.Create(ctx, jobs.CreateRequest{Workspace: "widgets", Kind: jobs.KindFix, Body: jobs.Body{Issue: issue(5)}, Source: jobs.Source{Kind: "poll", By: "poll"}}); err == nil {
		t.Errorf("a source of a kind that is not one of the triggers was accepted")
	}

	for _, tc := range []struct {
		kind  jobs.Kind
		issue int64
		want  string
	}{{jobs.KindFix, 42, existing.ID}, {jobs.KindFix, 99, ""}, {jobs.KindTriage, 42, ""}} {
		doc, err := e.Svc.OpenJob(ctx, "widgets", tc.kind, tc.issue)
		switch {
		case tc.want == "" && !errors.Is(err, jobs.ErrNotFound):
			t.Errorf("OpenJob(widgets, %s, %d) = %v, %v, want ErrNotFound", tc.kind, tc.issue, doc.ID, err)
		case tc.want != "" && (err != nil || doc.ID != tc.want):
			t.Errorf("OpenJob(widgets, %s, %d) = %v, %v, want %s", tc.kind, tc.issue, doc.ID, err, tc.want)
		}
	}
	// A job that is done is not open.
	if _, err := e.DB.Exec("UPDATE af_jobs SET state = 'done', finished_at = '2026-10-22T08:00:00.000Z' WHERE id = ?", existing.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Svc.OpenJob(ctx, "widgets", jobs.KindFix, 42); !errors.Is(err, jobs.ErrNotFound) {
		t.Errorf("OpenJob of a job that is done: err = %v, want ErrNotFound", err)
	}
}
