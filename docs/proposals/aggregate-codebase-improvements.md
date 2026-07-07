# Aggregate: Suggested Improvements to the Current Codebase

| | |
|---|---|
| **Status** | Draft |
| **Date** | 2026-07-07 |
| **Sources** | `evolution-analysis.md`, `simplification-prd.md`, `game-changer-features-prd.md` |

This document consolidates every actionable improvement to the existing
agent-fox codebase found across the proposal documents. These are not new
features — they are cleanup, consolidation, bug fixes, and structural
improvements that reduce complexity, remove dead weight, and fix silent
misbehavior.

---

## 1. Dead Code Removal

Grep-verified dead code with zero live call sites. Lowest risk, highest
signal-to-noise improvement.

| Item | Location | Issue |
|---|---|---|
| `AssessmentManager` | `engine/engine.py` | Deprecated stub retained only for test imports |
| `render_verification_context` | `session/context.py` | No-op stub since `verification_results` table was dropped |
| `_BARE_FILE_RE` | `graph/file_impacts.py` | Defined, never used by `_extract_file_paths` |
| `format_verdict_parts` / `sort_verdicts` | `knowledge/formatting.py` | No callers; belonged to verification channel removed in migration v26 |
| `converge_multi_instance_skeptic` | `review_persistence.py` | Called only from tests, not from live dispatch |
| `merge_fast_forward` | `workspace/` | Defined and re-exported but zero production callers — harvest always squash-merges |
| `PullRequestResult` / `create_pull_request` | `PlatformProtocol` | Remain despite comment stating "PR creation has been removed" |
| `self._instances` on `NodeSessionRunner` | `engine/session_lifecycle.py` | Computed via `clamp_instances`, never read |
| `query_knowledge_context()` | `fix/analyzer.py` | Always returns empty string — unreachable code after early return |
| `shallow_merge()` | `core/config.py` | Never called — local config completely replaces global |
| `_migrate_legacy_files()` | `session/context.py` | One-time migration from legacy `review.md` files; should be removed or moved to a migration script |
| Re-export module | `session/prompt.py` | Almost entirely re-exports from `context.py` and `steering.py` |
| `execute_batch` in `ParallelRunner` | `engine/parallel.py` | Documented as "used by tests only" — move to test utilities |
| `AssessedComplexity` | `nightshift/review_parser.py` | Parsed from triage output but never consumed downstream |
| DuckDB VSS extension load | `knowledge/db.py` | Still loaded at startup even though the embedding table was dropped in migration v18 — dead startup cost and needless failure mode |
| `agentspec.campaign.Campaign` | `agentspec/__init__.py` | Exported, zero callers in `spec/cli.py` |

**Estimated impact:** ~500+ lines removed. Cleaner imports. Elimination of
startup-time dead weight (VSS extension).

---

## 2. Configuration Drift and Doc/Code Mismatches

Silent misbehavior where documented settings have no effect or defaults
disagree with the code.

| Problem | Detail |
|---|---|
| **`[theme]` section is dead** | Fully documented in `config-reference.md`, but `OutputManager.banner()` constructs a hardcoded `ThemeConfig()` and never reads `load_config().theme` — every themed-output setting in a user's `config.toml` is silently ignored |
| **`caching.cache_policy` is dead** | Parsed but `core/client.py` call sites use a hardcoded default instead |
| **`reviewer_config.audit_max_retries`** | Docs say default `2`, code says `1` |
| **`reviewer_config.drift_review_block_threshold`** | Docs say default `null` (advisory-only), code says `1` (blocking by default) |
| **`develop` vs `main` onboarding** | Fresh `af init` creates a `develop` branch while config default `workspace.integration_branch` is `main` — disagreement on day one |
| **Undocumented live fields** | `archetypes.curator`, `knowledge.provider.max_cross_spec_items`, `max_drift_age_days`, `max_summary_items` all affect runtime but are absent from `config-reference.md` |
| **Confusing overlaps** | Three separate "clean the dirty tree" knobs (`workspace.force_clean` config, `--force-clean` CLI flag, `harvest._clean_conflicting_untracked`); `orchestrator.sync_interval` docs say default `5` but code default is `None` (auto-computed) |
| **`[routing]` docs describe unbuilt feature** | Docs describe "adaptive model routing... escalates to a more capable model tier based on past session outcomes" — the code only implements timeout retry parameters |
| **Legacy config path** | `agentspec/config.py` still reads `~/.af/settings.yaml` with a deprecation warning, three releases after config loading was unified |

**Recommendation:** Fix the two default mismatches, document the four
undocumented fields, wire `[theme]` and `caching.cache_policy` to their call
sites or explicitly mark them as reserved/no-op, fix the `develop`-vs-`main`
onboarding inconsistency, and correct or remove the `[routing]` documentation.

---

## 3. Structural Consolidation

### 3.1 De-inline "Extracted in Name Only" Blocks

Four of the largest files carry 200-300 line blocks with comments stating
they were "inlined from" separate modules that no longer exist:

| File | Lines | Inlined block |
|---|---|---|
| `engine/result_handler.py` | 1386 | ~310 lines of coverage measurement (`detect_coverage_tool`, `measure_coverage`, pytest/go/js parsers) |
| `engine/engine.py` | 1130 | ~260 lines of GitHub issue-summary posting (`parse_source_url`, `post_issue_summaries`) |
| `engine/dispatch.py` | 970 | ~175 lines of preflight logic (`run_preflight`, `do_tests_pass`) |
| `merge_lock.py` | 488 | ~140 lines of merge-conflict-resolution agent (`run_merge_agent`) |

**Recommendation:** Extract each into its own module (pure move, no behavior
change). This is the single highest-leverage cleanup target: low risk, and
immediately shrinks the four largest files by 25-30% each.

### 3.2 Consolidate Spec Discovery (Three Implementations)

Three separate discovery implementations with three different regexes:

1. `afspec/discovery.py` — `^\d+_[a-z][a-z0-9_]*$`
2. `agentfox/spec/discovery.py` — looser `^(\d+)_(.+)$` plus `requirements.json` check
3. `spec/cli.py` — own `_resolve_spec`/`_next_prefix`, loosest

These can silently accept or reject different directory sets depending on
which resolver a code path calls.

**Recommendation:** Consolidate into `afspec.discover_specs` as the single
implementation. Add property tests asserting the unified resolver accepts the
union of what all three currently accept.

### 3.3 Consolidate Git Worktree Cleanup (Three Implementations)

Cleanup logic is scattered across `worktree.py`,
`health.cleanup_stale_worktrees`, and `git.delete_branch` /
`_resolve_worktree_conflict`. This area has generated a disproportionate
share of bug reports (#638, #629, #628, #618, #616, #614 — six separate
worktree-related issues).

**Recommendation:** Consolidate into one owned module. Directly informed by
the six bugs already filed.

### 3.4 Consolidate Archetype Injection (Two Parallel Paths)

- `graph/builder.py:_inject_archetype_nodes` (build time)
- `graph/injection.py:ensure_graph_archetypes` (runtime patch)

Both include duplicated audit-review edge-rewiring logic.
`hot_load.py:_build_nodes_and_edges` reimplements a subset of
`builder.py:_create_nodes_and_intra_edges` without full parity.

**Recommendation:** Merge into one code path used at both build and
runtime-patch time.

### 3.5 Fix `tasks.md` References in a `tasks.json` World

Spec format v1.3 uses `tasks.json` exclusively, but several code paths
still reference `tasks.md`:

- `graph/file_impacts.py` (file-conflict detection)
- `engine.py`'s issue-summary path
- `session/prompt.py`'s task-prompt text

File-conflict detection is silently ineffective for every spec created
since the v1.3 migration — likely *why* it defaults to `false`.

### 3.6 Deduplicate Node/Archetype/Mode Lookups

Implemented twice: `dispatch.py:469-482` and `result_handler.py:106-116`.

---

## 4. Simplify the Archetype/Review Pipeline

### 4.1 Remove the Curator Archetype

The Curator sits between the last coder group and the Verifier
(`auto_post`, injection order 10). It runs at effort=medium with read-only +
`make` access. Its purpose overlaps heavily with the Coder's final
quality-check phase and the Verifier that runs immediately after.

**Impact:** One fewer session per spec (~5-15% faster, ~$0.50-2.00 saved per
spec). Simpler execution graph. ~89 lines of profile template + injection
code removed.

**Risk:** Low. The Verifier catches everything the Curator would.

### 4.2 Merge Pre-Review and Drift-Review into Single Pre-Flight

Two separate review sessions run at `auto_pre` before any code is written.
Both analyze the spec against different reference frames.

**Impact:** One fewer session per spec. Faster time-to-first-code. Simpler
config (one toggle instead of two).

**Risk:** Medium. Separate sessions allow independent parallelism, but the
pre-review already runs at ADVANCED tier with capacity for both.

---

## 5. Unify the Two Fix Systems

Two completely separate fix systems exist in parallel:

1. **Nightshift fix pipeline** (`agentfox/nightshift/`): Issue-driven,
   triggered by GitHub `af:fix` labels. ~4,300 lines.
2. **CLI fix system** (`agentfox/fix/`): Check-failure-driven, triggered by
   `af fix`. ~2,200 lines.

Both call `run_session` but have completely separate orchestration, prompt
construction, progress tracking, result types, cost tracking, and knowledge
retrieval. Overlapping implementations include:

- Cost tracking: `SharedBudget` (daemon) vs `_check_cost_limit` (engine) vs
  inline checks (fix loops) — three strategies
- Knowledge retrieval: `KnowledgeProvider.retrieve()` (nightshift) vs direct
  DuckDB queries (fix/analyzer)
- Session recording: nightshift manually constructs `SessionOutcomeRecord`;
  orchestrator has its own path
- Review parsing: `parse_fix_review_output` (nightshift) vs
  `parse_verifier_verdict` (fix/improve)

**Recommendation:** Extract a shared `FixSession` abstraction. Unify cost
tracking around `SharedBudget`. Standardize on `KnowledgeProvider.retrieve()`
for all knowledge access. Rename one system to resolve the naming collision
(e.g., `agentfox.localfix/`).

**Impact:** ~1,000 lines deduplicated. Consistent behavior across fix paths.

---

## 6. Night Shift Knowledge and Session Parity

Night Shift reimplements agent-fox's session machinery instead of reusing
it, and its knowledge integration is a stub:

- `_ingest_knowledge` always passes `touched_files=[]` and no `summary`,
  so file-based drift supersession never runs and session summaries are never
  written from Night Shift sessions
- Retrieval passes `task_group=None`, silently disabling three of five
  retrieval caps
- Night Shift reimplements its own telemetry (`_record_session_to_db`,
  parallel to `DuckDBSink`), prompt assembly (parallel to
  `assemble_context`), and retry loop (`CoderReviewerLoop`, parallel to
  `ResultHandler`) — ~1,346 lines in `fix_pipeline.py` doing a smaller
  version of what `engine/` already does

**Recommendation:**
- Pass real `touched_files` and session summaries through `_ingest_knowledge`
- Stop passing `task_group=None` on retrieval so existing caps apply
- Long-term: compose the shared session/retry infrastructure
  (`ResultHandler`, `assemble_context`) instead of maintaining parallel
  `CoderReviewerLoop`

---

## 7. Consolidate Three CLIs into One

Users install three separate CLIs (`af`, `nightshift`, `spec`). Three entry
points, three `--help` outputs, three `pyproject.toml` files to maintain.
The `nightshift` package is 237 lines of source — a thin Click wrapper.

**Recommendation:** Absorb as `af` subcommands:
- `af nightshift` or `af fix daemon` (was: `nightshift`)
- `af spec new|refine|generate|validate|render` (was: `spec new|...`)

Keep standalone entry points as deprecated shims for one release cycle.

**Impact:** Single CLI to learn. Two fewer packages to maintain. Simpler
installation. `af --help` shows the complete capability surface.

---

## 8. Retry State Fragmentation

Four independent mechanisms track node failure state:

1. `attempt_tracker` dict in dispatch
2. `_node_failure_counts` dict in `result_handler.py`
3. Per-node `_NodeRetryState` (with timeout/audit/workspace/environment
   sub-counters)
4. `SessionRecord.attempt` field

`_handle_failure` routes through seven special cases before falling back to
generic retry logic. Every review-blocking rule lives in a third,
separately-evaluated place (`blocking.py`, three evaluators).

**Recommendation:** Consolidate into a single retry ledger keyed by node ID,
with the seven-case `_handle_failure` tree expressed as data (lookup table)
rather than nested conditionals.

---

## 9. Naming Collisions and Duplicates

| Item | Location | Issue |
|---|---|---|
| Duplicate `TriageResult` | `nightshift/triage.py` vs `nightshift/fix_pipeline.py` | Same name, different shapes |
| Duplicate `FixResult` | `fix/fix.py` vs `fix/runner.py` | Runner's version adds `total_cost` — merge into one |
| Two `ProgressDisplay` classes | `io/progress.py` (JSONL/agent-mode) vs `ui/progress.py` (Rich/human) | Same name, different purpose — rename one |
| `agentfox.fix/` vs `agentfox.nightshift/` | Package level | Two unrelated "fix" systems with no shared code |

---

## 10. Miscellaneous

| Item | Detail |
|---|---|
| **Dead import** | `nightshift/app.py` imports `afaudit.nightshift_summary` inside a `try/except` — that module does not exist in `packages/afaudit/`. Silently fails every time. |
| **`spec/cli.py` reaches into privates** | Re-implements validation presentation by calling `afspec.validation`'s private functions (`_validate_ears_constraints`, `_validate_task_group_structure`). Should consume `afspec.validate()`'s output directly. |
| **Unreachable `run_lint_specs`** | `agentfox/spec/lint.py` — fully implemented, heavily tested, but has no CLI entry point. Neither `af lint` nor `spec lint` exists. |
| **Prompt sanitization duplication** | `spec_builder.py` and `triage.py` both sanitize issue titles/bodies independently |
| **Dual workspace health checking** | Pre-run health checks in `run.py` and per-session checks in `dispatch.py` have overlapping concerns that could share more code |
| **Three push/reconcile paths** | `harvest.py` alone has `_push_with_retry`, `_push_integration_branch`, plus `integration._sync_integration_under_lock` |
| **Migration weight** | `migrations.py` (1,290 lines) carries full create-drop history for six tables that no longer exist. Fresh databases skip via `_CURRENT_SCHEMA_DDL`, but the registry is pure maintenance weight. |
| **Auto-detect GitHub remote** | Auto-configure `[platform]` section — most users with a GitHub remote want `type = "github"` |
| **Inline audit sink abstraction** | `afaudit` package (1,200 lines) provides a protocol-based sink dispatcher with only two sinks (DuckDB and JSONL). Inline the DuckDB sink into the knowledge subsystem unless additional sinks are concretely planned. |

---

## 11. Summary of Impact

| Category | Est. lines removed/simplified | Sessions saved per spec | User impact |
|---|---|---|---|
| Dead code removal | ~500+ | 0 | Cleaner codebase, fewer false leads |
| Config drift fixes | ~200 | 0 | Settings that actually work; faster onboarding |
| De-inline extracted blocks | ~750 moved | 0 | 4 largest files shrink 25-30% |
| Consolidate spec discovery | ~300 | 0 | Consistent spec recognition |
| Consolidate worktree cleanup | ~200 | 0 | Eliminate a bug class (6 filed issues) |
| Remove Curator | ~500 | 1 | Faster execution |
| Merge pre/drift review | ~300 | 1 | Faster time-to-code |
| Unify fix systems | ~1,000 | 0 | Consistent behavior |
| CLI consolidation | ~1,000 (config/setup) | 0 | Single entry point |
| Retry consolidation | ~300 | 0 | Safer to modify |
| Inline audit sinks | ~400 | 0 | Fewer packages |
| **Total** | **~5,500+** | **2 per spec** | **Meaningful DX improvement** |

Net effect: ~11% reduction in production code, 2 fewer AI sessions per spec
(~$1-4 saved per spec in API costs), elimination of multiple bug classes,
and a significantly simpler mental model for contributors and users.

---

## 12. Recommended Sequencing

**P0 — Ship this week (low risk, no behavior change):**
Dead code removal, config drift fixes, dead import fix, VSS extension drop,
`develop`-vs-`main` onboarding fix.

**P1 — Structural consolidation (moderate risk, high value):**
De-inline extracted blocks, consolidate spec discovery, consolidate worktree
cleanup, consolidate archetype injection, split `merge_lock.py`, resolve
fix-system naming collision, wire or delete `run_lint_specs` and `Campaign`.

**P2 — Larger bets (higher effort, sequence after P1):**
Night Shift knowledge parity, Night Shift session infrastructure reuse,
retry ledger consolidation, CLI unification, auto-generate
`config-reference.md` from pydantic schema, remove Curator, merge
pre/drift review.
