"""Configuration system: TOML loading, pydantic models, defaults.

Loads project configuration from a TOML file, validates all fields using
pydantic models, and merges with documented defaults. Out-of-range numeric
values are clamped to the nearest valid bound rather than rejected.

The ``load_config()`` function is the single shared entry point used by
``af``, ``nightshift``, and ``spec`` CLIs.  When called without arguments
it resolves, merges, and validates a global config
(``$HOME/.agent-fox/config.toml``) with a local config
(``.agent-fox/config.toml``) using shallow section replacement semantics.

Requirements: 01-REQ-2.1, 01-REQ-2.2, 01-REQ-2.3, 01-REQ-2.4, 01-REQ-2.5,
              01-REQ-2.6, 01-REQ-2.E1, 01-REQ-2.E2, 01-REQ-2.E3,
              13-REQ-1.1, 13-REQ-1.2, 13-REQ-1.3, 13-REQ-2.1, 13-REQ-2.2,
              13-REQ-2.3, 13-REQ-3.1, 13-REQ-3.2, 13-REQ-3.3, 13-REQ-4.1,
              13-REQ-4.2, 13-REQ-4.3, 13-REQ-5.1, 13-REQ-5.2, 13-REQ-7.1,
              13-REQ-7.2, 13-REQ-7.3, 13-REQ-7.4
"""

from __future__ import annotations

import logging
import os
import tomllib
from enum import StrEnum
from pathlib import Path
from typing import Annotated, Any, Literal, Self

from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    PrivateAttr,
    ValidationError,
    field_validator,
    model_validator,
)

from agentfox.core.errors import ConfigError

logger = logging.getLogger(__name__)


def _clamp(
    value: int | float,
    *,
    ge: int | float | None = None,
    le: int | float | None = None,
    field_name: str,
) -> int | float:
    """Clamp a numeric value to valid bounds, logging a warning if adjusted."""
    original = value
    if ge is not None and value < ge:
        value = type(original)(ge) if isinstance(original, int) else ge
    if le is not None and value > le:
        value = type(original)(le) if isinstance(original, int) else le
    if value != original:
        logger.warning(
            "Config field '%s' value %s out of range, clamped to %s",
            field_name,
            original,
            value,
        )
    return value


class Clamped:
    """Annotation marking a numeric field for automatic clamping."""

    __slots__ = ("ge", "le", "cast")

    def __init__(
        self,
        ge: int | float | None = None,
        le: int | float | None = None,
        cast: type | None = None,
    ) -> None:
        self.ge = ge
        self.le = le
        self.cast = cast


def _auto_clamp_validator() -> Any:
    """Return a model_validator that clamps all fields annotated with Clamped."""

    @model_validator(mode="after")
    def _validate(self: Any) -> Any:
        for name, field_info in type(self).model_fields.items():
            for meta in field_info.metadata:
                if isinstance(meta, Clamped):
                    value = getattr(self, name)
                    if value is None:
                        continue
                    clamped = _clamp(value, ge=meta.ge, le=meta.le, field_name=name)
                    if meta.cast is not None:
                        clamped = meta.cast(clamped)
                    if clamped != value:
                        object.__setattr__(self, name, clamped)
        return self

    return _validate


class RoutingConfig(BaseModel):
    """Timeout retry configuration."""

    model_config = ConfigDict(extra="ignore")

    max_timeout_retries: Annotated[int, Clamped(ge=0, cast=int)] = Field(
        default=2,
        description="Maximum timeout retries before falling through to failure handler",
    )
    timeout_multiplier: Annotated[float, Clamped(ge=1.0)] = Field(
        default=1.5,
        description="Factor by which max_turns and session_timeout are extended on timeout retry",
    )
    timeout_ceiling_factor: Annotated[float, Clamped(ge=1.0)] = Field(
        default=2.0,
        description="Maximum session_timeout as a factor of the original configured value",
    )

    _auto_clamp = _auto_clamp_validator()


class OrchestratorConfig(BaseModel):
    model_config = ConfigDict(extra="ignore")

    parallel: Annotated[int, Clamped(ge=1, le=8)] = Field(default=2, description="Maximum parallel sessions")
    sync_interval: Annotated[int, Clamped(ge=0)] = Field(default=5, description="Sync interval in task groups")
    hot_load: bool = Field(default=True, description="Hot-load specs between sessions")
    max_retries: Annotated[int, Clamped(ge=0)] = Field(default=2, description="Maximum retries per task group")
    session_timeout: Annotated[int, Clamped(ge=1)] = Field(default=45, description="Session timeout in minutes")
    inter_session_delay: Annotated[int, Clamped(ge=0)] = Field(
        default=3, description="Delay between sessions in seconds"
    )
    max_cost: float | None = Field(default=None, description="Maximum cost limit")
    max_sessions: int | None = Field(default=None, description="Maximum number of sessions")
    audit_retention_runs: Annotated[int, Clamped(ge=1, cast=int)] = Field(
        default=20,
        description="Maximum number of runs to retain in the audit log",
    )
    max_blocked_fraction: float | None = Field(
        default=None,
        description=("Stop the run when this fraction of nodes are blocked (0.0-1.0). None = disabled."),
    )
    max_review_fraction: float = Field(
        default=0.34,
        ge=0.0,
        le=1.0,
        description=(
            "Maximum fraction of parallel slots that may be occupied by "
            "review archetype sessions (0.0-1.0). auto_pre (group 0) nodes "
            "are exempt. Default 0.34 means at most ~1/3 of slots for reviews."
        ),
    )
    max_budget_usd: float = Field(
        default=20.0,
        ge=0.0,
        description="Maximum USD spend per session, 0 = unlimited",
    )

    watch_interval: Annotated[int, Clamped(ge=10, cast=int)] = Field(
        default=60,
        description=("Seconds between watch polls when --watch is active. Values below 10 are clamped to 10."),
    )

    _auto_clamp = _auto_clamp_validator()

    @field_validator("max_blocked_fraction")
    @classmethod
    def clamp_max_blocked_fraction(cls, v: float | None) -> float | None:
        if v is None:
            return v
        return _clamp(v, ge=0.0, le=1.0, field_name="max_blocked_fraction")


class SecurityConfig(BaseModel):
    model_config = ConfigDict(extra="ignore")

    bash_allowlist: list[str] | None = Field(default=None, description="Allowed bash commands")
    bash_allowlist_extend: list[str] = Field(default_factory=list, description="Additional allowed bash commands")


class ThemeConfig(BaseModel):
    model_config = ConfigDict(extra="ignore")

    playful: bool = Field(default=True, description="Enable playful output style")
    header: str = Field(default="bold #ff8c00", description="Header text style")
    success: str = Field(default="bold green", description="Success text style")
    error: str = Field(default="bold red", description="Error text style")
    warning: str = Field(default="bold yellow", description="Warning text style")
    info: str = Field(default="#daa520", description="Info text style")
    tool: str = Field(default="bold #cd853f", description="Tool text style")
    muted: str = Field(default="dim", description="Muted text style")


class PlatformConfig(BaseModel):
    """Platform configuration for issue tracking.

    Requirements: 65-REQ-1.1, 65-REQ-1.2, 65-REQ-1.E1,
                  65-REQ-2.1, 65-REQ-2.2, 65-REQ-2.3, 65-REQ-2.E1
    """

    model_config = ConfigDict(extra="ignore")

    type: str = Field(default="none", description="Platform type (none or github)")
    url: str = Field(default="", description="Issue tracker URL (defaults from type)")


class KnowledgeProviderConfig(BaseModel):
    """Configuration for the pluggable knowledge provider.

    Requirements: 116-REQ-7.1, 116-REQ-7.2, 116-REQ-7.3, 116-REQ-7.E1
    """

    model_config = ConfigDict(extra="ignore")

    max_items: int = Field(default=10, description="Max total retrieval items")
    max_cross_group_items: int = Field(default=3, description="Max cross-group retrieval items")


class KnowledgeConfig(BaseModel):
    """Knowledge store configuration.

    Requirements: 39-REQ-4.2, 114-REQ-8.1, 114-REQ-8.4, 114-REQ-8.5
    """

    model_config = ConfigDict(extra="ignore")

    store_path: str = Field(default=".agent-fox/knowledge.duckdb", description="Path to knowledge store")
    provider: KnowledgeProviderConfig = Field(
        default_factory=KnowledgeProviderConfig,
        description="Pluggable knowledge provider configuration (gotcha TTL, retrieval caps, etc.)",
    )


class ThinkingConfig(BaseModel):
    """Extended thinking configuration for an archetype.

    Requirements: 56-REQ-4.1, 56-REQ-4.E1, 56-REQ-4.E2
    """

    model_config = ConfigDict(extra="ignore")

    mode: Literal["enabled", "adaptive", "disabled"] = "disabled"
    budget_tokens: int = Field(default=10000, ge=0)

    @model_validator(mode="after")
    def validate_budget(self) -> Self:
        """budget_tokens must be > 0 when mode is 'enabled'."""
        if self.mode == "enabled" and self.budget_tokens <= 0:
            raise ValueError("budget_tokens must be > 0 when mode is 'enabled'")
        return self


class PerArchetypeConfig(BaseModel):
    """Unified per-archetype configuration table.

    Used via ``[archetypes.overrides.<name>]`` in config.toml. Provides a
    single, consolidated surface for all per-archetype knobs that previously
    required separate dict fields (``models``, ``max_turns``, ``thinking``,
    ``allowlists``).

    Requirements: 207-REQ-1, 207-REQ-2, 207-REQ-3
    """

    model_config = ConfigDict(extra="ignore")

    model_tier: str | None = Field(
        default=None,
        description="Model tier override (SIMPLE, STANDARD, ADVANCED). None = use registry default.",
    )
    model_variant: str | None = Field(
        default=None,
        description="Model variant override (fast, standard, extended). None = use registry default.",
    )
    max_turns: int | None = Field(
        default=None,
        description="Max turns override. 0 = unlimited. None = use registry default.",
        ge=0,
    )
    thinking_mode: Literal["enabled", "adaptive", "disabled"] | None = Field(
        default=None,
        description="Extended thinking mode. None = use registry default.",
    )
    thinking_budget: int | None = Field(
        default=None,
        description="Extended thinking budget tokens. None = use registry default.",
        ge=0,
    )
    allowlist: list[str] | None = Field(
        default=None,
        description="Bash command allowlist override. None = use registry default.",
    )
    max_budget_usd: float | None = Field(
        default=None,
        description=(
            "Per-archetype budget ceiling in USD. None = inherit global orchestrator.max_budget_usd. 0 = unlimited."
        ),
        ge=0.0,
    )
    modes: dict[str, PerArchetypeConfig] = Field(
        default_factory=dict,
        description=(
            "Per-mode overrides for this archetype. TOML: [archetypes.overrides.<name>.modes.<mode>]. 97-REQ-3.1"
        ),
    )

    @model_validator(mode="after")
    def validate_thinking(self) -> Self:
        """thinking_budget must be > 0 when thinking_mode is 'enabled'."""
        if self.thinking_mode == "enabled" and self.thinking_budget is not None and self.thinking_budget <= 0:
            raise ValueError("thinking_budget must be > 0 when thinking_mode is 'enabled'")
        return self


# Required for self-referential Pydantic model (modes: dict[str, PerArchetypeConfig])
PerArchetypeConfig.model_rebuild()


class ArchetypeInstancesConfig(BaseModel):
    """Per-archetype instance count configuration.

    Requirements: 26-REQ-6.2, 46-REQ-2.2, 98-REQ-8.3
    """

    model_config = ConfigDict(extra="ignore")

    reviewer: Annotated[int, Clamped(ge=1, le=5)] = Field(default=1, description="Number of reviewer instances")
    verifier: int = Field(default=1, description="Number of verifier instances (max clamped to 1)")

    _auto_clamp = _auto_clamp_validator()

    @field_validator("verifier")
    @classmethod
    def clamp_verifier_to_one(cls, v: int) -> int:
        """Verifier is always single-instance (98-REQ-6.2)."""
        if v != 1:
            logger.warning(
                "verifier instances clamped from %d to 1 (maximum is 1)",
                v,
            )
        return 1


class ReviewerConfig(BaseModel):
    """Reviewer-specific configuration.

    Contains mode-specific settings keyed by mode name concept, stored as flat fields.

    Requirements: 98-REQ-8.2
    """

    model_config = ConfigDict(extra="ignore")

    pre_review_block_threshold: Annotated[int, Clamped(ge=0)] = Field(
        default=1,
        description="Finding count to block merge for pre-review mode",
    )
    drift_review_block_threshold: int | None = Field(
        default=None,
        description="Drift count to block for drift-review mode (None = advisory only)",
    )
    audit_min_ts_entries: Annotated[int, Clamped(ge=1, cast=int)] = Field(
        default=5,
        description="Minimum TS entries to trigger audit-review injection",
    )
    audit_max_retries: Annotated[int, Clamped(ge=0, cast=int)] = Field(
        default=2,
        description="Maximum audit-review/coder retry iterations",
    )

    _auto_clamp = _auto_clamp_validator()


class CustomArchetypeConfig(BaseModel):
    """Configuration for a custom (project-defined) archetype.

    Specifies which built-in archetype's permission profile the custom
    archetype should inherit. Validated semantically at runtime by
    ``get_archetype()`` — the config layer only validates types/syntax.

    Requirements: 99-REQ-4.2
    """

    model_config = ConfigDict(extra="ignore")

    permissions: str = Field(
        default="coder",
        description="Built-in archetype name whose permissions to inherit",
    )


class ArchetypesConfig(BaseModel):
    """Archetype enable/disable toggles and per-archetype configuration.

    Requirements: 26-REQ-6.1 through 26-REQ-6.5, 26-REQ-6.E1, 46-REQ-2.1,
                  98-REQ-8.1, 98-REQ-8.2, 98-REQ-8.3, 98-REQ-8.E1
    """

    model_config = ConfigDict(extra="ignore")

    reviewer: bool = Field(default=True, description="Enable reviewer archetype")
    verifier: bool = Field(default=True, description="Enable verifier archetype")

    instances: ArchetypeInstancesConfig = Field(
        default_factory=ArchetypeInstancesConfig,
        description="Per-archetype instance counts",
    )
    reviewer_config: ReviewerConfig = Field(
        default_factory=ReviewerConfig,
        description="Reviewer-specific configuration",
    )
    overrides: dict[str, PerArchetypeConfig] = Field(
        default_factory=dict,
        description=("Unified per-archetype configuration tables. TOML: [archetypes.overrides.<name>]."),
    )
    custom: dict[str, CustomArchetypeConfig] = Field(
        default_factory=dict,
        description=(
            "Custom archetype configurations keyed by archetype name. "
            "TOML: [archetypes.custom.<name>]. "
            "Requirements: 99-REQ-4.2"
        ),
    )


class ModelPricing(BaseModel):
    """Pricing for a single model.

    Requirements: 34-REQ-2.1, 34-REQ-2.E2
    """

    model_config = ConfigDict(extra="ignore")

    # Requirements: 34-REQ-2.E2
    input_price_per_m: Annotated[float, Clamped(ge=0.0)] = Field(
        default=0.0, description="USD per million input tokens"
    )
    output_price_per_m: Annotated[float, Clamped(ge=0.0)] = Field(
        default=0.0, description="USD per million output tokens"
    )
    cache_read_price_per_m: Annotated[float, Clamped(ge=0.0)] = Field(
        default=0.0, description="USD per million cache-read input tokens"
    )
    cache_creation_price_per_m: Annotated[float, Clamped(ge=0.0)] = Field(
        default=0.0, description="USD per million cache-creation input tokens"
    )

    _auto_clamp = _auto_clamp_validator()


def _default_pricing_models() -> dict[str, ModelPricing]:
    """Return default pricing for all known Claude models.

    Requirements: 34-REQ-2.2, 34-REQ-5.1
    """
    return {
        "claude-haiku-4-5": ModelPricing(
            input_price_per_m=1.00,
            output_price_per_m=5.00,
            cache_read_price_per_m=0.10,
            cache_creation_price_per_m=1.25,
        ),
        "claude-sonnet-4-6": ModelPricing(
            input_price_per_m=3.00,
            output_price_per_m=15.00,
            cache_read_price_per_m=0.30,
            cache_creation_price_per_m=3.75,
        ),
        "claude-opus-4-5": ModelPricing(
            input_price_per_m=5.00,
            output_price_per_m=25.00,
            cache_read_price_per_m=0.50,
            cache_creation_price_per_m=6.25,
        ),
        "claude-opus-4-6": ModelPricing(
            input_price_per_m=5.00,
            output_price_per_m=25.00,
            cache_read_price_per_m=0.50,
            cache_creation_price_per_m=6.25,
        ),
        # Rates retrieved from https://www.anthropic.com/pricing on 2026-06-29
        "claude-opus-4-6[1m]": ModelPricing(
            input_price_per_m=5.00,
            output_price_per_m=25.00,
            cache_read_price_per_m=0.50,
            cache_creation_price_per_m=6.25,
        ),
    }


class PricingConfig(BaseModel):
    """Per-model pricing configuration.

    Requirements: 34-REQ-2.1, 34-REQ-2.2, 34-REQ-2.E1
    """

    model_config = ConfigDict(extra="ignore")

    models: dict[str, ModelPricing] = Field(
        default_factory=_default_pricing_models,
        description="Per-model pricing configuration",
    )


class PlanningConfig(BaseModel):
    """Planning and dispatch configuration.

    Requirements: 39-REQ-1.E1, 39-REQ-2.1, 39-REQ-9.3
    """

    model_config = ConfigDict(extra="ignore")

    file_conflict_detection: bool = Field(
        default=False,
        description="Detect file conflicts between parallel tasks",
    )

    _auto_clamp = _auto_clamp_validator()


class CachePolicy(StrEnum):
    """Prompt caching strategy for auxiliary API calls.

    Requirements: 77-REQ-1.1, 77-REQ-1.3, 77-REQ-1.4, 77-REQ-1.5
    """

    NONE = "NONE"
    DEFAULT = "DEFAULT"
    EXTENDED = "EXTENDED"


class CachingConfig(BaseModel):
    """Prompt caching configuration.

    Requirements: 77-REQ-1.1, 77-REQ-1.2, 77-REQ-1.E1
    """

    model_config = ConfigDict(extra="ignore")

    cache_policy: CachePolicy = Field(
        default=CachePolicy.DEFAULT,
        description="Caching policy: NONE, DEFAULT (5-min), or EXTENDED (1-hour TTL)",
    )

    @field_validator("cache_policy", mode="before")
    @classmethod
    def _parse_policy_case_insensitive(cls, v: Any) -> Any:
        """Accept policy values case-insensitively."""
        if isinstance(v, str):
            return v.upper()
        return v


class NightShiftConfig(BaseModel):
    """Night-shift daemon configuration.

    Requirements: 61-REQ-9.1, 61-REQ-9.E1, 125-REQ-5.1, 125-REQ-5.3,
                  125-REQ-5.4
    """

    model_config = ConfigDict(extra="ignore")

    issue_check_interval: int = Field(
        default=900,
        description="Seconds between issue checks (minimum 60)",
    )

    push_fix_branch: bool = Field(
        default=False,
        description="Push fix branches to origin before harvest",
    )

    @field_validator("issue_check_interval")
    @classmethod
    def clamp_interval_minimum(cls, v: int, info: object) -> int:
        """Clamp intervals to a minimum of 60 seconds.

        Requirements: 61-REQ-9.E1
        """
        if v < 60:
            logger.warning(
                "Config field '%s' value %d below minimum, clamped to 60",
                getattr(info, "field_name", "interval"),
                v,
            )
            return 60
        return v


class PathsConfig(BaseModel):
    """Path configuration for project directories.

    Allows overriding the spec root directory location.
    The default was changed from ``.specs`` to ``.agent-fox/specs``
    to consolidate project artifacts under ``.agent-fox/``.

    Requirements: 371-REQ-1.1
    """

    model_config = ConfigDict(extra="ignore")

    spec_root: str = Field(
        default=".agent-fox/specs",
        description="Spec root directory relative to project root",
    )


class WorkspaceConfig(BaseModel):
    """Workspace health and cleanup configuration.

    Requirements: 118-REQ-2.2
    """

    model_config = ConfigDict(extra="ignore")

    force_clean: bool = Field(
        default=False,
        description=(
            "Automatically remove untracked files and reset dirty index before session dispatch instead of aborting."
        ),
    )
    integration_branch: str = Field(
        default="main",
        description="Git branch used as the integration target for all merges.",
    )


class SpecToolConfig(BaseModel):
    """Configuration for the agentspec tool.

    Holds model and authentication settings previously stored in
    ``~/.af/settings.yaml``.

    Requirements: 13-REQ-6.1
    """

    model_config = ConfigDict(extra="ignore")

    model: str = Field(
        default="claude-sonnet-4-6",
        description="Anthropic model used for spec generation",
    )
    auth_method: str = Field(
        default="",
        description="Authentication method (empty string = default API key)",
    )
    vertex_project: str = Field(
        default="",
        description="Google Cloud Vertex AI project ID",
    )
    vertex_region: str = Field(
        default="",
        description="Google Cloud Vertex AI region",
    )


class AgentFoxConfig(BaseModel):
    model_config = ConfigDict(extra="ignore")

    paths: PathsConfig = Field(default_factory=PathsConfig)
    workspace: WorkspaceConfig = Field(default_factory=WorkspaceConfig)
    orchestrator: OrchestratorConfig = Field(default_factory=OrchestratorConfig)
    routing: RoutingConfig = Field(default_factory=RoutingConfig)
    security: SecurityConfig = Field(default_factory=SecurityConfig)
    theme: ThemeConfig = Field(default_factory=ThemeConfig)
    platform: PlatformConfig = Field(default_factory=PlatformConfig)
    knowledge: KnowledgeConfig = Field(default_factory=KnowledgeConfig)
    archetypes: ArchetypesConfig = Field(default_factory=ArchetypesConfig)
    pricing: PricingConfig = Field(default_factory=PricingConfig)
    planning: PlanningConfig = Field(default_factory=PlanningConfig)
    caching: CachingConfig = Field(default_factory=CachingConfig)
    night_shift: NightShiftConfig = Field(default_factory=NightShiftConfig)
    spec_tool: SpecToolConfig = Field(default_factory=SpecToolConfig)

    # Private attribute to track whether [spec_tool] was explicitly present
    # in the raw merged config dict before Pydantic validation.  Set by
    # _load_config_global_local().  Used by agentspec to decide whether
    # to fall back to ~/.af/settings.yaml migration path.
    # Requirements: 13-REQ-6.3
    _spec_tool_explicit: bool = PrivateAttr(default=False)


def shallow_merge(global_dict: dict, local_dict: dict) -> dict:
    """Merge two config dicts using shallow section replacement.

    For each key in *local_dict*:
    - If the value is a ``dict`` (i.e. a TOML section), the entire
      corresponding section in *global_dict* is replaced wholesale.
    - If the value is a scalar, it overrides the matching global key.

    Keys present in *global_dict* but absent from *local_dict* are
    inherited unchanged.  Nested keys within a section are **never**
    deep-merged — a section present in the local config replaces
    the entire global section including all nested keys.

    Requirements: 13-REQ-3.1, 13-REQ-3.3
    """
    merged: dict = {}
    # Start with a shallow copy of all global keys
    for key, value in global_dict.items():
        if isinstance(value, dict):
            merged[key] = dict(value)  # copy so mutations don't leak
        else:
            merged[key] = value

    # Apply local overrides
    for key, value in local_dict.items():
        if isinstance(value, dict):
            # Section replacement: wholesale, not deep-merged
            merged[key] = dict(value)
        else:
            # Scalar override
            merged[key] = value

    return merged


def _check_symlink(path: Path) -> None:
    """Reject a config file path that is a symlink (CWE-59).

    Only the final file inode is checked — intermediate directories
    in the path are never checked for symlink status.

    Requirements: 13-REQ-2.E1, 13-REQ-3.E1, 13-REQ-3.E2
    """
    if path.is_symlink():
        raise ConfigError(
            f"Config file {path} is a symlink (CWE-59 violation). "
            "For security, config files must be regular files, not symlinks.",
            path=str(path),
        )


def _parse_toml_file(path: Path) -> dict:
    """Read and parse a TOML file, raising ConfigError on failure.

    Requirements: 13-REQ-4.1, 13-REQ-4.2, 13-REQ-4.3
    """
    raw = path.read_text(encoding="utf-8")
    try:
        return tomllib.loads(raw)
    except tomllib.TOMLDecodeError as exc:
        raise ConfigError(
            f"Failed to parse config file {path}: {exc}",
            path=str(path),
        ) from exc


def _validate_config_dict(data: dict, source: str = "<unknown>") -> AgentFoxConfig:
    """Validate a config dict through the AgentFoxConfig Pydantic model.

    Requirements: 13-REQ-1.3, 01-REQ-2.2
    """
    try:
        return AgentFoxConfig(**data)
    except ValidationError as exc:
        field_errors = []
        for err in exc.errors():
            loc = " → ".join(str(part) for part in err["loc"])
            msg = err["msg"]
            field_errors.append(f"  {loc}: {msg}")
        error_detail = "\n".join(field_errors)
        raise ConfigError(
            f"Invalid configuration in {source}:\n{error_detail}",
            path=source,
            details=exc.errors(),
        ) from exc


def load_config(path: Path | None = None) -> AgentFoxConfig:
    """Load config from TOML, validate, and merge with defaults.

    When called **without arguments** (``path is None``), resolves the
    global config from ``$HOME/.agent-fox/config.toml`` and the local
    config from ``.agent-fox/config.toml`` relative to the current
    working directory, merges them using shallow section replacement,
    validates through ``AgentFoxConfig``, and returns the result.

    When called **with a path**, loads and validates only that single
    file (backward compatibility with pre-spec-13 callers).

    Args:
        path: Path to a TOML configuration file.  If ``None``, use the
              global+local loading scheme.

    Returns:
        A fully populated AgentFoxConfig with defaults for missing fields.

    Raises:
        ConfigError: If a config file contains invalid TOML, fields with
                     wrong types, or the final config file path is a
                     symlink.

    Requirements: 13-REQ-1.1, 13-REQ-1.2, 13-REQ-1.3, 13-REQ-2.1,
                  13-REQ-2.2, 13-REQ-2.3, 13-REQ-2.E1, 13-REQ-2.E2,
                  13-REQ-3.1, 13-REQ-3.2, 13-REQ-3.3, 13-REQ-3.E1,
                  13-REQ-3.E2, 13-REQ-4.1, 13-REQ-4.2, 13-REQ-4.3,
                  13-REQ-5.1, 13-REQ-5.2, 13-REQ-7.1, 13-REQ-7.2,
                  13-REQ-7.3, 13-REQ-7.4
    """
    if path is not None:
        return _load_config_single_file(path)

    return _load_config_global_local()


def _load_config_single_file(path: Path) -> AgentFoxConfig:
    """Load config from a single explicit file path (backward compat).

    Preserves the pre-spec-13 behavior: missing file returns defaults,
    symlinked file raises ConfigError, invalid TOML raises ConfigError.

    Requirements: 01-REQ-2.E1, 01-REQ-2.E2
    """
    # 01-REQ-2.E1: missing file returns defaults without error
    if not path.exists():
        return AgentFoxConfig()

    # 13-REQ-2.E1 / 13-REQ-3.E1: symlink rejection
    _check_symlink(path)

    data = _parse_toml_file(path)

    # 01-REQ-2.6: log warning for unknown top-level keys
    known_sections = set(AgentFoxConfig.model_fields.keys())
    for key in data:
        if key not in known_sections:
            logger.warning("Ignoring unknown config section: '%s'", key)

    return _validate_config_dict(data, source=str(path))


def _load_config_global_local() -> AgentFoxConfig:
    """Load config using the global+local merge scheme.

    1. Check for AF_CONFIG deprecation.
    2. Resolve $HOME and load/auto-create the global config.
    3. Load the local config from CWD/.agent-fox/config.toml.
    4. Merge using shallow section replacement.
    5. Validate through AgentFoxConfig.

    Requirements: 13-REQ-1.2, 13-REQ-2.1, 13-REQ-2.2, 13-REQ-2.3,
                  13-REQ-3.1, 13-REQ-3.2, 13-REQ-5.1, 13-REQ-5.2,
                  13-REQ-7.1, 13-REQ-7.2, 13-REQ-7.3, 13-REQ-7.4
    """
    # --- Global config ---
    global_dict: dict = {}
    home: Path | None = None

    try:
        home = Path.home()
    except (RuntimeError, OSError):
        # 13-REQ-2.3, 13-REQ-7.4: HOME unresolvable
        logger.debug("$HOME could not be resolved; global config loading skipped")

    if home is not None:
        global_config_path = home / ".agent-fox" / "config.toml"
        global_dir = home / ".agent-fox"

        try:
            config_exists = global_config_path.exists()
        except OSError as exc:
            # 13-REQ-2.E2: can't even stat the path — directory is
            # inaccessible, so we can't create or read the global config.
            raise ConfigError(
                f"Failed to create directory {global_dir}: {exc}",
                path=str(global_dir),
            ) from exc

        if not config_exists:
            # 13-REQ-2.1: auto-create global config
            try:
                os.makedirs(str(global_dir), mode=0o700, exist_ok=True)
            except OSError as exc:
                # 13-REQ-2.E2: directory creation failure
                raise ConfigError(
                    f"Failed to create directory {global_dir}: {exc}",
                    path=str(global_dir),
                ) from exc

            from agentfox.core.config_gen import generate_default_config

            global_config_path.write_text(generate_default_config(), encoding="utf-8")

        # 13-REQ-2.E1: symlink rejection on final file
        _check_symlink(global_config_path)

        # 13-REQ-4.1: malformed global TOML -> fail fast
        global_dict = _parse_toml_file(global_config_path)

        # 13-REQ-7.1: debug log after successful load
        logger.debug("Loaded global config from %s", global_config_path)

    # --- Local config ---
    local_path = Path.cwd() / ".agent-fox" / "config.toml"

    if local_path.exists():
        # 13-REQ-3.E1: symlink rejection on final file
        _check_symlink(local_path)

        # 13-REQ-4.2: malformed local TOML -> ConfigError
        local_dict = _parse_toml_file(local_path)

        # Track which local keys are sections (dicts) for debug logging
        overridden_sections = [k for k, v in local_dict.items() if isinstance(v, dict)]

        # 13-REQ-3.1, 13-REQ-3.3: shallow section replacement merge
        merged_dict = shallow_merge(global_dict, local_dict)

        # 13-REQ-7.2: debug log with overridden section names
        logger.debug(
            "Merging local config from %s (sections overridden: %s)",
            local_path,
            overridden_sections,
        )
    else:
        merged_dict = global_dict
        # 13-REQ-7.3: debug log when no local config found
        logger.debug(
            "No local config found at %s",
            local_path,
        )

    # 13-REQ-1.3: validate and apply defaults via Pydantic
    config = _validate_config_dict(merged_dict, source="merged config")

    # 13-REQ-6.3: track whether [spec_tool] was explicitly present in the
    # raw merged dict (before Pydantic filled in defaults).  Used by
    # agentspec to decide whether to fall back to ~/.af/settings.yaml.
    if "spec_tool" in merged_dict:
        config._spec_tool_explicit = True

    return config


def resolve_spec_root(config: AgentFoxConfig, project_root: Path) -> Path:
    """Resolve the spec root directory from config.

    Args:
        config: Loaded AgentFoxConfig.
        project_root: Project root directory.

    Returns:
        Resolved Path to the spec root directory.
    """
    return project_root / config.paths.spec_root
