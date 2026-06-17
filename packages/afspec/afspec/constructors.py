"""Factory functions for EARS criteria and spec construction."""

from __future__ import annotations

from afspec.models import (
    Criterion,
    EARSPattern,
    PRDDocument,
    PRDFrontmatter,
    Requirements,
    Spec,
    Status,
    Tasks,
    TestSpec,
)


def create_spec(spec_id: str, spec_name: str) -> Spec:
    """Create a new Spec with initialized sub-artifacts.

    Returns a Spec where all sub-artifacts share the same spec_id and
    spec_name, and the PRD frontmatter status is set to draft.
    """
    frontmatter = PRDFrontmatter(
        spec_id=spec_id,
        spec_name=spec_name,
        status=Status.DRAFT,
    )
    prd = PRDDocument(frontmatter=frontmatter)
    requirements = Requirements(spec_id=spec_id, spec_name=spec_name)
    test_spec = TestSpec(spec_id=spec_id, spec_name=spec_name)
    tasks = Tasks(spec_id=spec_id, spec_name=spec_name)
    return Spec(
        prd=prd,
        requirements=requirements,
        test_spec=test_spec,
        tasks=tasks,
    )


def ubiquitous_criterion(id: str, system: str, action: str) -> Criterion:
    """Create a ubiquitous EARS criterion."""
    return Criterion(
        id=id,
        ears_pattern=EARSPattern.UBIQUITOUS,
        system=system,
        action=action,
    )


def event_driven_criterion(id: str, trigger: str, system: str, action: str) -> Criterion:
    """Create an event-driven EARS criterion."""
    return Criterion(
        id=id,
        ears_pattern=EARSPattern.EVENT_DRIVEN,
        trigger=trigger,
        system=system,
        action=action,
    )


def complex_event_criterion(id: str, trigger: str, condition: str, system: str, action: str) -> Criterion:
    """Create a complex-event EARS criterion."""
    return Criterion(
        id=id,
        ears_pattern=EARSPattern.COMPLEX_EVENT,
        trigger=trigger,
        condition=condition,
        system=system,
        action=action,
    )


def state_driven_criterion(id: str, state: str, system: str, action: str) -> Criterion:
    """Create a state-driven EARS criterion."""
    return Criterion(
        id=id,
        ears_pattern=EARSPattern.STATE_DRIVEN,
        state=state,
        system=system,
        action=action,
    )


def unwanted_criterion(id: str, error_condition: str, system: str, action: str) -> Criterion:
    """Create an unwanted-behavior EARS criterion."""
    return Criterion(
        id=id,
        ears_pattern=EARSPattern.UNWANTED,
        error_condition=error_condition,
        system=system,
        action=action,
    )


def optional_criterion(id: str, feature: str, system: str, action: str) -> Criterion:
    """Create an optional-feature EARS criterion."""
    return Criterion(
        id=id,
        ears_pattern=EARSPattern.OPTIONAL,
        feature=feature,
        system=system,
        action=action,
    )
