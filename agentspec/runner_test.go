package agentspec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	agentkit "github.com/agentfox/agentkit-go"
	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"
)

// newWorkspace makes a small source tree the model may read.
func newWorkspace(t *testing.T) *tools.Workspace {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/widgets\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := tools.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestWithNoWorkspaceTheModelGetsOnlyItsSubmitTool(t *testing.T) {
	p := faux.New(toolCallTurn("c1", ToolSubmitAssessment, validAssessment("ready")))
	if _, err := newFauxAgent(p).AssessPRD(context.Background(), "# PRD", "01_x"); err != nil {
		t.Fatal(err)
	}
	if names := toolNamesOf(t, p, 0); len(names) != 1 {
		t.Errorf("declared tools = %v, want only the submit tool", names)
	}
}

func TestAWorkspaceAddsTheReadToolsAndNothingThatWrites(t *testing.T) {
	// This is the capability the refactor adds: a spec agent that can read the
	// code it is describing. The mandate that it may only read is the tool
	// list, not a sentence in a prompt.
	p := faux.New(toolCallTurn("c1", ToolSubmitAssessment, validAssessment("ready")))
	opts := fauxRun(p)
	opts.Workspace = newWorkspace(t)

	if _, err := NewSpecAgentWith("STANDARD", "", opts).AssessPRD(
		context.Background(), "# PRD", "01_x"); err != nil {
		t.Fatal(err)
	}

	names := toolNamesOf(t, p, 0)
	for _, want := range []string{ToolSubmitAssessment, "read_file", "find_files", "search_files"} {
		if !slices.Contains(names, want) {
			t.Errorf("the model was not given %s; it has %v", want, names)
		}
	}
	for _, forbidden := range mutatingTools {
		if slices.Contains(names, forbidden) {
			t.Errorf("%s was declared to the model", forbidden)
		}
	}
	if slices.Contains(names, "fetch_url") {
		t.Error("fetch_url was declared; reaching it takes a second affirmative act this package does not make")
	}
}

func TestAssertReadOnlyNamesEveryMutatingToolItFound(t *testing.T) {
	// The invariant is deliberately redundant with the exclude policy and
	// with AgentKit's own unguarded-shell guard: widen the excludes by
	// mistake and the run must not start.
	err := assertReadOnly([]core.Tool{
		{Name: "read_file"}, {Name: "execute"}, {Name: "write_file"},
	})
	if err == nil {
		t.Fatal("expected the invariant to fail")
	}
	if !errors.Is(err, ErrNotReadOnly) {
		t.Errorf("the error does not unwrap to ErrNotReadOnly: %v", err)
	}
	for _, want := range []string{"execute", "write_file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %s: %v", want, err)
		}
	}
	if assertReadOnly([]core.Tool{{Name: "read_file"}, {Name: "search_files"}}) != nil {
		t.Error("a read-only set was refused")
	}
}

func TestTheExcludePolicyRemovesEveryMutatingBuiltIn(t *testing.T) {
	built, err := tools.All(tools.Options{Workspace: newWorkspace(t)})
	if err != nil {
		t.Fatal(err)
	}
	resolved := agentkit.ResolveToolPolicy(built, core.ToolPolicy{ExcludeTools: mutatingTools})
	if err := assertReadOnly(resolved); err != nil {
		t.Errorf("the policy let a mutating tool through: %v", err)
	}
	if len(resolved) == 0 {
		t.Error("the policy removed everything, so the workspace buys nothing")
	}
}

func TestARetiredPlatformVariableFailsLoudly(t *testing.T) {
	// Ignoring it would send the request to api.anthropic.com with
	// credentials meant for a managed deployment, and fail with an
	// authentication error naming neither the variable nor the reason.
	for _, name := range []string{"CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_BEDROCK"} {
		t.Setenv(name, "1")
		err := CheckRetiredPlatformVars()
		if err == nil {
			t.Errorf("%s was ignored", name)
			continue
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the error does not name %s: %v", name, err)
		}
		if !strings.Contains(err.Error(), "ANTHROPIC_BASE_URL") {
			t.Errorf("the error does not name a way through: %v", err)
		}
		t.Setenv(name, "")
	}
	if err := CheckRetiredPlatformVars(); err != nil {
		t.Errorf("an unset environment was refused: %v", err)
	}
}

func TestARetiredPlatformVariableIsCheckedBeforeAnythingIsSpent(t *testing.T) {
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	// No Model override: this is the path that resolves a real model.
	agent := NewSpecAgentWith("STANDARD", "", RunOptions{})
	if _, err := agent.AssessPRD(context.Background(), "# PRD", "01_x"); err == nil {
		t.Fatal("expected the run to be refused")
	}
}

func TestMissingCredentialsAreReportedWithTheVariablesToSet(t *testing.T) {
	for _, name := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_BASE_URL",
	} {
		t.Setenv(name, "")
	}
	m, err := ResolveModel("STANDARD", "", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	err = CheckCredentials(m)
	if err == nil {
		t.Fatal("expected a missing-credential error")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("the error does not name a variable to set: %v", err)
	}
}

func TestABaseURLAloneIsEnoughToPassThePreflight(t *testing.T) {
	// A credential has three states, not two. A gateway that authenticates by
	// URL leaves this process with no key it can read and a transport that
	// will nonetheless authenticate; refusing it would reject every such
	// deployment for a key it was never going to have.
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN"} {
		t.Setenv(name, "")
	}
	t.Setenv("ANTHROPIC_BASE_URL", "https://gateway.internal/anthropic")

	m, err := ResolveModel("STANDARD", "", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckCredentials(m); err != nil {
		t.Errorf("an ambient credential was refused: %v", err)
	}
}

func TestDefaultProvidersCarriesEveryFirstPartyWire(t *testing.T) {
	// The model is resolved from a spec the operator writes, so refusing to
	// serve a vendor after resolving one of its models would be a failure two
	// layers away from its cause.
	reg := DefaultProviders()
	for _, api := range []core.API{
		core.APIAnthropicMessages, core.APIOpenAICompletions,
		core.APIOpenAIResponses, core.APIGoogleGenerative, core.APIOllamaChat,
	} {
		if _, ok := reg.Get(api); !ok {
			t.Errorf("no provider registered for %s", api)
		}
	}
}

func TestDefaultProvidersReturnsAFreshRegistry(t *testing.T) {
	// A package-level registry would have to be frozen against late
	// registration; a pure function has nothing to race and nothing to freeze.
	a, b := DefaultProviders(), DefaultProviders()
	a.Register(core.APIProvider{API: core.API("scratch")})
	if _, ok := b.Get(core.API("scratch")); ok {
		t.Error("two calls returned the same registry")
	}
}

func TestTheBoundsDefaultRatherThanBeingUnset(t *testing.T) {
	// A zero turn budget or a zero cost cap would stop a run before its first
	// request, so the zero value has to mean "the default" and not "none".
	var o RunOptions
	if o.maxTurns() != DefaultMaxTurns {
		t.Errorf("maxTurns() = %d, want %d", o.maxTurns(), DefaultMaxTurns)
	}
	if o.maxBudget() != DefaultMaxBudgetUSD {
		t.Errorf("maxBudget() = %v, want %v", o.maxBudget(), DefaultMaxBudgetUSD)
	}
	if o.maxAttempts() != DefaultMaxAttempts {
		t.Errorf("maxAttempts() = %d, want %d", o.maxAttempts(), DefaultMaxAttempts)
	}
	set := RunOptions{MaxTurns: 7, MaxBudgetUSD: 1.5, MaxAttempts: 1}
	if set.maxTurns() != 7 || set.maxBudget() != 1.5 || set.maxAttempts() != 1 {
		t.Error("an explicit bound was overridden by a default")
	}
}

func TestTheTurnBudgetBoundsAModelThatKeepsFailingValidation(t *testing.T) {
	// The replacement for the old maxRepairs constant. Without it a model
	// that cannot satisfy a rule costs money until the context window ends
	// the run for it.
	turns := make([]faux.Turn, 0, 10)
	for i := 0; i < 10; i++ {
		turns = append(turns, toolCallTurn("c", ToolSubmitAssessment, map[string]any{"quality": "nope"}))
	}
	p := faux.New(turns...)
	opts := fauxRun(p)
	opts.MaxTurns = 3

	_, err := NewSpecAgentWith("STANDARD", "", opts).AssessPRD(context.Background(), "# PRD", "01_x")
	if err == nil {
		t.Fatal("expected the run to be stopped")
	}
	if p.Calls() > 4 {
		t.Errorf("the provider saw %d calls under a 3-turn budget", p.Calls())
	}
	if !strings.Contains(err.Error(), string(core.RunStopMaxTurns)) {
		t.Errorf("the error does not name the turn limit: %v", err)
	}
}

func TestWorkDirDefaultsToTheWorkspaceRoot(t *testing.T) {
	ws := newWorkspace(t)
	if got := (RunOptions{Workspace: ws}).workDir(); got != ws.Root {
		t.Errorf("workDir() = %q, want the workspace root %q", got, ws.Root)
	}
	if got := (RunOptions{WorkDir: "/explicit"}).workDir(); got != "/explicit" {
		t.Errorf("workDir() = %q, want the explicit value", got)
	}
	if got := (RunOptions{}).workDir(); got != "" {
		t.Errorf("workDir() = %q, want empty when there is nothing to discover", got)
	}
}

func TestConfigProjectsOntoRunOptions(t *testing.T) {
	cfg := AgentSpecConfig{
		Vendor: "openai", TrustProject: true,
		MaxTurns: 11, MaxBudgetUSD: 2.5, MaxAttempts: 1,
	}
	o := cfg.RunOptions()
	if o.Vendor != "openai" || !o.TrustProject || o.MaxTurns != 11 || o.MaxBudgetUSD != 2.5 || o.MaxAttempts != 1 {
		t.Errorf("RunOptions() = %+v", o)
	}
}

func TestAnUnsetOverlayFieldDoesNotResetTheConfiguredOne(t *testing.T) {
	// A CLI flag that was not passed must not silently undo a config file.
	base := RunOptions{Vendor: "openai", MaxTurns: 11, TrustProject: true, MaxBudgetUSD: 3}
	got := mergeRunOptions(base, RunOptions{MaxTurns: 5})
	if got.Vendor != "openai" || !got.TrustProject || got.MaxBudgetUSD != 3 {
		t.Errorf("the overlay reset configured fields: %+v", got)
	}
	if got.MaxTurns != 5 {
		t.Errorf("MaxTurns = %d, want the overlay's 5", got.MaxTurns)
	}
}
