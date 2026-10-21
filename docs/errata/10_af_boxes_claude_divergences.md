# Erratum 10: where the boxes and the claude brain do something other than the spec says

**Spec:** `10_af_boxes_claude`. Each divergence below was decided during the survey or on
the way, and is recorded here because the spec stays as it was written. The code and the
test of each are named so that a reader can check what is delivered. Where a commit
declares the divergence, its `Deviation:` trailer key is named.

The real `landlock` and `podman` boxes and the real Claude Code could not be run where this
was written (macOS, no engine, no `claude`). Their paths were checked by reading the code,
by stand-in providers and by the fake `claude`; the opt-in tests of 10-REQ-10.3 are the
check on a machine that has them, and they have not been run.

## 1. There is no `sandbox.Open(name)`: a provider is made by `Entry.Build` (tasks 1, 3, 4)

**Spec:** the PRD and the external APIs name `sandbox.Open(name string) (Provider, error)`
for `"landlock"` and `"podman"`, and `Entry.New`.

**Code:** agentkit-go has no such function (spec 07's erratum already said so). Each
provider has its own constructor, and two of them can fail. `box.Entry` has
`Build func(ProviderConfig) (sandbox.Provider, error)` beside `New`
(`internal/box/registry.go:17`), `ProviderConfig.ExtraRO` carries the directories of `af`
and `claude` that Landlock must let a process read and run (a landlock process runs from the
host's file system), and the adapter file makes the real providers
(`internal/box/adapter.go`). The ABI is read by the function variable `box.LandlockABI`
(`internal/box/landlock.go:23`). `brain.BoxRequest` fills `Request.ExtraRO` for a process in
a landlock box: the directory of the `af` it runs (`Options.AFPath`, else the running
executable) and of the `claude` on `PATH` for the claude brain, and where a program is a link
the directory of what it leads to (`programDirs`, `internal/brain/boxspec.go`); the three
commands pass `Options.AFPath` (`TestTheLandlockBoxOfAProcessBrainIsOpenedWithTheDirectoriesOfAfAndClaude`).
Not covered: a runtime that `claude` needs outside the system directories, such as `node` in a
version manager's directory. A failure that the provider itself raises when it creates the box
(`landlock.ErrNoLandlock`) is the same refusal as a failed check (`explainLandlock`).

**Test:** `TestLandlockOnAnotherOSIsRefusedBeforeASandbox_TS_10_15`,
`TestAnABIBelow4OrNoLandlockIsRefused_TS_10_17`
(`internal/box/landlock_test.go:73`, `:121`), `TestAnEngineThatCannotBeReachedIsASandboxFailure_TS_10_19`
and `TestAnImageThatCannotBePulledIsARetryableSandboxFailure_TS_10_20`
(`internal/box/podman_test.go:63`, `:111`).

## 2. `Entry` keeps `Unconfined`; `Isolates()` and `InPlace` are added (task 1)

**Spec:** each provider "declares whether it isolates", and whether it works in place.

**Code:** `Unconfined` stays, since spec 07's `box_unconfined` hangs on it; `Isolates()` is
its negation and `InPlace` is a field (`internal/box/registry.go:17`, TS-10-1).

## 3. The network is a string, and `host` is not a `sandbox.Spec` mode (tasks 2, 5, 10)

**Spec:** `Spec.Network` is `none` or `open`, and `host` is recorded under `local` and `none`.

**Code:** `sandbox.Spec` has no "host", so the network is a string, `none`, `open` or `host`,
on `box.Request` and `Session.Network()` (`internal/box/open.go`), mapped to the spec's modes
by `networkFor` in the adapter. The `none` session's `Spec()` carries the environment and the
work directory and an unrestricted network (`internal/box/none.go`). The `box` of the `fix`
result already existed from spec 09 (with the golden), so task 10 only fills `image` and
`network` in the new vocabulary (`fix.NewBoxInfo`, `internal/fix/result.go`).

**Test:** TS-10-11, TS-10-14 (`cmd/af/fix_boxes_test.go`, `cmd/af/triage_boxes_test.go`).

## 4. Two helpers of `gitx` were not new (tasks 5, 10)

`gitx.RevParse` exists from spec 09, so only `IsAncestor` is added (`internal/gitx/git.go`).
`gitx.EnvRunner` exists and appends to the environment of the process, so the runner with an
explicit environment is `gitx.ExplicitEnvRunner` (`internal/gitx/exec.go`).

## 5. `af fix run` and `af triage run` are the driver of a box (task 6, 9; `Deviation: fix_run_driver`)

**Spec:** both are "the process a box executes" and take the brief and `prefix_bytes` from
`run.json` when their own run state is absent.

**Code:** spec 09 built `af fix run` as the round of the host, with a box of its own and the
apply step. With no `fix.json` and a `run.json` that has `prefix_bytes`, both commands are the
driver of the box instead: no box, no apply, and the brief from `brief.md` and the prefix.
The driver must submit under the nonce the outer `Execute` placed, so `brain.Request` has a
`Nonce` that `Execute` adopts, and the run goes through `Execute` over `box.Here`.
`fixRunResult` has `submitted` (`cmd/af/fix_run.go:34`) and the `fix_run` schema and the
golden were regenerated. The loop places `fix.json` in the box for every brain but the driver
of a box (`WriteBoxFiles`, `internal/fix/round.go:79`): a brain that submits with `af fix
submit` needs it there, and for the driver its presence would make `af fix run` in the box the
round of the host.

**Test:** TS-10-58 (`cmd/af/placed_run_test.go`), and `TestTheDriverInABoxIsNotGivenTheStateOfTheRunAndClaudeIsNotOfferedTheTool`.

## 6. Under `none` the person's `gate.json` and `guard.json` are not overwritten (task 10)

The round writes `.agent-fox/gate.json` and `guard.json` into the box. Under `none` the box
is the checkout, so they are written only if absent (`WriteBoxFiles`, `internal/fix/round.go`).

## 7. `Spec.Env` shows on the engine's command line (task 2)

agentkit's podman provider warns that `Spec.Env` is visible on the command line of the engine
on the host. The spec puts `ANTHROPIC_API_KEY` there for a process brain, and this follows the
spec. It is documented in `docs/configuration.md` (The box).

## 8. The foreign-commit check is in `round.go` and `foreign.go`, not in `apply.go` (task 10)

**Spec:** task 10 touches `internal/fix/apply.go` for the skip of export and apply, and
`internal/triage/loop.go` for the triage loop.

**Code:** `Apply` has no session; the `Export` and `Apply` calls are in `RunRound`, so the
in-place branch, the recording and the check are in `internal/fix/round.go` and the new
`internal/fix/foreign.go`. A refused foreign state is a stop of the loop (`Finish` passes it
through without parking). The triage loop is in `cmd/af/triage.go`; `internal/triage/loop.go`
gains only `BriefTools`, the read-only tools the request for the box is computed from.

**Test:** TS-10-34 to TS-10-37 (`internal/fix/foreign_test.go`, `cmd/af/fix_boxes_test.go`).

## 9. A round that comes to no report leaves the brain's edits under `none` (task 10; `Deviation: none_round_leaves_edits`)

There is no box to throw away, so the next round starts from what the brain did, and a run
that ends without a change that lands parks it. Under `local` a round that fails applies
nothing.

## 10. TS-10-31 holds for decisions, not for the file under `none` (task 6)

"`submission.json` equals `local`" holds for every decision, but under `none` a rejected
attempt restores the bytes of the earlier acceptance (10-REQ-5.7), where under `local` the
host file is only ever written from the box. The tests state both.

## 11. Under `none`, `Execute` writes into the host run directory (task 6)

`brief.md`, `run.json` and `hook.json` are the files of the run directory itself, so `brief.md`
there is the last round's document. Its prefix is unchanged and the prefix hash of `fix.json`
holds.

## 12. The legacy tests are pinned by their helpers (task 11; `Deviation: legacy_tests_pinned_by_helpers`)

The tasks asked for `--sandbox local` on every invocation. The three helpers of the test
environments (`triageEnv.loop`, `fixEnv.fix`, `fixEnv.fixRun`) pass it, the direct runs say it,
and `TestEveryTestThatRunsTriageOrFixSaysWhichBox_TS_10_62` finds an invocation that does
not. Until task 11 `cmd/af` pinned `Defaults{Sandbox: "local"}` in the three commands, so that
every task's gate stayed green; the pins are gone. The spec 09 test of the documents expects
the new default (`internal/fix/docs_test.go`).

## 13. The fake claude and the gates are packages of their own (task 11; `Deviation: test_support_packages`)

`internal/fakeclaude` and `internal/optin` are outside the `touches`; `internal/brain` and
`cmd/af` share one fake `claude`, whose script format `docs/development.md` describes.

## 14. The smoke tests commit with a Go brain, and delete with `make` (task 12)

**Spec:** TS-10-67 and TS-10-68 use a scripted brain that commits.

**Code:** the tool gate of every in-process brain denies `git commit`, which is the point of
the gate, so the scripted brain cannot make the foreign commit or the commit in the box. The
smoke tests use a brain written in Go that commits through `Session.Exec`
(`shellRound`, `cmd/af/fix_boxes_test.go`), and a scripted run that deletes a file and sets
the executable bit through `make`, the one program of the fix policy that runs anything
(`fixScriptedFourChangesUnder`, `cmd/af/fix_optin_test.go`).

## 15. TS-10-69 injects `box.GOOS` and a podman CLI that is down (task 12)

The operating system is one exported variable, `box.GOOS`, read by the provider checks and by
the default provider of `brain.Flags`. The engine is a `podman` script first on `PATH`, so that
the test does not depend on the engine of the machine; its error does not mention a machine,
so the hint is `af`'s.

## 16. `run_command` is a shell tool, and no list of tools is every tool (task 2)

**Spec:** 10-REQ-2.3: the network is `none` when `Brief.Tools` holds no `execute` tool.

**Code:** `shellTools` is `execute` and `run_command`, the two tools of the hands that run a command
(`internal/brain/boxspec.go`), and a nil list of tools is every tool, a shell among them, which
is how the fix loop calls `BoxRequest`: a brain that can run a command is given the network
whichever tool it does it with. The triage loop passes the read-only tools (`triage.BriefTools`).

## 17. The hint of a default provider replaces the hint of the failure (task 4)

When the provider was chosen by default, `fix_hint` is `{flag: --sandbox, env: AF_SANDBOX}`
(10-REQ-1.5), also for a missing image, whose own hint, `AF_SANDBOX_IMAGE`, is then told by the
message alone (`defaultHint`, `internal/box/open.go`).

## 18. A cost source of `none` in the envelope of the driver is kept (task 9)

**Spec:** 10-REQ-9.7: the cost of the driver in a box is `measured`.

**Code:** an envelope whose model has no price says `cost_source` `none`, and the brain keeps
it, as the driver on the host does (`parseEnvelope`, `internal/brain/agentkit_process.go`;
`TestAnEnvelopeOfAnUnpricedModelHasNoCostSource`).

## 19. What is not verified of Claude Code (tasks 8, 9, 11)

- The command line was run against the real `claude` 2.1.306 with an invalid key, so that no
  model was called: every flag is accepted, and the output has the fields the brain reads
  (`type`, `subtype`, `is_error`, `num_turns`, `total_cost_usd`, `usage`, `session_id`,
  `result`). Claude Code prints an API failure as the subtype `success` with `is_error` true,
  an exit code of 1 and nothing on stderr, so the error of a failed run carries the `result`
  text when stderr is empty (`claudeError`).
- A successful run of the real `claude` through the box has not been seen. The opt-in test of
  the contract row with the real `claude` was run once, by a reviewer, with the key that this
  machine's environment holds: it failed with exit code 1 and no message, before `claudeError`
  made the cause visible, and was not run again, so as not to spend the key on a guess. The
  test now logs the output and the errors of the process when it fails.
- Whether Claude Code exits non-zero on `error_max_turns` is not known. If it does, a run that
  ended on its turns without a submission is a `brain` failure with the stderr tail, and not
  a `budget` failure with the `--max-turns` hint, since a run whose process failed is judged by
  the exit first (`noSubmission`, `internal/brain/decide.go`).
- A runtime that `claude` needs outside the system directories (see 1) is not opened to a
  landlock box.

## 20. The settings of the host reach a process with their values (task 7)

10-REQ-7.5 lists `LANG`, `LC_ALL`, `TZ`, the proxies and the certificates "that are set". The
shell of a box has not the environment of the host (only `none` passes it on), so the process
runner gives their host values inline, single-quoted, and refers to the box's own `HOME` and
`TMPDIR` (`environment`, `internal/brain/process.go`). A password in a proxy URL is therefore in
the text of the command; the credentials of `Spec.Env` still are never.

## 21. The tests run the paths in process, and some test the pieces (tasks 6, 10, 11, 12)

- TS-10-61, 66 and 69 run `af triage` and `af fix` through `newApp().Main`, not through a built
  binary; the `af` that the claude process runs and the hook is the test binary linked as `af`
  (`cmd/af/testmain_test.go`), which is the same code.
- TS-10-34 and TS-10-35 test `RecordCheckout` and `Checkout.Check` directly; the loop's use of
  them is tested at the commands (TS-10-33, 36, 37, 67, and a stopped run).
- TS-10-58 is two tests: `Execute` places `prefix_bytes` (`internal/brain/inplace_test.go`) and
  the commands take the brief from `run.json` (`cmd/af/placed_run_test.go`).
- "Export is never called" is read from `Session.ExportCalls`, which the loop's boxes are asked
  after the run, and from the absence of a `box_export` entry.
- 10-REQ-10.3: the invariants of a box under each provider are `boxInvariants`
  (`internal/brain/optin_test.go`), a restatement of spec 07's, run under `local` by `make check`;
  the real-claude test is a scenario of its own (a refused commit, a submission through the
  real `af`, the cost as reported) and not a row of the contract table, which has a claude row
  of its own with a fake `claude`.
- `cmd/af/sandbox_scan_test.go` reads the sources: it does not find a call whose command word is
  held in a variable, or a list whose second element is one. Its limits are in
  `docs/development.md`.

## 22. What `none` leaves of the files it places (task 10)

`gate.json` and `guard.json` that af places on the checkout for the rounds, where the person has
none, are taken off when the loop ends if they are as af wrote them (`Placed.Remove`,
`internal/fix/round.go`); the person's own are never touched. `fix.json` of the run directory stays,
as the run's.
