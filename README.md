## Before agent-fox

You write a spec, then sit in front of your terminal babysitting an AI agent
for hours. You paste context, fix merge conflicts, restart after crashes, and
lose track of what's done. 

By session 10 you're exhausted and the agent has forgotten everything from session 1.

## With agent-fox

You write the same spec, run `af code`, and go do something else.

The fox reads your specs, plans the work, spins up isolated worktrees, runs each
session with the right context, handles merge conflicts, retries failures,
extracts learnings into structured memory, and merges commits to
`develop`. 

You come back to a finished feature branch and a standup report.

### Quick Start

```bash
# Initialize your project (use --skills to install Claude Code skills)
af init --skills
```

Use the `/afspec` skill in Claude Code to generate a specification
from a PRD, a GitHub issue or a plain-english description:

```
/afspec [path-to-prd-or-prompt-or-github-issue-url]
```

```bash
# Create the task graph from your specs
af plan

# Run autonomous coding sessions
af code 

# Check results
af standup
```

See the [CLI reference](docs/cli-reference.md) for all command options.

## Installation

Install the CLI:

```bash
curl -fsSL https://raw.githubusercontent.com/agent-fox-dev/agent-fox/refs/heads/main/install.sh | sh
```

## Development

The repository is a [uv workspace](https://docs.astral.sh/uv/concepts/workspaces/):

| Package | Description |
|---------|-------------|
| `packages/af/` | CLI for the agent-fox orchestrator (`af` command) |
| `packages/agentfox/` | Core library — spec engine, graph planner, session runtime, workspace tools |

`afissues` and `afaudit` are not part of this workspace — they live in the
separate [agent-fox-dev/af-python](https://github.com/agent-fox-dev/af-python)
repository and are sourced from there via `uv`.

The specification format library (`afspec`) and AI-powered spec creation tools
(`agentspec`, `spec` CLI) live in the separate
[agent-fox-dev/spec-format](https://github.com/agent-fox-dev/spec-format) repository.

```
af  ──▶  agentfox  ──▶  afspec (external: spec-format)
 │            │
 │            ├──▶  afissues
 │            └──▶  afaudit
 └──────────▶  afaudit
```

```bash
uv sync                      # install all packages in editable mode
```

| Command | What it does |
|---------|-------------|
| `make check` | Lint + all tests (use before committing) |
| `make test` | All tests |
| `make test-unit` | Unit tests only |
| `make test-property` | Property-based tests only |
| `make test-integration` | Integration tests only |
| `make lint` | Check lint + formatting |
| `make format` | Auto-format code |

Changes are immediately reflected via editable install. To run the local
version explicitly (rather than a globally installed release):

```bash
uv run af <command>
```

## Using packages as standalone libraries

`agentfox`, `afissues`, and `afaudit` are designed for reuse outside the CLI tools.
Install any package directly from git:

```bash
pip install "agentfox @ git+https://github.com/agent-fox-dev/agent-fox.git@v4.7.0#subdirectory=packages/agentfox"
pip install "afissues @ git+https://github.com/agent-fox-dev/af-python.git#subdirectory=packages/afissues"
pip install "afaudit @ git+https://github.com/agent-fox-dev/af-python.git#subdirectory=packages/afaudit"
```

For `afspec` (spec format library), install from the
[spec-format](https://github.com/agent-fox-dev/spec-format) repository:

```bash
pip install "afspec @ git+https://github.com/agent-fox-dev/spec-format.git#subdirectory=packages/afspec"
```

- **agentfox** — core orchestrator library: execution engine, session runtime,
  configuration, workspace management, knowledge store, Anthropic client
  helpers. See [`packages/agentfox/README.md`](packages/agentfox/README.md)
  for the full API reference.
- **afissues** — lightweight platform/forge abstraction layer: `PlatformProtocol`,
  `GitHubPlatform`, label constants, SSRF guards. Only depends on `httpx`. See
  [`packages/afissues/`](packages/afissues/) for the package.
- **afaudit** — structured audit events, sink protocol, postmortem generation,
  trace reconstruction. Zero dependencies. See
  [`packages/afaudit/README.md`](packages/afaudit/README.md) for the full API
  reference.

## Documentation

Full documentation lives in [`docs/`](docs/README.md):

- [CLI Reference](docs/cli-reference.md) — all commands, flags, and exit codes
- [Configuration Reference](docs/config-reference.md) — every `config.toml` option (all sections and fields)
- [Agent Archetypes](docs/architecture/03-execution-and-archetypes.md#agent-archetypes) — archetype registry, modes, convergence
- [Skills](docs/skills.md) — bundled Claude Code slash commands (`/afspec`)

For a deeper understanding of the system's internals — how specs become task
graphs, how agents are dispatched in parallel, how the knowledge store works,
and how nightshift processes fix issues — see the
[Architecture Guide](docs/architecture/README.md).

## References

agent-fox draws on ideas from the following research:

- **MAGMA** — A multi-graph memory architecture for AI agents. agent-fox's
  knowledge system draws on MAGMA's concept of typed, structured facts. It
  uses SQL-based retrieval with relevance scoring, supersession-based
  deduplication, and a closed-loop finding lifecycle across sessions.
  [arXiv:2601.03236](https://arxiv.org/abs/2601.03236)

- **Sleep-time Compute** — Explores how pre-computation outside of inference
  time can improve agent performance. Night-shift's autonomous maintenance
  model applies this principle: the system does useful work while the
  developer is away, so the codebase is healthier when they return.
  [arXiv:2504.13171](https://arxiv.org/html/2504.13171v1)

- **Memory in the Age of AI Agents: A Survey** — A comprehensive survey of
  memory architectures for AI agents. Provides context for agent-fox's
  design choices around fact extraction, supersession, and retrieval.
  [GitHub](https://github.com/Shichun-Liu/Agent-Memory-Paper-List)

---
Built exclusively for Claude Code. And mostly by agent-fox.