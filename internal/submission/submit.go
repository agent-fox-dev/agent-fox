package submission

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"time"
)

// MaxPayloadBytes is the largest payload a submission may carry. It is far
// above any diagnosis or report, and it bounds what a brain can make af hold.
const MaxPayloadBytes = 1 << 20

// RunPath is run.json of a run, relative to the tree: where the loop places,
// before each brain run, the kind, the attempt and the nonce that a submission
// must carry.
func RunPath(runID string) string { return path.Join(".agent-fox", "run", runID, "run.json") }

// SubmissionPath is the submission of a run, relative to the tree.
func SubmissionPath(runID string) string {
	return path.Join(".agent-fox", "run", runID, "submission.json")
}

// Run is what run.json holds.
type Run struct {
	RunID   string `json:"run_id"`
	Kind    string `json:"kind"`
	Attempt int    `json:"attempt"`
	Nonce   string `json:"nonce"`
}

// File is what submission.json holds. PayloadSHA256 is the hex SHA-256 of
// Payload, which is compact.
type File struct {
	Kind          string          `json:"kind"`
	RunID         string          `json:"run_id"`
	Attempt       int             `json:"attempt"`
	Nonce         string          `json:"nonce"`
	SubmittedAt   string          `json:"submitted_at"`
	PayloadSHA256 string          `json:"payload_sha256"`
	Payload       json.RawMessage `json:"payload"`
}

// Target is where a submission goes: the tree it is validated against, how a
// file is written into that tree, and the run it is for.
type Target struct {
	Tree fs.FS
	// Write puts a file, named relative to the tree, in the tree. It must be
	// atomic: a reader sees the old file or the new one.
	Write func(ctx context.Context, rel string, data []byte) error
	RunID string
}

// Submit is the one path to a submission file. It validates raw as a payload of
// the kind against the tree, and writes .agent-fox/run/<id>/submission.json,
// with the attempt and the nonce of run.json. It refuses with *Refused, naming
// every problem at once, writing nothing and leaving an earlier file as it was,
// when the payload is larger than 1 MiB, is empty, is not a single JSON object,
// when run.json is missing or is not for this run and kind, and when the kind
// finds a problem with it. Each accepted submission replaces the one before:
// the last valid submission wins.
func Submit(ctx context.Context, target Target, kind Kind, raw []byte, now time.Time) (Receipt, error) {
	if target.RunID == "" || strings.Contains(target.RunID, "/") || !fs.ValidPath(RunPath(target.RunID)) {
		return Receipt{}, fmt.Errorf("submission: %q is not a run id", target.RunID)
	}
	payload, problems := checkShape(raw)
	if len(problems) > 0 {
		return Receipt{}, refuse(problems)
	}

	run, problems := readRun(target, kind)
	normalised := json.RawMessage(payload)
	if kind.Validate != nil {
		var found []Problem
		normalised, found = kind.Validate(ctx, target.Tree, payload)
		problems = append(problems, found...)
	}
	if len(problems) > 0 {
		return Receipt{}, refuse(problems)
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, normalised); err != nil || !bytes.HasPrefix(compact.Bytes(), []byte("{")) {
		return Receipt{}, fmt.Errorf("submission: the %s kind returned a payload that is not a JSON object", kind.Name)
	}
	digest := sha256.Sum256(compact.Bytes())
	sum := hex.EncodeToString(digest[:])
	// The payload goes in as it was hashed. A JSON encoder would write it
	// again (it escapes U+2028 and U+2029 even where it escapes nothing else),
	// and the hash would no longer be the hash of what is in the file.
	var out bytes.Buffer
	fmt.Fprintf(&out, `{"kind":%s,"run_id":%s,"attempt":%d,"nonce":%s,"submitted_at":%s,"payload_sha256":%s,"payload":`,
		quote(kind.Name), quote(target.RunID), run.Attempt, quote(run.Nonce), quote(now.UTC().Format(time.RFC3339Nano)), quote(sum))
	out.Write(compact.Bytes())
	out.WriteString("}\n")
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	rel := SubmissionPath(target.RunID)
	if err := target.Write(ctx, rel, out.Bytes()); err != nil {
		return Receipt{}, fmt.Errorf("submission: writing %s: %w", rel, err)
	}
	return Receipt{Path: rel, SHA256: sum}, nil
}

// quote is s as a JSON string.
func quote(s string) string {
	b, _ := json.Marshal(s) // a string always marshals
	return string(b)
}

// refuse is the refusal for the problems, sorted by Path and then by Message.
func refuse(problems []Problem) *Refused {
	problems = slices.Clone(problems)
	slices.SortStableFunc(problems, func(a, b Problem) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		return strings.Compare(a.Message, b.Message)
	})
	return &Refused{Problems: problems}
}

// checkShape says what is wrong with the payload before the kind looks at it,
// and returns it trimmed when nothing is.
func checkShape(raw []byte) ([]byte, []Problem) {
	whole := func(format string, args ...any) []Problem { return []Problem{{Message: fmt.Sprintf(format, args...)}} }
	if len(raw) > MaxPayloadBytes {
		return nil, whole("the payload is %d bytes, more than the 1 MiB that a submission may be", len(raw))
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, whole("the payload is empty: a submission is one JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, whole("the payload is not valid JSON: %v", err)
	}
	if _, ok := v.(map[string]any); !ok {
		return nil, whole("the payload is %s, and a submission is a single JSON object", describe(v))
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, whole("the payload has more after its object, and a submission is one JSON object and nothing else")
	}
	return raw, nil
}

// describe names the JSON type of a decoded value.
func describe(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case []any:
		return "a JSON array"
	case string:
		return "a JSON string"
	case float64:
		return "a JSON number"
	case bool:
		return "a JSON boolean"
	}
	return "not an object"
}

// readRun reads run.json of the run from the tree, and says what is wrong with
// it: it is missing, is not JSON, is for another run or another kind, or lacks
// an attempt or a nonce.
func readRun(target Target, kind Kind) (Run, []Problem) {
	rel := RunPath(target.RunID)
	wrong := func(format string, args ...any) (Run, []Problem) {
		return Run{}, []Problem{{Path: rel, Message: fmt.Sprintf(format, args...)}}
	}
	data, err := fs.ReadFile(target.Tree, rel)
	if errors.Is(err, fs.ErrNotExist) {
		return wrong("run.json is missing: the loop places it before the brain runs, and a submission takes its attempt and nonce from it")
	}
	if err != nil {
		return wrong("run.json cannot be read: %v", err)
	}
	var run Run
	if err := json.Unmarshal(data, &run); err != nil {
		return wrong("run.json is not valid JSON: %v", err)
	}
	switch {
	case run.RunID != target.RunID:
		return wrong("run.json is for run %q, not for run %q", run.RunID, target.RunID)
	case run.Kind != kind.Name:
		return wrong("run.json names the kind %q, and this is a %q submission", run.Kind, kind.Name)
	case run.Attempt < 1:
		return wrong("run.json has attempt %d, and attempts count from 1", run.Attempt)
	case run.Nonce == "":
		return wrong("run.json has no nonce")
	}
	return run, nil
}

// RootWriter writes files into the directory of root, and nothing outside it:
// a link that leads out is not followed. Each file is written to a temporary
// file in its directory and renamed over its name, so that a reader sees the old
// file or the new one and no temporary file is left.
func RootWriter(root *os.Root) func(ctx context.Context, rel string, data []byte) error {
	return func(_ context.Context, rel string, data []byte) error {
		if !fs.ValidPath(rel) || rel == "." {
			return fmt.Errorf("%q is not a path of the tree", rel)
		}
		dir := path.Dir(rel)
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		var suffix [6]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			return err
		}
		tmp := path.Join(dir, ".tmp-"+path.Base(rel)+"-"+hex.EncodeToString(suffix[:]))
		f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = root.Rename(tmp, rel)
		}
		if err != nil {
			_ = root.Remove(tmp)
			return err
		}
		return nil
	}
}
