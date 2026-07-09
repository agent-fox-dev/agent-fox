"""Tests for campaign directory management.

Test Spec Entries: TS-02-1 through TS-02-9 (acceptance criteria),
TS-02-E1 through TS-02-E6 (edge cases),
TS-02-P3, TS-02-P4 (property tests),
TS-02-SMOKE-1, TS-02-SMOKE-2 (integration smoke tests).
"""

from __future__ import annotations

import uuid
from datetime import datetime
from pathlib import Path

import pytest
import yaml
from agentspec.campaign import Campaign
from agentspec.errors import CampaignError
from agentspec.session import SessionState
from hypothesis import HealthCheck, given, settings
from hypothesis import strategies as st


class TestCampaignCreation:
    """Tests for Campaign.create() — TS-02-1 through TS-02-3."""

    def test_ts02_1_campaign_create(self, tmp_path: Path) -> None:
        """TS-02-1: Campaign.create creates dir, writes YAML, returns Campaign.

        Requirement: 02-REQ-1.1
        """
        target = tmp_path / "my_campaign"
        camp = Campaign.create(target, "Test Campaign", "A test")

        assert target.is_dir()
        assert (target / "campaign.yaml").exists()
        assert camp.metadata.name == "Test Campaign"
        assert camp.metadata.description == "A test"
        assert camp.path == target

    def test_ts02_2_campaign_create_fails_existing(self, tmp_path: Path) -> None:
        """TS-02-2: Creating campaign where campaign.yaml exists raises CampaignError.

        Requirement: 02-REQ-1.2
        """
        target = tmp_path / "existing"
        Campaign.create(target, "First", "Original")

        with pytest.raises(CampaignError, match="already exists"):
            Campaign.create(target, "Second", "Duplicate")

    def test_ts02_3_campaign_yaml_fields(self, tmp_path: Path) -> None:
        """TS-02-3: campaign.yaml contains name, description, created_at, updated_at.

        Requirement: 02-REQ-1.3
        """
        target = tmp_path / "field_test"
        Campaign.create(target, "Test", "Desc")

        data = yaml.safe_load((target / "campaign.yaml").read_text())
        assert data["name"] == "Test"
        assert data["description"] == "Desc"
        assert "created_at" in data
        assert "updated_at" in data
        # Verify ISO 8601 format — fromisoformat should not raise
        datetime.fromisoformat(data["created_at"])
        datetime.fromisoformat(data["updated_at"])


class TestCampaignOpen:
    """Tests for Campaign.open() and campaign.specs() — TS-02-4, TS-02-5."""

    def test_ts02_4_campaign_open(self, tmp_path: Path) -> None:
        """TS-02-4: Campaign.open reads campaign.yaml, returns Campaign.

        Requirement: 02-REQ-2.1
        """
        target = tmp_path / "open_test"
        Campaign.create(target, "My Camp", "Testing")

        camp = Campaign.open(target)
        assert camp.metadata.name == "My Camp"
        assert camp.metadata.description == "Testing"
        assert camp.path == target

    def test_ts02_5_campaign_specs_sorted(self, tmp_path: Path) -> None:
        """TS-02-5: specs() returns sorted spec subdirs, excludes archive.

        Requirement: 02-REQ-2.2
        """
        target = tmp_path / "specs_test"
        camp = Campaign.create(target, "Test", "Desc")
        camp.new_spec("alpha", "PRD content")
        camp.new_spec("beta", "PRD content 2")
        (target / "archive").mkdir()

        specs = camp.specs()
        assert len(specs) == 2
        assert specs[0].name == "01_alpha"
        assert specs[1].name == "02_beta"


class TestNewSpec:
    """Tests for campaign.new_spec() — TS-02-6 through TS-02-9."""

    def test_ts02_6_new_spec_string_prd(self, tmp_path: Path) -> None:
        """TS-02-6: new_spec with string PRD creates dir and session.

        Requirement: 02-REQ-3.1
        """
        target = tmp_path / "spec_test"
        camp = Campaign.create(target, "Test", "Desc")
        session = camp.new_spec("my_spec", "# My PRD\n\nContent here")

        spec_dir = target / "01_my_spec"
        assert spec_dir.is_dir()
        assert (spec_dir / "prd.md").exists()
        assert "Content here" in (spec_dir / "prd.md").read_text()
        assert (spec_dir / "_session.json").exists()
        assert session.state == SessionState.INIT

    def test_ts02_7_new_spec_path_prd(self, tmp_path: Path) -> None:
        """TS-02-7: new_spec with Path PRD copies file content into prd.md.

        Requirement: 02-REQ-3.2
        """
        prd_file = tmp_path / "source_prd.md"
        prd_file.write_text("# Source PRD\n\nOriginal content")

        target = tmp_path / "path_prd_test"
        camp = Campaign.create(target, "Test", "Desc")
        camp.new_spec("from_file", prd_file)

        prd_text = (target / "01_from_file" / "prd.md").read_text()
        assert "Original content" in prd_text

    def test_ts02_8_spec_dir_sequential_prefixes(self, tmp_path: Path) -> None:
        """TS-02-8: Spec directories use sequential numeric prefixes starting from 01.

        Requirement: 02-REQ-3.3
        """
        target = tmp_path / "seq_test"
        camp = Campaign.create(target, "Test", "Desc")
        camp.new_spec("first", "PRD 1")
        camp.new_spec("second", "PRD 2")
        camp.new_spec("third", "PRD 3")

        specs = camp.specs()
        assert specs[0].name == "01_first"
        assert specs[1].name == "02_second"
        assert specs[2].name == "03_third"

    def test_ts02_9_prd_frontmatter(self, tmp_path: Path) -> None:
        """TS-02-9: Generated prd.md has required YAML frontmatter fields.

        Requirement: 02-REQ-3.4
        """
        target = tmp_path / "frontmatter_test"
        camp = Campaign.create(target, "Test", "Desc")
        camp.new_spec("my_spec", "# My Spec PRD")

        prd_text = (target / "01_my_spec" / "prd.md").read_text()

        # Parse YAML frontmatter (between --- delimiters)
        assert prd_text.startswith("---"), "prd.md must start with YAML frontmatter"
        parts = prd_text.split("---", 2)
        assert len(parts) >= 3, "prd.md must have --- delimited frontmatter"
        frontmatter = yaml.safe_load(parts[1])

        assert frontmatter["spec_id"] == "01"
        assert frontmatter["spec_name"] == "my_spec"
        assert frontmatter["status"] == "draft"
        assert frontmatter["schema_version"] == 1
        assert "title" in frontmatter
        assert "created_at" in frontmatter
        assert "updated_at" in frontmatter
        assert "owner" in frontmatter
        assert "source" in frontmatter


class TestCampaignEdgeCases:
    """Edge case tests for campaign operations — TS-02-E1 through TS-02-E6."""

    def test_ts02_e1_create_non_empty_non_campaign(self, tmp_path: Path) -> None:
        """TS-02-E1: CampaignError when directory is non-empty but has no campaign.yaml.

        Requirement: 02-REQ-1.E1
        """
        target = tmp_path / "non_empty"
        target.mkdir()
        (target / "random_file.txt").write_text("stuff")

        with pytest.raises(CampaignError):
            Campaign.create(target, "Test", "Desc")

    def test_ts02_e2_create_parent_missing(self, tmp_path: Path) -> None:
        """TS-02-E2: CampaignError when parent directory does not exist.

        Requirement: 02-REQ-1.E2
        """
        with pytest.raises(CampaignError):
            Campaign.create(tmp_path / "nonexistent" / "child", "Test", "Desc")

    def test_ts02_e3_open_no_campaign_yaml(self, tmp_path: Path) -> None:
        """TS-02-E3: CampaignError when opening a directory without campaign.yaml.

        Requirement: 02-REQ-2.E1
        """
        target = tmp_path / "not_campaign"
        target.mkdir()

        with pytest.raises(CampaignError):
            Campaign.open(target)

    def test_ts02_e4_open_invalid_yaml(self, tmp_path: Path) -> None:
        """TS-02-E4: CampaignError when campaign.yaml contains invalid YAML.

        Requirement: 02-REQ-2.E2
        """
        target = tmp_path / "bad_yaml"
        target.mkdir()
        (target / "campaign.yaml").write_text(":::invalid yaml{{{")

        with pytest.raises(CampaignError):
            Campaign.open(target)

    def test_ts02_e5_new_spec_invalid_name(self, tmp_path: Path) -> None:
        """TS-02-E5: CampaignError for spec names with invalid characters.

        Requirement: 02-REQ-3.E1
        """
        target = tmp_path / "invalid_name_test"
        camp = Campaign.create(target, "Test", "Desc")

        with pytest.raises(CampaignError):
            camp.new_spec("Invalid-Name!", "PRD")

        with pytest.raises(CampaignError):
            camp.new_spec("123numeric", "PRD")

        with pytest.raises(CampaignError):
            camp.new_spec("has spaces", "PRD")

    def test_ts02_e6_new_spec_nonexistent_prd_path(self, tmp_path: Path) -> None:
        """TS-02-E6: CampaignError when PRD is a Path that does not exist.

        Requirement: 02-REQ-3.E2
        """
        target = tmp_path / "prd_path_test"
        camp = Campaign.create(target, "Test", "Desc")

        with pytest.raises(CampaignError):
            camp.new_spec("test", Path("/nonexistent/prd.md"))


# ---------------------------------------------------------------------------
# Property tests: TS-02-P3, TS-02-P4
# ---------------------------------------------------------------------------


class TestCampaignProperties:
    """Property tests for campaign operations — TS-02-P3, TS-02-P4."""

    @given(n=st.integers(min_value=1, max_value=20))
    @settings(
        max_examples=10,
        suppress_health_check=[HealthCheck.function_scoped_fixture],
    )
    def test_ts02_p3_property_numbering_monotonic(self, n: int, tmp_path: Path) -> None:
        """TS-02-P3: Spec directory numbering is monotonically increasing.

        Property 3: For any number of specs created sequentially, prefixes
        are 01, 02, ..., n with no gaps.

        Validates: 02-REQ-3.3
        """
        camp_dir = tmp_path / f"camp_{n}"
        camp = Campaign.create(camp_dir, "Test", "Desc")

        for i in range(n):
            # Generate unique spec names using letters a-z (up to 20)
            camp.new_spec(f"spec_{chr(ord('a') + i)}", f"PRD {i}")

        specs = camp.specs()
        assert len(specs) == n

        for i, spec_path in enumerate(specs):
            prefix = int(spec_path.name.split("_")[0])
            assert prefix == i + 1

    @given(
        name=st.text(
            alphabet=st.characters(whitelist_categories=("L", "N", "Z")),
            min_size=1,
            max_size=50,
        ),
        desc=st.text(min_size=0, max_size=200),
    )
    @settings(
        max_examples=10,
        suppress_health_check=[HealthCheck.function_scoped_fixture],
    )
    def test_ts02_p4_property_create_atomic(self, name: str, desc: str, tmp_path: Path) -> None:
        """TS-02-P4: Campaign.create is atomic w.r.t. campaign.yaml.

        Property 4: After a successful create, campaign.yaml exists and
        matches; after a failed create, no campaign.yaml is introduced.

        Validates: 02-REQ-1.1, 02-REQ-1.2, 02-REQ-1.E1
        """
        path = tmp_path / f"test_campaign_{uuid.uuid4().hex[:8]}"

        camp = Campaign.create(path, name, desc)
        data = yaml.safe_load((path / "campaign.yaml").read_text())
        assert data["name"] == name
        assert data["description"] == desc
        assert camp.metadata.name == name
        assert camp.metadata.description == desc


# ---------------------------------------------------------------------------
# Archive-aware prefix numbering tests: Issue #711
# ---------------------------------------------------------------------------


class TestArchiveAwarePrefixNumbering:
    """Tests for archive-aware prefix numbering in new_spec() — Issue #711."""

    def test_new_spec_continues_from_archived_prefix(self, tmp_path: Path) -> None:
        """AC-1: new_spec uses next prefix after highest archived spec.

        Given specs/ is empty and archive/ has 01_alpha through 05_epsilon,
        new_spec('zeta', 'PRD') should create 06_zeta.
        """
        target = tmp_path / "camp"
        camp = Campaign.create(target, "Test", "Desc")

        # Simulate archived specs
        archive = target / "archive"
        archive.mkdir()
        for i, name in enumerate(["alpha", "beta", "gamma", "delta", "epsilon"], 1):
            (archive / f"{i:02d}_{name}").mkdir()

        camp.new_spec("zeta", "PRD content")
        assert (target / "06_zeta").is_dir()
        assert not (target / "01_zeta").exists()

    def test_new_spec_max_of_active_and_archived(self, tmp_path: Path) -> None:
        """AC-2: new_spec uses max of both active and archived prefixes.

        Given archive/ has 01-05 and active has 06_zeta,
        new_spec('eta', 'PRD') should create 07_eta.
        """
        target = tmp_path / "camp"
        camp = Campaign.create(target, "Test", "Desc")

        # Simulate archived specs
        archive = target / "archive"
        archive.mkdir()
        for i, name in enumerate(["alpha", "beta", "gamma", "delta", "epsilon"], 1):
            (archive / f"{i:02d}_{name}").mkdir()

        # Create active spec 06_zeta
        camp.new_spec("zeta", "PRD Z")
        assert (target / "06_zeta").is_dir()

        # Next spec should be 07
        camp.new_spec("eta", "PRD E")
        assert (target / "07_eta").is_dir()

    def test_new_spec_fallback_when_both_empty(self, tmp_path: Path) -> None:
        """AC-3: new_spec falls back to 01 when both dirs are empty.

        Backward compatibility: no active specs, no archive dir.
        """
        target = tmp_path / "camp"
        camp = Campaign.create(target, "Test", "Desc")

        camp.new_spec("first", "PRD content")
        assert (target / "01_first").is_dir()

    def test_new_spec_no_archive_dir(self, tmp_path: Path) -> None:
        """AC-4: new_spec works when archive/ does not exist.

        No FileNotFoundError or AttributeError.
        """
        target = tmp_path / "camp"
        camp = Campaign.create(target, "Test", "Desc")
        camp.new_spec("alpha", "PRD A")

        # No archive dir — should still work normally
        camp.new_spec("beta", "PRD B")
        assert (target / "02_beta").is_dir()

    def test_new_spec_ignores_non_spec_entries_in_archive(self, tmp_path: Path) -> None:
        """AC-5: Stray files/folders in archive/ are silently ignored.

        Non-matching entries should not affect prefix computation.
        """
        target = tmp_path / "camp"
        camp = Campaign.create(target, "Test", "Desc")

        archive = target / "archive"
        archive.mkdir()
        (archive / "01_alpha").mkdir()
        (archive / "02_beta").mkdir()
        # Stray entries that should be ignored
        (archive / "README.md").write_text("# Archive")
        (archive / "not_a_spec").mkdir()

        camp.new_spec("gamma", "PRD G")
        assert (target / "03_gamma").is_dir()


# ---------------------------------------------------------------------------
# Integration smoke tests: TS-02-SMOKE-1, TS-02-SMOKE-2
# ---------------------------------------------------------------------------


class TestCampaignSmokeTests:
    """Integration smoke tests for campaign operations."""

    def test_ts02_smoke_1_campaign_to_spec_creation(self, tmp_path: Path) -> None:
        """TS-02-SMOKE-1: Full flow — create campaign, create spec, verify structure.

        Execution Path: Path 1, Path 3 from design.md.
        Must NOT satisfy with: Mocking Campaign or SpecSession internals.
        """
        camp = Campaign.create(tmp_path / "smoke", "Smoke Test", "Integration test")
        session = camp.new_spec("first_spec", "# PRD\n\nContent")

        assert (tmp_path / "smoke" / "campaign.yaml").exists()
        assert (tmp_path / "smoke" / "01_first_spec" / "prd.md").exists()
        assert (tmp_path / "smoke" / "01_first_spec" / "_session.json").exists()
        assert camp.metadata.name == "Smoke Test"
        assert session.state == SessionState.INIT

    def test_ts02_smoke_2_open_and_list_specs(self, tmp_path: Path) -> None:
        """TS-02-SMOKE-2: Full flow — create campaign with specs, reopen, list.

        Execution Path: Path 2 from design.md.
        Must NOT satisfy with: Mocking Campaign internals.
        """
        camp = Campaign.create(tmp_path / "smoke2", "Test", "Desc")
        camp.new_spec("alpha", "PRD A")
        camp.new_spec("beta", "PRD B")

        reopened = Campaign.open(tmp_path / "smoke2")
        assert reopened.metadata.name == "Test"

        specs = reopened.specs()
        assert len(specs) == 2
        assert specs[0].name == "01_alpha"
        assert specs[1].name == "02_beta"
