package agentspec

import "testing"

// ---------------------------------------------------------------------------
// TS-07-1: ModelTier constants, ModelEntry, ModelRegistry, TierDefaults
// ---------------------------------------------------------------------------

// TestSpec07_ModelRegistry_TierConstants verifies that ModelTier constants
// are defined with the correct string values.
// Test Spec: TS-07-1, Requirement: 07-REQ-1.1
func TestSpec07_ModelRegistry_TierConstants(t *testing.T) {
	tests := []struct {
		name string
		got  ModelTier
		want string
	}{
		{"TierSimple", TierSimple, "SIMPLE"},
		{"TierStandard", TierStandard, "STANDARD"},
		{"TierAdvanced", TierAdvanced, "ADVANCED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.got) != tt.want {
				t.Errorf("%s = %q; want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}

// TestSpec07_ModelRegistry_ModelEntryStruct verifies that the ModelEntry
// struct has ModelID, Tier, and Variant fields with expected types.
// Test Spec: TS-07-1, Requirement: 07-REQ-1.1
func TestSpec07_ModelRegistry_ModelEntryStruct(t *testing.T) {
	entry := ModelEntry{
		ModelID: "claude-sonnet-4-6",
		Tier:    TierStandard,
		Variant: "standard",
	}

	if entry.ModelID != "claude-sonnet-4-6" {
		t.Errorf("ModelID = %q; want %q", entry.ModelID, "claude-sonnet-4-6")
	}
	if entry.Tier != TierStandard {
		t.Errorf("Tier = %q; want %q", entry.Tier, TierStandard)
	}
	if entry.Variant != "standard" {
		t.Errorf("Variant = %q; want %q", entry.Variant, "standard")
	}
}

// TestSpec07_ModelRegistry_Entries verifies that ModelRegistry contains
// the expected model entries with correct tier assignments.
// Test Spec: TS-07-1, Requirement: 07-REQ-1.1
func TestSpec07_ModelRegistry_Entries(t *testing.T) {
	expected := []struct {
		modelID string
		tier    ModelTier
	}{
		{"claude-haiku-4-5", TierSimple},
		{"claude-sonnet-4-6", TierStandard},
		{"claude-opus-4-6", TierAdvanced},
	}
	for _, e := range expected {
		t.Run(e.modelID, func(t *testing.T) {
			entry, ok := ModelRegistry[e.modelID]
			if !ok {
				t.Fatalf("ModelRegistry[%q] not found; want entry with tier %q", e.modelID, e.tier)
			}
			if entry.Tier != e.tier {
				t.Errorf("ModelRegistry[%q].Tier = %q; want %q", e.modelID, entry.Tier, e.tier)
			}
			if entry.ModelID != e.modelID {
				t.Errorf("ModelRegistry[%q].ModelID = %q; want %q", e.modelID, entry.ModelID, e.modelID)
			}
		})
	}
}

// TestSpec07_ModelRegistry_TierDefaults verifies that TierDefaults maps
// each ModelTier to the correct default model ID.
// Test Spec: TS-07-1, Requirement: 07-REQ-1.1
func TestSpec07_ModelRegistry_TierDefaults(t *testing.T) {
	expected := map[ModelTier]string{
		TierSimple:   "claude-haiku-4-5",
		TierStandard: "claude-sonnet-4-6",
		TierAdvanced: "claude-opus-4-6",
	}
	for tier, wantModel := range expected {
		t.Run(string(tier), func(t *testing.T) {
			gotModel, ok := TierDefaults[tier]
			if !ok {
				t.Fatalf("TierDefaults[%q] not found; want %q", tier, wantModel)
			}
			if gotModel != wantModel {
				t.Errorf("TierDefaults[%q] = %q; want %q", tier, gotModel, wantModel)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TS-07-2: ResolveModel with tier constant
// ---------------------------------------------------------------------------

// TestSpec07_ResolveModel_TierConstant verifies that ResolveModel returns
// the correct default model ID when called with a tier constant string.
// Test Spec: TS-07-2, Requirement: 07-REQ-1.2
func TestSpec07_ResolveModel_TierConstant(t *testing.T) {
	tests := []struct {
		input  string
		wantID string
	}{
		{"SIMPLE", "claude-haiku-4-5"},
		{"STANDARD", "claude-sonnet-4-6"},
		{"ADVANCED", "claude-opus-4-6"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			gotID, err := ResolveModel(tt.input)
			if err != nil {
				t.Fatalf("ResolveModel(%q) returned error: %v", tt.input, err)
			}
			if gotID != tt.wantID {
				t.Errorf("ResolveModel(%q) = %q; want %q", tt.input, gotID, tt.wantID)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TS-07-3: ResolveModel with model ID key
// ---------------------------------------------------------------------------

// TestSpec07_ResolveModel_ModelIDKey verifies that ResolveModel returns the
// model ID directly when called with a known model ID from ModelRegistry.
// Test Spec: TS-07-3, Requirement: 07-REQ-1.3
func TestSpec07_ResolveModel_ModelIDKey(t *testing.T) {
	knownModels := []string{
		"claude-haiku-4-5",
		"claude-sonnet-4-6",
		"claude-opus-4-6",
	}
	for _, modelID := range knownModels {
		t.Run(modelID, func(t *testing.T) {
			gotID, err := ResolveModel(modelID)
			if err != nil {
				t.Fatalf("ResolveModel(%q) returned error: %v", modelID, err)
			}
			if gotID != modelID {
				t.Errorf("ResolveModel(%q) = %q; want %q", modelID, gotID, modelID)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TS-07-4: ResolveModel with unrecognized name
// ---------------------------------------------------------------------------

// TestSpec07_ResolveModel_UnrecognizedName verifies that ResolveModel returns
// an empty string and descriptive error for an unrecognized model name.
// Test Spec: TS-07-4, Requirement: 07-REQ-1.4
func TestSpec07_ResolveModel_UnrecognizedName(t *testing.T) {
	unknownNames := []string{
		"gpt-4-turbo",
		"unknown-model",
		"claude-99",
		"llama-3",
	}
	for _, name := range unknownNames {
		t.Run(name, func(t *testing.T) {
			gotID, err := ResolveModel(name)
			if err == nil {
				t.Fatalf("ResolveModel(%q) returned nil error; want descriptive error", name)
			}
			if gotID != "" {
				t.Errorf("ResolveModel(%q) = %q; want empty string on error", name, gotID)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Edge Cases
// ---------------------------------------------------------------------------

// TestSpec07_ResolveModel_EmptyString verifies that ResolveModel returns
// an error when called with an empty string.
// Edge Case: 07-REQ-1.E1
func TestSpec07_ResolveModel_EmptyString(t *testing.T) {
	gotID, err := ResolveModel("")
	if err == nil {
		t.Fatal("ResolveModel(\"\") returned nil error; want error indicating empty name")
	}
	if gotID != "" {
		t.Errorf("ResolveModel(\"\") = %q; want empty string on error", gotID)
	}
}

// TestSpec07_ResolveModel_CaseInsensitive verifies that ResolveModel
// performs case-insensitive comparison for tier constant strings.
// Edge Case: 07-REQ-1.E2
func TestSpec07_ResolveModel_CaseInsensitive(t *testing.T) {
	tests := []struct {
		input  string
		wantID string
	}{
		{"standard", "claude-sonnet-4-6"},
		{"Standard", "claude-sonnet-4-6"},
		{"STANDARD", "claude-sonnet-4-6"},
		{"simple", "claude-haiku-4-5"},
		{"Simple", "claude-haiku-4-5"},
		{"advanced", "claude-opus-4-6"},
		{"Advanced", "claude-opus-4-6"},
		{"aDvAnCeD", "claude-opus-4-6"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			gotID, err := ResolveModel(tt.input)
			if err != nil {
				t.Fatalf("ResolveModel(%q) returned error: %v", tt.input, err)
			}
			if gotID != tt.wantID {
				t.Errorf("ResolveModel(%q) = %q; want %q", tt.input, gotID, tt.wantID)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Extended model and variant-aware resolution (Issue #95)
// ---------------------------------------------------------------------------

// TestSpec07_ModelRegistry_ExtendedEntry verifies that claude-opus-4-6[1m]
// is registered with the correct tier and variant.
func TestSpec07_ModelRegistry_ExtendedEntry(t *testing.T) {
	entry, ok := ModelRegistry["claude-opus-4-6[1m]"]
	if !ok {
		t.Fatal("ModelRegistry missing claude-opus-4-6[1m]")
	}
	if entry.Tier != TierAdvanced {
		t.Errorf("Tier = %q; want %q", entry.Tier, TierAdvanced)
	}
	if entry.Variant != "extended" {
		t.Errorf("Variant = %q; want %q", entry.Variant, "extended")
	}
}

// TestSpec07_ResolveModel_DirectExtended verifies direct lookup of the
// extended model ID.
func TestSpec07_ResolveModel_DirectExtended(t *testing.T) {
	gotID, err := ResolveModel("claude-opus-4-6[1m]")
	if err != nil {
		t.Fatalf("ResolveModel(%q) returned error: %v", "claude-opus-4-6[1m]", err)
	}
	if gotID != "claude-opus-4-6[1m]" {
		t.Errorf("ResolveModel(%q) = %q; want %q", "claude-opus-4-6[1m]", gotID, "claude-opus-4-6[1m]")
	}
}

// TestSpec07_ResolveModel_VariantExtended verifies variant-aware tier
// resolution returns the extended model.
func TestSpec07_ResolveModel_VariantExtended(t *testing.T) {
	gotID, err := ResolveModel("ADVANCED", "extended")
	if err != nil {
		t.Fatalf("ResolveModel(%q, %q) returned error: %v", "ADVANCED", "extended", err)
	}
	if gotID != "claude-opus-4-6[1m]" {
		t.Errorf("got %q; want %q", gotID, "claude-opus-4-6[1m]")
	}
}

// TestSpec07_ResolveModel_VariantCaseInsensitiveTier verifies case-insensitive
// tier matching with a variant.
func TestSpec07_ResolveModel_VariantCaseInsensitiveTier(t *testing.T) {
	gotID, err := ResolveModel("advanced", "extended")
	if err != nil {
		t.Fatalf("ResolveModel(%q, %q) returned error: %v", "advanced", "extended", err)
	}
	if gotID != "claude-opus-4-6[1m]" {
		t.Errorf("got %q; want %q", gotID, "claude-opus-4-6[1m]")
	}
}

// TestSpec07_ResolveModel_NoVariantUnchanged verifies that no-variant
// resolution still returns the tier default.
func TestSpec07_ResolveModel_NoVariantUnchanged(t *testing.T) {
	gotID, err := ResolveModel("ADVANCED")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if gotID != "claude-opus-4-6" {
		t.Errorf("got %q; want %q", gotID, "claude-opus-4-6")
	}
}

// TestSpec07_ResolveModel_UnknownVariantFallback verifies that an unknown
// variant falls back to the tier default.
func TestSpec07_ResolveModel_UnknownVariantFallback(t *testing.T) {
	gotID, err := ResolveModel("ADVANCED", "nonexistent")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if gotID != "claude-opus-4-6" {
		t.Errorf("got %q; want %q", gotID, "claude-opus-4-6")
	}
}
