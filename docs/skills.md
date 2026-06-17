# Skills

agent-fox ships with a set of Claude Code skills -- slash commands that guide
you through common workflows like writing specs, documenting decisions, and
simplifying code. Skills are interactive: you invoke them in Claude Code and
work through the steps together with the agent.

## Installation

Install all bundled skills into your project with:

```bash
af init --skills
```

This copies each skill template to `.claude/skills/{name}/SKILL.md`, making
them available as slash commands in Claude Code. Re-running the command updates
skills to the latest bundled versions.

---

## af-spec

**Spec-driven development: from idea to implementation-ready spec package.**

Transforms a PRD, product idea, or GitHub issue into five specification
artifacts with full traceability from requirements through design, tests, and
tasks.

### What it produces

| File | Content |
|------|---------|
| `prd.md` | Finalized product requirements document |
| `requirements.md` | EARS-patterned acceptance criteria and edge cases |
| `design.md` | Interfaces, data models, correctness properties, error handling |
| `test_spec.md` | Language-agnostic test contracts with full requirement coverage |
| `tasks.md` | Implementation checklist (test-first: group 1 is always "write failing tests") |

All files are saved to `.agent-fox/specs/NN_specification_name/`.

### Workflow

1. **Understand the PRD** -- accepts a file path, GitHub issue URL, or inline
   description. Identifies ambiguities and asks for clarification.
2. **Learn the context** -- analyzes the existing codebase, finds the next spec
   number, identifies cross-spec dependencies.
3. **Write requirements** -- EARS syntax (WHEN/SHALL/IF/THEN), max 10
   requirements per spec, automated verification only.
4. **Write design** -- architecture overview with Mermaid diagrams, typed
   interfaces, correctness properties (formal invariants testable via
   property-based tests), error handling table.
5. **Write test spec** -- translates every acceptance criterion and correctness
   property into test contracts with preconditions, inputs, expected outputs,
   and assertion pseudocode. 100% coverage matrix.
6. **Write tasks** -- group 1 is always "write failing spec tests." Subsequent
   groups implement code. Each group has a verification subtask with specific
   test commands.

### When to use

Starting a new feature from a PRD, idea, or GitHub issue. When you want
test-first, spec-driven development with full traceability.
