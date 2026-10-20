package submission

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// Result is what the submit command reports when it wrote a submission.
type Result struct {
	RunID  string `json:"run_id"`
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// resultSchema is the JSON Schema of Result, which af --schema prints.
var resultSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["run_id", "kind", "path", "sha256"],
  "description": "The submission that was written.",
  "properties": {
    "run_id": { "type": "string", "description": "The run the submission is for." },
    "kind": { "type": "string", "description": "The kind of the submission." },
    "path": { "type": "string", "description": "The submission file, as an absolute path." },
    "sha256": { "type": "string", "description": "The hex SHA-256 of the compacted payload, which the file carries as payload_sha256." }
  }
}`)

// Command builds the `af <agent> submit` command of a kind, which a brain with
// its own hands runs inside the box to hand back its result: the payload is the
// JSON object on stdin, the tree is --dir (the current directory by default)
// and the run is --run-id. The payload is validated against the tree, which is
// opened so that no link in it can lead out. A refusal, malformed JSON included,
// is failed/submission and retryable, names every problem in the message, and
// writes nothing; an earlier valid submission stays. The command makes no run
// directory of its own and takes no --dry-run: it writes one file, into the run
// that it is for.
func Command(path string, kind Kind) toolio.Command {
	var runID string
	return toolio.Command{
		Path:    path,
		Summary: "Submit the " + kind.Name + " result of a run, as a JSON object on stdin",
		Usage:   "af " + path + " --run-id ID [--dir DIR]  < payload.json",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&runID, "run-id", "", "the `id` of the run the submission is for")
		},
		MaxArgs:      toolio.NoArgs,
		Dir:          true,
		RawStdin:     true,
		ResultSchema: resultSchema,
		Run:          func(r *toolio.Run) (toolio.Outcome, error) { return submit(r, kind, runID) },
	}
}

// submit is the run of the command.
func submit(r *toolio.Run, kind Kind, runID string) (toolio.Outcome, error) {
	if runID == "" {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: "--run-id is required: the id of the run the submission is for"}
	}
	run, err := toolio.OpenRunDir(r.Dir, runID)
	if err != nil {
		return toolio.Outcome{}, err
	}
	root, err := os.OpenRoot(r.Dir)
	if err != nil {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("--dir %s: %v", r.Dir, err)}
	}
	defer func() { _ = root.Close() }()

	// One byte more than a submission may be: enough to tell that it is, and
	// no more of an input that does not end.
	var payload []byte
	if r.Stdin != nil {
		payload, err = io.ReadAll(io.LimitReader(r.Stdin, MaxPayloadBytes+1))
		if err != nil {
			return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot read the payload on stdin: %v", err)}
		}
	}

	receipt, err := Submit(r.Ctx, Target{Tree: root.FS(), Write: RootWriter(root), RunID: run.ID}, kind, payload, time.Now())
	var refused *Refused
	switch {
	case errors.As(err, &refused):
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategorySubmission, Retryable: true, Message: refused.Error()}
	case err != nil:
		return toolio.Outcome{}, err
	}
	abs := filepath.Join(r.Dir, filepath.FromSlash(receipt.Path))
	r.Artifact(toolio.Artifact{Kind: "submission", Path: abs})
	return toolio.Outcome{
		Status:  toolio.StatusDone,
		Summary: fmt.Sprintf("submitted the %s result of run %s", kind.Name, run.ID),
		Result:  Result{RunID: run.ID, Kind: kind.Name, Path: abs, SHA256: receipt.SHA256},
	}, nil
}
