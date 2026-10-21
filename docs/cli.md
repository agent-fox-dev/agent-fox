# The af command line

`af` is the one binary of agent-fox. `make build` writes it to `bin/af`
(`go build ./cmd/af` builds it too). Every command of `af` speaks the same
contract, which the shell in `internal/toolio` implements once so that the
commands do not: one JSON envelope on stdout, a fixed table of statuses and
exit codes, coded warnings, a stop on a question that is resumed with
`--context`, a `--dry-run` that cannot write to the forge, a fetchable
schema, and a git-ignored run directory.

**This release registers sixteen commands: [`lang detect`](#lang-detect),
[`gate run`](#gate-run), [`gate compare`](#gate-compare),
[`revert-check`](#revert-check), [`guard check`](#guard-check) and
[`map`](#map), the five of [triage](#triage): `triage`, `triage brief`,
`triage run`, `triage submit` and `triage land`, and the five of [fix](#fix):
`fix`, `fix brief`, `fix run`, `fix submit` and `fix land`.** The binary also
answers `--version`, `--schema` and `help`; `af` with no arguments, or with a
command it does not know, prints a `usage` envelope and exits 2. The other
commands of later specs (`impl`, …) register on the shell and get everything
below.

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
It reads files. In a git work tree it runs `git rev-parse` and `git ls-files`
to list them and nothing else. It builds no forge client, calls no model, opens
no network connection and creates no run directory, and without `--write` it
writes nothing at all. The default `--timeout` is 1 minute.

| Flag | Meaning |
|---|---|
| `--write` | Store the freshly detected gate table in `.agent-fox/gate.json` (see [configuration](configuration.md)). Any `gate.json` there now is ignored, one that does not parse included, and replaced. The file is written through a temporary file and a rename, and is an artifact of kind `gate_file`. |

The result (its schema is `lang_detect` under `$defs`) holds:

| Field | Meaning |
|---|---|
| `languages` | `[{name, files, manifest?}]`: each language with its file count, most files first and the name breaking a tie. The files are the file view (git's tracked and untracked-not-ignored files in a work tree, a directory walk elsewhere, never `.git/` or `.agent-fox/`), classified by extension as `go`, `python`, `javascript`, `typescript`, `rust`, `java`, `kotlin`, `ruby`, `c`, `c++`, `c#`, `php`, `scala`, `lua` or `bash`. `manifest` is the file at the root that marks the project, when there is one. Empty for an empty directory. |
| `gate` | `{source, checks}`: the effective gate table. `source` is `file` when `.agent-fox/gate.json` gave it and `detected` otherwise. Each check is `{name, argv, kind, source, available}`: `argv` is the command with no shell; `kind` is `lint`, `test` or `check` (both); `source` is where it came from (`Makefile:check`, `go.mod`, `gate.json`); `available` says whether `argv[0]` can be run on this machine. |
| `suite` | The command forms that count as the full test suite: the gate's checks, `make check` and `make test` where the Makefile has those targets, and the forms of each language. |
| `programs` | The programs a gate may run: the first word of each check, the toolchain of each language found, and the first word of each check a manifest at the root gives, whether or not there are files of its language (`cargo` for a `Cargo.toml` with no `.rs` file). |

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

Which checks are selected, and their order and argv, depend on the file view
and on nothing else of the machine: not on which programs are on `PATH`. Only
`available` does. (The file view is git's list when `git` can be run in a work
tree and a directory walk when it cannot, so a machine without `git` sees the
ignored files too, and may select other checks.)

| Outcome | When |
|---|---|
| `done` | Always, when the repository could be read. A program that is not available is a warning, `check_unavailable`, once per check. |
| `failed`/`config` | `.agent-fox/gate.json` cannot be used: it cannot be read, does not parse, has a field that is not described in [configuration](configuration.md#gatejson), is followed by more than the one document, names a version other than 1, or holds a check that cannot run (no `argv`, a repeated `name`, a `kind` that is not `lint`, `test` or `check`). The message names the file, and the Makefile is not used as a fallback. |
| `failed`/`io` | `--dir` cannot be read or is not a directory, or `gate.json` cannot be written. |

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

### gate run

```
af gate run [--dir DIR] [--label LABEL] [--hermetic] [--no-cache] [--timeout DURATION]
```

Runs the checks of the gate table (the `gate` of [lang detect](#lang-detect))
on the tree in `--dir` and reports what they said, or returns the result
stored for that tree. It builds no forge client, calls no model, opens no
network connection and creates no run directory; it writes only under
`.agent-fox/` (making the directory and its `.gitignore` first, so that
nothing it writes shows in `git status`), and with `--hermetic` in a temporary
home in the system's temporary directory. The default `--timeout` is 30
minutes.

| Flag | Meaning |
|---|---|
| `--label <label>` | Name the result: the result's `label`, and `labels.json` (below) maps the label to the result's id, on a hit as well as after a run. A label is a lower-case letter and up to 31 lower-case letters, digits, `_` or `-`, and is not spelled like an id (12 hex characters); anything else is a `usage` error before any check runs. |
| `--hermetic` | Run the checks with a clean environment: a temporary `HOME` that is removed afterwards, whether the run completed, failed or was cancelled (a read-only directory in it, such as Go's module cache, included), no git configuration and no credential prompt. A download cache that already exists in the real home (Go's, Cargo's, npm's, uv's and pip's) is used where it is; one that does not exist is made in the temporary home and goes with it. |
| `--no-cache` | Run the checks even when a result is stored for this tree, and replace it. |

**How the checks run.** The checks that can be run (`available`) run one
after the other, in table order, in `--dir`. They run with the credential
variables of the environment stripped, as the hands' `ReducedEnv` strips them
(the variables of the model vendors and clouds by prefix, such as `ANTHROPIC_`,
`OPENAI_`, `AWS_`, `GOOGLE_` and `AGENTKIT_`, and anything that ends in
`_TOKEN`, `_SECRET`, `_KEY`, `_PASSWORD`, `_PASS`, `_PWD`, `_PAT`, `_AUTH` or
`_CREDENTIALS`; `PATH`, `HOME`, `LANG`, `TMPDIR`, `TERM`, `AWS_PROFILE`,
`AWS_REGION`, `AWS_DEFAULT_REGION` and `GOOGLE_CLOUD_PROJECT` stay), because a
suite is repository code. A check that is not available is skipped, not run, and is
listed in `skipped` with the reason `unavailable`. Each check has 20 minutes;
one that takes longer is stopped, with everything it started, and has the
status `timeout`, and the checks after it still run. The output of each check,
standard output and standard error together, is written in full to
`.agent-fox/gate/<id>/NN-<slug>.log`, with `NN` the two-digit position among
the checks that ran and `<slug>` the check's name. The logs are written when
the run is complete, just before its result: a run that is stopped writes none,
and the logs of a stored result that it would have replaced stay as they were.

**The result** (its schema is `gate_run` under `$defs`):

| Field | Meaning |
|---|---|
| `id` | The first 12 hex characters of the cache key (below). The result is stored in `.agent-fox/gate/<id>/`. |
| `tree` | The hash of the tree the checks ran on: a SHA-256 over the file view in order, each file as its kind, its executable bit, its path and the SHA-256 of its content (a symlink as its target, any other file as its type, an unreadable file as a fixed marker). It depends on the content of the files in the view and on nothing else: not on `HEAD`, not on where the checkout is, not on ignored files. A nested repository or a submodule is one entry, a directory, and what is in it does not count. The view is git's in a work tree and a walk elsewhere (see [lang detect](#lang-detect)), so the same files hash alike only when they are listed the same way: a tree that is hashed in a box without `git` and outside it with `git` is two trees. |
| `table_source` | `file` or `detected`: where the table came from. |
| `hermetic` | Whether the checks ran with `--hermetic`. |
| `label` | The label asked for; `""` for none. |
| `cached` | True when the result was stored and no check ran. |
| `passed` | True only when at least one check ran and every check that ran passed. |
| `checks` | `[{name, status, exit_code, duration_ms, log, tail}]` for the checks that ran. `status` is `pass` (exit code 0), `fail` (another exit code), `error` (the program could not start; `exit_code` is `-1` and the log says why) or `timeout` (`exit_code` is `-1`). `log` is relative to `--dir`. `tail` is the last 40 lines of the output, at most 4096 bytes. |
| `skipped` | `[{name, reason}]` for the checks that did not run. |

| Outcome | When |
|---|---|
| `done`, exit 0, `passed: false` | A check failed, errored or timed out. A red gate is a measured fact, not a failure of `af`. |
| `done`, exit 0, `passed: false`, warning `no_checks` | No check of the table can be run here, or the table is empty. Nothing ran. |
| `done`, warning `tree_changed_during_gate` | The tree hash taken after the checks differs from the one taken before. The result is kept under the earlier hash's id, and its `tree` is the earlier hash. |
| `failed`/`timeout` or `failed`/`cancelled` | `--timeout` ran out, or a signal arrived. The process group of the running check is killed, and neither the logs nor the result are stored. |
| `usage` | The label is not allowed. Nothing ran. |
| `failed`/`config` | `.agent-fox/gate.json` cannot be used (see [lang detect](#lang-detect)). |
| `failed`/`io` | The tree cannot be read, or a log, the result or `labels.json` cannot be written. |

**The cache.** The key is the SHA-256 of the tree hash, the canonical JSON of
the table's checks (`name`, `argv` and `kind` of each, with the keys in order
and no whitespace, and not `source` or `available`, which are not about what
is measured) and the runner mode, `reduced` or `hermetic`. The same tree and
the same table give the same key on every machine, and so a gate that ran in
a box is a hit for the gate that runs outside it. `result.json` is written
after the logs, through a temporary file and a rename, so that runs that start
together without a lock leave the last writer's complete file and never half
of one. Only a run that completed is stored.

- A stored result for the key is returned with `cached: true` and no check
  runs, unless `--no-cache` is given, or the result skipped a check that can
  be run here (it was made where its program was missing), or `result.json` is
  not a result of this key. Then the checks run and the result is replaced.
- A change of one byte in a file of the view makes the next run another key. A
  change confined to ignored files does not.

**Labels.** `.agent-fox/gate/labels.json` is `{"<label>": "<id>"}`, written
through a temporary file and a rename. The latest result for a label replaces
the earlier one, and the file is read again by every run, so labels set by
one run are there for the next. Runs that start together take turns at it: a
lock file, `labels.json.lock`, is held for the time a read and a write take, so
that neither loses the other's label. A lock older than ten seconds was left by
a process that was killed and is taken over, and a run that cannot get the lock
in five seconds goes on without it: a label is worth a short wait and never a
failed run. A file that is damaged starts again empty. `gate/<id>/` is safe to
delete, but a label that names a deleted id makes `gate compare` fail with
`usage` until the label is set again.

### gate compare

```
af gate compare --before LABEL|ID --after LABEL|ID [--dir DIR] [--timeout DURATION]
```

Compares two stored gate results check by check, and says whether the second
is a regression of the first. It runs no check and reads only
`.agent-fox/gate/`. The default `--timeout` is 30 seconds. Each argument is
resolved as a label in `labels.json` first, and as a gate id second.

| Flag | Meaning |
|---|---|
| `--before <label\|id>` | The result to compare from. Required. |
| `--after <label\|id>` | The result to compare to. Required. |

The verdict of each check, matched by name, is in this table; `fail`, `error`
and `timeout` all count as "not pass":

| Before | After | Verdict of the check |
|---|---|---|
| pass | pass | `pass` |
| not pass | pass | `pass_was_already_failing` |
| pass | not pass | `regressed` |
| not pass | not pass | `still_failing` |

The verdict of the comparison is the first of `regressed`, `still_failing`,
`pass_was_already_failing` and `pass` that any check has, so one check fixed
and another broken is a regression. When either result has no checks nothing
was measured and the verdict is `unverified`. `landable` is true for `pass`
and `pass_was_already_failing` only.

The result (its schema is `gate_compare` under `$defs`) holds `before` and
`after`, each `{id, label, passed}` (the label is the argument when it was a
label, else a label in `labels.json` that names the result, the first by name,
or `""` if none does), `verdict`, `landable` and
`rows`, `[{name, before, after, verdict}]` in the order of the `--before`
result (none when the verdict is `unverified`). The status is `done` and the
exit code 0 for every verdict, `unverified` included. `usage` (exit 2) means
the comparison could not be made: an argument is missing or names no result
(the message names the flag and the argument), or the two results were not
measured by the same checks (the message lists the names that are on one side
only).

### revert-check

```
af revert-check --since REF [--dir DIR] [--timeout DURATION]
```

Answers one question: do the tests of a change fail without the rest of the
change? It lists what differs from `REF`, sets the paths that are not tests
back to what `REF` has, runs the tests, and puts every path back as it was.
It calls no model and opens no network connection, and it creates no run
directory. The default `--timeout` is 30 minutes.

| Flag | Meaning |
|---|---|
| `--since <ref>` | The commit the change starts from: a ref or a hash. Required. |

`--dir` must be the top level of a git work tree and `--since` must resolve to
a commit; otherwise the status is `usage` (exit 2), the message names the
argument, and nothing was touched.

**What it does,** in this order, stopping at the first step that settles the
outcome:

1. It refuses, as `failed`/`restore`, if a directory exists under
   `.agent-fox/revert/`: an earlier check was killed, or another is working on
   the checkout, and nothing is overwritten. The message names the directory.
2. It lists the changed paths: those that differ from `REF`, staged or not,
   and the untracked files that are not ignored, byte for byte (a rename is
   its two paths). Af's own listing does not write the index, not even
   with git's refresh of its stat data: it runs against a copy of it. Each path is a
   *test path* or not: Go `_test.go` or `testdata/`; Python `test_*.py`,
   `*_test.py` or `conftest.py`; JavaScript and TypeScript `*.test.*`,
   `*.spec.*` or `__tests__/`; Java `src/test/`, `*Test.java`, `*Tests.java`
   or `*IT.java`; Ruby `*_spec.rb` or `*_test.rb`; and anything below a
   directory called `test`, `tests` or `spec`.
3. It returns the outcome of a case with nothing to measure, and changes no
   file: `no_changes` (nothing differs), `no_code_changes` (only tests do),
   `no_test_changes` (no test does) or `inconclusive` with the reason
   `no_test_check` (the gate table has no check of kind `test` or `check`).
4. It takes the full table's result for the current tree from the gate cache,
   or runs it and caches it (see [gate run](#gate-run)); `after` is its id.
   If a `test` or `check` check did not pass, the outcome is `inconclusive`
   with the reason `after_not_green`; if none could be run here, with
   `test_check_unavailable`. Nothing is reverted.
5. It backs up, reverts, tests and restores. The backup is
   `.agent-fox/revert/<id>/`, a directory this check made and no other did
   (`<id>` is 12 random hex characters; the directory is the claim on the
   checkout, and a check that finds another claim beside its own backs off and
   takes away only its own): `files/` holds a copy of every file it will
   change, `manifest.json` says what each path was (a file with its mode and
   modification time, a link with its target, or absent), and `index` is the
   index as it was. Then, without touching the index, each non-test path that
   the commit has gets the commit's content and mode (read with `git cat-file`
   from the blob `git ls-tree` names), and each one it does not have is moved
   aside; test paths stay as they are. The table of checks is found again on the
   reverted tree: if its `test` and `check` checks are not the ones that were
   measured (a Makefile the change added is not there to run), the outcome is
   `inconclusive` with the reason `table_changed_by_revert`, because a check
   that cannot run says nothing about the tests. Otherwise only the checks of
   kind `test` and `check` run on that tree, through the gate engine, cached by
   the reverted tree's hash.
6. It restores every path from the backup, with a context that nothing
   cancels, so that a signal, `--timeout` or a panic during the tests still
   puts the tree back. A path that something changed while the checks ran, and
   that is not what the revert put there, is not overwritten: what it held is
   kept in the backup under `changed/` and the command fails with
   `failed`/`restore`, naming it. A check that ran git or a build may have
   refreshed and rewritten `.git/index` with the same entries and other
   bytes; the bytes of the index as it was are written back, through
   `.git/index.lock` as git writes it. The command then checks that the tree
   hash equals the one taken before the revert and that `.git/index` is byte
   for byte the same, and removes the backup.

The checks run on the real index, not on a copy: a `git status` or a build in a
check may rewrite it, and the restore puts it back as it was. A restore is
given ten minutes after a signal or the deadline (the shell's own grace is 10
seconds, and this command asks for more), and the signals are SIGINT, SIGTERM
and SIGHUP. A process that is killed with SIGKILL, or that is abandoned after
that, leaves the backup, and the next check refuses until someone has looked
at it.

A directory (a nested repository) or a path where the commit has a directory
or a submodule is not reverted. The checks must not leave files of their own
in the tree outside the ignored paths: a file made while the reverted tree is
tested changes the tree hash, and is a restore failure although every
reverted path is back.

**The result** (its schema is `revert_check` under `$defs`):

| Field | Meaning |
|---|---|
| `outcome` | `proven`, `not_proven`, `inconclusive`, `no_changes`, `no_code_changes` or `no_test_changes`. |
| `reason` | Why, as a code. For `proven`: `tests_fail_without_the_change`. For `not_proven`: `tests_pass_without_the_change`. For `inconclusive`: `no_test_check`, `after_not_green`, `test_check_unavailable`, `reverted_check_error` (a check could not start on the reverted tree, so it measured nothing), `table_changed_by_revert` (the reverted tree has other test checks than the ones that were measured) or `nothing_to_revert`. For the others: `no_paths_changed`, `only_test_files_changed` or `no_test_files_changed`. |
| `closes` | True only for `proven`: a check of kind `test` or `check` failed or timed out on the reverted tree. |
| `since` | The commit `--since` resolved to, in full. |
| `test_files` | The changed test paths. |
| `reverted_files` | The paths that were set back to the commit; empty when nothing was reverted. |
| `after` | The id of the gate result for the current tree; `""` when it was not measured. |
| `reverted` | `{id, passed, checks}`, the gate result of the reverted tree, as in [gate run](#gate-run); null when nothing was reverted. |
| `restored` | True when the tree and the index are as they were, which is so for every result: a tree that could not be put back is `failed`/`restore` and has no result. |

| Outcome | When |
|---|---|
| `done`, exit 0 | Every outcome above, `not_proven` and `inconclusive` included. Every outcome except `proven` and `no_changes` also raises the warning `fix_not_proven`. |
| `usage` | `--dir` is not the top level of a work tree, or `--since` is missing or is not a commit. |
| `failed`/`restore` | A backup from an earlier check is there or another check is working, the tree could not be put back or came back as another tree, or a path was changed while the checks ran and its content is kept. The backup is kept in `.agent-fox/revert/<id>/`, the message names it, and nothing further is reverted. |
| `failed`/`git` | git could not be run, or failed. |
| `failed`/`config` | `.agent-fox/gate.json` cannot be used. |
| `failed`/`timeout`, `failed`/`cancelled` | `--timeout` ran out or a signal arrived. The tree has been put back first, within the ten minutes the command is given for it; if that takes longer the shell abandons the command (see [--timeout and signals](#--timeout-and-signals)) and the backup stays. |
| `failed`/`io` | A file could not be read or written. |

### guard check

```
af guard check [--dir DIR] [--policy FILE] [--host claude] [--timeout DURATION]  < payload.json
```

Decides whether a brain may make one tool call, from a JSON payload on stdin.
The same decision is made for the envelope, for a host's hook and, in
process, for the loop (`policy.DecideAt`). It reads and decides; it changes
nothing, calls no model and opens no network connection, and creates no run
directory. The default `--timeout` is 10 seconds.

| Flag | Meaning |
|---|---|
| `--policy <file>` | The policy to use instead of `.agent-fox/guard.json`; see [configuration](configuration.md#guardjson). The file must exist. |
| `--host claude` | Answer in the format of Claude Code's `PreToolUse` hook instead of the envelope. |

**The payload** is one JSON document of at most 1 MiB. It is read as it is, not
as the positional input of other commands (`input.kind` is `none`): it is not
trimmed or cut at 128 KiB. Two shapes are read:

- a Claude Code hook payload, `{"hook_event_name", "tool_name", "tool_input",
  "cwd", …}`; the other fields are ignored;
- af's neutral form, `{"tool": "...", "input": {...}}`.

A payload whose `hook_event_name` is there and is not `PreToolUse` is allowed
with no opinion; an empty or blank name names no other event, and the call is
judged. `cwd` is the directory the session is in: a relative path
means what it means there, and in `--dir` (the checkout) as well.

**The decision** covers the calls to three kinds of tool:

- *Shell tools* (`Bash`, and the hands' `execute`). The command is read as a
  shell would read it, and every command it runs is judged: those joined by
  `&&`, `||`, `;`, `|`, `&` and newlines, those in groups, in `$(…)`,
  backticks (whose body is unescaped as the shell does before it is read) and
  `<(…)`, in `$((…))`, `$[…]` and `((…))` where they hold substitutions, in
  heredoc bodies that expand them, and in the script of `sh|bash|zsh -c '…'`
  (whatever options come before the script, `--rcfile` and `-o` with their
  values included), `eval`, `trap` and the commands `find` executes with
  `-exec`. Words are read as the shell passes them: `$'git'`, `$"git"`,
  `g\it`, `{git,push}` and `git${IFS}push` are `git` and `push`, and the case
  of a program's name does not matter (`GIT`). Assignments (`FOO=1 cmd`), the
  wrappers `env` (and the command line its `-S` splits), `time`, `timeout`,
  `nice`, `nohup`, `command`, `builtin`, `exec` and `xargs`, `function NAME` and
  `coproc`, and the keywords that start a command are skipped to find the
  program. The rules apply in this order to each command, and the first
  denial wins, except that the hands' guard looks at the whole line before any
  of them.
- *Tools that write a file* (`Write`, `Edit`, `MultiEdit`, `NotebookEdit`, and
  the hands' `write_file` and `edit_file`; the path is `file_path`,
  `notebook_path` or `path`, whichever is the tool's own). A path under a
  protected path is denied (`protected_path`), and so is one outside `--dir`
  that is not under the system temporary directory (`outside_dir`). A call
  that names no path is denied (`unparseable`).
- *Every other tool* is allowed, with no rule: the host's own tool allowlist
  decides which tools exist.

| Rule | Denies |
|---|---|
| `unparseable` | A command with unbalanced quotes, an unterminated `$(`, `${` or `$[`, nested more than 64 deep, a brace expansion that makes more than 4096 words, longer than 64 KiB (the hands' guard takes time that grows with the square of the length, so a longer line is not read; write long content with a tool that writes files), or a shell call that has no command; and a shell that reads its commands from its input (`echo git push \| sh`, `sh <<< '…'`, `bash -s`, `bash` alone, `source <(…)`), which runs what cannot be read before it runs. |
| `hands_guard` | What the hands' guard refuses, with its reason: an assignment of a variable that changes which program runs or what is loaded into it (`PATH`, `LD_PRELOAD`, `BASH_ENV`, …), a line it cannot read, and a line that runs no program. It can only deny. |
| `git_mutation` | `git` with any subcommand but `status`, `log`, `diff`, `show`, `blame`, `ls-files`, `ls-tree`, `rev-parse`, `rev-list`, `grep`, `describe`, `cat-file`, `shortlog`, `merge-base`, `name-rev`, `for-each-ref` and `show-ref`; `branch` and `remote` only to list; a global `-c`, `--config-env` or `--exec-path=`; configuration from the environment that makes git run a program (`GIT_CONFIG_*`, `GIT_EXTERNAL_DIFF`, `GIT_EXEC_PATH`, `GIT_ASKPASS`, `GIT_SSH`, `GIT_SSH_COMMAND`, `GIT_PROXY_COMMAND`, `GIT_EDITOR`, `GIT_SEQUENCE_EDITOR` and `GIT_TEMPLATE_DIR`, in front of the command, given to `env` or exported by an earlier command of the line); and `git grep` with `-O` or `--open-files-in-pager`. The reason says that af makes every commit, branch and push. |
| `suite_command` | A command equal to a suite form once every token that starts with `-`, the value of an option that takes one as its next word (`go test -timeout 5m ./...`, `make -j 4 check`, `make -f Makefile check`: the options of `go`, `make`, `cargo` and `pytest` that the code lists) and every `NAME=value` word (`make check V=1`) is removed from both, and `$(go list ./...)` is `./...`: `make check`, `make -j4 check`, `go test ./...` and `go test -count=1 ./...` are the suite, `go test ./internal/x -run TestY` is not. The reason says to run `af gate run`, which caches the result by tree hash so that the landing reuses it, and that targeted runs are allowed. A flag that selects tests without a word of its own (`mvn -q test -Dtest=X`) is not told from the suite. |
| `af_command` | Any `af` command but `lang detect`, `gate run`, `gate compare`, `revert-check`, `map`, `guard check`, `triage brief`, `triage submit`, `fix brief`, `fix submit`, `--version` and `--schema`: the rest belongs to the loop. |
| `program_not_allowed` | With `allow_programs` set, a program that is not on it, as it is written (`/usr/bin/ls` is not `ls`); `af` and `git` are always allowed, by their name and by an absolute path (`./git` is a file of the checkout), and so are the builtins that start no program (`cd`, `true`, `[`, `test`, `echo`, `printf`, `read`, `export`, `set`, `for`, `case`, `eval`, `trap` and the like), whose own commands are judged in turn. |
| `protected_path` | A write into a protected path through an output redirection; through an argument of `rm`, `rmdir`, `unlink`, `shred`, `mv`, `cp`, `install`, `rsync`, `tee`, `touch`, `mkdir`, `mkfifo`, `truncate`, `chmod`, `chown`, `chgrp`, `patch`, `ln` (every operand: a link to a protected path is a way to write there), `sed -i` or `perl -i`, `dd of=`, or `find -delete` and `-fprint`; and through an option that names the file a command writes (`-o`, `--output`, `--out`, `--outfile`, `--output-file`, `--emit-events`, `-coverprofile` and the other profiles of `go test`), such as `git diff --output=.specs/x` or `af map --output .specs/x`. A path is resolved against `--dir` (and against the directory of the session and of any `cd` before it in the command), with its links followed, so that a link to a protected directory and another spelling of the checkout are the protected path; its case does not matter, because the file system of a Mac has `.SPECS` where it has `.specs`; and it may start with `~`, `$HOME` or `$PWD`, or have a glob in a component (`.spe?s`, `.sp*`), which is matched against the protected names as the shell would (a glob does not match a name that starts with a dot unless it starts with one). Entries of `protected_paths` are cleaned before they are matched: `./docs/`, `docs//` and `a/../docs` protect `docs`. `rm`, `mv` and `find -delete` also refuse a directory above a protected path, such as `rm -rf .` and `find . -delete`; a `find` with a test before `-delete` does not count as removing its roots. |
| `outside_dir` | A write tool call to a path outside `--dir` and the system temporary directory. |

The policy is prevention, not a boundary. It reads the command and does not
run it: a program named by a variable (except `$IFS`, which splits at once), a
shell function or an alias that is named like a program, a script a command
writes and then runs, a program that runs others (`ssh`, `sudo`, `watch`), a
variable that was set earlier in the line and used later in a path, and what
an interpreter writes (`python -c`, `awk`) are out of its sight, and the tool
`run_command` (which takes an argument list) is not a shell tool here. A line
that the hands' guard cannot read (an assignment on its own, a function
definition, `((i++))`, which run no program) is denied, as well as one that is
dangerous. The gate and the box are what holds.

**Without `--host`** the answer is the envelope: `done` and exit 0 for an
allow and for a deny, with the result (its schema is `guard_check` under
`$defs`) `{decision, rule, reason, tool, segment}`. `rule`, `reason` and
`segment` are empty for an allow. A payload that is malformed, empty or over 1
MiB is `usage` (exit 2); a policy file that cannot be used is `failed`/`config`
(exit 1). Neither emits a decision.

**With `--host claude`** stdout is the document Claude Code reads, and nothing
else, and the exit code is 0 for every answer:

```json
{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"…"}}
```

for a deny, and `{}` for an allow. `{}` is no opinion: the host's own
permission settings still apply. The hook never prints `permissionDecision:
"allow"`, which would bypass them. Neither document ends in a newline. Every
error is answered as a deny that carries it, so that a hook that cannot decide
does not let the call through: a payload that is malformed, empty or over 1
MiB, a policy that does not parse, a panic of the policy engine, and a hook
that does not decide within 80% of `--timeout` (the shell throws away what a
command returns after its deadline, and reports a failure that the host does
not act on). Only a wrong invocation (an unknown flag, an unknown `--host`, a
`--timeout` that is not a duration) is still an envelope with exit 2, which the
host treats as blocking. A `--timeout` too short for the command to finish in
(`1ns`) is an envelope with `failed`/`timeout` and exit 1, which the host does
not act on, so a hook leaves it at the default.

Without `.agent-fox/guard.json` or `--policy`, every call detects the
repository to find the suite (the work of [lang detect](#lang-detect)); a loop
that calls the hook often writes `guard.json` once.

### map

```
af map [--dir DIR] [--tokens N] [--timeout DURATION]
```

Renders a map of the repository that fits a token budget, for a brief to carry:
the directories, the files in them and, as far as the budget allows, the
declarations in the files. It reads files and runs nothing, calls no model,
opens no network connection, creates no run directory and writes nothing. The
default `--timeout` is 2 minutes.

| Flag | Meaning |
|---|---|
| `--tokens <n>` | The budget, in tokens, estimated as the bytes of the map divided by 4, rounded up. From 200 to 100000, default 4000; any other value is a `usage` error (exit 2) that names the range. |

The map is of the file view of `--dir` (see [lang detect](#lang-detect)). Each
directory that has files is a header, `path/` (the root is `./`), in path
order: the root first, a directory before its subdirectories, and the files of
each by name. A file is indented two spaces, and its declarations follow it,
each as `<kind> <name> L<line>` and joined by ` · `:

```
./
  README.md
  main.go: func main L3 · type Server L5
pkg/
  util.go: func Helper L3
  util_test.go: func TestHelper L3
```

The declarations come from the hands' outline: Go files always, and the other
languages it knows (Python, JavaScript, TypeScript, Rust, Java, Kotlin, C#, Ruby,
C, C++, PHP, Scala, Lua and shell) when the binary is built with cgo, as `make
build` does by default. A kind is `func`, `method`, `type`, `class`,
`interface`, `enum`, `trait`, `const`, `var`, `module` or `macro`, and a method
is named with its type (`Server.Close`). A file the outline cannot read is
listed by name only and is not counted in `files_outlined`: a language it does
not know, a binary file, one it cannot parse, and one over 1 MiB, which is not
offered to it. A control character in a name is written as `?`, and bytes that
are not text (not valid UTF-8) as the replacement character U+FFFD, so that the
map is the same text once it has been encoded in the envelope.

The map is the first level of this ladder whose output fits the budget:

| Level | What the map holds |
|---|---|
| 1 | Every file, with every declaration. |
| 2 | The same, but a test file (see [revert-check](#revert-check)) has no declarations. |
| 3 | A test file has none, and any other file has its first 20, 10, 5, 2 or 1 declarations, the first of these caps that fits, followed by `· … +N more`. |
| 4 | Every file by name only. |
| 5 | Directories only, each as `path/ (N files)`, N being the files directly in it. |
| 6 | As many of those lines as fit, and then `… N more directories`. |

The outliner is not called at all when the names alone do not fit the budget
(level 4 is too large) or the view has more than 5000 files. The warning
`map_outline_skipped` says so, `files_outlined` is 0, and the map is level 4 or
a later one.

**The result** (its schema is `map` under `$defs`) holds `map` (the text),
`tokens` (the budget), `estimated_tokens` (the size of the map, never more than
the budget), `level`, `files_total`, `files_outlined` (the files whose
declarations were read, which the map shows only at levels 1 to 3: from level 4
on it holds none, and the number is still the files that were read) and
`truncated` (true for any level above 1). The same tree and the same budget give the same bytes, in
whatever order the files are read: a brief that carries the map can rely on it
being the same on every attempt. The status is `done`.

### Brains and boxes

The agent commands (`af triage` and `af fix` in this release, `af impl` in a later
one) run a brief through a *brain* in a *box* and take back one submission. The
pieces they are built from are in `internal/brain`, `internal/box` and
`internal/submission`, and this is the contract they share. [`af triage`](#triage)
and [`af fix`](#fix) are the commands that use them so far.

**The shared flags** (`brain.Flags`, registered on a command's flags with the
defaults that command supplies):

| Flag | Meaning |
|---|---|
| `--brain <name>` | `agentkit` (the AgentKit driver; inside the box under `landlock` and `podman`, on the host over the box's tools under `local` and `none`), `scripted` (a replay of a script, with no model; see [development.md](development.md#the-scripted-brain)) or `claude` (headless Claude Code; see [The claude brain](#the-claude-brain)). An unknown name is a `usage` failure that lists the registered ones. |
| `--sandbox <provider>` | The box provider: `landlock`, `podman`, `local` or `none` (see [The providers](#the-providers)). Without it the provider is the one `AF_SANDBOX` names, and without that `landlock` on Linux and `podman` on any other operating system. An unknown name is a `usage` failure that lists all four; an unknown `AF_SANDBOX` is a `usage` failure that names the variable. When the default provider cannot open, `af` does not fall back to `local` or `none`: the `sandbox` failure carries a `fix_hint` that names `--sandbox` and `AF_SANDBOX`. |
| `--borrowed-hands` | Only with `--brain agentkit`: run the driver on the host over the box's tools, and not inside the box. It changes something only under `landlock` and `podman`, since `local` and `none` always run the driver on the host. With any other brain it is a `usage` failure. |
| `--budget <usd>` | Stop the brain after this much model cost. A finite number above 0. It needs a model whose price the catalog knows: with another, the `agentkit` brain fails as `usage` and names `AF_MODEL`, since no cost could be counted against it. |
| `--max-turns <n>` | Stop the brain after this many turns. At least 1. |
| `--script <file>` | The script of `--brain scripted`, and only with it. |

A value that is out of range, `--brain scripted` without `--script`, `--script`
with another brain and `--borrowed-hands` with another brain than `agentkit` are
`usage` failures (exit 2) that name the flag.
`Flags.Validate` and `Registry.New` report them, with a script that cannot be
used (it is read whole: text after its object is refused) and a missing
credential for the model, and a command that calls them first finds all of
these before a box opens.

**The box** holds the tree. The checkout goes in with its `.git` (it must be the
top level of a git work tree that keeps its own `.git` directory: a path inside
one, a linked worktree and a submodule are `usage` failures that say so, since a
commit made in a box that held one would write the repository it was linked to).
The files that the checkout's ignore rules ignore (a `.env`, build output) stay
out, except the ones git tracks, which go in all the same so that git in the box
sees what git on the host sees. The tree comes out again without `.git` into
`af-export-<run_id>-*` under the temporary directory of the OS, which `Close`
removes.

Git writes and the credentials stay on the host: a commit made in the box is
not on the host's branch, and what could carry a credential in the box's
`.git/config` (the URLs of the remotes, extra headers, credential helpers, URL
rewrites) is taken out when the box is opened and again after each sync. The
environment of the box's tools is what the provider gives them, which for
`local` is the host's `PATH`, `LANG` and `TERM` and a `HOME` and `TMPDIR` of the
box's own, and the host values of the names the brain asks for where it runs
(none for a brain on the host, and `ANTHROPIC_API_KEY` for one that runs as a
process): no forge token is among them, and no key but the model vendor's. The
`local` provider runs the tools in this process, with its privileges: it has no
process or network isolation, and every box opened raises the warning
`box_unconfined` once. A box is closed after a result, a failure, a panic of the
brain and a cancellation; a close that fails is the warning `box_close_failed`.

#### The providers

| Provider | Isolates | In place | What it is |
|---|---|---|---|
| `landlock` | yes | no | A confined child process on a copy of the tree. Linux only, and only with Landlock ABI 4 or newer, the first with rules for the network. On another operating system the status is `failed`/`sandbox`, not retryable, with the message "landlock needs Linux"; on a Linux with an older ABI, or none, the message names the ABI found and the one needed. Both carry `fix_hint` `{flag: "--sandbox", suggest: "podman"}`. A process started inside runs from the host's file system, so its binaries are the host's. |
| `podman` | yes | no | A container of the image `AF_SANDBOX_IMAGE`, or `quay.io/agentfox/tools:latest` without it (see [configuration.md](configuration.md#the-box)). An engine that cannot be reached is `failed`/`sandbox`, not retryable, with the error of the provider (and, off Linux, a pointer to `podman machine start`); an image that is missing and cannot be pulled is `failed`/`sandbox`, retryable, and the message names the image and `AF_SANDBOX_IMAGE`. |
| `local` | no | no | The tools run in this process, with its privileges, on a copy of the tree. |
| `none` | no | yes | No box: the brain works on the checkout itself. |

`landlock` and `podman` isolate, and `none` works in place. Every box obeys the
rules above (`.git` in and never out, the commit on the host); `none` obeys them
by other means, since there is no copy: its session has the hands' tools over the
checkout, a read-only view of it, and `Sync` copies nothing. Its `Export` is an
internal error ("in place: nothing to export"), so that a caller that forgets to
branch fails loudly. The run files of the box are written to the checkout under
`.agent-fox/`, and an `.agent-fox/.gitignore` that is there is left as it is. The
host's `submission.json` is the box's file, so `brain.Execute` keeps the bytes of
the one the host last accepted, and moves a submission it refuses to
`submission.rejected.<attempt>.json` and puts the kept bytes back: under `none` too
`<run dir>/submission.json` holds a submission the host accepted and nothing else.
`box_unconfined` is raised once for a run on `local` or on `none`; for `none` it
says that the brain works on the checkout itself, with no copy of it and no box
around it, and that the hook is the only guard.

#### Where the brain runs

A brain is on the host, in the box, or at the checkout:

| Brain | `landlock`, `podman` | `local` | `none` |
|---|---|---|---|
| `scripted` | host | host | host |
| `agentkit` | box; with `--borrowed-hands`, host | host | host |
| `claude` | box | box | checkout |

*Host* is `af`'s process over the box's tools. *Box* is a process that `af` starts
in the box with one command (below). *Checkout* is such a process on the host, in
the checkout. What reaches the box follows from this, and is the same in the
ledger entry `box_open {provider, image, network, placement}` and, for `af fix`,
in the result's `box`:

- **Credentials.** `Spec.Env` is nothing for a brain on the host, and for a process
  the host's value of `ANTHROPIC_API_KEY`, if it is set. A process also gets the
  few settings listed [below](#brains-that-run-as-a-process), and nothing else of
  the host's environment.
- **Network.** `none` for a brain on the host that has no tool that runs a command
  (every tool of `af triage` only reads), and `open` otherwise: a process in the box
  needs its vendor, and a gate that fetches modules needs the network. Podman has
  all of it or none, so a brain in a box on a developer's machine has the whole
  network. Under `local` and `none` nothing is enforced, and the network is recorded
  as `host`.
- **Image and lifetime.** The image is `podman`'s only. The box lives until the
  deadline of the command and ten minutes more.

#### Brains that run as a process

`claude` runs as a process, and so does the `agentkit` driver inside `landlock` and
`podman`: `af <agent> run --run-id <id> --dir <root> --budget … --max-turns …
--timeout …`, with `AF_MODEL` given on the command line. `Session.Exec` runs a
command in the box, at its root, through the box's `execute` tool (and with `sh -c`
on the host, at the checkout, under `none`), and returns the exit code and the last
bytes of the output; a command that exits with a code is not an error.

- **The probe.** Once for each box, before the first process, `af --version` runs in
  it, and `claude --version` for `claude`, and `HOME` is checked to be set and
  writable. A failure is `failed`/`sandbox`, not retryable, and the message names
  the binary, the provider and the image. Each probe is the ledger entry
  `box_probe {binary, ok, version}`. When the `af` of a `podman` image has another
  version or commit than the running one, the warning `box_af_version_mismatch` is
  raised and the run goes on: the host checks every submission again.
- **The binaries.** Under `local`, `landlock` and `none`, `af` is the running
  executable, by its absolute path, and its directory is first on `PATH`; `claude` is
  the one on this machine's `PATH`. Under `podman` both are the image's.
- **The environment.** The command runs under `env -i` with `PATH`, `HOME`, the ones
  of `LANG`, `LC_ALL`, `TZ`, `TMPDIR`, `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY`
  (in either case), `SSL_CERT_FILE`, `SSL_CERT_DIR` and `NODE_EXTRA_CA_CERTS` that
  are set, and the credentials of `Spec.Env`, which the command refers to by name so
  that no secret is in its text or in the ledger. Every argument is single-quoted.
- **Input and output.** The brief is on its standard input. Its standard output and
  error are redirected to `.agent-fox/run/<id>/<brain>.<attempt>.json` and
  `.stderr` in the box, and copied to the run directory (the output up to 1 MiB, the
  last 64 KiB of the errors) as the artifacts `brain_output`. Nothing in them decides
  whether there is a submission. The ledger entry is `brain_process {brain,
  exit_code, duration_ms}`.
- **The time.** The command is killed, with its process group, when the time of the
  brief or the time left on the command runs out.

#### The claude brain

`--brain claude` runs Claude Code headless in the box (or at the checkout, under
`none`) on the brief, and takes back one submission, which it makes with `af <agent>
submit --run-id <id>`: the briefs of such a brain name that command, not the tool.

- **Construction.** Before a box opens: except under `none`, where a person at their
  own checkout may be logged in, `ANTHROPIC_API_KEY` must be set (`failed`/`auth`,
  with `fix_hint` `{env: "ANTHROPIC_API_KEY"}`), since a box has a private `HOME`
  with no login; and, except under `podman`, `claude` must be on this machine's
  `PATH` (`failed`/`brain`, not retryable).
- **The files.** For each attempt `af` places `guard.json` (version 1, with the
  `protected_paths` and `allow_programs` of the brief's hook, and no `suite`, which
  is then detected) and `claude-settings.json` in `.agent-fox/run/<id>/` of the box,
  with copies in the run directory. The settings have one `PreToolUse` hook that
  matches every tool: `af guard check --host claude --dir <root> --policy
  <root>/.agent-fox/run/<id>/guard.json`, with a timeout of 30 seconds, so that the
  hook and the in-process gate decide from the same rules.
- **The command line.** `claude -p --output-format json --max-turns <n>
  --permission-mode dontAsk --settings <settings> --strict-mcp-config --allowedTools …
  --disallowedTools … --append-system-prompt <note>`, with `--model` when `AF_MODEL`
  is set. The note says that the run is headless, that the only deliverable is one
  submission, that nothing is committed or pushed, and that a refusal is the next
  turn. The skill is not appended again: the brief, which carries it, is the prompt.
- **The tools.** The tools of the brief map to those of Claude Code, from one table:
  `read_file` to `Read`, `list_files` to `LS` and `Glob`, `find_files` to `Glob`,
  `search_files` to `Grep`, `write_file` to `Write`, `edit_file` to `Edit` and
  `MultiEdit`, and `execute` to `Bash`. A tool with no equivalent is left out and
  noted in the ledger (`claude_tool_dropped`). `Bash(af <agent> submit:*)` is always
  allowed; `WebFetch` and `WebSearch` are always denied, and so are `Write`, `Edit`,
  `MultiEdit` and `NotebookEdit` for an agent whose brief has no tool that writes.
- **The outcome.** The cost is `total_cost_usd` (else `cost_usd`) of the output, with
  `cost_source` `reported`; the turns are `num_turns`; the input tokens are the sum of
  `input_tokens`, `cache_creation_input_tokens` and `cache_read_input_tokens`, and
  the output tokens `output_tokens`. The stop reason follows `subtype`: `success` is
  `ended`, `error_max_turns` is `turns`, one that contains `budget` is `budget`, and
  anything else is `error`. The brain claims a submission when the box's
  `submission.json` carries this attempt's nonce, and the host decides from the file
  all the same. The ledger entry is `claude_result`. An output that is missing or does
  not parse is cost 0, `cost_source` `none` and the warning `brain_usage_unavailable`.
  A non-zero exit with no submission is an error that carries the last 2 KiB of the
  errors.
- **The budget.** The cost is known only after the run, so `--budget` is checked then:
  a cost above it raises the warning `budget_exceeded` (and, when nothing was
  submitted, the stop reason `budget`). While the run goes on, `--max-turns` and the
  timeout are the bounds.

The host reads the box through its tools: it lists a directory with the list tool
(at most 500 entries) and takes a file out by exporting that file, so that any
bytes come out exact, at most 32 MiB of them. A symbolic link is not followed.

**The submission.** A brain hands back one JSON object, which af validates
against the tree and writes before it exists: `.agent-fox/run/<id>/submission.json`
in the box, `{kind, run_id, attempt, nonce, submitted_at, payload_sha256,
payload}`, with the attempt and the nonce of `run.json`, which the loop places
before each brain run. The brains of this release submit through the submit core
(`submission.Submit`); a brain with hands of its own could write the file by
other means, so the host decides from the file and trusts nothing about how it
got there. After the brain, the host reads that file (at most 2 MiB) and decides
from it, and from nothing the brain said or printed: it must parse, carry this
run's kind, run id, attempt and nonce, have a `payload_sha256` that is the hash
of its payload, and the payload must pass what a submission passes (at most 1
MiB, valid UTF-8, one JSON object, and the kind's own check against the box's
tree, in which a panic is a problem with the payload). Then the host writes
`<run dir>/submission.json` from what it checked and nothing else the file held
(no extra field, no second `payload`). A file left by an earlier attempt (another
nonce) is not this attempt's, and counts as no submission. The last valid
submission wins; a refused one never replaces anything. A run directory that has
no accepted submission has no `submission.json`: the one an earlier attempt
accepted is removed when the next attempt starts, and one that anything but af
wrote there is removed (the warning `submission_not_removed` if it cannot be).
Because the `local` box has no isolation, a process of the brain that outlives
its run could still rewrite the file; consumers take the payload from the result
of the run, or check the file's `payload_sha256` against the ledger's
`submission_accepted`. A valid submission wins over an error of the brain and
over a panic of it, which is the warning `brain_error` (the stack of a panic is
kept either way). Without one, the run fails as `timeout`, `brain`, `budget` or
`no_result` (see the [categories](#error-categories)).

**The tool gate.** Every tool call that the driver of an in-process brain passes
to its guard is decided before it runs: the tool must be in the brief's
allowlist (the tool that ends the run always is), and the rules of
[`guard check`](#guard-check), with the brief's hook as their configuration and
the suite of the checkout, must allow it. A path that a write names is judged as
the box's tools will resolve it (quotes, a leading `@`, a `file://` prefix and
the spaces around it are taken off first). A denied call is not executed, and its
reason is the brain's next turn. A call to a tool that does not exist, or with
arguments that do not fit the tool's schema, is answered by the driver and never
reaches the gate, so it is not in the transcript. The brief's hook always
protects `.agent-fox/`, where only af places the brief, `run.json` and the
submission, but the guard is prevention and not a boundary: it reads a command
and not what a program it allows goes on to write, so a script can write where a
redirect would be refused. What holds is the host's decision above, which
accepts only a file that is valid.

### The submit command of an agent

`submission.Command(path, kind)` builds the `af <agent> submit` command that a
brain with its own hands runs inside the box.

```
af <agent> submit --run-id ID [--dir DIR]  < payload.json
```

The payload is the JSON object on stdin (at most 1 MiB), `--dir` the tree (the
current directory by default, opened so that no link in it leads out of it) and
`--run-id` the run, which must exist under `--dir` (a missing or malformed id is
`usage`, exit 2). Accepted, the status is `done` with the result `{run_id, kind,
path, sha256}` (the file, absolute, and the hash its `payload_sha256` holds) and
the artifact `{kind: "submission", path}`. Refused, malformed JSON included, the
status is `failed`, the category `submission`, `retryable` is true, the result is
null, the message names every problem, and the exit code is 1; nothing is
written and an earlier valid submission stays. The command makes no run
directory and takes no `--dry-run`. Its result schema is under `$defs` of `af
--schema`, as `<agent>_submit`.

### triage

`af triage` diagnoses a report and writes the diagnosis back to the forge. It is
a read-only brain run: the brain reads the checkout, hands over one diagnosis,
and `af` checks it, and does every write itself.

```
af triage <issue|file|text|-> [--dir DIR] [--context ANSWER]... [--repo owner/name]
          [--land issue|none] [--label L]... [--dry-run]
          [--brain NAME] [--sandbox NAME] [--borrowed-hands] [--budget USD] [--max-turns N] [--timeout 15m]
af triage brief  <issue|file|text|-> [--dir DIR] [--context ANSWER]... [--repo owner/name]
af triage run    --run-id ID [--dir DIR] [--budget USD] [--max-turns N] [--timeout 15m]
af triage submit --run-id ID [--dir DIR]                          < diagnosis.json
af triage land   --run-id ID [--dir DIR] [--land issue|none] [--label L]... [--repo owner/name] [--dry-run]
```

The loop, `af triage`, is the four steps below in one process. The other four
commands are the steps one at a time, for a person at their own checkout and for
a box that executes a brain: they share a run directory (see
[below](#the-files-of-a-triage-run)), and `run`, `submit` and `land` take the
`--run-id` that `brief` (or the loop) made. A missing, malformed or unknown
`--run-id` is a `usage` failure (exit 2) that writes nothing and sends nothing
to the forge.

| Command | Takes | Default `--timeout` |
|---|---|---|
| `triage` | the input; `--dir`, `--context`, `--repo`, `--land`, `--label`, `--dry-run`, `--brain`, `--sandbox`, `--borrowed-hands`, `--budget`, `--max-turns`, `--script` | 15 minutes |
| `triage brief` | the input; `--dir`, `--context`, `--repo` | 2 minutes |
| `triage run` | `--run-id`, `--dir`, `--budget`, `--max-turns` | 15 minutes |
| `triage submit` | `--run-id`, `--dir`; the diagnosis on stdin | 30 seconds |
| `triage land` | `--run-id`, `--dir`, `--land`, `--label`, `--repo`, `--dry-run` | 2 minutes |

Triage does not need a clean working tree. It reads the tree as it is, runs no
git write, and creates nothing in the checkout outside `.agent-fox/`.

| Flag | Meaning |
|---|---|
| `--land issue\|none` | Where the diagnosis goes. `issue` (the default) writes it to the forge: it replaces the body of the issue that was given, or creates one for any other input. `none` writes `diagnosis.md` in the run directory and nothing else. |
| `--label <label>` | Add this label to the issue; may be repeated. A label is 1 to 50 characters, with no comma and no space at either end; there may be 10 distinct ones, and exact duplicates are collapsed in the order they came. Not with `--land none`. |
| `--repo <owner/name>` | For an input that is not an issue: the repository the issue is created in. For `land`: it must be the repository the run recorded. |
| `--context <answer>` | An answer to the question that an earlier run stopped on; see [Questions](#questions-and-answers). |
| `--dry-run` | Run the brain as usual and send no write to the forge; the writes are planned, not made. |
| `--brain`, `--sandbox`, `--borrowed-hands`, `--budget`, `--max-turns`, `--script` | The [shared flags](#brains-and-boxes). `--brain` defaults to `agentkit`, `--budget` to `2` (dollars) and `--max-turns` to `100`; `--sandbox` has no default of its own (`AF_SANDBOX`, else the platform's). |

#### The loop

1. **Preflight.** The checks that need no model run in this order, before a
   brief is rendered or a brain is made, and the first refusal ends the run
   with nothing rendered, no request to the model and no write to the forge.
   1. *The flags*: the brain and the box provider exist, `--script` goes with
      `--brain scripted`, `--budget` is a finite number of dollars above 0,
      `--max-turns` is at least 1, `--land` is `issue` or `none`, and the labels
      are as above. A violation is `usage` and names the flag or the label.
   2. *The input*: the URL of a pull or merge request is `usage` (triage takes an
      issue), and so is a text, a file or standard input that is not valid UTF-8
      (a brain and a forge take text). An issue that is closed raises the warning
      `issue_closed`, and the run goes on.
   3. *The landing* (`--land issue`). With an input that is not an issue and no
      repository to create the issue in, `usage`, naming `--repo` and `--land
      none`. Without `--dry-run`: an unauthenticated forge is `failed`/`auth`
      with the message "creating or updating an issue in <repo> needs a
      credential: set GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN, or pass --dry-run
      or --land none" and `fix_hint.env` `GITLAB_TOKEN` for a GitLab host,
      `GITHUB_TOKEN` otherwise; and the repository must be readable: a missing
      credential for it, or one the forge refuses (a 401 or a 403 that is not a
      rate limit), is `auth`, a repository that is not found is `forge` and not
      retryable, a rate limit or a lost connection is `forge` and retryable, and
      an archived repository is `forge`, "repository is archived: <repo> takes no
      new issue or edit". Last, a
      text, file or stdin report that cannot become the body of an issue whole is
      `usage`: the shell cut it, or its length plus 16896 bytes (the most the
      diagnosis and the framing can take) is more than the forge allows in a body
      (65536 bytes on GitHub, 1048576 on GitLab). The message gives both numbers
      and suggests `--land none`.
2. **The brief** is rendered as [`triage brief`](#triage-brief) does, for a brain
   that submits through its tool. See [model-usage.md](model-usage.md#triage) for
   what it holds.
3. **The brain** runs the brief in a box over the checkout, as
   [`brain.Execute`](#brains-and-boxes) runs any brief, with these in the
   `Brief`: the read-only tools `read_file`, `list_files`, `find_files`,
   `search_files`, `file_outline`, `find_symbol` and `find_references`, and
   `code_search` when the box offers it (the tool that ends the run,
   `file_diagnosis`, whose input is the [diagnosis](#the-diagnosis), is not among
   them: it is the tool of the submission kind, which the brain adds itself); the
   hook of a read-only run; `--budget` and `--max-turns`; and the time left on
   `--timeout`. A name the box does not offer is dropped, and no tool that writes
   or runs is ever offered. A refused call of `file_diagnosis` is the brain's
   next turn. A path the diagnosis cites in the form the box knows it by (its
   workspace, as an error of a tool may show it) is made relative to the
   repository.
4. **The landing.** When the brain ends, `rejections.jsonl` is brought out of the
   box into the run directory, and the host reads the submission it accepted and
   checks it against its own tree once more: a file that is gone, or a line that
   is past the end of a shorter file, makes it no submission. Then the diagnosis
   is [landed](#triage-land).

A run that ends with no valid submission lands nothing and writes nothing to the
forge: `failed`/`no_result` when the brain stopped for another reason (retryable),
and `failed`/`budget` when it spent its cost or its turns, with `fix_hint`
`{flag: "--budget"` or `"--max-turns", suggest: <twice the value>}`. The other
failures of a brain are those of [Brains and boxes](#brains-and-boxes).

The envelope's `usage` is the brain's. The `step` events are `preflight`,
`brief`, `brain` and `land`, and a run emits them up to the one it ended in.

**The result** of the loop and of `land`:

```json
{
  "run_id": "20261020_180425_8202d5",
  "issue": {"repo": "owner/name", "host": "github.com", "number": 7, "url": "https://github.com/owner/name/issues/7"},
  "action": "replaced_body",
  "diagnosis": {"title": "…", "severity": "high", "confidence": "probable", "root_cause": "…",
                "affected_files": [{"path": "main.go", "line": 6, "note": "…"}],
                "suggested_fix": "…", "acceptance_criteria": ["…"]},
  "rejected_path_calls": 0,
  "rejected_submissions": 0,
  "labels": ["triaged"],
  "original_report_sha256": "…"
}
```

| Field | Meaning |
|---|---|
| `issue` | The issue the diagnosis was written to, or would have been in a dry run; `null` when nothing was, and in a dry run of a text or file report, which has no issue to name. |
| `action` | `replaced_body` (the body of the issue was replaced and the original report kept), `commented` (the body would have been too long, so the diagnosis went in one comment and the body was left), `created_issue`, `planned` (a dry run) or `none` (`--land none`, or a question). |
| `diagnosis` | The submission as `af` normalized it. |
| `rejected_path_calls` | How many calls of the brain were refused because they cited a path that is no file. |
| `rejected_submissions` | How many calls were refused in all. Both counts are reported and decide nothing. |
| `labels` | The labels added, in the order they came, each once; none in a dry run, where they are planned (a side effect) and not added. |
| `original_report_sha256` | The SHA-256 of the original report the body holds. |

**Statuses.** A landing, `--land none`, a dry run and a diagnosis that is
`not_reproducible` without a question are `done` (exit 0). A submission that
carries a question is `needs_human` (exit 3; see
[Questions](#questions-and-answers)). No valid submission, a spent bound, a
failure of the brain or the box, and a landing write that failed are `failed`
(exit 1) with `result` null; a refusal of the preflight is `usage` (exit 2) or
`failed`/`auth` or `forge` (exit 1), as above. A failed envelope still lists the
artifacts `{kind: "brief"}`, `{kind: "submission"}` and `{kind: "diagnosis"}` that
exist (a run refused by the preflight has no brief).

**`next`.** After a `done` with an `issue` in its result (the issue the diagnosis
was written to, or would have been in a dry run) whose diagnosis is `confirmed`
or `probable`: `af fix <issue url> --dir <dir>`. After a landing of the loop that
failed with a valid submission: `af triage land --run-id <id> --dir <dir>`, with
`--repo <owner/name>` when the input was an issue URL.

**Warnings** the triage commands raise besides the shell's and those of
[Brains and boxes](#brains-and-boxes): `issue_closed`,
`report_changed_during_triage`, `body_too_long`, `labels_failed`,
`original_report_unparsed` and `rejections_unreadable`
(see [Warning codes](#warning-codes)); `input_truncated` is the shell's, and is
raised again when the report is cut in the brief. No failure category is new.

### triage brief

`af triage brief <input>` resolves the input, makes a run directory and renders
the brief of a run, for a person to read and for the steps that follow. It calls
no model and writes nothing to the forge. It refuses what the loop's preflight
refuses of the input (a pull or merge request, a text that is not UTF-8), warns of
a closed issue, and takes `--context` (see
[Questions](#questions-and-answers)) and `--repo`, and the forge client of the
shell for an issue.

It writes `brief.md`, `original.md` and `triage.json` into the run directory,
records `triage.input` and `triage.brief`, and lists the artifact `{kind:
"brief", path}`. The result:

| Field | Meaning |
|---|---|
| `brief` | The path of `brief.md`. |
| `prefix_bytes`, `prefix_sha256` | The size and the SHA-256 of the part of the brief that is the same for every run over this tree and this report. |
| `document_bytes`, `estimated_tokens` | The size of the brief, and its size in tokens estimated as bytes divided by 4, rounded up. |
| `map` | `{level, truncated}`: how much of the repository map the brief holds. |
| `report_truncated` | The report was cut in the brief. |
| `previous_question` | The brief holds the question of an earlier run that `--context` answers. |

`next` suggests `af triage submit --run-id <id> --dir <dir>` and `af triage land
--run-id <id> --dir <dir>`. The brief it renders names `af triage submit` as the
way to submit, where the loop's names the `file_diagnosis` tool.

### triage run

`af triage run --run-id ID` runs the `agentkit` brain in this process over `--dir`,
with no box, on the `brief.md` of the run: it is what a box executes. The tools
work on `--dir` itself, and `file_diagnosis` submits into the run directory under
it. `--budget` and `--max-turns` are the loop's. The result is `{run_id,
submitted, rejected_path_calls}`, and the status is `done` when a diagnosis was
accepted; the envelope's `usage` is the brain's. A run with no `brief.md` or no
`triage.json` is `usage`, "run `af triage brief` first", except in a box that was
started by [a process brain](#brains-that-run-as-a-process): `brain.Execute` places
there the `brief.md` and a `run.json` that also holds `prefix_bytes` (the length of
the brief's prefix), and the command takes the brief and the prefix from them, and
the attempt and the nonce of the host, and needs nothing else of the host's run
directory.

It reads the directory as it is, with no copy of it: a box keeps out the files that
git ignores (a `.env`) and takes the credentials out of `.git/config`, and a
directory in place does neither, so the brain's tools can read both, and what they
read goes to the model. The envelope says so with the warning `box_unconfined`.
Run it in the box that executes it, or where you are content for the model to read
everything in the directory; the loop works on a copy. It also replaces `brief.md`
with the brief it ran (the way to submit and the bounds are those of a brain that
has the tool), which has the prefix `brief` reported.

### triage submit

`af triage submit --run-id ID` reads a diagnosis on stdin and submits it: it is
[the submit command of an agent](#the-submit-command-of-an-agent) for the
`triage` kind. The diagnosis is checked against the files of `--dir`, and
stored in its normalized form (paths cleaned, `path:12` split into the path and
the line) in `submission.json`; `run.json`, which the protocol reads the attempt
and the nonce from, is made when the run has none. The result is `{run_id,
accepted, submission, rejected_path_calls}`, and `triage.submission` is recorded.
A refusal lists every problem, each with the JSON pointer of its field, writes no
submission and leaves an earlier valid one as it was; it appends one line
`{ts, problems, unresolved_paths}` to `rejections.jsonl`.

### triage land

`af triage land --run-id ID` writes the submission of a run to the forge, and can
be repeated: it writes the same bytes again, and a diagnosis that went in a
comment is not posted a second time.

1. A run with no valid submission, or one that no longer passes the diagnosis
   rules against the files as they are, is `failed`/`no_result`, and nothing is
   written. A submission that carries a question lands nothing (see
   [Questions](#questions-and-answers)).
2. The rendered body is written to `diagnosis.md`, listed as `{kind:
   "diagnosis", path}`, before any write to the forge. With `--land none` that is
   all; the action is `none`.
3. For an **issue** the body is read again. The person's original report is taken
   from the body as it is now (if it differs from the one the brief had, the
   warning `report_changed_during_triage` is raised and the current one is kept),
   and the body is replaced with the diagnosis around it (`replaced_body`). When
   that body would be longer than the forge allows, the diagnosis alone goes in
   one comment, the body is left, and the warning `body_too_long` is raised
   (`commented`). Then the labels are added: a label write that fails raises the
   warning `labels_failed` and changes nothing else.
4. For any **other input** an issue is created in the repository the run
   recorded, with the title of the diagnosis, the body and the labels
   (`created_issue`); a later landing of the same run updates that issue and does
   not create another.
5. Each write is a side effect (`update_issue`, `add_comment`, `create_issue` or
   `add_labels`, with its target and its state `done`, `planned` or `failed`), a
   landed issue is the artifact `{kind: "issue", url, number}`, and `triage.land`
   is recorded.

A failure of a write is `failed`/`auth` without a credential or with one the forge
refuses (a 401 or a 403 that is not a rate limit), `forge` and retryable after a
rate limit or a lost connection, and `forge` and not retryable otherwise, a server
error included. With `--dry-run` the writes are planned and not made; the action is
`planned`.

`land` builds its own forge client for the repository the run recorded
(`triage.json`), including its host, with the construction, the `User-Agent` and
the dry-run decoration of the shell's own client, and closes it when it returns.
`--repo` must be that repository: another one is a `usage` failure that names
both, and builds no client. It repeats the credential and repository checks of
the loop's preflight before its first write.

#### The body of the issue

`af` renders the body from the diagnosis, byte for byte the same every time:

```
<!-- agent-fox:triage v1 run=<run_id> -->
## Diagnosis

**Severity:** <severity> · **Confidence:** <confidence>

<root_cause>

## Affected files

- `<path>:<line>` — <note>

## Suggested fix

<suggested_fix>

## Acceptance Criteria

- [ ] <criterion>

## Question

<text>

A. <option>

<sub>Diagnosed by agent-fox `af triage` (run <run_id>). The original report is kept below.</sub>

## Original report

<details>
<summary>Original report</summary>

<!-- agent-fox:original-report:begin -->
<the original report, as the person wrote it>
<!-- agent-fox:original-report:end -->

</details>
```

A section with nothing in it is left out; `## Diagnosis` and `## Original report`
are always there. The first line, the headings and the two markers are the
contract that `af fix` reads. The original report is exactly what stands between
the begin marker and the newline before the end marker, so triaging an issue that
was triaged already keeps the first report, byte for byte, and replaces the old
diagnosis. A body that does not start with the triage marker is wholly the
original. One that does is read with the original between the markers; a body that
a browser saved with CRLF line endings is read as if it had LF endings, and its
original has them. One that starts with the marker and is not what `af` wrote (a
first line that is not the marker, markers that cannot be paired, a heading that is
missing, or any text after the original report but the close of its details block)
is wholly the original, with the warning `original_report_unparsed`: a person's
words are not thrown away.

#### The diagnosis

One JSON object of at most 65536 bytes, with exactly these fields (an unknown
field is a problem):

| Field | Rule |
|---|---|
| `title` | Required; one line of 1 to 120 characters. |
| `severity` | `critical`, `high`, `medium` or `low`. |
| `confidence` | `confirmed`, `probable`, `suspected` or `not_reproducible`. |
| `root_cause` | Required; 1 to 6000 bytes. |
| `affected_files` | 1 to 30 items of `{path, line?, note}`; `note` at most 300 bytes; `line` an integer from 1 to the number of lines of the file. |
| `suggested_fix` | 1 to 6000 bytes. |
| `acceptance_criteria` | 1 to 15 strings of 1 to 300 bytes. |
| `question` | Optional `{text, options}`; `text` 1 to 2000 bytes; `options` none, or 2 to 6 of 1 to 200 bytes. Only with the confidence `suspected` or `not_reproducible`. |

With the confidence `not_reproducible`, or a `question`, `affected_files`,
`suggested_fix` and `acceptance_criteria` may be empty. A path names a regular
file of the checkout as the file view of the repository has it (the tracked files
and the untracked files that are not ignored, less those deleted from the working
tree); a leading `./` is taken off, an absolute path under the checkout (or under
the workspace of the box the brain works in) is made relative, and `path:12` is
read as the path and the line when no `line` is given. A path that is outside the
checkout, has `..`, is under `.git/` or `.agent-fox/`, is a directory, is a link
that leads out of the checkout or to something that is no file, is the directory of
another repository (a submodule, a nested checkout) or is no file of the view is a
problem that names the path and up to three files with the same name. A string of
nothing but white space is empty, and a title is one line (no CR and no LF). No
string may hold a NUL byte or the text `<!-- agent-fox:`, and the diagnosis as the
body renders it, without the original report, is at most 16384 bytes. Every
problem is collected before the answer, each with the JSON pointer of its field
(`/affected_files/2/path`), up to 50, and a last line says how many were left out.

#### Questions and answers

A diagnosis with a `question` lands nothing: the status is `needs_human` (exit
3), `needs_human` carries the question and its options lettered in order, the
result is that of a landing with the action `none`, no comment is posted, and
`triage.question {text, options}` is recorded. The shell's `resume.argv` with
`--context <answer>` appended runs the loop again. The new brief has the same
`prefix_sha256`, since everything that differs is in the task block at its end:
the question of the earlier run, found by the fingerprint of the input among the
run directories under `--dir` (when none is found, the answers stand alone), and
each `--context` value in order, as the answer of a person. A run directory that
a person drives step by step answers the same way: `af triage brief <input>
--context <answer>`. For `af triage land` the resume argv is the shell's own, and
so it names `land`, which takes no `--context`: answer by running `af triage` or
`af triage brief` on the input again.

#### The files of a triage run

| File in `.agent-fox/run/<id>/` | Written by | What it is |
|---|---|---|
| `brief.md` | the loop, `brief` | The brief that was rendered. |
| `original.md` | the loop, `brief` | The original report, exactly. |
| `triage.json` | the loop, `brief` | `{format_version: 1, input: {kind, origin, fingerprint}, target: {repo, host}, issue: {repo, host, number, url} or null, original_report_sha256, prefix: {bytes, sha256}}`. The fingerprint is `owner/repo#N` for an issue, else the SHA-256 of the input. |
| `submission.json` | the brain, `submit` | The accepted diagnosis (see [Brains and boxes](#brains-and-boxes)). |
| `rejections.jsonl` | the brain, `submit` | One line `{ts, problems, unresolved_paths}` for each refused call. |
| `diagnosis.md` | `land` | The rendered body. |

The ledger of a run holds `triage.input {kind, origin, fingerprint}` when the
input is resolved, `triage.brief {brief, prefix_bytes, prefix_sha256,
document_bytes}` when the brief is written, `triage.submission {submission,
sha256}` when a diagnosis is accepted, `triage.question {text, options}` when it
carries a question, and `triage.land {action, issue, labels}` when a landing
ends, in that order for a full loop, among the entries of the box and the brain.

### fix

`af fix` fixes a reported problem: a verified change on a branch of its own, and a
pull request. The brain edits files in a box and hands over one report; `af`
does every other effect, and measures the change before it lands.

```
af fix <issue|file|text|-> [--dir DIR] [--base BRANCH] [--context ANSWER]... [--repo owner/name]
       [--land pr|branch|none] [--attempts N] [--no-verify] [--dry-run]
       [--brain NAME] [--sandbox NAME] [--borrowed-hands] [--budget USD] [--max-turns N] [--timeout 45m]
af fix brief  <issue|file|text|-> [--dir DIR] [--base BRANCH] [--context ANSWER]... [--repo owner/name]
af fix run    --run-id ID [--dir DIR] [--brain NAME] [--sandbox NAME] [--borrowed-hands] [--budget USD] [--max-turns N] [--timeout 45m]
af fix submit --run-id ID [--dir DIR]                                   < report.json
af fix land   --run-id ID [--dir DIR] [--land pr|branch|none] [--timeout 45m]
```

The loop, `af fix`, is the steps below in one process. The other four commands are
the steps one at a time, for a person at their own checkout and for a box that
executes a brain: they share a run directory (see
[below](#the-files-of-a-fix-run)), and `run`, `submit` and `land` take the
`--run-id` that `brief` (or the loop) made. A missing, malformed or unknown
`--run-id`, and a run directory without `fix.json` (a triage run's, for one), is a
`usage` failure (exit 2) that writes nothing and sends nothing to the forge.

| Command | Takes | Default `--timeout` |
|---|---|---|
| `fix` | the input; `--dir`, `--base`, `--context`, `--repo`, `--land`, `--attempts`, `--no-verify`, `--dry-run`, `--brain`, `--sandbox`, `--borrowed-hands`, `--budget`, `--max-turns`, `--script` | 45 minutes |
| `fix brief` | the input; `--dir`, `--base`, `--context`, `--repo` | 45 minutes |
| `fix run` | `--run-id`, `--dir`, `--brain`, `--sandbox`, `--borrowed-hands`, `--budget`, `--max-turns`, `--script` | 45 minutes |
| `fix submit` | `--run-id`, `--dir`; the report on stdin | 30 seconds |
| `fix land` | `--run-id`, `--dir`, `--land` | 45 minutes |

| Flag | Meaning |
|---|---|
| `--land pr\|branch\|none` | Where the change goes. `pr` pushes the branch and opens a pull request; `branch` pushes it; `none` keeps it on a local branch and sends nothing to the forge or the remote. |
| `--attempts <n>` | Up to `n` attempts, each with a budget of its own; from 1 to 5. An out-of-range value is a `usage` failure before anything is read. |
| `--no-verify` | Land without the gate: no baseline and no check after, a draft pull request that says `Refs`, the status `unverified` and the warning `not_verified`. |
| `--base <branch>` | The branch to fix on; it must be the one that is checked out, since a fix is made on the base as it stands. |
| `--context <answer>` | An answer to the question that an earlier run stopped on; see [Questions](#questions-and-answers). |
| `--repo <owner/name>` | The repository the pull request is opened in, when the checkout's origin does not name it. |
| `--dry-run` | Run the brain and the measuring as usual and write nothing beyond the working tree: no issue, branch, commit, push or pull request, which are planned and listed as side effects. The change is discarded. |
| `--brain`, `--sandbox`, `--borrowed-hands`, `--budget`, `--max-turns`, `--script` | The [shared flags](#brains-and-boxes). `--budget` and `--max-turns` bound each attempt. |

The defaults are `--brain` to `agentkit`, `--sandbox` to the provider that
`AF_SANDBOX` names, and without it `landlock` on Linux and `podman` anywhere else,
`--budget` to `10` (dollars), `--max-turns` to `150`, `--attempts` to `2`,
`--land` to `pr` and `--timeout` to 45 minutes. A pull or merge request as the input is a `usage`
failure, since a fix takes an issue, a file or text. `fix land` takes no
`--dry-run` and no `--repo`: it lands what the run recorded.

#### The steps of a fix run

1. **Preflight.** The checks that need no model run in this order, and the first
   refusal ends the run with no model call, no box, no branch and no write to the
   forge.
   1. *The flags and the input*: as above, with the brain and the box provider
      known. A violation is `usage` and names the flag.
   2. *The checkout*: `--dir` is the top level of a git work tree on a branch (a
      detached `HEAD` is `usage`, and so is a `--base` that is not the branch
      checked out).
   3. *A clean tree*: every change in the tree must be the run's own. A dirty tree
      is `failed`/`preflight` and names at most 20 paths.
   4. *A gate*: at least one check that can be run here (see
      [`lang detect`](#lang-detect)), else `failed`/`preflight` with `fix_hint`
      `{flag: "--no-verify"}`. A `.agent-fox/gate.json` that cannot be used is
      `config`.
   5. *The landing*, unless `--land none`: an `origin` remote to push to
      (`failed`/`preflight`; a dry run does not need one) and, for `pr`, a
      repository for the pull request (`usage`, `fix_hint` `--repo`), a
      credential for it (`failed`/`auth`, `fix_hint.env` `GITHUB_TOKEN` or
      `GITLAB_TOKEN`; under `--dry-run` the warning `no_forge_token`; a credential
      that the forge rejects is `failed`/`auth` too) and a repository that can be read
      and is not archived (`failed`/`forge`).
   6. *The brain*: it can be made, which needs the credential of its model
      (`failed`/`auth`, before a box is opened).
   7. *The baseline*: the full hash of the base is recorded as `base_head`, and
      the gate runs once on the tree as it is. A red baseline raises
      `baseline_failing` and the run goes on: a check that already fails is not
      counted against the change. `--no-verify` makes no baseline. A closed issue
      raises `issue_closed`, and the run goes on.
2. **The brief.** The prefix is the mandate, the body of the
   [`af-fix` skill](../skills/af-fix), the report in a fence, the gate with its
   baseline, the repository map and the schema of the report; it is the same for
   every round, attempt and resume over the same report and tree, whatever the
   run id, so that a provider can cache it. The task block at the end holds what
   changes: the run, attempt and round, the answers of a person, why the last
   round was refused (4 KiB of the end of each failing check) or why the last
   attempt ended, and how to submit. `brief.md`, `original.md`, `run.json` and, last,
   `fix.json` are written under `.agent-fox/run/<id>/`, and nothing outside
   `.agent-fox/` is (the baseline gate keeps its result in `.agent-fox/gate/<id>/`).
3. **The rounds.** One box is held for the whole run. Before each round the box
   is synced from the host's tree (the first round has it from opening), and
   `.agent-fox/gate.json` and `.agent-fox/guard.json` are written into it: the
   table of checks that `af` will measure by, and the policy of the brain's tool
   gate. The brain edits the box and submits one report with `submit_fix` (a brain that
   submits with `af fix submit` alone, like `claude`, is not told of the tool, and the
   driver that runs in the box finds no `fix.json` in its run directory, which would
   make its `af fix run` the round of the host), or one question. `af` takes the box's
   tree out and **applies** it to the checkout (see below), then **measures** it.
   Under `none` a `gate.json` or a `guard.json` that is there is the person's, and is
   left as it is.
4. **Apply.** The export is mirrored onto the checkout, uncommitted: the files
   it lacks are deleted first, then the new and changed files and links are
   written; `.git/` and `.agent-fox/` (whatever their case), ignored paths, anything
   that is not a file or a link, and everything under a submodule are left alone (and
   counted as `ignored`), and so are the files of the checkout that git tracks and a rule
   ignores, which the export never has and the brain does not delete. A round with no
   accepted report applies nothing, and an export with no file that can be applied is
   `failed`/`sandbox`. `fix run` is these steps for one round, without the
   measuring.

   Under `none` there is no box to take a tree out of, so nothing is exported or
   applied: the brain worked on the checkout itself. What a checkout cannot keep out
   is a commit of the brain's own, so before each run `af` records HEAD (the full
   hash) and the branch that is checked out, and after the run, before anything is
   measured, compares them with the checkout:

   - **Unchanged**: the run goes on, and git is not touched.
   - **The same branch, and the recorded HEAD is an ancestor of the HEAD now**: the
     commits are taken off the branch with `git reset --soft` to the recorded HEAD,
     the warning `foreign_commits_reset` is raised with their count, and the ledger
     records `foreign_commits_reset {count, from, to, branch}`. Their change is the
     change that is measured, and `af` commits it, so that its commit is the one that
     counts.
   - **Anything else** (another branch, a detached HEAD, rewritten history):
     `failed`/`git`, not retryable. The message names the branch and HEAD that were
     recorded and those it found; the tree and the refs are left as the brain left
     them, and nothing is landed or parked.

   A round under `none` that comes to no report leaves what the brain did in the
   checkout, since there is no box to throw away: the next round starts from it, and a
   run that ends with no change that lands parks it.
5. **Measure.** In order: a commit that is not the base's, or a branch that is not
   the base, is `failed`/`git`; a change under a protected path (`.git/`,
   `.agent-fox/`, `.specs/`, `.github/workflows/`, `.gitlab-ci.yml`) is put back
   (`protected_path_reverted`); a tree with nothing left is refused as
   `no_change`, and the gate is not run; the files the report declares are
   compared with the paths git sees changed (`scope_mismatch` lists both sides
   and refuses nothing); the gate runs again and is compared with the baseline by
   check; and, only on a verdict that may land, the revert check runs. The
   verdicts are `pass`, `pass_was_already_failing`, `regressed`, `still_failing`,
   `unverified` and `no_change`. Only a change whose tests the revert check
   proves **closes** the issue; any other says `Refs` (`fix_not_proven`). Under
   `--no-verify` neither the gate nor the revert check runs: the verdict is
   `unverified`, and the change lands.
6. **A refusal** (`regressed`, `still_failing`, `no_change`, and `unverified` when no
   check could be compared) is recorded in the
   ledger and is the task block of the next round, in the same box and the same
   tree, with the end of the output of each failing check.
7. **The bounds.** `--budget` and `--max-turns` hold for an attempt, which is
   rounds of one brain run in a row. Another round starts only while cost and
   turns remain and `--timeout` leaves the brain a minute after the time kept for
   measuring (three times what the baseline took, and a minute). An attempt that
   ends without a change that may land is followed by another, up to `--attempts`,
   from the tree as it is, with a fresh budget, and its task block says why the
   last attempt ended; a failure that cannot be retried starts none.
8. **Land** (below), or **park**.

`fix land` is steps 5 and 8 for the change that is in the checkout, whoever made
it, from the report that `fix submit` stored: it checks what landing needs, then
measures once, and lands the change or parks it.

#### How a fix run ends

| The run | `status` | Exit | `outcome` | What is left |
|---|---|---|---|---|
| A change that may land | `done` | 0 | `landed` | The issue (for a text or a file under `--land pr`), the branch, one commit, the push and the pull request; the checkout is back on the base with a clean tree. |
| The same, under `--no-verify` | `unverified` | 4 | `landed` | As above, a draft pull request, and the warning `not_verified`. |
| No attempt left, and a change in the tree that was refused or never reported | `unverified` | 4 | `parked` | A `wip:` commit on its branch; the branch is pushed unless `--land none`, and under `pr` a draft pull request that says `Refs` is opened. The warning `fix_not_landed`, and `attempt_failed` when the last round came to no report; `next` says how to look at the branch. |
| A question | `needs_human` | 3 | `needs_human` | The checkout is as the run found it: the question's round applied nothing, and a change that an earlier round applied and was refused is put back (`question_tree_kept` if it cannot be). See [Questions](#questions-and-answers). |
| No attempt left, and nothing applied | `failed` | 1 | | `result` null, and the category of the last failure of a round (`brain`, `no_result`, `budget`, `timeout`, `sandbox`, …); a last round refused as having changed nothing is `no_result`. |
| `--dry-run` | `done`, or `unverified` as above | 0 or 4 | `dry_run` | The change is discarded; every write is planned. |
| A signal, or `--timeout` that ends the command | `failed` | 1 | | `cancelled` or `timeout`. A change that was applied is parked on a local branch within 10 seconds, also when the stop comes while the change is landing (`dry_run_tree_kept` if a dry run could not discard it, `park_failed` if parking failed), so that nothing a model paid for is lost. |
| `--timeout` that ends a round first | as the rows above | | | The brain of a round has the time that is left less the time kept for measuring, so `--timeout` usually ends a round and not the command: the round is a failed one (`timeout`), and the run ends as it does when no attempt is left, parked or failed. |

**Landing** is, in order: the issue (a text or a file under `--land pr` gets one,
with the original report, since the pull request needs something to refer to); the
branch `fix/<N>-<slug>` for issue number `N`, or `fix/<slug>`, made unique against
the local and remote branches (`-2`, `-3`) and created from `base_head`; one
commit, which runs the repository's hooks, with the message `<type>: <subject>`,
the first 2000 bytes of the change, `Closes <issue URL>` or `Refs <issue URL>` and
the trailer `Agent-Fox-Run: <run id>`; the push (up to 3 attempts, each retry
raising `push_retried`); and, under `pr`, the pull request, whose body is the
comment that names the run, what `af` measured, what the brain reported (quoted,
and cut first if the body is over the limit of the forge), the files git sees
changed and the line that closes or refers to the issue. The pull request is a
draft when `--no-verify` was given or a criterion of the report is `unmet` or
`partial` (`criteria_unmet`). The body is kept as `report.md`.

A commit that a hook of the repository refuses parks the change instead
(`commit_hook_failed`). A push that still fails after its retries is
`failed`/`git`, with the branch and the commit kept and `next` naming the push to
run. A pull request that the forge refuses after the push is the warning
`pull_request_failed`, and the run still ends `done`: the change landed.

**Parking** commits `wip: <subject>` with the verdict, the failing checks, `Refs`
and the trailer, without running the hooks, so that a hook cannot lose the work.
A failed push, issue or pull request after that commit is a warning
(`push_failed`, `issue_creation_failed`, `pull_request_failed`), and a pull request
for a run with no issue is skipped (`pull_request_skipped`).

**The result** of `fix` and `fix land` (`$defs` `fix` and `fix_land`):

| Field | Meaning |
|---|---|
| `issue`, `issue_created` | The URL of the issue the report is, or `null`; `{number, url}` of the issue that was made for a text or a file, else `null`. |
| `base`, `base_head` | The branch the fix was made on and goes back to, and the full hash of its commit when the run began. |
| `attempts`, `rounds`, `attempts_max` | How many attempts and rounds the run made, and the most it could (`--attempts`); `attempts` and `attempts_max` are 0 for `fix land`, which runs no brain, and its `rounds` are the rounds of `fix run` that the run made before. `rounds` counts across attempts. |
| `outcome` | `landed`, `parked`, `needs_human`, `failed` or `dry_run`. |
| `verdict`, `landable`, `closes` | What `af` measured, whether it lets the change land, and whether the revert check proved it. `verdict` is `null` when nothing was measured. |
| `baseline`, `after`, `revert_check` | `{gate_id, passed}` of the gate before and after, and `{outcome, reason}` of the revert check, each `null` when it did not run. |
| `changed_files`, `reverted_paths` | The paths git sees changed, and the protected ones that were put back. |
| `scope` | `{undeclared, unchanged}`: the paths that changed without being declared, and the declared ones that did not. |
| `land` | `pr`, `branch` or `none`. |
| `branch`, `commit`, `pushed` | The branch and the full hash of the commit, `null` when none was made, and whether the branch is on the remote. |
| `pull_request` | `{number, url, draft}` or `null`. |
| `box` | `{provider, image, network}` of the box the brain worked in, or `null`. `image` is the image of a `podman` box and `""` for any other provider. `network` is `open` for a provider that isolates (the brief of a fix has a tool that runs a command), and `host` for `local` and `none`, where nothing is enforced. |

The side effects are `create_issue`, `branch`, `commit`, `push` and
`create_pull_request`, each `done`, `planned` or `failed`; the artifacts are the
`issue` (when one was made), the `branch`, the `commit` and the `pull_request`, with
`brief`, `submission`, `report`, `run_dir` and the `transcript` of a brain that keeps
one. The envelope's `usage` is the brain's, summed over the rounds, and a
dry run spends model money like any other.

**Warnings** the fix commands raise besides the shell's and those of
[Brains and boxes](#brains-and-boxes): `baseline_failing`, `issue_closed`,
`question_not_found`, `protected_path_reverted`, `scope_mismatch`,
`fix_not_proven`, `push_retried`, `criteria_unmet`, `pull_request_failed`,
`fix_not_landed`, `not_verified`, `attempt_failed`, `commit_hook_failed`,
`no_forge_token`, `park_failed`, `push_failed`, `issue_creation_failed`,
`pull_request_skipped`, `dry_run_tree_kept`, `question_tree_kept` and `report_unwritable` (see
[Warning codes](#warning-codes)). The new failure category is `preflight`.

### fix brief

`af fix brief <input>` makes the checks of steps 1 to 4 and the baseline of step 7
(not the checks of the landing and of the brain, which `fix land` and `fix run`
make when they need them), renders the brief of a run, and stops. It calls no model and writes
nothing to the forge, to git or outside `.agent-fox/`. It takes `--context`
and `--repo`, and the forge client of the shell for an issue.

It records `fix.brief {brief, prefix_sha256, document_bytes}` and lists the
artifact `{kind: "brief", path}`. The result:

| Field | Meaning |
|---|---|
| `brief_path` | The path of `brief.md`. |
| `prefix_sha256` | The SHA-256 of the prefix of the brief, which is the same for two briefs over the same report and tree. |
| `base`, `base_head` | The branch and the full hash of its commit, where `HEAD` must still be when the change is measured. |
| `baseline` | `{gate_id, passed}` of the gate on the tree as it was. |
| `table` | The table of checks of the gate, pinned for the run. |
| `submit_argv` | The command that submits the report: `af fix submit --run-id <id> --dir <dir>`. |

`next` suggests `af fix submit` and `af fix land`.

### fix run

`af fix run --run-id ID` runs one round of a brain on the `brief.md` of the run
and applies the tree it leaves to the checkout, without measuring it, branching or
committing: it is the second step of a person's flow when the brain is a model.
The result is `{run_id, round, submission, submission_path, question, apply,
submitted}`, where `submission` is `report` or `question`, `apply` is `{written,
deleted, ignored}` (null for a question) and `submitted` says that the brain handed
over a submission that was accepted. It records `fix.run`. Like `triage run`, it
takes the brief and the prefix from `run.json` when the run directory has no
`fix.json`, which is how the driver of the `agentkit` brain runs it in a `landlock`
or `podman` box: it then runs the brain over the tree it is in, applies nothing, and
the host reads its submission from the box.

### fix submit

`af fix submit --run-id ID` reads the report of a fix on stdin and stores it: it
is [the submit command of an agent](#the-submit-command-of-an-agent) for the
`fix_report` kind. One JSON object is either a question (`question` alone) or a
report (`subject`, `root_cause`, `change` and `files`, with `type`, `tests`,
`criteria` and `decisions`); a refusal lists every problem at once, each with the
JSON pointer of its field. The report is stored normalised (trimmed, LF line
ends, the type `fix` by default, the files sorted by path) in `submission.json`,
and its result is `{run_id, kind, path, sha256}`.

### fix land

`af fix land --run-id ID` measures the change that is in the checkout, once, and
lands it or parks it: the steps 5 and 8 above, for a change a person made or a
round of a brain left, with no brain and no box. It needs a stored report (a
question is refused as `failed`/`submission`, and the message points at
`fix brief --context`), checks what
the landing needs before it measures, and its result is that of `fix`, with
`attempts` and `attempts_max` at 0, its `rounds` those of `fix run`, and `box` null. A tree with no change
left is a `usage` failure; a refused change is parked, with no further round.

#### The files of a fix run

| File in `.agent-fox/run/<id>/` | Written by | What it is |
|---|---|---|
| `brief.md` | the loop, `brief` | The brief of the first round: the prefix, then the task block. |
| `original.md` | the loop, `brief` | The original report, exactly. |
| `run.json` | the loop, `brief`, `submit` | The attempt and the nonce that the submission protocol reads. |
| `fix.json` | the loop, `brief` | The state of the run: `run_id`, `input {kind, origin, key}`, `issue {url, title, number}` or null, `target {repo, host}`, `base`, `base_head`, `table`, `baseline_gate_id`, `baseline_passed`, `baseline_ms`, `no_verify`, `prefix_sha256`, `prefix_bytes`, `suite`, `programs`, `context`, `rounds`, and the `brain`, `cost_usd` and `cost_source` the rounds so far used. It is written last and atomically. |
| `submission.json` | the brain, `submit` | The accepted report (see [Brains and boxes](#brains-and-boxes)). |
| `report.md` | the loop, `land` | The body of the pull request, as it was rendered. |

The ledger of a run holds `fix.brief`, `fix.run {round, submission,
submission_path}`, `fix.question {text, options}` when a run ends on a question,
and, for each round, `apply {written, deleted, ignored}`, `measure {verdict,
closes, changed, reverted, after_gate_id, revert_outcome}` and `refusal {kind}`
when the change was refused, among the entries of the box and the brain.

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
| `usage` | object | `{brain, cost_usd, cost_source, input_tokens, output_tokens, turns}`. With no brain, `brain` is `""`, the numbers are 0 and `cost_source` is `none`; it is otherwise `measured` or `reported`, and `none` for a brain that used no model (`scripted`) or a model with no price in the catalog. |
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
| `auth` | An issue was not found without a token, so it may be private, or the `agentkit` brain has no credential for its model, or the `claude` brain has no `ANTHROPIC_API_KEY` for its box (not under `none`). `fix_hint.env` names the variable to set (a forge token, or `ANTHROPIC_API_KEY`). |
| `forge` | The forge could not be reached, refused, or the client could not be built. `retryable` is true after a rate limit, a server error or a lost connection, and false after a refusal (a write of `triage` that gets a server error is not retryable). |
| `io` | A file, stdin or the run directory could not be read or written. |
| `cancelled` | The process was sent SIGINT, SIGTERM or SIGHUP. |
| `timeout` | `--timeout` ran out, or the time a brief allows ran out with no submission (it counts from the start of the run, so placing the brief is part of it). Not retryable; `fix_hint` is `{flag: "--timeout", suggest: <twice the value>}`. |
| `internal` | A bug: a plain error, a panic, an inconsistent outcome, a result that is not JSON. |
| `config` | A configuration file `af` reads cannot be used, for example a `.agent-fox/gate.json` that does not parse, or the `.agent-fox/guard.json` that a brain's tool gate loads. The message names the file. The `agentkit` brain also uses it when the model provider cannot be configured from the environment. |
| `restore` | A tree that a command changed in order to measure it could not be put back, or a leftover of an earlier measurement stands in the way. The message names the directory that holds what is left. |
| `git` | git failed in a way the command cannot work around; also, under `none`, a checkout that the brain left on another branch, detached, or with its history rewritten (see [the steps of a fix run](#the-steps-of-a-fix-run)). |
| `sandbox` | The box could not be opened, synced or exported. The provider could not create it (not retryable: `landlock` on a host without Linux or without Landlock ABI 4, `podman` without a reachable engine, see [the providers](#the-providers)); the image of a `podman` box is missing and cannot be pulled, the checkout could not be synced in, or the tree could not be exported out (retryable); a process brain's binary or `HOME` fails the probe in the box (not retryable); or an export carried a `.git`, which stops the run. When the provider was chosen by default, `fix_hint` names `--sandbox` and `AF_SANDBOX`. |
| `brain` | A brain failed: it returned an error and left no submission (retryable), or it panicked (not retryable; the stack is in `brain-panic.<attempt>.txt` in the run directory); or the `claude` brain cannot find `claude` on the `PATH` of this machine (not retryable). |
| `no_result` | A brain ended without a valid submission: it stopped for another reason than the cost or the turns, or the submission file did not count. Retryable. |
| `budget` | A brain used its cost bound or its turn bound and left no submission. Not retryable; `fix_hint` is `{flag: "--budget"` or `"--max-turns", suggest: <twice the bound>}`. |
| `submission` | A submission was refused (the `submit` command, [below](#the-submit-command-of-an-agent)); nothing was written, and the message names every problem. Retryable. Also `fix land` on a run whose submission is a question. |
| `preflight` | A fact about the repository stops `fix` before it spends anything, and it is not an error of the invocation: the working tree is not clean (the message names up to 20 paths), no check of the gate can be run here (`fix_hint` `{flag: "--no-verify"}`), or `--land` needs an `origin` remote and there is none. Not retryable. |

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
| `no_checks` | `gate run`: no check of the gate table can be run here, or the table has none. Nothing ran and `passed` is false. |
| `tree_changed_during_gate` | `gate run`, `fix`, `fix land`: the checks changed the tree. The result is kept under the hash of the tree they ran on. |
| `fix_not_proven` | `revert-check`, `fix`, `fix land`: the outcome is not `proven` and not `no_changes`, so the change must not close its issue; the commit says `Refs`. |
| `map_outline_skipped` | `map`: the declarations were not read, because the names of the files alone do not fit the budget or the view has more than 5000 files. The map has the names only, or less. |
| `check_unavailable` | `lang detect`: a check of the gate table names a program that cannot be run on this machine. The check stays in the table with `available: false`; the message names the check. |
| `box_unconfined` | A box was opened with the `local` provider, once for each box: it has no process or network isolation, because the tools run in this process with its privileges. Also with the `none` provider, with a message of its own: the brain works on the checkout itself, with no copy of it and no box around it, and the hook is the only guard. Also `triage run`, which reads the directory in place, with no copy of it: the files git ignores and `.git/config` are within reach of the tools. |
| `box_af_version_mismatch` | A process brain's `af` in a `podman` box has another version or commit than the running one. The run goes on, since the host checks every submission again; the message names both. |
| `brain_usage_unavailable` | A process brain left no output that could be read (the JSON of `claude`, the envelope of `af <agent> run`), so the run has no cost to report: the cost is 0 and `cost_source` is `none`. |
| `budget_exceeded` | The `claude` brain reported a cost above `--budget`. Its cost is known only after the run, so only its turns and its time held it while it ran; with no submission, the stop reason is `budget`. |
| `foreign_commits_reset` | `fix`, under `none`: the brain made commits on the branch of the checkout, and `af` took them off it with a soft reset to where the branch was. The message has their number; their change is inside the commit of `af`. |
| `box_close_failed` | The box could not be closed, also one that could not be synced and was closed for that. The result of the run is what it was; the message carries the error. |
| `submission_invalid` | The submission file in the box did not count: it did not parse, is not for this run's kind, run, attempt or payload hash, or the kind refused its payload against the box's tree. The message lists the problems, and the run has no submission. |
| `submission_claim_mismatch` | A brain says it submitted and there is no valid submission file, or says it did not and there is one. The file decides. |
| `brain_error` | A brain returned an error, or panicked, after it had submitted, which does not change the submission. The message carries the error. |
| `submission_not_removed` | The run directory held a `submission.json` that af did not accept (one of an earlier attempt, or one that a process of the brain wrote), and it could not be removed. |
| `model_unpriced` | The `agentkit` brain's model is not in the catalog, so the run has no cost to report (`cost_source` is `none`). With `--budget` the run does not start: it is a `usage` failure. |
| `transcript_unwritable` | The transcript of an in-process brain could not be written whole. The run goes on and the file is partial. |
| `ledger_unwritable` | A ledger entry could not be written: at most once for the box and once for the brain run, since each reports its own. |
| `issue_closed` | `triage`, `fix`, `fix brief`: the issue is closed; the run goes on. |
| `report_changed_during_triage` | `triage`, `triage land`: the body of the issue is not the report the brief had. The report as the issue has it now is the one kept in the body. |
| `body_too_long` | `triage`, `triage land`: the body with the diagnosis would be longer than the forge allows, so the diagnosis was posted as one comment and the body was left. |
| `labels_failed` | `triage`, `triage land`: the diagnosis was written and adding the labels failed. The side effect `add_labels` is `failed`. |
| `original_report_unparsed` | `triage`, `triage land`: a body starts with the triage marker and is not what `af` wrote (the markers of its original report cannot be paired, a heading is missing, or text follows the original report), so the whole body is taken as the report. |
| `rejections_unreadable` | `triage`, `triage submit`, `triage land`: `rejections.jsonl` cannot be read, or holds a line that is not JSON, so the refused calls are counted as 0; also when it could not be brought out of the box. |
| `baseline_failing` | `fix`, `fix brief`: the gate fails on the tree as it is, before any change. A check that already fails is not counted against the change, and a change that makes it pass is `pass_was_already_failing`. |
| `question_not_found` | `fix`, `fix brief`: no earlier run asked the question that a `--context` answers, so the answer stands alone in the brief. |
| `protected_path_reverted` | `fix`, `fix land`: the change touched a protected path (`.git/`, `.agent-fox/`, `.specs/`, `.github/workflows/`, `.gitlab-ci.yml`), and `af` put it back to the base's content or deleted it. One for each path. |
| `scope_mismatch` | `fix`, `fix land`: the report declares files that did not change, or the change has files the report does not declare. Both lists are in the message, and nothing is refused. |
| `criteria_unmet` | `fix`, `fix land`: a criterion of the report is `unmet` or `partial`, so the pull request is a draft that says the work is unfinished. |
| `not_verified` | `fix`, `fix land`: the change was landed without the gate (`--no-verify`): nothing was measured, and a person has to check it. The status is `unverified`. |
| `no_forge_token` | `fix` with `--dry-run` and `--land pr`: opening a pull request needs a credential and none is set; a dry run goes on without one. Without `--dry-run` it is `failed`/`auth`. |
| `push_retried` | `fix`, `fix land`: a push failed and is tried again, up to 3 attempts in all. |
| `commit_hook_failed` | `fix`, `fix land`: a hook of the repository refused the commit, so the change was parked as `wip` without the hooks. |
| `pull_request_failed` | `fix`, `fix land`: the branch is pushed and the forge refused the pull request. The change landed, and the status is not changed. |
| `pull_request_skipped` | `fix`, `fix land`: no pull request was opened, because there is no issue for it to refer to (the issue of a text or a file could not be made). |
| `issue_creation_failed` | `fix`, `fix land`: the issue for a text or a file could not be made; the change goes on without one. |
| `fix_not_landed` | `fix`, `fix land`: the change did not land and is parked on a branch of its own; the message names the verdict and the branch. The status is `unverified`, or `failed` with `cancelled` or `timeout` for a run that was stopped and parked. |
| `push_failed` | `fix`, `fix land`: a parked branch could not be pushed and is kept here; `next` names the push. |
| `attempt_failed` | `fix`: the last round of the run came to no report, and the change in the tree is parked as it is. The message carries why. |
| `park_failed` | `fix`: the run was stopped and the change that was applied could not be parked; it is in the working tree. |
| `dry_run_tree_kept` | `fix --dry-run`: the change of the dry run could not be discarded (the checkout is not at the base any more, or git failed), and is left in the working tree. |
| `question_tree_kept` | `fix`: the run ended on a question and the change that an earlier round applied could not be put back (the checkout is not at the base any more, or git failed), and is left in the working tree. |
| `report_unwritable` | `fix`, `fix land`: `report.md` could not be written; the pull request has its body all the same. |

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
whatever the command returns after that. SIGINT, SIGTERM and SIGHUP (a closed
terminal) cancel the command's context in the same way; if the command then
returns an error the envelope is `failed`/`cancelled`, and a command that
returns an outcome has finished its job, so its outcome stands. A command that
has not returned 10 seconds after being cancelled, by a signal or by its
deadline, is abandoned: the shell prints `failed`/`cancelled` or
`failed`/`timeout` itself and exits 1. A second signal during those 10 seconds
is ignored. A command that has something to put back may ask for longer
(`Command.Grace`): `revert-check` asks for 10 minutes.

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
    brief.<attempt>.md       the brief a brain was given, if the command runs a brain
    hook.json                the hook configuration placed with it
    submission.json          the accepted submission, if there is one
    transcript.<attempt>.jsonl   the tool calls of an in-process brain, listed as {kind: "transcript", path}
    brain-panic.<attempt>.txt    the stack of a brain that panicked
    brief.md, original.md, triage.json, rejections.jsonl, diagnosis.md    the files of a triage run
    brief.md, original.md, run.json, fix.json, report.md    the files of a fix run
```

The five before the last are written by a command that runs a brain
([below](#brains-and-boxes)); `af` itself never reads a transcript. The files of
a triage run are in [the files of a triage run](#the-files-of-a-triage-run), and
those of a fix run in [the files of a fix run](#the-files-of-a-fix-run).

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
command ran. A command emits its own types through `Run.Emit`. A command that
runs a brain emits `step` (`{name, brain?}`, for the stages `brief`, `brain` and
`submission`; `triage` names its own, `preflight`, `brief`, `brain` and `land`), `heartbeat` (`{brain, elapsed_ms}`, every 30 seconds while the
brain runs) and `tool_call` (`{brain, tool, decision, reason, ok, duration_ms}`,
once for each call that reached the gate; it carries no argument and no result, because events can go
to an audit and arguments can hold the content of files).

### The ledger

`ledger.jsonl` is append-only: `{seq, ts, kind, data}` per line, `seq` from 1.
A command records what it measured with `RunDir.Record(kind, data)` and reads
it back with `RunDir.Ledger()`; `toolio.OpenRunDir(dir, id)` opens an
existing run, for the commands that carry one on, and refuses an id that is
not of the form of a run id. The shell records nothing in the ledger. A command
that runs a brain records `box_open {provider, image, network, placement}`, `box_sync {head}` (the host's
short HEAD, or "" for a repository with no commit, also at every later sync), `box_export {path}`, `box_close
{provider}`, `brief_placed {attempt, bytes, sha256, prefix_sha256}`,
`brain_start {brain, attempt}`, `brain_end {brain, stop_reason,
submitted_claim, cost_usd, cost_source, turns, duration_ms}`, for a brain that runs
as a process `box_probe {binary, ok, version}`, `brain_process {brain, exit_code,
duration_ms}` and, for `claude`, `claude_result {subtype, is_error, num_turns,
total_cost_usd, usage, session_id}` and `claude_tool_dropped {tool}`, and, for what it
found in the box after the brain, `submission_accepted {attempt, sha256}`,
`submission_invalid {attempt, problems}`, `submission_stale {attempt,
file_attempt}` or `submission_absent {attempt}`. `triage` adds `triage.input`,
`triage.brief`, `triage.submission`, `triage.question` and `triage.land` (see
[the files of a triage run](#the-files-of-a-triage-run)), and `fix` adds
`fix.brief`, `fix.run`, `fix.question`, `apply`, `measure`, `refusal` and, under `none`,
`foreign_commits_reset {count, from, to, branch}` (see
[the files of a fix run](#the-files-of-a-fix-run)).
