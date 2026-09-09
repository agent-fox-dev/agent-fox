# agent-fox

agent-fox is an autonomous spec-first coding agent (golang version).

The mono-repo for all agent-fox (golang) code: library modules, CLI tools and
services. It depends on

- [`coder`](https://github.com/agent-fox-dev/coder) — the AgentKit SDK
- [`spec`](https://github.com/agent-fox-dev/spec) — the spec format
- [`apikit`](https://github.com/txsvc/apikit)

and works with [`hub`](https://github.com/agent-fox-dev/hub) as its backend
service.

## The tools

Three programs, one interface: **one input, one JSON object out.**

```
spec  [flags] <input>     a product idea      → a validated specification package
issue [flags] <input>     a problem report    → a structured GitHub issue
fix   [flags] <input>     a problem           → a verified change on a branch
```

The input is exactly one of: a GitHub issue or pull-request URL, a path to a
readable file, `-` for stdin, or any other text. There is no flag that selects
the kind — the argument's shape decides, in Go, before anything else happens.

```sh
export ANTHROPIC_API_KEY=sk-ant-...
export GITHUB_TOKEN=ghp_...

issue "panic: assignment to entry in nil map in loop.go, after an abort"
fix   https://github.com/acme/widgets/issues/42 --dir ~/src/widgets
spec  ./docs/prds/widget-cache.md --architecture

kubectl logs deploy/api --since 1h | issue - --repo acme/widgets --dry-run
```

Each writes exactly one JSON object to stdout, on every path including the
failing ones. Progress goes to stderr, so the two never interleave. Exit codes
are shared: `0` done, `1` failed, `2` usage, `3` a person has to answer
something, `4` work exists but the checks do not pass.

See the [tool reference](docs/cli.md) for every flag, every result field and
every error category.

### Why they are programs

Each tool started as a markdown skill handed to a coding CLI. A skill can
describe a workflow; it cannot enforce one. `af-issue` states its read-only
mandate three times and then hands the CLI a `write_file` tool. `af-fix` opens
a pull request *and* squash-merges the same branch, posts its analysis before
checking whether it can work at all, and writes "✅ all tests pass" from a
template rather than from a test run.

So the model does the two or three steps that genuinely need judgment, and Go
does the rest:

| The skill says, in prose | The tool does, in code |
|---|---|
| "the codebase is read-only to you" | the mutating tools are not in the resolved set |
| the issue-body template | a JSON Schema; Go renders the markdown |
| "cite real files; do not guess" | every cited path resolved against the workspace |
| `gh issue create --repo …` | a `net/http` call after the run, suppressed by `--dry-run` |
| "run the tests" then "✅ all tests pass" | a measured before/after pair, and a renderer that takes one |
| "halt until input is received" | exit 2, before a token is spent |

None of the right-hand column depends on the model cooperating. That is the
whole argument for embedding an agent in a program rather than writing a longer
prompt: **the parts you cannot afford to have wrong stop being prompt.**

[ADR 03](docs/adr/03-rebuild-the-skills-as-tools.md) records the reasoning and
the errors the rewrite found.

## Modules

| Path | Package | Purpose |
|---|---|---|
| [`afspec/`](afspec/README.md) | `afspec` | Spec format library: load, validate, mutate, render and save specification packages. |
| `specgen/` | `specgen` | The spec pipeline: the PRD phase, the three generation phases, and the project audit. |
| `issuetriage/` | `issuetriage` | The triage pipeline: the diagnosis schema, the citation check, the rendered issue. |
| `codefix/` | `codefix` | The fix pipeline: pre-flight, analysis, implementation, verification, landing. |
| `internal/toolio/` | `toolio` | Input classification, the JSON envelope, exit codes, and the shell the three commands share. |
| `internal/agentrun/` | `agentrun` | Model and credential resolution, the phase runner, the read-only invariant, the shell guard. |
| `internal/ghapi/` | `ghapi` | A dependency-free GitHub REST client. |
| `internal/gitx/`, `internal/checks/` | | git, and the command that decides whether a change is correct. |
| `cmd/spec/`, `cmd/issue/`, `cmd/fix/` | `main` | The three tools. |

The spec format itself is specified in the
[`spec`](https://github.com/agent-fox-dev/spec) repository
(`specification/spec-format-v2.md`). The JSON Schemas the library compiles and
embeds live in `afspec/schemas/` — and they are also what the generation tools
declare to the model, so the two cannot drift.

## Quick start

The tools run on AgentKit, which lives in the `coder` repository under the
module path `github.com/agentfox/agentkit-go`. That path does not match its
repository URL, so the module proxy cannot serve it and it is consumed through
a `replace` to a sibling checkout:

```bash
git clone https://github.com/agent-fox-dev/coder ../coder
```

```bash
make check          # gofmt + go vet + all tests
make build          # build every CLI into bin/
```

The test suite needs no API key, no GitHub token and no network: the model half
runs against AgentKit's scripted provider, GitHub against an `httptest` server,
and git against real temporary repositories.

## Documentation

- [Tool Reference](docs/cli.md) — the three tools, their flags, their JSON
- [Configuration](docs/configuration.md) — credentials, model selection, what the model is allowed to read
- [Model Usage](docs/model-usage.md) — what each phase sends, and how a failure is repaired
- [Go Library API](afspec/README.md) — the `afspec` library
- [Development Guide](docs/development.md) — setup, testing, contributing
- [ADRs](docs/adr/) — architecture decisions
