package toolio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/schema"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// mapPhaseApp is a tool whose one phase carries repoMap, run by a real
// agentrun.Runner whose Observer is the Progress the shell built from the
// command line, the way cmd/fix wires it.
func mapPhaseApp(repoMap string) toolio.App {
	return toolio.App{
		Name:    "fix",
		Version: "test",
		Usage:   "fix [flags] <input>\n",
		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			p := faux.New(toolCallTurn("t1", "submit", map[string]any{"value": "done"}))
			runner, err := agentrun.NewRunner(agentrun.Config{
				Model:     faux.Model(),
				Providers: core.ProviderRegistry{faux.API: p.APIProvider()},
				Workspace: d.Workspace,
				Bounds:    agentrun.Bounds{MaxTurns: 4, MaxBudgetUSD: 1, MaxAttempts: 1},
				Observer:  d.Progress,
			})
			if err != nil {
				return toolio.ExitFailed, nil, &toolio.ErrorInfo{Stage: "runner", Message: err.Error()}
			}
			submit := core.Tool{
				Name:        "submit",
				Description: "submit the answer",
				InputSchema: schema.Object(schema.Prop("value", schema.String("the answer"))),
				Execute: func(context.Context, json.RawMessage) core.ToolResult {
					res := core.OKResult(map[string]any{"accepted": true})
					res.Terminate = true
					return res
				},
			}
			if _, err := runner.Run(ctx, agentrun.Phase{
				Name: "analyse", System: "system", User: "do the thing", RepoMap: repoMap,
				Terminator: "submit", Custom: []core.Tool{submit},
			}); err != nil {
				return toolio.ExitFailed, nil, &toolio.ErrorInfo{Stage: "analyse", Message: err.Error()}
			}
			return toolio.ExitOK, map[string]string{"stage": "done"}, nil
		},
	}
}

// TS-14-25 (unit): under --verbose the runner logs the map's token count, and
// without it the count is not shown.
//
// Verifies: 14-REQ-7.2
//
// The spec's pseudocode sets a Verbose field on the Runner, which has none:
// --verbose reaches the runner as its Observer, toolio.Progress, whose Detail
// prints only under the flag (docs/errata/14_repo_map.md). This test drives
// that whole path: the flag through App.Main, the Progress the shell builds,
// a real Runner and Observer.Detail.
func TestTS14_25_VerboseShowsTheRepoMapTokenCount(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	m := strings.Repeat("x", 400)
	want := fmt.Sprintf("repo map: %d tokens", afspec.EstimateTokens(m))
	if want != "repo map: 100 tokens" {
		t.Fatalf("fixture is %q, want 100 tokens", want)
	}

	run := func(repoMap string, flags ...string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		argv := append(append([]string{"--dir", t.TempDir()}, flags...), "some input")
		if code := mapPhaseApp(repoMap).Main(context.Background(), argv, strings.NewReader(""), &stdout, &stderr); code != toolio.ExitOK {
			t.Fatalf("Main(%v) = %d\nstdout: %s\nstderr: %s", argv, code, stdout.String(), stderr.String())
		}
		return stderr.String()
	}

	if got := run(m, "--verbose"); !strings.Contains(got, want) {
		t.Errorf("--verbose stderr lacks %q:\n%s", want, got)
	}
	if got := run(m); strings.Contains(got, "repo map") {
		t.Errorf("without --verbose the map's size was shown:\n%s", got)
	}
	if got := run("", "--verbose"); strings.Contains(got, "repo map") {
		t.Errorf("an empty map was logged under --verbose:\n%s", got)
	}
}
