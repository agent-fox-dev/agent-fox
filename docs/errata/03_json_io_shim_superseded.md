# Errata: 03-REQ-12 — af/json_io.py shim superseded by Spec 04

**Spec:** 03_unified_terminal_io
**Requirements:** 03-REQ-12.1, 03-REQ-12.2
**Test Spec:** TS-03-57, TS-03-58
**Task Group:** 10

## Divergence

Task group 10 specifies creating `af/json_io.py` as a temporary
compatibility shim that re-exports `emit`, `emit_line`, `emit_error`,
and `read_stdin` from `agentfox.io.json`, with a comment marking it
for removal in Spec 04.

This shim was never created because Spec 04 (`04_af_agentic_cli`) was
implemented first and its requirement 04-REQ-4 explicitly removes
`af/json_io.py` from the codebase. Spec 04 tests (TS-04-17, TS-04-18,
TS-04-E5) assert that:

- `af/json_io.py` does not exist on disk
- No `af.json_io` references remain in the af/ source tree
- `import af.json_io` raises `ModuleNotFoundError`

Creating the shim now would break 3 passing spec 04 tests. The shim's
intended lifecycle (created in Spec 03, removed in Spec 04) is already
complete — Spec 04 landed the removal.

## Implemented behavior

No `af/json_io.py` file exists. All af/ subcommands import directly
from `agentfox.io` (the canonical location), which is the desired
end-state after both Spec 03 and Spec 04.

## Rationale

The compatibility shim was designed as a transitional artifact to
avoid breaking existing `af.json_io` imports during the Spec 03
migration. Since no such imports exist in the current codebase (they
were migrated as part of Spec 04), the shim serves no purpose and
creating it would introduce a regression.

## Test coverage

- TS-03-57 and TS-03-58 were intentionally not created (no shim exists
  to test)
- TS-04-17, TS-04-18, TS-04-E5 verify the shim's absence (all pass)
- All 106 spec 03 IO unit tests pass
- All 5191 tests in the full suite pass
