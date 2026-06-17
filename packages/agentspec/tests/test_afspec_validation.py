"""Tests for afspec model construction and validation.

Covers artifact validation via Pydantic model construction, replacing
the old stub-based validate_artifact tests. Artifacts are now validated
by constructing afspec Pydantic models (Requirements, TestSpec, Tasks)
rather than via JSON Schema post-hoc validation.
"""

from __future__ import annotations

import json

import pytest
from afspec import Requirements, Tasks, TestSpec
from hypothesis import given, settings
from hypothesis import strategies as st
from pydantic import ValidationError as PydanticValidationError

# ---------------------------------------------------------------------------
# Minimal valid artifact fixtures
# ---------------------------------------------------------------------------

VALID_REQUIREMENTS: dict = {
    "spec_id": "test-01",
    "spec_name": "test_spec",
    "schema_version": 1,
    "introduction": "A test requirements artifact.",
    "glossary": {},
    "requirements": [],
    "correctness_properties": [],
    "execution_paths": [],
    "error_handling": [],
}

VALID_TEST_SPEC: dict = {
    "spec_id": "test-01",
    "spec_name": "test_spec",
    "schema_version": 1,
    "test_cases": [],
    "property_tests": [],
    "edge_case_tests": [],
    "smoke_tests": [],
    "coverage": {
        "requirements_covered": [],
        "properties_covered": [],
        "paths_covered": [],
        "gaps": [],
    },
}

VALID_TASKS: dict = {
    "spec_id": "test-01",
    "spec_name": "test_spec",
    "schema_version": 1,
    "test_commands": {
        "spec_tests": "pytest -q",
        "all_tests": "pytest -q",
        "linter": "ruff check",
    },
    "dependencies": [],
    "task_groups": [],
    "traceability": [],
}

ARTIFACT_MODELS = {
    "requirements": (Requirements, VALID_REQUIREMENTS),
    "test_spec": (TestSpec, VALID_TEST_SPEC),
    "tasks": (Tasks, VALID_TASKS),
}

ARTIFACT_NAMES: list[str] = ["requirements", "test_spec", "tasks"]


# ===================================================================
# Model construction tests
# ===================================================================


def test_requirements_model_importable():
    """afspec model classes are importable."""
    assert Requirements is not None
    assert TestSpec is not None
    assert Tasks is not None


def test_valid_requirements_construct():
    """Valid requirements dict constructs a Requirements model."""
    model = Requirements(**VALID_REQUIREMENTS)
    assert model.spec_id == "test-01"
    assert model.schema_version == 1


def test_valid_test_spec_construct():
    """Valid test_spec dict constructs a TestSpec model."""
    model = TestSpec(**VALID_TEST_SPEC)
    assert model.spec_id == "test-01"


def test_valid_tasks_construct():
    """Valid tasks dict constructs a Tasks model."""
    model = Tasks(**VALID_TASKS)
    assert model.spec_id == "test-01"


@pytest.mark.parametrize("name", ARTIFACT_NAMES)
def test_all_artifact_models_construct(name: str):
    """All three artifact types can be constructed from valid dicts."""
    model_cls, valid_data = ARTIFACT_MODELS[name]
    model = model_cls(**valid_data)
    assert model.spec_id == "test-01"


# ===================================================================
# Schema validation tests
# ===================================================================


def test_schemas_loadable():
    """Bundled JSON schema files exist and are parseable."""
    import importlib.resources

    schema_files = importlib.resources.files("afspec.schemas")
    for filename in ["requirements.v1.json", "test_spec.v1.json", "tasks.v1.json"]:
        data = schema_files.joinpath(filename).read_text(encoding="utf-8")
        schema = json.loads(data)
        assert isinstance(schema, dict)


# ===================================================================
# Glossary format tests
# ===================================================================


def test_glossary_must_be_dict():
    """Glossary field must be a dict (not an array)."""
    data = dict(VALID_REQUIREMENTS)
    data["glossary"] = {"token": "A credential"}
    model = Requirements(**data)
    assert model.glossary == {"token": "A credential"}


def test_glossary_as_array_rejected():
    """Glossary as an array of objects is rejected by Pydantic."""
    data = dict(VALID_REQUIREMENTS)
    data["glossary"] = [{"term": "token", "definition": "A credential"}]
    with pytest.raises(PydanticValidationError):
        Requirements(**data)


# ===================================================================
# EARS pattern tests
# ===================================================================


def test_ears_criterion_event_driven():
    """Event-driven EARS pattern constructs correctly."""
    from afspec import Criterion, EARSPattern

    c = Criterion(
        id="R1.1",
        ears_pattern=EARSPattern.EVENT_DRIVEN,
        trigger="a request arrives",
        system="the system",
        action="shall validate the token",
    )
    assert c.ears_pattern == EARSPattern.EVENT_DRIVEN
    assert c.trigger == "a request arrives"


def test_ears_pattern_string_accepted():
    """EARS pattern as string is accepted by Pydantic."""
    from afspec import Criterion

    c = Criterion(
        id="R1.1",
        ears_pattern="event_driven",
        trigger="a request arrives",
        system="the system",
        action="shall validate",
    )
    assert c.ears_pattern.value == "event_driven"


# ===================================================================
# Round-trip serialization tests
# ===================================================================


def test_requirements_round_trip():
    """Requirements model round-trips through JSON."""
    from afspec import marshal_json

    model = Requirements(**VALID_REQUIREMENTS)
    json_str = marshal_json(model)
    parsed = json.loads(json_str)
    model2 = Requirements(**parsed)
    assert model2.spec_id == model.spec_id
    assert model2.introduction == model.introduction


# ===================================================================
# Property tests
# ===================================================================


@given(
    name=st.sampled_from(ARTIFACT_NAMES),
    extra_id=st.text(min_size=1, max_size=20),
)
@settings(max_examples=30)
def test_valid_content_varying_spec_id(name: str, extra_id: str):
    """Any valid content with a varying spec_id constructs successfully."""
    model_cls, valid_data = ARTIFACT_MODELS[name]
    data = dict(valid_data)
    data["spec_id"] = extra_id if extra_id.strip() else "x"
    model = model_cls(**data)
    assert model.spec_id == data["spec_id"]


# ===================================================================
# Agent integration — model construction is the validation
# ===================================================================


def test_agent_uses_pydantic_validation():
    """agentspec/agent.py uses Pydantic model construction for validation."""
    import inspect

    from agentspec import agent

    source = inspect.getsource(agent.SpecAgent.generate_artifacts)
    assert "model_validate" in source
    assert "PydanticValidationError" in source or "ValidationError" in source


def test_model_validate_smoke():
    """End-to-end: model_validate constructs valid artifact."""
    model = Requirements.model_validate(VALID_REQUIREMENTS)
    assert model.spec_id == "test-01"
    assert model.introduction == "A test requirements artifact."
