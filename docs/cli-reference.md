# CLI Reference

Complete reference for all `agent-fox` commands, options, and configuration.

## Quick Reference

| Command | Description |
|---------|-------------|
| `agent-fox init` | Initialize project (creates `.agent-fox/`, integration branch, `.gitignore`, `AGENTS.md`) |
| `agent-fox plan` | Build execution plan from `.agent-fox/specs/` |
| `agent-fox code` | Execute the task plan via orchestrator |
| `agent-fox standup` | Generate daily activity report |
| `agent-fox insights` | Query review findings from the knowledge database |

## Global Options

```
agent-fox [OPTIONS] COMMAND [ARGS]
```

| Option | Short | Description |
|--------|-------|-------------|
| `--version` | | Show version and exit |
| `--verbose` | `-v` | Enable debug logging |
| `--quiet` | `-q` | Suppress info messages and banner |
| `--help` | | Show help and exit |

When invoked without a subcommand, displays help text.

### JSON Mode (`--json`)

The `--json` flag is available on commands that produce structured output:
`plan`, `code`, `standup`, and `insights`. It switches the command to
structured JSON input/output mode, designed for agent-to-agent and
script-driven workflows.

**Behavior when active:**

- **Banner suppressed:** No ASCII art or version line on stdout.
- **Structured output:** Batch commands emit a single JSON object; streaming
  commands (`code`) emit JSONL (one JSON object per line).
- **Error envelopes:** Failures emit `{"error": "<message>"}` to stdout with
  the original non-zero exit code preserved.
- **Logging to stderr:** All log messages go to stderr only -- stdout contains
  only valid JSON. Warning-level logs are also suppressed unless `--verbose`
  or `--trace` is active.
- **Stdin input:** When stdin is piped (not a TTY), the CLI reads a JSON
  object from stdin and uses its fields as parameter defaults. CLI flags
  take precedence over stdin fields. Unknown fields are silently ignored.

**Agent mode:** When `AF_AGENT=1` is set, commands that support `--json`
automatically enable JSON output mode. This can be overridden with
`--no-json`.

**Examples:**

```bash
# Get structured output from standup
agent-fox standup --json

# Combine with --verbose for JSON output + debug logs on stderr
agent-fox code --json --verbose
```

**Error handling:**

```bash
# Invalid JSON on stdin produces an error envelope
echo 'not json' | agent-fox code --json
# stdout: {"error": "invalid JSON input: ..."}
# exit code: 1
```

---

## Commands

### init

Initialize the current project for agent-fox.

```
agent-fox init [OPTIONS]
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `--config` | flag | off | Create a local `.agent-fox/config.toml` (overwrites if present) |
| `--skills` | flag | off | Install bundled agent skills into `.agents/skills/` |
| `--profiles` | flag | off | Copy default archetype profiles into `.agent-fox/profiles/` |

Creates the `.agent-fox/` directory structure, sets up the integration branch
(configured via `workspace.integration_branch`, default: `main`), updates
`.gitignore`, creates `.claude/settings.local.json` with canonical permissions,
scaffolds an `AGENTS.md` template with project instructions for coding agents,
and creates `.specs/steering.md` as a placeholder for project-level agent
directives. If `AGENTS.md` already exists it is silently skipped to preserve
customizations. If `.specs/steering.md` already exists it is also silently
skipped.

**Local config (`--config`):** A local `.agent-fox/config.toml` is only created
when `--config` is explicitly passed. When present, the local config is the
**sole** config source — the global `~/.agent-fox/config.toml` is ignored
entirely. Without a local config, the global config applies. If a local
`config.toml` already exists, `--config` overwrites it with a fresh template.

**Config loading precedence:**
- **No local config** → global config at `~/.agent-fox/config.toml` applies
- **Local config present** → only local config applies (global ignored)

**Steering document:** `init` creates `.specs/steering.md` as an empty
placeholder on first run. This file is the user's persistent directive surface
-- add project-specific "always do X" or "never do Y" instructions here. All
agent sessions and bundled skills read this file and follow any directives it
contains. If the file contains only the initial placeholder text (no real
directives), it is silently skipped during prompt assembly so agents are not
distracted by empty templates.

**Profiles installation (`--profiles`):** When `--profiles` is provided, copies
all built-in archetype profiles (coder, reviewer, verifier, gate, maintainer
and their mode variants) into `.agent-fox/profiles/`. Existing profile files are
preserved -- only missing profiles are created. This enables project-level
customization of agent behavior. See [Profiles](profiles.md) for details.

**Skills installation (`--skills`):** When `--skills` is provided, copies
bundled skill templates from the agent-fox package into
`.agents/skills/{name}/SKILL.md` and creates a `.claude/skills` symlink
pointing to `.agents/skills/` for Claude Code compatibility. Each skill
becomes available as a slash command in Claude Code (e.g., `/af-spec`).
Existing skill files are overwritten with the latest bundled versions.
Works on both fresh init and re-init. If `.claude/skills/` exists as a
regular directory from a prior install, its contents are migrated to
`.agents/skills/` automatically.

**GitHub labels:** When a `[platform]` section with `type = "github"` is
configured, `init` automatically creates labels on the repository for the
fix pipeline workflow (`af:fix`, `af:fixed`, `af:no-change`). If the
platform is not configured, this step is silently skipped.

**Exit codes:** `0` success (git-dependent steps are skipped when not inside
a git repository), `1` unexpected error.

---

### plan

Build an execution plan from specifications.

```
agent-fox plan [OPTIONS] [TASK_ID]
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `--dry-run` | flag | off | Show plan analysis without persisting to database |
| `--fast` | flag | off | Exclude optional tasks |
| `--spec NAME` | string | all | Plan or reset a single spec |
| `--specs-dir PATH` | path | from config | Path to specs directory (default: from config, or `.agent-fox/specs`) |
| `--clear` | flag | off | Mark all plan nodes as completed and truncate session tables |
| `--reset` | flag | off | Soft-reset failed/blocked/in-progress tasks to pending |
| `--reset-hard` | flag | off | Hard reset all tasks with code rollback |
| `--verify` | flag | off | Cross-check spec file states against database plan states |
| `--yes` / `-y` | flag | off | Skip confirmation prompts (for `--reset` and `--reset-hard`) |
| `--json` / `--no-json` | flag | off | Enable/disable JSON output mode |

| Argument | Required | Description |
|----------|----------|-------------|
| `TASK_ID` | no | Target a single task for `--reset` or `--reset-hard` |

Scans `.agent-fox/specs/` for specification folders, parses task groups, builds a
dependency graph, resolves topological ordering, and persists the plan to the
DuckDB knowledge store. The plan is always rebuilt from `.agent-fox/specs/` on every
invocation.

#### Dry-Run Mode (`--dry-run`)

When `--dry-run` is set, the full planning pipeline runs (discovery, parsing,
building, resolving) but database persistence is skipped. Instead, the command
computes and displays a richer analysis of the plan:

- **Parallelism phases** — groups of tasks that can execute concurrently, with
  phase numbers and peak parallelism.
- **Critical path** — the longest dependency chain through the plan, identifying
  the bottleneck sequence.
- **Dependency edges** — all edges grouped by type (intra-spec and cross-spec).

The analysis output includes a summary header (specs, task count, review node
count, dependency count, fast-mode status), followed by the three analysis
sections.

`--dry-run` composes with other flags:

- `--dry-run --fast` — applies fast-mode filtering before analysis.
- `--dry-run --spec NAME` — restricts the plan to the named spec.
- `--dry-run --json` — outputs a JSON object with keys `nodes`, `edges`,
  `order`, `metadata`, `phases`, `critical_path`, and `grouped_edges`.
- All flags can be combined: `--dry-run --fast --spec NAME --json`.

The `run_plan()` API also accepts a `dry_run` parameter. When `dry_run=True`,
it returns the `TaskGraph` without opening a database connection or calling
`save_plan()`.

#### Clear Mode (`--clear`)

When `--clear` is set, all plan nodes are marked as completed and the four
session-scoped tables (`runs`, `session_outcomes`, `review_findings`,
`drift_findings`) are truncated. This is useful when you want to mark
everything as done and start fresh session tracking.

- `--clear --spec NAME` — clears only nodes belonging to the named spec.
- `--clear --json` — outputs a JSON object with `cleared` (node count) and
  optionally `spec` (the scoped spec name).

No confirmation prompt is required for `--clear`.

**Exit codes:** `0` success, `1` no plan found.

#### Reset Mode (`--reset`)

When `--reset` is set, all tasks with failed, blocked, or in-progress status
are soft-reset to pending. Worktree directories and feature branches
associated with reset tasks are cleaned up.

- `--reset TASK_ID` — resets a single task and cascade-unblocks downstream
  dependents. No confirmation prompt when a task ID is provided.
- `--reset --spec NAME` — resets all tasks belonging to the named spec.
- `--reset --yes` — skips the confirmation prompt.
- `--reset --json` — outputs a JSON object with reset summary fields.

Without `TASK_ID` or `--yes`, a confirmation prompt is shown before
proceeding.

**Exit codes:** `0` success, `1` no plan found or error.

#### Hard Reset Mode (`--reset-hard`)

When `--reset-hard` is set, performs a comprehensive state wipe:

- Resets **all** tasks to pending (including completed tasks).
- Cleans up all worktree directories and local feature branches.
- Compacts the knowledge base (deduplication and supersession).
- Rolls back the integration branch to its pre-task state (if commit
  tracking data is available).
- Preserves session history, token counters, and cost totals.

With `--reset-hard TASK_ID`, performs a partial rollback:

- Rolls back the integration branch to the commit immediately before the
  target task.
- Resets the target task and any tasks whose code is no longer on the
  integration branch (cascaded reset).
- Earlier tasks remain completed.

`--reset-hard` requires confirmation unless `--yes` is provided.

`--reset-hard` is **not** compatible with `--spec` — the combination exits
with an error.

- `--reset-hard --yes` — skips the confirmation prompt.
- `--reset-hard --json` — outputs a JSON object with hard reset summary
  fields including `rollback_sha`.

**Exit codes:** `0` success, `1` no plan found or error.

#### Mutual Exclusivity of Mode Flags

The mode flags `--dry-run`, `--clear`, `--reset`, `--reset-hard`, and
`--verify` are mutually exclusive. Passing more than one exits with an error
before any work is performed. The `--fast` flag is silently ignored when
combined with `--clear`, `--reset`, or `--reset-hard`.

**Exit codes:** `0` success, `1` plan error.

---

### code

Execute the task plan.

```
agent-fox code [OPTIONS]
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `--dry-run` | flag | off | Show plan analysis without running the orchestrator |
| `--specs-dir PATH` | path | from config | Path to specs directory (default: from config, or `.agent-fox/specs`) |
| `--watch` | flag | off | Keep running and poll for new specs after all tasks complete |
| `--watch-interval N` | int | 60 | Seconds between watch polls (minimum: 10) |
| `--force-clean` | flag | off | Automatically remove untracked files and reset dirty index before dispatch |
| `--no-parallel` | flag | off | Force serial execution (`parallel=1`) for this run |
| `--archive` | flag | off | Move completed specs to `specs/archive/` after execution |
| `--json` / `--no-json` | flag | off | Enable/disable JSON output mode |

Runs the orchestrator, which dispatches coding sessions to a Claude agent for
each ready task in the plan. Sessions execute in isolated git worktrees with
feature branches. After each session, results are harvested (merged) and state
is persisted to the DuckDB knowledge store.

Requires a persisted plan in the knowledge store (run `agent-fox plan` first).

The `--force-clean` flag overrides the `workspace.force_clean` config setting.
When active, the orchestrator automatically removes untracked files and resets
a dirty index before dispatching a session, instead of blocking the node.

#### Dry-Run Mode (`--dry-run`)

When `--dry-run` is set, the command loads the persisted plan from DuckDB
(read-only), filters out completed tasks, and displays a rich analysis of the
remaining work without starting the orchestrator or dispatching any coding
sessions. No infrastructure is set up, no writes occur, and no coding sessions
are dispatched.

The analysis output includes:

- **Parallelism phases** — groups of tasks that can execute concurrently, with
  phase numbers and peak parallelism.
- **Critical path** — the longest dependency chain through the remaining plan.
- **Dependency edges** — all edges grouped by type (intra-spec and cross-spec).

Completed tasks are excluded from all sections of the output so that only
remaining work is displayed.

**Mutual exclusion with execution flags:** `--dry-run` cannot be combined with
`--watch`, `--force-clean`, `--no-parallel`, or `--archive`. If any of these
flags are provided alongside `--dry-run`, the command exits with code 1 and
an error message listing all incompatible flags.

**Daemon guard bypass:** Because `--dry-run` is a read-only operation, it
bypasses the nightshift daemon PID guard. You can run `code --dry-run` even
while the daemon is active.

**JSON output:** `--dry-run` composes with `--json`. When both are set, the
command outputs a JSON object with the following keys:

- `nodes` — remaining (non-completed) nodes keyed by ID.
- `edges` — dependency edges between remaining nodes.
- `order` — topological order of remaining nodes.
- `metadata` — plan metadata.
- `phases` — parallelism phases computed from remaining nodes.
- `critical_path` — longest dependency chain through remaining nodes.
- `grouped_edges` — edges split into `intra_spec` and `cross_spec` groups.

If the plan is empty or all tasks are completed, the JSON output contains
empty collections for `nodes`, `edges`, and `order`.

**Examples:**

```bash
# Preview remaining work in the plan
agent-fox code --dry-run

# Get structured JSON output for scripting
agent-fox code --dry-run --json
```

**Edge cases:**

- If no plan exists (knowledge DB not found), exits with code 1 and an error
  message suggesting `agent-fox plan`.
- If the plan has no tasks, displays "No tasks in plan." and exits with code 0.
- If all tasks are completed, displays "All tasks completed." and exits with
  code 0.

#### Watch Mode (`--watch`)

When `--watch` is set, the orchestrator does not exit after all tasks complete.
Instead it enters a sleep-poll loop, re-running the sync barrier every
`--watch-interval` seconds to discover new specs added to `.agent-fox/specs/`. When new
ready tasks are found, normal dispatch resumes. This turns a single `code`
invocation into a long-lived process that picks up new work as it appears.

**Requirements for watch mode:**

- `hot_load` must be enabled in project configuration (default: on). If
  `hot_load` is disabled, `--watch` is silently ignored and the run terminates
  with COMPLETED status.
- `--watch-interval` must be >= 10 seconds (values below 10 are clamped to 10).

**Example:**

```bash
# Keep the orchestrator running, check for new specs every 30 seconds
agent-fox code --watch --watch-interval 30
```

#### Serial Mode (`--no-parallel`)

When `--no-parallel` is set, the orchestrator forces serial execution
(`parallel=1`) for this run regardless of the `orchestrator.parallel` value
in `config.toml`. This is useful for debugging or isolating flaky behavior
without modifying the config file. The override is transient — the config
file is not modified.

```bash
agent-fox code --no-parallel
```

#### Archive Mode (`--archive`)

When `--archive` is set, completed specs are moved to `specs/archive/` after
execution finishes. A spec is eligible for archiving when all of its task
group nodes have `completed` status. Partially completed specs are left in
place.

```bash
agent-fox code --archive
```

**Exit codes:**

| Code | Meaning |
|------|---------|
| `0` | All tasks completed |
| `1` | Error (plan missing, unexpected failure, or block limit exceeded) |
| `2` | Stalled (no ready tasks, incomplete remain) |
| `3` | Cost or session limit reached |
| `130` | Interrupted (SIGINT) |

Exit code 1 is also returned when the fraction of blocked tasks exceeds
`orchestrator.max_blocked_fraction`. This indicates a systemic quality
problem (many specs have blocking review findings) rather than a dependency
deadlock.

---

### standup

Generate a daily activity report.

```
agent-fox standup [OPTIONS]
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `--hours N` | int | 24 | Reporting window in hours |
| `--json` / `--no-json` | flag | off | Enable/disable JSON output mode |

Covers agent activity (sessions, tokens, cost), human commits, file overlaps
between agent and human work, and queue status (ready/pending/blocked tasks).

Use `agent-fox standup --json` for structured JSON output.

**Exit codes:** `0` success.

---

### insights

Query review findings from the knowledge database.

```
agent-fox insights [OPTIONS]
```

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `--spec NAME` | string | all | Filter by spec name |
| `--severity LEVEL` | string | all | Minimum severity level (`critical`, `major`, `minor`, `observation`) |
| `--archetype NAME` | string | all | Filter by archetype (`reviewer`, `verifier`, `reviewer/pre-review`, `reviewer/drift-review`) |
| `--run ID` | string | all | Filter by run ID |
| `--dismiss ID REASON` | string pair | | Dismiss a finding by ID with a reason |
| `--json` / `--no-json` | flag | off | Enable/disable JSON output mode |

Displays active (non-superseded) review findings from the knowledge store.
Findings are produced by Reviewer (pre-review, drift-review, audit-review
modes) and Verifier archetypes during `agent-fox code` sessions.

**Exit codes:** `0` success.

---

## `spec` CLI (external)

The `spec` command has moved to the
[agent-fox-dev/spec-format](https://github.com/agent-fox-dev/spec-format)
repository. Install it from there for AI-powered spec creation (`spec new`,
`spec refine`, `spec generate`, `spec render`, `spec validate`).

---

## Configuration

For the complete configuration reference, see [config-reference.md](config-reference.md).
