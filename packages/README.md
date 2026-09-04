# Packages

This monorepo contains the following packages, managed as a
[uv workspace](https://docs.astral.sh/uv/concepts/workspaces/).

| Package | Description | Install |
|---------|-------------|---------|
| **[af](af/)** | CLI for the agent-fox orchestrator. Provides the `af` command. | `uv pip install -e packages/af` |
| **[agentfox](agentfox/)** | Core library — spec engine, graph planner, session runtime, and workspace tools. | `uv pip install -e packages/agentfox` |

`afissues` and `afaudit` are **not** part of this workspace. They live in the
separate [agent-fox-dev/af-python](https://github.com/agent-fox-dev/af-python)
repository and are sourced from there via `uv`.

## Dependency graph

```
af  ──▶  agentfox  ──▶  afspec (external: spec-format)
 │            │
 │            ├──▶  afissues  (external: af-python)
 │            └──▶  afaudit   (external: af-python)
 └──────────▶  afaudit        (external: af-python)
```

`afissues` and `afaudit` have no internal dependencies and can be used independently.

## Development

From the repo root:

```bash
uv sync          # install all packages in editable mode
make check       # lint + test everything
```
