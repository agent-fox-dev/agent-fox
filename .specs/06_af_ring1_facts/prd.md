---
spec_id: "06"
spec_name: "af_ring1_facts"
title: "Ring-1 fact commands: lang detect, gate run/compare, revert-check, guard check and the repository map"
status: "active"
created_at: "2026-10-20T07:39:04.701224Z"
updated_at: "2026-10-20T07:39:04.701224Z"
intent_hash: "7a7044ee3a8e51c8a33d76daf0877a2f61589553a89be135e49367d0643660c1"
schema_version: 2
source: "docs/prd/02-rebuild-triage-and-fix-on-the-toolbelt.md"
---
## Intent

Give `af` its ring-1 fact commands. These answer what a repository is written in and how it is checked, what the checks measured on a given tree (cached by tree hash) and how two measurements compare, whether the tests of a change prove the change, whether a brain may make a given tool call, and what the code looks like within a token budget. Each is a repeatable command that writes nothing outside the git-ignored `.agent-fox/` directory, so any brain or person may call it as often as they like. Each is also a Go package that the triage and fix loops call in-process. Every later effect (landing, `Closes` versus `Refs`, parking) is then decided from facts `af` measured, never from a brain's claim.

## Goals

1. `af lang detect` returns the expected languages, gate checks, suite forms and gate programs for fixture repositories: a Go module, a Makefile with a `check` target, a Node package with a pnpm lockfile, a uv-managed Python project, a Cargo crate, a Maven project, and an empty directory. On this repository it reports `go` with the single check `make check`.
2. Running `af gate run` twice on an unchanged tree executes each check once. A counter script proves it, and the second envelope says `cached: true`. Changing one byte of any file in the tree view makes the next run execute again. Ignored files do not affect the cache.
3. `af gate compare` produces each of `pass`, `pass_was_already_failing`, `regressed`, `still_failing` and `unverified` from recorded results, addressed by label or by id.
4. `af revert-check` reports `proven` on a fixture whose new test fails without the fix, and `not_proven` on one whose test passes without it. In every outcome it leaves the tree hash and the git index exactly as they were, including when it is cancelled mid-run.
5. `af guard check` behaves the same way in the envelope form and in the `--host claude` form:
   - It denies `make check` and `go test ./...` with a reason that names `af gate run`.
   - It allows `go test ./internal/x -run TestY`, `git diff` and `af gate run`.
   - It denies `git commit`, `git push`, `af fix land`, and a `Write` to `.specs/x.md`.
6. `af map --tokens N` never produces more than N estimated tokens, and the same tree always gives byte-identical output.
7. `make check` passes offline. Each new command's result schema is in the committed `af --schema` golden. `docs/cli.md`, `docs/configuration.md`, `docs/model-usage.md`, `docs/architecture.md`, `docs/development.md` and `README.md` describe what this scope ships, and `make doc` has been run and committed.

## Non-goals

- **The brains, the brief and `HookConfig`.** These belong to `af_brains`. That scope also writes the per-run `.agent-fox/guard.json` and `.agent-fox/gate.json` and syncs them into the box. This scope defines those file formats and reads them. It does not decide what a run puts in them.
- **The Claude Code settings file that installs `af guard check` as a `PreToolUse` hook.** This belongs to `af_boxes_claude`. The same goes for the `landlock`, `podman` and `none` boxes.
- **Triage and fix themselves.** These belong to `af_triage` and `af_fix`. That includes the scope check, the protected-path revert on the exported tree, and the decision to land, park or draft. This scope only supplies the facts those decisions read.
- **`af conform scan`, `af scope check`, `af spec validate` and `af serve` (MCP).** They are not in this split.
- **Writing to the run directory's ledger.** Ring-1 commands create no run directory. The loop records what it called.
- **Cache eviction, a cache shared across machines, and locking between concurrent gate runs.**
- **Language profiles beyond the detection table in R2, and a gate configuration committed to the repository.** The repository's Makefile is how it declares its gate.
- **Relevance ranking or focus paths in the map, and the `code_search` index.**
- **Making the hook a security boundary.** `af guard check` is prevention. It is best-effort, and the gate and the box are the invariants.
- **Changing `issuex`.** Ring-1 commands do not use the forge.

## Background

`docs/architecture.md` (*The toolbelt → Ring 1: facts*) names these commands:

- `af lang detect`, built from `internal/project`;
- `af map`, built from `internal/repomap` on the hands' `Walk` and `outline`;
- `af gate run` and `af gate compare`, built from `internal/checks`, with results cached by tree hash;
- `af revert-check`, built from `internal/conform`;
- `af guard check`, which reads a host's hook payload. Per *The policy hook*, it applies the hands' `guard.Check` plus the run's rules: the spec directory is protected, mutating git subcommands are refused, and the suite command is refused with `af gate run` named instead.

The architecture also says the gate cache is keyed by tree hash on both sides of the box, so a gate the brain ran inside is a cache hit for the landing gate.

None of these packages exists in the repository. The previous implementations live only at commit `1e6458d`, which cannot be read. What does exist:

- **`internal/gitx`** has:
  - the `Runner` type, which separates the exit code from exec failures;
  - `ExecRunner`, `ReducedEnvRunner` (credential variables stripped through the hands' `tools.ReducedEnv`) and `HermeticRunner(home)`, all of which kill the whole process group on timeout;
  - `TrackedFiles` (`ls-files --cached --others --exclude-standard`), `ChangedFiles`, `ShowFile` and `CheckoutPaths`.

  The list methods split on newlines and trim spaces, so names with special characters come back quoted by git and can break. This scope adds NUL-separated variants.
- **The verdict table** is in `skills/af-fix` §6: green→green `pass`, red→green `pass, baseline was failing`, green→red `regressed`, red→red `still failing`, nothing ran → `unverified`. The skill also tells the brain to run tests "in the suite's targeted form", with the full suite left to the gate.
- **`af_cli_envelope`** (spec 05) delivers:
  - `cmd/af` and `internal/toolio`, with commands registered on a shell that owns the envelope, the exit-code table, coded warnings, `--dir`, `--timeout` and `--schema` with its golden;
  - `.agent-fox/` with its own `.gitignore`.

  This scope registers its commands there.
- **`go.mod`** pulls tree-sitter grammars for Go, Python, JavaScript, TypeScript, Rust, Java, Kotlin, Ruby, C, C++, C#, PHP, Scala, Lua and Bash, indirectly through agentkit-go. That is the hands' outline. The sources of agentkit-go (`../agentkit-go`) are outside the repository and could not be read.

## Requirements

### R1. One view of the tree, and its hash

The file view is shared by the tree hash, language detection and the map:

- In a git work tree it is git's own view of `--dir`: tracked files plus untracked files that are not ignored. It is read NUL-separated so names with spaces or non-ASCII characters survive unquoted. Files deleted from the working tree are dropped.
- Outside a work tree it is a standard-library walk of `--dir` that skips `.git`, `.hg`, `.svn`, `node_modules`, `vendor`, `target`, `dist`, `build`, `__pycache__`, `.venv` and `venv`.
- Both forms always exclude `.git/` and `.agent-fox/`.
- Paths are slash-separated, relative to `--dir`, and sorted.

The tree hash is a SHA-256 over the file view's entries in order. Each entry holds:

- for a regular file: its kind, its executable bit, its path and the SHA-256 of its content;
- for a symlink: its target, not followed;
- for any other file type: its type;
- for an unreadable file: a fixed `unreadable` marker. An unreadable file does not fail the command.

The same content gives the same hash on any machine, whatever commit HEAD points at. Before hashing, `af` makes sure `.agent-fox/` exists with its `.gitignore`.

### R2. `af lang detect`

`af lang detect [--dir .] [--write]` reports:

- **`languages`**: each language with its file count from the file view, ordered by count. Each also carries the manifest that marks it, when there is one. Languages are classified by extension, using the languages the hands' outline supports (above).
- **`gate`**: the effective check table, with `source` set to `file` or `detected`. Each check has a `name`, an `argv` with no shell, a `kind` of `lint`, `test` or `check` (combined), the `source` it was detected from (for example `Makefile:check`), and `available`, which says whether `argv[0]` resolves on `PATH`.
- **`suite`**: the command forms the guard treats as "the full suite". This is the gate's checks, plus `make check` and `make test` when those targets exist, plus each detected language's full-suite form.
- **`programs`**: the gate programs. These are the checks' `argv[0]` plus each detected language's toolchain programs. They are what the guard and the agentkit brain may allow.

The effective table is `<dir>/.agent-fox/gate.json` when that file exists and parses (`{"version": 1, "checks": [...]}`). Otherwise it is detected in this order, from root-level files only:

1. A Makefile (`GNUmakefile`, `makefile` or `Makefile`) with a `check` target gives `[make check]`.
2. Otherwise, a Makefile with a `test` target gives `make lint` (when that target exists) and then `make test`.
3. Otherwise, each language manifest present adds its checks, in this order:

| Language | Marker | Checks |
|---|---|---|
| Go | `go.mod` | `go vet ./...` (lint), `go test ./...` (test) |
| Python | `pyproject.toml`, `setup.py`, `setup.cfg`, `pytest.ini`, `tox.ini` | `pytest` when there is any pytest signal (`pytest.ini`, a root `conftest.py`, `[tool.pytest`, or `pytest` named in pyproject, setup.cfg or requirements files), else `python -m unittest discover`; prefixed `uv run` with `uv.lock`, `poetry run` with `poetry.lock`; `ruff check .` (lint) when ruff is configured |
| JavaScript/TypeScript | `package.json` | `<pm> test` when `scripts.test` exists and is not npm's placeholder, `<pm> run lint` when `scripts.lint` exists; `<pm>` is `pnpm`, `yarn` or `bun` by lockfile, else `npm` |
| Rust | `Cargo.toml` | `cargo test` |
| Java | `pom.xml`, `build.gradle(.kts)` | `./mvnw test` or `mvn -q test`; `./gradlew test` or `gradle test` |

Other languages are reported with no checks.

`--write` writes the freshly detected table to `.agent-fox/gate.json`. A `gate.json` that does not parse, or names an unknown version, makes the command fail with category `config`, and the message names the file. A check whose program is not on `PATH` produces the coded warning `check_unavailable`. Whether a program is available does not change which checks are selected, because the table must be the same on every machine for the cache key to mean anything.

### R3. `af gate run`

`af gate run [--dir .] [--label L] [--hermetic] [--no-cache]` runs the effective table's available checks in order, in `--dir`:

- The checks run under `gitx.ReducedEnvRunner`. With `--hermetic` they run under `gitx.HermeticRunner` instead, with a temporary `HOME` that is removed afterwards.
- Each check's combined output is written in full to `.agent-fox/gate/<id>/NN-<slug>.log`.
- Each check records:
  - `status`, which is one of:
    - `pass` (exit 0);
    - `fail` (non-zero exit);
    - `error` (the program could not start);
    - `timeout`;
  - `exit_code`;
  - `duration_ms`;
  - `log`, the path relative to `--dir`, so a brief can cite it;
  - `tail`, the last 40 lines capped at 4 KiB.
- The result holds:
  - `id`;
  - `tree` (R1);
  - `table_source`;
  - `hermetic`;
  - `label`;
  - `cached`;
  - `passed`, which is true only when every check passed;
  - `checks`;
  - `skipped`, with a reason for each.

Statuses:

- A red gate is a measured fact: status `done`, exit 0, `passed: false`.
- A table with no available check gives `done`, with no checks and the warning `no_checks`.
- If the tree hash taken after the run differs from the one taken before, the result is kept under the earlier hash and the warning `tree_changed_during_gate` is added.
- If the command's own timeout or a signal stops the run, nothing is cached and the shell reports the failure.

### R4. The gate cache and labels

The cache key is the SHA-256 of three things: the tree hash, the canonical JSON of the check table, and the runner mode (reduced or hermetic). The result `id` is the key's first 12 hex characters.

The cache lives at `<dir>/.agent-fox/gate/<id>/result.json`. It is shared by every run on that checkout, and inside a box it sits in the box's own `.agent-fox/`.

- A hit returns the stored result with `cached: true` and runs nothing.
- `--no-cache` runs the checks and replaces the stored result.
- Only a completed result is stored. It is written atomically (temporary file, then rename) after its logs.

`--label` must match `^[a-z][a-z0-9_-]{0,31}$` and must not itself look like an id. It is recorded, on a hit as well as a miss, in `.agent-fox/gate/labels.json`, written atomically. The latest result for a label replaces the earlier one. An invalid label is `usage`.

### R5. `af gate compare`

`af gate compare --before <label|id> --after <label|id> [--dir .]` resolves each argument as a label first and then as an id. It compares the two stored results check by check, matching by name.

| Before | After | Per-check verdict |
|---|---|---|
| pass | pass | `pass` |
| not pass | pass | `pass_was_already_failing` |
| pass | not pass | `regressed` |
| not pass | not pass | `still_failing` |

`fail`, `error` and `timeout` all count as "not pass".

The overall `verdict` is the first of these that applies:

1. `regressed`, when any check regressed;
2. `still_failing`;
3. `pass_was_already_failing`;
4. `pass`.

When either result has no checks, the verdict is `unverified`. `landable` is true for `pass` and `pass_was_already_failing` only.

The result carries both results' `id`, `label` and `passed`, the overall verdict, `landable`, and the per-check rows. The status is `done` for every verdict. An unknown label or id, or two results whose check names differ, is `usage`, and the message says which.

### R6. `af revert-check`

`af revert-check --since <ref> [--dir .]` answers one question: do the tests of the change fail without the rest of the change?

`--dir` must be the work tree's top level. `<ref>` must resolve to a commit. If either is wrong the status is `usage`. The steps are:

1. **List the changed paths.** These are the paths that differ from `<ref>`, untracked files included, read NUL-separated. Each path is classified as a test or non-test path using the conventions of R2's languages:
   - Go: `_test.go`, `testdata/`;
   - Python: `test_*.py`, `*_test.py`, `conftest.py`;
   - JavaScript/TypeScript: `*.test.*`, `*.spec.*`, `__tests__/`;
   - Java: `src/test/`, `*Test.java`, `*Tests.java`, `*IT.java`;
   - Ruby: `*_spec.rb`, `*_test.rb`;
   - any path with a `test`, `tests` or `spec` directory segment.
2. **Return early when nothing can be measured:**
   - no changed paths: `no_changes`;
   - only test paths: `no_code_changes`;
   - no test paths: `no_test_changes`;
   - no check of kind `test` or `check` in the table: `inconclusive`.
3. **Measure the current tree.** Take the full table's result for the current tree from the cache, or run and cache it (R3, R4). Unless its `test` and `check` checks passed, the outcome is `inconclusive`, with reason `after_not_green`.
4. **Back up, then revert.** Copy every non-test changed path that exists now into `.agent-fox/revert/<id>/`, with a manifest. Then revert the non-test paths without touching the index:
   - a path that exists at `<ref>` gets the ref's content and mode;
   - a path that does not exist at `<ref>` is moved aside;
   - test paths are left as they are.
5. **Run the tests on the reverted tree.** Run only the `test` and `check` checks, through the same gate engine, which caches by the reverted tree's hash.
6. **Restore.** Restore every reverted path from the backup, with a context that is not cancelled, so a signal or timeout still restores. Check that the tree hash equals the hash taken before step 4, and that the index is unchanged. Then remove the backup.

Outcomes:

- `proven` when any of those checks fails on the reverted tree;
- `not_proven` when they all pass;
- `closes` is true only for `proven`;
- every outcome other than `proven` and `no_changes` adds the coded warning `fix_not_proven`.

The result carries:

- `outcome`;
- `reason`;
- `closes`;
- `since`, resolved to a hash;
- `test_files`;
- `reverted_files`;
- the `after` gate id;
- the reverted gate result (`id`, `passed`, `checks`);
- `restored`.

Failures:

- If the restore fails, or the hash does not match, the command fails with category `restore`. The backup is kept and the message names its directory.
- If a backup directory already exists when the command starts, it refuses with category `restore` and names the directory. The leftover backup means an earlier check was killed, and that tree may not be the caller's.

### R7. The guard policy and its decision

`internal/policy` decides one tool call against a policy. The loop's in-process interceptor calls the same function.

**Where the policy comes from.** The first that exists wins:

1. `--policy <path>`;
2. `<dir>/.agent-fox/guard.json`, of the form `{"version": 1, "suite": [[...]], "protected_paths": [...], "allow_programs": [...]}`. A key that is missing takes its default;
3. defaults derived from R2 on `--dir`.

The defaults are:

- `suite`: R2's suite;
- `protected_paths`: `.git/`, `.agent-fox/`, `.specs/`, `.github/workflows/`, `.gitlab-ci.yml`;
- `allow_programs`: empty, meaning no program allowlist.

**Calls to a shell tool** (Claude Code's `Bash`, the hands' `execute`) are tokenized with POSIX quoting and split into segments on `&&`, `||`, `;`, `|`, `&` and newlines. The contents of `$(…)`, backticks, and `sh|bash|zsh -c '…'` are inspected as commands too. Leading `VAR=value` assignments and the wrappers `env`, `time`, `timeout`, `nice`, `nohup`, `command` and `exec` are skipped to find the real program.

The checks run in this order, and the first deny wins:

1. A command that cannot be parsed (for example, unbalanced quotes) is denied.
2. The hands' `guard.Check` is applied as a deny-only first layer, and its reason is passed through.
3. For `git`:
   - only read-only subcommands are allowed: `status`, `log`, `diff`, `show`, `blame`, `ls-files`, `ls-tree`, `rev-parse`, `rev-list`, `grep`, `describe`, `cat-file`, `shortlog`, `merge-base`, `name-rev`, `for-each-ref` and `show-ref`;
   - `branch` and `remote` are allowed only in their listing forms;
   - a global `-c` is denied;
   - the reason says that `af` makes every commit, branch and push.
4. A segment that equals a suite form once every token starting with `-` is removed from both is denied. The reason names the command, says to run `af gate run` instead because it caches the result by tree hash and the landing reuses it, and says that targeted runs are allowed.
5. For `af`, only these are allowed: `lang detect`, `gate run`, `gate compare`, `revert-check`, `map`, `guard check`, `triage brief`, `triage submit`, `fix brief`, `fix submit`, `--version` and `--schema`. Any other `af` command (`land`, `run`, the loops) is denied as belonging to the loop.
6. When `allow_programs` is not empty, a program outside it (with `af` and `git` always permitted) is denied.
7. Writes into a protected path are denied. This covers the target of an output redirection, and any argument that `rm`, `mv`, `cp`, `tee`, `touch`, `mkdir`, `truncate`, `ln`, `chmod`, or `sed -i` writes to, once that argument resolves into a protected path.

**Calls to a write tool** (`Write`, `Edit`, `MultiEdit`, `NotebookEdit`, the hands' `write_file` and `edit_file`) are denied when the path is under a protected path. They are also denied when the path is outside `--dir`, unless it is under the system temporary directory. Relative paths resolve against `--dir`.

**Every other tool is allowed.** The host's own tool allowlist governs which tools exist.

Every decision is one of `allow` or `deny`. It names the `rule` that decided it: `unparseable`, `hands_guard`, `git_mutation`, `suite_command`, `af_command`, `program_not_allowed`, `protected_path` or `outside_dir`. It also names the deciding segment, and gives a reason a model can act on.

### R8. `af guard check` input and output

`af guard check [--dir .] [--policy path] [--host claude]` reads one JSON payload of at most 1 MiB from stdin. Two payload shapes are accepted:

- a Claude Code hook payload (`hook_event_name`, `tool_name`, `tool_input`, `cwd`);
- `af`'s neutral form `{"tool": "...", "input": {...}}`.

A payload whose `hook_event_name` is present and is not `PreToolUse` is allowed with no opinion.

**Without `--host`**, the output is the envelope:

- `result` holds `decision`, `rule`, `reason`, `tool` and `segment`.
- The status is `done` and the exit code is 0 for both allow and deny.
- A payload that is malformed or too large is `usage`.
- A policy file that does not parse is `failed`, category `config`.

**With `--host claude`**, stdout is Claude Code's `PreToolUse` response and the exit code is always 0:

- A deny is `{"hookSpecificOutput": {"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": "<reason>"}}`.
- An allow is `{}`. That is no opinion: the host's own permission settings still apply, and the hook never bypasses them.
- Any internal error (a malformed payload, a broken policy, a panic) is answered as a deny that carries the error. The hook fails closed.

This is the only `af` output that is not an envelope.

### R9. `af map`

`af map [--dir .] [--tokens 4000]` renders the repository map. Tokens are estimated as the byte count divided by 4, rounded up. `--tokens` must be between 200 and 100000, or the status is `usage`.

The format is the one shown below:

- each directory as a `path/` header, with the root as `./`;
- each file indented two spaces;
- each file's declarations after it as `<kind> <name> L<line>`, joined by ` · `.

Order is by path. Declarations come from the hands' outline, behind an `Outliner` interface. A file the outliner cannot handle (an unsupported language, a parse error, more than 1 MiB) is listed by name only.

The first level of this ladder whose output fits the budget is emitted:

1. Every file with every declaration.
2. Test files (R6's classification) without declarations.
3. Each other file's declarations capped at K, starting at 20 and halving down to 1, with `… +N more`.
4. Every file by name only.
5. Directories only, each as `path/ (N files)`.
6. Directories cut off at the budget, followed by `… N more directories`.

When the name-only form (level 4) already exceeds the budget, or the file view holds more than 5000 files, outlining is skipped, with the coded warning `map_outline_skipped`.

The result holds `map` (the text), `tokens`, `estimated_tokens`, `level`, `files_total`, `files_outlined` and `truncated`. The same tree and budget always give byte-identical output.

### R10. On the shell, offline and documented

All six commands are registered on the `af_cli_envelope` shell:

- They use its envelope, exit codes, coded warnings, `--dir` and `--timeout`.
- Their default timeouts are:
  - `lang detect`: 1 minute;
  - `gate run`: 30 minutes;
  - `gate compare`: 30 seconds;
  - `revert-check`: 30 minutes;
  - `guard check`: 10 seconds;
  - `map`: 2 minutes.
- None of them builds a forge client, calls a model or opens a network connection.
- Each declares a result schema that is embedded in the binary and appears in `af --schema`. A test fails if a schema's top-level properties and the JSON fields of the command's Go result type drift apart.
- The `--schema` golden is regenerated once.

Every requirement above is tested offline in `make check`:

- temporary git repositories built the way `internal/gitx`'s `newRepo` builds them, skipped when git is missing;
- gate checks that are small scripts or `go` invocations with a counter file;
- a fake `Outliner`;
- payload fixtures for both payload forms.

The documentation is updated as follows:

- `docs/cli.md`: the commands, flags, results, rules and warning codes.
- `docs/configuration.md`: the `gate.json` and `guard.json` formats and their precedence.
- `docs/model-usage.md`: a note that these commands call no model.
- `docs/architecture.md` and `docs/development.md` (created if absent): the new packages and the `.agent-fox/gate/` and `.agent-fox/revert/` layout.
- `README.md`: the package table.
- `make doc` is run first.

## Design Decisions

1. **A measured fact is `done`.** A red gate, a `regressed` verdict, a deny and `not_proven` all exit 0 with the fact in `result`. `failed` and `usage` mean that the command could not measure. *Why:* ring 1 answers questions, and a caller that reads the exit code alone would otherwise mistake "the tests are red" for "af broke".
2. **Package names follow the architecture.** `internal/project` (detection and test-path classification), `internal/checks` (tree hash, gate run, cache, compare), `internal/conform` (revert check) and `internal/repomap`. The guard engine is `internal/policy`. *Why:* this reuses the architecture's vocabulary and avoids an import collision with the hands' `guard` package.
3. **Command wiring lives in `cmd/af`.** The internal packages return plain Go results and do not import `toolio`. *Why:* the loops of later scopes call those packages in-process, without the shell.
4. **The file view is git's view, read NUL-separated, and not the hands' `Walk`.** *Why:* it is verifiable in this repository. It is also the view the tree hash must use, so the map, the languages and the cache agree on what the repository is.
5. **The tree hash covers content only, independent of HEAD.** *Why:* the brain may commit inside the box and the host does not, and the same content must still be a cache hit on both sides. Hashing all files costs little next to running a suite.
6. **The cache lives in `.agent-fox/gate/`, shared across runs, not in a run directory.** The key covers the tree, the table and the runner mode. *Why:* a brain calling `af gate run` inside a box has no run id, and the landing gate must find its result.
7. **Labels are free-form, and the latest result wins.** *Why:* `baseline` and `after` are conventions of the loop, not of the gate, and the loop re-runs `after` per attempt.
8. **Gate table precedence is `gate.json`, then the Makefile, then language manifests, root only.** *Why:* the loop must be able to pin the table it measured on the host. The Makefile is the repository's own declaration (this repository's is `check: lint test`). A deterministic order keeps the cache key stable.
9. **Checks run under `ReducedEnvRunner`, and `--hermetic` uses `HermeticRunner`.** *Why:* gitx already argues that a suite is repository code that must not see credentials, and the architecture's landing gate is the hermetic one.
10. **An unavailable program is skipped with a warning, and a check that cannot start counts as failing.** *Why:* the table stays machine-independent, and a check that broke at run time must not read as green.
11. **Verdicts are computed per check and aggregated `regressed` > `still_failing` > `pass_was_already_failing` > `pass`.** *Why:* one check fixed and another broken is a regression. The whole-gate table hides that, but reduces to it when there is a single check.
12. **Results with different check names cannot be compared and give `usage`.** *Why:* a verdict across different tables is meaningless, and naming the mismatch is the useful answer.
13. **The revert check runs in place, with a backup, never touching the index, and with a restore that cannot be cancelled.** *Why:* a separate worktree lacks ignored dependencies such as `node_modules` and virtual environments, and would report false `proven` verdicts.
14. **The revert check runs only `test` and `check` checks on the reverted tree, and requires them to be green on the current tree first.** *Why:* a lint failure caused by the revert proves nothing, and a red tree with the fix in place makes a failure without it meaningless.
15. **Test paths are classified by a fixed convention table.** Rust and other inline unit tests revert together with their source, so the outcome is `no_test_changes` or `not_proven`. *Why:* the alternative is per-language parsing, which belongs to the later language profiles.
16. **`af revert-check` requires `--dir` to be the top level of the work tree.** *Why:* `git diff` reports paths from the top level and `ls-files` reports them from the working directory, and mixing the two silently mis-reverts files.
17. **A leftover revert backup makes the next revert check refuse; it does not restore automatically.** *Why:* the files may have been edited since the crash, and overwriting a person's edits is worse than stopping.
18. **The suite form is matched by equality after every `-` token is stripped.** *Why:* it denies `go test -count=1 ./...` and allows `go test ./pkg -run X` without a per-tool flag grammar. Where it is wrong it lets an extra suite run through, which costs less than denying a targeted run the skill asks for.
19. **Git is allowlisted by read-only subcommand, and `af` commands are allowlisted for brains.** *Why:* an allowlist fails closed on subcommands nobody listed, and `af … land` from a brain under `--sandbox none` would push with the person's credentials.
20. **Unknown tools are allowed, and `allow_programs` is off by default.** *Why:* the host's tool allowlist already governs tools. A default program allowlist would deny the `ls` and `mkdir` that harnesses use legitimately, and the fix loop can turn it on in its `guard.json`.
21. **The hands' `guard.Check` is a deny-only first layer, behind one adapter, and the `af` rules are implemented in `af`.** *Why:* the architecture asks for both. The hands' semantics could not be read, so the rules that this scope's tests pin must not depend on them.
22. **`af` tokenizes shell commands with its own small standard-library tokenizer.** *Why:* no verified tokenizer is reachable in this repository, and the steering prefers the standard library.
23. **With `--host claude`, allow is `{}`, deny is `permissionDecision: "deny"`, the exit code is always 0, and errors fail closed.** *Why:* an explicit `allow` would bypass Claude Code's own permission system, and exit 2 means `usage` in `af`'s exit-code table.
24. **The envelope is the default output, and the host format is opt-in.** *Why:* every other `af` command prints an envelope, and the settings file `af_boxes_claude` writes can name `--host claude` explicitly.
25. **The policy comes from `--policy`, then `.agent-fox/guard.json`, then defaults derived from detection, and a missing key takes its default.** *Why:* the loop writes the file and syncs it into the box, and a person with no loop still gets the suite rule.
26. **`af map` is a command, its flag is `--tokens` (not `--budget`), and the default is 4000.** *Why:* the architecture lists `af map`, and `--budget` already means USD on `af triage` and `af fix`.
27. **The map uses a deterministic reduction ladder in path order.** *Why:* a brief's run-constant prefix must be byte-identical across attempts, and ranking would add judgment that is not needed yet.
28. **Outlines come from the hands through an `Outliner` interface.** If the hands export no Go outline API, the adapter calls the `file_outline` tool through the hands' toolset. *Why:* this reuses the hands' tree-sitter outline instead of duplicating it, and keeps the ladder testable with a fake.
29. **`toolio` is extended only if it lacks two things:** raw access to stdin for a command that takes no positional input, and a way for one command to supply its own stdout document. *Why:* `af guard check` needs both, and resolved positional input may truncate.
30. **The envelope's schema version is unchanged, and only the `--schema` golden is regenerated.** *Why:* new commands add result schemas, but the envelope itself does not change.
31. **There is no locking between concurrent gate runs; writes are atomic and the last writer wins.** *Why:* a duplicate run wastes time but never corrupts the cache, and a lock needs a stale-lock policy for no real caller.

## Dependencies

| Spec | Relationship | Reason |
|---|---|---|
| `05_af_cli_envelope` (`af_cli_envelope`) | depends on; may extend | The commands are registered on its `toolio` shell and use its envelope, exit codes, coded warnings, `Run.Dir` (`--dir`), `--timeout`, `--schema` and its golden, and its `.agent-fox/` directory with that directory's own `.gitignore`. If they are absent, this scope adds raw stdin access and a command-supplied stdout document for `af guard check --host claude`. If the `.gitignore` setup is reachable only through the run-directory constructor, this scope factors it out. |

## Verified External API

| Symbol | Package | Signature assumed | Status |
|---|---|---|---|
| `tools.ReducedEnv` | `github.com/agent-fox-dev/agentkit-go/tools` | `ReducedEnv(nil) []string`: the process environment without credential variables | used by `internal/gitx/exec.go` and `hermetic.go`; the parameter type was not read |
| `guard.Check` | `github.com/agent-fox-dev/agentkit-go/guard` | takes one shell command line and returns a refusal (reason) or none | **unverified**: source in `../agentkit-go`, outside the repository; bound in one adapter file |
| outline (package path unknown) | agentkit-go | returns a file's declarations as kind, name and start line; fallback is the `file_outline` tool (`path`, `include_private`) through `tools.For(workspace)` | **unverified**; bound in one adapter behind `repomap.Outliner` |
| Claude Code `PreToolUse` hook | Claude Code CLI (external protocol) | stdin JSON: `session_id`, `transcript_path`, `cwd`, `hook_event_name`, `tool_name`, `tool_input` (`Bash`: `command`; `Write`/`Edit`/`MultiEdit`: `file_path`; `NotebookEdit`: `notebook_path`). Stdout on exit 0: `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"\|"deny"\|"ask","permissionDecisionReason":"…"}}`. `allow` bypasses permission prompts. | **unverified**: an external CLI contract, recorded from its documentation |
| hands' tool names | agentkit-go `tools` | shell tool `execute` (`command`); writers `write_file` and `edit_file` (`path`) | **unverified** |
