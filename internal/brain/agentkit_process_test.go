package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/fakeaf"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// envelopeText is the document af <agent> run prints.
func envelopeText(status, result, errObj string) string {
	return fmt.Sprintf(`{"schema_version":"5.0.0","status":%q,"usage":{"cost_usd":0.42,"cost_source":"measured","input_tokens":1000,"output_tokens":200,"turns":5},"result":%s,"error":%s}`, status, result, errObj)
}

// inBox is the agentkit brain as a command makes it for a podman box, run with the fake
// af in the local box of the world.
func inBox(t *testing.T, w *procWorld, model string) Brain {
	t.Helper()
	brain, err := Default.New(AgentkitName, Options{Provider: "podman", Model: model, Getenv: envOf(map[string]string{VendorKeyVar: "sk-test"}), AFPath: w.af, AFVersion: "v1.0.0", AFCommit: "abc123"})
	if err != nil {
		t.Fatal(err)
	}
	return brain
}

// place puts a run.json with the nonce, and a submission.json made under made, in the
// box, as an attempt leaves them; an empty made is no submission.
func (w *procWorld) place(attempt int, nonce, made string) {
	w.t.Helper()
	ctx := context.Background()
	if err := w.session.WriteFile(ctx, submission.RunPath(w.run.ID), must(json.Marshal(submission.Run{RunID: w.run.ID, Kind: "test", Attempt: attempt, Nonce: nonce}))); err != nil {
		w.t.Fatal(err)
	}
	if made == "" {
		_ = os.Remove(filepath.Join(w.session.Root(), submission.SubmissionPath(w.run.ID)))
		return
	}
	payload := []byte(`{"summary":"done"}`)
	file := submission.File{Kind: "test", RunID: w.run.ID, Attempt: attempt, Nonce: made, PayloadSHA256: submission.PayloadSHA256(payload), Payload: payload}
	if err := w.session.WriteFile(ctx, submission.SubmissionPath(w.run.ID), submission.Marshal(file)); err != nil {
		w.t.Fatal(err)
	}
}

func TestNewAgentkit_InABoxNeedsTheVendorKeyUnlessTheBoxSuppliesIt(t *testing.T) {
	b, err := Default.New(AgentkitName, Options{Provider: "podman", Getenv: noEnv})
	if f := asFailure(t, err); b != nil || f.Category != toolio.CategoryAuth || f.FixHint == nil || f.FixHint.Env != VendorKeyVar {
		t.Errorf("podman without the key: %v, %+v; want failed/auth with the variable", b, f)
	}
	reg := box.NewRegistry()
	reg.Register(box.Entry{Name: "gateway", SuppliesCredentials: true})
	if b, err := Default.New(AgentkitName, Options{Provider: "gateway", Boxes: reg, Getenv: noEnv}); err != nil || b == nil {
		t.Errorf("a box that supplies the key: %v, %v, want the brain", b, err)
	}
}

func TestAgentkitInBox_RunsAfAgentRunWithTheBoundsOfTheBrief(t *testing.T) {
	w := newProcWorld(t, box.Request{Provider: "local", Env: map[string]string{VendorKeyVar: "sk-test"}}, nil)
	w.script(fakeaf.Script{OutputJSON: envelopeText("done", `{"submitted":true}`, "null")})
	inAnHour, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	base := []string{"fix", "run", "--run-id", w.run.ID, "--dir", w.session.Root()}
	for i, tc := range []struct {
		name    string
		model   string
		ctx     context.Context
		brief   func(*Brief)
		args    []string
		timeout time.Duration // the least the --timeout may be, if there is one
	}{
		{"the bounds", "m1", context.Background(), func(b *Brief) { b.MaxCostUSD = 2.5 }, []string{"--budget", "2.5", "--max-turns", "10"}, 0},
		{"no bounds and the brief's timeout", "", context.Background(), func(b *Brief) { b.MaxCostUSD, b.MaxTurns, b.Timeout = 0, 0, 20*time.Minute }, []string{"--timeout", "20m0s"}, 0},
		{"the time left", "", inAnHour, func(b *Brief) { b.MaxCostUSD, b.MaxTurns = 0, 0 }, []string{"--timeout"}, 59 * time.Minute},
	} {
		b := w.processBrief(i + 1)
		tc.brief(&b)

		out, err := inBox(t, w, tc.model).Run(tc.ctx, b)

		if err != nil || !out.Submitted || out.CostUSD != 0.42 || out.CostSource != CostMeasured || out.Turns != 5 || out.InputTokens != 1000 || out.OutputTokens != 200 || out.StopReason != StopEnded {
			t.Errorf("%s: %+v, %v, want the outcome of the envelope", tc.name, out, err)
		}
		run := w.runs()[i]
		args := run.Argv[:min(len(run.Argv), len(base)+len(tc.args))]
		if !slices.Equal(args, append(slices.Clone(base), tc.args...)) || len(run.Argv) > len(args)+1 {
			t.Errorf("%s: argv = %q, want %q", tc.name, run.Argv, append(base, tc.args...))
		}
		if tc.timeout > 0 {
			if d, err := time.ParseDuration(run.Argv[len(run.Argv)-1]); err != nil || d < tc.timeout || d > time.Hour || d%time.Second != 0 {
				t.Errorf("%s: --timeout %s, want the whole seconds left on the context", tc.name, run.Argv[len(run.Argv)-1])
			}
		}
		seen := envOfRecord(run)
		if model, ok := seen["AF_MODEL"]; seen[VendorKeyVar] != "sk-test" || (tc.model == "") == ok || model != tc.model {
			t.Errorf("%s: the process saw %v, want the vendor key, and AF_MODEL only for a model", tc.name, seen)
		}
	}

	w.script(fakeaf.Script{Exit: 1, OutputJSON: envelopeText("failed", "null", `{"category":"brain","message":"the driver broke"}`)})
	if out, err := inBox(t, w, "").Run(context.Background(), w.processBrief(4)); err == nil || !strings.Contains(err.Error(), "the driver broke") || out.StopReason != StopError {
		t.Errorf("a failed run: %+v, %v, want an error with the message of the envelope", out, err)
	}
}

func TestParseEnvelope_IsTheOutcomeOfTheDriverInTheBox(t *testing.T) {
	budget := func(flag string) string {
		return `{"category":"budget","message":"used the bound","fix_hint":{"flag":"` + flag + `"}}`
	}
	for _, tc := range []struct {
		name, data    string
		ok, submitted bool
		stop, failure string
		source        string
	}{
		{"a submission", envelopeText("done", `{"submitted":true}`, "null"), true, true, StopEnded, "", CostMeasured},
		{"no claim", envelopeText("done", `{"submitted":false}`, "null"), true, false, StopEnded, "", CostMeasured},
		{"the cost bound", envelopeText("failed", "null", budget("--budget")), true, false, StopBudget, "", CostMeasured},
		{"the turn bound", envelopeText("failed", "null", budget("--max-turns")), true, false, StopTurns, "", CostMeasured},
		{"another failure", envelopeText("failed", "null", `{"category":"brain","message":"the driver broke"}`), true, false, StopError, "the driver broke", CostMeasured},
		{"an unpriced model", `{"status":"done","usage":{"cost_source":"none","turns":1}}`, true, false, StopEnded, "", CostNone},
		{"no status", `{"usage":{}}`, false, false, "", "", ""},
		{"not JSON", "usage: af fix run ...", false, false, "", "", ""},
	} {
		out, failure, ok := parseEnvelope([]byte(tc.data))
		if ok != tc.ok || (ok && (out.Submitted != tc.submitted || out.StopReason != tc.stop || failure != tc.failure || out.CostSource != tc.source)) {
			t.Errorf("%s: %+v, %q, %v; want submitted %v, stop %s, failure %q, cost %s", tc.name, out, failure, ok, tc.submitted, tc.stop, tc.failure, tc.source)
		}
	}
}

func TestAgentkitInBox_WithoutAnEnvelopeTheClaimIsTheFileOfTheAttempt(t *testing.T) {
	w := newProcWorld(t, box.Request{Provider: "local", Env: map[string]string{VendorKeyVar: "sk-test"}}, nil)
	const nonce = "0011223344556677"
	for i, tc := range []struct {
		name             string
		script           fakeaf.Script
		nonce, made      string
		submitted, fails bool
	}{
		{"no run.json", fakeaf.Script{}, "", "-", false, false},
		{"a file of the attempt", fakeaf.Script{}, nonce, nonce, true, false},
		{"a file of another", fakeaf.Script{OutputJSON: "usage: af fix run"}, nonce, "ffffffffffffffff", false, false},
		{"a run.json with no nonce", fakeaf.Script{}, "", "", false, false},
		{"a failure, with a file", fakeaf.Script{Exit: 2}, nonce, nonce, true, false},
		{"a failure, and no file", fakeaf.Script{Exit: 2, StderrBytes: 5 << 10, Stderr: "TAIL-MARK"}, nonce, "", false, true},
	} {
		attempt := i + 1
		if tc.made != "-" {
			w.place(attempt, tc.nonce, tc.made)
		}
		w.script(tc.script)
		w.report = &recorder{}

		out, err := inBox(t, w, "").Run(context.Background(), w.processBrief(attempt))

		if out.Submitted != tc.submitted || out.CostUSD != 0 || out.CostSource != CostNone || (err != nil) != tc.fails || len(w.report.warned("brain_usage_unavailable")) != 1 {
			t.Errorf("%s: %+v, %v, warnings %+v; want the claim %v, no cost, one warning, failing %v", tc.name, out, err, w.report.warnings, tc.submitted, tc.fails)
		}
		if tc.fails && (!strings.Contains(err.Error(), "TAIL-MARK") || len(err.Error()) > stderrTail+300 || out.StopReason != StopError) {
			t.Errorf("%s: %v (%d bytes), want an error with the end of the errors", tc.name, err, len(err.Error()))
		}
	}
}

func TestProcessError_CarriesTheEndOfTheErrors(t *testing.T) {
	for _, tc := range []struct {
		res  ProcessResult
		want string
	}{
		{ProcessResult{ExitCode: 2, Stderr: []byte(strings.Repeat("x", 3<<10) + "END\n")}, "af run exited with code 2: "},
		{ProcessResult{ExitCode: box.ExitTimeout, TimedOut: true, Stderr: []byte("slow")}, "af run was killed when its time ran out: slow"},
	} {
		err := processError("af run", tc.res)
		if !strings.HasPrefix(err.Error(), tc.want) || len(err.Error()) > len(tc.want)+stderrTail || !strings.HasSuffix(err.Error(), strings.TrimSpace(string(tc.res.Stderr[max(0, len(tc.res.Stderr)-4):]))) {
			t.Errorf("processError = %q, want %q and the last 2 KiB of the errors", err, tc.want)
		}
	}
}
