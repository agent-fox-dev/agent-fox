"""Exit-codes decorator, JSON help renderer, and help scaffolding.

Requirements: 03-REQ-10, 04-REQ-5
"""

from __future__ import annotations

from typing import Any

import click


def exit_codes(**mapping: Any):  # noqa: ANN201
    """Decorator that stores exit-code metadata on a Click Command."""

    def decorator(cmd: Any) -> Any:
        if not isinstance(cmd, click.Command):
            raise TypeError(
                "@exit_codes must be applied above @click.command; received a plain function, not a Click Command"
            )
        cmd.exit_codes = mapping  # type: ignore[attr-defined]
        return cmd

    return decorator


def render_json_help(cmd: click.Command) -> dict[str, Any]:
    """Build a JSON-serializable dict describing a Click command.

    Returns a dict with:
    - ``name`` (str): the command name
    - ``description`` (str): the command docstring/help text
    - ``options`` (list): option descriptors with name, help, required,
      default, and type fields
    - ``exit_codes`` (list): exit code descriptors with integer ``code``
      and string ``description``; empty list when the ``@exit_codes``
      decorator was not applied

    Args:
        cmd: A Click Command instance to describe.

    Returns:
        JSON-serializable dict.

    Requirements: 04-REQ-5.1, 04-REQ-5.2, 04-REQ-5.E1
    """
    options: list[dict[str, Any]] = []
    for param in cmd.params:
        if isinstance(param, click.Option):
            opt: dict[str, Any] = {
                "name": param.name or "",
                "help": param.help or "",
                "required": param.required,
                "default": param.default,
                "type": param.type.name,
            }
            options.append(opt)

    # Derive exit code metadata from @exit_codes decorator (04-REQ-5.2).
    # When the decorator was not applied, exit_codes_list is empty (04-REQ-5.E1).
    raw_codes: dict[str, str] = getattr(cmd, "exit_codes", None) or {}
    exit_codes_list: list[dict[str, Any]] = [
        {"code": int(code_str), "description": desc} for code_str, desc in raw_codes.items()
    ]

    return {
        "name": cmd.name or "",
        "description": (cmd.help or "").strip(),
        "options": options,
        "exit_codes": exit_codes_list,
    }
