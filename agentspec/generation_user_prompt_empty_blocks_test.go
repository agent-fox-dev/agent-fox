package agentspec

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// TS-NS-1 (issue #56): nil specLandscape → no landscape section, no \n\n\n
// ---------------------------------------------------------------------------

// TestNS56_GenerationUserPrompt_NilLandscape_NoLandscapeSection verifies that
// when specLandscape is nil the rendered prompt contains no landscape section
// headings and no run of three or more consecutive newlines.
// Requirement: NS-REQ-1, Test Spec: TS-NS-1
func TestNS56_GenerationUserPrompt_NilLandscape_NoLandscapeSection(t *testing.T) {
	tmpDir := t.TempDir()
	prompt, err := GenerationUserPrompt("prd", "requirements", "07", tmpDir, nil, nil, nil)
	if err != nil {
		t.Fatalf("GenerationUserPrompt() returned error: %v", err)
	}
	if strings.Contains(prompt, "\n\n\n") {
		t.Error("prompt contains three or more consecutive newlines; want no excess blank lines")
	}
	if strings.Contains(prompt, "Existing Spec Landscape") {
		t.Error("prompt contains 'Existing Spec Landscape'; want no landscape section when specLandscape is nil")
	}
	if strings.Contains(prompt, "### Active Specs") {
		t.Error("prompt contains '### Active Specs'; want no landscape section when specLandscape is nil")
	}
	if strings.Contains(prompt, "Sibling Specifications") {
		t.Error("prompt contains 'Sibling Specifications'; want no landscape section when specLandscape is nil")
	}
}

// ---------------------------------------------------------------------------
// TS-NS-2 (issue #56): nil dependentInterfaces → no interfaces section, no \n\n\n
// ---------------------------------------------------------------------------

// TestNS56_GenerationUserPrompt_NilInterfaces_NoInterfacesSection verifies
// that when dependentInterfaces is nil the rendered prompt contains no
// "Dependent interfaces" heading and no run of three or more consecutive newlines.
// Requirement: NS-REQ-2, Test Spec: TS-NS-2
func TestNS56_GenerationUserPrompt_NilInterfaces_NoInterfacesSection(t *testing.T) {
	tmpDir := t.TempDir()
	prompt, err := GenerationUserPrompt("prd", "requirements", "07", tmpDir, nil, nil, nil)
	if err != nil {
		t.Fatalf("GenerationUserPrompt() returned error: %v", err)
	}
	if strings.Contains(prompt, "Dependent interfaces") {
		t.Error("prompt contains 'Dependent interfaces'; want no interfaces section when dependentInterfaces is nil")
	}
	if strings.Contains(prompt, "\n\n\n") {
		t.Error("prompt contains three or more consecutive newlines; want no excess blank lines")
	}
}

// ---------------------------------------------------------------------------
// TS-NS-3 (issue #56): all artifact types produce clean prompts with no \n\n\n
// ---------------------------------------------------------------------------

// TestNS56_GenerationUserPrompt_AllArtifactsClean verifies that all three
// artifact types produce prompts with no run of three or more consecutive
// newlines when all optional blocks are nil.
// Requirement: NS-REQ-3, Test Spec: TS-NS-3
func TestNS56_GenerationUserPrompt_AllArtifactsClean(t *testing.T) {
	tmpDir := t.TempDir()
	for _, artifact := range []string{"requirements", "test_spec", "tasks"} {
		t.Run(artifact, func(t *testing.T) {
			prompt, err := GenerationUserPrompt("prd", artifact, "07", tmpDir, nil, nil, nil)
			if err != nil {
				t.Fatalf("GenerationUserPrompt(%q) returned error: %v", artifact, err)
			}
			if strings.Contains(prompt, "\n\n\n") {
				t.Errorf("prompt for %q contains three or more consecutive newlines; want no excess blank lines", artifact)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TS-NS-4 (issue #56): non-empty landscape and interfaces appear in prompt
// ---------------------------------------------------------------------------

// TestNS56_GenerationUserPrompt_NonEmptyLandscapeAndInterfaces verifies that
// landscape table content and the dependent interfaces block appear in the
// prompt when the corresponding data is non-empty.
// Requirement: NS-REQ-4, Test Spec: TS-NS-4
func TestNS56_GenerationUserPrompt_NonEmptyLandscapeAndInterfaces(t *testing.T) {
	tmpDir := t.TempDir()
	landscape := []map[string]any{
		{"spec_id": "01", "title": "Core Spec", "status": "active"},
	}
	interfaces := []map[string]any{
		{"spec_id": "01", "spec_name": "core"},
	}

	prompt, err := GenerationUserPrompt("prd", "requirements", "07", tmpDir, nil, interfaces, landscape)
	if err != nil {
		t.Fatalf("GenerationUserPrompt() returned error: %v", err)
	}

	// Landscape table content must be present.
	if !strings.Contains(prompt, "01") {
		t.Error("prompt does not contain spec ID '01' from landscape entry")
	}
	if !strings.Contains(prompt, "Core Spec") {
		t.Error("prompt does not contain 'Core Spec' from landscape entry")
	}

	// Dependent interfaces section must be present.
	if !strings.Contains(prompt, "Dependent interfaces") {
		t.Error("prompt does not contain 'Dependent interfaces'; want section when dependentInterfaces is non-empty")
	}
}
