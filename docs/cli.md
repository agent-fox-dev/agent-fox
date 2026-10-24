# The af command line

`af` is the command-line binary of agent-fox. `make build` writes it to `bin/af`
(`go build ./cmd/af` builds it too), beside `bin/agent-fox`, the long-running service, whose own
command line is [described at the end of this document](#the-agent-fox-command-line). Every command of `af` speaks the same
contract, which the shell in `internal/toolio` implements once so that the
commands do not: one JSON envelope on stdout, a fixed table of statuses and
exit codes, coded warnings, a stop on a question that is resumed with
`--context`, a `--dry-run` that cannot write to the forge, a fetchable
schema, and a git-ignored run directory.

**This release registers eighteen commands: [`lang detect`](#lang-detect),
[`gate run`](#gate-run) and [`revert-check`](#revert-check), the two of [triage](#triage): `triage` and
[`triage run`](#triage-run), the two of [fix](#fix): `fix` and [`fix run`](#fix-run), and the eleven of the
[client of the agent-fox service](#the-client-of-the-agent-fox-service): [`login`](#login), the four of
[`workspace`](#workspace-subscribe-list-get-and-unsubscribe) (`workspace subscribe`, `workspace list`,
`workspace get` and `workspace unsubscribe`) and the six of [`jobs`](#jobs-list-get-watch-cancel-answer-and-retry)
(`jobs list`, `jobs get`, `jobs watch`, `jobs cancel`, `jobs answer` and `jobs retry`).** The binary also
answers `--version`, `--schema` and `help`, which does not list `triage run` and `fix run`:
they are what a box executes, and no commands for a person; `af` with no arguments, or with a
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
  --local --dir repo` and `af fix --local --dir repo ./bug.md` are the same), as `--flag
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
af gate run [--dir DIR] [--timeout DURATION]
```

Runs the checks of the gate table (the `gate` of [lang detect](#lang-detect))
on the tree in `--dir` and reports what they said, or returns the result
stored for that tree. It builds no forge client, calls no model, opens no
network connection and creates no run directory; it writes only under
`.agent-fox/` (making the directory and its `.gitignore` first, so that
nothing it writes shows in `git status`). The default `--timeout` is 30
minutes.

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
| `failed`/`config` | `.agent-fox/gate.json` cannot be used (see [lang detect](#lang-detect)). |
| `failed`/`io` | The tree cannot be read, or a log or the result cannot be written. |

**The cache.** The key is the SHA-256 of the tree hash, the canonical JSON of
the table's checks (`name`, `argv` and `kind` of each, with the keys in order
and no whitespace, and not `source` or `available`, which are not about what
is measured) and the runner mode, `reduced`. The same tree and
the same table give the same key on every machine, and so a gate that ran in
a box is a hit for the gate that runs outside it. `result.json` is written
after the logs, through a temporary file and a rename, so that runs that start
together without a lock leave the last writer's complete file and never half
of one. Only a run that completed is stored.

- A stored result for the key is returned with `cached: true` and no check
  runs, unless the result skipped a check that can
  be run here (it was made where its program was missing), or `result.json` is
  not a result of this key. Then the checks run and the result is replaced.
- A change of one byte in a file of the view makes the next run another key. A
  change confined to ignored files does not.

#### How two gate results are compared

[`af fix`](#fix) compares the gate of the change with the baseline, check by
check, to say whether the change is a regression of the tree it started from.
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

Two results that were not measured by the same checks cannot be compared, and
the run fails.

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

### The tool policy

There is no command for it: [the tool gate](#the-tool-gate) of a brain decides
whether the brain may make a tool call, in process (`policy.DecideAt`), by the
policy of `.agent-fox/guard.json` (see
[configuration](configuration.md#guardjson)). It reads and decides; it changes
nothing. A relative path means what it means in the directory the session of the
brain is in, and in the checkout as well.

**The decision** covers the calls to three kinds of tool:

- *Shell tools* (the hands' `execute`). The command is read as a
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
- *Tools that write a file* (the hands' `write_file` and `edit_file`; the path is
  `path`). A path under a
  protected path is denied (`protected_path`), and so is one outside the checkout
  that is not under the system temporary directory (`outside_dir`). A call
  that names no path is denied (`unparseable`).
- *Every other tool* is allowed, with no rule: the brief's allowlist decides which
  tools exist.

| Rule | Denies |
|---|---|
| `unparseable` | A command with unbalanced quotes, an unterminated `$(`, `${` or `$[`, nested more than 64 deep, a brace expansion that makes more than 4096 words, longer than 64 KiB (the hands' guard takes time that grows with the square of the length, so a longer line is not read; write long content with a tool that writes files), or a shell call that has no command; and a shell that reads its commands from its input (`echo git push \| sh`, `sh <<< '…'`, `bash -s`, `bash` alone, `source <(…)`), which runs what cannot be read before it runs. |
| `hands_guard` | What the hands' guard refuses, with its reason: an assignment of a variable that changes which program runs or what is loaded into it (`PATH`, `LD_PRELOAD`, `BASH_ENV`, …), a line it cannot read, and a line that runs no program. It can only deny. |
| `git_mutation` | `git` with any subcommand but `status`, `log`, `diff`, `show`, `blame`, `ls-files`, `ls-tree`, `rev-parse`, `rev-list`, `grep`, `describe`, `cat-file`, `shortlog`, `merge-base`, `name-rev`, `for-each-ref` and `show-ref`; `branch` and `remote` only to list; a global `-c`, `--config-env` or `--exec-path=`; configuration from the environment that makes git run a program (`GIT_CONFIG_*`, `GIT_EXTERNAL_DIFF`, `GIT_EXEC_PATH`, `GIT_ASKPASS`, `GIT_SSH`, `GIT_SSH_COMMAND`, `GIT_PROXY_COMMAND`, `GIT_EDITOR`, `GIT_SEQUENCE_EDITOR` and `GIT_TEMPLATE_DIR`, in front of the command, given to `env` or exported by an earlier command of the line); and `git grep` with `-O` or `--open-files-in-pager`. The reason says that af makes every commit, branch and push. |
| `suite_command` | A command equal to a suite form once every token that starts with `-`, the value of an option that takes one as its next word (`go test -timeout 5m ./...`, `make -j 4 check`, `make -f Makefile check`: the options of `go`, `make`, `cargo` and `pytest` that the code lists) and every `NAME=value` word (`make check V=1`) is removed from both, and `$(go list ./...)` is `./...`: `make check`, `make -j4 check`, `go test ./...` and `go test -count=1 ./...` are the suite, `go test ./internal/x -run TestY` is not. The reason says to run `af gate run`, which caches the result by tree hash so that the landing reuses it, and that targeted runs are allowed. A flag that selects tests without a word of its own (`mvn -q test -Dtest=X`) is not told from the suite. |
| `af_command` | Any `af` command but `lang detect`, `gate run`, `revert-check`, `--version` and `--schema`: the rest belongs to the loop. |
| `program_not_allowed` | With `allow_programs` set, a program that is not on it, as it is written (`/usr/bin/ls` is not `ls`); `af` and `git` are always allowed, by their name and by an absolute path (`./git` is a file of the checkout), and so are the builtins that start no program (`cd`, `true`, `[`, `test`, `echo`, `printf`, `read`, `export`, `set`, `for`, `case`, `eval`, `trap` and the like), whose own commands are judged in turn. |
| `protected_path` | A write into a protected path through an output redirection; through an argument of `rm`, `rmdir`, `unlink`, `shred`, `mv`, `cp`, `install`, `rsync`, `tee`, `touch`, `mkdir`, `mkfifo`, `truncate`, `chmod`, `chown`, `chgrp`, `patch`, `ln` (every operand: a link to a protected path is a way to write there), `sed -i` or `perl -i`, `dd of=`, or `find -delete` and `-fprint`; and through an option that names the file a command writes (`-o`, `--output`, `--out`, `--outfile`, `--output-file`, `--emit-events`, `-coverprofile` and the other profiles of `go test`), such as `git diff --output=.specs/x` or `af gate run --output .specs/x`. A path is resolved against the checkout (and against the directory of the session and of any `cd` before it in the command), with its links followed, so that a link to a protected directory and another spelling of the checkout are the protected path; its case does not matter, because the file system of a Mac has `.SPECS` where it has `.specs`; and it may start with `~`, `$HOME` or `$PWD`, or have a glob in a component (`.spe?s`, `.sp*`), which is matched against the protected names as the shell would (a glob does not match a name that starts with a dot unless it starts with one). Entries of `protected_paths` are cleaned before they are matched: `./docs/`, `docs//` and `a/../docs` protect `docs`. `rm`, `mv` and `find -delete` also refuse a directory above a protected path, such as `rm -rf .` and `find . -delete`; a `find` with a test before `-delete` does not count as removing its roots. |
| `outside_dir` | A write tool call to a path outside the checkout and the system temporary directory. |

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

Without `.agent-fox/guard.json`, every call detects the repository to find the
suite (the work of [lang detect](#lang-detect)); a loop writes `guard.json` into
the box once, before its rounds.

### The repository map

The brief of a triage or a fix carries a map of the repository that fits a
token budget (estimated as the bytes of the map divided by 4, rounded up): the
directories, the files in them and, as far as the budget allows, the
declarations in the files. Making it reads files and runs nothing.

The map is of the file view of the checkout (see [lang detect](#lang-detect)). Each
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
listed by name only: a language it does
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
`map_outline_skipped` says so, and the map is level 4 or a later one.

The same tree and the same budget give the same bytes, in whatever order the
files are read: a brief that carries the map can rely on it being the same on
every attempt.

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
| `--brain <name>` | `agentkit` (the AgentKit driver; inside the box under `podman`, on the host over the box's tools under `local`) or `scripted` (a replay of a script, with no model; see [development.md](development.md#the-scripted-brain)). An unknown name is a `usage` failure that lists the registered ones. |
| `--sandbox <provider>` | The box provider: `podman` or `local` (see [The providers](#the-providers)). Without it the provider is the one `AF_SANDBOX` names, and without that `podman`, on every operating system. An unknown name is a `usage` failure that lists them; an unknown `AF_SANDBOX` is a `usage` failure that names the variable. When the default provider cannot open, `af` does not fall back to `local`: the `sandbox` failure carries a `fix_hint` that names `--sandbox` and `AF_SANDBOX` (in place of the hint of the failure itself, so that a missing image is told by the message, which names `AF_SANDBOX_IMAGE`). |
| `--budget <usd>` | Stop the brain after this much model cost. A finite number above 0. It needs a model whose price the catalog knows: with another, the `agentkit` brain fails as `usage` and names `AF_MODEL`, since no cost could be counted against it. |
| `--max-turns <n>` | Stop the brain after this many turns. At least 1. |
| `--script <file>` | The script of `--brain scripted`, and only with it. |

A value that is out of range, `--brain scripted` without `--script` and `--script`
with another brain are `usage` failures (exit 2) that name the flag.
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

| Provider | Isolates | What it is |
|---|---|---|
| `podman` | yes | A container of the image `AF_SANDBOX_IMAGE`, or `quay.io/agentfox/tools:latest` without it (see [configuration.md](configuration.md#the-box)); the key of the vendor is on the engine's command line on the host while the box lives. An engine that cannot be reached is `failed`/`sandbox`, not retryable, with the error of the provider (and, off Linux, a pointer to `podman machine start`); an image that is missing and cannot be pulled is `failed`/`sandbox`, retryable, and the message names the image and `AF_SANDBOX_IMAGE`. |
| `local` | no | The tools run in this process, with its privileges, on a copy of the tree. |

Both obey the rules above (`.git` in and never out, the commit on the host); a box
on another machine, the `gateway` of a lease, does too. `box_unconfined` is raised
once for a run on `local`.

#### Where the brain runs

A brain is on the host or in the box:

| Brain | `podman` (or a `gateway`) | `local` |
|---|---|---|
| `scripted` | host | host |
| `agentkit` | box | host |

*Host* is `af`'s process over the box's tools. *Box* is a process that `af` starts
in the box with one command (below). What reaches the box follows from this, and is
the same in the ledger entry `box_open {provider, image, network, placement}` and, for `af fix`,
in the result's `box`:

- **Credentials.** `Spec.Env` is nothing for a brain on the host, and for a process
  the host's value of `ANTHROPIC_API_KEY`, if it is set. A process also gets the
  few settings listed [below](#brains-that-run-as-a-process), and nothing else of
  the host's environment.
- **Network.** `none` for a brain on the host that has no tool that runs a command
  (every tool of `af triage` only reads), and `open` otherwise: a process in the box
  needs its vendor, and a gate that fetches modules needs the network. Podman has
  all of it or none, so a brain in a box on a developer's machine has the whole
  network. Under `local` nothing is enforced, and the network is recorded as `host`.
- **Image and lifetime.** The image is `podman`'s only. The box lives until the
  deadline of the command and ten minutes more.

#### Brains that run as a process

The `agentkit` driver inside a box that isolates runs as a process: `af <agent> run
--run-id <id> --dir <root> --budget … --max-turns … --timeout …`, with `AF_MODEL`
given on the command line. `Session.Exec` runs a command in the box, at its root,
through the box's `execute` tool, and returns the exit code and the last bytes of
the output; a command that exits with a code is not an error.

- **The probe.** Once for each box, before the first process, `af --version` runs in
  it, and `HOME` is checked to be set and
  writable. A failure is `failed`/`sandbox`, not retryable, and the message names
  the binary, the provider and the image. Each probe is the ledger entry
  `box_probe {binary, ok, version}`. When the `af` of a `podman` image has another
  version or commit than the running one, the warning `box_af_version_mismatch` is
  raised and the run goes on: the host checks every submission again.
- **The binary.** Under `local`, `af` is the running executable, by its absolute
  path, and its directory is first on `PATH`. Under `podman` and a `gateway` it is
  the image's.
- **The environment.** The command runs under `env -i` with `PATH`, `HOME` (the
  box's own), the ones of `LANG`, `LC_ALL`, `TZ`, `HTTPS_PROXY`, `HTTP_PROXY` and
  `NO_PROXY` (in either case), `SSL_CERT_FILE`, `SSL_CERT_DIR` and
  `NODE_EXTRA_CA_CERTS` that are set on the host, with their values (the shell of a
  box has not the environment of the host), `TMPDIR` as the box's own when the host
  has one, and the credentials of `Spec.Env`, which the command refers to by name
  so that no secret is in its text or in the ledger. A password in the URL of a
  proxy is not a credential of `Spec.Env`: it is in the text of the command, with
  the rest of the proxy settings. Every argument and every value is single-quoted.
- **Output.** Its standard output and error are redirected to `.agent-fox/run/<id>/<brain>.<attempt>.json` and
  `.stderr` in the box, and copied to the run directory (the output up to 1 MiB, the
  last 64 KiB of the errors) as the artifacts `brain_output`. Nothing in them decides
  whether there is a submission. The ledger entry is `brain_process {brain,
  exit_code, duration_ms}`.
- **The time.** The command is killed, with its process group, when the time of the
  brief or the time left on the command runs out.

The host reads the box through its tools: it lists a directory with the list tool
(at most 500 entries) and takes a file out by exporting that file, so that any
bytes come out exact, at most 32 MiB of them. A symbolic link is not followed.

**The submission.** A brain hands back one JSON object, which af validates
against the tree and writes before it exists: `.agent-fox/run/<id>/submission.json`
in the box, `{kind, run_id, attempt, nonce, submitted_at, payload_sha256,
payload}`, with the attempt and the nonce of `run.json`, which the loop places
before each brain run. The brains of this release submit through the submit core
(`submission.Submit`); a brain that runs commands could write the file by
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

#### The tool gate

Every tool call that the driver of an in-process brain passes
to its guard is decided before it runs: the tool must be in the brief's
allowlist (the tool that ends the run always is), and the rules of
[the tool policy](#the-tool-policy), with the brief's hook as their configuration and
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

### triage

`af triage` diagnoses a report and writes the diagnosis back to the forge. It is
a read-only brain run: the brain reads the checkout, hands over one diagnosis,
and `af` checks it, and does every write itself.

```
af triage <issue|file|text|-> --local [--dir DIR] [--context ANSWER]... [--repo owner/name]
          [--land issue|none] [--label L]... [--dry-run]
          [--brain NAME] [--sandbox NAME] [--budget USD] [--max-turns N] [--timeout 15m]
af triage <input> --workspace SLUG [--server URL] [--wait] [--context ANSWER]... [--timeout D]
```

The loop, `af triage --local`, is the four steps below in one process. (Without `--local` the command
needs `--workspace`: see [where a triage or a fix runs](#where-a-triage-or-a-fix-runs).) Its run
directory is described [below](#the-files-of-a-triage-run). [`af triage run`](#triage-run) is what a
box executes, and no command for a person.

| Command | Takes | Default `--timeout` |
|---|---|---|
| `triage` | the input; `--dir`, `--context`, `--repo`, `--land`, `--label`, `--dry-run`, `--brain`, `--sandbox`, `--budget`, `--max-turns`, `--script` | 15 minutes |
| `triage run` | `--run-id`, `--dir`, `--budget`, `--max-turns` | 15 minutes |

Triage does not need a clean working tree. It reads the tree as it is, runs no
git write, and creates nothing in the checkout outside `.agent-fox/`.

| Flag | Meaning |
|---|---|
| `--land issue\|none` | Where the diagnosis goes. `issue` (the default) writes it to the forge: it replaces the body of the issue that was given, or creates one for any other input. `none` writes `diagnosis.md` in the run directory and nothing else. |
| `--label <label>` | Add this label to the issue; may be repeated. A label is 1 to 50 characters, with no comma and no space at either end; there may be 10 distinct ones, and exact duplicates are collapsed in the order they came. Not with `--land none`. |
| `--repo <owner/name>` | For an input that is not an issue: the repository the issue is created in. |
| `--context <answer>` | An answer to the question that an earlier run stopped on; see [Questions](#questions-and-answers). |
| `--dry-run` | Run the brain as usual and send no write to the forge; the writes are planned, not made. |
| `--brain`, `--sandbox`, `--budget`, `--max-turns`, `--script` | The [shared flags](#brains-and-boxes). `--brain` defaults to `agentkit`, `--budget` to `2` (dollars) and `--max-turns` to `100`; `--sandbox` has no default of its own (`AF_SANDBOX`, else the platform's). |

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
2. **The brief** is rendered, and with it `original.md` and `triage.json`. See
   [model-usage.md](model-usage.md#triage) for what it holds.
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
   is [landed](#the-landing).

A run that ends with no valid submission lands nothing and writes nothing to the
forge: `failed`/`no_result` when the brain stopped for another reason (retryable),
and `failed`/`budget` when it spent its cost or its turns, with `fix_hint`
`{flag: "--budget"` or `"--max-turns", suggest: <twice the value>}`. The other
failures of a brain are those of [Brains and boxes](#brains-and-boxes).

The envelope's `usage` is the brain's. The `step` events are `preflight`,
`brief`, `brain` and `land`, and a run emits them up to the one it ended in.

**The result**:

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
or `probable`: `af fix <issue url> --dir <dir> --local`, so that the suggestion runs as given.

**Warnings** the triage commands raise besides the shell's and those of
[Brains and boxes](#brains-and-boxes): `issue_closed`,
`report_changed_during_triage`, `body_too_long`, `labels_failed`,
`original_report_unparsed` and `rejections_unreadable`
(see [Warning codes](#warning-codes)); `input_truncated` is the shell's, and is
raised again when the report is cut in the brief. No failure category is new.

#### The landing

The landing writes the submission of the run to the forge.

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
   (`created_issue`).
5. Each write is a side effect (`update_issue`, `add_comment`, `create_issue` or
   `add_labels`, with its target and its state `done`, `planned` or `failed`), a
   landed issue is the artifact `{kind: "issue", url, number}`, and `triage.land`
   is recorded.

A failure of a write is `failed`/`auth` without a credential or with one the forge
refuses (a 401 or a 403 that is not a rate limit), `forge` and retryable after a
rate limit or a lost connection, and `forge` and not retryable otherwise, a server
error included. With `--dry-run` the writes are planned and not made; the action is
`planned`.

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
each `--context` value in order, as the answer of a person.

#### The files of a triage run

| File in `.agent-fox/run/<id>/` | Written by | What it is |
|---|---|---|
| `brief.md` | the loop | The brief that was rendered. |
| `original.md` | the loop | The original report, exactly. |
| `triage.json` | the loop | `{format_version: 1, input: {kind, origin, fingerprint}, target: {repo, host}, issue: {repo, host, number, url} or null, original_report_sha256, prefix: {bytes, sha256}}`. The fingerprint is `owner/repo#N` for an issue, else the SHA-256 of the input. |
| `submission.json` | the brain | The accepted diagnosis (see [Brains and boxes](#brains-and-boxes)). |
| `rejections.jsonl` | the brain | One line `{ts, problems, unresolved_paths}` for each refused call. |
| `diagnosis.md` | the landing | The rendered body. |

The ledger of a run holds `triage.input {kind, origin, fingerprint}` when the
input is resolved, `triage.brief {brief, prefix_bytes, prefix_sha256,
document_bytes}` when the brief is written, `triage.submission {submission,
sha256}` when a diagnosis is accepted, `triage.question {text, options}` when it
carries a question, and `triage.land {action, issue, labels}` when a landing
ends, in that order for a full loop, among the entries of the box and the brain.

### triage run

`af triage run --run-id ID` is what a box executes, and no command for a person:
help does not list it. [A process brain](#brains-that-run-as-a-process) runs it in
the box, where `brain.Execute` has placed the `brief.md` of the run and a
`run.json` that holds the attempt, the nonce and `prefix_bytes` (the length of the
brief's prefix). It runs the `agentkit` brain in its own process over `--dir`, on
that brief, and `file_diagnosis` submits into the run directory under the attempt
and the nonce of the host. `--budget` and `--max-turns` are the loop's. The result
is `{run_id, submitted, rejected_path_calls}`, and the status is `done` when a
diagnosis was accepted; the envelope's `usage` is the brain's. A run directory with
no such `run.json` is `usage`, "run <id> has no brief placed in it".

It reads the directory as it is, with no copy of it, and the envelope says so with
the warning `box_unconfined`: the box keeps out the files that git ignores and the
credentials of `.git/config`, and a directory that is not a box does neither.

### fix

`af fix` fixes a reported problem: a verified change on a branch of its own, and a
pull request. The brain edits files in a box and hands over one report; `af`
does every other effect, and measures the change before it lands.

```
af fix <issue|file|text|-> --local [--dir DIR] [--base BRANCH] [--context ANSWER]... [--repo owner/name]
       [--land pr|branch|none] [--attempts N] [--no-verify] [--dry-run]
       [--brain NAME] [--sandbox NAME] [--budget USD] [--max-turns N] [--timeout 45m]
af fix <issue> --workspace SLUG [--server URL] [--wait] [--context ANSWER]... [--timeout D]
```

The loop, `af fix --local`, is the steps below in one process. (Without `--local` the command needs
`--workspace`: see [where a triage or a fix runs](#where-a-triage-or-a-fix-runs).) Its run directory is
described [below](#the-files-of-a-fix-run). [`af fix run`](#fix-run) is what a box executes, and no
command for a person.

| Command | Takes | Default `--timeout` |
|---|---|---|
| `fix` | the input; `--dir`, `--base`, `--context`, `--repo`, `--land`, `--attempts`, `--no-verify`, `--dry-run`, `--brain`, `--sandbox`, `--budget`, `--max-turns`, `--script` | 45 minutes |
| `fix run` | `--run-id`, `--dir`, `--brain`, `--sandbox`, `--budget`, `--max-turns`, `--script` | 45 minutes |

| Flag | Meaning |
|---|---|
| `--land pr\|branch\|none` | Where the change goes. `pr` pushes the branch and opens a pull request; `branch` pushes it; `none` keeps it on a local branch and sends nothing to the forge or the remote. |
| `--attempts <n>` | Up to `n` attempts, each with a budget of its own; from 1 to 5. An out-of-range value is a `usage` failure before anything is read. |
| `--no-verify` | Land without the gate: no baseline and no check after, a draft pull request that says `Refs`, the status `unverified` and the warning `not_verified`. |
| `--base <branch>` | The branch to fix on; it must be the one that is checked out, since a fix is made on the base as it stands. |
| `--context <answer>` | An answer to the question that an earlier run stopped on; see [Questions](#questions-and-answers). |
| `--repo <owner/name>` | The repository the pull request is opened in, when the checkout's origin does not name it. |
| `--dry-run` | Run the brain and the measuring as usual and write nothing beyond the working tree: no issue, branch, commit, push or pull request, which are planned and listed as side effects. The change is discarded. |
| `--brain`, `--sandbox`, `--budget`, `--max-turns`, `--script` | The [shared flags](#brains-and-boxes). `--budget` and `--max-turns` bound each attempt. |

The defaults are `--brain` to `agentkit`, `--sandbox` to the provider that
`AF_SANDBOX` names, and without it `podman`,
`--budget` to `10` (dollars), `--max-turns` to `150`, `--attempts` to `2`,
`--land` to `pr` and `--timeout` to 45 minutes. A pull or merge request as the input is a `usage`
failure, since a fix takes an issue, a file or text.

#### The steps of a fix run

The checks of the repository (the detection of the gate, the baseline, the gate after
the change and the revert check) are the repository's code, and the brain writes
tests: they run in the box of the run, through its `af` (the image's under `podman`
and a `gateway`, this `af` under `local`), and never on this host. So the checks of
this host come first, and a run they refuse opens no box; then the box is opened,
serves the whole run, and the gate and the baseline are made in it (see
[Measuring in the box](architecture.md#the-brain-interface-as-built)).

1. **Preflight.** The checks that need no model run in this order, and the first
   refusal ends the run with no model call, no branch and no write to the forge;
   one of the first five also opens no box.
   1. *The flags and the input*: as above, with the brain and the box provider
      known. A violation is `usage` and names the flag.
   2. *The checkout*: `--dir` is the top level of a git work tree on a branch (a
      detached `HEAD` is `usage`, and so is a `--base` that is not the branch
      checked out).
   3. *A clean tree*: every change in the tree must be the run's own. A dirty tree
      is `failed`/`preflight` and names at most 20 paths.
   4. *The landing*, unless `--land none`: an `origin` remote to push to
      (`failed`/`preflight`; a dry run does not need one) and, for `pr`, a
      repository for the pull request (`usage`, `fix_hint` `--repo`), a
      credential for it (`failed`/`auth`, `fix_hint.env` `GITHUB_TOKEN` or
      `GITLAB_TOKEN`; under `--dry-run` the warning `no_forge_token`; a credential
      that the forge rejects is `failed`/`auth` too) and a repository that can be read
      and is not archived (`failed`/`forge`).
   5. *The brain*: it can be made, which needs the credential of its model
      (`failed`/`auth`, before a box is opened). The full hash of the base is recorded
      as `base_head`. Then the box is opened.
   6. *A gate*, in the box: at least one check that can be run there (see
      [`lang detect`](#lang-detect)), else `failed`/`preflight` with `fix_hint`
      `{flag: "--no-verify"}`. The checkout's own `.agent-fox/gate.json` is placed in
      the box for the detection, and one that cannot be used is `config`.
   7. *The baseline*, in the box: the gate runs once on the tree as it is. A red
      baseline raises `baseline_failing` and the run goes on: a check that already
      fails is not counted against the change. `--no-verify` makes no baseline. A
      closed issue raises `issue_closed`, and the run goes on.
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
   gate. The brain edits the box and submits one report with `submit_fix`, or one
   question. `af` takes the box's
   tree out and **applies** it to the checkout (see below), then **measures** it.
4. **Apply.** The export is mirrored onto the checkout, uncommitted: the files
   it lacks are deleted first, then the new and changed files and links are
   written; `.git/` and `.agent-fox/` (whatever their case), ignored paths, anything
   that is not a file or a link, and everything under a submodule are left alone (and
   counted as `ignored`), and so are the files of the checkout that git tracks and a rule
   ignores, which the export never has and the brain does not delete. A round with no
   accepted report applies nothing, and an export with no file that can be applied is
   `failed`/`sandbox`.
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

**The result** (`$defs` `fix`):

| Field | Meaning |
|---|---|
| `issue`, `issue_created` | The URL of the issue the report is, or `null`; `{number, url}` of the issue that was made for a text or a file, else `null`. |
| `base`, `base_head` | The branch the fix was made on and goes back to, and the full hash of its commit when the run began. |
| `attempts`, `rounds`, `attempts_max` | How many attempts and rounds the run made, and the most it could (`--attempts`). `rounds` counts across attempts. |
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

#### The files of a fix run

| File in `.agent-fox/run/<id>/` | Written by | What it is |
|---|---|---|
| `brief.md` | the loop | The brief of the first round: the prefix, then the task block. |
| `original.md` | the loop | The original report, exactly. |
| `run.json` | the loop | The attempt and the nonce that the submission protocol reads. |
| `fix.json` | the loop | The state of the run: `run_id`, `input {kind, origin, key}`, `issue {url, title, number}` or null, `target {repo, host}`, `base`, `base_head`, `table`, `baseline_gate_id`, `baseline_passed`, `baseline_ms`, `no_verify`, `prefix_sha256`, `prefix_bytes`, `suite`, `programs`, `context`, `rounds`, and the `brain`, `cost_usd` and `cost_source` the rounds so far used. It is written last and atomically. |
| `submission.json` | the brain | The accepted report (see [Brains and boxes](#brains-and-boxes)). |
| `report.md` | the loop | The body of the pull request, as it was rendered. |

The ledger of a run holds `fix.brief`, `fix.question {text, options}` when a run ends on a question,
and, for each round, `apply {written, deleted, ignored}`, `measure {verdict,
closes, changed, reverted, after_gate_id, revert_outcome}` and `refusal {kind}`
when the change was refused, among the entries of the box and the brain.

### fix run

`af fix run --run-id ID` is what a box executes, and no command for a person:
help does not list it. [A process brain](#brains-that-run-as-a-process) runs it in
the box, where `brain.Execute` has placed the `brief.md` of the round and a
`run.json` that holds the attempt, the nonce and `prefix_bytes`. It runs the brain
in its own process over the tree it is in, on that brief, and records `fix.run
{round, submission, submission_path}` in the run directory of the box. The result
is `{run_id, round, submission, submission_path, question, submitted}`, where
`submission` is `report` or `question` and `submitted` says that the brain handed
over a submission that was accepted; the host reads the submission and the tree
from the box, and applies and measures them itself. A run directory with no such
`run.json` is `usage`, "run <id> has no brief placed in it".

### Where a triage or a fix runs

`af triage` and `af fix` run in one of two modes, and the command line says which. The choice is made
before anything else happens: before the flags are parsed for a mode, the input is resolved, a forge
client is built, a run directory is made or a request is sent. An invocation that is refused for its mode
or its flags therefore reads nothing from the forge, sends nothing to the service and leaves no
`.agent-fox/run/` directory.

| Mode | Flag | What it does |
|---|---|---|
| local | `--local` | Runs the loop on this machine, as `af triage` and `af fix` always did: the flags, the defaults, the input, the forge client, the run directory, the events and the envelope are those described above. The one change is that [triage's suggestion](#triage) of a fix ends with `--local`. |
| service | `--workspace <slug>` | Queues a job on the [agent-fox service](#the-client-of-the-agent-fox-service) that `--server` or `AF_URL` names. No loop runs on this machine. |

With neither flag the invocation is a `usage` error (exit 2). Its message says to choose and names
`--workspace <slug>` (on the service that `--server` or `AF_URL` names) and `--local`; its `fix_hint` is
`{flag: "--workspace"}` when a server is named, by the flag or by `AF_URL`, and `{flag: "--local"}`
otherwise. With both flags it is a `usage` error that names both.

**How the words are read.** Only the words before the first `--` are read, and a word after it is the
input. A flag may be written with one dash or two. A word that is the value of a flag that takes one, in
either mode, is that flag's value and never a mode: in `af triage x --context --local` the context is
`--local`, and the invocation names no mode, so it exits 2. `--workspace <slug>` and
`--workspace=<slug>` are the same, and a `--workspace` with no value or an empty one is a `usage` error
that names `--workspace`. `--local` follows Go's boolean syntax, as every boolean flag of the shell does:
`--local` and `--local=true` choose the local mode, `--local=false` counts as not given, and a value that
`strconv.ParseBool` refuses is a `usage` error that names `--local`.

**Flags a mode refuses** are `usage` errors that name the flag. The local mode refuses `--server` and
`--wait`. The service mode refuses the flags only the loop reads: for both commands `--dir`, `--repo`,
`--dry-run`, `--land`, `--brain`, `--sandbox`, `--budget`, `--max-turns` and
`--script`; for `triage`, `--label`; for `fix`, `--base`, `--attempts` and `--no-verify`. Both modes accept
`--context`, `--timeout`, `--output` and `--emit-events`. `af triage --help`, `af help triage` and their
`fix` counterparts describe both modes and the flags of each.

**Only the binary.** The rule belongs to the command line of the `af` binary: the shell is given its
`Mode` hook there and nowhere else. A caller that runs the loops in its own process, such as the worker of
`agent-fox`, makes its shell with `afcmd.New`, runs them without `--local`, and never sends a request to
an agent-fox service.

**In service mode** `af triage <input> --workspace <slug>` and `af fix <input> --workspace <slug>`:

- turn the input into the request by the first rule that matches. `-` reads stdin and is sent as `text`.
  Any `http://` or `https://` URL is sent as the `issue` string, as given after trimming: the client
  does not classify it and reads no forge variable, and the service accepts the URLs of the forges it
  knows and refuses a pull or merge request and any other URL with a 400, which is a `usage` error that
  carries its message. A positive integer, with or without a leading `#`, is the `issue` as a JSON
  number. `owner/repo#N` (two or more `/`-separated segments of letters, digits, `_`, `.` and `-`) is the
  `issue` string. The name of an existing regular file is sent as `file: {name, content}`. Anything else is
  `text`. `af fix` takes only the three issue forms, and any other input is a `usage` error that says so;
- trim text and file content, which must not be empty, hold a NUL byte or be invalid UTF-8 (`usage`);
  a directory is a `usage` error and an unreadable file `io`. Content longer than 131072 bytes is cut at a
  UTF-8 boundary so that it and the truncation marker fit in 131072 bytes, `input.truncated` is set and
  the warning `input_truncated` is raised;
- report the input in the envelope: `kind` is `issue`, `file`, `text` or `stdin`, `origin` is the URL or
  the absolute path of the file (else `""`), and `truncated` says whether it was cut;
- post the body to `POST /api/v1/workspaces/<slug>/triage` or `…/fix`, with the slug path-escaped. The
  `--context` values go in order as `context`, which is absent when there are none;
- on 202 record the artifact `{kind: "job", name: <id>, url: <server>/api/v1/jobs/<id>}` at once.
  Without `--wait` the status is `done`, the result `{job, envelope: null}` and `next` is
  `af jobs watch <id>`, with `--server <url>` when the server came from the flag. With `--wait` the
  command [follows the job](#following-a-job);
- fail a 409 `duplicate_job` as a `conflict` with the message of the service, which names the open job.

## The client of the agent-fox service

Eleven commands make `af` the client of the agent-fox service (the long-running `agent-fox`,
[described at the end of this document](#the-agent-fox-command-line); its routes are in [api.md](api.md), and
the client adds none): [`login`](#login), the four `workspace` commands, the six `jobs` commands, and
`af triage` and `af fix` with [`--workspace`](#where-a-triage-or-a-fix-runs). They speak the one-envelope
contract and the exit codes of every `af` command. None of them builds a forge client, makes a run
directory or runs a model; the work of a job is done by the service, and its cost is in `result.job.usage`.

### The server, the credential and the order of the checks

**The server.** Every service command takes `--server <url>`; without it the URL is `AF_URL`, and an
empty `AF_URL` is unset. It must be an absolute `http` or `https` URL with a host and an optional path
prefix, with no query, no fragment and no user info; one trailing `/` is dropped, and the API is at
`<url>/api/v1/…`. A URL that breaks a rule is a `usage` error that names `--server` or `AF_URL`, whichever
gave it, and the rule (the password of a URL with user info is never echoed). No server is a `usage`
error that names `--server` and `AF_URL` (and `--local` for `triage` and `fix`), with the `fix_hint`
`{flag: "--server", env: "AF_URL"}`. A plain `http` URL whose host is not loopback (`localhost`,
`127.0.0.0/8`, `::1`) raises the warning `server_insecure`, because the credential crosses the link in
clear; the command goes on. No server is stored anywhere: `AF_URL` in a shell profile does the job.

**The credential.** Every request carries `Authorization: Bearer <credential>`. The credential is the
top-level string key `token` of `~/.af/config.toml` (`~` is what `os.UserHomeDir` reports), the file that
`afc login` writes; every other key is ignored and there is no other source. Before any request:

| The file | Failure |
|---|---|
| the home directory cannot be found; the file exists and cannot be read | `failed`/`io`, naming the path |
| the file does not exist; `token` is missing, is not a string or is empty | `failed`/`auth`, naming the file and saying to run `af login --token-file FILE` or `afc login`; `fix_hint` `{suggest: "af login --token-file FILE"}` |
| the file does not parse as TOML; the token holds a line break or a control character | `failed`/`config`, naming the file (never the token) |

On Unix a file with any group or other permission bit set raises the warning
`credential_file_permissions`, naming it; the command goes on.

**The order of the local checks.** A service command runs its checks in this order, reports the first
that fails, and sends a request only when all have passed: (1) the mode and the flags (a missing or
doubled mode, a refused or unknown flag, a malformed value); (2) the positional arguments and the input,
which includes reading stdin, the token file of `af login` and the token file of `af workspace subscribe`;
(3) the server; (4) the credential, or for `af login` the existing credential file. A usage mistake is
therefore always reported before an `auth` or `config` failure.

**Requests.** Every request carries `User-Agent: af/<version>` and `Accept: application/json` (`text/event-stream`
for the event stream), and any body is JSON with `Content-Type: application/json`. A request other than the
event stream lasts at most two minutes, or the time `--timeout` leaves if that is shorter; at most 8 MiB of an
answer is read; a redirect is not followed and no request is sent to its target, so the credential
reaches no host but the server's. `--timeout` has no default limit on any client command. The credential
and the token of a workspace are in no output: not on stdout or stderr, not in the `--output` file, not in
the events or `--emit-events`, whatever the service repeats in its answers: the shell takes a registered secret out of the envelope, its
copies and every event, wherever it stands. A value shorter than 8 bytes is not taken out, since it would be in ordinary
words, and no credential is that short. The client uses the standard proxy variables of Go (`HTTP_PROXY`,
`HTTPS_PROXY`, `NO_PROXY`), as the service does for its calls to hub: behind a proxy for a plain `http` server, the
proxy sees the request.

**Answers.** The service's documents are carried into the results as received, so fields that `af` does
not know survive. A failure is mapped by the status of the answer:

| The service answers | Status (exit code) | `error.category` | `retryable` |
|---|---|---|---|
| 2xx | the command's | | |
| 400 | `usage` (2) | `usage` | false |
| 401 | `failed` (1) | `auth` | false |
| 404 | `usage` (2) | `usage` | false |
| 409 | `failed` (1) | `conflict` | false |
| 422 | `failed` (1) | `auth` | false |
| 502 | `failed` (1) | `hub` | true |
| 500 | `failed` (1) | `service` | false |
| 503, 504 and any other 5xx | `failed` (1) | `service` | true |
| a 3xx, or any other 4xx (405, 413 and 415 among them) | `failed` (1) | `service` | false |
| a transport error, or the two minutes running out | `failed` (1) | `service` | true |
| a body that is too large or is not the JSON expected | `failed` (1) | `service` | false |

The message is the service's `error.message` followed by its `error.code` in parentheses, or "the service
answered 500" when the body has neither. A transport error names the server URL and what gave it
(`--server` or `AF_URL`); a 404 names the job id or the workspace slug that was asked for and says that it
does not exist or that hub does not let the caller see it. The `fix_hint` is `{suggest: "af login
--token-file FILE"}` for a 401; `{suggest: "af workspace subscribe <slug> --hub-token-file FILE"}` for a
404 on a workspace route and a 409 `not_subscribed` or `token_unreadable` (the slug is the real one on a
workspace route and `<slug>` on a job route, which does not name it); and `{flag: "--hub-token-file"}` for a 422.

### login

```
af login --token-file FILE|- [--server URL]
```

Stores the hub credential, an API key or a personal access token, in `~/.af/config.toml`. The credential
is read from the file, or from stdin for `-`, and never from the command line, where it would land in the
shell history. The steps run in this order, and one that fails ends the command with nothing written:

1. **Read the credential.** Trimmed, it must be non-empty, on one line, valid UTF-8, free of control
   characters and at most 4096 bytes: a source that cannot be read is `failed`/`io` naming the file or
   stdin, and a content rule that is broken is a `usage` error naming `--token-file`.
2. **Find the server** as above.
3. **Check the credential file that is there.** It must be readable (`io`) and parse as TOML (`config`), and
   hold, before any table header, a `token = "…"` or `token = '…'` line of its own whose value is a one-line
   string, optionally followed by a comment. Otherwise it is `failed`/`config`, naming the file and saying that
   its layout is not the one `af` expects and that `afc login` or an edit fixes it.
4. **Check the credential with the service** by `GET /api/v1/workspaces`. A 2xx accepts it. A 401 refuses it:
   `failed`/`auth`, saying that the service refused this credential. Any other answer, a transport error or the
   request running out fails as the table above maps it (a 502 is `hub`, retryable), with a message that says the
   credential could not be checked and never that it was refused.
5. **Write the file.** With none, `~/.af` is created with mode 0700 and the file holds the one line
   `token = "<credential>"`. With one, only the value on the `token` line is replaced, by the credential as a
   TOML basic string, and every other byte stays as it was: comments, other keys, tables, line endings. The
   content goes to a temporary file in `~/.af` with mode 0600, which is renamed over the old file. A step that
   fails is `failed`/`io` naming the file; the temporary file is removed and the old file is as it was.

The result is `{credential_file, server}`, never the credential.

### workspace subscribe, list, get and unsubscribe

| Command | Request | Result |
|---|---|---|
| `af workspace subscribe <slug> --hub-token-file FILE\|-` | `PUT /api/v1/workspaces/<slug>` with `{hub_token}` | the subscription view |
| `af workspace list` | `GET /api/v1/workspaces` | `{workspaces}` |
| `af workspace get <slug>` | `GET /api/v1/workspaces/<slug>` | the subscription view |
| `af workspace unsubscribe <slug>` | `DELETE /api/v1/workspaces/<slug>` | `{slug}` on 204 |

`subscribe` reads the token of the workspace under the rules of `af login` (an unreadable file or stdin is `io`
naming it; a content rule that is broken is a `usage` error naming `--hub-token-file`), and treats 201 (the summary says subscribed) and 200 (replaced) as
success. The slug is a required, path-escaped argument whose syntax is the service's to check, so a 400 is a
`usage` error. `unsubscribe` says that the open jobs of the workspace were cancelled; the service waits up to its
grace period for the runners, so it can take a minute.

### jobs list, get, watch, cancel, answer and retry

| Command | Request | Result |
|---|---|---|
| `af jobs list [--workspace S] [--state S] [--cursor C]` | `GET /api/v1/workspaces/<S>/jobs` with `--workspace`, else `GET /api/v1/jobs`; `state` and `cursor` only when given | the page `{jobs, next_cursor}` |
| `af jobs get <id>` | `GET /api/v1/jobs/<id>` | `{job, envelope: null}` |
| `af jobs watch <id> [--since N]` | [follows the job](#following-a-job) | the job result |
| `af jobs cancel <id>` | `POST /api/v1/jobs/<id>/cancel` | `{job, envelope: null}` |
| `af jobs answer <id> (<text> \| --context <text> \| --option <id>) [--wait]` | `POST /api/v1/jobs/<id>/answer` | `{job, envelope: null}`, or the job result with `--wait` |
| `af jobs retry <id> [--wait]` | `POST /api/v1/jobs/<id>/retry` with `{}` | `{job, envelope: null}`, or the job result with `--wait` |

`list` gives one page, and when `next_cursor` is not null `next` repeats the command with `--cursor <next_cursor>`
as its last two words; a 400 is a `usage` error. `answer` takes exactly one answer (none, or more than one, is a
`usage` error and sends nothing). An `--option` value is uppercased and sent as `{option}` without reading the job.
A text answer, positional or `--context`, must not be empty after trimming; `af` reads the job, and text that equals
an option id of the blocked question, ignoring case, is sent as that `{option}`, so that the label of the option is
the answer, and any other text as `{answer}`. A 409 (`invalid_state`, `not_subscribed`, `token_unreadable`) is a
`conflict`. `--wait` on `answer` and `retry` records the job artifact and follows the job; `cancel` takes no `--wait`.

### Following a job

`af jobs watch`, `--wait` on `af triage`, `af fix`, `af jobs answer` and `af jobs retry` read
`GET /api/v1/jobs/<id>/events?since=<n>`, a server-sent event stream, from the start (or from `--since`), and
stop when the job is at rest: its state is `blocked`, `done`, `failed` or `cancelled` and none of its attempts is
`running`. Each event of the service, `{seq, ts, job_id, attempt, type, data}`, is delivered once and in order
(an event whose `seq` is not above the last one is dropped) and emitted as the event `job_event`; `watch` also writes
it to stderr as one line of compact JSON, and `--wait` writes nothing to stderr. A line of the stream may be up to
1 MiB; comments and unknown fields are ignored.

**Tries.** A try asks for the stream from the last `seq` delivered and, once the stream ends, reads
`GET /api/v1/jobs/<id>`. A stream ends at its end, on a dropped connection, on a frame that is not an event, and after 60
seconds without a byte. If the read says the job is at rest, the following is over. Otherwise another try follows: after
1 second when the try delivered an event, which also clears the count of failed tries, and after 1, 2, 4 and 8 seconds after
the first four failed tries in a row. A try fails when it ends without the job at rest and delivered no new event, whatever
ended it, a 5xx or a transport error on either request included (a request for the stream that fails reads no job). The fifth failed try
in a row ends the following with the category of the last failure (`hub` for a 502, else `service`), retryable, naming the job and saying
that it may still be running; `next` is `af jobs watch <id> --since <last seq>`, with `--server <url>` when the server came from the flag.
A 401, a 404, a 3xx or any other 4xx on either request ends it at once, mapped by the table above.

**What it ends with.** The job is read with its attempts, the envelope of the highest-numbered attempt whose state is not
`queued` or `running` is fetched from `GET /api/v1/jobs/<id>/attempts/<n>/envelope` (a 404 leaves it null; another failure
leaves it null and raises the warning `attempt_envelope_unavailable`), and the result is `{job, envelope}`. The warnings of that
envelope are appended to the command's own; its artifacts, side effects, `next` and usage are not copied, and `usage` stays
the zero usage of a run with no brain. The status follows the `result` of the job, which the service fills from the latest
finished attempt:

| Job state | Status (exit code) |
|---|---|
| `done` | `done` (0), or `unverified` (4) when `result.status` says so |
| `blocked` | `needs_human` (3) with the question and the labels of the options in order; `resume` is `{argv: ["af", "jobs", "answer", <id>, "--wait"], flag: "--context"}`, with `--server <url>` in `argv` when the server came from the flag |
| `failed` | the failure of `result.error` (its category, message, `retryable` and `fix_hint`; `internal` when there is none), `unverified` (4) or `usage` (2) when `result.status` says so |
| `cancelled` | `failed`/`job_cancelled` (1), not retryable, whatever the category of the attempt's envelope |

A failed or cancelled ending has no `result`, because the shell clears the result of a failed envelope; the job is in the
`artifacts` and `af jobs get <id>` shows it. Answering with `--context B` appended to the resume `argv` sends
`{option: "B"}` and follows attempt 2.

**When `af` itself is stopped.** A SIGINT, SIGTERM or SIGHUP, or `--timeout` running out, while following ends the
following and never cancels the job, which is durable on the service: the envelope is the shell's `failed`/`cancelled` (a
signal sent to `af`, which the category `job_cancelled` is not) or `failed`/`timeout`, the job artifact stays, and `next`
holds `af jobs watch <id>` and `af jobs cancel <id>` (each with `--server <url>` when the server came from the flag). A
following that reaches rest has neither entry.

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

A command registered with `RawStdin` is given the process's stdin, unread, as
`Run.Stdin`; a command without it gets `nil` and reads nothing.

## Error categories

| `error.category` | Used for |
|---|---|
| `usage` | The invocation is wrong. Status `usage`. |
| `auth` | An issue was not found without a token, so it may be private, or the `agentkit` brain has no credential for its model (in a box, no `ANTHROPIC_API_KEY`). `fix_hint.env` names the variable to set (a forge token, or `ANTHROPIC_API_KEY`). |
| `forge` | The forge could not be reached, refused, or the client could not be built. `retryable` is true after a rate limit, a server error or a lost connection, and false after a refusal (a write of `triage` that gets a server error is not retryable). |
| `io` | A file, stdin or the run directory could not be read or written; also the credential file (`~/.af/config.toml`) of a service command when the home directory cannot be found or the file cannot be read, and the writing of it by `af login`. |
| `cancelled` | The process was sent SIGINT, SIGTERM or SIGHUP. It is a signal sent to the `af` process; a job of the service that was cancelled is `job_cancelled`. |
| `timeout` | `--timeout` ran out, or the time a brief allows ran out with no submission (it counts from the start of the run, so placing the brief is part of it). Not retryable; `fix_hint` is `{flag: "--timeout", suggest: <twice the value>}`. |
| `internal` | A bug: a plain error, a panic, an inconsistent outcome, a result that is not JSON. |
| `config` | A configuration file `af` reads cannot be used, for the service client the credential file that does not parse as TOML or whose token holds a control character, or whose layout `af login` will not edit; also, for example a `.agent-fox/gate.json` that does not parse, or the `.agent-fox/guard.json` that a brain's tool gate loads. The message names the file. The `agentkit` brain also uses it when the model provider cannot be configured from the environment. |
| `restore` | A tree that a command changed in order to measure it could not be put back, or a leftover of an earlier measurement stands in the way. The message names the directory that holds what is left. |
| `git` | git failed in a way the command cannot work around. |
| `sandbox` | The box could not be opened, synced or exported. The provider could not create it (not retryable: `podman` without a reachable engine, see [the providers](#the-providers)); the image of a `podman` box is missing and cannot be pulled, the checkout could not be synced in, or the tree could not be exported out (retryable); a process brain's binary or `HOME` fails the probe in the box (not retryable); or an export carried a `.git`, which stops the run. When the provider was chosen by default, `fix_hint` names `--sandbox` and `AF_SANDBOX`. |
| `brain` | A brain failed: it returned an error and left no submission (retryable), or it panicked (not retryable; the stack is in `brain-panic.<attempt>.txt` in the run directory). |
| `no_result` | A brain ended without a valid submission: it stopped for another reason than the cost or the turns, or the submission file did not count. Retryable. |
| `budget` | A brain used its cost bound or its turn bound and left no submission. Not retryable; `fix_hint` is `{flag: "--budget"` or `"--max-turns", suggest: <twice the bound>}`. |
| `submission` | A submission was refused; nothing was written, and the message names every problem. Retryable. No command of this release ends with it: a refusal is the brain's next turn. |
| `preflight` | A fact about the repository stops `fix` before it spends anything, and it is not an error of the invocation: the working tree is not clean (the message names up to 20 paths), no check of the gate can be run here (`fix_hint` `{flag: "--no-verify"}`), or `--land` needs an `origin` remote and there is none. Not retryable. |
| `service` | The agent-fox service could not be reached or gave an answer that `af` cannot use: a transport error, a 5xx, a redirect (which is not followed), a 4xx that no command should provoke (405, 413, 415), a body that is too large or is not the JSON expected, or a request that ran past its two minutes. Retryable after a transport error, the bound and a 5xx other than 500. |
| `conflict` | The service refused a request because of the state of a job or a workspace (409): `invalid_state`, `duplicate_job`, `not_subscribed`, `token_unreadable`. Not the invocation, and not retryable. |
| `hub` | The service could not get an answer from hub (502). Retryable. The same category the worker of the service uses for hub outages. |
| `job_cancelled` | A job that `af` followed was cancelled on the service. Not retryable. |

`auth` is also the category of a credential that is missing or that the service refuses (401, with `fix_hint` `{suggest: "af login --token-file FILE"}`) and of a workspace token that hub refuses (422, `fix_hint` `{flag: "--hub-token-file"}`). Later specs add categories of their own to this list.

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
| `server_insecure` | A service command: the server is a plain `http` URL whose host is not loopback (`localhost`, `127.0.0.0/8`, `::1`), so the credential crosses the link in clear. The command goes on. |
| `credential_file_permissions` | A service command, on Unix: `~/.af/config.toml` has a group or other permission bit set. The message names the file; the command goes on. |
| `attempt_envelope_unavailable` | `jobs watch` and `--wait`: the job is at rest and the envelope of its last finished attempt could not be fetched (any failure but a 404, or an envelope that is not JSON), so `result.envelope` is null. |
| `no_checks` | `gate run`: no check of the gate table can be run here, or the table has none. Nothing ran and `passed` is false. |
| `tree_changed_during_gate` | `gate run`, `fix`: the checks changed the tree. The result is kept under the hash of the tree they ran on. |
| `fix_not_proven` | `revert-check`, `fix`: the outcome is not `proven` and not `no_changes`, so the change must not close its issue; the commit says `Refs`. |
| `map_outline_skipped` | `triage`, `fix`: the declarations were not read, because the names of the files alone do not fit the budget or the view has more than 5000 files. The map has the names only, or less. |
| `check_unavailable` | `lang detect`: a check of the gate table names a program that cannot be run on this machine. The check stays in the table with `available: false`; the message names the check. |
| `box_unconfined` | A box was opened with the `local` provider, once for each box: it has no process or network isolation, because the tools run in this process with its privileges. Also `triage run`, which reads the directory in place, with no copy of it: the files git ignores and `.git/config` are within reach of the tools. |
| `box_af_version_mismatch` | The `af` of a box that a process brain runs (in practice the one of a `podman` image, since the other providers run this very executable) has another version, or another commit when both are known, than the running one. The run goes on, since the host checks every submission again; the message names both. |
| `brain_usage_unavailable` | The driver in a box left no envelope of `af <agent> run` that could be read, so the run has no cost to report: the cost is 0 and `cost_source` is `none`. |
| `box_close_failed` | The box could not be closed, also one that could not be synced and was closed for that. The result of the run is what it was; the message carries the error. |
| `submission_invalid` | The submission file in the box did not count: it did not parse, is not for this run's kind, run, attempt or payload hash, or the kind refused its payload against the box's tree. The message lists the problems, and the run has no submission. |
| `submission_claim_mismatch` | A brain says it submitted and there is no valid submission file, or says it did not and there is one. The file decides. |
| `brain_error` | A brain returned an error, or panicked, after it had submitted, which does not change the submission. The message carries the error. |
| `submission_not_removed` | The run directory held a `submission.json` that af did not accept (one of an earlier attempt, or one that a process of the brain wrote), and it could not be removed. |
| `brain_output_unreadable` | A process brain's output or errors could not be read out of the box or copied to the run directory; the run goes on, and the file is not among the artifacts. |
| `model_unpriced` | The `agentkit` brain's model is not in the catalog, so the run has no cost to report (`cost_source` is `none`). With `--budget` the run does not start: it is a `usage` failure. |
| `transcript_unwritable` | The transcript of an in-process brain could not be written whole. The run goes on and the file is partial. |
| `ledger_unwritable` | A ledger entry could not be written: at most once for the box and once for the brain run, since each reports its own. |
| `issue_closed` | `triage`, `fix`: the issue is closed; the run goes on. |
| `report_changed_during_triage` | `triage`: the body of the issue is not the report the brief had. The report as the issue has it now is the one kept in the body. |
| `body_too_long` | `triage`: the body with the diagnosis would be longer than the forge allows, so the diagnosis was posted as one comment and the body was left. |
| `labels_failed` | `triage`: the diagnosis was written and adding the labels failed. The side effect `add_labels` is `failed`. |
| `original_report_unparsed` | `triage`: a body starts with the triage marker and is not what `af` wrote (the markers of its original report cannot be paired, a heading is missing, or text follows the original report), so the whole body is taken as the report. |
| `rejections_unreadable` | `triage`: `rejections.jsonl` cannot be read, or holds a line that is not JSON, so the refused calls are counted as 0; also when it could not be brought out of the box. |
| `baseline_failing` | `fix`: the gate fails on the tree as it is, before any change. A check that already fails is not counted against the change, and a change that makes it pass is `pass_was_already_failing`. |
| `question_not_found` | `fix`: no earlier run asked the question that a `--context` answers, so the answer stands alone in the brief. |
| `protected_path_reverted` | `fix`: the change touched a protected path (`.git/`, `.agent-fox/`, `.specs/`, `.github/workflows/`, `.gitlab-ci.yml`), and `af` put it back to the base's content or deleted it. One for each path. |
| `scope_mismatch` | `fix`: the report declares files that did not change, or the change has files the report does not declare. Both lists are in the message, and nothing is refused. |
| `criteria_unmet` | `fix`: a criterion of the report is `unmet` or `partial`, so the pull request is a draft that says the work is unfinished. |
| `not_verified` | `fix`: the change was landed without the gate (`--no-verify`): nothing was measured, and a person has to check it. The status is `unverified`. |
| `no_forge_token` | `fix` with `--dry-run` and `--land pr`: opening a pull request needs a credential and none is set; a dry run goes on without one. Without `--dry-run` it is `failed`/`auth`. |
| `push_retried` | `fix`: a push failed and is tried again, up to 3 attempts in all. |
| `commit_hook_failed` | `fix`: a hook of the repository refused the commit, so the change was parked as `wip` without the hooks. |
| `pull_request_failed` | `fix`: the branch is pushed and the forge refused the pull request. The change landed, and the status is not changed. |
| `pull_request_skipped` | `fix`: no pull request was opened, because there is no issue for it to refer to (the issue of a text or a file could not be made). |
| `issue_creation_failed` | `fix`: the issue for a text or a file could not be made; the change goes on without one. |
| `fix_not_landed` | `fix`: the change did not land and is parked on a branch of its own; the message names the verdict and the branch. The status is `unverified`, or `failed` with `cancelled` or `timeout` for a run that was stopped and parked. |
| `push_failed` | `fix`: a parked branch could not be pushed and is kept here; `next` names the push. |
| `attempt_failed` | `fix`: the last round of the run came to no report, and the change in the tree is parked as it is. The message carries why. |
| `park_failed` | `fix`: the run was stopped and the change that was applied could not be parked; it is in the working tree. |
| `dry_run_tree_kept` | `fix --dry-run`: the change of the dry run could not be discarded (the checkout is not at the base any more, or git failed), and is left in the working tree. |
| `question_tree_kept` | `fix`: the run ended on a question and the change that an earlier round applied could not be put back (the checkout is not at the base any more, or git failed), and is left in the working tree. |
| `report_unwritable` | `fix`: `report.md` could not be written; the pull request has its body all the same. |

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
`argv[0]` replaced by `af`, unless the command supplies its own: a job of the service that is blocked ends a
command with `resume.argv` `["af", "jobs", "answer", <id>, "--wait"]` (and `--server <url>` when the server came
from the flag), because resuming the original command would queue a second job, which the service refuses as a
`duplicate_job`; `resume.flag` is `--context` all the same, and the answer is appended to it. Run it with
`--context <answer>` appended to resume. A command that opts into `--context` takes it any number of times;
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
`result` is null or a reference to the command's schema. The schema of `triage` and of `fix` is a `oneOf`: the
result of the local loop, or the job result `{job, envelope}` of the service mode. The schemas of the client commands
(`login`, `workspace …`, `jobs …`) name the fields `af` relies on and allow the others, which the service may add.

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
    <brain>.<attempt>.json, <brain>.<attempt>.stderr    what a brain that runs as a process printed, listed as {kind: "brain_output", path}
    brief.md, original.md, triage.json, rejections.jsonl, diagnosis.md    the files of a triage run
    brief.md, original.md, run.json, fix.json, report.md    the files of a fix run
```

The ones from `brief.<attempt>.md` to the process files are written by a command that runs a brain
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
command ran. A command emits its own types through `Run.Emit`. A command that follows a job of the service emits `job_event`
(`{seq, ts, job_id, attempt, type, data}`, the event document of the service as it was received) for each event it
delivers. A command that
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
submitted_claim, cost_usd, cost_source, turns, duration_ms, input_tokens,
output_tokens}`, for a brain that runs
as a process `box_probe {binary, ok, version}`, `brain_process {brain, exit_code,
duration_ms}`, and, for what it
found in the box after the brain, `submission_accepted {attempt, sha256}`,
`submission_invalid {attempt, problems}`, `submission_stale {attempt,
file_attempt}` or `submission_absent {attempt}`. `triage` adds `triage.input`,
`triage.brief`, `triage.submission`, `triage.question` and `triage.land` (see
[the files of a triage run](#the-files-of-a-triage-run)), and `fix` adds
`fix.brief`, `fix.run`, `fix.question`, `apply`, `measure` and `refusal` (see
[the files of a fix run](#the-files-of-a-fix-run)).

## The agent-fox command line

`agent-fox` is the long-running service of agent-fox: one process, started from one configuration
file, that keeps its state in one SQLite file and asks [hub](https://github.com/agent-fox-dev/hub) who
is calling. `make build` writes it to `bin/agent-fox` with the same version, commit and build time as
`bin/af`. It is not a command of the shell above: it has no envelope, and it runs until it is stopped.
[configuration.md](configuration.md#the-agent-fox-service) describes the file, and [api.md](api.md) what
it serves.

```
agent-fox [--config PATH]
agent-fox --version
agent-fox --help
```

| Flag | What it does |
|---|---|
| `--config PATH` | The configuration file. Without it the file is the first that exists of `./config.toml` and `$XDG_CONFIG_HOME/agent-fox/config.toml` (`$HOME/.config/agent-fox/config.toml` if `XDG_CONFIG_HOME` is unset, empty or not an absolute path). If none exists, start fails and the error lists the paths it tried. |
| `--version` | Prints the version document, one JSON object, and exits 0. It reads no configuration. |
| `--help` | Prints the usage on stdout and exits 0. It reads no configuration. |

```
$ agent-fox --version
{
  "name": "agent-fox",
  "version": "v0.1.0",
  "commit": "f79e3fe",
  "build_time": "2026-10-22T08:14:28Z",
  "go_version": "go1.27.2"
}
```

The document is the one of [`af --version`](#the-af-command-line), with the name `agent-fox`, and it is
what `GET /version` answers. An unknown flag, a flag without its value, and a positional argument print
the error and the usage as plain text on stderr and exit 2; it is the one thing on stderr that is not a
JSON record.

### What it writes

Nothing is written to stdout but the output of `--version` and `--help`. Stderr is a log: **one JSON
record on each line** (`log/slog`), from the first line the process writes. The records are:

| `msg` | `level` | Attributes | When |
|---|---|---|---|
| `start failed` | `ERROR` | `error` | The service could not start. It is the only record such a run writes, and the `error` names the key, file, address or reason (see below). |
| `hub.url is plain http to a host that is not a loopback address: caller tokens and workspace tokens cross the link in clear` | `WARN` | `hub_host` | `hub.url` is `http` and its host is not `localhost` or a loopback address. The service starts all the same. It is written after the listener is bound and before `listening`. |
| `listening` | `INFO` | `addr` | The listener is bound. `addr` is the address it is bound to, so that `listen = "127.0.0.1:0"` gives a port to read here. |
| `request` | `INFO` | `method`, `path`, `status`, `duration`, `user` | One for each request. `path` has no query, `duration` is in nanoseconds, and `user` (the hub user id) is there when the request showed who is calling. The `Authorization` header and the bodies are never logged. |
| `hub is unavailable` | `WARN` | `error` | A call to hub could not be answered; the request got `502`. |
| `panic in handler` | `ERROR` | `method`, `path`, `panic`, `stack` | A handler panicked. The request got `500`; the process goes on. |
| `the subscription could not be stored`, `the subscriptions could not be listed`, `the subscription could not be read`, `the subscription could not be removed` | `ERROR` | `error` | The database failed on a request; the request got `500`. |
| `an unexpected failure of the check of the caller` | `ERROR` | `error` | Checking the caller with hub failed in a way that is neither a refusal nor hub being unavailable; the request got `500`. |
| `the listener failed` | `ERROR` | `error` | The listener failed after the start. The service exits 1. |
| `requests in flight did not finish within the grace period` | `WARN` | `error` | The 30 seconds ran out while the service stopped; it closed what was open and exited 0 all the same. |
| `the database could not be closed` | `ERROR` | `error` | The database reported an error as it was closed at the end. |
| `http: …` | `WARN` | none | What Go's HTTP server itself reports (a handler that wrote its status twice, for one), as a record and not as a line of text. |

The key file, a token and the `Authorization` header are in none of them.

```
{"time":"2026-10-22T10:14:39.492024+02:00","level":"ERROR","msg":"start failed","error":"config file /etc/agent-fox/config.toml: unknown key server.external_url"}
{"time":"2026-10-22T10:14:47.581638+02:00","level":"INFO","msg":"listening","addr":"127.0.0.1:62760"}
{"time":"2026-10-22T10:14:47.728386+02:00","level":"INFO","msg":"request","method":"GET","path":"/healthz","status":200,"duration":66708}
```

### Exit codes

| Exit code | Meaning |
|---|---|
| `0` | The service was stopped by SIGINT or SIGTERM, or it printed the version or the usage. |
| `1` | The listener could not be bound (the address is in use, not allowed, does not resolve, or is not this host's), or the listener failed once the service was serving. |
| `2` | The command line, the configuration, the key file or the database is wrong, or a database was written by a newer `agent-fox` (it records a migration this binary does not know). The operator has to change a file. |

A failed start writes exactly one record (`start failed`) and nothing listens. The `error` names what
is wrong: the dotted key of the configuration (`unknown key server.external_url`,
`hub.auth_cache_ttl: 16m is outside 1s to 15m0s`, `the environment variable HUB is not set` with the
key that holds it), the key file and the reason (it is missing, is not base64, is not 32 bytes, or has a
group or other permission bit set), the database file, or `server.listen` with the address and the reason.

### Starting and stopping

The service starts in this order: the configuration, the key file, the database (it is created with its
directory when it is missing, and migrated), and the listener. A configuration or a key that is wrong
leaves no database behind.

On SIGINT or SIGTERM the service stops accepting connections, gives the requests in flight up to 30
seconds to finish, closes the database and exits 0. A second signal while it waits changes nothing (only
SIGKILL ends it sooner). `GET /healthz` answers while the process serves; `GET /readyz` answers when the database does.

