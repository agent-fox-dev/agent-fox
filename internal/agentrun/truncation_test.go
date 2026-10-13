package agentrun

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
)

// truncatedTurn is a response the output limit cut short before the model
// called anything: all of it went to thinking, which arrives unsigned because
// the stream ended inside the block.
func truncatedTurn() faux.Turn {
	return faux.Turn{
		Blocks:     []core.ContentBlock{faux.FauxThinking("Let me weigh the sections first...", "")},
		StopReason: core.StopReasonLength,
	}
}

// lastUserText is the text of the last user message a request carried.
func lastUserText(req core.Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if um, ok := req.Messages[i].(core.UserMessage); ok {
			return um.Content.Text()
		}
	}
	return ""
}

// A response the output limit cut off before any tool call is not the model
// answering in prose: it was still working. The phase tells it so and carries
// on, rather than ending on what the loop reports as a clean end_turn.
func TestATruncatedTurnWithoutAToolCallIsNudgedToSubmit(t *testing.T) {
	var got string
	var calls int
	p := faux.New(
		truncatedTurn(),
		toolCallTurn("c1", "submit", map[string]any{"value": "the answer"}),
	)
	r, err := NewRunner(fauxConfig(p, newWorkspace(t)))
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Run(context.Background(), Phase{
		Name: "phase", System: "s", User: "u", Terminator: "submit",
		Custom: []core.Tool{submitTool(&got, &calls)},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 1 || got != "the answer" {
		t.Fatalf("submit calls = %d, value = %q; want the submission after the nudge", calls, got)
	}
	if res.StopReason != core.RunStopToolTerminate {
		t.Errorf("StopReason = %q, want %q", res.StopReason, core.RunStopToolTerminate)
	}
	reqs := p.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want the truncated turn and the one after the nudge", len(reqs))
	}
	nudge := lastUserText(reqs[1])
	for _, want := range []string{"output token limit", "submit"} {
		if !strings.Contains(nudge, want) {
			t.Errorf("the nudge should mention %q: %q", want, nudge)
		}
	}
}

// The nudge is bounded: a model that overruns every response is not worth
// paying for indefinitely, and the turn limit alone would let it try until
// the budget runs out. When the nudges are used up the phase ends, and the
// result says the last response was cut off.
func TestTruncationRecoveryIsBounded(t *testing.T) {
	var got string
	var calls int
	turns := make([]faux.Turn, 0, maxTruncationNudges+2)
	for range maxTruncationNudges + 2 {
		turns = append(turns, truncatedTurn())
	}
	p := faux.New(turns...)
	r, err := NewRunner(fauxConfig(p, newWorkspace(t)))
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Run(context.Background(), Phase{
		Name: "phase", System: "s", User: "u", Terminator: "submit",
		Custom: []core.Tool{submitTool(&got, &calls)},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := len(p.Requests()); n != maxTruncationNudges+1 {
		t.Errorf("requests = %d, want the first response and one per nudge (%d)", n, maxTruncationNudges+1)
	}
	if res.LastReason != core.StopReasonLength {
		t.Errorf("LastReason = %q, want %q", res.LastReason, core.StopReasonLength)
	}
}

// A phase whose last response hit the output limit did not run out of turns
// or money, so the error must not send the operator to --max-turns or
// --budget: neither would have helped.
func TestNoResultErrorNamesTheOutputTokenLimit(t *testing.T) {
	err := NoResultError("prd", "submit_prd", Result{
		StopReason: core.RunStopEndTurn,
		LastReason: core.StopReasonLength,
	})
	var ae *Error
	if !errors.As(err, &ae) || ae.Category() != CategoryNoResult {
		t.Fatalf("NoResultError = %v (%T)", err, err)
	}
	msg := err.Error()
	for _, want := range []string{"output token limit", "submit_prd"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error should mention %q: %v", want, msg)
		}
	}
	for _, bad := range []string{"--max-turns", "--budget"} {
		if strings.Contains(msg, bad) {
			t.Errorf("the error should not suggest %s: %v", bad, msg)
		}
	}
}
