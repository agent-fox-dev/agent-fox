# Architecture

How agent-fox is built, and why it is built that way. This document is for
people who work on the code or want to understand what happens inside a run.
For what the programs do from the outside, see the reference documents:
[cli.md](cli.md) for the command lines, [api.md](api.md) for the service's
HTTP API and [configuration.md](configuration.md) for settings.
[development.md](development.md) covers building and testing.

## Contents

- [Overview](#overview)
- [The one rule](#the-one-rule)
- [Package map](#package-map)
- [The command shell](#the-command-shell)
- [The run directory and ledger](#the-run-directory-and-ledger)
- [Fact commands internals](#fact-commands-internals)
- [Boxes and brains](#boxes-and-brains)
  - [Providers](#providers)
  - [Where the brain runs](#where-the-brain-runs)
  - [Brains that run as a process](#brains-that-run-as-a-process)
  - [The submission protocol](#the-submission-protocol)
  - [Measuring in the box](#measuring-in-the-box)
  - [The tools image](#the-tools-image)
- [The tool policy](#the-tool-policy)
- [The repository map](#the-repository-map)
- [Triage internals](#triage-internals)
- [Fix internals](#fix-internals)
- [The service](#the-service)
- [The jobs layer](#the-jobs-layer)
- [The worker](#the-worker)
- [Redelivery and reconciliation](#redelivery-and-reconciliation)
- [The label poll](#the-label-poll)
- [Forge follow-ups](#forge-follow-ups)
- [The service client](#the-service-client)
- [Design records](#design-records)

## Overview

agent-fox triages and fixes issues on GitHub and GitLab. A language model does
the parts that need judgment; the program does everything that can be measured
and every effect that leaves the working tree.

```
 forge (GitHub, GitLab)       issues, labels, comments, pull requests
        ^  only through hub's forge API, with the workspace's credential
        |
 agent-fox (cmd/agent-fox)    subscriptions, jobs, the label poll, the worker
        |  one attempt: resolve, session, clone, lease, run the loop, mirror, report
        v
 the loops (internal/afcmd)   af triage, af fix: preflight, brief, brain, measure, land
        |  in a box: a sandbox leased from hub, or podman or local on a laptop
        v
 a brain                      the AgentKit driver, or a scripted replay for tests
                              hands back one submission through its submit tool
```

The repository builds two binaries.

- **`af`** (`cmd/af`) is the command-line tool. It runs the triage and fix
  loops on the machine it is started on, exposes the fact commands they use
  (`lang detect`, `gate run`, `revert-check`), and is the client of the
  service (`af workspace …`, `af jobs …`).
- **`agent-fox`** (`cmd/agent-fox`) is the long-running service. It keeps
  subscriptions of hub workspaces and a durable queue of jobs, finds work by
  polling issue labels, and runs each attempt of a job through the same loops
  as `af`, in its own process.

Three sibling projects supply the rest.

- **[hub](https://github.com/agent-fox-dev/hub)** owns identity, workspaces,
  the git server, secrets and variables, sandboxes and the connection to the
  forge. The service has no users of its own; it asks hub who is calling. It
  holds no forge token; it reaches the forge only through hub.
- **[agentkit-go](https://github.com/agent-fox-dev/agentkit-go)** supplies the
  agent: the driver loop, the model providers and price catalog, the tools
  (the "hands"), the shell-command reader and guard, the code outline, and the
  `sandbox` module with its providers and the remote gateway.
- **[aflib](https://github.com/agent-fox-dev/aflib)** supplies `issuex`, one
  forge client over GitHub and GitLab, and `jobqueue`, the durable SQLite
  queue the jobs layer is built on.

## The one rule

**The brain never touches git, the forge or hub except through `af`, and `af`
never runs a model except through a brain it was handed.**

Everything else follows from this. The model surveys the code, writes code and
tests, and decides what is wrong. The program measures what can be measured
(which checks pass, whether a test proves a fix) and owns every effect beyond
the working tree: commits, branches, pushes, issue edits, labels, comments and
pull requests.

The box makes the rule physical rather than a matter of trust:

- **The brain cannot push.** No forge token and no hub token is in the box.
  The only credential a box ever holds is the model vendor's key, and only
  when the brain runs inside it.
- **The brain cannot make a commit that counts.** `.git` goes into the box,
  so `git log` and `git diff` inside are true, but it never comes out: the
  export drops it. A commit the brain makes stays in the box. `af fix` takes
  the tree out, applies it to the host's checkout, measures it and makes the
  commit itself.
- **The brain cannot change protected paths.** The tool gate refuses writes
  to them, and `af fix` puts back any change to them before it measures.
- **The brain's word is not the result.** `af` decides only from a
  submission file it validates, never from what the brain printed or claimed.

`af fix` holds one box for a whole run, across all its rounds, and closes it on
every path out (result, failure, panic, cancellation).

## Package map

| Path | Purpose |
|---|---|
| `cmd/af` | The `af` binary: build identity, the shell from `internal/afcmd`, and the service client commands (`workspace`, `jobs`). The client lives here only, so that callers of `afcmd.New` never get it. |
| `cmd/agent-fox` | The `agent-fox` binary: flags, startup order (configuration, key, state file, server, listener), signal handling and the wiring of the jobs layer, worker, cancel cleanup, poll and reconciliation. |
| `internal/toolio` | The command shell behind `af`: the envelope, statuses and exit codes, warnings, flags, the positional input, `--context`, `--dry-run`, `--timeout`, signals, the run directory, events and ledger. |
| `internal/afcmd` | The `af` commands as an importable package, so that `agent-fox` can run them in process. One file per command (`lang.go`, `gate.go`, `revert_check.go`, `triage.go`, `fix.go`, `fix_start.go`, `fix_run.go`), plus `Config` with the seams a caller fills in. |
| `internal/afclient` | The service client library: server URL, credential file, bounded requests, error mapping, following a job's event stream. |
| `internal/afclient/fakeclock` | A test clock for the client's backoff. |
| `internal/agentdir` | The git-ignored `.agent-fox/` directory and atomic file writes into it. |
| `internal/gitx` | Hermetic git and process invocations, issued from Go: runners, NUL-separated listings of files and changed paths. |
| `internal/project` | What a repository is written in and how it is checked: languages, the gate table, suite forms, programs, which paths are tests. |
| `internal/checks` | The file view, the tree hash, the gate engine with its cache, and the comparison of two gate results. |
| `internal/conform` | The revert check: do the tests of a change fail without it. |
| `internal/policy` | The tool policy: may a brain make this tool call. |
| `internal/repomap` | The repository map at a token budget. |
| `internal/box` | The box that holds a brain's tree: provider registry (`podman`, `local`, and a per-attempt `gateway`), open, sync, read, export, exec, close. |
| `internal/submission` | The submission protocol: one validated JSON object, written by `af` and checked again by the host. |
| `internal/brain` | The `Brain` interface, its registry and shared flags, `Execute`, the tool gate and transcript, the `scripted` and `agentkit` brains, and `Process` for a driver that runs inside a box. |
| `internal/triage` | `af triage` without the shell: the diagnosis and its validator, the issue body, the brief, the preflight, the landing. |
| `internal/triage/fakeforge` | A GitHub and GitLab API server over `httptest`, for tests. |
| `internal/fix` | `af fix` without the shell: preflight, baseline, brief, report kind, apply, measure, land and park, the round loop and its result. |
| `internal/hub` | The hub API client (identity, the calls of an attempt) and the forge-through-hub client. |
| `internal/hub/fakehub` | An `httptest` hub with a git server and an in-process sandbox gateway, for tests. |
| `internal/service/config` | The service's `config.toml`: lookup, the seven keys, `${NAME}` expansion, defaults, validation. |
| `internal/service/secrets` | The key file and the sealing of workspace tokens. |
| `internal/service/store` | The SQLite state file, its migrations and the subscriptions table. |
| `internal/service/ttl` | A bounded cache with expiry for hub's answers. |
| `internal/service/api` | The HTTP surface: probes, authentication guards, subscription and job routes, the event stream, the request log. |
| `internal/service/jobs` | The jobs layer: job and attempt records, the queue adapter, the runner seam, cancel, answer, retry, unsubscribe, the event log, retention and the cancel-cleanup bookkeeping. |
| `internal/service/jobs/fakerunner`, `internal/service/jobs/fakeclock` | A scriptable runner and a controllable clock, for tests. |
| `internal/service/labels` | The one table of the label protocol: seven labels with kind, colour and description. Standard library only. |
| `internal/service/labelcleanup` | The hub-backed cancel cleanup that restores labels after a cancel. |
| `internal/service/poll` | The label poll: one loop per subscribed workspace, the rules for a labelled issue, and the forge follow-ups. |
| `internal/service/reconcile` | The pass that releases sandbox leases left behind by attempts. |
| `internal/worker` | The production runner of the jobs layer: it carries out one attempt against a hub workspace. |
| `internal/fakeaf` | A fake `af`: the test binary run under that name. |
| `internal/optin` | Gates for the tests that need a container engine. |
| `internal/testutil` | Helpers shared by the tests of every package: hermetic git and repositories, files, waiting with a deadline, a JSON log, JSON and SQLite helpers, HTTP requests and the error envelope. It imports no package of the repository. |
| `skills` | The coding-agent skills (`af-triage`, `af-fix`, `af-bug-hunter`, `af-code-quality`, `af-code-simplifier`) and a Go package that embeds them, so the briefs carry the same skill text a person installs. |
| `containers/sandbox`, `containers/tools` | Containerfiles of the sandbox image and of the tools image (the sandbox image with `af` in it). |

**Layering rules.** The packages `project`, `checks`, `conform`, `policy` and
`repomap` return plain Go results and errors. They never import
`internal/toolio`, never call `os.Exit` or `log.Fatal`, and never panic on bad
input. `internal/triage` imports
none of the shell, box or brain packages. The commands in `internal/afcmd` map
package errors onto the envelope's failure categories.

Two files bind to agentkit's hands, so that a change there has one place to
follow: `internal/policy/handsguard.go` (the guard, used as a floor under the
policy rules) and `internal/repomap/handsoutline.go` (the outline behind the
`Outliner` interface).

## The command shell

Every `af` command runs inside one shell, `internal/toolio`, which implements
the contract described in [cli.md](cli.md#the-envelope) once so that commands
do not. A command is a `toolio.Command` registered on a `toolio.App` under a
command path of one or more words. It declares the shared flags it opts into
(`--dir`, `--context`, `--dry-run`, `--repo`, `--timeout`), its default
timeout, whether it needs a run directory, and whether it reads stdin raw
(`RawStdin`).

The shell does the following around each command:

- **Parsing.** It picks the longest registered path that matches the words
  before the first flag, accepts flags before or after the positional
  argument, and answers `-h`, `--help` and `--version` before resolving
  input, building a forge client or creating a run directory.
- **Input.** It resolves the positional argument (stdin, an issue URL, a
  file, or text), reads the issue thread through the forge client, and
  truncates at 131072 bytes. A command that reads its own input reports it
  with `Run.SetInput`.
- **The forge client.** It builds one `issuex` client per run. Under
  `--dry-run` that client passes reads through and turns every write into an
  error wrapping `toolio.ErrDryRun`, recorded as a planned side effect.
  Because it is the only client a command gets, no write can reach the forge
  in a dry run whatever the command does. A command that commits or pushes
  honours `--dry-run` itself.
- **Deadlines and signals.** `--timeout` and SIGINT, SIGTERM or SIGHUP cancel
  the command's context. A command that has not returned 10 seconds later is
  abandoned and the shell prints the failure itself. A command may ask for a
  longer grace (`Command.Grace`); `revert-check` asks for 10 minutes so that
  it can put the tree back.
- **Outcomes.** A command returns an outcome (`done`, `unverified` or
  `needs_human`) or an error. A `*toolio.Failure` becomes `error` with its
  category; any other error, and a recovered panic, becomes
  `failed`/`internal`. The panic's stack goes to stderr and to `panic.txt` in
  the run directory. An outcome that contradicts itself (a question with the
  wrong status, more than 26 options) is also `failed`/`internal`.
- **Secrets.** `Run.Redact` registers secrets (the service credential, a
  workspace token). The shell removes every registered value of 8 bytes or
  more from the envelope, its copy and every event.
- **Output.** It writes exactly one envelope to stdout and the same bytes to
  `envelope.json` in the run directory first. It writes nothing to stderr
  except a panic's stack.

The shell also runs commands without a command line: `App.Invoke` takes a
`toolio.Call` and returns the envelope as a value beside its encoding. The
worker uses this to run the loops in process (see [The worker](#the-worker)).

`toolio.SchemaVersion` is the envelope's interface version; its rules are in
[cli.md](cli.md#the-envelope).

## The run directory and ledger

A command that declares a run directory gets `<dir>/.agent-fox/run/<id>/`
before it runs, under `--dry-run` too. `<id>` is `YYYYMMDD_HHMMSS_<6 hex>`:
the UTC time and three random bytes, drawn again (up to five ids) if the
directory exists. `<dir>/.agent-fox/.gitignore` holds `*`; it is created
exclusively when missing, so concurrent runs cannot clobber it, and the
repository's own `.gitignore` is never edited. The user-facing layout is in
[cli.md](cli.md#the-run-directory).

| File | Written by | Content |
|---|---|---|
| `envelope.json` | the shell | The envelope, byte for byte what stdout got. |
| `events.jsonl` | the shell and the command | `{seq, ts, run_id, type, data}` per line, flushed as written; `run_start` first, `run_end` last. |
| `ledger.jsonl` | the command, via `RunDir.Record` | Append-only `{seq, ts, kind, data}` per line. The shell records nothing in it. |
| `report.md` | the command, via `RunDir.WriteReport` | A report, listed as an artifact. |
| `panic.txt` | the shell | The stack of a command that panicked. |
| `brief.<attempt>.md`, `hook.json` | `brain.Execute` | The brief a brain was given and the hook configuration placed with it. |
| `submission.json` | `brain.Execute` | The accepted submission, rewritten from what the host checked. Absent when there is none. |
| `transcript.<attempt>.jsonl` | an in-process brain | Tool calls that reached the gate, `{seq, tool, args, decision, reason, ok, result}`, with `args` and `result` cut to 4096 bytes, and skipped script steps. `af` never reads it. |
| `brain-panic.<attempt>.txt` | `brain.Execute` | The stack of a brain that panicked. |
| `<brain>.<attempt>.json`, `<brain>.<attempt>.stderr` | a driver that runs in a box | Its stdout (up to 1 MiB) and the last 64 KiB of stderr, copied from the box. |

`toolio.OpenRunDir(dir, id)` reopens an existing run and refuses an id that
does not have the form of a run id before making any path from it.

**Events.** The shell emits `run_start` (`{tool, argv, input_kind}`), one
`warning` per warning, and `run_end` (`{status, exit_code, duration_ms}`),
also for an invocation refused before its command ran. A command emits its own
types through `Run.Emit`. A command that runs a brain emits `step`,
`heartbeat` (every 30 seconds while the brain runs) and `tool_call`
(`{brain, tool, decision, reason, ok, duration_ms}`). Tool-call events carry no
arguments or results, because events may go to an audit log and arguments can
hold file contents.

**Ledger entries.** A command that runs a brain records:

| Entry | When |
|---|---|
| `box_open {provider, image, network, placement}` | The box opened. |
| `box_sync {head}` | The host tree was synced in (short `HEAD`, or `""` with no commit). |
| `box_export {path}`, `box_close {provider}` | The tree came out; the box closed. |
| `brief_placed {attempt, bytes, sha256, prefix_sha256}` | The brief was written into the box. |
| `brain_start {brain, attempt}` | The brain started. |
| `brain_end {brain, stop_reason, submitted_claim, cost_usd, cost_source, turns, duration_ms, input_tokens, output_tokens}` | The brain stopped. |
| `box_probe {binary, ok, version}`, `brain_process {brain, exit_code, duration_ms}` | For a driver that runs as a process in the box. |
| `submission_accepted {attempt, sha256}`, `submission_invalid {attempt, problems}`, `submission_stale {attempt, file_attempt}`, `submission_absent {attempt}` | What the host found in the box after the brain. |

`af triage` adds `triage.input`, `triage.brief`, `triage.submission`,
`triage.question` and `triage.land`. `af fix` adds `fix.brief`, `fix.run`,
`fix.question`, and per round `apply {written, deleted, ignored}`, `measure
{verdict, closes, changed, reverted, after_gate_id, revert_outcome}` and
`refusal {kind}`.

## Fact commands internals

`af lang detect`, `af gate run` and `af revert-check` answer a question about
the tree and change nothing that lasts. Each is a thin command file in
`internal/afcmd` over a package that knows nothing of the shell, so the loops
call the same code in process.

| Package | Command | Holds |
|---|---|---|
| `internal/project` | `lang detect` | Languages, the gate table, suite forms, programs, and test-path classification. |
| `internal/checks` | `gate run` | The file view, the tree hash, the gate engine and its cache, the per-check comparison. |
| `internal/conform` | `revert-check` | The revert check. |

**The file view.** `checks.FileView` is git's own view of `--dir`: tracked
files plus untracked files that are not ignored, read NUL-separated so no name
is quoted. Outside a work tree, or where `git` cannot run, it is a directory
walk. It never includes `.git/` or `.agent-fox/`. Detection, the tree hash and
the repository map all read this one view, so they agree on what the
repository is. Which checks are selected depends only on the view, never on
which programs are on `PATH`; only the `available` field does.

**The tree hash** is a SHA-256 over the file view in order. Each file
contributes its kind, executable bit, path and the SHA-256 of its content. A
symlink contributes its target; another non-regular file its type; an
unreadable file a fixed marker. A submodule or nested repository is one
directory entry. The hash covers content only: not `HEAD`, not the checkout's
location, not ignored files. So a gate that a brain runs inside a box is a
cache hit for the gate `af` runs on the same tree, provided both list files the
same way (git's view and a directory walk are different views).

**The gate cache.** The key is the SHA-256 of the tree hash, the canonical JSON
of the table's checks (`name`, `argv`, `kind`, sorted keys, no whitespace) and
the runner mode `reduced`. The result id is the first 12 hex characters. The
same tree and table give the same key on every machine. Results live in
`.agent-fox/gate/<id>/`: the per-check logs, then `result.json`, written
through a temporary file and a rename. There is no lock: concurrent runs leave
the last writer's complete file. Only completed runs are stored. A stored
result is reused unless it skipped a check that can run here, or its
`result.json` belongs to another key.

**Running checks.** Checks run one at a time in table order, each with a
20-minute limit, under the "reduced" runner: the environment loses credential
variables (vendor and cloud prefixes such as `ANTHROPIC_`, `OPENAI_`, `AWS_`,
`GOOGLE_`, `AGENTKIT_`, and names ending in `_TOKEN`, `_SECRET`, `_KEY`,
`_PASSWORD`, `_PASS`, `_PWD`, `_PAT`, `_AUTH` or `_CREDENTIALS`), because a test
suite is repository code. `PATH`, `HOME`, `LANG`, `TMPDIR`, `TERM` and a few
cloud region and profile names stay. A timed-out check is killed with its
process group. If the tree hash after the checks differs from before, the
result is kept under the earlier hash with a warning.

**The revert check.** `internal/conform` answers "do the tests of this change
fail without the rest of it?" It takes care never to lose the change:

1. It refuses if `.agent-fox/revert.patch` exists. The patch file is the claim
   on the checkout; a leftover one means an earlier check was killed.
2. It lists changed paths against `--since` (staged or not, plus untracked,
   not-ignored files) using a copy of the index, so it never writes the real
   one. Each path is a test path or not, by the rules in `internal/project`.
3. It returns early for nothing to measure (`no_changes`, `no_code_changes`,
   `no_test_changes`, or no test check in the table).
4. It takes the full gate for the current tree from the cache, or runs it. A
   red or unavailable test check makes the outcome `inconclusive`.
5. It builds the non-test change as a binary patch on a private index read
   from the base (`git read-tree`, `git add --all` of those paths, `git diff
   --cached --binary`), ignoring submodules, and writes it exclusively to
   `revert.patch`. Then `git apply --reverse` takes the change out of the
   work tree only. If the reverted tree's test checks differ from the ones
   measured (for example the change added the Makefile), the outcome is
   `inconclusive`. Otherwise only `test` and `check` checks run, cached by the
   reverted tree's hash.
6. It applies the patch again with `git apply`, on a context that nothing
   cancels, so a signal, the deadline or a panic still puts the change back.
   If a path changed meanwhile, `git apply` refuses and the command fails with
   `restore`, keeping the patch. A check may have rewritten `.git/index` with
   the same entries; the original bytes are written back through
   `.git/index.lock`. Finally it verifies the tree hash and the index bytes
   match what they were, and removes the patch.

The shell gives the restore 10 minutes after a signal or the deadline. A
process killed with SIGKILL leaves the patch, and the next check refuses until
someone looks: when `git apply --check .agent-fox/revert.patch` passes, the
tree is without the change and `git apply` restores it. Checks must not leave
new files outside ignored paths, because that changes the tree hash and counts
as a restore failure.

## Boxes and brains

The agent commands run a brief through a **brain** inside a **box** and take
back one **submission**. `internal/box`, `internal/brain` and
`internal/submission` hold the pieces. The user-facing flags are in
[cli.md](cli.md#sandboxes-and-brains).

**The box** wraps agentkit's `sandbox` module. A box is created from a `Spec`
(image, explicit environment, network mode, resource bounds, TTL). `Sync`
fills its workspace from the host checkout, `Tools` returns proxies whose
every call runs inside, `Export` brings the tree out, and `Close` destroys it.
The box has no host mount and inherits no environment. `box.Session` adds what
a brain needs on top: run-file writes under `.agent-fox/`, a read-only `FS`, a
single-command `Exec`, and `With`, which closes the box on every path. All
uses of the sandbox module's non-type names sit in one adapter file,
`internal/box/adapter.go`.

What goes in and out:

- The checkout goes in with its `.git`. It must be the top level of a work
  tree with its own `.git` directory; a linked worktree or submodule is
  refused, because a commit in the box would write the repository it links to.
- Files ignored by the checkout's rules stay out, except ignored files that
  git tracks, so git in the box sees what git on the host sees.
- A `replace` directive of the checkout's `go.mod` whose target is a local
  directory outside the checkout (a sibling clone, as `agentkit-go`'s is for
  development) is copied into the box under `.agent-fox/vendor/`, and the
  box's `go.mod` is rewritten to reach it there, so a build inside the box
  resolves the same modules a build on the host does.
- Anything in the box's `.git/config` that could carry a credential (remote
  URLs, extra headers, credential helpers, URL rewrites) is removed when the
  box opens and after each sync.
- The tree comes out without `.git` into a temporary `af-export-<run_id>-*`
  directory, which `Close` removes.

**The brain interface.** One Go interface, so the loops are a loop over briefs
and name no vendor:

```go
type Brain interface {
    Run(ctx context.Context, b Brief) (Outcome, error)
}
```

A `Brief` carries the run id and attempt, the submission `Kind`, the brief
text as `Prefix` and `Task` (the prefix is byte-identical across attempts so a
provider can cache it), the tool allowlist, the hook configuration (run id,
protected paths, allowed programs), the box session, the environment for a
brain started inside the box, the cost, turn and time bounds, a transcript
path, and callbacks for tool calls and warnings. The `Outcome` carries the
brain's claim that it submitted, its stop reason (`submitted`, `ended`,
`budget`, `turns`, `error`), cost, cost source, tokens and turns. Whether a
submission exists is decided from the file alone; the claim only feeds the
`submission_claim_mismatch` warning.

**Registries.** `brain.Registry` holds entries with a name, cost source, the
host credential names a driver in the box needs (`ANTHROPIC_API_KEY` for
`agentkit`), and whether the driver runs in the box. `box` keeps a registry of
providers with flags for whether a provider isolates, calls programs by name
from its image, supplies credentials itself, and is remote (so an unreachable
box is an outage worth retrying).

**Executing a brief.** `brain.Execute` runs one brief in a held box. It draws
a 16-hex nonce, writes `.agent-fox/.gitignore`, `brief.md`, `run.json`
(`{run_id, kind, attempt, nonce, prefix_bytes}`) and `hook.json` into the box,
and `brief.<attempt>.md` and `hook.json` into the run directory. It runs the
brain, adds its cost to the run's tally (also for a failed or panicked
attempt, since the money was spent), and then decides from the box's
`submission.json`. One `Execute` runs at a time in a box.

### Providers

| Provider | Isolates | What it is |
|---|---|---|
| `podman` | yes | A container of `AF_SANDBOX_IMAGE`, default `quay.io/agentfox/tools:latest`. The default provider on every OS. |
| `local` | no | Tools run in the `af` process with its privileges, on a copy of the tree. Each box raises `box_unconfined`. |
| `gateway` | yes | A box on hub's sandbox gateway, built for one attempt from a lease (`box.GatewayEntry(url, token)`). It lives only in that attempt's registry, never in `box.Default`, so `--sandbox gateway` is a usage error. |

`af` never falls back from `podman` to `local`: if the default provider cannot
open, the run fails with a hint naming `--sandbox` and `AF_SANDBOX`. All
providers follow the same rules: `.git` in, never out, commits on the host.

### Where the brain runs

`brain.PlacementOf(brain, provider)` is the one function that decides, and
`brain.BoxRequest` turns the placement, provider and tools into the request
for the box.

| Brain | `podman`, `gateway` | `local` |
|---|---|---|
| `scripted` | host | host |
| `agentkit` | box | host |

*Host* means `af`'s own process drives the brain over the box's tools
("borrowed hands"): the model and the loop stay on the host and only tool
results cross the boundary. *Box* means a process started inside the box runs
the driver, keeping model traffic and tools on the same side.

The placement fixes the box's `Spec`:

- **Environment.** `Spec.Env` holds credentials and nothing else: the host's
  `ANTHROPIC_API_KEY` for a driver in a podman box, nothing for a brain on the
  host. No forge token ever enters a box. Under `podman` the key is on the
  engine's command line (as `--env`) while the box lives, readable by other
  users of the machine. A gateway box gets an empty `Env`: the lease's profile
  supplies the secrets.
- **Network.** `none` for a brain on the host whose brief has no tool that
  runs a command (all of `af triage`); `open` otherwise, since a driver in the
  box needs its vendor and a gate may fetch modules. Podman offers all or
  nothing, so a driver in a podman box has the whole network; only a gateway
  whose backend filters egress narrows it. Under `local` nothing is enforced
  and the ledger records `host`.
- **Image and lifetime.** The image applies to `podman` only. The TTL is the
  command's remaining deadline plus ten minutes. Labels are `af.run_id` and
  `af.tool` (plus `af.job` and `af.attempt` on a gateway).

The `box_open` ledger entry and the `box` field of the fix result record what
was built.

Vendor CLIs that bring their own hands (Claude Code, Codex) are not brains of
`af`. One driver, one cost model and one guard keep the design small.

### Brains that run as a process

A driver inside an isolating box runs as `af <agent> run --run-id <id> --dir
<root> --budget … --max-turns … --timeout …`, with `AF_MODEL` on the command
line. `brain.Process` starts it:

- **Probe.** Once per box, before the first process, it runs `af --version`
  and checks that `HOME` is set and writable. A failure is a `sandbox` failure
  that is not retryable and names the binary, provider and image (or the
  sandbox profile, for a gateway). A different version or commit raises
  `box_af_version_mismatch` and the run continues, since the host checks every
  submission again. The box remembers the probe, so measuring and the brain
  share it.
- **Binary.** Under `local`, `af` is the running executable by absolute path,
  with its directory first on `PATH`. Under `podman` and a gateway it is the
  image's `af`, called by name.
- **Environment.** The command runs under `env -i` with only `PATH`, the box's
  own `HOME`, the host's values of `LANG`, `LC_ALL`, `TZ`, the proxy variables
  (`HTTPS_PROXY`, `HTTP_PROXY`, `NO_PROXY`, either case), `SSL_CERT_FILE`,
  `SSL_CERT_DIR` and `NODE_EXTRA_CA_CERTS` where set, `TMPDIR` when the host
  has one, and the credentials of `Spec.Env` referenced by name, so no secret
  appears in the command text or the ledger. In a box that supplies its own
  credentials the references are to the names the brain lists. Every argument
  is single-quoted.
- **Output.** Stdout and stderr go to `.agent-fox/run/<id>/<brain>.<attempt>.json`
  and `.stderr` in the box and are copied to the run directory afterwards (up
  to 1 MiB of output, the last 64 KiB of errors). The driver's cost, tokens
  and claim come from that envelope; if it cannot be read, the cost is 0 with
  `brain_usage_unavailable`. Nothing in the output decides whether there is a
  submission.
- **Time.** The process and its group are killed when the brief's time or the
  command's remaining time runs out.

The host reads files from the box with the list tool (at most 500 entries per
directory) and a single-file export (at most 32 MiB, exact bytes), not with the
read tool, which shows capped lines and fails on non-UTF-8 files. Symbolic
links are not followed.

`af triage run` and `af fix run` are what the process executes. They read the
brief and `run.json` placed in the box, run the brain in place over the
directory, and submit into the run directory under the host's attempt and
nonce.

### The submission protocol

A brain hands back exactly one JSON object. One function writes it,
`submission.Submit`, which validates before it writes and never leaves a
refused file. Inside a box the `agentkit` brain reaches it through its
terminating tool (`file_diagnosis`, `submit_fix`); the `scripted` brain through
a `submit` step.

The file is `.agent-fox/run/<id>/submission.json` in the box:
`{kind, run_id, attempt, nonce, submitted_at, payload_sha256, payload}`. After
the brain ends, the host reads it (at most 2 MiB) with `submission.Check` and
accepts it only if:

- it parses and carries this run's kind, run id, attempt and nonce;
- `payload_sha256` is the hash of the payload;
- the payload is at most 1 MiB of valid UTF-8, one JSON object, and passes the
  kind's own check against the box's tree (a panic in that check counts as a
  bad payload).

The host then writes `<run dir>/submission.json` from what it checked and
nothing else. A file from an earlier attempt (another nonce) counts as no
submission, so a stale file left by a sync cannot be mistaken for a new one.
The last valid submission wins; a refused one replaces nothing. When a new
attempt starts, an earlier accepted `submission.json` in the run directory is
removed, as is any file there that `af` did not write.

Since a `local` box is not isolated, a stray process of the brain could still
rewrite the host file afterwards. Consumers take the payload from the run's
result, or check the file's hash against the ledger's `submission_accepted`.

A valid submission beats an error or panic of the brain (`brain_error`
warning). Without one, the run fails as `timeout`, `brain`, `budget` or
`no_result`.

**The tool gate.** Every tool call that a brain on the host makes passes one
gate before it runs: the tool must be in the brief's allowlist (the
terminating tool always is), and [the tool policy](#the-tool-policy) must allow
it, with the brief's hook as configuration and the checkout's suite. Paths in a
write are normalised as the box's tools will resolve them (quotes, a leading
`@`, a `file://` prefix and surrounding spaces removed). A denied call is not
run; its reason becomes the brain's next turn. The `agentkit` brain installs
the gate as the driver's `Guard` and `After` hooks. A call to an unknown tool,
or with arguments that do not fit the schema, is answered by the driver and
never reaches the gate. The hook always protects `.agent-fox/`, where only
`af` places the brief, `run.json` and the submission.

### Measuring in the box

`af fix` measures where the brain works. The checks are repository code, and
the brain writes tests, so they run in the box of the run through its `af`
(the image's under `podman` and a gateway, the running one under `local`), and
never on the host. The box opens before the checks are detected and serves the
whole run. `fix.BoxMeasurer` syncs the host's tree in, places the run's gate
table, runs `af lang detect`, `af gate run` (baseline and after) and `af
revert-check` through `brain.Process`, and reads each result from the envelope
the command wrote. Gate results are copied to the host's `.agent-fox/gate/<id>/`
for the comparison with the baseline.

The file view, repository map, path validation, applying the export, the
commit and the push stay on the host. An `af` in the box that is missing or
fails, an envelope of another major schema version, or a result that does not
decode is a `sandbox` failure that is not retryable. A box lost while
measuring is a retryable `sandbox` failure. Measuring has no timeout of its
own; the run's deadline applies.

### The tools image

`containers/tools/Containerfile` builds `quay.io/agentfox/tools` on this
repository's sandbox image (`ARG BASE_IMAGE`). It builds `af` from the
repository, cloning agentkit-go at `ARG AGENTKIT_REF` where the `replace`
directive in `go.mod` expects it, and keeps `WORKDIR /workspace` and `USER
1001:0`. `make build-tools-container` tags it with the version and `latest`;
`make build-containers` builds the sandbox image first.

## The tool policy

`internal/policy` decides whether a brain may make a tool call, in process
(`policy.DecideAt`), from the policy in `.agent-fox/guard.json` (keys in
[configuration.md](configuration.md#guardjson)). It reads and decides; it
changes nothing. Relative paths are resolved against the brain's session
directory and the checkout.

The policy covers three kinds of tool:

- **Shell tools** (the hands' `execute`). The command is read as a shell would
  read it, by the hands' reader (`guard.Commands`), and every command it would
  run is judged: commands joined by `&&`, `||`, `;`, `|`, `&` and newlines;
  commands in groups, `$(…)`, backticks and `<(…)`; substitutions inside
  `$((…))` and `${…}`; heredoc bodies that expand them; the script of `sh`,
  `bash` or `zsh -c '…'` (whatever options precede it); `eval`, `trap`; and
  the commands `find -exec` runs. Words are read as the shell passes them:
  `$'git'`, `g\it`, `{git,push}` and `git${IFS}push` are `git` and `push`, and
  a program name's case does not matter. Quotes are removed before braces,
  `$IFS` and `$'…'` are read, so a word may be read as more than the shell
  would make of it, which can only deny more. Assignments, the wrappers `env`
  (including `-S`), `time`, `timeout`, `nice`, `nohup`, `command`, `builtin`,
  `exec` and `xargs`, `function NAME`, `coproc` and leading keywords are
  skipped to find the program. The hands' guard looks at the whole line first;
  then the rules apply to each command in order and the first denial wins.
- **Write tools** (`write_file`, `edit_file`; the path is `path`). A path
  under a protected path is denied (`protected_path`), and so is one outside
  the checkout that is not under the system temporary directory
  (`outside_dir`). A call with no path is denied (`unparseable`).
- **Every other tool** is allowed. The brief's allowlist decides which tools
  exist.

| Rule | Denies |
|---|---|
| `unparseable` | A command the reader cannot read with certainty (unbalanced quotes, an unterminated `$(` or `${`, an unmatched `)`, a heredoc without its terminator, a `case` statement, a `()` function definition, `for ((…))`); nesting deeper than 64; a brace expansion of more than 4096 words; a line over 64 KiB (the guard's cost grows with the square of the length; long content should be written with a write tool); a shell call with no command; and a shell that reads commands from its input (`echo git push \| sh`, `sh <<< '…'`, `bash -s`, bare `bash`, `source <(…)`). |
| `hands_guard` | What the hands' guard refuses: assigning a variable that changes which program runs or what is loaded into it (`PATH`, `LD_PRELOAD`, `BASH_ENV`, …), and a line that runs no program (an assignment alone, `((i++))`). |
| `git_mutation` | `git` with any subcommand except `status`, `log`, `diff`, `show`, `blame`, `ls-files`, `ls-tree`, `rev-parse`, `rev-list`, `grep`, `describe`, `cat-file`, `shortlog`, `merge-base`, `name-rev`, `for-each-ref` and `show-ref` (`branch` and `remote` only to list); a global `-c`, `--config-env` or `--exec-path=`; git configuration from the environment that runs a program (`GIT_CONFIG_*`, `GIT_EXTERNAL_DIFF`, `GIT_EXEC_PATH`, `GIT_ASKPASS`, `GIT_SSH`, `GIT_SSH_COMMAND`, `GIT_PROXY_COMMAND`, `GIT_EDITOR`, `GIT_SEQUENCE_EDITOR`, `GIT_TEMPLATE_DIR`), whether set in front of the command, given to `env` or exported earlier in the line; `git grep -O`/`--open-files-in-pager`. The reason says `af` makes every commit, branch and push. |
| `suite_command` | A command equal to a suite form after removing every token that starts with `-`, the values of options known to take one (`go test -timeout 5m`, `make -j 4`, `make -f Makefile`, and the like for `go`, `make`, `cargo`, `pytest`) and `NAME=value` words, with `$(go list ./...)` read as `./...`. So `make check`, `make -j4 check` and `go test -count=1 ./...` are the suite; `go test ./internal/x -run TestY` is not. The reason points to `af gate run`, whose result is cached by tree hash and reused when the change lands. A flag that selects tests without its own word (`mvn -q test -Dtest=X`) is not distinguished from the suite. |
| `af_command` | Any `af` command except `lang detect`, `gate run`, `revert-check` and `--version`. The rest belongs to the loop. |
| `program_not_allowed` | With `allow_programs` set, a program not on the list, as spelled (`/usr/bin/ls` is not `ls`). `af` and `git` are always allowed by name or absolute path (`./git` is a file of the checkout), and so are builtins that start no program (`cd`, `true`, `[`, `test`, `echo`, `printf`, `read`, `export`, `set`, `for`, `case`, `eval`, `trap` and similar), whose own commands are judged in turn. |
| `protected_path` | A write into a protected path: through an output redirection; through an argument of `rm`, `rmdir`, `unlink`, `shred`, `mv`, `cp`, `install`, `rsync`, `tee`, `touch`, `mkdir`, `mkfifo`, `truncate`, `chmod`, `chown`, `chgrp`, `patch`, `ln` (every operand), `sed -i`, `perl -i`, `dd of=`, `find -delete` or `-fprint`; or through an option naming an output file (`-o`, `--output`, `--out`, `--outfile`, `--output-file`, `-coverprofile` and the other `go test` profiles). Paths are resolved against the checkout and any earlier `cd`, with links followed, case-insensitively (macOS file systems fold case), and may start with `~`, `$HOME` or `$PWD` or contain globs, which are matched as the shell would. `rm`, `mv` and `find -delete` also refuse a directory above a protected path (`rm -rf .`). Protected path entries are cleaned before matching (`./docs/` protects `docs`). |
| `outside_dir` | A write tool call to a path outside the checkout and the system temporary directory. |

**Prevention, not a boundary.** The policy reads a command; it does not run
it. Out of its sight are: a program named by a variable (except `$IFS`); a
shell function or alias named like a program; a script a command writes and
then runs; programs that run others (`ssh`, `sudo`, `watch`); a variable set
earlier in the line and used later in a path; and what an interpreter writes
(`python -c`, `awk`). The tool `run_command`, which takes an argument list, is
not treated as a shell tool. The value of an assignment in front of a program
is not read, only its name. What actually holds is the box and the host's
decision, which accepts only a submission that validates and only measures
what came out of the box.

Without `.agent-fox/guard.json`, every decision detects the repository to find
the suite (the work of `lang detect`), so a loop writes `guard.json` into the
box once before its rounds.

## The repository map

The brief of a triage or a fix carries a map of the repository that fits a
token budget (bytes divided by 4, rounded up; 4000 tokens for triage). Making
it reads files and runs nothing.

The map covers the file view. Each directory with files is a header `path/`
(the root is `./`), in path order: the root first, a directory before its
subdirectories, files by name. A file is indented two spaces, followed by its
declarations as `<kind> <name> L<line>`, joined by ` · `:

```
./
  README.md
  main.go: func main L3 · type Server L5
pkg/
  util.go: func Helper L3
  util_test.go: func TestHelper L3
```

Declarations come from the hands' outline: Go always, and Python, JavaScript,
TypeScript, Rust, Java, Kotlin, C#, Ruby, C, C++, PHP, Scala, Lua and shell when
the binary is built with cgo (as `make build` does). Kinds are `func`,
`method`, `type`, `class`, `interface`, `enum`, `trait`, `const`, `var`,
`module` and `macro`; a method is named with its type (`Server.Close`). Files
the outline cannot read (unknown language, binary, unparseable, over 1 MiB)
are listed by name. Control characters in names become `?` and invalid UTF-8
becomes U+FFFD, so the map is the same text after JSON encoding.

The map is the first level of this ladder that fits the budget:

| Level | Content |
|---|---|
| 1 | Every file with every declaration. |
| 2 | As 1, but test files have no declarations. |
| 3 | Test files have none; other files have their first 20, 10, 5, 2 or 1 declarations (the first cap that fits), then `· … +N more`. |
| 4 | Every file by name only. |
| 5 | Directories only, as `path/ (N files)`. |
| 6 | As many level-5 lines as fit, then `… N more directories`. |

The outliner is not called when level 4 is already too large or the view has
more than 5000 files; the warning `map_outline_skipped` says so. The same tree
and budget always give the same bytes, whatever order files are read in, so
the brief's prefix stays cacheable across attempts.

## Triage internals

`af triage` is a brain run with read-only hands. It shares everything with
`af fix`: the file view, the submission protocol, `brain.Execute`, the
envelope, the run directory and the ledger. What it adds is small: a mandate,
a skill, a validator, an issue-body format and a landing.

| Path | Holds |
|---|---|
| `internal/triage/` | The diagnosis and its validator (`Validate`, which checks every path against the file view and collects every problem), the body (`Render`, `Extract`), the brief (`BuildBrief`), the preflight, the landing (`Land`), the submission kind and the tool allowlist. It imports `issuex`, `submission`, `checks`, `repomap`, `agentdir` and `skills`, and nothing of the shell, box or brain. |
| `internal/afcmd/triage.go` | The two commands: `triage` (the loop) and `triage run` (what a box executes). It owns the run directory, the forge client, the box and the envelope. |
| `skills/af-triage`, `skills/skills.go` | The skill. The `skills` package embeds every `af-*` file, and `skills.Triage()` is what the brief carries, so the skill a person installs and the one the brain reads are the same file. |

**The loop.** A preflight that spends nothing, the brief, a bounded brain run,
the host's second look at the diagnosis, and the landing. The brain has
read-only tools and one terminating tool, `file_diagnosis`, whose schema is the
diagnosis. A diagnosis citing a file that does not exist is refused as the
brain's next turn, with files of the same name as hints. After the brain, the
host checks the accepted diagnosis against its own tree once more. The brief's
prefix is the mandate, the skill, the repository map and the report; the task
block holds everything particular to the run, so a resumed run has the same
prefix.

**The issue body** is the contract between `af triage` and whatever reads the
issue next (`af fix`, or `af triage` itself). Its format is in
[cli.md](cli.md#af-triage). The original report sits between two markers and
is exactly the bytes between the begin marker and the newline before the end
marker. So `Extract(Render(d, s)) == s` for every string `s`, a second triage
replaces the old diagnosis and keeps the first report byte for byte, and a body
`af` did not write is wholly the original. A body that starts with the marker
but has been edited in a way that would lose words is also taken whole as the
original, with a warning. CRLF line endings (a browser's save) are read as LF.
No diagnosis string may contain `<!-- agent-fox:`, so a diagnosis cannot forge
a marker.

## Fix internals

`af fix` is the agent command whose brain writes. It has the shape of triage,
and adds the part triage never needed: the brain's changes come out of the box
and are judged before anything is committed.

| Path | Holds |
|---|---|
| `internal/fix/` | Preflight (`Preflight`, `CheckLanding`), run state (`State`, `fix.json`), the brief (`BuildPrefix`, `TaskBlock`, `NewBrief`), the `fix_report` kind and validator, `Apply`, `Measure`, `Land`, `Park`, the pull request body (`RenderBody`), the loop (`RunLoop`), its ending (`Finish`) and the result. |
| `internal/afcmd/fix.go`, `fix_start.go`, `fix_run.go` | The two commands: `fix` (the loop, with the start of a run in `fix_start.go`) and `fix run` (one round, what a box executes). |
| `skills/af-fix` | The skill, embedded and carried by the brief (`skills.Fix()`). |

**The loop.** The preflight runs first: a clean tree on a branch, the landing's
needs, a brain that can be made. The full hash of the base is recorded as
`base_head`. Then the box opens and the checks are detected and the baseline
measured in it (see [Measuring in the box](#measuring-in-the-box)). `fix.json`
holds the run's state, written last and atomically. One box serves every
round. Before each round after the first, the box is synced from the host's
tree, which carries the change already applied, and `gate.json` and
`guard.json` are placed in it: the gate table the run is pinned to (so the
brain's own `af gate run` measures what `af` will) and the tool policy. The
brain edits files and submits one report with `submit_fix`, or a question.

**Apply.** The export is mirrored onto the checkout, uncommitted. Files the
export lacks are deleted first, then new and changed files, modes and links are
written, through an `os.Root` of the checkout so no path can escape. Left alone
(and counted as ignored): `.git/` and `.agent-fox/` in any case (on a
case-insensitive file system an export could otherwise write a hook the commit
would run), ignored paths, anything that is not a file or link, and everything
under a submodule. A file git tracks but a rule ignores is never deleted, since
the export never has it. A round without a report, or with a question, applies
nothing. An export with nothing applicable fails as `sandbox`. The host writes,
never the box.

**Measure.** In order:

1. The checkout must still be the base at `base_head`; a commit or a switched
   branch is a `git` failure, because `af` makes the commit that counts.
2. Changes under protected paths are put back to the base's content or
   deleted, and reported.
3. A tree with nothing left is refused as `no_change` without running the gate.
4. The report's file list is compared with what git sees changed (a mismatch
   warns).
5. The gate runs on the pinned table and is compared with the baseline check
   by check, so a check that already failed is not counted against the change.
6. Only on a verdict that may land does the revert check run. Only a proven
   change makes the commit say `Closes`; anything else says `Refs`.

A refusal is recorded and becomes the next round's task block, with the last
4 KiB of each failing check's output, in the same box and tree. Another round
starts only while cost and turns remain and the time left covers the time
kept for measuring (three times the baseline's duration, plus a minute) and a
minute for the brain.

**Land or park.** A landable change gets a branch `fix/<N>-<slug>` from
`base_head`, one commit with the repository's hooks, a push with retries, and
the pull request, whose body is rendered from facts: what `af` measured is
never cut, and what the brain reported is quoted. The checkout always ends on
the base, clean. A change that cannot land is parked: a `wip:` commit made
without hooks, so nothing can lose it, pushed and offered as a draft that says
`Refs`. An interrupted run parks what it applied on a local branch, on a
context the signal does not reach, within 10 seconds.

The brief's prefix (mandate, skill, report, gate and baseline, map, report
schema) is byte-identical for every round and every resume over the same report
and tree; its hash is in `fix.json`, so a provider that caches a prefix serves
the whole run from one write. Under the service, the jobs layer decides whether
a run that ended without a landable change gets another attempt; the fix loop
has no attempts of its own.

## The service

`agent-fox` is a separate binary, not a command of the `af` shell: a server's
lifecycle is flags, signals and logs, not one envelope. It imports `toolio`
only for the version document. Its command line and logs are in
[cli.md](cli.md#the-agent-fox-command), its API in [api.md](api.md) and its
configuration in [configuration.md](configuration.md#the-agent-fox-service).

**The process.** `agent-fox [--config PATH]` loads `config.toml`
(`internal/service/config`: seven keys, unknown keys refused), reads the key
file (`internal/service/secrets`), opens the state file, builds the hub client
and HTTP server, binds the listener, and starts the jobs layer, the worker, the
label poll and the reconciliation pass. It runs until SIGINT or SIGTERM, then
drains requests in flight for up to 30 seconds, stops the poll before the jobs
layer, closes the database and exits 0. A failed start is one `ERROR` record
and exit 2 (configuration, key or database) or 1 (the listener).
`cmd/agent-fox` is wiring only.

**The state file.** One SQLite file (`modernc.org/sqlite`, pure Go, WAL mode)
behind `database/sql` (`internal/service/store`). Every table it owns is named
`af_…`, and migrations are recorded in `af_schema_migrations`, never in
`PRAGMA user_version`, so `aflib/jobqueue`'s tables share the file. There is
one migration, which makes the whole schema. Subscriptions live in
`af_subscriptions`: slug, sealed token, the hub user behind the token, who
subscribed it, and timestamps. A token is sealed with AES-256-GCM under the
deployment key, with `agent-fox/subscription/<slug>` as additional data, so a
ciphertext is bound to its row. It is the only secret the file holds; a token
that does not open with the key reads as `unreadable` and does not stop the
service.

**Hub as identity provider.** The service has no users, tokens or tenants of
its own. Every route under `/api/v1` takes a bearer token and asks hub
(`internal/hub`, the one place that knows hub's paths and objects) whom it
belongs to (`GET /api/v1/user`) and whether that person sees the workspace
(`GET /api/v1/workspaces/{slug}`). Answers are kept for `hub.auth_cache_ttl`
(at most 15 minutes) in two bounded caches (`internal/service/ttl`, at most
10,000 entries each, expired entries evicted first, then the oldest), keyed by
the SHA-256 of the token, never the token. Hub being down is a `502` and is
never cached. Hub refusing a token on a later call evicts everything kept
about it. Hub's visibility is the whole access rule: whoever sees a workspace
on hub may subscribe, read and remove it.

**The hub client.** Besides the identity calls, the client makes the calls an
attempt needs, always with the subscription's token and never through the
caches: the workspace record (clone URL, base branch, mode, forge repository),
its resolved variables, sessions (open, usage, events, complete), sandbox
leases and their release, label creation, and the push of a branch to the
forge. Each call is bounded to 10 seconds; the lease and the push to 5 minutes,
because hub does slow work before answering. None follows a redirect or reads
more than 1 MiB. Every answer is one of five classes, told apart with
`errors.Is` and `errors.As`:

| Class | Answer |
|---|---|
| refused (`ErrRefused`) | 401 |
| forbidden (`ErrForbidden`) | 403; the message names the operation and points to `PUT /api/v1/workspaces/{slug}` |
| not found (`ErrNotFound`) | 404; a release that finds nothing counts as released |
| rejected (`*RejectedError`) | another 4xx, with hub's message, status and `error_type`; also a 502 saying the forge refused hub's credential (`forge_unauthenticated`, `forge_forbidden`), since a retry cannot fix it |
| unavailable (`*UnavailableError`) | 429, 5xx, a redirect, a transport failure, a timeout, or a body that is not hub's |

No error contains the token.

**The forge through hub.** `hub.Client.Forge(pat, slug, repo)` is an
`issuex.Client` for a workspace's one forge repository. The worker gives it to
the loops in place of a forge client with a token of its own. It supports
`GetRepository` (the workspace record), `ReadIssue` (with comments, marked as a
pull or merge request when hub says so), `ListComments`, `UpdateIssue`,
`CreateIssue`, `AddComment`, `AddLabels`, `RemoveLabel` and
`CreatePullRequest`, one request each. `ListIssues` lists open issues of
exactly one label (`state=open&labels=<label>&limit=100`), follows next pages
named in a `Link` header, leaves out pull requests, and reports itself
incomplete when it stopped at its limit with issues left, or when the last page
is exactly as long as the page asked for, since hub's bare array cannot say it
was cut. Requests for any other repository are refused before sending, and the
other `issuex` methods report that they are not available through hub. Hub's
answers become the errors the loops already handle (`*issuex.HTTPError` for 401
and 403, `issuex.ErrNotFound`, `issuex.ErrConflict`, `issuex.ErrRateLimited`,
and a retryable error when hub is unavailable), so `auth`, `forge` and
`retryable` come out as with a direct forge.

`aflib` recognises an issue URL's host as a forge when its name contains
`github` or `gitlab`, or when it is the host of `GITHUB_API_URL` or
`GITLAB_API_URL` in the service's environment. A workspace on a forge host with
neither in its name runs only if one of those variables is set; this is an
`aflib` limit.

## The jobs layer

`internal/service/jobs` sits between the routes and a runner. It keeps triage
and fix requests durable, runs them in order behind a seam, and records what
became of them. The API it serves is in [api.md](api.md#the-job-document).

- **Records.** `af_jobs` (one row per job), `af_attempts` (keyed by job and
  number) and `af_events` (the job's log, keyed by job and `seq`). An
  attempt's `envelope.json` and `report.md` (mode 0600) go under
  `attempts/<job id>/<n>/` beside the database, so purging a job is one
  directory removal. A partial unique index keeps a second open job of the
  same kind for the same issue out of a workspace, which also makes the
  duplicate check atomic with the insert.
- **States.** Jobs are `queued`, `running`, `blocked`, `done`, `failed` or
  `cancelled`; attempts `queued`, `running`, `done`, `needs_human`, `failed` or
  `cancelled`. *Open* means queued, running or blocked; *at rest* means blocked
  or finished with no attempt running or queued.
- **Queue and order.** Each attempt is one `aflib/jobqueue` item of type
  `attempt`, keyed by the nonce `<job id>:<n>`, grouped by workspace, with
  `{job_id, attempt}` as payload. Enqueuing an attempt the queue already has
  is a no-op, which makes the start-up enqueue and the once-a-minute sweep
  safe. The queue serializes a group, so a workspace runs one attempt at a
  time. Since the queue claims by the second, order within a workspace is kept
  by `af_attempts.queue_seq`: a handler whose attempt is not the workspace's
  first open attempt hands its item back. A semaphore of `[workers].count`
  slots bounds attempts in a runner; the queue has sixteen more workers than
  slots, so an attempt waiting for a slot or a backoff holds its workspace's
  place but no slot.
- **The runner contract.** `jobs.Runner` is `Run(ctx, Request) (Result,
  error)`. A result carries the envelope as the JSON the command wrote (stored
  verbatim), the report, the labels set and removed, and the maximum attempts
  per round. Every runner must: (1) treat one call as one brain-level attempt
  and never re-run the brain after a failure, because further attempts are the
  layer's; (2) return infrastructure failures (box or lease unavailable, hub
  down, a forge rate limit) as transient errors (`jobs.Transient`) and only
  real outcomes as envelopes; (3) read the workspace token once at the start
  and keep it, because an unsubscribe removes it while a cancelled attempt may
  still be cleaning up; (4) behave by the cause of a stop (`jobs.IsCancel`):
  on a cancel or unsubscribe, release what it holds and restore the request
  label within the grace; on a shutdown, write nothing, since what it returns
  is discarded and the attempt runs again after the restart. The context
  carries the cause (`ErrCancelled`, `ErrUnsubscribed`, `ErrShutdown`). The
  request carries `Round`, the attempt's position in its round, so the runner
  can tell whether an automatic attempt will follow a failure.
- **Transient retries versus automatic attempts.** A transient error asks for
  the *same* attempt to be handed over again, after 30 seconds and then 2
  minutes, with the worker slot released while waiting; the third transient
  failure fails the attempt and the job. An *automatic attempt* is a new
  attempt row, queued after a retryable `failed` envelope of category `brain`
  or `no_result`, bounded by the round (see
  [api.md](api.md#how-an-attempt-ends)). A handover after a shutdown or crash
  is neither: it raises `deliveries` and counts as no failure.
- **Cancel, unsubscribe, shutdown.** A cancel sets the job `cancelled` at once
  and gives a running runner 60 seconds (30 for the fix loop to park, 30 to
  release the lease and restore the label). After that the attempt is recorded
  cancelled with an `ERROR`, and the runner keeps its place and slot until it
  returns. An unsubscribe marks the workspace, cancels its open jobs with the
  unsubscribe cause, waits for their runners, then removes the row. A shutdown
  stops claiming, cancels attempts with the shutdown cause and waits up to 30
  seconds; the attempts stay `running` and are handed over again under the same
  number after the restart.
- **The event log.** Every transition emits `job_state`, a handover
  `attempt_start`, an end `attempt_end`; a runner reports its own events
  through the request's `Recorder`. An event is stored, with a `seq` without
  gaps per job, before it is published to a broker. Subscribers have a buffer
  of 256 events and are closed when it fills, so a slow client holds up neither
  the runner nor other clients. A stream subscribes first, replays the store
  after its starting point, drops numbers it already sent, and ends when the
  job is at rest.
- **Retention.** A purge runs at start and then hourly. It removes finished
  jobs older than `[database].retain` that have no running attempt, no runner
  in progress and no pending cancel cleanup: the directory first, then the
  rows, under the job's lock.
- **The label protocol.** The seven labels (request labels `triage` and `fix`;
  status labels `in-progress`, `ready`, `needs-human`, `landed` and `failed`;
  all under the prefix `af:`) are one table in `internal/service/labels`. The
  subscribe step creates them through hub (`hub.Client.CreateLabel`) after the
  two token checks and before the store write, one at a time, stopping at the
  first refusal, so a forge that cannot take labels fails the subscribe rather
  than a later job. Labels are never renamed or deleted.
- **The cancel cleanup.** When a cancel or unsubscribe reaches no runner that
  will clean up (the job was queued or blocked, its claimed attempt was in no
  runner, or its runner was cancelled before reporting a hub session), the
  layer cleans up behind the optional seam `jobs.Cleaner`, which
  `cmd/agent-fox` wires to `internal/service/labelcleanup`. The layer itself
  never imports hub. `af_jobs` tracks `cleanup` (`pending`, `done`,
  `label_not_set`), `cleanup_wanted` and `cleanup_warned`. Tries run in the
  background, one per job at a time and at most four at once, each bounded to
  a minute. A try reads the issue and writes only what is missing: it removes
  `in-progress` unless another job of the issue runs, removes each status label
  in the job's `labels.set` that no other open job of the issue also set, and
  puts back the request label. A failure that may pass leaves the cleanup
  pending (one `WARN`); a refusal ends it `label_not_set` (an `ERROR`). The
  sweep retries pending cleanups every minute and gives up an hour after the
  cancel. An unsubscribe waits for the first try of each cleanup it marked.

## The worker

`internal/worker` is the production `jobs.Runner`. It carries out one attempt
of a job against a subscribed hub workspace, in these stages:

1. **Resolve.** Read the subscription's token, the workspace record and its
   resolved variables from hub, and apply the refusals that need no box and no
   model (an out-of-range variable is a `config` failure; a fix on a
   `carry_patch` workspace or with `AF_FIX_LAND=patch` is `unsupported`). The
   input becomes an issue URL on the workspace's repository.
2. **Session.** Open a hub session for the attempt, or continue the one an
   earlier delivery opened.
3. **Mark.** Set `af:in-progress` on the issue.
4. **Clone.** Clone the workspace from hub's git server.
5. **Lease.** Lease a sandbox from hub and build the gateway box.
6. **Run.** Run the triage or fix loop in process.
7. **Mirror.** Write the outcome to the issue as labels and a comment.
8. **Release.** Release the lease.
9. **Report.** Post usage and events to hub and complete the session.

The worker holds nothing between calls, so attempts run side by side, each with
its own workspace token and lease token. It holds no vendor key and no forge
token. The loops, brains and settings get an environment lookup that finds
nothing, so the service's own environment changes no attempt.

**How early stops behave.** A refusal at resolve, hub not answering at resolve
or session, or a session refused with 401 or 403 writes nothing to the forge,
takes no lease and reports nothing to hub. A transient clone, lease or box
failure takes `in-progress` off, releases any granted lease and reports
nothing, leaving the session open for the next delivery. A credential refused
at the clone or the lease is mirrored as `failed` with one comment, the lease
released, and reported to hub. Infrastructure failures are transient errors
with no envelope; every other ending is an envelope (the worker builds one for
refusals before the loop; otherwise it is the loop's).

**The clone and git credentials.** The worker clones into `clone/` in the
attempt directory (mode 0700), with the base branch checked out and the
identity `agent-fox <agent-fox@noreply.invalid>` in the clone's own config. A
`clone/` left by an earlier delivery is removed first. Every git command of the
attempt runs in a clean environment (`gitx.HermeticEnv`): its own `HOME`,
removed with the attempt, no global or system config, no `GIT_ASKPASS` and no
prompt, so no credential helper of the operator is consulted. The token
reaches git only in commands that talk to hub (`clone`, `fetch`, `pull`,
`push`, `ls-remote`), as an `Authorization: Basic base64(agent-fox:<token>)`
header set through `GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_0`
(`http.<clone URL>.extraHeader`) and `GIT_CONFIG_VALUE_0`: never on a command
line, in `.git/config` or in any file, and never in a commit's environment,
whose hooks are the repository's. This needs git 2.31 or later. A clone refused
with 401 or 403 is a `failed` envelope of category `auth`; any other clone
failure (transport, 5xx, hang-up, no answer in ten minutes, missing base
branch) is a transient `git` error.

After the fix loop pushes a branch to hub's git server, the worker asks hub to
push it on to the forge (`PushBranch`) before the pull request is requested,
up to three times while hub is unavailable (waiting one, then two seconds). A
branch hub does not take ends the landing `failed`/`git`, not retryable, with
the branch kept on hub's git server and named in `next`. The seam is
`fix.LandParams.PostPush`, set through `afcmd.Config.PostPush`.

**The lease and the gateway box.** The worker leases a sandbox with
`{purpose: "triage"|"fix", labels: {job, attempt}}`, plus `profile` when
`AF_SANDBOX_PROFILE` is set, and tells the job the sandbox id. The attempt gets
a box registry with one provider, the lease's gateway, and the loop's box is
built there with an empty `Env` and the labels `af.run_id`, `af.tool`, `af.job`
and `af.attempt`. The profile supplies the image, network and secrets. The
image must provide an `af` of the same version as the worker, since the
repository's checks are measured by the box's `af`; a missing one is a
non-retryable `sandbox` failure that names `AF_SANDBOX_PROFILE`. A lease refused
with 401 or 403 is a `failed`/`auth` envelope pointing to `PUT
/api/v1/workspaces/{slug}`. Any other lease failure (5xx, capacity, timeout),
an unreachable or refusing gateway, and a lease with less than two minutes
left are transient `sandbox` errors. When the lease ends before `AF_TIMEOUT`
would, the loop's timeout is cut to end two minutes before the lease expires
(warning `lease_shortened_timeout`). Each lease is released once, after the box
closes, on every path (success, failure, transient error, cancel, shutdown,
panic), on a context the cancel does not reach, within 30 seconds. A failed
release adds `lease_release_failed`.

**Running the loop in process.** The worker runs the loop as the `af` command
it is, through `toolio.App.Invoke` with a `toolio.Call`, on a shell of its own
that no signal stops, only the attempt's context. `afcmd.Config` supplies the
seams: the forge is hub's for the workspace repository, the box provider is
the lease's gateway, the git runner is the clone's, and the loop settings are
`afcmd.LoopOptions` (brain in the gateway box, budget, turns, and for a fix
the landing). An issue is passed as its URL; a text as the call's stdin with
its title as a Markdown heading; a file is written to `input/<base name>`
(mode 0600, directory 0700) and passed by path. Context strings and answers are
passed as context values, with the earlier questions supplied so the loop can
pair them. Every line of the loop's events reaches the job's recorder as it is
written, beside the worker's own `step` events (`resolve`, `session`, `clone`,
`lease`, `run`, `mirror`, `release`, `report`). A loop that ends `failed` with
a retryable `sandbox`, `forge` or `hub` error becomes a transient error;
anything else is a result with the loop's envelope, amended only by added
warnings, side effects and the `hub_session` and `sandbox` artifacts
(`toolio.AddWarnings` changes nothing else). When the attempt returns,
`input/` and `clone/` are removed.

**The label mirror.** Setting a status label removes the other four the issue
carries, except that a fix's `in-progress` leaves `ready`, which only the fix's
outcome replaces. The outcome is mirrored to the issue the input named (or, for
a triage of text that ended `done`, the issue the loop created):

| Outcome | Labels and comment |
|---|---|
| triage `done`, confidence `confirmed` or `probable` | `ready`, no comment |
| triage `done`, `suspected` or `not_reproducible` | `needs-human`, with the reason |
| any `needs_human` | `needs-human`, with the question |
| fix that landed (`done` or `unverified`) | `landed`, with a summary |
| fix landed under `AF_FIX_LAND=none` | no label; the summary says nothing was kept |
| parked fix | `needs-human`, with what failed and how to ask again |
| any other `failed`, or `usage` | `failed`, with the failure |

A transient error, and a failure that an automatic attempt will follow, only
take `in-progress` off. Each comment ends with a marker `<!-- agent-fox
job=… attempt=… kind=… -->`, is not posted again when its marker is already on
the issue, and contains no path on the worker. The worker reads
`AF_POLL_INTERVAL` as the poll does, and names the forge path (comment, then
re-add the label) in its comments only while label triggers are on. A failed
write is a warning (`status_label_failed`, `comment_failed`,
`request_label_failed`) with a `failed` side effect, never a status change. A
cancel or unsubscribe is followed, after the loop stops and within a minute, by
taking `in-progress` off and putting the request label back, leaving `ready`;
the token read at resolve serves this even though the subscription may be
gone. A shutdown writes nothing to the forge.

**The report to hub.** After the release, an attempt with a session posts one
usage record when a brain ran (the session's model, and the envelope's brain,
tokens, cost, cost source and turns), all events of the delivery in batches of
at most 500, and completes the session: `completed` for `done`, `unverified`
and `needs_human`, `timeout` for a timeout failure, `failed` otherwise. A
transient error and a shutdown post nothing and leave the session open for the
delivery that reaches an outcome. A failed call adds a warning
(`hub_usage_failed`, `hub_events_failed`, `hub_session_failed`).

## Redelivery and reconciliation

The jobs layer can hand the worker the same attempt more than once: after a
restart, a crash, a shutdown or a transient error. `Request.Delivery` counts
the handovers. **Every delivery starts afresh**: it removes a leftover
`clone/`, clones again, takes a new lease and runs the loop from the start with
the whole budget. Nothing is carried over, so nothing needs journalling.

- **One session per attempt.** The jobs layer records the hub session and run
  id the worker reports (`Recorder.Hub`) and hands them to the next delivery
  (`Request.SessionID`, `Request.RunID`), which continues in that session. The
  delivery that reaches the outcome posts usage and events and completes it.
- **A lost box** ends the loop with a retryable `sandbox` failure, which is a
  transient error, so the attempt is handed over again.
- **What is given up.** A process that dies after a fix branch was pushed but
  before the pull request leaves the branch on hub's git server, and the next
  delivery lands the change again under another branch name. A triage of text
  that dies after creating its issue creates a second one. Both windows are
  narrow, and a person closes the extra. A process that dies while a brain runs
  pays for that brain again.

**Reconciliation.** `internal/service/reconcile` runs a pass at start (in the
background, delaying neither the listener nor the queue) and every 15 minutes.
For every subscribed workspace whose token opens, it lists leases through hub
and releases each orphan: a lease whose `job` label names a job in the
database and whose `attempt` label names an attempt no runner of this process
is carrying out. A lease with no labels, an unknown job or a running attempt is
never released. A workspace that cannot be asked is logged at `WARN` and tried
again next pass; each release is logged at `INFO`.

## The label poll

The poll (`internal/service/poll`) turns request labels on issues into jobs.
It is the only trigger besides the API. There is no event stream, so a label is
found within one interval of being added, and closing an issue cancels
nothing. The service starts the poll after the jobs layer and stops it first,
so no cycle is in flight when the queue and the state file close. The
user-facing rules are in [api.md](api.md#request-labels); the setting is in
[configuration.md](configuration.md#the-label-poll).

- **Loops.** A `Supervisor` reads the subscriptions at start and every 60
  seconds and keeps one loop per subscribed workspace. A new subscription gets
  a loop; a removed one loses its loop and its in-flight cycle. A loop waits a
  random 0 to 30 seconds, runs its first cycle, and starts each later cycle one
  interval after the previous one ended, so a workspace's cycles never
  overlap. At most four cycles run at once across workspaces, and a cycle
  running for 5 minutes is cancelled (what it did stands). Clock, first-cycle
  delay, scan period and release delay are injectable; only the interval
  (`AF_POLL_INTERVAL`) is a setting.
- **A cycle.** It first brings the poll's records up to date with their jobs
  (no hub calls), then reads the token and the resolved variables. If the poll
  is off it stops there. Otherwise it reads the workspace record and lists the
  open issues of `af:triage`, then `af:fix`, through `ListIssues` (at most 100
  each), sorts each listing by issue number, and applies the rules to each
  issue, one at a time, under the workspace's lock (so an old and a new loop of
  the same workspace never apply them at once).
- **The rules.** For issue N listed under label L of kind K, the first match
  wins: an open blocked job of kind K with no removal of L owed and no newer
  record is answered (see [Forge follow-ups](#forge-follow-ups)); any other
  open job of kind K absorbs L (removed, no job, an `INFO` record); a hold on L
  for N means do nothing; an owed removal of L means remove it, make no job; a
  record newer than the listing means leave L to the next cycle; the latest job
  of kind K for N having failed means retry it; otherwise it is a new request:
  the poll creates the job through `jobs.Service.Create`, writes its record,
  then removes L. A duplicate refusal from the jobs layer counts as absorbed (two
  racing creators yield one job). A not-subscribed refusal ends the cycle with
  nothing written; any other refusal ends it with a `WARN` and leaves L.
- **Records and owed removals.** The poll's records are rows of
  `af_poll_requests`: workspace, issue, label, job (none for an absorbed
  label), whether the removal is owed, what the poll has seen of the job (open,
  ended, holding) and the release clock. Every outcome that consumes a label
  writes a record before the removal. A failed removal stays owed: it is retried
  whenever the issue is listed under the label, confirmed by a successful
  removal or a not-found answer, and settled by a complete listing without the
  issue. So a label whose removal failed is never taken twice. A record written
  after the listing was requested means another cycle took the label since;
  the cycle leaves it alone, and the listing settles and releases nothing for
  it. Records are ordered by row id as well as time, so a stalled clock does
  not hide a newer one. There is no foreign key to `af_jobs`, so records outlive
  restarts and the retention purge. A record is dropped once nothing is owed,
  nothing holds and nothing guards; after that, a forge whose listing lags can
  still yield a second job of a new request. The record of an answer or a retry
  is a **guard** (`guard` column) until a complete listing requested after it
  lacks the label, or the release delay has passed since it was made; while it
  guards, a sighting of the label is left to the next cycle, so a lagging index
  cannot repeat the answer or the retry.
- **Holds and the release delay.** `jobs.Service.Standing` reports a job's
  state, source, request label, whether its cancel reached a runner, whether a
  runner is still in progress, and the state of its cancel cleanup
  (`ErrNotFound` for a purged job). A cancelled job the poll made holds its
  label when its cancel reached a runner (the worker writes the label back) or
  when its cleanup is pending or put the label back; a job purged before the
  poll saw it end holds from when the poll found it gone. A cancelled job that
  reached no runner and got no cleanup, or whose cleanup ended without the
  label back, holds nothing. The release clock starts when the poll first sees
  no runner in progress and no cleanup pending. Only a complete listing without
  the issue, requested at least the **release delay** (5 minutes) after that,
  releases the hold: the forge may not yet list a label just put back, and the
  runner may still be writing it.
- **Incomplete listings.** A listing is complete when `ListIssues` did not
  stop at its limit (see [The service](#the-service) for how hub's bare array
  is handled). An incomplete listing logs one `WARN` per workspace and label
  until it lists complete, releases no hold and settles no owed removal. Since
  consumed labels leave the filter, a backlog drains over successive cycles.
- **Failures.** Hub being unavailable ends the cycle with a `WARN`. A refused or
  forbidden token, or a workspace hub does not find, ends it with an `ERROR`
  that names the operation (and, for the token, the `PUT` route that replaces
  it). Another 4xx on a read ends it with an `ERROR`; on a label removal it
  leaves the removal owed and moves to the next issue. A failure is logged when
  it first occurs and not while it repeats; one `INFO` says when the poll has
  recovered. One workspace's failure never stalls another's loop beyond the four
  shared slots. No record holds a token.
- **The cost of a cycle.** An enabled cycle makes four read requests to hub
  (variables, workspace record, one list per request label) plus one removal
  per label it takes, absorbs or takes again. A disabled workspace costs one
  read of its variables every 5 minutes. The lists and removals reach the forge
  and spend the workspace's forge rate limit; `AF_POLL_INTERVAL` controls that
  cost. The poll runs no model.

## Forge follow-ups

Follow-ups let a team carry a job forward from the issue: comment, then add the
request label again. They are part of the poll's rules, not a separate trigger
(`internal/service/poll`: `answer.go`, `reply.go`, `thread.go`; the worker's
side in `internal/worker/mirror.go`). The user-facing behaviour is in
[api.md](api.md#answering-and-retrying-from-the-issue).

- **Rule order and guards.** A blocked job is answered and a failed job is
  retried only when no removal of L is owed and no record of L is newer than
  the sighting, and no record of L guards it. The newer-record guard matters: a
  listing requested before an answer or a retry would otherwise find the job
  blocked or failed again once the new attempt ended, and repeat it. The
  record of the answer or retry guards against a listing requested after it
  that still shows L because the forge lags the removal.
- **Reading comments.** An answer and a retry read the issue through hub's
  issue read (`ReadIssue`), never the comment list, because only the issue read
  says when hub cut the comments (`truncated`) or failed to read them
  (`comments_err`). Pure functions in `thread.go` take the comments after the
  first comment carrying the latest attempt's marker (or those made from the
  attempt's `finished_at` when none does), drop the service's own (containing
  `<!-- agent-fox`) and blank ones, render each as `--- comment by <login> ---`
  and its body, and cap the text at 8192 bytes.
- **Acting.** An answer goes through `jobs.Service.Answer`, a retry through
  `RetryWithContext`, both carrying the sighting's `by`. A cut thread gives no
  answer and no context; a reply names `af` and the API instead. An answer with
  no counting comment posts a "no answer found" reply. Each reply carries its
  own marker and is not posted again when it is already the latest comment.
- **Records.** Every outcome that consumes L writes a record before the
  removal and names the job only when L is that job's own request label, which
  a cancel puts back and the poll then holds.
- **Context.** `af_attempts.context` holds the comments a retry from the forge
  gave its attempt. The runner receives the job's context strings followed by
  each attempt's context in order, so an automatic attempt after a forge retry
  keeps them; the worker passes each as a context value with no question. A
  retry through the API adds none.

## The service client

`af` is the client of the service. It adds no route; it consumes the API in
[api.md](api.md). Its user-facing behaviour is in
[cli.md](cli.md#service-client-commands). It is built in three places that do
not know each other's concerns:

- **`internal/afclient`** is the library: the server URL (`--server`, else
  `AF_URL`), the credential (`token` in `~/.af/config.toml`, read and never
  written), and a `Client` that sends every request with the bearer token,
  bounds it to two minutes, reads at most 8 MiB, refuses redirects (so the
  credential reaches no other host) and maps every non-2xx answer to an
  `*Error` with category, retryable flag, message and hint. `Follow` reads a
  job's event stream in tries: it resumes from the last `seq`, drops
  duplicates, cuts a stream silent for 60 seconds, reads the job after every
  stream, backs off 1, 2, 4 and 8 seconds, and gives up after the fifth failed
  try in a row. Its clock is injectable. It never imports `internal/toolio`
  and never exits.
- **`cmd/af`** maps the library onto the shell: one command per route, the
  failure categories (`service`, `conflict`, `hub`, `job_cancelled`, besides
  `usage`, `auth`, `io` and `config`), and `followJob`, which ends a followed
  job by its outcome. A blocked job ends `needs_human` with a resume of `af
  jobs answer <id> --wait`. Interrupting `af` never cancels the job, which is
  durable on the service.
- **`internal/toolio`** supplies what the client needs from the shell:
  `Run.SetInput` for a command that reads its own input, a question's
  `ResumeArgv`, and `Run.Redact`, which removes the credential and any
  workspace token from every output.

The loops and the client are separate: `af triage` and `af fix` are the loops
of `internal/afcmd`, which the worker also runs in process, and `af jobs
submit` queues the same work on the service.

## Design records

- [`docs/prd/`](prd/) holds the product requirements that shaped the service
  and the loops (PRD 01 to 05).
- [`docs/adr/`](adr/) holds the architecture decision records: the rules the
  code follows where the specs left a choice open or the code diverged from
  them, grouped by topic (the shell, the tool policy, measuring, boxes and
  brains, the loops, hub's API, the jobs layer, the service API, the worker,
  the label poll and the service client).
- [`docs/drafts/`](drafts/) holds design drafts. `target_architecture.md` is
  the archived target architecture (the wider toolbelt, `af impl`, the spec
  pipeline, hub's tasks); it is out of scope and not built.
- [`.specs/archive/`](../.specs/archive/) holds the implemented specs (01 to 04
  are `issuex`'s, now in aflib; 05 to 19 are this repository's). They were the
  input to the code; the code, its tests and the decision records are the record.
