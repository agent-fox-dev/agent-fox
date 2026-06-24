"""Tests for documentation updates after nightshift extraction.

Test Spec: TS-07-25, TS-07-26, TS-07-27, TS-07-28, TS-07-29, TS-07-30,
           TS-07-31, TS-07-E7, TS-07-P5
Requirements: 07-REQ-5.1, 07-REQ-5.2, 07-REQ-5.3, 07-REQ-5.4, 07-REQ-5.5,
              07-REQ-5.E1, 07-REQ-6.1, 07-REQ-6.2
"""

from __future__ import annotations

import os
import re
import subprocess

import pytest


class TestGrepAfNightShift:
    """TS-07-25 / TS-07-P5: No 'af night-shift' in docs or README.

    Requirements: 07-REQ-5.1, 07-REQ-5.E1
    """

    def test_no_af_night_shift_in_docs(self) -> None:
        """grep -r 'af night-shift' docs/ returns no matches."""
        result = subprocess.run(
            ["grep", "-r", "af night-shift", "docs/"],
            capture_output=True,
            text=True,
        )
        assert result.stdout == "", f"Found stale 'af night-shift' in docs/:\n{result.stdout}"

    def test_no_af_night_shift_in_readme(self) -> None:
        """grep 'af night-shift' README.md returns no matches."""
        if not os.path.exists("README.md"):
            pytest.skip("README.md not found")
        result = subprocess.run(
            ["grep", "af night-shift", "README.md"],
            capture_output=True,
            text=True,
        )
        assert result.stdout == "", f"Found stale 'af night-shift' in README.md:\n{result.stdout}"

    def test_no_agent_fox_night_shift_in_docs(self) -> None:
        """grep -r 'agent-fox night-shift' docs/ returns no matches."""
        result = subprocess.run(
            ["grep", "-r", "agent-fox night-shift", "docs/"],
            capture_output=True,
            text=True,
        )
        assert result.stdout == "", f"Found stale 'agent-fox night-shift' in docs/:\n{result.stdout}"


class TestGrepAcceptanceMechanism:
    """TS-07-E7: Grep acceptance check mechanism validation.

    Requirements: 07-REQ-5.E1

    Verifies that the grep-based acceptance check WOULD catch stale
    'af night-shift' references by creating a temporary file containing
    the string and confirming grep finds it.
    """

    def test_grep_catches_stale_reference_in_temp_file(self, tmp_path: object) -> None:
        """Create a temp file with 'af night-shift' under docs/ and verify grep catches it."""
        # Create a temporary file in docs/ containing the stale string
        stale_file = os.path.join("docs", "_test_stale_ref_temp.md")
        try:
            with open(stale_file, "w") as f:
                f.write("Use af night-shift to run the daemon\n")

            result = subprocess.run(
                ["grep", "-r", "af night-shift", "docs/"],
                capture_output=True, text=True,
            )
            # grep should find the stale reference (exit code 0 = match found)
            assert result.returncode == 0, (
                "grep should detect stale 'af night-shift' in the temp file"
            )
            assert "af night-shift" in result.stdout, (
                "grep output should contain the stale reference"
            )
        finally:
            # Always clean up the temporary file
            if os.path.exists(stale_file):
                os.remove(stale_file)


class TestReadmeContent:
    """TS-07-26: README.md references standalone night-shift.

    Requirements: 07-REQ-5.2
    """

    def test_readme_contains_night_shift(self) -> None:
        if not os.path.exists("README.md"):
            pytest.skip("README.md not found")
        content = open("README.md").read()
        assert "night-shift" in content, "README.md should reference the standalone night-shift CLI"

    def test_readme_contains_nightshift_package(self) -> None:
        if not os.path.exists("README.md"):
            pytest.skip("README.md not found")
        content = open("README.md").read()
        assert "nightshift" in content, "README.md should reference the nightshift package"


class TestCliReferenceDoc:
    """TS-07-27: cli-reference.md documents standalone night-shift.

    Requirements: 07-REQ-5.3
    """

    def test_no_af_night_shift_in_cli_ref(self) -> None:
        path = "docs/cli-reference.md"
        if not os.path.exists(path):
            pytest.skip(f"{path} not found")
        content = open(path).read()
        assert "af night-shift" not in content, "docs/cli-reference.md still contains 'af night-shift'"

    def test_night_shift_in_cli_ref(self) -> None:
        path = "docs/cli-reference.md"
        if not os.path.exists(path):
            pytest.skip(f"{path} not found")
        content = open(path).read()
        assert "night-shift" in content, "docs/cli-reference.md should document night-shift"


class TestConfigReferenceDoc:
    """TS-07-28: config-reference.md has no stale references.

    Requirements: 07-REQ-5.4
    """

    def test_no_af_night_shift_in_config_ref(self) -> None:
        path = "docs/config-reference.md"
        if not os.path.exists(path):
            pytest.skip(f"{path} not found")
        content = open(path).read()
        assert "af night-shift" not in content
        assert "agent-fox night-shift" not in content


class TestArchitectureDocs:
    """TS-07-29: Architecture docs have no stale references.

    Requirements: 07-REQ-5.5
    """

    @pytest.mark.parametrize(
        "doc_path",
        [
            "docs/architecture/04-night-shift.md",
            "docs/architecture/README.md",
            "docs/architecture/03-execution-and-archetypes.md",
            "docs/profiles.md",
            "docs/architecture/prd.md",
            "docs/README.md",
        ],
    )
    def test_no_af_night_shift_in_arch_doc(self, doc_path: str) -> None:
        if not os.path.exists(doc_path):
            pytest.skip(f"{doc_path} not found")
        content = open(doc_path).read()
        assert "af night-shift" not in content, f"{doc_path} still contains 'af night-shift'"


class TestDependencyDiagramTopology:
    """TS-07-30 / TS-07-31: Dependency diagram updated with nightshift.

    Requirements: 07-REQ-6.1, 07-REQ-6.2
    """

    def test_readme_diagram_contains_nightshift(self) -> None:
        if not os.path.exists("README.md"):
            pytest.skip("README.md not found")
        content = open("README.md").read()
        assert "nightshift" in content
        assert "agentfox" in content

    def test_readme_diagram_contains_all_packages(self) -> None:
        """TS-07-31: Diagram contains all nodes in the updated topology."""
        if not os.path.exists("README.md"):
            pytest.skip("README.md not found")
        content = open("README.md").read()
        for pkg in ["af", "nightshift", "agentfox", "agentspec", "afspec"]:
            assert pkg in content, f"README.md should mention {pkg} in diagram"

    def test_no_nightshift_to_agentspec_arrow(self) -> None:
        """TS-07-30: No arrow from nightshift to agentspec in diagram."""
        if not os.path.exists("README.md"):
            pytest.skip("README.md not found")
        content = open("README.md").read()
        # In the diagram section, nightshift must not have an arrow to agentspec
        assert not re.search(r"nightshift.*agentspec", content), (
            "Diagram must NOT show nightshift depending on agentspec"
        )

    def test_no_nightshift_to_afspec_arrow(self) -> None:
        """TS-07-30: No arrow from nightshift to afspec in diagram."""
        if not os.path.exists("README.md"):
            pytest.skip("README.md not found")
        content = open("README.md").read()
        # In the diagram section, nightshift must not have an arrow to afspec
        assert not re.search(r"nightshift.*afspec", content), (
            "Diagram must NOT show nightshift depending on afspec"
        )


class TestDocFileWalk:
    """TS-07-P5 extended: Walk all doc files for stale references.

    Requirements: 07-REQ-5.E1
    """

    def test_no_af_night_shift_in_any_doc(self) -> None:
        """Walk docs/ and check every file for stale 'af night-shift' references."""
        if not os.path.exists("docs"):
            pytest.skip("docs/ directory not found")
        for root, _dirs, filenames in os.walk("docs"):
            # Skip errata directory (historical records)
            if "errata" in root:
                continue
            for fname in filenames:
                if not fname.endswith(".md"):
                    continue
                fpath = os.path.join(root, fname)
                content = open(fpath).read()
                assert "af night-shift" not in content, f"Stale 'af night-shift' found in {fpath}"


class TestGrepAcceptanceCheck:
    """Additional grep-based acceptance check.

    Requirements: 07-REQ-5.1
    """

    def test_grep_returns_no_matches(self) -> None:
        """grep -r 'af night-shift' docs/ README.md has empty output."""
        cmd = ["grep", "-r", "af night-shift", "docs/"]
        if os.path.exists("README.md"):
            cmd.append("README.md")
        result = subprocess.run(cmd, capture_output=True, text=True)
        assert result.stdout.strip() == "", f"Stale references found:\n{result.stdout}"
