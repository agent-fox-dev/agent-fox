package codefix

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/repomap"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// realAnalysisBrain runs the analysis phase through the real agentBrain, the
// real prompt and a real agentrun.Runner whose model is scripted, and leaves
// the implementation phase to the scripted brain. What the smoke test asserts
// on is therefore the prompt that reached the (faux) wire.
type realAnalysisBrain struct {
	*scriptedBrain
	real *agentBrain
}

func (b *realAnalysisBrain) Analyze(ctx context.Context, in analysisInput) (Analysis, agentrun.Result, error) {
	return b.real.Analyze(ctx, in)
}

// smokeFauxRunner is a Runner whose provider answers the analysis phase with
// a submit_analysis call.
func smokeFauxRunner(t *testing.T, ws *tools.Workspace, args map[string]any) (*agentrun.Runner, *faux.Provider) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	p := faux.New(faux.Turn{
		Blocks:     []core.ContentBlock{faux.FauxToolCall("a1", ToolSubmitAnalysis, string(raw))},
		StopReason: core.StopReasonToolUse,
	})
	r, err := agentrun.NewRunner(agentrun.Config{
		Model:         faux.Model(),
		Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
		Workspace:     ws,
		Bounds:        agentrun.Bounds{MaxTurns: 8, MaxBudgetUSD: 1, MaxAttempts: 1},
		SessionPrefix: "fix",
	})
	if err != nil {
		t.Fatal(err)
	}
	return r, p
}

// firstUserText is the text of the first user message that reached the wire.
func firstUserText(reqs []core.Request) string {
	for _, req := range reqs {
		for _, m := range req.Messages {
			um, ok := m.(core.UserMessage)
			if !ok {
				continue
			}
			var b strings.Builder
			for _, blk := range um.Content {
				if tb, ok := blk.(core.TextBlock); ok {
					b.WriteString(tb.Text)
				}
			}
			return b.String()
		}
	}
	return ""
}

// TS-14-39 (smoke): when the map cannot be built, the fix pipeline still runs.
// The analysis phase's prompt has no map block, the run lands, and the
// envelope carries a low repo_map_build_failed warning.
//
// Verifies: 14-PATH-4, 14-REQ-10.1
//
// Real components: repomap.Build (called with an already-cancelled context so
// that its own walk fails: a corrupt source file cannot make Build fail,
// because a per-file outline failure only leaves the file without
// declarations, 14-REQ-1.6; see docs/errata/14_repo_map.md), analysisPrompt,
// agentBrain, agentrun.Phase and Runner, toolio.WarnCode.
func TestTS14_39_MapBuildFailureDegradesGracefully(t *testing.T) {
	ws, g := newRepo(t, 0)
	args := map[string]any{
		"classification": "bug", "title": "stop double counting on retry",
		"summary":    "The counter increments twice when a retry occurs.",
		"root_cause": "count.go increments before and after the retry guard.",
		"approach":   "Move the increment inside the guard.",
		"files":      []any{map[string]any{"path": "count.go", "change": "move the increment"}},
	}
	runner, prov := smokeFauxRunner(t, ws, args)
	b := &realAnalysisBrain{scriptedBrain: defaultBrain(), real: &agentBrain{runner: runner}}

	o := newOptions(ws, g, b)
	o.RepoMapTokens = 6000
	var calls int
	o.buildMap = func(ctx context.Context, w *tools.Workspace, budget int, in []string) (string, error) {
		calls++
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		return repomap.Build(cancelled, w, budget, in)
	}

	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v (a map failure must not fail the run)", err)
	}
	if got.Stage != "landed" {
		t.Errorf("Stage = %q, want landed", got.Stage)
	}
	if calls == 0 {
		t.Fatal("the pipeline never called Build")
	}

	prompt := firstUserText(prov.Requests())
	if prompt == "" {
		t.Fatal("the analysis phase never reached the model")
	}
	if strings.Contains(prompt, "## Repository map") {
		t.Errorf("the analysis prompt has a map block although the build failed:\n%s", prompt)
	}

	var found bool
	for _, w := range o.Run.Envelope(0, got, nil).Warnings {
		if w.Code == toolio.WarnRepoMapBuildFailed {
			found = true
			if w.Severity != "low" {
				t.Errorf("severity = %q, want low", w.Severity)
			}
		}
	}
	if !found {
		t.Errorf("the envelope has no repo_map_build_failed warning: %+v", o.Run.Envelope(0, got, nil).Warnings)
	}
}

// systemText is the text of a request's system prompt.
func systemText(req core.Request) string {
	var b strings.Builder
	for _, blk := range req.System {
		if tb, ok := blk.(core.TextBlock); ok {
			b.WriteString(tb.Text)
		}
	}
	return b.String()
}

// TS-17-21 (smoke): fix's analyse phase, a read-only phase with the file
// tools and a shell, reaches the model with the rendered tool guidelines, the
// search-over-execute line once, the preference line limiting the shell to git,
// and an execute description that discourages reading through the shell.
//
// Verifies: 17-PATH-1, 17-REQ-7.1, 17-REQ-5.1
//
// Real components: agentBrain, analysisPrompt, agentrun.Phase and Runner, the
// built-in tools and their descriptions. Only the model is scripted.
func TestTS17_21_FixAnalysePromptCarriesTheToolGuidance(t *testing.T) {
	ws, g := newRepo(t, 0)
	args := map[string]any{
		"classification": "bug", "title": "stop double counting on retry",
		"summary":    "The counter increments twice when a retry occurs.",
		"root_cause": "count.go increments before and after the retry guard.",
		"approach":   "Move the increment inside the guard.",
		"files":      []any{map[string]any{"path": "count.go", "change": "move the increment"}},
	}
	runner, prov := smokeFauxRunner(t, ws, args)
	b := &realAnalysisBrain{scriptedBrain: defaultBrain(), real: &agentBrain{runner: runner}}
	if _, err := Run(context.Background(), newOptions(ws, g, b)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	reqs := prov.Requests()
	if len(reqs) == 0 {
		t.Fatal("the analyse phase never reached the model")
	}
	sys := systemText(reqs[0])
	for _, want := range []string{"\n\nGuidelines:\n",
		"Report the diagnosis by calling submit_analysis; do not write it as prose.",
		"Read files with `read_file`", "use `execute` only for git"} {
		if !strings.Contains(sys, want) {
			t.Errorf("the analyse system prompt lacks %q", want)
		}
	}
	if strings.Contains(sys, "building, formatting and testing") {
		t.Error("the read-only analyse phase is told it may build and test")
	}
	if n := strings.Count(sys, tools.SearchOverExecuteGuideline); n != 1 {
		t.Errorf("the search-over-execute line appears %d times", n)
	}
	var exec string
	for _, tl := range reqs[0].Tools {
		if tl.Name == "execute" {
			exec = tl.Description
		}
	}
	if !strings.Contains(exec, "cat") || !strings.Contains(exec, "grep") || !strings.Contains(exec, ".gitignore") {
		t.Errorf("the execute description does not discourage shell reads: %s", exec)
	}
}
