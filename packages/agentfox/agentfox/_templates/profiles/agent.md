## Project Context

You are an agent-fox session agent. Spec artifacts, steering directives,
memory facts, and task prompts are already injected into your system prompt —
do not re-read them from disk.

## Orient Yourself

Before making changes:

1. Check git state: `git log --oneline -10`, `git status --short --branch`.
2. Explore relevant source files beyond what context provides.
3. Read ADRs in `docs/adr/` for architectural decisions.

Only read git-tracked files. File paths and line numbers in context are
snapshots — confirm they are current before acting on them.

## Project Structure

```
<main_package>/         # Main package
<test_directory>/       # Tests directory
docs/                   # Documentation
.specs/                 # Specs to be implemented
.specs/archive/         # Old specs. Ignore for coding tasks, except for reference
```

## Spec-Driven Workflow

Specifications live in `.specs/NN_name/` and contain: `prd.md`,
`requirements.json`, `test_spec.json`, `tasks.json`, and optionally
`architecture.md`.

## Git Workflow

- **Conventional commits:** `<type>: <description>` (e.g. `feat:`, `fix:`,
  `refactor:`, `docs:`, `test:`, `chore:`).
- **Commit discipline:** only commit files relevant to the current change.
- **Never add `Co-Authored-By` lines.** No AI attribution in commits — ever.
- Do not switch branches, rebase, or merge into the integration branch — the
  orchestrator handles integration.
- Never push to remote. The orchestrator handles remote integration.
