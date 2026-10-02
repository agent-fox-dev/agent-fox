package toolio

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
)

// TS-13-32 (smoke): Operator overrides a tier's effort with --effort and the
// envelope reports the effective level.
//
// Verifies: 13-PATH-1, 13-REQ-1.3, 13-REQ-2.1, 13-REQ-2.4
//
// Real components: Common.Register, ResolveModelNamed, agentrun.ModelSpec,
// catalog.ClampThinkingLevel
func TestTS13_32_TierEffortOverrideWithEffortFlag(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("AF_MODEL_EFFORT", "")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	var modelThinking string
	var modelSpec string
	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		modelThinking = string(d.Model.Thinking)
		modelSpec = d.Model.Spec
		return ExitOK, nil, nil
	})
	app.PreflightExec = func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		modelThinking = string(d.Model.Thinking)
		modelSpec = d.Model.Spec
		return ExitOK, nil, nil
	}

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(),
		[]string{"--model", "ADVANCED", "--effort", "high", "--preflight",
			"--dir", t.TempDir(), "some input"},
		strings.NewReader(""), &stdout, &stderr)

	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, stderr.String())
	}

	// ADVANCED resolves to anthropic/claude-opus-5-5
	if modelSpec != "ADVANCED" {
		t.Errorf("model spec = %q, want %q", modelSpec, "ADVANCED")
	}

	// The effective effort should be high (overriding the tier's xhigh),
	// possibly clamped. claude-opus-5-5 supports high, so it should stay high.
	if modelThinking != string(core.ThinkingHigh) {
		t.Errorf("model thinking = %q, want %q (overriding tier's xhigh)", modelThinking, core.ThinkingHigh)
	}

	// Check the envelope reports model.thinking
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout:\n%s", err, stdout.String())
	}
	model, ok := env["model"].(map[string]any)
	if !ok {
		t.Fatalf("envelope has no model object: %v", env)
	}
	if thinking, ok := model["thinking"].(string); !ok || thinking != string(core.ThinkingHigh) {
		t.Errorf("envelope model.thinking = %v, want %q", model["thinking"], core.ThinkingHigh)
	}
}

// TS-13-33 (smoke): Operator sets effort on a model named by catalog id.
//
// Verifies: 13-PATH-2, 13-REQ-1.4
//
// Real components: Common.Register, ResolveModelNamed, catalog.ResolveModel,
// catalog.ClampThinkingLevel
func TestTS13_33_EffortOnCatalogID(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("AF_MODEL_EFFORT", "")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	var modelThinking string
	var modelID string
	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		modelThinking = string(d.Model.Thinking)
		modelID = d.Model.Model.ID
		return ExitOK, nil, nil
	})
	app.PreflightExec = func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		modelThinking = string(d.Model.Thinking)
		modelID = d.Model.Model.ID
		return ExitOK, nil, nil
	}

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(),
		[]string{"--model", "anthropic/claude-opus-5-5", "--effort", "max", "--preflight",
			"--dir", t.TempDir(), "some input"},
		strings.NewReader(""), &stdout, &stderr)

	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, stderr.String())
	}

	// The model should be resolved directly from the catalog.
	// The catalog strips the vendor prefix, so the ID is just the model name.
	if !strings.Contains(modelID, "opus-5-5") {
		t.Errorf("model ID = %q, want it to contain %q", modelID, "opus-5-5")
	}

	// The effective effort should be max or its clamped equivalent
	if modelThinking == string(core.ThinkingUnset) || modelThinking == "" {
		t.Error("expected model thinking to be set when --effort is given with a catalog id")
	}

	// Check the envelope reports model.thinking
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	model, ok := env["model"].(map[string]any)
	if !ok {
		t.Fatalf("envelope has no model object: %v", env)
	}
	if _, ok := model["thinking"].(string); !ok {
		t.Errorf("envelope model.thinking is missing or not a string: %v", model["thinking"])
	}
}

// TS-13-34 (smoke): $AF_MODEL_EFFORT overrides a tier's effort when no
// --effort flag is given.
//
// Verifies: 13-PATH-3, 13-REQ-1.3
//
// Real components: Common.Register, ResolveModelNamed, agentrun.ModelSpec,
// catalog.ClampThinkingLevel
func TestTS13_34_EnvEffortOverridesTier(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("AF_MODEL_EFFORT", "low")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	var modelThinking string
	app, _ := newApp(t, func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		modelThinking = string(d.Model.Thinking)
		return ExitOK, nil, nil
	})
	app.PreflightExec = func(_ context.Context, d Deps) (int, any, *ErrorInfo) {
		modelThinking = string(d.Model.Thinking)
		return ExitOK, nil, nil
	}

	var stdout, stderr bytes.Buffer
	// No --effort flag; $AF_MODEL_EFFORT=low should override STANDARD's high
	code := app.Main(context.Background(),
		[]string{"--model", "STANDARD", "--preflight",
			"--dir", t.TempDir(), "some input"},
		strings.NewReader(""), &stdout, &stderr)

	if code != ExitOK {
		t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, stderr.String())
	}

	// The effective effort should be low (env overrides STANDARD's high),
	// possibly clamped. claude-sonnet-5-5 supports low.
	if modelThinking != string(core.ThinkingLow) {
		t.Errorf("model thinking = %q, want %q (env overriding tier's high)", modelThinking, core.ThinkingLow)
	}

	// Check the envelope reports model.thinking
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	model, ok := env["model"].(map[string]any)
	if !ok {
		t.Fatalf("envelope has no model object: %v", env)
	}
	if thinking, ok := model["thinking"].(string); !ok || thinking != string(core.ThinkingLow) {
		t.Errorf("envelope model.thinking = %v, want %q", model["thinking"], core.ThinkingLow)
	}
}

// TS-13-36 (smoke): Passing --variant produces a removed-flag error directing
// to --effort and --model.
//
// Verifies: 13-PATH-5, 13-REQ-4.6, 13-REQ-4.7
//
// Real components: Common.Register, toolflags.unsupportedFlagMessage,
// removedFlagMessages
func TestTS13_36_VariantProducesRemovedFlagError(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	app, _ := newApp(t, nil)
	app.Name = "fix" // use a real tool name for unsupportedFlagMessage

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(),
		[]string{"--variant", "extended", "some input"},
		strings.NewReader(""), &stdout, &stderr)

	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}

	// Check stderr contains the removal message
	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "--effort") {
		t.Errorf("stderr should mention --effort: %q", stderrStr)
	}
	if !strings.Contains(stderrStr, "--model") {
		t.Errorf("stderr should mention --model: %q", stderrStr)
	}

	// Check the envelope
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout:\n%s", err, stdout.String())
	}
	errObj, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("envelope has no error object: %v", env)
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "--effort") || !strings.Contains(msg, "--model") {
		t.Errorf("error message should direct to --effort and --model: %q", msg)
	}
	// The generic 'flag provided but not defined' error should NOT be shown
	if strings.Contains(msg, "flag provided but not defined") {
		t.Errorf("the generic message leaked: %q", msg)
	}
}

// TS-13-37 (smoke): Invalid --effort value is refused with exit 2 and the
// accepted values listed.
//
// Verifies: 13-PATH-6, 13-REQ-1.6
//
// Real components: Common.Register, CLI validation
func TestTS13_37_InvalidEffortValueRefused(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	app, _ := newApp(t, nil)

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(),
		[]string{"--effort", "turbo", "some input"},
		strings.NewReader(""), &stdout, &stderr)

	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", code, ExitUsage)
	}

	// Check stderr lists the accepted effort values
	stderrStr := stderr.String()
	for _, v := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
		if !strings.Contains(stderrStr, v) {
			t.Errorf("stderr should list accepted value %q: %q", v, stderrStr)
		}
	}

	// Check the envelope
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout:\n%s", err, stdout.String())
	}
	errObj, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("envelope has no error object: %v", env)
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "turbo") {
		t.Errorf("error message should name the invalid value: %q", msg)
	}
}
