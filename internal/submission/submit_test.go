package submission

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestSubmit_WritesTheNormalisedPayloadWithTheAttemptAndNonceOfRunJSON(t *testing.T) {
	tr := newTree(t, true)
	sorted := validating(func(payload json.RawMessage) (json.RawMessage, []Problem) {
		v := testutil.Decode[map[string]any](t, payload)
		out, _ := json.MarshalIndent(v, "", "    ")
		return out, nil
	})

	receipt, err := tr.submit(sorted, "{\n  \"summary\": \"fixed\",\n  \"files\": [ \"a.go\", \"b.go\" ]\n}\n")

	if err != nil {
		t.Fatal(err)
	}
	want := File{Kind: "test", RunID: runID, Attempt: 2, Nonce: "abcdef0123456789", SubmittedAt: "2026-10-20T14:00:00Z",
		PayloadSHA256: testutil.SHA256(`{"files":["a.go","b.go"],"summary":"fixed"}`), Payload: json.RawMessage(`{"files":["a.go","b.go"],"summary":"fixed"}`)}
	if got := tr.readFile(); string(Marshal(got)) != string(Marshal(want)) {
		t.Errorf("the file holds %s, want %s", Marshal(got), Marshal(want))
	}
	if receipt != (Receipt{Path: submissionFile, SHA256: want.PayloadSHA256}) {
		t.Errorf("receipt = %+v", receipt)
	}
}

func TestSubmit_KeepsThePayloadAsItWasHashed(t *testing.T) {
	tr := newTree(t, true)
	raw := `{"html":"<b>a & b</b>","sep":"x` + " " + `y","uni":"ünï ☃","nested":{"k":[1,2.50,true,null]}}`

	if _, err := tr.submit(echoKind(), raw); err != nil {
		t.Fatal(err)
	}

	written := testutil.ReadFile(t, tr.dir+"/"+submissionFile)
	file := tr.readFile()
	if !strings.Contains(written, `"payload":`+raw+"}\n") || file.PayloadSHA256 != testutil.SHA256(raw) {
		t.Errorf("the file is %s, want the payload unescaped and its hash %s", written, testutil.SHA256(raw))
	}
	if got := string(Marshal(file)); got != written {
		t.Errorf("Marshal of the file read back is\n%s, want what Submit wrote\n%s", got, written)
	}
}

func TestSubmit_ARefusalNamesEveryProblemAtItsPathAndWritesNothing(t *testing.T) {
	for _, c := range []struct {
		name string
		run  bool
		kind Kind
		raw  string
		want string // the error
	}{
		{"every problem of the kind, sorted", true,
			refusing(Problem{"summary", "is empty"}, Problem{"files[2]", "b.go is not in the tree"}, Problem{"", "a whole"}, Problem{"files[0]", "a.go is not in the tree"}, Problem{"files[0]", "and more"}),
			`{"a":1}`, "the submission was refused, 5 problems: a whole; files[0]: a.go is not in the tree; files[0]: and more; files[2]: b.go is not in the tree; summary: is empty"},
		{"no run.json and a problem of the kind", false, refusing(Problem{"summary", "is empty"}),
			`{"a":1}`, "the submission was refused, 2 problems: " + runFile + ": run.json is missing: the loop places it before the brain runs, and a submission takes its attempt and nonce from it; summary: is empty"},
		{"a payload that is no object", true, echoKind(), `[1]`, "the submission was refused: the payload is a JSON array, and a submission is a single JSON object"},
		{"a kind that panics", true, panicking(), `{"a":1}`, "the submission was refused: the test kind could not check the payload: a string was a number"},
		{"a kind that normalises past the limit", true, validating(func(json.RawMessage) (json.RawMessage, []Problem) {
			return json.RawMessage(`{"a":"` + strings.Repeat("y", MaxPayloadBytes) + `"}`), nil
		}), `{"a":1}`, "the submission was refused: the payload is larger than the 1 MiB that a submission may be"},
	} {
		for _, prior := range []bool{false, true} {
			tr := newTree(t, true)
			if prior {
				if _, err := tr.submit(echoKind(), `{"first":true}`); err != nil {
					t.Fatal(err)
				}
			}
			if !c.run { // a run of no run.json
				tr.target.RunID = "20261020_140000_000000"
			}
			before := tr.files()

			_, err := Submit(context.Background(), tr.target, c.kind, []byte(c.raw), fixedNow.Add(time.Hour))

			if r := refused(t, err); r.Error() != strings.ReplaceAll(c.want, runID, tr.target.RunID) {
				t.Errorf("%s: %q, want %q", c.name, r.Error(), c.want)
			}
			if after := tr.files(); !maps.Equal(after, before) {
				t.Errorf("%s, prior file %v: the tree was %v, is %v", c.name, prior, before, after)
			}
		}
	}
}

func TestSubmit_RefusesARunJSONThatIsNotForThisRunAndKind(t *testing.T) {
	for content, says := range map[string]string{
		"{not json": "run.json is not valid JSON",
		`{"run_id":"20260101_000000_000000","kind":"test","attempt":1,"nonce":"n"}`: `run.json is for run "20260101_000000_000000", not for run "` + runID + `"`,
		`{"run_id":"` + runID + `","kind":"other","attempt":1,"nonce":"n"}`:         `run.json names the kind "other", and this is a "test" submission`,
		`{"run_id":"` + runID + `","kind":"test","attempt":0,"nonce":"n"}`:          "run.json has attempt 0, and attempts count from 1",
		`{"run_id":"` + runID + `","kind":"test","attempt":1}`:                      "run.json has no nonce",
		"a directory": "run.json cannot be read",
	} {
		tr := newTree(t, false)
		if content == "a directory" {
			testutil.WriteFile(t, tr.dir, runFile+"/x", "")
		} else {
			tr.writeRun(content)
		}

		_, err := tr.submit(echoKind(), `{"a":1}`)

		r := refused(t, err)
		if len(r.Problems) != 1 || r.Problems[0].Path != runFile || !strings.HasPrefix(r.Problems[0].Message, says) {
			t.Errorf("%s: problems %+v, want one at %s that says %q", content, r.Problems, runFile, says)
		}
	}
}

func TestSubmit_TheLastValidSubmissionWins(t *testing.T) {
	tr := newTree(t, true)

	r1, err1 := tr.submit(echoKind(), `{"v":"A"}`)
	r2, err2 := tr.submit(echoKind(), `{"v":"B"}`)

	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	if file := tr.readFile(); string(file.Payload) != `{"v":"B"}` || r2.SHA256 == r1.SHA256 || r2.SHA256 != file.PayloadSHA256 {
		t.Errorf("the file holds %s with %s, after the receipts %s and %s", file.Payload, file.PayloadSHA256, r1.SHA256, r2.SHA256)
	}
}

func TestSubmit_FailsWithoutARefusalWhenNothingCanBeWritten(t *testing.T) {
	tr := newTree(t, true)
	stopped, stop := context.WithCancel(context.Background())
	stop()
	noObject := validating(func(json.RawMessage) (json.RawMessage, []Problem) { return json.RawMessage(`[1]`), nil })
	for _, c := range []struct {
		name  string
		ctx   context.Context
		runID string
		kind  Kind
		write func(context.Context, string, []byte) error
		want  string
	}{
		{"a run id of no run", context.Background(), "a/b", echoKind(), nil, `submission: "a/b" is not a run id`},
		{"a run id of the parent", context.Background(), "..", echoKind(), nil, `submission: ".." is not a run id`},
		{"an empty run id", context.Background(), "", echoKind(), nil, `submission: "" is not a run id`},
		{"a kind that hands back no object", context.Background(), runID, noObject, nil, "submission: the test kind returned a payload that is not a JSON object"},
		{"a stopped context", stopped, runID, echoKind(), nil, "context canceled"},
		{"a write that fails", context.Background(), runID, echoKind(),
			func(context.Context, string, []byte) error { return errors.New("disk full") }, "submission: writing " + submissionFile + ": disk full"},
	} {
		target := tr.target
		target.RunID = c.runID
		if c.write != nil {
			target.Write = c.write
		}

		_, err := Submit(c.ctx, target, c.kind, []byte(`{"a":1}`), fixedNow)

		var r *Refused
		if err == nil || errors.As(err, &r) || err.Error() != c.want {
			t.Errorf("%s: %v, want the error %q", c.name, err, c.want)
		}
	}
	if files := tr.files(); len(files) != 0 {
		t.Errorf("the tree holds %v", files)
	}
}
