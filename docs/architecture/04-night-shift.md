# Night-Shift Mode

> **Note:** Night-shift is a separate product in its own repository:
> [agent-fox-dev/nightshift](https://github.com/agent-fox-dev/nightshift).
> It ships its own CLI (`nightshift` command), its own core library (`afcore`),
> and its own configuration file (`.nightshift/config.toml`). The architecture
> description below documents the nightshift pipeline as it integrates with an
> af repository.

## Purpose and Placement

Night-shift is a fix-only maintenance daemon that runs continuously,
processing `af:fix`-labelled issues without human intervention. It works with
any configured platform — GitHub, GitLab, or Gitea. While the spec-driven
pipeline ([Parts 1–3](01-spec-authoring.md)) implements features from authored
specifications, night-shift operates in the opposite direction: it picks up
issues filed against the codebase and generates the fixes needed to resolve
them.

The two modes are complementary. The spec pipeline builds new capabilities
by executing a human-authored plan. Night-shift maintains the codebase by
fixing issues that have been triaged and labelled for automatic repair. The
fix pipeline reuses the same session infrastructure — agents in isolated
workspaces — but with automatically generated specs rather than human-authored
ones.

---

## Conceptual Model

Night-shift operates as a single-stream fix loop: it polls the configured
platform for issues labelled `af:fix`, determines a safe processing order
using dependency analysis, and executes a multi-stage fix pipeline for each
issue.

The fix stream runs on a timer (default: every fifteen minutes) because it
is lightweight — it queries GitHub for labelled issues and dispatches fix
pipelines. The fix stream fires immediately on startup (so the first fix
attempt happens without waiting for the timer interval) and then repeats
at its configured interval.

---

## Issue Selection and Ordering

### Fetching Issues

The fix stream queries the configured platform for open issues with the
`af:fix` label. A human must review issues and apply the `af:fix` label to
approve automated repair — night-shift never processes unlabelled issues.

Issues are sorted by creation date ascending as a baseline ordering, with
a secondary sort by issue number to guarantee determinism.

### Dependency Detection

When three or more fixable issues exist, the system performs batch analysis
to determine a safe processing order. Dependency edges are gathered from
three sources:

**Text references.** The system scans all issue bodies for explicit
dependency language — patterns like "depends on #N", "blocked by #N",
"after #N", "requires #N" — using case-insensitive regex matching. These
produce hard edges in the dependency graph.

**Platform cross-references.** On platforms that support it (e.g. GitHub's
timeline API), the system queries cross-reference events between issues in
the batch. When issue A's timeline shows a cross-reference to issue B, a
potential dependency edge is recorded.

**AI batch triage.** A Maintainer agent in `hunt` mode (SIMPLE model tier,
read-only access) receives all issue titles and body previews (first 500
characters each) along with the already-known dependency edges. It returns
a JSON object containing:

- `processing_order` — a recommended sequence of issue numbers.
- `dependencies` — additional from/to/rationale edges the AI detected from
  semantic analysis of the issue descriptions.
- `supersession` — pairs of issues where fixing one would make the other
  obsolete (keep/obsolete/rationale).

AI-detected edges are merged with explicit edges. When there is a conflict
(explicit says A→B, AI says B→A), the explicit edge wins.

For fewer than three issues, batch triage is skipped and issues are
processed in creation-date order.

### Supersession Detection

Some issues become obsolete when another is fixed. The batch triage stage
identifies these pairs and closes the obsolete issue via the platform API
before processing begins, preventing wasted work. Superseded issues receive
the `af:fixed` label and a comment noting which issue supersedes them.

### Topological Sort

The dependency edges form a graph. Kahn's topological sort produces a safe
processing order that respects dependencies. Tie-breaking uses two criteria:

1. **Priority labels.** Issues labelled `priority:high` sort before
   unlabelled or `priority:medium` issues, which sort before `priority:low`
   issues.
2. **Issue number.** Within the same priority tier, lower issue numbers
   (older issues) are processed first.

If the dependency graph contains cycles, the system breaks them by removing
the edge pointing to the oldest (lowest-numbered) issue in the cycle.

---

## The Fix Pipeline

Each issue passes through a multi-stage pipeline that mirrors the spec
pipeline's session infrastructure but operates on automatically generated
specs rather than human-authored ones.

### Stage 1: Triage Analysis

A Maintainer agent in `fix-triage` mode analyzes the issue. This agent has
read-only access to the codebase (`cat`, `head`, `tail`, `ls`, `git`,
`grep`, `find`, `wc`) and operates at the STANDARD model tier.

The triage agent explores the codebase, traces the code path related to the
issue, identifies the root cause, and produces a structured JSON report
containing:

- **`summary`** — a 1–3 sentence root cause analysis.
- **`affected_files`** — a list of file paths that need modification.
- **`acceptance_criteria`** — an array of structured criteria, each with an
  `id`, `description`, `preconditions`, `expected` outcome, and `assertion`
  pseudocode.
- **`assessed_complexity`** — a complexity assessment with `tier`
  (SIMPLE/STANDARD/ADVANCED), `variant` (null/fast/standard/extended),
  `confidence` (0–1), and `rationale`.

The triage report is posted as a comment on the issue via the platform API,
providing visibility into the system's analysis. The acceptance criteria are
injected into both the coder and reviewer prompts in subsequent stages.

### Stage 2: In-Memory Spec Construction

The fix pipeline generates a lightweight in-memory spec from the issue rather
than writing spec files to disk. This avoids polluting `.agent-fox/specs/`
with ephemeral repair specifications.

**`InMemorySpec`** captures the minimal information needed to drive a
coding session:

- `issue_number` — the GitHub issue number.
- `title` — the issue title.
- `task_prompt` — assembled from the sanitized issue title and body.
- `system_context` — the full sanitized issue body for reference context.
- `branch_name` — generated as `fix/{issue_number}-{slug}` where the slug
  is derived from the issue title (lowercased, non-alphanumeric stripped,
  hyphens collapsed).

When triage produces acceptance criteria, the pipeline also builds a full
`afspec` `Spec` object using `build_afspec_from_triage()`. This converts
the triage output into structured requirements, test specs, and a single
task group — the same data model used by the spec pipeline. The afspec is
rendered to markdown via `afspec.render_individual()` and injected into
the coder and reviewer system prompts, giving them the same structured
context that spec-driven sessions receive.

### Stage 3: Knowledge Retrieval

Before entering the coder-reviewer loop, the pipeline queries the
`KnowledgeProvider` for relevant knowledge items. The retrieval uses the
spec name derived from the issue, a task description built from the issue
title and body, and the current session ID. Returned knowledge items are
injected as "Memory Facts" in the system prompt.

The knowledge provider's `run_id` is set to the current daemon run ID,
ensuring that knowledge ingested during the fix pipeline is associated
with the correct run for auditing purposes.

### Stage 4: Coder-Reviewer Loop

The coder-reviewer loop is the core execution mechanism. It retries up to
`max_retries + 1` iterations (default `max_retries = 3`).

Each iteration consists of:

**Coder phase.** A Coder agent in `fix` mode implements the fix on an
isolated branch. The system prompt contains the full issue body, rendered
afspec context (requirements, test spec, tasks), triage acceptance criteria,
and knowledge facts. The task prompt directs the agent to fix the described
problem. The coder uses the `coder_fix.md` profile, which requires
conventional commit format: `fix(#<N>, nightshift): <description>`.

On retry attempts (attempt > 1), the previous review feedback is rendered
and injected as "Previous Review Feedback (FAILED)" context, giving the
coder specific guidance on what to fix.

**Reviewer phase.** A Reviewer agent in `fix-review` mode (ADVANCED model
tier) reviews the patch. It produces a structured JSON verdict with
per-criterion assessments and an overall verdict (PASS/FAIL). If the
reviewer's output cannot be parsed, the system retries the reviewer once
before treating it as a parse failure.

The review verdict is posted as a comment on the issue via the platform API.

If the overall verdict is PASS, the loop exits successfully. If FAIL and
retries remain, the review feedback is stored and injected into the next
coder iteration. If retries are exhausted, a comment is posted noting that
manual intervention is required, and the pipeline returns failure.

The loop retries at the same model tier throughout — there is no
escalation to a higher tier.

### Stage 5: Harvest and Integration

After the coder-reviewer loop completes successfully:

1. **Auto-commit sweep.** Any uncommitted changes left by the coder or
   reviewer sessions are staged and committed automatically to prevent
   silent data loss.
2. **Optional push.** If `config.night_shift.push_fix_branch` is enabled,
   the fix branch is force-pushed to the remote.
3. **Squash merge.** The fix branch is harvested into the integration
   branch using the same squash-merge strategy as the spec pipeline. If
   merge conflicts arise, a merge agent resolves them.

### Post-Pipeline Issue Updates

The pipeline updates the issue via the platform API based on the outcome:

- **Successful merge**: The issue receives the `af:fixed` label and is
  closed with a comment pointing to the fix branch.
- **No commits produced**: The issue receives the `af:no-change` label,
  signalling the need for human review. The issue stays open.
- **Pipeline failure**: The issue receives a failure comment with the
  branch name for manual recovery. The branch is preserved — work done
  before the failure is not discarded.

### Knowledge Ingestion

After each completed session (coder or reviewer), the pipeline calls
`KnowledgeProvider.ingest()` with the session context (status, touched
files, commit SHA, archetype, attempt number). This ensures that knowledge
from fix sessions feeds back into the knowledge store for future sessions.

---

## Drain Behavior

The fix stream does not process one batch of issues per interval. Instead,
a drain loop re-polls the platform after each fix and continues processing
until zero `af:fix` issues remain, with a safety valve of 50 iterations. A
`seen` set prevents re-processing recently closed issues that the API may
still return due to eventual consistency. A separate `_processed_issues`
instance set provides additional deduplication across drain cycles.

The drain loop respects cost limits, session limits, and shutdown signals
between iterations. This means starting night-shift with many `af:fix`
issues will process all of them in rapid succession rather than spacing
them across intervals.

---

## Staleness Detection

After completing a round of fixes, the engine checks whether any remaining
open issues have become stale. A fix to one issue may resolve problems
reported in another — for example, fixing a deprecated API usage might also
resolve the linter warning that flagged it.

Staleness detection uses an AI evaluation to determine which remaining
issues may have been resolved by the fix, followed by platform API
verification to confirm the issue is still open. Issues identified as stale
are closed with a comment noting which fix resolved them and assigned the
`af:fixed` label.

---

## Labels

Night-shift uses platform labels to manage its fix workflow lifecycle:

| Label | Applied by | Meaning |
|-------|-----------|---------|
| `af:fix` | User | Issue eligible for automatic fixing |
| `af:fixed` | Fix pipeline | Fix successfully merged, or superseded/stale |
| `af:no-change` | Fix pipeline | Coder produced no commits; needs human review |
| `priority:high` | User | Process before other issues in topological sort |
| `priority:medium` | User | Default priority (same as unlabelled) |
| `priority:low` | User | Process after other issues in topological sort |

All labels are automatically created on the GitHub repository by
`af init` when a `[platform]` section is configured.

---

## Engine Lifecycle

### Startup

On startup, the engine validates that a platform is configured (one of
`github`, `gitlab`, or `gitea` is required for issue management), initializes
the platform client, initializes the knowledge store, cleans up stale merge
locks and audit files, and writes a PID file to `.agent-fox/daemon.pid`. The
first fix cycle fires immediately without waiting for the timer interval.

### Event Loop

The `DaemonRunner` manages work streams as asyncio tasks. Each stream runs
a loop that fires immediately on first run and then waits the configured
interval (default 15 minutes) between cycles. The runner uses a 50ms tick
between checks, keeping shutdown responsive without busy-looping.

Currently there is exactly one stream: the `fix-pipeline` stream, which
calls the engine's drain loop on each cycle.

### Cost and Session Limits

Night-shift enforces its own cost ceiling, set conservatively at 50% of the
configured maximum. This headroom accounts for the unpredictability of
autonomous operation — a large backlog of issues could trigger a cascade of
fix pipelines, each consuming tokens. The 50% threshold provides a safety
margin.

Session limits are also enforced. Both limits trigger graceful shutdown:
the engine finishes any in-flight work, emits final statistics, and exits.

### Graceful Shutdown

The engine responds to SIGINT and SIGTERM. The first signal sets a shutdown
flag that prevents new phases from starting and allows in-flight work to
complete. A second signal exits immediately with code 130. This matches the
two-stage shutdown behavior of the spec-driven orchestrator.

### State

The engine maintains runtime state: cumulative cost, session count, and
issues fixed. This state is transient — it exists only for the lifetime of
the daemon process. Persistent state lives in the platform (GitHub issues
with labels) and the repository (code changes on the integration branch).

---

## Interaction with the Spec Pipeline

Night-shift and the spec pipeline are designed to coexist but not to run
simultaneously. Night-shift operates on the integration branch and creates
fix branches that merge back into it. The spec pipeline also targets the
integration branch. Running both concurrently would create merge contention.

The intended workflow is:

- During active development: run the spec pipeline (`af code`) to
  implement features.
- During off-hours: run the standalone `nightshift` CLI to process
  fix issues.
- The merge lock ensures that if both do run concurrently, they serialize
  their merge operations rather than corrupting the branch.

Night-shift issues are visible on the platform alongside human-filed issues.
A human reviewing the repository sees a unified view of both feature work
(from specs) and maintenance work (from night-shift), with clear labels
(`af:fix` for approved repairs) distinguishing the two.

---

*Previous: [Execution and Archetypes](03-execution-and-archetypes.md)*
*Next: [Knowledge System Architecture](05-knowledge-system-architecture.md)*
