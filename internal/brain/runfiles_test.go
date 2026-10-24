package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/submission"
)

// brainSubmitting is a brain that submits a valid payload for the attempt n through the
// submit core.
func brainSubmitting(n int) stubBrain {
	return func(ctx context.Context, b Brief) (Outcome, error) {
		payload := fmt.Sprintf(`{"attempt_number":%d,"cite":"README.md"}`, n)
		target := submission.Target{Tree: b.Box.FS(ctx), Write: b.Box.WriteFile, RunID: b.RunID}
		if _, err := submission.Submit(ctx, target, b.Kind, []byte(payload), time.Now()); err != nil {
			return Outcome{StopReason: StopError}, err
		}
		return Outcome{Submitted: true, StopReason: StopSubmitted}, nil
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

// TestTheHostWritesWhatItCheckedAndNotWhatTheBrainLeftInTheFile guards 10-REQ-5.6 against a
// brain that is more than af's submit core: the file the host keeps is written from what the
// host checked, so a field the brain added and the layout it gave the file do not stay.
func TestTheHostWritesWhatItCheckedAndNotWhatTheBrainLeftInTheFile(t *testing.T) {
	w := newWorld(t)
	brain := stubBrain(func(ctx context.Context, b Brief) (Outcome, error) {
		out, err := brainSubmitting(1)(ctx, b)
		if err != nil {
			return out, err
		}
		path := submission.SubmissionPath(b.RunID)
		data, err := fs.ReadFile(b.Box.FS(ctx), path)
		if err != nil {
			return out, err
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			return out, err
		}
		doc["smuggled"] = "a field af did not write"
		spread, _ := json.MarshalIndent(doc, "\t", "      ")
		return out, b.Box.WriteFile(ctx, path, spread)
	})

	res, err := Execute(context.Background(), w.request(w.brief(1), brain))

	if err != nil || !res.Outcome.Submitted {
		t.Fatalf("Execute = %+v, %v", res, err)
	}
	got, ok := hostFile(t, w)
	if !ok {
		t.Fatal("no submission.json")
	}
	var file submission.File
	if err := json.Unmarshal(got, &file); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, submission.Marshal(file)) || bytes.Contains(got, []byte("smuggled")) {
		t.Errorf("submission.json = %q, want the file as the host writes what it accepted, without the field of the brain", got)
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

			res, err := Execute(context.Background(), w.request(b, brainSubmitting(3)))

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
	req := w.request(w.brief(1), brainSubmitting(1))
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
	shortReq := short.request(short.brief(1), brainSubmitting(1))
	shortReq.Nonce = "01234567"
	if res, err := Execute(context.Background(), shortReq); err != nil || !res.Outcome.Submitted {
		t.Errorf("a nonce of 8 hex digits: %+v, %v", res, err)
	}

	for _, bad := range []string{"not hex!", "../x", strings.Repeat("a", 65), "ABCDEF12"} {
		w := newWorld(t)
		req := w.request(w.brief(1), brainSubmitting(1))
		req.Nonce = bad
		if _, err := Execute(context.Background(), req); err == nil {
			t.Errorf("the nonce %q was adopted", bad)
		}
		if _, err := w.boxFile(submission.RunPath(w.run.ID)); err == nil {
			t.Errorf("the nonce %q was refused after run.json was placed", bad)
		}
	}
}
