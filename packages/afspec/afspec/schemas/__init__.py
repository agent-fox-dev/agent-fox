"""Bundled JSON Schema files for spec validation."""

from __future__ import annotations

import importlib.resources

_SCHEMA_FILES = {
    "prd-frontmatter.v1.json": "prd-frontmatter.v1.json",
    "requirements.v1.json": "requirements.v1.json",
    "test_spec.v1.json": "test_spec.v1.json",
    "tasks.v1.json": "tasks.v1.json",
}


def schemas() -> dict[str, bytes]:
    """Return a dict of schema name -> schema bytes.

    Loads bundled JSON Schema files from the ``afspec.schemas`` package
    data directory using ``importlib.resources``. No network access is
    required.
    """
    result: dict[str, bytes] = {}
    for name, filename in _SCHEMA_FILES.items():
        ref = importlib.resources.files("afspec.schemas").joinpath(filename)
        result[name] = ref.read_bytes()
    return result
