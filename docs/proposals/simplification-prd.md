# PRD: Simplify & Accelerate the agent-fox Workflow

| | |
|---|---|
| **Status** | Draft — for discussion |
| **Author** | Codebase analysis (agent-assisted), reviewed against source at commit `cc816a8c` |
| **Date** | 2026-07-07 |
| **Supersedes** | `docs/agent-fox-v2-prd.md` §2–3 (that document was written without reading the source in depth; several of its premises — e.g. that DuckDB or the JSON spec format are the root complexity drivers — are not supported by the evidence below) |

## 1. Executive Summary

agent-fox is a mature, actively self-hosted system (it builds itself: 419
GitHub issues to date, 8 shipped patch releases in the last two days of
history alone, a live `main` branch that the orchestrator itself commits
to). That maturity is visible in the code: a genuinely well-tested
orchestrator, a deliberately evolved spec format (v1.2 → v1.3), and a
knowledge system that has already been pruned once (archived spec `10`
removed 5 of 8 retrieval channels that never fired).

The complexity that remains is **not** architectural over-ambition — the
six-archetype model, the DuckDB store, and the three-package spec toolchain
each earn their keep and were each introduced to solve a real, evidenced
problem (see §3.6). The complexity that *should* be removed is narrower and
more mechanical:

1. **"Extracted" modules that were never actually extracted** — three files
   comment that a concern was pulled out into its own module, but the code
   lives on as a 200–300 line tail section of a different 1,000+ line file.
2. **Parallel implementations of the same concern** that drifted apart
   (three spec-discovery resolvers, two `ProgressDisplay` classes, four
   independent retry-tracking mechanisms, two same-named `TriageResult`
   types).
3. **Config and docs that silently disagree** with the code that reads them
   (two default-value mismatches, four undocumented-but-live fields, one
   config section — `[theme]` — that is fully documented but never wired
   to the code path it claims to control).
4. **Scaffolding for features that were designed but never finished**
   (multi-instance reviewer fan-out, duration-aware scheduling, adaptive
   model routing) — see the companion document,
   [`game-changer-features-prd.md`](game-changer-features-prd.md), for the
   recommendation to *finish* rather than delete these.
5. **Dead code left behind by three already-completed removals** (the
   `verification_results` table, vector/embedding retrieval, PR-creation
   support) whose call sites, config fields, and imports were not fully
   swept.

None of this requires a rewrite. It requires roughly a dozen small,
independently shippable specs. Total estimated payoff: **meaningfully
fewer files a new contributor must hold in their head to make a change
safely**, fewer config-drift bugs (we found two in this pass alone), and a
CLI surface that stops confusing users about which of three binaries to
run.

## 2. Why Now — Evidence of the Problem Class

This is not a one-off audit finding. The project's own issue tracker shows
this class of problem recurring every few weeks:

| Issue | Theme |
|---|---|
| #683 `chore: remove deprecated thinking_mode='enabled' and budget_tokens` | config sprawl |
| #667 `refactor: simplify local config.toml creation and loading` | config sprawl |
| #655 `chore: remove deprecated and unused configuration options` | config sprawl |
| #621 `refactor: remove 5 unused knowledge-system retrieval channels` | dead retrieval paths |
| #650 `chore: remove dead --trace CLI flag` (in one binary, not all) | duplicated CLI surface |
| #626 `chore: delete orphaned _templates/ai_validation folder` | dead templates |
| cc816a8c `refactor: simplify codebase — remove dead code, dedup, decompose large functions` (most recent commit on `main` at time of writing) | exactly this initiative, already in flight |

In other words: engineering already believes in this work and does it
reactively, issue by issue, whenever something breaks or someone notices.
This PRD proposes doing the next pass proactively and completely, using
the four-subsystem audit below as the backlog, instead of waiting for the
next bug report.

## 3. Current State Analysis

Scope: `packages/agentfox/agentfox` (~169k LOC, 608 files — by far the
largest package), plus `af`, `afspec`, `agentspec`, `spec`, `afaudit`,
`nightshift` (~36k LOC combined). All line numbers below are current as of
commit `cc816a8c` (the "Tier 1–3" simplification commit that landed as
this analysis started — see §3.7).

### 3.1 Orchestrator core (`engine/`, `graph/`, `session/`)

**Oversized / "extracted in name only" files:**

| File | Lines | What actually lives there |
|---|---|---|
| `engine/session_lifecycle.py` | 1400 | Full session lifecycle: workspace setup, prompt build, harvest, knowledge ingest |
| `engine/result_handler.py` | 1386 | Retry/timeout/blocking decision tree **+ ~310 lines of inlined coverage measurement** (`detect_coverage_tool`, `measure_coverage`, pytest/go/js parsers) |
| `engine/engine.py` | 1130 | Main dispatch loop **+ ~260 lines of inlined GitHub issue-summary posting** (`parse_source_url`, `post_issue_summaries`) |
| `engine/dispatch.py` | 970 | Dispatch strategies **+ ~175 lines of inlined preflight** (`run_preflight`, `do_tests_pass`) |
| `engine/reset.py` | 1000 | Reset/rollback operations |
| `core/config.py` | 941 | Full pydantic config surface |

The coverage, issue-summary, and preflight blocks each carry a comment
that says they were "inlined from" a separate module — but no such module
exists anymore; they are simply the largest section of someone else's
file. This is the opposite of the stated goal (decomposition) and is the
single highest-leverage cleanup target in the codebase, because it's
low-risk (pure move, not a behavior change) and immediately shrinks the
four largest files in the package by 25–30% each.

**Duplicated logic:**

- Node/archetype/mode lookups are implemented twice
  (`dispatch.py:469-482` and `result_handler.py:106-116`).
- Archetype injection has two near-parallel code paths —
  `graph/builder.py:_inject_archetype_nodes` (build time) and
  `graph/injection.py:ensure_graph_archetypes` (runtime patch) — including
  duplicated audit-review edge-rewiring logic.
- `hot_load.py:_build_nodes_and_edges` reimplements a subset of
  `builder.py:_create_nodes_and_intra_edges` without full injection
  parity.
- **`tasks.md` references survive in a `tasks.json`-only world**: spec
  format v1.3 uses `tasks.json` exclusively, but `graph/file_impacts.py`
  (file-conflict detection), `engine.py`'s issue-summary path, and
  `session/prompt.py`'s task-prompt text all still reference `tasks.md`.
  File-conflict detection (`planning.file_conflict_detection`) is
  therefore silently ineffective for every spec created since the v1.3
  migration — which is also almost certainly *why* it defaults to `false`
  and was never turned on.

**Confirmed dead code** (grep-verified, zero live call sites):

- `AssessmentManager` (`engine.py:93-100`) — explicitly marked
  "DEPRECATED: stub retained for backward-compatible test imports only."
- `self._instances` on `NodeSessionRunner` (`session_lifecycle.py:277`) —
  computed via `clamp_instances`, never read.
- `render_verification_context` (`session/context.py:224-234`) — explicit
  no-op stub since the `verification_results` table was dropped.
- `_BARE_FILE_RE` (`graph/file_impacts.py:34`) — defined, never used by
  `_extract_file_paths`.
- `converge_multi_instance_skeptic` (`review_persistence.py:415`) — called
  only from tests, not from live dispatch.

**Retry-state fragmentation.** Four independent mechanisms track "has this
node failed / how many times": an `attempt_tracker` dict in dispatch, a
`_node_failure_counts` dict in `result_handler.py`, a per-node
`_NodeRetryState` (with separate timeout/audit/workspace/environment
sub-counters), and the `SessionRecord.attempt` field. `_handle_failure`
(`result_handler.py:907-967`) routes through seven special cases before
falling back to generic retry logic. This is functionally correct — the
test suite around it is strong — but it is the hardest part of the
orchestrator to safely modify today, and every review-blocking rule lives
in a *third*, separately-evaluated place (`blocking.py`, three evaluators:
pre-review, drift, audit).

### 3.2 Knowledge system & Night Shift (`knowledge/`, `nightshift/`)

The knowledge system's core lifecycle (create → inject → supersede,
`review_store.py`) is well-designed and appropriately complex for what it
does in the main `af code` path. The problem is that **Night Shift
reimplements agent-fox's session machinery instead of reusing it**, and
the knowledge integration it *does* have is a stub:

```214:244:packages/agentfox/agentfox/nightshift/fix_pipeline.py
    def _ingest_knowledge(
        ...
        context: dict[str, object] = {
            "session_status": session_status,
            "touched_files": [],
            "commit_sha": "",
            ...
```

`touched_files` is always `[]` and no `summary` is ever passed, so file-based
drift supersession never runs and session summaries are never written from
Night Shift sessions. Retrieval also passes `task_group=None`, silently
disabling three of the five configured retrieval caps
(`max_cross_group_items`, `max_cross_spec_items`, `max_summary_items` —
none of which apply). Night Shift additionally reimplements its own
telemetry (`_record_session_to_db`, parallel to `DuckDBSink`), its own
prompt assembly (parallel to `assemble_context`), and its own retry loop
(`CoderReviewerLoop`, parallel to `ResultHandler`) — roughly 1,346 lines in
`fix_pipeline.py` alone doing a smaller version of what `engine/` already
does well.

There are also two unrelated systems both named "fix": `agentfox.fix/`
(local `make check`-failure clustering and repair) and
`agentfox.nightshift/` (the GitHub-issue daemon). They share no code and
the naming collision is confusing on its own.

**Confirmed dead code:**

- `format_verdict_parts` / `sort_verdicts` (`formatting.py`) — no callers;
  belonged to the verification channel removed in migration v26.
- `AssessedComplexity` — parsed from triage output
  (`review_parser.py:841-894`) but never consumed downstream, even though
  the fix pipeline's per-issue triage still requests it.
- The DuckDB VSS (vector similarity search) extension is still loaded at
  startup (`db.py:74-84`) even though the embedding-backed table it served
  was dropped in migration v18 — dead startup cost and a needless failure
  mode if the extension can't load.
- `migrations.py` (1,290 lines) carries the full create→drop history for
  six tables that no longer exist (`memory_facts`, `entity_graph`,
  `verification_results`, `adr_entries`, `gotchas`, `sleep_artifacts`).
  Fresh databases skip this via `_CURRENT_SCHEMA_DDL`, but the migration
  registry itself is now pure maintenance weight with no runtime benefit
  for anyone who isn't upgrading from a pre-v18 database.

### 3.3 Spec tooling (`afspec`, `agentspec`, `spec`, `af`, `afaudit`)

The library-level separation is clean: `agentspec` (AI-powered PRD
authoring) correctly delegates all format concerns to `afspec` (the
canonical v1.3 model/validator/renderer) rather than reimplementing them.
The clutter is concentrated in the **presentation and discovery layers**:

- **Spec discovery is implemented three separate times** with three
  different regexes: `afspec/discovery.py` (`^\d+_[a-z][a-z0-9_]*$`),
  `agentfox/spec/discovery.py` (looser, `^(\d+)_(.+)$`, plus a
  `requirements.json`-exists check), and `spec/cli.py`'s own
  `_resolve_spec`/`_next_prefix` (looser still). These can silently accept
  or reject different sets of directories depending on which resolver a
  given code path happens to call.
- **`spec/cli.py` (817 lines) re-implements validation presentation**
  instead of consuming `afspec.validate()`'s output directly — it reaches
  into `afspec.validation`'s **private** functions
  (`_validate_ears_constraints`, `_validate_task_group_structure`) to build
  its own JSON shape.
- `agentfox/spec/lint.py` (`run_lint_specs`) is fully implemented and
  heavily tested but has **no CLI entry point at all** — neither `af lint`
  nor `spec lint` exists. It's dead from a user's perspective.
- `agentspec.campaign.Campaign` (286 lines) is exported from
  `agentspec/__init__.py` but has zero callers in `spec/cli.py`, which
  inlines directory creation directly instead.
- `nightshift/app.py` imports `afaudit.nightshift_summary` inside a
  `try/except` — **that module does not exist** in `packages/afaudit/`.
  The import silently fails every time; whatever it was meant to surface
  has never run.
- A legacy config path is still live: `agentspec/config.py` still reads
  `~/.af/settings.yaml` with a deprecation warning, three releases after
  spec `13` unified config loading.

### 3.4 Configuration surface

`docs/config-reference.md` documents 13 top-level sections and ~54 leaf
fields, seven of which are marked "hidden" (not in the quick-start
template). This audit found the surface has already drifted from the code
that reads it:

| Problem | Detail |
|---|---|
| **Documented but dead** | `[theme]` is fully documented, but `OutputManager.banner()` constructs a hardcoded `ThemeConfig()` and never reads `load_config().theme` — every themed-output setting in a user's `config.toml` is silently ignored. `caching.cache_policy` is parsed but `core/client.py` call sites use a hardcoded default instead. |
| **Live but undocumented** | `archetypes.curator`, `knowledge.provider.max_cross_spec_items`, `max_drift_age_days`, `max_summary_items` all affect runtime behavior but appear nowhere in `config-reference.md`. |
| **Doc/code default mismatch** | `reviewer_config.audit_max_retries` — docs say default `2`, code says `1`. `reviewer_config.drift_review_block_threshold` — docs say default `null` (advisory-only), code says `1` (blocking by default). |
| **Confusing overlaps** | Three separate "clean the dirty tree" knobs (`workspace.force_clean` config, `--force-clean` CLI flag, `harvest._clean_conflicting_untracked`); `orchestrator.sync_interval` docs say default `5` but the code default is actually `None` → auto-computed as `parallel * 3`. |
| **Onboarding inconsistency** | Fresh `af init` creates a `develop` branch (`init_project.py:535`) while the config default `workspace.integration_branch` is `main` — a new user's repo and their config disagree on day one. |

None of these are hard bugs — every one is graceful-degradation-by-design
— but each one is a support burden: a user sets a documented option,
observes no effect, and either files an issue or (more likely) silently
concludes agent-fox is unreliable.

### 3.5 Support subsystems (`workspace/`, `ui/`, `io/`, `reporting/`)

- **Two `ProgressDisplay` classes** with the same name and similar purpose
  live in `io/progress.py` (JSONL events, agent-mode) and `ui/progress.py`
  (Rich human-facing spinner) — not a bug, but a naming collision that
  costs a "which one?" lookup on every touch.
- **Git worktree cleanup logic is scattered three ways**:
  `worktree.py`, `health.cleanup_stale_worktrees`, and
  `git.delete_branch`/`_resolve_worktree_conflict` all implement
  overlapping prune/remove behavior. This area has generated a
  disproportionate share of the issue tracker's bug reports (#638, #629,
  #628, #618, #616, #614 — six separate "stale/orphaned/colliding
  worktree" issues), which is itself evidence that the logic needs one
  home instead of three.
- **Three push/reconcile code paths** in `harvest.py` alone
  (`_push_with_retry`, `_push_integration_branch`, plus
  `integration._sync_integration_under_lock`).
- `merge_lock.py` (488 lines) mixes **file-lock/heartbeat mechanics** with
  a **140-line inlined merge-conflict-resolution agent** (`run_merge_agent`)
  — two concerns that should not share a module, let alone be
  independently untestable from each other.
- Confirmed dead: `merge_fast_forward` (defined, re-exported, zero
  production callers — harvest always squash-merges); `PullRequestResult`
  and `create_pull_request` remain on `PlatformProtocol` despite a comment
  stating "PR creation has been removed."

### 3.6 What we are *not* recommending removing (and why)

To be explicit about scope discipline, three things a naive "make it
simpler" pass might target are **not** on this list, because the evidence
shows they solve real, previously-experienced problems:

- **The three-package spec toolchain (`afspec`/`agentspec`/`spec`).** The
  library split is clean (see §3.3) and the JSON format itself
  (`prd.md` + `requirements.json` + `test_spec.json` + `tasks.json`) was
  adopted specifically to fix cross-spec inconsistency problems that a
  free-form Markdown format produced (see the user's own prior investigation
  notes in `prompts.md` describing spec-to-spec field-name mismatches
  found by drift review). Reverting to a single Markdown file would
  reintroduce exactly that failure mode.
- **DuckDB as the state store.** Its single-writer constraint caused one
  real incident (archived spec `06`, reader/writer split), which has
  already been fixed. The store is heavily tested (~33 test files under
  `tests/unit/knowledge/`) and gives `af insights`/`af standup` real SQL
  query power over findings and telemetry that a flat JSONL file would not.
- **The six-archetype model.** It was already consolidated once —
  `skeptic`/`oracle`/`auditor` were unified into the mode-based `reviewer`
  archetype specifically to reduce this kind of sprawl. The remaining six
  entries (Coder, Reviewer, Curator, Verifier, Gate, Maintainer) map to six
  genuinely distinct responsibilities with dedicated tests; collapsing
  further would re-merge concerns that were deliberately split apart.

### 3.7 Note on concurrent work

While this analysis was in progress, commit `cc816a8c` landed on `main`
("refactor: simplify codebase — remove dead code, dedup, decompose large
functions") — itself a direct instance of Tier-1/2/3 cleanup in exactly
this spirit (deduplicated `_strip_frontmatter`, removed deprecated
parameters, deleted a legacy migration loop, split two 200+ line
functions). This PRD's initiative list has been checked against that
commit and does not duplicate it; it identifies the *next* layer of the
same problem class. (Note: this repository is under active, concurrent
development in the same working directory as this analysis — further
commits landed on `main`, and uncommitted working-tree edits were reset
more than once, during this same session. Re-verify line numbers against
current `HEAD` before starting implementation.)

## 4. Proposed Initiatives

Organized by risk/effort tier, following the project's own
`af-code-simplifier` priority hierarchy (maintainability > readability >
reduced complexity > fewer lines). Each is sized to be one spec (one
`spec_name` under `.agent-fox/specs/`) so it can be planned and executed
independently without blocking the others.

### P0 — Quick wins (low risk, ship this week)

| # | Initiative | Fixes |
|---|---|---|
| P0.1 | **Delete confirmed dead code**: `AssessmentManager`, `render_verification_context`, `_BARE_FILE_RE`, `format_verdict_parts`/`sort_verdicts`, unused `AssessedComplexity` consumption path, `merge_fast_forward`, `PullRequestResult`/`create_pull_request` off `PlatformProtocol`, unused `self._instances` | §3.1, §3.2, §3.5 |
| P0.2 | **Fix or remove the dead `afaudit.nightshift_summary` import** in `nightshift/app.py` | §3.3 |
| P0.3 | **Reconcile config docs with code**: fix the two default-value mismatches, document the four undocumented fields, wire `[theme]` and `caching.cache_policy` into their actual call sites (or explicitly document them as reserved/no-op) | §3.4 |
| P0.4 | **Fix the `develop`-vs-`main` onboarding inconsistency** in `af init` | §3.4 |
| P0.5 | **Drop the VSS extension load** at knowledge-store startup (dead since migration v18) | §3.2 |
| P0.6 | **Add `spec status` and `plan --verify` to `docs/cli-reference.md`**; fix the "v1.2 JSON format" string still present in the bundled `af-spec` skill template | §3.3, docs |

**Estimated impact:** near-zero risk, no behavior change to any documented
API, removes ~5 confirmed-dead functions/classes and closes two config
support gaps that have already caused silent no-ops in production configs.

### P1 — Structural consolidation (moderate risk, high value)

| # | Initiative | Fixes |
|---|---|---|
| P1.1 | **De-inline the "extracted-in-name-only" blocks** — move the coverage logic out of `result_handler.py`, the issue-summary logic out of `engine.py`, and the preflight logic out of `dispatch.py` into their own modules (this is what the comments already claim happened) | §3.1 |
| P1.2 | **Consolidate spec discovery** into `afspec.discover_specs` as the single implementation; have `agentfox/spec/discovery.py` and `spec/cli.py` call it through a thin adapter instead of re-parsing directory names | §3.3 |
| P1.3 | **Move `spec validate`'s JSON-shaping logic into `afspec`** as a public `validate_structured()` function, so `spec/cli.py` stops reaching into `afspec.validation`'s private functions | §3.3 |
| P1.4 | **Consolidate git-worktree cleanup** (`worktree.py`, `health.py`, `git.py`) into one owned module — directly informed by the six worktree-related bugs already filed | §3.5 |
| P1.5 | **Split `merge_lock.py`**: extract `run_merge_agent` into its own module so file-locking and AI-assisted conflict resolution can be tested and changed independently | §3.5 |
| P1.6 | **Resolve `agentfox.fix/` vs `agentfox.nightshift/` naming collision** — rename one (e.g. `agentfox.localfix/`) and decide whether their clustering/triage logic should share a common base | §3.2 |
| P1.7 | **Decide the fate of `run_lint_specs` and `Campaign`**: either wire each to a real CLI command (`af lint`, `spec campaign`) or delete them — both are currently fully implemented, fully tested, and unreachable by any user | §3.3 |
| P1.8 | **Consolidate archetype injection** — merge `graph/builder.py:_inject_archetype_nodes` and `graph/injection.py:ensure_graph_archetypes` into one code path used at both build and runtime-patch time | §3.1 |

**Estimated impact:** removes 3 of the 5 largest files' most confusing
sections, eliminates the worktree bug class at its root, and removes ~1,100
lines of either-wire-it-or-delete-it dead weight (`Campaign` +
`run_lint_specs` + duplicated discovery/validation).

### P2 — Larger bets (higher effort, sequence after P0/P1)

| # | Initiative | Fixes |
|---|---|---|
| P2.1 | **Give Night Shift real knowledge parity**: pass real `touched_files` and session summaries through `_ingest_knowledge`, and stop passing `task_group=None` on retrieval so the existing caps actually apply | §3.2 |
| P2.2 | **Have Night Shift compose the shared session/retry infrastructure** (`ResultHandler`, `assemble_context`) instead of maintaining a parallel `CoderReviewerLoop` — this is the highest-effort item on the list and should be scoped as its own spec with a design doc, not attempted opportunistically | §3.2 |
| P2.3 | **Consolidate the four retry-tracking mechanisms** in `engine/` into a single retry ledger keyed by node ID, with the seven-case `_handle_failure` tree expressed as data (a lookup table) rather than nested conditionals | §3.1 |
| P2.4 | **Unify the three CLIs** (`af`, `spec`, `nightshift`) behind one binary with subcommand groups (`af spec new`, `af fix daemon`), retiring the "which of three tools do I run?" question. (This is also called out as a DX/game-changer initiative in the companion document — it belongs on both lists because it is simultaneously a complexity reduction and a UX win.) | §3.3 |
| P2.5 | **Auto-generate `config-reference.md` from the pydantic config schema** so doc/code drift (§3.4) becomes structurally impossible instead of something we re-audit manually every few months | §3.4 |

## 5. Success Metrics

- **Zero** confirmed-dead functions/classes remaining in `engine/`,
  `knowledge/`, `nightshift/`, `workspace/` after P0+P1 (verifiable by
  re-running the same grep-based dead-code sweep this audit used).
- **One** discovery implementation, **one** validation-presentation
  implementation (down from three and two respectively).
- **Zero** config fields where documented default ≠ code default (currently
  2) and **zero** live fields undocumented (currently 4).
- Largest four files in `engine/` reduced by 20-30% each without losing
  test coverage (`make test` green throughout — this is a pure-refactor
  initiative, not a behavior-change one, so no test should need to change
  except imports).
- Worktree-related bug reports (currently 6 closed issues on this exact
  theme) drop to zero for at least one full release cycle after P1.4 ships.

## 6. Non-Goals

- Rewriting or replacing DuckDB, the six-archetype model, or the v1.3 JSON
  spec format (see §3.6).
- Any change to public CLI flags, config field names, or the spec file
  format as part of P0/P1 — these are internal-only refactors. P2.4
  (CLI unification) is explicitly scoped as a separate, opt-in migration
  with backward-compatible aliases, not a breaking change bundled into
  cleanup work.
- Test suite refactoring — per the project's own `af-code-simplifier`
  guardrails, tests are not touched for DRYness during this initiative.

## 7. Risks & Mitigations

| Risk | Mitigation |
|---|---|
| De-inlining (P1.1) accidentally changes import order and breaks a circular-import-sensitive module | Each move is a pure relocation; run full `make check` per initiative before merge, not batched at the end |
| Consolidating spec discovery (P1.2) changes which directories are accepted, silently breaking someone's non-conforming spec folder | Add property tests asserting the unified resolver accepts the union of what all three currently accept before cutting over, per current test conventions in `tests/property/` |
| Night Shift knowledge parity (P2.1) increases session cost by pulling in more context per fix | Cap via the *existing* `knowledge.provider.max_*` fields — no new config surface needed, just remove the `task_group=None` bypass |
| CLI unification (P2.4) breaks existing scripts/CI that call `spec`/`nightshift` directly | Ship as aliases first (`af spec` calls into the same code `spec` does) with the standalone binaries deprecated but functional for at least one release |

## 8. Rollout Plan

Each numbered initiative above is sized to be its own spec under
`.agent-fox/specs/`, generated via the existing `af-spec` skill /
`spec` CLI workflow (`spec new` → `spec refine` → `spec generate` →
`spec validate`), then executed via `af plan` / `af code` — i.e., agent-fox
should simplify itself the same way it builds any other feature. Suggested
sequencing:

1. P0.1–P0.6 as a single small spec (or several same-day specs — each is
   independently low-risk enough to run in parallel).
2. P1.1–P1.8 as 4-6 specs, ordered by which files they touch, to minimize
   merge conflicts between parallel coder sessions (`P1.1` and `P1.4` touch
   disjoint file sets and can run fully in parallel; `P1.2` and `P1.3` both
   touch `spec/cli.py` and should be sequenced).
3. P2.1-P2.5 sequenced after P1 lands, each as its own spec with an
   `architecture.md` given their higher design risk.

## Appendix: Evidence Sources

This analysis is based on a direct, line-cited read of the source at
`packages/agentfox/`, `packages/af/`, `packages/afspec/`,
`packages/agentspec/`, `packages/spec/`, `packages/afaudit/`,
`packages/nightshift/`, cross-referenced against `docs/architecture/`,
`docs/config-reference.md`, `docs/cli-reference.md`, the 13 archived specs
under `.agent-fox/specs/archive/`, and the project's GitHub issue history
(419 issues total, `gh issue list --state closed`). All file:line
citations above were grep-verified for live call sites before being
labeled "dead."
