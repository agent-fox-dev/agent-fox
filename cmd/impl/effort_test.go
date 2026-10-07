package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"path/filepath"
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

// implPreflight runs the real impl app, as main does, with --preflight and the
// given flags against a prepared spec repository, and returns the exit code and
// the envelope: the run's resolved model, its warnings and error, and the
// preflight checklist. The tests below assert on that, not on a stub that
// calls resolveRepairRunner by hand.
type implPreflightEnvelope struct {
	toolio.Envelope
	Result struct {
		Preflight []toolio.PreflightCheck `json:"preflight"`
	} `json:"result"`
}

func implPreflight(t *testing.T, dir string, args ...string) (int, implPreflightEnvelope) {
	t.Helper()
	argv := append([]string{"--preflight", "--dir", dir, "--land", "none"}, args...)
	argv = append(argv, "09")
	var stdout, stderr bytes.Buffer
	code := newApp().Main(context.Background(), argv, strings.NewReader(""), &stdout, &stderr)
	var env implPreflightEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not an envelope: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	return code, env
}

// repairEntry is the repair_model_credential check, which names the model and
// the effort the repair phase will run at.
func (e implPreflightEnvelope) repairEntry() (toolio.PreflightCheck, bool) {
	for _, c := range e.Result.Preflight {
		if c.Check == "repair_model_credential" {
			return c, true
		}
	}
	return toolio.PreflightCheck{}, false
}

// TS-13-13: repair effort precedence is --repair-model-effort, then the repair
// model's tier effort, then the run's effort — through the real --preflight,
// where the checklist reports the model and effort the repair phase will run at.
// Verifies: 13-REQ-3.2
func TestTS13_13_RepairEffortPrecedence(t *testing.T) {
	implEnv(t)
	t.Setenv("AF_MODEL_EFFORT", "")
	dir, _ := preflightSpecRepo(t)

	cases := []struct {
		name        string
		args        []string
		wantEffort  string // in the repair_model_credential detail; "" means no repair entry
		wantRunLow  bool
		wantModelIn string
	}{
		{"--repair-model-effort wins over the tier's", []string{"--model", "STANDARD", "--effort", "low", "--repair-model", "ADVANCED", "--repair-model-effort", "high"}, "effort high", true, "opus"},
		{"the tier's own effort when no --repair-model-effort", []string{"--model", "STANDARD", "--effort", "low", "--repair-model", "ADVANCED"}, "effort xhigh", true, "opus"},
		// With no --repair-model and no --repair-model-effort the repair phase
		// runs on the run's own runner: nothing separate is resolved or reported.
		{"the run's own runner when neither is given", []string{"--model", "STANDARD", "--effort", "low"}, "", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, env := implPreflight(t, dir, tc.args...)
			if code != toolio.ExitOK {
				t.Fatalf("code = %d, error %+v", code, env.Error)
			}
			entry, ok := env.repairEntry()
			if tc.wantEffort == "" {
				if ok {
					t.Errorf("a repair_model_credential entry was reported with no repair model: %+v", entry)
				}
			} else {
				if !ok || !entry.OK {
					t.Fatalf("no ok repair_model_credential entry in %+v", env.Result.Preflight)
				}
				if !strings.Contains(entry.Detail, tc.wantEffort) || !strings.Contains(entry.Detail, tc.wantModelIn) {
					t.Errorf("repair_model_credential detail = %q, want the %s model at %s", entry.Detail, tc.wantModelIn, tc.wantEffort)
				}
			}
			// The run's own effort is untouched by the repair phase's.
			if tc.wantRunLow && (env.Model == nil || env.Model.Thinking != "low") {
				t.Errorf("the run's model = %+v, want thinking low", env.Model)
			}
		})
	}
}

// TS-13-14: --repair-model-effort alone implies --repair and runs the repair
// phase on the run's own model at the stated effort — through the real app.
// "Implies --repair" is observable in PreCheck: --repair cannot be combined with
// --no-verify, so --repair-model-effort with --no-verify is refused as usage.
// Verifies: 13-REQ-3.3
func TestTS13_14_RepairModelEffortAloneImpliesRepair(t *testing.T) {
	implEnv(t)
	t.Setenv("AF_MODEL_EFFORT", "")
	dir, _ := preflightSpecRepo(t)

	code, env := implPreflight(t, dir, "--repair-model-effort", "high")
	if code != toolio.ExitOK {
		t.Fatalf("code = %d, error %+v", code, env.Error)
	}
	entry, ok := env.repairEntry()
	if !ok || !entry.OK {
		t.Fatalf("no repair_model_credential entry for --repair-model-effort alone: %+v", env.Result.Preflight)
	}
	if env.Model == nil || !strings.Contains(entry.Detail, env.Model.ID) {
		t.Errorf("repair_model_credential detail = %q, want the run's own model %v", entry.Detail, env.Model)
	}
	if !strings.Contains(entry.Detail, "effort high") {
		t.Errorf("repair_model_credential detail = %q, want it to name effort high", entry.Detail)
	}

	// The implication itself.
	code, env = implPreflight(t, dir, "--repair-model-effort", "high", "--no-verify")
	if code != toolio.ExitUsage {
		t.Fatalf("--repair-model-effort with --no-verify: code = %d, want a usage error: the flag implies --repair", code)
	}
	if env.Error == nil || !strings.Contains(env.Error.Message, "--repair and --no-verify") {
		t.Errorf("error = %+v, want the --repair/--no-verify refusal", env.Error)
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

// TS-13-15 (through the real app): 13-REQ-3.4 — when the repair effort is
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

// 13-REQ-2.3 for the repair phase: an explicit --repair-model-effort that no
// level of the repair model can serve is a usage error (exit 2, category
// usage), as it is for the run's own model, before the first request.
func TestUnreachableRepairEffortIsAUsageError(t *testing.T) {
	implEnv(t)
	t.Setenv("GOOGLE_API_KEY", "test-key")
	t.Setenv("AF_MODEL_EFFORT", "")
	dir, _ := preflightSpecRepo(t)

	// google/gemini-3.5-flash-lite supports no thinking level at all.
	for _, mode := range [][]string{{"--preflight"}, {}} {
		argv := append(append([]string{}, mode...), "--dir", dir, "--land", "none",
			"--repair-model", "google/gemini-3.5-flash-lite", "--repair-model-effort", "high", "09")
		var stdout, stderr bytes.Buffer
		code := newApp().Main(context.Background(), argv, strings.NewReader(""), &stdout, &stderr)
		if code != toolio.ExitUsage {
			t.Errorf("%v: exit = %d, want %d\nstdout:\n%s", mode, code, toolio.ExitUsage, stdout.String())
		}
		var env toolio.Envelope
		if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
			t.Fatalf("%v: stdout is not an envelope: %v\n%s", mode, err, stdout.String())
		}
		if env.Error == nil || env.Error.Category != "usage" {
			t.Errorf("%v: error = %+v, want category usage", mode, env.Error)
		}
		if env.Error != nil && !strings.Contains(env.Error.Message, "gemini-3.5-flash-lite") {
			t.Errorf("%v: the message should name the repair model: %q", mode, env.Error.Message)
		}
	}
}

// 04-REQ-4 / 04-PATH-4 through the real cmd/impl app: a nested GitLab
// --repo is parsed by impl's own flag handling and reaches the options, so the
// preflight's land target is that project (and a GitLab credential from the
// environment is what satisfies the forge check). TS-04-39's smoke test in
// internal/toolio cannot import this package.
func TestImplRepoFlagReachesTheLandTarget(t *testing.T) {
	implEnv(t)
	t.Setenv("GITLAB_TOKEN", "gl-token")
	for _, v := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITHUB_API_URL", "GITLAB_API_URL", "AF_MODEL_EFFORT"} {
		t.Setenv(v, "")
	}
	dir, _ := preflightSpecRepo(t)
	// --land pr pushes, so the repository needs an origin to push to.
	origin := filepath.Join(t.TempDir(), "origin.git")
	gitIn(t, dir, "init", "-q", "--bare", "-b", "main", origin)
	gitIn(t, dir, "remote", "add", "origin", origin)

	var stdout, stderr bytes.Buffer
	code := newApp().Main(context.Background(),
		[]string{"--preflight", "--dir", dir, "--land", "pr", "--repo", "gitlab-org/subgroup/repo", "09"},
		strings.NewReader(""), &stdout, &stderr)
	if code != toolio.ExitOK {
		t.Fatalf("code = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var env struct {
		Result struct {
			Preflight []toolio.PreflightCheck `json:"preflight"`
		} `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not an envelope: %v\n%s", err, stdout.String())
	}
	var target *toolio.PreflightCheck
	for i, c := range env.Result.Preflight {
		if c.Check == "land_target" {
			target = &env.Result.Preflight[i]
		}
	}
	if target == nil || target.Detail != "gitlab-org/subgroup/repo" {
		t.Errorf("land_target = %+v, want the nested project gitlab-org/subgroup/repo", target)
	}

	// An unparsable --repo is refused by impl's own PreCheck, as a usage error.
	code = newApp().Main(context.Background(),
		[]string{"--preflight", "--dir", dir, "--repo", "not a repo", "09"},
		strings.NewReader(""), &stdout, &stderr)
	if code != toolio.ExitUsage {
		t.Errorf("--repo %q: code = %d, want a usage error (%d)", "not a repo", code, toolio.ExitUsage)
	}
}
