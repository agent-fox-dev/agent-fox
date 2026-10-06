package specgen

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-13-34 (smoke): A spec run reports per-phase tool-call counts in the
// envelope and event stream.
//
// Verifies: 13-PATH-1, 13-REQ-1.3, 13-REQ-2.3, 13-REQ-3.5
//
// Real components: agentrun.Runner (via the pipeline's recordPhase →
// PhaseFromResult → Run.AddPhase path), toolio.PhaseFromResult,
// toolio.Progress, specgen pipeline.
//
// The scriptedAuthor returns agentrun.Result with ToolCalls and
// ToolResultBytes populated, simulating what the real Runner produces. The
// real pipeline threads them through PhaseFromResult and into the envelope.
func TestTS_13_34_SpecRunReportsToolCallCountsInEnvelopeAndEvents(t *testing.T) {
	ws := newWorkspace(t)

	// Override the scriptedAuthor to return Results with ToolCalls populated.
	a := newAuthor(t, "01", "test_feature")
	wrapper := &toolCallsAuthor{inner: a}

	run := toolio.NewRun("spec", "test")
	o := newOptions(ws, wrapper)
	o.Run = run

	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !got.Validation.Valid {
		t.Fatalf("package does not validate: %+v", got.Validation.Errors)
	}

	// Verify the envelope's usage.phases[] entries carry tool_calls and
	// tool_result_bytes.
	envelope := run.Envelope(0, got, nil)
	if envelope.Usage == nil {
		t.Fatal("envelope.Usage is nil")
	}
	phases := envelope.Usage.Phases
	if len(phases) == 0 {
		t.Fatal("no phases recorded in the envelope")
	}

	// Every phase should have non-empty ToolCalls and ToolResultBytes.
	for _, p := range phases {
		if len(p.ToolCalls) == 0 {
			t.Errorf("phase %q has empty ToolCalls in envelope", p.Name)
		}
		if len(p.ToolResultBytes) == 0 {
			t.Errorf("phase %q has empty ToolResultBytes in envelope", p.Name)
		}
	}

	// Verify the prd phase's ToolCalls match what the author returned.
	prdPhase := phases[0]
	if prdPhase.Name != "prd" {
		t.Fatalf("first phase name = %q, want %q", prdPhase.Name, "prd")
	}
	if prdPhase.ToolCalls["read_file"] != 2 {
		t.Errorf("prd phase ToolCalls[read_file] = %d, want 2", prdPhase.ToolCalls["read_file"])
	}
	if prdPhase.ToolCalls["list_files"] != 1 {
		t.Errorf("prd phase ToolCalls[list_files] = %d, want 1", prdPhase.ToolCalls["list_files"])
	}

	// Verify a generation phase's ToolCalls.
	genPhase := phases[1]
	if genPhase.ToolCalls["read_file"] != 3 {
		t.Errorf("generation phase %q ToolCalls[read_file] = %d, want 3", genPhase.Name, genPhase.ToolCalls["read_file"])
	}

	// Verify the PhaseInfo JSON contains the tool_calls and tool_result_bytes
	// keys when marshalled.
	raw, err := json.Marshal(prdPhase)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"tool_calls"`) {
		t.Error("PhaseInfo JSON does not contain tool_calls key")
	}
	if !strings.Contains(string(raw), `"tool_result_bytes"`) {
		t.Error("PhaseInfo JSON does not contain tool_result_bytes key")
	}
}

// toolCallsAuthor wraps a scriptedAuthor to return Results with ToolCalls
// and ToolResultBytes populated, simulating what the real Runner produces.
type toolCallsAuthor struct {
	inner *scriptedAuthor
}

func (a *toolCallsAuthor) WritePRD(ctx context.Context, req prdRequest) (PRD, agentrun.Result, error) {
	prd, res, err := a.inner.WritePRD(ctx, req)
	res.ToolCalls = map[string]int{"read_file": 2, "list_files": 1, "submit_prd": 1}
	res.ToolResultBytes = map[string]int64{"read_file": 4096, "list_files": 256, "submit_prd": 64}
	return prd, res, err
}

func (a *toolCallsAuthor) GenerateArtifact(ctx context.Context, req artifactRequest) (map[string]any, agentrun.Result, error) {
	content, res, err := a.inner.GenerateArtifact(ctx, req)
	res.ToolCalls = map[string]int{"read_file": 3, "search_files": 1, "submit_" + string(req.Step): 1}
	res.ToolResultBytes = map[string]int64{"read_file": 8192, "search_files": 512, "submit_" + string(req.Step): 128}
	return content, res, err
}

func (a *toolCallsAuthor) WriteArchitecture(ctx context.Context, req architectureRequest) (string, agentrun.Result, error) {
	return a.inner.WriteArchitecture(ctx, req)
}

// TS-13-35 (smoke): A spec run passes relevant_files from the PRD phase to
// generation phases and into the envelope.
//
// Verifies: 13-PATH-2, 13-REQ-4.4, 13-REQ-5.1, 13-REQ-6.1
//
// Real components: specgen pipeline, submit_prd handler, generation prompt
// builder, specgen.Package.
func TestTS_13_35_SpecRunPassesRelevantFilesFromPRDToGenerationAndEnvelope(t *testing.T) {
	ws := newWorkspace(t)
	// Create real files in the workspace so path validation succeeds.
	for _, name := range []string{"internal/phase.go", "internal/envelope.go"} {
		dir := filepath.Join(ws.Root, filepath.Dir(name))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ws.Root, name), []byte("package internal\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	a := newAuthor(t, "01", "test_feature")
	a.prd.RelevantFiles = []RelevantFile{
		{Path: "internal/phase.go", Why: "the phase runner"},
		{Path: "internal/envelope.go", Why: "the envelope"},
	}

	// Capture artifact requests to verify the relevant-files block.
	wrapper := &relevantFilesCapture{inner: a}

	o := newOptions(ws, wrapper)
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 1. Verify the generation phase prompts contain the relevant-files block.
	// The wrapper captures artifact requests; each should carry RelevantFiles.
	if len(wrapper.artifactReqs) == 0 {
		t.Fatal("no artifact requests captured")
	}
	for _, req := range wrapper.artifactReqs {
		if len(req.RelevantFiles) != 2 {
			t.Errorf("step %s: RelevantFiles has %d entries, want 2", req.Step, len(req.RelevantFiles))
		}
		// Verify the block would be rendered.
		block := relevantFilesBlock(req.RelevantFiles)
		if !strings.Contains(block, "## Files the PRD phase found relevant") {
			t.Errorf("step %s: relevant-files block missing heading", req.Step)
		}
		if !strings.Contains(block, "`internal/phase.go`") {
			t.Errorf("step %s: relevant-files block missing phase.go entry", req.Step)
		}
	}

	// 2. Verify the envelope's result.relevant_files is populated.
	if len(got.RelevantFiles) != 2 {
		t.Errorf("Package.RelevantFiles has %d entries, want 2", len(got.RelevantFiles))
	}
	if got.RelevantFiles[0].Path != "internal/phase.go" {
		t.Errorf("RelevantFiles[0].Path = %q, want %q", got.RelevantFiles[0].Path, "internal/phase.go")
	}
	if got.RelevantFiles[1].Path != "internal/envelope.go" {
		t.Errorf("RelevantFiles[1].Path = %q, want %q", got.RelevantFiles[1].Path, "internal/envelope.go")
	}

	// 3. Verify the JSON envelope carries relevant_files.
	raw, err := json.Marshal(got.Package)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"relevant_files"`) {
		t.Error("JSON envelope does not contain relevant_files key")
	}
}

// TS-13-36 (smoke): A resumed split run warns about unavailable relevant
// files.
//
// Verifies: 13-PATH-3
//
// Real components: specgen pipeline, toolio.Run, generation prompt builder.
func TestTS_13_36_ResumedSplitWarnsAboutUnavailableRelevantFiles(t *testing.T) {
	ws := newWorkspace(t)

	// First run: write scope 1, fail on scope 2 to leave a plan.
	a := splitAuthor(t)
	a.prd.RelevantFiles = []RelevantFile{{Path: "a.go", Why: "scope 1 file"}}
	a.followOnErr["widget_github"] = failf("prd", "budget", "budget exceeded")

	_, err := Run(context.Background(), fileOptions(ws, a))
	if err == nil {
		t.Fatal("expected an error from the first run")
	}

	// Second run: resume from the plan. The PRD phase does not run, so
	// relevant_files are unavailable.
	b := splitAuthor(t)
	run := toolio.NewRun("spec", "test")
	o := fileOptions(ws, b)
	o.Run = run

	// Capture artifact requests to verify the relevant-files block is absent.
	wrapper := &relevantFilesCapture{inner: b}
	o.author = wrapper

	_, err = Run(context.Background(), o)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}

	// 1. Check for the warning with code relevant_files_unavailable.
	warnings := run.Warnings()
	var found bool
	for _, w := range warnings {
		if w.Code == toolio.WarnRelevantFilesUnavailable {
			found = true
			if w.Severity != "low" {
				t.Errorf("severity = %q, want %q", w.Severity, "low")
			}
		}
	}
	if !found {
		t.Errorf("expected a warning with code %q; warnings = %+v",
			toolio.WarnRelevantFilesUnavailable, warnings)
	}

	// 2. Verify the generation prompts for the resumed scope do NOT contain
	// the relevant-files block (because no relevant_files were available).
	for _, req := range wrapper.artifactReqs {
		if len(req.RelevantFiles) != 0 {
			t.Errorf("step %s for %s: RelevantFiles has %d entries, want 0 (resumed scope)",
				req.Step, req.SpecName, len(req.RelevantFiles))
		}
	}

	// 3. Verify the warning appears in the envelope.
	envelope := run.Envelope(0, nil, nil)
	var envelopeWarningFound bool
	for _, w := range envelope.Warnings {
		if w.Code == toolio.WarnRelevantFilesUnavailable {
			envelopeWarningFound = true
		}
	}
	if !envelopeWarningFound {
		t.Error("expected relevant_files_unavailable warning in envelope.Warnings")
	}
}

// TS-13-37 (smoke): submit_prd rejects invalid paths in relevant_files and
// the model can correct and resubmit.
//
// Verifies: 13-PATH-4, 13-REQ-5.2
//
// Real components: submit_prd handler, agentrun.Runner, tools.Workspace.
func TestTS_13_37_SubmitPRDRejectsInvalidPathsAndModelCorrects(t *testing.T) {
	ws := goWorkspace(t)
	// Create a real file that the corrected submission will reference.
	if err := os.WriteFile(filepath.Join(ws.Root, "real.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Build the PRD args with a nonexistent path in relevant_files.
	badArgs := map[string]any{
		"spec_name": "test_feature",
		"title":     "Test Feature",
		"body": "## Intent\n\nTest.\n\n## Goals\n\n- Test.\n\n## Non-goals\n\n- None.\n\n" +
			"## Background\n\nNone.\n\n## Requirements\n\n### REQ-1\n\nDo the thing.\n\n" +
			"## Design Decisions\n\n1. None.\n",
		"relevant_files": []map[string]any{
			{"path": "nonexistent.go", "why": "does not exist"},
		},
	}

	// Build the corrected PRD args with a valid path.
	goodArgs := map[string]any{
		"spec_name": "test_feature",
		"title":     "Test Feature",
		"body": "## Intent\n\nTest.\n\n## Goals\n\n- Test.\n\n## Non-goals\n\n- None.\n\n" +
			"## Background\n\nNone.\n\n## Requirements\n\n### REQ-1\n\nDo the thing.\n\n" +
			"## Design Decisions\n\n1. None.\n",
		"relevant_files": []map[string]any{
			{"path": "real.go", "why": "the main file"},
		},
	}

	// Script the faux provider: first call submit_prd with bad path, then
	// with corrected path.
	runner, _ := fauxRunner(t, ws,
		toolCall("c1", ToolSubmitPRD, badArgs),
		toolCall("c2", ToolSubmitPRD, goodArgs),
	)

	a := &agentAuthor{runner: runner, ws: ws}
	prd, res, err := a.WritePRD(context.Background(), prdRequest{
		Root:       ws.Root,
		SourceKind: "text",
		Input:      "test input",
	})
	if err != nil {
		t.Fatalf("WritePRD: %v", err)
	}

	// The first call should have been rejected (error result), the second
	// accepted. The model should have taken at least 2 turns.
	if res.Turns < 2 {
		t.Errorf("Turns = %d, want at least 2 (rejection + correction)", res.Turns)
	}

	// The PRD should have the corrected relevant_files.
	if len(prd.RelevantFiles) != 1 {
		t.Fatalf("RelevantFiles has %d entries, want 1", len(prd.RelevantFiles))
	}
	if prd.RelevantFiles[0].Path != "real.go" {
		t.Errorf("RelevantFiles[0].Path = %q, want %q", prd.RelevantFiles[0].Path, "real.go")
	}

	// Verify the error result was consumed by the runner's event loop
	// (the ToolErrors map should have an entry for submit_prd).
	if res.ToolErrors == nil {
		t.Fatal("ToolErrors is nil, expected an entry for the rejected call")
	}
	if res.ToolErrors["submit_prd/invalid_relevant_files"] != 1 {
		t.Errorf("ToolErrors = %v, want submit_prd/invalid_relevant_files:1", res.ToolErrors)
	}

	// Verify the error result was counted in ToolCalls (both calls counted).
	if res.ToolCalls["submit_prd"] != 2 {
		t.Errorf("ToolCalls[submit_prd] = %d, want 2", res.ToolCalls["submit_prd"])
	}
}
