package specgen

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/tools"
)

// TS-13-14: submit_prd tool schema includes an optional relevant_files array
// with path and why fields.
func TestTS_13_14_SchemaIncludesRelevantFiles(t *testing.T) {
	s := prdSchema()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)

	// The schema must contain relevant_files as a property.
	if !strings.Contains(text, `"relevant_files"`) {
		t.Fatal("schema does not contain relevant_files property")
	}
	// relevant_files must NOT be in the required list (it is optional).
	if strings.Contains(text, `"required"`) {
		// Parse and check that relevant_files is not in required.
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatal(err)
		}
		if req, ok := obj["required"].([]any); ok {
			for _, r := range req {
				if r == "relevant_files" {
					t.Fatal("relevant_files must be optional, but it is in the required list")
				}
			}
		}
	}
	// Items must have path and why properties.
	if !strings.Contains(text, `"path"`) {
		t.Fatal("relevant_files items must have a path property")
	}
	if !strings.Contains(text, `"why"`) {
		t.Fatal("relevant_files items must have a why property")
	}
}

// TS-13-15: submit_prd schema enforces maxItems of 30 on relevant_files.
func TestTS_13_15_SchemaMaxItems30(t *testing.T) {
	s := prdSchema()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	// The limit must sit on the relevant_files array itself, not merely appear
	// somewhere in the schema.
	var doc struct {
		Properties map[string]struct {
			MaxItems *int `json:"maxItems"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	rf, ok := doc.Properties["relevant_files"]
	if !ok {
		t.Fatalf("schema has no relevant_files property; got %s", string(raw))
	}
	if rf.MaxItems == nil {
		t.Fatalf("relevant_files has no maxItems, want 30; got %s", string(raw))
	}
	if *rf.MaxItems != 30 {
		t.Fatalf("relevant_files maxItems = %d, want 30", *rf.MaxItems)
	}
}

// TS-13-16: PRD struct carries RelevantFiles field of type []RelevantFile.
func TestTS_13_16_PRDStructRelevantFiles(t *testing.T) {
	prd := PRD{
		RelevantFiles: []RelevantFile{
			{Path: "internal/agentrun/phase.go", Why: "phase runner"},
		},
	}
	if len(prd.RelevantFiles) != 1 {
		t.Fatalf("expected 1 RelevantFile, got %d", len(prd.RelevantFiles))
	}
	if prd.RelevantFiles[0].Path != "internal/agentrun/phase.go" {
		t.Errorf("Path = %q", prd.RelevantFiles[0].Path)
	}
	if prd.RelevantFiles[0].Why != "phase runner" {
		t.Errorf("Why = %q", prd.RelevantFiles[0].Why)
	}
}

// TS-13-17: prdSink stores validated relevant_files alongside the rest of the PRD.
func TestTS_13_17_SinkStoresRelevantFiles(t *testing.T) {
	sink := &prdSink{}
	prd := PRD{
		SpecName: "test",
		Title:    "Test",
		Body:     "## Intent\n\nTest.\n",
		RelevantFiles: []RelevantFile{
			{Path: "a.go", Why: "reason"},
		},
	}
	sink.set(prd)
	got, ok := sink.get()
	if !ok {
		t.Fatal("sink.get() returned false")
	}
	if len(got.RelevantFiles) != 1 {
		t.Fatalf("expected 1 RelevantFile, got %d", len(got.RelevantFiles))
	}
	if got.RelevantFiles[0].Path != "a.go" {
		t.Errorf("Path = %q", got.RelevantFiles[0].Path)
	}
}

func testWorkspace(t *testing.T) *tools.Workspace {
	t.Helper()
	dir := t.TempDir()
	ws, err := tools.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func validPRDArgsWithRelevantFiles(paths ...string) map[string]any {
	args := validPRDArgs()
	var rf []map[string]any
	for _, p := range paths {
		rf = append(rf, map[string]any{"path": p, "why": "relevant for testing"})
	}
	args["relevant_files"] = rf
	return args
}

// TS-13-18: submit_prd handler resolves every path in relevant_files with
// Workspace.Resolve and os.Stat.
func TestTS_13_18_ValidPathsAccepted(t *testing.T) {
	ws := testWorkspace(t)
	// Create two real files.
	for _, name := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(ws.Root, name), []byte("package a\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var sink prdSink
	tool := submitPRDTool(&sink, ws)
	raw, err := json.Marshal(validPRDArgsWithRelevantFiles("a.go", "b.go"))
	if err != nil {
		t.Fatal(err)
	}
	res := tool.Execute(context.Background(), raw)
	if !res.OK {
		t.Fatalf("expected acceptance, got error: %s %s", res.Error, res.Detail)
	}
	prd, ok := sink.get()
	if !ok {
		t.Fatal("sink.get() returned false")
	}
	if len(prd.RelevantFiles) != 2 {
		t.Fatalf("expected 2 RelevantFiles, got %d", len(prd.RelevantFiles))
	}
}

// TS-13-19: submit_prd rejects submission when relevant_files contains a
// nonexistent path with error code invalid_relevant_files.
func TestTS_13_19_NonexistentPathRejected(t *testing.T) {
	ws := testWorkspace(t)
	if err := os.WriteFile(filepath.Join(ws.Root, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var sink prdSink
	tool := submitPRDTool(&sink, ws)
	raw, err := json.Marshal(validPRDArgsWithRelevantFiles("a.go", "nonexistent.go"))
	if err != nil {
		t.Fatal(err)
	}
	res := tool.Execute(context.Background(), raw)
	if res.OK {
		t.Fatal("expected rejection, got acceptance")
	}
	msg := res.Error + " " + res.Detail
	if !strings.Contains(msg, "invalid_relevant_files") {
		t.Errorf("expected error code invalid_relevant_files, got %q", msg)
	}
	if !strings.Contains(msg, "nonexistent.go") {
		t.Errorf("expected error message to name nonexistent.go, got %q", msg)
	}
	if _, ok := sink.get(); ok {
		t.Error("a rejected submission must not be recorded")
	}
}

// TS-13-20: submit_prd rejects submission when relevant_files contains a
// directory path.
func TestTS_13_20_DirectoryPathRejected(t *testing.T) {
	ws := testWorkspace(t)
	if err := os.MkdirAll(filepath.Join(ws.Root, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	var sink prdSink
	tool := submitPRDTool(&sink, ws)
	raw, err := json.Marshal(validPRDArgsWithRelevantFiles("subdir"))
	if err != nil {
		t.Fatal(err)
	}
	res := tool.Execute(context.Background(), raw)
	if res.OK {
		t.Fatal("expected rejection, got acceptance")
	}
	msg := res.Error + " " + res.Detail
	if !strings.Contains(msg, "subdir") {
		t.Errorf("expected error message to name subdir, got %q", msg)
	}
	if _, ok := sink.get(); ok {
		t.Error("a rejected submission must not be recorded")
	}
}

// TS-13-21: submit_prd accepts submission without relevant_files or with an
// empty array.
func TestTS_13_21_NoRelevantFilesAccepted(t *testing.T) {
	ws := testWorkspace(t)
	var sink prdSink
	tool := submitPRDTool(&sink, ws)
	// No relevant_files at all.
	raw, err := json.Marshal(validPRDArgs())
	if err != nil {
		t.Fatal(err)
	}
	res := tool.Execute(context.Background(), raw)
	if !res.OK {
		t.Fatalf("expected acceptance, got error: %s %s", res.Error, res.Detail)
	}
	prd, ok := sink.get()
	if !ok {
		t.Fatal("sink.get() returned false")
	}
	if len(prd.RelevantFiles) != 0 {
		t.Errorf("expected 0 RelevantFiles, got %d", len(prd.RelevantFiles))
	}
}
