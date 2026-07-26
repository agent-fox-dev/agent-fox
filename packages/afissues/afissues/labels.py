"""Centralised label constants for agent-fox platform operations.

The agent-fox pipeline requires these labels to exist on the target
repository before it can assign them to issues. Use the REQUIRED_LABELS
list with ``platform.create_label`` (called automatically by ``af init``)
to ensure they are present.

Requirements: 358-REQ-1, 358-REQ-2, 358-REQ-3
"""

from __future__ import annotations

from dataclasses import dataclass

# ---------------------------------------------------------------------------
# Label name constants
# ---------------------------------------------------------------------------

#: Applied to issues managed by the fix pipeline.
LABEL_FIX: str = "af:fix"

#: Applied to issues resolved by the fix pipeline (provenance + re-process guard).
LABEL_FIXED: str = "af:fixed"

#: Applied when the coder produced no commits — needs human review.
LABEL_NO_CHANGE: str = "af:no-change"

#: Applied when all task groups for a spec are completed — awaiting verification.
LABEL_IMPLEMENTED: str = "af:implemented"

#: Applied when a pull request has been created and is awaiting merge.
LABEL_PR: str = "af:pr"

#: Priority labels for processing order (high > medium/none > low).
LABEL_PRIORITY_HIGH: str = "priority:high"
LABEL_PRIORITY_MEDIUM: str = "priority:medium"
LABEL_PRIORITY_LOW: str = "priority:low"


# ---------------------------------------------------------------------------
# Label metadata for idempotent creation
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class LabelSpec:
    """Specification for a platform label to be created on init."""

    name: str
    color: str  # 6-character hex without leading #
    description: str


#: Labels that must exist on the target repository for agent-fox to operate.
REQUIRED_LABELS: list[LabelSpec] = [
    LabelSpec(
        name=LABEL_FIX,
        color="12ec39",
        description="Issues ready to be implemented by the fix pipeline",
    ),
    LabelSpec(
        name=LABEL_FIXED,
        color="2ea44f",
        description="Issues resolved by the agent-fox fix pipeline",
    ),
    LabelSpec(
        name=LABEL_NO_CHANGE,
        color="e4e669",
        description="Fix attempt produced no commits — needs human review",
    ),
    LabelSpec(
        name=LABEL_IMPLEMENTED,
        color="0969da",
        description="Spec implementation complete — awaiting manual verification",
    ),
    LabelSpec(
        name=LABEL_PR,
        color="#1d76db",
        description="Pull request created — awaiting merge",
    ),
]
