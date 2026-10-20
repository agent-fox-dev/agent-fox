package submission

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

// panicKind is a kind whose check assumes a string where the payload has a number.
func panicKind() Kind {
	k := echoKind()
	k.Validate = func(_ context.Context, _ fs.FS, payload json.RawMessage) (json.RawMessage, []Problem) {
		var v struct{ Summary any }
		_ = json.Unmarshal(payload, &v)
		_ = v.Summary.(string)
		return payload, nil
	}
	return k
}

func TestSubmitRefusesAPayloadThatIsNotUTF8(t *testing.T) {
	tr := newTree(t, "test")

	_, err := Submit(context.Background(), tr.target, echoKind(), []byte("{\"a\":\"\xff\xfe\"}"), fixedNow)

	if r := refused(t, err); !strings.Contains(r.Error(), "UTF-8") {
		t.Errorf("refusal = %v, want one that names UTF-8", r)
	}
	if _, ok := tr.submission(); ok {
		t.Error("a submission file was written")
	}
}

func TestAKindThatPanicsRefusesTheSubmission(t *testing.T) {
	tr := newTree(t, "test")

	_, err := Submit(context.Background(), tr.target, panicKind(), []byte(`{"summary":42}`), fixedNow)

	if r := refused(t, err); !strings.Contains(r.Error(), "could not check") {
		t.Errorf("refusal = %v, want one that says the kind could not check the payload", r)
	}
	if _, ok := tr.submission(); ok {
		t.Error("a submission file was written")
	}
}

func TestCheckHoldsAPayloadToTheShapeAndTheKindWithoutARun(t *testing.T) {
	ctx := context.Background()
	tree := fstest.MapFS{}
	for name, tc := range map[string]struct {
		kind Kind
		raw  string
		want string // part of the one problem; "" for a payload that is accepted
	}{
		"an object":          {echoKind(), ` {"a": 1} `, ""},
		"not UTF-8":          {echoKind(), "{\"a\":\"\xff\"}", "UTF-8"},
		"over 1 MiB":         {echoKind(), `{"a":"` + strings.Repeat("x", MaxPayloadBytes) + `"}`, "1 MiB"},
		"an array":           {echoKind(), `[1]`, "object"},
		"a kind that panics": {panicKind(), `{"summary":42}`, "could not check"},
		"a kind that says":   {problemsKind(Problem{Path: "cite", Message: "no such file"}), `{"a":1}`, "no such file"},
	} {
		t.Run(name, func(t *testing.T) {
			payload, problems := Check(ctx, tc.kind, tree, []byte(tc.raw))

			if tc.want == "" {
				if len(problems) != 0 || string(payload) != `{"a":1}` {
					t.Errorf("Check = %s, %v, want the compact payload", payload, problems)
				}
				return
			}
			if payload != nil || len(problems) != 1 || !strings.Contains(problems[0].String(), tc.want) {
				t.Errorf("Check = %s, %v, want one problem that mentions %q", payload, problems, tc.want)
			}
		})
	}
}

func TestCheckRefusesWhatTheKindNormalisesPastTheLimit(t *testing.T) {
	grow := echoKind()
	grow.Validate = func(context.Context, fs.FS, json.RawMessage) (json.RawMessage, []Problem) {
		return json.RawMessage(`{"a":"` + strings.Repeat("y", MaxPayloadBytes) + `"}`), nil
	}

	payload, problems := Check(context.Background(), grow, fstest.MapFS{}, []byte(`{"a":1}`))

	if payload != nil || len(problems) != 1 || !strings.Contains(problems[0].Message, "1 MiB") {
		t.Errorf("Check = %d bytes, %v, want one problem about 1 MiB", len(payload), problems)
	}
}

func TestMarshalIsTheLayoutSubmitWrites(t *testing.T) {
	tr := newTree(t, "test")
	payload := "{\"a\":\"line separator\",\"b\":[1,2]}"
	if _, err := Submit(context.Background(), tr.target, echoKind(), []byte(payload), fixedNow); err != nil {
		t.Fatal(err)
	}
	written, ok := tr.submission()
	if !ok {
		t.Fatal("no submission file")
	}
	var file File
	if err := json.Unmarshal(written, &file); err != nil {
		t.Fatal(err)
	}

	if got := Marshal(file); string(got) != string(written) {
		t.Errorf("Marshal of the parsed file differs from the file:\n%s\n%s", got, written)
	}
	if file.PayloadSHA256 != PayloadSHA256(file.Payload) {
		t.Errorf("payload_sha256 = %s, want the hash of the payload", file.PayloadSHA256)
	}
}

func TestSubmitRefusesARunIDThatNamesAPath(t *testing.T) {
	tr := newTree(t, "test")
	for _, id := range []string{"", ".", "..", "a/b"} {
		target := tr.target
		target.RunID = id

		_, err := Submit(context.Background(), target, echoKind(), []byte(`{"a":1}`), fixedNow)

		if err == nil || !strings.Contains(err.Error(), "not a run id") {
			t.Errorf("run id %q: error = %v, want 'not a run id'", id, err)
		}
	}
	if _, err := os.Stat(tr.dir + "/.agent-fox/submission.json"); err == nil {
		t.Error("a submission was written outside the run directory")
	}
}
