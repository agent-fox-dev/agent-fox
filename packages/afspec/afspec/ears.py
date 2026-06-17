"""EARS sentence rendering from criterion fields.

Renders EARS (Easy Approach to Requirements Syntax) sentences from
decomposed criterion fields using the six templates defined in
docs/spec-format.md section 6.2.1.
"""

from __future__ import annotations

from afspec.models import Criterion, EARSPattern


def render_ears_sentence(c: Criterion) -> str:
    """Render an EARS sentence from a criterion's fields.

    Uses one of six templates based on the criterion's ears_pattern:
      - ubiquitous:    THE {system} SHALL {action}
      - event_driven:  WHEN {trigger}, THE {system} SHALL {action}
      - complex_event: WHEN {trigger} AND {condition}, THE {system} SHALL {action}
      - state_driven:  WHILE {state}, THE {system} SHALL {action}
      - unwanted:      IF {error_condition}, THEN THE {system} SHALL {action}
      - optional:      WHERE {feature}, THE {system} SHALL {action}

    When return_contract is non-null, appends: " AND return {return_contract}".
    """
    pattern = c.ears_pattern

    if pattern == EARSPattern.UBIQUITOUS:
        sentence = f"THE {c.system} SHALL {c.action}"
    elif pattern == EARSPattern.EVENT_DRIVEN:
        sentence = f"WHEN {c.trigger}, THE {c.system} SHALL {c.action}"
    elif pattern == EARSPattern.COMPLEX_EVENT:
        sentence = f"WHEN {c.trigger} AND {c.condition}, THE {c.system} SHALL {c.action}"
    elif pattern == EARSPattern.STATE_DRIVEN:
        sentence = f"WHILE {c.state}, THE {c.system} SHALL {c.action}"
    elif pattern == EARSPattern.UNWANTED:
        sentence = f"IF {c.error_condition}, THEN THE {c.system} SHALL {c.action}"
    elif pattern == EARSPattern.OPTIONAL:
        sentence = f"WHERE {c.feature}, THE {c.system} SHALL {c.action}"
    else:
        msg = f"Unknown EARS pattern: {pattern}"
        raise ValueError(msg)

    if c.return_contract is not None:
        sentence += f" AND return {c.return_contract}"

    return sentence
