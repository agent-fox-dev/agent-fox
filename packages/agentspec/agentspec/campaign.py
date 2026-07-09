"""Campaign directory lifecycle management.

Handles creation, opening, spec enumeration, and new-spec provisioning
within a campaign working directory. A campaign is a directory containing
``campaign.yaml`` and one or more numbered spec subdirectories.
"""

from __future__ import annotations

import logging
import re
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import TYPE_CHECKING

import yaml
from afspec.discovery import parse_spec_dir_name

from agentspec.errors import CampaignError

logger = logging.getLogger(__name__)

if TYPE_CHECKING:
    from agentspec.session import SpecSession

# Pattern for valid spec names: starts with letter, then letters/digits/underscores
_SPEC_NAME_PATTERN = re.compile(r"^[a-z][a-z0-9_]*$")

_CAMPAIGN_YAML = "campaign.yaml"


@dataclass
class CampaignMetadata:
    """Metadata stored in campaign.yaml.

    Attributes:
        name: Human-readable campaign name.
        description: Campaign description.
        created_at: ISO 8601 creation timestamp.
        updated_at: ISO 8601 last-update timestamp.
    """

    name: str
    description: str
    created_at: str  # ISO 8601
    updated_at: str  # ISO 8601


def _now_iso() -> str:
    """Return the current UTC time as an ISO 8601 string."""
    return datetime.now(UTC).isoformat()


class Campaign:
    """Campaign directory management.

    A campaign is a working directory containing ``campaign.yaml`` and
    one or more numbered spec subdirectories following the
    ``{NN}_{snake_case_name}`` naming convention.
    """

    def __init__(self, path: Path, metadata: CampaignMetadata) -> None:
        self._path = path
        self._metadata = metadata

    @staticmethod
    def create(path: Path, name: str, description: str) -> Campaign:
        """Create a new campaign directory with campaign.yaml.

        Args:
            path: Target directory path for the campaign.
            name: Human-readable campaign name.
            description: Campaign description.

        Returns:
            A ``Campaign`` instance bound to the created directory.

        Raises:
            CampaignError: If the path already contains ``campaign.yaml``,
                the directory is non-empty and not a campaign, or the
                parent directory does not exist.
        """
        # Check parent directory exists (02-REQ-1.E2)
        if not path.parent.exists():
            msg = f"Parent directory does not exist: {path.parent}"
            raise CampaignError(msg)

        # Check for existing campaign.yaml (02-REQ-1.2)
        campaign_yaml = path / _CAMPAIGN_YAML
        if campaign_yaml.exists():
            msg = f"Campaign already exists at {path}"
            raise CampaignError(msg)

        # Check for non-empty non-campaign directory (02-REQ-1.E1)
        if path.exists() and any(path.iterdir()):
            msg = f"Directory is not empty and not a campaign: {path}"
            raise CampaignError(msg)

        # Create directory if it does not exist
        path.mkdir(exist_ok=True)

        # Write campaign.yaml
        now = _now_iso()
        metadata = CampaignMetadata(
            name=name,
            description=description,
            created_at=now,
            updated_at=now,
        )
        _write_campaign_yaml(path, metadata)

        return Campaign(path, metadata)

    @staticmethod
    def open(path: Path) -> Campaign:
        """Open an existing campaign directory.

        Args:
            path: Path to the campaign directory.

        Returns:
            A ``Campaign`` instance with populated metadata.

        Raises:
            CampaignError: If the path does not contain ``campaign.yaml``
                or the YAML is invalid.
        """
        campaign_yaml = path / _CAMPAIGN_YAML
        if not campaign_yaml.exists():
            msg = f"Not a campaign directory (no {_CAMPAIGN_YAML}): {path}"
            raise CampaignError(msg)

        try:
            raw = campaign_yaml.read_text()
            data = yaml.safe_load(raw)
        except yaml.YAMLError as exc:
            msg = f"Invalid YAML in {campaign_yaml}: {exc}"
            raise CampaignError(msg) from exc

        if not isinstance(data, dict):
            msg = f"Invalid campaign.yaml structure in {path}: expected a mapping"
            raise CampaignError(msg)

        metadata = CampaignMetadata(
            name=data.get("name", path.name),
            description=data.get("description", ""),
            created_at=data.get("created_at", ""),
            updated_at=data.get("updated_at", ""),
        )

        return Campaign(path, metadata)

    def specs(self) -> list[Path]:
        """List spec subdirectories sorted by numeric prefix.

        Returns:
            A list of ``Path`` objects for all spec subdirectories
            matching the canonical spec directory pattern, sorted by
            numeric prefix. Excludes ``archive/`` and non-matching entries.
        """
        result: list[tuple[int, Path]] = []
        for entry in self._path.iterdir():
            if not entry.is_dir():
                continue
            parsed = parse_spec_dir_name(entry.name)
            if parsed is not None:
                result.append((parsed[0], entry))

        result.sort(key=lambda x: x[0])
        return [p for _, p in result]

    def new_spec(
        self,
        spec_name: str,
        prd: str | Path,
        mode: str = "interactive",
    ) -> SpecSession:
        """Create a new spec directory and return its session.

        Args:
            spec_name: Snake-case name for the spec (must match
                ``[a-z][a-z0-9_]*``).
            prd: PRD content as a string, or a ``Path`` to a file
                containing the PRD.
            mode: Session mode — ``"interactive"`` or ``"one-shot"``.

        Returns:
            A ``SpecSession`` instance for the new spec.

        Raises:
            CampaignError: If ``spec_name`` is invalid or ``prd`` is a
                ``Path`` that does not exist.
        """
        # Validate spec name (02-REQ-3.E1)
        if not _SPEC_NAME_PATTERN.match(spec_name):
            msg = (
                f"Invalid spec name {spec_name!r}: must match "
                f"[a-z][a-z0-9_]* (start with lowercase letter, "
                f"only lowercase letters, digits, and underscores)"
            )
            raise CampaignError(msg)

        # Resolve PRD content
        if isinstance(prd, Path):
            if not prd.exists():
                msg = f"PRD file does not exist: {prd}"
                raise CampaignError(msg)
            prd_content = prd.read_text()
        else:
            prd_content = prd

        # Compute next numeric prefix (02-REQ-3.3)
        existing_specs = self.specs()
        max_prefix = 0
        if existing_specs:
            max_prefix = max(int(s.name.split("_")[0]) for s in existing_specs)

        # Also check archive for highest used prefix (#711)
        archive_dir = self._path / "archive"
        if archive_dir.is_dir():
            for entry in archive_dir.iterdir():
                if entry.is_dir():
                    parsed = parse_spec_dir_name(entry.name)
                    if parsed is not None:
                        max_prefix = max(max_prefix, parsed[0])

        next_prefix = max_prefix + 1

        spec_id = f"{next_prefix:02d}"
        spec_dir_name = f"{spec_id}_{spec_name}"
        spec_dir = self._path / spec_dir_name

        # Create spec directory
        spec_dir.mkdir()

        # Write prd.md with YAML frontmatter (02-REQ-3.4)
        now = _now_iso()
        frontmatter = {
            "spec_id": spec_id,
            "spec_name": spec_name,
            "title": spec_name.replace("_", " ").title(),
            "status": "draft",
            "created_at": now,
            "updated_at": now,
            "owner": "",
            "source": "interactive",
            "schema_version": 1,
        }
        frontmatter_yaml = yaml.dump(frontmatter, default_flow_style=False, sort_keys=False)
        prd_text = f"---\n{frontmatter_yaml}---\n{prd_content}\n"
        (spec_dir / "prd.md").write_text(prd_text)

        # Write initial _session.json (02-REQ-5.3)
        from agentspec.session import SpecSession

        session = SpecSession._create(spec_dir, mode=mode)

        # Update campaign.yaml updated_at
        self._metadata.updated_at = now
        _write_campaign_yaml(self._path, self._metadata)

        return session

    @property
    def path(self) -> Path:
        """Campaign root directory path."""
        return self._path

    @property
    def metadata(self) -> CampaignMetadata:
        """Campaign metadata from campaign.yaml."""
        return self._metadata


def _write_campaign_yaml(path: Path, metadata: CampaignMetadata) -> None:
    """Atomically write campaign.yaml to the given directory.

    Uses a temporary file and rename for crash safety.
    """
    data = {
        "name": metadata.name,
        "description": metadata.description,
        "created_at": metadata.created_at,
        "updated_at": metadata.updated_at,
    }
    content = yaml.dump(data, default_flow_style=False, sort_keys=False)

    target = path / _CAMPAIGN_YAML
    tmp = path / f".{_CAMPAIGN_YAML}.tmp"
    tmp.write_text(content)
    tmp.rename(target)
