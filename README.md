# agent-fox

agent-fox is an autonomous spec-first coding agent (golang version).

The new mono-repo for all agent-fox (golang) code. It provides library modules, CLI tools and services. The repo depends on repos

- [`coder`](https://github.com/agent-fox-dev/coder)
- [`spec`](https://github.com/agent-fox-dev/spec)
- [`apikit`](https://github.com/txsvc/apikit)

It works with [`hub`](https://github.com/agent-fox-dev/hub) as its backend service.

The repo is a typical `golang` project:

- cmd/ for executable commands, CLIs etc
- internal/ for any code that must not be included by anyone outside the repo
- <feature module>/ for any "features" that can be used "standalone" or conceptually belong to the same domain
- <repo root>/ the most important entry points into the repo.

## Modules

| Path | Package | Purpose |
|---|---|---|
| [`afspec/`](afspec/README.md) | `afspec` | Spec format library: load, validate, mutate, render and save specification packages. |
| `agentspec/` | `agentspec` | LLM-powered spec creation: session state machine, generation pipeline, Claude API integration. |
| `cmd/spec/` | `main` | The `spec` CLI. |
| `cmd/af/` | `main` | The `af` CLI. |
| `cmd/nightshift/` | `main` | The `nightshift` CLI. |

The spec format itself is specified in the
[`spec`](https://github.com/agent-fox-dev/spec) repository
(`specification/spec-format-v2.md`). The JSON Schemas the library compiles and
embeds live in `afspec/schemas/`.

## Quick start

```bash
make check          # gofmt + go vet + all tests
make build          # build af, nightshift and spec into bin/
```

## Documentation

- [Spec CLI Reference](docs/cli.md) — commands, flags and usage
- [Configuration](docs/configuration.md) — LLM provider setup and model selection
- [Go Library API](afspec/README.md) — the `afspec` library
- [Development Guide](docs/development.md) — setup, testing, contributing
- [ADRs](docs/adr/) — architecture decisions
