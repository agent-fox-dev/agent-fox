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
pip install "agentfox @ git+https://github.com/agent-fox-dev/agent-fox.git@v4.1.15#subdirectory=packages/agentfox"
```

In `pyproject.toml`:

```toml
[project]
dependencies = [
    "agentfox @ git+https://github.com/agent-fox-dev/agent-fox.git@v4.1.15#subdirectory=packages/agentfox",
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
| `shallow_merge(global_dict, local_dict)` | Merge two config dicts with section-level replacement semantics. |
| `AgentFoxConfig` | Root pydantic model. Contains all sub-configs below. |

Sub-config models (all pydantic `BaseModel` subclasses with documented defaults):

| Model | Key Fields |
|-------|------------|
| `OrchestratorConfig` | `parallel`, `sync_interval`, `max_retries`, `max_cost`, `max_sessions`, `max_blocked_fraction`, `inter_session_delay`, `hot_load`, `watch_interval`, `budget` |
| `RoutingConfig` | `max_timeout_retries`, `timeout_multiplier`, `timeout_ceiling_factor` |
| `SecurityConfig` | `bash_allowlist` (frozenset of allowed commands) |
| `WorkspaceConfig` | `force_clean`, `integration_branch` |
| `PathsConfig` | `spec_root` |
| `KnowledgeConfig` | `db_path`, `retrieval_caps` |
| `PricingConfig` | Model-keyed `ModelPricing` entries (`input_per_mtok`, `output_per_mtok`, `cache_read_per_mtok`) |
| `CachingConfig` | `policy: CachePolicy` (NONE / DEFAULT / EXTENDED) |
| `PerArchetypeConfig` | `thinking_mode` (adaptive / disabled), resolved per archetype |
| `ArchetypesConfig` | `reviewer_config: ReviewerConfig`, per-archetype enable/disable, custom archetypes |
| `ReviewerConfig` | `pre_review_block_threshold`, `drift_review_block_threshold`, `audit_min_ts_entries`, `audit_max_retries` |
| `PlatformConfig` | `type` (github), `url` |
| `NightShiftConfig` | `check_interval`, `push` settings |

### Engine (`agentfox.engine`)

| Symbol | Module | Description |
|--------|--------|-------------|
| `run_code` | `engine.run` | `async (config, *, max_cost, max_sessions, watch, ...) -> ExecutionState \| InterruptedResult` -- primary programmatic entry point. Configures infrastructure and runs the orchestrator. |
| `Orchestrator` | `engine.engine` | Deterministic execution engine. Loads task graph, dispatches sessions in dependency order, manages retries, cascade-blocks failures. `async run() -> ExecutionState`. |
| `ExecutionState` | `engine.state` | Run outcome. Fields: `run_status`, `node_states: dict[str, str]`, `session_history`, `total_cost`, `total_input_tokens`, `total_output_tokens`, `total_sessions`, `blocked_reasons`. |
| `RunStatus` | `engine.state` | StrEnum: `RUNNING`, `COMPLETED`, `COMPLETED_DIRTY`, `INTERRUPTED`, `COST_LIMIT`, `SESSION_LIMIT`, `STALLED`, `BLOCK_LIMIT`. |
| `SessionRecord` | `engine.state` | Per-session outcome: `node_id`, `attempt`, `status`, `archetype`, `model`, `duration_ms`, `cost`, `error_message`, token counts. |
| `InterruptedResult` | `engine.run` | Lightweight result for KeyboardInterrupt. |

### Anthropic Client (`agentfox.core.client`)

| Symbol | Description |
|--------|-------------|
| `create_anthropic_client()` | Sync Anthropic client. Auto-detects Vertex AI / Bedrock / direct API via env vars. |
| `create_async_anthropic_client()` | Async variant. |
| `ai_call` | `async (*, model_tier, max_tokens, messages, system, context, cache_policy) -> (text, response)` -- high-level: resolve model + create client + retry + track usage + extract text. |
| `ai_call_sync` | Synchronous variant of `ai_call`. |
| `cached_messages_create` | `async (client, *, model, max_tokens, messages, system, cache_policy) -> response` -- prompt-caching wrapper around `client.messages.create()`. |
| `cached_messages_create_sync` | Synchronous variant. |
| `retry_api_call_async` | `async (fn, *, context, max_retries=3) -> T` -- retry with exponential backoff on transient API errors. |
| `retry_api_call` | Synchronous variant. |
| `extract_response_text` | `(response) -> str \| None` -- extract text from first content block. |

### Models (`agentfox.core.models`)

| Symbol | Description |
|--------|-------------|
| `ModelTier` | Enum: `SIMPLE`, `STANDARD`, `ADVANCED`. |
| `ModelEntry` | Dataclass: `model_id`, `tier`, `variant`. |
| `MODEL_REGISTRY` | `dict[str, ModelEntry]` -- all known model IDs. |
| `resolve_model` | `(name_or_tier, variant=None) -> str` -- resolve a tier name or model alias to a concrete model ID. |
| `calculate_cost` | `(input_tokens, output_tokens, cache_read, cache_creation, model, pricing) -> float` -- USD cost. |

### Archetypes (`agentfox.archetypes`)

| Symbol | Description |
|--------|-------------|
| `ArchetypeEntry` | Dataclass -- full archetype config: `name`, `default_model_tier`, `default_model_variant`, `injection`, `task_assignable`, `retry_predecessor`, `default_allowlist`, `default_max_turns`, `thinking`, `modes: dict[str, ModeConfig]`. |
| `ModeConfig` | Dataclass -- per-mode overrides: `model_tier`, `model_variant`, `injection`, `allowlist`, `retry_predecessor`, `max_turns`, `thinking`. |
| `ARCHETYPE_REGISTRY` | `dict[str, ArchetypeEntry]` -- built-in archetypes: `coder`, `reviewer`, `curator`, `verifier`, `maintainer`. |
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
| `KnowledgeProvider` | `knowledge` | Protocol with `ingest(spec_name, session_id, response)` and `retrieve(spec_name, task_group) -> list[KnowledgeItem]`. |
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
| `create_worktree` | `workspace.worktree` | `(repo_root, branch_name, base_ref, worktree_dir) -> WorkspaceInfo` -- create an isolated git worktree for a coding session. |
| `destroy_worktree` | `workspace.worktree` | `(workspace) -> None` -- remove worktree and delete feature branch. |
| `WorkspaceInfo` | `workspace.worktree` | Dataclass: `path`, `branch`, `base_ref`. |
| `run_git` | `workspace.git` | `(*args, cwd) -> str` -- run a git command and return stdout. |
| `ensure_integration_branch` | `workspace.integration` | Set up the integration branch for merging. |
| `push_to_remote` | `workspace.git` | `(branch, cwd, remote="origin") -> None` -- push a branch to origin. |

### Platform (`agentfox.platform`)

| Symbol | Module | Description |
|--------|--------|-------------|
| `PlatformProtocol` | `platform.protocol` | Protocol for issue/PR management: `create_issue`, `list_issues_by_label`, `add_issue_comment`, `assign_label`, `close_issue`, `create_pull_request`, etc. |
| `IssueResult` | `platform.protocol` | Dataclass: `number`, `title`, `body`, `state`, `labels`, `url`. |
| `GitHubPlatform` | `platform.github` | GitHub implementation of `PlatformProtocol` using the `gh` CLI. |

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
