# agentfox

Core library for the [agent-fox](https://github.com/agent-fox-dev/agent-fox)
autonomous coding-agent orchestrator. Provides the deterministic execution
engine, session runtime, configuration system, workspace management,
knowledge store, and platform integrations.

Requires Python 3.12+.

## Installation

Install from the agent-fox monorepo via git:

```bash
pip install "agentfox @ git+https://github.com/agent-fox-dev/agent-fox.git#subdirectory=packages/agentfox"
```

Pin to a release tag:

```bash
pip install "agentfox @ git+https://github.com/agent-fox-dev/agent-fox.git@v4.2.0#subdirectory=packages/agentfox"
```

In `pyproject.toml`:

```toml
[project]
dependencies = [
    "agentfox @ git+https://github.com/agent-fox-dev/agent-fox.git@v4.2.0#subdirectory=packages/agentfox",
]
```

Dependencies include `afspec`, `afaudit`, `anthropic`, `claude-agent-sdk`,
`duckdb`, `pydantic`, `rich`, `click`, and others -- see `pyproject.toml` for
the full list.

## Quick Start

```python
import asyncio
from agentfox.core.config import load_config
from agentfox.engine.run import run_code

# Load configuration (merges global + local .agent-fox/config.toml)
config = load_config()

# Run the orchestrator
state = asyncio.run(run_code(config, max_cost=50.0))
print(f"Status: {state.run_status}")
print(f"Cost: ${state.total_cost:.2f}")
print(f"Sessions: {state.total_sessions}")
```

## API Reference

The package does not re-export from the top level. Import from submodules
directly: `from agentfox.core.config import load_config`.

### Configuration (`agentfox.core.config`)

| Symbol | Description |
|--------|-------------|
| `load_config(path=None)` | Load and merge global + local TOML config into `AgentFoxConfig`. Single entry point for all CLIs. |
| `resolve_spec_root(config, project_root)` | Resolve the spec directory path from config and project root. |
| `AgentFoxConfig` | Root pydantic model. Contains all sub-configs below. |

Sub-config models (all pydantic `BaseModel` subclasses with documented defaults):

| Model | Key Fields |
|-------|------------|
| `OrchestratorConfig` | `parallel`, `sync_interval`, `max_retries`, `max_cost`, `max_sessions`, `max_blocked_fraction`, `inter_session_delay`, `hot_load`, `watch_interval`, `max_budget_usd` |
| `RoutingConfig` | `max_timeout_retries`, `timeout_multiplier`, `timeout_ceiling_factor` |
| `SecurityConfig` | `bash_allowlist` (list[str] \| None), `bash_allowlist_extend` |
| `WorkspaceConfig` | `force_clean`, `integration_branch` |
| `PathsConfig` | `spec_root` |
| `KnowledgeConfig` | `store_path`, `provider: KnowledgeProviderConfig` |
| `PricingConfig` | Model-keyed `ModelPricing` entries (`input_price_per_m`, `output_price_per_m`, `cache_read_price_per_m`, `cache_creation_price_per_m`) |
| `CachingConfig` | `policy: CachePolicy` (NONE / DEFAULT / EXTENDED) |
| `PerArchetypeConfig` | `model_tier`, `max_turns`, `thinking_mode` (adaptive / disabled), `effort`, `allowlist`, `max_budget_usd`, `compaction` |
| `ArchetypesConfig` | `reviewer_config: ReviewerConfig`, per-archetype enable/disable, custom archetypes |
| `ReviewerConfig` | `pre_flight_block_threshold`, `pre_flight_drift_block_threshold`, `audit_min_ts_entries`, `audit_max_retries` |
| `PlatformConfig` | `type` (none \| github \| gitlab \| gitea), `url` |
| `NightShiftConfig` | `issue_check_interval`, `pr_check_interval`, `push_fix_branch`, `max_parallel`, `max_pr_retries` |

### Engine (`agentfox.engine`)

| Symbol | Module | Description |
|--------|--------|-------------|
| `run_code` | `engine.run` | `async (config, *, max_cost, max_sessions, watch, ...) -> ExecutionState \| InterruptedResult` -- primary programmatic entry point. Configures infrastructure and runs the orchestrator. |
| `Orchestrator` | `engine.engine` | Deterministic execution engine. Loads task graph, dispatches sessions in dependency order, manages retries, cascade-blocks failures. `async run() -> ExecutionState`. |
| `ExecutionState` | `engine.state` | Run outcome. Fields: `run_status`, `node_states: dict[str, str]`, `session_history`, `total_cost`, `total_input_tokens`, `total_output_tokens`, `total_sessions`, `blocked_reasons`. |
| `RunStatus` | `engine.state` | StrEnum: `RUNNING`, `COMPLETED`, `COMPLETED_DIRTY`, `INTERRUPTED`, `COST_LIMIT`, `SESSION_LIMIT`, `STALLED`, `BLOCK_LIMIT`. |
| `SessionRecord` | `engine.state` | Per-session outcome: `node_id`, `attempt`, `status`, `archetype`, `model`, `duration_ms`, `cost`, `error_message`, token counts. |
| `InterruptedResult` | `engine.run` | Lightweight result for KeyboardInterrupt. |

### Models (`agentfox.core.models`)

| Symbol | Description |
|--------|-------------|
| `ModelTier` | Enum: `SIMPLE`, `STANDARD`, `ADVANCED`. |
| `ModelEntry` | Dataclass: `model_id`, `tier`. |
| `MODEL_REGISTRY` | `dict[str, ModelEntry]` -- all known model IDs. |
| `resolve_model` | `(name_or_tier, *, models_config=None) -> str` -- resolve a tier name or model ID to a concrete model ID, honouring `[models.registry]` / `[models.tier_defaults]` when a config is passed. |
| `calculate_cost` | `(input_tokens, output_tokens, model_id, pricing, *, cache_read_input_tokens=0, cache_creation_input_tokens=0) -> float` -- USD cost. |

### Archetypes (`agentfox.archetypes`)

| Symbol | Description |
|--------|-------------|
| `ArchetypeEntry` | Dataclass -- full archetype config: `name`, `default_model_tier`, `injection`, `task_assignable`, `retry_predecessor`, `default_allowlist`, `default_max_turns`, `default_thinking_mode`, `default_effort`, `default_compaction`, `modes: dict[str, ModeConfig]`. |
| `ModeConfig` | Dataclass -- per-mode overrides: `model_tier`, `injection`, `allowlist`, `retry_predecessor`, `max_turns`, `thinking_mode`. |
| `ARCHETYPE_REGISTRY` | `dict[str, ArchetypeEntry]` -- built-in archetypes: `coder`, `reviewer`, `verifier`, `gate`, `maintainer`. |
| `get_archetype` | `(name, project_dir=None, config=None) -> ArchetypeEntry` -- look up by name with custom archetype fallback. |
| `resolve_effective_config` | `(entry, mode) -> ArchetypeEntry` -- merge mode overrides onto base entry. |

### Session (`agentfox.session`)

| Symbol | Module | Description |
|--------|--------|-------------|
| `run_session` | `session.session` | `async (workspace, node_id, system_prompt, task_prompt, config, ...) -> SessionOutcome` -- execute a single coding session via `ClaudeBackend`. |
| `build_system_prompt` | `session.prompt` | `(context, task_group, spec_name, archetype, mode, project_dir) -> str` -- 3-layer system prompt assembly (agent + role + task context). |
| `build_task_prompt` | `session.prompt` | Task prompt construction from spec artifacts and injected findings. |
| `assemble_context` | `session.context` | Gather spec documents, review findings, and steering directives into a structured context object. |

### Knowledge (`agentfox.knowledge`)

| Symbol | Module | Description |
|--------|--------|-------------|
| `KnowledgeProvider` | `knowledge.fox_provider` | Protocol with `ingest(session_id, spec_name, context: dict) -> None` and `retrieve(spec_name, task_description, task_group=None, session_id=None, file_footprint=None, archetype=None) -> list[str]`. |
| `NoOpKnowledgeProvider` | `knowledge` | Default no-op implementation. |
| `FoxKnowledgeProvider` | `knowledge.fox_provider` | Concrete implementation: review finding carry-forward, session summaries, drift findings. |
| `KnowledgeDB` | `knowledge.db` | DuckDB connection manager for the knowledge store. |

### Task Graph (`agentfox.graph.types`)

| Symbol | Description |
|--------|-------------|
| `TaskGraph` | Dataclass: `nodes: dict[str, Node]`, `edges: list[Edge]`, `order: list[str]`, `metadata`. Methods: `predecessors(node_id)`, `successors(node_id)`. |
| `Node` | Dataclass: `id`, `spec_name`, `group_number`, `title`, `optional`, `status`, `archetype`, `mode`, `instances`. |
| `Edge` | Dataclass: `source`, `target`, `kind`. |
| `NodeStatus` | Enum: `PENDING`, `IN_PROGRESS`, `COMPLETED`, `FAILED`, `BLOCKED`, `SKIPPED`, `COST_BLOCKED`, `MERGE_BLOCKED`, `DEFERRED`. |

### Workspace (`agentfox.workspace`)

| Symbol | Module | Description |
|--------|--------|-------------|
| `create_worktree` | `workspace.worktree` | `async (repo_root, spec_name, task_group, base_branch, branch_name=None, role=None, mode=None) -> WorkspaceInfo` -- create an isolated git worktree for a coding session. |
| `destroy_worktree` | `workspace.worktree` | `async (repo_root, workspace, *, preserve_branch=False) -> None` -- remove worktree and delete feature branch. |
| `WorkspaceInfo` | `workspace.worktree` | Dataclass: `path`, `branch`, `spec_name`, `task_group`, `role`, `mode`. |
| `run_git` | `workspace.git` | `async (args: list[str], cwd: Path, check=True, timeout=None) -> tuple[int, str, str]` -- run a git command and return (returncode, stdout, stderr). |
| `ensure_integration_branch` | `workspace.integration` | Set up the integration branch for merging. |
| `push_to_remote` | `workspace.git` | `async (repo_root, branch, remote='origin', *, force=False) -> bool` -- push a branch to origin. |

### Platform (via `afissues`)

The platform/forge abstraction layer has been extracted to the standalone
[`afissues`](../afissues/) package. Import from `afissues` directly:

| Symbol | Module | Description |
|--------|--------|-------------|
| `PlatformProtocol` | `afissues.protocol` | Protocol for issue/PR management: `create_issue`, `list_issues_by_label`, `add_issue_comment`, `assign_label`, `close_issue`, `create_pull_request`, etc. |
| `IssueResult` | `afissues.protocol` | Dataclass: `number`, `title`, `body`, `labels`, `html_url`. |
| `GitHubPlatform` | `afissues.github` | GitHub implementation of `PlatformProtocol` using `httpx.AsyncClient`. |

### Security (`agentfox.core.security`)

| Symbol | Description |
|--------|-------------|
| `DEFAULT_ALLOWLIST` | `frozenset[str]` -- ~46 default-allowed shell commands (ls, cat, git, make, pytest, etc.). |
| `make_pre_tool_use_hook` | `(security_config) -> Callable` -- build a permission callback for the session runtime. |

### Errors (`agentfox.core.errors`)

| Exception | Description |
|-----------|-------------|
| `AgentFoxError` | Base exception with `context: dict` for structured error metadata. |
| `ConfigError` | Configuration loading or validation failure. |
| `PlanError` | Task graph construction failure. |
| `WorkspaceError` | Git/worktree operation failure. |
| `IntegrationError` | Merge/push failure. Has `retryable: bool` flag. |
| `SecurityError` | Blocked command or permission violation. |
| `KnowledgeStoreError` | DuckDB or knowledge provider failure. |
