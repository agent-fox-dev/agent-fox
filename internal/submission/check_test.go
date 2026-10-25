package submission

import (
	"context"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestCheck_HoldsAPayloadToTheShapeAndTheKind(t *testing.T) {
	exact := `{"a":"` + strings.Repeat("x", MaxPayloadBytes-8) + `"}`
	plain := echoKind()
	plain.Validate = nil
	for name, c := range map[string]struct {
		kind Kind
		raw  string
		want string // the payload accepted, or the one problem
		ok   bool
	}{
		"an object, compacted":             {echoKind(), " {\"a\": 1}\n ", `{"a":1}`, true},
		"an object with no check of kind":  {plain, `{ "a" : 1 }`, `{"a":1}`, true},
		"exactly 1 MiB":                    {echoKind(), exact, exact, true},
		"over 1 MiB":                       {echoKind(), exact[:6] + "x" + exact[6:], "the payload is larger than the 1 MiB that a submission may be", false},
		"not UTF-8":                        {echoKind(), "{\"a\":\"\xff\xfe\"}", "the payload is not valid UTF-8: a submission is JSON text", false},
		"empty":                            {echoKind(), "", "the payload is empty: a submission is one JSON object", false},
		"whitespace":                       {echoKind(), " \n\t ", "the payload is empty: a submission is one JSON object", false},
		"not JSON":                         {echoKind(), `oops`, "the payload is not valid JSON: invalid character 'o' looking for beginning of value", false},
		"unterminated":                     {echoKind(), `{"a":`, "the payload is not valid JSON: unexpected EOF", false},
		"an array":                         {echoKind(), `[1,2]`, "the payload is a JSON array, and a submission is a single JSON object", false},
		"a string":                         {echoKind(), `"text"`, "the payload is a JSON string, and a submission is a single JSON object", false},
		"a number":                         {echoKind(), `42`, "the payload is a JSON number, and a submission is a single JSON object", false},
		"a boolean":                        {echoKind(), `true`, "the payload is a JSON boolean, and a submission is a single JSON object", false},
		"null":                             {echoKind(), `null`, "the payload is null, and a submission is a single JSON object", false},
		"two objects":                      {echoKind(), `{"a":1}{"b":2}`, "the payload has more after its object, and a submission is one JSON object and nothing else", false},
		"an object and a tail":             {echoKind(), `{"a":1} x`, "the payload has more after its object, and a submission is one JSON object and nothing else", false},
		"a kind that panics":               {panicking(), `{"a":1}`, "the test kind could not check the payload: a string was a number", false},
		"a problem of the kind":            {refusing(Problem{"cite", "no such file"}), `{"a":1}`, "cite: no such file", false},
		"a kind that hands back no object": {validating(func(json.RawMessage) (json.RawMessage, []Problem) { return json.RawMessage(`[1]`), nil }), `{"a":1}`, "submission: the test kind returned a payload that is not a JSON object", false},
	} {
		payload, problems := Check(context.Background(), c.kind, fstest.MapFS{}, []byte(c.raw))

		if c.ok {
			if len(problems) != 0 || string(payload) != c.want {
				t.Errorf("%s: Check = %.40s, %v, want the payload %.40s", name, payload, problems, c.want)
			}
			continue
		}
		if payload != nil || len(problems) != 1 || problems[0].String() != c.want {
			t.Errorf("%s: Check = %.40s, %v, want the one problem %q", name, payload, problems, c.want)
		}
	}
}

func TestCheck_GivesTheKindTheTreeAndThePayload(t *testing.T) {
	tree := fstest.MapFS{"a.go": {Data: []byte("package a")}}
	kind := echoKind()
	kind.Validate = func(_ context.Context, fsys fs.FS, payload json.RawMessage) (json.RawMessage, []Problem) {
		var p struct{ Cite string }
		_ = json.Unmarshal(payload, &p)
		if _, err := fs.Stat(fsys, p.Cite); err != nil {
			return nil, []Problem{{Path: "cite", Message: p.Cite + " is not in the tree"}}
		}
		return payload, nil
	}

	if _, problems := Check(context.Background(), kind, tree, []byte(`{"cite":"a.go"}`)); len(problems) != 0 {
		t.Errorf("a payload that cites a file of the tree: %v", problems)
	}
	if _, problems := Check(context.Background(), kind, tree, []byte(`{"cite":"b.go"}`)); len(problems) != 1 || problems[0].String() != "cite: b.go is not in the tree" {
		t.Errorf("a payload that cites no file of the tree: %v", problems)
	}
}
