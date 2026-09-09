package afspec

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// RenderCombined renders all artifacts (PRD body, requirements, test spec,
// tasks, architecture if present) as a single concatenated Markdown document.
// When called with WithMaxTokens(N), it applies progressive truncation to
// fit the output within N estimated tokens.
//
// Progressive truncation levels:
//   - Level 0: full render (returned if within budget)
//   - Level 1: drop architecture section
//   - Level 2: re-render test spec in slim mode (architecture already dropped)
func (s *Spec) RenderCombined(opts ...RenderOption) string {
	cfg := resolveOpts(opts)

	// Level 0 — full render
	result := s.renderCombinedFull()

	if !budgetActive(cfg) {
		return result
	}

	if EstimateTokens(result) <= cfg.maxTokens {
		return result
	}

	// Level 1 — drop architecture
	if s.Architecture != "" {
		result = s.renderCombinedLevel1()
		if EstimateTokens(result) <= cfg.maxTokens {
			return result
		}
	}

	// Level 2 — slim test spec (architecture already dropped)
	return s.renderCombinedSlim()
}

// renderCombinedFull renders all artifacts including architecture.
// Section order per spec Section 11.1: PRD → Architecture → Requirements →
// Test Specification → Tasks.
func (s *Spec) renderCombinedFull() string {
	var sb strings.Builder

	sb.WriteString("# PRD\n\n")
	sb.WriteString(s.PRDBody)
	sb.WriteString("\n")

	if s.Architecture != "" {
		sb.WriteString("# Architecture\n\n")
		sb.WriteString(s.Architecture)
		sb.WriteString("\n")
	}

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

// RenderIndividual renders each artifact separately and returns a map keyed
// by artifact name (e.g., "prd", "requirements", "test_spec", "tasks",
// "architecture") to its Markdown string. If architecture is absent, the
// "architecture" key is omitted from the returned map.
//
// When called with WithMaxTokens(N), it applies progressive truncation:
//   - Level 0: full render (returned if within budget)
//   - Level 1: drop architecture key
//   - Level 2: re-render test_spec in slim mode (architecture already dropped)
func (s *Spec) RenderIndividual(opts ...RenderOption) map[string]string {
	cfg := resolveOpts(opts)

	// Level 0 — full render
	result := make(map[string]string)

	result["prd"] = s.PRDBody

	if s.Requirements != nil {
		result["requirements"] = s.Requirements.Render()
	} else {
		result["requirements"] = ""
	}

	if s.TestSpec != nil {
		result["test_spec"] = s.TestSpec.Render()
	} else {
		result["test_spec"] = ""
	}

	if s.Tasks != nil {
		result["tasks"] = s.Tasks.Render()
	} else {
		result["tasks"] = ""
	}

	if s.Architecture != "" {
		result["architecture"] = s.Architecture
	}

	if !budgetActive(cfg) {
		return result
	}

	// Budget evaluation — Level 0
	if sumMapTokens(result) <= cfg.maxTokens {
		return result
	}

	// Level 1 — drop architecture
	if _, hasArch := result["architecture"]; hasArch {
		delete(result, "architecture")
		if sumMapTokens(result) <= cfg.maxTokens {
			return result
		}
	}

	// Level 2 — slim test spec
	if s.TestSpec != nil {
		result["test_spec"] = renderTestSpecSlim(s.TestSpec)
	}
	return result
}

// RenderIndividualScoped renders each artifact filtered to the refs of a
// target task group. It collects all requirement_refs and test_spec_refs
// from every subtask in targetGroup, renders only the referenced
// requirements and test entries, renders the target group with full subtask
// detail and all other groups as one-line summaries, and includes PRD body
// and architecture unfiltered.
//
// When subtasks have no explicit refs, the inference chain is invoked:
//  1. Traceability-based inference (inferRefsFromTraceability)
//  2. Text-based inference (inferRefsFromSubtaskText)
//  3. Unscoped fallback with scoped tasks
//
// Traceability inference supports partial inference: if only one ref type
// is found, the other type is rendered in full (unscoped for that section).
// The fallback always scopes tasks via renderScopedTasks.
//
// When called with WithMaxTokens(N), it applies progressive truncation:
//   - Level 0: full scoped render (returned if within budget)
//   - Level 1: drop architecture key
//   - Level 2: re-render test_spec in slim mode (scoped or full)
func (s *Spec) RenderIndividualScoped(targetGroup int, opts ...RenderOption) map[string]string {
	cfg := resolveOpts(opts)

	// Find the target group in tasks
	var group *TaskGroup
	if s.Tasks != nil {
		for i := range s.Tasks.TaskGroups {
			if s.Tasks.TaskGroups[i].Id == targetGroup {
				group = &s.Tasks.TaskGroups[i]
				break
			}
		}
	}

	// If the group doesn't exist, fall back to full rendering
	if group == nil {
		return s.RenderIndividual(opts...)
	}

	// Collect all explicit refs from subtasks in the target group
	reqRefs := make(map[string]bool)
	tsRefs := make(map[string]bool)
	for _, sub := range group.Subtasks {
		for _, ref := range sub.RequirementRefs {
			reqRefs[ref] = true
		}
		for _, ref := range sub.TestSpecRefs {
			tsRefs[ref] = true
		}
	}

	// partialInference tracks whether the inferred refs came from
	// traceability. When true and one ref type is empty, that section
	// is rendered in full rather than scoped to an empty set.
	partialInference := false

	// If no explicit refs found, run the inference chain
	if len(reqRefs) == 0 && len(tsRefs) == 0 {
		// Step 1: traceability-based inference
		inferredReq, inferredTS := inferRefsFromTraceability(s, targetGroup)
		if len(inferredReq) > 0 || len(inferredTS) > 0 {
			// Traceability yielded refs — activate partial inference
			partialInference = true
			for _, r := range inferredReq {
				reqRefs[r] = true
			}
			for _, r := range inferredTS {
				tsRefs[r] = true
			}
		} else {
			// Step 2: text-based inference
			inferredReq, inferredTS = inferRefsFromSubtaskText(s, targetGroup)
			if len(inferredReq) > 0 || len(inferredTS) > 0 {
				for _, r := range inferredReq {
					reqRefs[r] = true
				}
				for _, r := range inferredTS {
					tsRefs[r] = true
				}
			} else {
				// Step 3: both inference strategies returned empty —
				// full unscoped fallback, but still scope tasks to
				// the target group (fixes the pre-existing Go bug
				// where return s.RenderIndividual() was used).
				result := s.RenderIndividual(opts...)
				if s.Tasks != nil {
					result["tasks"] = s.renderScopedTasks(targetGroup)
				}
				return result
			}
		}
	}

	result := make(map[string]string)

	// PRD body and architecture are included unfiltered
	result["prd"] = s.PRDBody
	if s.Architecture != "" {
		result["architecture"] = s.Architecture
	}

	// Render requirements: scoped when refs available, full when partial
	// inference found no req refs (traceability only).
	if partialInference && len(reqRefs) == 0 {
		if s.Requirements != nil {
			result["requirements"] = s.Requirements.Render()
		} else {
			result["requirements"] = ""
		}
	} else {
		if s.Requirements != nil {
			result["requirements"] = s.renderScopedRequirements(reqRefs)
		} else {
			result["requirements"] = ""
		}
	}

	// Render test spec: scoped when refs available, full when partial
	// inference found no ts refs (traceability only).
	if partialInference && len(tsRefs) == 0 {
		if s.TestSpec != nil {
			result["test_spec"] = s.TestSpec.Render()
		} else {
			result["test_spec"] = ""
		}
	} else {
		if s.TestSpec != nil {
			result["test_spec"] = s.renderScopedTestSpec(tsRefs)
		} else {
			result["test_spec"] = ""
		}
	}

	// Render scoped tasks — always scoped to the target group
	if s.Tasks != nil {
		result["tasks"] = s.renderScopedTasks(targetGroup)
	} else {
		result["tasks"] = ""
	}

	// Budget evaluation
	if !budgetActive(cfg) {
		return result
	}

	// Budget check — Level 0
	if sumMapTokens(result) <= cfg.maxTokens {
		return result
	}

	// Level 1 — drop architecture
	if _, hasArch := result["architecture"]; hasArch {
		delete(result, "architecture")
		if sumMapTokens(result) <= cfg.maxTokens {
			return result
		}
	}

	// Level 2 — slim test spec (scoped or full depending on available IDs)
	if s.TestSpec != nil {
		if len(tsRefs) > 0 {
			result["test_spec"] = renderTestSpecScopedSlim(s.TestSpec, tsRefs)
		} else {
			result["test_spec"] = renderTestSpecSlim(s.TestSpec)
		}
	}

	return result
}

// renderScopedRequirements renders only the requirements whose IDs (or
// criteria/edge-case IDs) appear in the refs set, plus a Spec Overview
// listing all requirements.
func (s *Spec) renderScopedRequirements(refs map[string]bool) string {
	var sb strings.Builder

	// Spec Overview listing all requirement IDs and titles
	sb.WriteString("## Spec Overview\n\n")
	sb.WriteString("All requirements in this specification (full detail shown only for the active task group):\n\n")
	for _, req := range s.Requirements.Requirements {
		sb.WriteString(fmt.Sprintf("- **%s:** %s", req.Id, req.Title))
		if !isRequirementInScope(req, refs) {
			sb.WriteString(" (other group)")
		} else {
			sb.WriteString(" (included below)")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	// Render only the scoped requirements in full
	sb.WriteString("## Introduction\n\n")
	sb.WriteString(s.Requirements.Introduction)
	sb.WriteString("\n\n")

	if len(s.Requirements.Glossary) > 0 {
		sb.WriteString("## Glossary\n\n")
		sb.WriteString("| Term | Definition |\n")
		sb.WriteString("|------|------------|\n")
		glossaryTerms := make([]string, 0, len(s.Requirements.Glossary))
		for term := range s.Requirements.Glossary {
			glossaryTerms = append(glossaryTerms, term)
		}
		sort.Strings(glossaryTerms)
		for _, term := range glossaryTerms {
			sb.WriteString(fmt.Sprintf("| %s | %s |\n", term, s.Requirements.Glossary[term]))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Requirements\n\n")
	for _, req := range s.Requirements.Requirements {
		if !isRequirementInScope(req, refs) {
			continue
		}
		renderRequirement(&sb, req)
	}

	return sb.String()
}

// isRequirementInScope checks if a requirement or any of its criteria/edge
// cases are referenced by the scope refs.
func isRequirementInScope(req Requirement, refs map[string]bool) bool {
	if refs[req.Id] {
		return true
	}
	for _, c := range req.AcceptanceCriteria {
		if refs[c.Id] {
			return true
		}
	}
	for _, c := range req.EdgeCases {
		if refs[c.Id] {
			return true
		}
	}
	return false
}

// renderScopedTestSpec renders only the test entries whose IDs appear in the
// refs set.
func (s *Spec) renderScopedTestSpec(refs map[string]bool) string {
	var sb strings.Builder
	sb.WriteString("## Test Cases\n\n")

	for _, tc := range s.TestSpec.TestCases {
		if refs[tc.Id] {
			renderTestCase(&sb, tc)
		}
	}

	if len(s.TestSpec.PropertyTests) > 0 {
		hasScoped := false
		for _, pt := range s.TestSpec.PropertyTests {
			if refs[pt.Id] {
				hasScoped = true
				break
			}
		}
		if hasScoped {
			sb.WriteString("## Property Tests\n\n")
			for _, pt := range s.TestSpec.PropertyTests {
				if refs[pt.Id] {
					sb.WriteString(fmt.Sprintf("### %s: %s\n\n", pt.Id, pt.Description))
				}
			}
		}
	}

	if len(s.TestSpec.EdgeCaseTests) > 0 {
		hasScoped := false
		for _, ect := range s.TestSpec.EdgeCaseTests {
			if refs[ect.Id] {
				hasScoped = true
				break
			}
		}
		if hasScoped {
			sb.WriteString("## Edge Case Tests\n\n")
			for _, ect := range s.TestSpec.EdgeCaseTests {
				if refs[ect.Id] {
					sb.WriteString(fmt.Sprintf("### %s: %s\n\n", ect.Id, ect.Description))
				}
			}
		}
	}

	if len(s.TestSpec.SmokeTests) > 0 {
		hasScoped := false
		for _, st := range s.TestSpec.SmokeTests {
			if refs[st.Id] {
				hasScoped = true
				break
			}
		}
		if hasScoped {
			sb.WriteString("## Smoke Tests\n\n")
			for _, st := range s.TestSpec.SmokeTests {
				if refs[st.Id] {
					sb.WriteString(fmt.Sprintf("### %s: %s\n\n", st.Id, st.Description))
				}
			}
		}
	}

	return sb.String()
}

// renderScopedTasks renders the target group with full subtask detail and
// all other groups as one-line completion summaries.
func (s *Spec) renderScopedTasks(targetGroup int) string {
	var sb strings.Builder
	sb.WriteString("## Tasks\n\n")

	for _, g := range s.Tasks.TaskGroups {
		if g.Id == targetGroup {
			// Full detail for target group
			renderTaskGroupFull(&sb, g)
		} else {
			// One-line summary for other groups
			renderTaskGroupSummary(&sb, g)
		}
	}

	return sb.String()
}

// renderTaskGroupFull renders a task group with full subtask detail.
func renderTaskGroupFull(sb *strings.Builder, g TaskGroup) {
	doneCount := 0
	for _, sub := range g.Subtasks {
		if sub.State == SubtaskStateDone {
			doneCount++
		}
	}
	sb.WriteString(fmt.Sprintf("### %d. %s (%d/%d subtasks done)\n\n",
		g.Id, g.Title, doneCount, len(g.Subtasks)))

	for _, sub := range g.Subtasks {
		checkbox := "[ ]"
		if sub.State == SubtaskStateDone {
			checkbox = "[x]"
		}
		sb.WriteString(fmt.Sprintf("- %s %s: %s\n", checkbox, sub.Id, sub.Title))
		for _, detail := range sub.Details {
			sb.WriteString(fmt.Sprintf("  - %s\n", detail))
		}
		if len(sub.RequirementRefs) > 0 {
			sb.WriteString(fmt.Sprintf("  - _Requirements: %s_\n", strings.Join(sub.RequirementRefs, ", ")))
		}
		if len(sub.TestSpecRefs) > 0 {
			sb.WriteString(fmt.Sprintf("  - _Test Spec: %s_\n", strings.Join(sub.TestSpecRefs, ", ")))
		}
	}

	// Verification subtask
	if g.Verification.Id != "" {
		sb.WriteString(fmt.Sprintf("- [ ] %s Verify task group %d\n", g.Verification.Id, g.Id))
		for _, check := range g.Verification.Checks {
			sb.WriteString(fmt.Sprintf("  - %s\n", check))
		}
	}

	sb.WriteString("\n")
}

// renderTaskGroupSummary renders a task group as a one-line completion summary.
func renderTaskGroupSummary(sb *strings.Builder, g TaskGroup) {
	doneCount := 0
	for _, sub := range g.Subtasks {
		if sub.State == SubtaskStateDone {
			doneCount++
		}
	}
	checkbox := "[ ]"
	if doneCount == len(g.Subtasks) && len(g.Subtasks) > 0 {
		checkbox = "[x]"
	}
	sb.WriteString(fmt.Sprintf("- %s %d. %s (%d/%d subtasks done)\n\n",
		checkbox, g.Id, g.Title, doneCount, len(g.Subtasks)))
}

// Render renders the requirements artifact as a Markdown string.
func (r *RequirementsV1Json) Render() string {
	var sb strings.Builder

	sb.WriteString("## Introduction\n\n")
	sb.WriteString(r.Introduction)
	sb.WriteString("\n\n")

	if len(r.Glossary) > 0 {
		sb.WriteString("## Glossary\n\n")
		sb.WriteString("| Term | Definition |\n")
		sb.WriteString("|------|------------|\n")
		glossaryTerms := make([]string, 0, len(r.Glossary))
		for term := range r.Glossary {
			glossaryTerms = append(glossaryTerms, term)
		}
		sort.Strings(glossaryTerms)
		for _, term := range glossaryTerms {
			sb.WriteString(fmt.Sprintf("| %s | %s |\n", term, r.Glossary[term]))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Requirements\n\n")
	for _, req := range r.Requirements {
		renderRequirement(&sb, req)
	}

	if len(r.CorrectnessProperties) > 0 {
		sb.WriteString("## Correctness Properties\n\n")
		for _, prop := range r.CorrectnessProperties {
			sb.WriteString(fmt.Sprintf("### %s: %s\n\n", prop.Id, prop.Title))
			sb.WriteString(fmt.Sprintf("**For any:** %s\n\n", prop.ForAny))
			sb.WriteString(fmt.Sprintf("**Invariant:** %s\n\n", prop.Invariant))
			if len(prop.Validates) > 0 {
				sb.WriteString(fmt.Sprintf("**Validates:** %s\n\n", strings.Join(prop.Validates, ", ")))
			}
		}
	}

	if len(r.ExecutionPaths) > 0 {
		sb.WriteString("## Execution Paths\n\n")
		for _, path := range r.ExecutionPaths {
			sb.WriteString(fmt.Sprintf("### %s: %s\n\n", path.Id, path.Title))
			for i, step := range path.Steps {
				sb.WriteString(fmt.Sprintf("%d. **%s** %s\n", i+1, step.Actor, step.Action))
			}
			sb.WriteString("\n")
		}
	}

	if len(r.ErrorHandling) > 0 {
		sb.WriteString("## Error Handling\n\n")
		sb.WriteString("| ID | Condition | Behavior | Requirement |\n")
		sb.WriteString("|----|-----------|----------|-------------|\n")
		for _, eh := range r.ErrorHandling {
			sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
				eh.Id, eh.Condition, eh.Behavior, eh.RequirementId))
		}
		sb.WriteString("\n")
	}

	if len(r.ExternalApis) > 0 {
		sb.WriteString("## External APIs\n\n")
		for _, api := range r.ExternalApis {
			sb.WriteString(fmt.Sprintf("### `%s` (%s)\n\n", api.Package, api.Version))
			sb.WriteString("| Symbol | Import Path | Signature | Notes |\n")
			sb.WriteString("|--------|-------------|-----------|-------|\n")
			for _, sym := range api.Symbols {
				notes := ""
				if sym.Notes != nil {
					notes = *sym.Notes
				}
				sb.WriteString(fmt.Sprintf("| `%s` | `%s` | `%s` | %s |\n",
					sym.Name, sym.ImportPath, sym.Signature, notes))
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// renderRequirement renders a single requirement as Markdown.
func renderRequirement(sb *strings.Builder, req Requirement) {
	sb.WriteString(fmt.Sprintf("### %s: %s\n\n", req.Id, req.Title))

	sb.WriteString(fmt.Sprintf("**User Story:** As a %s, I want %s, so that %s.\n\n",
		req.UserStory.Role, req.UserStory.Goal, req.UserStory.Benefit))

	if len(req.AcceptanceCriteria) > 0 {
		sb.WriteString("#### Acceptance Criteria\n\n")
		for _, c := range req.AcceptanceCriteria {
			sb.WriteString(fmt.Sprintf("1. [%s] %s\n", c.Id, c.RenderEARSSentence()))
		}
		sb.WriteString("\n")
	}

	if len(req.EdgeCases) > 0 {
		sb.WriteString("#### Edge Cases\n\n")
		for _, c := range req.EdgeCases {
			sb.WriteString(fmt.Sprintf("1. [%s] %s\n", c.Id, c.RenderEARSSentence()))
		}
		sb.WriteString("\n")
	}
}

// Render renders the test spec artifact as a Markdown string.
func (ts *TestSpecV1Json) Render() string {
	var sb strings.Builder

	sb.WriteString("## Test Cases\n\n")
	for _, tc := range ts.TestCases {
		renderTestCase(&sb, tc)
	}

	if len(ts.PropertyTests) > 0 {
		sb.WriteString("## Property Tests\n\n")
		for _, pt := range ts.PropertyTests {
			sb.WriteString(fmt.Sprintf("### %s: %s\n\n", pt.Id, pt.Description))
			sb.WriteString(fmt.Sprintf("**Property:** %s\n\n", pt.PropertyId))
			sb.WriteString(fmt.Sprintf("**For any:** %s\n\n", pt.ForAnyStrategy))
			sb.WriteString(fmt.Sprintf("**Invariant:** %s\n\n", pt.InvariantCheck))
			if len(pt.Validates) > 0 {
				sb.WriteString(fmt.Sprintf("**Validates:** %s\n\n", strings.Join(pt.Validates, ", ")))
			}
		}
	}

	if len(ts.EdgeCaseTests) > 0 {
		sb.WriteString("## Edge Case Tests\n\n")
		for _, ect := range ts.EdgeCaseTests {
			sb.WriteString(fmt.Sprintf("### %s: %s\n\n", ect.Id, ect.Description))
			sb.WriteString(fmt.Sprintf("**Requirement:** %s\n\n", ect.RequirementId))
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

// renderInterfaceValue converts an interface{} field value to a string
// suitable for Markdown output.  It returns ("", false) when the value is nil
// (caller should omit the field), the raw string for string values, and
// compact JSON for any other type (map, slice, number, bool, …).
func renderInterfaceValue(v interface{}) (string, bool) {
	if v == nil {
		return "", false
	}
	if s, ok := v.(string); ok {
		return s, true
	}
	b, err := json.Marshal(v)
	if err != nil {
		// Fallback to fmt if Marshal fails (should not happen in practice).
		return fmt.Sprintf("%v", v), true
	}
	return string(b), true
}

// renderTestCase renders a single test case as Markdown.
func renderTestCase(sb *strings.Builder, tc TestCase) {
	sb.WriteString(fmt.Sprintf("### %s: %s\n\n", tc.Id, tc.Description))
	sb.WriteString(fmt.Sprintf("**Requirement:** %s\n", tc.RequirementId))
	sb.WriteString(fmt.Sprintf("**Type:** %s\n\n", tc.Kind))

	if len(tc.Preconditions) > 0 {
		sb.WriteString("**Preconditions:**\n\n")
		for _, p := range tc.Preconditions {
			sb.WriteString(fmt.Sprintf("- %s\n", p))
		}
		sb.WriteString("\n")
	}

	if inputStr, ok := renderInterfaceValue(tc.Input); ok {
		sb.WriteString(fmt.Sprintf("**Input:** `%s`\n\n", inputStr))
	}

	if expectedStr, ok := renderInterfaceValue(tc.Expected); ok {
		sb.WriteString(fmt.Sprintf("**Expected:** %s\n\n", expectedStr))
	}

	if tc.AssertionPseudocode != "" {
		sb.WriteString("**Assertion pseudocode:**\n\n")
		sb.WriteString(fmt.Sprintf("```\n%s\n```\n\n", tc.AssertionPseudocode))
	}
}

// Render renders the tasks artifact as a Markdown string with
// checkbox-formatted subtasks.
func (t *TasksV1Json) Render() string {
	var sb strings.Builder

	sb.WriteString("## Tasks\n\n")

	for _, g := range t.TaskGroups {
		renderTaskGroupFull(&sb, g)
	}

	return sb.String()
}
