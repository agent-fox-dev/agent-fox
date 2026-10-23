---
spec_id: "16"
spec_name: "af_service_client"
title: "af as the agent-fox service client: login, workspaces, jobs, --workspace and --wait, and an explicit --local"
status: "active"
created_at: "2026-10-21T23:05:30.086973Z"
updated_at: "2026-10-23T13:55:34Z"
intent_hash: "b6cba4a43c2cba0315e4959dc5cadf7b39033c7fd600be66d0dbacb45da6f826"
schema_version: 2
source: "docs/prd/03-run-the-agent-fox-service.md"
---
## Intent

Make `af` the client of the agent-fox service. A person or a script gives `af` a hub credential once. With it they can subscribe a hub workspace, queue triage and fix jobs on it, follow a job until it stops, and list, answer, retry and cancel jobs. Every one of these commands speaks the one-envelope contract and the exit codes that every `af` command speaks. Running the loops on the person's own machine stays available behind an explicit `--local`, so `af` never has to guess where a run goes.

## Goals

1. **Where a run goes is explicit.**
   - `af fix <issue url>` with neither `--local` nor `--workspace` exits 2. Its usage envelope's message names `--workspace` (with `--server`/`AF_URL`) and `--local`. Nothing reaches the service or the forge, and no `.agent-fox/run/` directory is created.
   - For the same input and flags, `af fix … --local` and `af triage … --local` give the same statuses, exit codes, results, events, warnings and run directory as `af fix` and `af triage` gave before this spec, even with `AF_URL` pointing at a closed port. The one difference in an envelope is triage's fix suggestion in `next`, which ends with `--local` (R1).
   - `--local` together with `--workspace` exits 2. So does `--workspace` together with `--dir`, `--brain` or any other flag only the local loop reads, and the message names that flag.
   - In `af triage x --context --local`, `--local` is the value of `--context`, so the invocation names no mode and exits 2.
   - `13_agent_fox_worker`'s in-process loop invocation needs no `--local`, and its tests pass unchanged.
2. **End to end against the real service.** The service of specs 11 and 12 runs in process over `fakehub`, with the jobs layer's fake runner:
   - `af workspace subscribe widgets --hub-token-file f` exits 0 with the subscription view. Running it again also exits 0, because the service answers 200.
   - When the runner returns `done`, `af fix 42 --workspace widgets --wait` exits 0. `result.job.state` is `done`, and `result.envelope` is attempt 1's stored envelope.
   - The other endings exit with their envelope's code:
     - A runner returning `unverified` with outcome `parked` gives exit 4.
     - A non-retryable failed envelope gives exit 1, with the same `error.category`.
     - `needs_human` gives exit 3. `needs_human.resume` is `{argv: ["af","jobs","answer",<id>,"--wait"], flag: "--context"}`. Running that argv with `--context B` appended starts attempt 2, and attempt 2's runner receives option B's label as the answer.
   - `af jobs watch <id>` writes every event of the job to stderr in `seq` order, one JSON line per event.
3. **The credential.**
   - Requests carry the `token` of `~/.af/config.toml`. With no credential, the command fails `auth`, names `af login`, and sends no request.
   - `af login --token-file -` with a token the service accepts writes `~/.af/config.toml` with mode 0600. In an existing file only the value of `token` changes, and every other byte stays as it was.
   - A token the service refuses (401) writes nothing and fails `auth`. A 502 writes nothing, and its message says the credential was not checked, not that it was refused.
   - An existing credential file with no top-level `token` line, an unreadable credential file, and a failed write each fail without replacing the file.
   - A sentinel credential and a sentinel workspace PAT never appear on stdout, stderr, `--output` or `--emit-events`. No request carries the credential to any host but the server's.
4. **Failures map predictably.** Against a scripted fake service:
   - each row of R4's table gives its status, exit code, category and retryable flag;
   - a redirect is not followed, and its target receives no request;
   - an invocation with several local mistakes reports the first one in R4's order of local checks.
5. **Following survives drops and interruptions.**
   - A stream cut after event k resumes with `since=k`. `--emit-events` then holds every event exactly once, in order.
   - Five failed tries in a row end the following with the category of the last failure, retryable.
   - An interrupt, or `--timeout` running out, during `--wait` sends no cancel request. The envelope names the job as an artifact and puts `af jobs watch <id>` and `af jobs cancel <id>` in `next`. A `--wait` that reaches rest has neither entry.
   - A job cancelled on the service ends `--wait` with category `job_cancelled`. An interrupted `af` ends with `cancelled`.
6. `make check` passes offline. The `--schema` golden (`cmd/af/testdata/schema.golden.json`, which `13_agent_fox_worker` keeps where it is) is rewritten on purpose and describes every new result.

## Non-goals

- **Changes to the service.** The API of `11_agent_fox_service_core` and `12_agent_fox_jobs` is consumed as specified. No route, field or code is added.
- **A browser or OAuth login.** `af login` takes a token from a file or stdin. Someone already logged in with `afc login` needs no `af login`.
- **`af logout`, several profiles, or a stored server URL.** The service is named only by `--server` or `AF_URL`.
- **A credential from the environment**, such as an `AF_TOKEN`. PRD 01 and PRD 03 name the credential file as the only source. Whether CI needs another source is left to the input's owner.
- **A default workspace** such as `AF_WORKSPACE`, and **`--title`** for text triage.
- **Filters and page sizes on `af jobs list`** beyond PRD 03's `--workspace` and `--state`. There is no `--kind`, `--issue` or `--limit`. `--cursor` is added only so that the next page can be reached (R8).
- **Commands that download an attempt's report or envelope by number.** `--wait` and `watch` fetch the final envelope, and nothing else is fetched.
- **Following pages automatically** in `af jobs list`.
- **`--local --workspace`**, PRD 01's local run reporting to hub. In this release that combination is a usage error.
- **Rendering for a terminal.** That covers progress bars, colours and interactive prompts. Output is JSON.
- **The label protocol, hub's event stream and `AF_AUTO_FIX`** (PRD 04), **carry patches** (PRD 05), and **a web client**.
- **The poll** belongs to `agent_fox_poll`. Jobs that the poll creates are listed and acted on like any other job.

## Background

**The `af` binary** (`cmd/af/main.go`, `register`) registers sixteen commands on `internal/toolio`'s shell.

- **Two of them are the loops.**
  - `af triage` (`registerTriageLoop`) and `af fix` (`registerFix`) run the whole pipeline on this machine.
  - Both declare `Input: InputRequired`, `UsesForge`, `UsesRunDir`, `Repo`, `DryRun`, `Context` and `Timeout`.
  - Before `Run` is called, the shell (`internal/toolio/app.go` `Main` → `match` → `parse` → `checkValues` → `prepare`) parses the flags. It then builds a forge client from `GITHUB_TOKEN`/`GITLAB_TOKEN`, resolves the input (reading an issue from the forge), and creates `<dir>/.agent-fox/run/<id>/`.
  - The shell's flag parse (`parseInterspersed`, `internal/toolio/flags.go`) accepts one or two dashes and `--flag=value`, and a flag that takes a value takes the next word whatever it looks like. `requests` (`internal/toolio/app.go`) applies the same rule to `--help`: in `--context --help`, `--help` is the value.
  - Nothing in `af` talks to an agent-fox service, and there is no `--local`, `--workspace`, `--server` or `AF_URL`.
- **Moving the commands.** `13_agent_fox_worker` moves every command implementation and its unit tests out of `cmd/af` into one importable package. Tests that build and run the binary stay in `cmd/af`, and `cmd/af/testdata/schema.golden.json` stays byte-identical where it is (13-REQ-1.1). Its worker runs `triage` and `fix` in process through a fresh `toolio.App` with flags, and no `--local`. Paths under `cmd/af` in this PRD are as they stand before that move, and name the code by its role.
- **The envelope** (`internal/toolio/envelope.go`):
  - its statuses map to exit codes 0 `done`, 1 `failed`, 2 `usage`, 3 `needs_human` and 4 `unverified`;
  - `Failure{Category, Message, Retryable, FixHint}` sets its error;
  - `needs_human.resume` is `{argv, flag}`, and the embedded schema fixes `flag` to the constant `"--context"` (`internal/toolio/schema/envelope.schema.json`);
  - the shell always builds `resume.argv` from the invocation's own argv.
  - Events go only to files: `events.jsonl` and `--emit-events` (`internal/toolio/events.go`).
  - `Run.Next` only appends; the collected `next` and artifacts reach the envelope even after a timeout or a signal.
- **`docs/cli.md`'s contracts.** Its Error categories table defines `cancelled` as "The process was sent SIGINT, SIGTERM or SIGHUP" and `io` as "A file, stdin or the run directory could not be read or written." Its `needs_human` section says `resume.argv` is "the invocation as it was received, with `argv[0]` replaced by `af`."
- **Input.** `toolio.Resolve` (`internal/toolio/input.go`) classifies the input in this order: `-`, an issue URL (`issuex.ParseIssueURL`), another URL, a file, then text. Another URL is refused with `notAnIssueURL`, whose fix hint tells the person to set `GITHUB_API_URL` or `GITLAB_API_URL` for a self-hosted forge. `Truncate` cuts at `MaxInputBytes` (131072) and then appends a marker, so its output can exceed `MaxInputBytes`.
- **Triage's suggestion.** After a confirmed or probable diagnosis, triage suggests `af fix <issue url> --dir <dir>` (today `cmd/af/triage.go:689`, which `13_agent_fox_worker` moves with the triage command). That suggestion would be a usage error once `--local` is required. `docs/docs_test.go` checks that the phrase `af fix <issue url> --dir <dir>` appears in `docs/cli.md`. Triage's outcome tests (today `cmd/af/triage_outcome_test.go`, `hasNext`) check the argv exactly.
- **The service this spec is a client of** (specs 11 and 12, not yet code):
  - `PUT|GET|DELETE /api/v1/workspaces/{slug}` and `GET /api/v1/workspaces`. `PUT` answers 201 on create and 200 on replace, and the core's Design Decision 23 asks this client to accept both. `GET /api/v1/workspaces` answers 502 rather than a partial list when hub is unavailable for any one subscription.
  - `POST /api/v1/workspaces/{slug}/triage|fix`, which answers 202 with the job. The triage route accepts bodies up to 1 MiB and the fix route up to 256 KiB; every other route keeps the core's 64 KiB. The service accepts an `issue` URL only when its own `issuex.ParseIssueURL` accepts it and does not mark it a pull or merge request.
  - `GET /api/v1/jobs` and `GET /api/v1/workspaces/{slug}/jobs`, with `kind`, `state`, `issue`, `limit` and `cursor`, answering `{jobs, next_cursor}`.
  - `GET /api/v1/jobs/{id}`, which includes `attempts`.
  - `POST …/cancel` (200), `POST …/answer` with `{answer}|{option}` (202), and `POST …/retry` (202).
  - `GET …/events?since=` as SSE, with frames `id`/`event`/`data` and a `: keepalive` every 15 s. The stream ends once the job is at rest, and its last event is then a `job_state` with `at_rest: true`.
  - `GET …/attempts/{n}/envelope`.
  - Errors are `{error:{code,message}}`. The codes are `invalid_request` 400, `unauthenticated` 401, `not_found` 404 (also for an unknown route), `method_not_allowed` 405, `invalid_state`/`duplicate_job`/`not_subscribed`/`token_unreadable` 409, `payload_too_large` 413, `unsupported_media_type` 415, `invalid_hub_token` 422, `internal` 500, `hub_unavailable` 502 and `not_ready` 503.
  - A job cancelled while running ends with an attempt whose synthesized envelope may carry category `cancelled` (12 R5).
- **The credential.** PRD 01 D2 says `af login` is `afc login`'s credential file, `~/.af/config.toml`, read as is; its §7 synopsis reads `af login   afc login; the same file`, and PRD 03 §7 reads `af login   afc's credential file`. Neither names any other source. Hub's source is not in this repository, so the file's layout cannot be read here.
- **Libraries.** `github.com/BurntSushi/toml` is in the module graph, and the core makes it a direct requirement. No SSE client is in `go.mod`.

## Requirements

### R1. Choosing where a triage or fix runs

`af triage` and `af fix` run in one of two modes. The mode is chosen from the command line before anything else happens: before the flags are parsed for a mode, before the input is resolved, before a forge client is built, before a run directory is created, and before any request is sent.

- **`--local`** runs the loop on this machine. What it keeps and what changes are listed below.
- **`--workspace <slug>`** queues a job on the service (R6).
- **Neither** is a usage error. The message says to choose, and it names both `--workspace <slug>` (on the service that `--server` or `AF_URL` names) and `--local`. The `fix_hint` is `{flag: "--workspace"}` when a server is named, and `{flag: "--local"}` otherwise.
- **Both** is a usage error.

**How the words are read.**
- Only the words after the command path and before the first `--` are read. A word after `--` is the input.
- A word that is the value of a flag that takes a value, in either mode, is that flag's value and never a mode. In `af triage x --context --local`, `--local` is the context, and the invocation names no mode.
- A flag may be written with one dash or two, as the shell reads every flag.
- `--workspace <slug>` and `--workspace=<slug>` both name the service. A `--workspace` with no value, or with an empty one, is a usage error naming `--workspace`.
- `--local` is a switch. Its value follows Go's boolean flag syntax, as every boolean flag of the shell does: `--local` and `--local=true` choose the local mode, and `--local=false` counts as not given. A value that syntax refuses is a usage error naming `--local`.

**What local mode keeps.** With `--local`, `af triage` and `af fix` keep, exactly as before this spec:
- their flags and defaults;
- how they resolve input, build a forge client and create the run directory;
- their events, warnings, results, statuses, exit codes and envelopes.

**What changes on purpose in local mode,** and nothing else:
- `--local` must be given, and `--server` and `--wait` are refused (below);
- triage's fix suggestion in `next` gains `--local` (below);
- `af triage --help`, `af help triage` and their `fix` counterparts describe both modes;
- `af triage --schema` and `af fix --schema` admit both the local result and the job result (R9).

**Flags each mode refuses.** These are usage errors that name the flag:

- In local mode: `--server` and `--wait`.
- In service mode, the flags only the loop reads:
  - for both commands: `--dir`, `--repo`, `--dry-run`, `--land`, `--brain`, `--sandbox`, `--borrowed-hands`, `--budget`, `--max-turns` and `--script`;
  - for `triage`: `--label`;
  - for `fix`: `--base`, `--attempts` and `--no-verify`.
- Both modes accept `--context`, `--timeout`, `--output` and `--emit-events`.

**In-process callers.** The rule belongs to the `af` binary's command line. A caller that runs the loops in process, such as the worker of `13_agent_fox_worker`, is not subject to it and needs no `--local`. It cannot reach the client either: an in-process invocation never sends a request to an agent-fox service.

**Triage's fix suggestion.** The `next` that triage gives after a confirmed or probable diagnosis becomes `af fix <issue url> --dir <dir> --local`, so the suggestion runs as given. This applies both from the loop and from `af triage land`.

### R2. Naming the service

**Where the URL comes from.** Every service command (R3 to R8) takes `--server <url>`. Without it, the URL is `AF_URL`. An empty `AF_URL` counts as unset.

**What the URL must be.**
- An absolute `http` or `https` URL with a host.
- An optional path prefix is allowed.
- A query, a fragment or user info is not allowed.
- A trailing `/` is dropped.

The API is at `<url>/api/v1/…`. A URL that breaks a rule is a usage error that names `--server` or `AF_URL`, whichever gave it, and the rule.

**No server named.** A service command with no server is a usage error. The message names `--server` and `AF_URL`, and also `--local` for `triage` and `fix`. The `fix_hint` is `{flag: "--server", env: "AF_URL"}`. Nothing is sent.

**Plain HTTP.** An `http` URL whose host is not loopback raises the warning `server_insecure`, because the hub credential crosses the link in clear. The command goes on. Loopback means `localhost`, `127.0.0.0/8` and `::1`.

### R3. The hub credential and `af login`

**Where the credential comes from.** Every service request carries a hub credential (an API key or a PAT) as `Authorization: Bearer`. Its only source is the string key `token` at the top level of `~/.af/config.toml`, the file `afc login` writes. The file is read as is, and every other key is ignored. `~` is the home directory the operating system reports (`HOME` on Unix).

**When it cannot be used.** Each of these is checked before any request is sent, and each message names the file or path:
- The home directory cannot be found: `failed`, category `io`.
- The file does not exist: `failed`, category `auth`. The message says to run `af login --token-file FILE` or `afc login`, and the `fix_hint` is `{suggest: "af login --token-file FILE"}`.
- The file exists but cannot be read (permission denied, a directory, a read error): `failed`, category `io`.
- The file does not parse as TOML: `failed`, category `config`.
- The file's `token` is missing, is not a string, or is empty: `failed`, category `auth`, with the same fix hint.
- The credential holds a line break or a control character: `failed`, category `config`.

**File permissions.** On Unix, a credential file with any group or other permission bit set raises the warning `credential_file_permissions`, naming the file. The command goes on.

**`af login --token-file <file|->`** stores a credential. Its steps run in this order. A failing step ends the command, and nothing is written.
1. **Read the credential** from the file, or from stdin for `-`. It is never read from the command line.
   - A file that does not exist or cannot be read, or stdin that cannot be read, is `failed`, category `io`, naming the path or stdin.
   - The credential is trimmed and must be non-empty, one line, free of control characters, and at most 4096 bytes. Otherwise the command is a usage error naming `--token-file`.
2. **Find the server** (R2).
3. **Check the existing credential file**, when there is one.
   - It must be readable (else `io`) and parse as TOML (else `config`).
   - Before any table header, it must hold a top-level `token` whose value is a one-line basic or literal string on a line of its own: `token = "…"` or `token = '…'`, optionally followed by a comment.
   - A file without such a line is `failed`, category `config`, naming the file. The message says its layout is not the one `af` expects, and that `afc login` or an edit fixes it.
4. **Check the credential with the service** by sending `GET /api/v1/workspaces` with it.
   - A 2xx answer accepts the credential.
   - A 401 refuses it: `failed`, category `auth`, with a message saying the service refused this credential.
   - Any other answer, a transport error, or the request bound running out fails as R4 maps it (for example `hub`, retryable, for a 502). Its message says the credential could not be checked, not that it was refused.
5. **Write `~/.af/config.toml`.**
   - With no existing file, `~/.af` is created with mode 0700 when missing, and the file holds one line: `token = "<credential>"`, a TOML basic string.
   - With an existing file, only the value on the `token` line is replaced, by the new credential as a TOML basic string. Every other byte stays as it was, comments, other keys and tables included.
   - Either way, the content is written to a temporary file in `~/.af`, which is renamed over the old file. The file's mode is 0600.
   - A failure to create `~/.af`, to write, sync or close the temporary file, or to rename it is `failed`, category `io`, naming the path. The temporary file is removed, and an existing file is left as it was.
6. **The result** is `{credential_file, server}`, and never the credential.

**Secrecy.** Neither the credential nor a workspace PAT (R5) appears in stdout, stderr, the `--output` file, the events, any error message or any warning.

### R4. Requests and answers

**Order of the local checks.** A service command runs its local checks in this order, and the first that fails gives the envelope:
1. The mode (for `triage` and `fix`) and the flags: a missing or doubled mode, a refused flag, an unknown flag, a malformed flag value.
2. The positional arguments and the input: their number and syntax, then reading stdin or a file. This includes `af login`'s token file and `af workspace subscribe`'s PAT file.
3. The server URL (R2).
4. The credential (R3). For `af login`, the existing credential file instead (R3, step 3).

**Every request:**
- carries `Authorization: Bearer <credential>` and `User-Agent: af/<version>`, where the version is `App.Identity`'s;
- carries `Accept: application/json`, or `Accept: text/event-stream` for the event stream;
- sends any body as JSON with `Content-Type: application/json`;
- is sent only after every local check of the command has passed.

**Bounds.**
- Each request other than the event stream is bounded by 2 minutes, or by the command's remaining `--timeout` when that is shorter.
- At most 8 MiB of a response body is read.
- Redirects are not followed, so a 3xx is an answer, mapped below.

**Documents.** The service's JSON documents are carried into `af`'s results as received, so fields that `af` does not know survive. `af` reads only the fields it needs.

**How answers map:**

| The service answers | Status | `error.category` | `retryable` |
|---|---|---|---|
| 2xx | the command's (R5–R8) | — | — |
| 400 | `usage` | `usage` | false |
| 401 | `failed` | `auth` | false |
| 404 | `usage` | `usage` | false |
| 409 | `failed` | `conflict` | false |
| 422 (`invalid_hub_token`) | `failed` | `auth` | false |
| 502 (`hub_unavailable`) | `failed` | `hub` | true |
| 500 | `failed` | `service` | false |
| 503, 504 and any other 5xx | `failed` | `service` | true |
| a 3xx, or any other 4xx (405, 413 and 415 among them) | `failed` | `service` | false |
| a transport error, or the 2-minute bound running out | `failed` | `service` | true |
| a body that is too large or is not the expected JSON | `failed` | `service` | false |

**Messages.**
- The message is the service's `error.message` followed by its `error.code` in parentheses. When the body has neither, it is "the service answered `<status>`".
- A transport error names the server URL and where it came from (`--server` or `AF_URL`).
- A 404 names what was asked for: the job id, or the workspace slug. It says that the thing does not exist or that hub does not let the caller see it.

**Fix hints:**

| Answer | `fix_hint` |
|---|---|
| 401 | `{suggest: "af login --token-file FILE"}` |
| 404 on a workspace route, and 409 `not_subscribed` or `token_unreadable` | `{suggest: "af workspace subscribe <slug> --hub-token-file FILE"}` |
| 422 | `{flag: "--hub-token-file"}` |

### R5. Workspace subscriptions

| Command | Request | Result | Summary |
|---|---|---|---|
| `af workspace subscribe <slug> --hub-token-file <file\|-> [--prefix P]` | `PUT /api/v1/workspaces/{slug}` with `{hub_token, label_prefix?}` | the subscription view | 201: the workspace was subscribed. 200: its subscription was replaced |
| `af workspace list` | `GET /api/v1/workspaces` | `{workspaces}` | — |
| `af workspace get <slug>` | `GET /api/v1/workspaces/{slug}` | the subscription view | — |
| `af workspace unsubscribe <slug>` | `DELETE /api/v1/workspaces/{slug}` | `{slug}` on 204 | the open jobs of the workspace were cancelled |

- `subscribe` reads the PAT under the same rules as `af login`'s token (R3, step 1): an unreadable file or stdin is `io` naming it, and a content rule broken is a usage error naming `--hub-token-file`. It sends `--prefix` as `label_prefix` only when the flag is given. Both 200 and 201 are success.
- The slug is a required positional argument and is path-escaped. Its syntax is the service's to check: a 400 from the service maps to usage.

### R6. Queuing a job: `af triage|fix <input> --workspace <slug>`

**How the input becomes the request.** The first rule that matches wins:

1. `-` reads stdin, and is sent as `text`.
2. Any `http://` or `https://` URL is sent as `issue`, the URL string as given after trimming. The client does not classify it and consults no forge variable. The service accepts the issue URLs of the forges it knows, and refuses a pull or merge request URL and any other URL with a 400, which maps to usage carrying the service's message (R4).
3. A positive integer, optionally written with a leading `#`, is sent as `issue`, a JSON number.
4. `<path>#<N>` is sent as `issue`, the string. `<path>` is two or more `/`-separated segments of `[A-Za-z0-9_.-]`, and `N` is a positive integer.
5. The name of an existing regular file is sent as `file: {name: <base name>, content}`.
6. Anything else is sent as `text`.

`af fix` takes only rules 2, 3 and 4. Any other input is a usage error saying that with `--workspace` `af fix` takes an issue: `N`, `#N`, `owner/repo#N` or an issue URL.

**Text and file content.**
- It is trimmed, and must not be empty.
- It must hold no NUL byte and be valid UTF-8; otherwise it is a usage error.
- A directory is a usage error, and an unreadable file is `io`.
- Content longer than `MaxInputBytes` is cut at a UTF-8 boundary so that the content plus the truncation marker is at most `MaxInputBytes`. The envelope's `input.truncated` is then set, and the warning `input_truncated` is raised.

**The envelope's `input`** reports:
- `kind`: `issue`, `file`, `text` or `stdin`;
- `origin`: the URL for rule 2 or the absolute path of the file, else `""`;
- `truncated`.

**The request.** The `--context` values go, in order, as `context`, which is absent when there are none. The body is posted to `POST /api/v1/workspaces/{slug}/triage` or `/fix`.

**On 202:**
- The artifact `{kind: "job", name: <id>, url: <server>/api/v1/jobs/<id>}` is recorded at once.
- Without `--wait`:
  - the status is `done`;
  - the result is `{job: <job document>, envelope: null}`;
  - the summary names the job and the workspace;
  - `next` is `af jobs watch <id>`, with `--server <url>` added when the server came from the flag.
- With `--wait`, `af` follows the job (R7).

**Refusals** map as R4 says. A 409 `duplicate_job` carries the service's message, which names the open job.

### R7. Following a job until it is at rest

This is used by `--wait` (on `triage`, `fix`, `jobs answer` and `jobs retry`) and by `af jobs watch <id> [--since N]`.

**The stream.**
- `af` reads `GET /api/v1/jobs/{id}/events?since=<n>`, starting from 0, or from `--since` for `watch`.
- It frames events by `id:`, `event:` and `data:` lines and a blank line. Comment lines and unknown fields are ignored, and a line may be at most 1 MiB.
- Duplicates are dropped: an event whose `seq` is at most the last one delivered is not delivered again.
- Each delivered event is emitted as the `af` event `job_event`, whose data is the service's event document `{seq, ts, job_id, attempt, type, data}`.
- `watch` also writes that document to stderr as one line of compact JSON. `--wait` writes nothing to stderr.

**Tries.**
- A *try* is one request to the events route and, once its stream ends, one read of `GET /api/v1/jobs/{id}`.
- A stream ends at end of file, on a dropped connection, on an unreadable frame, and after 60 s with no byte.
- When the read says the job is at rest, the following ends and the result is built. The job is at rest when its state is `blocked`, `done`, `failed` or `cancelled` and none of its attempts is `running`.
- Otherwise `af` makes another try, with `since` set to the last `seq` delivered.

**Failed tries.**
- A try that ends without the job at rest fails when it delivered no new event. Whatever ended it counts the same:
  - end of file, a dropped connection, the 60 s idle cut or an unreadable frame;
  - a transport error or a 5xx on the events request;
  - a transport error or a 5xx on the read of the job.
- A try that delivers a new event does not fail, and it resets the count of failed tries.
- **Waits.** After a try that delivered an event, `af` makes the next try after 1 s. After the first, second, third and fourth failed try in a row, it waits 1, 2, 4 and 8 s.
- **Giving up.** The fifth failed try in a row ends the following, with no further wait. The command fails with the category R4 gives the last failure: `hub` for a 502, `service` for anything else. It is retryable, and its message names the job and says the job may still be running. `next` is `af jobs watch <id> --since <last>`, with `--server <url>` added when the server came from the flag.
- A 401, a 404, a 3xx or any other 4xx during following maps as R4 says at once, with no further try.

**The result.** Once the job is at rest:
- `af` reads the job with its attempts.
- It fetches `GET /api/v1/jobs/{id}/attempts/{n}/envelope` for the highest-numbered attempt whose state is not `queued` or `running`.
  - A 404 leaves the envelope null.
  - Any other failure leaves it null and raises the warning `attempt_envelope_unavailable`.
- The result is `{job, envelope}`.
- The warnings of that envelope are appended to `af`'s own. Its artifacts, side effects, `next` and usage are not copied, and `af`'s `usage` stays the no-brain usage.
- The following adds no `next` of its own.

**The status follows the job:**

| Job state | `af` status (exit code) | Detail |
|---|---|---|
| `done` | `result.status`: `done` (0) or `unverified` (4) | `done` when `result` is null |
| `blocked` | `needs_human` (3) | the question and option labels of `blocked`, in order; `resume.argv` is `af jobs answer <id> --wait`, with `--server <url>` added when the server came from the flag; `resume.flag` is `--context` |
| `failed` | `result.status`: `failed` (1), `unverified` (4) or `usage` (2) | a failure carries `result.error`'s category, message, retryable flag and fix hint, and is category `internal` when `result.error` is null |
| `cancelled` | `failed` (1), category `job_cancelled`, not retryable | the message says the job was cancelled on the service; the attempt envelope's category, `cancelled` or another, is not used |

**When `af` itself is stopped.** Interrupting `af`, or `--timeout` running out, ends the following and never cancels the job.
- The envelope is the shell's `cancelled` or `timeout` failure. Category `cancelled` keeps its meaning, a signal sent to `af`, while the job may still run.
- The job artifact was recorded at creation (or, for `watch`, `answer` and `retry`, before following starts), so the envelope names the job.
- Only on this path does the command add two `next` entries: `af jobs watch <id>` and `af jobs cancel <id>`, each with `--server <url>` added when the server came from the flag.

### R8. Reading and acting on jobs

| Command | Request | Result |
|---|---|---|
| `af jobs list [--workspace S] [--state S] [--cursor C]` | `GET /api/v1/workspaces/{S}/jobs` with `--workspace`, else `GET /api/v1/jobs`, with `--state` and `--cursor` as the `state` and `cursor` query parameters when given | the page `{jobs, next_cursor}`; when `next_cursor` is not null, `next` repeats the command with `--cursor <next_cursor>` |
| `af jobs get <id>` | `GET /api/v1/jobs/{id}` | `{job, envelope: null}`, status `done` |
| `af jobs watch <id> [--since N]` | R7 | R7 |
| `af jobs cancel <id>` | `POST /api/v1/jobs/{id}/cancel` | `{job, envelope: null}`, status `done` |
| `af jobs answer <id> (<text> \| --context <text> \| --option <id>) [--wait]` | `POST /api/v1/jobs/{id}/answer` | `{job, envelope: null}`, status `done`, or R7 with `--wait` |
| `af jobs retry <id> [--wait]` | `POST /api/v1/jobs/{id}/retry` with `{}` | `{job, envelope: null}`, status `done`, or R7 with `--wait` |

- **`list`.** Its values are passed through, and the service validates them: a 400 maps to usage.
- **`answer`** takes exactly one answer; none, or more than one, is a usage error.
  - An `--option` value is uppercased and sent as `{option}`.
  - A text answer, positional or `--context`, must be non-empty after trimming. `af` first reads the job. When the text equals an option id of the blocked question, ignoring case, it is sent as that `{option}`; otherwise it is sent as `{answer}`.
- **A 409** (`invalid_state`, `not_subscribed`, `token_unreadable`) maps to `conflict` as R4 says.
- **Additions to PRD 03's synopsis.** `--cursor` on `list`, `--since` on `watch`, `--context` and `--wait` on `answer`, and `--wait` on `retry` are not in PRD 03 §7. Each is justified in Design Decisions 25, 26, 30 and 43.

### R9. Schema, help, documentation and tests

**Schemas.**
- Every new command registers a result schema. The job result `{job, envelope}`, the job page, the subscription view, `{workspaces}`, `{slug}` and `{credential_file, server}` describe the fields `af` relies on and allow the others.
- `af triage --schema` and `af fix --schema` admit both the local result and the job result.
- The `--schema` golden (`cmd/af/testdata/schema.golden.json`) is rewritten on purpose.

**Documentation.** Run `make doc` first. Then update:
- `docs/cli.md`:
  - the mode rule, including how the words are read;
  - the new commands and flags;
  - R4's order of local checks and its table;
  - the categories `service`, `conflict`, `hub` and `job_cancelled`, with `cancelled` still defined as a signal sent to the process;
  - the warnings `server_insecure`, `credential_file_permissions` and `attempt_envelope_unavailable`;
  - the event `job_event`;
  - the `needs_human` section: a command may supply its own `resume.argv`; a blocked job's is `af jobs answer <id> --wait`, which takes the appended `--context`;
  - the triage and fix synopses with `--local`;
  - the triage `next` written as `af fix <issue url> --dir <dir> --local`;
  - the count of registered commands.
- `docs/configuration.md` and `docs/model-usage.md`:
  - `AF_URL`;
  - `~/.af/config.toml` and its `token` key;
  - that the service mode runs no model on this machine, and that a job's cost is `result.job.usage`.
- `docs/architecture.md`: `af` as the client, in the service section.
- `docs/development.md`: the layout table, for any package the client adds.
- `README.md`: the client quick start.

A docs test checks these terms. Every divergence found while implementing is recorded in `docs/errata/16_*.md`.

**Tests.** Every test runs offline.
- At least one test runs against the real HTTP handler of specs 11 and 12, in process over `fakehub`, with the jobs layer's fake runner.
- The failure shapes run against a scripted fake service. These are R4's rows, redirects, cut streams, idle streams, duplicate frames and five failed tries.
- A sentinel-credential scan covers stdout, stderr, `--output` and `--emit-events`.
- The mode tests prove that a refused invocation sends no request to the service or the forge and creates no run directory. They include a mode flag given as another flag's value.
- `af login` tests cover a first write, a byte-preserving rewrite, a refused layout, each `io` failure leaving the file intact, a 401 and a 502.

## Design Decisions

1. **This is the sixth scope of PRD 03's split, and the service is not changed.** Every route and document it uses is already specified by `11_agent_fox_service_core` and `12_agent_fox_jobs`.
2. **`--local` is required for the loop, and giving neither mode is a usage error.** PRD 02 says local "needs no flag until the service exists", and PRD 03 says "with neither and no `--local`, `af` says which to use". A guessed mode would send a person's report to the wrong place.
3. **The shell chooses the mode before it parses, resolves input, builds a forge client or creates a run directory.** The shell prepares all of that from the static declaration of a command. Choosing later would let a refused invocation read an issue from the forge and leave a run directory behind.
4. **`--local` with `--workspace`, `--server` or `--wait` is a usage error.** PRD 01's local run reporting to hub is not built in any scope of this split. Refusing it keeps the combination free for later.
5. **A local-only flag given with `--workspace` is a usage error naming the flag.** A `--brain` or a `--dir` that silently does nothing is worse than a refusal, and the service reads its settings from hub variables.
6. **The mode rule binds only the `af` binary's command line. The worker's in-process invocation needs no `--local` and cannot reach the client.** The worker already invokes the loops without `--local`. A worker that could queue a job on a service would be a loop.
7. **Triage's fix suggestion appends `--local` at the end: `af fix <url> --dir <dir> --local`.** The suggestion must run as given. Putting `--local` last keeps the documented phrase `af fix <issue url> --dir <dir>` as a prefix, so spec 08's docs test still holds. The exact-argv assertions in triage's outcome tests are updated wherever `13_agent_fox_worker`'s move has put them.
8. **`--server` wins over `AF_URL`. The URL follows the core's `hub.url` rules, and user info is refused.** A flag is the more specific intent. A password in a URL would leak into `next` and `resume`.
9. **No server URL is stored anywhere, and `af login` writes nothing into afc's file but the value of `token`.** `AF_URL` in a shell profile does the job. Adding a key afc does not know to afc's file risks clobbering it. `token` is the one key both programs are taken to read. `af login` replaces only its value and leaves every other byte as it was, and it refuses a file whose layout is not the expected one (Design Decision 11). So the objection to a stored server URL does not reach `af login`.
10. **The credential is the `token` key of `~/.af/config.toml`, and nothing else.** PRD 01 D2 and §7 and PRD 03 §7 name the file and no other source. An environment variable such as `AF_TOKEN` would be a new user-facing surface that neither input asks for, so it is not added; the question is left to the input's owner. Only the client knows the key's name, so a correction is a one-line change.
11. **`af login` reads the token from `--token-file` (or `-`), checks it with the service, and changes only the value of `token`.**
    - A token on the command line would land in shell history.
    - A browser flow needs hub's OAuth details, which this repository does not have.
    - Checking with the service stores no token that would fail at first use.
    - PRD 01 §7 makes `af login` "afc login; the same file", so it writes that file rather than one of its own.
    - Replacing only the value keeps afc's comments, keys and tables.
    - Refusing a file with no top-level one-line `token` keeps `af` from adding a key afc may not read, if afc's layout differs from the one assumed.
12. **A credential file that others can read gets a warning, not a refusal.** `afc` may write the file with its own mode, and refusing would lock out a person who already logged in.
13. **Each request is bounded by 2 minutes, and the stream by 60 s without a byte. `--timeout` defaults to no limit for every client command.** The service's unsubscribe waits up to its 60 s grace plus hub calls. The service sends a keepalive every 15 s. A wait may last as long as a queue.
14. **Redirects are not followed.** A bearer token must never reach a host the person did not name. The core's hub client makes the same choice.
15. **Service documents are carried verbatim.** A field the service adds later then reaches scripts without a new `af`.
16. **400 and 404 are `usage` (exit 2), 401 and 422 are `auth`, 409 is `conflict`, 502 is `hub` (retryable), and the rest are `service`.** Exit 2 tells a script to fix the invocation, which is the right reading of a malformed request or a name that does not exist. A conflict is about state, not the invocation. A hub outage and a service outage are retried differently by an operator.
17. **The new categories are `service`, `conflict`, `hub` and `job_cancelled`. `hub` is the same category `13_agent_fox_worker` uses for hub outages.** One category for one cause across `af` and the attempt envelopes.
18. **In service mode, a bare positive integer (or `#N`) is an issue number. In local mode it is still text.** A workspace has one repository, so a number names an issue unambiguously. The service already accepts numbers.
19. **The client cuts text and file content so that it fits `MaxInputBytes` with the marker.** The service refuses content over `MaxInputBytes` (12 Design Decision 15), and `toolio.Truncate`'s output can exceed that limit by the length of the marker.
20. **Every `http://` or `https://` URL is passed to the service as `issue`, and the service accepts or refuses it.** Which forge hosts are known depends on the service's environment, not the client's. A client that classified URLs with its own `GITHUB_API_URL` would refuse a self-hosted forge the service knows, and local mode's hint to set that variable would mislead. The refusal of a pull request, a merge request or an unknown URL then lives in one place, and its 400 maps to a usage error carrying the service's message.
21. **Job commands return `{job, envelope}`, with `envelope` null unless the command followed the job until it was at rest.** One result type serves `--wait`, `watch`, `get`, `cancel`, `answer` and `retry`, and a script reads the job the same way after each.
22. **The status of `--wait` and `watch` follows the job's last finished attempt. `cancelled` becomes `failed`/`job_cancelled`, and the attempt's `usage` status is passed on.** PRD 03 says `--wait` exits with the envelope's exit code. A cancelled job did not do what was asked, whatever its runner returned.
23. **Only the warnings of the final attempt envelope are copied into `af`'s envelope.** A script checks warnings the same way after a local run and a service run. The worker's artifacts and `next` name paths on the worker's host, and the job document already carries the side effects and the summed usage.
24. **A blocked job's resume is `af jobs answer <id> --wait` with the flag `--context`.** The envelope schema fixes `resume.flag` to `--context`. Resuming the original command would create a second job, which the service would refuse as `duplicate_job`. `--wait` gives a model-driven caller the outcome, as resuming a local run does. `docs/cli.md`'s `needs_human` section, which today says `resume.argv` is the invocation as received, is updated to say a command may supply its own.
25. **`af jobs answer` accepts positional text, `--context` or `--option`. Text equal to one of the blocked question's option ids is sent as that option.** Positional text and `--option` are PRD 03's syntax. `--context` is an addition, needed because the resume flag is fixed to `--context`. Sending the option keeps its label as the answer, which 12 Design Decision 11 relies on.
26. **`answer` and `retry` take `--wait`, and `cancel` does not.** `--wait` on `answer` is what the resume argv runs. `--wait` on `retry` is an addition to PRD 03's synopsis: a retry queues an attempt worth following, as an answer does, through the same following. It is listed as an open question for the input's owner. A cancel takes effect at once.
27. **The following ends only when a fresh read of the job says it is at rest. Otherwise `af` tries again from the last `seq`, and gives up after five failed tries in a row (Design Decision 38).** End of file cannot tell the server's end from a dropped connection, and replay from `since` makes a reconnect lossless.
28. **Interrupting `af`, or its `--timeout`, never cancels the job. The job is named before the wait starts, and `watch` and `cancel` are suggested only on that path.** The job is durable on the service. A Ctrl-C that cancelled work in a shared queue would surprise the person and everyone who watches the job. `Run.Next` only appends, so the two entries are added when the interruption is seen, not when following starts; otherwise every finished `--wait` would suggest cancelling a job at rest.
29. **`watch` echoes events to stderr. `--wait` does not. Both re-emit the events as `job_event`.** Watching is the purpose of `watch`. `--wait` is for scripts and keeps local mode's quiet stderr, and `--emit-events /dev/stderr` shows the events live.
30. **`af jobs list` returns one page, with `next` carrying the cursor.** A listing of every job could be long and slow. `--cursor` is an addition to PRD 03's synopsis, without which `next` could not name the next page. `--kind`, `--issue` and `--limit` are not added, because PRD 03 does not list them and nothing in this scope needs them.
31. **`af workspace subscribe` accepts both 200 and 201.** The core answers 200 on replace (its Design Decision 23).
32. **The requirements state the client's behaviour, and leave packages, types and the shell's mechanisms to design.** The core and the jobs spec made the same choice (their Design Decision 36). The observable rules stay: the mode is decided before any forge read, run directory or request; a blocked job resumes with `af jobs answer <id> --wait`; and a command that reads its own input reports it in `input`. `docs/development.md`'s rule, that library packages return plain errors and commands map them, applies to whatever packages design adds.
33. **The tests run against the real service handler for the contract, and against a scripted fake for failure shapes.** The real handler proves that the routes and documents agree. A fake is the only way to produce a cut stream or a 3xx on demand.
34. **Local mode keeps its flags, defaults, input handling, run directory, events, results, statuses, exit codes and envelopes; four things change on purpose.** These are: `--local` is required, with `--server` and `--wait` refused; triage's fix `next` gains `--local`; the help describes both modes; and `--schema` admits both results. Goal 1 tests only the kept items, so it can pass.
35. **A job cancelled on the service is category `job_cancelled`; `cancelled` keeps meaning a signal sent to `af`.** `docs/cli.md` defines `cancelled` as a signal to the process. A script must tell a job that is gone from a job still running after `af` was interrupted, and the category is the field it reads.
36. **I/O failures are `io` and name the path, and a failed write leaves the existing credential file as it was.** `docs/cli.md` defines `io` as a file or stdin that could not be read or written. A temporary file and a rename make the replacement all or nothing.
37. **The mode scan reads words the way the shell's flag parse does.** The value of a value-taking flag is never a mode, as `requests` already treats `--help`. One or two dashes and the `=` form are accepted, and `--local` follows Go's boolean syntax like every boolean flag of the shell. Otherwise the scan and the parse could disagree about the same invocation.
38. **Every fruitless try counts the same, the waits are fixed, and giving up keeps the last failure's category.** The idle cut, end of file and a failed read of the job all mean the same thing to a follower: no progress. Waits of 1, 2, 4 and 8 s give a restarting service about 15 s plus the tries themselves. A 502 during following is the same hub outage as anywhere else, so it stays `hub`.
39. **Local checks run in a fixed order: the mode and flags, the arguments and input, the server, then the credential.** Every usage mistake is reported before an `auth` or `config` failure, so the exit code of a combined mistake does not depend on the implementation.
40. **The service's full error-code table is in Background, and 405, 413 and 415 fall under "any other 4xx".** None should follow from a valid invocation: the client uses only the routes and methods of specs 11 and 12, sends JSON, and cuts content to fit the triage route's 1 MiB limit. When one does happen, it is a client bug, which `service`, not retryable, says.
41. **`af login`'s check refuses a credential only on a 401.** `GET /api/v1/workspaces` answers 502 when hub is unavailable for any one subscription. Any answer other than 2xx or 401 says the credential was not checked, writes nothing, and keeps R4's retryable flag, so a valid credential is never reported as refused.
42. **Files are named by their role, with paths as they stand before `13_agent_fox_worker`'s move.** That spec moves triage, fix and their unit tests into one importable package, and keeps the `--schema` golden at `cmd/af/testdata/schema.golden.json`.
43. **`--since` on `af jobs watch` is an addition to PRD 03's synopsis.** It is what the `next` of a following that gave up runs, so the person resumes without a replay of every event.

## Dependencies

| Spec | Relationship | Why |
|---|---|---|
| `05_af_cli_envelope` | modifies | The shell's behaviour changes in three observable ways: `triage` and `fix` choose a mode before flags are parsed, a forge client is built, input is resolved or a run directory is created (R1); a `needs_human` envelope may carry a resume argv other than the invocation (R7); and a command that reads its own input reports it in the envelope's `input` (R6). `docs/cli.md`'s category, warning, event and `needs_human` sections change. |
| `08_af_triage` | modifies | `af triage` requires `--local` for the loop. The fix suggestion in triage's `next` gains `--local`. Tests that call the loop add `--local`. |
| `09_af_fix` | modifies | `af fix` requires `--local` for the loop. Tests that call the loop add `--local`. |
| `11_agent_fox_service_core` | depends on | The subscription routes and view, the error envelope and its codes, `PUT`'s 200 and 201, and `fakehub` for the end-to-end test. |
| `12_agent_fox_jobs` | depends on | The jobs routes, the job and attempt documents, the SSE framing and the at-rest end of a stream, the answer/retry/cancel semantics, and the fake runner for the end-to-end test. |
| `13_agent_fox_worker` | depends on | The commands, triage's fix suggestion and their unit tests live where its move puts them. Its in-process invocation of the loops must keep working without `--local` (R1). |

## Verified External API

**The agent-fox service API** is specified by `.specs/11_agent_fox_service_core/prd.md` (R6–R9) and `.specs/12_agent_fox_jobs/prd.md` (R2, R3, R6–R8). It is planned and not yet code, so the routes and documents in Background are taken from those PRDs. The SSE frame is `id: <seq>`, then `event: <type>`, then `data: <one-line JSON {seq, ts, job_id, attempt, type, data}>`. `since` takes precedence over `Last-Event-ID`.

**`github.com/agent-fox-dev/aflib/issuex`** is unverified at the source, but this repository compiles against it. The client itself does not classify URLs in service mode (R6); the service does.
- `func ParseIssueURL(s string) (IssueRef, bool)` (`internal/toolio/input.go:87`);
- `IssueRef.IsPullRequest` and `IssueRef.URL()`.

**`github.com/BurntSushi/toml` v1.6.0** is unverified, because its source is in the module cache. It is indirect in `go.mod`, and the core makes it direct.
- `func Decode(data string, v any) (MetaData, error)` / `func DecodeFile(path string, v any) (MetaData, error)`, decoding into `map[string]any`, used to check that the credential file parses and to read `token`;
- `func NewEncoder(w io.Writer) *Encoder` and `func (*Encoder) Encode(v any) error`, usable to encode the new `token` value as a basic string.

**Hub's `afc` credential file** is unverified, because hub's source is not in the repository. PRD 01 D2 and §7 give the path `~/.af/config.toml`. Assumed: TOML with a top-level string key `token`, written on a line of its own, that holds the API key or PAT. Only the client's credential code knows the key's name.

**Standard library:**
- `net/http.Client`, with `CheckRedirect` returning `http.ErrUseLastResponse`;
- `http.NewRequestWithContext`;
- `bufio.Reader` for SSE lines;
- `encoding/json.RawMessage`;
- `os.UserHomeDir`, `os.CreateTemp`, `os.Rename` and `os.Remove`;
- `unicode/utf8.ValidString`;
- `strconv.ParseBool` for `--local=<value>`;
- `net.ParseIP(...).IsLoopback()`.
