---
spec_id: '06'
spec_name: duckdb_reader_writer_split
title: Duckdb Reader Writer Split
status: draft
created_at: '2026-06-24T09:07:05.837532+00:00'
updated_at: '2026-06-24T09:07:05.837532+00:00'
owner: ''
source: interactive
schema_version: 1
---
# DuckDB Reader/Writer Split

## Problem

DuckDB's single-writer constraint causes contention in agent-fox when multiple
processes or CLI commands interact with `knowledge.duckdb` concurrently. The
orchestrator holds a write lock during long-running nightshift sessions, which
blocks CLI commands (e.g. `af standup`, `af code`, `af findings`) and prevents
read-only queries from completing.

DuckDB supports concurrent readers when one process holds a write lock, but
several call sites in the codebase open the database in read-write mode even
though they only perform read operations. Additionally, the session context
assembly path (`assemble_context`) performs two opportunistic write operations
(`_migrate_legacy_files`, `index_errata_from_markdown`) on what is conceptually
a read path, preventing it from using a read-only connection.

## Goal

Audit every DuckDB connection site and enforce a reader/writer split:

1. **Read-only callers** open connections with `read_only=True` so they can
   proceed concurrently while a writer holds the lock.
2. **Write operations embedded in read paths** are extracted and moved to the
   orchestrator's startup or to dedicated write-path callers.
3. A clear convention is established so future contributors know when to use
   `read_only=True`.

## Scope

### In Scope

- Switch `af code` (`packages/af/af/code.py`) to open the DB read-only. It
  only loads the plan for display — no writes needed.
- Switch `af plan --verify` to open the DB read-only when only verifying, not
  saving.
- Switch `agentfox.fix.analyzer` (`packages/agentfox/agentfox/fix/analyzer.py`)
  to open the DB read-only. It only queries active findings.
- Extract `_migrate_legacy_files()` from `assemble_context()` in
  `packages/agentfox/agentfox/session/context.py` and move it to the
  orchestrator startup path (run once before sessions start, not per-session).
- Extract `index_errata_from_markdown()` from `assemble_context()` and move it
  to the orchestrator startup path.
- Make the `read_only` parameter required (no default) on
  `open_knowledge_store` to force every caller to declare intent.
- Verify `af standup` already uses `read_only=True` (it does — no change
  needed, just confirmation in tests).

### Out of Scope

- Replacing DuckDB with another database.
- Adding a connection broker or background service.
- Write-ahead queue or multi-writer support.
- Changing the nightshift engine or orchestrator write patterns.
- Schema migrations (read-only connections skip migrations, which is correct).

## Technical Context

### Current Connection Sites (Production Code)

| Call site | Current mode | Operations | Target mode |
|-----------|-------------|------------|-------------|
| `af/nightshift.py` (orchestrator) | read-write | INSERT session outcomes, tool calls, audit events; UPDATE plan_nodes | read-write (no change) |
| `agentfox/engine/run.py` (orchestrator) | read-write | Same as nightshift | read-write (no change) |
| `af/plan.py` (save path) | read-write | DELETE + INSERT plan_nodes, plan_edges, plan_meta | read-write (no change) |
| `af/plan.py` (verify path) | read-write | SELECT only | **read-only** |
| `af/reset.py` | read-write | UPDATE plan_nodes | read-write (no change) |
| `af/findings.py` | read-write | SELECT + optional UPDATE (dismiss) | read-write (dismiss needs it) |
| `af/standup.py` | read-only | SELECT only | read-only (already correct) |
| `af/code.py` | read-write | SELECT only (load_plan, compute_phases) | **read-only** |
| `agentfox/fix/analyzer.py` | read-write | SELECT only (query_active_findings) | **read-only** |
| `agentfox/session/context.py` (via orchestrator conn) | read-write | SELECT + legacy migration writes + errata indexing writes | **read-only** (after extracting writes) |

### Key Constraint

`assemble_context()` currently receives the orchestrator's `conn` parameter and
uses it for both reads and writes. The two write operations it performs are:

1. `_migrate_legacy_files(conn, spec_dir, spec_name)` — one-time migration of
   legacy `review.md` / `verification.md` files into DB records. This is
   idempotent and only runs when no DB records exist for the spec.

2. `index_errata_from_markdown(conn, project_root)` — indexes errata markdown
   files into the DB. Also idempotent.

Both should be moved to the orchestrator's startup sequence (before any sessions
are dispatched) so `assemble_context` can work with a read-only connection.

## Design Decisions

1. **Keep `conn` parameter on `assemble_context`, make it read-only.** Sessions
   still need DB access to query findings, verdicts, drift reports, and prior
   group findings. Removing the `conn` parameter entirely and passing pre-fetched
   data would decouple sessions from DuckDB but is a much larger refactor with
   no real benefit — the reads are diverse and query-shaped. The simplest change
   is to keep `conn` but ensure it's a read-only connection after extracting the
   two write operations.

2. **Conditional open mode in `af plan`.** The plan command already has distinct
   code paths for verify vs save. The verify path (line 56) opens the DB just to
   load the plan for comparison — pass `read_only=True` there. The save path
   (lines 248, 297) continues to open read-write. No new functions needed.

3. **Make `read_only` required on `open_knowledge_store`.** Drop the default
   value from the `read_only` parameter on the public factory function
   `open_knowledge_store()`. This forces every caller to explicitly declare
   intent (`read_only=True` or `read_only=False`), making the convention
   self-enforcing. The `KnowledgeDB` class can keep its default for
   internal/test use, but the public API should not allow implicit read-write.
   There are fewer than 10 production callers, so updating them is trivial.

4. **Legacy migration at startup, not per-session.** Moving
   `_migrate_legacy_files` to orchestrator startup means it runs once per
   orchestrator invocation instead of once per `assemble_context` call. Since
   it's idempotent and only triggers when DB records are missing, this is safe.
   The migration covers all specs the orchestrator will work on, so no session
   will encounter un-migrated data.

5. **Errata indexing at startup.** Same rationale. The orchestrator indexes
   errata once before dispatching sessions. Sessions read the indexed data.

6. **`af findings --dismiss` stays read-write.** The dismiss flag requires a
   write, and splitting `findings.py` into separate read/write paths for a
   single optional flag adds complexity without benefit. The command is
   short-lived and unlikely to collide with the orchestrator.

7. **Unit tests only, no concurrent-access integration tests.** DuckDB's
   concurrent reader support is well-documented engine behavior — we don't
   need to test the database engine itself. Unit tests verify each call site
   passes the correct `read_only` flag. This is sufficient because the
   behavioral guarantee comes from DuckDB, not from our code.

## Source

Source: Input provided by user via interactive prompt (conversation about
DuckDB single-writer contention mitigation strategies)

## Acceptance Criteria

1. All production call sites pass an explicit `read_only` kwarg —
   `open_knowledge_store` has no default for `read_only`, so omitting it is a
   compile-time error (TypeError).
2. `af code`, `af plan --verify`, and `fix/analyzer.py` open the DB with
   `read_only=True`.
3. `assemble_context()` performs zero write operations — `_migrate_legacy_files`
   and `index_errata_from_markdown` are called in orchestrator startup instead.
4. `af standup` continues to use `read_only=True` (existing behavior, confirmed
   by test).
5. All existing tests pass (`make check` green).
6. No deprecation window needed — all callers are updated atomically. Tests use
   `KnowledgeDB` directly (which keeps its default) and are unaffected.

