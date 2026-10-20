package triage

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/schema"

	"github.com/agent-fox-dev/agentfox/internal/submission"
)

var nineNames = []string{"read_file", "list_files", "find_files", "search_files", "file_outline", "find_symbol", "find_references", "code_search", "file_diagnosis"}

// TestTheAllowlistIsReadOnlyAndHoldsTheTerminatingTool_TS_08_26 verifies TS-08-26:
// a subset of the nine names, file_diagnosis always, code_search and the rest
// only when the hands offer them, and never a tool that writes or runs.
// Verifies: 08-REQ-4.3
func TestTheAllowlistIsReadOnlyAndHoldsTheTerminatingTool_TS_08_26(t *testing.T) {
	all := []string{"read_file", "write_file", "edit_file", "list_files", "find_files", "search_files", "file_outline", "find_symbol", "find_references", "execute", "run_command", "code_search"}
	got := Allowlist(all)
	for _, name := range got {
		if !slices.Contains(nineNames, name) {
			t.Errorf("%s is on the allowlist, which has only the nine names", name)
		}
	}
	for _, name := range nineNames {
		if !slices.Contains(got, name) {
			t.Errorf("%s is missing from the allowlist of a toolset that offers everything: %v", name, got)
		}
	}
	for _, name := range []string{"write_file", "edit_file", "execute", "run_command"} {
		if slices.Contains(got, name) {
			t.Errorf("%s is on the allowlist", name)
		}
	}

	fewer := Allowlist([]string{"read_file", "list_files", "find_files", "search_files", "file_outline", "find_symbol", "write_file", "run_command", "unheard_of"})
	for _, absent := range []string{"find_references", "code_search", "write_file", "run_command", "unheard_of"} {
		if slices.Contains(fewer, absent) {
			t.Errorf("%s is on the allowlist of a toolset without it: %v", absent, fewer)
		}
	}
	if !slices.Contains(fewer, "file_diagnosis") || !slices.Contains(fewer, "read_file") {
		t.Errorf("allowlist = %v, want file_diagnosis and read_file", fewer)
	}
	if only := Allowlist(nil); !slices.Equal(only, []string{"file_diagnosis"}) {
		t.Errorf("a toolset that offers nothing: %v, want the terminating tool alone", only)
	}

	box := BoxTools(all)
	if slices.Contains(box, "file_diagnosis") || len(box) != len(got)-1 {
		t.Errorf("BoxTools = %v, want the allowlist without file_diagnosis", box)
	}
}

// inProcessTree is a tree with a.go and a run.json for kind triage, and the
// target of an in-process submit over it.
func inProcessTree(t *testing.T) (dir string, target submission.Target) {
	t.Helper()
	dir = plainTree(t, map[string]string{"a.go": "package a\n\nfunc A() {}\n\nfunc B() {}\n"})
	runDir := filepath.Join(dir, ".agent-fox", "run", testRun)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	run, _ := json.Marshal(submission.Run{RunID: testRun, Kind: KindName, Attempt: 1, Nonce: "0123456789abcdef"})
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), run, 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return dir, submission.Target{Tree: root.FS(), Write: submission.RootWriter(root), RunID: testRun}
}

func diagnosisFor(path string, mutate func(map[string]any)) []byte {
	m := map[string]any{
		"title": "A fails", "severity": "high", "confidence": "probable", "root_cause": "A does the wrong thing.",
		"affected_files": []any{map[string]any{"path": path, "note": "the function"}},
		"suggested_fix":  "Do the right thing.", "acceptance_criteria": []any{"A does the right thing"},
	}
	if mutate != nil {
		mutate(m)
	}
	data, _ := json.Marshal(m)
	return data
}

// TestTheKindMakesFileDiagnosisWriteTheSubmission_TS_08_27 verifies TS-08-27:
// the kind has the tool name and the schema of a diagnosis, and a call with a
// valid diagnosis writes submission.json in the brain's tree, normalized.
// Verifies: 08-REQ-4.4
func TestTheKindMakesFileDiagnosisWriteTheSubmission_TS_08_27(t *testing.T) {
	dir, target := inProcessTree(t)
	kind := Kind(dir)

	if kind.Name != KindName || kind.ToolName != "file_diagnosis" || !bytes.Equal(kind.Schema, Schema()) || kind.Validate == nil || kind.Description == "" {
		t.Fatalf("kind = %+v", kind)
	}
	if _, err := schema.Parse(kind.Schema); err != nil {
		t.Fatalf("the schema of the tool cannot be used by the driver: %v", err)
	}

	receipt, err := submission.Submit(context.Background(), target, kind, diagnosisFor("./a.go:3", nil), time.Now())

	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".agent-fox", "run", testRun, "submission.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Kind    string
		Payload Diagnosis
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if file.Kind != KindName || file.Payload.AffectedFiles[0] != (AffectedFile{Path: "a.go", Line: 3, Note: "the function"}) {
		t.Errorf("submission = %+v, want the normalized path a.go and line 3", file)
	}
	if receipt.Path != ".agent-fox/run/"+testRun+"/submission.json" {
		t.Errorf("receipt = %+v", receipt)
	}
}

// TestADiagnosisSchemaHasTheFieldsOfTheType guards 08-REQ-4.4: the schema that
// the driver shows the model names the fields that Validate reads.
func TestADiagnosisSchemaHasTheFieldsOfTheType(t *testing.T) {
	var s struct {
		Required   []string
		Properties map[string]json.RawMessage
	}
	if err := json.Unmarshal(Schema(), &s); err != nil {
		t.Fatal(err)
	}
	var have []string
	for name := range s.Properties {
		have = append(have, name)
	}
	slices.Sort(have)
	want := slices.Clone(fieldNames)
	slices.Sort(want)
	if !slices.Equal(have, want) {
		t.Errorf("the schema has %v, want %v", have, want)
	}
	if want := []string{"title", "severity", "confidence", "root_cause", "affected_files", "suggested_fix", "acceptance_criteria"}; !slices.Equal(s.Required, want) {
		t.Errorf("required = %v, want %v", s.Required, want)
	}
	// What the schema calls valid, Validate accepts.
	if _, problems := Validate(diagnosisFor("main.go", nil), testTree()); len(problems) != 0 {
		t.Errorf("a diagnosis of the shape of the schema: %v", problems)
	}
}

// TestEveryRefusedCallAppendsOneRejection guards 08-REQ-6.1: through the kind,
// a refused call appends one line beside the submission, an accepted one none,
// and an earlier submission stays as it was.
func TestEveryRefusedCallAppendsOneRejection(t *testing.T) {
	dir, target := inProcessTree(t)
	kind := Kind(dir)
	runDir := filepath.Join(dir, ".agent-fox", "run", testRun)
	submit := func(payload []byte) error {
		_, err := submission.Submit(context.Background(), target, kind, payload, time.Now())
		return err
	}

	if err := submit(diagnosisFor("a.go", nil)); err != nil {
		t.Fatal(err)
	}
	accepted, _ := os.ReadFile(filepath.Join(runDir, "submission.json"))
	if _, err := os.Stat(filepath.Join(runDir, RejectionsFile)); err == nil {
		t.Error("an accepted call left a rejections file")
	}

	if err := submit(diagnosisFor("missing.go", nil)); err == nil {
		t.Fatal("a missing path was accepted")
	}
	if err := submit(diagnosisFor("a.go", func(m map[string]any) { m["severity"] = "urgent" })); err == nil {
		t.Fatal("a bad severity was accepted")
	}
	if err := submit([]byte(`[1]`)); err == nil {
		t.Fatal("an array was accepted")
	}

	pathCalls, total, warning := CountRejections(filepath.Join(runDir, RejectionsFile))
	if pathCalls != 1 || total != 3 || warning != "" {
		t.Errorf("rejections = (%d path calls, %d in all, %q), want (1, 3, none)", pathCalls, total, warning)
	}
	data, _ := os.ReadFile(filepath.Join(runDir, RejectionsFile))
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], `"unresolved_paths":["missing.go"]`) || !strings.Contains(lines[1], `"unresolved_paths":[]`) {
		t.Errorf("rejections.jsonl:\n%s", data)
	}
	if after, _ := os.ReadFile(filepath.Join(runDir, "submission.json")); !bytes.Equal(after, accepted) {
		t.Error("an earlier submission was changed by the refusals")
	}
}

// TestTheTaskBlockIsAdaptedToTheInProcessRoute guards 08-REQ-4.7: the task block
// that af triage brief rendered names the command route and the bounds of the
// brief; for a brain that submits through the tool it names the tool and the
// bounds of this run, and keeps the rest.
func TestTheTaskBlockIsAdaptedToTheInProcessRoute(t *testing.T) {
	dir := goTree(t)
	p := params(dir, issueReport("body"))
	p.Context = []string{"B"}
	p.PreviousQuestion = &Question{Text: "Which?", Options: []string{"x", "y"}}
	task := build(t, p).Task
	bounds := Bounds{MaxTurns: 7, MaxCostUSD: 0.5, Timeout: 90 * time.Second}

	adapted := AdaptTask(task, testRun, bounds)

	p.InProcess, p.Bounds = true, bounds
	if want := build(t, p).Task; adapted != want {
		t.Errorf("adapted task block:\n%s\nwant the block rendered for an in-process brain:\n%s", adapted, want)
	}
	if strings.Contains(adapted, "af triage submit") || !strings.Contains(adapted, "file_diagnosis") {
		t.Errorf("the adapted block names the wrong route:\n%s", adapted)
	}
	if again := AdaptTask(adapted, testRun, bounds); again != adapted {
		t.Error("adapting a block that is adapted already changes it")
	}
}

// TestAPathInTheWorkspaceOfTheBoxIsAPathOfTheRepository guards 08-REQ-5.4 for the
// kind of a box: the workspace is another root of the tree, also under the name a
// link gives it, and a path under neither is refused.
func TestAPathInTheWorkspaceOfTheBoxIsAPathOfTheRepository(t *testing.T) {
	dir, target := inProcessTree(t)
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(workspace, link); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	kind := Kind(dir, link)

	for _, cited := range []string{workspace + "/a.go", link + "/a.go", dir + "/a.go"} {
		if _, problems := kind.Validate(context.Background(), target.Tree, diagnosisFor(cited, nil)); len(problems) != 0 {
			t.Errorf("%s: problems = %v, want the path taken as a.go", cited, problems)
		}
	}
	if _, problems := kind.Validate(context.Background(), target.Tree, diagnosisFor(filepath.Join(t.TempDir(), "a.go"), nil)); len(problems) != 1 {
		t.Errorf("a path under no root: problems = %v, want one", problems)
	}
}
