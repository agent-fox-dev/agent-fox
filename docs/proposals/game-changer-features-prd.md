# PRD: What Else? Game-Changing Features for agent-fox

| | |
|---|---|
| **Status** | Draft — for discussion |
| **Author** | Codebase analysis (agent-assisted), reviewed against source at commit `cc816a8c` |
| **Date** | 2026-07-07 |
| **Supersedes** | `docs/agent-fox-v2-prd.md` §4 (that document's ideas — a web dashboard, chat-based handoffs, PR-native workflow, preview environments, "chaos engineering" — are directionally reasonable but were proposed without checking what already exists; several are partially built already, and this document identifies exactly which wiring is missing rather than re-proposing the feature from zero) |
| **Companion** | [`simplification-prd.md`](simplification-prd.md) — the "make it simpler" half of this exercise |

## 1. Executive Summary

The single biggest opportunity this audit found is not a new feature to
build — it's **several already-designed features that were never finished
being wired up**. The config schema, the graph model, and the retrieval
layer all have fields, tables, and function signatures for capabilities
that the runtime silently ignores. Finishing these is lower-risk and
faster to ship than anything net-new, and should be the first wave of
"game-changer" work before any blue-sky idea below.

Beyond that, this document organizes ideas into six pillars, grounded in
(a) what the code already half-supports, (b) what the project's own open
issues are already asking for, and (c) genuinely new capability bets. Each
pillar ends with a prioritized shortlist; §5 gives an overall
recommendation and sequencing.

## 2. Evidence Base

- **Open GitHub issues already pointing this direction:** #689
  ("support `task_budget` for model-aware token pacing"), #669
  ("investigate merge conflict rate in parallel spec execution"), #666
  ("per-archetype built-in tool restrictions").
- **Half-wired features found during the codebase audit** (see §3.1): the
  graph model, config schema, and orchestrator all carry the scaffolding
  for multi-instance reviewer/verifier fan-out, duration-aware scheduling,
  and adaptive model-tier routing — none of which currently execute their
  full intended behavior.
- **Recurring pain in the archived-spec history:** context injection has
  been pruned and re-enriched three separate times (archived specs `10`,
  `11`, `12`) — a strong signal that "what knowledge should a session see"
  is still an unsolved product problem, not just an implementation detail.
- **The project self-hosts.** af already builds agent-fox. This is
  a unique asset: every feature idea below can be dogfooded on the
  project's own `main` branch before it ships to users.

## 3. The Six Pillars

### 3.1 Pillar 1 — Finish What's Already Built (highest ROI, do first)

These are not proposals for new architecture. They are gaps between what
the config/graph/session layers already declare and what the runtime
actually does.

| Gap | Current state | What "finished" looks like |
|---|---|---|
| **Multi-instance reviewer/verifier fan-out** | `archetypes.instances.reviewer = 2` is accepted, the graph node stores an `instances` count, `NodeSessionRunner` computes `clamp_instances` and stores it on `self._instances` — but never reads it. Convergence-merging algorithms for combining multiple reviewer outputs already exist and are unit-tested in isolation (`session/convergence.py`). | `NodeSessionRunner.execute` actually launches N parallel sessions for a node and merges results via the existing convergence code. This turns "run 2 reviewers and take the union of critical findings" from a config lie into a real quality lever. |
| **Duration-aware scheduling** | `graph/analyzer.py` already computes parallelism phases and a critical path for `af plan --dry-run`'s analysis output. `GraphSync.ready_tasks` accepts an optional `duration_hints` parameter and computes fan-out weights — but dispatch never passes duration hints in. | The orchestrator uses historical session duration (already logged per-node in `session_outcomes`) to bias which ready task gets the next free parallel slot toward the one on the critical path, shortening wall-clock time for a full `af code` run without changing cost. |
| **Adaptive model routing** | `docs/config-reference.md` describes the `[routing]` section as "adaptive model routing... escalates to a more capable model tier based on past session outcomes." The code only implements *timeout* retry parameters (`max_timeout_retries`, `timeout_multiplier`) — there is no tier-escalation-on-failure-history logic anywhere. | Either implement the escalation the docs describe (escalate model tier after N non-timeout failures on the same node, using the failure classification the retry tree already computes), or correct the docs. Given #689 (`task_budget` pacing) is an open ask for exactly this kind of budget/quality tradeoff, building it is the higher-value choice. |
| **Night Shift triage complexity signal** | `assessed_complexity` is already parsed out of every triage response (`review_parser.py`) but is discarded before reaching `CoderReviewerLoop`, which uses a fixed model tier for every fix regardless of assessed difficulty. | Route `assessed_complexity` into model-tier selection for the fix coder/reviewer — trivial fixes get SIMPLE/STANDARD, complex ones get ADVANCED automatically, without a human ever setting an override. |

**Why this is pillar 1:** each of these is "wire up code that already
exists and is already tested in isolation" rather than "design and build
something new." That is the cheapest kind of game-changer available.

### 3.2 Pillar 2 — Cost & Performance Intelligence

Building on Pillar 1's routing/scheduling work, but going further:

- **Session cost estimator at spec-authoring time.** `tasks.json`
  already carries per-group test-spec reference counts and `kind`. Before
  `af plan` even runs, `spec generate` (or a new `spec estimate` command)
  could project session count and expected cost per task group from
  historical `session_outcomes` data for similarly-shaped groups — directly
  addressing the root cause identified in archived spec `08`
  (oversized task groups produced $20+ sessions; the fix so far has only
  been prompt-level guidance to avoid it, not a way to *see it coming*).
- **`task_budget` token pacing** (already requested, #689) — cap not just
  dollar spend but pacing within a session so a coder doesn't burn 80% of
  its turn budget in the first two tool calls.
- **Cheap-model-first with automatic promotion.** Start every coder session
  on STANDARD; if the first attempt fails for a reason the retry
  classifier tags as "capability, not flakiness" (already distinguished in
  `_handle_failure`'s seven-way tree), promote immediately instead of
  retrying at the same tier. This is a direct extension of Pillar 1's
  routing gap.
- **Per-spec budget allocation**, not just a global `max_budget_usd` —
  large specs shouldn't be able to starve smaller ones running in parallel.

### 3.3 Pillar 3 — Observability & Self-Healing

- **Unified run timeline.** Today, understanding "why is node X blocked"
  requires cross-referencing `af insights`, the DuckDB `audit_events`
  table, and raw JSONL trace files by hand. A single `af trace <node_id>`
  command that assembles dispatch decisions, retry classifications,
  knowledge injections, and tool-call events into one chronological view
  would collapse three data sources most users don't know exist into one
  answer.
- **`af status` / workspace dashboard.** All the primitives already exist
  in `workspace/health.py` and `workspace/integration.py` (worktree list,
  merge-lock holder, integration-branch divergence, credential probe) —
  there is simply no command that surfaces them together. This is the
  single fastest way to answer "is the daemon actually healthy right now?"
  without reading logs.
- **Stall auto-diagnosis.** `GraphSync` already detects stalls (no ready
  tasks, work remains) and exits with a `STALLED` status. The orchestrator
  has everything it needs (blocking findings, dependency graph, node
  states) to *say why* in the exit message — "spec X is blocked on 2
  critical drift findings; run `af insights --spec X`" — instead of a bare
  stall code the user has to investigate manually.
- **Drift dashboard.** PRD `intent_hash`, drift findings, and
  file-based supersession (archived spec `12`) are all pieces of
  "does the codebase still do what the spec intended?" No command
  currently answers that question holistically across a whole spec's
  lifetime — `af insights` only shows currently-active findings, not the
  story of how a spec's assumptions evolved.

### 3.4 Pillar 4 — Smarter Knowledge & Cross-Session Learning

Approached carefully, given the project already over-corrected once here
(archived spec `10` removed 5 of 8 retrieval channels because they
retrieved nothing useful — any new retrieval surface must prove its value,
not just add more injection).

- **Upgrade retrieval scoring from keyword-overlap to something
  paraphrase-tolerant** (e.g. a lightweight BM25 pass, or embeddings scoped
  strictly to the channels that already prove out) — the current scoring
  in `formatting.py` is pure substring overlap and will miss a fix
  described in different words than the original finding.
- **Give Night Shift real knowledge parity with the main engine**
  (also listed in the simplification PRD as P2.1, because it's
  simultaneously a bug fix and a capability gap): once `touched_files` and
  session summaries actually flow from fix sessions, repeat fix attempts
  on the same issue — and future fixes to the same files — get real
  institutional memory instead of starting blind every time.
- **Cross-issue / cross-file learning for Night Shift.** Fix issues
  currently key knowledge under ephemeral `fix-issue-{N}` names that don't
  generalize. Keying supersession and retrieval by *file path or module*
  instead would let "we already tried X here and it didn't work" survive
  across unrelated issue numbers.

### 3.5 Pillar 5 — Night Shift Maturity

Night Shift is explicitly called out by the user's own prior working notes
(`prompts.md`) as "VERY important for the next iteration of agent-fox."
Beyond the knowledge-parity work above:

- **Coverage-regression gating for fixes**, matching what the main coder
  path already enforces (`result_handler.py`'s coverage gate) — today the
  fix-review reviewer is only prompted to "run `make check`," with no
  automated coverage-regression block the way spec-driven coding sessions
  get.
- **Finding-convergence retries in the fix loop.** The main engine uses
  `check_finding_convergence()` to stop retrying when a reviewer keeps
  reporting the same unfixable finding; `CoderReviewerLoop` doesn't, so a
  fix can loop on an unwinnable finding until it exhausts its retry budget
  for no reason.
- **Unify `agentfox.fix` (local check-failure clustering) and
  `agentfox.nightshift` (GitHub issue fixing)** so a human who files an
  `af:fix` issue after `make check` failed locally gets Night Shift's fix
  informed by the local clustering/repair attempt that already happened,
  instead of two disconnected systems solving the same failure twice.

### 3.6 Pillar 6 — Developer Experience & Control Plane

- **Unify the three CLIs.** Also listed in the simplification PRD (P2.4)
  because it cuts both ways: fewer binaries to teach, and one consistent
  `--json`/config/error-envelope story. `af spec new`, `af fix daemon`
  (Night Shift under `af`), with the standalone `spec`/`nightshift` binaries
  kept as thin deprecated aliases for one release.
- **Pre-plan quality gate.** `af plan` today builds a graph from specs
  without ever calling `afspec.validate()` — a structurally invalid spec
  only surfaces if someone remembers to run `spec validate` first, or when
  a coder session fails downstream. Have `af plan` refuse to plan (hard
  errors only, warnings still pass) unless every spec it discovers
  validates cleanly.
- **Cross-spec dependency visualizer.** `af plan --dry-run` already
  computes phases and a critical path; `afspec.discovery` already builds a
  cross-spec dependency graph for validation. Exporting that as Mermaid or
  a JSON graph (`spec validate --cross --graph`) would let a human sanity-check
  the intended parallelism *before* authoring five specs that turn out to
  fully serialize each other.
- **A genuinely human-facing spec preview.** The `spec new → refine →
  generate` loop is JSON-on-stdout, built for agents driving it from a
  skill. A single `spec render --combined --watch` mode (or a minimal local
  web preview) that a human can read comfortably while iterating on
  `spec refine` answers would shorten the refinement cycles that keep
  spinning up new specs whenever a PRD turns out to be underspecified
  (see archived specs `03`, `05`, `08` — all partly a reaction to painful
  agent-facing-only iteration).

## 4. Bigger Bets — "Crazy Ideas"

The user explicitly asked for ideas that go beyond incremental
improvement. These are bigger swings, ordered roughly by ambition:

1. **Fleet mode.** af currently reasons about one repo. A thin
   coordination layer that runs the *same* orchestrator concept across
   multiple repos a team owns — shared budget ceiling, shared standup
   report, shared knowledge about cross-repo API contracts — turns
   af from a per-project tool into an engineering-org control
   plane. (Directly enabled by the fact that `af standup --json` and
   `af insights --json` already emit structured, aggregable output.)

2. **Simulation mode.** Before spending real budget, run a spec through a
   cheap/fast model that only estimates: task count, expected session
   count, expected file-overlap/merge-conflict risk (using the same file
   footprint data `planning.file_conflict_detection` already computes),
   and a cost range — surfaced as `af plan --simulate`. This turns "how
   much will this cost and how likely is it to fight itself" from a
   post-hoc standup surprise into a pre-flight check, and it's a natural
   extension of the cost-estimator idea in Pillar 2.

3. **Self-proposing backlog.** Night Shift already watches for `af:fix`
   labelled issues. A natural extension: let it also *notice* things —
   recurring lint suppressions, TODO comments older than N days,
   coverage regressions that were merged anyway — and draft (never
   auto-merge) a spec/PRD for human approval, using the exact same
   `af-spec` skill pipeline a human would use. This makes the maintenance
   daemon proactive rather than purely reactive, without removing the
   human approval gate that keeps it safe.

4. **Live pairing / mid-session steering.** Today, redirecting a coder
   that's heading the wrong way means waiting for it to fail, then editing
   the spec, then re-running via `reset`. An "attach" mode — surface the
   live Claude Agent SDK stream for an in-progress worktree session and let
   a human inject a single steering message before the session's next tool
   call — would recover a lot of the wasted budget the retry-classification
   system in `result_handler.py` currently has to clean up after the fact.

5. **PR-native review mode as a first-class alternative to squash-to-main.**
   For teams that require human review before merge, let
   `workspace.integration_branch` optionally mean "open a draft PR and wait
   for approval" instead of "squash-merge directly." The merge-lock and
   harvest machinery already isolate work per-branch; this is primarily a
   `harvest.py` policy change, not a new subsystem — much smaller than it
   sounds once Pillar 1/simplification's `merge_lock.py` split (see
   companion PRD, P1.5) has already separated locking from merge logic.

## 5. Prioritized Shortlist & Recommendation

| Tier | Initiative | Why it's here |
|---|---|---|
| **Now** | Finish multi-instance reviewer/verifier fan-out (§3.1) | Scaffolding, config, and convergence algorithms already exist and are tested; this is the cheapest true "game changer" available — it upgrades review quality with zero new user-facing surface. |
| **Now** | `task_budget` pacing (§3.2, #689) | Already requested by a real open issue; directly reduces the "burn budget in the first two tool calls" failure mode already tracked in the retry classifier. |
| **Now** | `af status` workspace dashboard (§3.3) | All primitives exist in `health.py`/`integration.py`; pure composition, no new state. |
| **Next** | Session cost estimator at authoring time (§3.2) | Directly targets the root cause identified in archived spec `08`, which prompt-level guidance alone hasn't fully solved. |
| **Next** | Night Shift knowledge parity + coverage gating (§3.4, §3.5) | Shared with the simplification PRD's P2.1 — fixing the bug *is* the feature. |
| **Next** | Pre-plan quality gate (§3.6) | Closes a real correctness gap (`af plan` can plan invalid specs today) with a small, well-scoped change. |
| **Later** | Unified `af` CLI (§3.6) | High value, but should follow the simplification PRD's discovery/validation consolidation (P1.2/P1.3) so there's one code path to expose, not three to alias. |
| **Later** | Simulation mode / merge-conflict risk preview (§4.2) | Depends on duration-aware scheduling (Pillar 1) and file-conflict detection actually working against `tasks.json` (simplification PRD §3.1) being fixed first. |
| **Exploratory** | Fleet mode, self-proposing backlog, live pairing, PR-native review | Genuinely new product surface — each deserves its own PRD and a design partner/early-adopter before committing engineering time. |

**Recommendation:** start with the three "Now" items. All three are
finish-the-wiring or small-composition work rather than new architecture,
all three are dogfoodable immediately on agent-fox's own `main` branch,
and together they directly address the two structural themes this audit
found repeating across the issue tracker: **wasted budget** (task pacing,
routing) and **operator visibility** (status dashboard). Sequence the
"Next" tier afterward, and treat the "Exploratory" tier as candidates for
a future planning cycle once there's a specific team or use case pulling
for one of them — building fleet mode or live-pairing speculatively, before
a real user asks for it, is exactly the kind of premature abstraction the
companion simplification PRD argues against.

## Appendix: Evidence Sources

Same source base as the companion simplification PRD (§Appendix there),
plus: `gh issue list --state open` (4 open issues, all cited above), the
project's `docs/architecture/` guide (for the documented-vs-implemented
`[routing]` gap), and the 13 archived specs under
`.agent-fox/specs/archive/` (for historical context on what has already
been tried around context injection, task-group sizing, and CLI
unification).
