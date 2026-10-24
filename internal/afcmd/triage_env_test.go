package afcmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// triageEnv is a git repository, a fake forge with an issue 7 in it, and the
// shell wired to the forge.
type triageEnv struct {
	t     *testing.T
	dir   string
	forge *fakeforge.Forge
	app   *toolio.App
}

const originalReport = "The login button does nothing.\nIt used to work."

const helloTest = "[ \"$(sh src/greet.sh)\" = hello ] || { echo \"FAIL: not hello\"; exit 1; }\n"

// newTriageEnv makes the environment for a forge of the kind. The token is the
// forge's; the environment of the test has none.
func newTriageEnv(t *testing.T, kind string) *triageEnv {
	t.Helper()
	requireGit(t)
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "GITHUB_API_URL", "GITLAB_API_URL", "ANTHROPIC_API_KEY"} {
		t.Setenv(name, "")
	}
	e := &triageEnv{t: t, forge: fakeforge.New(t, kind, "o/r")}
	e.dir = gitRepo(t, map[string]string{
		"go.mod":       "module example.com/x\n",
		"main.go":      "package main\n\nimport \"example.com/x/util\"\n\n// Run starts the program.\nfunc Run() {\n\tutil.Help()\n}\n\nfunc main() { Run() }\n",
		"util/util.go": "package util\n\n// Help helps.\nfunc Help() {}\n",
	})
	e.forge.AddIssue(fakeforge.Issue{Number: 7, Title: "Login fails", Body: originalReport, Comments: []fakeforge.Comment{
		{Author: "bob", Body: "Works for me."},
	}})
	e.app = newApp()
	e.app.NewForge = e.forge.NewForge("test-token")
	return e
}

// run runs the shell with args and returns the exit code and the envelope.
func (e *triageEnv) run(stdin string, args ...string) (int, map[string]any) {
	e.t.Helper()
	var out, errb bytes.Buffer
	code := e.app.Main(context.Background(), append([]string{"af"}, args...), strings.NewReader(stdin), &out, &errb)
	env := oneDocument(e.t, out.String())
	see(env)
	return code, env
}

// runDir is the run directory of an envelope.
func (e *triageEnv) runDir(env map[string]any) string {
	e.t.Helper()
	id, _ := env["run_id"].(string)
	if id == "" {
		e.t.Fatalf("the envelope has no run_id: %v", env)
	}
	return filepath.Join(e.dir, ".agent-fox", "run", id)
}

func sha256Of(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// ledgerKinds are the kinds of the ledger of a run directory.
func ledgerKinds(t *testing.T, runDir string) []string {
	t.Helper()
	var kinds []string
	for _, line := range strings.Split(strings.TrimSpace(string(readFile(t, filepath.Join(runDir, "ledger.jsonl")))), "\n") {
		var entry struct{ Kind string }
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, entry.Kind)
	}
	return kinds
}

// resultOf is the result of an envelope as a map.
func resultOf(t *testing.T, env map[string]any) map[string]any {
	t.Helper()
	m, ok := env["result"].(map[string]any)
	if !ok {
		t.Fatalf("result = %v, want an object (status %v, error %v)", env["result"], env["status"], env["error"])
	}
	return m
}

func artifactsOf(env map[string]any, kind string) []map[string]any {
	var out []map[string]any
	list, _ := env["artifacts"].([]any)
	for _, a := range list {
		if m, ok := a.(map[string]any); ok && m["kind"] == kind {
			out = append(out, m)
		}
	}
	return out
}

func warningCodes(env map[string]any) []string {
	var out []string
	list, _ := env["warnings"].([]any)
	for _, w := range list {
		if m, ok := w.(map[string]any); ok {
			out = append(out, m["code"].(string))
		}
	}
	return out
}

// checkSchema compares the embedded schema of a command with the fields of its
// result type.
func checkSchema(t *testing.T, name string, typ any) {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(resultSchema(name), &schema); err != nil {
		t.Fatal(err)
	}
	for _, diff := range schemaDiff(schema, reflect.TypeOf(typ), name) {
		t.Error(diff)
	}
}

// git2 runs git in dir and returns what it printed.
func git2(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return strings.TrimSpace(gitDo(t, dir, args...))
}

// diagnosisPayload is a diagnosis that is valid against the repository of
// newTriageEnv, as the JSON a brain would send.
func diagnosisPayload(mutate func(map[string]any)) string {
	m := map[string]any{
		"title": "Run starts the wrong thing", "severity": "high", "confidence": "probable",
		"root_cause":          "Run calls Help before it checks its arguments.",
		"affected_files":      []any{map[string]any{"path": "main.go", "line": 6, "note": "Run"}, map[string]any{"path": "util/util.go", "note": "Help"}},
		"suggested_fix":       "Check the arguments first.",
		"acceptance_criteria": []any{"Run with no arguments prints the usage"},
	}
	if mutate != nil {
		mutate(m)
	}
	data, _ := json.Marshal(m)
	return string(data)
}

// inProcess submits the payload the way a brain's file_diagnosis tool does: the
// submit core over the tree, with the kind of the triage run.
func (e *triageEnv) inProcess(id, payload string) error {
	e.t.Helper()
	root, err := os.OpenRoot(e.dir)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	target := submission.Target{Tree: root.FS(), Write: submission.RootWriter(root), RunID: id}
	_, err = submission.Submit(context.Background(), target, triage.Kind(e.dir), []byte(payload), time.Now())
	return err
}

// failedWith asserts a failed envelope of the category with the exit code.
func failedWith(t *testing.T, code int, env map[string]any, wantCode int, category string) map[string]any {
	t.Helper()
	errObj, _ := env["error"].(map[string]any)
	if code != wantCode || errObj["category"] != category || env["result"] != nil {
		t.Fatalf("exit code %d, status %v, error %v, result %v; want exit code %d, category %s and no result", code, env["status"], errObj, env["result"], wantCode, category)
	}
	return errObj
}

// writeFile writes a file of the checkout.
func (e *fixEnv) writeFile(path, content string) {
	e.t.Helper()
	full := filepath.Join(e.dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// briefRun places a run of issue 7 in the checkout the way brain.Execute places one in a
// box: the brief of the report, and a run.json with the attempt, the nonce and where the
// prefix ends. It returns the run id, which af triage run takes.
func (e *triageEnv) briefRun() string {
	e.t.Helper()
	rd, err := toolio.NewRunDir(e.dir, nil, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	brief, err := triage.BuildBrief(context.Background(), triage.BriefParams{
		Dir: e.dir, Report: triage.Report{Kind: "text", Body: originalReport}, RunID: rd.ID, TruncationMarker: toolio.TruncationMarker,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	placeRun(e.t, e.dir, rd.ID, triage.KindName, 1, "0123456789abcdef", brief.Prefix, brief.Task)
	return rd.ID
}

// submissionFile is the submission of a run.
func (e *triageEnv) submissionFile(id string) string {
	return filepath.Join(e.dir, ".agent-fox", "run", id, "submission.json")
}
