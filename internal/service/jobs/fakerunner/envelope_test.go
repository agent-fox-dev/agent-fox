package fakerunner_test

import (
	"bytes"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestEnv_IsIndentedJSONOfTheStatusAsACommandWritesIt(t *testing.T) {
	raw := fakerunner.Env(toolio.StatusFailed)
	if !bytes.HasPrefix(raw, []byte("{\n  ")) || !bytes.HasSuffix(raw, []byte("}\n")) {
		t.Errorf("the envelope is %.40q, want indented JSON ending in a newline", raw)
	}
	env := testutil.Decode[toolio.Envelope](t, raw)
	if env.Status != toolio.StatusFailed || env.ExitCode != toolio.ExitCode(toolio.StatusFailed) || env.SchemaVersion != toolio.SchemaVersion ||
		env.Tool != "fix" || env.Warnings == nil || env.Artifacts == nil || env.SideEffects == nil || env.Next == nil {
		t.Errorf("the envelope is %s", raw)
	}
}

func TestEnv_ModsChangeTheEnvelope(t *testing.T) {
	artifact := toolio.Artifact{Kind: "pull_request", URL: "https://forge.example.com/pull/1", Number: 1}
	effect := toolio.SideEffect{Kind: "label", Target: "issue 42", State: "done"}
	usage := toolio.Usage{Brain: "scripted", CostUSD: 0.5, CostSource: "reported", Turns: 3}
	env := testutil.Decode[toolio.Envelope](t, fakerunner.Env(toolio.StatusNeedsHuman,
		fakerunner.WithTool("triage"), fakerunner.WithSummary("asks"), fakerunner.WithError("brain", "no answer", true),
		fakerunner.WithQuestion("Which?", "this", "that"), fakerunner.WithOutcome("parked"),
		fakerunner.WithArtifacts(artifact), fakerunner.WithSideEffects(effect), fakerunner.WithUsage(usage)))

	if env.Tool != "triage" || env.Summary != "asks" {
		t.Errorf("tool %q, summary %q", env.Tool, env.Summary)
	}
	if e := env.Error; e == nil || e.Category != "brain" || e.Message != "no answer" || !e.Retryable {
		t.Errorf("error = %+v", env.Error)
	}
	nh := env.NeedsHuman
	if nh == nil || nh.Question != "Which?" || len(nh.Options) != 2 || nh.Options[0] != (toolio.Option{ID: "A", Label: "this"}) ||
		nh.Options[1] != (toolio.Option{ID: "B", Label: "that"}) || nh.Resume.Flag != "--answer" {
		t.Errorf("needs_human = %+v", nh)
	}
	if outcome := testutil.Decode[map[string]any](t, testutil.JSON(t, env.Result))["outcome"]; outcome != "parked" {
		t.Errorf("result = %v, want the outcome parked", env.Result)
	}
	if len(env.Artifacts) != 1 || env.Artifacts[0] != artifact || len(env.SideEffects) != 1 || env.SideEffects[0] != effect || env.Usage != usage {
		t.Errorf("artifacts %v, side effects %v, usage %+v", env.Artifacts, env.SideEffects, env.Usage)
	}
}

func TestDoneAndFailed_ReturnTheirEnvelopes(t *testing.T) {
	r := fakerunner.New(fakerunner.Done(fakerunner.WithSummary("all good")), fakerunner.Failed("sandbox", true))
	res, err := r.Run(ctx, request("job_1"))
	if done := testutil.Decode[toolio.Envelope](t, res.Envelope); err != nil || done.Status != toolio.StatusDone || done.Summary != "all good" {
		t.Errorf("Done = %s, %v", res.Envelope, err)
	}
	res, err = r.Run(ctx, request("job_1"))
	failed := testutil.Decode[toolio.Envelope](t, res.Envelope)
	if err != nil || failed.Status != toolio.StatusFailed || failed.Error == nil || failed.Error.Category != "sandbox" || !failed.Error.Retryable {
		t.Errorf("Failed = %s, %v", res.Envelope, err)
	}
}
