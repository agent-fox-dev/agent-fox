package agentfox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TS-13-27 (unit): docs/cli.md replaces --variant with --effort, adds
// --repair-model-effort to impl, and records the interface-versions entry
// Verifies: 13-REQ-7.1
func TestTS13_27_DocsCliEffortReplacesVariant(t *testing.T) {
	root := findWorkspaceRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docs", "cli.md"))
	if err != nil {
		t.Fatalf("reading docs/cli.md: %v", err)
	}
	src := string(body)

	// The shared-flags table must contain --effort and not --variant.
	shared := docSection(t, src, "Shared flags")
	if !strings.Contains(shared, "--effort") {
		t.Error("shared-flags section does not contain --effort")
	}
	if strings.Contains(shared, "--variant") {
		t.Error("shared-flags section still contains --variant")
	}

	// impl's flags section must contain --repair-model-effort.
	implSec := docSection(t, src, "`impl`")
	if !strings.Contains(implSec, "--repair-model-effort") {
		t.Error("impl section does not contain --repair-model-effort")
	}

	// The --repair-model paragraph must not reference --variant.
	if strings.Contains(implSec, "--variant") {
		t.Error("impl section still references --variant")
	}

	// The interface-versions section must mention the --variant removal and
	// --effort addition.
	versions := docSection(t, src, "Interface versions")
	if !strings.Contains(versions, "variant") {
		t.Error("interface-versions section does not mention the variant removal")
	}
	if !strings.Contains(versions, "--effort") {
		t.Error("interface-versions section does not mention --effort addition")
	}
}

// TS-13-28 (unit): docs/configuration.md documents $AF_MODEL_EFFORT, the
// effort precedence, and the long-context example
// Verifies: 13-REQ-7.2
func TestTS13_28_DocsConfigurationEffort(t *testing.T) {
	root := findWorkspaceRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docs", "configuration.md"))
	if err != nil {
		t.Fatalf("reading docs/configuration.md: %v", err)
	}
	src := string(body)

	if strings.Contains(src, "--variant extended") {
		t.Error("docs/configuration.md still contains --variant extended")
	}
	if !strings.Contains(src, "AF_MODEL_EFFORT") {
		t.Error("docs/configuration.md does not contain AF_MODEL_EFFORT")
	}
	if !strings.Contains(src, "--model anthropic/claude-fable-5-1 --effort xhigh") {
		t.Error("docs/configuration.md does not contain the long-context example")
	}
}

// TS-13-29 (unit): docs/model-usage.md repair paragraph states the new effort
// rule without referencing --variant
// Verifies: 13-REQ-7.3
func TestTS13_29_DocsModelUsageRepairEffort(t *testing.T) {
	root := findWorkspaceRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docs", "model-usage.md"))
	if err != nil {
		t.Fatalf("reading docs/model-usage.md: %v", err)
	}
	src := string(body)

	if strings.Contains(src, "--variant") {
		t.Error("docs/model-usage.md still references --variant")
	}
	if !strings.Contains(src, "repair-model-effort") && !strings.Contains(src, "repair model") {
		t.Error("docs/model-usage.md does not describe the repair effort precedence")
	}
}

// TS-13-30 (unit): docs/errata/agentkit_model_resolution.md §2 has a closing
// note about the extended variant removal
// Verifies: 13-REQ-7.4
func TestTS13_30_DocsErrataExtendedVariantRemoval(t *testing.T) {
	root := findWorkspaceRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docs", "errata", "agentkit_model_resolution.md"))
	if err != nil {
		t.Fatalf("reading docs/errata/agentkit_model_resolution.md: %v", err)
	}
	src := string(body)

	// The closing note must state that the extended variant no longer exists.
	// It should mention both "extended variant" and "no longer exists" together.
	if !strings.Contains(src, "extended variant no longer exists") {
		t.Error("errata §2 does not contain 'extended variant no longer exists'")
	}
}

// TS-13-31 (unit): docs/adr/08-effort-replaces-variant.md exists and records
// the decision
// Verifies: 13-REQ-7.5
func TestTS13_31_DocsADR08EffortReplacesVariant(t *testing.T) {
	root := findWorkspaceRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docs", "adr", "08-effort-replaces-variant.md"))
	if err != nil {
		t.Fatalf("reading docs/adr/08-effort-replaces-variant.md: %v", err)
	}
	src := string(body)

	if src == "" {
		t.Fatal("docs/adr/08-effort-replaces-variant.md is empty")
	}
	if !strings.Contains(src, "effort") {
		t.Error("ADR 08 does not mention effort")
	}
	if !strings.Contains(src, "variant") {
		t.Error("ADR 08 does not mention variant")
	}
}
