package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	afclock "github.com/agent-fox-dev/agentfox/internal/afclient/fakeclock"
)

// atRestService serves the job j at rest with the document job: a short stream, the job, and the
// envelope of the attempts (envelopes maps the number of an attempt to the status and the body of
// its envelope; an attempt not in it is a 404).
func atRestService(t *testing.T, job string, envelopes map[string][2]any) *fakeService {
	t.Helper()
	return ready(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/events"):
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, eventFrame(1))
		case strings.Contains(r.URL.Path, "/attempts/"):
			attempt := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/jobs/j/attempts/"), "/")[0]
			if e, ok := envelopes[attempt]; ok {
				answering(e[0].(int), e[1].(string))(w, r)
				return
			}
			answering(404, `{"error":{"code":"not_found","message":"no such envelope"}}`)(w, r)
		default:
			answering(200, job)(w, r)
		}
	})
}

// jobDocument is the document of the job j in a state, with the attempts, the result and the
// blocked object (empty strings are null).
func jobDocument(state, attempts, result, blocked string) string {
	or := func(s string) string {
		if s == "" {
			return "null"
		}
		return s
	}
	return fmt.Sprintf(`{"id":"j","workspace":"widgets","state":%q,"future":{"kept":true},"result":%s,"blocked":%s,"attempts":%s}`, state, or(result), or(blocked), or(attempts))
}

// attemptList is the attempts array with one attempt for each state.
func attemptList(states ...string) string {
	var items []string
	for i, s := range states {
		items = append(items, fmt.Sprintf(`{"n":%d,"state":%q}`, i+1, s))
	}
	return "[" + strings.Join(items, ",") + "]"
}

// fetched are the attempt envelopes the service was asked for.
func (s *fakeService) fetched() []string {
	var out []string
	for _, r := range s.Requests() {
		if strings.Contains(r.Path, "/attempts/") {
			out = append(out, strings.TrimPrefix(r.Path, "/api/v1/jobs/j"))
		}
	}
	return out
}

// outcomeOf is the result of the envelope.
func (r afResult) result() map[string]any {
	m, _ := r.env["result"].(map[string]any)
	return m
}

// TestAtRestTheEnvelopeOfTheHighestFinishedAttemptIsFetchedAndA404LeavesItNull_TS_16_69
// verifies TS-16-69.
// Verifies: 16-REQ-10.1
func TestAtRestTheEnvelopeOfTheHighestFinishedAttemptIsFetchedAndA404LeavesItNull_TS_16_69(t *testing.T) {
	job := jobDocument("done", attemptList("done", "failed", "queued"), `{"status":"done","exit_code":0,"summary":"s","error":null}`, "")
	envelope := `{"schema_version":"5.0.0","tool":"fix","status":"done","warnings":[],"result":{"x":1}}`

	t.Run("the envelope is fetched", func(t *testing.T) {
		svc := atRestService(t, job, map[string][2]any{"1": {200, `{"which":1}`}, "2": {200, envelope}, "3": {200, `{"which":3}`}})
		res, _ := watch(t)
		if res.code != 0 {
			t.Fatalf("exit %d, %s", res.code, res.message())
		}
		if got := svc.fetched(); !slices.Equal(got, []string{"/attempts/2/envelope"}) {
			t.Errorf("fetched %v, want only the envelope of attempt 2", got)
		}
		var want any
		_ = json.Unmarshal([]byte(envelope), &want)
		if got := res.result()["envelope"]; !reflect.DeepEqual(got, want) {
			t.Errorf("result.envelope = %v, want the envelope as received", got)
		}
		if got := res.result()["job"].(map[string]any); got["id"] != "j" || got["future"] == nil {
			t.Errorf("result.job = %v, want the document as received", got)
		}
	})
	t.Run("a 404 leaves null and no warning", func(t *testing.T) {
		atRestService(t, job, nil)
		res, _ := watch(t)
		if env, present := res.result()["envelope"]; res.code != 0 || !present || env != nil || slices.Contains(codes(res.env), "attempt_envelope_unavailable") {
			t.Errorf("exit %d, envelope %v, warnings %v, want null and no warning", res.code, env, codes(res.env))
		}
	})
	t.Run("another failure leaves null and warns", func(t *testing.T) {
		atRestService(t, job, map[string][2]any{"2": {500, `{"error":{"code":"internal","message":"oops"}}`}})
		res, _ := watch(t)
		if env, present := res.result()["envelope"]; res.code != 0 || !present || env != nil || !slices.Contains(codes(res.env), "attempt_envelope_unavailable") {
			t.Errorf("exit %d, envelope %v, warnings %v, want null and the warning", res.code, env, codes(res.env))
		}
	})
	t.Run("an envelope that is not JSON warns", func(t *testing.T) {
		atRestService(t, job, map[string][2]any{"2": {200, `this is not json`}})
		res, _ := watch(t)
		if env := res.result()["envelope"]; res.code != 0 || env != nil || !slices.Contains(codes(res.env), "attempt_envelope_unavailable") {
			t.Errorf("exit %d, envelope %v, warnings %v, want null and the warning", res.code, env, codes(res.env))
		}
	})
	t.Run("no finished attempt, no fetch", func(t *testing.T) {
		svc := atRestService(t, jobDocument("failed", attemptList("queued"), `{"status":"failed","exit_code":1,"summary":"s","error":{"category":"x","message":"m","retryable":false,"fix_hint":null}}`, ""), nil)
		watch(t)
		if got := svc.fetched(); len(got) != 0 {
			t.Errorf("fetched %v, want nothing", got)
		}
	})
}

// TestOnlyTheEnvelopesWarningsAreAppendedAfterTheOwnWarnings_TS_16_70 verifies TS-16-70.
// Verifies: 16-REQ-10.2
func TestOnlyTheEnvelopesWarningsAreAppendedAfterTheOwnWarnings_TS_16_70(t *testing.T) {
	envelope := `{"status":"done","warnings":[{"code":"box_unconfined","message":"w1"},{"code":"issue_closed","message":"w2"}],
		"artifacts":[{"kind":"pull_request","url":"https://example.com/pr/1"}],"side_effects":[{"kind":"comment","target":"t","state":"done"}],
		"next":[{"argv":["af","fix"],"reason":"r"}],"usage":{"brain":"claude","cost_usd":1.5,"cost_source":"measured","input_tokens":10,"output_tokens":5,"turns":3}}`
	svc := atRestService(t, jobDocument("done", attemptList("done"), `{"status":"done","exit_code":0,"summary":"s","error":null}`, ""), map[string][2]any{"1": {200, envelope}})
	_, path := clientHome(t, str("token = \"T\"\n"))
	t.Setenv("AF_URL", svc.URL)
	if err := os.Chmod(path, 0o644); err != nil { // a warning of af's own
		t.Fatal(err)
	}
	res, _ := watch(t)
	if res.code != 0 {
		t.Fatalf("exit %d, %s", res.code, res.message())
	}
	if got, want := codes(res.env), []string{"credential_file_permissions", "box_unconfined", "issue_closed"}; !slices.Equal(got, want) {
		t.Errorf("warnings = %v, want af's own and then the envelope's: %v", got, want)
	}
	if artifacts, _ := res.env["artifacts"].([]any); len(artifacts) != 1 || artifacts[0].(map[string]any)["kind"] != "job" {
		t.Errorf("artifacts = %v, want only the job", artifacts)
	}
	if effects, _ := res.env["side_effects"].([]any); len(effects) != 0 {
		t.Errorf("side_effects = %v, want none", effects)
	}
	if next, _ := res.env["next"].([]any); len(next) != 0 {
		t.Errorf("next = %v, want none", next)
	}
	if usage, _ := res.env["usage"].(map[string]any); usage["cost_source"] != "none" || usage["cost_usd"] != 0.0 || usage["brain"] != "" {
		t.Errorf("usage = %v, want the no-brain usage", usage)
	}
}

// TestADoneJobTakesItsResultsStatusAndIsDoneWhenThereIsNone_TS_16_71 verifies TS-16-71.
// Verifies: 16-REQ-10.3
func TestADoneJobTakesItsResultsStatusAndIsDoneWhenThereIsNone_TS_16_71(t *testing.T) {
	for name, c := range map[string]struct {
		result string
		status string
		exit   int
	}{
		"done":        {`{"status":"done","exit_code":0,"summary":"fixed","error":null}`, "done", 0},
		"unverified":  {`{"status":"unverified","exit_code":4,"summary":"parked","error":null}`, "unverified", 4},
		"result null": {``, "done", 0},
	} {
		t.Run(name, func(t *testing.T) {
			atRestService(t, jobDocument("done", attemptList("done"), c.result, ""), map[string][2]any{"1": {200, `{"status":"done"}`}})
			res, _ := watch(t)
			if res.code != c.exit || field(res.env, "status") != c.status {
				t.Errorf("exit %d, status %v, want %d and %s", res.code, field(res.env, "status"), c.exit, c.status)
			}
			if job := res.result()["job"]; job == nil {
				t.Errorf("result = %v, want {job, envelope}", res.result())
			}
		})
	}
}

// TestABlockedJobEndsNeedsHumanWithItsQuestionAndTheAnswerResumeArgv_TS_16_72 verifies TS-16-72.
// Verifies: 16-REQ-10.4
func TestABlockedJobEndsNeedsHumanWithItsQuestionAndTheAnswerResumeArgv_TS_16_72(t *testing.T) {
	blocked := `{"reason":"needs_human","question":"Which branch?","options":[{"id":"A","label":"main"},{"id":"B","label":"release"}],"answer_with":"POST /api/v1/jobs/j/answer"}`
	svc := atRestService(t, jobDocument("blocked", attemptList("needs_human"), `{"status":"needs_human","exit_code":3,"summary":"asks","error":null}`, blocked), nil)
	for _, c := range []struct {
		name string
		args []string
		argv []string
	}{
		{"server from AF_URL", nil, []string{"af", "jobs", "answer", "j", "--wait"}},
		{"server from the flag", []string{"--server", svc.URL}, []string{"af", "jobs", "answer", "j", "--wait", "--server", svc.URL}},
	} {
		res, _ := watch(t, c.args...)
		if res.code != 3 || field(res.env, "status") != "needs_human" {
			t.Fatalf("%s: exit %d, status %v, want 3 and needs_human", c.name, res.code, field(res.env, "status"))
		}
		nh, _ := res.env["needs_human"].(map[string]any)
		if nh["question"] != "Which branch?" {
			t.Errorf("%s: question = %v", c.name, nh["question"])
		}
		var labels []string
		for _, o := range nh["options"].([]any) {
			labels = append(labels, o.(map[string]any)["id"].(string)+"="+o.(map[string]any)["label"].(string))
		}
		if !slices.Equal(labels, []string{"A=main", "B=release"}) {
			t.Errorf("%s: options = %v, want A=main and B=release in order", c.name, labels)
		}
		resume, _ := nh["resume"].(map[string]any)
		var argv []string
		for _, a := range resume["argv"].([]any) {
			argv = append(argv, a.(string))
		}
		if !slices.Equal(argv, c.argv) || resume["flag"] != "--context" {
			t.Errorf("%s: resume = %v, want argv %v and flag --context", c.name, resume, c.argv)
		}
	}
}

// TestAFailedJobTakesItsResultsStatusAndErrorAndIsInternalWhenThereIsNoError_TS_16_73 verifies
// TS-16-73.
// Verifies: 16-REQ-10.5
func TestAFailedJobTakesItsResultsStatusAndErrorAndIsInternalWhenThereIsNoError_TS_16_73(t *testing.T) {
	errorObj := `{"category":"sandbox","message":"the box was lost","retryable":true,"fix_hint":{"flag":"--sandbox","suggest":"podman"}}`
	usageObj := `{"category":"usage","message":"bad invocation","retryable":false,"fix_hint":null}`
	for name, c := range map[string]struct {
		result   string
		status   string
		exit     int
		category string
	}{
		"failed with an error": {`{"status":"failed","exit_code":1,"summary":"s","error":` + errorObj + `}`, "failed", 1, "sandbox"},
		"unverified":           {`{"status":"unverified","exit_code":4,"summary":"parked","error":null}`, "unverified", 4, ""},
		"usage":                {`{"status":"usage","exit_code":2,"summary":"s","error":` + usageObj + `}`, "usage", 2, "usage"},
		"failed, no error":     {`{"status":"failed","exit_code":1,"summary":"s","error":null}`, "failed", 1, "internal"},
		"no result":            {``, "failed", 1, "internal"},
	} {
		t.Run(name, func(t *testing.T) {
			atRestService(t, jobDocument("failed", attemptList("failed"), c.result, ""), map[string][2]any{"1": {200, `{"status":"failed"}`}})
			res, _ := watch(t)
			if res.code != c.exit || field(res.env, "status") != c.status || res.category() != c.category {
				t.Errorf("exit %d, status %v, category %q, want %d, %s, %q", res.code, field(res.env, "status"), res.category(), c.exit, c.status, c.category)
			}
			if c.category == "sandbox" {
				if res.message() != "the box was lost" || !res.retryable() {
					t.Errorf("message %q, retryable %v, want the error of the result", res.message(), res.retryable())
				}
				if h := res.hint(); h["flag"] != "--sandbox" || h["suggest"] != "podman" {
					t.Errorf("fix_hint = %v, want the hint of the error", h)
				}
			}
			if !strings.Contains(res.stdout, `"j"`) && c.status != "unverified" {
				t.Errorf("the envelope does not name the job:\n%s", res.stdout)
			}
		})
	}
}

// TestACancelledJobEndsJobCancelledWhateverTheEnvelopeSays_TS_16_74 verifies TS-16-74.
// Verifies: 16-REQ-10.6
func TestACancelledJobEndsJobCancelledWhateverTheEnvelopeSays_TS_16_74(t *testing.T) {
	cancelledResult := `{"status":"failed","exit_code":1,"summary":"s","error":{"category":"cancelled","message":"the command was cancelled","retryable":false,"fix_hint":null}}`
	for name, envelopes := range map[string]map[string][2]any{
		"category cancelled": {"1": {200, `{"status":"failed","error":{"category":"cancelled","message":"x"}}`}},
		"another category":   {"1": {200, `{"status":"failed","error":{"category":"brain","message":"x"}}`}},
		"no envelope":        nil,
	} {
		t.Run(name, func(t *testing.T) {
			atRestService(t, jobDocument("cancelled", attemptList("cancelled"), cancelledResult, ""), envelopes)
			res, _ := watch(t)
			if res.code != 1 || res.category() != "job_cancelled" || res.retryable() {
				t.Errorf("exit %d, category %q, retryable %v, want 1, job_cancelled, not retryable", res.code, res.category(), res.retryable())
			}
			if m := res.message(); !strings.Contains(m, "cancelled on the service") || !strings.Contains(m, "j") {
				t.Errorf("message %q, want it to say that the job was cancelled on the service", m)
			}
		})
	}
}

// TestInterruptingOrTimingOutAFollowingSendsNoCancelAndSuggestsWatchAndCancel_TS_16_75 verifies
// TS-16-75 for af jobs watch: a signal or --timeout while following ends the command with the
// shell's failure, no cancel request is sent, the job stays in the artifacts, and next holds
// af jobs watch and af jobs cancel. The same through af fix --wait is checked with that command.
// Verifies: 16-REQ-10.7
func TestInterruptingOrTimingOutAFollowingSendsNoCancelAndSuggestsWatchAndCancel_TS_16_75(t *testing.T) {
	for _, c := range []struct {
		name     string
		args     []string
		fromFlag bool
		interupt bool
		category string
	}{
		{"a signal", nil, false, true, "cancelled"},
		{"a signal, server from the flag", []string{"--server", "SERVER"}, true, true, "cancelled"},
		{"--timeout", []string{"--timeout", "400ms"}, false, false, "timeout"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var streaming atomic.Bool
			svc := ready(t, func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/events") {
					answering(200, jobDocument("running", attemptList("running"), "", ""))(w, r)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, eventFrame(1))
				w.(http.Flusher).Flush()
				streaming.Store(true)
				<-r.Context().Done()
			})
			args := slices.Clone(c.args)
			for i := range args {
				args[i] = strings.Replace(args[i], "SERVER", svc.URL, 1)
			}
			useClock(t, afclock.New(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var out, errb strings.Builder
			done := make(chan int, 1)
			go func() {
				done <- newApp().Main(ctx, append([]string{"af", "jobs", "watch", "j"}, args...), strings.NewReader(""), &out, &errb)
			}()
			if c.interupt {
				deadline := time.Now().Add(10 * time.Second)
				for !streaming.Load() && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				cancel()
			}
			select {
			case code := <-done:
				if code != 1 {
					t.Errorf("exit %d, want 1", code)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("the command did not end")
			}
			env := oneDocument(t, out.String())
			if got := field(env, "error", "category"); got != c.category {
				t.Errorf("category %v, want %s", got, c.category)
			}
			for _, r := range svc.Requests() {
				if r.Method == "POST" {
					t.Errorf("a %s request to %s was sent, want no cancel", r.Method, r.Path)
				}
			}
			if artifacts, _ := env["artifacts"].([]any); len(artifacts) != 1 || artifacts[0].(map[string]any)["name"] != "j" {
				t.Errorf("artifacts = %v, want the job", artifacts)
			}
			var argvs []string
			next, _ := env["next"].([]any)
			for _, n := range next {
				var parts []string
				for _, a := range n.(map[string]any)["argv"].([]any) {
					parts = append(parts, a.(string))
				}
				argvs = append(argvs, strings.Join(parts, " "))
			}
			suffix := ""
			if c.fromFlag {
				suffix = " --server " + svc.URL
			}
			if want := []string{"af jobs watch j" + suffix, "af jobs cancel j" + suffix}; !slices.Equal(argvs, want) {
				t.Errorf("next = %v, want %v", argvs, want)
			}
		})
	}

	// A following that reaches rest has neither entry.
	atRestService(t, jobDocument("done", attemptList("done"), `{"status":"done","exit_code":0,"summary":"s","error":null}`, ""), nil)
	res, _ := watch(t)
	if next, _ := res.env["next"].([]any); res.code != 0 || len(next) != 0 {
		t.Errorf("a following that reached rest: exit %d, next %v, want 0 and none", res.code, next)
	}
}
