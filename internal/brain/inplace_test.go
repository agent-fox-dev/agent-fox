package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// newNoneWorld is a repository, a run directory in it and a box of the none provider
// over it: the box-side run directory and the host's are one.
func newNoneWorld(t *testing.T) *world {
	t.Helper()
	dir := newRepo(t)
	rd, err := toolio.NewRunDir(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := box.Open(context.Background(), box.Request{
		Provider: "none", HostDir: dir, RunID: rd.ID, Tool: "fix", Record: rd.Record, Warn: func(string, string) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &world{t: t, dir: dir, run: rd, session: s, report: &recorder{}}
}

// The things a brain can do with its attempt.
type attempt int

const (
	acceptOne       attempt = iota // submit a valid payload
	writeInvalid                   // leave a file that is no submission
	writeWrongNonce                // leave a well-formed file with another attempt's nonce
	writeNothing                   // leave no file
)

func (a attempt) String() string {
	return [...]string{"accept", "reject-invalid", "reject-wrong-nonce", "no-file"}[a]
}

// brainDoing is a brain that does what the attempt says to the submission file.
func brainDoing(a attempt, n int) stubBrain {
	return func(ctx context.Context, b Brief) (Outcome, error) {
		payload := fmt.Sprintf(`{"attempt_number":%d,"cite":"README.md"}`, n)
		path := submission.SubmissionPath(b.RunID)
		switch a {
		case acceptOne:
			target := submission.Target{Tree: b.Box.FS(ctx), Write: b.Box.WriteFile, RunID: b.RunID}
			if _, err := submission.Submit(ctx, target, b.Kind, []byte(payload), time.Now()); err != nil {
				return Outcome{StopReason: StopError}, err
			}
			return Outcome{Submitted: true, StopReason: StopSubmitted}, nil
		case writeInvalid:
			return Outcome{StopReason: StopEnded}, b.Box.WriteFile(ctx, path, []byte("{\"not a submission"))
		case writeWrongNonce:
			compact := []byte(payload)
			file := submission.File{
				Kind: b.Kind.Name, RunID: b.RunID, Attempt: b.Attempt, Nonce: "ffffffffffffffff", SubmittedAt: "2026-10-21T00:00:00Z",
				PayloadSHA256: submission.PayloadSHA256(compact), Payload: compact,
			}
			return Outcome{StopReason: StopEnded}, b.Box.WriteFile(ctx, path, submission.Marshal(file))
		}
		return Outcome{StopReason: StopEnded}, nil
	}
}

// hostFile is the submission.json of the run directory, and whether there is one.
func hostFile(t *testing.T, w *world) ([]byte, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(w.run.Path, "submission.json"))
	if os.IsNotExist(err) {
		return nil, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return data, true
}

// TestAnAcceptedSubmissionIsRewrittenWithTheBytesTheHostAccepted_TS_10_29 verifies TS-10-29:
// under an in-place session, with a submission that the host accepted before, an
// attempt that submits is accepted and the file holds what the host accepted of it.
// Verifies: 10-REQ-5.6
func TestAnAcceptedSubmissionIsRewrittenWithTheBytesTheHostAccepted_TS_10_29(t *testing.T) {
	w := newNoneWorld(t)
	accepted := []byte(`{"the":"bytes of an earlier acceptance"}`)
	if err := os.WriteFile(filepath.Join(w.run.Path, "submission.json"), accepted, 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Execute(context.Background(), w.request(w.brief(2), brainDoing(acceptOne, 2)))

	if err != nil || !res.Outcome.Submitted {
		t.Fatalf("Execute = %+v, %v", res, err)
	}
	got, ok := hostFile(t, w)
	if !ok || bytes.Equal(got, accepted) {
		t.Fatalf("submission.json = %q, %v; want what this attempt submitted", got, ok)
	}
	var file submission.File
	if err := json.Unmarshal(got, &file); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, submission.Marshal(file)) || string(file.Payload) != `{"attempt_number":2,"cite":"README.md"}` || file.Attempt != 2 {
		t.Errorf("submission.json = %s, want the file as the host writes what it accepted, for attempt 2", got)
	}
	if string(res.Payload) != string(file.Payload) || res.Path != filepath.Join(w.run.Path, "submission.json") {
		t.Errorf("result = %s at %s, want the payload of the file", res.Payload, res.Path)
	}
	if matches, _ := filepath.Glob(filepath.Join(w.run.Path, "submission.rejected.*")); len(matches) != 0 {
		t.Errorf("an accepted submission left %v", matches)
	}
}

// TestARejectedSubmissionIsMovedAsideAndThePreviousBytesAreRestored_TS_10_30 verifies
// TS-10-30: a file that is no submission is moved to submission.rejected.<attempt>.json
// when it differs from the bytes kept, and the kept bytes come back, or no file is
// left when none were kept; a file identical to the kept bytes makes no rejected file.
// Verifies: 10-REQ-5.7
func TestARejectedSubmissionIsMovedAsideAndThePreviousBytesAreRestored_TS_10_30(t *testing.T) {
	for _, kind := range []attempt{writeInvalid, writeWrongNonce} {
		t.Run("with an earlier submission, "+kind.String(), func(t *testing.T) {
			w := newNoneWorld(t)
			first, err := Execute(context.Background(), w.request(w.brief(1), brainDoing(acceptOne, 1)))
			if err != nil {
				t.Fatal(err)
			}
			kept, _ := hostFile(t, w)

			res, err := Execute(context.Background(), w.request(w.brief(2), brainDoing(kind, 2)))

			if err == nil || res.Outcome.Submitted {
				t.Fatalf("Execute = %+v, %v, want a failure: there is no submission", res, err)
			}
			if got, ok := hostFile(t, w); !ok || !bytes.Equal(got, kept) {
				t.Errorf("submission.json = %q, %v; want the bytes of attempt 1 (%s)", got, ok, first.Payload)
			}
			rejected, err := os.ReadFile(filepath.Join(w.run.Path, "submission.rejected.2.json"))
			if err != nil || len(rejected) == 0 || bytes.Equal(rejected, kept) {
				t.Errorf("submission.rejected.2.json = %q, %v; want the file the brain left", rejected, err)
			}
		})
		t.Run("with no earlier submission, "+kind.String(), func(t *testing.T) {
			w := newNoneWorld(t)

			_, err := Execute(context.Background(), w.request(w.brief(1), brainDoing(kind, 1)))

			if err == nil {
				t.Fatal("Execute succeeded")
			}
			if got, ok := hostFile(t, w); ok {
				t.Errorf("submission.json = %q, want none: the host accepted nothing", got)
			}
			if _, err := os.Stat(filepath.Join(w.run.Path, "submission.rejected.1.json")); err != nil {
				t.Errorf("the rejected file was not kept: %v", err)
			}
		})
	}

	t.Run("a brain that leaves the file as it was", func(t *testing.T) {
		w := newNoneWorld(t)
		if _, err := Execute(context.Background(), w.request(w.brief(1), brainDoing(acceptOne, 1))); err != nil {
			t.Fatal(err)
		}
		kept, _ := hostFile(t, w)

		_, err := Execute(context.Background(), w.request(w.brief(2), brainDoing(writeNothing, 2)))

		if err == nil {
			t.Fatal("Execute succeeded")
		}
		if got, _ := hostFile(t, w); !bytes.Equal(got, kept) {
			t.Errorf("submission.json = %q, want the kept bytes", got)
		}
		if matches, _ := filepath.Glob(filepath.Join(w.run.Path, "submission.rejected.*")); len(matches) != 0 {
			t.Errorf("an unchanged file made %v", matches)
		}
	})

	t.Run("a brain that writes the kept bytes again", func(t *testing.T) {
		w := newNoneWorld(t)
		if _, err := Execute(context.Background(), w.request(w.brief(1), brainDoing(acceptOne, 1))); err != nil {
			t.Fatal(err)
		}
		kept, _ := hostFile(t, w)
		rewrite := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
			return Outcome{StopReason: StopEnded}, b.Box.WriteFile(ctx, submission.SubmissionPath(b.RunID), kept)
		})

		_, err := Execute(context.Background(), w.request(w.brief(2), rewrite))

		if err == nil {
			t.Fatal("Execute succeeded: the file is attempt 1's")
		}
		if got, _ := hostFile(t, w); !bytes.Equal(got, kept) {
			t.Errorf("submission.json = %q, want the kept bytes", got)
		}
		if matches, _ := filepath.Glob(filepath.Join(w.run.Path, "submission.rejected.*")); len(matches) != 0 {
			t.Errorf("an identical rewrite made %v", matches)
		}
	})

	t.Run("a brain that removes the file", func(t *testing.T) {
		w := newNoneWorld(t)
		if _, err := Execute(context.Background(), w.request(w.brief(1), brainDoing(acceptOne, 1))); err != nil {
			t.Fatal(err)
		}
		kept, _ := hostFile(t, w)
		remove := stubBrain(func(context.Context, Brief) (Outcome, error) {
			return Outcome{StopReason: StopEnded}, os.Remove(filepath.Join(w.run.Path, "submission.json"))
		})

		_, _ = Execute(context.Background(), w.request(w.brief(2), remove))

		if got, ok := hostFile(t, w); !ok || !bytes.Equal(got, kept) {
			t.Errorf("submission.json = %q, %v, want the kept bytes put back", got, ok)
		}
	})
}

// TestSubmissionJSONOnlyHoldsWhatTheHostAccepted_TS_10_31 verifies TS-10-31: for any
// sequence of attempts that are accepted, rejected for what they leave, or leave
// nothing, the file is absent or holds the latest submission the host accepted, after
// every attempt, under an in-place session; and the decisions are those of the same
// sequence under local. (Under local the file is the accepted one of the attempt
// or none, since an attempt starts by clearing it; under none a rejected attempt puts
// the earlier acceptance back.)
// Verifies: 10-REQ-5.8
func TestSubmissionJSONOnlyHoldsWhatTheHostAccepted_TS_10_31(t *testing.T) {
	rng := rand.New(rand.NewPCG(10, 31))
	for i := range 25 {
		var steps []attempt
		for range 1 + rng.IntN(7) {
			steps = append(steps, attempt(rng.IntN(4)))
		}
		inPlace, local := newNoneWorld(t), newWorld(t)
		var latest []byte
		for n, step := range steps {
			var decided [2]bool
			var files [2][]byte
			for k, w := range []*world{inPlace, local} {
				res, err := Execute(context.Background(), w.request(w.brief(n+1), brainDoing(step, n+1)))
				decided[k] = err == nil && res.Outcome.Submitted
				files[k], _ = hostFile(t, w)
			}
			if decided[0] != decided[1] || decided[0] != (step == acceptOne) {
				t.Fatalf("#%d %v, attempt %d (%s): accepted in place: %v, under local: %v", i, steps, n+1, step, decided[0], decided[1])
			}
			if step == acceptOne {
				latest = files[0]
				for k, file := range files {
					if !strings.Contains(string(file), fmt.Sprintf(`"attempt_number":%d`, n+1)) {
						t.Fatalf("#%d %v, attempt %d: the file of world %d is %s, want what this attempt submitted", i, steps, n+1, k, file)
					}
				}
			}
			if !bytes.Equal(files[0], latest) {
				t.Fatalf("#%d %v, attempt %d (%s): submission.json = %q, want %q: the latest the host accepted, or none", i, steps, n+1, step, files[0], latest)
			}
			if (files[1] != nil) != (step == acceptOne) {
				t.Fatalf("#%d %v, attempt %d (%s): under local the file is %q", i, steps, n+1, step, files[1])
			}
		}
	}
}

// TestRunJSONCarriesThePrefixBytesAndTheSubmitCoreIgnoresThem_TS_10_58 verifies the part
// of TS-10-58 that is Execute's: run.json holds prefix_bytes, the byte length of the
// prefix of the brief, beside the run, the kind, the attempt and the nonce, and a
// submission is accepted all the same.
// Verifies: 10-REQ-9.6
func TestRunJSONCarriesThePrefixBytesAndTheSubmitCoreIgnoresThem_TS_10_58(t *testing.T) {
	for name, prefix := range map[string]string{"ascii": "PREFIX: the run-constant part\n", "multibyte": "PREFIX é☃🙂\n", "none": ""} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			b := w.brief(3)
			b.Prefix = prefix

			res, err := Execute(context.Background(), w.request(b, brainDoing(acceptOne, 3)))

			if err != nil || !res.Outcome.Submitted {
				t.Fatalf("Execute = %+v, %v: the submit core must accept a submission with the extra field in run.json", res, err)
			}
			data, err := w.boxFile(submission.RunPath(w.run.ID))
			if err != nil {
				t.Fatal(err)
			}
			var run map[string]any
			if err := json.Unmarshal([]byte(data), &run); err != nil {
				t.Fatal(err)
			}
			if run["prefix_bytes"] != float64(len(prefix)) || run["run_id"] != w.run.ID || run["kind"] != "test" || run["attempt"] != float64(3) || run["nonce"] == "" {
				t.Errorf("run.json = %v, want prefix_bytes %d beside the run, the kind, the attempt and the nonce", run, len(prefix))
			}
		})
	}
}

// TestAnAdoptedNonceIsTheNonceOfTheRun guards Request.Nonce: a run that was given the
// nonce of a run.json already placed puts that nonce in the run.json it places, so
// that a submission made under it counts for the process that placed the first; a
// nonce that is not hex is refused, and nothing is placed.
func TestAnAdoptedNonceIsTheNonceOfTheRun(t *testing.T) {
	w := newWorld(t)
	req := w.request(w.brief(1), brainDoing(acceptOne, 1))
	req.Nonce = "0123456789abcdef"

	res, err := Execute(context.Background(), req)

	if err != nil || !res.Outcome.Submitted {
		t.Fatalf("Execute = %+v, %v", res, err)
	}
	var file submission.File
	got, _ := hostFile(t, w)
	if err := json.Unmarshal(got, &file); err != nil || file.Nonce != "0123456789abcdef" {
		t.Errorf("submission.json = %s, %v, want it made under the adopted nonce", got, err)
	}
	var placed submission.Run
	data, _ := w.boxFile(submission.RunPath(w.run.ID))
	if err := json.Unmarshal([]byte(data), &placed); err != nil || placed.Nonce != "0123456789abcdef" {
		t.Errorf("run.json = %s, %v, want the adopted nonce", data, err)
	}

	short := newWorld(t)
	shortReq := short.request(short.brief(1), brainDoing(acceptOne, 1))
	shortReq.Nonce = "01234567"
	if res, err := Execute(context.Background(), shortReq); err != nil || !res.Outcome.Submitted {
		t.Errorf("a nonce of 8 hex digits: %+v, %v", res, err)
	}

	for _, bad := range []string{"not hex!", "../x", strings.Repeat("a", 65), "ABCDEF12"} {
		w := newWorld(t)
		req := w.request(w.brief(1), brainDoing(acceptOne, 1))
		req.Nonce = bad
		if _, err := Execute(context.Background(), req); err == nil {
			t.Errorf("the nonce %q was adopted", bad)
		}
		if _, err := w.boxFile(submission.RunPath(w.run.ID)); err == nil {
			t.Errorf("the nonce %q was refused after run.json was placed", bad)
		}
	}
}
