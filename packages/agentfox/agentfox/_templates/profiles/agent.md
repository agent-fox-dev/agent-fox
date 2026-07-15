## Project Context

You are an agent-fox session agent. The orchestrator has already injected all
relevant spec context, task prompts, and curated knowledge into your system
prompt. Work within the context provided.

## Pre-Injected Context

The orchestrator has already injected into your system prompt:

- **Steering directives** from `.agent-fox/steering.md`
- **Spec artifacts** (requirements, test spec, tasks) scoped to your assigned work
- **Memory facts** and prior-group findings

Do not re-read these files from disk — they are already in your context above.

## Orient Yourself

Before making changes, quickly orient yourself in the codebase:

1. **Check git state:** `git log --oneline -10`, `git status --short --branch`.
2. **Explore relevant source files** when implementation details are needed
   beyond what the spec context provides.
3. **Read ADRs** in `docs/adr/` when your task involves architectural decisions.

**Important:** Only read files tracked by git. Skip anything matched by
`.gitignore`. When in doubt, run `git ls-files` to see what's tracked.

**Verify External References:** File paths, line numbers, and function names
in issue descriptions, triage analysis, spec artifacts, and context summaries
are snapshots from when they were written. Before navigating to or acting on a
specific file:line reference, confirm the file still exists at that path and
the referenced code is at (or near) the cited line. Files may have been
renamed, moved, or reorganized; line numbers shift with every edit. Use `grep`
or search to locate the actual code if references are stale.

## Project Structure

```
<main_package>/         # Main package
<test_directory>/       # Tests directory
docs/                   # Documentation
.specs/                 # Specs to be implemented
.specs/archive/         # Old specs. Ignore for coding tasks, except for reference
```

## Spec-Driven Workflow

This project uses spec-driven development. Specifications live in
`.specs/NN_name/` (numbered by creation order) and contain five artifacts:

- `prd.md` — product requirements document (source of truth)
- `requirements.json` — EARS-syntax acceptance criteria
- `test_spec.json` — language-agnostic test contracts
- `tasks.json` — implementation plan with state machine
- `architecture.md` — (optional) architecture overview

## Quality Commands

| Command | What it does |
|---------|-------------|
| `make check` | Run lint + all tests (use before committing) |
| `make test` | Run all tests (see `test_commands.all_tests` in `tasks.json`) |

The exact test and lint commands for this project are defined in the
**Test Commands** section of `tasks.json` (rendered in your context under
`## Test Commands`). Always use those commands instead of assuming a specific
test runner or linter.

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

- **ADRs** live in `docs/adr/NN-imperative-verb-phrase.md`. To choose NN,
  list existing files, find the max numeric prefix, and use the next number
  zero-padded to two digits for consistency (three digits once past 99).
- **Errata** live in `docs/errata/NN_snake_case_topic.md` — for spec
  divergences. NN is the spec number the erratum relates to (e.g.
  `28_github_issue_rest_api.md` for spec 28). For project-wide errata not
  tied to a specific spec, omit the numeric prefix.
- **Other docs** live in `docs/{topic}.md`.
- When you add or change user-facing behavior, public APIs, configuration, or
  architecture, update the relevant documentation in the same session.
