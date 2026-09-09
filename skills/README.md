# skills/

The three markdown skills these tools were built from, kept for reference.

| File | Became |
|---|---|
| `af-issue` | the [`issue`](../docs/cli.md#issue) tool |
| `af-fix` | the [`fix`](../docs/cli.md#fix) tool |
| `af-spec` | the [`spec`](../docs/cli.md#spec) tool |

Every one of them describes a workflow a prompt cannot enforce — a read-only mandate
handed to a CLI that has `write_file`, an issue template a model is asked to follow,
a `gh issue create` invocation the model is asked to run — and comparing each against its
replacement is the clearest statement of what moved from prose into code.
[ADR 03](../docs/adr/03-rebuild-the-skills-as-tools.md) has the table, and the
list of logic errors that writing them out as programs surfaced.
