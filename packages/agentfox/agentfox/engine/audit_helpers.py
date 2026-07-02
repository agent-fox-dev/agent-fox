"""Session cost calculation helper.

``emit_audit_event`` has been migrated to ``afaudit.emit``.
This module retains only ``calculate_session_cost`` because it depends
on agentfox-internal pricing models.
"""

from __future__ import annotations

import logging
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from agentfox.core.config import AgentFoxConfig

logger = logging.getLogger(__name__)


def calculate_session_cost(
    config: AgentFoxConfig,
    model_id: str,
    input_tokens: int,
    output_tokens: int,
    cache_read_input_tokens: int = 0,
    cache_creation_input_tokens: int = 0,
) -> float:
    """Calculate session cost from token counts and pricing config."""
    from agentfox.core.config import PricingConfig
    from agentfox.core.models import calculate_cost

    pricing = getattr(config, "pricing", PricingConfig())
    return calculate_cost(
        input_tokens,
        output_tokens,
        model_id,
        pricing,
        cache_read_input_tokens=cache_read_input_tokens,
        cache_creation_input_tokens=cache_creation_input_tokens,
    )
