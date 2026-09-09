package agentspec

import (
	"fmt"
	"strings"
)

// ModelTier classifies LLM models by capability level.
type ModelTier string

const (
	// TierSimple represents lightweight, cost-effective models.
	TierSimple ModelTier = "SIMPLE"
	// TierStandard represents balanced capability models.
	TierStandard ModelTier = "STANDARD"
	// TierAdvanced represents the most capable models.
	TierAdvanced ModelTier = "ADVANCED"
)

// ModelEntry holds metadata for a registered model.
type ModelEntry struct {
	ModelID string
	Tier    ModelTier
	Variant string
}

// ModelRegistry maps model ID strings to their ModelEntry metadata.
// Populated at init time with known Anthropic models.
var ModelRegistry = map[string]ModelEntry{
	"claude-haiku-4-5": {
		ModelID: "claude-haiku-4-5",
		Tier:    TierSimple,
		Variant: "simple",
	},
	"claude-sonnet-4-6": {
		ModelID: "claude-sonnet-4-6",
		Tier:    TierStandard,
		Variant: "standard",
	},
	"claude-opus-4-6": {
		ModelID: "claude-opus-4-6",
		Tier:    TierAdvanced,
		Variant: "advanced",
	},
	"claude-opus-4-6[1m]": {
		ModelID: "claude-opus-4-6[1m]",
		Tier:    TierAdvanced,
		Variant: "extended",
	},
}

// TierDefaults maps each ModelTier to its default model ID string.
var TierDefaults = map[ModelTier]string{
	TierSimple:   "claude-haiku-4-5",
	TierStandard: "claude-sonnet-4-6",
	TierAdvanced: "claude-opus-4-6",
}

// ResolveModel resolves a model name or tier constant string to a canonical
// Anthropic model ID. It performs case-insensitive matching for tier constants
// and direct lookup for model ID keys in ModelRegistry.
//
// An optional variant string may be provided. When a tier matches and a
// variant is given, the registry is scanned for an entry whose (tier, variant)
// pair matches before falling back to the tier default.
//
// Returns the resolved model ID and nil on success, or an empty string and
// a descriptive error if the name is not recognized.
func ResolveModel(name string, variant ...string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("ResolveModel: model name must not be empty")
	}

	// Case-insensitive tier constant matching.
	upper := strings.ToUpper(name)
	tier := ModelTier(upper)
	if _, ok := TierDefaults[tier]; ok {
		if len(variant) > 0 && variant[0] != "" {
			for _, entry := range ModelRegistry {
				if entry.Tier == tier && entry.Variant == variant[0] {
					return entry.ModelID, nil
				}
			}
		}
		return TierDefaults[tier], nil
	}

	// Direct lookup in model registry by model ID.
	if entry, ok := ModelRegistry[name]; ok {
		return entry.ModelID, nil
	}

	return "", fmt.Errorf("ResolveModel: unrecognized model name %q", name)
}
