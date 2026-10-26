package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/afclient"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestJobOutcome_EndsByWhatTheJobCameTo(t *testing.T) {
	hint := &toolio.FixHint{Flag: "--sandbox", Suggest: "podman"}
	errorOf := func(category string) string {
		return `{"category":"` + category + `","message":"lost SECRET","retryable":true,"fix_hint":{"flag":"--sandbox","suggest":"podman"}}`
	}
	result := func(status, errObj string) string {
		if errObj == "" {
			errObj = "null"
		}
		return `{"status":"` + status + `","exit_code":0,"summary":"said SECRET","error":` + errObj + `}`
	}
	blocked := `{"question":"Which SECRET?","options":[{"id":"A","label":"main SECRET"},{"id":"B","label":"release"}]}`
	for _, c := range []struct {
		name, job string
		fromFlag  bool
		want      toolio.Outcome
		failure   *toolio.Failure
	}{
		{"done", jobJSON("done", result("done", ""), "", "done"), false, toolio.Outcome{Status: toolio.StatusDone, Summary: "said ***"}, nil},
		{"done unverified", jobJSON("done", result("unverified", ""), "", "done"), false, toolio.Outcome{Status: toolio.StatusUnverified, Summary: "said ***"}, nil},
		{"done with no result", jobJSON("done", "", "", "done"), false, toolio.Outcome{Status: toolio.StatusDone, Summary: "the job j is done"}, nil},
		{"blocked", jobJSON("blocked", "", blocked, "needs_human"), false, toolio.Outcome{Status: toolio.StatusNeedsHuman, Summary: "the job j is blocked",
			NeedsHuman: &toolio.Question{Text: "Which ***?", Options: []string{"main ***", "release"}, ResumeArgv: []string{"af", "jobs", "answer", "j", "--wait"}}}, nil},
		{"blocked, server from the flag", jobJSON("blocked", "", `{}`, "needs_human"), true, toolio.Outcome{Status: toolio.StatusNeedsHuman, Summary: "the job j is blocked",
			NeedsHuman: &toolio.Question{Text: "the job j waits for an answer", ResumeArgv: []string{"af", "jobs", "answer", "j", "--wait", "--server", "http://svc"}}}, nil},
		{"failed unverified", jobJSON("failed", result("unverified", ""), "", "failed"), false, toolio.Outcome{Status: toolio.StatusUnverified, Summary: "said ***"}, nil},
		{"failed", jobJSON("failed", result("failed", errorOf("sandbox")), "", "failed"), false, toolio.Outcome{},
			&toolio.Failure{Category: "sandbox", Message: "lost ***", Retryable: true, FixHint: hint}},
		{"failed in usage", jobJSON("failed", result("usage", errorOf("brain")), "", "failed"), false, toolio.Outcome{},
			&toolio.Failure{Category: toolio.CategoryUsage, Message: "lost ***", Retryable: true, FixHint: hint}},
		{"failed with no error", jobJSON("failed", result("failed", ""), "", "failed"), false, toolio.Outcome{},
			&toolio.Failure{Category: toolio.CategoryInternal, Message: "the job j failed on the service and its result has no error"}},
		{"cancelled", jobJSON("cancelled", result("failed", errorOf("brain")), "", "cancelled"), false, toolio.Outcome{},
			&toolio.Failure{Category: toolio.CategoryJobCancelled, Message: "the job j was cancelled on the service"}},
		{"not JSON", `[1]`, false, toolio.Outcome{},
			&toolio.Failure{Category: toolio.CategoryService, Message: "the document of the job j is not the JSON expected"}},
	} {
		res := jobResult{Job: json.RawMessage(c.job), Envelope: nullEnvelope}
		got, err := jobOutcome(afclient.Server{URL: "http://svc", FromFlag: c.fromFlag}, "j", res, func(s string) string { return strings.ReplaceAll(s, "SECRET", "***") })
		if c.failure != nil {
			want := *c.failure
			if c.name != "not JSON" { // every ending of a job at rest carries {job, envelope}
				want.Result = res
			}
			var f *toolio.Failure
			if !errors.As(err, &f) || !reflect.DeepEqual(f, &want) {
				t.Errorf("%s: error %#v, want %#v", c.name, err, &want)
			}
			continue
		}
		c.want.Result = res
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v, %v; want %+v", c.name, got, err, c.want)
		}
	}
}

func TestFollow_AFailedCancelledOrUsageEndingCarriesTheJobAndTheEnvelope(t *testing.T) {
	errObj := `"error":{"category":"brain","message":"lost","retryable":false,"fix_hint":null}`
	envelope := `{"tool":"fix","status":"failed","warnings":[],"result":{"x":1}}`
	for _, c := range []struct {
		name, job string
		code      int
		status    string
		category  string
	}{
		{"failed", jobJSON("failed", `{"status":"failed","summary":"s",`+errObj+`}`, "", "failed"), 1, "failed", "brain"},
		{"cancelled", jobJSON("cancelled", `{"status":"failed","summary":"s",`+errObj+`}`, "", "cancelled"), 1, "failed", "job_cancelled"},
		{"usage", jobJSON("failed", `{"status":"usage","summary":"s",`+errObj+`}`, "", "failed"), 2, "usage", "usage"},
	} {
		serve(t, atRest(c.job, answer(200, envelope)))
		res := af(t, "jobs", "watch", "j")
		result, _ := res.result().(map[string]any)
		if res.code != c.code || res.status() != c.status || res.category() != c.category ||
			!reflect.DeepEqual(result["job"], value(t, c.job)) || !reflect.DeepEqual(result["envelope"], value(t, envelope)) {
			t.Errorf("%s: exit %d, status %v, category %v, result %v; want %d, %s, %s and {job, envelope}", c.name, res.code, res.status(), res.category(), result, c.code, c.status, c.category)
		}
	}
}

func TestFollow_AtRestGivesTheEnvelopeOfTheFinishedAttempt(t *testing.T) {
	job := jobJSON("done", "", "", "done")
	envelope := `{"tool":"fix","status":"done","warnings":[],"result":{"x":1}}`
	for _, c := range []struct {
		name     string
		job      string
		envelope http.HandlerFunc
		want     any
		warns    bool
	}{
		{"stored", job, answer(200, envelope), value(t, envelope), false},
		{"not stored", job, answer(404, `{"error":{"code":"not_found","message":"no envelope"}}`), nil, false},
		{"unreadable", job, answer(500, `{"error":{"code":"internal","message":"oops"}}`), nil, true},
		{"no attempt finished", jobJSON("done", "", "", "queued"), answer(200, envelope), nil, false},
	} {
		serve(t, atRest(c.job, c.envelope))
		res := af(t, "jobs", "watch", "j")
		result, _ := res.result().(map[string]any)
		if _, present := result["envelope"]; res.code != 0 || !present || !reflect.DeepEqual(result["envelope"], c.want) || !reflect.DeepEqual(result["job"], value(t, c.job)) {
			t.Errorf("%s: exit %d, result %v, want the job as received and the envelope %v", c.name, res.code, result, c.want)
		}
		if slices.Contains(res.warnings(), "attempt_envelope_unavailable") != c.warns {
			t.Errorf("%s: warnings %v, want attempt_envelope_unavailable %v", c.name, res.warnings(), c.warns)
		}
	}
}

func TestFollow_AppendsOnlyTheWarningsOfTheEnvelopeAfterItsOwn(t *testing.T) {
	envelope := `{"status":"done","warnings":[{"code":"box_unconfined","message":"w1"},{"code":"issue_closed","message":"w2"}],
		"artifacts":[{"kind":"pull_request","url":"https://example.com/pr/1"}],"next":[{"argv":["af","fix"],"reason":"r"}]}`
	serve(t, atRest(doneJob, answer(200, envelope)))
	path := home(t, "token = \"T\"\n")
	if err := os.Chmod(path, 0o644); err != nil { // others can read it: a warning of af's own
		t.Fatal(err)
	}
	res := af(t, "jobs", "watch", "j")
	if got, want := res.warnings(), []string{"credential_file_permissions", "box_unconfined", "issue_closed"}; res.code != 0 || !slices.Equal(got, want) {
		t.Errorf("exit %d, warnings %v, want %v", res.code, got, want)
	}
	if w := field(res.env["warnings"].([]any)[0], "message"); !strings.Contains(str(w), path) {
		t.Errorf("the warning %q does not name %s", w, path)
	}
	if !slices.Equal(res.artifacts(), []string{"j"}) || len(res.next()) != 0 {
		t.Errorf("artifacts %v, next %v, want only the job and nothing next", res.artifacts(), res.next())
	}
}

func TestFollow_StoppedByASignalOrTheTimeoutSuggestsWatchAndCancelAndCancelsNothing(t *testing.T) {
	for _, c := range []struct {
		name     string
		args     []string
		fromFlag bool
		category string
	}{
		{"a signal", nil, false, "cancelled"},
		{"a signal, server from the flag", nil, true, "cancelled"},
		{"--timeout", []string{"--timeout", "200ms"}, false, "timeout"},
	} {
		streaming := make(chan struct{}, 1)
		svc := serve(t, func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasSuffix(r.URL.Path, "/events") {
				answer(200, jobJSON("running", "", "", "running"))(w, r)
				return
			}
			stream(frame(1))(w, r)
			w.(http.Flusher).Flush()
			select {
			case streaming <- struct{}{}:
			default:
			}
			<-r.Context().Done()
		})
		args := append([]string{"jobs", "watch", "j"}, c.args...)
		suffix := ""
		if c.fromFlag {
			args = append(args, "--server", svc.URL)
			suffix = " --server " + svc.URL
		}
		ctx, cancel := context.WithCancel(context.Background())
		if c.category == "cancelled" {
			go func() { <-streaming; cancel() }()
		}
		res := afWith(t, ctx, strings.NewReader(""), args...)
		cancel()
		if res.code != 1 || res.category() != c.category || !slices.Equal(res.artifacts(), []string{"j"}) {
			t.Errorf("%s: exit %d, category %v, artifacts %v, want 1, %s and the job", c.name, res.code, res.category(), res.artifacts(), c.category)
		}
		if want := []string{"af jobs watch j" + suffix, "af jobs cancel j" + suffix}; !slices.Equal(res.next(), want) {
			t.Errorf("%s: next %v, want %v", c.name, res.next(), want)
		}
		for _, r := range svc.requests() {
			if r.Method != http.MethodGet {
				t.Errorf("%s: %s %s was sent, want no cancel", c.name, r.Method, r.Path)
			}
		}
	}
}
