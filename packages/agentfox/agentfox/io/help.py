"""Exit-codes decorator and help scaffolding.

Requirements: 03-REQ-10
"""

from __future__ import annotations

from typing import Any

import click


def exit_codes(**mapping: Any):  # noqa: ANN201
    """Decorator that stores exit-code metadata on a Click Command."""

    def decorator(cmd: Any) -> Any:
        if not isinstance(cmd, click.Command):
            raise TypeError(
                "@exit_codes must be applied above @click.command; "
                "received a plain function, not a Click Command"
            )
        cmd.exit_codes = mapping  # type: ignore[attr-defined]
        return cmd

    return decorator
