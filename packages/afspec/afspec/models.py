"""Data model types for the afspec library.

Pydantic model definitions for all spec artifacts: PRD, Requirements,
TestSpec, Tasks, and the top-level Spec container. Also enums for
Status, EARSPattern, SubtaskState, TaskGroupKind, and the
valid_transition function for the subtask state machine.
"""

from __future__ import annotations

from enum import Enum
from typing import Any, Optional

from pydantic import BaseModel, Field, PrivateAttr, field_validator

from afspec.exceptions import LifecycleError

# ---------------------------------------------------------------------------
# Enums
# ---------------------------------------------------------------------------


class Status(str, Enum):
    """Lifecycle states for specs."""

    DRAFT = "draft"
    ACTIVE = "active"
    SEALED = "sealed"
    SUPERSEDED = "superseded"
    ARCHIVED = "archived"


class EARSPattern(str, Enum):
    """EARS requirement patterns."""

    UBIQUITOUS = "ubiquitous"
    EVENT_DRIVEN = "event_driven"
    COMPLEX_EVENT = "complex_event"
    STATE_DRIVEN = "state_driven"
    UNWANTED = "unwanted"
    OPTIONAL = "optional"


class SubtaskState(str, Enum):
    """Subtask states in the task state machine."""

    PENDING = "pending"
    QUEUED = "queued"
    IN_PROGRESS = "in_progress"
    DONE = "done"
    PENDING_REEVALUATION = "pending_reevaluation"
    DROPPED = "dropped"


class TaskGroupKind(str, Enum):
    """Task group kinds."""

    TESTS = "tests"
    STANDARD = "standard"
    CHECKPOINT = "checkpoint"
    WIRING_VERIFICATION = "wiring_verification"


# ---------------------------------------------------------------------------
# Subtask state machine
# ---------------------------------------------------------------------------

_LEGAL_TRANSITIONS: set[tuple[SubtaskState, SubtaskState]] = {
    (SubtaskState.PENDING, SubtaskState.QUEUED),
    (SubtaskState.PENDING, SubtaskState.DROPPED),
    (SubtaskState.QUEUED, SubtaskState.IN_PROGRESS),
    (SubtaskState.QUEUED, SubtaskState.PENDING),
    (SubtaskState.QUEUED, SubtaskState.DROPPED),
    (SubtaskState.IN_PROGRESS, SubtaskState.DONE),
    (SubtaskState.IN_PROGRESS, SubtaskState.PENDING_REEVALUATION),
    (SubtaskState.DONE, SubtaskState.PENDING_REEVALUATION),
    (SubtaskState.PENDING_REEVALUATION, SubtaskState.PENDING),
    (SubtaskState.PENDING_REEVALUATION, SubtaskState.DROPPED),
}


def valid_transition(current: SubtaskState, target: SubtaskState) -> bool:
    """Check if a subtask state transition is legal.

    Returns True if the transition from current to target is in the legal
    transition set defined in docs/spec-format.md section 8.3.1.
    """
    return (current, target) in _LEGAL_TRANSITIONS


# ---------------------------------------------------------------------------
# PRD Models
# ---------------------------------------------------------------------------


class PRDFrontmatter(BaseModel):
    """YAML frontmatter from prd.md.

    Fields are declared in the order they appear in the YAML frontmatter,
    matching docs/spec-format.md section 4.1.
    """

    spec_id: str = ""
    spec_name: str = ""
    title: str = ""
    status: Status = Status.DRAFT
    created_at: str = ""
    updated_at: str = ""
    owner: str = ""
    source: str = ""
    supersedes: list[str] = Field(default_factory=list)
    tags: list[str] = Field(default_factory=list)
    intent_hash: Optional[str] = None
    schema_version: int = 1


class PRDDocument(BaseModel):
    """Parsed prd.md document containing frontmatter and body."""

    frontmatter: PRDFrontmatter = Field(default_factory=PRDFrontmatter)
    body: str = ""


# ---------------------------------------------------------------------------
# Requirements Models
# ---------------------------------------------------------------------------


class UserStory(BaseModel):
    """User story for a requirement."""

    role: str = ""
    goal: str = ""
    benefit: str = ""


class Criterion(BaseModel):
    """EARS acceptance criterion or edge case.

    Common fields (all patterns): id, ears_pattern, system, action,
    return_contract. Pattern-specific fields are Optional and excluded
    from JSON serialization when None (matching Go's omitempty).
    """

    id: str = ""
    ears_pattern: EARSPattern = EARSPattern.UBIQUITOUS
    system: str = ""
    action: str = ""
    return_contract: Optional[str] = None
    # Pattern-specific fields (Optional, excluded from JSON when None)
    trigger: Optional[str] = None
    condition: Optional[str] = None
    error_condition: Optional[str] = None
    state: Optional[str] = None
    feature: Optional[str] = None

    def with_return_contract(self, rc: str) -> Criterion:
        """Return a new Criterion with return_contract set."""
        return self.model_copy(update={"return_contract": rc})


class Requirement(BaseModel):
    """A single requirement with criteria and edge cases."""

    id: str = ""
    title: str = ""
    user_story: UserStory = Field(default_factory=UserStory)
    acceptance_criteria: list[Criterion] = Field(default_factory=list)
    edge_cases: list[Criterion] = Field(default_factory=list)


class CorrectnessProperty(BaseModel):
    """Correctness property / invariant."""

    id: str = ""
    title: str = ""
    for_any: str = ""
    invariant: str = ""
    validates: list[str] = Field(default_factory=list)


class PathStep(BaseModel):
    """A single step in an execution path."""

    actor: str = ""
    action: str = ""


class ExecutionPath(BaseModel):
    """An execution path through the system."""

    id: str = ""
    title: str = ""
    steps: list[PathStep] = Field(default_factory=list)


class ErrorHandlingEntry(BaseModel):
    """Error condition -> behavior mapping."""

    id: str = ""
    condition: str = ""
    behavior: str = ""
    requirement_id: str = ""


class ExternalAPISymbol(BaseModel):
    """A verified symbol from an external package."""

    name: str = ""
    import_path: str = ""
    signature: str = ""
    notes: str = ""


class ExternalAPI(BaseModel):
    """Verified API surface for an external dependency."""

    package: str = ""
    version: str = ""
    symbols: list[ExternalAPISymbol] = Field(default_factory=list)


class Requirements(BaseModel):
    """The requirements.json artifact."""

    schema_ref: Optional[str] = Field(default=None, alias="$schema")
    spec_id: str = ""
    spec_name: str = ""
    schema_version: int = 1
    introduction: str = ""
    glossary: dict[str, str] = Field(default_factory=dict)
    requirements: list[Requirement] = Field(default_factory=list)
    correctness_properties: list[CorrectnessProperty] = Field(default_factory=list)
    execution_paths: list[ExecutionPath] = Field(default_factory=list)
    error_handling: list[ErrorHandlingEntry] = Field(default_factory=list)
    external_apis: list[ExternalAPI] = Field(default_factory=list)

    model_config = {"populate_by_name": True}


# ---------------------------------------------------------------------------
# TestSpec Models
# ---------------------------------------------------------------------------


class TestCase(BaseModel):
    """A test case for an acceptance criterion."""

    id: str = ""
    requirement_id: str = ""
    kind: str = ""
    description: str = ""
    preconditions: list[str] = Field(default_factory=list)
    input: Any = None
    expected: Any = None
    assertion_pseudocode: str = ""


class PropertyTest(BaseModel):
    """A property-based test for a correctness property."""

    id: str = ""
    property_id: str = ""
    validates: list[str] = Field(default_factory=list)
    description: str = ""
    for_any_strategy: str = ""
    invariant_check: str = ""


class EdgeCaseTest(BaseModel):
    """A test for an edge case."""

    id: str = ""
    requirement_id: str = ""
    kind: str = ""
    description: str = ""
    preconditions: list[str] = Field(default_factory=list)
    input: Any = None
    expected: Any = None
    assertion_pseudocode: str = ""


class SmokeTest(BaseModel):
    """An integration smoke test for an execution path."""

    id: str = ""
    execution_path_id: str = ""
    description: str = ""
    trigger: str = ""
    real_components: list[str] = Field(default_factory=list)
    mockable: list[str] = Field(default_factory=list)
    expected_effects: list[str] = Field(default_factory=list)


class Coverage(BaseModel):
    """Computed coverage summary."""

    requirements_covered: list[str] = Field(default_factory=list)
    properties_covered: list[str] = Field(default_factory=list)
    paths_covered: list[str] = Field(default_factory=list)
    gaps: list[str] = Field(default_factory=list)


class TestSpec(BaseModel):
    """The test_spec.json artifact."""

    schema_ref: Optional[str] = Field(default=None, alias="$schema")
    spec_id: str = ""
    spec_name: str = ""
    schema_version: int = 1
    test_cases: list[TestCase] = Field(default_factory=list)
    property_tests: list[PropertyTest] = Field(default_factory=list)
    edge_case_tests: list[EdgeCaseTest] = Field(default_factory=list)
    smoke_tests: list[SmokeTest] = Field(default_factory=list)
    coverage: Coverage = Field(default_factory=Coverage)

    model_config = {"populate_by_name": True}


# ---------------------------------------------------------------------------
# Tasks Models
# ---------------------------------------------------------------------------


class TestCommands(BaseModel):
    """Test command configuration."""

    spec_tests: str = ""
    all_tests: str = ""
    linter: str = ""


class TaskDependency(BaseModel):
    """Cross-spec dependency declaration."""

    depends_on_spec: str = ""
    from_group: int = 0
    to_group: int = 0
    relationship: str = ""
    sentinel: bool = False


class VerificationSubtask(BaseModel):
    """Verification subtask for a task group."""

    id: str = ""
    checks: list[str] = Field(default_factory=list)


class Subtask(BaseModel):
    """A subtask within a task group."""

    id: str = ""
    title: str = ""
    details: list[str] = Field(default_factory=list)
    test_spec_refs: list[str] = Field(default_factory=list)
    requirement_refs: list[str] = Field(default_factory=list)
    state: SubtaskState = SubtaskState.PENDING
    optional: bool = False

    @field_validator("details", mode="before")
    @classmethod
    def _coerce_details_to_list(cls, v: Any) -> list[str]:
        if isinstance(v, str):
            return [v]
        return v

    def transition_to(self, target: SubtaskState) -> Subtask:
        """Transition to a new state.

        Returns a new Subtask with the updated state if the transition is
        legal, or raises LifecycleError if the transition is illegal.
        """
        if not valid_transition(self.state, target):
            raise LifecycleError(f"Illegal subtask transition from {self.state.value} to {target.value}")
        return self.model_copy(update={"state": target})


class TaskGroup(BaseModel):
    """A task group containing subtasks."""

    id: int = 0
    kind: TaskGroupKind = TaskGroupKind.STANDARD
    title: str = ""
    subtasks: list[Subtask] = Field(default_factory=list)
    verification: VerificationSubtask = Field(default_factory=VerificationSubtask)


class TraceabilityEntry(BaseModel):
    """Traceability link from requirement to test to task."""

    requirement_id: str = ""
    test_spec_id: str = ""
    task_id: str = ""
    test_path: Optional[str] = None


class Tasks(BaseModel):
    """The tasks.json artifact."""

    schema_ref: Optional[str] = Field(default=None, alias="$schema")
    spec_id: str = ""
    spec_name: str = ""
    schema_version: int = 1
    test_commands: TestCommands = Field(default_factory=TestCommands)
    dependencies: list[TaskDependency] = Field(default_factory=list)
    task_groups: list[TaskGroup] = Field(default_factory=list)
    traceability: list[TraceabilityEntry] = Field(default_factory=list)

    model_config = {"populate_by_name": True}


# ---------------------------------------------------------------------------
# Spec (top-level)
# ---------------------------------------------------------------------------


class _ImmutableSnapshot(BaseModel):
    """Snapshot of immutable fields captured at load time for mutation guard."""

    spec_id: str
    spec_name: str
    created_at: str


class Spec(BaseModel):
    """Complete specification package with all four artifacts."""

    prd: PRDDocument = Field(default_factory=PRDDocument)
    requirements: Requirements = Field(default_factory=Requirements)
    test_spec: TestSpec = Field(default_factory=TestSpec)
    tasks: Tasks = Field(default_factory=Tasks)
    architecture: str | None = None
    _loaded: Optional[_ImmutableSnapshot] = PrivateAttr(default=None)


# ---------------------------------------------------------------------------
# Discovery Models
# ---------------------------------------------------------------------------


class SpecMeta(BaseModel):
    """Lightweight spec metadata from discovery."""

    spec_id: str = ""
    spec_name: str = ""
    status: Status = Status.DRAFT
    dir: str = ""


class DependencyEdge(BaseModel):
    """An edge in the dependency graph."""

    from_spec: str = ""
    to_spec: str = ""
    from_group: int = 0
    to_group: int = 0
    relationship: str = ""
