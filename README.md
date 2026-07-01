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

### Night Shift — Fix-Only Daemon

Keep your codebase healthy while you sleep. Night Shift is a continuously-running
fix-only daemon that polls for `af:fix`-labelled GitHub issues and autonomously
processes them through a three-stage pipeline (triage → coder → reviewer).

```bash
# Start the fix daemon (Ctrl-C to stop gracefully)
nightshift
```

## Installation

```bash
uv tool install af --from git+https://github.com/agent-fox-dev/agent-fox.git#subdirectory=packages/af
```

```bash
curl -fsSL https://raw.githubusercontent.com/agent-fox-dev/agent-fox/refs/heads/main/install.sh | sh
```

## Development

The repository is a [uv workspace](https://docs.astral.sh/uv/concepts/workspaces/)
with five packages:

| Package | Description |
|---------|-------------|
| `packages/af/` | CLI for the agent-fox orchestrator (`af` command) |
| `packages/nightshift/` | Standalone CLI for the night-shift fix daemon (`nightshift` command) |
| `packages/agentfox/` | Core library — spec engine, graph planner, session runtime, workspace tools |
| `packages/afspec/` | Standalone library for the agent-fox specification format (v1.2) |
| `packages/agentspec/` | AI-powered spec creation library |
| `packages/spec/` | CLI for AI-powered spec creation (`spec` command) |

```
af  ──▶  agentfox  ──▶  afspec
              ▲
spec ──▶ agentspec ──┘──▶  afspec

nightshift ──▶ agentfox
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
  knowledge system uses a similar approach: typed facts with causal links,
  embedding-based retrieval, and lifecycle management (deduplication,
  contradiction detection, decay).
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