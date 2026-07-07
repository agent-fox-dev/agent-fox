# Product Requirements Document: agent-fox

**Version:** 3.0  
**Date:** 2026-04-08  
**Status:** Current

---

## 1. Product Overview

agent-fox is an autonomous coding-agent orchestrator that drives AI coding agents through software development tasks without requiring continuous human supervision. A user describes what to build through specification documents; agent-fox takes it from there — planning work, dispatching agents, managing failures, merging results, and maintaining a persistent knowledge store — returning a finished feature branch and activity report when complete.

The product eliminates the primary pain point of AI-assisted development: the need to babysit an agent session by session, re-providing context, managing merge conflicts, and tracking what was done. agent-fox keeps working while the user is away.

---

## 2. Goals

- **Unattended execution:** A user should be able to initiate a multi-session coding run and walk away. agent-fox handles all coordination, retries, and merging autonomously.
- **Spec-driven correctness:** All work flows from structured specifications. Agents that deviate from specs are caught before their code lands.
- **Adaptive quality gates:** Multiple specialized reviewer roles check work at different stages and block or retry sessions that fall below quality thresholds.
- **Cost and session control:** Users can set hard limits on total API spend and the number of sessions, with the system halting cleanly when limits are reached.
- **Continuous maintenance:** A daemon mode processes `af:fix`-labelled issues autonomously, keeping codebases healthy over time.

## Non-Goals

- agent-fox does not write or modify the user's specifications — that remains a human or skill-assisted activity.
- agent-fox does not replace version control or project management tooling — it integrates with git and GitHub but does not own those systems.
- agent-fox is not a general-purpose AI chat interface; it has no conversational mode.
- agent-fox does not support AI providers other than Claude.

---

## 3. User Personas

### The Autonomous Developer
A software engineer who writes detailed specifications and wants them implemented while they focus on higher-level design, meetings, or rest. They run `agent-fox code` and return hours later to review a finished feature branch. They care about correctness and cost, and want clear visibility into what was done and what remains.

### The Team Lead / Engineering Manager
An engineering leader who uses agent-fox to ship maintenance work, backlog items, and spec-driven features without consuming developer time for repetitive implementation tasks. They use the standup report to track agent activity and compare it against human commits. They care about cost accountability and the quality of landed code.

### The Solo Developer / Open-Source Maintainer
A developer maintaining a project alone who uses Night Shift to keep the codebase healthy overnight — processing `af:fix`-labelled issues and waking up to fixes already merged into develop by autonomous agents.

### The CI/Script Consumer
A pipeline or automation script that drives agent-fox in `--json` mode, consuming structured JSON output from every command, piping parameters via stdin, and reacting to exit codes. This persona has no interactive terminal.

---

## 4. User Workflows

### 4.1 First-Time Setup

1. The user installs agent-fox and navigates to their project root.
2. The user runs `agent-fox init --skills` to initialize the project.
3. agent-fox creates the project directory structure, a default configuration file, updates the ignore list, scaffolds an agent instructions document, creates the integration branch, and installs bundled skills as slash commands.
4. The user opens the configuration file and adjusts any settings (parallelism, cost limits, model tiers).
5. The user opens the steering document and adds any project-level directives that agents must always follow.

### 4.2 Spec-Driven Feature Development

1. The user writes or generates a specification package (PRD, requirements, design, test contracts, task list) under the project's specifications directory.
2. The user runs `agent-fox plan` to build an execution plan from the specifications.
3. agent-fox scans all specification folders, resolves task dependencies, and saves the plan.
4. The user runs `agent-fox code` (optionally with `--parallel N`) to start autonomous execution.
5. agent-fox dispatches agents to each ready task in dependency order. Reviewer agents run before and after coder agents. Coders work in isolated branches.
6. After each session, agent-fox harvests the result, merges clean commits to the integration branch, updates its knowledge store, and dispatches the next ready tasks.
7. The user runs `agent-fox status` at any time to see progress: task counts, token usage, cost, and any blocked or failed tasks.
8. When all tasks complete (or a cost/session limit is reached), the user receives a clean integration branch with all implemented work.

### 4.3 Quality Issue Resolution

1. The user runs `agent-fox fix` when quality checks are failing.
2. agent-fox detects available quality tools (test runner, linter, type checker), runs them, clusters failures by root cause, and generates fix specifications.
3. Coding agents resolve each cluster. agent-fox iterates until all checks pass or the maximum pass count is reached.
4. The user receives either a clean quality check result or a report of remaining failures.

### 4.4 Night Shift Maintenance

1. The user configures a GitHub platform connection and runs `nightshift`.
2. The daemon immediately polls GitHub for issues labelled `af:fix` and routes each through a three-stage pipeline (triage → coder → reviewer in fix-review mode).
3. Completed fixes are merged into the integration branch and the originating issue is closed.
4. The daemon continues polling for new `af:fix` issues at the configured interval.
5. The user stops the daemon with Ctrl-C; it completes any active operation before exiting.

### 4.5 Progress Monitoring and Reporting

1. The user runs `agent-fox status` to see a dashboard: task counts by state (done, in-progress, pending, failed, blocked), total token usage, estimated cost, cost breakdown by agent role and by specification, and details on any blocked or failed tasks.
2. The user runs `agent-fox standup` to generate a report covering agent sessions, human commits, file overlaps between agent and human work, and the current task queue.
3. Both commands support JSON output for machine consumption.

### 4.6 Recovery and Retry

1. When tasks fail or become blocked, the user runs `agent-fox reset` to clear failed and blocked tasks and retry them.
2. For targeted recovery, the user passes a specific task identifier; agent-fox resets that task and unblocks its dependents.
3. For a full restart, the user runs `agent-fox reset --hard`, which resets all tasks (including completed ones), cleans up branches and isolated working directories, compacts the knowledge store, and rolls back the integration branch to its pre-task state.

---

## 5. Functional Requirements

### 5.1 Initialization

- WHEN a user runs `agent-fox init`, the system SHALL create the project directory structure, a configuration file, a steering document placeholder, an agent instructions document, the integration branch, and update the project's ignore list.
- WHEN `--skills` is provided, the system SHALL install all bundled skills as slash commands and report how many were installed.
- WHEN `agent-fox init` is run on a project that already has a configuration file, the system SHALL merge the existing file non-destructively: preserving active user values, adding new fields as commented-out entries, marking unrecognized active fields as deprecated, and making no changes if the file is already current.
- WHEN `agent-fox init` is run outside a git repository, the system SHALL exit with an error.

### 5.2 Planning

- WHEN a user runs `agent-fox plan`, the system SHALL scan the specifications directory, parse all task groups, build a dependency graph, resolve a topological task ordering, and save the plan.
- WHEN `--fast` is provided, the system SHALL exclude tasks marked as optional.
- WHEN `--spec NAME` is provided, the system SHALL plan only the named specification.
- WHEN `--analyze` is provided, the system SHALL display a parallelism analysis alongside the plan.
- WHEN a plan already exists, `agent-fox plan` SHALL rebuild it from scratch on every invocation.

### 5.3 Autonomous Execution

- WHEN a user runs `agent-fox code`, the system SHALL dispatch coding sessions for all ready tasks in the plan, honoring dependency order.
- WHEN `--parallel N` is provided, the system SHALL run up to N sessions concurrently (maximum 8).
- WHEN configured reviewer archetypes are enabled, the system SHALL automatically insert pre-coding review sessions before the first coding group and post-coding verification sessions after the last coding group.
- WHEN a pre-coding review produces critical findings exceeding the configured threshold, the system SHALL block coding sessions and report the blocking findings.
- WHEN multiple reviewer instances are configured, the system SHALL run them in parallel and converge findings using majority agreement.
- WHEN a session fails, the system SHALL retry it at the current model tier up to the configured retry limit, then mark the task as blocked.
- WHEN all tasks complete successfully, the system SHALL exit with code 0.
- WHEN a cost ceiling is reached, the system SHALL stop dispatching new sessions and exit with code 3.
- WHEN a session limit is reached, the system SHALL stop dispatching new sessions and exit with code 3.
- WHEN the system stalls (no ready tasks remain but incomplete tasks exist), the system SHALL exit with code 2.
- WHEN `--watch` is enabled and `hot_load` is active in configuration, the system SHALL poll for new specifications after all tasks complete and resume dispatching when new ready tasks appear.
- WHEN interrupted by the user (SIGINT), the system SHALL exit with code 130.

### 5.4 Agent Archetypes

- WHEN the Reviewer archetype is enabled in pre-review mode, the system SHALL run one or more independent spec review sessions before the first coding group, each producing structured findings categorized by severity.
- WHEN the Reviewer archetype is enabled in drift-review mode, the system SHALL run a codebase-verification session that checks specification assumptions against the actual codebase before coding begins.
- WHEN the Reviewer archetype is enabled in audit-review mode, the system SHALL run a test-quality audit after each test-writing session, and trigger a coder retry if the audit finds missing, misaligned, or excessively weak tests.
- WHEN the Curator archetype is enabled, the system SHALL run a post-implementation curation session after the last coding group and before verification.
- WHEN the Verifier archetype is enabled, the system SHALL run a post-implementation quality review after curation, and trigger a coder retry with verifier feedback if the review fails.
- WHEN the Gate archetype is assigned to a checkpoint task group, the system SHALL run a lightweight verification session for that checkpoint.

### 5.5 Status and Reporting

- WHEN a user runs `agent-fox status`, the system SHALL display: task counts by state, total token usage, estimated cost, cost breakdown by agent role and by specification, and details on any problematic tasks.
- WHEN `--model` is provided, the system SHALL additionally display the execution model and critical-path analysis.
- WHEN a user runs `agent-fox standup`, the system SHALL report agent session activity, human commits, file overlaps between agent and human work, and queue status for the configured time window.
- WHEN `--json` is active, every command SHALL emit structured JSON to stdout and send all log messages to stderr only.

### 5.6 Quality Fix

- WHEN a user runs `agent-fox fix`, the system SHALL detect available quality check tools, run them, cluster failures by root cause, generate fix specifications, and dispatch coding sessions to resolve each cluster.
- WHEN `--dry-run` is provided, the system SHALL generate fix specifications but not dispatch any sessions.
- WHEN `--auto` is provided and all checks pass after repair, the system SHALL run iterative improvement passes up to `--improve-passes` times.
- WHEN all quality checks pass, the system SHALL exit with code 0; otherwise with code 1.

### 5.7 Reset and Recovery

- WHEN a user runs `agent-fox reset` without arguments, the system SHALL prompt for confirmation and then reset all failed, blocked, and in-progress tasks, cleaning up their working directories and branches.
- WHEN a task identifier is provided, the system SHALL reset only that task and unblock its dependents, without prompting.
- WHEN `--hard` is provided, the system SHALL reset all tasks including completed ones, clean up all working directories and branches, compact the knowledge store, and roll back the integration branch.

### 5.8 Knowledge Export

- WHEN a user runs `agent-fox export --memory`, the system SHALL write all active knowledge facts grouped by category to stdout (or `docs/knowledge.json` in JSON mode).
- WHEN a user runs `agent-fox export --db`, the system SHALL write a full knowledge store dump to `.agent-fox/knowledge_dump.md` (or `.agent-fox/knowledge_dump.json` in JSON mode).
- WHEN both or neither flag is provided, the system SHALL exit with an error.

### 5.9 Night Shift Daemon

- WHEN a user runs `nightshift`, the system SHALL immediately begin polling for `af:fix`-labelled GitHub issues, repeating on the configured interval.
- WHEN an `af:fix`-labelled issue is detected, the system SHALL route it through a three-stage pipeline (triage → coder → reviewer in fix-review mode).
- WHEN the platform is not configured or the required access token is absent, the system SHALL exit with code 1 at startup.
- WHEN the accumulated cost reaches the configured maximum, the system SHALL stop dispatching new fix sessions and exit with code 0.
- WHEN interrupted once (SIGINT), the system SHALL complete the current operation and exit cleanly.
- WHEN interrupted a second time (SIGINT), the system SHALL abort immediately and exit with code 130.

---

## 6. Configuration and Input Specification

### 6.1 Configuration File

The project configuration lives in `.agent-fox/config.toml`. It is generated automatically by `agent-fox init`, which produces a fully-commented file with every available option, its description, valid range, and default value.

Key configurable areas:

| Area | What the user controls |
|------|------------------------|
| Parallelism | Number of concurrent coding sessions (1–8) |
| Cost ceiling | Maximum total API spend in USD before the system halts |
| Session limit | Maximum number of sessions before the system halts |
| Model tiers | Which Claude model tier each agent role uses (SIMPLE / STANDARD / ADVANCED) |
| Thinking mode | Extended thinking behavior per role (adaptive / disabled) |
| Effort | Output effort level per archetype (low / medium / high / xhigh / max) |
| Archetype toggles | Which reviewer and specialist roles are active |
| Archetype instances | How many independent reviewer instances run per role |
| Blocking thresholds | How many critical findings trigger a coding block |
| Retry limits | How many times to retry a failed session before escalating |
| Hot load | Whether the orchestrator watches for new specs after completion |
| Platform | GitHub connection for Night Shift (type, credentials) |
| Night Shift intervals | How often issue checks run |
| Priority labels | Issue priority for night-shift processing order (high / medium / low) |
| Prompt caching | Cache control policy for system prompts |

When the user re-runs `agent-fox init` after a tool upgrade, new configuration options are added as commented-out entries and removed options are marked deprecated, without disturbing any user-set values.

### 6.2 Specification Format

Specifications live under `.specs/NN_name/` (numbered by creation order) and contain five files: a PRD, acceptance criteria, design, test contracts, and a task list. The `agent-fox plan` command reads these files to build the execution graph.

The `/afspec` skill in Claude Code generates the full five-file package from a PRD, plain description, or GitHub issue URL.

### 6.3 Steering Document

`.agent-fox/steering.md` is a persistent directive surface. Any instruction placed here (e.g., "always add type hints," "never use global state") is injected into every agent session and skill invocation. If the file contains only the initial placeholder text, it is silently skipped.

### 6.4 Command-Line Inputs

All commands accept `--verbose` for debug logging and `--quiet` to suppress informational output. In `--json` mode, parameter defaults can be provided by piping a JSON object to stdin; CLI flags take precedence.

---

## 7. Output Specification

### 7.1 Terminal Output (Default Mode)

- **`agent-fox init`:** Confirmation messages for each created or updated artifact, plus the count of skills installed when `--skills` is used.
- **`agent-fox plan`:** Summary of the built plan (task count, dependency edges, optional parallelism analysis).
- **`agent-fox code`:** Live session progress as tasks start, complete, or fail. Final summary on exit.
- **`agent-fox status`:** Dashboard showing task counts, token usage, cost totals, cost breakdowns, and problem task details.
- **`agent-fox standup`:** A human-readable daily activity report covering agent sessions, human commits, file overlaps, and queue status.
- **`agent-fox fix`:** Per-iteration pass/fail results and a final summary.
- **`agent-fox reset`:** Confirmation of which tasks were reset and which working directories were cleaned up.
- **`agent-fox export`:** The destination path of the written file.
- **`nightshift`:** Live log of fix session activity.

### 7.2 JSON Mode Output

When `--json` is active:

- Batch commands emit a single JSON object on stdout.
- Streaming commands (`code`, `fix`) emit JSONL (one JSON object per line).
- All log messages go to stderr only.
- Errors produce `{"error": "<message>"}` on stdout with a non-zero exit code.
- Unknown or invalid stdin JSON produces an error envelope.

### 7.3 Code Artifacts

- Completed coding sessions produce conventional commits merged onto the integration branch.
- Night Shift fix sessions produce feature branches and open pull requests on GitHub.
- Knowledge extracted from sessions is stored in a persistent knowledge store and accessible via `agent-fox export`.

### 7.4 Reports

- `agent-fox standup --output PATH` writes the standup report to the specified file.
- `agent-fox export --memory` writes knowledge facts to stdout (or JSON mode).
- `agent-fox export --db` writes `.agent-fox/knowledge_dump.md` (or `.agent-fox/knowledge_dump.json`).
---

## 8. Error Handling and User Feedback

| Situation | User-visible behavior |
|-----------|----------------------|
| Task session fails after all retries | Task marked blocked; reason shown in `agent-fox status` |
| Pre-coding review finds too many critical issues | Coding sessions blocked; blocking findings listed; user must revise specs and re-run |
| Cost ceiling reached | System halts, reports total spend, exits with code 3 |
| Session limit reached | System halts, reports session count, exits with code 3 |
| No ready tasks (stall) | System exits with code 2; `status` shows which tasks are blocked and why |
| Plan file missing for `code` or `status` | Error message directing the user to run `agent-fox plan` first; exits with code 1 |
| `init` run outside a git repository | Clear error message; exits with code 1 |
| Night Shift: platform not configured | Clear error at startup directing user to configure `[platform]`; exits with code 1 |
| Night Shift: platform API temporarily unavailable | Error logged as warning; next interval retries normally |
| Invalid JSON piped to `--json` mode | Error envelope `{"error": "..."}` on stdout; exits with code 1 |
| Configuration file contains invalid TOML | Warning logged; file left untouched; user must fix manually |
| `export` called without `--memory` or `--db` | Error message explaining mutual exclusivity; exits with code 1 |
| SIGINT during `code` or `nightshift` | Graceful shutdown after current operation; clean exit (code 0 or 130 on second SIGINT) |

---

## 9. Constraints and Assumptions

- **Claude only:** agent-fox works exclusively with Claude models via the Anthropic SDK. No other AI provider is supported.
- **Git required:** The product requires a git repository. All isolation, branching, and merging workflows depend on git.
- **Python 3.12+:** The product targets Python 3.12 and later.
- **uv package manager:** Installation and dependency management use `uv`.
- **Specifications required before planning:** `agent-fox plan` and `agent-fox code` require well-formed specification files in `.specs/`. The product does not create specifications; that is a human or skill-assisted step.
- **GitHub for Night Shift:** The Night Shift daemon requires a GitHub platform configuration and valid access token. Other issue trackers are not supported.
- **Parallelism ceiling:** No more than 8 concurrent coding sessions are supported.
- **Feature branches are local only:** Coding session branches are not pushed to the remote; only the integration branch is pushed.
- **Knowledge store is local:** The DuckDB-backed knowledge store lives in `.agent-fox/` and is not shared across machines.
- **Prompt caching minimum size:** Prompt caching is automatically skipped for system prompts below the model's minimum cacheable size.

---

## 10. Open Questions

1. **Multi-repository support:** Should a future version support orchestrating agents across multiple repositories in a single run, or is single-repository scope a permanent constraint?
2. **Other issue trackers:** Is there user demand for Night Shift to support issue trackers other than GitHub (GitLab, Linear, Jira)?
3. **Human-in-the-loop gates:** Should there be a configurable pause point where the system waits for human approval before proceeding past a review stage (e.g., "pause after Skeptic findings for human review")?
4. **Distributed execution:** Is there interest in running coding sessions on remote machines or containers rather than local worktrees, enabling larger parallelism and better isolation?
5. **Spec generation quality:** How should the product handle specs that are syntactically valid but semantically poor (vague requirements, missing edge cases)? The `spec validate` command addresses structural issues — should semantic checks be added?
6. **Cost attribution:** Should cost reporting distinguish between API spend on reviewer archetypes vs. coding archetypes more prominently to help users optimize their configuration?
