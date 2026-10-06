# Erratum: two places where spec 14's tests ask for what the code cannot be

Recorded because `.specs/14_repo_map` reads as unmet in TS-14-25 and TS-14-39.
In both, the test's outcome is met; what the spec describes around it does not
exist in the code, and could not without making the code worse.

## TS-14-25 and 14-REQ-7.2: verbosity is the observer's, not the runner's

**Spec:** "a Runner configured with verbose=true and a recording Observer",
with the pseudocode `agentrun.Runner{Observer: obs, Verbose: true}`.

**Is:** `agentrun.Runner` has no `Verbose` field, and gets none. `--verbose`
reaches a phase as the runner's Observer, `toolio.Progress`, whose `Detail`
prints only when the flag is set (`internal/toolio/progress.go:130`). Every
other detail the runner reports goes the same way, so the runner reports the
map's size whenever `RepoMap` is non-empty (`internal/agentrun/phase.go:322`)
and the observer decides whether it is shown. A `Verbose` field beside it would
be a second switch for the same thing.

`TestTS14_25_VerboseShowsTheRepoMapTokenCount`
(`internal/toolio/repomap_verbose_test.go:71`) drives the whole path the spec
means: `--verbose` through `App.Main`, the `Progress` the shell builds, a real
`Runner` and `Observer.Detail`. With the flag, stderr carries
`repo map: 100 tokens` for a 400-byte map; without it, or with an empty map,
nothing about the map is shown. `TestTS14_25_RunnerLogsRepoMapTokens`
(`internal/agentrun/phase_test.go:402`) pins the runner's half alone.

## TS-14-39 and 14-PATH-4: a corrupt source file cannot make the build fail

**Spec:** "a git repository where outline.File fails on a corrupt source file
causing repomap.Build to return an error".

**Is:** that repository cannot exist under the spec's own 14-REQ-1.6: an
outline failure on one file leaves the file in the map without declarations,
and `Build` returns no error (`internal/repomap/repomap.go:122`). The walk
cannot fail on the tree either: `tools.Walk` skips an unreadable entry and
treats a missing root as empty. What makes `Build` fail is a cancelled context
or a workspace the walk refuses, neither of which a source file causes.

So `TestTS14_39_MapBuildFailureDegradesGracefully`
(`codefix/smoke_test.go:87`) has the pipeline's build seam call the real
`repomap.Build` with an already-cancelled context (`codefix/smoke_test.go:104`),
which fails in the real walk. Everything the spec asks of the run is then
checked against the real pipeline: the analysis prompt that reaches the
(scripted) model has no `## Repository map` block, the run lands, and the
envelope carries a `low` `repo_map_build_failed` warning. The degradation path
is defensive: in a real run a cancelled context ends the run as well.
