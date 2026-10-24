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
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
)

// MaxPayloadBytes is the largest payload a submission may carry. It is far
// above any diagnosis or report, and it bounds what a brain can make af hold.
const MaxPayloadBytes = 1 << 20

// MaxFileBytes is the largest submission file af reads out of a box: a payload
// of at most MaxPayloadBytes, with room for the fields around it, and for the
// space that a brain's own hands may leave in it.
const MaxFileBytes = 2 * MaxPayloadBytes

// RunPath is run.json of a run, relative to the tree: where the loop places,
// before each brain run, the kind, the attempt and the nonce that a submission
// must carry.
func RunPath(runID string) string { return path.Join(agentdir.Name, "run", runID, "run.json") }

// SubmissionPath is the submission of a run, relative to the tree.
func SubmissionPath(runID string) string {
	return path.Join(agentdir.Name, "run", runID, "submission.json")
}

// Run is what run.json holds.
type Run struct {
	RunID   string `json:"run_id"`
	Kind    string `json:"kind"`
	Attempt int    `json:"attempt"`
	Nonce   string `json:"nonce"`
	// PrefixBytes is the length of the run-constant prefix of brief.md, which a
	// process that runs the brief in the box splits it at. The submit core does not
	// use it.
	PrefixBytes int `json:"prefix_bytes"`
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
	// Write puts a file, named relative to the tree, in the tree. It should be
	// atomic, so that a reader sees the old file or the new one: RootWriter is.
	// Box.WriteFile replaces a file that is there atomically and writes a new
	// one in place, which is enough for the host, since it reads only after the
	// brain has stopped.
	Write func(ctx context.Context, rel string, data []byte) error
	RunID string
}

// Submit is the path to a submission file for a brain that is af's own: the
// hands of an in-process brain and the submit command both come here. It
// validates raw as a payload of
// the kind against the tree, and writes .agent-fox/run/<id>/submission.json,
// with the attempt and the nonce of run.json. It refuses with *Refused, naming
// every problem at once, writing nothing and leaving an earlier file as it was,
// when the payload is larger than 1 MiB, is empty, is not a single JSON object,
// when run.json is missing or is not for this run and kind, and when the kind
// finds a problem with it. Each accepted submission replaces the one before:
// the last valid submission wins.
func Submit(ctx context.Context, target Target, kind Kind, raw []byte, now time.Time) (Receipt, error) {
	if target.RunID == "" || target.RunID == "." || target.RunID == ".." || strings.Contains(target.RunID, "/") || !fs.ValidPath(RunPath(target.RunID)) {
		return Receipt{}, fmt.Errorf("submission: %q is not a run id", target.RunID)
	}
	payload, problems := checkShape(raw)
	if len(problems) > 0 {
		return Receipt{}, refuse(problems)
	}

	run, problems := readRun(target, kind)
	compact, found, err := normalise(ctx, kind, target.Tree, payload)
	if err != nil {
		return Receipt{}, err
	}
	problems = append(problems, found...)
	if len(problems) > 0 {
		return Receipt{}, refuse(problems)
	}

	sum := PayloadSHA256(compact)
	out := Marshal(File{
		Kind: kind.Name, RunID: target.RunID, Attempt: run.Attempt, Nonce: run.Nonce,
		SubmittedAt: now.UTC().Format(time.RFC3339Nano), PayloadSHA256: sum, Payload: compact,
	})
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	rel := SubmissionPath(target.RunID)
	if err := target.Write(ctx, rel, out); err != nil {
		return Receipt{}, fmt.Errorf("submission: writing %s: %w", rel, err)
	}
	return Receipt{Path: rel, SHA256: sum}, nil
}

// Check is what Submit decides about a payload, without the run: raw must be a
// payload of the kind that passes the shape rules (at most 1 MiB, valid UTF-8,
// one JSON object) and the kind's own check against the tree. It returns the
// payload as the kind normalised it, compact, or every problem. It is what a
// host asks of a submission it did not write itself.
func Check(ctx context.Context, kind Kind, tree fs.FS, raw []byte) (json.RawMessage, []Problem) {
	payload, problems := checkShape(raw)
	if len(problems) > 0 {
		return nil, problems
	}
	compact, problems, err := normalise(ctx, kind, tree, payload)
	if err != nil {
		// A kind that hands back what is no object is a defect of the kind; to a
		// host that reads a file it did not write, the file does not count.
		return nil, []Problem{{Message: err.Error()}}
	}
	return compact, problems
}

// normalise runs the kind's check over a payload that has the right shape, and
// returns what it accepted, compact. A kind that panics, or that hands back more
// than a payload may be, has found a problem with the payload and takes no one
// down with it. A kind that hands back something other than a JSON object is
// defective, which is an error and not a refusal of the brain's.
func normalise(ctx context.Context, kind Kind, tree fs.FS, payload []byte) (json.RawMessage, []Problem, error) {
	normalised := json.RawMessage(payload)
	if kind.Validate != nil {
		var problems []Problem
		func() {
			defer func() {
				if v := recover(); v != nil {
					problems = []Problem{{Message: fmt.Sprintf("the %s kind could not check the payload: %v", kind.Name, v)}}
				}
			}()
			normalised, problems = kind.Validate(ctx, tree, payload)
		}()
		if len(problems) > 0 {
			return nil, problems, nil
		}
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, normalised); err != nil || !bytes.HasPrefix(buf.Bytes(), []byte("{")) {
		return nil, nil, fmt.Errorf("submission: the %s kind returned a payload that is not a JSON object", kind.Name)
	}
	if buf.Len() > MaxPayloadBytes {
		return nil, []Problem{{Message: "the payload is larger than the 1 MiB that a submission may be"}}, nil
	}
	return buf.Bytes(), nil, nil
}

// PayloadSHA256 is the hex SHA-256 of a compact payload, which a submission
// file carries as payload_sha256.
func PayloadSHA256(compact []byte) string {
	digest := sha256.Sum256(compact)
	return hex.EncodeToString(digest[:])
}

// Marshal is the bytes of a submission file, with the payload as it is hashed:
// a JSON encoder would write it again (it escapes U+2028 and U+2029 even where
// it escapes nothing else), and the hash would no longer be the hash of what is
// in the file. Submit writes this layout, and a host that accepts a submission
// writes it again from what it checked, so that the file it keeps holds nothing
// else.
func Marshal(f File) []byte {
	var out bytes.Buffer
	fmt.Fprintf(&out, `{"kind":%s,"run_id":%s,"attempt":%d,"nonce":%s,"submitted_at":%s,"payload_sha256":%s,"payload":`,
		quote(f.Kind), quote(f.RunID), f.Attempt, quote(f.Nonce), quote(f.SubmittedAt), quote(f.PayloadSHA256))
	out.Write(f.Payload)
	out.WriteString("}\n")
	return out.Bytes()
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
		return nil, whole("the payload is larger than the 1 MiB that a submission may be")
	}
	if !utf8.Valid(raw) {
		return nil, whole("the payload is not valid UTF-8: a submission is JSON text")
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
