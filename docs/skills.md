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

Transforms a PRD, product idea, or GitHub issue into a complete specification
package in the v1.2 JSON format using the `spec` CLI. Full traceability from
requirements through tests and tasks.

### What it produces

| File | Content |
|------|---------|
| `prd.md` | Finalized PRD with YAML frontmatter |
| `requirements.json` | EARS-patterned acceptance criteria, correctness properties, execution paths |
| `test_spec.json` | Language-agnostic test contracts with full requirement coverage |
| `tasks.json` | Implementation task groups with state machine |
| `architecture.md` | Architecture overview with interfaces and diagrams (optional) |

All files are saved to `.agent-fox/specs/NN_specification_name/`.

### Workflow

1. **Understand the PRD** -- accepts a file path, GitHub issue URL, or inline
   description. Identifies ambiguities and asks for clarification.
2. **Learn the context** -- analyzes the existing codebase, finds the next spec
   number, identifies cross-spec dependencies.
3. **Create spec** -- runs `spec new` to create the spec directory and PRD with
   YAML frontmatter.
4. **Refine** -- runs `spec refine` for AI-powered PRD quality assessment.
   Loops with user answers until the PRD is accepted.
5. **Generate artifacts** -- runs `spec generate` to create `requirements.json`,
   `test_spec.json`, and `tasks.json`.
6. **Create architecture** (optional) -- `architecture.md` with high-level
   design, module responsibilities, and Mermaid diagrams. Skipped for simple
   specs.
7. **Validate** -- runs `spec validate` to check JSON schema conformance and
   cross-file integrity via the `afspec` library.

### When to use

Starting a new feature from a PRD, idea, or GitHub issue. When you want
test-first, spec-driven development with full traceability.
