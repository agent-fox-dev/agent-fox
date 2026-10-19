# agent-fox

agent-fox is an autonomous spec-first coding agent (golang version).

This repository is being rebuilt from a clean state. The previous
implementation (the `triage`, `fix` and `impl` tools and the packages under
them) was removed; `1e6458d` is the last commit that has it.

It depends on

- [`agentkit-go`](https://github.com/agent-fox-dev/agentkit-go) — the AgentKit SDK
  (module `github.com/agent-fox-dev/agentkit-go`)

and works with [`hub`](https://github.com/agent-fox-dev/hub) as its backend
service. The spec format and the `spec` tool that writes specification
packages live in the [`spec`](https://github.com/agent-fox-dev/spec)
repository.

## What is here

| Path | Purpose |
|---|---|
| `issuex/` | The forge client: one interface over GitHub and GitLab for issues, labels, and pull or merge requests. |
| `internal/gitx/` | The git and shell invocations the tools make, hermetic and issued from Go. |
| `skills/` | The coding-agent skills (`af-prd`, `af-impl`, `af-fix`, `af-bug-hunter`, `af-code-quality`, `af-code-simplifier`). `make install-skills` copies them to `~/.claude/skills`. |
| `containers/sandbox/` | The sandbox image. |
| `.specs/` | `steering.md` holds the directives every session follows; `archive/` holds the specs of `issuex`. |
| `docs/` | `architecture.md` is the proposed target architecture, `prd/` holds the product requirements, `drafts/` holds design drafts, `errata/` holds the errata of `issuex`. |

[AGENTS.md](AGENTS.md) is the policy for every coding session in this
repository.

## Building

AgentKit is consumed through a `replace` to a sibling checkout:

```bash
git clone https://github.com/agent-fox-dev/agentkit-go ../agentkit-go
```

```bash
make check          # gofmt + go vet + all tests
make build          # go build ./...
```

The test suite needs no API key, no token and no network. It does start
`httptest` servers on the loopback interface, so a sandbox that forbids
binding a local port fails it.
