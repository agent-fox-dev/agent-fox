# Erratum 05: the old envelope's fields this scope does not carry

**Spec:** `05_af_cli_envelope`, Design Decision 2 ("Define the envelope here
rather than 'revive as it was'").

The spec says the old `internal/toolio` is not readable from the tree and asks
the implementer, if the old code is recovered and a field is missing from the
new envelope, to report it here instead of adding it silently. The old code is
readable in history: `git show 1e6458d:internal/toolio/envelope.go`. This
erratum lists what it had that the delivered envelope does not.

## Top-level fields

The old `Envelope` (`1e6458d:internal/toolio/envelope.go:327`) had these
fields the delivered `Envelope` (`internal/toolio/envelope.go:70`) does not:

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

## Consequence

Nothing was added to the delivered envelope on account of this list. A later
spec that needs one of the dropped fields, `untrusted_fields` above all, which
marks text a model wrote, must add it to `toolio.Envelope` and to
`internal/toolio/schema/envelope.schema.json`, and change `SchemaVersion`.
`TestEnvelopeFieldOrder_TS_05_10` and the schema drift test fail until the
two agree.
