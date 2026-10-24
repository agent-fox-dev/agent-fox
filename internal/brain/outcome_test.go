package brain

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The nonce that the run.json of these tests holds.
const placedNonce = "0011223344556677"

// placeSubmission puts a run.json with the nonce, and a submission.json made under the
// given one, in the box, as an attempt of a brain would leave them. An empty made means
// no submission.
func (w *procWorld) placeSubmission(attempt int, made string) {
	w.t.Helper()
	ctx := context.Background()
	run, _ := json.Marshal(submission.Run{RunID: w.run.ID, Kind: "test", Attempt: attempt, Nonce: placedNonce})
	if err := w.session.WriteFile(ctx, submission.RunPath(w.run.ID), run); err != nil {
		w.t.Fatal(err)
	}
	if made == "" {
		return
	}
	payload := []byte(`{"summary":"done"}`)
	file := submission.File{
		Kind: "test", RunID: w.run.ID, Attempt: attempt, Nonce: made, SubmittedAt: "2026-10-21T00:00:00Z",
		PayloadSHA256: submission.PayloadSHA256(payload), Payload: payload,
	}
	if err := w.session.WriteFile(ctx, submission.SubmissionPath(w.run.ID), submission.Marshal(file)); err != nil {
		w.t.Fatal(err)
	}
}

// warned are the warnings of the code the run raised.
func (w *procWorld) warned(code string) []toolio.Warning {
	var out []toolio.Warning
	for _, x := range w.report.warnings {
		if x.Code == code {
			out = append(out, x)
		}
	}
	return out
}

// TestASubmissionOfARunWithNoNonceIsNoClaim guards the claim of the brain: a run.json
// with no nonce has no attempt for a submission to belong to, however empty the nonce of
// the file is.
func TestASubmissionOfARunWithNoNonceIsNoClaim(t *testing.T) {
	key := map[string]string{"ANTHROPIC_API_KEY": "sk-test"}
	w := newProcWorld(t, "podman", key, nil)
	w.script(fakeScript{}) // no envelope: the claim is read from the files of the box
	brain, err := inBoxBrain(t, w, "podman", "", key)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	run, _ := json.Marshal(submission.Run{RunID: w.run.ID, Kind: "test", Attempt: 1})
	payload := []byte(`{"summary":"done"}`)
	file := submission.Marshal(submission.File{Kind: "test", RunID: w.run.ID, Attempt: 1, PayloadSHA256: submission.PayloadSHA256(payload), Payload: payload})
	if err := w.session.WriteFile(ctx, submission.RunPath(w.run.ID), run); err != nil {
		t.Fatal(err)
	}
	if err := w.session.WriteFile(ctx, submission.SubmissionPath(w.run.ID), file); err != nil {
		t.Fatal(err)
	}

	out, err := brain.Run(ctx, w.processBrief(1))

	if err != nil || out.Submitted {
		t.Errorf("outcome = %+v, %v, want no claim", out, err)
	}
}
