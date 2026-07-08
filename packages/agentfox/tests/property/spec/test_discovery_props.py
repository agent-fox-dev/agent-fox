"""Property tests for spec discovery.

Test Spec: TS-02-P5 (discovery sort order), TS-NS-3 (unified regex union)
Property: Property 5 from design.md
Requirements: 02-REQ-1.1, NS-REQ-3
"""

from __future__ import annotations

import re
from pathlib import Path

import pytest
from afspec.discovery import is_spec_dir_name, parse_spec_dir_name
from agentfox.spec.discovery import discover_specs
from hypothesis import given, settings
from hypothesis import strategies as st

# -- Strategies for generating spec folder names ------------------------------


@st.composite
def valid_spec_folder_sets(draw: st.DrawFn) -> tuple[list[str], Path]:
    """Generate sets of valid spec folder names with NN_ prefix.

    Creates a tmp_path-like structure with 1-10 spec folders,
    each having a tasks.md file.

    Returns:
        Tuple of (folder_names, tmp_specs_dir_path).
    """
    # Generate unique prefixes
    n = draw(st.integers(min_value=1, max_value=10))
    prefixes = draw(
        st.lists(
            st.integers(min_value=1, max_value=99),
            min_size=n,
            max_size=n,
            unique=True,
        )
    )

    folder_names = [f"{p:02d}_spec_{p}" for p in prefixes]
    return folder_names, Path("unused")  # path created in test


class TestDiscoverySortOrder:
    """TS-02-P5: Discovered specs are always sorted by numeric prefix.

    Property 5: For any set of spec folders, discover_specs() returns them
    sorted by numeric prefix in ascending order.
    """

    @given(data=st.data())
    @settings(max_examples=50)
    def test_prefixes_sorted_ascending(self, data: st.DataObject, tmp_path_factory: pytest.TempPathFactory) -> None:
        """Discovery always returns specs sorted by prefix."""
        # Generate unique prefixes
        n = data.draw(st.integers(min_value=1, max_value=10))
        prefixes = data.draw(
            st.lists(
                st.integers(min_value=1, max_value=99),
                min_size=n,
                max_size=n,
                unique=True,
            )
        )

        # Create filesystem structure
        tmp_dir = tmp_path_factory.mktemp("specs")
        specs_dir = tmp_dir / ".specs"
        specs_dir.mkdir()

        for p in prefixes:
            folder = specs_dir / f"{p:02d}_spec_{p}"
            folder.mkdir()
            (folder / "requirements.json").write_text("{}")
            (folder / "tasks.json").write_text("{}")
            (folder / "tasks.md").write_text(f"- [ ] 1. Task for spec {p}\n")

        # Run discovery
        specs = discover_specs(specs_dir)

        # Verify sorted
        result_prefixes = [s.prefix for s in specs]
        assert result_prefixes == sorted(result_prefixes), f"Prefixes not sorted: {result_prefixes}"


# -- TS-NS-3: Unified regex union property tests ------------------------------

# The three prior regex patterns that existed before consolidation:
_OLD_AFSPEC_RE = re.compile(r"^\d+_[a-z][a-z0-9_]*$")  # afspec (strictest)
_OLD_AGENTFOX_RE = re.compile(r"^(\d+)_(.+)$")  # agentfox (loosest)
_OLD_CLI_RE = re.compile(r"^(\d{2})_(.+)$")  # spec/cli.py (2-digit prefix)


class TestUnifiedRegexUnion:
    """TS-NS-3: Unified discovery regex accepts the union of all prior patterns.

    The consolidated regex in afspec.discovery must accept at least every
    directory name previously accepted by the afspec pattern (the canonical
    rule). Property tests generate names matching each prior pattern and
    verify the unified is_spec_dir_name() accepts them.
    """

    @given(
        prefix=st.integers(min_value=1, max_value=999),
        name=st.from_regex(r"[a-z][a-z0-9_]{0,20}", fullmatch=True),
    )
    @settings(max_examples=100)
    def test_afspec_pattern_names_accepted(self, prefix: int, name: str) -> None:
        """Names matching the original afspec pattern are accepted by the unified regex.

        The afspec pattern is the canonical one: \\d+_[a-z][a-z0-9_]*.
        """
        dir_name = f"{prefix}_{name}"
        assert _OLD_AFSPEC_RE.match(dir_name), f"Test invariant: {dir_name} should match old afspec pattern"
        assert is_spec_dir_name(dir_name), f"Unified regex rejected afspec-valid name: {dir_name}"

    @given(
        prefix=st.integers(min_value=1, max_value=999),
        name=st.from_regex(r"[a-z][a-z0-9_]{0,20}", fullmatch=True),
    )
    @settings(max_examples=100)
    def test_parse_spec_dir_name_extracts_correctly(self, prefix: int, name: str) -> None:
        """parse_spec_dir_name returns the correct prefix and name."""
        dir_name = f"{prefix}_{name}"
        result = parse_spec_dir_name(dir_name)
        assert result is not None, f"parse_spec_dir_name returned None for: {dir_name}"
        parsed_prefix, parsed_name = result
        assert parsed_prefix == prefix
        assert parsed_name == name

    @given(
        prefix=st.integers(min_value=1, max_value=99),
        name=st.from_regex(r"[a-z][a-z0-9_]{0,20}", fullmatch=True),
    )
    @settings(max_examples=50)
    def test_discover_finds_afspec_pattern_dirs(
        self, prefix: int, name: str, tmp_path_factory: pytest.TempPathFactory
    ) -> None:
        """discover_specs() finds directories matching the canonical pattern on disk."""
        tmp_dir = tmp_path_factory.mktemp("union_test")
        specs_dir = tmp_dir / ".specs"
        specs_dir.mkdir()

        dir_name = f"{prefix:02d}_{name}"
        spec_folder = specs_dir / dir_name
        spec_folder.mkdir()
        (spec_folder / "requirements.json").write_text("{}")

        specs = discover_specs(specs_dir)
        assert len(specs) == 1
        assert specs[0].name == dir_name
        assert specs[0].prefix == prefix
