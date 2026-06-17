"""afspec — Python library for agent-fox specification format (v1)."""

from afspec.bootstrap import BootstrapSpec
from afspec.constructors import (
    complex_event_criterion,
    create_spec,
    event_driven_criterion,
    optional_criterion,
    state_driven_criterion,
    ubiquitous_criterion,
    unwanted_criterion,
)
from afspec.coverage import compute_coverage
from afspec.discovery import DependencyGraph, build_dependency_graph, discover_specs
from afspec.ears import render_ears_sentence
from afspec.exceptions import (
    BootstrapError,
    IntentError,
    LifecycleError,
    LoadError,
    SaveError,
    SpecError,
)
from afspec.intent import compute_intent_hash
from afspec.io import load_spec, marshal_json, save
from afspec.lifecycle import move_to_archive, supersede, transition
from afspec.models import (
    CorrectnessProperty,
    Coverage,
    Criterion,
    DependencyEdge,
    EARSPattern,
    EdgeCaseTest,
    ErrorHandlingEntry,
    ExecutionPath,
    PathStep,
    PRDDocument,
    PRDFrontmatter,
    PropertyTest,
    Requirement,
    Requirements,
    SmokeTest,
    Spec,
    SpecMeta,
    Status,
    Subtask,
    SubtaskState,
    TaskDependency,
    TaskGroup,
    TaskGroupKind,
    Tasks,
    TestCase,
    TestCommands,
    TestSpec,
    TraceabilityEntry,
    UserStory,
    VerificationSubtask,
    valid_transition,
)
from afspec.render import render_combined, render_individual, render_requirements, render_tasks, render_test_spec
from afspec.schemas import schemas
from afspec.validation import ValidationError, validate, validate_cross_file, validate_schema

__all__ = [
    # Core types
    "Spec",
    "PRDDocument",
    "PRDFrontmatter",
    "Requirements",
    "TestSpec",
    "Tasks",
    "Criterion",
    "Requirement",
    "UserStory",
    "CorrectnessProperty",
    "ExecutionPath",
    "PathStep",
    "ErrorHandlingEntry",
    "TestCase",
    "PropertyTest",
    "EdgeCaseTest",
    "SmokeTest",
    "Coverage",
    "TaskGroup",
    "Subtask",
    "VerificationSubtask",
    "TaskDependency",
    "TraceabilityEntry",
    "TestCommands",
    "SpecMeta",
    "DependencyEdge",
    # Enums
    "Status",
    "EARSPattern",
    "SubtaskState",
    "TaskGroupKind",
    # Functions
    "valid_transition",
    "load_spec",
    "save",
    "marshal_json",
    "validate",
    "validate_schema",
    "validate_cross_file",
    "transition",
    "supersede",
    "move_to_archive",
    "compute_intent_hash",
    "compute_coverage",
    "render_requirements",
    "render_test_spec",
    "render_tasks",
    "render_combined",
    "render_individual",
    "render_ears_sentence",
    "discover_specs",
    "build_dependency_graph",
    "create_spec",
    # EARS criterion builders
    "ubiquitous_criterion",
    "event_driven_criterion",
    "complex_event_criterion",
    "state_driven_criterion",
    "unwanted_criterion",
    "optional_criterion",
    # Classes
    "BootstrapSpec",
    "DependencyGraph",
    "ValidationError",
    # Exceptions
    "SpecError",
    "LoadError",
    "SaveError",
    "LifecycleError",
    "IntentError",
    "BootstrapError",
    # Schema access
    "schemas",
]
