package main

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// queued is the 202 answer of the service to a job it queued.
const queued = `{"id":"job_abc","workspace":"widgets","kind":"fix","state":"queued","future":{"kept":true}}`

// submit runs af jobs submit with args and stdin in an empty directory of the test's own.
func submit(t *testing.T, stdin string, args ...string) afResult {
	t.Helper()
	return afWith(t, t.Context(), strings.NewReader(stdin), append([]string{"jobs", "submit"}, args...)...)
}

func TestSubmit_RefusesInOrderBeforeAnyRequest(t *testing.T) {
	t.Chdir(t.TempDir())
	testutil.WriteFile(t, ".", "adir/notes.txt", "the notes\n")
	svc := newService(t, answer(202, queued))
	home(t, "")
	t.Setenv("AF_URL", "")
	bad := "ftp://" + strings.TrimPrefix(svc.URL, "http://")
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, "name the kind of job first"},
		{[]string{"impl", "42", "--workspace", "w"}, "name the kind of job first"},
		{[]string{"fix", "42"}, "--workspace is required"},
		{[]string{"triage", "--workspace", "w"}, "no input given"},
		{[]string{"fix", "-", "--workspace", "w"}, "a fix job takes an issue: N, #N, owner/repo#N or an issue URL"},
		{[]string{"fix", "adir/notes.txt", "--workspace", "w"}, "a fix job takes an issue"},
		{[]string{"fix", "some words", "--workspace", "w"}, "a fix job takes an issue"},
		{[]string{"fix", "0", "--workspace", "w"}, "a fix job takes an issue"},
		{[]string{"triage", "adir", "--workspace", "w", "--server", bad, "--bogus"}, "bogus"},
		{[]string{"triage", "adir", "--workspace", "w", "--server", bad}, "adir is a directory"},
		{[]string{"triage", "1", "--workspace", "w", "--server", bad}, "http or https"},
		{[]string{"triage", "1", "--workspace", "w"}, "no server is named"},
		{[]string{"triage", "1", "--workspace", "w", "--server", svc.URL}, "token = "},
	} {
		if res := submit(t, "", c.args...); res.code == 0 || !strings.Contains(res.message(), c.want) {
			t.Errorf("%v: exit %d, message %q, want it to say %q", c.args, res.code, res.message(), c.want)
		}
	}
	if n := len(svc.requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
	if _, err := os.Stat(".agent-fox"); err == nil {
		t.Error("a run directory was made")
	}
}

func TestSubmit_EachInputBecomesItsFieldAndTheInputOfTheEnvelope(t *testing.T) {
	t.Chdir(t.TempDir())
	testutil.WriteFile(t, ".", "notes.txt", "the notes\n")
	abs, err := filepath.Abs("notes.txt")
	if err != nil {
		t.Fatal(err)
	}
	svc := serve(t, answer(202, queued))
	for _, c := range []struct {
		kind, input, stdin string
		body               map[string]any
		input2             map[string]any
	}{
		{"triage", "-", " from stdin\n", map[string]any{"text": "from stdin"}, map[string]any{"kind": "stdin", "origin": ""}},
		{"triage", "  https://github.com/o/r/issues/1 ", "", map[string]any{"issue": "https://github.com/o/r/issues/1"}, map[string]any{"kind": "issue", "origin": "https://github.com/o/r/issues/1"}},
		{"fix", "HTTP://example.com/x", "", map[string]any{"issue": "HTTP://example.com/x"}, map[string]any{"kind": "issue", "origin": "HTTP://example.com/x"}},
		{"fix", "42", "", map[string]any{"issue": float64(42)}, map[string]any{"kind": "issue", "origin": ""}},
		{"triage", "#42", "", map[string]any{"issue": float64(42)}, map[string]any{"kind": "issue", "origin": ""}},
		{"fix", "a/b/c#7", "", map[string]any{"issue": "a/b/c#7"}, map[string]any{"kind": "issue", "origin": ""}},
		{"triage", "notes.txt", "", map[string]any{"file": map[string]any{"name": "notes.txt", "content": "the notes"}}, map[string]any{"kind": "file", "origin": abs}},
		{"triage", "some words", "", map[string]any{"text": "some words"}, map[string]any{"kind": "text", "origin": ""}},
		{"triage", "o#7", "", map[string]any{"text": "o#7"}, map[string]any{"kind": "text", "origin": ""}},
		{"triage", "0", "", map[string]any{"text": "0"}, map[string]any{"kind": "text", "origin": ""}},
	} {
		res := submit(t, c.stdin, c.kind, c.input, "--workspace", "w")
		c.input2["truncated"] = false
		if res.code != 0 || !reflect.DeepEqual(svc.last(t).body(t), any(c.body)) || !reflect.DeepEqual(res.env["input"], any(c.input2)) {
			t.Errorf("%s %q: exit %d, body %v, input %v, want %v and %v", c.kind, c.input, res.code, svc.last(t).body(t), res.env["input"], c.body, c.input2)
		}
		if want := "POST /api/v1/workspaces/w/" + c.kind; svc.last(t).Method+" "+svc.last(t).URI != want {
			t.Errorf("%s %q: request %s %s, want %s", c.kind, c.input, svc.last(t).Method, svc.last(t).URI, want)
		}
	}
	// The slug is escaped in the route, and the context goes with the input in order.
	res := submit(t, "", "triage", "1", "--workspace", "a b/c", "--context", "x", "--context", "y")
	if got := svc.last(t); res.code != 0 || got.URI != "/api/v1/workspaces/a%20b%2Fc/triage" || !reflect.DeepEqual(got.body(t), map[string]any{"issue": float64(1), "context": []any{"x", "y"}}) {
		t.Errorf("exit %d, %s with %s, want the escaped route and the context in order", res.code, got.URI, got.Body)
	}
}

func TestSubmit_RefusesInputThatIsNotText(t *testing.T) {
	t.Chdir(t.TempDir())
	for name, content := range map[string]string{"blank.txt": " \n\t \n", "nul.txt": "ab\x00cd", "latin.txt": "a\xff\xfeb", "locked.txt": "words"} {
		testutil.WriteFile(t, ".", name, content)
	}
	if err := os.Chmod("locked.txt", 0); err != nil {
		t.Fatal(err)
	}
	svc := serve(t, answer(202, queued))
	for _, c := range []struct {
		input, stdin string
		code         int
		category     string
	}{
		{"blank.txt", "", 2, "usage"}, {"nul.txt", "", 2, "usage"}, {"latin.txt", "", 2, "usage"},
		{"-", "  \n", 2, "usage"}, {"-", "a\x00b", 2, "usage"}, {"-", "a\xffb", 2, "usage"}, {"locked.txt", "", 1, "io"},
	} {
		if c.input == "locked.txt" && os.Geteuid() == 0 { // the owner's mode does not keep root out
			continue
		}
		if res := submit(t, c.stdin, "triage", c.input, "--workspace", "w"); res.code != c.code || res.category() != c.category {
			t.Errorf("%q: exit %d, category %v, message %q, want %d and %s", c.input, res.code, res.category(), res.message(), c.code, c.category)
		}
	}
	res := afWith(t, t.Context(), brokenReader{}, "jobs", "submit", "triage", "-", "--workspace", "w")
	if res.code != 1 || res.category() != "io" {
		t.Errorf("an unreadable stdin: exit %d, category %v, want 1 and io", res.code, res.category())
	}
	if n := len(svc.requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestSubmit_CutsLongContentAtARuneBoundaryToFitTheLimit(t *testing.T) {
	t.Chdir(t.TempDir())
	long := strings.Repeat("€", (toolio.MaxInputBytes+1000)/3)
	testutil.WriteFile(t, ".", "long.txt", long)
	svc := serve(t, answer(202, queued))
	sent := func() string {
		body := svc.last(t).body(t)
		if text, ok := field(body, "text").(string); ok {
			return text
		}
		s, _ := field(body, "file", "content").(string)
		return s
	}
	for _, c := range []struct{ input, stdin string }{{"-", long}, {"long.txt", ""}} {
		res := submit(t, c.stdin, "triage", c.input, "--workspace", "w")
		got := sent()
		if res.code != 0 || len(got) > toolio.MaxInputBytes || !utf8.ValidString(got) || !strings.HasSuffix(got, toolio.TruncationMarker) {
			t.Errorf("%q: exit %d, sent %d bytes ending %q, want at most %d of valid UTF-8 with the marker", c.input, res.code, len(got), got[len(got)-20:], toolio.MaxInputBytes)
		}
		if field(res.env, "input", "truncated") != true || !slices.Contains(res.warnings(), "input_truncated") {
			t.Errorf("%q: input %v, warnings %v, want it truncated", c.input, res.env["input"], res.warnings())
		}
	}
	exact := strings.Repeat("a", toolio.MaxInputBytes)
	if res := submit(t, exact, "triage", "-", "--workspace", "w"); res.code != 0 || sent() != exact || field(res.env, "input", "truncated") != false {
		t.Errorf("at the limit: exit %d, sent %d bytes, input %v, want it whole", res.code, len(sent()), res.env["input"])
	}
}

func TestSubmit_RecordsTheJobAndSuggestsWatchingIt(t *testing.T) {
	svc := serve(t, answer(202, queued))
	for _, c := range []struct {
		args []string
		next string
	}{
		{nil, "af jobs watch job_abc"},
		{[]string{"--server", svc.URL}, "af jobs watch job_abc --server " + svc.URL},
	} {
		res := submit(t, "", append([]string{"fix", "42", "--workspace", "widgets"}, c.args...)...)
		if res.code != 0 || res.summary() != "queued the fix job job_abc on the workspace widgets" || !slices.Equal(res.next(), []string{c.next}) {
			t.Errorf("%v: exit %d, summary %q, next %v, want %q", c.args, res.code, res.summary(), res.next(), c.next)
		}
		if want := map[string]any{"job": value(t, queued), "envelope": nil}; !reflect.DeepEqual(res.result(), want) {
			t.Errorf("%v: result %v, want the job as received and no envelope", c.args, res.result())
		}
		if got := field(res.env, "artifacts"); !reflect.DeepEqual(got, []any{map[string]any{"kind": "job", "name": "job_abc", "url": svc.URL + "/api/v1/jobs/job_abc"}}) {
			t.Errorf("%v: artifacts %v, want the job", c.args, got)
		}
	}
}

func TestSubmit_FailsWhenTheServiceRefusesOrAnswersNoJob(t *testing.T) {
	for _, c := range []struct {
		status            int
		body, category, m string
	}{
		{409, `{"error":{"code":"duplicate_job","message":"an open job exists: job_abc"}}`, "conflict", "an open job exists: job_abc (duplicate_job)"},
		{202, `{"state":"queued"}`, "service", "the service queued the job and its document has no id"},
		{202, `not json`, "service", ""},
	} {
		serve(t, answer(c.status, c.body))
		res := submit(t, "", "fix", "42", "--workspace", "w")
		if res.code != 1 || res.category() != c.category || (c.m != "" && res.message() != c.m) {
			t.Errorf("%d %s: exit %d, category %v, message %q, want 1, %s, %q", c.status, c.body, res.code, res.category(), res.message(), c.category, c.m)
		}
	}
}

func TestSubmit_WithWaitFollowsTheJobQuietly(t *testing.T) {
	svc := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			answer(202, `{"id":"j","state":"queued"}`)(w, r)
			return
		}
		atRest(doneJob, answer(200, `{"status":"done"}`))(w, r)
	})
	res := submit(t, "", "fix", "1", "--workspace", "w", "--wait")
	if res.code != 0 || res.status() != "done" || res.summary() != "fixed" || !slices.Equal(res.artifacts(), []string{"j"}) {
		t.Fatalf("exit %d, status %v, summary %q, artifacts %v, want done, the job's summary and the job", res.code, res.status(), res.summary(), res.artifacts())
	}
	if res.stderr != "" || strings.Count(res.events, `"type":"job_event"`) != 1 {
		t.Errorf("stderr %q, events %q, want nothing on stderr and the event as job_event", res.stderr, res.events)
	}
	if got := svc.lines(); got[0] != "POST /api/v1/workspaces/w/fix" {
		t.Errorf("requests %v, want the job queued first", got)
	}
}
