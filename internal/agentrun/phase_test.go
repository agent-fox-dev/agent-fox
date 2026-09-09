package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/schema"
	"github.com/agentfox/agentkit-go/tools"
)

// The tests below script a provider rather than mocking this package.
// provider/faux sits where a vendor sits, so they drive the REAL loop — prompt
// assembly, tool declaration, the handler, the stop policy — with no key, no
// network and no double of this package's own types.

func fauxConfig(p *faux.Provider, ws *tools.Workspace) Config {
	return Config{
		Model:         faux.Model(),
		Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
		Workspace:     ws,
		Bounds:        Bounds{MaxTurns: 6, MaxBudgetUSD: 1, MaxAttempts: 1},
		SessionPrefix: "test",
	}
}

func toolCallTurn(id, name string, args any) faux.Turn {
	raw, err := json.Marshal(args)
	if err != nil {
		panic(err)
	}
	return faux.Turn{
		Blocks:     []core.ContentBlock{faux.FauxToolCall(id, name, string(raw))},
		StopReason: core.StopReasonToolUse,
	}
}

func textTurn(s string) faux.Turn {
	return faux.Turn{Blocks: []core.ContentBlock{faux.FauxText(s)}, StopReason: core.StopReasonStop}
}

// submitTool is a terminating tool that records what it was called with and
// rejects an empty value, so a test can exercise the repair path.
func submitTool(got *string, calls *int) core.Tool {
	return core.Tool{
		Name:        "submit",
		Description: "submit the answer",
		InputSchema: schema.Object(schema.Prop("value", schema.String("the answer"))),
		Execute: func(_ context.Context, in json.RawMessage) core.ToolResult {
			*calls++
			var payload struct {
				Value string `json:"value"`
			}
			if err := json.Unmarshal(in, &payload); err != nil {
				return core.ErrResult("invalid_arguments", err.Error())
			}
			if payload.Value == "" {
				return core.ErrResult("empty_value", "value is empty; submit the answer")
			}
			*got = payload.Value
			res := core.OKResult(map[string]any{"accepted": true})
			res.Terminate = true
			return res
		},
	}
}

func newWorkspace(t *testing.T) *tools.Workspace {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := tools.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestRunReachesTheTerminator(t *testing.T) {
	var got string
	var calls int
	p := faux.New(toolCallTurn("c1", "submit", map[string]any{"value": "done"}))

	r, err := NewRunner(fauxConfig(p, newWorkspace(t)))
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Run(context.Background(), Phase{
		Name:       "phase",
		System:     "system",
		User:       "user",
		Terminator: "submit",
		Custom:     []core.Tool{submitTool(&got, &calls)},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "done" {
		t.Errorf("value = %q", got)
	}
	if res.StopReason != core.RunStopToolTerminate && res.StopReason != core.RunStopPolicy {
		t.Errorf("StopReason = %s, want the terminator to end the run", res.StopReason)
	}
}

// A rejection is not a crash: the loop appends it to the transcript and the
// model corrects itself on the next turn. That is the whole repair mechanism,
// and it is the loop's rather than this package's.
func TestRejectedSubmissionIsRepairedInTheLoop(t *testing.T) {
	var got string
	var calls int
	p := faux.New(
		toolCallTurn("c1", "submit", map[string]any{"value": ""}),
		toolCallTurn("c2", "submit", map[string]any{"value": "corrected"}),
	)

	r, err := NewRunner(fauxConfig(p, newWorkspace(t)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), Phase{
		Name: "phase", System: "s", User: "u", Terminator: "submit",
		Custom: []core.Tool{submitTool(&got, &calls)},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 2 {
		t.Errorf("submit calls = %d, want 2 (one rejected, one accepted)", calls)
	}
	if got != "corrected" {
		t.Errorf("value = %q", got)
	}
}

// A run that answers in prose produced no result, and the caller must be able
// to tell that apart from a finished phase.
func TestRunWithoutATerminatorIsNotAResult(t *testing.T) {
	var got string
	var calls int
	p := faux.New(textTurn("I think the problem is somewhere in the parser."))

	r, err := NewRunner(fauxConfig(p, newWorkspace(t)))
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Run(context.Background(), Phase{
		Name: "phase", System: "s", User: "u", Terminator: "submit",
		Custom: []core.Tool{submitTool(&got, &calls)},
	})
	if err != nil {
		t.Fatalf("Run returned an error for a clean run: %v", err)
	}
	if calls != 0 {
		t.Errorf("submit was called %d times", calls)
	}
	// The caller turns "no result" into an error naming the stop reason.
	err = NoResultError("phase", "submit", res)
	var ae *Error
	if !errors.As(err, &ae) || ae.Category() != CategoryNoResult {
		t.Fatalf("NoResultError = %v (%T)", err, err)
	}
	if !strings.Contains(err.Error(), "submit") {
		t.Errorf("the error should name the tool that was never called: %v", err)
	}
}

// The read-only mandate is the resolved tool set. A phase that asks for
// read-only built-ins gets no shell and no write tools, and the invariant is
// checked before the first request.
func TestReadOnlyPhaseResolvesNoMutatingTool(t *testing.T) {
	p := faux.New(toolCallTurn("c1", "submit", map[string]any{"value": "x"}))
	ws := newWorkspace(t)

	var got string
	var calls int
	r, err := NewRunner(fauxConfig(p, ws))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), Phase{
		Name: "read", System: "s", User: "u", Terminator: "submit",
		Custom: []core.Tool{submitTool(&got, &calls)}, BuiltinTools: ReadOnlyFileTools,
		ReadOnly: true,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The request the loop actually sent is the evidence.
	reqs := p.Requests()
	if len(reqs) == 0 {
		t.Fatal("no request reached the wire")
	}
	declared := map[string]bool{}
	for _, tool := range reqs[0].Tools {
		declared[tool.Name] = true
	}
	for _, banned := range MutatingTools {
		if declared[banned] {
			t.Errorf("%s was declared to the model in a read-only phase", banned)
		}
	}
	for _, wanted := range ReadOnlyFileTools {
		if !declared[wanted] {
			t.Errorf("%s should have been declared", wanted)
		}
	}
	if !declared["submit"] {
		t.Error("the terminator should have been declared")
	}
}

func TestAssertReadOnlyNamesTheOffender(t *testing.T) {
	err := AssertReadOnly([]core.Tool{{Name: "read_file"}, {Name: "write_file"}, {Name: "execute"}})
	if !errors.Is(err, ErrNotReadOnly) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "write_file") || !strings.Contains(err.Error(), "execute") {
		t.Errorf("the error should name both: %v", err)
	}
	if err := AssertReadOnly([]core.Tool{{Name: "read_file"}}); err != nil {
		t.Errorf("a clean set was rejected: %v", err)
	}
}

// A read-only phase that keeps `execute` gets a description that matches what
// its policy allows: a model told pipes work wastes turns finding out.
func TestSelectToolsRewritesExecuteForAReadOnlyPhase(t *testing.T) {
	all := []core.Tool{{Name: "execute", Description: "Run a command with pipes and redirection"}}
	got := SelectTools(all, true, "execute")
	if len(got) != 1 {
		t.Fatal("execute was dropped")
	}
	if !strings.Contains(got[0].Description, "refused in this phase") {
		t.Errorf("Description = %q", got[0].Description)
	}
	if same := SelectTools(all, false, "execute"); same[0].Description != all[0].Description {
		t.Error("a write phase's execute description should be left alone")
	}
}

func TestPhaseWithoutATerminatorIsRefused(t *testing.T) {
	p := faux.New()
	r, err := NewRunner(fauxConfig(p, newWorkspace(t)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), Phase{Name: "x", System: "s", User: "u"}); err == nil {
		t.Fatal("a phase with no terminating tool should not run")
	}
	if p.Calls() != 0 {
		t.Error("nothing should have reached the wire")
	}
}

func TestNewRunnerRequiresAModel(t *testing.T) {
	if _, err := NewRunner(Config{}); err == nil {
		t.Fatal("want an error")
	} else if CategoryOf(err) != CategoryModel {
		t.Errorf("category = %s", CategoryOf(err))
	}
}
