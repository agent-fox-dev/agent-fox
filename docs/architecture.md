# Architecture

How the pieces of agent-fox fit together. [Development](development.md) lists
the packages one by one; this page follows the data.

## Every phase is a fresh agent

`internal/agentrun` runs one phase: a system prompt, a user prompt, a set of
tools and a terminating tool. A phase starts with no memory of the one before
it, so everything it should know has to be in its user prompt. The four
read-only file tools (`read_file`, `list_files`, `find_files`, `search_files`)
are how it learns the rest.

## The repository map

`internal/repomap` builds a token-budgeted repository map from the tracked file
tree and top-level declarations. It is injected into every phase's user prompt
of `triage`, `fix`, `spec` and `impl`, under a `## Repository map` heading, so a
phase does not spend turns on `list_files` to learn the layout or where a
declaration lives.

- **Contents.** The directory tree of every non-ignored, non-hidden file, and
  each source file's top-level declarations (kind, name, line). It walks with
  AgentKit's `tools.Walk`, so it follows the same ignore rules and workspace
  confinement as `search_files`, and it reads declarations with AgentKit's
  `outline` package.
- **Budget.** `--repo-map-tokens` (default `6000`, measured with
  `afspec.EstimateTokens`) bounds the map. When the full map is larger it is
  reduced in a fixed order: unexported declarations, then test-file
  declarations, then declarations of the deepest directories, then the deepest
  directories collapse to `dir/ (N files)`. Directories the tool's input names
  are reduced last. `--repo-map-tokens 0` disables the map, and every prompt is
  then byte-identical to one built without it.
- **Determinism.** The same tree, input paths and budget give a byte-identical
  map. It carries no timestamps or sizes.
- **Placement.** The map is in the user prompt, not the system prompt, so it
  does not fragment the cached system prefix. It sits before the prior phase's
  conclusions. `agentrun.Phase.RepoMap` carries the same text so the runner can
  report its size under `--verbose` and tests can assert on it; the runner
  never injects it into the prompt.
- **When it is built.** `triage` and `spec` never change the tree, so each
  builds the map once per run and reuses it for every phase and every scope of
  a split. `fix` and `impl` change the tree, so they build before the first
  phase and rebuild before a later phase only when `HEAD` or the set of dirty
  files changed since the last build (`repomap.TreeChangeDetector`, driven by
  `repomap.Refresher`). A declaration added by task N is therefore in task
  N+1's map.
- **Failure.** The map never fails a run. When it cannot be built the phase
  runs without it and a `low` warning with code `repo_map_build_failed` is
  recorded.
- **Trust.** Declaration names are repository text, like anything `read_file`
  returns. The map's opening sentence says it is derived from the repository,
  not instructions. It is prompt-only and never appears in the envelope.

See the [tool reference](cli.md#shared-flags) for the flag and the
[warning codes](cli.md#warning-codes) for `repo_map_build_failed`.
