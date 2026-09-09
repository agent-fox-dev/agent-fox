package afspec

import (
	"fmt"
	"strings"
)

// renderConfig holds the resolved rendering options.
type renderConfig struct {
	maxTokens int
}

// RenderOption configures a render call.
type RenderOption func(*renderConfig)

// WithMaxTokens caps the estimated token count of the rendered output.
// Values below 1 disable the budget.
func WithMaxTokens(n int) RenderOption {
	return func(c *renderConfig) {
		c.maxTokens = n
	}
}

func resolveOpts(opts []RenderOption) renderConfig {
	var cfg renderConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

func budgetActive(cfg renderConfig) bool { return cfg.maxTokens > 0 }

func sumMapTokens(m map[string]string) int {
	total := 0
	for _, v := range m {
		total += EstimateTokens(v)
	}
	return total
}

// renderTestSlim renders a test in reduced form. It keeps `verifies` and
// `then` — a coder that cannot see what a test asserts cannot write it — and
// drops `given`, `when`, `pseudocode` and `real_components`.
func renderTestSlim(sb *strings.Builder, t Test) {
	sb.WriteString(fmt.Sprintf("### %s (%s): %s\n\n", t.Id, t.Kind, t.Title))
	sb.WriteString(fmt.Sprintf("**Verifies:** %s\n\n", strings.Join(t.Verifies, ", ")))
	sb.WriteString("**Then:**\n\n")
	for _, then := range t.Then {
		sb.WriteString(fmt.Sprintf("- %s\n", then))
	}
	sb.WriteString("\n")
}

// renderTestSpecSlim renders every test in slim form.
func renderTestSpecSlim(ts *TestSpecV2Json) string {
	return renderTestsScoped(ts, nil, true)
}

// renderCombinedLevel1 renders the combined document without the architecture
// section.
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

// renderCombinedSlim renders the combined document without the architecture
// section and with the tests in slim form.
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
