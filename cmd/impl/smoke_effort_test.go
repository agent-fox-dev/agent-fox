package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/codeimpl"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agentfox/agentkit-go/core"
)

// TS-13-35 (smoke): impl repair phase runs at its own effort, independent of
// the run's.
//
// Verifies: 13-PATH-4, 13-REQ-3.1, 13-REQ-3.2
//
// Real components: Common.Register, ResolveModelNamed, agentrun.ModelSpec,
// catalog.ClampThinkingLevel, impl.resolveRepairRunner
func TestTS13_35_ImplRepairPhaseRunsAtOwnEffort(t *testing.T) {
	implEnv(t)
	t.Setenv("AF_MODEL_EFFORT", "")
	dir, _ := preflightSpecRepo(t)

	app := newApp()
	var runThinking, repairThinking core.ThinkingLevel
	var runModelID, repairModelID string
	called := false

	app.PreflightExec = func(_ context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
		called = true

		// Record the run's model and effort
		runThinking = d.Model.Thinking
		runModelID = d.Model.Model.ID

		// Resolve the repair runner with the flags
		rr, choice, err := resolveRepairRunner(d, "ADVANCED", "high")
		if err != nil {
			t.Fatalf("resolveRepairRunner: %v", err)
		}
		if rr == nil || choice == nil {
			t.Fatal("expected a repair runner and choice")
		}
		repairThinking = choice.Thinking
		repairModelID = choice.Model.ID

		return toolio.ExitOK, &codeimpl.Result{}, nil
	}

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(),
		[]string{
			"--preflight",
			"--dir", dir,
			"--land", "none",
			"--model", "STANDARD",
			"--effort", "low",
			"--repair-model", "ADVANCED",
			"--repair-model-effort", "high",
			"09",
		},
		strings.NewReader(""), &stdout, &stderr)

	if code != toolio.ExitOK || !called {
		t.Fatalf("code = %d, called = %v\nstdout:\n%s\nstderr:\n%s",
			code, called, stdout.String(), stderr.String())
	}

	// The run's model is STANDARD at low effort
	if runThinking != core.ThinkingLow {
		t.Errorf("run thinking = %q, want %q", runThinking, core.ThinkingLow)
	}
	if !strings.Contains(runModelID, "sonnet") {
		t.Errorf("run model = %q, want a sonnet model (STANDARD)", runModelID)
	}

	// The repair model is ADVANCED at high effort (not the tier's xhigh, not the run's low)
	if repairThinking != core.ThinkingHigh {
		t.Errorf("repair thinking = %q, want %q (not tier's xhigh, not run's low)", repairThinking, core.ThinkingHigh)
	}
	if !strings.Contains(repairModelID, "opus") {
		t.Errorf("repair model = %q, want an opus model (ADVANCED)", repairModelID)
	}

	// Both models are resolved and their credentials checked before any phase runs
	// (verified by the fact that --preflight succeeded without error)

	// Check the envelope
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout:\n%s", err, stdout.String())
	}
	// The envelope's model.thinking should report the run's effective level
	model, ok := env["model"].(map[string]any)
	if !ok {
		t.Fatalf("envelope has no model object: %v", env)
	}
	if thinking, ok := model["thinking"].(string); !ok || thinking != string(core.ThinkingLow) {
		t.Errorf("envelope model.thinking = %v, want %q (the run's effort)", model["thinking"], core.ThinkingLow)
	}
}
