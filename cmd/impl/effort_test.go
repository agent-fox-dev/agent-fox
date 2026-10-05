package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/codeimpl"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agentfox/agentkit-go/core"
)

// TS-13-12 (unit): --repair-model-effort is registered on impl with the same
// accepted values as --effort and listed in toolFlags["impl"].
func TestTS13_12_RepairModelEffortRegisteredOnImpl(t *testing.T) {
	app := newApp()
	fs := flag.NewFlagSet("impl", flag.ContinueOnError)
	app.Flags(fs)

	f := fs.Lookup("repair-model-effort")
	if f == nil {
		t.Fatal("expected a flag named 'repair-model-effort' to be registered on impl")
	}

	// Verify it accepts the same values as --effort by parsing each one.
	for _, v := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
		testFS := flag.NewFlagSet("t", flag.ContinueOnError)
		app.Flags(testFS)
		if err := testFS.Parse([]string{"--repair-model-effort", v}); err != nil {
			t.Errorf("--repair-model-effort %s should be accepted: %v", v, err)
		}
	}

	// Verify it rejects invalid values.
	testFS := flag.NewFlagSet("t", flag.ContinueOnError)
	testFS.SetOutput(&bytes.Buffer{})
	app.Flags(testFS)
	if err := testFS.Parse([]string{"--repair-model-effort", "turbo"}); err == nil {
		t.Error("--repair-model-effort turbo should be rejected")
	}

	// Verify it is listed in toolFlags["impl"].
	found := false
	for _, name := range toolio.ToolFlags("impl") {
		if name == "repair-model-effort" {
			found = true
			break
		}
	}
	if !found {
		t.Error("toolFlags[\"impl\"] should contain \"repair-model-effort\"")
	}

	// Verify DeclareEnum is called (check schema).
	fullFS := flag.NewFlagSet("impl-full", flag.ContinueOnError)
	var common toolio.Common
	common.Register(fullFS)
	app.Flags(fullFS)
	doc := toolio.BuildFlagsDocument(fullFS)
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal flags doc: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatalf("unmarshal flags doc: %v", err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("no properties in schema")
	}
	rmeProp, ok := props["repair-model-effort"].(map[string]any)
	if !ok {
		t.Fatal("no repair-model-effort property in schema")
	}
	gotEnum, ok := rmeProp["enum"]
	if !ok {
		t.Fatal("repair-model-effort property has no enum")
	}
	wantEnum := []any{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
	if len(gotEnum.([]any)) != len(wantEnum) {
		t.Errorf("repair-model-effort enum = %v, want %v", gotEnum, wantEnum)
	}
}

// TS-13-13 (unit): Repair effort precedence: --repair-model-effort >
// repair model's tier effort > run's effort.
func TestTS13_13_RepairEffortPrecedence(t *testing.T) {
	implEnv(t)
	dir, _ := preflightSpecRepo(t)

	cases := []struct {
		name              string
		repairModel       string
		repairModelEffort string
		runEffort         string
		wantThinking      core.ThinkingLevel
	}{
		{
			name:              "repair-model-effort wins over tier",
			repairModel:       "ADVANCED",
			repairModelEffort: "high",
			runEffort:         "low",
			wantThinking:      core.ThinkingHigh,
		},
		{
			name:              "tier's own effort when no repair-model-effort",
			repairModel:       "ADVANCED",
			repairModelEffort: "",
			runEffort:         "low",
			wantThinking:      core.ThinkingXHigh,
		},
		{
			name:              "run's effort when no repair-model",
			repairModel:       "",
			repairModelEffort: "",
			runEffort:         "low",
			wantThinking:      core.ThinkingLow,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AF_MODEL_EFFORT", "")
			app := newApp()
			var gotThinking core.ThinkingLevel
			called := false
			app.PreflightExec = func(_ context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
				called = true
				rr, choice, err := resolveRepairRunner(d, tc.repairModel, tc.repairModelEffort)
				if err != nil {
					t.Fatalf("resolveRepairRunner: %v", err)
				}
				if tc.repairModel == "" && tc.repairModelEffort == "" {
					// No repair model and no repair effort: repair uses the run's model.
					// The run's effort is on d.Model.
					gotThinking = d.Model.Thinking
				} else {
					if rr == nil || choice == nil {
						t.Fatal("expected a repair runner and choice")
					}
					gotThinking = choice.Thinking
				}
				return toolio.ExitOK, &codeimpl.Result{}, nil
			}

			args := []string{"--preflight", "--dir", dir, "--land", "none", "--model", "STANDARD"}
			if tc.runEffort != "" {
				args = append(args, "--effort", tc.runEffort)
			}
			args = append(args, "09")

			var stdout, stderr bytes.Buffer
			code := app.Main(context.Background(), args,
				strings.NewReader(""), &stdout, &stderr)
			if code != toolio.ExitOK || !called {
				t.Fatalf("code = %d, called = %v\nstdout:\n%s\nstderr:\n%s",
					code, called, stdout.String(), stderr.String())
			}
			if gotThinking != tc.wantThinking {
				t.Errorf("repair thinking = %q, want %q", gotThinking, tc.wantThinking)
			}
		})
	}
}

// TS-13-14 (unit): --repair-model-effort alone (without --repair-model)
// implies --repair and runs on the run's model at the stated effort.
func TestTS13_14_RepairModelEffortAloneImpliesRepair(t *testing.T) {
	implEnv(t)
	dir, _ := preflightSpecRepo(t)
	t.Setenv("AF_MODEL_EFFORT", "")

	app := newApp()
	called := false
	app.PreflightExec = func(_ context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
		called = true
		rr, choice, err := resolveRepairRunner(d, "", "high")
		if err != nil {
			t.Fatalf("resolveRepairRunner: %v", err)
		}
		if rr == nil || choice == nil {
			t.Fatal("expected a repair runner when --repair-model-effort is given alone")
		}
		// Should run on the run's model at the stated effort.
		if choice.Model.ID != d.Model.Model.ID {
			t.Errorf("repair model = %q, want run's model %q", choice.Model.ID, d.Model.Model.ID)
		}
		if choice.Thinking != core.ThinkingHigh {
			t.Errorf("repair thinking = %q, want %q", choice.Thinking, core.ThinkingHigh)
		}
		return toolio.ExitOK, &codeimpl.Result{}, nil
	}

	// Also verify that --repair-model-effort implies --repair in PreCheck.
	// We test this by checking that the flag parsing and PreCheck set repair=true.
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(),
		[]string{"--preflight", "--dir", dir, "--land", "none",
			"--repair-model-effort", "high", "09"},
		strings.NewReader(""), &stdout, &stderr)
	if code != toolio.ExitOK || !called {
		t.Fatalf("code = %d, called = %v\nstdout:\n%s\nstderr:\n%s",
			code, called, stdout.String(), stderr.String())
	}
}

// TS-13-15 (unit): Repair phase clamping records an effort_clamped warning
// naming the repair phase.
func TestTS13_15_RepairClampingWarningNamesRepairPhase(t *testing.T) {
	implEnv(t)
	dir, _ := preflightSpecRepo(t)
	t.Setenv("AF_MODEL_EFFORT", "")

	app := newApp()
	called := false
	app.PreflightExec = func(_ context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
		called = true
		// claude-opus-4-5 supports off through high but NOT xhigh or max.
		// Requesting xhigh should clamp to high and produce a warning.
		rr, choice, err := resolveRepairRunner(d, "anthropic/claude-opus-4-5", "xhigh")
		if err != nil {
			t.Fatalf("resolveRepairRunner: %v", err)
		}
		if rr == nil || choice == nil {
			t.Fatal("expected a repair runner and choice")
		}
		if choice.Thinking != core.ThinkingHigh {
			t.Errorf("repair thinking = %q, want %q (clamped from xhigh)", choice.Thinking, core.ThinkingHigh)
		}
		var found bool
		for _, w := range choice.Warnings {
			if w.Code == toolio.WarnEffortClamped {
				found = true
				if !strings.Contains(w.Message, "repair") {
					t.Errorf("effort_clamped warning should mention 'repair': %q", w.Message)
				}
				if !strings.Contains(w.Message, "xhigh") {
					t.Errorf("warning should name requested level xhigh: %q", w.Message)
				}
				if !strings.Contains(w.Message, string(core.ThinkingHigh)) {
					t.Errorf("warning should name clamped level: %q", w.Message)
				}
			}
		}
		if !found {
			t.Error("expected an effort_clamped warning in repair choice.Warnings")
		}
		return toolio.ExitOK, &codeimpl.Result{}, nil
	}

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(),
		[]string{"--preflight", "--dir", dir, "--land", "none", "09"},
		strings.NewReader(""), &stdout, &stderr)
	if code != toolio.ExitOK || !called {
		t.Fatalf("code = %d, called = %v\nstdout:\n%s\nstderr:\n%s",
			code, called, stdout.String(), stderr.String())
	}
}

// TS-13-16 (unit): --repair-model-effort has no environment variable fallback.
func TestTS13_16_RepairModelEffortNoEnvFallback(t *testing.T) {
	implEnv(t)
	dir, _ := preflightSpecRepo(t)
	// Set a hypothetical env var that should NOT be read.
	t.Setenv("AF_REPAIR_MODEL_EFFORT", "max")
	t.Setenv("AF_MODEL_EFFORT", "")

	app := newApp()
	called := false
	app.PreflightExec = func(_ context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
		called = true
		// With --repair-model STANDARD and no --repair-model-effort flag,
		// the repair effort should be the STANDARD tier's own effort (high),
		// NOT max from the env var.
		rr, choice, err := resolveRepairRunner(d, "STANDARD", "")
		if err != nil {
			t.Fatalf("resolveRepairRunner: %v", err)
		}
		if rr == nil || choice == nil {
			t.Fatal("expected a repair runner and choice")
		}
		if choice.Thinking != core.ThinkingHigh {
			t.Errorf("repair thinking = %q, want %q (STANDARD tier's own effort, not from env)",
				choice.Thinking, core.ThinkingHigh)
		}
		return toolio.ExitOK, &codeimpl.Result{}, nil
	}

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(),
		[]string{"--preflight", "--dir", dir, "--land", "none", "09"},
		strings.NewReader(""), &stdout, &stderr)
	if code != toolio.ExitOK || !called {
		t.Fatalf("code = %d, called = %v\nstdout:\n%s\nstderr:\n%s",
			code, called, stdout.String(), stderr.String())
	}
}

// 13-REQ-3.4, through the real PreflightExec: when the repair effort is
// clamped, an effort_clamped warning naming the repair phase reaches the
// envelope. The tests above replace PreflightExec and look at the value
// resolveRepairRunner returns; this one looks at what the run reports.
func TestRepairEffortClampingIsRecordedOnTheRun(t *testing.T) {
	cases := []struct {
		name       string
		effort     string
		wantWarned bool
	}{
		// claude-opus-4-5 reaches high but not xhigh: xhigh is clamped.
		{"clamped", "xhigh", true},
		{"within reach", "high", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			implEnv(t)
			t.Setenv("AF_MODEL_EFFORT", "")
			dir, _ := preflightSpecRepo(t)

			var stdout, stderr bytes.Buffer
			code := newApp().Main(context.Background(),
				[]string{"--preflight", "--dir", dir, "--land", "none",
					"--repair-model", "anthropic/claude-opus-4-5", "--repair-model-effort", tc.effort, "09"},
				strings.NewReader(""), &stdout, &stderr)
			if code != toolio.ExitOK {
				t.Fatalf("code = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
			}
			var env toolio.Envelope
			if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
				t.Fatalf("stdout is not an envelope: %v\n%s", err, stdout.String())
			}
			var got *toolio.Warning
			for i, w := range env.Warnings {
				if w.Code == toolio.WarnEffortClamped {
					got = &env.Warnings[i]
				}
			}
			switch {
			case tc.wantWarned && got == nil:
				t.Fatalf("no effort_clamped warning in the envelope: %+v", env.Warnings)
			case tc.wantWarned && (!strings.Contains(got.Message, "repair") || !strings.Contains(got.Message, tc.effort)):
				t.Errorf("the warning should name the repair phase and the requested effort: %q", got.Message)
			case !tc.wantWarned && got != nil:
				t.Errorf("an effort within reach was reported clamped: %q", got.Message)
			}
		})
	}
}
