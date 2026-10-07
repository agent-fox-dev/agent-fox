package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/prompt"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/schema"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/project"
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
	ws := newWorkspace(t)
	skipIfNoFindReferences(t, ws)
	p := faux.New(toolCallTurn("c1", "submit", map[string]any{"value": "x"}))

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

	// TS-17-13 (unit): AssertReadOnly returns nil for a tool set holding all
	// seven read tools.
	// Verifies: 17-REQ-5.1
	t.Run("TS-17-13 seven read tools pass", func(t *testing.T) {
		var set []core.Tool
		for _, n := range ReadOnlyFileTools {
			set = append(set, core.Tool{Name: n})
		}
		if len(set) != 7 {
			t.Fatalf("set has %d tools, want 7", len(set))
		}
		if !hasTool(set, "find_references") {
			t.Fatal("find_references not in set")
		}
		if err := AssertReadOnly(set); err != nil {
			t.Errorf("a set of the seven read tools was rejected: %v", err)
		}
	})

	// TS-17-14 (unit): A set of the seven read tools plus write_file is
	// refused with ErrNotReadOnly naming only write_file.
	// Verifies: 17-REQ-5.2
	t.Run("TS-17-14 write_file named beside seven read tools", func(t *testing.T) {
		var set []core.Tool
		for _, n := range ReadOnlyFileTools {
			set = append(set, core.Tool{Name: n})
		}
		set = append(set, core.Tool{Name: "write_file"})
		err := AssertReadOnly(set)
		if !errors.Is(err, ErrNotReadOnly) {
			t.Fatalf("err = %v, want ErrNotReadOnly", err)
		}
		msg := err.Error()
		if !strings.Contains(msg, "write_file") {
			t.Errorf("the error should name write_file: %v", err)
		}
		for _, n := range ReadOnlyFileTools {
			if strings.Contains(msg, n) {
				t.Errorf("the error names a read tool %s: %v", n, err)
			}
		}
	})
}

// TS-17-3 (integration): A read-only phase run through the real Runner and
// tools.All declares every name in ReadOnlyFileTools on its first request,
// find_references included, and no mutating tool.
//
// Verifies: 17-REQ-1.4
func TestTS17_3_ReadOnlyPhaseDeclaresTheReadTools(t *testing.T) {
	ws := newWorkspace(t)
	skipIfNoFindReferences(t, ws)
	p := faux.New(toolCallTurn("c1", "submit", map[string]any{"value": "x"}))
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
	declared := declaredTools(t, p)
	requireDeclared(t, declared)
	if !declared["find_references"] {
		t.Error("find_references was not declared")
	}
	for _, n := range MutatingTools {
		if declared[n] {
			t.Errorf("%s was declared in a read-only phase", n)
		}
	}
}

// TS-17-4 (integration): A writing phase run through the real Runner declares
// every ReadOnlyFileTools name beside write_file, edit_file, execute and
// code_search when the run has an index.
//
// Verifies: 17-REQ-1.4
func TestTS17_4_WritingPhaseDeclaresTheReadTools(t *testing.T) {
	ws := newWorkspace(t)
	skipIfNoFindReferences(t, ws)
	cfg := fauxConfig(faux.New(), ws)
	cfg.Index = &fakeIndex{}
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	builtins := WithCodeSearch(append(append([]string(nil), ReadOnlyFileTools...), append(WriteFileTools, "execute")...), true)
	ts, err := r.registeredTools(Phase{
		Name: "write", BuiltinTools: builtins, ReadOnly: false, Programs: ReadOnlyPrograms,
	})
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]bool{}
	for _, tl := range ts {
		declared[tl.Name] = true
	}
	requireDeclared(t, declared)
	for _, n := range []string{"write_file", "edit_file", "execute", "code_search"} {
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

// skipIfNoFindReferences skips the test when the replace target's tools.All
// does not offer find_references.
func skipIfNoFindReferences(t *testing.T, ws *tools.Workspace) {
	t.Helper()
	built, err := tools.All(tools.Options{Workspace: ws})
	if err != nil {
		t.Skipf("tools.All failed: %v", err)
	}
	for _, tl := range built {
		if tl.Name == "find_references" {
			return
		}
	}
	t.Skip("find_references not offered by the replace target")
}

// missingTools returns the names from want that are not in declared, in want's
// order.
func missingTools(declared map[string]bool, want []string) []string {
	var missing []string
	for _, n := range want {
		if !declared[n] {
			missing = append(missing, n)
		}
	}
	return missing
}

// requireDeclared calls t.Errorf once per ReadOnlyFileTools name that is
// missing from declared.
func requireDeclared(tb testing.TB, declared map[string]bool) {
	tb.Helper()
	for _, n := range ReadOnlyFileTools {
		if !declared[n] {
			tb.Errorf("%s was not declared", n)
		}
	}
}

// recordingTB records whether Errorf was called and what it said.
type recordingTB struct {
	testing.TB
	failed bool
	output strings.Builder
}

func (r *recordingTB) Helper() {}
func (r *recordingTB) Errorf(format string, args ...any) {
	r.failed = true
	fmt.Fprintf(&r.output, format+"\n", args...)
}
func (r *recordingTB) Fatalf(format string, args ...any) {
	r.failed = true
	fmt.Fprintf(&r.output, format+"\n", args...)
}

// TS-17-5 (unit): The wire-membership helper returns every ReadOnlyFileTools
// name a declared set lacks, so a replace target without find_references fails
// the tests by name.
//
// Verifies: 17-REQ-1.5
func TestTS17_5_MissingToolsHelper(t *testing.T) {
	// Build a full declared set from ReadOnlyFileTools.
	full := map[string]bool{}
	for _, n := range ReadOnlyFileTools {
		full[n] = true
	}

	// Full set: nothing missing.
	if got := missingTools(full, ReadOnlyFileTools); len(got) != 0 {
		t.Errorf("full set: missingTools = %v, want empty", got)
	}

	// Without find_references.
	without := map[string]bool{}
	for k, v := range full {
		without[k] = v
	}
	delete(without, "find_references")
	if got := missingTools(without, ReadOnlyFileTools); len(got) != 1 || got[0] != "find_references" {
		t.Errorf("without find_references: missingTools = %v, want [find_references]", got)
	}

	// Without find_references and find_symbol.
	delete(without, "find_symbol")
	got := missingTools(without, ReadOnlyFileTools)
	want := []string{"find_symbol", "find_references"}
	if !slices.Equal(got, want) {
		t.Errorf("without find_symbol and find_references: missingTools = %v, want %v", got, want)
	}

	// requireDeclared on the reduced set fails and names find_references.
	rec := &recordingTB{TB: t}
	requireDeclared(rec, without)
	if !rec.failed {
		t.Error("requireDeclared did not fail on a reduced set")
	}
	if !strings.Contains(rec.output.String(), "find_references") {
		t.Errorf("requireDeclared output does not name find_references: %s", rec.output.String())
	}
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
// value; it sets only Workspace and Index (16-REQ-1.2).
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
	if !strings.Contains(body, "tools.All(tools.Options{Workspace: r.cfg.Workspace, Index: r.cfg.Index})") {
		t.Errorf("tools.All should be called with only Workspace and Index set:\n%s", body)
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

// TS-15-17 (smoke): a read-only phase and a writing phase on one Runner, the
// way fix runs analyse then implement, each declare the six read tools, the
// read-only invariant holds for a phase without a shell, and each phase gets
// its own fresh symbol table: a declaration that appears between the phases is
// found by the second phase's find_symbol and was not in the first's.
//
// Verifies: 15-PATH-1, 15-REQ-1.2, 15-REQ-1.3, 15-REQ-1.4, 15-REQ-2.1
//
// Real components: Runner, SelectTools, AssertReadOnly, tools.All. Only the
// model is scripted.
func TestTS15_17_AnalyseThenImplementRegisterSixReadToolsWithAFreshTable(t *testing.T) {
	ws := newWorkspace(t)
	skipIfNoFindReferences(t, ws)
	p := faux.New(
		toolCallTurn("a1", "find_symbol", map[string]any{"name": "LateArrival"}),
		toolCallTurn("a2", "submit", map[string]any{"value": "x"}),
		toolCallTurn("i1", "find_symbol", map[string]any{"name": "LateArrival"}),
		toolCallTurn("i2", "submit", map[string]any{"value": "x"}),
	)
	r, err := NewRunner(fauxConfig(p, ws))
	if err != nil {
		t.Fatal(err)
	}
	var got string
	var calls int

	// The read-only phase's resolved set passes AssertReadOnly.
	ro, err := r.registeredTools(Phase{Name: "analyse", BuiltinTools: ReadOnlyFileTools, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := AssertReadOnly(core.ToolPolicy{ExcludeTools: MutatingTools}.Resolve(ro)); err != nil {
		t.Errorf("AssertReadOnly refused the read-only set: %v", err)
	}

	if _, err := r.Run(context.Background(), Phase{
		Name: "analyse", System: "s", User: "u", Terminator: "submit",
		Custom: []core.Tool{submitTool(&got, &calls)}, BuiltinTools: ReadOnlyFileTools,
		ReadOnly: true,
	}); err != nil {
		t.Fatalf("analyse: %v", err)
	}
	nAnalyse := len(p.Requests())

	// Go changes the tree between the phases.
	late := filepath.Join(ws.Root, "late.go")
	if err := os.WriteFile(late, []byte("package main\n\nfunc LateArrival() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	builtins := append(append([]string(nil), ReadOnlyFileTools...), WriteFileTools...)
	builtins = append(builtins, "execute")
	calls = 0
	if _, err := r.Run(context.Background(), Phase{
		Name: "implement", System: "s", User: "u", Terminator: "submit",
		Custom: []core.Tool{submitTool(&got, &calls)}, BuiltinTools: builtins,
		ReadOnly: false, Programs: ReadOnlyPrograms,
	}); err != nil {
		t.Fatalf("implement: %v", err)
	}
	reqs := p.Requests()
	if nAnalyse == 0 || len(reqs) <= nAnalyse {
		t.Fatalf("expected requests from both phases, got %d then %d", nAnalyse, len(reqs))
	}

	names := func(req core.Request) map[string]bool {
		m := map[string]bool{}
		for _, tool := range req.Tools {
			m[tool.Name] = true
		}
		return m
	}
	analyse, implement := names(reqs[0]), names(reqs[nAnalyse])
	for _, n := range ReadOnlyFileTools {
		if !analyse[n] {
			t.Errorf("analyse did not declare %s", n)
		}
		if !implement[n] {
			t.Errorf("implement did not declare %s", n)
		}
	}
	for _, n := range MutatingTools {
		if analyse[n] {
			t.Errorf("analyse declared %s", n)
		}
	}
	for _, n := range []string{"write_file", "edit_file", "execute"} {
		if !implement[n] {
			t.Errorf("implement did not declare %s", n)
		}
	}

	// The last request of each phase carries its find_symbol result.
	result := func(req core.Request) string {
		raw, err := json.Marshal(req.Messages)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	if strings.Contains(result(reqs[nAnalyse-1]), "late.go") {
		t.Error("analyse's find_symbol saw a file that did not exist yet")
	}
	if !strings.Contains(result(reqs[len(reqs)-1]), "late.go") {
		t.Errorf("implement's find_symbol did not find the declaration added between the phases:\n%s",
			result(reqs[len(reqs)-1]))
	}
}

// fakeIndex is a tools.Index test double. Tools returns a code_search tool,
// as the real codesearch index does.
type fakeIndex struct {
	invalidated []string
	closed      int
}

func (f *fakeIndex) Symbols(context.Context, tools.SymbolQuery) (tools.SymbolAnswer, bool, error) {
	return tools.SymbolAnswer{}, false, nil
}

func (f *fakeIndex) Tools() []core.Tool {
	return []core.Tool{{Name: "code_search", Description: "ranked search"}}
}

func (f *fakeIndex) Invalidate(rel string) { f.invalidated = append(f.invalidated, rel) }

func (f *fakeIndex) Close() error { f.closed++; return nil }

func hasTool(ts []core.Tool, name string) bool {
	for _, x := range ts {
		if x.Name == name {
			return true
		}
	}
	return false
}

func withCodeSearch() []string {
	return append(append([]string(nil), ReadOnlyFileTools...), "code_search")
}

// TS-16-1 (unit): Config.Index is passed through to tools.Options.Index in
// registeredTools, so code_search is in the resolved set.
//
// Verifies: 16-REQ-1.1, 16-REQ-1.2
func TestTS16_1_ConfigIndexReachesToolsAll(t *testing.T) {
	cfg := fauxConfig(faux.New(), newWorkspace(t))
	cfg.Index = &fakeIndex{}
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.registeredTools(Phase{Name: "a", BuiltinTools: withCodeSearch(), ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasTool(got, "code_search") {
		t.Error("code_search missing from the resolved tool set")
	}
	src, err := os.ReadFile("phase.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "Index: r.cfg.Index") {
		t.Error("registeredTools must pass r.cfg.Index as tools.Options.Index")
	}
}

// TS-16-6 (unit): code_search joins the six read tools when Index is set.
//
// Verifies: 16-REQ-2.1, 16-REQ-2.3
func TestTS16_6_CodeSearchJoinsTheReadTools(t *testing.T) {
	ws := newWorkspace(t)
	skipIfNoFindReferences(t, ws)
	cfg := fauxConfig(faux.New(), ws)
	cfg.Index = &fakeIndex{}
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.registeredTools(Phase{Name: "a", BuiltinTools: withCodeSearch(), ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if !hasTool(got, "code_search") {
		t.Error("code_search missing")
	}
	for _, n := range ReadOnlyFileTools {
		if !hasTool(got, n) {
			t.Errorf("%s missing from the resolved set", n)
		}
	}
}

// TS-16-7 (unit): code_search is absent when Index is nil, though the phase
// names it.
//
// Verifies: 16-REQ-2.2, 16-REQ-2.4
func TestTS16_7_CodeSearchAbsentWithoutIndex(t *testing.T) {
	ws := newWorkspace(t)
	skipIfNoFindReferences(t, ws)
	cfg := fauxConfig(faux.New(), ws)
	cfg.Index = nil
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.registeredTools(Phase{Name: "a", BuiltinTools: withCodeSearch(), ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if hasTool(got, "code_search") {
		t.Error("code_search present without an index")
	}
	for _, n := range ReadOnlyFileTools {
		if !hasTool(got, n) {
			t.Errorf("%s missing from the resolved set", n)
		}
	}
}

// TS-16-9 (unit): AssertReadOnly passes when the resolved set holds
// code_search.
//
// Verifies: 16-REQ-6.2
func TestTS16_9_AssertReadOnlyAcceptsCodeSearch(t *testing.T) {
	set := []core.Tool{{Name: "code_search"}, {Name: "read_file"}, {Name: "list_files"}, {Name: "find_files"}, {Name: "search_files"}}
	if err := AssertReadOnly(set); err != nil {
		t.Errorf("AssertReadOnly refused code_search: %v", err)
	}
	// And through the real runner, with a real read-only phase.
	cfg := fauxConfig(faux.New(), newWorkspace(t))
	cfg.Index = &fakeIndex{}
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ro, err := r.registeredTools(Phase{Name: "a", BuiltinTools: withCodeSearch(), ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := AssertReadOnly(core.ToolPolicy{ExcludeTools: MutatingTools}.Resolve(ro)); err != nil {
		t.Errorf("AssertReadOnly refused the resolved read-only set: %v", err)
	}
}

// TS-17-15 (integration): With ReadRoots configured, the find_references
// description is exactly as AgentKit offers it, while read_file and execute
// carry the read-root note.
//
// Verifies: 17-REQ-5.4
func TestTS17_15_FindReferencesDescriptionUnchangedByReadRoots(t *testing.T) {
	ws := newWorkspace(t)

	// Get AgentKit's own description of find_references.
	refTools, err := tools.All(tools.Options{Workspace: ws})
	if err != nil {
		t.Fatalf("tools.All: %v", err)
	}
	var refDesc string
	for _, tl := range refTools {
		if tl.Name == "find_references" {
			refDesc = tl.Description
			break
		}
	}
	if refDesc == "" {
		t.Skip("find_references not offered by the replace target")
	}

	cfg := fauxConfig(faux.New(), ws)
	cfg.ReadRoots = []project.ReadRoot{{
		Path:   "../agentkit-go",
		Module: "github.com/agentfox/agentkit-go",
		Abs:    t.TempDir(),
	}}
	r, err := NewRunner(cfg)
	if err != nil {
		t.Fatal(err)
	}
	builtins := append(slices.Clone(ReadOnlyFileTools), "execute")
	ts, err := r.registeredTools(Phase{
		Name: "a", BuiltinTools: builtins, ReadOnly: true, Programs: ReadOnlyPrograms,
	})
	if err != nil {
		t.Fatal(err)
	}

	var frDesc, rfDesc, exDesc string
	for _, tl := range ts {
		switch tl.Name {
		case "find_references":
			frDesc = tl.Description
		case "read_file":
			rfDesc = tl.Description
		case "execute":
			exDesc = tl.Description
		}
	}
	if frDesc != refDesc {
		t.Errorf("find_references description differs from AgentKit's:\ngot:  %s\nwant: %s", frDesc, refDesc)
	}
	if strings.Contains(frDesc, "You may also read, but not change") {
		t.Error("find_references carries the read-root note")
	}
	if !strings.Contains(rfDesc, "You may also read, but not change") {
		t.Error("read_file does not carry the read-root note")
	}
	if !strings.Contains(exDesc, "You may also read, but not change") {
		t.Error("execute does not carry the read-root note")
	}
}

// guidelinesHeading opens the block prompt.Build renders the tools'
// guidelines in after a custom system prompt.
const guidelinesHeading = "\n\nGuidelines:\n"

// systemPrompt is what reaches the model for a phase registering set: the
// phase's text and the tools note, followed by AgentKit's guidelines block.
func systemPrompt(set []core.Tool) string {
	return prompt.Build(prompt.Input{Custom: "system" + toolsNote(set), Tools: set})
}

// guidelinesSection is the part of a system prompt from the guidelines
// heading on, or "" when there is none.
func guidelinesSection(sys string) string {
	if i := strings.Index(sys, guidelinesHeading); i >= 0 {
		return sys[i:]
	}
	return ""
}

// TS-17-1 (unit): the guidelines of every registered tool are rendered once,
// after the tool list, and the search-over-execute line appears once.
//
// Verifies: 17-REQ-1.1, 17-REQ-2.1
func TestTS17_1_ToolsNoteRendersEveryGuideline(t *testing.T) {
	note := systemPrompt([]core.Tool{
		{Name: "read_file", PromptGuidelines: []string{"Read in ranges."}},
		{Name: "search_files", PromptGuidelines: []string{"Use a glob."}},
		{Name: "execute"},
		{Name: "submit_x", PromptGuidelines: []string{"Report by calling submit_x."}},
	})
	if strings.Count(note, guidelinesHeading) != 1 ||
		strings.Index(note, guidelinesHeading) < strings.Index(note, "Your tools are exactly:") {
		t.Fatalf("note = %q", note)
	}
	for _, g := range []string{"Read in ranges.", "Use a glob.", "Report by calling submit_x."} {
		if !strings.Contains(note, "- "+g) {
			t.Errorf("the note lacks the guideline %q:\n%s", g, note)
		}
	}
	if n := strings.Count(note, tools.SearchOverExecuteGuideline); n != 1 {
		t.Errorf("the search-over-execute guideline appears %d times", n)
	}
}

// TS-17-2 (unit): tools are visited in registration order, each tool's
// guidelines in their declared order (prompt.Build keeps first-seen order).
//
// Verifies: 17-REQ-1.2
func TestTS17_2_GuidelinesFollowTheSortedToolOrder(t *testing.T) {
	note := systemPrompt([]core.Tool{
		{Name: "zeta", PromptGuidelines: []string{"z1", "z2"}},
		{Name: "alpha", PromptGuidelines: []string{"a2", "a1"}},
		{Name: "mid", PromptGuidelines: []string{"m1"}},
	})
	sec := guidelinesSection(note)
	last := -1
	for _, g := range []string{"- z1", "- z2", "- a2", "- a1", "- m1"} {
		i := strings.Index(sec, g)
		if i <= last {
			t.Fatalf("%q is out of order in:\n%s", g, sec)
		}
		last = i
	}
}

// TS-17-3 (unit): an empty or whitespace-only guideline is skipped.
//
// Verifies: 17-REQ-1.3
func TestTS17_3_BlankGuidelinesAreSkipped(t *testing.T) {
	sec := guidelinesSection(systemPrompt([]core.Tool{
		{Name: "t", PromptGuidelines: []string{"", "   ", "\t\n", "Real one."}},
	}))
	var bullets []string
	for _, line := range strings.Split(sec, "\n") {
		if strings.HasPrefix(line, "-") {
			if strings.TrimSpace(strings.TrimPrefix(line, "-")) == "" {
				t.Errorf("an empty bullet: %q", line)
			}
			bullets = append(bullets, line)
		}
	}
	if len(bullets) != 1 || bullets[0] != "- Real one." {
		t.Errorf("bullets = %q", bullets)
	}
}

// TS-17-4 (unit): a guideline whose trimmed text was already emitted is
// skipped.
//
// Verifies: 17-REQ-1.4
func TestTS17_4_DuplicateGuidelinesAppearOnce(t *testing.T) {
	sec := guidelinesSection(systemPrompt([]core.Tool{
		{Name: "a", PromptGuidelines: []string{"Same text.", "Same text."}},
		{Name: "b", PromptGuidelines: []string{"  Same text.  ", "Other."}},
	}))
	if strings.Count(sec, "Same text.") != 1 || strings.Count(sec, "Other.") != 1 {
		t.Errorf("section = %q", sec)
	}
}

// TS-17-5 (unit): with no guideline there is no section, and the rest of the
// note is as before.
//
// Verifies: 17-REQ-1.5
func TestTS17_5_NoGuidelinesNoSection(t *testing.T) {
	plain := systemPrompt([]core.Tool{{Name: "read_file"}, {Name: "find_files"}, {Name: "submit_prd"}})
	blank := systemPrompt([]core.Tool{{Name: "read_file", PromptGuidelines: []string{" ", ""}}})
	empty := systemPrompt(nil)
	for _, n := range []string{plain, blank, empty} {
		if strings.Contains(n, guidelinesHeading) {
			t.Errorf("a note with no guidelines has a section: %q", n)
		}
	}
	if !strings.Contains(plain, "find_files, read_file, submit_prd") || !strings.Contains(plain, "There is no shell") {
		t.Errorf("note = %q", plain)
	}
}

// TS-17-6 (unit): with search_files and a shell, the search-over-execute line
// appears exactly once, whether search_files declares it or not.
//
// Verifies: 17-REQ-2.1
func TestTS17_6_SearchOverExecuteOnceWithAShell(t *testing.T) {
	for name, set := range map[string][]core.Tool{
		"undeclared": {{Name: "search_files"}, {Name: "execute"}},
		"declared":   {{Name: "search_files", PromptGuidelines: []string{tools.SearchOverExecuteGuideline}}, {Name: "execute"}},
		"run_command": {{Name: "search_files", PromptGuidelines: []string{tools.SearchOverExecuteGuideline}},
			{Name: "run_command"}},
	} {
		if n := strings.Count(systemPrompt(set), tools.SearchOverExecuteGuideline); n != 1 {
			t.Errorf("%s: the line appears %d times", name, n)
		}
	}
}

// TS-17-7 (unit): without search_files or without a shell, the line does not
// appear.
//
// Verifies: 17-REQ-2.2
func TestTS17_7_NoSearchOverExecuteWithoutBoth(t *testing.T) {
	all := []core.Tool{{Name: "search_files", PromptGuidelines: []string{tools.SearchOverExecuteGuideline, "Use a glob."}}}
	noShell := SelectTools(all, true, nil, "search_files")
	noSearch := []core.Tool{{Name: "read_file"}, {Name: "execute"}}
	for name, set := range map[string][]core.Tool{"no shell": noShell, "no search_files": noSearch} {
		if n := strings.Count(systemPrompt(set), tools.SearchOverExecuteGuideline); n != 0 {
			t.Errorf("%s: the line appears %d times", name, n)
		}
	}
}

// TS-17-8 (unit): a read-only shell phase is told which file tool to use for
// what, naming only the ones it has, and that the shell is for git.
//
// Verifies: 17-REQ-3.1, 17-REQ-3.3
func TestTS17_8_PreferenceLineInAReadOnlyShellPhase(t *testing.T) {
	r := systemPrompt([]core.Tool{{Name: "read_file"}, {Name: "search_files"}, {Name: "find_files"},
		{Name: "list_files"}, {Name: "execute"}})
	want := "Read files with `read_file`, search with `search_files`, find files with `find_files` and list " +
		"directories with `list_files`"
	if strings.Count(r, want) != 1 || !strings.Contains(r, "use `execute` only for git") ||
		strings.Contains(r, "building, formatting and testing") {
		t.Errorf("note = %q", r)
	}
	s := systemPrompt([]core.Tool{{Name: "read_file"}, {Name: "list_files"}, {Name: "execute"}})
	if !strings.Contains(s, "`read_file`") || !strings.Contains(s, "`list_files`") ||
		strings.Contains(s, "search with") || strings.Contains(s, "find files with") {
		t.Errorf("note = %q", s)
	}
}

// TS-17-9 (unit): a writing shell phase may also build, format and test, and
// the shell is named by its registered name.
//
// Verifies: 17-REQ-3.2
func TestTS17_9_PreferenceLineInAWritingShellPhase(t *testing.T) {
	w1 := systemPrompt([]core.Tool{{Name: "read_file"}, {Name: "search_files"}, {Name: "write_file"}, {Name: "execute"}})
	if !strings.Contains(w1, "use `execute` only for git and for building, formatting and testing") {
		t.Errorf("note = %q", w1)
	}
	w2 := systemPrompt([]core.Tool{{Name: "read_file"}, {Name: "edit_file"}, {Name: "run_command"}})
	if !strings.Contains(w2, "use `run_command` only for git and for building, formatting and testing") {
		t.Errorf("note = %q", w2)
	}
}

// TS-17-10 (unit): the preference line comes after the tool list and before
// the guidelines.
//
// Verifies: 17-REQ-3.4
func TestTS17_10_PreferenceLineOrder(t *testing.T) {
	n := systemPrompt([]core.Tool{{Name: "read_file"}, {Name: "search_files", PromptGuidelines: []string{"Use a glob."}},
		{Name: "execute"}})
	a, b, c := strings.Index(n, "Your tools are exactly:"), strings.Index(n, "Read files with"),
		strings.Index(n, guidelinesHeading)
	if a < 0 || b < 0 || c < 0 || !(a < b && b < c) {
		t.Errorf("order %d, %d, %d in %q", a, b, c, n)
	}
}

// TS-17-11 (unit): a shell phase with none of the four file tools gets no
// preference line.
//
// Verifies: 17-REQ-3.5
func TestTS17_11_NoPreferenceLineWithoutFileTools(t *testing.T) {
	n := systemPrompt([]core.Tool{{Name: "execute"}, {Name: "write_file"}, {Name: "submit_x"}})
	for _, s := range []string{"Read files with", "search with", "list directories with", "find files with"} {
		if strings.Contains(n, s) {
			t.Errorf("the note contains %q: %q", s, n)
		}
	}
	if !strings.Contains(n, "execute, submit_x, write_file") {
		t.Errorf("note = %q", n)
	}
}

// TS-17-12 (unit): a phase without a shell keeps its "no shell" sentence and
// gets neither the preference line nor the search-over-execute line.
//
// Verifies: 17-REQ-4.1
func TestTS17_12_NoShellPhaseIsUnchangedInSubstance(t *testing.T) {
	n := systemPrompt([]core.Tool{{Name: "read_file"}, {Name: "search_files"}, {Name: "find_files"},
		{Name: "list_files"}, {Name: "submit_issue", PromptGuidelines: []string{"File it with submit_issue."}}})
	if !strings.Contains(n, "There is no shell: read with the file tools, and do not call `bash`, `execute` or `run_command`.") {
		t.Errorf("note = %q", n)
	}
	if strings.Contains(n, "Read files with") || strings.Contains(n, tools.SearchOverExecuteGuideline) {
		t.Errorf("a no-shell note steers between the file tools and a shell: %q", n)
	}
}

// TS-17-13 (integration): SelectTools drops execute-mentioning guidelines in a
// phase without a shell, and the rendered section then has none.
//
// Verifies: 17-REQ-4.2
func TestTS17_13_NoExecuteGuidelineReachesANoShellNote(t *testing.T) {
	all := []core.Tool{
		{Name: "search_files", PromptGuidelines: []string{"Prefer this over execute+grep.", "Use a glob."}},
		{Name: "read_file", PromptGuidelines: []string{"Do not use execute to cat files."}},
	}
	sec := guidelinesSection(systemPrompt(SelectTools(all, true, nil, "read_file", "search_files")))
	if !strings.Contains(sec, "- Use a glob.") || strings.Contains(sec, "execute") {
		t.Errorf("section = %q", sec)
	}
}

// TS-17-17 (property): the note is a pure function of the tool set: the same
// set, in any order, gives the same bytes.
//
// Verifies: 17-REQ-6.1
func TestTS17_17_ToolsNoteIsDeterministic(t *testing.T) {
	names := []string{"read_file", "search_files", "find_files", "list_files", "execute", "run_command",
		"write_file", "edit_file", "submit_a", "submit_b"}
	pool := []string{"", "  ", "Same.", "Same.", "Read in ranges.", "Use a glob.", tools.SearchOverExecuteGuideline}
	rng := rand.New(rand.NewSource(17))
	for i := 0; i < 200; i++ {
		var set []core.Tool
		for _, n := range names {
			if rng.Intn(2) == 0 {
				continue
			}
			var gs []string
			for j := rng.Intn(4); j > 0; j-- {
				gs = append(gs, pool[rng.Intn(len(pool))])
			}
			set = append(set, core.Tool{Name: n, PromptGuidelines: gs})
		}
		perm := append([]core.Tool(nil), set...)
		rng.Shuffle(len(perm), func(a, b int) { perm[a], perm[b] = perm[b], perm[a] })
		a, b, c := toolsNote(set), toolsNote(set), toolsNote(perm)
		if a != b || a != c {
			t.Fatalf("set %d gives different notes:\n%q\n%q\n%q", i, a, b, c)
		}
	}
}

// TS-17-18 (integration): the system prompt that reaches the provider carries
// the rendered guidelines, the search-over-execute line once and the
// preference line, after the phase's own system text.
//
// Verifies: 17-REQ-7.1
func TestTS17_18_TheRunnerSendsTheGuidelines(t *testing.T) {
	p := faux.New(toolCallTurn("c1", "submit", map[string]any{"value": "done"}))
	r, err := NewRunner(fauxConfig(p, newWorkspace(t)))
	if err != nil {
		t.Fatal(err)
	}
	var got string
	var calls int
	submit := submitTool(&got, &calls)
	submit.PromptGuidelines = []string{"Report by calling submit."}
	if _, err := r.Run(context.Background(), Phase{Name: "phase", System: "system", User: "user",
		Terminator: "submit", Custom: []core.Tool{submit}, ReadOnly: true, Programs: ReadOnlyPrograms,
		BuiltinTools: []string{"read_file", "search_files", "execute"}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	reqs := p.Requests()
	if len(reqs) == 0 {
		t.Fatal("no request reached the provider")
	}
	var b strings.Builder
	for _, blk := range reqs[0].System {
		if tb, ok := blk.(core.TextBlock); ok {
			b.WriteString(tb.Text)
		}
	}
	sys := b.String()
	if !strings.HasPrefix(sys, "system") {
		t.Errorf("the system prompt does not start with the phase's text: %q", sys[:min(len(sys), 80)])
	}
	for _, want := range []string{guidelinesHeading, "- Report by calling submit.", "Read files with `read_file`",
		"use `execute` only for git"} {
		if !strings.Contains(sys, want) {
			t.Errorf("the system prompt lacks %q", want)
		}
	}
	if n := strings.Count(sys, tools.SearchOverExecuteGuideline); n != 1 {
		t.Errorf("the search-over-execute line appears %d times", n)
	}
}

// TS-17-6 (unit): registeredTools makes one tools.All call per phase with
// only Workspace and Index set, and two phases receive separate tool slices
// that each include find_references.
//
// Verifies: 17-REQ-2.1
func TestTS17_6_RegisteredToolsOneCallPerPhaseWithFindReferences(t *testing.T) {
	// Part 1: source-level assertion on the body of registeredTools.
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
	if n := strings.Count(body, "tools.All("); n != 1 {
		t.Errorf("registeredTools has %d tools.All calls, want 1", n)
	}
	if !strings.Contains(body, "tools.All(tools.Options{Workspace: r.cfg.Workspace, Index: r.cfg.Index})") {
		t.Errorf("tools.All should be called with only Workspace and Index set:\n%s", body)
	}
	if strings.Contains(body, "Symbols") {
		t.Error("registeredTools must not set Symbols")
	}

	// Part 2: two registeredTools calls on one Runner both hold
	// find_references and return slices with different backing arrays.
	ws := newWorkspace(t)
	skipIfNoFindReferences(t, ws)
	r, err := NewRunner(fauxConfig(faux.New(), ws))
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
	if !hasTool(t1, "find_references") {
		t.Error("phase a lacks find_references")
	}
	if !hasTool(t2, "find_references") {
		t.Error("phase b lacks find_references")
	}
	if len(t1) == 0 || len(t2) == 0 || &t1[0] == &t2[0] {
		t.Error("the two phases share a tool slice")
	}
}

// TS-17-7 (integration): Each phase holds its own reference table: a call
// site added between a read-only phase and a writing phase is absent from
// the first phase's find_references answer and present in the second's.
//
// Verifies: 17-REQ-2.1
func TestTS17_7_PerPhaseReferenceTableFreshness(t *testing.T) {
	ws := newWorkspace(t)
	skipIfNoFindReferences(t, ws)

	// Write a.go with a function the first phase will look up.
	aGo := filepath.Join(ws.Root, "a.go")
	if err := os.WriteFile(aGo, []byte("package main\n\nfunc LateArrival() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := faux.New(
		// Read-only phase: find_references LateArrival, then submit.
		toolCallTurn("a1", "find_references", map[string]any{"name": "LateArrival", "path": "a.go"}),
		toolCallTurn("a2", "submit", map[string]any{"value": "x"}),
		// Writing phase: find_references LateArrival, then submit.
		toolCallTurn("i1", "find_references", map[string]any{"name": "LateArrival", "path": "a.go"}),
		toolCallTurn("i2", "submit", map[string]any{"value": "x"}),
	)

	var got string
	var calls int
	r, err := NewRunner(fauxConfig(p, ws))
	if err != nil {
		t.Fatal(err)
	}

	// Run the read-only phase.
	if _, err := r.Run(context.Background(), Phase{
		Name: "analyse", System: "s", User: "u", Terminator: "submit",
		Custom: []core.Tool{submitTool(&got, &calls)}, BuiltinTools: ReadOnlyFileTools,
		ReadOnly: true,
	}); err != nil {
		t.Fatalf("analyse: %v", err)
	}
	nAnalyse := len(p.Requests())

	// Write late.go between the phases.
	lateGo := filepath.Join(ws.Root, "late.go")
	if err := os.WriteFile(lateGo, []byte("package main\n\nfunc UseLate() { LateArrival() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run the writing phase.
	builtins := append(append([]string(nil), ReadOnlyFileTools...), WriteFileTools...)
	builtins = append(builtins, "execute")
	calls = 0
	if _, err := r.Run(context.Background(), Phase{
		Name: "implement", System: "s", User: "u", Terminator: "submit",
		Custom: []core.Tool{submitTool(&got, &calls)}, BuiltinTools: builtins,
		ReadOnly: false, Programs: ReadOnlyPrograms,
	}); err != nil {
		t.Fatalf("implement: %v", err)
	}

	reqs := p.Requests()
	if nAnalyse == 0 || len(reqs) <= nAnalyse {
		t.Fatalf("expected requests from both phases, got %d then %d", nAnalyse, len(reqs))
	}

	// Extract tool results from the requests.
	result := func(req core.Request) string {
		raw, err := json.Marshal(req.Messages)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}

	// The first phase's find_references result should not mention UseLate.
	firstResult := result(reqs[nAnalyse-1])
	if strings.Contains(firstResult, "UseLate") {
		t.Error("analyse's find_references saw UseLate, which did not exist yet")
	}

	// The second phase's find_references result should mention UseLate.
	secondResult := result(reqs[len(reqs)-1])
	if !strings.Contains(secondResult, "UseLate") {
		t.Errorf("implement's find_references did not find UseLate:\n%s", secondResult)
	}
}

// TS-17-8 (integration): toolsNote lists find_references, generated from the
// registered set, in the system prompt of both a read-only and a writing phase.
//
// Verifies: 17-REQ-2.2
func TestTS17_8_ToolsNoteListsFindReferences(t *testing.T) {
	// Part 1: synthetic set — does not need the replace target.
	t.Run("synthetic", func(t *testing.T) {
		note := toolsNote([]core.Tool{
			{Name: "read_file"}, {Name: "find_references"}, {Name: "submit"},
		})
		if !strings.Contains(note, "find_references, read_file, submit") {
			t.Errorf("synthetic note does not list find_references in sorted order: %q", note)
		}
	})

	// Part 2: real tools.All for a read-only and a writing phase.
	ws := newWorkspace(t)
	skipIfNoFindReferences(t, ws)

	for _, tc := range []struct {
		name     string
		readOnly bool
		builtins []string
		programs []string
	}{
		{"read-only", true, ReadOnlyFileTools, nil},
		{"writing", false, append(append([]string(nil), ReadOnlyFileTools...), append(WriteFileTools, "execute")...), ReadOnlyPrograms},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := faux.New(toolCallTurn("c1", "submit", map[string]any{"value": "x"}))
			var got string
			var calls int
			r, err := NewRunner(fauxConfig(p, ws))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.Run(context.Background(), Phase{
				Name: "test", System: "s", User: "u", Terminator: "submit",
				Custom: []core.Tool{submitTool(&got, &calls)}, BuiltinTools: tc.builtins,
				ReadOnly: tc.readOnly, Programs: tc.programs,
			}); err != nil {
				t.Fatalf("Run: %v", err)
			}
			reqs := p.Requests()
			if len(reqs) == 0 {
				t.Fatal("no request reached the wire")
			}
			var sb strings.Builder
			for _, blk := range reqs[0].System {
				if tb, ok := blk.(core.TextBlock); ok {
					sb.WriteString(tb.Text)
				}
			}
			sys := sb.String()
			if !strings.Contains(sys, "Your tools are exactly:") {
				t.Error("system prompt lacks 'Your tools are exactly:'")
			}
			// find_files, find_references, find_symbol must appear in that order.
			fi := strings.Index(sys, "find_files")
			fr := strings.Index(sys, "find_references")
			fs := strings.Index(sys, "find_symbol")
			if fi < 0 || fr < 0 || fs < 0 {
				t.Errorf("system prompt missing one of find_files/find_references/find_symbol: %s", sys)
			} else if !(fi < fr && fr < fs) {
				t.Errorf("tools not in sorted order: find_files@%d, find_references@%d, find_symbol@%d", fi, fr, fs)
			}
			// Extract the tool list and verify it is sorted.
			const marker = "Your tools are exactly: "
			start := strings.Index(sys, marker)
			if start < 0 {
				t.Fatal("marker not found")
			}
			after := sys[start+len(marker):]
			end := strings.Index(after, ". Calling any other tool")
			if end < 0 {
				t.Fatal("end marker not found")
			}
			list := strings.Split(after[:end], ", ")
			sorted := slices.Clone(list)
			sort.Strings(sorted)
			if !slices.Equal(list, sorted) {
				t.Errorf("tool list is not sorted: %v", list)
			}
		})
	}
}

// TS-17-9 (integration): SelectTools keeps AgentKit's PromptGuidelines for
// find_references unchanged in a phase without a shell and in a phase with one.
//
// Verifies: 17-REQ-2.3
func TestTS17_9_SelectToolsKeepsFindReferencesGuidelines(t *testing.T) {
	ws := newWorkspace(t)
	built, err := tools.All(tools.Options{Workspace: ws})
	if err != nil {
		t.Fatalf("tools.All: %v", err)
	}
	var orig []string
	for _, tl := range built {
		if tl.Name == "find_references" {
			orig = tl.PromptGuidelines
			break
		}
	}
	if len(orig) == 0 {
		t.Skip("find_references not offered or has no PromptGuidelines")
	}
	// The guidelines must not mention execute or any mutating tool.
	for _, g := range orig {
		if strings.Contains(g, "execute") {
			t.Errorf("find_references guideline mentions execute: %q", g)
		}
		for _, m := range MutatingTools {
			if strings.Contains(g, m) {
				t.Errorf("find_references guideline mentions %s: %q", m, g)
			}
		}
	}

	// Read-only phase without shell.
	ro := SelectTools(built, true, nil, ReadOnlyFileTools...)
	var roGuidelines []string
	for _, tl := range ro {
		if tl.Name == "find_references" {
			roGuidelines = tl.PromptGuidelines
			break
		}
	}
	if !slices.Equal(roGuidelines, orig) {
		t.Errorf("read-only phase guidelines differ:\ngot:  %v\nwant: %v", roGuidelines, orig)
	}

	// Writing phase with execute.
	rw := SelectTools(built, false, ReadOnlyPrograms, append(slices.Clone(ReadOnlyFileTools), "write_file", "edit_file", "execute")...)
	var rwGuidelines []string
	for _, tl := range rw {
		if tl.Name == "find_references" {
			rwGuidelines = tl.PromptGuidelines
			break
		}
	}
	if !slices.Equal(rwGuidelines, orig) {
		t.Errorf("writing phase guidelines differ:\ngot:  %v\nwant: %v", rwGuidelines, orig)
	}
}

// TS-17-12 (unit): SelectTools drops a listed name the built set lacks and
// returns the other tools in built order, and registeredTools returns a nil
// error.
//
// Verifies: 17-REQ-2.6
func TestTS17_12_SelectToolsDropsMissingName(t *testing.T) {
	// Part 1: SelectTools over a built set of six tools without find_references.
	built := []core.Tool{
		{Name: "read_file"}, {Name: "list_files"}, {Name: "find_files"},
		{Name: "search_files"}, {Name: "file_outline"}, {Name: "find_symbol"},
	}
	got := SelectTools(built, true, nil, ReadOnlyFileTools...)
	gotNames := make([]string, len(got))
	for i, tl := range got {
		gotNames[i] = tl.Name
	}
	wantNames := make([]string, len(built))
	for i, tl := range built {
		wantNames[i] = tl.Name
	}
	if !slices.Equal(gotNames, wantNames) {
		t.Errorf("SelectTools returned %v, want %v", gotNames, wantNames)
	}

	// Part 2: registeredTools for BuiltinTools = ReadOnlyFileTools plus 'no_such_tool'.
	ws := newWorkspace(t)
	r, err := NewRunner(fauxConfig(faux.New(), ws))
	if err != nil {
		t.Fatal(err)
	}
	builtins := append(slices.Clone(ReadOnlyFileTools), "no_such_tool")
	ts, err := r.registeredTools(Phase{Name: "a", BuiltinTools: builtins, ReadOnly: true})
	if err != nil {
		t.Fatalf("registeredTools returned error: %v", err)
	}
	for _, tl := range ts {
		if tl.Name == "no_such_tool" {
			t.Error("no_such_tool was not dropped")
		}
	}
	for _, n := range ReadOnlyFileTools {
		if !hasTool(ts, n) {
			// find_references may be missing if the replace target doesn't offer it.
			if n == "find_references" {
				continue // acceptable: the replace target may not offer it
			}
			t.Errorf("%s was not in the returned set", n)
		}
	}
}
