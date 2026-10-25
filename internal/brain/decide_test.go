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
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// forged is a submission file as text, with the fields of the run that was placed, the
// payload as given, the hash of hashed and more fields.
func forged(t *testing.T, b Brief, payload, hashed, more string) string {
	t.Helper()
	data, err := fs.ReadFile(b.Box.FS(context.Background()), submission.RunPath(b.RunID))
	if err != nil {
		t.Fatal(err)
	}
	run := testutil.Decode[submission.Run](t, data)
	return fmt.Sprintf(`{"kind":%q,"run_id":%q,"attempt":%d,"nonce":%q,"submitted_at":"2026-10-20T14:00:00Z","payload_sha256":%q,"payload":%s%s}`,
		run.Kind, run.RunID, run.Attempt, run.Nonce, testutil.SHA256(hashed), payload, more)
}

// writes is a brain that writes the box's submission.json itself, and claims it submitted.
func writes(t *testing.T, text func(b Brief) string) stubBrain {
	return func(ctx context.Context, b Brief) (Outcome, error) {
		if err := b.Box.WriteFile(ctx, submission.SubmissionPath(b.RunID), []byte(text(b))); err != nil {
			t.Error(err)
		}
		return Outcome{Submitted: true, StopReason: StopSubmitted}, nil
	}
}

func TestDecide_AcceptsTheSubmissionOfTheAttemptAsTheHostCheckedIt(t *testing.T) {
	w := newWorld(t)

	res, err := w.execute(context.Background(), w.brief(1), submits(`{ "summary": "fixed", "files": ["src/main.go"] }`))

	host := filepath.Join(w.run.Path, "submission.json")
	if err != nil || string(res.Payload) != `{"summary":"fixed","files":["src/main.go"]}` || res.Path != host || !res.Outcome.Submitted {
		t.Fatalf("Execute = %+v, %v, want the payload, compact, in %s", res, err, host)
	}
	boxed, _ := w.boxFile(submission.SubmissionPath(w.run.ID))
	if got, _ := w.hostSubmission(); got != boxed {
		t.Errorf("the host's file is not the box's:\n%s\n%s", got, boxed)
	}
	if !slices.Contains(w.report.artifacts, toolio.Artifact{Kind: "submission", Path: host}) || w.entry("submission_accepted")["sha256"] != testutil.SHA256(string(res.Payload)) {
		t.Errorf("artifacts %+v and the ledger %v, want the submission listed and its hash recorded", w.report.artifacts, w.kinds())
	}
	if len(w.report.warnings) != 0 {
		t.Errorf("warnings = %+v, want none", w.report.warnings)
	}
}

func TestDecide_KeepsOnlyWhatTheHostChecked(t *testing.T) {
	w := newWorld(t)
	const checked = `{"summary":"checked"}`
	// A second payload key that a decoder takes for the first (it ignores case, and the
	// last wins), a field of no submission file, and a layout of the brain's own.
	brain := writes(t, func(b Brief) string {
		return strings.ReplaceAll(forged(t, b, `{"summary":"UNCHECKED"}`, checked, `,"PAYLOAD":`+checked+`,"extra":"x"`), ",", ",\n\t")
	})

	res, err := w.execute(context.Background(), w.brief(1), brain)

	if err != nil || string(res.Payload) != checked {
		t.Fatalf("Execute = %+v, %v, want the payload that was checked", res, err)
	}
	host, _ := w.hostSubmission()
	file := testutil.Decode[submission.File](t, []byte(host))
	if host != string(submission.Marshal(file)) || strings.Contains(host, "UNCHECKED") || strings.Contains(host, "extra") {
		t.Errorf("the host's file holds more than what was checked:\n%s", host)
	}
}

func TestDecide_AFileThatDoesNotCountIsNoSubmission(t *testing.T) {
	refuses := testKind()
	refuses.Validate = func(_ context.Context, _ fs.FS, payload json.RawMessage) (json.RawMessage, []submission.Problem) {
		if bytes.Contains(payload, []byte(`"panic"`)) {
			panic("the kind did not expect this")
		}
		if bytes.Contains(payload, []byte(`"bad"`)) {
			return nil, []submission.Problem{{Path: "bad", Message: "is not allowed"}}
		}
		return payload, nil
	}
	big := `{"a":"` + strings.Repeat("x", submission.MaxPayloadBytes) + `"}`
	huge := `{"a":"` + strings.Repeat("x", submission.MaxFileBytes) + `"}`
	const ok = `{"summary":"direct"}`
	w := newWorld(t)
	for i, tc := range []struct {
		name, mention string
		text          func(b Brief) string
	}{
		{"not JSON", "not valid JSON", func(Brief) string { return `{"kind":` }},
		{"another kind", "kind is", func(b Brief) string { return strings.Replace(forged(t, b, ok, ok, ""), `"test"`, `"other"`, 1) }},
		{"another run", "run_id is", func(b Brief) string {
			return strings.Replace(forged(t, b, ok, ok, ""), b.RunID, "20260101_000000_000000", 1)
		}},
		{"another attempt", "attempt is 9", func(b Brief) string {
			return strings.Replace(forged(t, b, ok, ok, ""), fmt.Sprintf(`"attempt":%d`, b.Attempt), `"attempt":9`, 1)
		}},
		{"a hash of another payload", "payload_sha256", func(b Brief) string { return forged(t, b, ok, "else", "") }},
		{"a payload that is no object", "not a JSON object", func(b Brief) string { return forged(t, b, `[1]`, `[1]`, "") }},
		{"refused by the kind", "is not allowed", func(b Brief) string { return forged(t, b, `{"bad":1}`, `{"bad":1}`, "") }},
		{"a kind that panics", "could not check", func(b Brief) string { return forged(t, b, `{"panic":1}`, `{"panic":1}`, "") }},
		{"a payload over 1 MiB", "1 MiB", func(b Brief) string { return forged(t, b, big, big, "") }},
		{"a file over 2 MiB", "larger than", func(b Brief) string { return forged(t, b, huge, huge, "") }},
	} {
		b := w.brief(i + 1)
		b.Kind = refuses

		res, err := w.execute(context.Background(), b, writes(t, tc.text))

		if f := asFailure(t, err); f.Category != toolio.CategoryNoResult || res.Payload != nil {
			t.Errorf("%s: %+v, %v, want no_result and no payload", tc.name, res, err)
		}
		if got := w.report.warned("submission_invalid"); len(got) != 1 || !strings.Contains(got[0], tc.mention) {
			t.Errorf("%s: warnings = %+v, want one submission_invalid that says %q", tc.name, w.report.warnings, tc.mention)
		}
		if w.entry("submission_invalid")["attempt"] != float64(i+1) || len(w.report.warned("submission_claim_mismatch")) != 1 {
			t.Errorf("%s: the ledger %v and warnings %+v, want submission_invalid and the claim that differs", tc.name, w.kinds(), w.report.warnings)
		}
		if _, there := w.hostSubmission(); there {
			t.Errorf("%s: the run directory holds a submission", tc.name)
		}
	}

	// A file the box cannot read does not count either.
	_ = os.Remove(filepath.Join(w.session.Root(), submission.SubmissionPath(w.run.ID)))
	testutil.WriteFile(t, w.session.Root(), submission.SubmissionPath(w.run.ID)+"/x", "")
	_, _ = w.execute(context.Background(), w.brief(20), idle)
	if got := w.report.warned("submission_invalid"); len(got) != 1 || !strings.Contains(got[0], "could not be read") {
		t.Errorf("a submission.json that is a directory: warnings %+v", w.report.warnings)
	}
}

func TestDecide_TheFileOfAnotherAttemptIsStaleAndNoFileIsAbsent(t *testing.T) {
	w := newWorld(t)
	if _, err := w.execute(context.Background(), w.brief(1), submits(`{"summary":"one"}`)); err != nil {
		t.Fatal(err)
	}
	req := w.request(w.brief(2), idle)
	req.Rand = bytes.NewReader([]byte{1, 2, 3, 4, 5, 6, 7, 8})

	_, err := Execute(context.Background(), req)

	if f := asFailure(t, err); f.Category != toolio.CategoryNoResult || !f.Retryable || !strings.Contains(f.Message, "reason ended") {
		t.Errorf("failure = %+v, want no_result, retryable, with the reason of the stop", f)
	}
	if stale := w.entry("submission_stale"); stale["attempt"] != 2.0 || stale["file_attempt"] != 1.0 {
		t.Errorf("submission_stale = %v, want attempt 2 and the file of attempt 1", stale)
	}
	if _, there := w.hostSubmission(); there {
		t.Error("the submission of attempt 1 is still in the run directory of attempt 2, which failed")
	}

	empty := newWorld(t)
	_, _ = empty.execute(context.Background(), empty.brief(1), idle)
	if kinds := empty.kinds(); !slices.Contains(kinds, "submission_absent") || slices.Contains(kinds, "submission_stale") {
		t.Errorf("an empty box: ledger %v, want submission_absent", kinds)
	}
}

func TestDecide_WarnsWhenTheClaimAndTheFileDisagree(t *testing.T) {
	w := newWorld(t)
	for i, tc := range []struct {
		name  string
		brain Brain
		warns bool
	}{
		{"a claim and no file", stubBrain(func(context.Context, Brief) (Outcome, error) { return Outcome{Submitted: true}, nil }), true},
		{"a file and no claim", stubBrain(func(ctx context.Context, b Brief) (Outcome, error) { return Outcome{}, submitIn(ctx, b, `{"a":1}`) }), true},
		{"a claim and a file", submits(`{"a":1}`), false},
		{"neither", idle, false},
	} {
		_, _ = w.execute(context.Background(), w.brief(i+1), tc.brain)
		if got := len(w.report.warned("submission_claim_mismatch")); got != map[bool]int{true: 1}[tc.warns] {
			t.Errorf("%s: %d submission_claim_mismatch warnings (%+v)", tc.name, got, w.report.warnings)
		}
	}
}

func TestDecide_ListsATranscriptAsAnArtifactAndNeverReadsIt(t *testing.T) {
	w := newWorld(t)
	claims := stubBrain(func(_ context.Context, b Brief) (Outcome, error) {
		// A transcript with what looks like a payload, which af does not read.
		return Outcome{Submitted: true}, os.WriteFile(b.TranscriptPath, []byte(`{"submit":{"summary":"looks like a payload"}}`), 0o600)
	})

	res, err := w.execute(context.Background(), w.brief(1), claims)

	if f := asFailure(t, err); f.Category != toolio.CategoryNoResult || res.Payload != nil {
		t.Errorf("Execute = %+v, %v, want no submission", res, err)
	}
	if want := (toolio.Artifact{Kind: "transcript", Path: filepath.Join(w.run.Path, "transcript.1.jsonl")}); !slices.Contains(w.report.artifacts, want) {
		t.Errorf("artifacts = %+v, want %+v", w.report.artifacts, want)
	}
	_, _ = w.execute(context.Background(), w.brief(2), idle)
	if len(w.report.artifacts) != 0 {
		t.Errorf("artifacts = %+v, want none for a transcript that is not there", w.report.artifacts)
	}
}

func TestDecide_AValidSubmissionWinsOverTheWayTheBrainEnded(t *testing.T) {
	w := newWorld(t)
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i, tc := range []struct {
		name    string
		ctx     context.Context
		timeout time.Duration
		brain   stubBrain
		warning string
	}{
		{"an error", context.Background(), 0, func(ctx context.Context, b Brief) (Outcome, error) {
			return Outcome{Submitted: true, StopReason: StopError}, errors.Join(submitIn(ctx, b, `{"n":1}`), errors.New("the model went away"))
		}, "went away"},
		{"the budget", context.Background(), 0, func(ctx context.Context, b Brief) (Outcome, error) {
			return Outcome{Submitted: true, StopReason: StopBudget}, submitIn(ctx, b, `{"n":2}`)
		}, ""},
		{"the timeout of the brief", context.Background(), time.Second, func(ctx context.Context, b Brief) (Outcome, error) {
			err := submitIn(ctx, b, `{"n":3}`)
			<-ctx.Done()
			return Outcome{Submitted: err == nil}, errors.Join(err, ctx.Err())
		}, "deadline exceeded"},
		{"the caller's cancel", caller, 0, func(ctx context.Context, b Brief) (Outcome, error) {
			err := submitIn(ctx, b, `{"n":4}`)
			cancel()
			return Outcome{Submitted: err == nil}, errors.Join(err, ctx.Err())
		}, "canceled"},
	} {
		b := w.brief(i + 1)
		b.Timeout = tc.timeout

		res, err := w.execute(tc.ctx, b, tc.brain)

		if err != nil || string(res.Payload) != fmt.Sprintf(`{"n":%d}`, i+1) {
			t.Errorf("%s: Execute = %+v, %v, want the submission", tc.name, res, err)
		}
		if got := w.report.warned("brain_error"); (tc.warning == "") != (len(got) == 0) || (len(got) > 0 && !strings.Contains(got[0], tc.warning)) {
			t.Errorf("%s: warnings = %+v, want brain_error carrying %q, if anything", tc.name, w.report.warnings, tc.warning)
		}
	}
}

func TestDecide_WithoutASubmissionTheRunFailsWithTheFirstCause(t *testing.T) {
	blocks := func(ctx context.Context, b Brief) (Outcome, error) {
		<-ctx.Done()
		return Outcome{}, ctx.Err()
	}
	usage := &toolio.Failure{Category: toolio.CategoryUsage, Message: "a mistake of the caller's"}
	runErr := errors.New("the provider refused")
	w := newWorld(t)
	for i, tc := range []struct {
		name      string
		timeout   time.Duration
		cost      float64
		turns     int
		brain     stubBrain
		category  string // or the context's error, when it is one
		retryable bool
		hint      string // flag and suggestion
		mention   string
	}{
		{"the timeout of the brief", 40 * time.Millisecond, 1, 10, blocks, toolio.CategoryTimeout, false, "--timeout 80ms", "within the 40ms"},
		{"an error of the brain", 0, 1, 10, outcome(Outcome{StopReason: StopBudget}, runErr), toolio.CategoryBrain, true, "", "the provider refused"},
		{"a failure of the brain's own", 0, 1, 10, outcome(Outcome{}, usage), toolio.CategoryUsage, false, "", "a mistake"},
		{"the cost bound", 0, 1.5, 10, outcome(Outcome{StopReason: StopBudget}, nil), toolio.CategoryBudget, false, "--budget 3.0", "$1.5"},
		{"the cost bound, with none set", 0, 0, 10, outcome(Outcome{StopReason: StopBudget}, nil), toolio.CategoryBudget, false, "--budget ", "$0.0"},
		{"the turn bound", 0, 1, 10, outcome(Outcome{StopReason: StopTurns}, nil), toolio.CategoryBudget, false, "--max-turns 20", "its 10 turns"},
		{"the turn bound, with none set", 0, 1, 0, outcome(Outcome{StopReason: StopTurns}, nil), toolio.CategoryBudget, false, "--max-turns ", "its 0 turns"},
		{"an error stop", 0, 1, 10, outcome(Outcome{StopReason: StopError}, nil), toolio.CategoryNoResult, true, "", "reason error"},
		{"no stop reason", 0, 1, 10, outcome(Outcome{}, nil), toolio.CategoryNoResult, true, "", "with no reason given"},
	} {
		b := w.brief(i + 1)
		b.Timeout, b.MaxCostUSD, b.MaxTurns = tc.timeout, tc.cost, tc.turns

		_, err := w.execute(context.Background(), b, tc.brain)

		f := asFailure(t, err)
		hint := ""
		if f.FixHint != nil {
			hint = f.FixHint.Flag + " " + f.FixHint.Suggest
		}
		if f.Category != tc.category || f.Retryable != tc.retryable || hint != tc.hint || !strings.Contains(f.Message, tc.mention) {
			t.Errorf("%s: %+v (hint %q), want %s, retryable %v, hint %q, saying %q", tc.name, f, hint, tc.category, tc.retryable, tc.hint, tc.mention)
		}
		if w.report.lastUsage().Brain != ScriptedName {
			t.Errorf("%s: no usage was reported", tc.name)
		}
	}

	for _, ctxErr := range []error{context.Canceled, context.DeadlineExceeded} {
		ctx, cancel := context.WithCancel(context.Background())
		if ctxErr == context.DeadlineExceeded {
			ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
		}
		brain := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
			if ctxErr == context.Canceled {
				cancel()
			}
			return blocks(ctx, b)
		})
		_, err := w.execute(ctx, w.brief(20), brain)
		cancel()
		var f *toolio.Failure
		if !errors.Is(err, ctxErr) || errors.As(err, &f) {
			t.Errorf("the caller's context ended with %v: %v, want that error and no failure", ctxErr, err)
		}
	}
}

// outcome is a brain that returns the outcome and the error.
func outcome(o Outcome, err error) stubBrain {
	return func(context.Context, Brief) (Outcome, error) { return o, err }
}

func TestDecide_TheRunDirectoryHoldsNoSubmissionThatAfDidNotWrite(t *testing.T) {
	w := newWorld(t)
	host := filepath.Join(w.run.Path, "submission.json")
	forge := func() error { return os.WriteFile(host, []byte(`{"forged":true}`), 0o644) }

	// What the brain's own process wrote there, with nothing in the box.
	_, err := w.execute(context.Background(), w.brief(1), stubBrain(func(context.Context, Brief) (Outcome, error) {
		return Outcome{Submitted: true}, forge()
	}))
	if _, there := w.hostSubmission(); err == nil || there {
		t.Errorf("Execute = %v; the forged file is there: %v", err, there)
	}

	// What it wrote there after a valid submission, before the host looked.
	res, err := w.execute(context.Background(), w.brief(2), stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
		return Outcome{Submitted: true}, errors.Join(submitIn(ctx, b, `{"summary":"valid"}`), forge())
	}))
	if got, _ := w.hostSubmission(); err != nil || res.Path != host || !strings.Contains(got, `"payload":{"summary":"valid"}`) {
		t.Errorf("Execute = %+v, %v; the host's file = %s, want the one af wrote", res, err, got)
	}

	// A file that cannot be removed is a warning.
	_ = os.Remove(host)
	testutil.WriteFile(t, host, "x", "")
	_, _ = w.execute(context.Background(), w.brief(3), idle)
	if got := w.report.warned("submission_not_removed"); len(got) == 0 || !strings.Contains(got[0], host) {
		t.Errorf("warnings = %+v, want submission_not_removed that names %s", w.report.warnings, host)
	}
}
