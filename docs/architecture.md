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
                    af fix brief/run/submit/land · af forge ...
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
| `af fix brief <input>`, `af fix run`, `af fix submit`, `af fix land` | the same for a fix: preflight and baseline; the brief; a round of a brain whose tree is applied to the checkout; the report; gate, revert check, `Closes` or `Refs`, commit, push, PR (see [Fix, as built](#fix-as-built)) | a dirty tree, no gate, a foreign commit on the branch, a change that regressed |
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
| `submission.json` | `brain.Execute` | the accepted submission, written from what the host checked again in the box's file, and only if there is one: an attempt that has none leaves no such file |
| `guard.json`, `claude-settings.json` | the `claude` brain | the policy file and the settings (the `PreToolUse` hook) it ran with, copied from the box |
| `claude.<attempt>.json`, `claude.<attempt>.stderr`, `agentkit.<attempt>.json`, `agentkit.<attempt>.stderr` | a brain that runs as a process | what the process printed (the JSON up to 1 MiB, the last 64 KiB of the errors), copied from the box and listed as `brain_output` artifacts; nothing in them decides whether there is a submission |
| `transcript.<attempt>.jsonl` | an in-process brain | its tool calls that reached the gate, `{seq, tool, args, decision, reason, ok, result}` with `args` and `result` cut to 4096 bytes, and the steps of a script that were skipped, `{seq, skipped, step, what}`; af never reads it |
| `brain-panic.<attempt>.txt` | `brain.Execute` | the stack of a brain that panicked |

The box has run files of its own under `.agent-fox/run/<id>/`: `brief.md`,
`run.json` (which also holds `prefix_bytes`, the length of the brief's prefix),
`hook.json` and `submission.json`, placed by `af` and not by the brain; for `claude`
also `guard.json` and `claude-settings.json`. The ledger of a run that has a brain holds the entries `box_open`,
`box_sync`, `box_export`, `box_close`, `brief_placed`, `brain_start`,
`brain_end` and the `submission_*` ones, and for a brain that runs as a process
`box_probe`, `brain_process` and `claude_result`; under `none` a fix run also holds
`foreign_commits_reset`. [cli.md](cli.md#the-ledger) lists them.

`toolio.OpenRunDir(dir, id)` opens an existing run for the commands that
carry one on (`submit`, `land`); an id that is not of the form of a run id is
refused before any path is made of it. The baseline and the other files that
later specs keep in the run directory join these files.
[`docs/cli.md`](cli.md) has the formats in full.

### Ring 1, as built

Six of these commands exist: `af lang detect`, `af gate run`, `af gate
compare`, `af revert-check`, `af guard check` and `af map`. (`af guard check`
is listed with ring 2 above, for the hook it serves; as built it is a fact
command like the others: it reads one payload and writes nothing.) Each is a
file in `internal/afcmd` over a package that knows nothing of the shell and returns a
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
  commit to be on. (Under the `none` provider there is no box, so there is
  such a branch: the checkout's own. `af` records HEAD before the brain runs,
  takes the commits it made off the branch afterwards, and refuses a checkout
  that was moved; see *Placement, as built*.)
- **The brain never reaches the spec state.** `tasks.json` is written by
  the host after the landing; the copy in the box is overwritten by the
  next sync. The protected-path revert still runs, on the exported tree,
  and still reports.

The run directory stays on the host, as *State* says. What the box needs
of it, the rendered brief and the gate's command table, is synced in under
the workspace's ignored `.agent-fox/`; what the brain produces for `af`,
the submission, is a file in the same place, written by `af task submit`
inside after validation and read by the host from the box when the
brain's process ends (as built, with the list tool and a single-file export,
not `read_file`: see *The brain interface, as built*). The Brain interface's rule holds: the submission
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

`internal/brain`, `internal/box` and `internal/submission` hold the three brains
(`agentkit`, `scripted` and `claude`) and what they run on. The sketch above is
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
    Hook           HookConfig     // {run_id, protected_paths, allow_programs}; the one Execute hands the brain protects .agent-fox/
    Box            *box.Session   // never nil in this release
    Env            map[string]string // for a brain started inside the box; empty for the in-process brains
    MaxCostUSD     float64
    MaxTurns       int
    Timeout        time.Duration
    TranscriptPath string         // a host path the brain may write a transcript to
    Observe        func(ToolCall) // told of every tool call that reaches the gate
    Warn           func(code, message string) // a warning of the run, such as a transcript that cannot be written
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
the envelope's `usage`, for one warning, `submission_claim_mismatch`, and to tell
which bound a run without a submission reached: whether a submission exists is
decided from the file alone. `box.Session.FS` is read-only and built on the
box's own tools: a directory is listed with the list tool and a file is
exported, one file at a time, because the read tool shows lines, caps them, and
takes the box down on a file that is not UTF-8.

**Choosing.** `brain.Registry` holds `Entry{Name, CostSource, Credentials, OwnHands,
DriverInBox, CommandRoute, New}`; the default has `agentkit` (cost source `measured`;
`DriverInBox`), `scripted` (`none`) and `claude` (`reported`; `OwnHands`,
`CommandRoute`). `Credentials` names the host variables that reach the box of a brain
that runs as a process, and nothing else does: `ANTHROPIC_API_KEY` for `agentkit` and
`claude`, and none ever for a brain on the host. `box` has a registry of providers of
its own, `Entry{Name, Unconfined, InPlace, ByName, SuppliesCredentials, Remote, New, Build,
Explain}`, with `landlock`, `podman`, `local` and `none` in it; a provider isolates unless it
is `Unconfined`. `ByName` says the programs of the box are called by name from its image
(`podman`, `gateway`), `SuppliesCredentials` that the box has the credentials of its brain in an
environment of its own, and `Remote` that a call which cannot reach the box is an outage to try
again. The `gateway` provider (`box.GatewayEntry(url, token)`) is made for one attempt from the
lease of a sandbox and is held only in the registry of that attempt, never in `box.Default`, so
that `--sandbox gateway` is a usage error: it opens the box on hub's gateway with the lease's
token and no environment, labelled `af.run_id`, `af.tool`, `af.job` and `af.attempt`, and a
gateway that cannot be reached or that refuses the box is a sandbox failure to try again.
`brain.Flags` is the shared set of flags ([cli.md](cli.md#brains-and-boxes)).

**Running.** `brain.Execute(ctx, Request{Session, Name, Brain, Brief, RunDir,
Report, Tally, Heartbeat, Rand, Nonce})` runs one brief in a held box (the caller opens
it with `box.With`, which closes it on every path). Before the brain it draws a
16-hex nonce and writes into the box `.agent-fox/.gitignore`, `brief.md`,
`run.json` (`{run_id, kind, attempt, nonce}`) and `hook.json`, and into the run
directory `brief.<attempt>.md` and `hook.json`; then it runs the brain, adds its
cost to the `Tally` (a failed attempt and a panic included) and decides from the
box's `submission.json` whether there is a submission, also after a panic (see
[cli.md](cli.md#brains-and-boxes)). A run is one `Execute` at a time in a box.
The nonce makes a stale file, left by an earlier attempt or by a sync, count as
no submission whatever the sync does with `.agent-fox/`.

**The submission file** is written by one function, `submission.Submit`, which
validates before it writes and never leaves a refused one: inside a box a brain
reaches it through its terminating tool (`agentkit`), a step of its script
(`scripted`), or `af <agent> submit` (a brain with its own hands). A brain whose
process can write the file some other way (the `local` box is not isolated) gains
nothing by it: the host reads the file again with `submission.Check`, and keeps
the file it writes itself from what it checked.

**The tool gate.** Both in-process brains send every call through one gate: the
brief's allowlist, then the ring-1 policy (`policy.DecideAt`) with the brief's
hook and the suite of the checkout, which are the rules of `af guard check`
configured by the brief's hook (the gate does not read the checkout's
`guard.json` beyond its suite; a `guard.json` that cannot be used is a `config`
failure). The paths of a write are normalised as the box's tools do before they
are judged. The `agentkit` brain uses it as the driver's `Guard` and `After`; a
call the driver answers itself (a tool that does not exist, arguments that do
not fit) never reaches it. The policy is prevention and not a boundary, as its
package says.

**Placement, as built.** `brain.PlacementOf(brain, provider, borrowedHands)` is the one
function that says where a brain runs, and `brain.BoxRequest` the one that turns it,
with the provider and the tools of the agent's brief, into the request for the box:

| Brain | `landlock`, `podman` | `local` | `none` |
|---|---|---|---|
| `scripted` | host | host | host |
| `agentkit` | box; with `--borrowed-hands`, host | host | host |
| `claude` | box | box | checkout |

*Host* is `af`'s process over `Session.Tools`; *box* is a process started in the box with
`Session.Exec`; *checkout* is such a process on the host, in the checkout, which is the
tree under `none`. The default provider is `AF_SANDBOX`, else `landlock` on Linux and
`podman` elsewhere, and it never falls back to `local` or `none`.

**The `Spec` of a box, for each brain.** `Spec.Env` holds credentials and nothing else:
the host's `ANTHROPIC_API_KEY` for a brain in the box or at the checkout, and nothing for a
brain on the host. `Spec.Network` is `none` for a brain on the host whose brief has no tool
that runs a command (all of `af triage`), and `open` for the rest, since a process in the
box needs its vendor and Podman filters nothing narrower; `host` is what the ledger and
the `fix` result record for `local` and `none`, where nothing is enforced.
`Spec.Image` is `AF_SANDBOX_IMAGE`, else `quay.io/agentfox/tools:latest`, for `podman` only;
`Spec.TTL` is the time left on the command's deadline and ten minutes; `RequireLandlock` is
set for `landlock`, which also refuses a host with a Landlock ABI below 4, the first with
rules for the network, since a box that cannot confine the network it was asked to would
break the guarantee of borrowed hands. The labels are `af.run_id` and `af.tool`. The
ledger entry `box_open {provider, image, network, placement}` and the `box` of the `fix`
result say what was built.

**The host settings, for each brain.** `scripted` and the `agentkit` driver on the host need
none beyond the files `Execute` places. For a process in a box:

- `claude` gets, for each attempt, `.agent-fox/run/<id>/guard.json` (spec 06's policy file,
  generated from the brief's hook: `protected_paths` and `allow_programs`, no `suite`) and
  `claude-settings.json`, whose one `PreToolUse` hook, for every tool, is `af guard check
  --host claude --dir <root> --policy <root>/.agent-fox/run/<id>/guard.json` with a timeout
  of 30 seconds; the brief is its prompt on stdin, and the command line has
  `--permission-mode dontAsk`, `--strict-mcp-config`, an allowlist and a denylist made from
  one table of the box's tools, `--append-system-prompt` with a fixed note, and `--model`
  from `AF_MODEL`. Its cost is read from the JSON it prints, after it has stopped.
- The `agentkit` driver inside `landlock` and `podman` is `af <agent> run --run-id <id>
  --dir <root> …`: the box gets only what `Execute` places, so `run.json` also holds the
  `prefix_bytes` of the brief, and `af <agent> run` takes the brief and the prefix from it
  when the run directory has no state of its own. Its cost, tokens and claim are read from
  the envelope it wrote to `.agent-fox/run/<id>/agentkit.<attempt>.json`.

Both are started by `brain.Process`: a probe once for each box (`af --version`, `claude
--version`, a writable `HOME`; the box remembers what was probed, so that the measuring below and
a brain share it), then one command under `env -i` with an explicit allowlist, the
credentials of `Spec.Env` by reference, its output and errors redirected to files of the run
directory in the box and copied to the host afterwards. In a box that supplies its own
credentials (the gateway's) the references are to the names the brain lists
(`ProcessSpec.Credentials`: `ANTHROPIC_API_KEY`), and no key is needed on the host or sent.

**Measuring in the box.** `afcmd.Config.InBox` makes a fix measure where the brain works: the
box is opened before the detection of the checks and serves the whole attempt, and
`fix.BoxMeasurer` runs `af lang detect`, `af gate run` (the baseline, and the change) and
`af revert-check` in it through `brain.Process`, after syncing the host's tree into the box and
placing the run's gate table there, and reads each result from the envelope the command wrote.
The results of a gate run are copied to the host's `.agent-fox/gate/<id>/`, where the comparison
with the baseline reads them. The file view, the repository map, the path validation, the apply
of the export, the commit and the push stay on the host, and the host's gate and revert check
are not called. The image of the lease's profile has to provide `af`: the first ring-1 command of
a box is preceded by `af --version`, and an `af` that is missing or fails is a `sandbox` failure
that is not retryable and names the profile (`AF_SANDBOX_PROFILE`, or hub's default); another
version or commit is the warning `box_af_version_mismatch`; an envelope of another major version
of the schema, one that cannot be read, or a result that does not decode is the same failure. A
box that is lost while it measures is a `sandbox` failure that is retryable.

**Under `none`, the checkout is the tree.** `Session.InPlace()` is the one question the apply
step asks: it skips the export, and the loop checks the checkout for commits the brain made
(see [cli.md](cli.md#the-steps-of-a-fix-run)). The protected-path revert runs on the checkout,
and the hook and the checks `af` makes afterwards are the boundary, since no box is.

**The tools image.** `containers/tools/Containerfile` builds `quay.io/agentfox/tools` from
this repository's sandbox image (`ARG BASE_IMAGE`): `af` is built from this repository's
build context, with agentkit-go cloned at `ARG AGENTKIT_REF` where the `replace` directive
of `go.mod` expects it, and Claude Code is installed at `ARG CLAUDE_CODE_VERSION`; it keeps
`WORKDIR /workspace` and `USER 1001:0`. `make build-tools-container` tags it
`$(CONTAINER_REGISTRY)/tools:$(VERSION)` and `:latest`, and `make build-containers` builds it
after the sandbox image. An image whose `af` has another version or commit than the running
`af` raises `box_af_version_mismatch` and the run goes on, since the host checks every
submission again.

### Triage, as built

This document left open whether triage moves to the same shape as the other
agent commands. It does: `triage` is a brain run like `fix` and `impl` will be,
with fewer hands. The
reasons are what it shares. It runs in a box over the same file view, submits
through the same protocol (`internal/submission`), is bounded and costed by the
same loop (`brain.Execute`), speaks the same envelope, and keeps the same run
directory and ledger; a person can drive it a step at a time, and a service can
drive the same steps. What it needs of its own is small: a mandate, a skill, a
validator, a body format and a landing.

**Where it lives.**

| Path | What it holds |
|---|---|
| `internal/triage/` | The part with no shell in it: the diagnosis and its validator (`Validate`, which checks every path against the file view and collects every problem), the body (`Render`, `Extract`), the brief (`BuildBrief`), the preflight, the landing on a forge (`Land`), the submission kind (`Kind`) and the tool allowlist. It imports `issuex`, `submission`, `checks`, `repomap`, `agentdir` and the `skills` package, and none of `toolio`, `box` or `brain` itself. |
| `internal/afcmd/triage.go` | The five commands on the shell: the loop, `brief`, `run`, `submit` and `land`. It does what needs the shell, the box and the brains: the run directory and its ledger, the forge client, `box.With` and `brain.Execute`, the envelope. |
| `skills/af-triage` and `skills/skills.go` | The skill that says how to diagnose. The `skills` package embeds every `af-*` entry (`//go:embed af-*`), and `skills.Triage()` is the body that the brief carries, so the skill a person installs with `make install-skills` and the one the brain reads are the same file. |
| `internal/triage/fakeforge/` | The GitHub and GitLab server of the tests, over `httptest`. |

**The loop** is a preflight that spends nothing, the brief, a bounded brain run,
the host's second look at what the brain handed over, and the landing; see
[cli.md](cli.md#triage). The brain has the read-only hands and one terminating
tool, `file_diagnosis`, whose schema is the diagnosis; it never writes, and `af`
does every write after it has checked the diagnosis against the host's tree. A
diagnosis that cites a file that does not exist is refused as the brain's next
turn, with the files that have the same name; a refusal is counted in
`rejections.jsonl` and decides nothing.

**The body of the issue** is the contract between `af triage` and what reads an
issue after it (`af fix`, and `af triage` itself the next time). It is rendered
the same way every time, and the person's report is kept in it:

```
<!-- agent-fox:triage v1 run=<run_id> -->
## Diagnosis … ## Affected files … ## Suggested fix … ## Acceptance Criteria … ## Question
<sub>Diagnosed by agent-fox `af triage` (run <run_id>). The original report is kept below.</sub>

## Original report

<details>
<summary>Original report</summary>

<!-- agent-fox:original-report:begin -->
<the original report, byte for byte>
<!-- agent-fox:original-report:end -->

</details>
```

The first line, the headings and the two markers are what is read back. The
original is what lies between the first begin marker and the newline before the
last end marker, so `Extract(Render(d, s))` is `s` for every string `s`, a second
triage replaces the old diagnosis and keeps the first report, and a body that
`af` did not write is wholly the original. So is one that starts with the marker
and has been changed in a way that would lose words (text after the original
report, a broken first line), with a warning; a body saved with CRLF line endings,
as a browser does, is read as if it had LF endings. No string of a diagnosis may hold the
text `<!-- agent-fox:`, which keeps a diagnosis from forging a marker. When the
body would be longer than the forge allows, the diagnosis goes in one comment and
the issue is left as it is.

**Where it differs from the sketch above.** The loop runs the brain on the host
over a box, which is the only placement the `local` provider means anything for;
`af triage run` is the same brain in the process that a box would execute, over
the directory itself and with no box. The refusals of the diagnosis are written
by a hook of the submission kind (`Kind.OnRefused`), which the submission
protocol gained for this, and `submission.CommandWith` lets a command make its
kind from `--dir`. The shell gained `Run.ForgeFor`, a forge client for a
repository that the command learns at run time (`land` reads it from
`triage.json`). The divergences from the spec, with the code of each, are in
`docs/errata/08_af_triage_divergences.md`.

### Fix, as built

`af fix` is the second agent command, and the first whose brain writes. It has the
shape of triage (a preflight that spends nothing, a brief, a bounded brain run in a
box, the host's second look at what came back, a landing), and what it adds is the
part that triage never needed: the brain's changes have to come out of the box and
be judged before anything is committed. The rule that makes it safe is the one the
whole design rests on: the brain edits files and nothing else, and `af` makes every
other effect, from the tree the brain left and from checks that `af` ran on it.

**Where it lives.**

| Path | What it holds |
|---|---|
| `internal/fix/` | The feature with no shell in it: the preflight (`Preflight`, `CheckLanding`), the state of a run (`State`, `fix.json`), the brief (`BuildPrefix`, `TaskBlock`, `NewBrief`), the submission kind (`Kind`, the `fix_report` and its validator), `Apply`, `Measure`, `Land`, `Park`, the body of the pull request (`RenderBody`), the loop (`RunLoop`) and its ending (`Finish`), and the result (`Result`). It imports `gitx`, `checks`, `conform`, `policy`, `project`, `repomap`, `box`, `brain`, `submission`, `issuex` and the `skills` package. |
| `internal/afcmd/fix.go`, `fix_brief.go`, `fix_run.go`, `fix_land.go` | The five commands on the shell: the loop, `brief` and `submit`, `run`, and `land`. They do what needs the shell: the run directory and its ledger, the forge client, `box.With`, the envelope and its schemas (`internal/afcmd/schemas/fix*.json`). |
| `skills/af-fix` | The skill that says how to fix, compiled into the binary and carried by the brief (`skills.Fix()`), and the file a person installs with `make install-skills`. |
| `internal/triage/fakeforge/` | The server of the tests, which gained pull and merge requests. |

What the other packages gained for it: `gitx` can read a full hash, ask whether a
path is ignored and make a branch from a commit; `repomap.BuildDir` makes the map of
a directory; `project.MarshalGateFile` and `policy.MarshalGuardFile` write the two
files a box is given; `policy.IsProtected` says whether a path is one only `af`
writes; `toolio` has the failure category `preflight`; the scripted brain replays a
script of rounds, one for each run of the brain.

**The loop.** The preflight runs first (a clean tree on a branch, a gate to measure
by, the landing's needs, a brain that can be made), then the baseline, once, on the
tree as it is, and the full hash of the base is recorded as `base_head`. `fix.json`
holds the state; it is written last and atomically, so a run directory that has one
is a run that can be carried on. One box is held for every round of the run, and
before each round after the first it is synced from the host's tree, which carries
the change `af` has already applied. Each round places `gate.json` and `guard.json`
in the box, runs `brain.Execute` on the brief, and takes the tree out of the box.

**Apply.** The export is mirrored onto the checkout, and not committed. The files
the export lacks are deleted first, then new and changed files, modes and links are
written, through an `os.Root` of the checkout so that no path can leave it. `.git/`,
`.agent-fox/` (whatever their case, since on a file system that does not tell `.GIT`
from `.git` an export could write a hook that the landing runs), ignored paths,
anything that is not a file or a link, and everything under a submodule are left
alone; a path beyond a link of the checkout is judged after the links the export
replaces are deleted (and left out if the checkout keeps the link). A file that git
tracks and a rule ignores is never deleted, since the export never has it: the
rules of the box's hands leave it out. The deletions cover the other files of the
checkout's file view, and the directories they empty. A round that did not
submit a report, and one that asked a question, applies nothing, and a run that ends on
a question later puts back what an earlier round applied; an export with no
file that can be applied fails as `sandbox`. It is the host that writes, never the
box, and a box that cheats gets nothing past what the host's own rules let through.

**Measure.** The checkout must still be the base at `base_head`, since `af` makes
the commit that counts, and a commit or a switched branch is `git`. A change under
a protected path is put back to the base's content, or deleted, and reported; a
change that is nothing after that is refused as `no_change` with no gate run. The
report's files are compared with the paths git sees changed (a mismatch warns). The
gate runs again on the table the run was pinned to and is compared with the
baseline by check id, so a check that failed before is not counted against the
change; only on a verdict that may land does the revert check run, and only a
proven one makes the commit say `Closes`. A refusal is recorded and is the next
round's task block, with the tail of each failing check, in the same box and the
same tree.

**Land or park.** A landable change gets, in order, an issue (for a text or a file,
so that the pull request has something to refer to), a branch `fix/<N>-<slug>` from
`base_head`, one commit with the hooks of the repository, a push with retries and
the pull request, whose body is rendered from facts: what `af` measured is never
cut and what the brain reported is quoted. The checkout always ends on the base,
clean. A change that cannot land is parked: a `wip:` commit made without the hooks,
so that nothing can lose it, pushed and offered as a draft that says `Refs`. An
interrupted run parks what it applied on a local branch, on a context the signal
did not reach and within 10 seconds; a dry run discards it. `fix land` is the end
of the loop for a change a person made: it measures and lands or parks, with no
round of a brain.

**The run files** are `brief.md`, `original.md`, `run.json`, `fix.json`,
`submission.json` and `report.md`, which is the body of the pull request as it was
rendered (see [configuration.md](configuration.md#the-files-of-a-fix-run)). The
prefix of the brief (mandate, skill, report, gate and baseline, map, schema) is
byte-identical for every round of every attempt, and its hash is in `fix.json`, so
that a provider that caches a prefix serves the whole run from one write.

**Where it differs from the sketch above.** The sketch gave a fix a start, a
separate submission of its analysis and a land; the built commands are `brief`,
`run`, `submit` and `land`, because the analysis is not a separate submission any
more (the report is one object, and a question is the other form of it), and
`run` is the round of a brain that a person's own checkout does without. The divergences from the spec,
with the code of each, are in `docs/errata/09_af_fix_divergences.md`.

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

## The service, as built

`agent-fox` is the second binary of the repository (`cmd/agent-fox`), the long-running service that
[The hub](#the-hub) and PRD 03 describe. It is not part of the toolbelt: `af` does not depend on it, it
does not run on `internal/toolio`'s one-envelope shell (a server's lifecycle is flags, signals and logs; it
imports `toolio` only for the version document), and it runs no brain in this release. What exists is its foundation, which the specs that follow (jobs, the
worker, the poll, the `af` client) build on:

- **The process.** `agent-fox [--config PATH]` loads one `config.toml` (`internal/service/config`: five
  keys, unknown keys refused), reads the key file (`internal/service/secrets`), opens the state file,
  builds the hub client and the HTTP server, binds the listener, and runs until SIGINT or SIGTERM, when it
  drains the requests in flight (up to 30 seconds), closes the database and exits 0. A failed start is one
  `ERROR` log record and exit 2 (configuration, key or database) or 1 (the listener). Its stderr is a
  stream of JSON records; `cmd/agent-fox` is wiring, as `cmd/af` is.
- **The state file.** One SQLite file (`modernc.org/sqlite`, pure Go, WAL mode) behind `database/sql`
  (`internal/service/store`). Every table it owns is named `af_…`, and its migrations are recorded in
  `af_schema_migrations`, never in `PRAGMA user_version`, so that `aflib/jobqueue`'s tables can live in the
  same file later. Migration 1 is `af_subscriptions`: the slug, the label prefix, the sealed token, the hub
  user behind the token, who subscribed it, and the times. A token is sealed with AES-256-GCM under the
  deployment key, with the slug as additional data, and it is the only secret the file holds; a token that
  does not open with the key reads `unreadable` and does not stop the service.
- **Hub as the identity provider.** The service has no users, tokens or tenants of its own. Every route
  under `/api/v1` takes a bearer token and asks hub (`internal/hub`, the one place that knows hub's paths
  and its user object) whom it belongs to and whether that person sees the workspace; the answers are kept
  for `hub.auth_cache_ttl`, at most 15 minutes, in two bounded caches (`internal/service/ttl`) keyed by the
  hash of the token. Hub being down is `502`, never a cached answer; hub refusing a token on a later call
  evicts everything kept about it. Whoever sees a workspace on hub may subscribe it, read it and remove it.
- **The surface** (`internal/service/api`, documented in [api.md](api.md)): the probes, `GET`, `PUT` and
  `DELETE` of subscriptions, one error envelope, and a log of requests that holds no credential. A `PUT`
  checks the token the caller hands over against hub with two calls that bypass the caches, then seals and
  stores it in one statement.
- **The calls of an attempt.** Beside those two calls, the hub client makes the ones an attempt of the
  worker needs, always with the PAT of the subscription and never through the caches: the record of a
  workspace (clone URL, base branch, mode, URL of the forge repository), its resolved variables, a session
  (open, usage, complete), a batch of events, a sandbox lease and its release, and the push of a branch to the
  forge. Each is bounded to 10 seconds, the lease and the push to 5 minutes (`Client.LongTimeout`), because hub
  does slow work before it answers; none follows a redirect or reads more than 1 MiB. An answer is one of
  five things, which a caller tells apart with `errors.Is` and `errors.As`: refused (401, `ErrRefused`),
  forbidden (403, `ErrForbidden`, whose message names the operation and points to `PUT
  /api/v1/workspaces/{slug}`), not found (404, `ErrNotFound`; a release that finds nothing is released),
  rejected (another 4xx, `*RejectedError` with hub's message, read from its error object, whose `code` is the
  integer status and whose `error_type` is the kind; a `502` that says the forge refused the credential hub
  holds for it, `forge_unauthenticated` or `forge_forbidden`, is one too, since a retry cannot cure it) and unavailable (429, 5xx, a redirect, a
  transport failure, a timeout or a body that is not what hub sends, `*UnavailableError`). No error holds
  the PAT.
- **The forge through hub.** `hub.Client.Forge(pat, slug, repo)` is an `issuex.Client` for the one forge
  repository of a workspace, which the worker gives the loops in place of a forge client with a token of its
  own, and `ForgeFactory` is the same as a function of the signature of `toolio.App.NewForge`. It reads and
  writes through hub's forge API under `/api/v1/workspaces/{slug}`: `GetRepository` is the record of the
  workspace; `ReadIssue` (the issue with its comments, marked a pull or merge request when hub says it is
  one), `ListComments`, `UpdateIssue`, `CreateIssue`, `AddComment`, `AddLabels`, `RemoveLabel` and
  `CreatePullRequest` are one request each. `ListIssues` lists the open issues of exactly one label, which is
  all the label poll asks (`state=open&labels=<label>&limit=100`; any other filter sends nothing and is not
  available through hub): it follows a next page that hub names in a `Link` header, holds at most the
  filter's `Limit`, leaves out what hub marks as a pull or merge request, and is `Incomplete` when it stopped
  at the limit with issues left, or when the last page is as long as the page asked for, because hub's own
  list is a bare array that cannot say it cut a list short. A repository other than the workspace's (host, owner and name,
  without case; an empty host is the workspace's) is refused before a request is sent, and every other
  method of `issuex.Client` says the operation is not available through hub in this release. Hub's answers
  become the errors that the loops already tell apart: 401 and 403 an `*issuex.HTTPError` of that status
  that carries the message of the hub client, 404 `issuex.ErrNotFound`, 409 `issuex.ErrConflict`, 429
  `issuex.ErrRateLimited`, another refusal an `*issuex.HTTPError` with hub's message, and hub being
  unavailable an error that the loops may try again. So `auth`, `forge` and `retryable` come out as with a
  direct forge. The client reports GitLab's body limit for a GitLab host and GitHub's otherwise.
- **The clone and its git.** The worker clones the workspace's clone URL from hub's git server into `clone/` of
  the attempt directory (mode 0700), with the workspace's base branch checked out and the identity `agent-fox
  <agent-fox@noreply.invalid>` in the clone's own configuration; a `clone/` that an earlier delivery kept is
  removed first, unless the delivery carries the attempt on, which uses it (see [Recovery, as built](#recovery-as-built)). Every git command of the attempt, the clone, the commands of the fix pipeline and its push,
  runs in a clean environment: a `HOME` of its own that is removed with the attempt, no global or system
  configuration, no `GIT_ASKPASS` and no prompt (`gitx.HermeticEnv`), so a credential helper of the operator is
  never asked. The PAT reaches git only in the commands that can reach hub (`clone`, `fetch`, `pull`, `push`,
  `ls-remote`), as the header `Authorization: Basic base64(agent-fox:<PAT>)` set by `GIT_CONFIG_COUNT`,
  `GIT_CONFIG_KEY_0` (`http.<clone URL>.extraHeader`) and `GIT_CONFIG_VALUE_0`: never on a command line, in
  `.git/config` or in any file, and not in the environment of a commit, whose hooks are the repository's own.
  This needs git 2.31 or later. The environment of a git command is made of a short list of what git needs of
  the process (`PATH`, the locale, the proxies and the certificates) and nothing else of it. The box's sync and
  `checks.FileView` keep the default runner. A server that
  refuses the credential, 401 or 403, ends the attempt with a `failed` envelope of category `auth`; any other
  failure of the clone (a transport error, a 5xx, a hang-up, no answer in ten minutes, a base branch that is not
  there) is a transient error of category `git` and the attempt has no envelope. After the pipeline pushes a
  branch to hub's git server, the worker asks hub to push it on to the forge (`PushBranch`), before any pull
  request is asked for, up to three times in all while hub is unavailable, waiting one second and then two;
  any other answer is final. A branch that hub does not take ends the landing `failed` in category `git`, not
  retryable, with the branch kept on hub's git server and named in `next`, and no pull request. The seam is
  `fix.LandParams.PostPush`, which `FinishParams` and `ParkParams` carry and `afcmd.Config.PostPush` sets.
- **The worker.** `internal/worker` is the production `jobs.Runner`: it carries out one attempt of a job against a
  subscribed hub workspace. In order: *resolve* (the token and the label prefix of the subscription, the record and
  the variables of the workspace from hub, the refusals that need no box and no model), the hub *session*, the
  *mark* of the issue, the *clone*, the *lease* of a sandbox, the *run* of the loop in process, the *mirror* of the
  outcome to the issue, the *release* of the lease and the *report* to hub. It holds nothing of an attempt between
  calls, so attempts run side by side, each with its own workspace token and its own lease token, no vendor
  key and no forge token, and with a lookup of the environment, which the loops, the brains and the settings
  read, that yields nothing: the environment of the service changes no attempt. What an attempt that stops early writes and takes is fixed:
  a refusal at resolve, hub not answering at resolve or at the session, and a session refused with 401 or 403
  write nothing to the forge, take no lease and report nothing to hub; a transient clone, lease or box takes
  `in-progress` off and releases a granted lease, and reports nothing to hub, so that the session stays open for
  the delivery that is handed over again; a credential refused at the clone or at the lease is mirrored
  `failed` with one comment, with the lease released, and is reported to hub. Infrastructure failures (hub down, a clone, a lease or a box that cannot be had, a forge rate
  limit) are *transient errors* that the jobs layer hands over again, with no envelope; every other ending is an
  envelope, built by the worker for a refusal before the loop (tool `triage` or `fix`, empty arrays, usage source
  `none`) and the loop's own otherwise.
- **The forge host.** `aflib` recognises the host of an issue URL as a forge when its name holds `github` or
  `gitlab`, or when it is the host of `GITHUB_API_URL` or `GITLAB_API_URL` in the environment of the process. The
  worker gives the loop the URL of the issue on the workspace's repository, so a workspace whose forge host has
  neither in its name runs only if the service has one of those variables set. This is a limit of `aflib`, not
  something the worker can lift.
- **The gateway box and the `af` it needs.** The loop's box is made by the gateway of the lease (`box.GatewayEntry`),
  the only provider the attempt has, which counts as isolating, calls its programs by name from the image of the
  lease's profile, and is made with an empty `Env`: the profile supplies the image, the network and the secrets,
  and the brain's credentials are named, never valued, in the commands run in the box. The image **must provide `af`**
  of the same version and commit as the worker, since the checks of the repository are measured by the `af` of the
  box: `af --version` is run once per box, an `af` of another version is the warning `box_af_version_mismatch`, and one
  that is missing or fails is a `sandbox` failure that is not retryable and names the profile whose image has to
  provide it (`fix_hint.env` `AF_SANDBOX_PROFILE`).
- **Measuring in the box.** For a fix run under the service the detection of the checks (`af lang detect`), the
  baseline and the gate after the change (`af gate run`) and the revert check (`af revert-check`) are *measured in
  the box*, through the `af` of its image, and never on the host of the worker (`afcmd.Config.InBox`); the host's
  gate and revert check are not called. A box that is lost while it measures is replaced once in the delivery (see
  [Recovery, as built](#recovery-as-built)); a second loss ends the loop `failed`, category `sandbox`, retryable, which is
  a transient error.
- **The lease and the box.** After the clone the worker leases a sandbox: `POST …/sandboxes` with `{purpose:
  "triage"|"fix", labels: {job, attempt}}` and `profile` only when `AF_SANDBOX_PROFILE` is set, and tells the job
  the sandbox id. The attempt gets a box registry of its own that holds one provider, the gateway of the lease
  (its URL and token), which is not in `box.Default` and so is no choice of `--sandbox`; the box of the loop is
  made in it with the labels `af.run_id`, `af.tool`, `af.job` and `af.attempt`. A lease that hub refuses with 401
  or 403 ends the attempt with a `failed` envelope of category `auth` that points to `PUT
  /api/v1/workspaces/{slug}`. Any other answer (a 5xx, a capacity rejection with hub's reason in the message, a
  timeout), a gateway that cannot be reached or refuses the box, and a lease with less than two minutes left are
  a transient error of category `sandbox` and the attempt has no envelope. A lease that ends before `AF_TIMEOUT`
  would cut the timeout of the loop to a whole number of seconds that ends two minutes before `expires_at`, and
  the envelope gets the warning `lease_shortened_timeout`. Each lease is released once, after the box is closed (the lease of a box that is lost is released when the box is replaced),
  on every path (success, failure, a transient error, a cancellation, a shutdown and a panic), on a context
  that the cancellation does not reach, bounded to 30 seconds. A release that fails leaves the status of the
  envelope as it is and adds the warning `lease_release_failed`; under a transient error it is logged.
  `toolio.AddWarnings` adds the warnings to the encoded envelope and changes nothing else in it.
- **The loop and how the attempt ends.** After the lease the worker runs the loop in its own process, as the
  `af` command it is (`internal/afcmd`), with a shell of its own that no signal stops, only the context of the
  attempt: the forge is hub's for the workspace's repository, the box providers are the gateway of the lease
  alone, the git runner is the clone's, the checks of the repository are measured in the box, and the settings
  are the flags (`--brain`, `--budget`, `--max-turns`, `--timeout`, and for a fix `--attempts 1` and `--land`;
  the model goes to the brain in its options). The environment of the process is read by nothing: the brain
  gets a lookup that finds nothing. An issue is its URL on the workspace's repository, a text goes on stdin
  with its title as a Markdown heading, a file is written to `input/<base name>` (mode 0600 in a directory of
  mode 0700) and passed by path, and the strings of context come as `--context` values, then the answers;
  the earlier questions say there is no question for a string and give the question with its options for an
  answer. Every line of the loop's events reaches the job's recorder as it is written, beside the worker's
  own `step` events (`resolve`, `session`, `clone`, `lease`, `run`, `mirror`, `release` and `report`). The attempt ends in one
  of two ways. A loop whose envelope is `failed` with a retryable error of category `sandbox`, `forge` or `hub`
  is the infrastructure: a transient error that carries that failure, with no envelope, which the jobs
  layer hands over again. Every other ending is a result with the envelope the loop wrote, amended only by
  added warnings, side effects and artifacts (`hub_session` and `sandbox`), the report of the run, and
  `AF_MAX_ATTEMPTS` as the most attempts of a round (2 when the attempt stopped before the variables were read).
  When the attempt returns, `input/` is removed and so is `clone/`, except after a transient error and after a
  shutdown; `recovery/` (the journal of the attempt, which a later delivery of it reads) is never removed by the worker;
  `envelope.json`, `report.md` and the attempt directory are the jobs layer's and are never touched.
- **The mark, the mirror and the cancel — the label mirror.** Once the session is open and an issue is named, the worker sets
  `<prefix>in-progress` on it; the prefix is always the subscription's `label_prefix`, `AF_LABEL_PREFIX` is read
  for nothing but the warning `label_prefix_ignored` when it differs. Setting a status label (`in-progress`,
  `ready`, `needs-human`, `landed`, `failed`) removes the other four the issue carries, except that a fix's
  `in-progress` leaves `ready`, which only the outcome of the fix replaces. When the loop is over the outcome is
  mirrored to the issue the input named, or, for a triage of a text or a file that ended `done`, to the issue the
  loop created, and to none when there is neither: a triage `done` with confidence `confirmed` or `probable` is
  `ready` with no comment, `suspected` or `not_reproducible` is `needs-human` with the reason, any `needs_human`
  is `needs-human` with the question, a fix `done` or `unverified` that landed is `landed` with a `fix_summary`,
  one that landed under `AF_FIX_LAND=none` sets no label and its `fix_summary` says nothing was kept, a parked fix
  is `needs-human` with a `fix_summary` that says what failed and how to ask again, and any other `failed`, or
  `usage`, is `failed` with the failure. An attempt that returns a transient error, and a failure that an
  automatic attempt follows (`failed`, category `brain` or `no_result`, retryable, and a position in the round
  below `AF_MAX_ATTEMPTS`), only take `in-progress` off. Each comment ends with a marker naming the job, the
  attempt and the kind (`<!-- agent-fox job=… attempt=… kind=… -->`), is not posted when its marker is on the issue,
  and holds no path of the worker. A write that fails is a warning (`status_label_failed`, `comment_failed`,
  `request_label_failed`) with a side effect of the state `failed`, never a change of the status; with no
  envelope it is logged. The result reports the labels it set and removed, a label that was set and taken off
  again being removed only. A cancel or an unsubscribe is followed, after the loop has stopped and within a
  minute, by taking `in-progress` off and putting the request label of the job back, leaving `ready` in place,
  with the lease released; the result carries the envelope of the loop, or one of category `cancelled` when the
  loop had not begun, and the PAT read at resolve serves it though the subscription is gone. A shutdown writes
  nothing to the forge, keeps `clone/`, releases the lease and returns its cause.
- **The report to hub.** Once the lease is released, an attempt that has a session posts one usage record when a
  brain ran (the model of the session, and the brain, tokens, cost, source of the cost and turns of the envelope),
  all the events of the delivery that reports (an attempt that was resumed reports the events of the delivery that
  comes to its outcome or finds it stored), the loop's and the worker's steps, in batches of at most 500 in the order they
  happened, and completes the session: `completed` for `done`, `unverified` and `needs_human`, `timeout` for a
  failure of category `timeout`, and `failed` for the rest. An attempt that returns a transient error, and one
  that is stopped for a shutdown, posts none of this and leaves the session open: the delivery that comes to the
  outcome reports, with the usage of the whole attempt. A call that fails is a warning of the envelope
  (`hub_usage_failed`, `hub_events_failed`, `hub_session_failed`) and changes nothing else.
- **Tests.** `internal/hub/fakehub` is an `httptest` hub in the manner of `fakeforge`: a table of tokens,
  users and workspaces, a record of every request, and failures, hang-ups, delays, redirects and oversize
  answers by method and route on demand. It serves the calls above in the shapes of hub's OpenAPI
  document from state a test sets and reads: workspaces with a mode, a base branch and variables, issues
  with comments and labels (a pull request among them), pull requests, sessions with their usage and
  events, branch pushes, and leases. A workspace with files has a git smart-HTTP server (`git
  http-backend`) on the hub's own address that takes only `Authorization: Basic` with the user `agent-fox`
  and the PAT of the workspace. A lease points at an in-process gateway: agentkit's `gatewayd` over the
  local provider, on a loopback port, that takes the token of a lease that is held and records the specs
  it is asked to create and the boxes it closes. `make check` needs no hub, no key and no network beyond
  the loopback interface.

What it does: run `af triage` and `af fix` on a subscribed workspace with its token, in its own process, through
the worker below. What it will do, and does not yet: poll the forge through hub, and be driven by an `af` that
talks to it.

### The jobs, as built

`internal/service/jobs` is the layer between the routes and a runner: it keeps the requests for `triage` and
`fix` durable, runs them in order behind a seam, and records what became of them. The surface is in
[api.md](api.md#jobs); this is how it is made.

- **Records.** Three tables, appended to the migrations of the state file: `af_jobs` (one row per job),
  `af_attempts` (one row per attempt, keyed by job and number) and `af_events` (the log of a job, keyed by job and
  `seq`). `aflib/jobqueue` keeps its own tables in the same file; the `af_` prefix keeps the two apart. The files
  of an attempt, `envelope.json` and `report.md` (mode 0600), go under `attempts/<job id>/<n>/` beside the
  database, so that purging a job is one removal.
- **States.** A job is `queued`, `running`, `blocked`, `done`, `failed` or `cancelled`; an attempt is `queued`,
  `running`, `done`, `needs_human`, `failed` or `cancelled`. *Open* is queued, running or blocked; *at rest* is
  blocked or finished with no attempt running or queued. `finished_at` is when a job last entered `done`,
  `failed` or `cancelled`, and the retention counts from it. A partial unique index keeps a second open job of a
  kind for an issue out of a workspace.
- **The queue key and the order.** Each attempt is one item of `aflib/jobqueue`, of the type `attempt`, with the
  nonce `<job id>:<n>`, the workspace as its group, and `{job_id, attempt}` as its payload. The queue refuses a
  second queued or running item with the same type and key, so the key is the nonce, which is unique; enqueuing an
  attempt the queue has in any state is a no-op, and that makes the start-up enqueue and the sweep that runs every
  minute safe. The queue serializes a group, so one attempt of a workspace runs at a time. It claims by the second,
  so the order of a workspace is kept by `af_attempts.queue_seq`: a handler whose attempt is not the first open
  attempt of its workspace hands its item back. The number of attempts in a runner at once is `[workers].count`,
  held by a semaphore of slots; the queue has sixteen more workers than slots, so that an attempt that waits for a
  slot or out a backoff holds its workspace's place and no slot.
- **The runner contract** (`jobs.Runner`: `Run(ctx, Request) (Result, error)`). A result carries the envelope as
  the JSON the command wrote (stored verbatim), the report, the labels set and removed, and the most attempts a
  round may make. Four obligations belong to every runner: (1) one attempt is one brain-level attempt, and the
  runner does not run the brain again after a failure, because further attempts are the layer's; (2) infrastructure
  failures (a box or a lease that cannot be had, hub down or answering 5xx, a forge rate limit) are *transient
  errors*, `jobs.Transient`, and only what a brain-level attempt came to is an envelope; (3) the runner reads the
  token of the workspace when the attempt starts and keeps it for the life of the attempt, because an unsubscribe
  removes the subscription while a cancelled attempt may still be cleaning up; (4) what it does when it is stopped
  depends on why (`jobs.IsCancel`): for a cancel or an unsubscribe it releases what it holds and restores the
  request label within the grace, for a shutdown it leaves the attempt resumable and what it returns is discarded.
  The context carries the cause: `ErrCancelled`, `ErrUnsubscribed` (which is a cancel as well) or `ErrShutdown`.
  The request carries `Round`, the position of the attempt in its round (1 for the first attempt of a job and for
  one that an answer or a retry starts, 2 for the attempt that follows by itself a failure that is tried again),
  which the runner needs to tell an attempt that an automatic attempt follows from one that ends the job.
  `jobs/fakerunner` is the exported fake that scripts a runner for tests; `jobs/fakeclock` is its clock.
- **Transient retries versus automatic attempts.** They are different things. A transient error asks for the
  *same* attempt to be handed over again, after 30 seconds and then 2 minutes, on the layer's clock and with
  the worker slot released during the wait; the third transient failure fails the attempt and the job, and never
  becomes an automatic attempt, whatever the failure's `retryable` says. An *automatic attempt* is a new
  attempt (a new row) queued after a `failed` envelope of the category `brain` or `no_result` that is
  `retryable`, bounded by the round (see [api.md](api.md#what-an-attempt-comes-to)). A handover after a shutdown or
  a crash is neither: it raises `deliveries` and counts as no failure.
- **Cancel, unsubscribe and shutdown.** A cancel sets the job `cancelled` at once and gives a running runner
  60 seconds (`fixGrace` of 30 plus 30 to release the lease and restore the label); after them the attempt is
  recorded cancelled without it, an `ERROR` is logged, and the runner keeps its place and slot until it returns.
  An unsubscribe marks the workspace, cancels its open jobs with the unsubscribe cause, waits for their runners
  and then removes the row. A shutdown stops claiming, cancels the attempts with the shutdown cause and waits up
  to 30 seconds; the attempts stay `running` and are handed over again, under the same number, after the restart.
- **The event log.** Every transition emits `job_state`, a handover `attempt_start`, an end `attempt_end`, and a
  runner reports its own events through the `Recorder` of its request. An event is stored, with a `seq` that
  is gap-free per job, before it is published; the store hands the stored event to a broker whose subscribers have
  a buffer of 256 and are closed when it is full, so a client that does not read can hold up neither the
  runner nor the others. The stream (`GET …/events`) subscribes first, replays the store after its starting point,
  drops the numbers it has sent, and ends when the job is at rest; a client that was cut recovers from the store
  with the last id it saw.
- **Retention.** A purge runs at start and then every hour on the layer's clock, and removes finished jobs with no
  running attempt and no runner in progress that were finished more than `[database].retain` ago: the directory
  first, then the rows, under the lock of the job.
- **Tests.** Every test runs offline against `fakehub`, the fake runner and a fake clock. `cmd/agent-fox` wires the
  worker; `jobs.PlaceholderRunner`, which fails every attempt as `unsupported`, is left for the tests of the layer.

### Recovery, as built

An attempt can be handed to the worker more than once: after a restart, a crash, a shutdown or a transient error the jobs
layer hands over the same attempt again, and `Request.Delivery` is how many times it has been (the handover count, 1 on the
first). A delivery above 1 is a *resume*. What lets it carry on is in `recovery/` of the attempt directory, which the
worker never removes, and the clone, which it keeps after a transient error and a shutdown.

- **The journal.** The journal, `journal.json` in `recovery/`, is written by the worker, mode 0600 in a directory of mode 0700, by
  replacing the file atomically and syncing it, each time a step is done: the job, the attempt and the kind; the clone
  and the `clone_url` of the workspace without user information; the settings the run was started with (brain, model,
  sandbox profile, landing mode, budget, turns); the run id and the base branch and commit it was begun at; the hub
  session (`completed` once it is); the leases, each with whether it was released and whether the sandbox was lost; the
  deliveries, each with how it ended (`outcome`, `transient` or `shutdown`, none when the process died); what the brain
  runs of the attempt cost and the part of it that was spent in run directories that were removed; the plan of the
  landing and the steps of it that are done; and the steps of the mirror and of the report that are done. It holds no PAT,
  no token of a lease, no `Authorization` value and no URL with user information. A write that fails is the warning
  `journal_unwritable`, once in a delivery, and the delivery goes on from what it holds in memory; only the plan of a
  landing is required, and a plan that cannot be written stops the landing with a transient error of category `io`,
  before anything is pushed, and before any write of the landing to the forge but the issue that a text or a file
  is made into, which the journal records as soon as hub answers and the next delivery goes on with.
- **The outcome.** As soon as an attempt comes to its outcome, and before anything is written for it to the forge or to
  hub, the worker writes the outcome, `outcome.json` in `recovery/`, the same way: the envelope as text, the report, the most attempts
  and the labels, `mirrored` false. When the mirror is done it writes it again, with the envelope the mirror amended
  (its warnings, side effects, the session and the sandbox) and `mirrored` true, before the lease is released, the usage
  is posted and the session is completed. A shutdown and a transient error store none. The first delivery of an
  attempt removes the journal and the outcome that an earlier run of the same directory left.
- **How a delivery begins.** The first delivery begins afresh. A later one reads the journal. With no clone to carry on
  (none was made, or it is gone) there is nothing to discard, and it begins as a first delivery would, without a word, on
  the session that the journal records as open. A clone with no journal, a journal that does not read, and one of another
  job, attempt or kind are `resume_discarded` with the reason `journal_unreadable`. A journal that reads gives back the leases it records as
  held, pins the settings (the brain, `AF_MODEL`, `AF_SANDBOX_PROFILE`, `AF_FIX_LAND`, `AF_MAX_BUDGET_USD` and `AF_MAX_TURNS`; the
  timeout is read afresh for each delivery, and so is `AF_MAX_ATTEMPTS`) and takes the open session; and the clone is
  carried on only if it is the top level of a git work tree (`clone_invalid`), its `origin` is the clone URL of the
  workspace now (`origin_changed`), the forge repository is the journal's (`forge_repository_changed`), and the run
  directory has its pinned file (`run_missing`) and a ledger that reads (`ledger_unreadable`). Otherwise the warning
  `resume_discarded` names the reason and the delivery clones afresh on the same attempt, with the spend and the
  session it had carried over. What neither rule covers ends the attempt `failed`, category `internal`: a commit on the
  base branch after the commit the run began at that has no `Agent-Fox-Run` trailer for the run, or the branch of the plan
  on hub's git server at a commit other than the plan's. So does a clone that cannot be carried on (it is gone, its
  run directory or its ledger is lost) after the branch of a landing reached hub's git server, or a pushed branch that
  is gone from it: a landing made again would take another branch, and open another pull request. A local branch of the run that
  holds no commit of its own, which a landing that died before it committed leaves (the landing marks the branch it
  makes with the run), is deleted, so that the landing keeps the name it would have had.
- **The stage a delivery picks up from.** In the stage `outcome` an outcome is stored for this job and attempt; there is no loop and
  no lease, the leases of the journal are released, the mirror is finished where it is not (the labels that the journal
  does not record, a comment only when its marker is not on the issue), the usage, the events and the completion
  that the journal does not record are posted, and the stored envelope is returned with the one warning
  `attempt_resumed` added. In the stage `published` the branch of the plan is on hub's git server at the plan's commit; nothing
  is measured, committed or pushed again and no lease is taken; what the journal does not record of the landing is made
  from the plan (the branch push route of hub, the pull request), and the envelope is built from the plan. In the stage `applied` the
  host accepted a fix report and applied it (an `apply` record follows the last `submission_accepted`, and the payload
  still passes its check): the change is brought back into the working tree (it is there, or it is the commits of an
  unpushed local branch that all carry the trailer of the run, which are put back in the tree, and the branch deleted so that
  the landing names it as it did), the brain does not run, one lease is taken, and the delivery re-measures the change once against
  the baseline and lands or parks it. In the stage `submitted` a question or a diagnosis was accepted, and there is no lease and no box: a
  fix question ends the run `needs_human` and the mirror posts its comment once, a triage diagnosis is landed again
  through `triage.Land` on the clone's tree, with the issue an earlier landing created, if any, so that a triage of text
  makes one issue. In the stage `brain`, which is any other run, the clone is put back at the commit the run began at, the run
  directory is reused, and the loop runs again with the budget and the turns that the attempt has left.
- **Re-measuring.** A baseline result that the clone lacks (`.agent-fox/gate/<id>`) is not a reason to leave a change
  unverified: the delivery runs the baseline again in the box, on the tree of the base with the pinned table, with
  the change of the run set aside (`git stash`) and put back, and fix.json names what ran; a gate result is named by its tree, its
  checks and its mode, so the id is the one that was lost. A change that is nothing once the protected paths are put
  back ends `failed` with category `no_result`, not retryable, and nothing is landed or parked.
- **Budget and usage.** One budget and one bound of turns belong to the attempt, however many deliveries it takes. The
  spend is what the ledger of the run records (every `brain_end` has its tokens), plus what was spent in run directories
  that a discarded resume removed; a brain run that began and has no end in the ledger counts as nothing and is the
  warning `brain_spend_unrecorded`. With nothing left the attempt ends `failed`, category `budget`, before the lease and the brain, and
  its envelope reports what was spent. The usage the envelope reports, and the one record that is posted to hub, are the attempt's.
- **A box that is lost.** A box whose sandbox is gone, or cannot be reached, is noticed by the box layer
  (`box.ErrUnavailable`, `Session.Lost`). The loop stops without landing or parking, and the worker, once in a delivery,
  releases the lost lease (a failure is logged and the journal keeps it as held), leases a replacement with the profile and
  the purpose of the first, cuts the timeout of the loop to what is left of the delivery and to two minutes before
  the replacement expires (`lease_shortened_timeout`), and runs the loop again from where the attempt stands, by the stages
  above. The box is replaced as the warning and the step event `box_replaced` say, with both sandbox ids. A second loss,
  a replacement that hub refuses or that has less than two minutes left, and one that cannot be opened are transient
  errors of category `sandbox`; a lease that hub refuses at first, or a first box that cannot be opened, is not a loss.
- **The reconciliation pass.** `internal/service/reconcile` runs a pass when the service starts, in a goroutine that
  delays neither the listener nor the queue, and every 15 minutes: one at a time, and stopped when the service stops. For
  every subscribed workspace whose token opens it lists the leases through hub and releases each orphan, a lease whose
  `job` label names a job of the database and whose `attempt` label names an attempt that no runner of this process
  carries out (a 404 is released; a lease with no labels, an unknown job or a running attempt is never released). It
  then posts the usage that the journal of an attempt that ended records as not posted, and completes the session that
  the journal records as open, with the status of the stored outcome or `failed`; it posts no events. A workspace
  that cannot be asked is logged at `WARN`, without its token, and tried again by the next pass; each release and each
  completion is logged at `INFO` with the workspace, the id, the job and the attempt.
- **What an operator sees.** A resume emits the step event `resume` (the handover count, the stage, whether the
  brain is skipped, and the reason when the resume was discarded), and its envelope carries the warning
  `attempt_resumed`, which names the same; `resume_discarded`, `box_replaced`, `journal_unwritable` and
  `brain_spend_unrecorded` come when they apply.
- **Tests.** Nothing here needs a hub, a key or a container engine. A process that dies is simulated by the stage-abort
  seam, which only the tests of the worker can set, `Runner.abortAfter`: it stops a delivery right after a named stage
  (a stage of the pipeline, or `briefed`, `submission_accepted`, `applied`, `plan_recorded`, `pushed`, `hub_push`,
  `pull_request`, `created_issue`, `outcome_stored`, `mirror_comment`, `mirror_labels`, `mirrored`, `released`,
  `usage_posted`, `events_posted`, `session_completed`, `box_synced`; `applied#2` is the second time) without cleanup,
  as a process that died is stopped. A graceful shutdown goes through the real jobs layer, restarted on the same database
  file.

### The label poll, as built

The poll is the trigger that needs no client (`internal/service/poll`). It runs inside `agent-fox`, after the jobs layer
and before it stops: `serve` starts it once the jobs layer runs and defers its stop after the jobs layer's, so that the stop
runs first, with no cycle left in flight, before the queue is closed and before the state file is.

- **Loops.** A `Supervisor` reads the store's subscriptions when it starts and every 60 seconds, and keeps
  one loop for each subscribed workspace: a new subscription gets a loop, and a subscription that is gone loses its loop and the cycle it has in
  flight. A loop waits a random 0 to 30 seconds, makes its first cycle, and makes each later cycle one interval after the
  previous one ended. At most four cycles run at once across the workspaces (a due cycle waits for a slot), and a cycle that has
  run for 5 minutes is cancelled, keeping what it has done, and followed after the interval the workspace last read. The clock,
  the first-cycle delay, the scan period and the release delay are injectable; no setting is configurable but the interval
  (`AF_POLL_INTERVAL`, see [configuration.md](configuration.md#the-label-poll)).
- **A cycle.** It first brings the poll's records for the workspace up to date with their jobs, which asks nothing of hub; then it
  reads the label prefix and the token from the store and the resolved variables from hub. If the poll is off it ends there.
  Otherwise it reads the workspace record, for the forge repository, and lists the open issues of `<prefix>triage` and then of
  `<prefix>fix` through the hub forge client's `ListIssues`, at most 100 of each, takes each listing's issues in ascending number
  whatever order hub returned them in, and applies the rules to each. Pull requests, closed issues and issues without a request
  label never reach the rules.
- **The rules,** in order, for an issue N listed under label L of kind K: the jobs layer has an open job of kind K for N, which
  absorbs L (removed, no job, an `INFO` record); a hold on L for N is in place (nothing is done); the removal of L from N is owed
  (removed, no job); otherwise a new request, for which the poll makes the job through `jobs.Service.Create`, writes its record,
  and removes L. A refusal of the jobs layer for a duplicate is handled as an absorbed label (two creators that race yield one
  job, because the unique index of the open jobs makes the check atomic with the insert), one for a workspace that is not
  subscribed ends the cycle with no label written, and any other ends it with a `WARN` and the label left.
- **Owed removals.** The poll writes a record for each job it makes before it removes the label, and for each absorbed label
  whose removal fails. A removal that fails leaves the removal *owed*: it is retried whenever the issue is listed under the label,
  confirmed by a removal that succeeds or that hub answers with not found, and settled by a complete listing of the label that does
  not contain the issue, so a label whose removal failed is never taken a second time once the first job has ended.
- **Holds and the release delay.** The `Standing` read of the jobs layer (`Service.Standing`) gives a job by id: its state, source and request label, whether its
  cancel reached an attempt that was in a runner, and whether a runner of it is still in progress; it is `ErrNotFound` for a job
  that retention purged. A job the poll made holds its label from the moment its cancel reached a runner, because the worker's
  cancel cleanup writes the label back before the runner returns, whatever the runner reports and even if it overruns the cancel
  grace; so does a job that was purged before the poll saw it end, from the time the poll found it gone. The release clock of a
  hold starts when the poll first sees no runner of the job in progress. A hold is released only by a complete listing that does
  not contain the issue and was requested, by the poll's clock, at least the **release delay** (5 minutes) after the release
  clock started: the forge's list may not yet show a label that was just put back, and the runner may still be writing it.
- **Records that outlive their jobs.** The poll's records are the rows of `af_poll_requests` (migration 5): the workspace, the
  issue, the label, the job (none for an absorbed label), whether the removal is owed, what the poll has seen of the job (open,
  ended, holding) and the release clock. There is no foreign key to `af_jobs`, so a record survives a restart and the retention
  purge of its job, which needs no step for the poll. A record is dropped once nothing is owed and nothing holds.
- **Incomplete listings.** A listing is complete when `ListIssues` did not stop at its limit. Hub's own list is a bare array that
  cannot say it cut a list short, so a last page as long as the page asked for is incomplete as well (erratum 15). An incomplete
  listing logs a `WARN` that names the workspace and the label, once until the label lists complete, releases no hold and settles
  no owed removal; the issues past its last page wait for a later cycle. Consumed labels leave the filter, so a backlog drains over
  successive cycles; only held labels and owed removals stay in it.
- **Failures.** Hub being unavailable ends the cycle with a `WARN`, and the next one runs an interval later. A refused or forbidden
  token, or a workspace that hub does not find, ends it with an `ERROR` that names the operation and, for the token, the `PUT`
  route that replaces it; another 4xx on a read ends it with an `ERROR` that carries hub's message; one on a label removal leaves
  it owed and goes on with the next issue. A failure is logged when it first occurs, not again while it repeats, and one `INFO`
  record says that the poll has recovered when a cycle completes without a failure. A failure of one workspace never stops or delays
  another's loop beyond the four slots, and no record holds a PAT.
- **The cost of a cycle.** On hub, an enabled cycle makes four requests of hub that read (the variables, the workspace record and one list of each
  request label, each of at most 100 issues), and one removal for each label it takes, absorbs or takes again; a cycle of a workspace
  that is off makes one read of the variables, every 5 minutes. The two lists and the removals are the calls that reach the forge, so
  the poll spends the workspace's forge rate limit, and `AF_POLL_INTERVAL` (at least a minute) is how to control it. The poll runs no
  model and its jobs spend as any other job does.

## What moves where

| Today | In this architecture |
|---|---|
| `codeimpl` and `codefix` phase loops | `af impl` and `af fix`: a loop over briefs calling a `Brain` (`af fix` is built; see [Fix, as built](#fix-as-built)) |
| `internal/agentrun` (phase runner, guard, policy, scratch) | gone; `guard.Check` from the hands behind `af guard check`; scratch is the run directory |
| the `submit_*` tools and their handlers | `af ... submit` commands over the same schemas |
| `internal/checks`, `internal/conform`, `internal/gitx`, the untracked snapshots, parking, the PR body | `af`'s effect layer, on the hands' runner and outline |
| `afspec`, `issuex`, `internal/project` (later the language profiles) | unchanged, inside `af`; `issuex` is imported from `github.com/agent-fox-dev/aflib/issuex` |
| prompts as Go strings | the briefs, as templated documents rendered by `af task brief` and `af review brief` |
| `issuetriage` | `af triage`: a read-only brain run in a box, with the toolbelt around it (see [Triage, as built](#triage-as-built)) |
| `codefix` | `af fix`: a brain run in a box that edits, with `af` applying its tree, measuring the change and landing or parking it (see [Fix, as built](#fix-as-built)) |
| the `af-impl` skill | shrinks to the attended loop over `af` commands |
| `cmd/impl`, `cmd/fix` | thin entry points that become `af impl`, `af fix` (the second is built: `cmd/af/fix*.go`); the envelope, flags and exit codes are those of `docs/cli.md`, whose envelope is a new shape (`docs/errata/05_old_envelope_fields.md`) |
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
- The tools image after the overlay line (the hands' agent at `AgentPath`,
  Codex and OpenCode): what is pinned, and how the hands' version and `af`'s are
  kept in step. The recipe that exists builds `af` and Claude Code on the sandbox image.
- The resources of a box (CPU and memory), which no flag sets yet.
- Hub's task templates for `impl`, `fix`, the stack gate and the conflict
  fix, once the tasks draft is reconciled with *The hub*.
- The measured comparison this design has to win: tokens per landed task
  and the share of `impl` runs whose pull request merges without a human
  fix, against the numbers in PRD 14's intent.
