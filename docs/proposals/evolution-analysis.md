# Agent-Fox Evolution: Simplification & Game-Changers

A product analysis and roadmap for the next phase of af — cutting
complexity that doesn't earn its keep, and adding capabilities that would
fundamentally change how developers work with AI coding agents.

---

## Part 1: The Current State

### What af is

Agent-fox is an autonomous coding-agent orchestrator built exclusively for
Claude Code. You write specs, it plans the work as a dependency graph, spins
up isolated git worktrees, dispatches parallel AI coding sessions, handles
merge conflicts, retries failures, accumulates knowledge across sessions, and
merges results to your integration branch. You come back to a finished feature
and a standup report.

Night Shift extends this into an always-on maintenance daemon that polls
GitHub for `af:fix`-labelled issues and autonomously triages, fixes, and
reviews them.

### By the numbers

| Metric | Value |
|--------|-------|
| Packages | 7 (`af`, `agentfox`, `afspec`, `afaudit`, `agentspec`, `nightshift`, `spec`) |
| Production Python (non-test) | ~49,500 lines |
| Test Python | ~156,000 lines |
| `agentfox` core library alone | ~38,800 lines source, ~130K lines test |
| Engine subsystem | ~10,500 lines |
| Nightshift subsystem (in agentfox) | ~4,300 lines |
| Knowledge subsystem | ~3,500 lines |
| Session subsystem | ~3,700 lines |
| Workspace subsystem | ~3,600 lines |
| Agent archetypes | 6 (Coder, Reviewer×4 modes, Curator, Verifier, Gate, Maintainer×3 modes) |
| Profile templates | 13 markdown files |
| Config sections | 11 top-level sections, 40+ fields |
| DuckDB tables | 11 |
| CLI commands | 9 across 3 CLIs |
| Bundled skills | 6 Claude Code skills |
| Archived specs | 13 completed |

---

## Part 2: Simplification — What to Cut

The goal: reduce cognitive load, maintenance burden, and time-to-value
without jeopardizing code quality outcomes. Every cut must pass the test:
*does removing this make the tool less effective, or just less complicated?*

### 2.1 Remove the Curator Archetype

**Problem:** The Curator sits between the last coder group and the Verifier
(`auto_post`, injection order 10). It runs at effort=medium with read-only +
`make` access. Its purpose is "post-implementation curation" — but this
overlaps heavily with what the Coder already does in its final quality-check
phase and what the Verifier does immediately after.

Every spec pays for an extra AI session (~$0.50-2.00) for marginal
incremental value.

**Recommendation:** Remove the Curator archetype entirely. Fold any unique
quality-check responsibilities into the Coder profile (which already has a
quality-gate section) and the Verifier.

**Impact:**
- One fewer session per spec (~5-15% faster execution)
- Simpler execution graph (fewer nodes)
- Eliminates ~89 lines of profile template + injection/convergence code
- Simpler mental model for users

**Risk:** Low. The Verifier (running right after at injection order 20) catches
everything the Curator would.

### 2.2 Merge Pre-Review and Drift-Review into a Single Pre-Flight

**Problem:** Two separate review sessions run at `auto_pre` before any code is
written — one for spec quality (pre-review, ADVANCED tier) and one for codebase
drift (drift-review, STANDARD tier). Both analyze the spec against different
reference frames (spec quality vs. codebase assumptions).

This means two full AI sessions before a single line of code is written.

**Recommendation:** Merge into a single "pre-flight review" that does both
analyses in one session. The ADVANCED model is already capable of both spec
quality assessment and codebase drift detection. The drift-review's read-only
filesystem access can be granted to the merged session.

**Impact:**
- One fewer session per spec
- Faster time-to-first-code
- Simpler config (one enable/disable toggle instead of two)
- Simpler convergence (one finding set instead of two parallel sets)

**Risk:** Medium. Separate sessions allow independent parallelism. But for
most projects, drift checking is fast and could be a section within the
pre-review prompt. The pre-review already runs at ADVANCED tier — it has the
capacity.

### 2.3 Consolidate Three CLIs into One

**Problem:** Users install three separate CLIs (`af`, `nightshift`, `spec`).
Three entry points, three `--help` outputs to learn, three `pyproject.toml`
files to maintain. The `nightshift` package is 237 lines of source — it's a
thin Click wrapper around `agentfox.nightshift`.

**Recommendation:** Absorb `nightshift` and `spec` as `af` subcommands:
- `af nightshift` (was: `nightshift`)
- `af spec new|refine|generate|validate|render` (was: `spec new|...`)

Keep the standalone entry points as deprecated shims for one release cycle.

**Impact:**
- Single CLI to learn and tab-complete
- Two fewer packages to maintain
- Simpler installation
- `af --help` shows the complete capability surface

**Risk:** Low. The `af spec` subcommand would need the `agentspec` dependency
added to `af`, but it's already an indirect dependency via `agentfox`.

### 2.4 Dead Code and Stubs

Research identified concrete dead code and stubs across the codebase:

| Item | Location | Issue |
|------|----------|-------|
| `query_knowledge_context()` | `fix/analyzer.py` | Always returns empty string — unreachable code after the early return |
| `AssessmentManager` stub | `engine/engine.py` | Deprecated class retained only for test imports |
| `render_verification_context()` | `session/context.py` | Always returns `None` — the verification_results table was removed |
| `shallow_merge()` | `core/config.py` | Never called — local config completely replaces global |
| `_migrate_legacy_files()` | `session/context.py` | One-time migration from legacy `review.md` files; should be moved to a migration script or removed |
| Re-export module | `session/prompt.py` | Almost entirely re-exports from `context.py` and `steering.py` |
| `execute_batch` in `ParallelRunner` | `engine/parallel.py` | Documented as "used by tests only" — move to test utilities |
| Duplicate `TriageResult` | `nightshift/triage.py` vs `nightshift/fix_pipeline.py` | Same name, different shapes — rename one |
| Duplicate `FixResult` | `fix/fix.py` vs `fix/runner.py` | Runner's version adds `total_cost` — merge into one |

**Recommendation:** Remove all dead code and stubs. Rename conflicting types.

**Impact:** ~500 lines of dead weight removed. Cleaner imports.

### 2.5 Unify the Two Fix Systems

**Problem:** Two completely separate fix systems exist in parallel:

1. **Nightshift fix pipeline** (`agentfox/nightshift/`): Issue-driven fixing
   triggered by GitHub `af:fix` labels. Has triage, coder-reviewer loop,
   worktree isolation, branch harvesting. ~4,300 lines.

2. **CLI fix system** (`agentfox/fix/`): Check-failure-driven fixing triggered
   by `af fix` CLI. Has check detection, failure clustering, iterative repair
   loop. ~2,200 lines.

Both call `run_session` to fix code, but they have completely separate
orchestration, prompt construction, progress tracking, result types, cost
tracking, and knowledge context retrieval. There's no shared abstraction for
"run a coder session to fix something and verify the result."

**Overlapping implementations:**
- Cost tracking: `SharedBudget` (daemon), `_check_cost_limit` (engine), inline checks (fix loops) — three strategies
- Knowledge retrieval: `KnowledgeProvider.retrieve()` (nightshift) vs direct DuckDB queries (fix/analyzer)
- Session recording: nightshift manually constructs `SessionOutcomeRecord`; orchestrator has its own path
- Review parsing: `parse_fix_review_output` (nightshift) vs `parse_verifier_verdict` (fix/improve)

**Recommendation:** Extract a shared `FixSession` abstraction that both
systems use. Unify cost tracking around `SharedBudget`. Standardize on
`KnowledgeProvider.retrieve()` for all knowledge access.

**Impact:** ~1,000 lines of deduplicated code. Consistent behavior across
fix paths. Single place to fix bugs.

**Risk:** Medium. The two systems have different triggers and workflows, so
full unification isn't possible — but the session-level mechanics can share.

### 2.6 Simplify Configuration

**Problem:** 11 config sections with 40+ fields. The config gen system has
visible/hidden sections, promoted defaults, deprecated field tracking, and
legacy footer stripping. The `Clamped` annotation, `_auto_clamp_validator`,
and 4-level archetype config override chain (ArchetypeEntry → ModeConfig →
PerArchetypeConfig → PerArchetypeConfig.modes) add real complexity.

**Recommendation:**
- Auto-detect GitHub remote and auto-configure `[platform]` — most users with
  a GitHub remote want `type = "github"`
- Group power-user sections under `[advanced]` namespace
- Simplify archetype overrides to a flat `model = "opus"` per archetype
  instead of the tier/variant/mode resolution chain
- Document the "just works" defaults more prominently — most users need
  zero config changes

**Impact:** Faster onboarding. Less config confusion. Fewer support questions.

**Risk:** Low for UX changes. Medium for the archetype override simplification
(power users may depend on mode-level overrides).

### 2.7 Inline the Audit Sink Abstraction

**Problem:** The `afaudit` package (1,200 lines) provides a protocol-based
sink dispatcher that forwards structured events to registered sinks. Only two
sinks exist: DuckDB and JSONL file. The protocol abstraction exists for future
extensibility (Datadog, OpenTelemetry) but no additional sinks are planned.

**Recommendation:** Inline the DuckDB sink directly into the knowledge
subsystem. Keep the JSONL audit trail as simple file-append. Remove the sink
protocol unless there's a concrete plan for additional sinks.

**Impact:** Simpler event flow. One fewer package to maintain.

**Risk:** Low. If new sinks are needed, the protocol can be reintroduced.

### 2.8 Simplification Summary

| Change | Est. lines removed | Sessions saved/spec | User impact |
|--------|-------------------|---------------------|-------------|
| Remove Curator | ~500 | 1 | Faster execution |
| Merge pre/drift review | ~300 | 1 | Faster time-to-code |
| Consolidate 3 CLIs → 1 | ~1,000 (config/setup) | 0 | Single entry point |
| Remove dead code/stubs | ~500 | 0 | Cleaner codebase |
| Unify fix systems | ~1,000 | 0 | Consistent behavior |
| Simplify config | ~200 | 0 | Faster onboarding |
| Inline audit sinks | ~400 | 0 | Fewer packages |
| **Total** | **~3,900** | **2 per spec** | **Meaningful UX improvement** |

Net effect: ~8% reduction in production code, 2 fewer AI sessions per spec
(saving ~$1-4 per spec in API costs), and significantly simpler mental model.

---

## Part 3: Game-Changers — What to Add

Ideas ranked by potential impact × feasibility. Some are practical near-term
additions; others are genuinely ambitious bets.

### 3.1 🔥 Live Spec-from-Conversation ("Just Tell Me What You Want")

**The insight:** The spec creation workflow (`spec new` → `spec refine` →
answer questions → `spec generate` → `spec validate`) is agent-fox's
*biggest friction point*. Users already have Claude Code open. Making them
context-switch to a separate CLI workflow with JSON answer files is painful.
The refine loop alone can take 3-4 iterations.

**The game-changer:** Let users describe what they want in natural language,
directly in their Claude Code session:

> "I want a REST API for user preferences with validation, caching, and
> audit logging"

Agent-fox (via an enhanced `/af-spec` skill) should:

1. **Analyze the codebase** in real-time — imports, patterns, existing APIs,
   test conventions, dependency graph
2. **Generate a complete spec pack** in one shot — no refine loop, no answer
   files. The codebase analysis provides the context that the refine loop
   currently extracts through Q&A.
3. **Show a rendered preview** and let the user iterate conversationally:
   "add rate limiting", "make the cache TTL configurable", "split into two specs"
4. **Auto-run `af plan` + `af code`** when the user approves

The entire flow from idea to running code: *one sentence → one approval → done.*

**Why it's a game-changer:** Every other AI coding tool starts from code.
Agent-fox starts from specs — which is its superpower — but only if spec
creation is frictionless. Today the spec workflow is the bottleneck. This
removes it.

**Effort:** Medium-high. The `/af-spec` skill exists but needs codebase
analysis, conversational iteration, and auto-execution.

### 3.2 🔥 Self-Healing Pipeline ("It Broke? Fix It Surgically.")

**The insight:** When `af code` hits a failure, it retries the *entire task
group* with the error message appended. The agent re-does work that was fine
and might fail again on the same edge case. A retry costs the same as the
original session.

**The game-changer:** Instead of retrying the whole task group:

1. **Analyze the failure** — classify it: test failure (which test, which
   assertion), type error (which file, which line), lint error, merge
   conflict, timeout
2. **Generate a micro-spec** — a minimal, targeted spec covering only the
   failure: "fix the `test_user_preferences_validation` test failure caused by
   missing null check in `validate_preferences()`"
3. **Dispatch a micro-fix session** — a short, focused session that gets the
   original session's diff as context and only addresses the specific failure
4. **Verify with scoped tests** — run only the failing tests + a fast
   regression check, not the full suite

**Why it's a game-changer:** Current retry burns the full session budget
again ($1-5+). Micro-fix sessions would be 10-20x cheaper and more targeted.
It also models how human developers actually work — you don't re-implement a
feature when a test fails; you debug the specific failure.

**Implementation sketch:**
```
Session fails → FailureAnalyzer classifies the failure
    → MicroSpecGenerator creates a targeted fix spec
    → Coder(fix) session runs in the same worktree
    → ScopedVerifier runs only the relevant tests
    → On success: mark original task group complete
    → On failure: fall back to full retry (current behavior)
```

**Effort:** High. Requires failure classification, micro-spec generation,
scoped test execution, and worktree continuation (not fresh checkout).

### 3.3 🔥 Real-Time TUI Dashboard ("What's the Fox Doing?")

**The insight:** `af code` runs for minutes to hours with log output as the
only visibility. `af standup` is post-hoc. There's no way to see the live
state of the DAG, which sessions are running, what each agent is working on,
or where things are stuck.

**The game-changer:** A terminal UI (using Textual or Rich Live) that shows:

```
┌─ Agent-Fox Dashboard ──────────────────────────────────────────┐
│                                                                 │
│  ┌─ Task Graph ─────────────────────────────────────────────┐  │
│  │  auth_api:1 [████████████] DONE  → auth_api:2 [▓▓▓▓░░░░] │  │
│  │  auth_api:3 [pending]            → auth_api:0:verifier    │  │
│  │  user_prefs:1 [████████░░] 80%   → user_prefs:2 [pending]│  │
│  └──────────────────────────────────────────────────────────┘  │
│                                                                 │
│  ┌─ Active Sessions ──────────────┐ ┌─ Cost ────────────────┐ │
│  │  auth_api:2 (coder, sonnet)    │ │  This run:   $4.32    │ │
│  │    Writing test_auth_token.py  │ │  Session:    $0.87    │ │
│  │    Turn 47/300                 │ │  Budget:     $20.00   │ │
│  │  user_prefs:1 (coder, sonnet)  │ │  ████████░░░ 22%     │ │
│  │    Running pytest...           │ │                       │ │
│  └────────────────────────────────┘ └───────────────────────┘ │
│                                                                 │
│  [p]ause  [s]kip  [r]etry  [q]uit                              │
└─────────────────────────────────────────────────────────────────┘
```

1. **Live DAG** — nodes colored by status, real-time progress
2. **Session streams** — see what each agent is doing right now
3. **Cost meter** — running total with per-session breakdown and budget bar
4. **Intervention controls** — pause, skip, force-retry, adjust budget mid-run
5. **Timeline** — scrollable history of completions and failures

**Why it's a game-changer:** Trust. The #1 reason users babysit AI agents is
lack of visibility. A dashboard turns af from "fire and pray" to "fire
and monitor." Users who can *see* what's happening and *intervene* when needed
will actually walk away.

**Quick-win variant:** Before a full TUI, add `af code --live` that uses
Rich Live to show a real-time table of node statuses with cost. This could
be 200-300 lines using the existing DuckDB state.

**Effort:** Medium for the quick-win table. High for a full Textual TUI.

### 3.4 🔥 Cross-Spec Intelligence ("The Fox Remembers Everything")

**The insight:** The knowledge system currently scopes retrieval to same-spec
sessions. Cross-group findings exist within a spec, but cross-*spec* learning
doesn't. If spec A implements a service client and spec B implements the
server, spec B's agent doesn't know what spec A decided about the wire format.

**The game-changer:** Expand the knowledge system with three new capabilities:

**a) Code artifact indexing.** After each successful merge, index what was
created: new functions, new types, new API endpoints, new test patterns.
Store as structured entries in the knowledge DB.

**b) Cross-spec retrieval.** Before a session starts, query for relevant
artifacts from *other completed specs* that touch related code. If spec B's
task references files that spec A modified, inject spec A's session summaries
and code artifacts as context.

**c) Pattern learning.** Detect recurring patterns across specs:
- "This project always uses Pydantic models with `model_validator`"
- "Error handling follows the `Result[T, E]` pattern"
- "Tests use `pytest.mark.parametrize` with fixture factories"

Inject detected patterns as "project conventions" in the system prompt.

**d) Anti-pattern learning.** When a coder makes a mistake that the verifier
catches, store the mistake-and-fix pair. Warn future sessions about the same
class of mistake.

**Why it's a game-changer:** Current sessions are amnesiac across specs. A
human developer builds project intuition over weeks. Cross-spec intelligence
gives af the same capability — each session gets smarter because of
every session that came before it.

**Effort:** Medium. The knowledge DB and retrieval infrastructure exist. The
missing piece is broader indexing and cross-spec query scoping.

### 3.5 🔥 Adaptive Cost Optimization ("Same Quality, Half the Price")

**The insight:** Agent-fox defaults to Sonnet for coding and Opus for reviews.
For well-specified tasks — especially after earlier task groups have established
patterns — cheaper models often produce identical results.

**The game-changer:** An adaptive cost optimizer that:

1. **Starts at STANDARD** (Sonnet) as today
2. **Tracks success rates by task characteristics** — task group number,
   subtask count, spec complexity, file count, archetype
3. **Downgrades confident tasks** — if task groups with ≤3 subtasks in
   well-tested specs succeed 95%+ on Haiku, route them to Haiku
4. **Escalates on failure** — if Haiku fails, immediately retry on Sonnet
   (cost: one cheap failed session + one normal session, vs. always paying
   for the expensive session)
5. **Per-run budget optimization** — given a total budget, optimize the model
   mix to maximize completed tasks

The key insight: the cost of a failed cheap attempt is much less than the
savings of many successful cheap attempts.

**Implementation data needed:**
- Success rate by model tier × task characteristics (collect from existing
  `session_outcomes` table)
- Cost per session by model tier (already tracked)
- Failure cost penalty (already tracked via retries)

**Why it's a game-changer:** Cost is the #2 concern (after quality) for AI
coding tool adoption. If af can deliver the same quality at 40-60%
lower cost by routing easy tasks to cheaper models, that's a massive
competitive advantage.

**Effort:** Medium. The model tier system and routing infrastructure exist.
The missing piece is the adaptive routing logic.

### 3.6 🔥 GitHub-Native Mode ("Drop a Spec, Get a PR")

**The insight:** Agent-fox requires local installation, a local git repo, and
a local CLI. But the natural place for collaborative spec-driven work is the
platform: GitHub.

**The game-changer:** A GitHub Action that:

1. **Watches for spec changes** — when a PR adds or modifies files in
   `.agent-fox/specs/`, the action runs `af plan` + `af code` on a runner
2. **Posts results as PR comments** — standup report, findings, test results,
   cost breakdown
3. **Creates fix PRs from issues** — monitors `af:fix` issues and creates
   fix PRs automatically (replaces local `nightshift`)
4. **Interactive review** — when a reviewer comments `@af fix this` on
   a PR review comment, the bot runs a targeted fix session

**Why it's a game-changer:**
- Teams can try af on one PR without local installation
- CI/CD integration for spec validation
- Code review integration (reviewers can ask af to fix issues)
- Non-Python teams can use af (they don't need `uv` locally)

**Effort:** Medium-high. The core orchestrator works headlessly. The GitHub
Action wrapper and CI environment setup are new.

### 3.7 💡 Spec Fuzzing ("What If We Try to Break the Spec?")

**The insight:** The quality pipeline is linear: review spec → write code →
review code → verify. The spec is treated as correct once it passes pre-review.
But most spec-driven bugs come from spec *incompleteness* — edge cases, error
paths, and concurrency scenarios that the spec author didn't think of.

**The game-changer:** After spec generation but before coding, run an
adversarial "spec fuzzing" pass:

1. **Generate edge cases** from requirements — boundary values, empty inputs,
   max-size inputs, null fields, concurrent access
2. **Challenge assumptions** — "What if the DB is empty? What if the request
   is 10MB? What if two users hit this endpoint simultaneously?"
3. **Find contradictions** — requirements that conflict with each other or
   with the codebase
4. **Auto-generate additional test contracts** — add fuzz-discovered edge
   cases to `test_spec.json`

**Implementation:** A new Reviewer mode (`fuzz-review`) that runs at
`auto_pre`, examines the spec adversarially, and outputs additional test
spec entries.

**Why it matters:** Catches spec gaps before coding — when fixing is cheapest.

**Effort:** Medium. Could be implemented as a new reviewer mode with a
specialized profile.

### 3.8 💡 Explain Mode ("What Did It Do and Why?")

**The insight:** After `af code` completes, the user gets a standup report
with token counts. But understanding *what the code does* and *why the agent
made specific decisions* requires reading the diff manually.

**The game-changer:** After a successful run, automatically generate:

1. **PR description** — a ready-to-merge description with change summary,
   test plan, and risk assessment
2. **Architecture decision record** — why the agent chose this approach
   (extracted from session summaries and thinking traces)
3. **Code walkthrough** — a narrated tour of the key changes
4. **Regression risk analysis** — what existing functionality might break

These would be generated by a lightweight post-run agent session (Haiku-tier,
read-only) that reads the diff and session summaries.

**Why it matters:** Bridges the trust gap. If you understand *why* the code
looks the way it does, you're more likely to merge it.

**Effort:** Low-medium. Session summaries exist. The missing piece is
synthesis into user-facing documents.

### 3.9 💡 Incremental Execution ("Only Re-Run What Changed")

**The insight:** `af plan` always rebuilds the full graph. If you edit one spec
out of ten, all ten get re-planned. After a failure, `af plan --reset` + `af code`
re-evaluates everything.

**The game-changer:**
- `af code --changed` — detect which specs have been modified since the last
  successful run (content hash diff) and only execute those subgraphs
- **Smart resume** — after a crash, resume from exactly where things stopped
  (the infrastructure is partially there — `in_progress` nodes are reset to
  `pending` on startup, but the worktree with partial work is destroyed)
- **Worktree preservation on failure** — keep failed worktrees so the next
  attempt can continue from the partial work instead of starting fresh

**Why it matters:** Large projects with 10+ specs currently pay a linear cost
even when only one spec changed. Incremental execution turns this sublinear.

**Effort:** Low-medium. Content hashing exists for plan state comparison.

### 3.10 🧪 Multi-Agent Collaboration ("The Fox Pack")

**The insight:** Current sessions are isolated — each agent works alone. But
some tasks are genuinely collaborative: "implement the API server and the
client library, and make sure they agree on the wire format."

**The game-changer:** Allow task groups to be designated as "collaborative":
- A **coordinator agent** decomposes the work
- **Worker agents** execute sub-tasks in parallel worktrees
- The coordinator **verifies contract compatibility** across outputs
- Cross-agent **type checking** ensures interfaces agree

**Why it matters:** Unlocks implementation of features spanning multiple
modules or services — the most common type of real-world feature work.

**Effort:** Very high. Requires a new execution model.

---

## Part 4: Prioritized Roadmap

### Tier 1: Quick Wins (1-2 weeks each)

| # | Initiative | Type | Impact |
|---|-----------|------|--------|
| 1 | Remove Curator archetype | Simplify | 1 fewer session/spec, simpler graph |
| 2 | Remove dead code and stubs | Simplify | ~500 lines, cleaner codebase |
| 3 | Consolidate 3 CLIs → `af` subcommands | Simplify | Single entry point |
| 4 | Auto-detect GitHub remote for `[platform]` | Simplify | Zero-config for GitHub users |
| 5 | `af code --changed` incremental execution | Enhance | Sublinear re-execution |
| 6 | Explain Mode (auto-generate PR descriptions) | Enhance | Faster code review |

### Tier 2: High-Impact Features (2-4 weeks each)

| # | Initiative | Type | Impact |
|---|-----------|------|--------|
| 7 | Merge pre/drift review into single pre-flight | Simplify | 1 fewer session/spec |
| 8 | Live Spec-from-Conversation | Game-changer | Eliminates spec friction |
| 9 | Adaptive Cost Optimization | Game-changer | 40-60% cost reduction |
| 10 | Real-Time TUI Dashboard | Game-changer | Trust and visibility |
| 11 | Cross-Spec Intelligence | Game-changer | Accumulated project knowledge |

### Tier 3: Strategic Bets (1-3 months)

| # | Initiative | Type | Impact |
|---|-----------|------|--------|
| 12 | Self-Healing Pipeline | Game-changer | 10x cheaper retries |
| 13 | GitHub-Native Mode | Game-changer | Team adoption, zero local install |
| 14 | Spec Fuzzing | Game-changer | Catch spec bugs before coding |
| 15 | Unify fix systems | Simplify | Consistent behavior, ~1K lines saved |
| 16 | Package consolidation (7 → 3) | Simplify | Contributor experience |
| 17 | Multi-Agent Collaboration | Game-changer | Cross-module features |

---

## Part 5: The North Star

**Today:** "Write a spec, run `af code`, come back to a feature branch."

**Tomorrow:** "Describe what you want in one sentence. The fox analyzes your
codebase, writes the spec, shows you a preview, plans the work, writes the
code, surgically fixes its own mistakes, learns from every session, and
delivers a PR-ready branch with an ADR explaining every design decision — all
while you watch the live dashboard and sip coffee. And it costs half what it
used to."

The gap between today and tomorrow is five capabilities:

1. **Frictionless input** — spec-from-conversation eliminates the authoring
   bottleneck
2. **Self-correction** — micro-fix sessions replace expensive full retries
3. **Transparency** — live dashboard + explain mode build trust
4. **Efficiency** — adaptive cost optimization + incremental execution cut
   costs in half
5. **Memory** — cross-spec intelligence makes every session smarter than the
   last

These five, layered onto the already-solid orchestration engine, would make
af not just an orchestrator but an *autonomous development partner*.

---

## Appendix A: Competitive Landscape

| Tool | Approach | Agent-Fox Advantage |
|------|----------|---------------------|
| Claude Code (solo) | Single-agent, interactive | Multi-session parallel work with knowledge accumulation |
| Cursor / Windsurf | IDE-embedded, interactive | Fully autonomous — no babysitting |
| Devin / Factory | Autonomous cloud agents | Local execution with full git control; spec-driven |
| SWE-Agent | Research benchmark agent | Production-grade: retry, knowledge, merge handling |
| OpenHands | Open-source agent platform | Deeper spec integration and quality pipeline |

Agent-fox's unique position: **spec-driven autonomy with institutional
memory.** No other tool combines structured specifications, multi-archetype
quality pipelines, and cross-session knowledge accumulation.

## Appendix B: What NOT to Simplify

Some complexity earns its keep:

- **Git worktree isolation** — essential for parallel safety
- **The merge lock** — prevents corruption under concurrency
- **The archetype/mode system** (post-consolidation) — separation between
  coding, reviewing, and verifying is fundamental to quality
- **The knowledge store** — institutional memory is the moat
- **Spec-driven execution** — this is the product's identity
- **The retry/circuit-breaker system** — hard-won reliability
- **Prompt caching** — real cost savings with minimal complexity
- **The spec format (v1.3)** — well-designed, validated, and stable

## Appendix C: Codebase Observations

Detailed findings from the deep-dive that inform the recommendations above:

**Engine complexity:** The engine subsystem (10,500 lines) contains inlined
modules that were originally separate — issue summary posting (~260 lines at
the bottom of `engine.py`) and preflight checking (~170 lines at the bottom of
`dispatch.py`). Comments still reference the original module locations.

**Dual workspace health checking:** Pre-run health checks in `run.py` and
per-session health checks in `dispatch.py` have overlapping concerns. Defense
in depth is valuable but the two levels could share more code.

**Config resolution depth:** A model tier for a session can be specified in
four places: `ArchetypeEntry.default_model_tier` → `ModeConfig.model_tier` →
`PerArchetypeConfig.model_tier` → `PerArchetypeConfig.modes[mode].model_tier`.
This is powerful but creates a debugging surface where users don't know which
level is winning.

**Prompt sanitization duplication:** `spec_builder.py` and `triage.py` both
sanitize issue titles and bodies independently, calling
`sanitize_prompt_content` with different labels on the same data flowing
through different code paths.

**Knowledge retrieval inconsistency:** The nightshift fix pipeline uses
`KnowledgeProvider.retrieve()` (clean abstraction), while `fix/analyzer.py`
bypasses it entirely and opens DuckDB directly via `load_review_context()`.
The `query_knowledge_context()` function in analyzer is dead code.

**Integration branch sync escalation:** The merge resolution chain goes
through three fallback layers (rebase → merge commit → AI merge agent) for
what is essentially keeping a branch up to date. This is comprehensive but
each layer adds error handling and recovery code.
