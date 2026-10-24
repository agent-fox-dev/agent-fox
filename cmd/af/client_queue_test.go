package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// queued is the 202 answer of the service to a request for a job.
const queued = `{"id":"job_abc","workspace":"widgets","kind":"fix","state":"queued","attempt_count":1,"future":{"kept":true}}`

// queueService is a service that accepts every job: 202 and the job.
func queueService(t *testing.T) *fakeService {
	t.Helper()
	return ready(t, answering(202, queued))
}

// inDir runs the test in an empty directory of its own, and returns it.
func inDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

// postedBody is the JSON body of the last request the service saw.
func postedBody(t *testing.T, svc *fakeService) map[string]any {
	t.Helper()
	reqs := svc.Requests()
	if len(reqs) == 0 {
		t.Fatal("the service saw no request")
	}
	body, _ := reqs[len(reqs)-1].bodyJSON(t).(map[string]any)
	return body
}

// noRunDir fails the test if a run directory was made under dir.
func noRunDir(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, ".agent-fox")); err == nil {
		t.Errorf("%s/.agent-fox exists", dir)
	}
}

// TestJobsSubmitRefusesAMissingKindOrWorkspaceAndSendsNothing_TS_16_1 verifies TS-16-1 as it
// stands: af jobs submit needs the kind of the job, triage or fix, and --workspace, and a refusal
// sends nothing and makes no run directory.
// Verifies: 16-REQ-1.1
func TestJobsSubmitRefusesAMissingKindOrWorkspaceAndSendsNothing_TS_16_1(t *testing.T) {
	dir := inDir(t)
	svc := queueService(t)
	t.Setenv("GITHUB_TOKEN", "forge-sentinel")
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"no kind", []string{"jobs", "submit"}, "name the kind of job first"},
		{"another kind", []string{"jobs", "submit", "impl", "42", "--workspace", "w"}, "name the kind of job first"},
		{"no workspace", []string{"jobs", "submit", "fix", "42"}, "--workspace is required"},
		{"no input", []string{"jobs", "submit", "triage", "--workspace", "w"}, "no input given"},
	} {
		res := afRun(t, "", c.args...)
		if res.code != 2 || field(res.env, "status") != "usage" || !strings.Contains(res.message(), c.want) {
			t.Errorf("%s: exit %d, status %v, message %q, want 2, usage and %q", c.name, res.code, field(res.env, "status"), res.message(), c.want)
		}
		if field(res.env, "tool") != "jobs submit" {
			t.Errorf("%s: tool = %v, want jobs submit", c.name, field(res.env, "tool"))
		}
	}
	if n := len(svc.Requests()); n != 0 {
		t.Errorf("%d requests reached the service", n)
	}
	noRunDir(t, dir)
}

// TestEachInputFormOfTriageBecomesTheRightRequestField_TS_16_45 verifies TS-16-45.
// Verifies: 16-REQ-7.1
func TestEachInputFormOfTriageBecomesTheRightRequestField_TS_16_45(t *testing.T) {
	dir := inDir(t)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("the notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_API_URL", "http://127.0.0.1:1") // consulted by no one
	svc := queueService(t)
	for _, c := range []struct {
		input, stdin string
		want         map[string]any
	}{
		{"-", "some text from stdin\n", map[string]any{"text": "some text from stdin"}},
		{"https://github.com/o/r/issues/1", "", map[string]any{"issue": "https://github.com/o/r/issues/1"}},
		{"http://example.com/x", "", map[string]any{"issue": "http://example.com/x"}},
		{"  https://github.com/o/r/pull/3  ", "", map[string]any{"issue": "https://github.com/o/r/pull/3"}},
		{"42", "", map[string]any{"issue": float64(42)}},
		{"#42", "", map[string]any{"issue": float64(42)}},
		{"o/r#7", "", map[string]any{"issue": "o/r#7"}},
		{"a/b/c#7", "", map[string]any{"issue": "a/b/c#7"}},
		{"notes.txt", "", map[string]any{"file": map[string]any{"name": "notes.txt", "content": "the notes"}}},
		{"some words", "", map[string]any{"text": "some words"}},
		{"7x", "", map[string]any{"text": "7x"}},
		{"o#7", "", map[string]any{"text": "o#7"}},
		{"0", "", map[string]any{"text": "0"}},
	} {
		res := afRun(t, c.stdin, "jobs", "submit", "triage", c.input, "--workspace", "w")
		if res.code != 0 {
			t.Errorf("%q: exit %d, %s", c.input, res.code, res.message())
			continue
		}
		if got := postedBody(t, svc); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: body %v, want %v", c.input, got, c.want)
		}
	}
}

// TestFixWithAWorkspaceTakesOnlyIssueFormsAndRefusesTheRest_TS_16_46 verifies TS-16-46.
// Verifies: 16-REQ-7.2
func TestFixWithAWorkspaceTakesOnlyIssueFormsAndRefusesTheRest_TS_16_46(t *testing.T) {
	dir := inDir(t)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("the notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := queueService(t)
	for _, in := range []string{"-", "notes.txt", "some words", "0"} {
		res := afRun(t, "text", "jobs", "submit", "fix", in, "--workspace", "w")
		if res.code != 2 {
			t.Errorf("%q: exit %d, want 2", in, res.code)
		}
		for _, want := range []string{"N", "#N", "owner/repo#N", "issue URL"} {
			if !strings.Contains(res.message(), want) {
				t.Errorf("%q: message %q does not name %s", in, res.message(), want)
			}
		}
	}
	if n := len(svc.Requests()); n != 0 {
		t.Errorf("%d requests were sent for refused inputs", n)
	}
	for in, want := range map[string]any{"7": float64(7), "#7": float64(7), "o/r#7": "o/r#7", "https://github.com/o/r/issues/7": "https://github.com/o/r/issues/7"} {
		if res := afRun(t, "", "jobs", "submit", "fix", in, "--workspace", "w"); res.code != 0 {
			t.Errorf("%q: exit %d, %s", in, res.code, res.message())
		} else if got := postedBody(t, svc)["issue"]; !reflect.DeepEqual(got, want) {
			t.Errorf("%q: issue %v, want %v", in, got, want)
		}
	}
}

// TestEmptyNULNonUTF8AndDirectoryInputsAreUsageErrorsAndAnUnreadableFileIsIO_TS_16_47
// verifies TS-16-47.
// Verifies: 16-REQ-7.3
func TestEmptyNULNonUTF8AndDirectoryInputsAreUsageErrorsAndAnUnreadableFileIsIO_TS_16_47(t *testing.T) {
	dir := inDir(t)
	svc := queueService(t)
	write := func(name string, data []byte, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), data, mode); err != nil {
			t.Fatal(err)
		}
	}
	write("blank.txt", []byte(" \n\t \n"), 0o644)
	write("nul.txt", []byte("ab\x00cd"), 0o644)
	write("latin.txt", []byte{'a', 0xff, 0xfe, 'b'}, 0o644)
	write("locked.txt", []byte("secret"), 0o644)
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	locked := os.Geteuid() != 0
	if locked {
		if err := os.Chmod(filepath.Join(dir, "locked.txt"), 0); err != nil {
			t.Fatal(err)
		}
	}

	for name, c := range map[string]struct {
		input, stdin string
		code         int
		category     string
	}{
		"blank file":  {"blank.txt", "", 2, "usage"},
		"NUL file":    {"nul.txt", "", 2, "usage"},
		"non-UTF-8":   {"latin.txt", "", 2, "usage"},
		"directory":   {"adir", "", 2, "usage"},
		"blank stdin": {"-", "  \n", 2, "usage"},
		"NUL stdin":   {"-", "a\x00b", 2, "usage"},
		"bytes stdin": {"-", "a\xff\xfeb", 2, "usage"},
		"locked file": {"locked.txt", "", 1, "io"},
	} {
		if name == "locked file" && !locked {
			continue
		}
		res := afRun(t, c.stdin, "jobs", "submit", "triage", c.input, "--workspace", "w")
		if res.code != c.code || res.category() != c.category {
			t.Errorf("%s: exit %d, category %q, message %q, want %d and %s", name, res.code, res.category(), res.message(), c.code, c.category)
		}
	}
	if n := len(svc.Requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

// TestOverLongContentIsCutAtAUTF8BoundaryAndTheResultFitsMaxInputBytes_TS_16_48 verifies
// TS-16-48.
// Verifies: 16-REQ-7.4
func TestOverLongContentIsCutAtAUTF8BoundaryAndTheResultFitsMaxInputBytes_TS_16_48(t *testing.T) {
	dir := inDir(t)
	svc := queueService(t)
	long := strings.Repeat("€", (toolio.MaxInputBytes+1000)/3) // 3-byte runes
	exact := strings.Repeat("a", toolio.MaxInputBytes)
	if err := os.WriteFile(filepath.Join(dir, "long.txt"), []byte(long), 0o644); err != nil {
		t.Fatal(err)
	}

	sent := func(body map[string]any) string {
		if text, ok := body["text"].(string); ok {
			return text
		}
		return body["file"].(map[string]any)["content"].(string)
	}
	for name, run := range map[string]func() afResult{
		"stdin": func() afResult { return afRun(t, long, "jobs", "submit", "triage", "-", "--workspace", "w") },
		"file":  func() afResult { return afRun(t, "", "jobs", "submit", "triage", "long.txt", "--workspace", "w") },
	} {
		res := run()
		if res.code != 0 {
			t.Fatalf("%s: exit %d, %s", name, res.code, res.message())
		}
		content := sent(postedBody(t, svc))
		if len(content) > toolio.MaxInputBytes || !utf8.ValidString(content) || !strings.HasSuffix(content, toolio.TruncationMarker) {
			t.Errorf("%s: sent %d bytes, valid %v, marker %v; want at most %d, valid UTF-8, ending with the marker", name, len(content), utf8.ValidString(content), strings.HasSuffix(content, toolio.TruncationMarker), toolio.MaxInputBytes)
		}
		if truncated := field(res.env, "input", "truncated"); truncated != true || !slices.Contains(codes(res.env), "input_truncated") {
			t.Errorf("%s: input.truncated = %v, warnings %v, want true and input_truncated", name, truncated, codes(res.env))
		}
	}
	res := afRun(t, exact, "jobs", "submit", "triage", "-", "--workspace", "w")
	if got := sent(postedBody(t, svc)); res.code != 0 || got != exact || field(res.env, "input", "truncated") != false || slices.Contains(codes(res.env), "input_truncated") {
		t.Errorf("exact length: exit %d, sent %d bytes, truncated %v, want it unchanged and not truncated", res.code, len(got), field(res.env, "input", "truncated"))
	}
}

// TestTheEnvelopeInputReportsKindOriginAndTruncatedForEachForm_TS_16_49 verifies TS-16-49.
// Verifies: 16-REQ-7.5
func TestTheEnvelopeInputReportsKindOriginAndTruncatedForEachForm_TS_16_49(t *testing.T) {
	dir := inDir(t)
	abs, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("the notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	queueService(t)
	for _, c := range []struct {
		input, stdin string
		want         map[string]any
	}{
		{"https://github.com/o/r/issues/1", "", map[string]any{"kind": "issue", "origin": "https://github.com/o/r/issues/1", "truncated": false}},
		{"42", "", map[string]any{"kind": "issue", "origin": "", "truncated": false}},
		{"o/r#7", "", map[string]any{"kind": "issue", "origin": "", "truncated": false}},
		{"notes.txt", "", map[string]any{"kind": "file", "origin": filepath.Join(abs, "notes.txt"), "truncated": false}},
		{"some words", "", map[string]any{"kind": "text", "origin": "", "truncated": false}},
		{"-", "from stdin", map[string]any{"kind": "stdin", "origin": "", "truncated": false}},
	} {
		res := afRun(t, c.stdin, "jobs", "submit", "triage", c.input, "--workspace", "w")
		got, _ := res.env["input"].(map[string]any)
		// the temp directory may be reached through a link; compare the base of a file's origin
		if c.want["kind"] == "file" {
			if origin, _ := got["origin"].(string); filepath.Base(origin) != "notes.txt" || !filepath.IsAbs(origin) {
				t.Errorf("%q: origin %q, want the absolute path of the file", c.input, origin)
			}
			got["origin"], c.want["origin"] = "", ""
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: input = %v, want %v", c.input, got, c.want)
		}
	}
}

// TestTheBodyIsPostedToTheRouteOfTheWorkspaceWithTheSlugEscapedAndTheContextInOrder_TS_16_50
// verifies TS-16-50.
// Verifies: 16-REQ-7.6
func TestTheBodyIsPostedToTheRouteOfTheWorkspaceWithTheSlugEscapedAndTheContextInOrder_TS_16_50(t *testing.T) {
	inDir(t)
	svc := queueService(t)
	if res := afRun(t, "", "jobs", "submit", "triage", "1", "--workspace", "a b/c", "--context", "x", "--context", "y"); res.code != 0 {
		t.Fatalf("triage: exit %d, %s", res.code, res.message())
	}
	if res := afRun(t, "", "jobs", "submit", "fix", "1", "--workspace", "w"); res.code != 0 {
		t.Fatalf("fix: exit %d, %s", res.code, res.message())
	}
	reqs := svc.Requests()
	if len(reqs) != 2 || reqs[0].RequestURI != "/api/v1/workspaces/a%20b%2Fc/triage" || reqs[1].RequestURI != "/api/v1/workspaces/w/fix" {
		t.Fatalf("requests %+v, want the two routes", reqs)
	}
	if got := reqs[0].bodyJSON(t).(map[string]any)["context"]; !reflect.DeepEqual(got, []any{"x", "y"}) {
		t.Errorf("triage context = %v, want [x y]", got)
	}
	if _, present := reqs[1].bodyJSON(t).(map[string]any)["context"]; present {
		t.Errorf("fix body %s has a context, want none", reqs[1].Body)
	}
	for _, r := range reqs {
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("%s %s with %q, want a JSON POST", r.Method, r.Path, r.Header.Get("Content-Type"))
		}
	}
}

// TestA202RecordsTheJobAtOnceAndWithoutWaitFinishesDoneWithNextPointingAtWatch_TS_16_51
// verifies TS-16-51.
// Verifies: 16-REQ-7.7
func TestA202RecordsTheJobAtOnceAndWithoutWaitFinishesDoneWithNextPointingAtWatch_TS_16_51(t *testing.T) {
	inDir(t)
	svc := queueService(t)
	for _, c := range []struct {
		name string
		args []string
		argv []string
	}{
		{"server from AF_URL", nil, []string{"af", "jobs", "watch", "job_abc"}},
		{"server from the flag", []string{"--server", svc.URL}, []string{"af", "jobs", "watch", "job_abc", "--server", svc.URL}},
	} {
		res := afRun(t, "", append([]string{"jobs", "submit", "fix", "42", "--workspace", "widgets"}, c.args...)...)
		if res.code != 0 || field(res.env, "status") != "done" {
			t.Fatalf("%s: exit %d, %s", c.name, res.code, res.message())
		}
		artifacts, _ := res.env["artifacts"].([]any)
		if len(artifacts) != 1 || !reflect.DeepEqual(artifacts[0], map[string]any{"kind": "job", "name": "job_abc", "url": svc.URL + "/api/v1/jobs/job_abc"}) {
			t.Errorf("%s: artifacts = %v, want the job", c.name, artifacts)
		}
		result := res.result()
		var job any
		_ = json.Unmarshal([]byte(queued), &job)
		if env, present := result["envelope"]; !present || env != nil || !reflect.DeepEqual(result["job"], job) {
			t.Errorf("%s: result = %v, want {job as received, envelope: null}", c.name, result)
		}
		if s, _ := res.env["summary"].(string); !strings.Contains(s, "job_abc") || !strings.Contains(s, "widgets") {
			t.Errorf("%s: summary %q, want it to name the job and the workspace", c.name, s)
		}
		next, _ := res.env["next"].([]any)
		if len(next) != 1 {
			t.Fatalf("%s: next = %v, want one entry", c.name, next)
		}
		var argv []string
		for _, a := range next[0].(map[string]any)["argv"].([]any) {
			argv = append(argv, a.(string))
		}
		if !slices.Equal(argv, c.argv) {
			t.Errorf("%s: next argv = %v, want %v", c.name, argv, c.argv)
		}
	}
}

// TestADuplicateJobFailsConflictWithTheServicesMessage_TS_16_52 verifies TS-16-52.
// Verifies: 16-REQ-7.8
func TestADuplicateJobFailsConflictWithTheServicesMessage_TS_16_52(t *testing.T) {
	inDir(t)
	ready(t, answering(409, `{"error":{"code":"duplicate_job","message":"an open job of this kind exists for the issue: job_abc"}}`))
	res := afRun(t, "", "jobs", "submit", "fix", "42", "--workspace", "w")
	if res.code != 1 || res.category() != "conflict" || res.retryable() {
		t.Errorf("exit %d, category %q, retryable %v, want 1, conflict, not retryable", res.code, res.category(), res.retryable())
	}
	if m := res.message(); !strings.Contains(m, "job_abc") || !strings.Contains(m, "(duplicate_job)") {
		t.Errorf("message %q, want the service's text naming the open job and its code", m)
	}
}

// TestWaitFollowsTheQueuedJobAndKeepsStderrQuiet_TS_16_63 verifies, for af fix --workspace
// --wait, the second half of TS-16-63: nothing is written to stderr, and every event is
// emitted as job_event all the same.
// Verifies: 16-REQ-9.3
func TestWaitFollowsTheQueuedJobAndKeepsStderrQuiet_TS_16_63(t *testing.T) {
	inDir(t)
	svc := ready(t, routes(map[string]reply{
		"POST /api/v1/workspaces/w/fix":                {202, queued, false},
		"GET /api/v1/jobs/job_abc/events":              {200, strings.ReplaceAll(eventFrame(1)+eventFrame(2), `"job_id":"j"`, `"job_id":"job_abc"`), true},
		"GET /api/v1/jobs/job_abc":                     {200, strings.ReplaceAll(doneJob, `"id":"j"`, `"id":"job_abc"`), false},
		"GET /api/v1/jobs/job_abc/attempts/1/envelope": {200, `{"status":"done","warnings":[]}`, false},
	}))
	useClockNow(t)
	res, data := afRunEvents(t, "jobs", "submit", "fix", "1", "--workspace", "w", "--wait")
	if res.code != 0 || field(res.env, "status") != "done" {
		t.Fatalf("exit %d, %s", res.code, res.message())
	}
	if res.stderr != "" {
		t.Errorf("stderr = %q, want nothing under --wait", res.stderr)
	}
	if strings.Count(data, `"type":"job_event"`) != 2 {
		t.Errorf("events = %q, want two job_event lines", data)
	}
	if artifacts, _ := res.env["artifacts"].([]any); len(artifacts) != 1 {
		t.Errorf("artifacts = %v, want only the job", artifacts)
	}
	if reqs := svc.requestLines(); reqs[0] != "POST /api/v1/workspaces/w/fix" {
		t.Errorf("requests = %v, want the job queued first", reqs)
	}
}

// TestLocalChecksOfAQueueCommandRunInOrderAndSendNothing_TS_16_37 verifies TS-16-37 for the
// queue command: the input is checked before the server, and the server before the credential.
// Verifies: 16-REQ-6.1
func TestLocalChecksOfAQueueCommandRunInOrderAndSendNothing_TS_16_37(t *testing.T) {
	dir := inDir(t)
	svc := queueService(t)
	clientHome(t, nil) // no credential file
	t.Setenv("AF_URL", "")
	bad := "ftp://" + strings.TrimPrefix(svc.URL, "http://")
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"flag, input, server, credential", []string{"jobs", "submit", "triage", "adir", "--workspace", "w", "--server", bad, "--bogus"}, "bogus"},
		{"input, server, credential", []string{"jobs", "submit", "triage", "adir", "--workspace", "w", "--server", bad}, "directory"},
		{"server, credential", []string{"jobs", "submit", "triage", "1", "--workspace", "w", "--server", bad}, "http or https"},
		{"no server, credential", []string{"jobs", "submit", "triage", "1", "--workspace", "w"}, "AF_URL"},
		{"credential", []string{"jobs", "submit", "triage", "1", "--workspace", "w", "--server", svc.URL}, "token = "},
	} {
		res := afRun(t, "", c.args...)
		if res.code == 0 || !strings.Contains(res.message(), c.want) {
			t.Errorf("%s: exit %d, message %q, want it to name %q", c.name, res.code, res.message(), c.want)
		}
	}
	if n := len(svc.Requests()); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}
