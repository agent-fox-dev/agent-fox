# Agent Instructions

Instructions for coding agents (Cursor, Claude Code, Codex, etc.) working on
this repository. Treat this file as mandatory policy for every coding session.

## Understand Before You Code

Before making any changes, orient yourself:

1. Read `README.md` for the project overview and quick-start.
2. Read `.specs/steering.md` if it exists: project-level directives that apply
   to every agent.
3. Read the architectural decisions and errata under `docs/`.
4. Look at the code layout and at the tests beside the code you will touch.
5. Check git state: `git log --oneline -20`, `git status --short --branch`.

Read in depth, not by skimming. Read only files tracked by git (`git ls-files`);
skip anything `.gitignore` covers. Do not implement anything before finishing
these steps.

## Spec-Driven Workflow

Work is specified in `.specs/NN_name/` (numbered by creation order):
`prd.md` (intent), `requirements.json`, `test_spec.json`, `tasks.json`, and
optionally `architecture.md`. A spec is valid only when every criterion is
verified by a test, every test is owned by a task, and every execution path is
exercised by a smoke test. Specs in `.specs/archive/` are history: read them
for reference only.

Check any `external_apis` named in a spec against the libraries actually
installed; signatures in a spec may be unverified assumptions.

## Quality Commands

| Command | What it does |
|---|---|
| `make check` | lint + all tests; run before committing |
| `make test` | all tests |
| `make lint` | formatting and static checks |

If a target is missing, find the project's own test and lint commands.

## Git Workflow

- Branch from `main` as `feature/<descriptive-name>`. Never commit to `main`
  directly.
- Use conventional commits: `<type>: <description>` (`feat:`, `fix:`,
  `refactor:`, `docs:`, `test:`, `chore:`).
- Commit only files that belong to the current change.
- No AI attribution in commits: no `Co-Authored-By` lines.
- **Branches you create by hand stay local.** Do not push a feature branch to
  origin; only `main` is pushed. This rule is about you, the coding agent. It
  does not govern the project's own tools: `impl` and `fix` push the
  branches they create (`impl/*`, `fix/*`) and open a pull request by default
  (`--land pr`). `--land branch` pushes without a pull request and `--land
  none` commits locally and pushes nothing. The default stays `pr`; use
  `--land none` for a run that must not leave the machine.

## Scope

- One coherent change per session; no unrelated "while here" fixes.
- Fix broken behavior before adding new behavior.

## Documentation

- When you add or change user-facing behavior, public APIs, configuration or
  architecture, update the relevant docs in the same session.
- Product requirement docs live in `docs/prds/`, decisions in `docs/adr/`,
  spec divergences in `docs/errata/`, other docs in `docs/`. Number new PRDs
  and ADRs with the next free two-digit prefix.

## Session Completion

A session is complete when:

1. `make lint` and `make test` pass.
2. The change is committed with a conventional message.
3. The change is merged into `main` locally.
4. `git status` is clean.
5. You have left a brief handoff note: what was done, what remains.
