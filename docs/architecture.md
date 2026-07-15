# Coding Session Architecture

How agent-fox turns a collection of specifications into committed code through
autonomous, parallel, knowledge-accumulating coding sessions.

This document is a top-down walkthrough — from the persistent state in the
database, through the orchestrator's dispatch loop, through each agent
archetype's role, through workspace isolation and merge integration, and down
to how knowledge is harvested and fed forward to future sessions.

---

## 1. Foundational Concepts

### 1.1 The Contract Model

Everything agent-fox does traces back to a **spec** — a set of structured
artifacts that together define a coherent unit of work. Specs use the v1.3
JSON-based format. See
[Part 6: Spec Format v1.3](architecture/06-spec-format-v13.md) for details.

| Artifact | Role |
|---|---|
| `prd.md` | Why the feature exists; inter-spec dependency declarations |
| `requirements.json` | Testable acceptance criteria in EARS syntax (`NN-REQ-M.S` identifiers) |
| `architecture.md` (optional) | Interfaces, data models, correctness properties, definition of done |
| `test_spec.json` | Language-agnostic test contracts keyed by `TS-NN-N` identifiers |
| `tasks.json` | Sequenced task groups; the planner's primary input |

A spec is not documentation written after the fact. It is the input artifact
that drives every downstream decision: which agents run, in what order, with
what constraints, and with what success criteria.

### 1.2 The Three Pillars

Three concerns are explicitly kept separate:

- **The orchestrator** is entirely deterministic — zero LLM calls. It manages
  the task graph, dispatches sessions, handles retries, and persists state. It
  never decides what code to write.

- **Sessions** are the LLM-powered work units. Each session runs a Claude
  agent inside an isolated workspace for a single task group. The agent reads
  files, writes code, runs commands, and commits results.

- **The knowledge system** is the institutional memory. It accumulates
  review findings and session summaries across sessions and injects relevant
  context into future sessions for the same or related specs.

---

## 2. Persistent State in DuckDB

All system state lives in a single DuckDB database
(`.agent-fox/knowledge.duckdb`). There is no external server; the database is
an embedded file opened by the orchestrator process.

### 2.1 Plan State

The task graph is stored across three tables:

- **`plan_nodes`** — One row per task group node. Fields: node ID
  (`spec_name:group_number`), spec name, group number, title, status, archetype,
  mode, instance count, optional flag, subtask count, body text.
- **`plan_edges`** — Directed dependency edges: `(from_node, to_node,
  edge_type)`. Edge type is either `intra_spec` (sequential groups within a spec) or
  `cross_spec` (dependency declared in `prd.md`).
- **`plan_meta`** — Content hash of the plan, version, fast-mode flag, and
  optional spec filter.

The plan is rebuilt deterministically from `.agent-fox/specs/` on every
`af plan` invocation and written atomically to DuckDB. The orchestrator
loads it at startup.

Node status is one of: `pending`, `in_progress`, `completed`, `failed`,
`blocked`, `skipped`, `cost_blocked`, `merge_blocked`, `deferred`. The
orchestrator updates status after every session completes and writes it back
to DuckDB.

### 2.2 Execution State

Two tables track runtime history:

- **`session_outcomes`** — One row per session attempt: node ID, spec name,
  status, model used, input/output tokens, cost, duration, touched file paths,
  commit SHA, attempt number, archetype, error message, and transport error
  flag.
- **`runs`** — One row per orchestrator run: run ID, plan content hash, start
  time, end time, final status, aggregate token counts and cost.

### 2.3 Quality Assurance State

Two tables store the outputs of review and verification agents:

- **`review_findings`** — Findings from pre-review and audit-review sessions,
  classified by severity (`critical`, `major`, `minor`, `observation`). Only
  `critical` and `major` findings are persisted; `minor` and `observation`
  findings are dropped at write time. Each finding has provenance (spec, task
  group, session) and a `superseded_by` column so resolved findings can be
  retired without deletion.
- **`drift_findings`** — Spec-to-code discrepancies detected by drift-review.
  Structured identically to review_findings with additional spec and artifact
  reference fields.

### 2.4 Knowledge State

Three tables store the institutional memory:

- **`session_summaries`** — Append-only log of session summaries. Each record
  carries the spec name, task group, archetype, attempt number, run ID, and a
  free-text summary extracted from the agent's session output. Used to provide
  cross-session context: later sessions in the same spec receive summaries from
  earlier sessions.
- **`finding_injections`** — Tracks which review findings were injected into
  which sessions. When a session completes successfully, all findings that were
  injected into it are automatically superseded — the assumption being the coder
  addressed them. This table enables the closed-loop finding lifecycle.
- **`audit_events`** — Structured log of every significant operation: run
  start, session start/complete/fail, git merge, config reload, preflight
  skip. Each event has a type, severity, run ID, node ID, and a JSON payload.

### 2.5 Telemetry State

Two tables record tool-level telemetry:

- **`tool_calls`** — One row per tool invocation during a session: session ID,
  node ID, tool name, timestamp.
- **`tool_errors`** — Failed tool invocations: session ID, node ID, tool name,
  timestamp.

### 2.6 Schema Evolution

The database uses forward-only migrations applied at open time. Each migration
is versioned and idempotent. A `schema_version` table tracks which migrations
have been applied. The migration system ensures databases created by earlier
versions of agent-fox are brought up to date transparently.

---

## 3. From Specs to Task Graph

### 3.1 Discovery and Validation

Before any planning occurs, the system discovers specs by scanning
`.agent-fox/specs/` for directories matching the `NN_name` pattern. Each
discovered spec becomes a `SpecInfo` record.

Validation delegates to `afspec.validate()`, which runs JSON Schema validation
and cross-file integrity checks:
- Structural checks (all required artifacts present)
- EARS syntax on requirements
- Traceability chain integrity (every requirement traced through test spec and
  tasks; every test entry traced back to a requirement)
- Inter-spec dependency validity (referenced specs exist, referenced group
  numbers exist, no cycles)

Findings are severity-classified as error (blocks planning), warning (quality
gap), or hint (stylistic).

### 3.2 Graph Construction — Four Phases

Given validated specs, the planner builds a directed acyclic graph in four
phases.

**Phase 1: Base Nodes and Intra-Spec Edges.** For each spec with a tasks file,
the planner parses task groups from `tasks.json` via `afspec` and creates one
`Node` per group. Consecutive groups within a spec get intra-spec
edges: group N → group N+1. Groups already marked complete start as
`COMPLETED`. The node ID format is `spec_name:group_number`.

**Phase 2: Archetype Injection.** The planner inserts non-coder agent nodes at
three injection points in each spec's chain:

- **`auto_pre`** (before group 1): Reviewer in `pre-review` mode (spec quality
  review) and `drift-review` mode (spec-vs-codebase drift detection). The
  drift-review node is only injected if the spec references files that already
  exist on disk — there is nothing to drift-check against for brand-new specs.
- **`auto_mid`** (after test-writing groups): Reviewer in `audit-review` mode
  (test coverage validation against test spec contracts). Only injected
  if the test spec has at least a configurable minimum number of entries.
- **`auto_post`** (after the last coder group): Verifier (runs tests, checks
  requirements against implementation).

**Phase 3: Archetype Tag Overrides.** If a task group carries an `[archetype:
<name>]` tag in `tasks.json`, that group's node is reassigned from the default
`coder` archetype to the tagged archetype. This takes highest priority over
both injection rules and defaults.

**Phase 4: Inter-Spec Edges.** Dependencies declared in `prd.md` create edges
between groups in different specs. The planner validates that both endpoints
exist. Spec-level dependencies (coarse format) resolve to the last group of the
referenced spec; group-level dependencies (fine format) create edges between
precise groups. Inter-spec edges are propagated to any auto_pre review nodes
that gate the target — with the exception of `pre-review` nodes, which validate
spec content rather than upstream implementation and can therefore run early.

### 3.3 Execution Order

Kahn's topological sort produces the execution order. Ties among nodes with
zero in-degree are broken deterministically by `(spec_name, group_number)` —
alphabetical by spec, ascending by group. This makes the execution order fully
reproducible across runs from the same specs.

The sorted order, along with nodes and edges, is persisted to DuckDB. The
orchestrator loads this complete structure at startup.

### 3.4 Runtime Graph Patching

The plan is not entirely static. Two mechanisms modify it at runtime:

**Archetype injection patching.** If the loaded plan was built with a different
archetype configuration than the current one (e.g., audit-review was disabled
when the plan was built but is now enabled), the orchestrator injects the
missing nodes into the in-memory graph at startup and persists the updated plan.

**Hot-load discovery.** During sync barriers, the orchestrator checks for new
specs that have appeared since the plan was built. A spec passes four gates
before admission: git-tracked on the integration branch, all required artifacts
present and non-empty, passes static lint validation, and not fully
implemented. Admitted specs are merged into the live graph — new nodes, new
edges, archetype injection — and the `GraphSync` state machine is rebuilt.

---

## 4. The Orchestrator's Dispatch Loop

The orchestrator is a pure state machine with zero LLM calls. It delegates to
three collaborators:

- **StateManager** — state loading, initialization, persistence, node status
  tracking, cascade blocking.
- **DispatchManager** — session runners, dispatchers, launch preparation,
  preflight checks.
- **ConfigReloader** — configuration hot-reload from disk.

### 4.1 Initialization

Before entering the main loop, the orchestrator performs startup:

1. **Generate a run ID** for audit trail correlation.
2. **Load the task graph** from DuckDB. If no plan exists, error out.
3. **Inject missing archetype nodes** if the current archetype configuration
   differs from the plan's configuration.
4. **Load or initialize execution state.** If prior state exists in DuckDB:
   - Same plan hash: reuse state, adding any new nodes as `pending`.
   - Different plan hash: merge — carry forward `completed`/`skipped` statuses
     for nodes that still exist; reset everything else to `pending`.
   - No prior state: seed entirely from the task graph.
5. **Reset stale statuses.** Any `in_progress` nodes (from a prior crash) are
   reset to `pending`. Blocked nodes are also reset for fresh retry attempts.
6. **Build the GraphSync** state machine from node states and edge adjacency.
7. **Defer ready reviews.** Non-auto_pre review nodes whose predecessors are
   already completed are set to `deferred` — they run after coder work, not
   before.
8. **Initialize the result handler** with retry configuration and timeout
   tracking.

### 4.2 The Main Loop

Each iteration:

1. **Check interruption.** If SIGINT was received, drain in-flight sessions and
   return.

2. **Check circuit breaker.** Three limits gate execution:
   - Cost ceiling: total accumulated LLM cost across all sessions.
   - Session ceiling: total session invocations (including retries).
   - Block budget: fraction of tasks that are blocked exceeds a threshold.
   If a limit is exceeded, stop dispatching and return with a status code.

3. **Identify ready tasks.** A task is ready when all its predecessor nodes are
   `completed` and its own status is `pending`. The `GraphSync` component
   maintains this set incrementally — when a node completes, it decrements
   in-degree counts for successors and surfaces any that become ready.

4. **Promote deferred reviews.** If no tasks are ready, deferred review nodes
   are promoted back to `pending` to fill idle slots.

5. **Filter file conflicts.** When parallel execution is enabled and multiple
   tasks are ready, the file conflict detector compares predicted file impacts
   (extracted from spec text). Conflicting pairs are serialized — only one of
   them dispatches in this iteration.

6. **Dispatch.** Available parallel slots are filled with ready tasks, each
   launching a session in an isolated worktree. See Section 4.4 for serial vs.
   parallel dispatch strategies.

7. **Process results.** Completed sessions return a `SessionRecord`. The result
   handler classifies the outcome (success, failure, timeout), applies retry
   logic, persists state, and updates the graph.

8. **Sync barrier.** At configurable intervals (every N completed tasks), the
   orchestrator pauses dispatch, verifies worktrees, pulls remote changes,
   checks for new specs, and reloads config.

9. **Stall detection.** If no tasks are ready and the graph is not fully
   completed, the orchestrator detects a stall (all remaining tasks are blocked
   by failed predecessors or circular dependencies).

10. **End-of-run discovery.** Before reporting completion, the orchestrator runs
    one final hot-load check for new specs. If new tasks are discovered, the
    loop continues.

11. **Watch mode.** If `--watch` is enabled, the orchestrator enters a polling
    loop after all tasks complete, checking for new specs at a configurable
    interval.

12. **Repeat** until all tasks complete, a circuit breaker trips, or SIGINT
    arrives.

### 4.3 Ready Task Ordering

When multiple tasks are simultaneously ready, the dispatcher uses a three-tier
priority system:

1. **Pre-review tier** — Group 0 nodes (pre-review, drift-review) go first,
   ordered by spec fan-out descending so critical-path blockers surface early.
2. **Coder tier** — Implementation tasks, ordered by spec-fair round-robin
   so no single spec dominates the pool.
3. **Review tier** — Non-pre review and verification tasks, also round-robin.

Within each tier, ties are broken by `(spec_name, group_number)` for full
determinism.

### 4.4 Dispatch Strategies

Two dispatch strategies are available:

**Serial dispatch** (`parallel=1`). The dispatcher picks the first
ready task, prepares launch parameters, marks it `in_progress`, executes the
session, processes the result, and loops. An inter-session delay can be
configured to avoid API rate limits.

**Parallel dispatch** (default, `parallel=2..8`). A streaming asyncio task pool
maintains up to N concurrent sessions. When a session completes and frees a
slot, the orchestrator immediately evaluates which tasks are now ready and
fills the slot. Review archetype sessions (other than auto_pre group-0 nodes)
are capped at a configurable fraction of the pool to prevent review tasks from
crowding out coder sessions.

Only tasks that are actually running carry the `in_progress` status. Tasks
that are ready but waiting for a pool slot remain `pending`.

### 4.5 Launch Preparation

Before dispatching a task, the `DispatchManager` runs a preparation pipeline:

1. **Workspace health check.** The repository is inspected for untracked files
   and dirty index state. If untracked files are detected in a coder session's
   predicted file impact zone, the task is skipped for this dispatch cycle.
   Known build artifacts (e.g. `__pycache__`, `.pytest_cache`) are
   auto-remediated.
2. **Circuit breaker check.** The per-task retry limit is evaluated. If
   exhausted, the task is blocked.
3. **Preflight check** (coder tasks only, first attempt). The preflight
   verifier checks whether all checkboxes in `tasks.json` are already marked
   complete, no active critical findings exist, and tests pass. If all three
   hold, the session is skipped — the work is already done. When the verdict
   is LAUNCH, the gate results are captured as a `PreflightResult` and
   forwarded to the coder's task prompt as a `## Preflight State` section,
   so the coder can skip its redundant Quick Triage checks.
4. **Parameter resolution.** The archetype's model tier and variant, security
   allowlist, session timeout, max turns, thinking configuration, and budget
   cap are resolved from the archetype registry, mode overrides, and
   configuration.

### 4.6 Signal Handling

The orchestrator installs a two-stage signal handler for both SIGINT and
SIGTERM. The first signal sets an `interrupted` flag: no new dispatches, but
in-flight sessions finish cleanly and their results are persisted. A second
signal cancels in-flight sessions and exits immediately. Signal handlers are
restored on exit so the parent process is unaffected.

---

## 5. Session Lifecycle

Each task node is executed as a session — a single invocation of a Claude agent
in an isolated workspace. The session lifecycle has four phases.

```
┌─────────────────────────────────────────────────────────────┐
│  SESSION LIFECYCLE                                          │
│                                                             │
│  1. PREPARE                                                 │
│     ensure_integration_branch → create_worktree → build_prompts        │
│                                                             │
│  2. EXECUTE                                                 │
│     run_session(system_prompt, task_prompt)                  │
│         → Claude SDK streams tool_use / assistant messages   │
│                                                             │
│  3. HARVEST                                                 │
│     acquire merge_lock → git merge --squash feature branch  │
│         → resolve conflicts via merge agent if needed       │
│         → push integration branch to origin (best-effort)              │
│                                                             │
│  4. ASSESS                                                  │
│     parse review findings → persist to DuckDB               │
│         → emit audit events                                 │
│         → record session outcome                            │
└─────────────────────────────────────────────────────────────┘
```

### 5.1 Prepare

**Workspace creation.** The system first ensures the integration branch exists
and is synchronized with the remote. It then creates an isolated git worktree at
`.agent-fox/worktrees/{spec_name}/{task_group}`, on a new feature branch named
`feature/{spec_name}/{task_group}`. A worktree is a fully checked-out working
copy of the repository at the current state of the integration branch, with its
own branch that can diverge independently. Multiple sessions can run in parallel
in separate worktrees without touching each other.

Worktree creation includes stale artifact cleanup: orphaned empty directories
from prior crashes are removed, stale branch references are pruned, and
conflicting branches are deleted along with their remote tracking branches.

**Prompt construction.** The system builds two prompts — a system prompt and a
task prompt — tailored to the session's archetype, mode, and task. The full
prompt construction process is described in Section 7.

### 5.2 Execute

The session is submitted to the Claude agent SDK. The SDK drives the agent
loop: the agent reads files, writes code, runs commands (from the archetype's
tool allowlist), and produces output over multiple turns.

Per-session SDK parameters are resolved before dispatch:

- **Model ID** — Determined by the archetype's default tier and variant, with
  optional per-archetype and per-mode config overrides. See
  [Model Tiers and Variants](model-escalation.md) for the resolution priority.
- **Max turns** — Archetype-specific cap on the number of agent turns (300 for
  Coder, 120 for Verifier, 80 for Reviewer).
- **Thinking mode** — Extended thinking with configurable token budget. Currently
  enabled only for the Coder archetype (`adaptive` mode, 64K budget).
- **Timeout** — Wall-clock limit in minutes (default: 45). Timeout failures
  receive specialized retry handling (see Section 6.2).
- **Budget cap** — Optional per-session USD ceiling. When hit, the SDK signals
  budget exhaustion and the orchestrator skips retries.
- **Fallback model** — Alternative model if the primary is unavailable.

The backend streams canonical message types — `ToolUseMessage`,
`AssistantMessage`, `ResultMessage` — to the sink dispatcher. Tool invocations
and errors are recorded for telemetry. Agent conversation traces (session init,
tool use, assistant messages, session result) are emitted as structured events
for later transcript reconstruction.

On completion, the SDK returns a `SessionOutcome` carrying status, token
counts (including cache read and creation), cost, duration, full response text,
and any error message.

### 5.3 Harvest

On successful session completion, the feature branch is integrated into
the integration branch. The harvest process:

1. **Check for new commits.** If the feature branch has no new commits relative
   to the integration branch, harvest is skipped — the session produced no code
   changes.

2. **Acquire the merge lock.** An asyncio lock (intra-process) combined with an
   atomic file lock (`O_CREAT | O_EXCL` with rename-based stale detection)
   serializes all merge operations. No two sessions can merge simultaneously.

3. **Clean working tree.** Any dirty tracked files from a prior failed harvest
   are reset. Untracked files that would conflict with the incoming merge are
   removed — but only if their on-disk content is identical to the feature
   branch version. Files with divergent content raise an error rather than
   being silently overwritten.

4. **Squash merge.** `git merge --squash feature/{spec}/{group}` collapses the
   feature branch into a single staged diff on the integration branch,
   regardless of how many commits the feature branch contains. This keeps the
   integration branch history linear.

5. **Conflict resolution.** If the squash merge has conflicts, a dedicated merge
   agent session is spawned — a restricted Claude session using the ADVANCED
   model tier whose only job is to resolve the conflicting hunks. If the merge
   agent also fails, the merge is aborted (`git reset --merge`) and the session
   is marked failed.

6. **Commit.** The squash commit is created on the integration branch with a
   message derived from the tip commit of the feature branch.

7. **Push.** The integration branch is pushed to origin with up to 3 retry
   attempts. On non-fast-forward rejection, the system attempts a
   reconciliation cascade: rebase onto origin, then merge commit if rebase
   conflicts, then AI merge agent as a last resort. Push failures are logged
   as warnings and do not fail the session.

8. **Worktree cleanup.** The feature branch worktree is always destroyed at the
   end of the session lifecycle, even if the session failed. The feature branch
   is deleted, its remote tracking branch is removed, and empty ancestor
   directories are cleaned up.

### 5.4 Assess

After harvest, the system processes the session's outputs.

**Review finding persistence.** For review archetypes (Reviewer in all modes,
Verifier), the session output is parsed for structured findings. Parsed
findings are written to the appropriate DuckDB quality tables
(`review_findings`, `drift_findings`).

**Multi-instance convergence.** When a node is configured with `instances > 1`,
multiple sessions run in parallel for the same task. Their outputs are merged
deterministically:

- **Reviewer convergence** (pre-review, drift-review): Union all findings,
  deduplicate by description similarity, majority-gate critical findings —
  a finding is only promoted to critical severity if a majority of instances
  flagged it as critical.
- **Verifier convergence**: Majority-vote each requirement result across
  instances. A requirement is FAIL only if a majority of instances failed it.
- **Auditor convergence** (audit-review): Worst-result-wins — if any instance
  flags an issue, the converged result includes it.

Convergence is entirely deterministic (no LLM calls). It ensures that
multi-instance results are consistent and that individual outlier sessions
do not produce spurious blocking findings.

**Session summary generation.** For reviewer and verifier sessions, a
structured summary is generated from the persisted findings and verification
results. This summary includes severity counts and lists specific requirement
IDs for failed checks.

**Knowledge ingestion.** For completed sessions, the `KnowledgeProvider`
ingests session context — superseding previously injected findings, performing
file-based drift finding supersession (for coder sessions), and storing session
summaries. See Section 10.3 for the full ingestion pipeline.

**File-based drift finding supersession.** For completed coder sessions
(not reviewer or verifier sessions), the system performs targeted retirement
of drift findings whose `artifact_ref` matches files modified by the session.
Exact path matching is used for file-level references; prefix matching is used
for directory references. Findings with a null `artifact_ref` are never
superseded by file matching — they persist until a future drift review replaces
them.

**Outcome recording.** The `SessionRecord` (node ID, attempt, status, token
counts, cost, duration, touched files, model, commit SHA, archetype) is
written to `session_outcomes` in DuckDB.

**Audit events.** `session.complete` or `session.fail` events are emitted to
the audit trail with full metadata including token counts, cost, duration,
and archetype.

---

## 6. Result Handling and Retry Logic

### 6.1 Success

The node is marked `completed` in the graph. Three post-success evaluations
may alter downstream behavior:

**Review blocking.** If the session was a review agent with blocking authority
(Reviewer in `pre-review` or `drift-review` mode), the result handler evaluates
whether the findings exceed the configured blocking threshold (default: any
critical finding). Critical security findings always block regardless of
threshold. If the threshold is exceeded, all downstream coder tasks for that
spec are set to `blocked`, and all their transitive dependents are
cascade-blocked via BFS.

**Retry-predecessor.** The `audit-review` and `verifier` archetypes both have
`retry_predecessor=true`. When their session completes with failing outputs
(MISSING tests, FAIL requirements), the orchestrator re-runs the preceding
coder session with the review/verification findings injected as context.
Audit-review findings that reference a future task group are excluded from
the blocking set to avoid unwinnable retry loops.

**Coverage regression detection.** Before a coder session, the result handler
captures a test coverage baseline. After the session completes, it measures
coverage again and compares per-file percentages. Coverage regressions are
reported as audit events for visibility.

### 6.2 Timeout Handling

Timeout failures receive specialized handling. Instead of immediately blocking
the task, the orchestrator extends the session parameters: max turns and
timeout are multiplied by a configurable factor (`timeout_multiplier`,
default 1.5) and clamped to a ceiling (`timeout_ceiling_factor`, default 2.0x
the original value). The task is reset to `pending` for retry.

Only after timeout retries are exhausted (`max_timeout_retries`, default 2)
does the task fall through to the normal failure path.

### 6.3 Failure and Retry

Non-timeout failures are classified and handled based on their nature:

**Transport errors** (network failures, API timeouts): The Claude backend
retries internally with exponential backoff. If the error surfaces to the
orchestrator, the task is reset to `pending` without consuming a retry — the
problem is infrastructure, not the task.

**Budget exhaustion** (session cost approaches per-session cap): The task is
blocked immediately with no retry — repeating the same work would burn the same
budget again.

**Workspace/environment failures** (worktree setup errors, zero-token sessions
with no prior failures): Exponential backoff (cap 30 seconds), block after 3
consecutive failures.

**Generic failures** (LLM errors, code failures): If the attempt count is
within the `max_retries` limit (default: 2), the task is reset to `pending`
for another attempt at the same model tier. If retries are exhausted, the task
is marked `blocked` and all transitive dependents are cascade-blocked.

On each retry, the previous error message is sanitized and appended to the task
prompt. Active critical and major review findings are also prepended so the
coder can address identified issues.

### 6.4 Budget Exhaustion

When a session's cost approaches the configured per-session budget cap (≥90% of
the limit) and the SDK reports a failure with no specific error message, the
orchestrator classifies this as budget exhaustion. These sessions are not
retried.

### 6.5 Transport Errors

Transient connection errors (network failures, API timeouts) are flagged on the
session outcome. The Claude backend retries transport errors internally with
exponential backoff (up to 3 retries) before surfacing the failure to the
orchestrator.

---

## 7. Prompt Construction

Each session receives a two-part prompt: a **system prompt** (establishes
context and behavioral guidance) and a **task prompt** (specifies what to do).
The system prompt is built from three layers; the task prompt is
archetype-specific.

### 7.1 Three-Layer System Prompt

```
Layer 1: Agent base profile  (agent.md)
   ↕ section break
Layer 2: Archetype profile   (e.g., coder.md, reviewer_pre-review.md)
   ↕ section break
Layer 3: Task context        (spec docs + knowledge + findings + steering)
```

**Layer 1 — Agent base profile.** The `agent.md` template provides instructions
shared by every agent regardless of archetype: acknowledgment of pre-injected
context (steering, spec artifacts, knowledge), lightweight orientation guidance,
external reference verification, directory structure conventions, git workflow
rules, scope discipline, and documentation conventions.

**Layer 2 — Archetype profile.** Loaded from a markdown profile file for the
session's archetype and mode. Profile resolution uses a four-step priority chain
where the first matching file wins:

1. Project-level mode-specific: `.agent-fox/profiles/{archetype}_{mode}.md`
2. Package-embedded mode-specific: `_templates/profiles/{archetype}_{mode}.md`
3. Project-level base: `.agent-fox/profiles/{archetype}.md`
4. Package-embedded base: `_templates/profiles/{archetype}.md`

Project-level profiles always override package defaults. Mode-specific profiles
always override base profiles. This allows projects to customize agent behavior
without modifying the package.

**Layer 3 — Task context.** Assembled from five sources in order:

1. **Spec documents.** Spec artifacts rendered as markdown section headers.
   JSON artifacts are loaded via `afspec` and rendered to markdown;
   `architecture.md` is read directly. Missing files are logged as warnings
   but do not prevent the session. When a `task_group` is provided, rendering
   is **scoped**: only requirements and test cases referenced by the active
   group's subtasks (via `requirement_refs` and `test_spec_refs`) are rendered
   in full. Non-target task groups appear as one-line summaries with completion
   counts. A `## Spec Overview` section always lists all requirement IDs and
   titles for orientation. The `## Test Commands` section is always included.
   Falls back to unscoped (full) rendering when the target group's subtasks
   have no refs.

2. **DB-backed findings.** Review findings and drift findings are queried from
   DuckDB and rendered as structured markdown (grouped by severity). This gives
   the agent visibility into prior quality assessments on this spec.

3. **Steering directives.** Project-wide guidance from `.agent-fox/steering.md`
   is included after spec files.

4. **Knowledge facts.** Relevant context from the knowledge store is retrieved
   by the `KnowledgeProvider` and injected. See Section 7.2 for how retrieval
   works.

5. **Prior group findings.** For task groups beyond group 1, active findings
   from earlier groups in the same spec (reviews, drift, verifications) are
   appended as accumulated context. This gives later groups visibility into
   issues found during earlier implementation work.

All external content passes through a sanitization layer that labels each source
and filters patterns that could constitute prompt injection attacks.

### 7.2 Knowledge Retrieval

Before each session, the `KnowledgeProvider` is queried with the spec name, a
task description derived from the subtask list, the task group number, and a
session ID. The concrete implementation (`FoxKnowledgeProvider`) retrieves
three categories of knowledge, each formatted with a distinct prefix for
prompt traceability:

| Category | Source | Prefix | Scope |
|---|---|---|---|
| **Review findings** | `review_findings` table | `[REVIEW]` | Active critical/major for this spec and task group |
| **Cross-group reviews** | `review_findings` table | `[CROSS-GROUP]` | Critical/major findings from other task groups in the same spec |
| **Same-spec context summaries** | `session_summaries` table | `[CONTEXT]` | Summaries from earlier sessions on the same spec (current run) |

**Relevance scoring.** Within each category, results are sorted by keyword
overlap between the task description and the finding/summary text. This
ensures the most relevant items appear first when results are capped.

**Injection tracking.** When a `session_id` is provided, the IDs of all
injected review findings are recorded in the `finding_injections` table. This
enables the supersession lifecycle: when the session completes, injected
findings are automatically retired (Section 10.3). Cross-group items are
informational and are not tracked in the injection table.

**Capping and degradation.** Results are capped at a configurable maximum
(`max_items` for primary categories, separate cap for cross-group). If any
table does not exist (fresh database) or any query fails, that category is
silently skipped — the session proceeds with whatever knowledge is available.

### 7.3 Task Prompt

The task prompt is short and archetype-specific:

- **Coder sessions** receive explicit instructions: implement task group N from
  specification X, update checkbox states in `tasks.json`, commit with
  conventional messages, and run quality gates before finalizing. When the
  orchestrator's preflight check ran before launch, a `## Preflight State`
  section is appended with the gate results (checkbox completion, active
  findings, test baseline), allowing the coder to skip its redundant Quick
  Triage checks. For retry attempts, the previous error and active
  critical/major findings are prepended.

- **Review/verification sessions** receive a concise prompt that defers to the
  archetype profile for detailed instructions, since the profile fully specifies
  the review process and expected output format.

---

## 8. Agent Archetypes in Detail

The archetype registry defines six built-in entries. Several support
**modes** — named variants that override specific fields (injection point,
tool allowlist, model tier, effort, max turns) while inheriting everything
else from the base entry.

Custom archetypes are supported via project-level profile files at
`.agent-fox/profiles/{name}.md` combined with a permission preset in the
configuration that references a built-in archetype for tool access rules.

### 8.1 Coder

**Role:** Primary implementation agent. Writes code and tests, runs quality
gates, commits with conventional messages.

**Execution model:** One task group per session. By convention, task group 1
writes failing tests from `test_spec.json` without implementing production code.
Subsequent groups implement code to make those tests pass. The Coder runs
quality checks, verifies its work against the requirements, and produces a
session summary artifact.

**Configuration:**
- Model tier: STANDARD (variant: standard)
- Thinking mode: adaptive
- Effort: xhigh
- Max turns: 300
- Tool access: unrestricted (inherits from global security config)
- Injection: none (assigned to task groups directly)

**Modes:** `fix` — used by the night-shift fix pipeline for targeted fixes on
specific issues. Same max turns and thinking configuration as the base.

### 8.2 Reviewer

**Role:** Quality gate agent with four modes covering distinct review roles.
All modes produce structured findings and have restricted tool allowlists
(they cannot modify code).

**Pre-review mode** (`auto_pre`, before group 1):
- Model tier: ADVANCED (variant: standard)
- Examines spec quality: completeness, consistency, feasibility, testability,
  edge case coverage, security.
- Has no shell access — works entirely from spec documents in context.
- If critical findings exceed a configured threshold, downstream coder tasks
  are blocked.
- `retry_predecessor`: true — can trigger re-runs of prior work.

**Drift-review mode** (`auto_pre`, before group 1, parallel with pre-review):
- Model tier: STANDARD (variant: standard)
- Validates spec assumptions against the actual codebase: referenced files
  exist, function signatures match, interfaces are consistent.
- Has read-only filesystem access (`ls`, `cat`, `git`, `grep`, `find`, `head`,
  `tail`, `wc`).
- Automatically skipped for specs that reference no existing code.

**Audit-review mode** (`auto_mid`, after test-writing groups):
- Model tier: ADVANCED (variant: standard)
- Validates test quality against test spec contracts: coverage, assertion
  strength, precondition fidelity, edge case rigor, test independence.
- Has read-only access plus `uv` for test collection.
- `retry_predecessor`: true — triggers a coder retry when tests are MISSING or
  MISALIGNED.

**Fix-review mode** (used by night-shift):
- Model tier: ADVANCED (variant: standard)
- Broader tool access (`make`, `uv`).
- Max turns: 120 (higher than other reviewer modes).
- Not injected automatically into coding session plans.

When both pre-review and drift-review are enabled, they execute in parallel
before the first coder group. Either can produce blocking findings
independently.

### 8.3 Curator

**Role:** Post-implementation curation. Runs after the last coder group and
before the Verifier, forming a quality chain: last coder → Curator → Verifier.

**Injection:** `auto_post` (injection order 10) — added before the Verifier.

**Configuration:**
- Model tier: STANDARD (variant: standard)
- Effort: medium
- Max turns: 80
- Tool access: read-only plus `make`
- Instances: always clamped to 1
- Enabled by default (`archetypes.curator`)

### 8.4 Verifier

**Role:** Post-implementation verification. Runs the test suite, checks each
requirement against acceptance criteria, produces per-requirement pass/fail
assessments.

**Injection:** `auto_post` (injection order 20) — added after the Curator.
Uses a sentinel group number (0) with a 3-part node ID format
(`spec:0:verifier`) to avoid collisions with real coder group nodes.

**Configuration:**
- Model tier: STANDARD (variant: standard)
- Effort: high
- Max turns: 120
- Tool access: full (needs to run tests)
- `retry_predecessor`: true

### 8.5 Gate

**Role:** Lightweight checkpoint verification for mid-spec progress checks.
Not auto-injected — assigned automatically when a task group has
`kind: "checkpoint"` in `tasks.json`.

**Configuration:**
- Model tier: STANDARD (variant: standard)
- Effort: low
- Max turns: 30
- Thinking: disabled
- Task assignable: yes

### 8.6 Maintainer

**Role:** Internal archetype used exclusively by the night-shift daemon. Not
assignable to task groups in coding session plans (`task_assignable: false`).

**Modes:**
- `fix-triage` — Read-only triage of single issues. Model tier: STANDARD.
- `hunt` — Read-only batch analysis. Model tier: SIMPLE.
- `extraction` — No shell access; pure LLM analysis. Model tier: SIMPLE.

---

## 9. Worktree and Git Architecture

### 9.1 The Integration Branch

The configured integration branch (default: `main`) is the sole integration
target. All session work merges into it. Feature branches are local-only — they
are never pushed to the remote.

Before the first session, the orchestrator ensures the integration branch
exists locally. If not, it is created from the corresponding remote branch
(if available) or from the default branch.

### 9.2 Isolation via Worktrees

Each session gets its own `git worktree` at
`.agent-fox/worktrees/{spec_name}/{task_group}`. Worktrees share the same
object store and ref namespace as the main repository but have independent
working trees and HEAD pointers. This makes it safe to run multiple sessions
concurrently — each session operates on its own branch, in its own directory,
without any shared mutable state.

The feature branch name follows the pattern `feature/{spec_name}/{task_group}`.
The worktree is created from the current state of the integration branch, so
each session starts from the latest integrated work.

### 9.3 The Merge Lock

All merge operations are serialized by a two-layer lock:

- **Asyncio lock** — Prevents two concurrent sessions in the same process from
  merging simultaneously.
- **File lock** — Prevents multiple `agent-fox` processes targeting the same
  repository from merging simultaneously. Uses `O_CREAT | O_EXCL` for atomic
  creation; stale lock detection uses a rename-to-temp pattern safe against
  TOCTOU races.

The file lock has a configurable stale timeout (default: 1 hour). If a lock
file is older than the timeout, it is considered abandoned and broken. A
background heartbeat task updates the lock file's modification time
periodically while the lock is held, preventing live sessions from being
falsely timed out.

### 9.4 The Merge Agent

When a squash merge produces conflicts, a dedicated Claude session — the
**merge agent** — is spawned at the ADVANCED model tier. The merge agent's
prompt restricts it to resolving the specific conflicts presented, without
making any other changes. If the merge agent also fails, the merge is aborted
(`git reset --merge`) and the session record carries a failure status so the
orchestrator can retry.

### 9.5 Reset Operations

**Soft reset** resets failed, blocked, and in-progress task nodes to `pending`
without rolling back any code. Session history and attempt counters are
preserved. Appropriate after transient failures (network issues, API errors).

**Hard reset** resets tasks and rolls back the integration branch to the commit
before the earliest affected task, undoing all code changes since that point.
Both reset types can target a single spec or the entire plan.

---

## 10. The Knowledge System

The knowledge system provides institutional memory across coding sessions. It
operates through a protocol-based interface (`KnowledgeProvider`) with two
entry points: **retrieve** (pre-session) and **ingest** (post-session).

### 10.1 The KnowledgeProvider Protocol

The engine interacts with knowledge exclusively through the `KnowledgeProvider`
protocol — it never imports knowledge internals directly. This clean boundary
allows the knowledge implementation to evolve without affecting the engine.

The protocol defines two methods:

- `retrieve(spec_name, task_description, task_group?, session_id?) → list[str]`
  — Called before each session. Returns formatted text blocks for prompt
  injection. The optional `task_group` and `session_id` parameters enable
  scoped retrieval and injection tracking.
- `ingest(session_id, spec_name, context) → None` — Called after each
  successful session. Receives session metadata (touched files, commit SHA,
  session status).

A `NoOpKnowledgeProvider` is used as the default when no knowledge system is
configured.

### 10.2 Knowledge Retrieval

The concrete `FoxKnowledgeProvider` queries three sources when `retrieve` is
called. A full description of each category, including prefixes, scoping, and
capping behavior, is given in Section 7.2.

In summary, the retrieval pipeline aggregates:

1. **Same-group review findings** — Active critical/major review findings for
   the current task group. These are the highest-priority items: they represent
   known issues the coder must address.

2. **Cross-group review findings** — Findings from other task groups in the same
   spec. These provide awareness of issues in adjacent work without creating
   blocking obligations. Pre-review findings are excluded from cross-group
   results since they concern spec quality, not implementation.

3. **Same-spec context summaries** — Natural-language summaries from prior
   sessions on the same spec (what earlier groups did). These give the agent
   continuity of purpose across the session boundary.

All results are relevance-scored against the task description, capped per
category, and formatted with distinctive prefixes. Finding IDs are tracked for
the supersession lifecycle (Section 10.3).

### 10.3 Knowledge Ingestion

After each session, the `FoxKnowledgeProvider.ingest()` method performs up to
three actions:

**Injection-based finding supersession.** If the session completed
successfully, all review findings that were injected into this session (as
tracked in the `finding_injections` table) are automatically superseded. The
logic: if the coder saw the finding and completed the work, the finding is
considered addressed. Superseded findings remain in the database for audit
history but are excluded from active queries. This closes the
retrieve-inject-address-supersede feedback loop.

**File-based drift finding supersession.** For completed coder sessions only,
immediately after injection-based supersession, the system retires drift
findings whose `artifact_ref` matches any file the session modified. Exact
matching is used for file-level references and prefix matching for
directory-level references. Line-number suffixes are stripped and whitespace
is normalized before comparison. Findings with a null `artifact_ref` are never
superseded by this mechanism. This call is guarded so that any failure is
logged as a warning without affecting the session outcome. Reviewer and
verifier sessions do not trigger file-based drift supersession.

**Session summary storage.** Completed sessions that produced a non-empty
summary (either extracted from the agent's artifact or auto-generated from
review findings for reviewer/verifier archetypes) are stored in the
`session_summaries` table. Each summary record carries the spec name, task
group, archetype, attempt number, run ID, and creation timestamp. These
summaries are later retrieved to provide cross-session context within the
same spec.

### 10.4 Quality Assurance Knowledge

Beyond the `KnowledgeProvider`, the quality assurance layer contributes
knowledge through structured review persistence:

- **Review findings** from pre-review and audit-review sessions are parsed
  from the agent's structured output and stored in `review_findings`.
- **Drift findings** from drift-review sessions are stored in
  `drift_findings`.

These findings flow back into future sessions through three paths:
1. **Context assembly** — findings are rendered as structured markdown sections
   in the session's system prompt (Section 7.1, Layer 3).
2. **Retry context** — active critical/major findings are prepended to coder
   task prompts on retry attempts.
3. **Knowledge retrieval** — the `FoxKnowledgeProvider` queries active findings
   as part of the `retrieve` call, including cross-group items.

**Finding lifecycle.** Findings follow a closed-loop lifecycle with two
distinct supersession paths depending on finding type:

```
Created (by reviewer/drift-review session)
    ↓
Active (queryable by context assembly and knowledge retrieval)
    ↓
    ├── Path A: Injection-based (review_findings)
    │   Injected (tracked in finding_injections when served to a session)
    │       ↓
    │   Superseded (retired when the session that received them completes)
    │
    └── Path B: File-based (drift_findings)
        Matched (artifact_ref compared against session's touched_files)
            ↓
        Superseded (retired when a coder session touches the referenced file)
```

Superseded findings of both types remain in the database with a
`superseded_by` reference for audit history but are excluded from active
queries. This ensures that resolved issues do not permanently consume prompt
space.

### 10.5 The Audit Trail

Every significant operation emits a structured audit event to the
`SinkDispatcher`. Events carry a type, severity, run ID, node ID, archetype,
and a JSON payload. The sink dispatcher forwards events to all registered
sinks — the DuckDB sink for persistence and an optional JSONL file sink for
human-readable logs.

Agent conversation traces are a specialized form of audit event. During
session execution, the backend emits structured trace events: `session.init`
(with prompts), `tool.use` (with tool input), `assistant.message` (with
response text), `tool.error` (with error details), and `session.result` (with
metrics). These traces can be reconstructed into full conversation transcripts
from the JSONL audit trail after the fact.

Audit retention is managed per-run: the oldest runs beyond a configurable
limit are pruned at the start of each new run.

---

## 11. Sync Barriers

At configurable intervals (every N completed tasks), the orchestrator pauses
dispatch and runs a synchronization sequence:

1. **Worktree verification.** Check for orphaned worktrees and clean them up.

2. **Integration branch sync.** Pull remote changes into the integration branch,
   reconciling divergence using the same merge strategy as harvest.

3. **Hot-load discovery.** Scan `.agent-fox/specs/` for new specs that weren't
   present when the plan was built. Each candidate passes four gates:
   git-tracked on the integration branch, all required artifacts non-empty,
   passes static lint, not fully implemented. Admitted specs are merged into
   the live graph. Current node statuses are persisted back to DuckDB
   immediately after hot-loading so a crash does not lose new specs.

4. **Config reload.** Re-read `config.toml` and apply changes (new cost limits,
   archetype settings, routing parameters). The `parallel` field is immutable
   and cannot be changed at runtime. All other fields take effect immediately.

Sync barriers are periodic rather than continuous to limit overhead.

---

## 12. End-of-Run Cleanup

When the orchestrator's main loop terminates (all tasks complete, circuit
breaker trips, or SIGINT received):

1. **Persist final node statuses** to DuckDB so the state accurately reflects
   actual progress.

2. **Clean up transient audit reports** for fully-completed specs.

3. **Post issue summaries** (if a platform is configured) for newly completed
   specs. If a spec's `prd.md` contains a `## Source` section with a GitHub
   issue URL, a summary comment is posted to that issue listing the spec
   name, integration branch HEAD commit, and all task group titles.

4. **Mark the run record complete** in DuckDB with the final status and
   timestamp.

5. **Emit `run.complete`** audit event with total sessions, total cost,
   duration, and final status.

---

## 13. Spec Creation

Specifications can be created manually or through the AI-powered `spec` CLI
and the `/af-spec` skill in Claude Code. Both tools produce the same v1.3
JSON format.

### 13.1 The Spec CLI Workflow

The `spec` CLI drives a multi-stage conversation with a Claude model to
transform a product idea into a complete spec package.

```
PRD (human-written or imported)
    ↓
spec new prd.md --name my_feature    # creates NN_my_feature/ with prd.md
    ↓
spec refine my_feature               # AI assesses PRD quality, returns gaps
    ↓  (loop: answer questions, re-refine until quality = "ready")
spec refine my_feature --answers a.json
    ↓
spec generate my_feature             # generates requirements.json,
    ↓                                # test_spec.json, tasks.json
spec validate my_feature             # schema + cross-file integrity check
```

**Assessment.** The spec agent evaluates the PRD against quality criteria:
completeness, consistency, feasibility, testability. It produces a structured
assessment with `quality` (ready, needs-work, blocked), `summary`, `gaps`,
and `questions`. The assessment is persisted in `_session.json` so progress
survives across CLI invocations.

**Refinement.** The user provides answers to the agent's questions. The agent
updates the PRD and re-assesses. This loop continues until quality reaches
`"ready"`.

**Generation.** Once the PRD is accepted, the agent generates the three JSON
artifacts in sequence: `requirements.json` (EARS-patterned acceptance criteria),
`test_spec.json` (language-agnostic test contracts), `tasks.json` (sequenced
implementation plan with subtask breakdowns).

**Validation.** The `spec validate` command delegates to `afspec.validate()`,
which runs JSON Schema checks and cross-file integrity rules (requirement
coverage, test traceability, task group structure).

### 13.2 The af-spec Skill

The `/af-spec` skill in Claude Code wraps the same `spec` CLI in an
interactive workflow. It accepts a file path, GitHub issue URL, or inline
description, analyzes the existing codebase for context (next spec number,
cross-spec dependencies), and drives the refine/generate/validate loop.
Optionally generates an `architecture.md` with high-level design and diagrams.

### 13.3 The afspec Library

The `afspec` package is the pure-data foundation shared by all spec tools.
It provides:

- **Data models** — Pydantic models for all spec artifacts (PRD, Requirements,
  TestSpec, Tasks) with enums for EARS patterns, subtask states, and lifecycle
  status.
- **Validation** — JSON Schema validation plus cross-file integrity checks
  (requirement coverage, test traceability, task group structure).
- **Rendering** — Deterministic markdown rendering for human-readable output.
- **Discovery** — Spec directory scanning and dependency graph construction.
- **Lifecycle** — Status transitions (draft → active → sealed → archived).
- **I/O** — PRD frontmatter parsing, JSON load/save with deterministic
  serialization.

The library has zero dependencies on `agentfox` — it is fully self-contained.

---

## 14. Information Flow Summary

```
Human Intent
    │
    ▼
Spec Artifacts (.agent-fox/specs/NN_name/)
    │  v1.3: prd.md, requirements.json, test_spec.json, tasks.json
    ▼
Planner  [deterministic, zero LLM]
    │  Static validation → Graph construction → Topo sort
    ▼
Task Graph (DuckDB: plan_nodes + plan_edges + plan_meta)
    │
    ▼
Orchestrator  [deterministic dispatch loop, zero LLM]
    │
    ├─ Ready tasks → dispatch to sessions
    │
    │   ┌─── Session Lifecycle ──────────────────────────────────────┐
    │   │                                                            │
    │   │  Worktree (feature/{spec}/{group})                         │
    │   │      ↓                                                     │
    │   │  System prompt [agent.md + archetype profile + context]    │
    │   │      ↓                                                     │
    │   │  Knowledge retrieval [reviews + cross-group + summaries]    │
    │   │      ↓                                                     │
    │   │  Claude Agent SDK ──→ LLM inference                        │
    │   │      ↓                                                     │
    │   │  Session output (code, tests, commits, JSON findings)      │
    │   │      ↓                                                     │
    │   │  Harvest: squash merge → integration branch                │
    │   │      ↓                                                     │
    │   │  Assess: parse findings → converge (multi-instance)        │
    │   │      → supersede injected findings → store summary         │
    │   │      → emit audit events                                   │
    │   │                                                            │
    │   └────────────────────────────────────────────────────────────┘
    │
    │   ┌─── Knowledge Store (DuckDB) ──────────────────────────────┐
    │   │                                                            │
    │   │  review_findings ── drift_findings ── audit_events          │
    │   │  session_summaries  finding_injections ── tool_calls       │
    │   │  session_outcomes ─ runs                                   │
    │   │                                                            │
    │   └────────────────────────────────────────────────────────────┘
    │
    └─ Sync barriers: hot-load specs, reload config, persist state
```

Each session's review findings and session summaries feed back into the
knowledge store, which feeds forward into the context of the next session for
the same spec. Quality findings from reviewer sessions are carried forward as
structured context — ensuring that later coder sessions address issues found
by earlier quality gates. Session summaries provide continuity of purpose
across session boundaries within a spec (what earlier groups accomplished).
Findings that were injected into a session and addressed are automatically
superseded when the session completes, keeping the knowledge store current
without manual intervention.

---

## 15. Design Principles in Action

**Deterministic planning, stochastic execution.** The graph is fully determined
by the specs before any LLM is invoked. The LLM work happens inside sessions;
the orchestrator coordinates but never improvises.

**Isolation by default.** Every session runs in its own worktree on its own
branch. Multiple sessions share no mutable workspace state. Integration happens
through a serializing merge lock, not through shared files.

**Separation of concerns through archetypes.** The Coder writes code; the
Reviewer judges quality; the Verifier checks correctness; the Maintainer handles
maintenance. Their tool allowlists enforce the separation — Reviewers cannot
write code; Verifiers can run tests; Coders have full access.

**Knowledge compounds over time.** Review findings and session summaries
accumulate in the knowledge store. Each subsequent session receives relevant
accumulated knowledge as context — same-group reviews, cross-group reviews,
and same-spec summaries. Findings follow a closed-loop lifecycle (created →
injected → superseded) that keeps the active set current without losing audit
history.

**Graceful degradation everywhere.** If knowledge retrieval fails, the session
proceeds without knowledge context. If review parsing fails, the session outcome
is unaffected. If the merge agent fails, the session is retried. The knowledge
system never blocks the coding session lifecycle.

**Specs are contracts.** Every generated artifact traces back to a spec
artifact. Agents that deviate from specs are caught by review agents before
their code lands. The human judgment is front-loaded in the spec; the machine
executes without improvisation.
