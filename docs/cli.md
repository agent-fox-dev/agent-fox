# The af command line

`af` is the one binary of agent-fox. `make build` writes it to `bin/af`
(`go build ./cmd/af` builds it too). Every command of `af` speaks the same
contract, which the shell in `internal/toolio` implements once so that the
commands do not: one JSON envelope on stdout, a fixed table of statuses and
exit codes, coded warnings, a stop on a question that is resumed with
`--context`, a `--dry-run` that cannot write to the forge, a fetchable
schema, and a git-ignored run directory.

**This release registers one work command, [`lang detect`](#lang-detect).**
The binary also answers `--version`, `--schema` and `help`; `af` with no
arguments, or with a command it does not know, prints a `usage` envelope and
exits 2. The other commands of this scope and of later specs (`gate run`,
`triage`, `fix`, …) register on the shell and get everything below.

```
$ af --version
{
  "name": "af",
  "version": "v0.1.0",
  "commit": "bf4a001",
  "build_time": "2026-10-20T10:20:24Z",
  "go_version": "go1.27.2"
}
```

`version`, `commit` and `build_time` are set by `make build` from the
Makefile's `VERSION`, `COMMIT` and `BUILD_TIME`. A binary built without them
reports what the Go toolchain recorded (the main module's version,
`vcs.revision` and `vcs.time`), and `"dev"` with an empty commit and build
time when it recorded none.

## Invoking a command

```
af <command> [flags] [argument]
af help [command]
af --version
af --schema
```

- A command is registered under a **command path** of one or more words
  (`triage`, `gate run`). The shell picks the longest registered path that
  matches the words before the first flag. The path is the envelope's `tool`.
- Flags may come before or after the positional argument (`af fix ./bug.md
  --dir repo` and `af fix --dir repo ./bug.md` are the same), as `--flag
  value` or `--flag=value`. `--` ends the flags. A flag that takes a value
  takes the next word whatever it looks like.
- A command takes at most one positional argument unless it declares
  otherwise. Another is a `usage` error. A bad flag is a `usage` error too,
  and the flag package prints nothing.
- `-h`, `--help`, `--version` and `--schema` are answered wherever they stand
  before a `--`, without resolving input, building a forge client, reading
  stdin or creating a run directory. If more than one is given, help wins
  over version, and version over schema. The word after a flag that takes a
  value is that value: in `--context --help` the answer is `--help`.

### Flags

Every command accepts:

| Flag | Meaning |
|---|---|
| `--output <file>` | Also write the envelope to this file. |
| `--emit-events <file>` | Also append the events (see [Events](#events)) to this file. |

A command opts into these, and rejects any it did not opt into as an unknown
flag:

| Flag | Meaning |
|---|---|
| `--dir <path>` | The checkout to work in; default `.`, made absolute. A command that uses a forge or a run directory has it. |
| `--context <text>` | An answer to a question the command asked; may be repeated. See [needs_human and --context](#needs_human-and---context). |
| `--dry-run` | Send no write request to the forge. See [--dry-run](#--dry-run). |
| `--repo <owner/repo>` | The repository to work on. |
| `--timeout <duration>` | Stop the command after this long, such as `90s` or `15m`; `0` means no limit. The command declares the default. |

## Commands

### lang detect

```
af lang detect [--dir DIR] [--write] [--timeout DURATION]
```

Reports what the repository at `--dir` is written in and how it is checked.
It reads files and runs nothing. It builds no forge client, calls no model,
opens no network connection and creates no run directory, and without
`--write` it writes nothing at all. The default `--timeout` is 1 minute.

| Flag | Meaning |
|---|---|
| `--write` | Store the freshly detected gate table in `.agent-fox/gate.json` (see [configuration](configuration.md)). Any `gate.json` there now is ignored, one that does not parse included, and replaced. The file is written through a temporary file and a rename, and is an artifact of kind `gate_file`. |

The result (its schema is `lang_detect` under `$defs`) holds:

| Field | Meaning |
|---|---|
| `languages` | `[{name, files, manifest?}]`: each language with its file count, most files first and the name breaking a tie. The files are the file view (git's tracked and untracked-not-ignored files in a work tree, a directory walk elsewhere, never `.git/` or `.agent-fox/`), classified by extension as `go`, `python`, `javascript`, `typescript`, `rust`, `java`, `kotlin`, `ruby`, `c`, `c++`, `c#`, `php`, `scala`, `lua` or `bash`. `manifest` is the file at the root that marks the project, when there is one. Empty for an empty directory. |
| `gate` | `{source, checks}`: the effective gate table. `source` is `file` when `.agent-fox/gate.json` gave it and `detected` otherwise. Each check is `{name, argv, kind, source, available}`: `argv` is the command with no shell; `kind` is `lint`, `test` or `check` (both); `source` is where it came from (`Makefile:check`, `go.mod`, `gate.json`); `available` says whether `argv[0]` can be run on this machine. |
| `suite` | The command forms that count as the full test suite: the gate's checks, `make check` and `make test` where the Makefile has those targets, and the forms of each language. |
| `programs` | The programs a gate may run: the first word of each check, and the toolchain of each language found. |

Without a `gate.json` the table is detected from the files at the root only,
in this order, and the first rule that gives a check decides:

1. A `GNUmakefile`, `makefile` or `Makefile` with a `check` target gives
   `make check`.
2. Otherwise a Makefile with a `test` target gives `make lint` (when it has
   that target too) and `make test`.
3. Otherwise each manifest present adds its checks, in the order Go, Python,
   Node, Rust, Java: `go vet ./...` and `go test ./...` for `go.mod`;
   `pytest` (when `pytest.ini`, a root `conftest.py` or the name `pytest` in
   `pyproject.toml`, `setup.cfg` or a `requirements*.txt` shows it) or
   `python -m unittest discover` for a Python manifest, behind `uv run` with
   `uv.lock` or `poetry run` with `poetry.lock`, and `ruff check .` when ruff
   is configured; `<pm> test` (unless it is npm's placeholder) and `<pm> run
   lint` for the scripts of `package.json`, with `pnpm`, `yarn` or `bun` by
   lockfile and `npm` otherwise; `cargo test` for `Cargo.toml`;
   `./mvnw test` or `mvn -q test` for `pom.xml` and `./gradlew test` or
   `gradle test` for `build.gradle(.kts)`, the wrapper when it exists.

Which checks are selected, and their order and argv, depend on the files and
on nothing else: not on `PATH`, not on the machine. Only `available` does.

| Outcome | When |
|---|---|
| `done` | Always, when the repository could be read. A program that is not available is a warning, `check_unavailable`, once per check. |
| `failed`/`config` | `.agent-fox/gate.json` does not parse, names a version other than 1, or holds a check that cannot run (no `argv`, a repeated `name`, a `kind` that is not `lint`, `test` or `check`). The message names the file, and the Makefile is not used as a fallback. |
| `failed`/`io` | The directory cannot be read, or `gate.json` cannot be written. |

On this repository:

```json
{
  "languages": [{"name": "go", "files": 57, "manifest": "go.mod"}],
  "gate": {
    "source": "detected",
    "checks": [
      {"name": "make check", "argv": ["make", "check"], "kind": "check", "source": "Makefile:check", "available": true}
    ]
  },
  "suite": [["make", "check"], ["make", "test"], ["go", "vet", "./..."], ["go", "test", "./..."]],
  "programs": ["make", "go", "gofmt"]
}
```

(The result is indented and the arrays are spread over lines in the real
output; the count of Go files is the repository's at the time.)

## The envelope

Every invocation other than `--version`, `--schema` and help writes exactly
one JSON object to stdout, indented by two spaces and followed by one newline,
and nothing else. Every field is always present and the fields appear in this
order; arrays are `[]`, never null, and an object that does not apply is
`null`. A dry run that reads a repository and is refused two writes:

```json
{
  "schema_version": "5.0.0",
  "tool": "example",
  "run_id": "20261020_072649_a31f9c",
  "status": "done",
  "exit_code": 0,
  "summary": "diagnosed; 2 writes planned",
  "input": {
    "kind": "text",
    "origin": "",
    "truncated": false
  },
  "result": null,
  "needs_human": null,
  "error": null,
  "warnings": [
    {
      "code": "label_missing",
      "message": "the label triaged does not exist yet"
    }
  ],
  "artifacts": [
    {
      "kind": "run_dir",
      "path": "/work/repo/.agent-fox/run/20261020_072649_a31f9c"
    },
    {
      "kind": "report",
      "path": "/work/repo/.agent-fox/run/20261020_072649_a31f9c/report.md"
    }
  ],
  "side_effects": [
    {
      "kind": "add_comment",
      "target": "acme/shop#12",
      "state": "planned"
    },
    {
      "kind": "add_labels",
      "target": "acme/shop#12",
      "state": "planned"
    }
  ],
  "next": [],
  "usage": {
    "brain": "",
    "cost_usd": 0,
    "cost_source": "none",
    "input_tokens": 0,
    "output_tokens": 0,
    "turns": 0
  },
  "started_at": "2026-10-20T07:26:48.120Z",
  "finished_at": "2026-10-20T07:26:55.440Z",
  "duration_ms": 7320
}
```

(`example` stands for a command of a later spec; the payload was printed by
the shell under a test command, with the path of the checkout shortened.)

| Field | Type | Meaning |
|---|---|---|
| `schema_version` | string | `"5.0.0"`. |
| `tool` | string | The command path, or `"af"` when no command was resolved. |
| `run_id` | string or null | The name of the run directory; null for a command without one. |
| `status` | string | `done`, `failed`, `usage`, `needs_human` or `unverified`. |
| `exit_code` | integer | The code the process exits with. |
| `summary` | string | One line, with no line break. The shell writes it when it sets the status itself. |
| `input` | object | `{kind, origin, truncated}`: what the positional argument resolved to. `kind` is `issue`, `file`, `text`, `stdin` or `none`; `origin` is the URL of the issue or the absolute path of the file, else `""`. |
| `result` | any or null | The command's own result. Its schema is the command's (see [--schema](#--schema)). |
| `needs_human` | object or null | The question the command stopped on. Non-null exactly when `status` is `needs_human`. |
| `error` | object or null | `{category, message, retryable, fix_hint}`. Non-null exactly when `status` is `failed` or `usage`. `fix_hint` is null, or an object with any of `flag`, `env`, `var` and `suggest`. |
| `warnings` | array | `[{code, message}]`, in the order they were raised. |
| `artifacts` | array | `[{kind, path?, url?, name?, number?}]`. Paths are absolute; a relative path a command records is made absolute. |
| `side_effects` | array | `[{kind, target, state, detail?}]`. `state` is `done`, `planned` or `failed`. |
| `next` | array | `[{argv, reason}]`: suggested follow-up commands. |
| `usage` | object | `{brain, cost_usd, cost_source, input_tokens, output_tokens, turns}`. With no brain, `brain` is `""`, the numbers are 0 and `cost_source` is `none`; it is otherwise `measured` or `reported`. |
| `started_at`, `finished_at` | string | RFC 3339, UTC, with milliseconds. |
| `duration_ms` | integer | The wall time of the invocation. |

The same bytes are written to `<run_dir>/envelope.json`, when there is a run
directory, and to the file `--output` names, before they are printed. If a
copy cannot be written, the envelope on stdout (the one that counts) carries
the warning `envelope_unwritable` or `output_unwritable`, and its status does
not change. If stdout cannot be written, the process still exits with the
code of the status. The shell writes nothing to stderr except the stack of a
panic.

## Statuses and exit codes

| `status` | Exit code | When |
|---|---|---|
| `done` | 0 | The command did what it was asked. |
| `failed` | 1 | It could not, for a reason given in `error`. |
| `usage` | 2 | The invocation was wrong: flags, arguments or the form of the input. |
| `needs_human` | 3 | It stopped on a question. |
| `unverified` | 4 | Work exists but could not be verified. A command that returns it raises a warning that says what was not verified. |

A command returns an outcome (`done`, `unverified` or `needs_human`, with a
summary, a result and, for a question, the question) or an error. An error
that is a `*toolio.Failure` becomes `error`, with status `usage` for the
category `usage` and `failed` for any other. Any other error is `failed` with
the category `internal`, and so is a panic, which the shell recovers: the
stack goes to stderr and, when there is a run directory, to
`<run_dir>/panic.txt`. An outcome that contradicts itself (`needs_human` with
no question, a question with another status, more than 26 options) is a
command bug and is reported as `failed`/`internal`.

Two things are for the one command whose caller is a host that reads another
format than the envelope (`af guard check --host claude`), and no other
command uses them. A command registered with `RawStdin` is given the
process's stdin, unread, as `Run.Stdin`; a command without it gets `nil` and
reads nothing. An outcome with a `Document` and the status `done` has the
document written to stdout in place of the envelope, and the exit code is
`done`'s, 0; the envelope is still made, and still written to `--output` and
to the run directory. A document with any other status is an inconsistent
outcome, and a run that fails after the command returned prints the envelope.

## Error categories

| `error.category` | Used for |
|---|---|
| `usage` | The invocation is wrong. Status `usage`. |
| `auth` | An issue was not found without a token, so it may be private. `fix_hint.env` names the token to set. |
| `forge` | The forge could not be reached, refused, or the client could not be built. `retryable` is true after a rate limit, a server error or a lost connection, and false after a refusal. |
| `io` | A file, stdin or the run directory could not be read or written. |
| `cancelled` | The process was sent SIGINT or SIGTERM. |
| `timeout` | `--timeout` ran out. Not retryable; `fix_hint` is `{flag: "--timeout", suggest: <twice the value>}`. |
| `internal` | A bug: a plain error, a panic, an inconsistent outcome, a result that is not JSON. |
| `config` | A configuration file `af` reads cannot be used, for example a `.agent-fox/gate.json` that does not parse. The message names the file. |
| `restore` | A tree that a command changed in order to measure it could not be put back, or a leftover of an earlier measurement stands in the way. The message names the directory that holds what is left. |
| `git` | git failed in a way the command cannot work around. |

Later specs add categories of their own to this list.

## Warning codes

A warning never changes the status: a run that is done stays done. A warning
that repeats another one exactly (same code, same message) is recorded once.
A code matches `^[a-z][a-z0-9_]*$`; one that does not is reported under
`invalid_warning_code`.

| Code | Raised when |
|---|---|
| `input_truncated` | The body of the input was cut at 131072 bytes, or the thread of an issue has more comments than were read. |
| `comments_unreadable` | The issue was read and its comments were not; the run goes on with the issue. |
| `envelope_unwritable` | `<run_dir>/envelope.json` could not be written. |
| `output_unwritable` | The file named by `--output` could not be written. |
| `events_unwritable` | An event could not be written to the run directory or to `--emit-events`. Raised once. |
| `invalid_warning_code` | A command raised a warning whose code does not match the pattern; the message begins with the code it gave. |
| `check_unavailable` | `lang detect`: a check of the gate table names a program that cannot be run on this machine. The check stays in the table with `available: false`; the message names the check. |

## The positional input

A command that takes an input (`<issue|file|text|->`) resolves its first
positional argument the same way. The first rule that matches wins.

1. `-` reads stdin until EOF: kind `stdin`.
2. A GitHub or GitLab issue, pull request or merge request URL: kind `issue`.
   The thread is read with the forge client and rendered as text (title,
   author, labels, body, comments).
3. Any other `http://` or `https://` URL is a `usage` error naming the
   accepted forms; for a self-hosted forge set `GITHUB_API_URL` or
   `GITLAB_API_URL` (see [configuration](configuration.md)).
4. The name of an existing regular file, relative to the working directory:
   kind `file`, `origin` its absolute path. A file with a NUL byte in its
   first 8 KiB, or a directory, is a `usage` error; a file that cannot be
   read is `failed`/`io`.
5. Anything else is kind `text`.

The body is trimmed; an empty one, or no argument for a command that requires
one, is a `usage` error. A body over 131072 bytes is cut at a UTF-8 boundary,
ends with `[... input truncated at 131072 bytes ...]`, sets
`input.truncated` and raises `input_truncated`.

Reading an issue can fail: a 404 without a token is `failed`/`auth`; a 404
with one is `failed`/`forge`, not retryable; a rate limit, a server error or
a lost connection is `failed`/`forge`, retryable.

## needs_human and --context

A command that cannot go on without a decision returns status `needs_human`
and exits 3. The envelope says what it needs and how to answer:

```json
"needs_human": {
  "question": "Which service is affected?",
  "options": [
    { "id": "A", "label": "api" },
    { "id": "B", "label": "web" }
  ],
  "resume": {
    "argv": ["af", "example", "the login page crashes"],
    "flag": "--context"
  }
}
```

`options` are lettered `A`, `B`, … in order (at most 26); none means free
text is expected. `resume.argv` is the invocation as it was received, with
`argv[0]` replaced by `af`. Run it with `--context <answer>` appended to
resume. A command that opts into `--context` takes it any number of times;
the values reach the command verbatim and in order, and answers accumulate
because the earlier `--context` values stay in `argv`. An empty value is a
`usage` error. What an answer such as `B` means is up to the command.

One input cannot be resumed this way: if the invocation used `--` to give an
input that begins with a dash, `resume.argv` ends with that input, and a
`--context` appended after the `--` is read as part of it. Put the answer
before the `--` in that case.

## --dry-run

A command that opts into `--dry-run` and is given it gets a forge client that
reads and does not write. The reads (`Authenticated`, `Close`,
`GetRepository`, `ReadIssue`, `ListIssues`, `ListComments`,
`ReadPullRequest`, `ReadChangedFiles`, `GetPRState`, `GetCIChecks` and
`GetPRReviews`) pass through. Each write (`CreateIssue`, `UpdateIssue`,
`CloseIssue`, `AddComment`, `AddLabels`, `RemoveLabel`, `CreateLabel`,
`CreatePullRequest`, `PostReviewComment`, `MergePullRequest` and
`ClosePullRequest`) sends nothing and returns an error wrapping
`toolio.ErrDryRun`, and the envelope lists it as a side effect:
`{kind: <method in snake_case>, target: <owner/repo#number, or owner/repo>,
state: "planned"}`. Because it is the one client the shell hands out, a test
at an HTTP server shows that no write request reaches the forge whatever the
command does.

The run directory is created under `--dry-run` too: it is local, ignored
state, and the brief that a dry run exists to show is written there. A command
that opted into `--dry-run` is told so (`Run.DryRun`) even when it uses no
forge. The shell enforces `--dry-run` for the forge only; a command that
commits or pushes honours the flag itself.

## --timeout and signals

A command that opts into `--timeout` runs under that deadline; the deadline
covers the command, not the reading of its input. When it runs out, the
command's context is cancelled, and the envelope is `failed`/`timeout`
whatever the command returns after that. SIGINT and SIGTERM cancel the
command's context in the same way; if the command then returns an error the
envelope is `failed`/`cancelled`, and a command that returns an outcome has
finished its job, so its outcome stands. A command that has not returned 10
seconds after being cancelled, by a signal or by its deadline, is abandoned:
the shell prints `failed`/`cancelled` or `failed`/`timeout` itself and exits
1. A second signal during those 10 seconds is ignored.

## --schema

`af --schema` prints the JSON Schema (draft 2020-12) of the envelope: every
field, `schema_version` a constant, `required` listing the fields in envelope
order, `result` open. The result schema of each registered command is under
`$defs`, keyed by the command path with spaces replaced by `_`. `af
<command> --schema` narrows it to the command: `tool` is a constant, and
`result` is null or a reference to the command's schema.

The base schema is `internal/toolio/schema/envelope.schema.json`, embedded in
the binary. A test fails if its `required` list and the fields of
`toolio.Envelope` differ in names or in order, and the output is compared
with two committed files: `internal/toolio/testdata/schema.golden.json` (no
commands registered) and `cmd/af/testdata/schema.golden.json` (the binary).
Rewrite both after a change on purpose with

```sh
go test ./internal/toolio ./cmd/af -update
```

(`go test ./... -update` fails in the packages that do not define the flag;
see `docs/errata/05_update_flag.md`.)

## Interface versions

`schema_version` is the version of the envelope's interface: its fields and
their types. It is separate from the build, which `af --version` reports. A
caller compares the major number with the one it was written for before it
reads the rest of the envelope.

Within a major version a change is additive only. These need no new major
version: a new top-level field; a new field on an existing object; a new value
in a field documented as an open set (a warning `code`, an error `category`,
an artifact `kind`, an event `type`). These are breaking, and each
bumps the major version: removing a field, renaming it, changing its type, or
narrowing a field documented as a closed set (`status`, `exit_code`,
`input.kind`, `side_effects[].state`, `usage.cost_source`). A change on purpose changes
`toolio.SchemaVersion`, the `const` in
`internal/toolio/schema/envelope.schema.json` and the two golden files
together; a test fails until they agree.

`5.0.0` is the envelope of the `af` binary. The earlier `triage`, `fix` and
`impl` tools printed an envelope that reached 4.0.0, and this one is not
compatible with it: it removes `ok`, `version`, `session_id`, `model`,
`timings`, `untrusted_fields` and `report_file`, renames and retypes fields
inside `needs_human`, `error`, `side_effects`, `artifacts` and `next`, and
always prints every field. `docs/errata/05_old_envelope_fields.md` lists the
differences.

## Help

`-h`, `--help`, `af help` and `af help <command>` print plain text on stdout
and exit 0: the commands, or one command's synopsis and flags. It is the only
output that is not JSON. `af help <command>` for a command that does not
exist is a `usage` envelope, like any unknown command.

## The run directory

A command that declares one gets `<dir>/.agent-fox/run/<id>/` before it runs,
with `<dir>` the `--dir`. It is created once the command is about to run, so
an invocation that is refused leaves nothing behind. `<id>` is
`YYYYMMDD_HHMMSS_<6 lowercase hex>`: the UTC time and three random bytes,
drawn again if the directory exists, for up to five ids in all. The envelope's `run_id` is
the id, and its artifacts list `{kind: "run_dir", path}`.

`<dir>/.agent-fox/.gitignore` contains `*`, so Git ignores everything under
`.agent-fox/`, itself included. It is created, opened exclusively so that runs
starting together cannot clobber one another, only when it is missing; one
that is there is left as it is, and the repository's own `.gitignore` is never
edited.

```
<dir>/.agent-fox/
  .gitignore          "*\n", created if it is missing; the repository's own .gitignore is never edited
  run/<id>/
    envelope.json     the envelope, byte for byte what stdout got
    events.jsonl      the event stream
    ledger.jsonl      what the command recorded (empty or absent unless it records)
    report.md         if the command wrote a report; listed as {kind: "report", path}
    panic.txt         if the command panicked
```

### Events

`events.jsonl`, and the file `--emit-events` names (to which the lines are
appended), hold one JSON object per line, written as the event happens:

```
{"seq": <n>, "ts": <RFC 3339>, "run_id": <id or null>, "type": <type>, "data": <any>}
```

`seq` counts from 1; `run_id` is null without a run directory. The shell
emits `run_start` (`{tool, argv, input_kind}`) first, a `warning`
(`{code, message}`) for each warning, and `run_end` (`{status, exit_code,
duration_ms}`) last, also for an invocation that was refused before its
command ran. A command emits its own types (`step`, `tool_call`,
`heartbeat`, …) through `Run.Emit`.

### The ledger

`ledger.jsonl` is append-only: `{seq, ts, kind, data}` per line, `seq` from 1.
A command records what it measured with `RunDir.Record(kind, data)` and reads
it back with `RunDir.Ledger()`; `toolio.OpenRunDir(dir, id)` opens an
existing run, for the commands that carry one on, and refuses an id that is
not of the form of a run id. The shell records nothing in the ledger.
