# Errata: verification_results callers not covered by spec 10

**Spec:** 10_remove_unused_knowledge_channels
**Severity:** Critical
**Requirement:** 10-REQ-1.1 (Drop verification_results table)

## Issue

Spec 10 (10-REQ-1.1) adds a migration (v26) that drops the
`verification_results` table, but does not include requirements or tasks
to update all production code that queries or mutates this table. The
six functions listed in 10-REQ-4.1 for removal from `review_store.py`
do not cover all callers.

## Remaining callers after spec 10 task groups 4-9

The following production modules still reference `verification_results`
and will fail at runtime after the migration is applied:

| Module | Function/Location | Impact |
|--------|-------------------|--------|
| `session/context.py:520-524` | `_query_findings_table("verification_results", ...)` | Breaks coder session context assembly |
| `reporting/findings.py:183-213` | `_query_verification_results()` | Breaks verifier-archetype finding queries |
| `reporting/findings.py:426-449` | `find_finding_by_id()` | Breaks finding lookup by ID |
| `graph/injection.py:489` | `print_review_only_summary()` | Breaks review-only run summary output |
| `review_store.py:420` | `query_verdicts_by_session()` | Breaks verifier summary path (called from session_lifecycle.py:984) |
| `review_store.py:660` | `supersede_injected_findings()` | Breaks injection dedup (called from fox_provider.py:298) |
| `review_store.py:598` | `dismiss_finding_by_id()` | Breaks finding dismissal |
| `engine/reset.py:42` | Table name in reset list | Fails on DB reset |
| `session/review_parser.py:309,659` | `parse_verification_results()` | Parses verifier output into dropped table |

## Resolution

All callers were removed or updated in issue #647. Functions that existed
solely to query the dropped table were deleted; functions that queried
multiple tables had their `verification_results` branch removed.

## Source

Identified by reviewer findings in spec 10 review session (critical
and major severity).
