# Packages

This monorepo contains the following packages, managed as a
[uv workspace](https://docs.astral.sh/uv/concepts/workspaces/).

| Package | Description | Install |
|---------|-------------|---------|
| **[af](af/)** | CLI for the agent-fox orchestrator. Provides the `af` command. | `uv pip install -e packages/af` |
| **[agentfox](agentfox/)** | Core library — spec engine, graph planner, session runtime, and workspace tools. | `uv pip install -e packages/agentfox` |
| **[afspec](afspec/)** | Standalone library for the agent-fox specification format (v1). Loads, validates, renders, and mutates spec directories. | `uv pip install -e packages/afspec` |
| **[agentspec](agentspec/)** | AI-powered spec creation library. Drives PRD assessment, refinement, and artifact generation via Claude. | `uv pip install -e packages/agentspec` |

## Dependency graph

```
af  ──▶  agentfox  ──▶  afspec
              ▲
agentspec ────┘──────▶  afspec
```

`afspec` has no internal dependencies and can be used independently.

## Development

From the repo root:

```bash
uv sync          # install all packages in editable mode
make check       # lint + test everything
```
