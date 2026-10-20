# Erratum 09: where `af fix` does something other than the spec says

**Spec:** `09_af_fix`. Each divergence below was decided during the survey or on the
way, and is recorded here because the spec stays as it was written. The code and the
test of each are named so that a reader can check what is delivered. Where a commit
declares the divergence, its `Deviation:` trailer is named.

## 1. The skill is a flat file, `skills/af-fix` (task 11)

**Spec:** task 11 and TS-09-71 name `skills/af-fix/SKILL.md`.

**Code:** every skill of the repository is one file, `skills/af-<name>`, with its
frontmatter: the Go package `skills` embeds them with `//go:embed af-*`
(`skills/skills.go:14`), `skills.Fix()` reads `af-fix` (`skills/skills.go:23`), and
`make install-skills` copies each file to `~/.claude/skills/<name>/SKILL.md`. A
directory `skills/af-fix/` would have changed the embedding for the one skill. The
rewritten skill is `skills/af-fix`, with the same three frontmatter keys.

**Test:** `TestTheFixSkillIsTheToolbeltFlow_TS_09_71`
(`internal/fix/docs_test.go:45`), which reads `skills/af-fix`, and
`TestFixIsTheBodyOfTheFixSkill` (`skills/skills_test.go:158`).

## 2. TS-09-70 runs in `cmd/af`, and has no `--model` flag (task 11)

**Spec:** TS-09-70 is in `internal/fix/fix_e2e_test.go` and runs `af fix … --model
provider/faux`.

**Code:** the run needs `newApp`, the fix registry and `fixBrainOptions`, which are
in package `main`, so the test is `cmd/af/fix_e2e_test.go`. No command has a
`--model` flag: the model is `AF_MODEL`, and the provider is `brain.Options.NewProvider`,
which a test replaces (`fixFauxBrains`, `cmd/af/fix_e2e_test.go:16`), as TS-08-69 does
for triage. The environment holds no vendor key and no token, the forge is
`fakeforge` as GitHub and as GitLab, and a transport guard fails any request to a
host that is not this machine.

**Test:** `TestTheFixSuiteRunsOffline_TS_09_70` (`cmd/af/fix_e2e_test.go:44`). It
passed when it was first written, because tasks 1 to 10 had made the path real: there
was no red run to watch, and the test was checked against mutants of the production
code instead.

## 3. `fix run` is in `cmd/af/fix_run.go` (task 4)

**Spec:** the architecture puts the five commands in `cmd/af/fix.go`.

**Code:** `registerFixRun` (`cmd/af/fix_run.go:34`) and `fixRun`
(`cmd/af/fix_run.go:84`). `fix.go` holds the loop, `fix_brief.go` the brief and the
submit, `fix_land.go` the land.

## 4. `fix.json` holds more than the spec lists (task 3)

**Spec:** 09-REQ-3.5 lists the fields of `fix.json`.

**Code:** `State` (`internal/fix/state.go:20`) also holds `prefix_bytes` (where the
prefix of `brief.md` ends, so that a later step splits the brief where it was
rendered and checks the hash), `suite` and `programs` (what the gate detected beside
the table, which the box's `guard.json` is made of), `baseline_ms` and
`baseline_passed`, and `brain`, `cost_usd` and `cost_source` (what the rounds so far
used, which the footer of the pull request says). A later step cannot be made without
them.

**Test:** `TestFixJSONHasTheInputKeyOfEveryKindOfInput_TS_09_17` (`cmd/af/fix_brief_test.go:94`).

## 5. Helpers in the packages the plan did not name (tasks 3 to 7)

**Spec:** `touches` of the tasks name `internal/fix`, `cmd/af`, `internal/brain`,
`internal/gitx`, `skills` and the docs.

**Code:** the survey decided the following, each small and each with a test of its
own: `project.MarshalGateFile` (`internal/project/gate.go:150`) and
`policy.MarshalGuardFile` (`internal/policy/load.go:46`) write the two files the box
is given; `policy.IsProtected` (`internal/policy/policy.go:67`) is the rule that
decides whether a path belongs to `af`; `repomap.BuildDir`
(`internal/repomap/dir.go:13`) makes the map of a directory, and the triage brief now
uses it; `gitx.RevParse`, `Ignored`, `TrackedIgnored` and `CreateBranchFrom`
(`internal/gitx/git.go:156`, `546`, `605`, `613`); `toolio.CategoryPreflight`
(`internal/toolio/failure.go:32`); and `fakeforge` can create pull and merge requests.

## 6. Some tests are at the level of the loop function and the command (tasks 9 and 10)

**Spec:** TS-09-24, 36, 37, 44, 46, 48, 49 verify behaviour of the loop.

**Code:** `RunLoop` (`internal/fix/loop.go:123`) is tested with a held box and a
scripted brain in `internal/fix`, and the same behaviour through the shell in
`cmd/af/fix_loop_test.go` and `cmd/af/fix_end_test.go`. The command glue of tasks 3
and 9 was written together with its tests, and not after a red run of the glue alone.

## 7. The smoke tests are in `cmd/af/fix_smoke_test.go` only (task 12)

**Spec:** task 12 names `internal/fix/smoke_test.go` and `cmd/af/fix_smoke_test.go`.

**Code:** a smoke test runs the commands through the shell, with the registry, the
forge injected through `App.NewForge` and the flags of the commands, which are in
package `main`; there is nothing for it to run in package `fix` alone. TS-09-73 to
TS-09-79 are in `cmd/af/fix_smoke_test.go`, and `internal/fix/smoke_test.go` does not
exist. A brain cannot run `git commit` through the tool gate of the box, so
TS-09-73 commits in the box with a `make` target that its fixture repository has.

**Test:** `TestAnIssueBecomesOneCommitOnItsBranchAndAPullRequest_TS_09_73` and the six
after it (`cmd/af/fix_smoke_test.go`).
