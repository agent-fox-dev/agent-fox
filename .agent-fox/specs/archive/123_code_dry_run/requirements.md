# Requirements Document

## Introduction

This specification defines the `--dry-run` flag for the `agent-fox code`
command. The flag loads the persisted plan from DuckDB (read-only), computes
parallelism phases, critical path, and dependency edges, and displays the
analysis without starting the orchestrator or dispatching any coding sessions.

## Glossary

- **Persisted plan**: The TaskGraph stored in DuckDB by a prior
  `agent-fox plan` invocation, consisting of nodes, edges, and metadata in the
  `plan_nodes`, `plan_edges`, and `plan_meta` tables.
- **Orchestrator**: The engine component (`engine.engine.Orchestrator`) that
  dispatches coding sessions to Claude agents for each ready task in the plan.
- **Infrastructure setup**: The process of creating knowledge DB connections,
  sink dispatchers, session runner factories, platform instances, and workspace
  health checks that the orchestrator requires before dispatching sessions.
- **Parallelism phase**: A maximal set of task graph nodes that can execute
  concurrently (see spec 122 glossary).
- **Critical path**: The longest dependency chain by node count from any source
  to any sink node in the task graph DAG (see spec 122 glossary).
- **Completed node**: A node whose status is `NodeStatus.COMPLETED` in the
  persisted plan.
- **Execution flag**: Any CLI flag that controls orchestrator behavior:
  `--watch`, `--debug`, `--force-clean`, `--parallel`.

## Requirements

### Requirement 1: Dry-Run Skips Orchestrator

**User Story:** As a user, I want to preview what `code` would work on without
actually running any coding sessions, so that I can verify the plan state
before committing compute time.

#### Acceptance Criteria

1. [123-REQ-1.1] WHEN the user runs `code --dry-run`, THE system SHALL load
   the persisted plan from DuckDB (read-only), compute analysis (phases,
   critical path, grouped edges), and display the analysis using
   `format_plan_analysis()` AND return exit code 0.

2. [123-REQ-1.2] WHEN the user runs `code --dry-run`, THE system SHALL NOT
   invoke `run_code()`, construct an `Orchestrator`, set up infrastructure
   (sinks, session runners, platform connections), or perform workspace
   health checks.

3. [123-REQ-1.3] WHEN the user runs `code --dry-run`, THE system SHALL
   filter out all nodes with status COMPLETED from the analysis output
   (phases, critical path, edges) so that only remaining work is displayed.

4. [123-REQ-1.4] WHEN the user runs `code` without `--dry-run`, THE system
   SHALL behave identically to the current implementation (existing behavior
   unchanged).

#### Edge Cases

1. [123-REQ-1.E1] IF the knowledge store database file does not exist when
   `--dry-run` is used, THEN THE system SHALL display an error message
   mentioning `plan` and exit with code 1.

2. [123-REQ-1.E2] IF the persisted plan is empty (no nodes) when `--dry-run`
   is used, THEN THE system SHALL display "No tasks in plan." and exit with
   code 0.

3. [123-REQ-1.E3] IF all nodes in the persisted plan are COMPLETED when
   `--dry-run` is used, THEN THE system SHALL display "All tasks completed."
   and exit with code 0.

### Requirement 2: Mutual Exclusion with Execution Flags

**User Story:** As a user, I want clear feedback when I accidentally combine
`--dry-run` with execution flags, so that I do not mistakenly think the
orchestrator ran.

#### Acceptance Criteria

1. [123-REQ-2.1] WHEN the user runs `code --dry-run` combined with any of
   `--watch`, `--debug`, `--force-clean`, or `--parallel`, THE system SHALL
   display an error message listing the incompatible flag(s) and exit with
   code 1 without loading the plan or starting the orchestrator.

#### Edge Cases

1. [123-REQ-2.E1] IF the user combines `--dry-run` with multiple execution
   flags (e.g. `--dry-run --watch --debug`), THEN THE system SHALL list all
   incompatible flags in the error message.

### Requirement 3: JSON Output

**User Story:** As a script or agent consumer, I want structured JSON output
from `code --dry-run`, so that I can programmatically inspect the plan state.

#### Acceptance Criteria

1. [123-REQ-3.1] WHEN the user runs `code --dry-run` with `--json` (global
   flag), THE system SHALL output a JSON object containing keys `nodes`,
   `edges`, `order`, `metadata`, `phases`, `critical_path`, and
   `grouped_edges` AND return exit code 0.

#### Edge Cases

1. [123-REQ-3.E1] IF the plan is empty or all nodes are completed when
   `--dry-run --json` is used, THEN THE system SHALL output a JSON object
   with empty `nodes`, `edges`, `order` fields and exit with code 0.

### Requirement 4: Daemon Guard

**User Story:** As a user, I want `code --dry-run` to be usable even when the
night-shift daemon is running, since it is a read-only operation.

#### Acceptance Criteria

1. [123-REQ-4.1] WHEN the user runs `code --dry-run` while the night-shift
   daemon is active, THE system SHALL skip the daemon PID guard and proceed
   with the dry-run analysis, since no writes or orchestration occur.

2. [123-REQ-4.2] WHEN the user runs `code` without `--dry-run` while the
   night-shift daemon is active, THE system SHALL refuse to run with the
   existing error message (existing behavior unchanged).
