# Night-Shift Mode

## Purpose and Placement

Night-shift is a fix-only maintenance daemon that runs continuously,
processing `af:fix`-labelled GitHub issues without human intervention. While
the spec-driven pipeline ([Parts 1–3](01-spec-authoring.md)) implements
features from authored specifications, night-shift operates in the opposite
direction: it picks up issues filed against the codebase and generates the
fixes needed to resolve them.

The two modes are complementary. The spec pipeline builds new capabilities
by executing a human-authored plan. Night-shift maintains the codebase by
fixing issues that have been triaged and labelled for automatic repair. The
fix pipeline reuses the same session infrastructure — Claude agents in
isolated workspaces — but with automatically generated specs rather than
human-authored ones.

---

## Conceptual Model

Night-shift operates as a single-stream fix loop: it polls GitHub for
issues labelled `af:fix`, determines a safe processing order using
dependency analysis, and executes a three-stage pipeline
(Triage → Coder → Reviewer in fix-review mode) for each issue.

The fix phase runs on a timer (default: every fifteen minutes) because it
is lightweight — it queries GitHub for labelled issues and dispatches fix
pipelines. The fix phase fires immediately on startup (so the first fix
attempt happens without waiting for the timer interval) and then repeats
at its configured interval.

---

## The Fix Phase

### Issue Selection and Triage

The fix phase queries GitHub for open issues with the `af:fix` label.
A human must review issues and apply the `af:fix` label to approve
automated repair.

When three or more fixable issues exist, the system performs batch triage
using an LLM. The triage analysis serves three purposes:

**Dependency detection.** Some issues depend on others — fixing a type error
may require first fixing the deprecated API usage that introduced it. The
triage stage identifies these edges from three sources: explicit text references
in issue bodies ("depends on," "blocked by," "after," "requires"), GitHub
cross-references from the timeline API, and LLM-inferred dependencies based
on the issue descriptions.

**Supersession detection.** Some issues become obsolete when another is fixed.
The triage stage identifies these pairs and closes the obsolete issue before
processing begins, preventing wasted work.

**Processing order.** The dependencies form a graph. Kahn's topological sort
(with tie-breaking by issue number) produces a safe processing order that
respects dependencies. If the dependency graph contains cycles, the system
breaks them before sorting.

For fewer than three issues, triage is skipped and issues are processed in
creation-date order.

### The Fix Pipeline

Each issue passes through a three-stage pipeline:

1. **Triage analysis.** A Maintainer agent in fix-triage mode analyzes the
   issue: identifies root cause, affected files, and produces structured
   acceptance criteria. The triage report is posted as a comment on the
   GitHub issue. These criteria are injected into both the coder and
   reviewer prompts.

2. **Coder implementation.** A Coder agent implements the fix on an isolated
   branch. The branch name includes the issue number and a sanitized slug
   derived from the title (`fix/{issue-number}-{slug}`). The system prompt
   contains the full issue body and triage criteria; the task prompt directs
   the agent to fix the described problem.

3. **Reviewer validation.** A Reviewer agent in fix-review mode reviews the
   patch for correctness and quality. If the review identifies issues, the
   pipeline loops back to the Coder with review feedback, up to the
   configured retry limit.

The coder-reviewer loop starts at the STANDARD model tier and escalates to
ADVANCED on repeated failures, controlled by `routing.retries_before_escalation`.

All sessions share the same fix branch, which is created from the
current integration branch HEAD. After the pipeline completes successfully, the fix
branch is harvested into the integration branch using the same squash-merge strategy as
the spec-driven pipeline (squash merge, with merge agent on conflict).
The originating issue is labelled `af:fixed` and closed with a comment
pointing to the fix branch. If the coder produces no commits, the issue
receives an `af:no-change` label instead, signalling the need for human
review.

If any stage fails, the issue receives a failure comment with the branch name
for manual recovery. The branch is preserved — the work done before the
failure is not discarded.

### Drain Behavior

The fix phase does not process one batch of issues per interval. Instead, a
drain loop re-polls GitHub after each fix and continues processing until zero
`af:fix` issues remain, with a safety valve of 50 iterations. A `seen` set
prevents re-processing recently closed issues that the API may still return
due to eventual consistency. The drain loop respects cost limits, session
limits, and shutdown signals between iterations. This means starting
night-shift with many `af:fix` issues will process all of them in rapid
succession rather than spacing them across intervals.

### Spec Construction

The fix pipeline generates a lightweight in-memory spec from the issue rather
than writing spec files to disk. This spec contains a task prompt (assembled
from the issue title and body), system context (the full issue body for
reference), and the fix branch name. This avoids polluting `.agent-fox/specs/` with
ephemeral repair specifications that do not represent lasting feature work.

---

## Engine Lifecycle

### Startup

On startup, the engine validates that a platform is configured (GitHub is
required for issue management), initializes the platform client, and runs
the issue check immediately. This ensures that the first fix cycle happens
without waiting for the timer interval.

### Event Loop

The engine runs a 50-millisecond tick loop. On each tick, it checks elapsed
time for the issue-check timer. When the timer exceeds its configured
interval, the fix phase fires and the timer resets. The short tick keeps
shutdown responsive without busy-looping. This is simpler and more
predictable than a scheduler-based approach — the engine always knows
exactly when the next phase will fire.

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

## Staleness Detection

After completing a round of fixes, the engine checks whether any remaining
open issues have become stale. A fix to one issue may resolve problems
reported in another — for example, fixing a deprecated API usage might also
resolve the linter warning that flagged it. Staleness detection re-evaluates
open issues against the current codebase state and closes those that no
longer apply.

---

## Labels

Night-shift uses GitHub labels to manage its fix workflow lifecycle:

| Label | Applied by | Meaning |
|-------|-----------|---------|
| `af:fix` | User | Issue eligible for automatic fixing |
| `af:fixed` | Fix pipeline | Fix successfully merged into integration branch |
| `af:no-change` | Fix pipeline | Coder produced no commits; needs human review |

All labels are automatically created on the GitHub repository by
`agent-fox init` when a `[platform]` section is configured.

---

## Interaction with the Spec Pipeline

Night-shift and the spec pipeline are designed to coexist but not to run
simultaneously. Night-shift operates on the integration branch and creates fix branches
that merge back into it. The spec pipeline also targets the integration branch.
Running both concurrently would create merge contention.

The intended workflow is:

- During active development: run the spec pipeline (`agent-fox code`) to
  implement features.
- During off-hours: run the standalone `night-shift` CLI to process
  fix issues.
- The merge lock ensures that if both do run concurrently, they serialize
  their merge operations rather than corrupting the branch.

Night-shift issues are visible in GitHub alongside human-filed issues. A human
reviewing the repository sees a unified view of both feature work (from specs)
and maintenance work (from night-shift), with clear labels (`af:fix` for
approved repairs) distinguishing the two.

---

*Previous: [Execution and Archetypes](03-execution-and-archetypes.md)*
*Next: [Knowledge System Architecture](05-knowledge-system-architecture.md)*
