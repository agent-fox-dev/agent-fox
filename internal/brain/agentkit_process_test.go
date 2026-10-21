package brain

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/provider/faux"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/local"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// inBoxBrain is the agentkit brain made for the provider as the command makes it, with
// the fake af.
func inBoxBrain(t *testing.T, w *procWorld, provider string, borrowed bool, model string, host map[string]string) (Brain, error) {
	t.Helper()
	return Default.New(AgentkitName, Options{
		Provider: provider, BorrowedHands: borrowed, Model: model, Getenv: hostWith(host), AFPath: w.af, AFVersion: "v1.0.0", AFCommit: "abc123",
	})
}

// envelopeJSON is the document af <agent> run prints.
func envelopeJSON(status string, usage string, result string, errObj string) string {
	return fmt.Sprintf(`{"schema_version":"5.0.0","tool":"fix run","status":%q,"usage":%s,"result":%s,"error":%s}`, status, usage, result, errObj)
}

const measuredUsage = `{"brain":"agentkit","cost_usd":0.42,"cost_source":"measured","input_tokens":1000,"output_tokens":200,"turns":5}`

// TestAgentkitInsideABoxRunsAfAgentRunWithOnlyTheVendorKey_TS_10_57 verifies TS-10-57:
// under landlock and podman, without --borrowed-hands, the agentkit brain runs
// <af> <agent> run with the run, the directory, the bounds and the timeout, AF_MODEL in
// the command and the vendor key in the spec of the box; and without the key it is not
// made.
// Verifies: 10-REQ-9.5
func TestAgentkitInsideABoxRunsAfAgentRunWithOnlyTheVendorKey_TS_10_57(t *testing.T) {
	key := map[string]string{"ANTHROPIC_API_KEY": "sk-test"}
	for _, provider := range []string{"landlock", "podman"} {
		t.Run(provider, func(t *testing.T) {
			w := newProcWorld(t, provider, key, nil)
			w.script(fakeScript{OutputJSON: envelopeJSON("done", measuredUsage, `{"submitted":true}`, "null")})
			brain, err := inBoxBrain(t, w, provider, false, "m1", key)
			if err != nil {
				t.Fatal(err)
			}

			_, err = brain.Run(context.Background(), w.processBrief(1, func(b *Brief) { b.MaxCostUSD = 2.5 }))
			if err != nil {
				t.Fatal(err)
			}

			var af []fakeRecord
			for _, r := range w.records() {
				if r.Program == ProgramAF && !slices.Equal(r.Argv, []string{"--version"}) {
					af = append(af, r)
				}
			}
			if len(af) != 1 {
				t.Fatalf("af ran %d times", len(af))
			}
			want := []string{"fix", "run", "--run-id", w.run.ID, "--dir", w.session.Root(), "--budget", "2.5", "--max-turns", "10"}
			if !slices.Equal(af[0].Argv, want) {
				t.Errorf("argv = %q, want %q", af[0].Argv, want)
			}
			seen := map[string]string{}
			for _, kv := range af[0].Env {
				name, value, _ := strings.Cut(kv, "=")
				seen[name] = value
			}
			if seen["AF_MODEL"] != "m1" || seen["ANTHROPIC_API_KEY"] != "sk-test" {
				t.Errorf("the process saw %v, want AF_MODEL=m1 and the vendor key", seen)
			}
			if got := w.session.Spec().Env; len(got) != 1 || got["ANTHROPIC_API_KEY"] != "sk-test" {
				t.Errorf("Spec().Env = %v, want {ANTHROPIC_API_KEY}", got)
			}
		})
	}

	// The timeout of the brief is in the command, and the model is left out when there is none.
	w := newProcWorld(t, "landlock", key, nil)
	w.script(fakeScript{OutputJSON: envelopeJSON("done", measuredUsage, `{"submitted":true}`, "null")})
	brain, _ := inBoxBrain(t, w, "landlock", false, "", key)
	b := w.processBrief(2)
	b.Timeout = 20 * time.Minute
	if _, err := brain.Run(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	run := w.runsOf(ProgramAF)[0]
	if !slices.Contains(run.Argv, "--timeout") || run.Argv[slices.Index(run.Argv, "--timeout")+1] != "20m0s" {
		t.Errorf("argv = %q, want --timeout 20m0s", run.Argv)
	}
	for _, kv := range run.Env {
		if strings.HasPrefix(kv, "AF_MODEL=") {
			t.Errorf("the process saw %s with no model", kv)
		}
	}

	// Without the key the brain is not made, as failed/auth, whatever the placement.
	for _, borrowed := range []bool{false} {
		b, err := Default.New(AgentkitName, Options{Provider: "landlock", BorrowedHands: borrowed, Getenv: hostWith(nil)})
		f := asFailure(t, err)
		if b != nil || f.Category != toolio.CategoryAuth || f.FixHint == nil || f.FixHint.Env != "ANTHROPIC_API_KEY" {
			t.Errorf("without the key: %v, %+v, want failed/auth with the variable", b, f)
		}
	}
}

// TestTheEnvelopeOfTheDriverMapsToTheOutcome_TS_10_59 verifies TS-10-59: the cost, the
// tokens and the turns come from the usage of the envelope with the source measured; the
// claim is result.submitted; a failure of the category budget is budget or turns by the
// flag of its hint, and any other failure is error.
// Verifies: 10-REQ-9.7
func TestTheEnvelopeOfTheDriverMapsToTheOutcome_TS_10_59(t *testing.T) {
	key := map[string]string{"ANTHROPIC_API_KEY": "sk-test"}
	budgetErr := func(flag string) string {
		return fmt.Sprintf(`{"category":"budget","message":"used the bound","retryable":false,"fix_hint":{"flag":%q}}`, flag)
	}
	for _, tc := range []struct {
		name, output string
		exit         int
		stop         string
		submitted    bool
		wantErr      bool
	}{
		{"success", envelopeJSON("done", measuredUsage, `{"submitted":true}`, "null"), 0, StopEnded, true, false},
		{"success without a claim", envelopeJSON("done", measuredUsage, `{"submitted":false}`, "null"), 0, StopEnded, false, false},
		{"the cost bound", envelopeJSON("failed", measuredUsage, "null", budgetErr("--budget")), 1, StopBudget, false, false},
		{"the turn bound", envelopeJSON("failed", measuredUsage, "null", budgetErr("--max-turns")), 1, StopTurns, false, false},
		{"another failure", envelopeJSON("failed", measuredUsage, "null", `{"category":"brain","message":"the driver broke","retryable":true,"fix_hint":null}`), 1, StopError, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newProcWorld(t, "landlock", key, nil)
			w.script(fakeScript{OutputJSON: tc.output, Exit: tc.exit})
			brain, err := inBoxBrain(t, w, "landlock", false, "", key)
			if err != nil {
				t.Fatal(err)
			}

			out, err := brain.Run(context.Background(), w.processBrief(1))

			if (err != nil) != tc.wantErr || (err != nil && !strings.Contains(err.Error(), "the driver broke")) {
				t.Errorf("error = %v, want one: %v, carrying the message of the envelope", err, tc.wantErr)
			}
			if out.CostUSD != 0.42 || out.CostSource != CostMeasured || out.Turns != 5 || out.InputTokens != 1000 || out.OutputTokens != 200 ||
				out.StopReason != tc.stop || out.Submitted != tc.submitted {
				t.Errorf("outcome = %+v, want the measured usage of the envelope, stop %s, submitted %v", out, tc.stop, tc.submitted)
			}
		})
	}
}

// TestAMissingOrUnparseableEnvelopeIsCostZeroAndOneWarning_TS_10_54 verifies TS-10-54 for
// the driver inside a box: an envelope that is not there, or is not JSON, is cost 0, cost
// source none and one brain_usage_unavailable warning, and the submission is decided
// from the file.
// Verifies: 10-REQ-9.2
func TestAMissingOrUnparseableEnvelopeIsCostZeroAndOneWarning_TS_10_54(t *testing.T) {
	key := map[string]string{"ANTHROPIC_API_KEY": "sk-test"}
	for name, output := range map[string]string{"no envelope": "", "not json": "usage: af fix run ..."} {
		t.Run(name, func(t *testing.T) {
			w := newProcWorld(t, "landlock", key, nil)
			w.script(fakeScript{OutputJSON: output})
			driver, err := inBoxBrain(t, w, "landlock", false, "", key)
			if err != nil {
				t.Fatal(err)
			}
			submitted := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
				target := submission.Target{Tree: b.Box.FS(ctx), Write: b.Box.WriteFile, RunID: b.RunID}
				if _, err := submission.Submit(ctx, target, b.Kind, []byte(`{"summary":"made"}`), time.Now()); err != nil {
					return Outcome{}, err
				}
				return driver.Run(ctx, b)
			})
			req := w.request(w.brief(1), submitted)
			req.Name = AgentkitName

			res, err := Execute(context.Background(), req)

			if err != nil || len(res.Payload) == 0 || !res.Outcome.Submitted {
				t.Fatalf("Execute = %+v, %v, want the submission decided from the file, and the claim to be the file's", res, err)
			}
			if res.Outcome.CostUSD != 0 || res.Outcome.CostSource != CostNone {
				t.Errorf("outcome = %+v, want cost 0 and source none", res.Outcome)
			}
			if n := len(w.warned("brain_usage_unavailable")); n != 1 {
				t.Errorf("%d brain_usage_unavailable warnings, want one", n)
			}
		})
	}
}

// TestAgentkitStaysOnTheHostOverTheBoxsToolsWithNoEnvUnderLocalNoneAndBorrowedHands_TS_10_60
// verifies TS-10-60: under local, under none and under landlock with --borrowed-hands the
// driver runs on the host over Session.Tools, no process is run through Session.Exec, and
// the spec of the box has no environment.
// Verifies: 10-REQ-9.8
func TestAgentkitStaysOnTheHostOverTheBoxsToolsWithNoEnvUnderLocalNoneAndBorrowedHands_TS_10_60(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-host")
	entry, err := Default.Lookup(AgentkitName)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		provider string
		borrowed bool
	}{{"local", false}, {"none", false}, {"landlock", true}, {"podman", true}} {
		t.Run(fmt.Sprintf("%s/borrowed=%v", tc.provider, tc.borrowed), func(t *testing.T) {
			providerEntry, _ := box.Default.Lookup(tc.provider)
			dir := newRepo(t)
			rd := newRunDir(t, dir)
			reg := box.Default
			req := BoxRequest(BoxParams{
				Brain: entry, Provider: providerEntry, BorrowedHands: tc.borrowed, Tools: []string{"read_file", "list_files"}, Getenv: os.LookupEnv,
				HostDir: dir, RunID: rd.ID, Tool: "fix", Record: rd.Record, Warn: func(string, string) {},
			})
			// The provider that is not one of the real ones is the local one under its name.
			if tc.provider == "landlock" || tc.provider == "podman" {
				r := box.NewRegistry()
				r.Register(box.Entry{Name: tc.provider, New: func() sandbox.Provider { return standIn{Provider: local.Unisolated(), name: tc.provider} }})
				reg = r
			}
			req.Registry, req.Image = reg, ""
			sess, err := box.Open(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sess.Close() })
			w := &world{t: t, dir: dir, run: rd, session: sess, report: &recorder{}}
			p := faux.New(
				withUsage(call("c1", "list_files", `{"path":"."}`), usage(0.01, 10, 2)),
				withUsage(call("c2", "submit_test", `{"summary":"hosted","cite":"README.md"}`), usage(0.01, 10, 2)),
			)
			brain, err := Default.New(AgentkitName, Options{
				Provider: tc.provider, BorrowedHands: tc.borrowed, Model: "faux-1", Getenv: noEnv,
				NewProvider: func(string) (core.ProviderClient, error) { return p, nil },
			})
			if err != nil {
				t.Fatal(err)
			}

			res, err := runFaux(t, w, brain, nil)

			if err != nil || !res.Outcome.Submitted {
				t.Fatalf("run = %+v, %v", res, err)
			}
			if len(sess.Spec().Env) != 0 {
				t.Errorf("Spec().Env = %v, want it empty", sess.Spec().Env)
			}
			kinds, _ := w.ledger()
			for _, k := range kinds {
				if k == "box_probe" || k == "brain_process" {
					t.Errorf("the ledger has %s: a process was run through Session.Exec", k)
				}
			}
			if len(p.Requests()) != 2 {
				t.Errorf("%d requests to the model, want 2: the driver ran on the host", len(p.Requests()))
			}
		})
	}
}

// TestAnEnvelopeOfAnUnpricedModelHasNoCostSource guards the cost source of the envelope: a
// driver that could not price its model says none, and the brain says so.
func TestAnEnvelopeOfAnUnpricedModelHasNoCostSource(t *testing.T) {
	key := map[string]string{"ANTHROPIC_API_KEY": "sk-test"}
	w := newProcWorld(t, "landlock", key, nil)
	unpriced := `{"brain":"agentkit","cost_usd":0,"cost_source":"none","input_tokens":10,"output_tokens":2,"turns":1}`
	w.script(fakeScript{OutputJSON: envelopeJSON("done", unpriced, `{"submitted":true}`, "null")})
	brain, err := inBoxBrain(t, w, "landlock", false, "", key)
	if err != nil {
		t.Fatal(err)
	}

	out, err := brain.Run(context.Background(), w.processBrief(1))

	if err != nil || out.CostSource != CostNone || out.CostUSD != 0 || out.Turns != 1 {
		t.Errorf("outcome = %+v, %v, want the usage of the envelope with the source none", out, err)
	}
}

// TestAnEnvelopeThatCannotBeReadAndAFailingExitIsAnErrorWithTheEndOfTheErrors guards the
// driver in a box that ends badly and says nothing: no submission, an exit code, and no
// envelope is an error that carries the end of its errors; one that submitted is not.
func TestAnEnvelopeThatCannotBeReadAndAFailingExitIsAnErrorWithTheEndOfTheErrors(t *testing.T) {
	key := map[string]string{"ANTHROPIC_API_KEY": "sk-test"}
	w := newProcWorld(t, "landlock", key, nil)
	w.script(fakeScript{Exit: 2, StderrBytes: 5 << 10, Stderr: "TAIL-MARK"})
	w.placeSubmission(1, "")
	brain, err := inBoxBrain(t, w, "landlock", false, "", key)
	if err != nil {
		t.Fatal(err)
	}

	out, err := brain.Run(context.Background(), w.processBrief(1))

	if err == nil || !strings.Contains(err.Error(), "TAIL-MARK") || len(err.Error()) > 2<<10+400 || out.StopReason != StopError {
		t.Errorf("Run = %+v, %v, want an error with the last 2 KiB of the errors", out, err)
	}

	w2 := newProcWorld(t, "landlock", key, nil)
	w2.script(fakeScript{Exit: 2, Stderr: "TAIL-MARK"})
	w2.placeSubmission(1, placedNonce)
	brain2, _ := inBoxBrain(t, w2, "landlock", false, "", key)
	if out, err := brain2.Run(context.Background(), w2.processBrief(1)); err != nil || !out.Submitted {
		t.Errorf("with a submission: %+v, %v, want no error", out, err)
	}
}
