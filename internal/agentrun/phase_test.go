package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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

// TS-11-15 (unit): ResolvedBounds reports the configured per-phase ceilings
// without starting a phase.
func TestTS11_15_ResolvedBoundsReportsCeilings(t *testing.T) {
	p := faux.New()
	cfg := fauxConfig(p, newWorkspace(t))
	cfg.Bounds = Bounds{MaxTurns: 12, MaxBudgetUSD: 3.5}
	obs := &recObserver{}
	cfg.Observer = obs
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	turns, budget := r.ResolvedBounds()
	if turns != 12 || budget != 3.5 {
		t.Errorf("ResolvedBounds = (%d, %v), want (12, 3.5)", turns, budget)
	}
	if len(obs.starts) != 0 {
		t.Errorf("a phase was started: %v", obs.starts)
	}

	// A zero Bounds takes the same defaults Run applies.
	cfg.Bounds = Bounds{}
	r, _ = NewRunner(cfg)
	turns, budget = r.ResolvedBounds()
	if turns != (Bounds{}).maxTurns() || budget != (Bounds{}).maxBudget() || turns == 0 || budget == 0 {
		t.Errorf("default ResolvedBounds = (%d, %v)", turns, budget)
	}
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

// A phase's tool descriptions state its own rules, generated from its
// allowlist: a model that is not told them learns them from refusals.
func TestSelectToolsTellsThePhaseItsRules(t *testing.T) {
	all := []core.Tool{
		{Name: "execute", Description: "Run a command with pipes and redirection."},
		{Name: "write_file", Description: "Write a file."},
		{Name: "edit_file", Description: "Edit a file."},
		{Name: "read_file", Description: "Read a file."},
	}
	programs := []string{"go", "git", "make"}

	ro := SelectTools(all, true, programs, "execute")
	if !strings.Contains(ro[0].Description, "refused in this phase") ||
		!strings.Contains(ro[0].Description, "go, git, make") ||
		strings.Contains(ro[0].Description, "pipes and redirection") {
		t.Errorf("read-only execute = %q", ro[0].Description)
	}

	w := SelectTools(all, false, programs, "execute", "write_file", "edit_file", "read_file")
	byName := map[string]string{}
	for _, tl := range w {
		byName[tl.Name] = tl.Description
	}
	for name, wants := range map[string][]string{
		"execute":    {"go, git, make", "repository root", "do not `cd`", "write_file"},
		"write_file": {"inside the repository", "/tmp", "heredoc"},
		"edit_file":  {"JSON array", "old_string", "new_string"},
	} {
		for _, want := range wants {
			if !strings.Contains(byName[name], want) {
				t.Errorf("%s description lacks %q: %s", name, want, byName[name])
			}
		}
	}
	if byName["read_file"] != "Read a file." {
		t.Errorf("read_file was rewritten: %q", byName["read_file"])
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

func TestFirstLineTruncatesOnARuneBoundary(t *testing.T) {
	got := firstLine("ééééé", 3) // each é is two bytes: byte 3 is mid-rune
	if !utf8.ValidString(got) {
		t.Errorf("firstLine produced invalid UTF-8: %q", got)
	}
	if got != "é…" {
		t.Errorf("firstLine = %q", got)
	}
}
