# Configuration reference

How to configure `af` (the command-line tool), the files it keeps in a repository, `agent-fox` (the
service), and the per-workspace settings the service reads from hub. `af` needs no configuration file:
it reads a few environment variables, and everything else is a flag (see [cli.md](cli.md)). The service
reads one TOML file.

## Contents

- [af](#af)
  - [Forge access](#forge-access)
  - [Model and credentials](#model-and-credentials)
  - [Sandbox](#sandbox)
  - [Service client](#service-client)
- [Repository files](#repository-files)
  - [gate.json](#gatejson)
  - [guard.json](#guardjson)
- [The agent-fox service](#the-agent-fox-service)
  - [Where the file is](#where-the-file-is)
  - [Keys](#keys)
  - [Paths and variables in the file](#paths-and-variables-in-the-file)
  - [The key file](#the-key-file)
  - [The state file](#the-state-file)
- [Workspace variables on hub](#workspace-variables-on-hub)
  - [The label poll](#the-label-poll)
- [Environment variable index](#environment-variable-index)

## af

### Forge access

`af triage` and `af fix` read issues from GitHub or GitLab and write back to them. Set a token for
the forge you use:

| Variable | Used for |
|---|---|
| `GITHUB_TOKEN` | The GitHub token. |
| `GH_TOKEN` | The same, under the name the `gh` CLI uses. Read when `GITHUB_TOKEN` is not set. |
| `GITLAB_TOKEN` | The GitLab token. |
| `GITHUB_API_URL` | The API URL of a GitHub Enterprise Server, such as `https://ghe.example.com/api/v3`. Default `https://api.github.com`. |
| `GITLAB_API_URL` | The API URL of a self-hosted GitLab, such as `https://gitlab.example.com/api/v4`. Default `https://gitlab.com/api/v4`; `/api/v4` is added if missing. |

**When a token is needed.** Without a token, `af` can read public issues only; a `404` then fails as
`auth`, because the issue may be private, and `fix_hint.env` names the variable to set. Writing to the
forge needs a token: `af triage` landing on an issue, and `af fix --land pr` opening a pull request.
Without one the run fails as `auth` before it spends anything. `af triage --dry-run` and `--land none`,
and `af fix --land branch` and `--land none`, need no forge token to write.

**Which forge.** `af` uses the forge the issue URL names. Without an issue URL, it uses the one
`--repo` and the environment point to, then the one the `origin` remote of `--dir` names. If only
GitHub variables are set it picks GitHub, if only GitLab variables, GitLab; if both or neither, the
`origin` remote decides. A host whose name contains neither `github` nor `gitlab`
(`git.example.com`) needs its API URL in `GITHUB_API_URL` or `GITLAB_API_URL`; until then an issue URL
on that host is a `usage` error.

**Where a token goes.** A token is sent only to the host it belongs to: the host of `GITHUB_API_URL`
or `GITLAB_API_URL` when that is set, otherwise the public forge. Requests to any other host carry no
token.

### Model and credentials

The `agentkit` brain, which `af triage` and `af fix` use by default, calls an Anthropic model.

| Variable | Used for |
|---|---|
| `AF_MODEL` | The model, as the AgentKit catalog names it. Empty means the catalog's default, `claude-sonnet-5-5`. |
| `ANTHROPIC_API_KEY` | The API key for the model provider. |

On the host, the brain also accepts the other credentials AgentKit's Anthropic provider supports:
`ANTHROPIC_AUTH_TOKEN` (or `ANTHROPIC_OAUTH_TOKEN`), an optional `ANTHROPIC_BASE_URL`, and Vertex AI or
Bedrock when `CLAUDE_CODE_USE_VERTEX=1` or `CLAUDE_CODE_USE_BEDROCK=1`. Inside a `podman` sandbox the
brain supports **only** `ANTHROPIC_API_KEY`. Without a usable credential the run fails as `auth`, with
`fix_hint.env` `ANTHROPIC_API_KEY`, before a sandbox is opened.

A model that the catalog does not list has no known price. The run then reports no cost
(`cost_source` `none`, warning `model_unpriced`), and `--budget` cannot be enforced, so a run with
`--budget` fails as `usage` naming `AF_MODEL`. See [model-usage.md](model-usage.md).

### Sandbox

The brain works on a copy of the repository inside a sandbox ("box"). See
[cli.md](cli.md#sandboxes-and-brains) for the flags.

| Variable | Used for |
|---|---|
| `AF_SANDBOX` | The sandbox provider when `--sandbox` is not given: `podman` (the default on every OS) or `local`. Empty means unset; an unknown value is a `usage` error naming the variable. If the provider cannot start, `af` fails; it never falls back to `local`. |
| `AF_SANDBOX_IMAGE` | The image of a `podman` sandbox. Default `quay.io/agentfox/tools:latest`: the sandbox image with `af` installed, built by `make build-tools-container`. There is no flag for it. |

**What enters the sandbox.** No forge token (`GITHUB_TOKEN`, `GH_TOKEN`, `GITLAB_TOKEN`) ever enters a
sandbox, and credentials in the checkout's `.git/config` are stripped from the sandbox's copy. What
else the sandbox gets depends on where the brain runs:

| Brain | Credentials in the sandbox | Network |
|---|---|---|
| `agentkit` under `podman` (runs inside the sandbox) | `ANTHROPIC_API_KEY`, if set on the host | open: the whole network, since Podman cannot restrict it to the model vendor |
| `agentkit` under `local`, or `scripted` (run on the host) | none | `none` for triage (its tools only read), `open` for fix; under `local` nothing is enforced |

Under `podman`, the API key appears on the container engine's command line on the host while the
sandbox lives, where other users of the machine can read it. The `local` provider has no isolation:
tools run in the `af` process with its privileges, and every run says so with the warning
`box_unconfined`. Use `local` for tests and trusted repositories only. See
[architecture.md](architecture.md#boxes-and-brains) for details.

### Service client

`af workspace …` and `af jobs …` talk to the agent-fox service. See
[cli.md](cli.md#connecting-to-the-service).

**The server** comes from `--server <url>`, or from `AF_URL` when the flag is absent (an empty
`AF_URL` counts as unset). It must be an absolute `http` or `https` URL with a host and an optional
path prefix, and no query, fragment or user info; a trailing `/` is dropped. A plain `http` URL to a
host that is not loopback raises the warning `server_insecure`, because the credential crosses the
network in clear.

**The credential** is your hub credential (an API key or a personal access token). Its only source is
the top-level `token` key of `~/.af/config.toml`:

```toml
# ~/.af/config.toml
token = "the credential"
```

hub's `afc login` writes this file; you can also write it yourself, with mode `0600`. `af` reads it and
never writes it, and ignores every other key. There is no environment variable for the credential. On
Unix, a file readable by group or others raises the warning `credential_file_permissions`.

The credential and any workspace token you hand over (8 bytes or longer) never appear in `af`'s
output, events or messages. The client honours `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY`.

## Repository files

`af` keeps its local state in `.agent-fox/` at the top of the checkout it works in (`--dir`). The
directory has its own `.gitignore` that ignores everything in it, so it never shows in `git status`;
the repository's own `.gitignore` is never edited. Nothing in it counts as part of the repository when
`af` detects languages, hashes the tree or maps the code.

| Path | What it is |
|---|---|
| `.gitignore` | Ignores the whole directory. |
| `gate.json` | Optional. Pins the checks the gate runs; see [gate.json](#gatejson). |
| `guard.json` | Optional. The policy that limits what a brain may do; see [guard.json](#guardjson). |
| `gate/<id>/` | Logs and the cached result of a gate run (see [cli.md](cli.md#af-gate-run)). Safe to delete. |
| `revert.patch` | Exists only while `af revert-check` has a change taken out of the tree. If one is left behind, a check was killed or could not put the change back: inspect the tree, and when `git apply --check .agent-fox/revert.patch` passes, run `git apply .agent-fox/revert.patch` and delete the file. See [cli.md](cli.md#af-revert-check). |
| `run/<id>/` | One directory per run of a command that keeps one: the envelope, events, and the files of a triage or fix run. Safe to delete; only a later run of the same input with `--context` reads an earlier one. See [cli.md](cli.md#the-run-directory). |

### gate.json

The **gate** is the list of checks `af` runs to judge a change: lint and tests. Without `gate.json`,
`af` detects the checks from the repository (a Makefile `check` target, else `test` and `lint`
targets, else the language manifests; see [cli.md](cli.md#af-lang-detect)). `gate.json` pins the list
so that it does not change with the files. `af lang detect --write` writes it from what it detects;
you can also write it by hand.

```json
{
  "version": 1,
  "checks": [
    {"name": "make check", "argv": ["make", "check"], "kind": "check", "source": "Makefile:check"}
  ]
}
```

| Field | Meaning |
|---|---|
| `version` | Must be `1`. |
| `checks[].argv` | The command as a list of words; no shell. Required. |
| `checks[].name` | The check's name, unique in the file. Default: `argv` joined by spaces. |
| `checks[].kind` | `lint`, `test` or `check` (both). Default `check`. |
| `checks[].source` | Where the check came from. Default `gate.json`. |

An `available` field is accepted and ignored: whether a program exists is worked out on each run. Any
other field, more than one JSON document, or a check that cannot run (no `argv`, a repeated `name`, an
unknown `kind`) makes the file unusable. Commands then fail as `config`, naming the file; `af` does not
fall back to detection.

### guard.json

When a brain asks to run a shell command or write a file, `af` checks the request against a policy
first. The policy blocks git writes (af makes every commit and push itself), writes to protected
paths, running the full test suite directly (use `af gate run`, which caches results), and optionally
any program not on an allowlist. The rules are described in
[architecture.md](architecture.md#the-tool-policy).

`guard.json` configures that policy. The policy is `.agent-fox/guard.json` when it exists, otherwise
the defaults below. `af fix` writes its own `guard.json` into the sandbox for each run, built from the
detected gate.

```json
{
  "version": 1,
  "suite": [["make", "check"], ["go", "test", "./..."]],
  "protected_paths": [".git/", ".agent-fox/", ".specs/", ".github/workflows/", ".gitlab-ci.yml"],
  "allow_programs": []
}
```

| Key | Meaning | Default when missing |
|---|---|---|
| `version` | Must be `1`. | required |
| `suite` | Commands that count as the full test suite, each a list of words. A shell command that matches one, ignoring flags, option values and `NAME=value` words, is refused with a pointer to `af gate run`. Targeted test runs stay allowed. | The suite that [`af lang detect`](cli.md#af-lang-detect) reports. |
| `protected_paths` | Paths, relative to the checkout, that only `af` writes. A trailing `/` marks a directory. Paths may not be absolute or leave the checkout; they are matched case-insensitively, after cleaning (`./docs/` and `a/../docs` both mean `docs`), and through symlinks. | `.git/`, `.agent-fox/`, `.specs/`, `.github/workflows/`, `.gitlab-ci.yml` |
| `allow_programs` | If not empty, the only programs a shell command may run, as spelled (`/usr/bin/ls` is not `ls`). `af`, `git` and shell builtins are always allowed. | empty: no allowlist |

A key that is present is used as given, even an empty list: `"protected_paths": []` protects nothing.
An unknown key, another version, or an entry that cannot be used makes the file unusable, and the run
fails as `config`, naming the file.

## The agent-fox service

`agent-fox` reads one TOML file. A minimal file sets two keys:

```toml
[hub]
url = "https://hub.example.com"

[secrets]
key_file = "key"
```

Apart from the file, the service reads only:

- the variables the file names with `${NAME}` (see [below](#paths-and-variables-in-the-file));
- `XDG_CONFIG_HOME`, `XDG_STATE_HOME` and `HOME`, to find the file and the default database;
- `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY`, for its calls to hub. Those calls carry caller and
  workspace tokens, so a configured proxy sees them.

The service holds no model API key and no forge token. The only credentials it handles are the tokens
callers send (checked with hub, never stored) and the workspace tokens handed over when a workspace is
subscribed (stored encrypted; see [the key file](#the-key-file)). Per-attempt settings such as the
model and budget come from [hub](#workspace-variables-on-hub), not from this file.

### Where the file is

The file is the `--config` path when given. Otherwise it is the first that exists of:

1. `./config.toml`
2. `$XDG_CONFIG_HOME/agent-fox/config.toml` (or `$HOME/.config/agent-fox/config.toml` when
   `XDG_CONFIG_HOME` is unset, empty or not absolute)

If none exists, the service does not start and the error lists the paths it tried.

### Keys

| Key | Type | Default | Rule |
|---|---|---|---|
| `server.listen` | string | `127.0.0.1:8090` | A `host:port`. Port `0` picks a free port; the `listening` log record shows which. |
| `hub.url` | string | **required** | An absolute `http` or `https` URL, with an optional path prefix and no query or fragment. A trailing `/` is dropped. |
| `hub.auth_cache_ttl` | duration | `60s` | `1s` to `15m`. How long hub's answers about callers are cached (see [api.md](api.md#authentication)). |
| `database.path` | string | `$XDG_STATE_HOME/agent-fox/agent-fox.db` (or `$HOME/.local/state/agent-fox/agent-fox.db`) | The SQLite state file. Created, with its directory, when missing. |
| `database.retain` | duration | `720h` (30 days) | `1h` to `8760h`. How long a finished job is kept, counted from when it finished. See [api.md](api.md#retention). |
| `workers.count` | integer | `2` | `1` to `64`. How many attempts run at once across all workspaces. A workspace's own attempts always run one at a time, in order, so a count of 4 lets four workspaces work at once. |
| `secrets.key_file` | string | **required** | The file holding the encryption key; see [the key file](#the-key-file). |

Durations use Go syntax (`60s`, `15m`, `720h`). A value outside its range is refused, not clamped.

The file accepts no other key: an unknown key (a typo such as `auth_cach_ttl`, or `server.external_url`)
stops the service with an error naming it. So does a value of the wrong type, or an empty value for a
required key or a key with a default.

**Plain HTTP to hub.** An `http` `hub.url` to a host that is not loopback is accepted, since
compose-style private networks are common, but the service logs a warning at start: tokens cross the
link in clear.

### Paths and variables in the file

**Relative paths.** A relative `database.path` or `secrets.key_file` is resolved against the directory
of the configuration file, not the working directory, so the file means the same thing wherever the
service is started.

**Variables.** `${NAME}` in a string value is replaced by the environment variable `NAME` (`NAME`
matches `[A-Za-z_][A-Za-z0-9_]*`). Substitution happens after the file is parsed, so a value cannot
break the file's quoting. A variable set to the empty string gives an empty string (and a required key
left empty then fails as missing). A variable that is **not set** stops the service with an error
naming the variable and the key. A `$` that does not start `${NAME}` is left alone.

### The key file

`secrets.key_file` holds the key that workspace tokens are encrypted with: **exactly 32 random bytes,
base64-encoded** (surrounding whitespace is ignored). Create one with:

```
(umask 077; openssl rand -base64 32 > key)
```

The `umask` matters. The service refuses a key file that has any group or other permission bit set
(`0600`, `0400` and `0700` are fine), just as ssh does for a private key. It also refuses a file that is
missing, is not base64, or does not decode to 32 bytes. The error names the file and the reason, never
the contents.

Tokens are encrypted with AES-256-GCM, bound to their workspace. Neither the key nor a token is ever
logged or written in plain text.

**Changing the key.** If you replace the key file, the service still starts, but subscriptions made
under the old key show `token_state: unreadable` and cannot run jobs until the workspace is subscribed
again (or removed). Keep the old key to keep them. There is no key rotation.

### The state file

All of the service's state is one SQLite file (`database.path`). While the service runs, SQLite's
`-wal` and `-shm` files sit beside it. The service creates the directory with mode `0700` and the file
with mode `0600`, plus a directory `attempts/` beside the file that holds each attempt's envelope and
report.

- **Do not share it.** Two `agent-fox` processes must not use the same file.
- **Other tables are safe.** The service's tables are prefixed `af_`, and it never touches tables it
  does not own.
- **Migrations run at start.** A file written by a newer `agent-fox` stops the start with an error
  naming both versions, and is left untouched. So does a file made by an unreleased build from before
  October 2026: delete it and the service creates a new one.

## Workspace variables on hub

The service takes each attempt's settings from the **variables of its hub workspace**, resolved by hub
and read at the start of every attempt. The service's own environment does not affect attempts.

An unset or empty variable takes its default, and an unknown variable is ignored. A value out of range
fails the attempt before anything runs: a `failed` envelope of category `config`, not retryable, whose
message names the variable and value and whose `fix_hint.var` names the variable.

| Variable | Triage default | Fix default | Allowed values |
|---|---|---|---|
| `AF_MODEL` | the catalog's default Anthropic model | the same | any non-empty string |
| `AF_MAX_BUDGET_USD` | `2` | `10` | a finite number above 0 |
| `AF_MAX_TURNS` | `100` | `150` | a whole number, 1 or more |
| `AF_TIMEOUT` | `15m` | `45m` | a duration from `1m` to `24h`. Cut, if needed, to end two minutes before the sandbox lease expires (warning `lease_shortened_timeout`). |
| `AF_MAX_ATTEMPTS` | `2` | `2` | 1 to 10: the most attempts in a round, counting automatic retries (see [api.md](api.md#how-an-attempt-ends)). |
| `AF_SANDBOX_PROFILE` | hub's default | hub's default | non-empty: the hub sandbox profile to lease from. Its image must contain `af`, ideally of the same version as the service (another version raises the warning `box_af_version_mismatch`). |
| `AF_FIX_LAND` | — | `pr` | `pr`, `branch` or `none`. `patch` is refused: fixes on `carry_patch` workspaces fail as `unsupported`. |
| `AF_POLL_INTERVAL` | `5m` | `5m` | see [the label poll](#the-label-poll) |

The brain is always `agentkit`. `AF_BRAIN`, `AF_BRAIN_TRIAGE` and `AF_BRAIN_FIX` are ignored if a
workspace still sets them. The model's API key comes from the sandbox profile on hub, not from the
service; see [model-usage.md](model-usage.md#under-the-service).

### The label poll

The service turns [request labels](api.md#request-labels) on a workspace's issues into jobs by asking
hub for labelled issues at a regular interval. `AF_POLL_INTERVAL` is its only setting; each cycle
reads it fresh, so a change takes effect at the next cycle.

| `AF_POLL_INTERVAL` | Effect |
|---|---|
| unset or empty | A cycle every 5 minutes. |
| zero (`0`, `0s`, `0m`) | Polling is off for the workspace: no jobs from labels, and no answers or retries from the issue. The worker's comments then stop mentioning the label route. The service checks the variable again every 5 minutes, so turning polling back on takes effect within that time. |
| a duration from `1m` to `24h` | A cycle that long after the previous one ended. |
| anything else (not a duration, negative, below `1m`, above `24h`) | Falls back to 5 minutes, with one `WARN` in the log per distinct bad value. It never turns polling off. |

Other timing is fixed:

- A workspace's first cycle starts 0 to 30 seconds after the service starts (randomly, to spread the
  load), and each later cycle starts one interval after the previous one ended, so cycles of one
  workspace never overlap.
- At most four cycles run at once across all workspaces. A cycle that runs for 5 minutes is stopped;
  what it did stands.
- A newly subscribed workspace is first polled within about 90 seconds; an unsubscribed one stops
  being polled within a minute.

**Rate limits.** Each enabled cycle makes four reads on hub (the variables, the workspace record and one
list per request label, each up to 100 issues) plus one label removal per label it takes. The lists and
removals reach the forge and count against the workspace's forge rate limit; raise `AF_POLL_INTERVAL`
to spend less. A disabled workspace costs one read every 5 minutes.

**Cancelled jobs.** When a job made from a label is cancelled, its request label is put back on the
issue. The poll leaves that label alone until a person removes it and adds it back, no sooner than
about 5 minutes after the cancel finished; that is a new request. See
[api.md](api.md#request-labels) and [architecture.md](architecture.md#the-label-poll).

## Environment variable index

| Variable | Read by | Where it is described |
|---|---|---|
| `GITHUB_TOKEN`, `GH_TOKEN`, `GITLAB_TOKEN` | `af` | [Forge access](#forge-access) |
| `GITHUB_API_URL`, `GITLAB_API_URL` | `af` (and the service, to recognise a forge host) | [Forge access](#forge-access) |
| `AF_MODEL` | `af`; hub workspace variable for the service | [Model and credentials](#model-and-credentials), [Workspace variables](#workspace-variables-on-hub) |
| `ANTHROPIC_API_KEY` | `af` (the `agentkit` brain) | [Model and credentials](#model-and-credentials) |
| `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_OAUTH_TOKEN`, `ANTHROPIC_BASE_URL`, `CLAUDE_CODE_USE_VERTEX`, `CLAUDE_CODE_USE_BEDROCK` | `af` (brain on the host only) | [Model and credentials](#model-and-credentials) |
| `AF_SANDBOX`, `AF_SANDBOX_IMAGE` | `af` | [Sandbox](#sandbox) |
| `AF_URL` | `af` service client | [Service client](#service-client) |
| `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY` | `af` service client, `agent-fox` | [Service client](#service-client), [The agent-fox service](#the-agent-fox-service) |
| `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, `HOME` | `agent-fox` | [Where the file is](#where-the-file-is), [Keys](#keys) |
| `${NAME}` in `config.toml` | `agent-fox` | [Paths and variables in the file](#paths-and-variables-in-the-file) |
| `AF_MAX_BUDGET_USD`, `AF_MAX_TURNS`, `AF_TIMEOUT`, `AF_MAX_ATTEMPTS`, `AF_SANDBOX_PROFILE`, `AF_FIX_LAND` | hub workspace variables | [Workspace variables on hub](#workspace-variables-on-hub) |
| `AF_POLL_INTERVAL` | hub workspace variable | [The label poll](#the-label-poll) |
| `AF_TEST_PODMAN` | the test suite | [development.md](development.md#opt-in-tests) |
