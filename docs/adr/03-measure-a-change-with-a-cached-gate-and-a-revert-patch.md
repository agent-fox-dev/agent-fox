# ADR 03: Measure a change with a cached gate and a revert patch

- **Status:** Accepted
- **Date:** 2026-10-25 (records decisions taken while specs 06 and 09 were implemented)

## Context

`af fix` decides whether a change may land by measuring it: the gate runs the
repository's checks before and after the change, and the revert check asks
whether the change's tests fail without the rest of it
([architecture](../architecture.md#fact-commands-internals)). The spec of these
fact commands keyed the cache on the whole gate table, numbered the logs by the
table, read the content to revert with `git show`, and backed every changed
file up under a directory of its own. It also gave a person and a brain
commands that no loop used (`af gate compare`, `af map`, `gate run --label`,
`--hermetic`, `--no-cache`). What the code does instead is below.

## Decision

**The gate.**

- The cache key holds the tree hash, the `name`, `argv` and `kind` of each
  check, and the runner mode `reduced`. It leaves out `source` and
  `available`, which say where a check came from and what this machine can
  run, not what is measured. The mode stays in the key, though the reduced
  runner is the only one, so that the ids of stored results do not change.
- A stored result is not a hit when it skipped a check that can run here, or
  when its `result.json` is not a result of the key.
- A log is named `NN-<slug>.log` with `NN` the position among the checks that
  ran, so the logs of a table whose first check is unavailable start at `01`.
  Logs are written when the run is complete, just before the result, so a run
  that is stopped writes none.
- Two results are compared in process, `checks.Compare` over two gate ids. A
  brief renders its repository map itself (`repomap.BuildDir`).

**The revert check.**

- The change of the non-test paths is taken out as a binary patch made on a
  private index read from the base, written exclusively to
  `.agent-fox/revert.patch`, removed with `git apply --reverse` and put back
  with `git apply`. Git handles modes, links, deletions, new directories and a
  directory that became a file, and changes every path or none.
- The listing of changed paths runs on a private copy of the index that keeps
  the modification time of the real one, because `git diff` rewrites stat data
  even with `--no-optional-locks` and git judges racily clean entries by that
  time.
- When the checks found on the reverted tree lack the `test` and `check`
  checks that were measured (the change added the Makefile), the outcome is
  `inconclusive` with the reason `table_changed_by_revert`.
- A path that a check changed while the change was out makes `git apply`
  refuse: the tree stays without the change, nothing the check did is
  overwritten, and the command fails with `restore`, naming the path and the
  patch. A leftover patch is the claim on the checkout: the next check refuses
  until a person puts it back with `git apply .agent-fox/revert.patch`. A tree
  that `af fix` could not put back after its revert check is `failed`/`restore`
  as well, in process and in a box.
- `af revert-check` asks the shell for ten minutes of grace after a signal
  (`Command.Grace`), since the shell abandons a command after ten seconds.

## Consequences

- The revert check is a few calls of git instead of a copy of every changed
  file, and git's own rules decide what "put back" means.
- Modification times of the changed paths are not put back after a revert, so
  a build sees them as changed.
- Measuring has nothing for a person to drive step by step; the loops call the
  packages in process, and the fact commands are what a box runs.
