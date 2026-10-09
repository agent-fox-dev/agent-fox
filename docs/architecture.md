# Architecture: a brain, a toolbelt, and the hands

**Status: proposed. This is a placeholder for the target architecture, not a
description of the code as it is.** Today's architecture is the one ADR 03,
ADR 04 and ADR 09 describe: four programs that each own an agent loop built
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
 brain (interchangeable)        Claude Code | Pi | the AgentKit driver | a person
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
                                guard.Check, the MCP pool, code mode, the driver
```

Three layers, each replaceable without the others noticing:

- **The brain** is whatever runs the model and its loop. It is not part of
  this repository. Claude Code and Pi are the brains a person already has
  open; the AgentKit driver is the one `af` can run unattended without a
  vendor CLI on the machine; a person at a shell is a valid brain too.
- **`af`** is the toolbelt: the enforcement that today lives inside
  `codeimpl`, `codefix`, `internal/conform`, `internal/checks`,
  `internal/gitx` and `issuex`, exposed as commands with the envelope
  `internal/toolio` already defines. It is what
  [PRD 13](prds/13-rebuild-fix-on-a-shared-change-engine.md) called
  `internal/engine`, minus the phases and the prompts.
- **The hands** are AgentKit after PRD 10: workspace mechanics with no
  opinion about phases, specs or envelopes. `af` is built on them. The
  AgentKit driver is one of the brains, not a layer of its own.

## The toolbelt

### Ring 1: facts

Pure, idempotent, JSON out, no side effects. Any brain may call them as
often as it likes, and so may a person.

| Command | What it answers | Built from |
|---|---|---|
| `af spec validate <ref>` | is this package a complete plan (C1 to C11) | `afspec` |
| `af lang detect` | what the repository is written in, its gate commands | `internal/project`, later the language profiles of PRD 13 §1 |
| `af map` | the repository map at a token budget | `internal/repomap` on the hands' `Walk` and `outline` |
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

- `.agent-fox/run/<session_id>/`: the ledger, the baseline result and its
  log, the rendered briefs, the check logs a brief refers to by path. Git
  ignores it through its own `.gitignore`.
- `tasks.json`: the task states, written only by `af task land` and
  `af task start`, committed beside the work.
- git: the branch, the commits with their trailers, the parked `wip:`
  commit. "Where did it stop" is answered by `git log`.

A brain that dies mid-task loses nothing `af` had measured. The next brain,
or the next run, continues from `af task brief`.

## The three bindings

From weakest to strongest. The first must work on its own, because it is
the one every host has.

**Shell.** Every `af` command reads JSON on stdin where it takes input,
writes one envelope on stdout, and uses the exit-code table `docs/cli.md`
already documents. Any brain with a shell tool can drive it.

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

## A task, end to end

The `impl` replacement, with Claude Code as the brain:

1. `af run start 16` validates the package, detects the language, runs the
   baseline gate, creates the branch, snapshots untracked files, writes
   the run directory and a settings file with the hook and the MCP server.
2. `af task brief 16/3` renders the task's scoped spec, the survey's
   decisions, the repository map, the gate commands with their log paths,
   and the previous attempt's failure if there was one, into one document.
   The run-constant part is byte-identical for every task so the brain's
   cache prefix holds; the task block follows it.
3. The brain works. **Attended:** a person is in Claude Code and a short
   skill says "read the brief, implement, submit". **Unattended:**
   `af impl 16 --brain claude` runs the brain headless with the brief as
   the prompt, the allowlist, the settings file, and reads cost from the
   brain's own output.
4. `af task submit 16/3` validates the report and rejects naming every
   problem at once.
5. `af task land 16/3` measures and either commits or refuses with a
   structured reason the brain reads on its next turn. The attempt budget
   is `af`'s. A spent budget parks the work as `wip:` and returns the
   checkout to the base branch, as today.
6. After the last task, `af review brief` emits the packet and
   `af review submit` validates the answers. The reviewer is a fresh
   context by construction: a Claude Code subagent with read-only tools, a
   second headless run, or the AgentKit driver.
7. `af run land` runs the hermetic gate, pushes, and opens the pull request
   from facts, as a draft when findings were neither fixed nor declared.

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

| Brain | Mechanism | Notes |
|---|---|---|
| `claude` | the Claude Code CLI headless, with the allowlist, the settings file and JSON output for cost | the default for unattended runs on a machine that has it |
| `pi` | Pi's headless mode where it exists; otherwise attended only | *to be written once Pi's non-interactive surface is confirmed* |
| `agentkit` | the PRD 10 driver in-process, with the hands' tools, `guard.Check` as the interceptor, and optionally code mode over the `af` fact tools | the one brain `af` fully controls; the one that needs no vendor CLI; the one that can bind code mode |

The interface is honest only if the three agree on the contract above. A
test runs the same brief through each available brain against the scripted
provider and checks that `af` saw the same submission.

## What moves where

| Today | In this architecture |
|---|---|
| `codeimpl` and `codefix` phase loops | `af impl` and `af fix`: a loop over briefs calling a `Brain` |
| `internal/agentrun` (phase runner, guard, policy, scratch) | gone; `guard.Check` from the hands behind `af guard check`; scratch is the run directory |
| the `submit_*` tools and their handlers | `af ... submit` commands over the same schemas |
| `internal/checks`, `internal/conform`, `internal/gitx`, the untracked snapshots, parking, the PR body | `af`'s effect layer, on the hands' runner and outline |
| `afspec`, `issuex`, `internal/project` (later the language profiles) | unchanged, inside `af` |
| prompts as Go strings | the briefs, as templated documents rendered by `af task brief` and `af review brief` |
| `specgen`, `issuetriage` | unchanged for now; they are single-phase and the cost of the current shape is small. *To be decided.* |
| the `af-impl` skill | shrinks to the attended loop over `af` commands |
| `cmd/impl`, `cmd/fix` | thin entry points that become `af impl`, `af fix`; the envelope, flags and exit codes stay as documented |

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
- **Whether `spec` and `triage` move to the same shape.** They are one
  phase each and already fit the current design; moving them buys
  uniformity and little else.
- **What `af serve` exposes beyond the toolbelt.** The hands' navigation
  tools (`file_outline`, `find_symbol`, `find_references`, `code_search`)
  could be served to a brain that lacks them. Pi and Claude Code have their
  own; serving duplicates them. *To be decided per brain.*

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

- The `af` command reference, once the commands exist, in `docs/cli.md`.
- The run directory layout and the ledger file formats.
- The host settings `af run start` writes for each brain.
- The measured comparison this design has to win: tokens per landed task
  and the share of `impl` runs whose pull request merges without a human
  fix, against the numbers in PRD 14's intent.
