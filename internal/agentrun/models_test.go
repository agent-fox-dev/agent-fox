package agentrun

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/catalog"
	"github.com/agentfox/agentkit-go/core"
)

func TestTierNamesResolveCaseInsensitively(t *testing.T) {
	for _, name := range []string{"STANDARD", "standard", "Standard"} {
		spec, _, err := ModelSpec(name, "")
		if err != nil {
			t.Fatalf("ModelSpec(%q): %v", name, err)
		}
		if spec != "anthropic/claude-sonnet-5-5" {
			t.Errorf("ModelSpec(%q) = %q, want anthropic/claude-sonnet-5-5", name, spec)
		}
	}
}

func TestEveryTierOfEveryVendorResolvesInTheCatalog(t *testing.T) {
	// The tier table is aliases over the catalog, so an entry naming a row
	// that does not exist is a configuration this package would only discover
	// on somebody's first paid run.
	for _, vendor := range TierVendors() {
		for _, tier := range Tiers {
			m, _, err := ResolveModel(string(tier), vendor)
			if err != nil {
				t.Errorf("%s/%s: %v", vendor, tier, err)
				continue
			}
			if m.Cloned {
				t.Errorf("%s/%s resolved to %s, which is a CLONE of %s: the catalog has no row "+
					"for it, so its cost and context window are another model's",
					vendor, tier, m.ID, m.ClonedFrom)
			}
			if m.Provider != vendor {
				t.Errorf("%s/%s resolved to a %s model (%s)", vendor, tier, m.Provider, m.ID)
			}
		}
	}
}

func TestAModelIDIsHandedToTheCatalogUnchanged(t *testing.T) {
	// Anything that is not a tier name is the catalog's business, so a model
	// released after this build was cut needs no code change here.
	for _, name := range []string{"anthropic/claude-opus-5-5", "openai/gpt-6-astra"} {
		spec, _, err := ModelSpec(name, "")
		if err != nil {
			t.Fatalf("ModelSpec(%q): %v", name, err)
		}
		if spec != name {
			t.Errorf("ModelSpec(%q) = %q, want it unchanged", name, spec)
		}
		if _, _, err := ResolveModel(name, ""); err != nil {
			t.Errorf("ResolveModel(%q): %v", name, err)
		}
	}
}

func TestAModelSpecNamingNothingIsAnErrorThatNamesTheFix(t *testing.T) {
	_, _, err := ResolveModel("not-a-model-anyone-ships", "")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, catalog.ErrUnresolvedModel) {
		t.Errorf("error does not unwrap to ErrUnresolvedModel: %v", err)
	}
	// The catalog's message names the vendors, which is the fix. Losing it in
	// a wrap would leave the operator with "unknown model" and nothing else.
	if !strings.Contains(err.Error(), "anthropic") {
		t.Errorf("the error does not name a vendor to try: %v", err)
	}
}

func TestAnEmptyModelNameIsRefused(t *testing.T) {
	if _, _, err := ModelSpec("", ""); err == nil {
		t.Fatal("expected an error for an empty model name")
	}
}

func TestAModelIDIsNotBlockedByAnUnknownVendor(t *testing.T) {
	// The vendor only ever selects a tier table. A model named by id must
	// resolve through the catalog regardless of vendor, including a vendor
	// (ollama, an OpenAI-compatible gateway, ...) that has no tier table at
	// all: naming it must not turn into "unknown model vendor".
	for _, vendor := range []string{"ollama", "openrouter"} {
		spec, _, err := ModelSpec("anthropic/claude-opus-5-5", vendor)
		if err != nil {
			t.Fatalf("ModelSpec(id, vendor=%s): %v", vendor, err)
		}
		if spec != "anthropic/claude-opus-5-5" {
			t.Errorf("ModelSpec(id, vendor=%s) = %q, want it unchanged", vendor, spec)
		}
		if _, _, err := ResolveModel("anthropic/claude-opus-5-5", vendor); err != nil {
			t.Errorf("ResolveModel(id, vendor=%s): %v", vendor, err)
		}
	}
}

func TestAnUnknownVendorIsRefusedWithTheKnownOnes(t *testing.T) {
	_, _, err := ModelSpec("STANDARD", "nosuchvendor")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range TierVendors() {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not list vendor %q: %v", want, err)
		}
	}
}

func TestTheVendorSelectsWhichTierTableIsUsed(t *testing.T) {
	m, _, err := ResolveModel("STANDARD", "openai")
	if err != nil {
		t.Fatal(err)
	}
	if m.Provider != "openai" {
		t.Errorf("STANDARD under openai resolved to a %s model (%s)", m.Provider, m.ID)
	}
}

func TestTheResolvedModelCarriesTheFactsTheRunNeeds(t *testing.T) {
	// The point of resolving through the catalog rather than a local map is
	// that everything downstream — the max_tokens clamp, the budget policy,
	// the cost report — reads these from one place.
	m, _, err := ResolveModel("STANDARD", "")
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case m.API == "":
		t.Error("the resolved model names no wire API")
	case m.ContextWindow == 0:
		t.Error("the resolved model has no context window")
	case m.MaxTokens == 0:
		t.Error("the resolved model has no output cap")
	case m.Cost.Input == 0:
		t.Error("the resolved model has no input price, so the budget policy cannot bound a run")
	}
}

func TestAnthropicTiersCarryThePrescribedThinkingLevel(t *testing.T) {
	tests := []struct {
		tier ModelTier
		want core.ThinkingLevel
	}{
		{TierSimple, core.ThinkingMedium},
		{TierStandard, core.ThinkingHigh},
		{TierAdvanced, core.ThinkingXHigh},
	}
	for _, tt := range tests {
		_, got, err := ResolveModel(string(tt.tier), "anthropic")
		if err != nil {
			t.Fatalf("%s: %v", tt.tier, err)
		}
		if got != tt.want {
			t.Errorf("%s: thinking = %q, want %q", tt.tier, got, tt.want)
		}
	}
}

func TestALiteralModelSpecReturnsThinkingUnset(t *testing.T) {
	_, thinking, err := ModelSpec("anthropic/claude-opus-5-5", "")
	if err != nil {
		t.Fatal(err)
	}
	if thinking != core.ThinkingUnset {
		t.Errorf("a literal spec returned thinking %q, want unset", thinking)
	}
}

// TS-13-18 (unit): tierTable has type map[string]map[ModelTier]tierEntry and no extended entry
func TestTS13_18_TierTableHasNoVariantDimensionAndNoExtendedEntry(t *testing.T) {
	// Compile-time: tierTable is declared as map[string]map[ModelTier]tierEntry.
	// The type system enforces no variant dimension — this function would not
	// compile if the table still had a third map level.
	var _ map[string]map[ModelTier]tierEntry = tierTable

	for vendor, byTier := range tierTable {
		for tier, entry := range byTier {
			if entry.spec == "" {
				t.Errorf("%s/%s: spec is empty", vendor, tier)
			}
		}
	}

	// The extended model spec should not appear anywhere in the table.
	if tierTable["anthropic"][TierAdvanced].spec == "anthropic/claude-fable-5-1" {
		t.Error("the extended entry (anthropic/claude-fable-5-1) still exists in the tier table")
	}
}

// TS-13-19 (unit): ModelSpec and ResolveModel accept exactly two parameters (name, vendor) — no variant
func TestTS13_19_ModelSpecAndResolveModelAcceptTwoParameters(t *testing.T) {
	// This is a compile-time check: the test file calls ModelSpec and
	// ResolveModel with two arguments and the build succeeds. A call with
	// three args (the old signature) would fail to compile.
	spec, thinking, err := ModelSpec("STANDARD", "")
	if err != nil {
		t.Fatalf("ModelSpec(STANDARD, \"\"): %v", err)
	}
	if spec == "" {
		t.Error("ModelSpec returned an empty spec")
	}
	if thinking == core.ThinkingUnset {
		t.Error("ModelSpec for a tier returned ThinkingUnset")
	}

	m, thinking2, err := ResolveModel("STANDARD", "")
	if err != nil {
		t.Fatalf("ResolveModel(STANDARD, \"\"): %v", err)
	}
	if m == nil {
		t.Error("ResolveModel returned a nil model")
	}
	if thinking2 == core.ThinkingUnset {
		t.Error("ResolveModel for a tier returned ThinkingUnset")
	}
}

// TS-13-24 (property): For any vendor and tier whose tierEntry has a non-unset
// thinking level, ClampThinkingLevel returns the same level (clamps to itself).
func TestTS13_24_TierEffortSelfConsistency(t *testing.T) {
	for _, vendor := range TierVendors() {
		for _, tier := range Tiers {
			m, thinking, err := ResolveModel(string(tier), vendor)
			if err != nil {
				t.Errorf("%s/%s: %v", vendor, tier, err)
				continue
			}
			if thinking == core.ThinkingUnset {
				continue
			}
			clamped, _, ok := catalog.ClampThinkingLevel(m, thinking)
			if !ok {
				t.Errorf("%s/%s: ClampThinkingLevel(%s, %s) returned ok=false",
					vendor, tier, m.ID, thinking)
				continue
			}
			if clamped != thinking {
				t.Errorf("%s/%s: ClampThinkingLevel(%s, %s) = %s, want %s (tier's own effort should clamp to itself)",
					vendor, tier, m.ID, thinking, clamped, thinking)
			}
		}
	}
}

// TS-13-25 (unit): TestTheExtendedVariantHasAMillionTokenWindow and
// TestAnUnknownVariantFallsBackToTheTierDefault are deleted
func TestTS13_25_DeletedVariantTestsNoLongerExist(t *testing.T) {
	src, err := os.ReadFile("models_test.go")
	if err != nil {
		t.Fatalf("reading models_test.go: %v", err)
	}
	lines := strings.Split(string(src), "\n")
	// Look for function declarations (lines starting with "func Test")
	// that contain the deleted test names. String literals inside this
	// test do not start with "func Test", so they cannot match.
	deleted := []string{
		"MillionTokenWindow",
		"UnknownVariantFallsBack",
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "func Test") {
			continue
		}
		for _, fragment := range deleted {
			if strings.Contains(line, fragment) {
				t.Errorf("deleted test still exists: %s", strings.TrimSpace(line))
			}
		}
	}
}
