package submission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

const runID = "20261020_140000_abcdef"

var (
	runFile        = ".agent-fox/run/" + runID + "/run.json"
	submissionFile = ".agent-fox/run/" + runID + "/submission.json"
	fixedNow       = time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC)
)

// tree is a directory and the Target over it.
type tree struct {
	t      *testing.T
	dir    string
	target Target
}

// newTree makes a directory whose run.json is for the run, the kind "test", attempt 2 and a
// nonce, unless run is false.
func newTree(t *testing.T, run bool) *tree {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	tr := &tree{t: t, dir: dir, target: Target{Tree: root.FS(), Write: RootWriter(root), RunID: runID}}
	if run {
		tr.writeRun(fmt.Sprintf(`{"run_id":%q,"kind":"test","attempt":2,"nonce":"abcdef0123456789"}`, runID))
	}
	return tr
}

func (tr *tree) writeRun(content string) { testutil.WriteFile(tr.t, tr.dir, runFile, content) }

// submit submits raw as a payload of the kind.
func (tr *tree) submit(kind Kind, raw string) (Receipt, error) {
	return Submit(context.Background(), tr.target, kind, []byte(raw), fixedNow)
}

// files are the files of the tree but run.json.
func (tr *tree) files() map[string]string {
	files := testutil.Files(tr.t, tr.dir)
	delete(files, runFile)
	return files
}

// echoKind is the kind "test", which accepts every payload as it is.
func echoKind() Kind {
	return Kind{Name: "test", ToolName: "submit_test", Schema: json.RawMessage(`{"type":"object"}`),
		Validate: func(_ context.Context, _ fs.FS, payload json.RawMessage) (json.RawMessage, []Problem) {
			return payload, nil
		}}
}

// validating is the kind "test" whose check is validate.
func validating(validate func(payload json.RawMessage) (json.RawMessage, []Problem)) Kind {
	k := echoKind()
	k.Validate = func(_ context.Context, _ fs.FS, payload json.RawMessage) (json.RawMessage, []Problem) {
		return validate(payload)
	}
	return k
}

// refusing is the kind "test" that refuses every payload with the problems.
func refusing(problems ...Problem) Kind {
	return validating(func(json.RawMessage) (json.RawMessage, []Problem) { return nil, problems })
}

// panicking is the kind "test" whose check panics.
func panicking() Kind {
	return validating(func(json.RawMessage) (json.RawMessage, []Problem) { panic("a string was a number") })
}

func refused(t *testing.T, err error) *Refused {
	t.Helper()
	var r *Refused
	if !errors.As(err, &r) {
		t.Fatalf("error = %v (%T), want a *Refused", err, err)
	}
	return r
}

// readFile is the submission file of the tree, decoded.
func (tr *tree) readFile() File {
	tr.t.Helper()
	return testutil.Decode[File](tr.t, []byte(testutil.ReadFile(tr.t, filepath.Join(tr.dir, submissionFile))))
}
