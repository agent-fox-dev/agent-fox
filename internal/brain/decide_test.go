package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// submitIn submits the payload from inside the run, as a brain does: through the
// submit core against the session.
func submitIn(ctx context.Context, b Brief, payload string) error {
	_, err := submission.Submit(ctx, submission.Target{Tree: b.Box.FS(ctx), Write: b.Box.WriteFile, RunID: b.RunID}, b.Kind, []byte(payload), time.Now())
	return err
}

// runJSON is run.json of the run, as Execute placed it in the box.
func runJSON(t *testing.T, b Brief) submission.Run {
	t.Helper()
	data, err := fs.ReadFile(b.Box.FS(context.Background()), submission.RunPath(b.RunID))
	if err != nil {
		t.Fatal(err)
	}
	var run submission.Run
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatal(err)
	}
	return run
}

// writeFileIn writes the box's submission.json directly, the way a brain with
// its own hands might, with the fields of the run and the changes.
func writeFileIn(t *testing.T, b Brief, change func(*submission.File)) {
	t.Helper()
	run := runJSON(t, b)
	payload := `{"summary":"direct"}`
	file := submission.File{Kind: run.Kind, RunID: run.RunID, Attempt: run.Attempt, Nonce: run.Nonce, SubmittedAt: "2026-10-20T14:00:00Z", PayloadSHA256: sha(payload), Payload: json.RawMessage(payload)}
	change(&file)
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Box.WriteFile(context.Background(), submission.SubmissionPath(b.RunID), data); err != nil {
		t.Fatal(err)
	}
}

// stubs for the outcomes of a brain.
func submits(payload string) stubBrain {
	return func(ctx context.Context, b Brief) (Outcome, error) {
		if err := submitIn(ctx, b, payload); err != nil {
			return Outcome{}, err
		}
		return Outcome{Submitted: true, StopReason: StopSubmitted}, nil
	}
}

func (w *world) hostSubmission() (string, bool) {
	data, err := os.ReadFile(filepath.Join(w.run.Path, "submission.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	if err != nil {
		w.t.Fatal(err)
	}
	return string(data), true
}

func (r *recorder) warned(code string) []toolio.Warning {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []toolio.Warning
	for _, w := range r.warnings {
		if w.Code == code {
			out = append(out, w)
		}
	}
	return out
}

func (w *world) ledgerHas(kind string) bool {
	kinds, _ := w.ledger()
	return slices.Contains(kinds, kind)
}

// TestAClaimWithoutAFileIsNoSubmission_TS_07_47 verifies TS-07-47:
// a brain that claims Submitted with no valid file yields no payload.
// Verifies: 07-REQ-6.8
func TestAClaimWithoutAFileIsNoSubmission_TS_07_47(t *testing.T) {
	w := newWorld(t)
	brain := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
		// A transcript with a plausible payload, which nobody may read.
		if err := os.WriteFile(b.TranscriptPath, []byte(`{"submit":{"summary":"looks like a payload"}}`), 0o000); err != nil {
			t.Error(err)
		}
		return Outcome{Submitted: true, StopReason: StopSubmitted}, nil
	})
	t.Cleanup(func() { os.Chmod(filepath.Join(w.run.Path, "transcript.1.jsonl"), 0o644) })

	res, err := Execute(context.Background(), w.request(w.brief(1), brain))

	if err == nil || res.Payload != nil || res.Path != "" {
		t.Errorf("Execute = %+v, %v; want no payload and a failure", res, err)
	}
	if f := asFailure(t, err); f.Category != CategoryNoResult {
		t.Errorf("failure = %+v, want no_result", f)
	}
	if _, ok := w.hostSubmission(); ok {
		t.Error("a submission file was written")
	}
}

// TestTheSubmissionIsReadAfterATimeout_TS_07_48 verifies TS-07-48:
// the submission is read after a timeout, with a fresh bounded context.
// Verifies: 07-REQ-7.1
func TestTheSubmissionIsReadAfterATimeout_TS_07_48(t *testing.T) {
	w := newWorld(t)
	b := w.brief(1)
	b.Timeout = 80 * time.Millisecond
	brain := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
		if err := submitIn(ctx, b, `{"summary":"in time"}`); err != nil {
			return Outcome{}, err
		}
		<-ctx.Done()
		return Outcome{Submitted: true}, ctx.Err()
	})

	res, err := Execute(context.Background(), w.request(b, brain))

	if err != nil || string(res.Payload) != `{"summary":"in time"}` {
		t.Fatalf("Execute = %+v, %v; want the submission although the run's context expired", res, err)
	}
	if len(w.report.warned("brain_error")) != 1 {
		t.Errorf("warnings = %+v, want brain_error for the deadline the brain returned", w.report.warnings)
	}
}

// TestAMatchingSubmissionIsAccepted_TS_07_49 verifies TS-07-49:
// a matching, re-validated submission is copied to the run directory and
// recorded as accepted.
// Verifies: 07-REQ-7.2
func TestAMatchingSubmissionIsAccepted_TS_07_49(t *testing.T) {
	w := newWorld(t)
	b := w.brief(1)

	res, err := Execute(context.Background(), w.request(b, submits(`{ "summary": "fixed", "files": ["src/main.go"] }`)))

	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(w.run.Path, "submission.json")
	if res.Path != want || string(res.Payload) != `{"files":["src/main.go"],"summary":"fixed"}` && string(res.Payload) != `{"summary":"fixed","files":["src/main.go"]}` {
		t.Errorf("Result = %+v, want the payload and %s", res, want)
	}
	boxed, err := w.boxFile(submission.SubmissionPath(w.run.ID))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := w.hostSubmission(); got != boxed {
		t.Errorf("the run directory's file is not the box's:\n%s\n%s", got, boxed)
	}
	if !slices.Contains(w.report.artifacts, toolio.Artifact{Kind: "submission", Path: want}) {
		t.Errorf("artifacts = %+v, want the submission", w.report.artifacts)
	}
	accepted := w.entry("submission_accepted")
	if accepted["sha256"] != sha(string(res.Payload)) {
		t.Errorf("submission_accepted = %v, want the hash of the payload %s", accepted, sha(string(res.Payload)))
	}
	entries, _ := os.ReadDir(w.run.Path)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp") {
			t.Errorf("a temporary file is left in the run directory: %s", e.Name())
		}
	}
}

// TestAnInvalidBoxFileIsNoSubmission_TS_07_50 verifies TS-07-50:
// an unparsable, mismatching or invalid box submission raises
// submission_invalid and counts as absent.
// Verifies: 07-REQ-7.3
func TestAnInvalidBoxFileIsNoSubmission_TS_07_50(t *testing.T) {
	refuses := testKind()
	refuses.Validate = func(_ context.Context, _ fs.FS, payload json.RawMessage) (json.RawMessage, []submission.Problem) {
		if bytes.Contains(payload, []byte(`"bad"`)) {
			return nil, []submission.Problem{{Path: "bad", Message: "is not allowed"}, {Path: "worse", Message: "is not either"}}
		}
		return payload, nil
	}
	for name, tc := range map[string]struct {
		change  func(*submission.File)
		raw     string
		mention string
	}{
		"broken JSON":   {raw: `{"kind":`, mention: "JSON"},
		"wrong kind":    {change: func(f *submission.File) { f.Kind = "other" }, mention: "kind"},
		"wrong run id":  {change: func(f *submission.File) { f.RunID = "20260101_000000_000000" }, mention: "run_id"},
		"wrong attempt": {change: func(f *submission.File) { f.Attempt = 9 }, mention: "attempt"},
		"wrong sha":     {change: func(f *submission.File) { f.PayloadSHA256 = sha("something else") }, mention: "payload_sha256"},
		"refused": {change: func(f *submission.File) {
			f.Payload = json.RawMessage(`{"bad":true}`)
			f.PayloadSHA256 = sha(`{"bad":true}`)
		}, mention: "is not allowed"},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			b := w.brief(1)
			b.Kind = refuses
			brain := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
				if tc.raw != "" {
					if err := b.Box.WriteFile(ctx, submission.SubmissionPath(b.RunID), []byte(tc.raw)); err != nil {
						return Outcome{}, err
					}
				} else {
					writeFileIn(t, b, tc.change)
				}
				return Outcome{Submitted: true, StopReason: StopSubmitted}, nil
			})

			res, err := Execute(context.Background(), w.request(b, brain))

			if f := asFailure(t, err); f.Category != CategoryNoResult || res.Payload != nil {
				t.Errorf("Execute = %+v, %v; want no_result and no payload", res, err)
			}
			warned := w.report.warned("submission_invalid")
			if len(warned) != 1 || !strings.Contains(warned[0].Message, tc.mention) {
				t.Errorf("warnings = %+v, want one submission_invalid that mentions %q", w.report.warnings, tc.mention)
			}
			if !w.ledgerHas("submission_invalid") {
				t.Error("the ledger has no submission_invalid")
			}
			if _, ok := w.hostSubmission(); ok {
				t.Error("a submission file was written")
			}
			if len(w.report.warned("submission_claim_mismatch")) != 1 {
				t.Errorf("the brain claimed a submission and the host found none, and said nothing: %+v", w.report.warnings)
			}
		})
	}
}

// TestAStaleOrMissingFile_TS_07_51 verifies TS-07-51:
// a stale nonce records submission_stale and a missing file records
// submission_absent.
// Verifies: 07-REQ-7.4
func TestAStaleOrMissingFile_TS_07_51(t *testing.T) {
	w := newWorld(t)
	first := w.request(w.brief(1), submits(`{"summary":"attempt one"}`))
	if _, err := Execute(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(w.run.Path, "submission.json")); err != nil {
		t.Fatal(err)
	}
	second := w.request(w.brief(2), stubBrain(func(context.Context, Brief) (Outcome, error) { return Outcome{StopReason: StopEnded}, nil }))
	second.Rand = bytes.NewReader([]byte{1, 2, 3, 4, 5, 6, 7, 8})

	_, err := Execute(context.Background(), second)

	if f := asFailure(t, err); f.Category != CategoryNoResult {
		t.Errorf("failure = %+v, want no_result", f)
	}
	if !w.ledgerHas("submission_stale") || w.ledgerHas("submission_absent") {
		kinds, _ := w.ledger()
		t.Errorf("ledger = %v, want submission_stale", kinds)
	}
	if _, ok := w.hostSubmission(); ok {
		t.Error("the stale file of attempt 1 was taken for attempt 2's")
	}

	empty := newWorld(t)
	_, err = Execute(context.Background(), empty.request(empty.brief(1), stubBrain(func(context.Context, Brief) (Outcome, error) { return Outcome{StopReason: StopEnded}, nil })))
	if f := asFailure(t, err); f.Category != CategoryNoResult || !empty.ledgerHas("submission_absent") || empty.ledgerHas("submission_stale") {
		kinds, _ := empty.ledger()
		t.Errorf("an empty box: %v, ledger %v, want submission_absent", err, kinds)
	}
}

// TestAClaimThatDiffers_TS_07_52 verifies TS-07-52:
// a claim that differs from the host's decision raises
// submission_claim_mismatch.
// Verifies: 07-REQ-7.5
func TestAClaimThatDiffers_TS_07_52(t *testing.T) {
	for name, tc := range map[string]struct {
		brain Brain
		want  int
	}{
		"claims, no file": {stubBrain(func(context.Context, Brief) (Outcome, error) { return Outcome{Submitted: true}, nil }), 1},
		"no claim, a file": {stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
			return Outcome{}, submitIn(ctx, b, `{"a":1}`)
		}), 1},
		"claims, a file": {submits(`{"a":1}`), 0},
		"neither":        {stubBrain(func(context.Context, Brief) (Outcome, error) { return Outcome{}, nil }), 0},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)

			Execute(context.Background(), w.request(w.brief(1), tc.brain))

			if got := len(w.report.warned("submission_claim_mismatch")); got != tc.want {
				t.Errorf("%d submission_claim_mismatch warnings, want %d (%+v)", got, tc.want, w.report.warnings)
			}
		})
	}
}

// TestATranscriptIsAnArtifactNotRead_TS_07_53 verifies TS-07-53:
// an existing transcript is added as an artifact without being read.
// Verifies: 07-REQ-7.6
func TestATranscriptIsAnArtifactNotRead_TS_07_53(t *testing.T) {
	w := newWorld(t)
	var path string
	with := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
		path = b.TranscriptPath
		return Outcome{}, os.WriteFile(path, []byte("not for af\n"), 0o000)
	})
	t.Cleanup(func() { os.Chmod(path, 0o644) })

	Execute(context.Background(), w.request(w.brief(1), with))

	want := toolio.Artifact{Kind: "transcript", Path: filepath.Join(w.run.Path, "transcript.1.jsonl")}
	if path != want.Path || !slices.Contains(w.report.artifacts, want) {
		t.Errorf("artifacts = %+v, want %+v", w.report.artifacts, want)
	}

	none := newWorld(t)
	Execute(context.Background(), none.request(none.brief(1), stubBrain(func(context.Context, Brief) (Outcome, error) { return Outcome{}, nil })))
	for _, a := range none.report.artifacts {
		if a.Kind == "transcript" {
			t.Errorf("a transcript artifact for a transcript that is not there: %+v", a)
		}
	}
}

// TestAValidSubmissionWins_TS_07_54 verifies TS-07-54:
// a valid submission wins over a brain error or a non-submitted stop reason.
// Verifies: 07-REQ-7.7
func TestAValidSubmissionWins_TS_07_54(t *testing.T) {
	w := newWorld(t)
	failing := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
		if err := submitIn(ctx, b, `{"summary":"done"}`); err != nil {
			return Outcome{}, err
		}
		return Outcome{Submitted: true, StopReason: StopError}, errors.New("the model went away after the submission")
	})

	res, err := Execute(context.Background(), w.request(w.brief(1), failing))

	if err != nil || string(res.Payload) != `{"summary":"done"}` {
		t.Fatalf("Execute = %+v, %v", res, err)
	}
	if warned := w.report.warned("brain_error"); len(warned) != 1 || !strings.Contains(warned[0].Message, "went away") {
		t.Errorf("warnings = %+v, want brain_error carrying the error", w.report.warnings)
	}

	w2 := newWorld(t)
	over := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
		return Outcome{Submitted: true, StopReason: StopBudget}, submitIn(ctx, b, `{"summary":"done"}`)
	})
	if res, err := Execute(context.Background(), w2.request(w2.brief(1), over)); err != nil || res.Payload == nil || len(w2.report.warned("brain_error")) != 0 {
		t.Errorf("a budget stop with a submission: %+v, %v, warnings %+v", res, err, w2.report.warnings)
	}
}

// TestTimeoutAndCancellation_TS_07_55 verifies TS-07-55:
// a timeout yields failed/timeout with a doubled --timeout hint, and
// cancellation returns the context error.
// Verifies: 07-REQ-7.8
func TestTimeoutAndCancellation_TS_07_55(t *testing.T) {
	blocks := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
		<-ctx.Done()
		return Outcome{}, ctx.Err()
	})

	w := newWorld(t)
	b := w.brief(1)
	b.Timeout = 40 * time.Millisecond
	_, err := Execute(context.Background(), w.request(b, blocks))
	f := asFailure(t, err)
	if f.Category != toolio.CategoryTimeout || f.FixHint == nil || f.FixHint.Flag != "--timeout" || f.FixHint.Suggest != "80ms" {
		t.Errorf("a brief timeout: %+v, want failed/timeout with --timeout 80ms", f)
	}

	w2 := newWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(40 * time.Millisecond); cancel() }()
	_, err = Execute(ctx, w2.request(w2.brief(1), blocks))
	var failure *toolio.Failure
	if !errors.Is(err, context.Canceled) || errors.As(err, &failure) {
		t.Errorf("a cancelled context: %v, want context.Canceled and no Failure", err)
	}
	if w2.report.lastUsage().Brain != "scripted" {
		t.Error("the usage of a cancelled run was not reported")
	}

	w3 := newWorld(t)
	ctx3, cancel3 := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel3()
	_, err = Execute(ctx3, w3.request(w3.brief(1), blocks))
	if !errors.Is(err, context.DeadlineExceeded) || errors.As(err, &failure) {
		t.Errorf("a passed deadline: %v, want context.DeadlineExceeded and no Failure", err)
	}
}

// TestWithoutASubmissionTheFailureIsInOrder_TS_07_56 verifies TS-07-56:
// without a submission the brain error, budget, turns and other stops map to
// their categories in order.
// Verifies: 07-REQ-7.8
func TestWithoutASubmissionTheFailureIsInOrder_TS_07_56(t *testing.T) {
	runErr := errors.New("the provider refused")
	for name, tc := range map[string]struct {
		outcome   Outcome
		err       error
		category  string
		retryable bool
		flag, sug string
	}{
		"a run error":           {Outcome{StopReason: StopEnded}, runErr, "brain", true, "", ""},
		"budget":                {Outcome{StopReason: StopBudget}, nil, "budget", false, "--budget", "3.0"},
		"turns":                 {Outcome{StopReason: StopTurns}, nil, "budget", false, "--max-turns", "20"},
		"ended":                 {Outcome{StopReason: StopEnded}, nil, "no_result", true, "", ""},
		"an error stop":         {Outcome{StopReason: StopError}, nil, "no_result", true, "", ""},
		"budget and an error":   {Outcome{StopReason: StopBudget}, runErr, "brain", true, "", ""},
		"no stop reason at all": {Outcome{}, nil, "no_result", true, "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			b := w.brief(1)
			b.MaxCostUSD, b.MaxTurns = 1.5, 10

			_, err := Execute(context.Background(), w.request(b, stubBrain(func(context.Context, Brief) (Outcome, error) { return tc.outcome, tc.err })))

			f := asFailure(t, err)
			if f.Category != tc.category || f.Retryable != tc.retryable {
				t.Errorf("failure = %+v, want %s, retryable %v", f, tc.category, tc.retryable)
			}
			switch {
			case tc.flag == "" && f.FixHint != nil:
				t.Errorf("a fix hint %+v that is not wanted", f.FixHint)
			case tc.flag != "" && (f.FixHint == nil || f.FixHint.Flag != tc.flag || f.FixHint.Suggest != tc.sug):
				t.Errorf("fix hint = %+v, want %s %s", f.FixHint, tc.flag, tc.sug)
			}
			if u := w.report.lastUsage(); u.Brain != "scripted" {
				t.Errorf("usage = %+v, want it reported", u)
			}
		})
	}
	_ = fmt.Sprint
}
