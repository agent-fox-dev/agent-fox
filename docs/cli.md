# The af command line

`af` is the one binary of agent-fox. `make build` writes it to `bin/af`
(`go build ./cmd/af` builds it too). Every command of `af` speaks the same
contract, which the shell in `internal/toolio` implements once so that the
commands do not: one JSON envelope on stdout, a fixed table of statuses and
exit codes, coded warnings, a stop on a question that is resumed with
`--context`, a `--dry-run` that cannot write to the forge, a fetchable
schema, and a git-ignored run directory.

**This release registers six work commands, [`lang detect`](#lang-detect),
[`gate run`](#gate-run), [`gate compare`](#gate-compare),
[`revert-check`](#revert-check), [`guard check`](#guard-check) and
[`map`](#map).** The binary also answers `--version`, `--schema` and `help`; `af` with no arguments, or with a command it does not
know, prints a `usage` envelope and exits 2. The other commands of this scope
and of later specs (`triage`, `fix`, …) register on the shell and get
everything below.

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

### gate run

```
af gate run [--dir DIR] [--label LABEL] [--hermetic] [--no-cache] [--timeout DURATION]
```

Runs the checks of the gate table (the `gate` of [lang detect](#lang-detect))
on the tree in `--dir` and reports what they said, or returns the result
stored for that tree. It builds no forge client, calls no model, opens no
network connection and creates no run directory; it writes only under
`.agent-fox/gate/`. The default `--timeout` is 30 minutes.

| Flag | Meaning |
|---|---|
| `--label <label>` | Name the result: the result's `label`, and `labels.json` (below) maps the label to the result's id, on a hit as well as after a run. A label is a lower-case letter and up to 31 lower-case letters, digits, `_` or `-`, and is not spelled like an id (12 hex characters); anything else is a `usage` error before any check runs. |
| `--hermetic` | Run the checks with a clean environment: a temporary `HOME` that is removed afterwards, whether the run completed, failed or was cancelled, no git configuration and no credential prompt. The toolchains' download caches stay where they are. |
| `--no-cache` | Run the checks even when a result is stored for this tree, and replace it. |

**How the checks run.** The checks that can be run (`available`) run one
after the other, in table order, in `--dir`. They run with the credential
variables of the environment stripped (the model vendors' keys and anything
named `*_TOKEN`, `*_SECRET`, `*_API_KEY` or `*_PASSWORD`), because a suite is
repository code. A check that is not available is skipped, not run, and is
listed in `skipped` with the reason `unavailable`. Each check has 20 minutes;
one that takes longer is stopped, with everything it started, and has the
status `timeout`, and the checks after it still run. The output of each check,
standard output and standard error together, is written in full to
`.agent-fox/gate/<id>/NN-<slug>.log`, with `NN` the two-digit position among
the checks that ran and `<slug>` the check's name.

**The result** (its schema is `gate_run` under `$defs`):

| Field | Meaning |
|---|---|
| `id` | The first 12 hex characters of the cache key (below). The result is stored in `.agent-fox/gate/<id>/`. |
| `tree` | The hash of the tree the checks ran on: a SHA-256 over the file view in order, each file as its kind, its executable bit, its path and the SHA-256 of its content (a symlink as its target, any other file as its type, an unreadable file as a fixed marker). It depends on the content of the files in the view and on nothing else: not on `HEAD`, not on where the checkout is, not on ignored files. |
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
| `failed`/`timeout` or `failed`/`cancelled` | `--timeout` ran out, or a signal arrived. The process group of the running check is killed, and nothing is stored. |
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
one run are there for the next. A file that is damaged starts again empty.

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
label, else the one the result was stored under), `verdict`, `landable` and
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
   `.agent-fox/revert/`: an earlier check was killed, the tree may not be the
   one it started on, and nothing is overwritten. The message names the
   directory.
2. It lists the changed paths: those that differ from `REF`, staged or not,
   and the untracked files that are not ignored, byte for byte (a rename is
   its two paths). The index is never written, not even by git's own refresh
   of its stat data: the listing runs against a copy of it. Each path is a
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
   `.agent-fox/revert/<id>/` (`<id>` is the first 12 hex characters of the
   tree hash): `files/` holds a copy of every file it will change,
   `manifest.json` says what each path was (a file with its mode and
   modification time, a link with its target, or absent), and `index` is the
   index as it was. Then, without touching the index, each non-test path that
   the commit has gets the commit's content and mode (read with `git cat-file`
   from the blob `git ls-tree` names), and each one it does not have is moved
   aside; test paths stay as they are. Then only the checks of kind `test` and
   `check` run on that tree, through the gate engine, cached by the reverted
   tree's hash.
6. It restores every path from the backup, with a context that nothing
   cancels, so that a signal, `--timeout` or a panic during the tests still
   puts the tree back. It checks that the tree hash equals the one taken
   before the revert and that `.git/index` is byte for byte the same, and
   then removes the backup.

A directory (a nested repository) or a path where the commit has a directory
or a submodule is not reverted. The checks must not leave files of their own
in the tree outside the ignored paths: a file made while the reverted tree is
tested changes the tree hash, and is a restore failure although every
reverted path is back.

**The result** (its schema is `revert_check` under `$defs`):

| Field | Meaning |
|---|---|
| `outcome` | `proven`, `not_proven`, `inconclusive`, `no_changes`, `no_code_changes` or `no_test_changes`. |
| `reason` | Why, as a code. For `proven`: `tests_fail_without_the_change`. For `not_proven`: `tests_pass_without_the_change`. For `inconclusive`: `no_test_check`, `after_not_green`, `test_check_unavailable`, `reverted_check_error` (a check could not start on the reverted tree, so it measured nothing) or `nothing_to_revert`. For the others: `no_paths_changed`, `only_test_files_changed` or `no_test_files_changed`. |
| `closes` | True only for `proven`: a check of kind `test` or `check` failed or timed out on the reverted tree. |
| `since` | The commit `--since` resolved to, in full. |
| `test_files` | The changed test paths. |
| `reverted_files` | The paths that were set back to the commit; empty when nothing was reverted. |
| `after` | The id of the gate result for the current tree; `""` when it was not measured. |
| `reverted` | `{id, passed, checks}`, the gate result of the reverted tree, as in [gate run](#gate-run); null when nothing was reverted. |
| `restored` | True when the tree and the index are as they were, which is so for every result. |

| Outcome | When |
|---|---|
| `done`, exit 0 | Every outcome above, `not_proven` and `inconclusive` included. Every outcome except `proven` and `no_changes` also raises the warning `fix_not_proven`. |
| `usage` | `--dir` is not the top level of a work tree, or `--since` is missing or is not a commit. |
| `failed`/`restore` | A backup from an earlier check is there, or the tree could not be put back or came back as another tree. The backup is kept in `.agent-fox/revert/<id>/`, the message names it, and nothing further is reverted. |
| `failed`/`git` | git could not be run, or failed. |
| `failed`/`config` | `.agent-fox/gate.json` cannot be used. |
| `failed`/`timeout`, `failed`/`cancelled` | `--timeout` ran out or a signal arrived. The tree has been put back first. |
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
with no opinion. `cwd` is the directory the session is in: a relative path
means what it means there, and in `--dir` (the checkout) as well.

**The decision** covers the calls to three kinds of tool:

- *Shell tools* (`Bash`, and the hands' `execute`). The command is read as a
  shell would read it, and every command it runs is judged: those joined by
  `&&`, `||`, `;`, `|`, `&` and newlines, those in groups, in `$(…)`,
  backticks and `<(…)`, in heredoc bodies that expand them, and in the script
  of `sh|bash|zsh -c '…'` and `eval`. Assignments (`FOO=1 cmd`), the wrappers
  `env`, `time`, `timeout`, `nice`, `nohup`, `command` and `exec`, and the
  keywords that start a command are skipped to find the program. The rules
  apply in this order to each command, and the first denial wins.
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
| `unparseable` | A command with unbalanced quotes, an unterminated `$(` or `${`, nested more than 64 deep, longer than 64 KiB (the hands' guard takes time that grows with the square of the length, so a longer line is not read; write long content with a tool that writes files), or a shell call that has no command. |
| `hands_guard` | What the hands' guard refuses, with its reason: an assignment of a variable that changes which program runs or what is loaded into it (`PATH`, `LD_PRELOAD`, `BASH_ENV`, …), a line it cannot read, and a line that runs no program. It can only deny. |
| `git_mutation` | `git` with any subcommand but `status`, `log`, `diff`, `show`, `blame`, `ls-files`, `ls-tree`, `rev-parse`, `rev-list`, `grep`, `describe`, `cat-file`, `shortlog`, `merge-base`, `name-rev`, `for-each-ref` and `show-ref`; `branch` and `remote` only to list; and a global `-c`. The reason says that af makes every commit, branch and push. |
| `suite_command` | A command equal to a suite form once every token that starts with `-` is removed from both: `make check`, `make -j4 check`, `go test ./...` and `go test -count=1 ./...` are the suite, `go test ./internal/x -run TestY` is not. The reason says to run `af gate run`, which caches the result by tree hash so that the landing reuses it, and that targeted runs are allowed. A flag that selects tests without a word of its own (`mvn -q test -Dtest=X`) is not told from the suite. |
| `af_command` | Any `af` command but `lang detect`, `gate run`, `gate compare`, `revert-check`, `map`, `guard check`, `triage brief`, `triage submit`, `fix brief`, `fix submit`, `--version` and `--schema`: the rest belongs to the loop. |
| `program_not_allowed` | With `allow_programs` set, a program that is not on it; `af` and `git` are always allowed. |
| `protected_path` | A write into a protected path through an output redirection, or through an argument of `rm`, `mv`, `cp`, `tee`, `touch`, `mkdir`, `truncate`, `ln`, `chmod` or `sed -i`, resolved against `--dir` (and against the directory of the session and of any `cd` before it in the command). `rm` and `mv` also refuse a directory above a protected path, such as `rm -rf .`. |
| `outside_dir` | A write tool call to a path outside `--dir` and the system temporary directory. |

The policy is prevention, not a boundary. It reads the command and does not
run it: a program named by a variable, a script a command writes and then
runs, or a program that runs others (`xargs`, `find -exec`, `ssh`) is out of
its sight, and the tool `run_command` (which takes an argument list) is not a
shell tool here. The gate and the box are what holds.

**Without `--host`** the answer is the envelope: `done` and exit 0 for an
allow and for a deny, with the result (its schema is `guard_check` under
`$defs`) `{decision, rule, reason, tool, segment}`. `rule`, `reason` and
`segment` are empty for an allow. A payload that is malformed, empty or over 1
MiB is `usage` (exit 2); a policy file that cannot be used is `failed`/`config`
(exit 1). Neither emits a decision.

**With `--host claude`** stdout is the document Claude Code reads, and nothing
else, and the exit code is always 0:

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
not act on). Only a wrong invocation (an unknown flag, an unknown `--host`)
is still an envelope with exit 2, which the host treats as blocking.

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
offered to it. A control character in a name is written as `?`.

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
the budget), `level`, `files_total`, `files_outlined` and `truncated` (true for
any level above 1). The same tree and the same budget give the same bytes, in
whatever order the files are read: a brief that carries the map can rely on it
being the same on every attempt. The status is `done`.

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
| `no_checks` | `gate run`: no check of the gate table can be run here, or the table has none. Nothing ran and `passed` is false. |
| `tree_changed_during_gate` | `gate run`: the checks changed the tree. The result is kept under the hash of the tree they ran on. |
| `fix_not_proven` | `revert-check`: the outcome is not `proven` and not `no_changes`, so the change must not close its issue. |
| `map_outline_skipped` | `map`: the declarations were not read, because the names of the files alone do not fit the budget or the view has more than 5000 files. The map has the names only, or less. |
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
