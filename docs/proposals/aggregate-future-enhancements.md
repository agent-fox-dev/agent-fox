# Aggregate: Future Enhancements

| | |
|---|---|
| **Status** | Draft |
| **Date** | 2026-07-07 |
| **Sources** | `evolution-analysis.md`, `game-changer-features-prd.md`, `agent-fox-v2-prd.md` |

This document consolidates every proposed new capability and enhancement for
af across the proposal documents. Organized from lowest-effort
highest-ROI (finishing half-built features) through strategic bets to
exploratory ideas.

---

## 1. Finish What's Already Built (Highest ROI)

These are not new features — they are gaps between what the config, graph,
and session layers already declare and what the runtime actually does. The
scaffolding, config fields, and in some cases unit-tested algorithms already
exist. Each is "wire up existing code," the cheapest class of game-changer.

### 1.1 Multi-Instance Reviewer/Verifier Fan-Out

**Current state:** `archetypes.instances.reviewer = 2` is accepted, the
graph node stores an `instances` count, `NodeSessionRunner` computes
`clamp_instances` and stores it on `self._instances` — but never reads it.
Convergence-merging algorithms for combining multiple reviewer outputs
already exist and are unit-tested in `session/convergence.py`.

**Finished state:** `NodeSessionRunner.execute` launches N parallel sessions
for a node and merges results via existing convergence code. "Run 2
reviewers and take the union of critical findings" becomes a real quality
lever instead of a config lie.

**Effort:** Medium. **Impact:** Direct upgrade to review quality with zero
new user-facing surface.

### 1.2 Duration-Aware Scheduling

**Current state:** `graph/analyzer.py` computes parallelism phases and a
critical path for `af plan --dry-run`. `GraphSync.ready_tasks` accepts an
optional `duration_hints` parameter and computes fan-out weights — but
dispatch never passes duration hints in.

**Finished state:** The orchestrator uses historical session duration
(already logged per-node in `session_outcomes`) to bias which ready task
gets the next free parallel slot toward the one on the critical path,
shortening wall-clock time without changing cost.

**Effort:** Low-medium. **Impact:** Faster `af code` wall-clock time.

### 1.3 Adaptive Model-Tier Routing

**Current state:** `docs/config-reference.md` describes `[routing]` as
"adaptive model routing... escalates to a more capable model tier based on
past session outcomes." The code only implements timeout retry parameters
(`max_timeout_retries`, `timeout_multiplier`). No tier-escalation logic
exists.

**Finished state:** Escalate model tier after N non-timeout failures on the
same node, using the failure classification the retry tree already computes.
Directly addresses open issue #689 (`task_budget` for model-aware token
pacing).

**Effort:** Medium. **Impact:** Better quality/cost tradeoff automatically.

### 1.4 Night Shift Triage-to-Model Routing

**Current state:** `assessed_complexity` is parsed from every triage
response but discarded before reaching `CoderReviewerLoop`, which uses a
fixed model tier for every fix regardless of assessed difficulty.

**Finished state:** Route `assessed_complexity` into model-tier selection —
trivial fixes get STANDARD, complex ones get ADVANCED automatically, without
human override.

**Effort:** Low. **Impact:** Smarter cost allocation for Night Shift.

---

## 2. Cost and Performance Intelligence

### 2.1 Session Cost Estimator at Spec-Authoring Time

Before `af plan` runs, project session count and expected cost per task
group from historical `session_outcomes` data for similarly-shaped groups.
Surfaces via `spec estimate` or as part of `spec generate` output. Directly
targets the root cause identified in archived spec `08` (oversized task
groups produced $20+ sessions).

### 2.2 `task_budget` Token Pacing (Issue #689)

Cap not just dollar spend but pacing within a session so a coder doesn't
burn 80% of its turn budget in the first two tool calls. Already requested
as an open issue.

### 2.3 Cheap-Model-First with Automatic Promotion

Start every coder session on STANDARD. If the first attempt fails for a
reason the retry classifier tags as "capability, not flakiness" (already
distinguished in `_handle_failure`'s seven-way tree), promote immediately
instead of retrying at the same tier. Extension of §1.3.

**Why it matters:** The cost of a failed cheap attempt is much less than
the savings of many successful cheap attempts. Estimated 40-60% cost
reduction for well-specified tasks.

### 2.4 Per-Spec Budget Allocation

Large specs shouldn't starve smaller ones running in parallel. Allocate
budget per-spec rather than only via global `max_budget_usd`.

---

## 3. Observability and Self-Healing

### 3.1 Real-Time TUI Dashboard

A terminal UI (Textual or Rich Live) showing:

- **Live DAG** — nodes colored by status, real-time progress
- **Session streams** — what each agent is doing right now
- **Cost meter** — running total with per-session breakdown and budget bar
- **Intervention controls** — pause, skip, force-retry, adjust budget mid-run
- **Timeline** — scrollable history of completions and failures

**Quick-win variant:** Before a full TUI, add `af code --live` using Rich
Live to show a real-time table of node statuses with cost. ~200-300 lines
using existing DuckDB state.

**Why it matters:** Trust. The #1 reason users babysit AI agents is lack of
visibility. A dashboard turns "fire and pray" into "fire and monitor."

### 3.2 `af status` Workspace Dashboard

All primitives already exist in `workspace/health.py` and
`workspace/integration.py` (worktree list, merge-lock holder,
integration-branch divergence, credential probe). There is simply no
command that surfaces them together. Fastest way to answer "is the daemon
healthy?" without reading logs.

**Effort:** Low — pure composition, no new state.

### 3.3 Unified Run Timeline (`af trace`)

Understanding "why is node X blocked" requires cross-referencing
`af insights`, the DuckDB `audit_events` table, and raw JSONL trace files.
A single `af trace <node_id>` command that assembles dispatch decisions,
retry classifications, knowledge injections, and tool-call events into one
chronological view.

### 3.4 Stall Auto-Diagnosis

`GraphSync` detects stalls but exits with a bare `STALLED` status. The
orchestrator has everything it needs (blocking findings, dependency graph,
node states) to say *why*: "spec X is blocked on 2 critical drift findings;
run `af insights --spec X`."

### 3.5 Self-Healing Pipeline (Micro-Fix Sessions)

Instead of retrying the entire task group on failure:

1. **Analyze the failure** — classify: test failure, type error, lint error,
   merge conflict, timeout
2. **Generate a micro-spec** — minimal, targeted spec covering only the
   failure
3. **Dispatch a micro-fix session** — short session that gets the original
   diff as context and addresses only the specific failure
4. **Verify with scoped tests** — only the failing tests + fast regression
   check

Current retry burns the full session budget ($1-5+). Micro-fix sessions
would be 10-20x cheaper and more targeted. Models how human developers
actually work.

**Effort:** High. **Impact:** Dramatic cost reduction on retries.

### 3.6 Explain Mode (Auto-Generated Artifacts)

After a successful run, automatically generate:

- PR description with change summary, test plan, risk assessment
- Architecture decision record extracted from session summaries
- Code walkthrough narrating key changes
- Regression risk analysis

Generated by a lightweight post-run session (Haiku-tier, read-only) reading
the diff and session summaries.

**Effort:** Low-medium. **Impact:** Faster code review, bridges the trust gap.

---

## 4. Smarter Knowledge and Cross-Session Learning

### 4.1 Cross-Spec Intelligence

Currently knowledge scopes retrieval to same-spec sessions. Expand with:

- **Code artifact indexing** — after each successful merge, index new
  functions, types, API endpoints, test patterns as structured knowledge
  entries
- **Cross-spec retrieval** — before a session starts, query for relevant
  artifacts from other completed specs that touch related code
- **Pattern learning** — detect recurring patterns across specs ("this
  project always uses Pydantic models with `model_validator`") and inject as
  "project conventions"
- **Anti-pattern learning** — when a coder makes a mistake the verifier
  catches, store the mistake-and-fix pair; warn future sessions

**Why it matters:** Current sessions are amnesiac across specs. Cross-spec
intelligence gives af accumulated project intuition.

### 4.2 Upgrade Retrieval Scoring

Current scoring in `formatting.py` is pure substring overlap and misses
fixes described in different words than the original finding. Move to
BM25 or targeted embeddings scoped to channels that have proven value.

### 4.3 Cross-Issue / Cross-File Learning for Night Shift

Fix issues currently key knowledge under ephemeral `fix-issue-{N}` names
that don't generalize. Key supersession and retrieval by *file path or
module* instead, so "we already tried X here and it didn't work" survives
across unrelated issue numbers.

---

## 5. Night Shift Maturity

### 5.1 Coverage-Regression Gating for Fixes

Match the coverage gate the main coder path enforces
(`result_handler.py`). Today the fix-review reviewer only runs `make check`
with no automated coverage-regression block.

### 5.2 Finding-Convergence Retries

The main engine uses `check_finding_convergence()` to stop retrying when a
reviewer keeps reporting the same unfixable finding. `CoderReviewerLoop`
doesn't, so a fix can loop on an unwinnable finding until it exhausts its
retry budget.

### 5.3 Self-Proposing Backlog

Night Shift currently watches for `af:fix` issues. Extend to *notice*
things — recurring lint suppressions, TODO comments older than N days,
coverage regressions merged anyway — and draft (never auto-merge) a
spec/PRD for human approval using the `af-spec` skill pipeline.

**Why it matters:** Makes the maintenance daemon proactive rather than
purely reactive, without removing the human approval gate.

---

## 6. Developer Experience and Control Plane

### 6.1 Live Spec-from-Conversation

The biggest friction point: let users describe what they want in natural
language directly in their Claude Code session:

> "I want a REST API for user preferences with validation, caching, and
> audit logging"

The enhanced `/af-spec` skill should:

1. Analyze the codebase in real-time (imports, patterns, existing APIs)
2. Generate a complete spec pack in one shot — no refine loop, no answer files
3. Show a rendered preview for conversational iteration
4. Auto-run `af plan` + `af code` on approval

Entire flow from idea to running code: one sentence, one approval, done.

**Why it matters:** Agent-fox starts from specs — its superpower — but only
if spec creation is frictionless. Today the spec workflow is the bottleneck.

### 6.2 Pre-Plan Quality Gate

`af plan` builds a graph without calling `afspec.validate()`. A structurally
invalid spec only surfaces when a coder session fails downstream. Have
`af plan` refuse to plan unless every discovered spec validates cleanly
(hard errors only, warnings pass).

### 6.3 Cross-Spec Dependency Visualizer

`af plan --dry-run` already computes phases and a critical path;
`afspec.discovery` builds a cross-spec dependency graph. Export as Mermaid
or JSON graph (`spec validate --cross --graph`) to let users sanity-check
parallelism before authoring specs that serialize each other.

### 6.4 Human-Facing Spec Preview

The `spec new → refine → generate` loop is JSON-on-stdout for agents. A
`spec render --combined --watch` mode (or minimal local web preview) that
humans can read comfortably would shorten refinement cycles.

### 6.5 Incremental Execution

- `af code --changed` — detect which specs modified since last successful
  run (content hash diff) and only execute those subgraphs
- **Smart resume** — after a crash, resume from exactly where things stopped
  (partially built — `in_progress` nodes reset to `pending`, but worktrees
  with partial work are destroyed)
- **Worktree preservation on failure** — keep failed worktrees so the next
  attempt continues from partial work

**Why it matters:** Large projects with 10+ specs pay linear cost even when
one spec changed. This makes execution sublinear.

---

## 7. Strategic Bets

### 7.1 GitHub-Native Mode

A GitHub Action that:

1. Watches for spec changes in PRs and runs `af plan` + `af code` on a runner
2. Posts results as PR comments (standup report, findings, costs)
3. Creates fix PRs from `af:fix` issues automatically (replaces local Night Shift)
4. Responds to `@af fix this` in PR review comments

**Why it matters:** Teams can try af without local installation.
CI/CD and code review integration. Non-Python teams can use it.

### 7.2 PR-Native Review Mode

For teams requiring human review before merge, let
`workspace.integration_branch` optionally mean "open a draft PR and wait
for approval" instead of "squash-merge directly." Primarily a `harvest.py`
policy change, not a new subsystem.

### 7.3 Fleet Mode

Run the same orchestrator across multiple repos a team owns — shared budget
ceiling, shared standup report, shared knowledge about cross-repo API
contracts. Turns af from a per-project tool into an engineering-org
control plane. Enabled by `af standup --json` and `af insights --json`
already emitting structured, aggregable output.

### 7.4 Simulation Mode

Before spending real budget, run a spec through a cheap/fast model that
estimates: task count, session count, file-overlap/merge-conflict risk
(using file footprint data `planning.file_conflict_detection` already
computes), and cost range. Surfaces as `af plan --simulate`.

### 7.5 Spec Fuzzing

After spec generation but before coding, run an adversarial "spec fuzzing"
pass:

1. Generate edge cases from requirements (boundary values, empty inputs,
   concurrent access)
2. Challenge assumptions ("What if the DB is empty? What if two users hit
   this simultaneously?")
3. Find contradictions between requirements and codebase
4. Auto-generate additional test contracts

Implement as a new Reviewer mode (`fuzz-review`) at `auto_pre`.

**Why it matters:** Catches spec gaps before coding — when fixing is cheapest.

---

## 8. Exploratory Ideas

These are genuinely ambitious bets that each deserve their own PRD and a
design partner before committing engineering time.

### 8.1 Multi-Agent Collaboration ("The Fox Pack")

Allow task groups to be designated as "collaborative":

- A coordinator agent decomposes the work
- Worker agents execute sub-tasks in parallel worktrees
- The coordinator verifies contract compatibility across outputs
- Cross-agent type checking ensures interfaces agree

**Why it matters:** Unlocks features spanning multiple modules or services.
**Effort:** Very high — requires a new execution model.

### 8.2 Live Pairing / Mid-Session Steering

Surface the live Claude Agent SDK stream for an in-progress worktree session
and let a human inject a steering message before the next tool call. Would
recover wasted budget that the retry system currently cleans up after the
fact.

### 8.3 Human-in-the-Loop Async Handoffs

When an agent hits a complex architectural issue or budget cap, it pauses
its worktree, sends a notification (Slack, Discord, IDE extension), and asks
a specific question. Blends autonomous execution with human intuition.

### 8.4 Ephemeral Preview Environments

For frontend/full-stack tasks, spin up an ephemeral preview environment
(Docker or Vite) on a local port when a task group completes. Standup
report includes `Preview ready at http://localhost:3001`.

---

## 9. Recommended Sequencing

| Tier | Initiative | Rationale |
|---|---|---|
| **Now** | Finish multi-instance fan-out (§1.1) | Scaffolding, config, convergence already exist and are tested |
| **Now** | `task_budget` pacing (§2.2, #689) | Open issue, directly reduces known failure mode |
| **Now** | `af status` dashboard (§3.2) | All primitives exist; pure composition |
| **Now** | Stall auto-diagnosis (§3.4) | Small change, big UX improvement |
| **Next** | Session cost estimator (§2.1) | Targets root cause from archived spec `08` |
| **Next** | Night Shift coverage gating + convergence (§5.1, §5.2) | Parity with main engine |
| **Next** | Pre-plan quality gate (§6.2) | Closes real correctness gap |
| **Next** | Adaptive model routing (§1.3) | Docs already promise it; build or correct |
| **Next** | Explain mode (§3.6) | Low effort, high trust impact |
| **Next** | `af code --live` quick-win dashboard (§3.1) | ~300 lines, immediate visibility |
| **Later** | Live spec-from-conversation (§6.1) | High impact but medium-high effort |
| **Later** | Self-healing micro-fix pipeline (§3.5) | High impact but high effort |
| **Later** | Cross-spec intelligence (§4.1) | Medium effort, transformative |
| **Later** | GitHub-native mode (§7.1) | Team adoption unlock |
| **Later** | Incremental execution (§6.5) | Sublinear scaling |
| **Exploratory** | Fleet mode, multi-agent collaboration, live pairing, spec fuzzing, simulation mode | Each needs its own PRD and design partner |

**Guiding principle:** Start with the "Now" items — all are
finish-the-wiring or small-composition work, all dogfoodable on agent-fox's
own `main` branch, and together they address the two structural themes
repeating across the issue tracker: **wasted budget** and **operator
visibility**.
