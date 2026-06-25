# Errata: 11_enrich_context_summaries

## E1: Parameter name — `archetype` not `session_type`

**Test Spec refs:** TS-11-16, TS-11-17, TS-11-18, TS-11-19, TS-11-E4

The test spec pseudocode uses `session_type=` as the keyword argument for
`generate_archetype_summary()`. The actual function signature in
`formatting.py` uses `archetype` as the parameter name. Tests use
`archetype=` (positional first arg) to match the real function signature.

## E2: Session-summary.json format — top-level keys

**Test Spec refs:** TS-11-1, TS-11-2, TS-11-3, TS-11-4

The test spec wraps fields under a `"session_summary"` top-level key.
Production `_read_session_artifacts()` returns a flat JSON dict with all
fields at the top level (e.g. `{"summary": "...", "rejected_approaches":
[...]}`). Tests use the top-level format consistent with production.

## E3: Spec 120 conflict — empty findings/verdicts behavior

**Requirement refs:** 11-REQ-4.1 vs 120-REQ-3.E1, 11-REQ-4.2 vs 120-REQ-3.E2

Spec 11 requires `generate_archetype_summary` to return `None` for empty
findings/verdicts. Spec 120 requires it to return a non-empty string for
the same cases. Spec 11 supersedes spec 120 for this behavior. Existing
spec-120 tests (`TestReviewerZeroFindings`, `TestVerifierZeroVerdicts` in
`test_archetype_summaries.py`) will need to be updated when spec 11 is
implemented.

## E4: TS-11-E4 count attribute edge case

**Requirement refs:** 11-REQ-4.E1

The test spec input `[{"count": 0, "severity": "high"}, ...]` uses dict-style
objects, but `generate_archetype_summary` accesses attributes via `getattr()`.
Production `ReviewFinding` objects don't have a `count` attribute.
Tests use `SimpleNamespace(count=0, severity="high")` mock objects to satisfy
the attribute-access pattern while testing the zero-count edge case.
