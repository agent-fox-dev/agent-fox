package agentspec

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

// tierTable maps a (vendor, tier, variant) to a catalog model spec.
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
var tierTable = map[string]map[ModelTier]map[string]string{
	"anthropic": {
		TierSimple:   {"": "anthropic/claude-haiku-4-5"},
		TierStandard: {"": "anthropic/claude-sonnet-4-6"},
		// The extended variant used to name claude-opus-4-6[1m]. That is not
		// a catalog row: an unknown id under a known vendor clones the
		// vendor's DEFAULT row (REQ-CAT-03), so the run would have carried
		// sonnet-5's price against opus requests and the error would have
		// shown up in a bill rather than in a log. It now names a row that
		// exists and really has the window. See
		// docs/errata/agentkit_model_resolution.md.
		TierAdvanced: {"": "anthropic/claude-opus-4-6", "extended": "anthropic/claude-fable-5-1"},
	},
	"openai": {
		TierSimple:   {"": "openai/gpt-5.6-luna"},
		TierStandard: {"": "openai/gpt-5.6-terra"},
		TierAdvanced: {"": "openai/gpt-6-astra"},
	},
	"google": {
		TierSimple:   {"": "google/gemini-3.5-flash-lite"},
		TierStandard: {"": "google/gemini-3.8-flash"},
		TierAdvanced: {"": "google/gemini-3.1-pro-preview"},
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

// ModelSpec turns a tier name or a model spec into a catalog spec string.
//
// A tier name (case-insensitive) is looked up in the vendor's tier table.
// Anything else is returned unchanged and handed to the catalog, so
// "anthropic/claude-opus-4-6", a bare unambiguous id, and a model released
// after this build was cut all work without a code change.
func ModelSpec(name, variant, vendor string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("agentspec: model name must not be empty")
	}
	if vendor == "" {
		vendor = DefaultVendor
	}

	tier := ModelTier(strings.ToUpper(name))
	byTier, known := tierTable[vendor]
	if !known {
		return "", fmt.Errorf("agentspec: unknown model vendor %q; known vendors: %s",
			vendor, strings.Join(TierVendors(), ", "))
	}
	if byVariant, ok := byTier[tier]; ok {
		if variant != "" {
			if spec, ok := byVariant[variant]; ok {
				return spec, nil
			}
		}
		return byVariant[""], nil
	}

	// Not a tier: the catalog is the authority on whether it is a model.
	return name, nil
}

// ResolveModel resolves a tier name or model spec to a catalog model.
//
// The returned *core.Model carries the wire API, base URL, context window,
// output cap, price and thinking-level map. Everything downstream — the budget
// stop policy, the max_tokens clamp, the cost report — reads them from here
// rather than from a constant in this package.
func ResolveModel(name, variant, vendor string) (*core.Model, error) {
	spec, err := ModelSpec(name, variant, vendor)
	if err != nil {
		return nil, err
	}
	m, err := catalog.ResolveModel(spec)
	if err != nil {
		return nil, fmt.Errorf("agentspec: resolving model %q (from %q): %w", spec, name, err)
	}
	return m, nil
}
