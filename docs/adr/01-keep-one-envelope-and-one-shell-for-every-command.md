# ADR 01: Keep one envelope and one shell for every command

- **Status:** Accepted
- **Date:** 2026-10-25 (records decisions taken while specs 05 to 19 were implemented)

## Context

Every `af` command prints one JSON envelope and runs inside one shell,
`internal/toolio` ([architecture](../architecture.md#the-command-shell)). The
spec that defined the shell was written before the old `internal/toolio` could
be read and before `issuex` had left this repository, and later specs added
outputs to it (a schema dump, a copy of the envelope, an events file) that no
caller ended up reading. The worker then came to run the loops in process. Each
of these was settled once; the settlements that still bind the code are here.

## Decision

**One shell, one envelope, one way out.**

- `cmd/af/main.go` hands the process to `App.Main` and exits with its return
  value; it is the only file of `cmd/af` that ends the process. The other files
  of `cmd/af` are the service client's commands.
- The envelope is written to stdout and, byte for byte, to `envelope.json` in
  the run directory. There is no `--schema`, `--output` or `--emit-events`, no
  result schema per command and no envelope schema file: a box takes a
  command's envelope from its stdout, the worker reads the run directory, and a
  caller in the same process has `App.EventSink`.
- `schema_version` is `5.0.0`. It names the envelope's fields and follows the
  compatibility rule in [cli.md](../cli.md#envelope-versioning); removing an
  output never changed a field, so it did not move the version.
- `issuex` is the package of `github.com/agent-fox-dev/aflib`, required at a
  pseudo-version until aflib is tagged. This repository has no copy of it.

**Running a command without a command line.** `App.Invoke` runs a
`toolio.Call` (the command, its one argument, the directory, the context
values, the timeout and the command's options) as `Main` runs a command line,
and returns the envelope as a value beside its encoding. The `argv` that the
events and `needs_human.resume` of such a run carry is the command and its
argument, without the flags a command line would have; nothing in the service
reads it.

**Amending an envelope after the fact.** A caller that learns something after
the command wrote its envelope (the worker's lease and mirror warnings) adds it
with `toolio.AddWarnings` or `toolio.Amend`, which change the warnings, side
effects and artifacts of the encoded document and nothing else: not the order
of its fields, not its result and never its status.

**Warnings and secrets.** `toolio.WarningCodes` lists the codes the shell
itself raises; a command's own codes are constants of its package, and
[cli.md](../cli.md) lists them all. `Run.Redact` takes a registered secret out
of the envelope, its copy and every event, but only a secret of 8 bytes or
more: a shorter one would match ordinary words.

## Consequences

- The `5.0.0` envelope is not compatible with the one of the earlier `triage`,
  `fix` and `impl` tools (up to `4.0.0`). A caller of the old tools reads the
  new one as follows; nothing of the old shape was carried over.

  | Old | Now |
  |---|---|
  | `version` (the tool's build) | `af --version`; the forge User-Agent `af/<version>` |
  | `session_id` | `run_id` (`YYYYMMDD_HHMMSS_<6 hex>`), null without a run directory |
  | `ok` | `status` `done` |
  | `model` | `usage.brain` |
  | `timings`, `untrusted_fields` | not carried |
  | `report_file` | an artifact `{kind: "report", path}` |
  | `side_effects[]` `{action, target, kind?, url?, ok, warning?}` | `{kind, target, state, detail?}` |
  | `artifacts[]`, one shape per kind | `{kind, path?, url?, name?, number?}` |
  | `next[]` `{tool, input, flags, why}` | `{argv, reason}` |
  | `needs_human.resume`, a command with placeholders | `{argv, flag}` with the real argv |
  | `error.stage`, `error.resumable`, `warnings[].severity`, `warnings[].stage`, `needs_human.needed`, `needs_human.stage` | gone |
  | `fix_hint.valid`, `fix_hint.current`; `env` a list; `suggest` a number | gone; `env` one name; `suggest` text |
  | `needs_human.options[].text` | `options[].label` |
  | `input.bytes`, `input.comments_truncated`, `input.context_bytes` | gone |
  | `usage.cache_read_tokens`, `usage.cache_creation_tokens`, `usage.phases` | gone |

  The envelope always prints `warnings`, `artifacts`, `side_effects` and
  `next` (as `[]`) and `error`, `needs_human`, `result` and `run_id` (as
  `null`), and adds `finished_at`.
- A field that marks text a model wrote (the old `untrusted_fields`) has to be
  added to `toolio.Envelope` by the change that needs it, under the
  compatibility rule.
