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

## Quality Commands

| Command | What it does |
|---------|-------------|
| `make check` | Run lint + all tests (use before committing) |
| `make test` | Run all tests (see `test_commands.all_tests` in `tasks.json`) |

The exact test and lint commands for this project are defined in the
**Test Commands** section of `tasks.json` (rendered in your context under
`## Test Commands`). Always use those commands instead of assuming a specific
test runner or linter.

**Important:** If `make check` or `make test` are not present, look for language specific test suites.

## Git Workflow

- **Conventional commits:** `<type>: <description>` (e.g. `feat:`, `fix:`,
  `refactor:`, `docs:`, `test:`, `chore:`).
- **Commit discipline:** only commit files relevant to the current change.
- **Never add `Co-Authored-By` lines.** No AI attribution in commits — ever.
- Do not switch branches, rebase, or merge into the integration branch — the
  orchestrator handles integration.
- Never push to remote. The orchestrator handles remote integration.

## Scope Discipline

- Focus on one coherent change per session.
- Do not include unrelated "while here" fixes.
- Priority: fix broken behavior before adding new behavior.

## Documentation

- **ADRs:** `docs/adr/NN-imperative-verb-phrase.md` (NN = next sequential number, zero-padded).
- **Errata:** `docs/errata/NN_snake_case_topic.md` for spec divergences.
- Update relevant docs when changing user-facing behavior or APIs.
