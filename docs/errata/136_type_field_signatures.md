# Erratum: Spec 136 — Type Field Signature Mismatches

**Spec:** 136_legacy_format_removal
**Date:** 2026-06-15
**Severity:** Critical (blocks faithful implementation of design.md interface)

## Issue

The design.md interface definition for `agent_fox/spec/types.py` specifies
field signatures that differ from the actual `parser.py` dataclasses.
Requirement 136-REQ-1.1 mandates "identical field signatures to those in
the deleted `parser.py`," which directly contradicts the design's interface.

## Discrepancies

### SubtaskDef

| Field | parser.py (actual) | design.md |
|-------|-------------------|-----------|
| id | `str` | `str` |
| title | `str` | `str` |
| completed | `bool` | `bool` |
| optional | *absent* | `bool` |

Design adds an `optional` field that does not exist in parser.py.

### CrossSpecDep

| Field | parser.py (actual) | design.md |
|-------|-------------------|-----------|
| from_spec | `str` | `str` |
| from_group | `int` | `int` |
| to_spec | `str` | `str` |
| to_group | `int` | `int` |
| relationship | *absent* | `str` |

Design adds a `relationship` field that does not exist in parser.py.

### TaskGroupDef

| Field | parser.py (actual) | design.md |
|-------|-------------------|-----------|
| subtasks | `tuple[SubtaskDef, ...]` | `list[SubtaskDef]` |

Design uses `list` but parser.py uses `tuple`. The dataclass is also
`frozen=True` in parser.py, which the design does not specify.

## Resolution

Tests and implementation follow the parser.py actual signatures
(requirement 136-REQ-1.1 takes precedence over design.md interface):

- `SubtaskDef`: 3 fields (id, title, completed)
- `CrossSpecDep`: 4 fields (from_spec, from_group, to_spec, to_group)
- `TaskGroupDef.subtasks`: `tuple[SubtaskDef, ...]`
- All dataclasses: `frozen=True`

The test_spec.md pseudocode for TS-136-1 also uses the design's erroneous
signatures (`optional=False`, `relationship="test"`). The actual tests
follow parser.py's field signatures instead.
