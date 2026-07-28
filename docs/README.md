# agent-fox Documentation

## How It Works

You write a spec, run `agent-fox code`, and walk away. The fox reads your
specs, plans the work, spins up isolated git worktrees, runs each coding
session with the right context, handles merge conflicts, retries failures,
extracts learnings into structured memory, and merges clean commits to the
integration branch (default: `main`). You come back to a finished feature branch and a standup report.

### The Workflow

The typical workflow has four stages:

1. **Write specs.** Describe your feature as a structured specification
   package under `.agent-fox/specs/`. New specs use the v1.3 JSON format: a PRD
   (`prd.md`), requirements (`requirements.json`), test spec (`test_spec.json`),
   and tasks (`tasks.json`), plus an optional `architecture.md`. Each spec maps
   to one coherent feature or change. Use the `spec` CLI to create and refine
   specs from a PRD:
   ```bash
   spec new prd.md --name my_feature     # create spec from PRD
   spec refine my_feature                # assess and get questions
   spec refine my_feature --answers a.json  # refine until ready
   spec generate my_feature              # generate JSON artifacts
   spec validate my_feature              # check validity
   ```
   Use `spec validate` to check validity before planning.

2. **Plan.** Run `agent-fox plan` to compile your specs into a dependency
   graph of tasks. The planner is deterministic — same specs, same graph,
   every time. It parses task groups from each spec, builds intra-spec chains
   (groups execute sequentially), wires cross-spec dependencies declared in
   PRDs, and injects review agents at the right positions. Use `--dry-run` to
   see a parallelism analysis, or `--fast` to exclude optional tasks.

3. **Execute.** Run `agent-fox code` to start autonomous
   execution. The orchestrator dispatches agents to each ready task in
   dependency order. Each agent works in an isolated git worktree on its own
   feature branch, so multiple agents work simultaneously without conflicts.
   Reviewer agents (pre-review, drift-review modes) check specs before
   coding starts; audit-review and Verifier agents check the result after. Failed
   tasks are retried with escalation to stronger models. Completed work is
   merged into the integration branch under a serializing lock via squash merge (with
   AI-assisted conflict resolution when needed). When all tasks for a spec
   complete, a summary comment is automatically posted to the originating
   GitHub issue (if `prd.md` contains a `## Source` section with a URL).

4. **Monitor.** Run `agent-fox standup` for an activity report covering
   agent sessions, human commits, and token consumption. Run
   `agent-fox insights` for a structured view of review findings, drift
   reports, and verification verdicts across specs. Both commands support
   `--json` for machine consumption.

### Agent Archetypes

agent-fox uses a six-entry archetype registry with a mode system to divide
labor:

- **Coder** — the primary implementation agent. Receives the full spec
  context and implements one task group per session. Follows a test-first
  workflow: group 1 writes failing tests, subsequent groups implement code.
- **Reviewer** — a single archetype with four modes that cover all review
  roles:
  - *pre-review* — reviews spec quality before implementation. Checks
    completeness, consistency, feasibility, and security. Can block coding
    if critical findings exceed a threshold.
  - *drift-review* — validates spec assumptions against the actual codebase.
    Detects drift between what specs expect and what actually exists.
    Automatically skipped when the spec references no existing code.
  - *audit-review* — validates test quality against test spec contracts
    after tests are written. Triggers coder retries when tests are missing,
    weak, or misaligned with their specifications.
  - *fix-review* — reviews fix-mode patches (quality fixes, night-shift
    repairs) with full tool access and extended turn budget.
- **Curator** — performs post-implementation curation after coders and
  before the verifier. Read-only access with medium effort.
- **Verifier** — performs post-implementation verification. Runs the test
  suite, checks each requirement against acceptance criteria, and triggers
  coder retries when verification fails.
- **Gate** — lightweight checkpoint verification for mid-spec progress
  checks. Assigned automatically to `checkpoint` task groups.
- **Maintainer** — drives night-shift operations with three modes (hunt,
  fix-triage, extraction). Not assignable to spec tasks.

Review and verification archetypes can run multiple instances in parallel on
the same task, with outputs merged using mode-specific convergence strategies.
For full archetype details, see the
[Archetypes section](architecture/03-execution-and-archetypes.md#agent-archetypes)
in the Architecture Guide.

### Night Shift

For ongoing codebase health, the standalone `night-shift` CLI runs as a continuously
running fix-only daemon. It polls GitHub for issues labelled `af:fix` and
processes them through a three-stage pipeline (Triage, Coder, Reviewer in
fix-review mode). Each fix is implemented on an isolated branch and merged
back into the integration branch.

### Knowledge System

agent-fox maintains a persistent knowledge store that provides
institutional memory across sessions. Each new session starts with a fresh
context window but receives curated, relevant knowledge from prior sessions
so agents build on each other's work rather than starting blind.

The knowledge system tracks three categories of context: review findings
(active critical and major findings for the current task group), cross-group
findings (issues found in other groups of the same spec), and same-spec
session summaries (what earlier groups accomplished). Findings follow a
closed-loop lifecycle — when a finding is injected into a session and the
session completes, the finding is automatically superseded. This keeps the
active knowledge set current without manual intervention.

### Recovery

When tasks fail or become blocked, start by diagnosing what went wrong.
Run `agent-fox insights` to list active review findings — critical findings
from pre-review, drift-review, or verification often explain why a task is
blocked. Filter by spec with `--spec NAME` or by severity with
`--severity critical` to narrow down the cause. Once you understand and
address the blocking finding (e.g., fix a spec issue flagged by pre-review,
resolve a drift detected against the codebase), dismiss it with
`--dismiss ID REASON`, then run `agent-fox plan --reset` to restart the
affected task. For targeted recovery, pass a specific task ID:
`agent-fox plan --reset TASK_ID`. For a full restart, use
`agent-fox plan --reset-hard` to reset all tasks, clean up worktrees and
branches, compact the knowledge store, and roll back the integration branch.

## Architecture

For a detailed understanding of how agent-fox works internally, start with
the [Coding Session Architecture](architecture.md) — a top-down walkthrough
covering persistent state, the orchestrator's dispatch loop, session
lifecycle, prompt construction, the knowledge system, and worktree/git
architecture. For topic-specific deep dives, see the
[Architecture Guide](architecture/README.md). Both are written for senior
engineers joining the project and stay at the conceptual level without code
snippets or class hierarchies.

## Reference

| Document | Description |
|----------|-------------|
| [Coding Session Architecture](architecture.md) | Top-down walkthrough of session and knowledge system |
| [CLI Reference](cli-reference.md) | All commands, flags, and exit codes |
| [Configuration Reference](config-reference.md) | Every `config.toml` section and option |
| [Archetypes](architecture/03-execution-and-archetypes.md#agent-archetypes) | Archetype registry, modes, and convergence |
| [Profiles](profiles.md) | Agent profiles, resolution, and customization |
| [Skills](skills.md) | Claude Code skill reference |
| [Architecture Guide](architecture/README.md) | Topic-specific architecture deep dives |
| [Spec Format v1.3](architecture/06-spec-format-v13.md) | JSON-based spec format, parsing pipeline, validation, context assembly |
