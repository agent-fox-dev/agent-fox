# Rebuild triage and fix on the toolbelt and run them locally

Status: **proposed** (2026-10-20). Nothing here is implemented. This is
step 1 of [PRD 01](01-triage-and-fix-forge-issues-as-a-service.md)'s
order of work (§10): the toolbelt the brains call, the two agents, and
`af --local`, which needs nothing from hub. Written to PRD 01's decisions
D1 (two binaries), D6 (triage replaces the body), D7 (the landing), D8
(two brains) and to [`docs/architecture.md`](../architecture.md)'s rule:
the brain never touches git, the forge or hub except through `af`, and
`af` never runs a model except through a brain it was handed. Details
left to settle at spec time are marked *spec time*.

## Intent

The previous implementation (`1e6458d`) had three programs that each
owned an agent loop: the model was called once per phase, the program
measured around it, and the envelope on stdout said what happened. Its
contracts were right and its shape is what `docs/architecture.md`
retires: the loop belongs to the brain, the measuring and every effect
belong to the program, and the two meet through commands.

This PRD rebuilds `triage` and `fix` in that shape, as subcommands of one
binary, `af`, on AgentKit's hands:

- **Ring 1**, the facts a brain may ask for as often as it likes:
  `af lang detect`, `af gate run`, `af gate compare`, `af revert-check`,
  `af guard check`, `af triage brief`, `af fix brief`.
- **Ring 2**, the effects the program owns: `af triage submit`,
  `af fix submit` (validation, the only way a submission exists),
  `af triage land`, `af fix land` (the body rewrite, the branch, the
  commit, the push, the pull request, the labels).
- **The brains**, behind one interface: `af triage run` and `af fix run`
  (the AgentKit driver in-process), a headless Claude Code harness, and
  a scripted brain for tests.
- **The loop**, `af triage <input>` and `af fix <input>`, which strings
  the above together on one machine: a box from a local provider, the
  brief, the brain, the measurement, the landing, the envelope on stdout
  with the old exit-code table.

The service (PRD 03) is a worker that runs the same loop over a hub
lease; `af --local` is the same loop with the developer's own machine,
credentials and checkout. One implementation, three callers.

## Goals

1. `af fix <issue> --dir ./repo` on a developer's machine, with a forge
   token and a vendor key in the environment, lands a verified change as
   a pull request and prints one envelope; `af triage` does the same for
   a diagnosis. Both run with a person watching or unattended.
2. Every effect beyond the working tree (a branch, a commit, a push, a
   forge write) is made by `af` from facts it measured, never from a
   brain's claim, and every submission is validated before it exists.
3. The brain is a value behind one interface: the driver, Claude Code
   headless, or a script, chosen by a flag, and a test runs the same
   brief through each and sees the same submission.
4. The brain's tree lives in a box the loop holds (`landlock` or `podman`
   on the machine, `local` in tests, `none` for a person who is their own
   boundary); `.git` goes in and never comes out; the commit that counts
   is the host's.
5. The old envelope contract holds: one JSON object on stdout, `status`
   in {`done`, `failed`, `usage`, `needs_human`, `unverified`}, the
   exit-code table, `needs_human` with `--context`, `artifacts`,
   `side_effects`, `next`, coded warnings, `--dry-run`, `--schema`.
6. Everything runs offline in `make check`: the faux provider for the
   driver, a scripted harness, `httptest` forges, `local` as the box.

## Non-goals

- **The service, hub, leases, labels as triggers.** PRD 03 to 05.
- **`impl`, `spec`, `review`.** Their own PRDs, on this toolbelt.
- **Resuming a brain's transcript.** An attempt that dies is re-run from
  the branch; the submission file is the only state the brain leaves.
- **A second harness beyond Claude Code** in the first release. `pi`,
  `codex` and `opencode` are rows in the brain table once their
  non-interactive surfaces are confirmed; the interface is theirs.
- **Code mode in the brief.** The driver may bind it later; nothing here
  depends on it.
- **Changing `issuex`.** It is consumed from `aflib` (aflib PRD 01), as
  it is.

## Background: what is reused

| Piece | From | What |
|---|---|---|
| The envelope, exit codes, warnings, `--context`, `--schema`, `--dry-run`, the report and events files | `internal/toolio` at `1e6458d` | revived as it was, under `internal/toolio`, with `tool` naming the subcommand |
| The triage contract | `issuetriage` at `1e6458d` | the system mandate, the fenced report, the `file_diagnosis` schema, path resolution against the workspace, `rejected_path_calls` |
| The fix contract | `codefix` at `1e6458d` | the verdict table, the revert check deciding `Closes` versus `Refs`, parking as `wip:`, the pull-request body rendered from facts, the structural checks |
| Hermetic git | `internal/gitx` | `Git`: branches, commits, `Push` with retries, `DiffSince`, `CommitsSince`, the hermetic runner |
| The hands | agentkit-go | `workspace.Open`, `tools.For`, `guard.Allow`, `guard.Check`, `agentkit.New` and `NewRunner`, `provider/faux`, `sandbox.Open`, `local`, `landlock`, `podman`, `LocalDir` sync and export, `mcpserve` for a harness that borrows hands |
| A harness in a box | agentkit-go `examples/harness` | Claude Code headless with `--tools ""`, the served tools, `--output-format json` for cost and turns |
| The forge | aflib `issuex` | issues, labels, comments, pull requests on GitHub and GitLab; `httptest` forges for tests |
| The skills | `skills/af-fix`, `skills/af-bug-hunter` | the method a harness brain follows; `af-triage` is new |

## 1. The binary

```
af triage <issue|file|text> [--dir .] [--brain agentkit|claude|scripted] [--sandbox landlock|podman|local|none]
          [--context …] [--repo owner/repo] [--land issue|none] [--dry-run] [--budget 2] [--max-turns 100] [--timeout 15m]
af fix    <issue|file|text> [--dir .] [--brain …] [--sandbox …] [--context …] [--land pr|branch|none] [--base main]
          [--dry-run] [--no-verify] [--budget 10] [--max-turns 150] [--timeout 45m] [--attempts 2]

af lang detect [--dir .]                        the languages and the gate commands; JSON
af gate run [--dir .] [--label baseline|after]  run the checks, cache by tree hash; JSON
af gate compare --before <id> --after <id>      the verdict
af revert-check [--dir .] --since <ref>         the tests with the fix put back
af guard check                                  a host hook's payload on stdin; allow or deny
af triage brief|run|submit|land                 ring 1 and ring 2 of triage, for a brain and for the loop
af fix brief|run|submit|land                    the same for fix
af --version · af --schema
```

Every command reads JSON on stdin where it takes input, writes one JSON
object on stdout, and uses the exit-code table. `--local` from PRD 01 is
the default here and needs no flag until the service exists; PRD 03 adds
`--workspace` and makes `--local` explicit.

## 2. The run directory and the box

A run lives in `.agent-fox/run/<id>/` under the checkout, git-ignored by
its own `.gitignore`: the ledger (what the program measured, when), the
baseline result and its log, the rendered briefs, the submission, the
check logs a brief refers to by path. It is the state; nothing in a
transcript is.

The box, from `--sandbox`:

| Provider | Where the tree is | When |
|---|---|---|
| `landlock` (default on Linux) | a confined child on this machine, the checkout synced in | a developer's loop; CI |
| `podman` | a container, the tools image | a developer who wants the boundary the service has |
| `local` | an in-process agent, no isolation | tests |
| `none` | the checkout itself | a person with Claude Code open, who is the boundary; the hook is the only guard |

With a box, the tree is synced in with `.git` (so `git log` and `git
diff` inside are true), the brief and the gate's command table under
`.agent-fox/`, and exported back without `.git`; the host classifies the
exported tree against its baseline and makes the commit. With `none`,
the host's clone is the tree and the protected-path revert and the
foreign-commit check are the invariant.

## 3. The brains

```go
type Brain interface {
	Run(ctx context.Context, b Brief) (Outcome, error)
}
type Brief struct {
	Document   string          // the rendered markdown; byte-identical run-constant prefix, then the task block
	Tools      []string        // the allowlist the host is asked to enforce
	Hook       HookConfig      // what af guard check reads
	Box        sandbox.Sandbox // nil with --sandbox none
	Env        map[string]string
	MaxCostUSD float64
	Timeout    time.Duration
}
type Outcome struct {
	Submitted  bool    // af … submit received a valid submission during the run
	CostUSD    float64 // as the brain reports it; 0 when it cannot
	Transcript string  // a path when the brain keeps one; never read by af
}
```

| Brain | Where it runs | How |
|---|---|---|
| `agentkit` | inside the box as `af triage run` / `af fix run` over `tools.For` on the tree (or on the host over `Box.Tools` with `--borrowed-hands`, PRD 01's `--local` exception) | `agentkit.New` with the brief as the first user message and the run-constant part in `Config.Prefix`, `guard.Allow` over the gate programs, the terminating tool calling `af … submit` in-process; `provider/faux` in tests |
| `claude` | inside the box from the tools image, or on the host with `--sandbox none` | Claude Code headless: the brief as the prompt, the skill as the appended system prompt, `--allowedTools`, `--permission-mode dontAsk`, the settings file with the `af guard check` hook, `--output-format json` for cost; one `execute` |
| `scripted` | the host | replays a recorded sequence of tool calls and a submission; the proof that the interface is honest |

The submission arrives through `af … submit`, inside the box, as
`.agent-fox/run/<id>/submission.json`, validated there and read by the
host after the brain's process ends. Nothing is parsed from a brain's
output. A contract test runs one brief through every available brain and
asserts the same submission reached `af`.

## 4. Triage

As PRD 01 §4.1, with the mechanics named:

- **Input** by `issuex.ParseIssueURL`, a file, or text; `--repo` or the
  input issue's repository or the checkout's `origin` names the forge.
- **Brief**: the report fenced with its provenance, the comments, the
  repository map at a token budget (`internal/repomap` revived on the
  hands' `Walk` and `outline`), the triage skill, `--context`.
- **Tools**: the six read tools and `code_search` when indexed; no
  writer, no shell. **Submit** validates `file_diagnosis` (`severity`,
  `confidence`, `root_cause`, `affected_files` resolved against the
  workspace, `suggested_fix`, `acceptance_criteria`, `question`) and
  rejects naming every problem at once; a path not in the workspace is
  the brain's next turn and counts in `rejected_path_calls`.
- **Land** (`--land issue`, the default): the body replaced with the
  rendered diagnosis and `## Original report` (D6), or an issue created
  for text and file inputs; `--land none` prints the diagnosis only.
  Labels are the service's (PRD 04); `--label` adds any the caller names,
  as the old tool did.
- **Bounds**: 100 turns, USD 2, 15 minutes.

## 5. Fix

As PRD 01 §4.2, with the mechanics named:

- **Preflight** before any model call: a clean tree, the base branch
  (`--base`, else the checked-out branch), `af lang detect` finds a gate,
  the forge is reachable when `--land pr`. The baseline gate runs once
  and is cached by tree hash.
- **Brief**: the issue (and its diagnosis body when triaged), the
  `af-fix` skill, the gate commands and the baseline, the map,
  `--context`; the closing rule: never commit, never push, never edit the
  issue, `af fix submit` when done.
- **Tools**: the hands' set with the guard over the detected gate
  programs plus `git` for reading; the hook refuses the suite command and
  names `af gate run`, which feeds the cache.
- **Measure**: the gate after; `af gate compare` (`pass`,
  `pass_was_already_failing`, `regressed`, `still_failing`); `af
  revert-check` (the non-test files put back, the tests kept; failing
  proves the fix); the scope check; the protected-path revert
  (`.specs/`, `.github/workflows/`, the forge's config files). A refusal
  is the brain's next turn within the attempt's bounds.
- **Land**: branch `fix/<N>-<slug>` from the base, one commit with the
  trailer `Agent-Fox-Run: <id>`, push with `gitx.Push`'s retries, a pull
  request through `issuex` whose body is rendered from facts with
  `Closes` or `Refs <issue url>`, draft when the verdict did not land;
  `--land branch` stops after the push, `--land none` after the commit.
  A run that does not land parks as `wip:` and returns the checkout to
  the base, as the old tool did.
- **Bounds**: 150 turns, USD 10, 45 minutes, 2 attempts.

## 6. The envelope

`internal/toolio` as it was at `1e6458d`, with these changes only: `tool`
is `triage` or `fix` (the subcommand); `artifacts` gains `{kind:
"submission", path}` and `{kind: "run_dir", path}`; `usage` carries
`brain` and the brain's reported cost; `result` for `fix` gains `box`
(`provider`, `image`, `network`). The schema version is the next major
of the old one, because the field order and the `warnings` shape of the
old PRD 01 are kept and nothing is removed, but the tools' names moved.
`--schema` prints it; the golden files are regenerated once.

## 7. Tests

- Offline, every path: `faux` for the driver, `scripted` as the harness
  stand-in, `httptest` GitHub and GitLab, `local` as the box, temporary
  repositories for the landing.
- The Brain contract test over the three brains.
- The old tools' smoke tests, ported: every exit code, `--dry-run`
  writes nothing, `needs_human` round-trips through `--context`, a path
  the brain did not read is refused, the revert check decides `Closes`,
  a parked run leaves a clean tree, `--schema` matches the golden.
- The box invariant: a commit the brain makes inside is not on the
  branch; `.git` is not exported; `Spec.Env` for a harness brain holds
  the vendor key and nothing of the host's forge token.
- Opt-in: `landlock` on a kernel with ABI 4, `podman` with an engine,
  `claude` with a key; each skipped, not failed, when absent.

## 8. Order of work

1. **The envelope and the shell**: `cmd/af`, `internal/toolio` revived,
   `--schema`, `--version`, the exit codes, the run directory.
2. **Ring 1**: `af lang detect`, `af gate run|compare`, `af revert-check`,
   `af guard check`, the repository map, on the hands.
3. **The brains and the briefs**: the interface, `agentkit` over `faux`,
   `scripted`, `af triage brief|run|submit`, `af fix brief|run|submit`.
4. **Triage end to end** with `local`, the body rewrite through `issuex`,
   the `af-triage` skill.
5. **Fix end to end** with `local`: measure, land, park, the pull request.
6. **The boxes**: `landlock`, `podman`, `none`; the export and the
   host-side commit; the `claude` brain in the tools image.
7. **Documentation**: `docs/cli.md`, `docs/configuration.md`,
   `docs/architecture.md` (the *to be written* sections), `README.md`,
   the skills.

## 9. Acceptance criteria

1. `af fix ./bug.md --dir repo --brain scripted --sandbox local --land pr`
   against an `httptest` forge lands `fix/…`, opens a pull request with
   `Closes` when the revert check failed and `Refs` when it passed,
   prints an envelope with `status: done` and exit 0, and the brain's
   inside commit is not on the branch.
2. `af triage <issue> --brain agentkit --sandbox local` with `faux`
   replaces the issue body through the `httptest` forge with a diagnosis
   whose `## Original report` is byte-identical to the original; a
   scripted brain citing a missing path gets the refusal as its next turn
   and the envelope reports `rejected_path_calls: 1`.
3. A brain ending with a `question` gives exit 3, `needs_human` with
   options, and the identical command with `--context "B"` reaches the
   implement phase with the answer in the brief and a byte-identical
   run-constant prefix.
4. `--dry-run` sends no request to the forge and makes no commit;
   `--land none` commits and pushes nothing; a red gate after the change
   parks the work as `wip:` with exit 4 and a clean tree.
5. The Brain contract test passes for `agentkit`, `scripted` and, when a
   key is present, `claude`.
6. `af gate run` twice on the same tree runs the checks once; `af guard
   check` denies the suite command with a message naming `af gate run`
   and allows the gate programs.
7. `make check` passes offline; `make doc` is committed; the `--schema`
   goldens are committed.

## 10. What is given up, stated plainly

- **Three programs become one binary.** `triage` and `fix` as commands
  stop existing; the envelope's `tool` field says which ran.
- **The brain's cost is as reported.** The driver measures it; a harness
  reports it; a script reports nothing. `usage` says which.
- **`none` has no boundary** but the hook and the gate. It is for a
  person at their own checkout and says so in its name.

## Documentation

| Change | Update |
|---|---|
| `af` and every subcommand, flags, exit codes, the envelope | `docs/cli.md` (new) |
| environment variables, the box providers, the brains | `docs/configuration.md` (new) |
| the run directory, the brief, the brains, the boxes | `docs/architecture.md` (its *to be written* sections) |
| the Go packages | `make doc`, `docs/api.md` (new) |
| the skills | `skills/af-triage` (new), `skills/af-fix` |
| setup | `README.md` |
