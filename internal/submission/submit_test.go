package submission

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const runID = "20261020_140000_abcdef"

var (
	runFile        = ".agent-fox/run/" + runID + "/run.json"
	submissionFile = ".agent-fox/run/" + runID + "/submission.json"
	fixedNow       = time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC)
)

// tree is a directory with a run.json in it, and the Target over it.
type tree struct {
	t      *testing.T
	dir    string
	target Target
}

// newTree makes a directory whose run.json names the kind, attempt 2 and a nonce.
func newTree(t *testing.T, kind string) *tree {
	t.Helper()
	dir := t.TempDir()
	tr := &tree{t: t, dir: dir}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	tr.target = Target{Tree: root.FS(), Write: RootWriter(root), RunID: runID}
	if kind != "" {
		tr.writeRun(fmt.Sprintf(`{"run_id":%q,"kind":%q,"attempt":2,"nonce":"abcdef0123456789"}`, runID, kind))
	}
	return tr
}

func (tr *tree) writeRun(content string) {
	tr.t.Helper()
	path := filepath.Join(tr.dir, filepath.FromSlash(runFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		tr.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		tr.t.Fatal(err)
	}
}

// submission is the bytes of the submission file, and whether there is one.
func (tr *tree) submission() ([]byte, bool) {
	data, err := os.ReadFile(filepath.Join(tr.dir, filepath.FromSlash(submissionFile)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false
	}
	if err != nil {
		tr.t.Fatal(err)
	}
	return data, true
}

// listing is the names in the run directory.
func (tr *tree) listing() []string {
	entries, err := os.ReadDir(filepath.Join(tr.dir, ".agent-fox", "run", runID))
	if err != nil {
		tr.t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// echoKind accepts every payload and returns it as it is.
func echoKind() Kind {
	return Kind{Name: "test", ToolName: "submit_test", Description: "a test kind", Schema: json.RawMessage(`{"type":"object"}`),
		Validate: func(_ context.Context, _ fs.FS, payload json.RawMessage) (json.RawMessage, []Problem) {
			return payload, nil
		}}
}

// problemsKind refuses every payload with the problems.
func problemsKind(problems ...Problem) Kind {
	k := echoKind()
	k.Validate = func(context.Context, fs.FS, json.RawMessage) (json.RawMessage, []Problem) {
		return nil, slices.Clone(problems)
	}
	return k
}

func refused(t *testing.T, err error) *Refused {
	t.Helper()
	var r *Refused
	if !errors.As(err, &r) {
		t.Fatalf("error = %v (%T), want a *submission.Refused", err, err)
	}
	return r
}

func compact(t *testing.T, raw string) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// TestSubmitWritesTheFile_TS_07_26 verifies TS-07-26:
// a valid payload is written with attempt and nonce from run.json and a
// matching sha256.
// Verifies: 07-REQ-4.1
func TestSubmitWritesTheFile_TS_07_26(t *testing.T) {
	tr := newTree(t, "test")
	kind := echoKind()
	kind.Validate = func(_ context.Context, _ fs.FS, payload json.RawMessage) (json.RawMessage, []Problem) {
		// Normalised: the keys of a map come out in order, with the layout of the input.
		var v map[string]any
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, []Problem{{Message: err.Error()}}
		}
		out, _ := json.MarshalIndent(v, "", "    ")
		return out, nil
	}
	raw := "{\n  \"summary\": \"fixed\",\n  \"files\": [ \"a.go\", \"b.go\" ]\n}\n"

	receipt, err := Submit(context.Background(), tr.target, kind, []byte(raw), fixedNow)

	if err != nil {
		t.Fatal(err)
	}
	data, ok := tr.submission()
	if !ok {
		t.Fatal("no submission file")
	}
	var file struct {
		Kind          string          `json:"kind"`
		RunID         string          `json:"run_id"`
		Attempt       int             `json:"attempt"`
		Nonce         string          `json:"nonce"`
		SubmittedAt   string          `json:"submitted_at"`
		PayloadSHA256 string          `json:"payload_sha256"`
		Payload       json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("the file is not JSON: %v\n%s", err, data)
	}
	want := compact(t, `{"files":["a.go","b.go"],"summary":"fixed"}`)
	if file.Kind != "test" || file.RunID != runID || file.Attempt != 2 || file.Nonce != "abcdef0123456789" {
		t.Errorf("file = %+v, want kind, run_id, attempt 2 and the nonce of run.json", file)
	}
	if string(file.Payload) != want {
		t.Errorf("payload = %s, want the compacted normalised payload %s", file.Payload, want)
	}
	if file.PayloadSHA256 != sum(want) || receipt.SHA256 != file.PayloadSHA256 {
		t.Errorf("payload_sha256 %s, receipt %s, want %s", file.PayloadSHA256, receipt.SHA256, sum(want))
	}
	if receipt.Path != submissionFile {
		t.Errorf("receipt path = %q, want %q", receipt.Path, submissionFile)
	}
	if at, err := time.Parse(time.RFC3339Nano, file.SubmittedAt); err != nil || !at.Equal(fixedNow) {
		t.Errorf("submitted_at = %q (%v), want %v", file.SubmittedAt, err, fixedNow)
	}
}

// TestSubmitRefusesBadShapes_TS_07_27 verifies TS-07-27:
// an oversized, empty or non-object payload is refused with a Refused naming
// the problem, and nothing is written.
// Verifies: 07-REQ-4.2
func TestSubmitRefusesBadShapes_TS_07_27(t *testing.T) {
	big := `{"a":"` + strings.Repeat("x", 1<<20-7) + `"}`
	for name, tc := range map[string]struct{ raw, mention string }{
		"over 1 MiB":           {big, "1 MiB"},
		"empty":                {"", "empty"},
		"whitespace":           {" \n\t ", "empty"},
		"an array":             {`[1,2]`, "object"},
		"two objects":          {`{"a":1}{"b":2}`, "one"},
		"an object and a tail": {`{"a":1} x`, "one"},
		"a bare string":        {`"text"`, "object"},
		"a number":             {`42`, "object"},
		"null":                 {`null`, "object"},
		"unterminated":         {`{"a":`, "JSON"},
		"not JSON":             {`oops`, "JSON"},
	} {
		t.Run(name, func(t *testing.T) {
			tr := newTree(t, "test")

			_, err := Submit(context.Background(), tr.target, echoKind(), []byte(tc.raw), fixedNow)

			r := refused(t, err)
			if len(r.Problems) == 0 || !strings.Contains(r.Error(), tc.mention) {
				t.Errorf("problems = %+v (%v), want one that mentions %q", r.Problems, r, tc.mention)
			}
			if _, ok := tr.submission(); ok {
				t.Error("a submission file was written")
			}
		})
	}
	if len(big) != 1<<20+1 {
		t.Fatalf("the test payload is %d bytes", len(big))
	}
	// A payload of exactly 1 MiB is accepted.
	tr := newTree(t, "test")
	exact := `{"a":"` + strings.Repeat("x", 1<<20-8) + `"}`
	if len(exact) != 1<<20 {
		t.Fatalf("the exact payload is %d bytes", len(exact))
	}
	if _, err := Submit(context.Background(), tr.target, echoKind(), []byte(exact), fixedNow); err != nil {
		t.Errorf("a payload of exactly 1 MiB: %v", err)
	}
}

// TestSubmitNeedsRunJSON_TS_07_28 verifies TS-07-28:
// a missing run.json, one that names another kind and a damaged one are
// refused with a problem that names run.json.
// Verifies: 07-REQ-4.3
func TestSubmitNeedsRunJSON_TS_07_28(t *testing.T) {
	for name, tr := range map[string]*tree{
		"missing":      newTree(t, ""),
		"another kind": newTree(t, "other"),
		"damaged":      func() *tree { tr := newTree(t, ""); tr.writeRun("{not json"); return tr }(),
		"another run": func() *tree {
			tr := newTree(t, "")
			tr.writeRun(`{"run_id":"20260101_000000_000000","kind":"test","attempt":1,"nonce":"n"}`)
			return tr
		}(),
		"no nonce": func() *tree {
			tr := newTree(t, "")
			tr.writeRun(fmt.Sprintf(`{"run_id":%q,"kind":"test","attempt":1}`, runID))
			return tr
		}(),
		"attempt zero": func() *tree {
			tr := newTree(t, "")
			tr.writeRun(fmt.Sprintf(`{"run_id":%q,"kind":"test","attempt":0,"nonce":"n"}`, runID))
			return tr
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Submit(context.Background(), tr.target, echoKind(), []byte(`{"a":1}`), fixedNow)

			r := refused(t, err)
			named := false
			for _, p := range r.Problems {
				named = named || strings.Contains(p.Message, "run.json")
			}
			if !named {
				t.Errorf("problems = %+v, want one that names run.json", r.Problems)
			}
			if _, ok := tr.submission(); ok {
				t.Error("a submission file was written")
			}
		})
	}
}

// TestSubmitReportsEveryProblem_TS_07_29 verifies TS-07-29:
// every problem Validate reports appears in the refusal at once.
// Verifies: 07-REQ-4.4
func TestSubmitReportsEveryProblem_TS_07_29(t *testing.T) {
	tr := newTree(t, "test")
	problems := []Problem{{"files[0]", "a.go is not in the tree"}, {"summary", "is empty"}, {"files[2]", "b.go is not in the tree"}}

	_, err := Submit(context.Background(), tr.target, problemsKind(problems...), []byte(`{"a":1}`), fixedNow)

	r := refused(t, err)
	if len(r.Problems) != 3 {
		t.Fatalf("problems = %+v, want all three", r.Problems)
	}
	for _, p := range problems {
		if !slices.Contains(r.Problems, p) {
			t.Errorf("problem %+v is missing from %+v", p, r.Problems)
		}
		if !strings.Contains(r.Error(), p.Message) || !strings.Contains(r.Error(), p.Path) {
			t.Errorf("the message %q does not name %+v", r.Error(), p)
		}
	}
	if _, ok := tr.submission(); ok {
		t.Error("a submission file was written")
	}
}

// TestSubmitProblemsAreSorted_TS_07_30 verifies TS-07-30:
// for any set of problems Refused.Problems is sorted by Path then Message.
// Verifies: 07-REQ-4.5
func TestSubmitProblemsAreSorted_TS_07_30(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 30))
	paths := []string{"", "a", "a[0]", "b", "files[1]", "files[10]", "ünï"}
	messages := []string{"x", "is empty", "does not exist", "Z", "é"}
	tr := newTree(t, "test")
	for i := range 200 {
		var given []Problem
		for range rng.IntN(12) + 1 {
			given = append(given, Problem{paths[rng.IntN(len(paths))], messages[rng.IntN(len(messages))]})
		}

		_, err := Submit(context.Background(), tr.target, problemsKind(given...), []byte(`{"a":1}`), fixedNow)

		r := refused(t, err)
		sorted := slices.IsSortedFunc(r.Problems, func(a, b Problem) int {
			return strings.Compare(a.Path+"\x00"+a.Message, b.Path+"\x00"+b.Message)
		})
		want := slices.Clone(given)
		got := slices.Clone(r.Problems)
		less := func(a, b Problem) int { return strings.Compare(a.Path+"\x00"+a.Message, b.Path+"\x00"+b.Message) }
		slices.SortFunc(want, less)
		slices.SortFunc(got, less)
		if !sorted || !slices.Equal(got, want) {
			t.Fatalf("#%d: problems %+v from %+v: sorted %v", i, r.Problems, given, sorted)
		}
	}
}

// TestRefusalLeavesEverythingAlone_TS_07_31 verifies TS-07-31:
// for any refusal an existing submission.json stays byte-identical and no file
// is created.
// Verifies: 07-REQ-4.6
func TestRefusalLeavesEverythingAlone_TS_07_31(t *testing.T) {
	refusals := map[string]struct {
		raw  string
		kind func(*tree) Kind
	}{
		"oversized":     {`{"a":"` + strings.Repeat("x", 1<<20) + `"}`, func(*tree) Kind { return echoKind() }},
		"empty":         {``, func(*tree) Kind { return echoKind() }},
		"not an object": {`[1]`, func(*tree) Kind { return echoKind() }},
		"another kind":  {`{"a":1}`, func(tr *tree) Kind { k := echoKind(); k.Name = "other"; return k }},
		"validation":    {`{"a":1}`, func(*tree) Kind { return problemsKind(Problem{"a", "bad"}, Problem{"b", "worse"}) }},
	}
	for name, tc := range refusals {
		for _, prior := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s with a prior file %v", name, prior), func(t *testing.T) {
				tr := newTree(t, "test")
				var before []byte
				if prior {
					if _, err := Submit(context.Background(), tr.target, echoKind(), []byte(`{"first":true}`), fixedNow); err != nil {
						t.Fatal(err)
					}
					before, _ = tr.submission()
				}
				listing := tr.listing()

				_, err := Submit(context.Background(), tr.target, tc.kind(tr), []byte(tc.raw), fixedNow.Add(time.Hour))

				_ = refused(t, err)
				after, ok := tr.submission()
				if ok != prior || !bytes.Equal(after, before) {
					t.Errorf("the submission file was %q, is %q (exists %v)", before, after, ok)
				}
				if got := tr.listing(); !slices.Equal(got, listing) {
					t.Errorf("the directory held %v, holds %v", listing, got)
				}
			})
		}
	}
}

// TestSubmitWritesAtomically_TS_07_32 verifies TS-07-32:
// Submit writes through Target.Write via a temp file renamed in place and leaves
// no temp file.
// Verifies: 07-REQ-4.7
func TestSubmitWritesAtomically_TS_07_32(t *testing.T) {
	tr := newTree(t, "test")
	var mu sync.Mutex
	var written []string
	inner := tr.target.Write
	tr.target.Write = func(ctx context.Context, rel string, data []byte) error {
		mu.Lock()
		written = append(written, rel)
		mu.Unlock()
		return inner(ctx, rel, data)
	}

	// A reader polls the path while the submissions replace one another.
	stop, torn := make(chan struct{}), make(chan string, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		path := filepath.Join(tr.dir, filepath.FromSlash(submissionFile))
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, err := os.ReadFile(path)
			if err == nil && !json.Valid(data) {
				select {
				case torn <- string(data):
				default:
				}
			}
		}
	}()
	for i := range 200 {
		raw := fmt.Sprintf(`{"n":%d,"pad":%q}`, i, strings.Repeat("p", 20000))
		if _, err := Submit(context.Background(), tr.target, echoKind(), []byte(raw), fixedNow); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()

	select {
	case data := <-torn:
		t.Errorf("a reader saw a partial file of %d bytes: %.60q", len(data), data)
	default:
	}
	if got := tr.listing(); !slices.Equal(got, []string{"run.json", "submission.json"}) {
		t.Errorf("the directory holds %v, want run.json and submission.json only", got)
	}
	if len(written) != 200 || written[0] != submissionFile {
		t.Errorf("Write was called with %d paths, first %q; want 200 of %q", len(written), written[0], submissionFile)
	}
}

// TestLastValidSubmissionWins_TS_07_33 verifies TS-07-33:
// a later valid submission replaces the earlier one.
// Verifies: 07-REQ-4.8
func TestLastValidSubmissionWins_TS_07_33(t *testing.T) {
	tr := newTree(t, "test")

	r1, err1 := Submit(context.Background(), tr.target, echoKind(), []byte(`{"v":"A"}`), fixedNow)
	r2, err2 := Submit(context.Background(), tr.target, echoKind(), []byte(`{"v":"B"}`), fixedNow.Add(time.Minute))

	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	data, _ := tr.submission()
	var file struct {
		Payload       json.RawMessage `json:"payload"`
		PayloadSHA256 string          `json:"payload_sha256"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if string(file.Payload) != `{"v":"B"}` || r2.SHA256 == r1.SHA256 || r2.SHA256 != file.PayloadSHA256 {
		t.Errorf("file payload %s, sha %s; receipts %s and %s", file.Payload, file.PayloadSHA256, r1.SHA256, r2.SHA256)
	}
}

// TestSubmitWithoutValidate guards 07-REQ-4.1: a kind with no Validate takes the
// payload as it is, and a kind whose Validate returns what is not an object is
// a bug that is not written.
func TestSubmitWithoutValidate(t *testing.T) {
	tr := newTree(t, "test")
	kind := echoKind()
	kind.Validate = nil

	if _, err := Submit(context.Background(), tr.target, kind, []byte("{ \"a\" : 1 }"), fixedNow); err != nil {
		t.Fatal(err)
	}
	data, _ := tr.submission()
	if !strings.Contains(string(data), `"payload":{"a":1}`) {
		t.Errorf("the file is %s", data)
	}

	bad := echoKind()
	bad.Validate = func(context.Context, fs.FS, json.RawMessage) (json.RawMessage, []Problem) {
		return json.RawMessage(`[1]`), nil
	}
	before, _ := tr.submission()
	_, err := Submit(context.Background(), tr.target, bad, []byte(`{"a":1}`), fixedNow)
	var r *Refused
	if err == nil || errors.As(err, &r) {
		t.Errorf("a kind that returns an array: %v, want an error that is not a refusal of the brain's", err)
	}
	if after, _ := tr.submission(); !bytes.Equal(before, after) {
		t.Error("the file changed")
	}
}

// TestSubmitKeepsThePayloadBytes guards 07-REQ-4.1: the payload is in the file
// as it was hashed, whatever the text in it, so that whoever reads the file back
// and compacts the payload finds the hash payload_sha256 holds.
func TestSubmitKeepsThePayloadBytes(t *testing.T) {
	tr := newTree(t, "test")
	raw := `{"html":"<b>a & b</b>","sep":"x` + " " + `y","uni":"ünï ☃","nested":{"k":[1,2.50,true,null]}}`

	receipt, err := Submit(context.Background(), tr.target, echoKind(), []byte(raw), fixedNow)

	if err != nil {
		t.Fatal(err)
	}
	data, _ := tr.submission()
	var file File
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	var back bytes.Buffer
	if err := json.Compact(&back, file.Payload); err != nil {
		t.Fatal(err)
	}
	// Nothing is escaped on the way in: not the HTML characters, and not U+2028,
	// which an encoder that writes the payload again does escape.
	if want := compact(t, raw); back.String() != want || string(file.Payload) != want || sum(back.String()) != file.PayloadSHA256 || file.PayloadSHA256 != receipt.SHA256 {
		t.Errorf("the file holds %s with hash %s; the payload compacted is %s", file.Payload, file.PayloadSHA256, compact(t, raw))
	}
	if !strings.Contains(string(file.Payload), "<b>a & b</b>") {
		t.Errorf("the HTML characters were escaped: %s", file.Payload)
	}
}
