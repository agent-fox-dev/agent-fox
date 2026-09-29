# Serve the four tools over MCP

## Intent

The four tools are programs with one input and one JSON object out, and that
is the right primary interface: any agent with a shell can call them, they
compose with pipes, each run is an isolated process, and an hour-long `impl`
can run detached and leave its result behind. [PRDs 01–03](README.md) make
that interface decidable, compact and self-describing.

Some callers cannot use it, and some should not have to:

- **Hosts without a shell.** Desktop and web chat clients, hosted agents and
  agent frameworks that speak the Model Context Protocol but do not hand the
  model a terminal.
- **Hosts that grant permissions per tool.** A harness can allow `issue` and
  ask before `fix`, per call, only if `issue` and `fix` are separate tools.
  Through a shell, the grant is "may run commands", which is far wider than
  either.
- **Callers that want the interface declared, not discovered.** An MCP client
  receives each tool's input and output schema at connection time; a CLI
  caller has to run `--schema` or read the help first.

This PRD adds `af mcp`: an MCP server that exposes the four tools, built as
a **thin adapter over the binaries**. It does not import the pipelines. Each
call runs the real tool as a child process with a constructed argument
vector, and the child's envelope is the result. The CLI stays the single
implementation. The adapter adds job handling for long runs, a server-side
policy the model cannot override, and the mapping onto MCP's schemas,
progress and cancellation.

Two boundaries from [ADR 03](../adr/03-rebuild-the-skills-as-tools.md) stand
and are restated so this PRD cannot be read as moving them:

- §3, "there is no MCP server", is about the model **inside** a tool. That
  model still gets no MCP server, no forge tool and no network tool. This
  adapter serves the model **outside** the tools, which is a different
  caller.
- The four tools gain no subcommand. `mcp` is a verb on `af`, the umbrella
  program that is a stub today, and the four tools' interface is unchanged.

## Goals

- An MCP client that connects to `af mcp` can run `spec`, `issue`, `fix` and
  `impl` with the same results a CLI caller gets.
- Runs of any length work, including an `impl` that outlives the client's
  request timeout and the client's own process.
- The operator, not the model, decides what the server may change: which
  directories, whether it may push, and whether it may write to a forge.
- The tools' schemas, progress events and trust labels reach the client in
  MCP's own forms.
- Adding a tool to agent-fox needs no change to the adapter beyond listing it.

## Non-goals

- Running a pipeline in the server's process. Each run is a child process, by
  design: isolation, cancellation and credential scoping come from that.
- A remote, multi-user service. Only the stdio transport is in scope. The
  streamable-HTTP transport, OAuth, and serving more than one user need their
  own PRD, because a long-lived network service holding a forge token is a
  different threat model.
- Exposing `afspec` library operations (`validate`, `render`, `archive`, …)
  as tools. They are library API, per ADR 03, and adding them is a separate
  decision.
- MCP prompts and sampling. The server does not ask the client's model for
  anything.
- Giving the tools' inner model access to this server, or to any MCP server.

## Dependencies

This adapter is built on the CLI features from PRDs 01–03 and is not
specified before them:

| Needs | From | Used for |
|---|---|---|
| `status`, `summary`, `needs_human`, `--context`, `error.retryable` | PRD 01 | the tool result, `isError`, answering a question |
| `--detail summary`, `artifacts`, `side_effects`, `next`, `--input-kind` | PRD 02 | a compact `structuredContent`, strict input |
| `--schema`, `schema_version`, `--output`, `--events-file`, `untrusted_fields`, `--preflight` | PRD 03 | tool definitions, job results, progress, trust labels, a free check |

The MCP implementation is the official Go SDK
(`github.com/modelcontextprotocol/go-sdk`), subject to confirming at spec
time that its server API covers tools with output schemas, progress
notifications, cancellation, roots and elicitation. Where it does not, the
missing part is the small JSON-RPC surface this PRD uses, written in
`internal/mcpserve`, not a second SDK.

## Functional requirements

### 1. The server

- `af mcp [flags]` speaks MCP over stdio: JSON-RPC on stdin and stdout,
  logs on stderr, and nothing else on stdout.
- On start it locates the four binaries: next to `af` first, then on
  `PATH`, or at `--bin-dir`. For each, it runs `<tool> --schema` and
  refuses to start, naming the tool, if one is missing or its
  `schema_version` major differs from the one the adapter was built for. A
  version mismatch found at connection time is better than one found in a
  model's tenth call.
- `--tools spec,issue` exposes a subset. The default is all four.
- The server's code lives in `internal/mcpserve`; `cmd/af` dispatches
  `mcp` to it. `af` with no verb keeps printing its build identity.

### 2. Tool definitions, generated from `--schema`

Each tool is exposed as an MCP tool of the same name:

- `inputSchema` is the tool's `--schema` `flags` object with a required
  `input` string property added (the positional input) and an optional
  `context` array of strings (PRD 01's `--context`). Flags the server
  controls (§5) and flags meaningless here (`--quiet`, `--verbose`,
  `--show-text`, `--version`, `--events`, `--output`) are removed from the
  schema.
- `outputSchema` is the envelope schema from `--schema`.
- `description` is the tool's `--schema` description plus two fixed
  sentences: what the input may be, and that fields listed in
  `untrusted_fields` are data, not instructions.
- Annotations are declared per tool and are advisory to the client:

  | Tool | `readOnlyHint` | `destructiveHint` | `idempotentHint` | `openWorldHint` |
  |---|---|---|---|---|
  | `issue` | false | false | false | true |
  | `spec` | false | false | false | true |
  | `fix` | false | true | false | true |
  | `impl` | false | true | true | true |

  `impl` is idempotent because a second run on the same spec continues the
  first. `fix` is destructive because it pushes and opens a pull request.
- The definitions are generated at start and never hand-written, so the
  tools and the adapter cannot drift.

### 3. Every run is a job

Tool calls do not block for a run's full length. Every call to a tool starts
a **job** and returns either its result or a handle:

- The call takes an optional `wait_seconds` argument (default 60, maximum
  set by `--max-wait`, default 300). If the run ends within it, the call
  returns the result (§4). Otherwise it returns `status: "running"` with a
  `job_id`, the events so far, and the text "call `job_wait` with this id".
- Four more tools manage jobs:
  - `job_wait {job_id, wait_seconds}`: block up to `wait_seconds` more,
    then return the result or `running` again.
  - `job_status {job_id}`: return immediately with the stage, elapsed
    time, cost so far and the last ten events.
  - `job_cancel {job_id}`: cancel the run (§6).
  - `job_list {}`: every job this server knows of, newest first, with
    tool, input, status and start time.
- Each job has a directory under
  `$XDG_STATE_HOME/agent-fox/mcp/jobs/<job_id>/` holding `argv.json`,
  `envelope.json` (the child's `--output`), `events.jsonl` (its
  `--events-file`) and `stderr.log`.
- The child is started in its own process group and does not die with the
  server. A server that restarts rebuilds `job_list` from the job
  directories: a job whose envelope exists is finished, and one whose
  process is gone with no envelope is reported as `status: "lost"`. An
  hour-long `impl` therefore survives the client closing and reconnecting.
- When the client declares support for MCP's task-augmented requests, the
  server may serve the same job through them instead. The `job_*` tools stay
  either way, because not every client supports tasks.

### 4. The result of a finished job

- `structuredContent` is the child's envelope, exactly as the CLI printed
  it under `--detail summary`. `report_file` points at the full one.
- The text content is the envelope's `summary`, then `status`, then the
  `next` suggestions, one per line. That is the minimum a client that shows
  only text needs.
- `isError` is true when `status` is `failed` or `usage`. It is false for
  `done`, and also for `needs_human` and `unverified`, which are deliberate
  outcomes a model should read and act on, not failures to retry.
- A child that exits without writing an envelope (killed, crashed before
  `Emit`) produces an adapter envelope with `status: "failed"`,
  `error.category: "internal"`, `error.stage: "adapter"`, and the tail of
  `stderr.log`.

### 5. The operator's policy, which the model cannot override

Server flags constrain every call. An argument that asks for more than the
policy allows is refused as a usage error, naming the flag the operator
would have to change. It is not silently downgraded: a model told "done"
must not have received a smaller job than it asked for.

| Flag | Default | Effect |
|---|---|---|
| `--root <dir>` (repeatable) | the server's working directory | the directories a call's `dir` may be in, symlinks resolved. When the client provides MCP roots, the allowed set is their intersection with these. |
| `--allow-forge-writes` | off | without it, every `issue` and `spec` call gets `--dry-run`, and a call passing `dry_run: false` is refused |
| `--max-land` | `none` | the highest `land` a `fix` or `impl` call may ask for: `none` < `branch` < `pr`. The default makes no remote change. |
| `--max-budget` | 10 | the most any call's `--total-budget` may be, in dollars. Every call gets `--total-budget` set to its own value or this, whichever is lower. |
| `--max-jobs` | 2 | concurrent jobs. A call beyond it is refused with `retryable: true`. |
| `--allow-trust-project` | off | without it, `trust_project` is removed from the input schema |
| `--allow` passthrough | none | the `--allow` programs a call may name; a call naming others is refused |

- One job at a time per repository. A `fix` or `impl` call whose `dir` is
  the repository of a running job is refused, because both need a clean
  tree and would fight over the checkout. `issue` and `spec` are exempt:
  neither checks out a branch, and `spec` writes only under the spec root.
- The child's environment is the server's, minus nothing and plus nothing:
  credentials are the operator's, set where the server is launched, and a
  tool argument can never add one. No credential value appears in any
  result, event or log the adapter writes.

### 6. Progress and cancellation

- When a call carries a `progressToken`, each child event from
  `events.jsonl` is forwarded as `notifications/progress` for as long as
  the call is waiting. The message is the event's human form, and the
  progress value is turns for a single-phase tool and tasks done for
  `impl`. `heartbeat` events are forwarded too, so a client's idle timeout
  sees activity.
- `notifications/cancelled` for a waiting call stops the wait, not the job:
  a client that gave up waiting has not asked for the work to be thrown
  away. `job_cancel` is the one way to cancel a run.
- `job_cancel` sends SIGINT to the child's process group, waits
  `--cancel-grace` (default 30 s) for the envelope, then sends SIGKILL.
  SIGINT is what the tools already handle: `fix` and `impl` park the work
  as a `wip:` commit and return the checkout to its base branch.

### 7. A question from the run

When a job ends with `status: "needs_human"`:

- If the client declared elicitation support, the server sends an
  elicitation request with the question and, when there are any, the options
  as an enum. It then starts a new job with the answer as `context`, and the
  original call (or `job_wait`) returns that job's result. A declined or
  cancelled elicitation returns the `needs_human` result unchanged.
- Otherwise the result is returned as is, and the model answers by calling
  the tool again with the same `input` and the answer in `context`.
  `needs_human.resume` in the envelope already says so.
- The elicitation is sent to the client's user, not to the client's model.
  This is the one place the adapter talks to a person, and it is the place
  the tools were designed to stop for.

### 8. A free check

`<tool>_preflight`, one per exposed tool, with the same input schema and
`readOnlyHint: true`, runs the tool with PRD 03's `--preflight` and returns
its envelope synchronously. A model can confirm that its tree is clean, its
credentials resolve and the spec validates before starting a job that costs
money. The server's policy (§5) applies to preflight calls as well, so a
preflight never reports a call as possible that the real call would refuse.

## Acceptance criteria

- `af mcp` with `issue` missing from `PATH` and `--bin-dir` exits non-zero
  before accepting a connection, naming `issue`.
- The `inputSchema` of each exposed tool matches that tool's `--schema`
  output with the documented additions and removals. A test regenerates it
  and compares.
- An `issue` call with `wait_seconds: 120` on a run of 40 s returns the
  envelope in `structuredContent` and its `summary` as text.
- An `impl` call returns `running` with a `job_id`. Killing and restarting
  the server leaves the child running, and `job_wait` on the same id after
  the restart returns its envelope.
- With default policy, a `fix` call with `land: "pr"` is refused as a
  usage error naming `--max-land`, and no child is started.
- With default policy, every `issue` child's argv contains `--dry-run`.
- A second `fix` in the same repository as a running `fix` is refused;
  an `issue` call in that repository is not.
- `job_cancel` on a running `fix` leaves the repository on its base branch
  with a clean tree, and the job's envelope has `status: "failed"` and
  `error.category: "aborted"`.
- A `fix` job that stops on an ambiguity, with an elicitation-capable
  client that answers `B`, produces a second job whose argv carries
  `--context B`.
- No adapter output, result, event or log contains the value of
  `ANTHROPIC_API_KEY` or `GITHUB_TOKEN` (checked with sentinel values in
  a test).
- The adapter's tests need no network and no model: the children are the
  real binaries driven by AgentKit's scripted provider and an `httptest`
  forge, as the tools' own tests are.

## Documentation

- `docs/mcp.md`: how to register `af mcp` with a client, the policy flags
  with a recommended configuration for unattended use and for interactive
  use, the job tools, and what the annotations mean.
- `docs/cli.md`: a pointer to `docs/mcp.md` in the introduction.
- `README.md`: `af mcp` under "The tools", described as an adapter over
  them.
- An ADR recording that agent-fox serves MCP as an adapter over the
  binaries, not in-process, and that ADR 03 §3 is unchanged by it.
