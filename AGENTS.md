# Agent Instructions

Instructions for coding agents (Cursor, Claude Code, Codex, etc.) working on
this repository. Treat this file as mandatory policy for every coding session.

## Understand Before You Code (MANDATORY)

Before making any changes, orient yourself:

1. **Read `README.md`** for project overview and quick-start.
2. **Read `.specs/steering.md`** if it exists — project-level directives that
   apply to all agents and skills. Follow any instructions found there.
3. **Read ADRs and errata** in `docs/` for architectural context.
4. **Explore the codebase:** see the layout below; Go tests sit beside the code
   they cover.
5. **Check git state:** `git log --oneline -20`, `git status --short --branch`.

**Important:** Read all documents and code in depth — don't skim.

**Important:** Only read files tracked by git. Skip anything matched by
`.gitignore`. When in doubt, run `git ls-files` to see what's tracked.

Do not implement anything before completing these steps.

## Project Structure

```
afspec/                 # Spec format library (package afspec)
  legacy/               # Read-only version 1 types
  schemas/              # Bundled JSON Schemas, embedded at compile time
specgen/                # The spec pipeline: PRD, three generation phases, project audit
issuetriage/            # The issue triage pipeline
codefix/                # The fix pipeline
codeimpl/               # The implementation pipeline: a spec, task by task, on a branch
internal/               # Shared, not importable from outside this repo
  toolio/               # Input classification, the JSON envelope, the shared CLI shell
  agentrun/             # Model resolution, the phase runner, the read-only invariant, the shell guard
  project/              # What the repository is written in, and the test-command audit
  ghapi/                # GitHub REST client
  gitx/  checks/        # git, and the command that decides whether a change is correct
cmd/                    # Executables: spec, issue, fix, impl (plus af and nightshift stubs)
docs/                   # Documentation, ADRs and PRDs
skills/                 # The markdown skills the tools replaced, kept for reference
testdata/               # Shared test fixtures
.specs/                 # Specs to be implemented
.specs/archive/         # Old specs. Ignore for coding tasks, except for reference
```

The four tools share one interface: one positional input (text, a file path, a
GitHub URL, or `-`), one JSON object on stdout, progress on stderr, one
exit-code table. Adding a subcommand to any of them is a change to that
interface — see [ADR 03](docs/adr/03-rebuild-the-skills-as-tools.md) before
proposing one.

Tests live beside the code they cover, as `*_test.go`.

## Spec-Driven Workflow

This project uses spec-driven development. Specifications live in
`.specs/NN_name/` (numbered by creation order) and contain:

- `prd.md` — narrative intent: the "why" and "what"
- `requirements.json` — EARS criteria and end-to-end execution paths
- `test_spec.json` — one flat list of tests, each with a `kind` and a `verifies` list
- `tasks.json` — one flat list of tasks, each owning criteria and tests
- `architecture.md` — (optional) modules, interfaces, data models, technology choices

The format is version 2, specified in the
[`spec`](https://github.com/agent-fox-dev/spec) repository
(`specification/spec-format-v2.md`). A spec is valid only when every criterion
is verified by a test, every test is owned by a task, every execution path is
exercised by a smoke test, and the final integration task owns every smoke
test. Coverage and traceability are derived, never stored.

Cross-reference `external_apis` in `requirements.json` against installed
libraries — API signatures in specs may be unverified assumptions.

## Quality Commands

| Command | What it does |
|---------|-------------|
| `make check` | Run lint + all tests (use before committing) |
| `make test` | Run all tests (`go test ./... -count=1`) |
| `make lint` | `gofmt` + `go vet` |

Run the full quality suite before committing:

```
make check
```

**Important:** If `make check` or `make test` are not present, look for language specific test suites.

## Git Workflow

- **Branch from `main`: `feature/<descriptive-name>`.
- **Never commit directly** to `main`.
- **Conventional commits:** `<type>: <description>` (e.g. `feat:`, `fix:`,
  `refactor:`, `docs:`, `test:`, `chore:`).
- **Commit discipline:** only commit files relevant to the current change.
- **Never add `Co-Authored-By` lines.** No AI attribution in commits — ever.
- **Feature branches are local-only** — do not push them to origin. Only `main` is pushed to the remote.

## Scope Discipline

- Focus on one coherent change per session.
- Do not include unrelated "while here" fixes.
- Priority: fix broken behavior before adding new behavior.

## Documentation

- **PRDs** live in `docs/prd/NN-imperative-verb-phrase.md`. To choose NN,
  list existing files, find the max numeric prefix, and use the next number
  zero-padded to two digits for consistency (three digits once past 99).
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

## Session Completion

A session is not complete until:

1. `make lint` and `make test` passes (no regressions).
2. Changes are committed with a clear conventional commit message.
3. Changes are merged into `main` locally.
4. `git status` shows a clean working tree.
5. You provide a brief handoff note summarizing what was done and what remains.