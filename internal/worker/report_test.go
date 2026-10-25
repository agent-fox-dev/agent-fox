package worker

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

var brainUsage = toolio.Usage{Brain: "scripted", CostUSD: 1.25, CostSource: toolio.CostMeasured, InputTokens: 1000, OutputTokens: 200, Turns: 7}

// emitting makes the loop a stand-in that writes n events to its sink and ends done.
func (e *env) emitting(n int) {
	e.runner.loop = func(_ context.Context, a *attempt) *outcome {
		sink := a.eventSink(e.runner.cfg.Now)
		for i := 1; i <= n; i++ {
			sink(fmt.Appendf(nil, `{"seq":%d,"ts":"2026-10-22T08:00:00.%03dZ","run_id":%q,"type":"tick","data":{"i":%d}}`, i, i%1000, a.runID, i))
		}
		env := fakerunner.Env(toolio.StatusDone, fakerunner.WithTool("fix"), withResult(fixResult("landed", "pr", true)), fakerunner.WithUsage(brainUsage))
		return &outcome{result: jobs.Result{Envelope: env, MaxAttempts: a.maxAttempts}}
	}
}

// The events go in batches of 500, in the order they happened.
func TestReport_PostsTheUsageTheEventsAndTheCompletionAfterTheRelease(t *testing.T) {
	e := varsEnv(t, map[string]string{"AF_MODEL": "model-1"})
	e.emitting(1193) // and the worker's steps: resolve, session, clone, lease, mirror, release and report

	if _, err := e.run(e.fix()); err != nil {
		t.Fatal(err)
	}

	session := e.session()
	var sizes []int
	var ids []string
	for _, b := range e.hub.EventBatches(slug, session.RunID) {
		sizes = append(sizes, len(b))
		for _, ev := range b {
			ids = append(ids, ev.ID)
		}
	}
	if !slices.Equal(sizes, []int{500, 500, 200}) {
		t.Errorf("batches of %v events, want 500, 500 and 200", sizes)
	}
	for i, id := range ids {
		if want := fmt.Sprintf("%s-%d", session.RunID, i+1); id != want {
			t.Fatalf("event %d is %s, want %s", i, id, want)
		}
	}
	want := fakehub.Usage{Model: "model-1", Brain: "scripted", CostUSD: 1.25, CostSource: "measured", InputTokens: 1000, OutputTokens: 200, Turns: 7}
	if len(session.Usage) != 1 || session.Usage[0] != want || session.Status != "completed" {
		t.Errorf("usage %+v, status %q, want %+v and completed", session.Usage, session.Status, want)
	}
	release, usage, events := e.hub.Leases()[0].ReleasedSeq, e.seqOf("POST", sessionsPath+"/"+session.ID+"/usage", ""), e.seqOf("POST", "/api/v1/workspaces/acme/runs/", "/events/batch")
	if !(release < usage && usage < events && events < session.CompletedSeq) {
		t.Errorf("release %d, usage %d, events %d, completion %d: want that order", release, usage, events, session.CompletedSeq)
	}
}

func TestReport_CompletesTheSessionWithTheStatusOfTheEnvelope(t *testing.T) {
	long := strings.Repeat("x", 2000)
	for name, tc := range map[string]struct {
		status  toolio.Status
		mods    []func(*toolio.Envelope)
		want    string
		message string
		usage   int
	}{
		"done":        {"done", mods(withResult(fixResult("landed", "pr", true)), fakerunner.WithUsage(brainUsage)), "completed", "", 1},
		"unverified":  {"unverified", mods(withResult(fixResult("landed", "pr", false))), "completed", "", 0},
		"needs_human": {"needs_human", mods(fakerunner.WithQuestion("Which?", "a", "b")), "completed", "", 0},
		"failed":      {"failed", mods(fakerunner.WithError("git", "no", false), fakerunner.WithUsage(brainUsage)), "failed", "no", 1},
		"too long":    {"failed", mods(fakerunner.WithError("git", long, false)), "failed", long[:500], 0},
		"usage":       {"usage", mods(fakerunner.WithError("usage", "the input is empty", false)), "failed", "the input is empty", 0},
		"timeout":     {"failed", mods(fakerunner.WithError("timeout", "too slow", true)), "timeout", "too slow", 0},
	} {
		e := newEnv(t, fakehub.Workspace{})
		e.endsWith(tc.status, tc.mods...)

		if _, err := e.run(e.fix()); err != nil {
			t.Fatal(err)
		}
		if s := e.session(); s.Status != tc.want || s.Message != tc.message || len(s.Usage) != tc.usage {
			t.Errorf("%s: completed %q %q with %d usage records, want %q %q and %d", name, s.Status, s.Message, len(s.Usage), tc.want, tc.message, tc.usage)
		}
	}
}

// After a batch of events that hub did not take, no later one is posted.
func TestReport_AFailedCallIsAWarning(t *testing.T) {
	for name, tc := range map[string]struct {
		path, warning  string
		usage, batches int
		status         string
		skip           int
	}{
		"the usage":             {sessionsPath + "/ses_0001/usage", WarnHubUsageFailed, 0, 3, "completed", 0},
		"the events":            {"/api/v1/workspaces/acme/runs/*", WarnHubEventsFailed, 1, 0, "completed", 0},
		"a batch of the events": {"/api/v1/workspaces/acme/runs/*", WarnHubEventsFailed, 1, 1, "completed", 1},
		"the completion":        {sessionsPath + "/ses_0001/complete", WarnHubSessionFailed, 1, 3, "open", 0},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, fakehub.Workspace{})
			e.emitting(1193)
			e.hub.Inject("POST", tc.path, fakehub.Fault{Status: 500, Skip: tc.skip})

			res, err := e.run(e.fix())

			env := envelope(t, res)
			if err != nil || env["status"] != "done" || env["error"] != nil || !slices.Equal(codes(env), []string{tc.warning}) {
				t.Fatalf("error %v, status %v, warnings %v, want done with %s alone", err, env["status"], codes(env), tc.warning)
			}
			s := e.session()
			if len(s.Usage) != tc.usage || len(e.hub.EventBatches(slug, s.RunID)) != tc.batches || s.Status != tc.status {
				t.Errorf("%d usage records, %d batches, status %q, want %d, %d and %q", len(s.Usage), len(e.hub.EventBatches(slug, s.RunID)), s.Status, tc.usage, tc.batches, tc.status)
			}
		})
	}
}

// A line without a time is given the time it was kept; a line that is not an event is left out.
func TestEventSink_PassesTheEventsOnAndKeepsThem(t *testing.T) {
	s := &sink{}
	now := time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)
	a := &attempt{report: s, now: func() time.Time { return now }}
	write := a.eventSink(a.now)

	a.step("run")
	for _, line := range []string{`{"ts":"2026-10-22T07:00:00.000Z","type":"run_start","data":{"x":1}}`, `not json`, `{"data":{}}`, `{"type":"tick","data":{}}`} {
		write([]byte(line))
	}

	kept := a.eventsOf()
	if len(kept) != 3 || kept[1].Type != "run_start" || kept[1].TS != "2026-10-22T07:00:00.000Z" || kept[2].TS != "2026-10-22T08:00:00.000Z" || kept[2].Seq != 3 {
		t.Errorf("kept %+v, want the step, run_start at its time and tick at the time it was kept", kept)
	}
	if got := s.emitted(); len(got) != 3 || got[1].Type != "run_start" || string(got[1].Data) != `{"x":1}` {
		t.Errorf("the recorder got %+v", got)
	}
}
