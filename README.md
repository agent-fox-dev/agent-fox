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
| `agentspec/` | `agentspec` | LLM-powered spec creation: session state machine, generation pipeline, and the agent that runs it — built on the AgentKit SDK from [`coder`](https://github.com/agent-fox-dev/coder). |
| `cmd/spec/` | `main` | The `spec` CLI. |
| `cmd/af/` | `main` | The `af` CLI. |
| `cmd/nightshift/` | `main` | The `nightshift` CLI. |

The spec format itself is specified in the
[`spec`](https://github.com/agent-fox-dev/spec) repository
(`specification/spec-format-v2.md`). The JSON Schemas the library compiles and
embeds live in `afspec/schemas/`.

## Quick start

`agentspec` runs on the AgentKit SDK, which lives in the `coder` repository
under the module path `github.com/agentfox/agentkit-go`. That path does not
match its repository URL, so the module proxy cannot serve it and it is
consumed through a `replace` to a sibling checkout:

```bash
git clone https://github.com/agent-fox-dev/coder ../coder
```

```bash
make check          # gofmt + go vet + all tests
make build          # build af, nightshift and spec into bin/
```

The test suite needs no API key and makes no network calls.

## Documentation

- [Spec CLI Reference](docs/cli.md) — commands, flags and usage
- [Configuration](docs/configuration.md) — credentials, model selection, and what the model is allowed to read
- [Model Usage](docs/model-usage.md) — what each pipeline phase sends, and how a failure is repaired
- [Go Library API](afspec/README.md) — the `afspec` library
- [Development Guide](docs/development.md) — setup, testing, contributing
- [ADRs](docs/adr/) — architecture decisions
