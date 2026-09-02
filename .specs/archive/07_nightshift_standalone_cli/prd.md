---
spec_id: '07'
spec_name: nightshift_standalone_cli
title: Nightshift Standalone Cli
status: draft
created_at: '2026-06-24T13:31:47.450260+00:00'
updated_at: '2026-06-24T13:36:49.602019+00:00'
owner: ''
source: interactive
schema_version: 1
---
# Extract night-shift into standalone CLI package

## Overview

Move the `night-shift` command from the `af` CLI into its own standalone CLI
tool and Python package (`nightshift`). The new `night-shift` CLI replicates
the exact same functionality, output, and user experience as the current
`af night-shift` subcommand.

## Owner

Michael Kuehl (project maintainer)

## Motivation

Night Shift is a continuously-running fix-only daemon that operates
independently of the rest of the af orchestrator workflow (plan → code →
standup). Extracting it into its own package:

- Lets users install and run night-shift without the full af orchestrator.
- Reduces coupling between the daemon and the orchestrator CLI.
- Makes the install footprint smaller for users who only want fix automation.

## Goals

The success criteria for this extraction are deliberately narrow — functional
parity with the current `af night-shift` subcommand is the primary objective.
Specifically:

- The standalone `night-shift` CLI must reproduce the exact behavior, output,
  and user experience of the removed `af night-shift` subcommand.
- All existing night-shift tests, migrated to `packages/nightshift/tests/`,
  must pass without modification to business logic.
- An integration test verifying the `python -m nightshift` (or `night-shift`)
  entry point must pass.
- No formal performance or adoption metrics are required; successful extraction
  and verified test parity constitute a complete and successful delivery.

## Tech Stack

- Python 3.12+
- Click 8.1+ (CLI framework)
- agentfox (core library — reuse `agentfox.nightshift.*` modules)
- uv workspace member
- Hatchling build backend

## Functional Requirements

### FR-1: Remove night-shift from the af CLI

Remove the `night-shift` command registration from `packages/af/af/app.py`.
Delete `packages/af/af/nightshift.py`. Remove completely — no deprecation stub.
This removal is intentional and coordinated with the `4.0.0-rc4` pre-release
cycle; no prior deprecation notice or major version bump is required.

### FR-2: Create new `nightshift` package

Create `packages/nightshift/` with the following structure:

```
packages/nightshift/
  pyproject.toml
  nightshift/
    __init__.py
    __main__.py      # python -m nightshift support
    app.py           # Click entry point
```

- Python package name: `nightshift`
- CLI entry point: `night-shift = "nightshift.app:main"`
- Version: `4.0.0-rc4` (aligned with other packages)
- Dependencies: `agentfox>=4.0.0rc4`, `click>=8.1`, `rich>=15.0`, and
  `duckdb>=1.5.4`. Both `rich` and `duckdb` must be declared as **direct**
  dependencies in `pyproject.toml`, matching the pattern used in `af`'s
  `pyproject.toml` (where both are also direct dependencies, not transitive).

#### FR-2a: Package metadata

The `pyproject.toml` for the new package must carry minimal metadata consistent
with the style of other workspace packages (`af`, `spec`). Required fields:

- `name = "nightshift"`
- `version = "4.0.0-rc4"`
- `description = "Standalone CLI for the AgentFox Night Shift fix daemon"`
- `license` and `authors` — follow the same values as the `af` package
- PyPI classifiers and homepage URL are optional and should mirror the `af`
  package if present there

### FR-3: Replicate CLI behavior

The standalone `night-shift` CLI must provide the exact same user experience:

- **Banner**: Display the fox ASCII art banner on startup (same as `af` does),
  suppressed by `--quiet` or `--json`.
- **Global options**: `--json/--no-json`, `--verbose/-v`, `--quiet/-q`,
  `--trace`, `--version`.
- **`--version` output**: Use Click's default `version_option` behavior, which
  reads the version from the package metadata defined in `pyproject.toml`
  (i.e., `4.0.0-rc4`). The exact format produced by Click's `version_option`
  is acceptable and no custom format string is required.
- **Config loading**: Load config from `.agent-fox/config.toml` (same as af).
- **Output**: Same startup message ("Night-shift daemon starting…"), summary
  stats at exit, JSONL progress events in `--json` mode.
- **Signal handling**: Same graceful shutdown (first SIGINT) and immediate
  abort (second SIGINT) behavior.
- **Exit codes**: 0 (clean shutdown), 1 (startup failure), 130 (immediate abort).
- **AF_AGENT=1 support**: Use `AgentFoxGroup` from `agentfox.io` for
  consistent error handling, OutputManager construction, and agent-mode defaults.
- **Environment variable parity**: All environment variables currently
  supported by `af night-shift` are provided automatically by reusing
  `AgentFoxGroup` and `common_options` from `agentfox.io` — no env var
  bindings are implemented in `af` itself; they all live in the shared
  infrastructure. Known env vars include `AF_CONFIG`, `AF_LOG_LEVEL`, and
  `AF_AGENT`; any others defined in `AgentFoxGroup` or `common_options` are
  inherited automatically. The implementer should verify the complete list by
  auditing `agentfox.io` source, but no env var supported by the current
  `af night-shift` invocation path may be silently dropped.

### FR-4: Workspace integration

- Add `nightshift` as a uv workspace member in the root `pyproject.toml`.
- Add `nightshift>=4.0.0rc4` to the root project dependencies.
- Add `nightshift = { workspace = true }` to `[tool.uv.sources]`.
- Add `packages/nightshift/tests/` to the `testpaths` list in the root
  `pyproject.toml`, matching the pattern used for other packages (e.g.
  `packages/af/tests/`, `packages/spec/tests/`).

### FR-5: Update documentation

Update all documentation that references `af night-shift`:

- `README.md` — update the Night Shift section and Quick Start to reference
  the standalone `night-shift` CLI instead of `af night-shift`. Update the
  package dependency diagram (see FR-6).
- `docs/cli-reference.md` — move the night-shift section out of the af
  subcommands and document it as a standalone CLI. Update the command table.
- `docs/config-reference.md` — update any references to `af night-shift` or
  `agent-fox night-shift` to just `night-shift`.
- `docs/architecture/04-night-shift.md` — update CLI references.
- `docs/architecture/README.md` — update CLI references.
- `docs/architecture/03-execution-and-archetypes.md` — update if it references
  `af night-shift`.
- `docs/profiles.md` — update if it references `af night-shift`.

No dedicated migration guide or runtime deprecation warning is required — the
removal is intentional and clean within the `4.0.0-rc4` pre-release cycle.
Updated documentation in `README.md` is the sole user-facing communication of
the change.

**Acceptance criterion for FR-5**: After implementation, run the following
grep command and confirm zero matches:

```sh
grep -r "af night-shift" docs/ README.md
```

A clean result (no output) is sufficient to verify documentation update
completeness. No separate CI step or reviewer sign-off is required.

### FR-6: Update the package diagram

The README contains a package dependency diagram. Update it to reflect the new
`nightshift` package and its dependencies. `nightshift` depends only on
`agentfox` (plus `click`, `rich`, and `duckdb` per the dependency pattern
established in FR-2) — it has no direct dependency on `agentspec` or `afspec`:

```
af  ──▶  agentfox  ──▶  afspec
              ▲
spec ──▶ agentspec ──┘──▶  afspec

nightshift ──▶  agentfox
```

## Non-Functional Requirements

### NFR-1: Code reuse

The new CLI module (`nightshift/app.py`) must import and delegate to the same
`agentfox.nightshift.*` modules that the current `af/nightshift.py` uses.
No business logic should be duplicated.

### NFR-2: Test migration

The following test files currently in `packages/af/tests/` are known to be
night-shift-related and are candidates for migration to
`packages/nightshift/tests/`:

| File | Notes |
|------|-------|
| `test_spec04_req3.py` | JSONL progress event tests |
| `test_spec04_smoke.py` | Smoke tests for the night-shift daemon |
| `test_spec04_properties.py` | Property-based tests for daemon behavior |
| `test_code_dry_run.py` | Daemon guard / dry-run tests |

> **Note**: Some of these files may test both `af` and night-shift behavior.
> They should be **split** rather than moved entirely where mixed concerns exist.

Migration strategy:

1. **Before moving any files**, inspect `packages/af/tests/conftest.py` (and
   any sub-conftest files) to identify shared fixtures. The fixture sharing
   situation is unknown and must be determined by the implementer at migration
   time.
2. If fixtures are used exclusively by night-shift tests, physically move them
   (`git mv`) alongside the test files.
3. If fixtures are shared between af and night-shift tests, copy the relevant
   fixtures into `packages/nightshift/tests/conftest.py` and refactor as
   needed to avoid duplication — do not break the af test suite.
4. Tests that verify `af` CLI behavior (e.g., "af night-shift is not a
   recognized command") must remain in or be added to the af test suite.

The precise fixture split is left to the implementer's judgment after
inspection; the requirement is that all migrated tests pass and no af tests
regress. Passing the full migrated test suite is a required success criterion.

### NFR-3: Entry point integration test

Add an integration test to `packages/nightshift/tests/` that invokes the CLI
entry point and verifies it is correctly wired:

- The test must invoke either `python -m nightshift --help` or
  `night-shift --help` (or both) and assert a zero exit code and expected
  output (e.g. presence of the `--version` flag in help text).
- This test must run as part of the standard `make check` / CI pipeline.

### NFR-4: CI/CD pipeline integration

No changes to CI/CD pipeline configuration files are required. The root
`pyproject.toml` must be updated in two ways to fully integrate the new
package:

1. Add `nightshift` as a uv workspace member and dependency (covered in FR-4).
2. **Manually add `packages/nightshift/tests/` to the `testpaths` list** in
   the root `pyproject.toml` (covered in FR-4), matching the explicit pattern
   used for all other packages. This single edit is sufficient for the existing
   `make check` pipeline to automatically include lint and test runs for the
   new package — no other CI/CD configuration changes are required.

## Out of Scope

- Changing the night-shift business logic, fix pipeline, or daemon behavior.
- Modifying `agentfox.nightshift.*` modules (these stay as-is).
- Adding new features to the night-shift CLI.
- Deprecation notices, runtime migration warnings, or a dedicated upgrade guide
  — removal is clean and intentional within the `4.0.0-rc4` pre-release cycle.
- CHANGELOG.md or release-notes updates for this extraction (not required).
- Direct dependencies on `agentspec` or `afspec` in the `nightshift` package.
- Rollback planning — since this extraction occurs entirely within the
  `4.0.0-rc4` pre-release cycle, no downstream consumers depend on a stable
  `af night-shift` command and no rollback plan is required.

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 04_af_agentic_cli | all | 1 | Imports AgentFoxGroup, OutputManager, common_options from agentfox.io. **Status: complete** — spec 04 artifacts are available and interfaces are stable and in use. This spec is not blocked by spec 04. |

## Source

Source: Input provided by user via interactive prompt
