package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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

// TestClaudeOutputMapsToTheOutcome_TS_10_53 verifies TS-10-53: the cost comes from
// total_cost_usd, else cost_usd; the turns from num_turns; the input tokens are the sum
// of the input, cache-creation and cache-read tokens; the stop reason comes from the
// subtype; Submitted says whether the box has a submission with this attempt's nonce; the
// transcript is empty; and claude_result is noted.
// Verifies: 10-REQ-9.1
func TestClaudeOutputMapsToTheOutcome_TS_10_53(t *testing.T) {
	usageJSON := `"usage":{"input_tokens":100,"output_tokens":20,"cache_creation_input_tokens":30,"cache_read_input_tokens":400}`
	for _, tc := range []struct {
		name, output       string
		cost               float64
		turns, in, out     int
		stop               string
		made               string
		wantSubmitted      bool
		wantRecordedTotals bool
	}{
		{"success", `{"type":"result","subtype":"success","is_error":false,"num_turns":4,"total_cost_usd":0.5,"cost_usd":9,` + usageJSON + `,"session_id":"s1"}`, 0.5, 4, 530, 20, StopEnded, placedNonce, true, true},
		{"max turns", `{"type":"result","subtype":"error_max_turns","is_error":true,"num_turns":9,"total_cost_usd":0.75,` + usageJSON + `,"session_id":"s2"}`, 0.75, 9, 530, 20, StopTurns, "", false, true},
		{"a budget subtype", `{"type":"result","subtype":"error_max_budget_usd","is_error":true,"num_turns":2,"total_cost_usd":0.8,` + usageJSON + `,"session_id":"s3"}`, 0.8, 2, 530, 20, StopBudget, "", false, true},
		{"another subtype", `{"type":"result","subtype":"error_during_execution","is_error":true,"num_turns":1,"total_cost_usd":0.1,` + usageJSON + `,"session_id":"s4"}`, 0.1, 1, 530, 20, StopError, "", false, true},
		{"only cost_usd", `{"type":"result","subtype":"success","is_error":false,"num_turns":3,"cost_usd":0.25,"usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":3},"session_id":"s5"}`, 0.25, 3, 4, 2, StopEnded, "ffffffffffffffff", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newClaudeWorld(t, "local", map[string]string{"ANTHROPIC_API_KEY": "sk-test"}, "", false)
			w.script(fakeScript{OutputJSON: tc.output})
			w.placeSubmission(1, tc.made)

			out, err := w.runClaude(1, nil)

			if err != nil {
				t.Fatal(err)
			}
			if out.CostUSD != tc.cost || out.Turns != tc.turns || out.InputTokens != tc.in || out.OutputTokens != tc.out || out.StopReason != tc.stop ||
				out.CostSource != CostReported || out.Submitted != tc.wantSubmitted || out.Transcript != "" {
				t.Errorf("outcome = %+v, want cost %v, %d turns, %d tokens in and %d out, stop %s, reported, submitted %v, no transcript",
					out, tc.cost, tc.turns, tc.in, tc.out, tc.stop, tc.wantSubmitted)
			}
			entry := w.entry("claude_result")
			for _, key := range []string{"subtype", "is_error", "num_turns", "total_cost_usd", "usage", "session_id"} {
				if _, ok := entry[key]; !ok {
					t.Errorf("claude_result = %v, has no %s", entry, key)
				}
			}
			if entry["num_turns"] != float64(tc.turns) || !strings.HasPrefix(fmt.Sprint(entry["session_id"]), "s") {
				t.Errorf("claude_result = %v", entry)
			}
		})
	}
}

// TestTheUsageOfAClaudeRunIsReportedCost verifies the other half of TS-10-53: through
// Execute the usage of the run says its cost source is reported, with the cost and the
// turns the output gave.
func TestTheUsageOfAClaudeRunIsReportedCost(t *testing.T) {
	w := newClaudeWorld(t, "local", map[string]string{"ANTHROPIC_API_KEY": "sk-test"}, "", false)
	w.script(fakeScript{OutputJSON: `{"subtype":"success","num_turns":5,"total_cost_usd":0.42,"usage":{"input_tokens":10,"output_tokens":3}}`})
	req := w.request(w.brief(1), w.brain)
	req.Name = ClaudeName

	_, _ = Execute(context.Background(), req) // no submission: the run fails, and its cost was spent

	u := w.report.lastUsage()
	if u.Brain != ClaudeName || u.CostSource != CostReported || u.CostUSD != 0.42 || u.Turns != 5 || u.InputTokens != 10 || u.OutputTokens != 3 {
		t.Errorf("usage = %+v, want the reported cost of the claude brain", u)
	}
}

// TestAMissingOrUnparseableOutputIsCostZeroAndOneWarning_TS_10_54 verifies TS-10-54 for
// claude: with no output, and with output that is not JSON, the cost is 0, the cost source
// none and there is one brain_usage_unavailable warning, and the submission is decided
// from the file as ever.
// Verifies: 10-REQ-9.2
func TestAMissingOrUnparseableOutputIsCostZeroAndOneWarning_TS_10_54(t *testing.T) {
	for name, output := range map[string]string{"no output": "", "not json": "not json at all", "an array": "[1,2]", "null": "null"} {
		t.Run(name, func(t *testing.T) {
			w := newClaudeWorld(t, "local", map[string]string{"ANTHROPIC_API_KEY": "sk-test"}, "", false)
			w.script(fakeScript{OutputJSON: output})
			submitted := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
				target := submission.Target{Tree: b.Box.FS(ctx), Write: b.Box.WriteFile, RunID: b.RunID}
				if _, err := submission.Submit(ctx, target, b.Kind, []byte(`{"summary":"made"}`), time.Now()); err != nil {
					return Outcome{}, err
				}
				return w.brain.Run(ctx, b)
			})
			req := w.request(w.brief(1), submitted)
			req.Name = ClaudeName

			res, err := Execute(context.Background(), req)

			if err != nil || !res.Outcome.Submitted {
				t.Fatalf("Execute = %+v, %v, want the submission decided from the file", res, err)
			}
			if res.Outcome.CostUSD != 0 || res.Outcome.CostSource != CostNone {
				t.Errorf("outcome = %+v, want cost 0 and source none", res.Outcome)
			}
			if u := w.report.lastUsage(); u.CostSource != CostNone || u.CostUSD != 0 {
				t.Errorf("usage = %+v, want cost_source none", u)
			}
			if n := len(w.warned("brain_usage_unavailable")); n != 1 {
				t.Errorf("%d brain_usage_unavailable warnings, want one", n)
			}
		})
	}
}

// TestANonZeroExitWithNoSubmissionIsAnErrorWithTheEndOfStderr_TS_10_55 verifies TS-10-55:
// a claude that exits 2 after 5 KiB of errors ending in TAIL-MARK, and submitted nothing,
// makes Run return an error with the last 2 KiB of the errors; with a submission, the
// same exit is no error.
// Verifies: 10-REQ-9.3
func TestANonZeroExitWithNoSubmissionIsAnErrorWithTheEndOfStderr_TS_10_55(t *testing.T) {
	stderr := strings.Repeat("e", 5<<10)
	w := newClaudeWorld(t, "local", map[string]string{"ANTHROPIC_API_KEY": "sk-test"}, "", false)
	w.script(fakeScript{Exit: 2, StderrBytes: len(stderr), Stderr: "TAIL-MARK"})
	w.placeSubmission(1, "")

	out, err := w.runClaude(1, nil)

	if err == nil {
		t.Fatalf("Run = %+v, nil, want an error", out)
	}
	msg := err.Error()
	if !strings.Contains(msg, "TAIL-MARK") || !strings.Contains(msg, "2") {
		t.Errorf("error = %q, want the end of the errors and the exit code", msg)
	}
	if len(msg) > 2<<10+400 {
		t.Errorf("the error is %d bytes long, want the last 2 KiB of the errors and a few words", len(msg))
	}
	if out.CostSource != CostNone && out.CostSource != CostReported {
		t.Errorf("outcome = %+v", out)
	}

	w2 := newClaudeWorld(t, "local", map[string]string{"ANTHROPIC_API_KEY": "sk-test"}, "", false)
	w2.script(fakeScript{Exit: 2, StderrBytes: 1 << 10, Stderr: "TAIL-MARK"})
	w2.placeSubmission(1, placedNonce)
	if out, err := w2.runClaude(1, nil); err != nil || !out.Submitted {
		t.Errorf("with a submission: %+v, %v, want no error", out, err)
	}
}

// TestACostAboveTheBoundIsAWarningAndABudgetStopOnlyWithoutASubmission_TS_10_56 verifies
// TS-10-56: a reported cost above the bound raises budget_exceeded once, and sets the
// stop reason to budget when nothing was submitted and leaves it alone when something
// was.
// Verifies: 10-REQ-9.4
func TestACostAboveTheBoundIsAWarningAndABudgetStopOnlyWithoutASubmission_TS_10_56(t *testing.T) {
	output := `{"subtype":"success","num_turns":3,"total_cost_usd":2.5,"usage":{"input_tokens":1,"output_tokens":1}}`
	for _, tc := range []struct {
		name, made string
		stop       string
	}{{"nothing submitted", "", StopBudget}, {"submitted", placedNonce, StopEnded}} {
		t.Run(tc.name, func(t *testing.T) {
			w := newClaudeWorld(t, "local", map[string]string{"ANTHROPIC_API_KEY": "sk-test"}, "", false)
			w.script(fakeScript{OutputJSON: output})
			w.placeSubmission(1, tc.made)

			out, err := w.runClaude(1, func(b *Brief) { b.MaxCostUSD = 1 })

			if err != nil || out.StopReason != tc.stop || out.CostUSD != 2.5 {
				t.Errorf("outcome = %+v, %v, want stop %s and the cost of 2.5", out, err, tc.stop)
			}
			if n := len(w.warned("budget_exceeded")); n != 1 {
				t.Errorf("%d budget_exceeded warnings, want one", n)
			}
		})
	}

	for name, bound := range map[string]float64{"a cost that is the bound": 2.5, "no bound": 0} {
		w := newClaudeWorld(t, "local", map[string]string{"ANTHROPIC_API_KEY": "sk-test"}, "", false)
		w.script(fakeScript{OutputJSON: output})
		w.placeSubmission(1, "")
		if out, _ := w.runClaude(1, func(b *Brief) { b.MaxCostUSD = bound }); out.StopReason == StopBudget || len(w.warned("budget_exceeded")) != 0 {
			t.Errorf("%s: %+v, %v warnings, want neither", name, out, w.warned("budget_exceeded"))
		}
	}
}

// TestASubmissionOfARunWithNoNonceIsNoClaim guards the claim of the brain: a run.json
// with no nonce has no attempt for a submission to belong to, however empty the nonce of
// the file is.
func TestASubmissionOfARunWithNoNonceIsNoClaim(t *testing.T) {
	w := newClaudeWorld(t, "local", map[string]string{"ANTHROPIC_API_KEY": "sk-test"}, "", false)
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

	out, err := w.runClaude(1, nil)

	if err != nil || out.Submitted {
		t.Errorf("outcome = %+v, %v, want no claim", out, err)
	}
}
