"""afaudit — audit file-writing infrastructure for agent-fox.

Re-exports the public API so consumers can ``from afaudit import <symbol>``.
Symbols are added incrementally as submodules are implemented.
"""

from afaudit.constants import AUDIT_DIR

__all__ = [
    "AUDIT_DIR",
]
