package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// runModels executes `spec models` and decodes its envelope.
func runModels(t *testing.T, args ...string) map[string]any {
	t.Helper()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs(append([]string{"models"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("spec models: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("the output is not JSON: %v\n%s", err, out.String())
	}
	if env["ok"] != true {
		t.Errorf("envelope is not ok: %v", env)
	}
	return env
}

func TestModelsReportsWhatEachPhaseWillRunOn(t *testing.T) {
	// Which model a phase uses, what it costs and whether this shell can
	// authenticate to it were three things an operator could previously only
	// learn from a bill.
	env := runModels(t)
	rows, ok := env["models"].([]any)
	if !ok || len(rows) != 3 {
		t.Fatalf("models = %v, want one row per phase", env["models"])
	}
	seen := map[string]bool{}
	for _, r := range rows {
		row := r.(map[string]any)
		seen[row["phase"].(string)] = true
		if row["model"] == "" {
			t.Errorf("phase %v resolved to no model", row["phase"])
		}
		if row["context_window"].(float64) == 0 {
			t.Errorf("phase %v reports no context window", row["phase"])
		}
		if row["credential"] == "" {
			t.Errorf("phase %v reports no credential state", row["phase"])
		}
	}
	for _, phase := range []string{"assess", "refine", "generate"} {
		if !seen[phase] {
			t.Errorf("no row for the %s phase", phase)
		}
	}
}

func TestModelsAllListsEveryTierOfEveryVendor(t *testing.T) {
	env := runModels(t, "--all")
	rows := env["models"].([]any)
	vendors := map[string]int{}
	for _, r := range rows {
		row := r.(map[string]any)
		if row["phase"] == nil {
			vendors[row["vendor"].(string)]++
		}
	}
	if len(vendors) < 2 {
		t.Errorf("--all listed %d vendors; the point of the flag is choosing between them", len(vendors))
	}
	for vendor, n := range vendors {
		if n != 3 {
			t.Errorf("vendor %s has %d tiers listed, want 3", vendor, n)
		}
	}
}

func TestModelsReportsAMissingCredentialWithoutFailing(t *testing.T) {
	// Reporting is the command's whole job: exiting non-zero because a vendor
	// is unconfigured would make it useless for finding out that it is.
	for _, name := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_BASE_URL",
	} {
		t.Setenv(name, "")
	}
	env := runModels(t)
	for _, r := range env["models"].([]any) {
		if got := r.(map[string]any)["credential"]; got != "missing" {
			t.Errorf("credential = %v with nothing configured, want missing", got)
		}
	}
}

func TestModelsHonoursTheModelEnvironmentOverride(t *testing.T) {
	t.Setenv("AF_SPEC_MODEL", "anthropic/claude-haiku-4-5")
	env := runModels(t)
	for _, r := range env["models"].([]any) {
		row := r.(map[string]any)
		if got := row["model"].(string); got != "claude-haiku-4-5" {
			t.Errorf("phase %v resolved to %q, want the override", row["phase"], got)
		}
	}
}

func TestModelsReportsAnUnresolvableModelRatherThanFailing(t *testing.T) {
	t.Setenv("AF_SPEC_MODEL", "not-a-model-anyone-ships")
	env := runModels(t)
	for _, r := range env["models"].([]any) {
		row := r.(map[string]any)
		if !strings.HasPrefix(row["model"].(string), "unresolved:") {
			t.Errorf("model = %q, want it reported as unresolved", row["model"])
		}
	}
}
