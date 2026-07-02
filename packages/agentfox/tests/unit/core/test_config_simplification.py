"""Unit tests for config simplification.

Test Spec: TS-68-1 through TS-68-10, TS-68-12 through TS-68-17,
           TS-68-E1 through TS-68-E4
Requirements: 68-REQ-1.*, 68-REQ-2.*, 68-REQ-3.*, 68-REQ-4.*, 68-REQ-5.*, 68-REQ-6.*
"""

from __future__ import annotations

import re
import tomllib

import pytest
import tomlkit
from agentfox.core.config import ArchetypeInstancesConfig
from agentfox.core.config_gen import (
    _FOOTER_COMMENT,
    _get_description,
    generate_default_config,
    merge_existing_config,
)
from pydantic.fields import FieldInfo

# Sections that must appear (active or commented) in the simplified template.
_EXPECTED_VISIBLE_SECTIONS = {
    "orchestrator",
    "archetypes",
    "archetypes.instances",
    "platform",
}

# Sections that must be completely absent from the simplified template.
_EXPECTED_HIDDEN_SECTIONS = {
    "routing",
    "theme",
    "security",
    "knowledge",
    "pricing",
    "planning",
    "night_shift",
}

_FOOTER = _FOOTER_COMMENT

# Promoted fields expected after implementation (section_path, field_name).
_EXPECTED_PROMOTED_FIELDS = [
    ("orchestrator", "parallel"),
    ("orchestrator", "max_budget_usd"),
    ("platform", "type"),
    ("archetypes", "reviewer"),
    ("archetypes", "verifier"),
    ("archetypes.instances", "verifier"),
]


def _extract_section_headers(template: str) -> set[str]:
    """Extract all section names from active and commented section headers."""
    headers: set[str] = set()
    for m in re.finditer(r"^#?\s*\[([a-zA-Z_][a-zA-Z0-9_.]*)\]\s*$", template, re.MULTILINE):
        headers.add(m.group(1).strip())
    return headers


def _find_description_above(template: str, field_name: str) -> str | None:
    """Find the ## comment line immediately above an active (uncommented) field."""
    lines = template.split("\n")
    for i, line in enumerate(lines):
        stripped = line.strip()
        # Match active (non-commented) field assignment line
        if re.match(rf"^{re.escape(field_name)}\s*=", stripped):
            # Walk backward looking for the most recent ## comment
            for j in range(i - 1, max(i - 6, -1), -1):
                prev = lines[j].strip()
                if prev.startswith("##"):
                    return prev
                if prev and not prev.startswith("#"):
                    break
    return None


# ---------------------------------------------------------------------------
# TS-68-1 & TS-68-2: Visible sections / hidden sections
# ---------------------------------------------------------------------------


class TestTemplateSectionVisibility:
    """TS-68-1, TS-68-2: Template contains only visible sections."""

    def test_visible_sections_present(self):
        """Key visible sections appear as headers in the template."""
        template = generate_default_config()
        headers = _extract_section_headers(template)
        for section in ["orchestrator", "archetypes"]:
            assert section in headers, f"[{section}] not found in template"

    def test_hidden_sections_not_present(self):
        """Hidden sections must be completely absent — not even commented out."""
        template = generate_default_config()
        for section in _EXPECTED_HIDDEN_SECTIONS:
            assert f"[{section}]" not in template, f"Hidden section [{section}] found in template"
            assert f"# [{section}]" not in template, f"Commented hidden section # [{section}] found in template"

    def test_only_visible_section_headers(self):
        """Every section header in the template belongs to the visible set."""
        template = generate_default_config()
        headers = _extract_section_headers(template)
        for header in headers:
            assert header in _EXPECTED_VISIBLE_SECTIONS, f"Unexpected section [{header}] found in template"


# ---------------------------------------------------------------------------
# TS-68-3: Template footer
# ---------------------------------------------------------------------------


class TestTemplateFooter:
    """TS-68-3: Template ends with reference doc footer."""

    def test_footer_present(self):
        """Footer comment is present in the template."""
        template = generate_default_config()
        assert _FOOTER in template, "Footer line not found in template"

    def test_footer_appears_exactly_once(self):
        """Footer appears exactly one time."""
        template = generate_default_config()
        count = template.count("docs/config-reference.md")
        assert count == 1, f"Expected exactly 1 occurrence of 'docs/config-reference.md', got {count}"

    def test_footer_in_header_of_template(self):
        """Footer URL appears in the header (first 8 non-empty lines) of the template."""
        template = generate_default_config()
        non_empty_lines = [ln for ln in template.split("\n") if ln.strip()]
        assert _FOOTER in non_empty_lines[:8], "Footer URL not in the header area of the template"


# ---------------------------------------------------------------------------
# TS-68-4: Template line count
# ---------------------------------------------------------------------------


class TestTemplateLineCount:
    """TS-68-4: Simplified template does not exceed 60 lines."""

    def test_line_count_at_most_60(self):
        """Template must have at most 60 lines (including blanks and comments)."""
        template = generate_default_config()
        lines = template.strip().split("\n")
        assert len(lines) <= 60, f"Template has {len(lines)} lines, expected <= 60"


# ---------------------------------------------------------------------------
# TS-68-6: archetypes.instances.verifier promoted with value 2
# ---------------------------------------------------------------------------


class TestVerifierInstancesPromoted:
    """TS-68-6: archetypes.instances.verifier is promoted with value 1."""

    def test_verifier_instances_in_parsed_template(self):
        """Parsed template has archetypes.instances.verifier == 1."""
        template = generate_default_config()
        parsed = tomllib.loads(template)
        actual = parsed["archetypes"]["instances"]["verifier"]
        assert actual == 1, f"archetypes.instances.verifier is {actual}, expected 1"

    def test_verifier_instances_line_not_commented(self):
        """verifier = 1 under [archetypes.instances] is not commented out."""
        template = generate_default_config()
        in_instances = False
        for line in template.split("\n"):
            stripped = line.strip()
            if stripped in ("[archetypes.instances]", "# [archetypes.instances]"):
                in_instances = True
                continue
            if in_instances and re.match(r"^\[", stripped):
                in_instances = False
            if in_instances and re.match(r"^verifier\s*=\s*1", stripped):
                assert not line.strip().startswith("#"), f"verifier = 1 is commented: {line!r}"
                return
        # If we reach here, line was not found as uncommented
        pytest.fail("Uncommented 'verifier = 1' not found under [archetypes.instances]")


# ---------------------------------------------------------------------------
# TS-68-7: Quality archetype toggles promoted and active
# ---------------------------------------------------------------------------


class TestArchetypeTogglesPromoted:
    """TS-68-7: All quality archetype toggles are promoted and active."""

    def test_all_toggles_promoted(self):
        """reviewer, verifier all equal true in parsed template."""
        template = generate_default_config()
        parsed = tomllib.loads(template)
        for toggle in ["reviewer", "verifier"]:
            actual = parsed["archetypes"].get(toggle)
            assert actual is True, f"archetypes.{toggle} is {actual!r}, expected True"


# ---------------------------------------------------------------------------
# TS-68-8: max_budget_usd and models.coding promoted
# ---------------------------------------------------------------------------


class TestBudgetAndModelPromoted:
    """TS-68-8: max_budget_usd and models.coding are promoted with correct values."""

    def test_max_budget_usd_promoted_as_20(self):
        """orchestrator.max_budget_usd >= 20.0 in parsed template (NS-REQ-4.1)."""
        template = generate_default_config()
        parsed = tomllib.loads(template)
        actual = parsed["orchestrator"]["max_budget_usd"]
        assert actual >= 20.0, f"max_budget_usd is {actual}, expected >= 20.0"
        assert actual != 8.0, "max_budget_usd must not be the old 8.0 value"

    def test_models_coding_not_promoted(self):
        """models.coding is NOT an active (uncommented) field in the default template.

        The [models] coding field is deprecated (issue #597). It must not appear
        as an active line in the generated config to avoid the deprecation warning.
        The coder's ADVANCED tier is now the archetype registry default.
        """
        template = generate_default_config()
        non_comment_lines = [ln for ln in template.splitlines() if not ln.lstrip().startswith("#")]
        assert not any("coding =" in ln for ln in non_comment_lines), (
            "models.coding must not appear as an active line in the default template"
        )

    def test_max_budget_line_not_commented(self):
        """max_budget_usd appears as an active (uncommented) line (NS-REQ-4.1)."""
        template = generate_default_config()
        for line in template.split("\n"):
            if "max_budget_usd" in line and not line.strip().startswith("#"):
                # Found an active max_budget_usd line; ensure it has the new value
                assert "8.0" not in line, f"max_budget_usd must not be 8.0: {line!r}"
                return
        raise AssertionError("max_budget_usd not found as an active line in the template")


# ---------------------------------------------------------------------------
# TS-68-9: ArchetypeInstancesConfig default verifier == 2
# ---------------------------------------------------------------------------


class TestVerifierDefaultChanged:
    """TS-68-9: ArchetypeInstancesConfig() default verifier is 1."""

    def test_verifier_default_is_1(self):
        """ArchetypeInstancesConfig() has verifier == 1."""
        config = ArchetypeInstancesConfig()
        assert config.verifier == 1, f"ArchetypeInstancesConfig().verifier is {config.verifier}, expected 1"


# ---------------------------------------------------------------------------
# TS-68-10: Promoted field descriptions are meaningful
# ---------------------------------------------------------------------------


class TestDescriptionsMeaningful:
    """TS-68-10: Promoted field descriptions are not mechanical name transformations."""

    def test_no_mechanical_descriptions(self):
        """No promoted field has a description equal to its title-cased name."""
        template = generate_default_config()
        for _section, field_name in _EXPECTED_PROMOTED_FIELDS:
            desc_line = _find_description_above(template, field_name)
            if desc_line is None:
                continue  # Field not active — other tests cover that
            mechanical = field_name.replace("_", " ").title()
            assert mechanical not in desc_line, f"Field '{field_name}' has mechanical description: {desc_line!r}"

    def test_promoted_fields_have_descriptions(self):
        """Every promoted field in the template has a ## comment above it."""
        template = generate_default_config()
        for _section, field_name in _EXPECTED_PROMOTED_FIELDS:
            desc_line = _find_description_above(template, field_name)
            assert desc_line is not None, f"Promoted field '{field_name}' has no ## description comment above it"


# ---------------------------------------------------------------------------
# TS-68-12: Merge preserves hidden sections already present
# ---------------------------------------------------------------------------


class TestMergePreservesHiddenSections:
    """TS-68-12: Merge preserves hidden sections already in existing config."""

    def test_routing_section_preserved(self):
        """[routing] with active values is preserved after merge."""
        existing = "[orchestrator]\nparallel = 4\n\n[routing]\nretries_before_escalation = 3\n"
        result = merge_existing_config(existing)
        assert "[routing]" in result, "[routing] section not preserved"
        assert "retries_before_escalation = 3" in result, "retries_before_escalation = 3 not preserved"

    def test_theme_section_preserved(self):
        """[theme] with active values is preserved after merge."""
        existing = "[orchestrator]\nparallel = 2\n\n[theme]\nplayful = false\n"
        result = merge_existing_config(existing)
        assert "[theme]" in result, "[theme] section not preserved"
        assert "playful = false" in result, "theme.playful = false not preserved"


# ---------------------------------------------------------------------------
# TS-68-13: Merge does not add hidden sections not in original config
# ---------------------------------------------------------------------------


class TestMergeNoHiddenInjection:
    """TS-68-13: Merge does not introduce hidden sections."""

    def test_hidden_sections_not_added(self):
        """Merge on a visible-only config must not add hidden sections."""
        existing = "[orchestrator]\nparallel = 4\n\n[archetypes]\nreviewer = true\n"
        result = merge_existing_config(existing)
        for section in _EXPECTED_HIDDEN_SECTIONS:
            assert f"[{section}]" not in result, f"Hidden section [{section}] was added by merge"
            assert f"# [{section}]" not in result, f"Commented hidden section # [{section}] was added by merge"

    def test_merge_with_only_orchestrator_no_hidden(self):
        """Merge of orchestrator-only config adds no hidden sections."""
        existing = "[orchestrator]\nparallel = 2\n"
        result = merge_existing_config(existing)
        for section in _EXPECTED_HIDDEN_SECTIONS:
            assert f"[{section}]" not in result, f"Hidden section [{section}] injected by merge"
            assert f"# [{section}]" not in result, f"Commented hidden section # [{section}] injected by merge"


# ---------------------------------------------------------------------------
# TS-68-14: Merge on empty config produces simplified template
# ---------------------------------------------------------------------------


class TestMergeEmptyConfig:
    """TS-68-14, 68-REQ-5.E2: Merging empty config produces simplified template."""

    def test_empty_string_produces_simplified_template(self):
        """merge_existing_config('') must equal generate_default_config()."""
        result = merge_existing_config("")
        expected = generate_default_config()
        assert result == expected, "merge_existing_config('') did not produce the simplified template"

    def test_whitespace_only_produces_simplified_template(self):
        """Whitespace-only existing config also produces simplified template."""
        result = merge_existing_config("   \n  \n  ")
        expected = generate_default_config()
        assert result == expected, "merge_existing_config(whitespace) did not produce the simplified template"


# ---------------------------------------------------------------------------
# TS-68-17: Footer not duplicated on merge
# ---------------------------------------------------------------------------


class TestFooterNotDuplicated:
    """TS-68-17, 68-REQ-6.E1: Footer is not duplicated when merging a config
    that already has the footer."""

    def test_footer_not_duplicated_after_merge(self):
        """merge_existing_config on a config with footer keeps exactly one footer."""
        existing = generate_default_config()  # already has footer
        result = merge_existing_config(existing)
        count = result.count("docs/config-reference.md")
        assert count == 1, f"Expected exactly 1 footer occurrence after merge, got {count}"

    def test_double_merge_still_one_footer(self):
        """Two successive merges still result in exactly one footer."""
        content = generate_default_config()
        content = merge_existing_config(content)
        content = merge_existing_config(content)
        count = content.count("docs/config-reference.md")
        assert count == 1, f"Expected exactly 1 footer occurrence after 2 merges, got {count}"


# ---------------------------------------------------------------------------
# TS-68-E1: Multiple hidden sections preserved
# ---------------------------------------------------------------------------


class TestEdgeCaseMultipleHiddenSectionsPreserved:
    """TS-68-E1: Existing configs with multiple hidden sections keep them."""

    def test_multiple_hidden_sections_preserved(self):
        """routing, theme, and knowledge sections all preserved in merged output."""
        existing = (
            "[orchestrator]\nparallel = 2\n\n"
            "[routing]\nretries_before_escalation = 2\n\n"
            "[theme]\nplayful = false\n\n"
            "[knowledge]\nask_top_k = 50\n"
        )
        result = merge_existing_config(existing)
        assert "retries_before_escalation = 2" in result, "routing.retries_before_escalation not preserved"
        assert "playful = false" in result, "theme.playful not preserved"
        assert "ask_top_k = 50" in result, "knowledge.ask_top_k not preserved"


# ---------------------------------------------------------------------------
# TS-68-E2: Template parses without errors
# ---------------------------------------------------------------------------


class TestEdgeCaseTemplateValidToml:
    """TS-68-E2: Generated template is valid TOML."""

    def test_template_parses_without_error(self):
        """tomlkit.parse(template) succeeds without raising."""
        template = generate_default_config()
        parsed = tomlkit.parse(template)
        assert parsed is not None

    def test_template_valid_with_stdlib_tomllib(self):
        """tomllib.loads(template) also succeeds."""
        template = generate_default_config()
        parsed = tomllib.loads(template)
        assert isinstance(parsed, dict)


# ---------------------------------------------------------------------------
# TS-68-E3: Deprecated fields are marked during merge
# ---------------------------------------------------------------------------


class TestEdgeCaseDeprecatedFields:
    """TS-68-E3: Unknown active fields get DEPRECATED comment on merge."""

    def test_unknown_field_marked_deprecated(self):
        """A field 'foo_bar' not in schema gets marked with DEPRECATED."""
        existing = "[orchestrator]\nparallel = 2\nfoo_bar = 42\n"
        result = merge_existing_config(existing)
        assert "DEPRECATED" in result, "No DEPRECATED marker found in merge result"
        assert "foo_bar" in result, "Deprecated field name 'foo_bar' not in result"


# ---------------------------------------------------------------------------
# TS-68-E4: Description fallback for fields without explicit description
# ---------------------------------------------------------------------------


class TestEdgeCaseDescriptionFallback:
    """TS-68-E4: Fields without description metadata get a title-cased fallback."""

    def test_fallback_returns_title_cased_field_name(self):
        """_get_description returns title-cased fallback for unknown field."""
        from pydantic import BaseModel

        class _TestModel(BaseModel):
            unknown_field: str = "default"

        # Construct a FieldInfo without description
        field_info_no_desc = FieldInfo(default="default")
        result = _get_description(_TestModel, "unknown_field", field_info_no_desc)
        assert result == "Unknown Field", f"Expected fallback 'Unknown Field', got {result!r}"

    def test_fallback_uses_description_when_present(self):
        """_get_description returns the provided description if set."""
        from pydantic import BaseModel

        class _TestModel(BaseModel):
            my_field: str = "default"

        field_info_with_desc = FieldInfo(default="default", description="My custom desc")  # noqa: E501
        result = _get_description(_TestModel, "my_field", field_info_with_desc)
        assert result == "My custom desc", f"Expected 'My custom desc', got {result!r}"
