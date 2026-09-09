package afspec

import (
	"fmt"
	"strings"
)

// renderConfig holds resolved options for rendering methods.
// It is intentionally unexported — external callers configure rendering
// via the RenderOption functional-option pattern.
type renderConfig struct {
	maxTokens int
}

// RenderOption is a functional option for rendering methods.
type RenderOption func(*renderConfig)

// WithMaxTokens returns a RenderOption that sets the token budget cap.
// When N is 0 or negative, it is treated as no budget constraint.
func WithMaxTokens(n int) RenderOption {
	return func(cfg *renderConfig) {
		cfg.maxTokens = n
	}
}

// resolveOpts applies all RenderOption functions and returns the
// resolved configuration.
func resolveOpts(opts []RenderOption) renderConfig {
	var cfg renderConfig
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// budgetActive returns true when maxTokens represents a real budget cap.
func budgetActive(cfg renderConfig) bool {
	return cfg.maxTokens > 0
}

// sumMapTokens computes the total estimated tokens across all map values.
func sumMapTokens(m map[string]string) int {
	total := 0
	for _, v := range m {
		total += EstimateTokens(v)
	}
	return total
}

// ---------------------------------------------------------------------------
// Slim test spec renderers (Level 2 truncation)
// ---------------------------------------------------------------------------

// renderTestSpecSlim renders a test spec in slim mode, omitting verbose
// fields like assertion_pseudocode, input, expected, expected_effects,
// for_any_strategy, and invariant_check. Only id, description, type/kind,
// and requirement linkage fields are preserved.
func renderTestSpecSlim(ts *TestSpecV1Json) string {
	var sb strings.Builder

	sb.WriteString("## Test Cases\n\n")
	for _, tc := range ts.TestCases {
		sb.WriteString(fmt.Sprintf("### %s: %s\n\n", tc.Id, tc.Description))
		sb.WriteString(fmt.Sprintf("**Requirement:** %s\n", tc.RequirementId))
		sb.WriteString(fmt.Sprintf("**Type:** %s\n\n", tc.Kind))
	}

	if len(ts.PropertyTests) > 0 {
		sb.WriteString("## Property Tests\n\n")
		for _, pt := range ts.PropertyTests {
			sb.WriteString(fmt.Sprintf("### %s: %s\n\n", pt.Id, pt.Description))
			sb.WriteString(fmt.Sprintf("**Property:** %s\n\n", pt.PropertyId))
			if len(pt.Validates) > 0 {
				sb.WriteString(fmt.Sprintf("**Validates:** %s\n\n", strings.Join(pt.Validates, ", ")))
			}
		}
	}

	if len(ts.EdgeCaseTests) > 0 {
		sb.WriteString("## Edge Case Tests\n\n")
		for _, ect := range ts.EdgeCaseTests {
			sb.WriteString(fmt.Sprintf("### %s: %s\n\n", ect.Id, ect.Description))
			sb.WriteString(fmt.Sprintf("**Requirement:** %s\n", ect.RequirementId))
			sb.WriteString(fmt.Sprintf("**Type:** %s\n\n", ect.Kind))
		}
	}

	if len(ts.SmokeTests) > 0 {
		sb.WriteString("## Smoke Tests\n\n")
		for _, st := range ts.SmokeTests {
			sb.WriteString(fmt.Sprintf("### %s: %s\n\n", st.Id, st.Description))
			sb.WriteString(fmt.Sprintf("**Execution Path:** %s\n\n", st.ExecutionPathId))
		}
	}

	return sb.String()
}

// renderTestSpecScopedSlim renders a test spec in slim mode, filtered
// to only the test entries whose IDs are in the ids set.
func renderTestSpecScopedSlim(ts *TestSpecV1Json, ids map[string]bool) string {
	var sb strings.Builder

	sb.WriteString("## Test Cases\n\n")
	for _, tc := range ts.TestCases {
		if !ids[tc.Id] {
			continue
		}
		sb.WriteString(fmt.Sprintf("### %s: %s\n\n", tc.Id, tc.Description))
		sb.WriteString(fmt.Sprintf("**Requirement:** %s\n", tc.RequirementId))
		sb.WriteString(fmt.Sprintf("**Type:** %s\n\n", tc.Kind))
	}

	if len(ts.PropertyTests) > 0 {
		hasScoped := false
		for _, pt := range ts.PropertyTests {
			if ids[pt.Id] {
				hasScoped = true
				break
			}
		}
		if hasScoped {
			sb.WriteString("## Property Tests\n\n")
			for _, pt := range ts.PropertyTests {
				if !ids[pt.Id] {
					continue
				}
				sb.WriteString(fmt.Sprintf("### %s: %s\n\n", pt.Id, pt.Description))
				sb.WriteString(fmt.Sprintf("**Property:** %s\n\n", pt.PropertyId))
				if len(pt.Validates) > 0 {
					sb.WriteString(fmt.Sprintf("**Validates:** %s\n\n", strings.Join(pt.Validates, ", ")))
				}
			}
		}
	}

	if len(ts.EdgeCaseTests) > 0 {
		hasScoped := false
		for _, ect := range ts.EdgeCaseTests {
			if ids[ect.Id] {
				hasScoped = true
				break
			}
		}
		if hasScoped {
			sb.WriteString("## Edge Case Tests\n\n")
			for _, ect := range ts.EdgeCaseTests {
				if !ids[ect.Id] {
					continue
				}
				sb.WriteString(fmt.Sprintf("### %s: %s\n\n", ect.Id, ect.Description))
				sb.WriteString(fmt.Sprintf("**Requirement:** %s\n", ect.RequirementId))
				sb.WriteString(fmt.Sprintf("**Type:** %s\n\n", ect.Kind))
			}
		}
	}

	if len(ts.SmokeTests) > 0 {
		hasScoped := false
		for _, st := range ts.SmokeTests {
			if ids[st.Id] {
				hasScoped = true
				break
			}
		}
		if hasScoped {
			sb.WriteString("## Smoke Tests\n\n")
			for _, st := range ts.SmokeTests {
				if !ids[st.Id] {
					continue
				}
				sb.WriteString(fmt.Sprintf("### %s: %s\n\n", st.Id, st.Description))
				sb.WriteString(fmt.Sprintf("**Execution Path:** %s\n\n", st.ExecutionPathId))
			}
		}
	}

	return sb.String()
}

// ---------------------------------------------------------------------------
// Combined slim renderer (Level 2 for RenderCombined)
// ---------------------------------------------------------------------------

// renderCombinedSlim renders the combined output with slim test spec.
// Architecture is omitted (already removed at Level 1).
func (s *Spec) renderCombinedSlim() string {
	var sb strings.Builder

	sb.WriteString("# PRD\n\n")
	sb.WriteString(s.PRDBody)
	sb.WriteString("\n")

	sb.WriteString("# Requirements\n\n")
	if s.Requirements != nil {
		sb.WriteString(s.Requirements.Render())
	}
	sb.WriteString("\n")

	sb.WriteString("# Test Specification\n\n")
	if s.TestSpec != nil {
		sb.WriteString(renderTestSpecSlim(s.TestSpec))
	}
	sb.WriteString("\n")

	sb.WriteString("# Tasks\n\n")
	if s.Tasks != nil {
		sb.WriteString(s.Tasks.Render())
	}
	sb.WriteString("\n")

	return sb.String()
}

// renderCombinedLevel1 renders the combined output without architecture.
func (s *Spec) renderCombinedLevel1() string {
	var sb strings.Builder

	sb.WriteString("# PRD\n\n")
	sb.WriteString(s.PRDBody)
	sb.WriteString("\n")

	sb.WriteString("# Requirements\n\n")
	if s.Requirements != nil {
		sb.WriteString(s.Requirements.Render())
	}
	sb.WriteString("\n")

	sb.WriteString("# Test Specification\n\n")
	if s.TestSpec != nil {
		sb.WriteString(s.TestSpec.Render())
	}
	sb.WriteString("\n")

	sb.WriteString("# Tasks\n\n")
	if s.Tasks != nil {
		sb.WriteString(s.Tasks.Render())
	}
	sb.WriteString("\n")

	return sb.String()
}
