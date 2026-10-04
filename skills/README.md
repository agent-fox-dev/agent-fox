# skills/

Two markdown skills for a coding CLI, for the work that stays a conversation
with a person:

| Skill | What it does |
|---|---|
| [`af-prd`](af-prd.md) | Writes a PRD with the user, in the shape the `spec` tool's own PRD phase produces, and saves it under `docs/prds/` ready for `spec <path>` |
| [`af-code-simplifier`](af-code-simplifier.md) | Makes existing code smaller and clearer without changing its behaviour, one verified commit at a time |

Both are workflows a person drives: an interview and a review. That is the
line [ADR 03](../docs/adr/03-rebuild-the-skills-as-tools.md) draws. The three
skills that ran unattended — `af-issue`, `af-fix` and `af-spec` — were rebuilt
as programs and removed from this directory:

| Skill | Became |
|---|---|
| `af-issue` | the [`triage`](../docs/cli.md#triage) tool |
| `af-fix` | the [`fix`](../docs/cli.md#fix) tool |
| `af-spec` | the [`spec`](../docs/cli.md#spec) tool |

Each of those described a workflow a prompt cannot enforce — a read-only
mandate handed to a CLI that has `write_file`, an issue template a model is
asked to follow, a `gh issue create` invocation the model is asked to run — and
comparing each against its replacement is the clearest statement of what moved
from prose into code. ADR 03 has that table, quotes the skills, and lists the
logic errors that writing them out as programs surfaced. The files themselves
are in the git history: `skills/afspec.md` before commit `8f6aa81`,
`skills/af-issue` and `skills/af-fix` before commit `b9cfd87`.
