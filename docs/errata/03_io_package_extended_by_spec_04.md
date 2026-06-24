# Errata: 03-REQ-1.1, 03-REQ-1.3 — agentfox/io/ package extended by Spec 04

**Spec:** 03_unified_terminal_io
**Requirements:** 03-REQ-1.1, 03-REQ-1.3
**Test Spec:** TS-03-1, TS-03-3
**Task Group:** 8 (original), 9 (checkpoint verification)

## Divergence

### 03-REQ-1.1 — Public API exports

Spec 03 requires `agentfox/io/__init__.py` to re-export "exactly twelve"
public symbols. The current `__init__.py` exports fourteen symbols: the
original twelve from Spec 03 plus `format_table` and `ProgressDisplay`
added by Spec 04 (`04_af_agentic_cli`).

The additional symbols are:

- `format_table` — utility function in `agentfox/io/output.py`
- `ProgressDisplay` — multi-task progress class in `agentfox/io/progress.py`

### 03-REQ-1.3 — Package file count

Spec 03 requires the `agentfox/io/` package to consist of "exactly seven
files." The current package contains nine files: the original seven from
Spec 03 plus `group.py` and `progress.py` added by Spec 04.

The additional files are:

- `group.py` — extracted AgentFoxGroup implementation
- `progress.py` — ProgressDisplay for multi-task orchestration

## Implemented behavior

`agentfox/io/__init__.py` exports all fourteen symbols. The package
directory contains all nine files. All twelve original Spec 03 symbols
remain present and importable. The Spec 03 test suite (TS-03-1, TS-03-3)
was updated by Spec 04 to accept the extended set.

## Rationale

Spec 04 was implemented on the same branch and extended the `agentfox/io/`
package with new functionality. The "exactly twelve" / "exactly seven"
constraints in Spec 03 described the initial package surface at the time
of Spec 03's creation. Spec 04 intentionally grew the package as part of
its requirements (adding agentic CLI features). Reverting to "exactly
twelve" would break Spec 04 functionality.

## Test coverage

The Spec 03 tests validate the **original contracts** while accepting
documented Spec 04 extensions:

- TS-03-1 verifies all twelve original Spec 03 symbols are importable,
  and asserts that any additional symbols are from the known Spec 04
  extension set (`format_table`, `ProgressDisplay`) — undocumented extras
  cause test failure.
- TS-03-3 verifies all seven original Spec 03 files exist, and asserts
  that any additional `.py` files are from the known Spec 04 extension
  set (`group.py`, `progress.py`) — undocumented extras cause test failure.
