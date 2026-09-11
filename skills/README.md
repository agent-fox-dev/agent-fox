# skills/

The three markdown skills the tools were built from — `af-issue`, `af-fix` and
`af-spec` — are no longer in this repository. They were removed once each had
been rebuilt as a program:

| Skill | Became |
|---|---|
| `af-issue` | the [`issue`](../docs/cli.md#issue) tool |
| `af-fix` | the [`fix`](../docs/cli.md#fix) tool |
| `af-spec` | the [`spec`](../docs/cli.md#spec) tool |

Every one of them described a workflow a prompt cannot enforce — a read-only
mandate handed to a CLI that has `write_file`, an issue template a model is
asked to follow, a `gh issue create` invocation the model is asked to run — and
comparing each against its replacement is the clearest statement of what moved
from prose into code. [ADR 03](../docs/adr/03-rebuild-the-skills-as-tools.md)
has that table, quotes the skills, and lists the logic errors that writing them
out as programs surfaced. The files themselves are in the git history:
`skills/afspec.md` before commit `8f6aa81`, `skills/af-issue` and
`skills/af-fix` before commit `b9cfd87`.
