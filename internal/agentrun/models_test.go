package agentrun

import (
	"errors"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/catalog"
	"github.com/agentfox/agentkit-go/core"
)

func TestTierNamesResolveCaseInsensitively(t *testing.T) {
	for _, name := range []string{"STANDARD", "standard", "Standard"} {
		spec, _, err := ModelSpec(name, "", "")
		if err != nil {
			t.Fatalf("ModelSpec(%q): %v", name, err)
		}
		if spec != "anthropic/claude-sonnet-5" {
			t.Errorf("ModelSpec(%q) = %q, want anthropic/claude-sonnet-5", name, spec)
		}
	}
}

func TestEveryTierOfEveryVendorResolvesInTheCatalog(t *testing.T) {
	// The tier table is aliases over the catalog, so an entry naming a row
	// that does not exist is a configuration this package would only discover
	// on somebody's first paid run.
	for _, vendor := range TierVendors() {
		for _, tier := range Tiers {
			m, _, err := ResolveModel(string(tier), "", vendor)
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

func TestTheExtendedVariantHasAMillionTokenWindow(t *testing.T) {
	// The variant exists so a spec too large for the tier's default window
	// can be generated at all. A variant that resolved to a row with the same
	// window would be a setting that reads as doing something and does not.
	def, _, err := ResolveModel("ADVANCED", "", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	ext, _, err := ResolveModel("ADVANCED", "extended", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if ext.ID == def.ID {
		t.Fatalf("the extended variant resolved to the tier default %s", def.ID)
	}
	if ext.ContextWindow < 1_000_000 {
		t.Errorf("the extended variant %s has a %d-token window", ext.ID, ext.ContextWindow)
	}
}

func TestAnUnknownVariantFallsBackToTheTierDefault(t *testing.T) {
	// A variant is a preference. Refusing to run because a vendor has no
	// long-context model in the requested tier is a worse answer than running
	// in the tier that was asked for.
	spec, _, err := ModelSpec("SIMPLE", "extended", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if spec != "anthropic/claude-sonnet-5" {
		t.Errorf("ModelSpec(SIMPLE, extended) = %q, want the tier default", spec)
	}
}

func TestAModelIDIsHandedToTheCatalogUnchanged(t *testing.T) {
	// Anything that is not a tier name is the catalog's business, so a model
	// released after this build was cut needs no code change here.
	for _, name := range []string{"anthropic/claude-opus-5", "openai/gpt-6-astra"} {
		spec, _, err := ModelSpec(name, "", "")
		if err != nil {
			t.Fatalf("ModelSpec(%q): %v", name, err)
		}
		if spec != name {
			t.Errorf("ModelSpec(%q) = %q, want it unchanged", name, spec)
		}
		if _, _, err := ResolveModel(name, "", ""); err != nil {
			t.Errorf("ResolveModel(%q): %v", name, err)
		}
	}
}

func TestAModelSpecNamingNothingIsAnErrorThatNamesTheFix(t *testing.T) {
	_, _, err := ResolveModel("not-a-model-anyone-ships", "", "")
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
	if _, _, err := ModelSpec("", "", ""); err == nil {
		t.Fatal("expected an error for an empty model name")
	}
}

func TestAnUnknownVendorIsRefusedWithTheKnownOnes(t *testing.T) {
	_, _, err := ModelSpec("STANDARD", "", "nosuchvendor")
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
	m, _, err := ResolveModel("STANDARD", "", "openai")
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
	m, _, err := ResolveModel("STANDARD", "", "")
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
		_, got, err := ResolveModel(string(tt.tier), "", "anthropic")
		if err != nil {
			t.Fatalf("%s: %v", tt.tier, err)
		}
		if got != tt.want {
			t.Errorf("%s: thinking = %q, want %q", tt.tier, got, tt.want)
		}
	}
}

func TestALiteralModelSpecReturnsThinkingUnset(t *testing.T) {
	_, thinking, err := ModelSpec("anthropic/claude-opus-5", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if thinking != core.ThinkingUnset {
		t.Errorf("a literal spec returned thinking %q, want unset", thinking)
	}
}
