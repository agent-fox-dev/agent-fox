package triage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/agent-fox-dev/agentkit-go/schema"

	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// The allowlist is the read-only tools the hands offer and file_diagnosis,
// never a tool that writes or runs; the tools of a box are the same without
// file_diagnosis.
func TestAllowlist_IsTheReadOnlyToolsOfferedAndTheTerminatingTool(t *testing.T) {
	readOnly := []string{"read_file", "list_files", "find_files", "search_files", "file_outline", "find_symbol", "find_references", "code_search"}
	for _, tc := range []struct {
		offered, want []string
	}{
		{append([]string{"write_file", "edit_file", "execute", "run_command"}, readOnly...), append(slices.Clone(readOnly), ToolName)},
		{[]string{"find_symbol", "read_file", "write_file", "unheard_of"}, []string{"read_file", "find_symbol", ToolName}},
		{nil, []string{ToolName}},
	} {
		if got := Allowlist(tc.offered); !slices.Equal(got, tc.want) {
			t.Errorf("Allowlist(%v) = %v, want %v", tc.offered, got, tc.want)
		}
		if got := BoxTools(tc.offered); !slices.Equal(got, tc.want[:len(tc.want)-1]) {
			t.Errorf("BoxTools(%v) = %v, want the allowlist without %s", tc.offered, got, ToolName)
		}
	}
}

// The tools a brief allows before a box says what it offers are the read-only
// ones, in a list the caller may change.
func TestBriefTools_AreTheReadOnlyToolsAndACopy(t *testing.T) {
	tools := BriefTools()
	if got := BoxTools(tools); !slices.Equal(got, tools) || slices.Contains(tools, ToolName) || slices.Contains(tools, "write_file") {
		t.Errorf("BriefTools = %v, want every read-only tool and nothing else", tools)
	}
	tools[0] = "changed"
	if BriefTools()[0] == "changed" {
		t.Error("BriefTools returned the list it keeps, not a copy")
	}
}

// inProcessTree is a checkout with a.go, whose last line has no newline, and a
// run.json for kind triage, and the target of an in-process submit over it.
func inProcessTree(t *testing.T) (dir string, target submission.Target) {
	t.Helper()
	dir = plainTree(t, map[string]string{"a.go": "package a\n\nfunc A() {}\n\nfunc B() {}"})
	run := testutil.JSON(t, submission.Run{RunID: testRun, Kind: KindName, Attempt: 1, Nonce: "0123456789abcdef"})
	testutil.WriteFile(t, dir, ".agent-fox/run/"+testRun+"/run.json", string(run))
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return dir, submission.Target{Tree: root.FS(), Write: submission.RootWriter(root), RunID: testRun}
}

// A call of file_diagnosis with a valid diagnosis writes submission.json in
// the brain's tree, in the normalized form.
func TestKind_FileDiagnosisWritesTheNormalizedSubmission(t *testing.T) {
	dir, target := inProcessTree(t)
	kind := Kind(dir)
	if kind.Name != KindName || kind.ToolName != ToolName || kind.Description == "" {
		t.Fatalf("kind = %+v", kind)
	}
	if _, err := schema.Parse(kind.Schema); err != nil {
		t.Fatalf("the driver cannot use the schema of the tool: %v", err)
	}

	receipt, err := submission.Submit(context.Background(), target, kind, citing(t, "./a.go:5"), time.Now())

	if err != nil {
		t.Fatal(err)
	}
	file := testutil.Decode[struct {
		Kind    string
		Payload Diagnosis
	}](t, []byte(testutil.ReadFile(t, filepath.Join(dir, ".agent-fox", "run", testRun, "submission.json"))))
	if file.Kind != KindName || file.Payload.AffectedFiles[0] != (AffectedFile{Path: "a.go", Line: 5, Note: "the function"}) {
		t.Errorf("submission = %+v, want the normalized path a.go and line 5", file)
	}
	if receipt.Path != ".agent-fox/run/"+testRun+"/submission.json" {
		t.Errorf("receipt = %+v", receipt)
	}
}

// The kind refuses a diagnosis with the problems of the validator, the lines
// counted in the tree the submission is made in, and refuses all when the
// checkout cannot be listed.
func TestKind_RefusesWhatDoesNotHoldInTheTree(t *testing.T) {
	dir, target := inProcessTree(t)
	for name, tc := range map[string]struct {
		kind          submission.Kind
		tree          fstest.MapFS
		payload       []byte
		pointer, text string
	}{
		"a line past the end":       {Kind(dir), nil, citing(t, "a.go:6"), "/affected_files/0/line", "is 6, and a.go has 5 lines"},
		"a file the box has not":    {Kind(dir), fstest.MapFS{}, citing(t, "a.go:1"), "/affected_files/0/line", "cannot be checked"},
		"a payload that is no JSON": {Kind(dir), nil, []byte("{"), "", "the payload is not valid JSON"},
		"no checkout":               {Kind(filepath.Join(dir, "missing")), nil, citing(t, "a.go"), "", "the files of the repository could not be listed"},
	} {
		tree := target.Tree
		if tc.tree != nil {
			tree = tc.tree
		}

		normalized, problems := tc.kind.Validate(context.Background(), tree, tc.payload)

		if normalized != nil || len(problems) != 1 || problems[0].Path != tc.pointer || !strings.HasPrefix(problems[0].Message, tc.text) {
			t.Errorf("%s: %s, %+v, want the problem %s: %s", name, normalized, problems, tc.pointer, tc.text)
		}
	}
}

// A call of file_diagnosis that is refused writes nothing and answers with
// every problem at its pointer, which the brain reads on its next turn; the
// call that corrects them is accepted.
func TestKind_ARefusedCallAnswersWithEveryProblem(t *testing.T) {
	dir, target := inProcessTree(t)
	kind := Kind(dir)
	bad := payloadOf(t, with(func(m map[string]any) {
		m["severity"] = "urgent"
		m["affected_files"] = []any{map[string]any{"path": "missing.go", "note": "n"}}
	}))

	_, err := submission.Submit(context.Background(), target, kind, bad, time.Now())

	var refused *submission.Refused
	if !errors.As(err, &refused) || len(refused.Problems) != 2 || refused.Problems[0].Path != "/affected_files/0/path" || refused.Problems[1].Path != "/severity" ||
		!strings.Contains(err.Error(), `unresolved path "missing.go"`) {
		t.Fatalf("err = %v, want both problems, at their pointers", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".agent-fox", "run", testRun, "submission.json")); err == nil {
		t.Error("a refused call wrote submission.json")
	}
	if _, err := submission.Submit(context.Background(), target, kind, citing(t, "a.go"), time.Now()); err != nil {
		t.Errorf("the corrected call: %v", err)
	}
}

// A cited link is a file of the repository only when it leads to one inside
// the checkout.
func TestKind_ACitedLinkMustLeadToAFileInsideTheCheckout(t *testing.T) {
	dir, target := inProcessTree(t)
	outside := testutil.WriteFile(t, t.TempDir(), "outside.go", "package x\n")
	for name, to := range map[string]string{"inside.go": "a.go", "outside.go": outside, "dir": "."} {
		if err := os.Symlink(to, filepath.Join(dir, name)); err != nil {
			t.Skipf("no symbolic links here: %v", err)
		}
	}
	for cited, ok := range map[string]bool{"inside.go": true, "outside.go": false, "dir": false, dir + "/inside.go": true, outside: false} {
		_, problems := Kind(dir).Validate(context.Background(), target.Tree, citing(t, cited))
		if ok != (len(problems) == 0) || (!ok && (len(problems) != 1 || problems[0].Path != "/affected_files/0/path")) {
			t.Errorf("%s: problems = %+v, want accepted %v", cited, problems, ok)
		}
	}
}

// The workspace of a box is another root of the tree, as is the checkout under
// the name a link gives it; a path under no root is refused.
func TestKind_APathUnderAnyRootOfTheTreeIsAPathOfTheRepository(t *testing.T) {
	dir, target := inProcessTree(t)
	links := t.TempDir()
	workspace := t.TempDir()
	for name, to := range map[string]string{"checkout": dir, "workspace": workspace} {
		if err := os.Symlink(to, filepath.Join(links, name)); err != nil {
			t.Skipf("no symbolic links here: %v", err)
		}
	}
	kind := Kind(filepath.Join(links, "checkout"), filepath.Join(links, "workspace"))

	for _, root := range []string{dir, filepath.Join(links, "checkout"), workspace, filepath.Join(links, "workspace")} {
		if _, problems := kind.Validate(context.Background(), target.Tree, citing(t, root+"/a.go")); len(problems) != 0 {
			t.Errorf("%s/a.go: problems = %v, want the path taken as a.go", root, problems)
		}
	}
	if _, problems := kind.Validate(context.Background(), target.Tree, citing(t, filepath.Join(t.TempDir(), "a.go"))); len(problems) != 1 {
		t.Errorf("a path under no root: problems = %v, want one", problems)
	}
}

// The task block placed in a box gets the bounds of the run about to start,
// and keeps the rest.
func TestAdaptTask_GivesTheTaskBlockTheBoundsOfTheRun(t *testing.T) {
	p := params(goTree(t), issueReport("body"))
	p.Context = []string{"B"}
	p.PreviousQuestion = &Question{Text: "Which?", Options: []string{"x", "y"}}
	task := build(t, p).Task
	p.Bounds = Bounds{MaxTurns: 7, MaxCostUSD: 0.5, Timeout: 90 * time.Second}

	adapted := AdaptTask(task, p.Bounds)

	if want := build(t, p).Task; adapted != want {
		t.Errorf("adapted task block:\n%s\nwant the block rendered with the bounds:\n%s", adapted, want)
	}
	if AdaptTask(adapted, p.Bounds) != adapted || AdaptTask("no bounds here", p.Bounds) != "no bounds here" {
		t.Error("adapting a block that is adapted already, or that has no bounds, changes it")
	}
}
