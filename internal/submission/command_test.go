package submission

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// citeKind reads the path that the payload cites from the tree. A path it can
// read is a problem too, and carries what it read, so that a read of a file
// that should not be read shows in the output.
func citeKind() Kind {
	k := echoKind()
	k.Validate = func(_ context.Context, tree fs.FS, payload json.RawMessage) (json.RawMessage, []Problem) {
		var p struct {
			Cite string `json:"cite"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, []Problem{{Message: err.Error()}}
		}
		if p.Cite == "" {
			return payload, nil
		}
		data, err := fs.ReadFile(tree, p.Cite)
		if err != nil {
			return nil, []Problem{{Path: "cite", Message: "cannot be read: " + err.Error()}}
		}
		return nil, []Problem{{Path: "cite", Message: "was read: " + string(data)}}
	}
	return k
}

// app is a shell with the command registered at "fake submit".
func app(kind Kind) *toolio.App {
	a := &toolio.App{Version: "test", Now: func() time.Time { return fixedNow }}
	a.Register(Command("fake submit", kind))
	return a
}

// result is what one run of the shell wrote.
type result struct {
	code     int
	stdout   string
	envelope map[string]any
}

// field digs a value out of the envelope.
func (r result) field(path ...string) any {
	var v any = r.envelope
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[p]
	}
	return v
}

func run(t *testing.T, a *toolio.App, stdin string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := a.Main(context.Background(), append([]string{"af"}, args...), strings.NewReader(stdin), &out, &errb)
	r := result{code: code, stdout: out.String()}
	if err := json.Unmarshal(out.Bytes(), &r.envelope); err != nil {
		t.Fatalf("stdout is not an envelope: %v\n%s\nstderr: %s", err, out.String(), errb.String())
	}
	return r
}

func submitArgs(tr *tree, extra ...string) []string {
	return append([]string{"fake", "submit", "--dir", tr.dir, "--run-id", runID}, extra...)
}

// TestCommandSubmitsAgainstDir_TS_07_34 verifies TS-07-34:
// the command factory builds a command at the given path that reads stdin and
// submits against --dir.
// Verifies: 07-REQ-5.1
func TestCommandSubmitsAgainstDir_TS_07_34(t *testing.T) {
	tr := newTree(t, "test")
	cmd := Command("fake submit", echoKind())

	r := run(t, app(echoKind()), `{"summary": "done"}`, submitArgs(tr)...)

	if cmd.Path != "fake submit" {
		t.Errorf("Path = %q", cmd.Path)
	}
	if r.code != 0 {
		t.Fatalf("exit code %d\n%s", r.code, r.stdout)
	}
	data, ok := tr.submission()
	if !ok || !strings.Contains(string(data), `"payload":{"summary":"done"}`) {
		t.Errorf("submission file = %q (exists %v)", data, ok)
	}
}

// TestCommandNeedsAValidRunID_TS_07_35 verifies TS-07-35:
// a missing or invalid --run-id is a usage failure with exit 2 and nothing is
// written.
// Verifies: 07-REQ-5.2
func TestCommandNeedsAValidRunID_TS_07_35(t *testing.T) {
	tr := newTree(t, "test")
	for name, args := range map[string][]string{
		"missing":         {"fake", "submit", "--dir", tr.dir},
		"empty":           {"fake", "submit", "--dir", tr.dir, "--run-id", ""},
		"not an id":       {"fake", "submit", "--dir", tr.dir, "--run-id", "bad"},
		"a path":          {"fake", "submit", "--dir", tr.dir, "--run-id", "../../etc"},
		"no such run":     {"fake", "submit", "--dir", tr.dir, "--run-id", "20261020_140000_ffffff"},
		"not a directory": {"fake", "submit", "--dir", filepath.Join(tr.dir, "nowhere"), "--run-id", runID},
	} {
		t.Run(name, func(t *testing.T) {
			r := run(t, app(echoKind()), `{"a":1}`, args...)

			if r.code != 2 || r.field("status") != "usage" || r.field("error", "category") != "usage" {
				t.Errorf("exit code %d, status %v, category %v; want 2, usage, usage\n%s", r.code, r.field("status"), r.field("error", "category"), r.stdout)
			}
			if _, ok := tr.submission(); ok {
				t.Error("a submission file was written")
			}
		})
	}
}

// TestCommandNeverReadsThroughALink_TS_07_36 verifies TS-07-36:
// for any tree, a symlink resolving outside --dir is never read by validation.
// Verifies: 07-REQ-5.3
func TestCommandNeverReadsThroughALink_TS_07_36(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 36))
	const secret = "the-host-secret-0451"
	for i := range 12 {
		t.Run(fmt.Sprintf("target %d", i), func(t *testing.T) {
			tr := newTree(t, "test")
			outside := t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte(secret), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(tr.dir, "a", "b"), 0o755); err != nil {
				t.Fatal(err)
			}
			up, err := filepath.Rel(filepath.Join(tr.dir, "a", "b"), outside)
			if err != nil {
				t.Fatal(err)
			}
			targets := []string{
				filepath.Join(outside, "secret.txt"), // absolute
				filepath.Join(outside),               // a directory outside
				filepath.Join(up, "secret.txt"),      // ../ relative, from a/b
				filepath.Join("..", "..", "..", up, "secret.txt"),
			}
			link := "link"
			linkDir := tr.dir
			if i%2 == 1 {
				link, linkDir = "a/b/link", filepath.Join(tr.dir, "a", "b")
			}
			target := targets[rng.IntN(len(targets))]
			if err := os.Symlink(target, filepath.Join(linkDir, "link")); err != nil {
				t.Fatal(err)
			}

			cited := link
			if strings.HasSuffix(target, "secret.txt") == false {
				cited = link + "/secret.txt" // through a link to a directory
			}
			r := run(t, app(citeKind()), fmt.Sprintf(`{"cite":%q}`, cited), submitArgs(tr)...)

			if r.field("error", "category") != "submission" || r.code != 1 {
				t.Errorf("exit code %d, category %v; want 1, submission\n%s", r.code, r.field("error", "category"), r.stdout)
			}
			if strings.Contains(r.stdout, secret) {
				t.Errorf("the output carries the content of a file outside --dir:\n%s", r.stdout)
			}
			if _, ok := tr.submission(); ok {
				t.Error("a submission file was written")
			}
		})
	}
}

// TestCommandAcceptedEnvelope_TS_07_37 verifies TS-07-37:
// an accepted submission yields a done envelope with result and a submission
// artifact.
// Verifies: 07-REQ-5.4
func TestCommandAcceptedEnvelope_TS_07_37(t *testing.T) {
	tr := newTree(t, "test")

	r := run(t, app(echoKind()), `{"summary": "done", "n": 3}`, submitArgs(tr)...)

	if r.code != 0 || r.field("status") != "done" {
		t.Fatalf("exit code %d, status %v\n%s", r.code, r.field("status"), r.stdout)
	}
	data, _ := tr.submission()
	var file File
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tr.dir, filepath.FromSlash(submissionFile))
	if r.field("result", "run_id") != runID || r.field("result", "kind") != "test" || r.field("result", "sha256") != file.PayloadSHA256 || r.field("result", "path") != path {
		t.Errorf("result = %v, want run_id, kind, sha256 %s and path %s", r.field("result"), file.PayloadSHA256, path)
	}
	artifacts, _ := r.field("artifacts").([]any)
	found := false
	for _, a := range artifacts {
		if m, ok := a.(map[string]any); ok && m["kind"] == "submission" && m["path"] == path {
			found = true
		}
	}
	if !found {
		t.Errorf("artifacts = %v, want a submission artifact at %s", artifacts, path)
	}
	if got := tr.listing(); !slices.Equal(got, []string{"run.json", "submission.json"}) {
		t.Errorf("the run directory holds %v", got)
	}
}

// TestCommandRefusedEnvelope_TS_07_38 verifies TS-07-38:
// a refused or malformed submission yields a failed/submission envelope that
// names every problem and keeps the earlier file.
// Verifies: 07-REQ-5.5
func TestCommandRefusedEnvelope_TS_07_38(t *testing.T) {
	tr := newTree(t, "test")
	if r := run(t, app(echoKind()), `{"first": true}`, submitArgs(tr)...); r.code != 0 {
		t.Fatalf("the first submission: %s", r.stdout)
	}
	before, _ := tr.submission()
	kind := problemsKind(Problem{"files[0]", "a.go is not in the tree"}, Problem{"summary", "is empty"})

	for name, tc := range map[string]struct {
		kind    Kind
		stdin   string
		mention []string
	}{
		"validation": {kind, `{"summary": ""}`, []string{"a.go is not in the tree", "summary", "is empty", "files[0]"}},
		"malformed":  {echoKind(), `{oops`, []string{"not valid JSON"}},
		"empty":      {echoKind(), ``, []string{"empty"}},
		"an array":   {echoKind(), `[1]`, []string{"array"}},
	} {
		t.Run(name, func(t *testing.T) {
			r := run(t, app(tc.kind), tc.stdin, submitArgs(tr)...)

			if r.code != 1 || r.field("status") != "failed" || r.field("error", "category") != "submission" || r.field("error", "retryable") != true || r.field("result") != nil {
				t.Errorf("exit code %d, status %v, category %v, retryable %v, result %v; want 1, failed, submission, true, null", r.code, r.field("status"), r.field("error", "category"), r.field("error", "retryable"), r.field("result"))
			}
			message, _ := r.field("error", "message").(string)
			for _, want := range tc.mention {
				if !strings.Contains(message, want) {
					t.Errorf("error.message = %q, want it to mention %q", message, want)
				}
			}
			if after, _ := tr.submission(); !bytes.Equal(after, before) {
				t.Errorf("the earlier submission changed:\n%s\n%s", before, after)
			}
		})
	}
}

// TestCommandIsSmall_TS_07_39 verifies TS-07-39:
// the submit command creates no run directory, has no --dry-run flag and
// declares its result schema.
// Verifies: 07-REQ-5.6
func TestCommandIsSmall_TS_07_39(t *testing.T) {
	tr := newTree(t, "test")
	runs := filepath.Join(tr.dir, ".agent-fox", "run")
	entries := func() []string {
		es, err := os.ReadDir(runs)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range es {
			names = append(names, e.Name())
		}
		return names
	}
	cmd := Command("fake submit", echoKind())
	before := entries()

	r := run(t, app(echoKind()), `{"a":1}`, submitArgs(tr)...)
	dry := run(t, app(echoKind()), `{"a":1}`, submitArgs(tr, "--dry-run")...)
	schema := run(t, app(echoKind()), "", "--schema")

	if r.code != 0 || r.field("run_id") != nil {
		t.Errorf("exit code %d, run_id %v; want 0 and no run", r.code, r.field("run_id"))
	}
	if after := entries(); !slices.Equal(before, after) {
		t.Errorf("the run directories were %v, are %v", before, after)
	}
	if cmd.UsesRunDir || cmd.DryRun || cmd.UsesForge {
		t.Errorf("the command opts into a run directory, --dry-run or a forge: %+v", cmd)
	}
	if dry.code != 2 || dry.field("status") != "usage" {
		t.Errorf("--dry-run: exit code %d, status %v; want a usage failure", dry.code, dry.field("status"))
	}
	if defs, _ := schema.field("$defs", "fake_submit").(map[string]any); defs == nil || !strings.Contains(schema.stdout, `"sha256"`) {
		t.Errorf("af --schema has no fake_submit result schema with a sha256:\n%.600s", schema.stdout)
	}
	if len(cmd.ResultSchema) == 0 || !json.Valid(cmd.ResultSchema) {
		t.Errorf("the command's ResultSchema is %q", cmd.ResultSchema)
	}
}

// TestCommandReadsAtMostOneMiB guards 07-REQ-5.1: the command does not read a
// payload past the limit, and a payload over it is refused as a size problem.
func TestCommandReadsAtMostOneMiB(t *testing.T) {
	tr := newTree(t, "test")
	huge := &countingReader{}

	var out, errb bytes.Buffer
	code := app(echoKind()).Main(context.Background(), append([]string{"af"}, submitArgs(tr)...), huge, &out, &errb)

	if code != 1 || !strings.Contains(out.String(), "1 MiB") {
		t.Errorf("exit code %d\n%.300s", code, out.String())
	}
	if huge.n > 2<<20 {
		t.Errorf("the command read %d bytes of an endless input", huge.n)
	}
}

// countingReader is an endless input of spaces, and counts what is read.
type countingReader struct{ n int }

func (r *countingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	r.n += len(p)
	return len(p), nil
}
