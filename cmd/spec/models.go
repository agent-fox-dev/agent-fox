package main

import (
	"fmt"

	"github.com/agentfox/agentkit-go/core"
	"github.com/spf13/cobra"

	"github.com/agent-fox-dev/agentfox/agentspec"
)

// modelDescriptor is the catalog's model type, named locally so the rest of
// this file reads without a package qualifier on every line.
type modelDescriptor = core.Model

// modelEntry is one row of `spec models`.
type modelEntry struct {
	Tier          string  `json:"tier,omitempty"`
	Variant       string  `json:"variant,omitempty"`
	Vendor        string  `json:"vendor"`
	Model         string  `json:"model"`
	ContextWindow int     `json:"context_window"`
	MaxTokens     int     `json:"max_tokens"`
	InputCost     float64 `json:"input_cost_per_mtok"`
	OutputCost    float64 `json:"output_cost_per_mtok"`
	Credential    string  `json:"credential"`
	Phase         string  `json:"phase,omitempty"`
}

// newModelsCmd creates the "spec models" subcommand.
//
// It exists because the model surface stopped being four hardcoded Claude ids
// and became a catalog spanning several vendors, each with its own credential
// variables. Which model a phase will actually use, what it costs and whether
// this shell can authenticate to it were three things an operator could
// previously only learn by running a spec and reading the bill.
func newModelsCmd() *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:   "models",
		Short: "Show the models each phase resolves to, and whether they can be reached",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := agentspec.LoadConfig()
			if err != nil {
				return err
			}
			entries := resolvedPhaseModels(cfg)
			if all {
				entries = append(entries, tierTableEntries()...)
			}
			return emitOKTo(cmd.OutOrStdout(), "vendor", vendorOf(cfg), "models", entries)
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "also list every tier of every vendor")
	return cmd
}

func vendorOf(cfg agentspec.AgentSpecConfig) string {
	if cfg.Vendor != "" {
		return cfg.Vendor
	}
	return agentspec.DefaultVendor
}

// resolvedPhaseModels reports what each phase would run on right now, under
// the configuration this working directory actually has.
func resolvedPhaseModels(cfg agentspec.AgentSpecConfig) []modelEntry {
	out := make([]modelEntry, 0, 3)
	for _, phase := range []string{"assess", "refine", "generate"} {
		name := cfg.ModelForPhase(phase)
		entry := modelEntry{Phase: phase, Tier: name, Variant: cfg.ModelVariant, Vendor: vendorOf(cfg)}
		m, err := agentspec.ResolveModel(name, cfg.ModelVariant, cfg.Vendor)
		if err != nil {
			entry.Model = fmt.Sprintf("unresolved: %v", err)
			entry.Credential = "unknown"
			out = append(out, entry)
			continue
		}
		out = append(out, describe(entry, m))
	}
	return out
}

// tierTableEntries lists every tier of every vendor, so an operator choosing a
// vendor can see what they would get before switching to it.
func tierTableEntries() []modelEntry {
	var out []modelEntry
	for _, vendor := range agentspec.TierVendors() {
		for _, tier := range agentspec.Tiers {
			entry := modelEntry{Tier: string(tier), Vendor: vendor}
			m, err := agentspec.ResolveModel(string(tier), "", vendor)
			if err != nil {
				entry.Model = fmt.Sprintf("unresolved: %v", err)
				entry.Credential = "unknown"
				out = append(out, entry)
				continue
			}
			out = append(out, describe(entry, m))
		}
	}
	return out
}

// describe fills a row from a resolved model.
func describe(entry modelEntry, m *modelDescriptor) modelEntry {
	entry.Vendor = m.Provider
	entry.Model = m.ID
	entry.ContextWindow = m.ContextWindow
	entry.MaxTokens = m.MaxTokens
	entry.InputCost = m.Cost.Input
	entry.OutputCost = m.Cost.Output
	// The credential state is the answer to "will this run at all", and it is
	// three-valued: a deployment behind a gateway or an instance role has no
	// key this process can read and authenticates anyway.
	if err := agentspec.CheckCredentials(m); err != nil {
		entry.Credential = "missing"
	} else {
		entry.Credential = "ok"
	}
	return entry
}
