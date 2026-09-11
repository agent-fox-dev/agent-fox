# PRDs

The product requirements documents this repository was built from. Both were
written before [ADR 03](../adr/03-rebuild-the-skills-as-tools.md) replaced the
`spec` CLI with the four tools, so they describe a program that no longer
exists; they are kept as the record of why `afspec` looks the way it does.

| Document | What it asked for | What became of it |
|---|---|---|
| [afspecgo.md](afspecgo.md) | A Go port of the Python `afspec` library with byte-for-byte round-trip fidelity | The [`afspec`](../../afspec/README.md) package, since moved to format version 2 ([ADR 01](../adr/01-adopt-spec-format-v2.md)) |
| [gospec.md](gospec.md) | A Go port of the `agentspec` package and the `spec` CLI, subcommand for subcommand | Built, then deleted: ADR 03 replaced the fifteen-subcommand CLI with the `spec` tool, and [ADR 02](../adr/02-build-the-spec-pipeline-on-agentkit.md) replaced its model layer with AgentKit |

New PRDs go here as `NN-imperative-verb-phrase.md`, numbered from `01`.
