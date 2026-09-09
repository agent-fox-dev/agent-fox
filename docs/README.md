# agent-fox documentation

| Document | What it covers |
|---|---|
| [Spec CLI Reference](cli.md) | Every `spec` command, its flags, its JSON envelope and its exit codes |
| [Configuration](configuration.md) | Credentials, model selection, source access and the config file |
| [Model Usage](model-usage.md) | What each pipeline phase sends, and what happens when the answer is wrong |
| [Development](development.md) | Setup, repository layout, the schema workflow, testing |
| [Go Library API](../afspec/README.md) | The `afspec` spec-format library |

## Architecture decisions

| ADR | Decision |
|---|---|
| [01](adr/01-adopt-spec-format-v2.md) | Adopt spec format version 2 |
| [02](adr/02-build-the-spec-pipeline-on-agentkit.md) | Build the spec pipeline on AgentKit |

## Errata

Where the implementation diverges from a specification, or from what a reader
of the previous version would expect.

| Erratum | Subject |
|---|---|
| [agentkit_model_resolution](errata/agentkit_model_resolution.md) | Vertex and Bedrock refusal, the extended-variant model, cache policy, forced tool calls |

## PRDs

`docs/prds/` holds the product requirements documents this repository was
built from.
