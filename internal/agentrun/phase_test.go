package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/schema"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/afspec"
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

	// TS-15-4 (unit): the symbol tools are read tools.
	// Verifies: 15-REQ-1.4, 15-REQ-10.1
	t.Run("TS-15-4 symbol tools pass", func(t *testing.T) {
		set := []core.Tool{
			{Name: "read_file"}, {Name: "list_files"}, {Name: "find_files"},
			{Name: "search_files"}, {Name: "file_outline"}, {Name: "find_symbol"},
		}
		if err := AssertReadOnly(set); err != nil {
			t.Errorf("a set of the six read tools was rejected: %v", err)
		}
	})

	// TS-15-5 (unit): write_file is named, the symbol tools are not.
	// Verifies: 15-REQ-10.2
	t.Run("TS-15-5 write_file named beside symbol tools", func(t *testing.T) {
		err := AssertReadOnly([]core.Tool{
			{Name: "read_file"}, {Name: "file_outline"}, {Name: "find_symbol"}, {Name: "write_file"},
		})
		if !errors.Is(err, ErrNotReadOnly) {
			t.Fatalf("err = %v, want ErrNotReadOnly", err)
		}
		msg := err.Error()
		if !strings.Contains(msg, "write_file") {
			t.Errorf("the error should name write_file: %v", err)
		}
		if strings.Contains(msg, "file_outline") || strings.Contains(msg, "find_symbol") {
			t.Errorf("the error names a read tool: %v", err)
		}
	})
}

// TS-15-2 (unit): a read-only phase declares all six read tools and no
// mutating tool.
//
// Verifies: 15-REQ-1.2, 15-REQ-9.1
func TestTS15_2_ReadOnlyPhaseDeclaresTheSixReadTools(t *testing.T) {
	p := faux.New(toolCallTurn("c1", "submit", map[string]any{"value": "x"}))
	var got string
	var calls int
	r, err := NewRunner(fauxConfig(p, newWorkspace(t)))
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
	declared := declaredTools(t, p)
	for _, n := range []string{"read_file", "list_files", "find_files", "search_files", "file_outline", "find_symbol"} {
		if !declared[n] {
			t.Errorf("%s was not declared", n)
		}
	}
	for _, n := range ReadOnlyFileTools {
		if !declared[n] {
			t.Errorf("%s was not declared", n)
		}
	}
	for _, n := range MutatingTools {
		if declared[n] {
			t.Errorf("%s was declared in a read-only phase", n)
		}
	}
}

// TS-15-3 (unit): a writing phase declares the six read tools beside the
// write tools and execute.
//
// Verifies: 15-REQ-1.3, 15-REQ-9.2
func TestTS15_3_WritingPhaseDeclaresTheSixReadTools(t *testing.T) {
	p := faux.New(toolCallTurn("c1", "submit", map[string]any{"value": "x"}))
	var got string
	var calls int
	r, err := NewRunner(fauxConfig(p, newWorkspace(t)))
	if err != nil {
		t.Fatal(err)
	}
	builtins := append(append([]string(nil), ReadOnlyFileTools...), WriteFileTools...)
	builtins = append(builtins, "execute")
	if _, err := r.Run(context.Background(), Phase{
		Name: "write", System: "s", User: "u", Terminator: "submit",
		Custom: []core.Tool{submitTool(&got, &calls)}, BuiltinTools: builtins,
		ReadOnly: false, Programs: ReadOnlyPrograms,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	declared := declaredTools(t, p)
	for _, n := range ReadOnlyFileTools {
		if !declared[n] {
			t.Errorf("%s was not declared", n)
		}
	}
	for _, n := range []string{"file_outline", "find_symbol", "write_file", "edit_file", "execute"} {
		if !declared[n] {
			t.Errorf("%s was not declared", n)
		}
	}
}

func declaredTools(t *testing.T, p *faux.Provider) map[string]bool {
	t.Helper()
	reqs := p.Requests()
	if len(reqs) == 0 {
		t.Fatal("no request reached the wire")
	}
	declared := map[string]bool{}
	for _, tool := range reqs[0].Tools {
		declared[tool.Name] = true
	}
	return declared
}

// TS-15-6 (unit): toolsNote names the symbol tools.
//
// Verifies: 15-REQ-1.5
func TestTS15_6_ToolsNoteListsTheSymbolTools(t *testing.T) {
	note := toolsNote([]core.Tool{
		{Name: "read_file"}, {Name: "find_files"}, {Name: "file_outline"},
		{Name: "find_symbol"}, {Name: "submit"},
	})
	for _, want := range []string{"file_outline", "find_symbol", "no shell"} {
		if !strings.Contains(note, want) {
			t.Errorf("note %q lacks %q", note, want)
		}
	}
}

// TS-15-7 (unit): each phase gets its own tool set from its own tools.All
// call, so each holds its own symbol table.
//
// Verifies: 15-REQ-2.1, 15-REQ-2.2
func TestTS15_7_RegisteredToolsIsFreshPerPhase(t *testing.T) {
	r, err := NewRunner(fauxConfig(faux.New(), newWorkspace(t)))
	if err != nil {
		t.Fatal(err)
	}
	t1, err := r.registeredTools(Phase{Name: "a", BuiltinTools: ReadOnlyFileTools, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	t2, err := r.registeredTools(Phase{Name: "b", BuiltinTools: ReadOnlyFileTools, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	has := func(ts []core.Tool, name string) bool {
		for _, x := range ts {
			if x.Name == name {
				return true
			}
		}
		return false
	}
	for _, n := range []string{"file_outline", "find_symbol"} {
		if !has(t1, n) || !has(t2, n) {
			t.Errorf("%s missing from a phase's tools", n)
		}
	}
	if len(t1) == 0 || len(t2) == 0 || &t1[0] == &t2[0] {
		t.Error("the two phases share a tool slice")
	}
}

// TS-15-8 (unit): registeredTools leaves tools.Options.Symbols at its zero
// value.
//
// Verifies: 15-REQ-3.1
func TestTS15_8_RegisteredToolsSetsOnlyTheWorkspace(t *testing.T) {
	src, err := os.ReadFile("phase.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	i := strings.Index(text, "func (r *Runner) registeredTools(")
	if i < 0 {
		t.Fatal("registeredTools not found")
	}
	body := text[i:]
	if j := strings.Index(body, "\n}\n"); j >= 0 {
		body = body[:j]
	}
	if !strings.Contains(body, "tools.All(tools.Options{Workspace: r.cfg.Workspace})") {
		t.Errorf("tools.All should be called with only Workspace set:\n%s", body)
	}
	if strings.Contains(body, "Symbols") {
		t.Error("registeredTools must not set Symbols")
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
	if got != "é…[+8 bytes]" {
		t.Errorf("firstLine = %q", got)
	}
}

func TestToolErrorCounterKeysByToolAndCode(t *testing.T) {
	var c toolErrorCounter
	c.add("search_files", `{"ok":false,"error":"invalid_arguments"}`)
	c.add("search_files", `{"ok":false,"error":"invalid_arguments"}`)
	c.add("bash", "unknown tool")
	got := c.snapshot()
	if got["search_files/invalid_arguments"] != 2 || got["bash"] != 1 {
		t.Errorf("snapshot = %v", got)
	}
	if (&toolErrorCounter{}).snapshot() != nil {
		t.Error("no errors should snapshot to nil")
	}
}

func TestToolsNoteListsTheToolsAndSaysWhenThereIsNoShell(t *testing.T) {
	ro := toolsNote([]core.Tool{{Name: "read_file"}, {Name: "find_files"}, {Name: "submit_prd"}})
	if !strings.Contains(ro, "find_files, read_file, submit_prd") || !strings.Contains(ro, "no shell") {
		t.Errorf("read-only note = %q", ro)
	}
	rw := toolsNote([]core.Tool{{Name: "execute"}, {Name: "read_file"}})
	if strings.Contains(rw, "no shell") || !strings.Contains(rw, "execute, read_file") {
		t.Errorf("shell note = %q", rw)
	}
}

func TestSelectToolsDropsExecuteGuidelinesWithoutAShell(t *testing.T) {
	all := []core.Tool{{Name: "search_files", PromptGuidelines: []string{"Prefer this over execute+grep.", "Use a glob."}}}
	if got := SelectTools(all, true, nil, "search_files")[0].PromptGuidelines; len(got) != 1 || got[0] != "Use a glob." {
		t.Errorf("guidelines = %v", got)
	}
}

// runMapPhase runs one phase carrying repoMap and returns what the observer
// heard and the requests that reached the provider.
func runMapPhase(t *testing.T, repoMap, user string) (*recObserver, []core.Request) {
	t.Helper()
	var got string
	var calls int
	p := faux.New(toolCallTurn("c1", "submit", map[string]any{"value": "done"}))
	cfg := fauxConfig(p, newWorkspace(t))
	obs := &recObserver{verbose: true}
	cfg.Observer = obs
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Run(context.Background(), Phase{
		Name: "phase", System: "system", User: user, RepoMap: repoMap,
		Terminator: "submit", Custom: []core.Tool{submitTool(&got, &calls)},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return obs, p.Requests()
}

// TS-14-24 (unit): Phase carries a RepoMap string field.
func TestTS14_24_PhaseCarriesRepoMap(t *testing.T) {
	p := Phase{RepoMap: "test map"}
	if p.RepoMap != "test map" {
		t.Errorf("RepoMap = %q, want %q", p.RepoMap, "test map")
	}
}

// TS-14-25 (unit): the runner reports the map's token count through
// Observer.Detail when RepoMap is non-empty, and says nothing when it is empty.
func TestTS14_25_RunnerLogsRepoMapTokens(t *testing.T) {
	m := strings.Repeat("x", 400)
	want := fmt.Sprint(afspec.EstimateTokens(m))
	if want != "100" {
		t.Fatalf("fixture is %s tokens, want 100", want)
	}
	obs, _ := runMapPhase(t, m, "do the thing")
	found := false
	for _, d := range obs.details {
		if strings.Contains(d, "repo map") && strings.Contains(d, want) {
			found = true
		}
	}
	if !found {
		t.Errorf("no detail line carries the token count %s: %q", want, obs.details)
	}

	obs, _ = runMapPhase(t, "", "do the thing")
	for _, d := range obs.details {
		if strings.Contains(d, "repo map") {
			t.Errorf("an empty map was logged: %q", d)
		}
	}
}

// TS-14-26 (unit): the runner never puts Phase.RepoMap into a prompt.
func TestTS14_26_RunnerNeverInjectsRepoMap(t *testing.T) {
	_, reqs := runMapPhase(t, "MARKER_STRING", "do the thing")
	if len(reqs) == 0 {
		t.Fatal("no request reached the wire")
	}
	sys, err := json.Marshal(reqs[0].System)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := json.Marshal(reqs[0].Messages)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sys), "MARKER_STRING") {
		t.Error("the system prompt contains the repository map")
	}
	if strings.Contains(string(msgs), "MARKER_STRING") {
		t.Error("the user prompt contains the repository map")
	}
	if !strings.Contains(string(msgs), "do the thing") {
		t.Error("the user prompt did not reach the wire; the check proves nothing")
	}
}
