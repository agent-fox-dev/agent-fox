# Erratum 07: where the box, the submission and the brains do something other than the spec says

**Spec:** `07_af_brains`. Each divergence below was decided during the survey
(the `D-` numbers) or found in review, and is recorded here because the spec
stays as it was written. The code and the test of each are named so that a
reader can check what is delivered.

## 1. The providers of the box are a registry of the box's own (D-2, D-3)

**Spec:** `external_apis` has `sandbox.Open(name) (Provider, error)`, "opens a
provider by name", `unverified`; 07-REQ-1.5 and the first step of task 3 wrap it.

**Code:** agentkit-go has no `Open(name)`. `sandbox` is a module of its own, and
`local.Unisolated()` is the provider (`internal/box/adapter.go:14`). `box` keeps
the registry by name (`internal/box/registry.go`), and every name of the
sandbox module that is not a type is in `adapter.go`. The sandbox's real API is
`Create(ctx, Spec)`, `Sync(ctx, Source)`, `Export(ctx, path, Destination)`,
`Tools(ctx, ToolOptions)` and `Close(ctx)`, which `Session` calls
(`internal/box/session.go`, `internal/box/open.go`). `go.mod` requires the
module and replaces it with the sibling checkout, `../agentkit-go/sandbox`.

**Test:** `TestUnknownProviderIsUsage_TS_07_5` (`internal/box/box_test.go`).

## 2. The `local` provider offers only an unrestricted network (D-4)

**Spec:** 07-REQ-2.1 creates the sandbox with a network of none.

**Code:** `networkFor` (`internal/box/adapter.go:27`) asks for `none` when the
provider's capabilities list it, and otherwise for the first mode the provider
offers; `local` offers only an unrestricted one (it refuses `none`), which is
part of why it raises `box_unconfined`.

**Test:** `TestOpenRaisesBoxUnconfinedOnce_TS_07_7` (`internal/box/box_test.go`).

## 3. The sync takes the hands' ignore rules, and sends the tracked files they hide (D-5)

**Spec:** 07-REQ-2.1 syncs `HostDir` "including `.git`"; 07-REQ-2.7 and 2.8 want
the box's HEAD to be the host's and git in the box to agree with the host.

**Code:** the transfer applies the hands' ignore rules, so a `.env` and build
output stay out of the box (`syncSource`, `internal/box/adapter.go:20`). A file
that git tracks and a rule matches would then be missing, and git in the box
would see it deleted: `Session.syncLocked` (`internal/box/session.go:142`)
sends those files with a second, verbatim transfer (`gitx.TrackedIgnored`,
regular files only). `Sync` mirrors the host (`Delete`) and removes git's own
state in the box first (`refreshGit`, `session.go:97`), since the transfer takes
a file of the same size and modification second for unchanged, and a branch file
is as long as a hash is. The box's short HEAD is the host's only for the same
abbreviation: git's `core.abbrev` is the user's.

**Test:** `TestATrackedFileThatARuleIgnoresIsInTheBox`
(`internal/box/conformance_test.go`), `TestSyncSyncsAgain_TS_07_14` and
`TestSyncMirrorsTheHost` (`internal/box/box_test.go`).

## 4. A work tree that keeps its git elsewhere is refused, and the credentials of `.git/config` are taken out

**Spec:** 07-REQ-2.2 wants the top level of a work tree; 2.8 wants the host
untouched by any commit made in the box; the PRD wants git and the credentials
to stay on the host.

**Code:** `topLevel` (`internal/box/open.go`) also refuses a `HostDir` whose
`.git` is a file (a linked worktree, a submodule), because a commit made in a
box that held one would write the repository it is linked to. `Session.scrubGit`
(`internal/box/session.go:106`) removes from the box's `.git/config` the remote
URLs, extra headers, credential helpers and URL rewrites, after the first sync
and after every `Sync`.

**Test:** `TestOpenRefusesAWorkTreeThatKeepsItsGitElsewhere`,
`TestTheBoxHoldsNoCredentialOfTheHostsGitConfiguration`
(`internal/box/conformance_test.go`).

## 5. `Session.FS` does not use the read tool (D-6)

**Spec:** 07-REQ-3.1 serves `FS(ctx)` "over the box's read tools"; design
decision 4 says the host reads the submission with them.

**Code:** the read tool shows lines, at most 2000 and 50 KB, and a file that is
not valid UTF-8 makes the agent fail to encode its answer, which takes the whole
box down (every later call is `sandbox_unavailable`; an agentkit-go defect).
`FS` is built on the list tool, which says what exists, and on the export of a
single file, which is exact and has no line limit (`internal/box/fs.go:40`,
`exportOne` at `:145`). A file is read whole up to 32 MiB; a link is not
followed; a directory lists at most the 500 entries the tool gives. Transfers
go one at a time (`Session.export`), because the box takes one stream.

**Test:** `TestFSIsReadOnly_TS_07_18`, `TestFSReadsTheExactBytes`
(`internal/box/session_test.go`), `TestFilesAreReadTogether`,
`TestStatInADirectoryTooLongToList` (`internal/box/conformance_test.go`).

## 6. The host writes the run directory's submission from what it checked

**Spec:** 07-REQ-7.2 copies the re-validated submission to
`<run_dir>/submission.json`; TS-07-49 compares the copy with the box's file.

**Code:** `readSubmission` (`internal/brain/decide.go`) writes
`submission.Marshal` of what `submission.Check` accepted, so a file that a brain
with hands of its own wrote (a second `payload` key that a decoder takes for the
first, a field no kind of file has) is not copied as it is. For a file `Submit`
wrote the bytes are the same, which is what TS-07-49 asserts. A run directory
with no accepted submission has no `submission.json`: it is removed when an
attempt starts and when one ends without a submission (`clearHostSubmission`),
since the `local` box lets a process of the brain write the host's files.
`submission.Check` also bounds what the host reads (at most 2 MiB of file, 1 MiB
of payload, valid UTF-8) and turns a panic of the kind into a problem.

**Test:** `TestAMatchingSubmissionIsAccepted_TS_07_49` (`internal/brain/decide_test.go`),
`TestTheHostKeepsOnlyWhatItChecked`, `TestAForgedFileLargerThanASubmissionIsRefused`,
`TestARunDirectoryHoldsNoSubmissionThatAfDidNotAccept`
(`internal/brain/conformance_test.go`).

## 7. A panic does not stop the host from deciding

**Spec:** 07-REQ-6.7 and 7.7 return a failure for a brain that panicked.

**Code:** `Execute` (`internal/brain/execute.go`) decides from the file after a
panic as it does after an error: a valid submission wins, with the warning
`brain_error` and the stack kept; without one the failure is `brain` as before.

**Test:** `TestAPanicAfterAValidSubmissionKeepsTheSubmission`,
`TestAPanicWithNoSubmissionIsABrainFailure`
(`internal/brain/conformance_test.go`).

## 8. The tools the brief does not allow are in the driver's toolset

**Spec:** 07-REQ-10.1 gives the driver "the allowlisted box tools plus a
terminating tool".

**Code:** the driver answers a call to a name it does not know itself (`unknown
tool`), before its guard is asked, so a call to a tool outside the allowlist
would reach the model as that error and never the gate. The other tools of the box
are therefore in the toolset as stubs that deny with the allowlist's words, a
wrapper over the provider leaves them out of what the model is offered, and
their guidelines are left out of the system prompt
(`internal/brain/agentkit.go:130`, `visibleTools` in
`internal/brain/agentkit_adapter.go:117`). The model is offered the allowlist
and the terminating tool, as the spec says.

**Test:** `TestTheFirstRequestCarriesTheBrief_TS_07_72`,
`TestADenialIsTheNextTurn_TS_07_62` (`internal/brain/agentkit_test.go`),
`TestTheModelIsNotToldOfTheToolsItMayNotUse` (`internal/brain/conformance_test.go`).

## 8a. The gate judges the path the tool will use, and is not a boundary

**Spec:** 07-REQ-8.5 denies a write under `.agent-fox/`; PRD design decision 15
says a brain cannot write the submission itself.

**Code:** the gate normalises a path as the box's tools do before the policy
judges it (`normalisedPaths`, `internal/brain/agentkit_adapter.go`), which closes
the spellings that got past it (spaces, a leading `@`, quotes, a `file://`
prefix). It cannot read what a program that the guard allows goes on to write,
nor decide a batch of calls after the earlier calls of the batch have run, and the
`local` box has no isolation; `internal/policy` says the policy is prevention and
not a boundary. What holds is the host's decision from the file (item 6).

**Test:** `TestTheGateJudgesThePathTheToolWillUse`
(`internal/brain/conformance_test.go`) and `TestWritesUnderAgentFoxAreDenied_TS_07_61`
(`internal/brain/gate_test.go`).

## 9. Under a typed schema the two brains can leave different bytes

**Spec:** 07-REQ-10.6 wants the same payload bytes from the `scripted` and the
`agentkit` brain for any valid payload.

**Code:** the driver prepares the arguments of a tool before the tool sees them
(it coerces a number given as a string, and drops an optional field that is
`null`), so when the schema of the kind is typed the payload the `agentkit`
brain submits can differ in those bytes from the one the `scripted` brain does,
though both are valid. With a permissive schema they are the same. `Kind.Validate`
judges the payload that arrives, and the host re-checks the file.

**Test:** `TestAnyValidPayloadIsTheSameForBothBrains_TS_07_78`
(`internal/brain/contract_test.go`) uses a permissive schema, as the spec's test
does.

## 10. `Getenv` also says whether the variable is set

**Spec:** `Options.Getenv` is a function of a name.

**Code:** `brain.Getenv` is `func(name string) (string, bool)`
(`internal/brain/registry.go:15`), because that is what the hands' provider
resolves its credentials with (`anthropic.Resolve` takes a `Lookup`), and an
empty value must be told from an unset one.

**Test:** `TestAMissingCredentialIsAuth_TS_07_73` (`internal/brain/agentkit_test.go`),
`TestTheBrainsNeedNoKeyAndNoNetwork_TS_07_79` (`internal/brain/contract_test.go`).
