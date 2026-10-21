package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/provider/faux"
)

// The tests of this file hold the run of a brief to what the host promises
// when the brain is not well behaved: a file it wrote itself, a panic, a
// context that is gone, a path spelled to get past the gate.

// rawSubmission is a submission file as text, with the fields of the run, the
// payload as given and the hash of hashed.
func rawSubmission(t *testing.T, b Brief, payload, hashed, extra string) string {
	t.Helper()
	run := runJSON(t, b)
	return fmt.Sprintf(`{"kind":%q,"run_id":%q,"attempt":%d,"nonce":%q,"submitted_at":"2026-10-20T14:00:00Z","payload_sha256":%q,"payload":%s%s}`,
		run.Kind, run.RunID, run.Attempt, run.Nonce, sha(hashed), payload, extra)
}

// putSubmission writes the box's submission.json as the brain's own hands would.
func putSubmission(t *testing.T, b Brief, text string) {
	t.Helper()
	if err := b.Box.WriteFile(context.Background(), submission.SubmissionPath(b.RunID), []byte(text)); err != nil {
		t.Fatal(err)
	}
}

func TestTheHostKeepsOnlyWhatItChecked(t *testing.T) {
	w := newWorld(t)
	// Two payload keys that a decoder takes for one (it ignores case, and the last
	// wins), and a field of no kind of file: the first payload was not checked.
	brain := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
		const checked = `{"summary":"checked"}`
		putSubmission(t, b, rawSubmission(t, b, `{"summary":"UNCHECKED"}`, checked, `,"PAYLOAD":`+checked+`,"extra":"x"`))
		return Outcome{Submitted: true, StopReason: StopSubmitted}, nil
	})

	res, err := Execute(context.Background(), w.request(w.brief(1), brain))

	if err != nil || string(res.Payload) != `{"summary":"checked"}` {
		t.Fatalf("Execute = %+v, %v; want the payload that was checked", res, err)
	}
	host, ok := w.hostSubmission()
	if !ok || strings.Contains(host, "UNCHECKED") || strings.Contains(host, "extra") || strings.Contains(host, "PAYLOAD") {
		t.Errorf("the file in the run directory holds more than what was checked:\n%s", host)
	}
}

func TestAForgedFileLargerThanASubmissionIsRefused(t *testing.T) {
	for name, size := range map[string]int{
		"a payload over 1 MiB in a file that is not over 2 MiB": submission.MaxPayloadBytes + 1024,
		"a file over 2 MiB": 3 << 20,
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			payload := `{"a":"` + strings.Repeat("x", size) + `"}`
			brain := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
				putSubmission(t, b, rawSubmission(t, b, payload, payload, ""))
				return Outcome{Submitted: true, StopReason: StopSubmitted}, nil
			})

			res, err := Execute(context.Background(), w.request(w.brief(1), brain))

			if f := asFailure(t, err); f.Category != toolio.CategoryNoResult || res.Payload != nil {
				t.Errorf("Execute = %+v, %v; want no payload and no_result", res, err)
			}
			if len(w.report.warned("submission_invalid")) != 1 {
				t.Errorf("warnings = %+v, want submission_invalid", w.report.warnings)
			}
			if _, ok := w.hostSubmission(); ok {
				t.Error("the run directory holds a submission that was refused")
			}
		})
	}
}

func TestAKindThatPanicsOnAForgedFileIsNoSubmission(t *testing.T) {
	w := newWorld(t)
	b := w.brief(1)
	b.Kind.Validate = func(context.Context, fs.FS, json.RawMessage) (json.RawMessage, []submission.Problem) {
		panic("the validator did not expect this")
	}
	brain := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
		const payload = `{"summary":42}`
		putSubmission(t, b, rawSubmission(t, b, payload, payload, ""))
		return Outcome{Submitted: true, StopReason: StopSubmitted}, nil
	})

	_, err := Execute(context.Background(), w.request(b, brain))

	if f := asFailure(t, err); f.Category != toolio.CategoryNoResult {
		t.Errorf("failure = %+v, want no_result", f)
	}
	if got := w.report.warned("submission_invalid"); len(got) != 1 || !strings.Contains(got[0].Message, "could not check") {
		t.Errorf("warnings = %+v, want submission_invalid that says the kind could not check", w.report.warnings)
	}
}

func TestAPanicAfterAValidSubmissionKeepsTheSubmission(t *testing.T) {
	w := newWorld(t)
	brain := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
		if err := submitIn(ctx, b, `{"summary":"done"}`); err != nil {
			t.Error(err)
		}
		panic("a bug after the submit")
	})

	res, err := Execute(context.Background(), w.request(w.brief(1), brain))

	if err != nil || string(res.Payload) != `{"summary":"done"}` {
		t.Fatalf("Execute = %+v, %v; want the submission that was valid", res, err)
	}
	if got := w.report.warned("brain_error"); len(got) != 1 || !strings.Contains(got[0].Message, "panicked") {
		t.Errorf("warnings = %+v, want brain_error that says the brain panicked", w.report.warnings)
	}
	if _, err := os.Stat(filepath.Join(w.run.Path, "brain-panic.1.txt")); err != nil {
		t.Errorf("the stack of the panic is not kept: %v", err)
	}
	if _, ok := w.hostSubmission(); !ok {
		t.Error("the submission was not copied to the run directory")
	}
}

func TestAPanicWithNoSubmissionIsABrainFailure(t *testing.T) {
	w := newWorld(t)

	_, err := Execute(context.Background(), w.request(w.brief(1), stubBrain(func(context.Context, Brief) (Outcome, error) { panic("boom") })))

	if f := asFailure(t, err); f.Category != toolio.CategoryBrain || f.Retryable || !strings.Contains(f.Message, "boom") {
		t.Errorf("failure = %+v, want brain, not retryable, with the panic", f)
	}
}

func TestARunDirectoryHoldsNoSubmissionThatAfDidNotAccept(t *testing.T) {
	hostFile := func(w *world) string { return filepath.Join(w.run.Path, "submission.json") }
	idle := stubBrain(func(context.Context, Brief) (Outcome, error) { return Outcome{StopReason: StopEnded}, nil })

	t.Run("one of an earlier attempt", func(t *testing.T) {
		w := newWorld(t)
		if _, err := Execute(context.Background(), w.request(w.brief(1), submits(`{"summary":"first"}`))); err != nil {
			t.Fatal(err)
		}
		if _, ok := w.hostSubmission(); !ok {
			t.Fatal("attempt 1 left no submission")
		}

		_, err := Execute(context.Background(), w.request(w.brief(2), idle))

		if f := asFailure(t, err); f.Category != toolio.CategoryNoResult {
			t.Errorf("failure = %+v, want no_result", f)
		}
		if _, ok := w.hostSubmission(); ok {
			t.Error("attempt 2 failed and the run directory still holds the submission of attempt 1")
		}
	})
	t.Run("one that the brain's own process wrote", func(t *testing.T) {
		w := newWorld(t)
		brain := stubBrain(func(context.Context, Brief) (Outcome, error) {
			if err := os.WriteFile(hostFile(w), []byte(`{"forged":true}`), 0o644); err != nil {
				t.Error(err)
			}
			return Outcome{Submitted: true, StopReason: StopSubmitted}, nil
		})

		_, err := Execute(context.Background(), w.request(w.brief(1), brain))

		if err == nil {
			t.Fatal("Execute accepted a run with no submission in the box")
		}
		if _, ok := w.hostSubmission(); ok {
			t.Error("the file the brain wrote in the run directory is still there")
		}
	})
	t.Run("one that the brain's process overwrote", func(t *testing.T) {
		w := newWorld(t)
		brain := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
			if err := submitIn(ctx, b, `{"summary":"valid"}`); err != nil {
				return Outcome{}, err
			}
			// What a process of the brain does at once, before the host has looked.
			if err := os.WriteFile(hostFile(w), []byte(`{"forged":true}`), 0o644); err != nil {
				t.Error(err)
			}
			return Outcome{Submitted: true, StopReason: StopSubmitted}, nil
		})

		res, err := Execute(context.Background(), w.request(w.brief(1), brain))

		if err != nil {
			t.Fatal(err)
		}
		if host, _ := w.hostSubmission(); !strings.Contains(host, `"payload":{"summary":"valid"}`) || strings.Contains(host, "forged") || res.Path != hostFile(w) {
			t.Errorf("the run directory's file = %s, want the one af wrote from what it checked", host)
		}
	})
}

func TestTheBrainIsGivenTheHookThatWasPlaced(t *testing.T) {
	w := newWorld(t)
	b := w.brief(1)
	b.Hook = HookConfig{RunID: w.run.ID, ProtectedPaths: []string{".specs/"}}
	var got HookConfig
	brain := stubBrain(func(_ context.Context, b Brief) (Outcome, error) {
		got = b.Hook
		return Outcome{}, nil
	})

	_, _ = Execute(context.Background(), w.request(b, brain))

	if len(got.ProtectedPaths) != 2 || got.ProtectedPaths[1] != ".agent-fox/" {
		t.Errorf("Brief.Hook.ProtectedPaths = %v, want .specs/ and .agent-fox/, as hook.json has", got.ProtectedPaths)
	}
}

func TestACancelledContextIsTheContextError(t *testing.T) {
	w := newWorld(t)
	stale := filepath.Join(w.run.Path, "submission.json")
	if err := os.WriteFile(stale, []byte(`{"stale":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Execute(ctx, w.request(w.brief(1), stubBrain(func(context.Context, Brief) (Outcome, error) {
		t.Error("the brain ran in a cancelled run")
		return Outcome{}, nil
	})))

	var f *toolio.Failure
	if !errors.Is(err, context.Canceled) || errors.As(err, &f) {
		t.Errorf("error = %v, want context.Canceled and no Failure (not a sandbox failure of a box that could not be written)", err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("the submission of an earlier attempt is still in the run directory of an attempt that never ran")
	}
}

func TestASubmissionIsReadWhenTheCallersContextWasCancelled(t *testing.T) {
	w := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	brain := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
		if err := submitIn(ctx, b, `{"summary":"before the cancel"}`); err != nil {
			return Outcome{}, err
		}
		cancel()
		return Outcome{Submitted: true}, ctx.Err()
	})

	res, err := Execute(ctx, w.request(w.brief(1), brain))

	if err != nil || string(res.Payload) != `{"summary":"before the cancel"}` {
		t.Errorf("Execute = %+v, %v; want the submission that was in the box when the context was cancelled", res, err)
	}
}

func TestTallySpendReportsEachTotalInTurn(t *testing.T) {
	var tally Tally
	var totals []float64 // appended under the Tally's lock
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tally.Spend(Outcome{CostUSD: 1, CostSource: CostMeasured, Turns: 1}, "x", func(u toolio.Usage) { totals = append(totals, u.CostUSD) })
		}()
	}
	wg.Wait()

	for i := 1; i < len(totals); i++ {
		if totals[i] <= totals[i-1] {
			t.Fatalf("the totals reported are %v: a smaller one came after a larger", totals)
		}
	}
	if len(totals) != 64 || totals[63] != 64 {
		t.Errorf("%d totals, the last %v, want 64 and 64", len(totals), totals[len(totals)-1])
	}
}

// heartbeatPanics is a Report whose heartbeat panics.
type heartbeatPanics struct{ *recorder }

func (r heartbeatPanics) Emit(typ string, data any) {
	if typ == "heartbeat" {
		panic("the report is broken")
	}
	r.recorder.Emit(typ, data)
}

func TestAReportThatPanicsInTheHeartbeatDoesNotTakeTheRunDown(t *testing.T) {
	w := newWorld(t)
	req := w.request(w.brief(1), stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
		time.Sleep(100 * time.Millisecond)
		return Outcome{}, nil
	}))
	req.Report = heartbeatPanics{w.report}
	req.Heartbeat = 5 * time.Millisecond

	_, err := Execute(context.Background(), req)

	if f := asFailure(t, err); f.Category != toolio.CategoryNoResult {
		t.Errorf("failure = %+v, want no_result for a brain that submitted nothing", f)
	}
}

func TestAClosedTranscriptThatCannotBeClosedIsAWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	tr, err := NewTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.f.Close(); err != nil { // as a file that was lost under the brain
		t.Fatal(err)
	}
	rec := &recorder{}

	closeTranscript(tr, Brief{TranscriptPath: path, Warn: rec.Warn})

	if got := rec.warned("transcript_unwritable"); len(got) != 1 || !strings.Contains(got[0].Message, path) {
		t.Errorf("warnings = %+v, want transcript_unwritable that names the file", rec.warnings)
	}
}

func TestTheGateJudgesThePathTheToolWillUse(t *testing.T) {
	w := newWorld(t)
	g, err := NewGate(GateConfig{
		Brain: "scripted", Allowed: []string{"write_file", "edit_file"}, Terminating: "submit_test",
		Hook: HookConfig{ProtectedPaths: []string{".agent-fox/"}}, Dir: w.session.Spec().Workdir, Box: w.session.Tools(),
	})
	if err != nil {
		t.Fatal(err)
	}
	run := ".agent-fox/run/" + w.run.ID + "/run.json"
	for name, path := range map[string]string{
		"plain":                       run,
		"spaces around it":            "  " + run + " ",
		"a newline after it":          run + "\n",
		"an @ before it":              "@" + run,
		"double quotes":               `"` + run + `"`,
		"single quotes":               "'" + run + "'",
		"a file URL":                  "file://" + filepath.Join(w.session.Spec().Workdir, run),
		"quotes and an @ and a space": ` @"` + run + `"`,
	} {
		for _, tool := range []string{"write_file", "edit_file"} {
			if v := g.Decide(tool, map[string]any{"path": path}); v.Decision != DecisionDeny {
				t.Errorf("%s with the path %s (%q): %+v, want denied", tool, name, path, v)
			}
		}
	}
	if v := g.Decide("write_file", map[string]any{"path": " src/ok.go "}); v.Decision != DecisionAllow {
		t.Errorf("a path outside .agent-fox/ with spaces: %+v, want allowed", v)
	}
}

func TestTheGateRefusesAKindWhoseToolHasABoxToolsName(t *testing.T) {
	w := newWorld(t)

	_, err := NewGate(GateConfig{Brain: "scripted", Allowed: []string{"read_file"}, Terminating: "read_file", Box: w.session.Tools()})

	if err == nil || !strings.Contains(err.Error(), "read_file") {
		t.Errorf("NewGate = %v, want an error that names the clash", err)
	}
}

func TestAGuardPolicyThatCannotBeUsedIsAConfigFailure(t *testing.T) {
	w := newWorld(t)
	if err := os.MkdirAll(filepath.Join(w.dir, ".agent-fox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.dir, ".agent-fox", "guard.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runScript(t, w, `{"steps":[]}`, nil)

	f := asFailure(t, err)
	if f.Category != toolio.CategoryConfig || f.Retryable || !strings.Contains(f.Message, "guard.json") {
		t.Errorf("failure = %+v, want config, not retryable, naming guard.json", f)
	}
}

func TestAScriptWithTextAfterItsObjectIsRefused(t *testing.T) {
	_, err := Default.New(ScriptedName, Options{Script: writeScript(t, `{"steps":[]} trailing`)})

	if f := asFailure(t, err); f.Category != toolio.CategoryUsage {
		t.Errorf("failure = %+v, want usage", f)
	}
}

func TestBuildBoxEnvReadsTheProcessEnvironmentWhenGivenNoLookup(t *testing.T) {
	t.Setenv("AF_TEST_BOX_ENV", "value")

	env := BuildBoxEnv(Entry{Credentials: []string{"AF_TEST_BOX_ENV", "AF_TEST_BOX_ENV_UNSET"}}, PlacementBox, nil)

	if len(env) != 1 || env["AF_TEST_BOX_ENV"] != "value" {
		t.Errorf("env = %v, want only the variable that is set", env)
	}
}

func TestAModelTheCatalogDoesNotListHasNoCostToBound(t *testing.T) {
	w := newWorld(t)
	getenv := func(name string) (string, bool) { return "key-for-the-test", name == "ANTHROPIC_API_KEY" }
	brain, err := Default.New(AgentkitName, Options{Model: "a-model-nobody-listed", Getenv: getenv})
	if err != nil {
		t.Fatal(err)
	}

	_, err = runFaux(t, w, brain, func(b *Brief) { b.MaxCostUSD = 2 })

	f := asFailure(t, err)
	if f.Category != toolio.CategoryUsage || f.FixHint == nil || f.FixHint.Env != "AF_MODEL" || !strings.Contains(f.Message, "a-model-nobody-listed") {
		t.Errorf("failure = %+v, want a usage failure that says the cost bound cannot be enforced and names AF_MODEL", f)
	}
}

func TestAnUnpricedModelWithNoBoundReportsNoCost(t *testing.T) {
	w := newWorld(t)
	p := faux.New(withUsage(call("c1", "submit_test", `{"summary":"done"}`), usage(0.5, 1000, 100)))
	brain := &agentkitBrain{model: "faux-1", provider: p, priced: false}

	res, err := runFaux(t, w, brain, func(b *Brief) { b.MaxCostUSD = 0 })

	if err != nil || !res.Outcome.Submitted {
		t.Fatalf("Execute = %+v, %v", res, err)
	}
	if u := w.report.lastUsage(); u.CostUSD != 0 || u.CostSource != CostNone {
		t.Errorf("usage = %+v, want no cost and source none for a model with no price", u)
	}
	if len(w.report.warned("model_unpriced")) != 1 {
		t.Errorf("warnings = %+v, want model_unpriced", w.report.warnings)
	}
}

func TestTwoSubmitsInOneTurnAreTakenInOrder(t *testing.T) {
	two := func(first, second string) faux.Turn {
		return faux.FauxAssistantMessage(core.StopReasonToolUse,
			faux.FauxToolCall("c1", "submit_test", first), faux.FauxToolCall("c2", "submit_test", second))
	}
	for name, tc := range map[string]struct {
		first, second string
		want          string
		lines         int
	}{
		"a refusal and then an acceptance": {`{"summary":"a","cite":"missing.go"}`, `{"summary":"b","cite":"README.md"}`, `{"summary":"b","cite":"README.md"}`, 2},
		"two acceptances":                  {`{"summary":"one"}`, `{"summary":"two"}`, `{"summary":"one"}`, 1},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			brain, p := fauxBrain(t, two(tc.first, tc.second))

			res, err := runFaux(t, w, brain, nil)

			if err != nil || string(res.Payload) != tc.want && !strings.Contains(string(res.Payload), tc.want[1:len(tc.want)-1]) {
				t.Fatalf("Execute = %+v, %v; want the payload %s", res, err, tc.want)
			}
			if p.Calls() != 1 {
				t.Errorf("%d requests to the provider, want 1: the run ends with the acceptance", p.Calls())
			}
			lines := w.transcript()
			if len(lines) < tc.lines {
				t.Fatalf("the transcript has %d lines, want at least %d: %v", len(lines), tc.lines, lines)
			}
			if tc.lines == 2 {
				if got := fmt.Sprint(lines[0]["result"]); !strings.Contains(got, "not in the tree") || strings.Contains(got, "Rsync") {
					t.Errorf("the refusal that came first reads %q, want the problem of the payload and no error of the box", got)
				}
			}
		})
	}
}

func TestTheModelIsNotToldOfTheToolsItMayNotUse(t *testing.T) {
	w := newWorld(t)
	brain, p := fauxBrain(t, faux.Text("nothing to do"))

	_, _ = runFaux(t, w, brain, func(b *Brief) { b.Tools = []string{"read_file"} })

	reqs := p.Requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(reqs))
	}
	var system strings.Builder
	for _, block := range reqs[0].System {
		if text, ok := block.(core.TextBlock); ok {
			system.WriteString(text.Text)
		}
	}
	// The guideline of an allowed tool may name another (read_file's names
	// search_files); what the stubs would add is what must not be there.
	for _, hidden := range []string{"write_file", "edit_file", "run_command"} {
		if strings.Contains(system.String(), hidden) {
			t.Errorf("the system prompt names %s, which the brief does not allow:\n%s", hidden, system.String())
		}
	}
}
