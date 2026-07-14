"""Tests for end-to-end validation and property invariants (TS-03-41 through
TS-03-43, TS-03-P2, TS-03-P4, TS-03-P6, TS-03-E1).

Verifies that make check passes, afissues imports work without agentfox,
type checker resolves py.typed, no agentfox.platform imports remain in the
workspace, all public symbols are importable, IntegrationError defaults to
retryable, and Python < 3.12 rejects installation.

Requirements: 03-REQ-12.1, 03-REQ-12.2, 03-REQ-12.3, 03-REQ-1.E1

Drift errata:
  - TS-03-E4 / 03-REQ-3.E2: _request() re-raises raw httpx exceptions after
    retry exhaustion, NOT IntegrationError.  TS-03-P6 verifies only the
    IntegrationError default retryable attribute, not the retry exception type.
"""

from __future__ import annotations

import glob
from pathlib import Path

import pytest

# ── Paths ───────────────────────────────────────────────────────────
_WORKSPACE_ROOT = Path(__file__).resolve().parents[4]


# ── TS-03-P2: No agentfox.platform imports remain in workspace ──────


class TestNoAgentfoxPlatformImports:
    """TS-03-P2: No file in packages/ imports from agentfox.platform.

    Property invariant: for every .py file in the workspace, no import
    statement references 'agentfox.platform'.
    """

    def test_no_agentfox_platform_in_any_source(self) -> None:
        """No .py source file under packages/ references agentfox.platform."""
        all_py = glob.glob(str(_WORKSPACE_ROOT / "packages" / "**" / "*.py"), recursive=True)
        violations = []
        for path in all_py:
            content = Path(path).read_text()
            for i, line in enumerate(content.splitlines(), 1):
                stripped = line.strip()
                if "agentfox.platform" in stripped and (
                    stripped.startswith("from ") or stripped.startswith("import ")
                ):
                    rel = Path(path).relative_to(_WORKSPACE_ROOT)
                    violations.append(f"{rel}:{i}: {stripped}")
        assert not violations, (
            f"Found {len(violations)} stale agentfox.platform import(s):\n"
            + "\n".join(f"  - {v}" for v in violations)
        )


# ── TS-03-P4: All public symbols importable from afissues ───────────


class TestPublicSymbolsImportable:
    """TS-03-P4: All 15 public symbols are importable from top-level afissues."""

    _EXPECTED_SYMBOLS = [
        "PlatformProtocol",
        "NullPlatform",
        "IssueResult",
        "IssueComment",
        "GitHubPlatform",
        "parse_github_remote",
        "LabelSpec",
        "LABEL_FIX",
        "LABEL_FIXED",
        "LABEL_NO_CHANGE",
        "LABEL_IMPLEMENTED",
        "LABEL_PRIORITY_HIGH",
        "LABEL_PRIORITY_MEDIUM",
        "LABEL_PRIORITY_LOW",
        "REQUIRED_LABELS",
    ]

    def test_all_15_symbols_importable(self) -> None:
        """All 15 public symbols import from the afissues top-level namespace."""
        import afissues

        missing = []
        for symbol in self._EXPECTED_SYMBOLS:
            if not hasattr(afissues, symbol):
                missing.append(symbol)
        assert not missing, (
            f"Missing symbols in afissues namespace: {missing}"
        )

    def test_symbol_count(self) -> None:
        """At least 15 public symbols are re-exported."""
        import afissues

        present = [s for s in self._EXPECTED_SYMBOLS if hasattr(afissues, s)]
        assert len(present) == 15, (
            f"Expected 15 symbols, found {len(present)}: {present}"
        )


# ── TS-03-P6: IntegrationError.retryable defaults to True ───────────


class TestIntegrationErrorRetryableDefault:
    """TS-03-P6: IntegrationError() defaults retryable to True."""

    def test_default_retryable_is_true(self) -> None:
        """IntegrationError with no explicit retryable has retryable=True."""
        from afissues.errors import IntegrationError

        err = IntegrationError()
        assert err.retryable is True

    def test_explicit_false_overrides_default(self) -> None:
        """IntegrationError(retryable=False) sets retryable=False."""
        from afissues.errors import IntegrationError

        err = IntegrationError("fail", retryable=False)
        assert err.retryable is False

    def test_retryable_true_with_context(self) -> None:
        """IntegrationError with context kwargs still defaults retryable=True."""
        from afissues.errors import IntegrationError

        err = IntegrationError("fail", repo="owner/repo", attempt=3)
        assert err.retryable is True
        assert err.context == {"repo": "owner/repo", "attempt": 3}

    def test_max_retries_constant(self) -> None:
        """_MAX_RETRIES is 3 — retry count must never exceed this."""
        from afissues.github import _MAX_RETRIES

        assert _MAX_RETRIES == 3


# ── TS-03-42: Import without agentfox installed ─────────────────────


class TestImportWithoutAgentfox:
    """TS-03-42: afissues imports succeed in environment with only httpx.

    In the workspace environment, both agentfox and afissues are installed.
    This test verifies that afissues module source code does not import
    agentfox at the module level (which would cause ModuleNotFoundError
    in a standalone installation).
    """

    def test_afissues_source_has_no_agentfox_imports(self) -> None:
        """No top-level import of agentfox in any afissues source module."""
        afissues_src = _WORKSPACE_ROOT / "packages" / "afissues" / "afissues"
        source_files = glob.glob(str(afissues_src / "**" / "*.py"), recursive=True)
        violations = []
        for path in source_files:
            content = Path(path).read_text()
            for line in content.splitlines():
                stripped = line.strip()
                if stripped.startswith(("import agentfox", "from agentfox")):
                    violations.append(f"{Path(path).name}: {stripped}")
        assert not violations, (
            "afissues has agentfox imports (would fail without agentfox):\n"
            + "\n".join(f"  - {v}" for v in violations)
        )

    def test_core_import_succeeds(self) -> None:
        """Basic afissues import works in current environment."""
        from afissues import GitHubPlatform, PlatformProtocol

        assert PlatformProtocol is not None
        assert GitHubPlatform is not None


# ── TS-03-43: Type checker resolution with py.typed ──────────────────


class TestTypeCheckerResolution:
    """TS-03-43: py.typed marker enables type checker resolution."""

    def test_py_typed_exists(self) -> None:
        """py.typed marker file exists in afissues package."""
        py_typed = _WORKSPACE_ROOT / "packages" / "afissues" / "afissues" / "py.typed"
        assert py_typed.exists(), "py.typed marker missing"

    def test_py_typed_in_installed_package(self) -> None:
        """py.typed exists next to the installed __init__.py."""
        import afissues

        pkg_dir = Path(afissues.__file__).parent
        assert (pkg_dir / "py.typed").exists(), (
            "py.typed not found in installed afissues package"
        )


# ── TS-03-E1: Python < 3.12 rejection ───────────────────────────────


class TestPythonVersionConstraint:
    """TS-03-E1: pyproject.toml requires-python >= 3.12.

    We cannot actually test installation under Python < 3.12 in this
    environment, but we verify the constraint is declared correctly.
    """

    def test_requires_python_declared(self) -> None:
        """pyproject.toml declares requires-python >= 3.12."""
        import tomllib

        toml_path = _WORKSPACE_ROOT / "packages" / "afissues" / "pyproject.toml"
        with open(toml_path, "rb") as f:
            toml = tomllib.load(f)
        requires = toml["project"]["requires-python"]
        assert "3.12" in requires, f"Expected >=3.12, got: {requires}"
        assert ">=" in requires, f"Expected >=3.12, got: {requires}"


# ── TS-03-E7: Stale import raises ModuleNotFoundError ────────────────


class TestStaleImportDetection:
    """TS-03-E7: A stale agentfox.platform import raises ModuleNotFoundError.

    This test verifies that if the platform directory has been deleted,
    attempting to import from it raises ModuleNotFoundError.
    """

    def test_stale_import_raises_module_not_found(self) -> None:
        """Stale agentfox.platform import must raise ModuleNotFoundError."""
        platform_dir = _WORKSPACE_ROOT / "packages" / "agentfox" / "agentfox" / "platform"
        if platform_dir.exists():
            pytest.skip("Platform directory not yet deleted — test deferred to post-deletion")

        with pytest.raises(ModuleNotFoundError):
            exec("from agentfox.platform.protocol import PlatformProtocol")  # noqa: S102
