"""CLI entry point for afspec.

Provides the ``afspec`` command with subcommands for spec management.
Currently supports ``update-subtask`` for transitioning subtask states.
"""

from __future__ import annotations

import argparse
import sys

from afspec.exceptions import LifecycleError, LoadError, SaveError
from afspec.io import load_spec, save
from afspec.models import SubtaskState
from afspec.mutate import transition_subtask


def _build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="afspec", description="afspec CLI")
    sub = parser.add_subparsers(dest="command")

    up = sub.add_parser(
        "update-subtask",
        help="Transition a subtask to a new state",
    )
    up.add_argument("spec_dir", help="Path to the spec directory")
    up.add_argument("subtask_id", help="Subtask ID (e.g. 1.1)")
    up.add_argument(
        "target_state",
        choices=[s.value for s in SubtaskState],
        help="Target state",
    )

    return parser


def main(argv: list[str] | None = None) -> int:
    parser = _build_parser()
    args = parser.parse_args(argv)

    if args.command is None:
        parser.print_help()
        return 1

    if args.command == "update-subtask":
        return _cmd_update_subtask(args)

    return 1


def _cmd_update_subtask(args: argparse.Namespace) -> int:
    target = SubtaskState(args.target_state)

    try:
        spec = load_spec(args.spec_dir)
    except LoadError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1

    try:
        spec = spec.model_copy(update={"tasks": transition_subtask(spec.tasks, args.subtask_id, target)})
    except KeyError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1
    except LifecycleError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1

    try:
        save(spec, args.spec_dir)
    except (SaveError, LifecycleError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1

    print(f"{args.subtask_id} -> {args.target_state}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
