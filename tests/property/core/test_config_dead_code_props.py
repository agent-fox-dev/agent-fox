"""Property tests for config dead code removal.

Test Spec: TS-130-P1 (silent ignore of old config keys),
           TS-130-P2 (metadata keys match real fields)
Properties: Property 2, Property 3 from design.md
Requirements: 130-REQ-1.E1, 130-REQ-2.E1, 130-REQ-3.1, 130-REQ-3.2,
              130-REQ-3.E1, 130-REQ-4.1, 130-REQ-4.2, 130-REQ-1.5,
              130-REQ-2.5
"""

from __future__ import annotations

import inspect

from hypothesis import given, settings
from hypothesis import strategies as st

from agent_fox.core.config import AgentFoxConfig
from agent_fox.core.config_gen import (
    _BOUNDS_MAP_OVERRIDES,
)

# ---------------------------------------------------------------------------
# TS-130-P1: Silent ignore of old config keys (Property 2)
# ---------------------------------------------------------------------------


# Strategy: generate arbitrary values for each removed key
_removed_orchestrator_keys = {
    "quality_gate": st.text(min_size=0, max_size=50),
    "quality_gate_timeout": st.integers(min_value=0, max_value=3600),
}

_removed_models_keys = {
    "coding": st.sampled_from(["SIMPLE", "STANDARD", "ADVANCED"]),
    "memory_extraction": st.sampled_from(["SIMPLE", "STANDARD"]),
}

_removed_archetype_keys = {
    "triage": st.booleans(),
    "skeptic": st.booleans(),
    "oracle": st.booleans(),
    "auditor": st.booleans(),
    "skeptic_config": st.text(min_size=0, max_size=20),
    "fix_reviewer": st.booleans(),
    "fix_coder": st.booleans(),
}


@st.composite
def removed_keys_dict(draw: st.DrawFn) -> dict:
    """Build a config dict with a random subset of removed keys."""
    result: dict = {}

    # Optionally add removed orchestrator keys
    if draw(st.booleans()):
        orch: dict = {}
        for key, strategy in _removed_orchestrator_keys.items():
            if draw(st.booleans()):
                orch[key] = draw(strategy)
        if orch:
            result["orchestrator"] = orch

    # Optionally add removed models section
    if draw(st.booleans()):
        models: dict = {}
        for key, strategy in _removed_models_keys.items():
            if draw(st.booleans()):
                models[key] = draw(strategy)
        if models:
            result["models"] = models

    # Optionally add removed archetype keys
    if draw(st.booleans()):
        archetypes: dict = {}
        for key, strategy in _removed_archetype_keys.items():
            if draw(st.booleans()):
                archetypes[key] = draw(strategy)
        if archetypes:
            result["archetypes"] = archetypes

    return result


class TestSilentIgnoreOldKeys:
    """TS-130-P1: Config parsing silently ignores any combination of removed keys.

    Property 2 from design.md.
    Requirements: 130-REQ-1.E1, 130-REQ-2.E1, 130-REQ-3.1, 130-REQ-3.2,
                  130-REQ-3.E1
    """

    @given(data=removed_keys_dict())
    @settings(max_examples=50)
    def test_silent_ignore(self, data: dict) -> None:
        """Config with arbitrary removed keys parses without error."""
        config = AgentFoxConfig.model_validate(data)
        default_config = AgentFoxConfig()

        # Remaining fields should have default values
        assert config.orchestrator.parallel == default_config.orchestrator.parallel
        assert config.orchestrator.max_budget_usd == default_config.orchestrator.max_budget_usd

        # The models field must not exist
        assert "models" not in AgentFoxConfig.model_fields


# ---------------------------------------------------------------------------
# TS-130-P2: Metadata keys match real fields (Property 3)
# ---------------------------------------------------------------------------


def _get_config_model_by_name(name: str) -> type | None:
    """Look up a Pydantic config model class by name from config module."""
    import agent_fox.core.config as config_mod

    for attr_name in dir(config_mod):
        obj = getattr(config_mod, attr_name)
        if inspect.isclass(obj) and obj.__name__ == name:
            return obj
    return None


class TestMetadataKeysMatchFields:
    """TS-130-P2: Every _BOUNDS_MAP_OVERRIDES key corresponds to a real Pydantic field.

    Property 3 from design.md.
    Requirements: 130-REQ-4.1, 130-REQ-4.2, 130-REQ-1.5, 130-REQ-2.5
    """

    def test_bounds_map_keys_match_real_fields(self) -> None:
        """Every (model_name, field_name) in _BOUNDS_MAP_OVERRIDES has a real field."""
        for model_name, field_name in _BOUNDS_MAP_OVERRIDES:
            model_cls = _get_config_model_by_name(model_name)
            assert model_cls is not None, (
                f"_BOUNDS_MAP_OVERRIDES references non-existent model '{model_name}'"
            )
            assert field_name in model_cls.model_fields, (
                f"_BOUNDS_MAP_OVERRIDES references non-existent field "
                f"'{model_name}.{field_name}'"
            )
