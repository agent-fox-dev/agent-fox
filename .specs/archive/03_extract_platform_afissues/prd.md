---
spec_id: '03'
spec_name: extract_platform_afissues
title: Extract Platform Layer into Standalone afissues Package
status: draft
created_at: '2026-07-14T07:39:57.138201+00:00'
updated_at: '2026-07-14T07:47:31.020770+00:00'
owner: ''
source: https://github.com/agent-fox-dev/agent-fox/issues/715
schema_version: 1
---
# Extract Platform Layer into Standalone `afissues` Package

## Intent

The `PlatformProtocol` interface, `GitHubPlatform` implementation, and label constants currently live inside the `agentfox` core library. Any service that needs issue-tracking or PR operations must depend on the entire `agentfox` package, pulling in heavyweight transitive dependencies (Anthropic SDK, sentence-transformers, tree-sitter, DuckDB) that have nothing to do with issue management.

Extracting the platform layer into a standalone `afissues` package enables lightweight consumers, external tooling, and future forge implementations (GitLab, Gitea) to use the platform abstraction independently. This extraction is a prerequisite for a planned follow-on GitLab Platform spec — the GitLab implementation will be added directly to `afissues` rather than `agentfox.platform`.

## Goals

- Decouple the platform abstraction from the `agentfox` monolith so it can be consumed independently with minimal dependencies (`httpx` + stdlib).
- Preserve all existing platform functionality — no behavioral changes, no new features.
- Enable future forge implementations (GitLab, Gitea) to be added to `afissues` without touching `agentfox`.
- Keep the migration transparent to existing `agentfox` consumers — all internal imports are updated in one pass.
- Unblock the planned GitLab Platform spec which will target `afissues` as its home package.

## Non-goals

- Adding new platform implementations (GitLab, Gitea) — those are separate specs.
- Changing the `PlatformProtocol` interface or any method signatures.
- Adding backward-compatible re-export shims in `agentfox.platform.*` — this is an internal monorepo with no external consumers of that import path.
- Modifying the platform factory (`platform_factory.py`) beyond updating its import paths — factory logic stays in `agentfox.nightshift`.
- Extracting SSRF guard or retry logic into shared utilities — keep them co-located with `GitHubPlatform` in `afissues` for now.
- Adding a CI lint/import-isolation check to enforce `afissues` has zero imports from other workspace packages — deferred to a follow-on hardening spec; the `pyproject.toml` dependency declaration is sufficient for now.

## Background

### Prior Art

The archived `backend_protocol` spec covered a different concern — abstracting the AI backend protocol (Anthropic vs Google ADK), not the platform/forge abstraction. There is no scope overlap or conflict with this extraction.

### Downstream Specs

The GitLab Platform spec (the original "spec 03") was overwritten by this extraction spec during planning. It is planned as a follow-on spec but has not yet been created. No cross-spec dependency declaration is required in this PRD beyond this note.

### Execution Model

This spec is executed by the agent-fox autonomous pipeline (no designated human owner). The import migration across 56 files (12 source + 44 tests) is atomic within the task group that performs it — agent-fox processes specs in isolated worktrees per task group. If CI tests fail, the task group is retried or rejected. No additional migration strategy documentation is required.

## Functional Requirements

### Package Creation

- A new `packages/afissues/` workspace package is created with its own `pyproject.toml` using **hatchling** as the build backend, consistent with the existing workspace convention (matching `afaudit` and other workspace packages).
- The initial package version is **4.2.0**, matching the current `agentfox` version to align with the workspace release cadence.
- The `pyproject.toml` declares Python 3.12+ and `httpx>=0.27` as its sole external dependency. A minimal representative structure:
  ```toml
  [build-system]
  requires = ["hatchling"]
  build-backend = "hatchling.build"

  [project]
  name = "afissues"
  version = "4.2.0"
  description = "Standalone platform/forge abstraction layer for agent-fox"
  requires-python = ">=3.12"
  dependencies = ["httpx>=0.27"]

  [tool.hatch.build.targets.wheel]
  packages = ["afissues"]
  ```
- The package is registered in the uv workspace configuration (the root `pyproject.toml` uses `members = ["packages/*"]` so registration is automatic).
- The package is installable independently via `pip install` from the git repo subdirectory, without `agentfox` installed.
- A `py.typed` marker file (`afissues/py.typed`) is included in the package to declare it as a fully typed package for mypy/pyright consumers. This is required because `afissues` exports `PlatformProtocol`, a `typing.Protocol`, that downstream consumers type-check against.

### Module Extraction

- `afissues/protocol.py` contains: `PlatformProtocol`, `IssueResult`, `IssueComment`, and `NullPlatform` — moved verbatim from `agentfox/platform/protocol.py`.
  - `NullPlatform.create_pr()` intentionally raises `NotImplementedError` — this is existing behavior that must be preserved exactly as-is. Do not suppress or replace this error.
- `afissues/github.py` contains: `GitHubPlatform`, `_SSRFGuardTransport`, `_validate_github_url`, `_validate_transport_address`, `_check_address`, `parse_github_remote`, and all associated constants — moved verbatim from `agentfox/platform/github.py`, with import paths updated to reference `afissues.errors` instead of `agentfox.core.errors` and `afissues.protocol` instead of `agentfox.platform.protocol`.
- `afissues/labels.py` contains: `LabelSpec`, all `LABEL_*` constants, and `REQUIRED_LABELS` — moved verbatim from `agentfox/platform/labels.py`.
- `afissues/__init__.py` re-exports all public symbols explicitly. The complete re-export list is:
  ```python
  from afissues.protocol import PlatformProtocol, NullPlatform, IssueResult, IssueComment
  from afissues.github import GitHubPlatform, parse_github_remote
  from afissues.labels import (
      LabelSpec,
      LABEL_FIX,
      LABEL_FIXED,
      LABEL_NO_CHANGE,
      LABEL_IMPLEMENTED,
      LABEL_PRIORITY_HIGH,
      LABEL_PRIORITY_MEDIUM,
      LABEL_PRIORITY_LOW,
      REQUIRED_LABELS,
  )
  ```
  All symbols are re-exported; the package is small and all constants are public API.

### Deletion of `agentfox/platform/`

- The entire `packages/agentfox/agentfox/platform/` directory is deleted. This includes all source files and the `__init__.py`.
- The `agentfox/platform/__init__.py` is confirmed to contain only a docstring with no re-exports and no public symbols — it is safe to delete without any additional audit or migration step. The 12-file import migration count is unaffected by this.
- No re-export shims, no empty directories, and no `__init__.py` stubs are left behind.

### Error Type Independence

- `afissues/errors.py` defines its own error hierarchy with the following signatures:

  ```python
  class AfIssuesError(Exception):
      """Base error for afissues. Accepts **context kwargs stored as .context."""
      def __init__(self, message: str = "", **context: object) -> None:
          super().__init__(message)
          self.context: dict[str, object] = context

  class ConfigError(AfIssuesError):
      """Raised for configuration and validation errors (e.g., SSRF guard)."""

  class IntegrationError(AfIssuesError):
      """Raised for platform API/integration errors."""
      def __init__(self, message: str = "", *, retryable: bool = True, **context: object) -> None:
          super().__init__(message, **context)
          self.retryable = retryable
  ```

  - `AfIssuesError` accepts `**context` kwargs and stores them as a `.context` attribute, mirroring `AgentFoxError` exactly. This ensures `ConfigError` and `IntegrationError` both support the same calling convention.
  - `IntegrationError` preserves the `retryable` attribute with the same default (`True`) and the `**context` keyword arguments pattern.

- `afissues` error types do NOT subclass `agentfox.core.errors.AgentFoxError` — the package has zero dependency on `agentfox`.
- `agentfox.core.errors` continues to define its own `ConfigError` and `IntegrationError` for non-platform use within `agentfox`. Both hierarchies coexist — they serve different domains.
- Callers within `agentfox` that catch errors raised by platform operations (i.e., errors originating from `afissues` code) update their except clauses to catch `afissues.errors.IntegrationError` or `afissues.errors.ConfigError`. These callers are fully covered by the 12-file source migration.
- Callers that catch `agentfox.core.errors.IntegrationError` for non-platform errors (e.g., workspace integration failures) keep their existing imports unchanged.
- **Teardown paths are not a concern:** `GitHubPlatform.close()` is a no-op and does not raise or catch `IntegrationError`. No `finally` or `except` blocks in teardown paths catch platform errors — all exception handling is in the callers already covered by the 12-file migration.

### Dependency Footprint

- `afissues` declares only `httpx>=0.27` as an external dependency, plus stdlib.
- `afissues` has zero imports from any other workspace package (`agentfox`, `afspec`, `afaudit`, etc.).
- `agentfox` adds `afissues` as a workspace dependency in its `pyproject.toml`.
- `nightshift` does **not** import any `agentfox.platform.*` symbols directly (verified by grep). `nightshift` therefore gets `afissues` transitively through `agentfox` and does **not** need to declare `afissues` as an explicit dependency in its own `pyproject.toml`.
- The `packages/af` package declares `agentfox` as a dependency; `afissues` is therefore available to `af` transitively. The `af/pyproject.toml` does **not** need to be updated as part of this extraction.

### Import Migration

- 12 source files within `agentfox` that import from `agentfox.platform.*` are updated to import from `afissues` instead.
- 44 test files within `agentfox` that import from `agentfox.platform.*` are updated to import from `afissues` instead.
- 1 cross-package test file (`packages/af/tests/unit/test_init_labels.py`) is updated to import from `afissues.labels`. No change to `af/pyproject.toml` is required — `afissues` is available transitively through `af`'s existing `agentfox` dependency.
- The old `packages/agentfox/agentfox/platform/` directory is removed entirely — no `__init__.py`, no re-export shims, no empty directory left behind.
- The root `pyproject.toml` `testpaths` is updated to include `packages/afissues/tests`.
- After migration, no remaining imports of `agentfox.platform` exist anywhere in the workspace (verified by grep as part of CI validation).

### Test Migration

- All 10 test files currently in `packages/agentfox/tests/unit/platform/` are relocated to `packages/afissues/tests/unit/`.
- The `conftest.py` from `packages/agentfox/tests/unit/platform/` is relocated to `packages/afissues/tests/unit/conftest.py`. Any fixtures referencing `agentfox.platform.*` imports are updated to reference `afissues.*`. The `conftest.py` contains only platform-scoped fixtures with no `agentfox`-specific session-scoped state, so relocation does not affect test isolation in the remaining `agentfox` test suite.
- The property test `packages/agentfox/tests/property/platform/test_overhaul_props.py` is relocated to `packages/afissues/tests/property/`. The `.hypothesis/` directory is in `.gitignore` and is not checked in — no Hypothesis database or example files need to be relocated alongside the test file; Hypothesis will start fresh with no behavioral impact.
- The `packages/afissues/tests/` directory structure mirrors the standard workspace convention:
  ```
  packages/afissues/
  ├── tests/
  │   ├── conftest.py          # top-level conftest (if needed; may be empty)
  │   ├── unit/
  │   │   ├── conftest.py      # relocated from agentfox tests/unit/platform/
  │   │   └── <10 test files>
  │   └── property/
  │       └── test_overhaul_props.py
  ```
- Pytest configuration for `afissues` follows the same pattern as other workspace packages (pytest ini settings in `pyproject.toml` under `[tool.pytest.ini_options]`), including `testpaths = ["tests"]` scoped to the package root.
- Tests are updated to import from `afissues` instead of `agentfox.platform`.
- All relocated tests pass with no behavioral changes.

### Documentation Updates

- `packages/README.md` is updated: the package table includes `afissues` with its description, and the dependency graph shows `agentfox ──▶ afissues`.
- The root `README.md` dependency graph, package table, and "standalone libraries" section are updated to include `afissues`.

### Validation

- `make check` passes with no regressions (lint + all tests).
- `afissues` can be imported and used independently: `from afissues import PlatformProtocol, GitHubPlatform` works without `agentfox` installed.
- No remaining imports of `agentfox.platform` exist anywhere in the workspace (verified by grep).
- Type checkers (mypy/pyright) resolve `afissues` types correctly due to the presence of `py.typed`.

## Technical Boundaries

- Python 3.12+
- `httpx` for async HTTP client (version >=0.27, compatible with httpx 0.28.1 currently installed)
- uv workspace for monorepo package management
- Build backend: **hatchling** (consistent with workspace convention)
- Package name: `afissues` (both distribution name and import name)
- Initial version: **4.2.0** (aligned with current `agentfox` version)

## Dependencies

- **`httpx`** — async HTTP client used by `GitHubPlatform` for all REST API calls. Currently a transitive dependency of `agentfox` via `anthropic`; must be declared as a direct dependency in `afissues/pyproject.toml`.

## Verified External API

### `agentfox.platform.protocol` (local — being extracted)

| Symbol | Module | Signature | Notes |
|--------|--------|-----------|-------|
| `PlatformProtocol` | `protocol` | `class PlatformProtocol(Protocol)` | 12 async methods + `close()` |
| `NullPlatform` | `protocol` | `class NullPlatform` | No-op stub; `create_pr()` intentionally raises `NotImplementedError` — preserve exactly |
| `IssueResult` | `protocol` | `@dataclass(frozen=True)` | Fields: `number`, `title`, `html_url`, `body`, `labels` |
| `IssueComment` | `protocol` | `@dataclass(frozen=True)` | Fields: `id`, `body`, `user`, `created_at` |

### `agentfox.platform.github` (local — being extracted)

| Symbol | Module | Signature | Notes |
|--------|--------|-----------|-------|
| `GitHubPlatform` | `github` | `class GitHubPlatform` | 14 async methods, `forge_type = "github"` |
| `parse_github_remote` | `github` | `(remote_url: str) -> tuple[str, str] \| None` | Extracts `(owner, repo)` from GitHub URLs |
| `_validate_github_url` | `github` | `(url: str) -> None` | SSRF guard — raises `ConfigError` |
| `_SSRFGuardTransport` | `github` | `class _SSRFGuardTransport(httpx.AsyncHTTPTransport)` | Transport-level SSRF validation |
| `_GITHUB_TIMEOUT` | `github` | `httpx.Timeout(connect=30.0, ...)` | Timeout config constant |
| `_MAX_RETRIES` | `github` | `int = 3` | Retry limit constant |

### `agentfox.platform.labels` (local — being extracted)

| Symbol | Module | Signature | Notes |
|--------|--------|-----------|-------|
| `LabelSpec` | `labels` | `@dataclass(frozen=True)` | Fields: `name`, `color`, `description` |
| `LABEL_FIX` | `labels` | `str = "af:fix"` | |
| `LABEL_FIXED` | `labels` | `str = "af:fixed"` | |
| `LABEL_NO_CHANGE` | `labels` | `str = "af:no-change"` | |
| `LABEL_IMPLEMENTED` | `labels` | `str = "af:implemented"` | |
| `LABEL_PRIORITY_HIGH` | `labels` | `str = "priority:high"` | |
| `LABEL_PRIORITY_MEDIUM` | `labels` | `str = "priority:medium"` | |
| `LABEL_PRIORITY_LOW` | `labels` | `str = "priority:low"` | |
| `REQUIRED_LABELS` | `labels` | `list[LabelSpec]` | 4 entries (the 4 `af:*` labels, not the priority labels) |

### `agentfox.core.errors` (local — stays in agentfox, parallel hierarchy created in afissues)

| Symbol | Module | Signature | Notes |
|--------|--------|-----------|-------|
| `AgentFoxError` | `errors` | `class AgentFoxError(Exception)` | Base with `**context` kwargs |
| `ConfigError` | `errors` | `class ConfigError(AgentFoxError)` | Used by SSRF guard |
| `IntegrationError` | `errors` | `class IntegrationError(AgentFoxError)` | Has `retryable: bool = True` |

### `httpx` (external, v0.28.1)

| Symbol | Module | Signature | Notes |
|--------|--------|-----------|-------|
| `AsyncClient` | `httpx` | `class AsyncClient(...)` | Context manager for async HTTP |
| `AsyncHTTPTransport` | `httpx` | `class AsyncHTTPTransport(...)` | Base for SSRF guard transport |
| `Timeout` | `httpx` | `class Timeout(connect=, read=, write=, pool=)` | Connection timeout config |
| `ConnectTimeout` | `httpx` | exception | Retryable transport error |
| `ConnectError` | `httpx` | exception | Retryable transport error |
| `ReadTimeout` | `httpx` | exception | Retryable transport error |

## Design Decisions

1. **Clean error hierarchy break:** `afissues` defines its own `AfIssuesError` → `ConfigError` / `IntegrationError` hierarchy, completely independent of `agentfox.core.errors.AgentFoxError`. `AfIssuesError` mirrors `AgentFoxError` in signature (accepting `**context` kwargs stored as `.context`), ensuring the same calling convention throughout. All callers are updated in the same change. Rationale: true package independence outweighs the convenience of shared base classes; all callers are within the monorepo and can be updated atomically.

2. **No re-export shims:** The old `agentfox.platform.*` module is deleted entirely rather than kept as a re-export layer. The `agentfox/platform/__init__.py` contains only a docstring with no re-exports, confirming there is no public surface to preserve. Rationale: there are no external consumers of `agentfox.platform`, and shims create maintenance burden and confusion about the canonical import path.

3. **Extraction before GitLab Platform spec:** This extraction lands before the GitLab Platform spec is implemented, so that `GitLabPlatform` is built directly in `afissues` rather than being extracted after the fact. The GitLab Platform spec is planned but not yet created.

4. **Factory stays in `agentfox`:** The platform factory (`platform_factory.py`) remains in `agentfox.nightshift` because it couples platform construction with nightshift-specific configuration and environment variable handling. Only its import paths are updated.

5. **Coexisting error hierarchies:** Both `agentfox.core.errors.IntegrationError` and `afissues.errors.IntegrationError` exist simultaneously. The agentfox version is for workspace/engine errors; the afissues version is for platform/API errors. Callers catching platform-originated errors update to the afissues type; callers catching workspace errors keep the agentfox type. Teardown paths (`close()`) are a non-issue — `GitHubPlatform.close()` is a no-op and does not interact with the error hierarchy.

6. **Autonomous execution and atomic migration:** This spec is executed by the agent-fox autonomous pipeline operating in isolated worktrees. The 56-file import migration (12 source + 44 tests) is treated as a single atomic task group. CI gating enforces correctness — partial migrations that break imports cause the task group to be retried or rejected. No additional rollback strategy is required beyond this execution model.

7. **`py.typed` marker included:** `afissues` ships a `py.typed` PEP 561 marker file so that mypy/pyright consumers resolve inline types correctly. This is especially important because `PlatformProtocol` is a `typing.Protocol` intended for structural subtyping by downstream forge implementations.

8. **Version aligned with workspace:** `afissues` starts at version 4.2.0 (current `agentfox` version), using hatchling as build backend — consistent with `afaudit` and workspace convention. Future versioning follows the same release cadence as the other workspace packages.

9. **`nightshift` uses transitive dependency only:** Grep confirms `nightshift` does not import any `agentfox.platform.*` symbols directly. It therefore does not declare `afissues` as an explicit dependency — transitive resolution through `agentfox` is sufficient.

10. **`af` uses transitive dependency only:** The `packages/af` package declares `agentfox` as a dependency; `afissues` is available transitively. The `af/pyproject.toml` does not need to be updated. The one affected test file (`test_init_labels.py`) can resolve `afissues.labels` without any manifest change.

11. **Hypothesis database not relocated:** The `.hypothesis/` directory is listed in `.gitignore` and is never checked in. Relocating `test_overhaul_props.py` to `packages/afissues/tests/property/` requires no database handling — Hypothesis will build a fresh example database at the new location on first run with no behavioral impact.

12. **Import-isolation enforcement deferred:** A CI lint rule to enforce that `afissues` never imports from other workspace packages is explicitly deferred to a follow-on hardening spec. The `pyproject.toml` dependency declaration (listing only `httpx`) is the isolation guarantee for this iteration.
