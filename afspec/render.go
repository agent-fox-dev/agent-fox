package afspec

import (
	"fmt"
	"sort"
	"strings"
)

// Rendering is deterministic: same JSON in, same Markdown out (§11).
//
// Two v1 rendering bugs are fixed by construction here: every criterion
// renders its contract, and every test renders all of its fields whatever its
// kind. A test rendered as a title alone was a v1 bug, not a feature.

// RenderCombined renders the whole spec as one Markdown document in the order
// of §11.1: PRD body, architecture if present, requirements, tests, tasks.
//
// With WithMaxTokens(N) it applies progressive truncation:
//   - level 0: full render
//   - level 1: drop the architecture section
//   - level 2: additionally render the tests in slim form
func (s *Spec) RenderCombined(opts ...RenderOption) string {
	cfg := resolveOpts(opts)

	result := s.renderCombinedFull()
	if !budgetActive(cfg) || EstimateTokens(result) <= cfg.maxTokens {
		return result
	}

	if s.Architecture != "" {
		result = s.renderCombinedLevel1()
		if EstimateTokens(result) <= cfg.maxTokens {
			return result
		}
	}

	return s.renderCombinedSlim()
}

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

// RenderIndividual renders each artifact separately, keyed by "prd",
// "requirements", "test_spec", "tasks" and — only when present —
// "architecture". The same truncation levels as RenderCombined apply.
func (s *Spec) RenderIndividual(opts ...RenderOption) map[string]string {
	cfg := resolveOpts(opts)

	result := map[string]string{
		"prd":          s.PRDBody,
		"requirements": "",
		"test_spec":    "",
		"tasks":        "",
	}
	if s.Requirements != nil {
		result["requirements"] = s.Requirements.Render()
	}
	if s.TestSpec != nil {
		result["test_spec"] = s.TestSpec.Render()
	}
	if s.Tasks != nil {
		result["tasks"] = s.Tasks.Render()
	}
	if s.Architecture != "" {
		result["architecture"] = s.Architecture
	}

	if !budgetActive(cfg) || sumMapTokens(result) <= cfg.maxTokens {
		return result
	}

	if _, hasArch := result["architecture"]; hasArch {
		delete(result, "architecture")
		if sumMapTokens(result) <= cfg.maxTokens {
			return result
		}
	}

	if s.TestSpec != nil {
		result["test_spec"] = renderTestSpecSlim(s.TestSpec)
	}
	return result
}

// RenderIndividualScoped renders the spec scoped to one task (§11.1): the PRD
// body and architecture unfiltered; the requirements that own the task's
// criteria in full and all others as one line each; the task's tests in full;
// the paths those tests verify; the task in full and all others as one line.
//
// Because rules C7 and C8 guarantee that every task lists its own tests and
// criteria, the v1 inference chain (traceability lookup, then text matching,
// then a full-spec fallback) is not needed and does not exist here. An unknown
// task ID falls back to the unscoped render.
func (s *Spec) RenderIndividualScoped(taskID int, opts ...RenderOption) map[string]string {
	cfg := resolveOpts(opts)

	var task *Task
	if s.Tasks != nil {
		for i := range s.Tasks.Tasks {
			if s.Tasks.Tasks[i].Id == taskID {
				task = &s.Tasks.Tasks[i]
				break
			}
		}
	}
	if task == nil {
		return s.RenderIndividual(opts...)
	}

	// Criteria in scope: the task's criterion IDs, plus every criterion of a
	// requirement the task claims wholesale.
	criteriaRefs := map[string]bool{}
	requirementRefs := map[string]bool{}
	for _, ref := range task.Criteria {
		criteriaRefs[ref] = true
		requirementRefs[ref] = true
	}

	testRefs := make(map[string]bool, len(task.Tests))
	for _, ref := range task.Tests {
		testRefs[ref] = true
	}

	// Paths in scope: those verified by the task's tests.
	pathRefs := map[string]bool{}
	if s.TestSpec != nil {
		for _, t := range s.TestSpec.Tests {
			if !testRefs[t.Id] {
				continue
			}
			for _, ref := range t.Verifies {
				pathRefs[ref] = true
			}
		}
	}

	result := map[string]string{"prd": s.PRDBody}
	if s.Architecture != "" {
		result["architecture"] = s.Architecture
	}

	if s.Requirements != nil {
		result["requirements"] = s.renderScopedRequirements(criteriaRefs, requirementRefs, pathRefs)
	} else {
		result["requirements"] = ""
	}

	if s.TestSpec != nil {
		result["test_spec"] = renderTestsScoped(s.TestSpec, testRefs, false)
	} else {
		result["test_spec"] = ""
	}

	if s.Tasks != nil {
		result["tasks"] = s.renderScopedTasks(taskID)
	} else {
		result["tasks"] = ""
	}

	if !budgetActive(cfg) || sumMapTokens(result) <= cfg.maxTokens {
		return result
	}

	if _, hasArch := result["architecture"]; hasArch {
		delete(result, "architecture")
		if sumMapTokens(result) <= cfg.maxTokens {
			return result
		}
	}

	if s.TestSpec != nil {
		result["test_spec"] = renderTestsScoped(s.TestSpec, testRefs, true)
	}
	return result
}

// isRequirementInScope reports whether a requirement is claimed by the task,
// either wholesale by its own ID or through one of its criteria.
func isRequirementInScope(req Requirement, criteriaRefs, requirementRefs map[string]bool) bool {
	if requirementRefs[req.Id] {
		return true
	}
	for _, c := range req.Criteria {
		if criteriaRefs[c.Id] {
			return true
		}
	}
	return false
}

// renderScopedRequirements renders the in-scope requirements in full, every
// other requirement as one line, and the execution paths the task's tests
// verify.
func (s *Spec) renderScopedRequirements(criteriaRefs, requirementRefs, pathRefs map[string]bool) string {
	var sb strings.Builder

	sb.WriteString("## Spec Overview\n\n")
	sb.WriteString("All requirements in this specification (full detail shown only for the active task):\n\n")
	for _, req := range s.Requirements.Requirements {
		suffix := " (other task)"
		if isRequirementInScope(req, criteriaRefs, requirementRefs) {
			suffix = " (included below)"
		}
		sb.WriteString(fmt.Sprintf("- **%s:** %s%s\n", req.Id, req.Title, suffix))
	}
	sb.WriteString("\n")

	sb.WriteString("## Introduction\n\n")
	sb.WriteString(s.Requirements.Introduction)
	sb.WriteString("\n\n")

	renderGlossary(&sb, s.Requirements.Glossary)

	sb.WriteString("## Requirements\n\n")
	for _, req := range s.Requirements.Requirements {
		if isRequirementInScope(req, criteriaRefs, requirementRefs) {
			renderRequirement(&sb, req)
		}
	}

	var scopedPaths []ExecutionPath
	for _, p := range s.Requirements.ExecutionPaths {
		if pathRefs[p.Id] {
			scopedPaths = append(scopedPaths, p)
		}
	}
	renderExecutionPaths(&sb, scopedPaths)

	return sb.String()
}

// renderScopedTasks renders the target task in full and every other task as a
// one-line summary.
func (s *Spec) renderScopedTasks(taskID int) string {
	var sb strings.Builder
	sb.WriteString("## Tasks\n\n")
	for _, t := range s.Tasks.Tasks {
		if t.Id == taskID {
			renderTaskFull(&sb, t, &s.Tasks.TestCommands)
		} else {
			renderTaskSummary(&sb, t)
		}
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// requirements.json
// ---------------------------------------------------------------------------

// Render renders the requirements artifact as Markdown.
func (r *RequirementsV2Json) Render() string {
	var sb strings.Builder

	sb.WriteString("## Introduction\n\n")
	sb.WriteString(r.Introduction)
	sb.WriteString("\n\n")

	renderGlossary(&sb, r.Glossary)

	sb.WriteString("## Requirements\n\n")
	for _, req := range r.Requirements {
		renderRequirement(&sb, req)
	}

	renderExecutionPaths(&sb, r.ExecutionPaths)

	if len(r.ExternalApis) > 0 {
		sb.WriteString("## External APIs\n\n")
		for _, api := range r.ExternalApis {
			status := "verified"
			if !api.Verified {
				status = "UNVERIFIED — confirm these signatures before use"
			}
			sb.WriteString(fmt.Sprintf("### `%s` (%s) — %s\n\n", api.Package, api.Version, status))
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

func renderGlossary(sb *strings.Builder, glossary RequirementsV2JsonGlossary) {
	if len(glossary) == 0 {
		return
	}
	sb.WriteString("## Glossary\n\n")
	sb.WriteString("| Term | Definition |\n")
	sb.WriteString("|------|------------|\n")
	terms := make([]string, 0, len(glossary))
	for term := range glossary {
		terms = append(terms, term)
	}
	sort.Strings(terms)
	for _, term := range terms {
		sb.WriteString(fmt.Sprintf("| %s | %s |\n", term, glossary[term]))
	}
	sb.WriteString("\n")
}

// renderRequirement renders one requirement with every criterion as its EARS
// sentence followed by its contract when it has one (§11.2).
func renderRequirement(sb *strings.Builder, req Requirement) {
	sb.WriteString(fmt.Sprintf("### %s: %s\n\n", req.Id, req.Title))

	if req.Rationale != nil && *req.Rationale != "" {
		sb.WriteString(fmt.Sprintf("**Rationale:** %s\n\n", *req.Rationale))
	}

	sb.WriteString("#### Criteria\n\n")
	for _, c := range req.Criteria {
		sb.WriteString(fmt.Sprintf("1. [%s] %s\n", c.Id, c.RenderEARSSentence()))
		if contract := c.ContractText(); contract != "" {
			sb.WriteString(fmt.Sprintf("   → %s\n", contract))
		}
	}
	sb.WriteString("\n")
}

func renderExecutionPaths(sb *strings.Builder, paths []ExecutionPath) {
	if len(paths) == 0 {
		return
	}
	sb.WriteString("## Execution Paths\n\n")
	for _, path := range paths {
		sb.WriteString(fmt.Sprintf("### %s: %s\n\n", path.Id, path.Title))
		for i, step := range path.Steps {
			sb.WriteString(fmt.Sprintf("%d. **%s** %s\n", i+1, step.Actor, step.Action))
		}
		sb.WriteString("\n")
	}
}

// ---------------------------------------------------------------------------
// test_spec.json
// ---------------------------------------------------------------------------

// Render renders the test spec artifact as Markdown. Every test renders all
// of its fields, whatever its kind.
func (ts *TestSpecV2Json) Render() string {
	return renderTestsScoped(ts, nil, false)
}

// renderTestsScoped renders the tests, optionally filtered to an ID set and
// optionally in slim form. A nil filter renders every test.
func renderTestsScoped(ts *TestSpecV2Json, refs map[string]bool, slim bool) string {
	var sb strings.Builder
	sb.WriteString("## Tests\n\n")
	for _, t := range ts.Tests {
		if refs != nil && !refs[t.Id] {
			continue
		}
		if slim {
			renderTestSlim(&sb, t)
		} else {
			renderTest(&sb, t)
		}
	}
	return sb.String()
}

// renderTest renders one test with all of its fields.
func renderTest(sb *strings.Builder, t Test) {
	sb.WriteString(fmt.Sprintf("### %s (%s): %s\n\n", t.Id, t.Kind, t.Title))
	sb.WriteString(fmt.Sprintf("**Verifies:** %s\n\n", strings.Join(t.Verifies, ", ")))

	if len(t.Given) > 0 {
		sb.WriteString("**Given:**\n\n")
		for _, g := range t.Given {
			sb.WriteString(fmt.Sprintf("- %s\n", g))
		}
		sb.WriteString("\n")
	}

	sb.WriteString(fmt.Sprintf("**When:** %s\n\n", t.When))

	sb.WriteString("**Then:**\n\n")
	for _, then := range t.Then {
		sb.WriteString(fmt.Sprintf("- %s\n", then))
	}
	sb.WriteString("\n")

	if len(t.RealComponents) > 0 {
		sb.WriteString(fmt.Sprintf("**Real components (must not be mocked):** %s\n\n",
			strings.Join(t.RealComponents, ", ")))
	}

	if t.Pseudocode != nil && *t.Pseudocode != "" {
		sb.WriteString("**Pseudocode:**\n\n")
		sb.WriteString(fmt.Sprintf("```\n%s\n```\n\n", *t.Pseudocode))
	}
}

// ---------------------------------------------------------------------------
// tasks.json
// ---------------------------------------------------------------------------

// Render renders the tasks artifact as Markdown.
func (t *TasksV2Json) Render() string {
	var sb strings.Builder
	sb.WriteString("## Tasks\n\n")
	for _, task := range t.Tasks {
		renderTaskFull(&sb, task, &t.TestCommands)
	}
	return sb.String()
}

// renderTaskFull renders a task with its steps, touches, criteria, tests,
// done_when and the implicit definition of done (§8.3, §11.2).
func renderTaskFull(sb *strings.Builder, task Task, commands *TestCommands) {
	checkbox := "[ ]"
	if task.State == TaskStateDone {
		checkbox = "[x]"
	}
	optional := ""
	if task.IsOptional() {
		optional = " _(optional)_"
	}
	sb.WriteString(fmt.Sprintf("### %s %d. %s (%s, %s)%s\n\n",
		checkbox, task.Id, task.Title, task.Kind, task.State, optional))

	if len(task.Criteria) > 0 {
		sb.WriteString(fmt.Sprintf("**Criteria:** %s\n\n", strings.Join(task.Criteria, ", ")))
	}
	sb.WriteString(fmt.Sprintf("**Tests:** %s\n\n", strings.Join(task.Tests, ", ")))

	if len(task.DependsOn) > 0 {
		deps := make([]string, len(task.DependsOn))
		for i, d := range task.DependsOn {
			deps[i] = fmt.Sprint(d)
		}
		sb.WriteString(fmt.Sprintf("**Depends on:** %s\n\n", strings.Join(deps, ", ")))
	}

	sb.WriteString("**Steps:**\n\n")
	for _, step := range task.Steps {
		sb.WriteString(fmt.Sprintf("1. %s\n", step))
	}
	sb.WriteString("\n")

	if len(task.Touches) > 0 {
		sb.WriteString("**Touches:**\n\n")
		for _, f := range task.Touches {
			sb.WriteString(fmt.Sprintf("- `%s`\n", f))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("**Done when:**\n\n")
	sb.WriteString("- the tests listed above exist, are executable and pass\n")
	if commands != nil && commands.AllTests != "" {
		sb.WriteString(fmt.Sprintf("- `%s` passes\n", commands.AllTests))
	}
	if commands != nil && commands.Linter != "" {
		sb.WriteString(fmt.Sprintf("- `%s` passes\n", commands.Linter))
	}
	for _, d := range task.DoneWhen {
		sb.WriteString(fmt.Sprintf("- %s\n", d))
	}
	sb.WriteString("\n")
}

// renderTaskSummary renders a task as a one-line summary.
func renderTaskSummary(sb *strings.Builder, task Task) {
	checkbox := "[ ]"
	if task.State == TaskStateDone {
		checkbox = "[x]"
	}
	sb.WriteString(fmt.Sprintf("- %s %d. %s (%s, %s)\n", checkbox, task.Id, task.Title, task.Kind, task.State))
}
