# PRD: Night-Shift Prior Attempt Context

## Problem Statement

The night-shift fix pipeline writes session outcomes to the DuckDB knowledge
store but never reads them back. When an issue that was previously attempted
(e.g., labelled `af:no-change` and then re-labelled `af:fix`) is encountered
again, the coder session starts from scratch with no memory of what was tried
or why it failed. This leads to the agent repeating the same failed approach.

## Goals

Feed prior fix attempt context into the coder prompt when processing an issue
that has been attempted before. The coder should know what was tried, what the
outcome was, and what the error was so it can try a different approach.

## Approach

1. **Query function**: Before dispatching a fix session, query
   `session_outcomes` for prior coder sessions matching the same issue number
   (via `spec_name = 'fix-issue-{N}'`). Exclude sessions from the current run.
   Return the 3 most recent prior attempts, newest first.

2. **Context formatting**: Format the query results into a concise markdown
   block with date, outcome status, and error message for each prior attempt.
   Truncate individual error messages to 500 characters to bound prompt size.

3. **Prompt injection**: Pass the formatted context into
   `FixPipeline._build_coder_prompt()` and inject it into the task prompt
   before the issue description.

## Design Decisions

1. **Data source: `session_outcomes` table.** Session summaries
   (`session_summaries` table) would be richer, but fix pipeline sessions do
   not currently write to that table — only the `code` command does (via
   `FoxKnowledgeProvider`). Rather than adding summary ingestion to the fix
   pipeline (a separate concern), this spec uses the data that is already
   being written: `session_outcomes.status`, `session_outcomes.error_message`,
   `session_outcomes.created_at`, and `session_outcomes.model`. This is
   sufficient to tell the coder "the last attempt on 2026-05-28 using
   claude-sonnet failed with: merge conflict in parser.py".

2. **History depth: 3 most recent prior runs.** Each prior attempt adds
   ~200-400 tokens to the prompt. Three attempts show enough pattern without
   excessive prompt bloat. Only coder-archetype sessions are included
   (reviewer/triage sessions are internal and not useful context for the
   coder).

3. **Grouping: by run, not by session.** A single fix run may have multiple
   coder sessions (retries within the coder-reviewer loop). The context should
   show one entry per prior *run*, using the last coder session from each run
   (which has the final outcome). This avoids showing 3 entries that are all
   from the same retry sequence.

4. **Injection point: task prompt.** The existing `review_feedback` for
   intra-run retries is injected into the task prompt. Prior attempt context
   follows the same pattern, placed before the issue description so the coder
   sees it first.

5. **Fail-open: query errors are non-fatal.** If the DB query fails (e.g.,
   table doesn't exist yet, connection issue), log a warning and proceed
   without prior context. The fix pipeline must never fail because of a
   context retrieval error.

6. **No new tables or migrations.** This feature uses existing tables and
   columns. No schema changes required.

## Source

Source: Input provided by user via interactive prompt.
