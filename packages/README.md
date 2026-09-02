# Packages

This monorepo contains the following packages, managed as a
[uv workspace](https://docs.astral.sh/uv/concepts/workspaces/).

| Package | Description | Install |
|---------|-------------|---------|
| **[af](af/)** | CLI for the agent-fox orchestrator. Provides the `af` command. | `uv pip install -e packages/af` |
| **[agentfox](agentfox/)** | Core library — spec engine, graph planner, session runtime, and workspace tools. | `uv pip install -e packages/agentfox` |
| **[afissues](afissues/)** | Standalone platform/forge abstraction layer — protocol, GitHub integration, label definitions. | `uv pip install -e packages/afissues` |
| **[afaudit](afaudit/)** | Zero-dependency audit infrastructure — structured events, sinks, postmortem, traces, cleanup. | `uv pip install -e packages/afaudit` |

## Dependency graph

```
af  ──▶  agentfox  ──▶  afspec (external: spec-format)
 │            │
 │            ├──▶  afissues
 │            └──▶  afaudit
 └──────────▶  afaudit
```

`afissues` and `afaudit` have no internal dependencies and can be used independently.

## Development

From the repo root:

```bash
uv sync          # install all packages in editable mode
make check       # lint + test everything
```
