package agentrun

import (
	"fmt"
	"sort"
	"strings"

	"github.com/agentfox/agentkit-go/catalog"
	"github.com/agentfox/agentkit-go/core"
)

// ModelTier classifies models by capability level. A tier is what a spec
// author actually chooses between — cheap, balanced, best — and keeping it is
// the point: the alternative is asking whoever writes a PRD to know which
// model id is current this month.
type ModelTier string

const (
	// TierSimple represents lightweight, cost-effective models.
	TierSimple ModelTier = "SIMPLE"
	// TierStandard represents balanced capability models.
	TierStandard ModelTier = "STANDARD"
	// TierAdvanced represents the most capable models.
	TierAdvanced ModelTier = "ADVANCED"
)

// Tiers is the declaration order of the tiers, cheapest first.
var Tiers = []ModelTier{TierSimple, TierStandard, TierAdvanced}

// tierEntry pairs a catalog model spec with the thinking level the tier
// prescribes. Separating model from reasoning effort lets two tiers share a
// model (SIMPLE and STANDARD both use sonnet-5) while running it at different
// depth.
type tierEntry struct {
	spec     string
	thinking core.ThinkingLevel
}

// tierTable maps a (vendor, tier, variant) to a catalog model spec and
// thinking level.
//
// It is an ALIAS TABLE over the catalog, not a registry. The previous
// implementation held its own map of four model ids with their own notion of
// what each was, which meant every fact the SDK needs about a model — context
// window, output cap, per-token cost, which thinking levels the wire accepts —
// was either absent or a second copy that could disagree with the first. Here
// a tier resolves to a spec string and catalog.ResolveModel supplies the rest.
//
// The empty variant is the tier default. A variant that does not exist for a
// tier falls back to that default rather than failing: a variant is a
// preference ("give me the long-context one"), and refusing to run because a
// vendor has no long-context model in that tier would be a worse answer than
// running in the tier that was asked for.
var tierTable = map[string]map[ModelTier]map[string]tierEntry{
	"anthropic": {
		TierSimple:   {"": {spec: "anthropic/claude-sonnet-5", thinking: core.ThinkingMedium}},
		TierStandard: {"": {spec: "anthropic/claude-sonnet-5", thinking: core.ThinkingHigh}},
		TierAdvanced: {"": {spec: "anthropic/claude-opus-5", thinking: core.ThinkingXHigh}, "extended": {spec: "anthropic/claude-fable-5-1", thinking: core.ThinkingXHigh}},
	},
	"openai": {
		TierSimple:   {"": {spec: "openai/gpt-5.6-luna"}},
		TierStandard: {"": {spec: "openai/gpt-5.6-terra"}},
		TierAdvanced: {"": {spec: "openai/gpt-6-astra"}},
	},
	"google": {
		TierSimple:   {"": {spec: "google/gemini-3.5-flash-lite"}},
		TierStandard: {"": {spec: "google/gemini-3.8-flash"}},
		TierAdvanced: {"": {spec: "google/gemini-3.1-pro-preview"}},
	},
}

// DefaultVendor is the vendor whose tier table is used when the config names
// none. It is Anthropic because that is what every prompt template in this
// package was written and tuned against.
const DefaultVendor = "anthropic"

// TierVendors lists the vendors that have a tier table, sorted.
func TierVendors() []string {
	out := make([]string, 0, len(tierTable))
	for v := range tierTable {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// ModelSpec turns a tier name or a model spec into a catalog spec string and
// the thinking level the tier prescribes.
//
// A tier name (case-insensitive) is looked up in the vendor's tier table.
// Anything else is returned unchanged and handed to the catalog, so
// "anthropic/claude-opus-5", a bare unambiguous id, and a model released
// after this build was cut all work without a code change. A literal model
// spec returns ThinkingUnset because the operator chose the model, not a tier.
//
// Whether name is a tier is decided before vendor is looked at: a
// vendor absent from the tier table (ollama, an OpenAI-compatible gateway,
// ...) must not block a model named by id, so vendor is only defaulted and
// validated once name has already been recognized as a tier name.
func ModelSpec(name, variant, vendor string) (string, core.ThinkingLevel, error) {
	if name == "" {
		return "", core.ThinkingUnset, fmt.Errorf("agentrun: model name must not be empty")
	}

	tier := ModelTier(strings.ToUpper(name))
	if !isTierName(tier) {
		// Not a tier: the catalog is the authority on whether it is a model.
		// The vendor plays no part in resolving a model named by id.
		return name, core.ThinkingUnset, nil
	}

	if vendor == "" {
		vendor = DefaultVendor
	}
	byTier, known := tierTable[vendor]
	if !known {
		return "", core.ThinkingUnset, fmt.Errorf("agentrun: unknown model vendor %q; known vendors: %s",
			vendor, strings.Join(TierVendors(), ", "))
	}
	byVariant := byTier[tier]
	if variant != "" {
		if entry, ok := byVariant[variant]; ok {
			return entry.spec, entry.thinking, nil
		}
	}
	entry := byVariant[""]
	return entry.spec, entry.thinking, nil
}

// isTierName reports whether tier is one of the declared tiers, independent
// of any vendor. Membership in Tiers is what makes a name a tier at all; the
// vendor only selects which table backs the tier once it is recognized.
func isTierName(tier ModelTier) bool {
	for _, t := range Tiers {
		if t == tier {
			return true
		}
	}
	return false
}

// ResolveModel resolves a tier name or model spec to a catalog model and the
// thinking level the tier prescribes.
//
// The returned *core.Model carries the wire API, base URL, context window,
// output cap, price and thinking-level map. Everything downstream — the budget
// stop policy, the max_tokens clamp, the cost report — reads them from here
// rather than from a constant in this package.
func ResolveModel(name, variant, vendor string) (*core.Model, core.ThinkingLevel, error) {
	spec, thinking, err := ModelSpec(name, variant, vendor)
	if err != nil {
		return nil, core.ThinkingUnset, err
	}
	m, err := catalog.ResolveModel(spec)
	if err != nil {
		return nil, core.ThinkingUnset, fmt.Errorf("agentrun: resolving model %q (from %q): %w", spec, name, err)
	}
	return m, thinking, nil
}
