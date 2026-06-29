"""NodeSessionRunner two-step model variant wiring tests.

Test Spec: TS-14-43, TS-14-44, TS-14-E8
Requirements: 14-REQ-12.1, 14-REQ-12.2, 14-REQ-12.E1
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest
from agentfox.core.config import AgentFoxConfig

# NodeSessionRunner import chain pulls in ui.progress → rich.
# Runtime tests that instantiate NodeSessionRunner are skipped when rich is
# unavailable; source-inspection tests that only read the .py file work fine.
try:
    import rich  # noqa: F401
    _has_rich = True
except ModuleNotFoundError:
    _has_rich = False

_skip_no_rich = pytest.mark.skipif(
    not _has_rich, reason="rich not installed; NodeSessionRunner import chain fails"
)

# ---------------------------------------------------------------------------
# TS-14-43: NodeSessionRunner calls resolve_model_variant() first, then
#           passes the result as variant= to resolve_model(), in that order
# Requirement: 14-REQ-12.1
# ---------------------------------------------------------------------------


@_skip_no_rich
class TestNodeSessionRunnerCallOrder:
    """Verify resolve_model_variant is called before resolve_model with variant= kwarg."""

    def test_variant_resolved_before_model(self) -> None:
        """TS-14-43: resolve_model_variant is called first; resolve_model receives variant='extended'."""
        from agentfox.engine.session_lifecycle import NodeSessionRunner

        call_order: list[str] = []

        def track_resolve_model_variant(*args: object, **kwargs: object) -> str:
            call_order.append("variant")
            return "extended"

        def track_resolve_model(*args: object, **kwargs: object) -> str:
            call_order.append("model")
            return "claude-opus-4-6[1m]"

        mock_parsed = MagicMock(spec_name="test_spec", group_number=1)

        with (
            patch(
                "agentfox.engine.session_lifecycle.resolve_model_variant",
                create=True,
                side_effect=track_resolve_model_variant,
            ) as mock_rmv,
            patch(
                "agentfox.engine.session_lifecycle.resolve_model",
                side_effect=track_resolve_model,
            ) as mock_rm,
            patch("agentfox.engine.session_lifecycle.resolve_model_tier", return_value="ADVANCED"),
            patch("agentfox.engine.session_lifecycle.resolve_security_config", return_value=None),
            patch("agentfox.engine.session_lifecycle.clamp_instances", side_effect=lambda a, i, **kw: i),
            patch("agentfox.engine.session_lifecycle.parse_node_id", return_value=mock_parsed),
        ):
            NodeSessionRunner(
                node_id="test_spec_1_coder_1",
                config=AgentFoxConfig(),
                knowledge_db=MagicMock(),
            )

            # Verify call order: variant first, model second
            assert call_order == ["variant", "model"], (
                f"Expected ['variant', 'model'] but got {call_order}"
            )
            # Verify resolve_model received variant='extended' as keyword argument
            mock_rmv.assert_called_once()
            mock_rm.assert_called_once()
            assert mock_rm.call_args.kwargs.get("variant") == "extended"

    def test_variant_resolved_for_assessed_tier_path(self) -> None:
        """TS-14-43 corollary: Even with assessed_tier, resolve_model_variant is called
        and its result is passed to resolve_model.
        """
        from agentfox.core.models import ModelTier
        from agentfox.engine.session_lifecycle import NodeSessionRunner

        mock_parsed = MagicMock(spec_name="test_spec", group_number=1)

        with (
            patch(
                "agentfox.engine.session_lifecycle.resolve_model_variant",
                create=True,
                return_value="extended",
            ) as mock_rmv,
            patch(
                "agentfox.engine.session_lifecycle.resolve_model",
                return_value="claude-opus-4-6[1m]",
            ) as mock_rm,
            patch("agentfox.engine.session_lifecycle.resolve_model_tier", return_value="ADVANCED"),
            patch("agentfox.engine.session_lifecycle.resolve_security_config", return_value=None),
            patch("agentfox.engine.session_lifecycle.clamp_instances", side_effect=lambda a, i, **kw: i),
            patch("agentfox.engine.session_lifecycle.parse_node_id", return_value=mock_parsed),
        ):
            NodeSessionRunner(
                node_id="test_spec_1_coder_1",
                config=AgentFoxConfig(),
                knowledge_db=MagicMock(),
                assessed_tier=ModelTier.ADVANCED,
            )

            mock_rmv.assert_called_once()
            assert mock_rm.call_args.kwargs.get("variant") == "extended"


# ---------------------------------------------------------------------------
# TS-14-44: NodeSessionRunner source code contains resolve_model_variant call
#           and does not embed inline variant-resolution logic
# Requirement: 14-REQ-12.2
# ---------------------------------------------------------------------------


class TestNodeSessionRunnerSourceInspection:
    """Verify session_lifecycle.py delegates all variant resolution to external functions."""

    def test_source_contains_resolve_model_variant(self) -> None:
        """TS-14-44: The source code calls resolve_model_variant and resolve_model."""
        source_path = (
            Path(__file__).resolve().parents[2] / "agentfox" / "engine" / "session_lifecycle.py"
        )
        source = source_path.read_text(encoding="utf-8")
        assert "resolve_model_variant" in source, (
            "session_lifecycle.py must contain a call to resolve_model_variant"
        )
        assert "resolve_model" in source, (
            "session_lifecycle.py must contain a call to resolve_model"
        )

    def test_no_inline_model_registry_scanning(self) -> None:
        """TS-14-44 corollary: NodeSessionRunner does not scan MODEL_REGISTRY or VARIANT_ORDER inline."""
        source_path = (
            Path(__file__).resolve().parents[2] / "agentfox" / "engine" / "session_lifecycle.py"
        )
        source = source_path.read_text(encoding="utf-8")
        # VARIANT_ORDER should never appear in session_lifecycle.py
        assert "VARIANT_ORDER" not in source, (
            "session_lifecycle.py must not reference VARIANT_ORDER directly"
        )


# ---------------------------------------------------------------------------
# TS-14-E8: When resolve_model_variant returns None, NodeSessionRunner
#           passes variant=None to resolve_model (TIER_DEFAULTS path)
# Requirement: 14-REQ-12.E1
# ---------------------------------------------------------------------------


@_skip_no_rich
class TestNodeSessionRunnerVariantNone:
    """Verify variant=None is passed to resolve_model when resolve_model_variant returns None."""

    def test_variant_none_passed_through(self) -> None:
        """TS-14-E8: resolve_model_variant returns None; resolve_model gets variant=None."""
        from agentfox.engine.session_lifecycle import NodeSessionRunner

        mock_parsed = MagicMock(spec_name="test_spec", group_number=1)

        with (
            patch(
                "agentfox.engine.session_lifecycle.resolve_model_variant",
                create=True,
                return_value=None,
            ),
            patch(
                "agentfox.engine.session_lifecycle.resolve_model",
                return_value="claude-opus-4-6",
            ) as mock_rm,
            patch("agentfox.engine.session_lifecycle.resolve_model_tier", return_value="ADVANCED"),
            patch("agentfox.engine.session_lifecycle.resolve_security_config", return_value=None),
            patch("agentfox.engine.session_lifecycle.clamp_instances", side_effect=lambda a, i, **kw: i),
            patch("agentfox.engine.session_lifecycle.parse_node_id", return_value=mock_parsed),
        ):
            NodeSessionRunner(
                node_id="test_spec_1_coder_1",
                config=AgentFoxConfig(),
                knowledge_db=MagicMock(),
            )

            mock_rm.assert_called_once()
            assert mock_rm.call_args.kwargs.get("variant") is None
