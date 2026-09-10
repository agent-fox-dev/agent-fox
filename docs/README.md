# agent-fox documentation

| Document | What it covers |
|---|---|
| [Tool Reference](cli.md) | `spec`, `issue`, `fix` and `impl`: the shared interface, every flag, the JSON envelope and the exit codes |
| [Configuration](configuration.md) | Credentials, model selection, what the model is allowed to read, bounds |
| [Model Usage](model-usage.md) | What each phase sends, and what happens when the answer is wrong |
| [Development](development.md) | Setup, repository layout, the schema workflow, testing |
| [Go Library API](../afspec/README.md) | The `afspec` spec-format library |

## Architecture decisions

| ADR | Decision |
|---|---|
| [01](adr/01-adopt-spec-format-v2.md) | Adopt spec format version 2 |
| [02](adr/02-build-the-spec-pipeline-on-agentkit.md) | Build the spec pipeline on AgentKit |
| [03](adr/03-rebuild-the-skills-as-tools.md) | Rebuild the three skills as tools |
| [04](adr/04-write-every-scope-of-a-split.md) | Write every scope of a split, and record the split until it is done |

## Errata

Where the implementation diverges from a specification, or from what a reader
of the previous version would expect.

| Erratum | Subject |
|---|---|
| [agentkit_model_resolution](errata/agentkit_model_resolution.md) | Vertex and Bedrock refusal, the extended-variant model, cache policy, forced tool calls |
| [tool_schema_property_names](errata/tool_schema_property_names.md) | Why the generation tools do not declare the artifact's `$schema`, and who writes it |

## PRDs

`docs/prds/` holds the product requirements documents this repository was
built from.
