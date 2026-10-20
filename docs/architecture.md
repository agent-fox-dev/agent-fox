# Architecture: a brain, a toolbelt, and the hands

**Status: proposed. This is a placeholder for the target architecture, not a
description of the code as it is.** Today's architecture is the one ADR 03,
ADR 04 and ADR 09 describe: three programs that each own an agent loop built
on AgentKit, with the model called once per phase. This document describes
what replaces it once AgentKit
[PRD 10](https://github.com/agent-fox-dev/agentkit-go/blob/main/docs/prd/10-cut-agentkit-down-to-the-hands.md)
is in place. The sections marked *to be written* are filled in as the
pieces land; until then the design is the content.

## The one rule

**The brain never touches git, the forge, or the spec state except through
`af`, and `af` never runs a model except through a brain it was handed.**

Everything below is a consequence of that rule. The model does the parts
that need judgment: surveying, writing code and tests, reviewing, deciding
what is a deviation. The program measures everything measurable and owns
every effect that reaches beyond the working tree. ADR 03's argument stands
unchanged; what changes is who owns the loop.

## The shape

```
 brain (interchangeable)        Claude Code | Codex | Pi | the AgentKit driver | a person
   owns:  the loop, its context, read/edit/shell, its own compaction
   gets:  a brief (markdown + facts), a tool allowlist, a policy hook
   gives: a submission (JSON) through af
        |
        |  shell, or MCP (af serve), or in-process (the AgentKit driver)
        v
 af (this repository, one binary)   the toolbelt
   ring 1  facts    af spec validate · af lang detect · af map · af gate run/compare
                    af revert-check · af conform scan · af scope check · af task brief
   ring 2  effects  af run start · af task start/land · af run land · af review submit
                    af fix start/submit/land · af forge ...
   state            .agent-fox/run/<id>/ (ledger, baseline, briefs) + tasks.json + git
        |
        v
 agentkit-go (the hands)        Workspace, Walk, the ignore engine, the subprocess
                                runner, outline, symbols, references, codesearch,
                                guard.Check, the MCP pool, code mode, the driver,
                                and the sandbox: the box the tree lives in
        |
        |  gateway.Dial, or a local provider
        v
 hub (the backend)              workspaces, scoped tokens, the git server, carry
                                patches, the merge queue, the job queue, tasks
```

Three layers, each replaceable without the others noticing:

- **The brain** is whatever runs the model and its loop. It is not part of
  this repository. Claude Code and Pi are the brains a person already has
  open; the AgentKit driver is the one `af` can run unattended without a
  vendor CLI on the machine; a person at a shell is a valid brain too.
- **`af`** is the toolbelt: the enforcement that today lives inside
  `codeimpl`, `codefix`, `internal/conform`, `internal/checks`,
  `internal/gitx` and `issuex` (now a package of
  [aflib](https://github.com/agent-fox-dev/aflib)), exposed as commands with the envelope
  `internal/toolio` defines and [`docs/cli.md`](cli.md) describes. It is what
  [PRD 13](prds/13-rebuild-fix-on-a-shared-change-engine.md) called
  `internal/engine`, minus the phases and the prompts.
- **The hands** are AgentKit after PRD 10: workspace mechanics with no
  opinion about phases, specs or envelopes. `af` is built on them. The
  AgentKit driver is one of the brains, not a layer of its own. The
  sandbox is the hands' too: the box a tree lives in while a brain works
  on it (*The box*, below).
- **The hub** is the backend: the one caller of `af impl` that is not a
  person, and the authority on where a landed branch goes afterwards. It
  is not a layer `af` is built on, and `af` runs without it (*The hub*,
  below).

## The toolbelt

### Ring 1: facts

Pure, idempotent, JSON out, no side effects. Any brain may call them as
often as it likes, and so may a person.

| Command | What it answers | Built from |
|---|---|---|
| `af spec validate <ref>` | is this package a complete plan (C1 to C11) | `afspec` |
| `af lang detect` | what the repository is written in, its gate commands | `internal/project`, later the language profiles of PRD 13 §1 |
| `af map` | the repository map at a token budget | `internal/repomap` on the file view and the hands' `outline` |
| `af gate run [--baseline\|--label]` | run the project's checks; results cached by tree hash | `internal/checks` on the hands' runner |
| `af gate compare` | the before/after verdict | `checks.Compare` |
| `af revert-check` | do the tests the change wrote fail without it | `internal/conform` |
| `af conform scan` | the structural findings over the changed files | `internal/conform` on `outline` |
| `af scope check` | files outside the spec's `touches` | the ledger |
| `af task brief <spec>/<task>` | the document a brain implements from | the templated documents of PRD 13 §6.6 |
| `af review brief` | the packet an independent reviewer answers | `internal/conform/review` |

### Ring 2: effects

Gated on ring-1 facts, program-owned, the only path to git, the forge and
the spec state.

| Command | What it does | What refuses it |
|---|---|---|
| `af run start <spec>` | preflight, baseline gate, branch, ledger snapshot, the run directory, the host hook and MCP settings | an invalid package, a dirty tree, a red baseline without `--repair`, an unmet upstream |
| `af task start <id>` | `pending → in_progress`, the attempt counter, the untracked snapshot | a task whose dependency is not done |
| `af task submit <id>` | validates the report JSON: every owned test answered with evidence, every citation resolves, a commit subject | the missing ids, named all at once; what was right is kept |
| `af task land <id>` | gate, compare, revert check, ledger classification, protected-path revert, scope; then the state write and the commit with the `Spec:` trailer | a non-landable verdict, a foreign commit on the branch, a report that says `fail` |
| `af review submit` | validates the review: every requirement, test and decision in scope answered, every `file:line` real | the skipped ids |
| `af run land` | the hermetic gate, push, the pull request rendered from facts, draft when findings remain | a red hermetic gate |
| `af fix start <input>`, `af fix submit-analysis`, `af fix land` | the same for a fix: preflight and baseline; the diagnosis; gate, revert check, `Closes` or `Refs`, commit, push, PR | as above |
| `af forge ...` | issue, comment, PR, labels | `--dry-run` |
| `af guard check` | reads a host's hook payload, answers allow or deny | see *The policy hook* |

Every terminating tool the pipelines have today (`submit_task`,
`submit_review`, `submit_analysis`, `submit_implementation`) becomes one of
these commands over the same schema. The handler's rejection rules do not
change; only who calls them does.

### State

A run's state lives in three places, none of them a transcript:

- `.agent-fox/run/<id>/`: the envelope, the events, the ledger, the baseline
  result and its log, the rendered briefs, the check logs a brief refers to
  by path. Git ignores it through its own `.gitignore` (*The run directory*,
  below).
- `tasks.json`: the task states, written only by `af task land` and
  `af task start`, committed beside the work.
- git: the branch, the commits with their trailers, the parked `wip:`
  commit. "Where did it stop" is answered by `git log`.

A brain that dies mid-task loses nothing `af` had measured. The next brain,
or the next run, continues from `af task brief`.

### The run directory

The shell creates `<dir>/.agent-fox/run/<id>/` for a command that declares
one, before the command runs, under `--dry-run` too. `<dir>` is `--dir`, made
absolute; `<id>` is `YYYYMMDD_HHMMSS_<6 lowercase hex>`, the UTC time and
three random bytes. `<dir>/.agent-fox/.gitignore` holds `*` and is created,
exclusively, when it is missing: Git ignores the whole directory, and the
repository's own `.gitignore` is never edited. The shell puts these files in
the run directory, and a command adds its own beside them:

| File | Written by | Content |
|---|---|---|
| `envelope.json` | the shell | the envelope, byte for byte what stdout got |
| `events.jsonl` | the shell and the command | JSON lines `{seq, ts, run_id, type, data}`, `seq` from 1, each flushed when written; `run_start` first, `run_end` last |
| `ledger.jsonl` | the command, through `RunDir.Record` | append-only JSON lines `{seq, ts, kind, data}`, `seq` from 1; the shell records nothing in it |
| `report.md` | the command, through `RunDir.WriteReport` | a report, listed in the envelope as an artifact |
| `panic.txt` | the shell | the stack of a panic of the command |
| `brief.<attempt>.md`, `hook.json` | `brain.Execute` | the brief a brain was given, and the hook configuration placed with it |
| `submission.json` | `brain.Execute` | the accepted submission, copied from the box once it is validated again |
| `transcript.<attempt>.jsonl` | an in-process brain | its tool calls, `{seq, tool, args, decision, reason, ok, result}` with `args` and `result` cut to 4096 bytes; af never reads it |
| `brain-panic.<attempt>.txt` | `brain.Execute` | the stack of a brain that panicked |

The box has run files of its own under `.agent-fox/run/<id>/`: `brief.md`,
`run.json`, `hook.json` and `submission.json`, placed by `af` and not by the
brain. The ledger of a run that has a brain holds the entries `box_open`,
`box_sync`, `box_export`, `box_close`, `brief_placed`, `brain_start`,
`brain_end` and the `submission_*` ones, which [cli.md](cli.md#the-ledger)
lists.

`toolio.OpenRunDir(dir, id)` opens an existing run for the commands that
carry one on (`submit`, `land`); an id that is not of the form of a run id is
refused before any path is made of it. The briefs, the baseline and the
submission that later specs keep in the run directory join these files.
[`docs/cli.md`](cli.md) has the formats in full.

### Ring 1, as built

Six of these commands exist: `af lang detect`, `af gate run`, `af gate
compare`, `af revert-check`, `af guard check` and `af map`. (`af guard check`
is listed with ring 2 above, for the hook it serves; as built it is a fact
command like the others: it reads one payload and writes nothing.) Each is a
file in `cmd/af` over a package that knows nothing of the shell and returns a
plain result, so that the loops of later specs call the same code in process:

| Package | Commands | What it holds |
|---|---|---|
| `internal/project` | `lang detect` | The languages, the gate table (`.agent-fox/gate.json`, else a Makefile with a `check` or `test` target, else the manifests), the suite forms, the programs, and the classification of a path as a test. |
| `internal/checks` | `gate run`, `gate compare` | The file view and the tree hash; the gate engine, which runs the checks under the reduced or the hermetic runner; the cache keyed by tree hash, table and mode; the labels; the per-check comparison. |
| `internal/conform` | `revert-check` | The revert check: a claimed backup, a revert that never writes the index, the tests on the reverted tree, and a restore that nothing cancels and that does not overwrite what someone else changed. |
| `internal/policy` | `guard check` | The tokenizer, the seven rules, the policy files (`guard.json`) and the decision for a shell or a write tool. |
| `internal/repomap` | `map` | The map at a token budget: a six-level ladder over the file view and the declarations the hands' outline gives. |

One view of the tree is shared. `checks.FileView` is git's own view of `--dir`
(the tracked files and the untracked files that are not ignored, read
NUL-separated, so no name is quoted), or a walk of the directory outside a work
tree, and never `.git/` or `.agent-fox/`. Detection, the tree hash and the map
all read it, so they agree on what the repository is. The tree hash covers
content only, whatever `HEAD` is, so that a gate a brain ran inside a box is a
cache hit for the landing gate outside it.

What `af` keeps for these commands is under `.agent-fox/`, git-ignored by a
`.gitignore` of its own, and never part of the view:

- `.agent-fox/gate/<id>/` holds the logs and `result.json` of a gate run, and
  `.agent-fox/gate/labels.json` maps the labels the loop gives (`baseline`,
  `after`) to those ids. `<id>` is the first 12 hex characters of the SHA-256
  of the tree hash, the canonical JSON of the checks and the runner mode, so the
  same tree and table give the same id on every machine. A result is not
  locked: writes are atomic (a temporary file and a rename) and the last
  writer wins. `labels.json` is written under a lock file, held for a read and
  a write, so that two runs that start together keep both labels.
- `.agent-fox/revert/<id>/` is the backup that `af revert-check` keeps while
  it works: a copy of every file it changes, the index as it was, and a
  manifest. The directory is made exclusively and is the claim on the
  checkout: a check that finds another beside its own backs off. It exists only
  during the check. A directory left there means a check was killed, the tree
  could not be put back, or a path was changed while the checks ran (what it
  held is in `changed/`), and the next check refuses until the tree has been
  looked at.
- `.agent-fox/gate.json` and `.agent-fox/guard.json` are the files a loop
  writes to pin the gate table and the guard policy
  ([configuration](configuration.md)).

Three properties hold, and tests hold them. A result is a fact of the tree and
the table and not of the machine: availability is reported and never changes
which checks are selected or the key. A revert check gives back the tree hash
and the bytes of `.git/index` in every outcome the shell lets it finish:
a result, an error, a signal (SIGINT, SIGTERM or SIGHUP), the timeout and a
panic, with ten minutes for the restore after a signal or the deadline. A
process that is killed outright, or abandoned after those ten minutes, leaves
its backup, and the next check refuses until someone has looked at it. A path
that something else changed meanwhile is kept in the backup and reported, not
overwritten. Af's own listing of changes refreshes a copy of the index and not
the real one; the checks run on the real one, and if they rewrote it the
restore writes the bytes it had back.
The hook fails closed: with `--host claude` an error, a panic or a late answer
is a denial, because the exit code of a failed command is not one the host acts
on. [development.md](development.md) has the layout and the conventions in
full.

## The three bindings

From weakest to strongest. The first must work on its own, because it is
the one every host has.

**Shell.** Every `af` command takes its input as one positional argument (an
issue URL, a file, text, or `-` for stdin), writes one envelope on stdout, and
uses the exit-code table of [`docs/cli.md`](cli.md). Any brain with a shell
tool can drive it.

**MCP.** `af serve` exposes the same commands as typed tools over stdio, on
the official MCP Go SDK. Claude Code attaches it with one `claude mcp add`
line; Pi binds MCP servers through an extension; the AgentKit driver binds
it through the hands' pool, or calls `af` in-process. The brain then gets
schemas and structured results. The MCP layer is a wrapper over the
commands, never a second implementation.

**The policy hook.** The host's interception point calls `af guard check`,
which reads the host's hook payload, applies the hands' `guard.Check` plus
the run's rules (the spec directory is protected, mutating git subcommands
refused, the suite command refused and `af gate run` named instead), and
answers allow or deny. In Claude Code that is a `PreToolUse` hook in the
settings file `af run start` writes; in Pi an extension on the tool-call
event; in the AgentKit driver the `BeforeToolCall` interceptor.

The hook is prevention and it is best-effort: a host may not have one.
**Detection at the gate is the invariant.** `af task land` refuses a commit
on the branch that does not carry the program's trailer, reverts any change
under the spec directory, flags any file outside `touches`, and re-measures
the tree itself. Nothing the brain did unobserved can reach `tasks.json`,
the trailer, or the forge.

## The box

The hands' `sandbox` package is the box. A `Provider` (`podman` for a
container or a micro-VM, `landlock` for a confined child process on Linux,
`gateway` for a sandbox on another host, `local` for tests) creates one
from a `Spec`: an image, an explicit environment, a network mode, resource
bounds, a TTL. `Sync` fills its workspace from a host directory over the
rsync protocol, or from a public repository in an init step; `Tools`
returns `tools.All` as proxies whose every call runs inside; `Export`
brings the tree back out, never `.git`; `Close` destroys it. The box has
no host mount, inherits no environment, holds none of the provider's
credentials, and inside it the hands' agent confines itself with Landlock
to the workspace, a spill directory and a private `HOME`.

The sections above say nothing about where the tree is. With the box they
say this:

**The tree lives in the box. Git and the credentials live on the host.
Ring 1 runs where the tree is, ring 2 runs where the credentials are, and
the brain runs wherever it brings its hands.**

### Where the brain sits

A brain either borrows the hands or brings its own.

| | Borrowed hands | Own hands |
|---|---|---|
| Brains | the AgentKit driver; Claude Code with its native tools denied and `af serve` as its only tools | Claude Code, Codex, Pi, OpenCode; a person at a shell |
| The brain runs | on the host, over `sb.Tools` | inside the box, from the image |
| What it sees | tool results | the tree, a shell, its own tools |
| What the box holds | no credential at all | the model vendor's key, nothing else |
| Network the box needs | none | the vendor; a package registry if the gate fetches |
| How it is started | `agentkit.New` over the proxy tools, or the MCP server over them | one `execute` call: the headless CLI, the brief as its prompt, its JSON output to a file in `$HOME` |

Borrowed hands is the box as AgentKit
[PRD 11](https://github.com/agent-fox-dev/agentkit-go/blob/main/docs/prd/11-run-tools-in-an-isolated-sandbox.md)
designed it: the model, the driver and the loop on the host, only tool
results crossing the boundary. It is the right placement for the driver.
It is possible for Claude Code (an allowlist of `mcp__af__*` and nothing
else, an empty working directory), but it takes from Claude Code the tools
that are the reason to run it. So the vendor CLIs go inside, with the tools
image as `Spec.Image` and the key in `Spec.Env`. The price is the network:
on Podman the choice is nothing or everything (PRD 11 §9), so a brain in a
box on a developer's machine has the whole network, and only a gateway
whose backend filters egress narrows it to the vendor. The brain's own hook
(`af guard check` is in the image) is still installed; the box is what
makes its best-effort nature acceptable.

### What the box makes physical

The three things the hook could only ask for, the box enforces:

- **The brain never pushes.** There is no forge token and no hub token in
  the box, only the vendor's. `af forge`, `af run land` and the push are
  host commands; inside the box they have nothing to authenticate with.
- **The brain never makes a commit that counts.** `.git` goes in with the
  sync, so `git log` and `git diff` inside are true, and never comes out:
  `Export` drops it. A commit the brain makes inside stays inside. `af task
  land` exports the tree, classifies it against the baseline, makes the
  commit on the host's clone, and the next sync carries that commit back
  in, so the box's history is the host's again. ADR 09's foreign-commit
  check becomes a tree comparison: there is no branch inside for a foreign
  commit to be on.
- **The brain never reaches the spec state.** `tasks.json` is written by
  the host after the landing; the copy in the box is overwritten by the
  next sync. The protected-path revert still runs, on the exported tree,
  and still reports.

The run directory stays on the host, as *State* says. What the box needs
of it, the rendered brief and the gate's command table, is synced in under
the workspace's ignored `.agent-fox/`; what the brain produces for `af`,
the submission, is a file in the same place, written by `af task submit`
inside after validation and read by the host through `read_file` when the
brain's process ends. The Brain interface's rule holds: the submission
arrives through `af`, never by parsing the brain's output. The gate cache
is keyed by tree hash on both sides, so a gate the brain ran inside is a
hit for the landing gate the host runs through the same `execute`.

### Who holds the box

A `Sandbox` is owned by the process that created it; there is no reconnect
(PRD 11 §9). So the loop holds the box: `af impl` and `af fix` create it at
`run start`, keep it across every task, and close it at `run land` or when
the attempt budget is spent. The ring-1 commands a brain calls from inside
need no handle: they run on the tree they can see. Attended use has no
box: a person in Claude Code on their own checkout is the brain and the
host at once, and the hook is the only boundary, as today. `af serve`
holding a box for an attended session is possible and is not the first
release.

### The image

`quay.io/agentfox/tools` already is the sandbox base plus `af`, the three
tools, Pi and Claude Code. It gains the overlay line
(`COPY --from=quay.io/agentfox/agentkit-sandboxd:X.Y.Z …`), which puts the
hands' agent at `AgentPath`, and Codex and OpenCode beside the other two.
That image is `Spec.Image` for every brain with its own hands; the hands'
base image suffices for borrowed hands.

### What the hands lack for this

Two additions to the sandbox, neither needed for the first run:

- **A streaming `Exec`.** A headless brain is one process for twenty
  minutes; `execute` returns when it ends, with the output capped and
  spilled. The first release reads the brain's own JSON output from its
  file afterwards; a streaming RPC would carry its progress events to the
  host as they happen.
- **A credential for the init step.** `GitRepo` is public only; a private
  clone happens on the host and is synced in. For hub, whose git server is
  private by construction, the gateway can hold the workspace-scoped token
  and use it in the init step, one hop of trust as PRD 11 §5 has it; the
  sandbox still never sees it.

## A task, end to end

The `impl` replacement, with Claude Code as the brain:

1. `af run start 16` validates the package, detects the language, creates
   the branch, snapshots untracked files, creates the box from the brain's
   `Spec`, syncs the tree in, runs the baseline gate inside, and writes
   the run directory and a settings file with the hook and the MCP server.
2. `af task brief 16/3` renders the task's scoped spec, the survey's
   decisions, the repository map, the gate commands with their log paths,
   and the previous attempt's failure if there was one, into one document.
   The run-constant part is byte-identical for every task so the brain's
   cache prefix holds; the task block follows it.
3. The brain works. **Attended:** a person is in Claude Code and a short
   skill says "read the brief, implement, submit". **Unattended:**
   `af impl 16 --brain claude` runs the brain headless inside the box with
   the brief as the prompt, the allowlist, the settings file, and reads
   cost from the brain's own output file.
4. `af task submit 16/3` validates the report and rejects naming every
   problem at once.
5. `af task land 16/3` measures inside, exports the tree, and either
   commits on the host and syncs the commit back in, or refuses with a
   structured reason the brain reads on its next turn. The attempt budget
   is `af`'s. A spent budget parks the work as `wip:` and returns the
   checkout to the base branch, as today.
6. After the last task, `af review brief` emits the packet and
   `af review submit` validates the answers. The reviewer is a fresh
   context by construction: a Claude Code subagent with read-only tools, a
   second headless run, or the AgentKit driver.
7. `af run land` runs the hermetic gate, closes the box, pushes, opens the
   pull request from facts, as a draft when findings were neither fixed
   nor declared, and, under hub, registers the branch as a carry patch.

`fix` is the same shape with three briefs (analyse, implement, review)
instead of one per task, and `af fix land` deciding `Closes` versus `Refs`
from the revert check.

The survey phase stays a brief-and-submit pair before step 2 and is the one
place a run may stop to ask; a blocker exits 3 with no branch created.

## The brain interface

One Go interface in `af`, so that `impl` and `fix` are a loop over briefs
and nothing in them names a vendor:

```go
// Brain runs one brief to a submission. It must not depend on anything
// inside the brain's transcript: the submission arrives through
// `af task submit` from inside the session, never by parsing output.
type Brain interface {
    Run(ctx context.Context, b Brief) (Outcome, error)
}

type Brief struct {
    Document string   // the rendered markdown
    Tools    []string // the allowlist the host is asked to enforce
    Hook     HookConfig
    Box      sandbox.Sandbox // the tree; nil only when attended, on the host's own checkout
    MaxCostUSD float64
    Timeout  time.Duration
}

type Outcome struct {
    Submitted  bool    // af received a submission during the run
    CostUSD    float64 // as the brain reports it
    Transcript string  // a path, when the brain keeps one; never read by af
}
```

Three implementations:

| Brain | Where | Mechanism | Notes |
|---|---|---|---|
| `claude` | in the box | the Claude Code CLI headless, started by one `execute`, with the allowlist, the settings file and JSON output for cost | the default for unattended runs |
| `codex`, `opencode` | in the box | the same shape as `claude` | *rows to be written once their non-interactive surfaces are confirmed* |
| `pi` | in the box | Pi's headless mode where it exists; otherwise attended only | *to be written once Pi's non-interactive surface is confirmed* |
| `agentkit` | on the host | the PRD 10 driver in-process over `Box.Tools`, `guard.Check` as the interceptor, and optionally code mode over the `af` fact tools | the one brain `af` fully controls; the one that needs no vendor CLI and no network in the box; the one that can bind code mode |

The interface is honest only if the three agree on the contract above. A
test runs the same brief through each available brain against the scripted
provider and checks that `af` saw the same submission.

### The brain interface, as built

`internal/brain`, `internal/box` and `internal/submission` hold the first two
brains (`agentkit` and `scripted`) and what they run on. The sketch above is
what was built, with these differences:

```go
type Brain interface {
    Run(ctx context.Context, b Brief) (Outcome, error)
}

type Brief struct {
    RunID, Attempt                // the run, and the attempt, from 1
    Kind           submission.Kind // what the brain submits and how it is validated
    Prefix, Task   string         // Document() is Prefix + Task; Prefix is byte-identical across attempts
    Tools          []string       // the allowlist of box tool names
    Hook           HookConfig     // {run_id, protected_paths, allow_programs}; always protects .agent-fox/
    Box            *box.Session   // never nil in this release
    Env            map[string]string // for a brain started inside the box; empty for the in-process brains
    MaxCostUSD     float64
    MaxTurns       int
    Timeout        time.Duration
    TranscriptPath string         // a host path the brain may write a transcript to
    Observe        func(ToolCall) // told of every tool call
}

type Outcome struct {
    Submitted    bool   // the brain's claim; af decides from the file
    StopReason   string // submitted | ended | budget | turns | error
    CostUSD      float64
    CostSource   string // measured | reported | none
    Turns, InputTokens, OutputTokens int
    Transcript   string // a path; never read by af
}
```

`Brief.Box` is a `*box.Session` and not the hands' `sandbox.Sandbox`, because a
brain needs the run-file writes, the read view and the tools together, and the
session keeps the sandbox's API behind one adapter file. `Outcome` is used for
the envelope's `usage` and for one warning, `submission_claim_mismatch`:
whether a submission exists is decided from the file alone.

**Choosing.** `brain.Registry` holds `Entry{Name, CostSource, BoxEnv, New}`; the
default has `agentkit` (cost source `measured`) and `scripted` (`none`).
`BoxEnv` names the host variables that reach the box of that brain, and nothing
else does: it is empty for both. `box` has a registry of providers of its own,
with `local` in it. `brain.Flags` is the shared set of flags ([cli.md](cli.md#brains-and-boxes)).

**Running.** `brain.Execute(ctx, Request{Session, Name, Brain, Brief, RunDir,
Report, Tally, Heartbeat, Rand})` runs one brief in a held box
(`box.With` opens it and closes it on every path). Before the brain it draws a
16-hex nonce and writes into the box `.agent-fox/.gitignore`, `brief.md`,
`run.json` (`{run_id, kind, attempt, nonce}`) and `hook.json`, and into the run
directory `brief.<attempt>.md` and `hook.json`; then it runs the brain, adds its
cost to the `Tally` (a failed attempt and a panic included) and decides from the
box's `submission.json` whether there is a submission (see [cli.md](cli.md#brains-and-boxes)).
The nonce makes a stale file, left by an earlier attempt or by a sync, count as
no submission whatever the sync does with `.agent-fox/`.

**The submission file** is written by one function, `submission.Submit`, which
validates before it writes and never leaves a refused one: inside a box a brain
reaches it through its terminating tool (`agentkit`), a step of its script
(`scripted`), or `af <agent> submit` (a brain with its own hands).

**The tool gate.** Both in-process brains send every call through one gate: the
brief's allowlist, then the ring-1 policy (`policy.DecideAt`) with the brief's
hook and the suite of the checkout, which is the same rule set that `af guard
check` gives a hook. The `agentkit` brain uses it as the driver's `Guard` and
`After`.

**Placement.** In this release the `agentkit` brain always runs on the host,
over the box's tools, which is the only placement that means anything with the
`local` provider; running the driver inside a box with one `execute`, and the
`claude` brain with its settings file and hook, belong to the scope that adds
the other box providers.

## The hub

[hub](https://github.com/agent-fox-dev/hub) owns what one machine cannot:
workspaces, identity and scoped tokens, secrets, a git server, a merge
queue, carry patches, a job queue and, per
[the tasks draft](drafts/add_tasks_to_the_hub.md), tasks. `af` does not
depend on it. Hub depends on `af`: a task's program is `af impl` or
`af fix`, and what hub adds is the scheduling before the run and the
carrying after it.

### Topology

```
 hub (control plane)   tasks · workspaces · secrets · the git server · patches · the merge queue
        |  a task.attempt job: the program, its input, a workspace-scoped token, the vendor key
        v
 worker                af impl <spec> --brain claude      ring 2, git, the tokens
        |  gateway.Dial(url, token): Create, Sync, Tools, Export, Close
        v
 agentkit-gateway      podman on the worker's host, or pods, or elsewhere
        |
        v
 box                   the tree · agentkit-sandboxd · the brain · af (ring 1)
```

The tasks draft puts an `af-runner` inside the image as the process that
talks to hub. This document moves it out: the worker runs `af impl`, which
holds the token and the box, and the box holds the brain and the vendor
key. That is the same split as on a developer's machine, with the gateway
in place of a local Podman, and it is why neither the Brain interface nor
the toolbelt changes between the two. Hub's sandbox concept is the hands'
gateway: hub deploys one, or points at one, and does not build its own.

### Carry patches

Hub's carry-patch workspace tracks an upstream, holds an ordered list of
patch branches, rebuilds an integration branch (`deploy`) as upstream plus
the patches in order, detects when a patch has merged upstream, squash
merges included, remembers conflict resolutions with rerere, and marks a
patch `conflict` with its files when a rebuild cannot apply it. That is the
lifecycle of an agent's output. `af` uses it in four places:

1. **A landed run is a patch.** `af run land` pushes the branch and opens
   the pull request as today. The workspace is forge-authoritative
   (`PATCH_BRANCH_SOURCE=origin`: `origin` is the repository the branch was
   pushed to, `upstream` the repository whose default branch the spec was
   written against, the same URL when the team owns it), so hub's next
   sync sees the branch; `af run land` registers it with
   `POST /patches {branch, upstream_pr_url, description}` at the position
   below. From then on hub carries it: every upstream advance rebuilds
   `deploy` with the branch on top, and when the pull request merges, hub
   retires the patch and compacts the list. The branch is never rebased by
   hand, and `deploy` is always "main plus every open agent change", which
   is what a staging deployment wants.
2. **Spec order is patch order.** A spec's dependencies give the patch its
   position: after the patches of the specs it depends on. Today `af run
   start` refuses an unmet upstream; with hub it has a second answer: when
   the upstream spec is landed and still an active patch, the run branches
   from `deploy` at the position after it and is registered there. The
   rebase strategy's `--cherry-pick` skips the commits the two branches
   share, so the stack stays one commit per change.
3. **A conflict is a fix.** A rebuild that leaves a patch in `conflict`
   names the files. Hub creates a `fix` task from it (the tasks draft's
   `next` rules) whose input is the patch branch, the upstream base and
   `conflict_files`; the brain resolves it in a box; `af fix land` pushes
   the rebased branch; sync picks it up and the next rebuild applies it.
   The `Closes`-or-`Refs` decision of `af fix land` has its analogue: the
   patch leaves `conflict` or it does not.
4. **The stack is gated.** Hub rebuilds mechanically and runs nothing.
   Each patch was gated alone by `af run land`'s hermetic gate; two green
   patches can still be red together. A rebuild that completes enqueues a
   task that runs `af gate run` on `deploy` in a box and records the
   verdict on the workspace; a red stack of green patches is a `fix` task
   of its own. This is the one new thing hub has to learn about `af`; the
   rest is the patch API it has.

Hub-authoritative workspaces (`PATCH_BRANCH_SOURCE=hub`: patches pushed to
hub's git server and mirrored to the forge) are the other configuration.
They fit a worker that may reach hub but not the forge. They are not the
first release, because the forge-authoritative one needs nothing new on
either side.

### The merge queue

Hub's merge queue merges a branch into a target with its own
serialization. `af run land` does not use it: the pull request is the
review surface and the forge merges. A workspace that merges agent changes
without a person is a hub policy, decided per workspace, and `af` is not
involved beyond landing the branch.

## What moves where

| Today | In this architecture |
|---|---|
| `codeimpl` and `codefix` phase loops | `af impl` and `af fix`: a loop over briefs calling a `Brain` |
| `internal/agentrun` (phase runner, guard, policy, scratch) | gone; `guard.Check` from the hands behind `af guard check`; scratch is the run directory |
| the `submit_*` tools and their handlers | `af ... submit` commands over the same schemas |
| `internal/checks`, `internal/conform`, `internal/gitx`, the untracked snapshots, parking, the PR body | `af`'s effect layer, on the hands' runner and outline |
| `afspec`, `issuex`, `internal/project` (later the language profiles) | unchanged, inside `af`; `issuex` is imported from `github.com/agent-fox-dev/aflib/issuex` |
| prompts as Go strings | the briefs, as templated documents rendered by `af task brief` and `af review brief` |
| `issuetriage` | unchanged for now; it is single-phase and the cost of the current shape is small. *To be decided.* |
| the `af-impl` skill | shrinks to the attended loop over `af` commands |
| `cmd/impl`, `cmd/fix` | thin entry points that become `af impl`, `af fix`; the envelope, flags and exit codes are those of `docs/cli.md`, whose envelope is a new shape (`docs/errata/05_old_envelope_fields.md`) |
| `containers/tools` | `Spec.Image` for a brain with its own hands: the overlay line, Codex and OpenCode added |

## Open decisions

*To be resolved before the first spec is written from this document.*

- **How much the brain may verify itself.** Claude Code will run the test
  suite on its own. Either the hook refuses the suite command and names
  `af gate run`, which feeds the gate cache so the landing gate is a cache
  hit (PRD 13 §6.3), or the extra runs are accepted. The first is the
  design; the second is the fallback where no hook exists.
- **Whether the AgentKit driver stays a first-class brain.** It is the only
  brain `af` fully controls and the only one that can bind code mode over
  the fact tools. If the Claude Code brain is the one that gets run, the
  driver can shrink to `af`'s test double.
- **Where the run brief's stable prefix comes from in each brain.** Claude
  Code owns its system prompt; the brief is the first user message. The
  AgentKit driver puts it in `Config.Prefix` with a cache breakpoint. Pi:
  *to be written.*
- **Whether `triage` moves to the same shape.** It is one phase and
  already fits the current design; moving it buys uniformity and little
  else.
- **What `af serve` exposes beyond the toolbelt.** The hands' navigation
  tools (`file_outline`, `find_symbol`, `find_references`, `code_search`)
  could be served to a brain that lacks them. Pi and Claude Code have their
  own; serving duplicates them. *To be decided per brain.*
- **The network of a box with a brain inside.** On Podman it is all or
  nothing, and a brain needs its vendor, so on a developer's machine the
  box has the whole network. Acceptable for the first release; a gateway
  whose backend filters egress is the answer where it is not. The
  alternative, Claude Code on the host with borrowed hands, keeps the box
  at `NetworkNone` and loses the brain's own tools. *Measure both.*
- **`af-runner` inside the image or `af impl` on the worker.** The tasks
  draft has the first; *The hub* argues the second, because it keeps the
  hub token out of the box and the toolbelt identical on a laptop and
  under hub. *To be reconciled in the tasks draft.*
- **One repository as both `origin` and `upstream`.** The carry-patch
  workspace assumes a fork and an upstream. A team that owns its
  repository has one URL for both; whether hub accepts that, and what
  sync does with it, is hub's to confirm.
- **Who gates the stack.** Hub, on rebuild completion, as the fourth use
  above says; or `af`, polling patch status. Hub, unless the tasks draft
  finds a reason not to.

## What this replaces

- [ADR 02](adr/02-build-the-spec-pipeline-on-agentkit.md), ADR 03 and
  ADR 04 stay as the record of why the facts moved into Go. Their "a phase
  is an agent" structure is what this document retires.
- [PRD 13](prds/13-rebuild-fix-on-a-shared-change-engine.md) and
  [PRD 14](prds/14-rebuild-impl-on-the-shared-change-engine.md): the
  engine, the ledger, the gate cache, the language profiles, the prompt
  documents and the token discipline survive as `af`. The phases built on
  them do not; the brain owns the loop.
- AgentKit [PRD 06](https://github.com/agent-fox-dev/agentkit-go/blob/main/docs/prd/06-support-multi-phase-coding-pipelines.md)
  is withdrawn there; what agent-fox needed from it is in PRD 10's driver.
- Specs 19 to 23 under `.specs/` were written from PRDs 13 and 14 and are
  superseded by whatever specs are generated from this document.

## Sections to be written

- The reference of each `af` command, in `docs/cli.md`, as the commands
  land. The shell's contract (the envelope, the exit codes, `--dry-run`,
  `--context`, `--schema`) is documented there already.
- The host settings `af run start` writes for each brain.
- The `Spec` `af run start` builds for each brain: image, environment,
  network, resources, TTL, and `RequireLandlock`.
- The tools image after the overlay line: what is pinned, and how the
  hands' version and `af`'s are kept in step.
- Hub's task templates for `impl`, `fix`, the stack gate and the conflict
  fix, once the tasks draft is reconciled with *The hub*.
- The measured comparison this design has to win: tokens per landed task
  and the share of `impl` runs whose pull request merges without a human
  fix, against the numbers in PRD 14's intent.
