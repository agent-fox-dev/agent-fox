# Erratum: Spec 04 Subcommand Filenames

**Spec:** 04_af_agentic_cli
**Date:** 2026-06-24

## Discrepancy

The spec (tasks.json subtask 5.3 and related design docs) references two
subcommand files by names that differ from the actual codebase filenames:

| Spec reference      | Actual filename      |
|----------------------|----------------------|
| `af/insights.py`     | `af/findings.py`     |
| `af/night_shift.py`  | `af/nightshift.py`   |

## Click command registration

The Click commands are still registered under the names the spec expects:

- `af/findings.py` registers the `insights` command.
- `af/nightshift.py` registers the `night-shift` command.

## Impact

- Tests reference the correct actual filenames (`af/findings.py`,
  `af/nightshift.py`).
- No functional impact: the CLI surface (`af insights`, `night-shift`)
  matches the spec. Note: `night-shift` was extracted from `af` into its
  own standalone CLI package (`nightshift`).
- Only the Python module filenames differ from what the spec text says.
