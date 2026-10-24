# Erratum 06: where the ring-1 commands do something other than the spec says

**Spec:** `06_af_ring1_facts`. Each divergence below was decided during the
survey or found in review, and is recorded here because the spec stays as it
was written. The code and the test of each are named so that a reader can
check what is delivered.

## 1. The hands' guard is `guard.Restricted`, and it looks at the whole line first

**Spec:** 06-REQ-7.3 binds the hands' `guard.Check` as a deny-only first layer;
06-REQ-7.1 evaluates rules 1 to 7 "per segment".

**Code:** `internal/policy/handsguard.go:33` binds `guard.Restricted`, not
`guard.Check`, whose real signature is `Check(argv, Options)` and not the
PRD's. The programs the line runs are given to it as its allowlist, so it adds
only what the policy lacks: an assignment that changes which program runs
(`PATH`, `LD_PRELOAD`, `BASH_ENV`, …) and a line it cannot read or that runs no
program. Program decisions stay rule 6. It is asked about the whole line, once,
before the rules of the segments (`internal/policy/policy.go:188`), because it
reads a line and not a segment. A line it refuses is denied with `hands_guard`
even when a later rule would have refused it too, and a line it cannot read
(an assignment on its own, a function definition, `((i++))`) is denied.

**Test:** `TestHandsGuardIsADenyOnlyFirstLayer_TS_06_49`
(`internal/policy/policy_test.go`) pins the reason passed through, an allow
from the hands that does not override a later rule, and the order.

**Why:** The segment-by-segment form would have the hands' guard read pieces
of a line that it cannot parse alone (a heredoc body, a `$(…)`), and refuse
what it would accept whole.

## 2. Limits and budgets the spec does not name

**Spec:** silent.

**Code:** a command over 64 KiB is denied as `unparseable`
(`internal/policy/policy.go:74`): the hands' guard takes time that grows with
the square of the length of the line, and a hook that does not answer in time
answers nothing. With `--host claude` the hook decides within 80% of
`--timeout` (`cmd/af/guard.go:245`), because the shell throws away what a
command returns after its deadline and reports a failure (exit 1) that Claude
Code does not act on. A brace expansion that makes more than 4096 words or 1
MiB is `unparseable` (`internal/policy/expand.go`). A `guard.json` with a key
the loader does not know is `failed`/`config` (`internal/policy/load.go:71`):
a typo that dropped a protected path would be protection lost without a word.

**Test:** `TestOversizedCommandIsDenied`, `TestGuardCheckHostFailsClosed_TS_06_62`,
`TestParseRefusesWhatItCannotBound`, and the refusals of `load_test.go`
(TS-06-55 to 57).

## 3. A payload whose `hook_event_name` is blank is judged

**Spec:** 06-REQ-8.4 allows with no opinion a payload whose `hook_event_name` is
present and is not `PreToolUse`.

**Code:** an empty or blank name is taken for a missing one, and the call is
judged (`readPayload` in `cmd/af/guard.go`). A hook that fails open on a blank
field is no guard.

**Test:** `TestGuardCheckPayloadForms_TS_06_58` (`cmd/af/guard_test.go`).

## 4. The guard reads more than the spec lists, and denies more shapes

**Spec:** 06-REQ-7.1 names the wrappers `env`, `time`, `timeout`, `nice`,
`nohup`, `command` and `exec`, the contents of `$(…)`, backticks and
`sh|bash|zsh -c`; 06-REQ-7.5 compares a command with a suite form once the
tokens that start with `-` are removed; 06-REQ-7.8 lists the commands that
write.

**Code:** a review against a real bash showed commands the policy allowed that
the shell ran as `git push` or the suite: `$'git'`, `{git,push}`,
`git${IFS}push`, `env -S 'git push'`, `bash --rcfile … -c`, `trap`,
`function`, `(( 1 << 2 ))` hiding a newline from the parser, backticks with
escapes, `echo git push | sh`, git configuration in the environment, `Git` on a
case-insensitive file system, and writes into `.specs` through a link, a glob,
`~`, `$PWD`, `dd of=`, `find -delete`, `git diff --output=` and the like. The
tokenizer and the rules read all of them (`internal/policy/tokenize.go`,
`expand.go`, `policy.go`), and the wrappers also include `builtin` and `xargs`.
A suite form is compared once the values of the options that take one
(`-timeout 5m`, `-j 4`) and `NAME=value` words are removed as well, because
`go test -timeout 5m ./...` is the suite. A shell that reads its commands from
its input is denied as `unparseable`, because what it runs cannot be read
first. The new reading is documented in [`docs/cli.md`](../cli.md#guard-check).

**Test:** `internal/policy/bypass_test.go`, including
`TestTheLineIsReadByTheShellToo`, which runs each line of its table in a real
bash with stubs for `git`, `make` and `go` and requires that the policy deny
every line that makes the stubs run a push or the suite.

## 5. Gate logs and the cache key

**Spec:** 06-REQ-3.1 names the logs `NN-<slug>.log` with NN "the two-digit
1-based check index"; 06-REQ-4.1 keys the cache on "the canonical JSON of the
gate table".

**Code:** NN is the position among the checks that ran
(`internal/checks/gate.go:221`), so the logs of a table whose first check is
unavailable start at `01`. The key holds `name`, `argv` and `kind` of each
check, and not `source` or `available`, which say where a check came from and
what this machine can run, and not what is measured. A stored result is not a
hit when it skipped a check that can be run here (`measuresTable`), or when
its `result.json` is not a result of the key. The logs are written when the
run is complete, just before the result, so that a run that is stopped writes
none.

**Test:** `TestKey_TS_06_25` and `TestCacheHit_TS_06_26`
(`internal/checks/gate_test.go`), and
`TestCancellationStopsTheGroupAndStoresNothing_TS_06_24`
(`internal/checks/gate_unix_test.go`).

## 6. `git cat-file` reads the blobs of the revert, and there is one more reason

**Spec:** 06-REQ-6.5 reads the content "via `git show`".

**Code:** a blob is read with `git cat-file blob <object>`, with the object
that `git ls-tree -z` names, because `git show` applies a textconv driver
(`internal/conform/revert.go`). The listing of changes runs on a private copy
of the index through `gitx.EnvRunner`, because `git diff` rewrites the stat
data of the index even with `--no-optional-locks`. The copy keeps the
modification time of the index, because git decides by it whether an entry is
racily clean.

An outcome the spec does not list was added: `inconclusive` with the reason
`table_changed_by_revert`, when the table of checks found again on the reverted
tree does not have the `test` and `check` checks that were measured (a
Makefile the change added is not there to run, and a check that cannot run says
nothing about the tests). `failed`/`restore` also covers a path that something
changed while the checks ran: its content is kept in the backup and not
overwritten. The command asks for ten minutes to restore after a signal
(`Command.Grace`), because the shell abandons a command after ten seconds.

**Test:** `TestARevertThatChangesTheChecksIsInconclusive`,
`TestCopyIndexKeepsTheModificationTime`, and the restore tests of
`internal/conform/revert_test.go`.

## 7. The map line is `name: declarations`

**Spec:** 06-REQ-9.1 says each file's declarations come after it as
`<kind> <name> L<line>` joined by ` · `.

**Code:** the line of a file is `  name: decls`, and a method is named with its
type (`Server.Close`), as the hands' outline gives it
(`internal/repomap/map.go:281`). A name with bytes that are not UTF-8 is written
with U+FFFD, so that the map is the same text after it has been encoded and the
budget holds. `files_outlined` counts the files whose declarations were read,
and the map shows them only at levels 1 to 3.

**Test:** `TestBuildFormat_TS_06_63`, `TestHandsOutliner`, `TestNamesThatAreNotUTF8`.

## 8. `gate compare`, `map` and the flags of `gate run` are gone

06-REQ-3, 06-REQ-4.4, 06-REQ-4.6, 06-REQ-5 and 06-REQ-9 give a brain and a person
`af gate compare`, `af map`, and `af gate run --label`, `--hermetic` and `--no-cache`. No loop
used any of them: `af fix` compares its gate results by id in process, and a brief renders its
map itself. They are removed, with `labels.json` and its lock, the hermetic runner of the gate
and its mode in the cache key (the key keeps the word `reduced`, so that the ids of results
stored before stay the same). The comparison stays, as `checks.Compare` over two gate ids, and
so does the map, as `repomap`. The tool policy no longer lets a brain run the commands that
are gone.

**Test:** `TestCompareRefusals_TS_06_36_and_37` (`internal/checks/compare_test.go`),
`TestKey_TS_06_25` (`internal/checks/gate_test.go`),
`TestTheRing1CommandsAreRegistered_TS_06_69` (`internal/afcmd/ring1_test.go`).
