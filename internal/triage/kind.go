package triage

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/submission"
)

// KindName and ToolName are the name of the kind of submission of a triage run,
// and of the terminating tool of an in-process brain.
const (
	KindName = "triage"
	ToolName = "file_diagnosis"
)

//go:embed diagnosis.schema.json
var diagnosisSchema []byte

// Schema is the JSON Schema of a diagnosis, which is the input schema of
// file_diagnosis.
func Schema() json.RawMessage { return diagnosisSchema }

// kindDescription is what the model reads of file_diagnosis.
const kindDescription = "Hand over your diagnosis of the reported problem. Call it once, when you have read enough; if it is refused, read every problem it lists, correct them all and call it again. Every path you cite must be a file of the repository that you read."

// Kind is the submission kind of a triage run over the checkout hostDir. Its
// check is Validate against the file view of that checkout, which is the same
// view on the host and in a box, and whatever tree the submission is made in
// supplies the number of lines of a file. extraRoots are absolute directories
// that stand for the root of the tree beside hostDir, such as the workspace of a
// box. A refused submission is noted in rejections.jsonl beside it.
func Kind(hostDir string, extraRoots ...string) submission.Kind {
	roots := []string{hostDir}
	if resolved, err := filepath.EvalSymlinks(hostDir); err == nil && resolved != hostDir {
		roots = append(roots, resolved)
	}
	for _, extra := range extraRoots {
		roots = append(roots, extra)
		if resolved, err := filepath.EvalSymlinks(extra); err == nil && resolved != extra {
			roots = append(roots, resolved)
		}
	}
	return submission.Kind{
		Name:        KindName,
		ToolName:    ToolName,
		Description: kindDescription,
		Schema:      Schema(),
		Validate: func(ctx context.Context, tree fs.FS, payload json.RawMessage) (json.RawMessage, []submission.Problem) {
			files, err := checks.FileView(ctx, hostDir)
			if err != nil {
				return nil, []submission.Problem{{Message: "the files of the repository could not be listed to check the paths against: " + err.Error()}}
			}
			d, problems := Validate(payload, Tree{Roots: roots, Files: files, Regular: func(p string) bool { return RegularFile(hostDir, p) }, Lines: linesIn(tree)})
			if len(problems) > 0 {
				return nil, asSubmission(problems)
			}
			normalized, err := json.Marshal(d)
			if err != nil {
				return nil, []submission.Problem{{Message: "the diagnosis could not be written out: " + err.Error()}}
			}
			return normalized, nil
		},
		OnRefused: noteRefusal,
	}
}

// linesIn counts the lines of a file of the tree.
func linesIn(tree fs.FS) func(string) (int, error) {
	return func(p string) (int, error) {
		data, err := fs.ReadFile(tree, p)
		if err != nil {
			return 0, err
		}
		return countLines(data), nil
	}
}

// countLines is the number of lines of a text: its line breaks, and one more for
// a last line that has none.
func countLines(data []byte) int {
	n := bytes.Count(data, []byte{'\n'})
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}

// asSubmission is the problems as the submission protocol has them.
func asSubmission(problems []Problem) []submission.Problem {
	out := make([]submission.Problem, len(problems))
	for i, p := range problems {
		out[i] = submission.Problem{Path: p.Pointer, Message: p.Message}
	}
	return out
}

// unresolvedPrefix starts the message of a problem with a path that is no file of
// the tree; the path follows, quoted.
const unresolvedPrefix = "unresolved path "

// fromSubmission is the problems of a refusal as this package has them, with the
// paths that did not resolve told from the rest by the message that says so.
func fromSubmission(problems []submission.Problem) []Problem {
	out := make([]Problem, len(problems))
	for i, p := range problems {
		out[i] = Problem{Pointer: p.Path, Message: p.Message}
		if rest, ok := strings.CutPrefix(p.Message, unresolvedPrefix); ok {
			if quoted, err := strconv.QuotedPrefix(rest); err == nil {
				if cited, err := strconv.Unquote(quoted); err == nil {
					out[i].Unresolved, out[i].Path = true, cited
				}
			}
		}
	}
	return out
}

// noteRefusal appends the line of a refused call to rejections.jsonl, in the
// directory the submission would be written to, through the target the
// submission was made with.
func noteRefusal(ctx context.Context, target submission.Target, problems []submission.Problem) {
	rel := RejectionsPath(target.RunID)
	existing, _ := fs.ReadFile(target.Tree, rel) // a file that is not there is an empty one
	line := FormatRejection(time.Now(), fromSubmission(problems))
	_ = target.Write(ctx, rel, append(existing, line...)) // noted if it can be; the refusal stands either way
}

// readOnlyTools are the tools of the hands that only read.
var readOnlyTools = []string{"read_file", "list_files", "find_files", "search_files", "file_outline", "find_symbol", "find_references", "code_search"}

// Allowlist is the tools a triage brain may call, named in one place: the
// read-only tools of the hands that the toolset offers, and the terminating tool
// file_diagnosis. A name the hands do not offer is dropped, code_search with it,
// and a tool that writes or runs is never there.
func Allowlist(offered []string) []string {
	var list []string
	for _, name := range readOnlyTools {
		if slices.Contains(offered, name) {
			list = append(list, name)
		}
	}
	return append(list, ToolName)
}

// BoxTools is the allowlist as the tools of the box: without the terminating
// tool, which the brain adds itself.
func BoxTools(offered []string) []string {
	list := Allowlist(offered)
	return list[:len(list)-1]
}

// AdaptTask gives the task block of a brief the bounds of the run that is about to start:
// only the line of the bounds changes, the run id, the earlier question and the answers stay.
func AdaptTask(task string, b Bounds) string {
	if i := strings.LastIndex(task, boundsPrefix); i >= 0 {
		task = task[:i] + boundsLine(b)
	}
	return task
}
