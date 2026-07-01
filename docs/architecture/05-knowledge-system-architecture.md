# Knowledge System Architecture

## Conceptual Overview

---

## 1. What the Knowledge System Is

The agent-fox knowledge system is the institutional memory of an autonomous
coding orchestrator. It captures what happens during sessions — quality
concerns, architectural decisions, spec corrections, session summaries — and
makes that information available to future sessions for the same or related
specs. This happens through a clean protocol boundary: the engine calls
`retrieve()` before a session and `ingest()` after, never importing knowledge
internals directly.

The system operates on a fundamental architectural principle: **every coding
session starts with a fresh context window, but not a blank mind.** The
orchestrator deliberately resets the LLM's context between sessions to prevent
accumulated confusion, while the knowledge system provides curated, relevant
prior knowledge to each new session. This separation of concerns — stateless
execution with persistent knowledge — is what allows agent-fox to run
autonomously across dozens or hundreds of sessions without context window
degradation.

---

## 2. The KnowledgeProvider Protocol

The engine interacts with the knowledge system through a two-method protocol:

```
KnowledgeProvider
  retrieve(spec_name, task_description, task_group?, session_id?) → list[str]
  ingest(session_id, spec_name, context)
```

`retrieve()` is called before a session to load relevant knowledge items.
`ingest()` is called after a session to process its outputs and update the
knowledge store.

The concrete implementation is `FoxKnowledgeProvider`, constructed at startup
in `run.py` and threaded through the session runner factory. A
`NoOpKnowledgeProvider` (which discards all ingestion and returns empty
retrieval results) serves as the fallback — useful for testing.

This boundary means the engine never imports knowledge internals directly. The
full knowledge pipeline can be replaced by providing a different implementation
of the protocol.

---

## 3. Knowledge Flow

```
     Coding Session (completed)
          │
          │ context dict
          │  • spec_name
          │  • touched_files
          │  • commit_sha
          │  • session_status
          │  • summary
          │  • archetype, task_group, attempt
          ▼
  ┌───────────────────────────────┐
  │  FoxKnowledgeProvider         │
  │       ingest()                │
  │                               │
  │  supersede injected findings  │
  │  store session summary        │
  └───────────┬───────────────────┘
              │
              ▼
  ┌──────────────────────────────────────────────────────┐
  │                KNOWLEDGE STORE (DuckDB)               │
  │                                                      │
  │   review_findings ──── drift_findings                │
  │   finding_injections ── session_summaries             │
  │   audit_events                                       │
  └──────────────────────┬───────────────────────────────┘
                         │
              ┌──────────▼──────────┐
              │  RETRIEVAL          │
              │  3 query categories │
              │  by spec + group    │
              │  keyword scoring    │
              └──────────┬──────────┘
                         │ prefixed text blocks
              ┌──────────▼──────────┐
              │  CONTEXT INJECTION  │
              │  Into session prompt│
              └─────────────────────┘
```

---

## 4. Ingestion: Post-Session Processing

`FoxKnowledgeProvider.ingest()` runs after every session. It performs up to
three actions:

### 4.1 Finding Supersession

Two distinct supersession mechanisms keep the knowledge store current:

**Injection-based supersession** (review findings). If the session completed
successfully, all review findings that were injected into that session (as
tracked in the `finding_injections` table) are automatically superseded. The logic: if the
coder saw the finding and completed the work, the finding is considered
addressed. Superseded findings remain in the database with a `superseded_by`
reference for audit history but are excluded from active queries. This closes
the retrieve-inject-address-supersede feedback loop — the core mechanism by
which the knowledge store stays current.

**File-based supersession** (drift findings). For completed coder sessions
only, immediately after injection-based supersession, the system calls
`supersede_drift_findings_by_files` to retire drift findings whose
`artifact_ref` matches files modified by the session. This mechanism applies
only to the `drift_findings` table and uses path matching rather than
injection tracking:

- Each active drift finding's `artifact_ref` is normalized: line-number
  suffixes (e.g. `:42`, `:42:10`) are stripped and whitespace is trimmed.
- **Exact matching**: when the normalized `artifact_ref` does not end with
  `/`, the finding is superseded only if the exact path appears in the
  session's `touched_files`.
- **Prefix matching**: when the normalized `artifact_ref` ends with `/`
  (indicating a directory reference), the finding is superseded if any
  touched file starts with that prefix.
- **Null `artifact_ref` fallback**: findings with a null `artifact_ref` are
  never superseded by file matching. They persist until a future drift
  review's `insert_drift_findings` call retires them via bulk supersession.

The file-based supersession call is wrapped in a try/except guard — any
exception is caught and logged as a warning without affecting the session
outcome. Reviewer and verifier sessions do not trigger file-based
supersession.

Without these two mechanisms, resolved findings would accumulate indefinitely
and consume prompt space.

### 4.2 Session Summary Storage

Completed sessions that produced a non-empty summary are stored in the
`session_summaries` table. The summary is extracted from one of two sources:

- The agent's `session-summary.json` artifact (written by coder sessions).
- An auto-generated summary from persisted findings (for reviewer and
  verifier sessions that don't produce artifact files).

The `session-summary.json` schema supports three optional structured fields
beyond the narrative `summary` text:

- `rejected_approaches` — an array of objects, each with an `approach` key
  (what was tried) and a `reason` key (why it was rejected).
- `gotchas` — an array of strings warning the next coder about edge cases,
  fragile patterns, or counter-intuitive behavior.
- `assumptions` — an array of strings recording assumptions made during the
  session that might not hold for later task groups.

When these structured fields are present, the `compose_enriched_summary()`
function merges them with the narrative summary into a single composed text
before database storage. Each rejected approach is formatted as
`Tried: {approach} — rejected because: {reason}`, each gotcha as
`Watch out: {gotcha}`, and each assumption as `Assumes: {assumption}`.
Sections are separated by newlines with no trailing newline. When none of
the structured fields are present, the raw summary text is stored unchanged
— preserving backward compatibility with older session-summary.json files.

Reviewer and verifier sessions that produced no findings are suppressed:
`generate_archetype_summary()` returns `None` for these trivial
sessions, and the existing `if summary_text:` guard prevents database
insertion. This avoids accumulating completion-status noise in the
`session_summaries` table.

Each summary record carries the spec name, task group, archetype, attempt
number, run ID, and creation timestamp. These summaries are later retrieved by
future sessions to provide cross-session context.

### 4.3 Graceful Failure

Any exception during ingestion is logged as a WARNING. The session outcome is
not affected — ingestion never blocks the coding lifecycle.

---

## 5. The Knowledge Store

The knowledge store lives in a single DuckDB database
(`.agent-fox/knowledge.duckdb`). The following tables form the knowledge layer
(separate from plan state and execution state):

### 5.1 `review_findings`

Findings from pre-review, drift-review, and audit-review sessions, classified
by severity (critical, major, minor, observation). Only critical and major
findings are persisted; minor and observation findings are dropped at write
time. Unrecognized severity values are normalized to observation (and
therefore also dropped). Each finding has provenance (spec, task group,
session, attempt) and a `superseded_by` column. Unresolved critical and major
findings are the highest-priority knowledge items — they represent known
quality issues that coders must address.

### 5.2 `drift_findings`

Spec-to-code discrepancies detected by drift-review sessions. Structured
identically to review findings with additional spec and artifact reference
fields.

### 5.3 `finding_injections`

Tracks which review findings were injected into which sessions. This is the
bookkeeping table that enables the supersession lifecycle. When a session
completes, this table is queried to identify which findings to retire.

Cross-group items are informational and are not recorded here — they are not
subject to automatic supersession.

### 5.4 `session_summaries`

Append-only log of enriched session summaries containing non-obvious learnings
rather than completion-status pings. Coder session summaries capture rejected
approaches (techniques tried and abandoned), gotchas (edge cases and fragile
patterns), and assumptions that may not hold for later task groups — composed
into a single text via `compose_enriched_summary()` before storage. Reviewer
and verifier sessions with no findings are suppressed entirely (no row is
written). Each record carries the spec name, task group, archetype, attempt
number, run ID, and summary text. Used for same-spec context retrieval (what
earlier groups learned).

### 5.5 `audit_events`

Structured log of every significant operation: run start, session
start/complete/fail, git merge, config reload, preflight skip. Each event has
a type, severity, run ID, node ID, and a JSON payload.

---

## 6. Retrieval: Finding the Right Knowledge

Before each session, `FoxKnowledgeProvider.retrieve()` is called with the spec
name, a task description derived from subtask bullets, the task group number,
and the session ID. It queries three categories from DuckDB using direct SQL
with keyword-based relevance scoring — no embeddings, no vector search:

| Category | Source | Prefix | Scope |
|---|---|---|---|
| Review findings | `review_findings` | `[REVIEW]` | Active critical/major for this spec and task group |
| Cross-group review findings | `review_findings` | `[CROSS-GROUP]` | Critical/major from other groups in the same spec |
| Same-spec context summaries | `session_summaries` | `[CONTEXT]` | Enriched summaries from earlier sessions on this spec, containing non-obvious learnings such as rejected approaches, gotchas, and assumptions from structured session-summary fields |

### Relevance Scoring

Within each category, results are sorted by keyword overlap between the task
description and the item text. Keywords are extracted as lowercased words from
the task description; each item is scored by counting substring matches. This
ensures the most relevant items appear first when the result cap is reached.

### Priority and Capping

Review findings are never dropped — they represent critical institutional
knowledge. Other categories are capped at configurable maximums. Cross-group
items have a separate, typically smaller cap since they are informational
rather than actionable.

### Injection Tracking

When a `session_id` is provided, the IDs of all injected review findings are
recorded in `finding_injections`. This enables automatic supersession on
session completion (Section 4.1). Cross-group items are not tracked — they are
informational context that does not create supersession obligations.

### Graceful Degradation

If any table does not exist (fresh database) or any query fails, that category
is silently skipped. The session proceeds with whatever knowledge is available.
The knowledge system never prevents a session from launching.

---

## 7. Context Injection: How Knowledge Enters a Session

When the orchestrator prepares a coding session, context assembly follows this
sequence:

1. **Extract subtask descriptions** from the task group in `tasks.md` to
   form the `task_description` passed to `retrieve()`.

2. **Call `KnowledgeProvider.retrieve()`** → returns prefixed text blocks.

3. **Assemble context** — spec files (`requirements.md`, `design.md`,
   `test_spec.md`, `tasks.md`), DB-backed review/drift/verification findings,
   steering directives, the retrieved knowledge items, and prior group findings.

4. **Build system prompt** from the three-layer assembly: agent base profile +
   archetype profile + task context.

5. **Build task prompt** with archetype-specific instructions; for retry
   attempts, the previous error and active critical/major findings are prepended.

6. **Launch session.** The coding agent receives both prompts in a fresh context
   window inside an isolated git worktree.

After the session completes:

7. **Parse review findings** (for review archetypes) and persist to DuckDB.

8. **Converge multi-instance outputs** (if applicable) using mode-specific
   convergence strategies.

9. **Call `KnowledgeProvider.ingest()`** → supersede injected findings, store
   session summary.

10. **Record session outcome** in `session_outcomes`.

11. **Emit audit events** to the per-run JSONL file.

This cycle repeats for every session. Findings are created by review sessions,
injected into coder sessions, and superseded when the coder completes — a
closed loop that keeps the knowledge store current without manual intervention.

---

## 8. The Finding Lifecycle

Findings follow a closed-loop lifecycle that is central to how the knowledge
system operates. Two distinct supersession paths exist, depending on the
finding type:

```
Created (by reviewer/verifier/drift-review session)
    ↓
Active (queryable by context assembly and knowledge retrieval)
    ↓
    ├── Path A: Injection-based (review_findings)
    │   Injected (tracked in finding_injections when served to a session)
    │       ↓
    │   Superseded (retired when the session that received them completes)
    │
    └── Path B: File-based (drift_findings)
        Matched (artifact_ref compared against session's touched_files)
            ↓
        Superseded (retired when a coder session touches the referenced file)
```

**Created.** Review and verifier sessions parse structured JSON output to
produce findings classified by severity. Drift-review sessions produce drift
findings with an `artifact_ref` field referencing the file or directory where
drift was detected. Findings are stored with full provenance: spec name, task
group, session ID, attempt number, archetype, and mode.

**Active.** Findings in the active state are visible to three consumers:
context assembly (rendered as structured markdown in Layer 3 of the system
prompt), knowledge retrieval (returned as `[REVIEW]` items), and retry context
(prepended to coder task prompts on retry attempts).

**Path A — Injection-based supersession** (review findings). When `retrieve()`
serves a finding to a session, the finding ID and
session ID are recorded in `finding_injections`. When that session completes
successfully, `ingest()` queries `finding_injections` for all findings served
to that session and marks them as superseded. This bookkeeping is what enables
automatic supersession — without it, the system would not know which findings
the coder actually saw.

**Path B — File-based supersession** (drift findings). When a coder session
completes, `supersede_drift_findings_by_files` matches each active drift
finding's `artifact_ref` against the session's `touched_files` using two
matching rules:

- **Exact match**: normalized `artifact_ref` (without trailing `/`) must
  equal a path in `touched_files`.
- **Prefix match**: normalized `artifact_ref` ending with `/` matches if any
  touched file starts with that prefix.

Before matching, the `artifact_ref` is normalized by stripping line-number
suffixes (e.g. `:42`) and trimming whitespace. Findings with a null
`artifact_ref` are never superseded by file matching — they persist until a
future drift review's `insert_drift_findings` call retires them via bulk
supersession.

Superseded findings of both types retain their `superseded_by` reference for
audit trail purposes but are excluded from all active queries.

Cross-group findings are not tracked in the injection table. They are
informational — the coder may benefit from seeing them, but they are not
considered addressed merely because the session completed.

---

## 9. Quality Assurance Layer

Beyond the knowledge protocol, agent-fox maintains a quality layer through its
archetype system. Multiple agent archetypes produce structured findings stored
in DuckDB:

### Review Findings

The reviewer archetype in pre-review and audit-review modes produces findings
classified by severity: critical, major, minor, and observation. Critical
security findings always block downstream coder tasks regardless of the
configured blocking threshold. Other critical findings block when they exceed
the threshold.

### Drift Findings

The reviewer archetype in drift-review mode compares spec assumptions against
the actual codebase and detects drift: places where existing code diverges from
what the spec assumes. Each drift finding carries an optional `artifact_ref`
field that records the file or directory path where the drift was detected.

Drift findings are superseded through two mechanisms:

1. **Bulk supersession via `insert_drift_findings`**: when a new drift-review
   session runs, all prior active findings for the same `(spec_name,
   task_group)` are superseded and replaced by the new batch.
2. **File-based supersession via `supersede_drift_findings_by_files`**: after
   each successful coder session merge, drift findings whose `artifact_ref`
   matches any file the session touched are superseded. The matching rules
   normalize the `artifact_ref` (stripping line-number suffixes like `:42`
   and trimming whitespace), then apply exact matching for file references
   or prefix matching for directory references ending with `/`. Findings
   with a null `artifact_ref` are never superseded by file matching and
   persist until a future drift review replaces them.

### Multi-Instance Convergence

When reviewer or verifier nodes run with multiple instances, their outputs are
merged deterministically before persistence:

- **Pre-review and drift-review**: Union all findings, deduplicate, and
  majority-gate critical findings (a finding is only promoted to critical if
  a majority of instances flagged it).
- **Audit-review**: Worst-result-wins — if any instance flags an issue, it
  is included.
- **Verifier**: Majority-vote each requirement result across instances.

This prevents individual outlier sessions from producing spurious blocking
findings while ensuring genuine issues are still caught.

---

## 10. Audit and Observability

Every significant operation emits a structured audit event to the
`SinkDispatcher`. Events carry a type, severity, run ID, node ID, archetype,
and a JSON payload. The sink dispatcher forwards events to all registered
sinks — the DuckDB sink for persistence and a JSONL file sink for
human-readable logs.

Agent conversation traces are a specialized form of audit event. During
session execution, the backend emits structured trace events: `session.init`
(with prompts), `tool.use` (with tool input), `assistant.message` (with
response text), `tool.error` (with error details), and `session.result` (with
metrics). These traces can be reconstructed into full conversation transcripts
from the JSONL audit trail after the fact.

Completed spec audit files are cleaned up at end-of-run to avoid unbounded
growth. Audit retention is managed per-run: the oldest runs beyond a
configurable limit are pruned at the start of each new run.

---

## 11. Design Principles

**A clean protocol boundary.** The engine calls `retrieve()` and `ingest()` on
a `KnowledgeProvider` — it never imports knowledge internals. The implementation
can be replaced (or no-oped) without touching the engine.

**Spec-scoped, high-signal.** Retrieval is intentionally precise: review
findings are keyed by spec and task group, summaries by spec name. This trades
recall breadth for precision — the agent gets a small number of directly
relevant items rather than a large ranked list of loosely related ones.

**No embeddings.** All retrieval uses direct column-filter SQL with keyword
scoring. This eliminates the computational overhead and operational complexity
of embedding generation and vector indexing. The tradeoff favors operational
simplicity over fuzzy semantic discovery.

**Closed-loop finding lifecycle.** Findings are not merely stored — they are
tracked through injection and retired on completion. This prevents resolved
issues from permanently consuming prompt space, while preserving full audit
history through supersession references.

**Cross-session continuity.** Session summaries bridge the context gap between
sessions. Same-spec context summaries give later groups visibility into what
earlier groups learned — including rejected approaches, gotchas, and
assumptions — rather than generic completion status. This provides continuity
of purpose across the session boundary.

**Graceful degradation everywhere.** If knowledge retrieval fails, the session
proceeds without context. If ingestion fails, the session outcome is
unaffected. The knowledge system never blocks the coding lifecycle.

---

*Previous: [Night-Shift Mode](04-night-shift.md)*
