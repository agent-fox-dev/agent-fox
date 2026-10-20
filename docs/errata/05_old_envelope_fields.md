# Erratum 05: the old envelope, its version and the fields this scope does not carry

**Spec:** `05_af_cli_envelope`, Design Decision 2 ("Define the envelope here
rather than 'revive as it was'").

The spec says the old `internal/toolio` is not readable from the tree and asks
the implementer, if the old code is recovered and a field is missing from the
new envelope, to report it here instead of adding it silently. The old code is
readable in history: `git show 1e6458d:internal/toolio/envelope.go`. This
erratum lists what it had that the delivered envelope does not.

## Top-level fields

The old `Envelope` (`1e6458d:internal/toolio/envelope.go:327`) had these
fields the delivered `Envelope` (`internal/toolio/envelope.go:71`) does not:

| Old field | Where the information is now |
|---|---|
| `version` (the tool's build identity) | `af --version`; the forge User-Agent `af/<version>` |
| `session_id` (32 hex characters) | `run_id`, `YYYYMMDD_HHMMSS_<6 hex>`, null for a command without a run directory |
| `ok` | `status` and `exit_code`; the old description of `ok` was "true only when the tool did the whole job it was asked to do", which is `status` `done` |
| `model` | `usage.brain` |
| `timings` | not carried |
| `untrusted_fields` (JSON pointers to model-authored text in `result`) | not carried |
| `report_file` | an artifact `{kind: "report", path}` |

The delivered envelope also adds `finished_at`, which the old one lacked, and
always prints `warnings`, `artifacts`, `side_effects` and `next` (as `[]`), and
`error`, `needs_human`, `result` and `run_id` (as `null`), where the old one
left them out when empty.

## Nested shapes that changed

| Object | Old (`1e6458d`) | Delivered |
|---|---|---|
| `side_effects[]` | `{action, target, kind?, url?, ok, warning?}` | `{kind, target, state, detail?}`, `state` one of `done`, `planned`, `failed` |
| `artifacts[]` | one shape per kind (`branch`, `commit`, `pull_request`, `comment`, `issue`, `report_file`, `events_file`) | `{kind, path?, url?, name?, number?}`, `kind` an open string |
| `next[]` | `{tool, input, flags, why}` | `{argv, reason}` |
| `needs_human.resume` | a placeholder command (`<same input>`, `"<answer>"`) | `{argv, flag}` with the real argv |

Inside the objects they share, the old shapes also had fields the delivered
ones do not:

| Object | Fields of the old shape that are gone |
|---|---|
| `error` | `stage`, `resumable` |
| `fix_hint` | `valid`, `current`; `env` was a list of variable names and is one name; `suggest` was a number and is text |
| `needs_human` | `needed`, `stage`; `options[].text` is now `options[].label`; `resume` was a command line with placeholders and is `{argv, flag}` |
| `warnings[]` | `severity`, `stage` |
| `input` | `bytes`, `comments_truncated`, `context_bytes` |
| `usage` | `cache_read_tokens`, `cache_creation_tokens`, `phases` |

## The schema version

Design Decision 3 originally set `schema_version` to `"4.0.0"` as "the next
major version after the old one", on the ground that "the only recorded old
value is the hub tasks draft's `"3.0.0"`". The old code records another:
`1e6458d:internal/toolio/envelope.go:319` was `const SchemaVersion = "4.0.0"`
(bumped from 3.1.0 on 2026-10-14, when the `spec` tool left), and
`1e6458d:docs/cli.md` called it the current version. The draft's example had
been written while the version was still 3.x. The old interface rule made
removing or renaming a field, or changing its type, a breaking change that
bumps the major version, and the tables above are such changes, so the old
and the new envelope would have carried the same version for two shapes that
a caller reads differently.

This was found after the implementation, and settled at the maintainer's
request: the spec package now asks for `"5.0.0"` (its Design Decision 3 says
why), `toolio.SchemaVersion`, the `const` in
`internal/toolio/schema/envelope.schema.json`, the goldens and `docs/cli.md`
follow, and `docs/cli.md` states the compatibility rule that the number
stands for. Nothing in the delivered envelope differs from the spec any more
on this point.

## Consequence

Nothing was added to the delivered envelope on account of this list. A later
spec that needs one of the dropped fields, `untrusted_fields` above all, which
marks text a model wrote, must add it to `toolio.Envelope` and to
`internal/toolio/schema/envelope.schema.json`, and change `SchemaVersion`.
`TestEnvelopeFieldOrder_TS_05_10` and the schema drift test fail until the
two agree.
