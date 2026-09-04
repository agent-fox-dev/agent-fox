# ADR 01: Make the Integration Branch Configurable

## Status

Accepted

## Context

af previously hardcoded `"develop"` as the integration branch
throughout the codebase, coupling the tool to a git-flow branching
strategy.  Many projects use `main` as their sole long-lived branch
and do not want or need a separate `develop` branch.

## Decision

Introduce a `workspace.integration_branch` configuration field
(default: `"main"`) in `config.toml`.  Rename the internal
`workspace/develop.py` module to `workspace/integration.py` and
parameterize all functions that previously hardcoded the branch name.
The configured value is threaded through the engine layer via the
existing `AgentFoxConfig` object.

## Consequences

- **Default change:** new projects default to `main`.  Existing
  projects using `develop` must add `integration_branch = "develop"`
  to their `[workspace]` section in `config.toml`.
- **Module rename:** `workspace/develop.py` becomes
  `workspace/integration.py`; function names change accordingly
  (e.g. `ensure_develop` to `ensure_integration_branch`).
- **Audit event types unchanged:** `DEVELOP_SYNC`,
  `DEVELOP_FETCH_FAILED`, and `DEVELOP_SYNC_FAILED` retain their
  enum names to avoid breaking audit log consumers.
- **Agent prompts:** static templates use the generic term
  "integration branch" rather than naming a specific branch.
