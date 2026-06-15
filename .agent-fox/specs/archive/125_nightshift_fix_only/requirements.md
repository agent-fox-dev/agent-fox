# Requirements Document

## Introduction

Strip the `night-shift` command down to fix-only mode by removing the
hunt-scan stream, the spec-executor stream, and all supporting modules,
tests, config fields, and documentation.

## Glossary

- **night-shift**: The autonomous maintenance daemon CLI command
  (`agent-fox night-shift`).
- **work stream**: An independent polling loop within the daemon framework
  (fix-pipeline, hunt-scan, spec-executor).
- **fix-pipeline stream**: The stream that polls for `af:fix`-labelled issues
  and processes them through triage → coder → reviewer.
- **hunt-scan stream**: The stream that scans the codebase for maintenance
  issues and creates GitHub issues (being removed).
- **spec-executor stream**: The stream that discovers and executes new specs
  from `.agent-fox/specs/` (being removed).
- **hunt modules**: Source modules under `agent_fox/nightshift/` that
  exclusively support the hunt-scan stream: `hunt.py`, `critic.py`,
  `dedup.py`, `finding.py`, `ignore_filter.py`, `ignore.py`, and the
  `categories/` directory.
- **NightShiftConfig**: Pydantic config model for night-shift settings.

## Requirements

### Requirement 1: Remove hunt-scan source modules

**User Story:** As a maintainer, I want hunt-related dead code removed so
that the codebase is smaller and easier to understand.

#### Acceptance Criteria

1. [125-REQ-1.1] WHEN the repository is checked, THE system SHALL NOT
   contain any of the following source files: `agent_fox/nightshift/hunt.py`,
   `agent_fox/nightshift/critic.py`, `agent_fox/nightshift/dedup.py`,
   `agent_fox/nightshift/finding.py`, `agent_fox/nightshift/ignore_filter.py`,
   `agent_fox/nightshift/ignore.py`, `agent_fox/nightshift/categories/__init__.py`,
   `agent_fox/nightshift/categories/base.py`,
   `agent_fox/nightshift/categories/builtins.py`,
   `agent_fox/nightshift/categories/quality_gate.py`.

2. [125-REQ-1.2] WHEN the repository is checked, THE system SHALL NOT
   contain the directory `agent_fox/nightshift/categories/`.

3. [125-REQ-1.3] THE remaining source files in `agent_fox/nightshift/` SHALL
   NOT import from any deleted module.

#### Edge Cases

1. [125-REQ-1.E1] IF any non-nightshift source file imports from a deleted
   module, THEN THE import SHALL be removed or replaced.

### Requirement 2: Remove hunt-scan engine methods

**User Story:** As a maintainer, I want the engine to only contain fix-pipeline
logic so that the engine's responsibility is clear.

#### Acceptance Criteria

1. [125-REQ-2.1] THE `NightShiftEngine` class SHALL NOT contain
   `_run_hunt_scan()` or `_run_hunt_scan_inner()` methods.

2. [125-REQ-2.2] THE `NightShiftEngine.__init__()` SHALL NOT accept
   `auto_fix` or `embedder` parameters.

3. [125-REQ-2.3] THE `NightShiftEngine` class SHALL NOT contain a
   `_hunt_scan_in_progress` attribute.

4. [125-REQ-2.4] THE `NightShiftEngine` class SHALL retain `_drain_issues()`,
   `_run_issue_check()`, and `_process_fix()` methods with unchanged behavior.

### Requirement 3: Remove spec-executor stream

**User Story:** As a maintainer, I want the spec-executor stream removed so
that night-shift only processes fixes.

#### Acceptance Criteria

1. [125-REQ-3.1] THE `SpecExecutorStream` class SHALL be deleted from
   `agent_fox/nightshift/streams.py`.

2. [125-REQ-3.2] THE `build_streams()` function SHALL NOT accept
   `no_specs`, `no_hunts`, `auto`, `discover_fn`, or `orch_factory`
   parameters.

3. [125-REQ-3.3] WHEN `build_streams()` is called, THE function SHALL
   return a list containing exactly one `WorkStream` — the fix-pipeline
   stream — AND return that list to the caller.

4. [125-REQ-3.4] THE `build_streams()` function SHALL retain the
   `no_fixes` parameter for symmetry with the `--no-fixes` flag, and
   disable the fix-pipeline stream when `no_fixes=True`.

#### Edge Cases

1. [125-REQ-3.E1] IF `build_streams()` is called with `no_fixes=True`,
   THEN THE returned list SHALL contain one stream with `enabled=False`.

### Requirement 4: Simplify CLI flags

**User Story:** As a user, I want the `night-shift` command to have fewer
flags since it only processes fixes.

#### Acceptance Criteria

1. [125-REQ-4.1] THE `night-shift` command SHALL NOT accept `--auto`,
   `--no-specs`, `--no-hunts`, or `--specs-dir` options.

2. [125-REQ-4.2] THE `night-shift` command SHALL retain the `--no-fixes`
   option for diagnostic use (run daemon without processing fixes).

3. [125-REQ-4.3] THE `_SpecBatchRunner` class SHALL be deleted from
   `agent_fox/cli/nightshift.py`.

4. [125-REQ-4.4] THE CLI SHALL NOT set up spec discovery closures,
   orchestrator factories, or spec-related imports.

### Requirement 5: Remove unused config fields

**User Story:** As a maintainer, I want unused config fields removed so
that the config model reflects actual behavior.

#### Acceptance Criteria

1. [125-REQ-5.1] THE `NightShiftConfig` model SHALL NOT contain fields
   `hunt_scan_interval`, `categories`, `quality_gate_timeout`,
   `spec_interval`, `enabled_streams`, or `similarity_threshold`.

2. [125-REQ-5.2] THE `NightShiftCategoryConfig` class SHALL be deleted.

3. [125-REQ-5.3] THE `NightShiftConfig` model SHALL retain fields
   `issue_check_interval` and `push_fix_branch` with unchanged defaults
   and behavior.

4. [125-REQ-5.4] WHEN an existing config file contains removed fields,
   THE system SHALL silently ignore them without error (via Pydantic
   `extra="ignore"`).

#### Edge Cases

1. [125-REQ-5.E1] IF a validator references a removed field, THEN THE
   validator SHALL also be removed.

### Requirement 6: Clean up init_project

**User Story:** As a maintainer, I want the `.night-shift` ignore-file
seed removed from project initialization since hunt scans no longer exist.

#### Acceptance Criteria

1. [125-REQ-6.1] THE `init_project` module SHALL NOT import from
   `agent_fox.nightshift.ignore`.

2. [125-REQ-6.2] THE `init_project` module SHALL NOT create a
   `.night-shift` file during project initialization.

### Requirement 7: Delete hunt-related tests

**User Story:** As a maintainer, I want tests for deleted modules removed
so that the test suite is clean.

#### Acceptance Criteria

1. [125-REQ-7.1] THE following test files SHALL be deleted:
   `tests/unit/nightshift/test_critic.py`,
   `tests/unit/nightshift/test_dedup.py`,
   `tests/unit/nightshift/test_finding.py`,
   `tests/unit/nightshift/test_hunt.py`,
   `tests/unit/nightshift/test_quality_gate.py`,
   `tests/unit/test_nightshift_ignore.py`,
   `tests/integration/nightshift/test_critic.py`,
   `tests/integration/nightshift/test_dedup.py`,
   `tests/integration/nightshift/test_hunt_scan.py`,
   `tests/integration/test_nightshift_ignore_smoke.py`,
   `tests/property/nightshift/test_critic_props.py`,
   `tests/property/nightshift/test_dedup_props.py`,
   `tests/property/nightshift/test_quality_gate_props.py`,
   `tests/property/test_nightshift_ignore_props.py`,
   `tests/test_critic_false_positives.py`,
   `tests/test_hunt_dedup_similarity.py`,
   `tests/test_ignore_filter.py`.

2. [125-REQ-7.2] THE remaining test files SHALL NOT import from any deleted
   source module.

3. [125-REQ-7.3] WHEN `make test` is run, THE test suite SHALL pass with
   zero failures and zero collection errors.

### Requirement 8: Update documentation

**User Story:** As a user, I want the documentation to reflect the current
fix-only behavior of night-shift.

#### Acceptance Criteria

1. [125-REQ-8.1] THE architecture document `docs/architecture/04-night-shift.md`
   SHALL describe night-shift as a fix-only daemon and SHALL NOT reference
   hunt scans, hunt categories, the critic, dedup, or the spec-executor stream.

2. [125-REQ-8.2] THE CLI reference `docs/cli-reference.md` SHALL NOT list
   `--auto`, `--no-specs`, `--no-hunts`, or `--specs-dir` as options for
   the `night-shift` command.

3. [125-REQ-8.3] THE config reference `docs/config-reference.md` SHALL NOT
   document removed `NightShiftConfig` fields.

4. [125-REQ-8.4] THE project root `README.md` SHALL describe night-shift
   as a fix-only daemon and SHALL NOT reference hunt scans, hunt categories,
   the spec-executor stream, or the `.night-shift` ignore file.

5. [125-REQ-8.5] THE following documentation files SHALL be updated to
   remove or correct references to hunt scans, hunt categories, the critic,
   dedup, the spec-executor stream, and the `.night-shift` ignore file:
   `docs/README.md`, `docs/architecture.md`, `docs/architecture/README.md`,
   `docs/architecture/prd.md`, `docs/architecture/02-planning.md`,
   `docs/architecture/03-execution-and-archetypes.md`,
   `docs/architecture/05-knowledge-system-architecture.md`.

6. [125-REQ-8.6] ADRs (`docs/adr/`), audit reports (`docs/audits/`), and
   errata (`docs/errata/`) SHALL NOT be modified — these are historical
   records.
