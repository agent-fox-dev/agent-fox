# Configuration

`af` needs no configuration, and `agent-fox`, the service, is configured by one file
([below](#the-agent-fox-service)). What `af` reads from the environment, it reads
through `issuex`, the forge client of
[aflib](https://github.com/agent-fox-dev/aflib); the checks of the gate can be
pinned in [`.agent-fox/gate.json`](#gatejson); everything else is a flag (see
[cli.md](cli.md)).

## Environment variables

| Variable | Used for |
|---|---|
| `GITHUB_TOKEN` | The token for GitHub. |
| `GH_TOKEN` | The same token under the name the `gh` CLI uses; read when `GITHUB_TOKEN` is not set. |
| `GITLAB_TOKEN` | The token for GitLab. |
| `GITHUB_API_URL` | The API URL of a GitHub Enterprise Server, such as `https://ghe.example.com/api/v3`. Default `https://api.github.com`. |
| `GITLAB_API_URL` | The API URL of a self-hosted GitLab, such as `https://gitlab.example.com/api/v4`. Default `https://gitlab.com/api/v4`; `/api/v4` is added if it is missing. |
| `AF_URL` | The URL of the agent-fox service that the [service commands](#the-client-of-the-agent-fox-service) talk to: `af workspace …` and `af jobs …`. `--server` wins over it, and an empty value is unset. |

Without a token, `af` reads public issues and a 404 means the issue may be
private (`failed`/`auth` with the token to set in `fix_hint`). Writing needs a
token, and so does `af fix --land pr`: opening a pull request needs `GITHUB_TOKEN`
or `GH_TOKEN` for a GitHub repository and `GITLAB_TOKEN` for a GitLab one. Without
it the run is `failed`/`auth` before it spends anything, with the variable in
`fix_hint.env`; under `--dry-run` it is the warning `no_forge_token`, and
`--land branch` and `--land none` need none.

**Which forge.** The forge of a command is the one the issue URL names, else
the one `--repo` and the environment say, else the one the `origin` remote of
`--dir` names. If only the GitHub variables are set, it is GitHub; if only
the GitLab ones, GitLab; if both or neither, the origin remote decides. A
host that names neither forge (`git.example.com`) needs its API URL in
`GITHUB_API_URL` or `GITLAB_API_URL`; an issue URL on such a host is a `usage`
error until it has one, and a command that was given no issue URL runs with a
client that does nothing.

**Where a token goes.** The token of the environment is sent only to the host
the environment points it at: the host of `GITHUB_API_URL` or
`GITLAB_API_URL` when that names the forge's host, or the public forge when no
API URL is set. A request to any other host carries no token.

### The brain

| Variable | Used for |
|---|---|
| `AF_MODEL` | The model the `agentkit` brain runs, as the catalog of the hands names it. Empty is the catalog's default for the vendor, `claude-sonnet-5-5`. A model the catalog does not list has no price: the run reports no cost (`cost_source` `none`, with the warning `model_unpriced`), and with `--budget` it does not start (`failed`/`usage`, with `fix_hint.env` `AF_MODEL`). The commands that run a brain read the variable and give it to the brain as `brain.Options.Model`; `af triage`, `af triage run`, `af fix` and `af fix run` do. The `agentkit` driver that runs in a box has it on its command line as `AF_MODEL`. |
| `ANTHROPIC_API_KEY` | The credential of the `agentkit` brain's provider, when no provider is injected. The provider resolves its credentials as the hands' does: `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN` (and `ANTHROPIC_OAUTH_TOKEN`), optionally `ANTHROPIC_BASE_URL`, and Vertex AI or Bedrock when `CLAUDE_CODE_USE_VERTEX` or `CLAUDE_CODE_USE_BEDROCK` is `1`. Without any, the brain is not made: `failed`/`auth` with `fix_hint.env` `ANTHROPIC_API_KEY`, before a box is opened. The driver that runs in a box needs `ANTHROPIC_API_KEY` itself: it has no other way to authenticate there (a subscription token, Bedrock, Vertex and `ANTHROPIC_BASE_URL` are not supported there). |

### The box

| Variable | Used for |
|---|---|
| `AF_SANDBOX` | The box provider, when `--sandbox` is not given: `podman` or `local`. An empty value is unset, and an unknown one is a `usage` failure that names the variable. Without it the provider is `podman`, on every operating system. When that provider cannot open, `af` fails with the reason and never falls back to `local`. |
| `AF_SANDBOX_IMAGE` | The image of a `podman` box. Without it, `quay.io/agentfox/tools:latest`: the sandbox image with `af` in it, built from `containers/tools/Containerfile` by `make build-tools-container`. There is no flag for it: the image is configuration of the machine. |

**Which credential enters which box.** Nothing of the host's environment reaches a
box but what the brain asks for, and that is decided by where the brain runs (see
[the placements](cli.md#where-the-brain-runs)):

| Where the brain runs | The box holds | Network |
|---|---|---|
| on the host (`scripted`; `agentkit` under `local`) | no credential at all | `none` when the brief has no tool that runs a command (`af triage`), `open` when it has (`af fix`); under `local` nothing is enforced and the network is `host` |
| in the box (`agentkit` under `podman`) | `ANTHROPIC_API_KEY`, if the host has it | `open` |

No forge token (`GITHUB_TOKEN`, `GH_TOKEN`, `GITLAB_TOKEN`) is in any box. A process
brain is started under `env -i` with only `PATH`, `HOME`, the locale and time zone,
the proxy and certificate settings that are set, and the key (see [the process
brains](cli.md#brains-that-run-as-a-process)); a podman box has the whole network or
none, so a brain in one on a developer's machine has all of it. The credentials in
the checkout's `.git/config` are taken out of the box's copy. Under `podman` the key of
the vendor is on the command line of the engine on the host while the box lives, as
`--env`, which another user of the machine can read. The `scripted` brain reads no
variable. The `local` provider has no process or network isolation, and a box opened
with it says so with the warning `box_unconfined`.

## The `.agent-fox` directory

`af` keeps its local state under the checkout it works in (`--dir`), in
`.agent-fox/`. It is never part of the file view that detection, the tree hash
and the map read, and its `.gitignore` keeps it out of the repository.

| Path | What it is |
|---|---|
| `.gitignore` | Ignores the whole directory, without touching the repository's own `.gitignore`. |
| `gate.json` | The table of checks that the gate runs; see [gate.json](#gatejson). |
| `guard.json` | The policy of the tool gate of a brain; see [guard.json](#guardjson). |
| `gate/<id>/` | The logs and the stored result of a gate run (see [cli.md](cli.md#gate-run)). Safe to delete: the next run makes them again. |
| `revert/<id>/` | The backup that [`revert-check`](cli.md#revert-check) keeps while it works and removes when the tree is back. One that is left means a check was killed, or the tree could not be put back, or something changed a path while the checks ran (what it held is in `changed/`): look at it before checking again. |
| `run/<id>/` | One directory per invocation of a command that has one: the envelope, the event stream, the ledger, and a report or a panic's stack, and for a command that runs a brain its briefs, hook, accepted submission, transcripts and the stack of a brain's panic, and what a driver that runs as a process in a box printed (`agentkit.<attempt>.json`, `agentkit.<attempt>.stderr`). Delete one when it is no use; nothing refers to one except a later run of the same input with `--context`, which reads the question it asked. The layout is in [cli.md](cli.md#the-run-directory). |

### The files of a triage run

`af triage` makes a run directory. Besides the files every run directory has, a
triage run keeps in `.agent-fox/run/<id>/`:

| File | What it is |
|---|---|
| `brief.md` | The brief that was rendered, which the brain read: the mandate, the `af-triage` skill, the repository map, the report, and the task block. |
| `original.md` | The original report, exactly as `af` took it. The body of the issue keeps these bytes. |
| `triage.json` | The facts of the run: `format_version` (1), the `input` (`kind`, `origin`, `fingerprint`), the `target` repository and its host, the `issue` or null, `original_report_sha256`, and the `prefix` (`bytes`, `sha256`) of the brief. |
| `submission.json` | The diagnosis the brain handed over and `af` accepted, in its normalized form. |
| `rejections.jsonl` | One line for each refused call to submit: `{ts, problems, unresolved_paths}`. |
| `diagnosis.md` | The issue body, rendered from the submission; written before anything is sent to the forge, and the only output of `--land none`. |

The triage commands read **no new environment variable**. The forge token and
API URL are those above, and the model and its credential are `AF_MODEL` and
`ANTHROPIC_API_KEY` of [the brain](#the-brain). The skill `af-triage` that the
brief embeds is compiled into the binary, so no file is read for it. Everything
else is a flag: [cli.md](cli.md#triage).

### The files of a fix run

`af fix` makes a run directory. Besides the files every run directory has, a fix
run keeps in `.agent-fox/run/<id>/`:

| File | What it is |
|---|---|
| `brief.md` | The brief of the first round, which the brain read: the mandate, the `af-fix` skill, the report in a fence, the gate and its baseline, the repository map, the schema of the report, and the task block. |
| `original.md` | The original report, exactly as `af` took it from the issue. |
| `run.json` | The attempt and the nonce that the submission protocol reads, as the run made them; the box has a copy that is placed again before each round. |
| `fix.json` | The state of the run, written last and atomically: the `input` (`kind`, `origin`, `key`), the `issue` or null, the `target` repository and its host, the `base` branch and the full hash `base_head` of its commit, the pinned gate `table`, the baseline (`baseline_gate_id`, `baseline_passed`, `baseline_ms`), `no_verify`, the `prefix_sha256` and `prefix_bytes` of the brief, the `suite` and `programs` the gate detected, the `context` answers, the number of `rounds` and the `brain`, `cost_usd` and `cost_source` they used. |
| `submission.json` | The report the brain handed over and `af` accepted, in its normalised form. |
| `report.md` | The body of the pull request of a change that landed, rendered from what `af` measured and what the report says; written before the commit. A change that was parked has none. |

A round of a brain runs in a box, and `af` places two files in it before each
round: `.agent-fox/gate.json`, the table of checks the run was pinned to, so that
the brain's own `af gate run` measures what `af` will, and `.agent-fox/guard.json`,
the policy of the tool gate (the `suite` of the gate, the default protected paths
and, as `allow_programs`, the programs of the gate together with `af`, `git` and
a few utilities that only read or make a directory). They are the host's table and rules, and not what the
box would detect; the files of the checkout itself are not changed, and the
`.agent-fox/` directory of the box never comes back out (see
[gate.json](#gatejson) and [guard.json](#guardjson)).

The fix commands read **no new environment variable**. The forge token and API
URL are those above (`--land pr` needs the token), and the model and its credential
are `AF_MODEL` and `ANTHROPIC_API_KEY` of [the brain](#the-brain). The skill
`af-fix` that the brief embeds is compiled into the binary, so no file is read for
it. Everything else is a flag: [cli.md](cli.md#fix).

### gate.json

`.agent-fox/gate.json` pins the table of checks that the gate runs, so that
the table is the one that was measured and not whatever the files say today.
`af lang detect --write` writes it from the repository's own files; a person
or a loop may write it by hand.

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
| `checks[].argv` | The command, with no shell. Required, and its first word is the program. |
| `checks[].name` | What the check is called; unique in the file. Default: `argv` joined by spaces. |
| `checks[].kind` | `lint`, `test` or `check` (both). Default `check`. |
| `checks[].source` | Where the check came from. Default `gate.json`. |

`available` is accepted in a check and ignored: whether a program can be run
is a fact about the machine, worked out on every read and never stored. Any
other field is an error, and so is more than the one document in the file.

**Precedence.** The table is `gate.json` when that file exists. Otherwise it
is detected from the files at the root: a Makefile with a `check` target, else
a Makefile with a `test` target (and `lint` if it has one), else the language
manifests (see [cli.md](cli.md#lang-detect)). A `gate.json` that does not
parse, names another version or holds a check that cannot run is
`failed`/`config`, naming the file; detection does not fall back to the
Makefile.

### guard.json

`.agent-fox/guard.json` is the policy of the tool gate of a brain (see
[the tool policy](cli.md#the-tool-policy)): what a brain may not do. A loop writes it once for a run; a person may write it
by hand.

```json
{
  "version": 1,
  "suite": [["make", "check"], ["go", "test", "./..."]],
  "protected_paths": [".git/", ".agent-fox/", ".specs/", ".github/workflows/", ".gitlab-ci.yml"],
  "allow_programs": []
}
```

| Key | Meaning | Default when the key is missing |
|---|---|---|
| `version` | Must be `1`. | (required) |
| `suite` | The command forms that count as the full test suite, each a non-empty list of words. A shell command equal to one of them, ignoring the words that start with `-`, the values of the options that take one (`-timeout 5m`, `-j 4`, `-f Makefile`) and the `NAME=value` words, is refused with a pointer to `af gate run`. | The `suite` that [lang detect](cli.md#lang-detect) reports for `--dir`: the gate checks, `make check` and `make test` where the Makefile has them, and the forms of each language. |
| `protected_paths` | Paths relative to the checkout that only af writes. One that ends in `/` is a directory. A path may not be absolute or above the checkout; it is cleaned before it is matched (`./docs/`, `docs//` and `a/../docs` all protect `docs`), compared without regard to case, and a link that leads into it is it. | `.git/`, `.agent-fox/`, `.specs/`, `.github/workflows/` and `.gitlab-ci.yml`. |
| `allow_programs` | When it is not empty, the only programs a shell command may run, as the command spells them (`/usr/bin/ls` is not `ls`), besides `af` and `git` (by name or absolute path) and the builtins that start no program (`cd`, `echo`, `test`, `for`, `eval`, `trap`, …). | Empty: no allowlist. |

A key that is there is taken as it is, an empty list included: `"protected_paths": []` protects nothing. Any other key is an error.

**Precedence.** The policy is the first that exists of: the file named by
`--policy`, which must be there; `<dir>/.agent-fox/guard.json`; the defaults,
all of them. A key a file does not have takes its default, and a file that is
used replaces the files after it entirely (`guard.json` is not read when
`--policy` is given). A file that does not parse, names another version, has an
unknown key or an entry that cannot be used is `failed`/`config`, naming the
file; no decision is made with a policy that was half read.

## The client of the agent-fox service

The commands of `af` that talk to the agent-fox service take the service from `--server <url>` or, without the
flag, from `AF_URL`. The URL is an absolute `http` or `https` URL with a host and an optional path prefix, with no
query, no fragment and no user info; one trailing `/` is dropped. No server is stored anywhere: `AF_URL` in a shell
profile does the job. A plain `http` URL whose host is not loopback raises the warning `server_insecure`.

**The credential** is the hub credential (an API key or a personal access token) that every request sends as
`Authorization: Bearer`. Its one source is the file `~/.af/config.toml`, the file that `afc login` writes, and in it the
string key `token` at the top level; every other key is ignored. There is no environment variable for it, and `AF_URL`
is not a place for it either (a URL with user info is refused). `~` is the home directory that the operating system
reports (`HOME` on Unix).

```toml
# ~/.af/config.toml
token = "the credential"
```

`af` reads the file and never writes it: `afc login` writes it, or a person does, with mode 0600. On Unix a file with a group or other permission bit set raises the
warning `credential_file_permissions`; the command goes on. The credential is never in an envelope, an event or a message,
nor is the token of a workspace that `af workspace subscribe` hands over, as long as it is 8 bytes or more (a shorter value
would be in ordinary words and is not taken out). The standard proxy variables (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`) are
honoured by the requests of the client, as they are by the calls of the service to hub: behind a proxy, a plain `http` server's
requests are seen by the proxy. The commands are in [cli.md](cli.md#the-client-of-the-agent-fox-service).

## The agent-fox service

`agent-fox`, the long-running service (see [cli.md](cli.md#the-agent-fox-command-line)), reads **one
file**, `config.toml`, and nothing else from the environment but the variables that the file itself
names with `${NAME}`, `XDG_CONFIG_HOME`, `XDG_STATE_HOME` and `HOME` (which say where files are), and the
standard proxy variables `HTTP_PROXY`, `HTTPS_PROXY` and `NO_PROXY`, which Go's HTTP client reads for the
calls to hub: with one set, those calls, and the tokens in them, go through that proxy. `af` does not read
the file. The service holds no vendor key and no forge token: the only credentials it holds
are the tokens that callers send it, which it does not keep, and the workspace tokens that callers hand
over to subscribe a workspace, which it keeps sealed (see [the key file](#the-key-file)).

A minimal file sets two keys:

```toml
[hub]
url = "https://hub.example.com"

[secrets]
key_file = "key"
```

### Where the file is

The file is `--config` when it is given. Otherwise it is the first that exists of `./config.toml` and
`$XDG_CONFIG_HOME/agent-fox/config.toml` (`$HOME/.config/agent-fox/config.toml` when `XDG_CONFIG_HOME` is
unset, empty or not an absolute path). If none exists, the service does not start, and says which paths it tried.

### The keys

| Key | Type | Default | Rule |
|---|---|---|---|
| `server.listen` | string | `127.0.0.1:8090` | A `host:port` that Go's `net.SplitHostPort` accepts. Port `0` is allowed: the system picks one, and the `listening` record says which. |
| `hub.url` | string | **required** | An absolute `http` or `https` URL, with an optional path prefix and no query or fragment. A trailing `/` is dropped. Hub's API is `<hub.url>/api/v1/…`. |
| `hub.auth_cache_ttl` | duration (Go syntax, as `60s`) | `60s` | From `1s` to `15m` inclusive. A value outside is refused, not clamped. How long hub's answers about a caller's token and about the workspaces the caller sees are kept. |
| `database.path` | string | `$XDG_STATE_HOME/agent-fox/agent-fox.db` (`$HOME/.local/state/agent-fox/agent-fox.db` when `XDG_STATE_HOME` is unset, empty or not an absolute path) | The SQLite file. The service makes it, with its directory, when it is missing. |
| `database.retain` | duration (Go syntax, as `720h`) | `720h` | From `1h` to `8760h` inclusive. A value outside is refused, not clamped. How long a finished job (done, failed or cancelled) is kept before the purge removes it. |
| `workers.count` | integer | `2` | From `1` to `64` inclusive. A value outside, or one that is not an integer, is refused. The number of attempts that run at once across all workspaces. |
| `secrets.key_file` | string | **required** | The file that holds the key (see [the key file](#the-key-file)). |

`workers.count` is the key `count` of the `[workers]` table, and `database.retain` the key `retain` of
`[database]`.

- `[workers].count` bounds the attempts that are in a runner at once, across all workspaces. The attempts of one
  workspace run one at a time in the order they were made whatever the count is, so a count of 4 lets four
  workspaces work together. An attempt that waits out a backoff after a transient failure gives its slot back.
- `[database].retain` is how long a job that is `done`, `failed` or `cancelled` is kept, counted from the time it was
  finished (`finished_at`), and not from when it was asked or made. A purge runs when the service starts and then
  every hour, and removes the job's `attempts/<job id>/` directory (beside the database file) and its rows. A
  `blocked` job is never removed, and neither is a job with a runner that has not returned. See
  [api.md](api.md#how-long-a-job-is-kept).

**No other key is defined.** A key that is not in the table, in any table, is an error that names it, so
that a misspelt key (`auth_cach_ttl`) is never silently replaced by the default. That includes
`server.external_url`, which is not accepted in this release: it arrives with the spec that uses it, and
until then a file that sets it is refused. A
value of the wrong type is an error that names the key as well, and so is an empty value for a key that
is required or that has a default.

**Relative paths.** A `database.path` or `secrets.key_file` that is relative is resolved against the
directory of the configuration file, not the working directory, so that the file means the same thing
wherever the service is started from.

**Variables.** `${NAME}` (`NAME` matches `[A-Za-z_][A-Za-z0-9_]*`) in a string value is replaced by the
environment variable of that name, after the file has been read, so that the quoting of the file cannot
be broken by a value. A variable that is set to the empty string expands to the empty string, and a
required key left empty then fails as missing. A variable that is **not set** is an error that names the
variable and the key. A `$` that is not the start of `${NAME}` is left alone.

**Plain http to hub.** A `hub.url` that is `http` to a host that is not a loopback address is accepted,
because compose-style networks are common, and the service logs a warning when it starts: caller tokens
and workspace tokens cross the link in clear.

### The hub variables of an attempt

An attempt takes its settings from the **resolved variables of its hub workspace**, read from hub at the start of
every attempt (`GET /api/v1/workspaces/{slug}/vars/resolved`), and not from the environment of the service. A variable
that is unset or empty takes its default; a variable it does not know is ignored; one outside its range fails the
attempt before a session is opened, as a `failed` envelope of category `config` that is not retryable, whose
`fix_hint.var` names the variable and whose message names it and its value. The brain is always `agentkit`, and no
variable names it: an `AF_BRAIN`, `AF_BRAIN_TRIAGE` or `AF_BRAIN_FIX` that a workspace still holds is ignored.

| Variable | Triage | Fix | Range |
|---|---|---|---|
| `AF_MODEL` | the catalog's default Anthropic model | the same | any non-empty string; the session, the brain and the usage record carry it. |
| `AF_MAX_BUDGET_USD` | `2` | `10` | finite and above 0. |
| `AF_MAX_TURNS` | `100` | `150` | a whole number from 1. |
| `AF_TIMEOUT` | `15m` | `45m` | a Go duration from `1m` to `24h`. It is cut so that the loop ends two minutes before the sandbox lease expires, with the warning `lease_shortened_timeout`. |
| `AF_MAX_ATTEMPTS` | `2` | `2` | a whole number from 1 to 10: the most attempts a round may make. The attempts are the jobs layer's: a fix loop has none of its own. |
| `AF_SANDBOX_PROFILE` | none | none | non-empty: the profile that hub leases the sandbox from; unset is hub's default. Its image must provide `af`. |
| `AF_FIX_LAND` | | `pr` | `pr`, `branch` or `none`. `patch` is refused: a fix on a `carry_patch` workspace, or landing as a patch, fails as `unsupported`, because carry-patch landing comes in a later release. A triage on a `carry_patch` workspace runs as on any other, and does not read it. |
| `AF_POLL_INTERVAL` | | | read by the worker as the label poll reads it, and never a failure: a zero duration turns the label triggers off, which drops the forge path from the question, failure and parked-fix comments; anything else, an invalid value included, leaves them on. See [the label poll](#the-label-poll). |

### The label poll

The service makes a job of each new request label on a workspace's issues (see
[api.md](api.md#jobs-that-a-request-label-makes)) by polling: it asks hub every interval for the issues that carry the
request labels. The poll has no key in `config.toml`: its one setting is a hub variable of the workspace, read at the
start of every cycle from the resolved variables, and each cycle uses the value it reads then.

| `AF_POLL_INTERVAL` | The poll does |
|---|---|
| unset or empty | A cycle every `5m`. |
| a zero duration (`0`, `0s`, `0m`) | The poll is off for the workspace: no job is made from a label, and no job is answered or retried from the forge. The worker's comments then leave out the forge path. The cycle ends right after the variables are read: it reads no workspace record, lists nothing and writes no label. It reads the variable again 5 minutes later, so that a workspace that turns polling on is picked up within the default interval. |
| a Go duration from `1m` to `24h` | A cycle that long after the previous one ended. |
| anything else: not a duration, negative, below `1m`, above `24h` | It falls back to 5 minutes and does not turn the poll off, and the service logs a `WARN` that names the workspace, the variable and the value once for each distinct value. |

The request labels are `af:triage` and `af:fix`; the prefix `af:` is not configurable.

A cancel puts the request label back: when a job the poll made is cancelled, by a person or by an unsubscribe, the worker
(for a runner that opened its hub session) or the service's cancel cleanup (for every other cancel) puts the request label
back on the issue and takes off the status labels the job set (see [Cancel](api.md#cancel)). The poll holds such a label
rather than take it for a new request, until a complete listing without the issue releases it, requested at least 5 minutes
after the poll first saw that no runner of the job was in progress and its cleanup had finished: removing the label and
adding it again is then a new request.

Nothing else about the timing is a setting. A loop starts its first cycle after a random delay of 0 to 30 seconds, so that
the workspaces do not all ask hub at once when the service starts, and each later cycle one interval after the previous
one ended, so that the cycles of one workspace never overlap. At most four cycles run at once across all workspaces. A
cycle that has run for 5 minutes is cancelled, and what it has done stands. The service looks for new and gone
subscriptions every 60 seconds: a workspace that is subscribed is first polled within 90 seconds, and one that is
unsubscribed loses its loop and the cycle it has in flight. The poll spends the workspace's forge rate limit, and the
interval is how to control it: see [the cost of a cycle](architecture.md#the-label-poll-as-built).

The worker holds no vendor key and no forge token. Its credentials are the workspace token of the subscription, which
it sends to hub and to hub's git server, and the token of the lease, which it sends to the gateway of the box; the
brain's credentials come from the profile of the box (see [model-usage.md](model-usage.md#the-agent-fox-service)).

### The key file

`secrets.key_file` holds the key that workspace tokens are sealed with at rest: the standard base64
encoding of **exactly 32 random bytes**; whitespace around it is ignored. Make one with

```
(umask 077; openssl rand -base64 32 > key)
```

The `umask` matters. A bare `openssl rand -base64 32 > key` under the usual umask makes a file with the
mode `0644`, and the service refuses to start on a key file that any group or other permission bit is set
on (the mode `0600`, `0400` or `0700` is fine; the owner's bits are not checked), as it would for an ssh key.
It also refuses a file that is missing, is not base64, or does not decode to 32 bytes. The error names the
file and the reason and never holds what the file holds.

A token is sealed with AES-256-GCM under a fresh nonce, with the additional data
`agent-fox/subscription/<slug>`, which ties the ciphertext to its row. It is stored as one version byte
(`0x01`), the nonce, and the ciphertext with its tag. Neither the key nor a token is ever logged or put in
an error message, and the plaintext of a token is in no file.

If the key file is replaced by another, the service starts all the same: the subscriptions made under the
old key read `token_state: unreadable` until someone subscribes the workspace again with `PUT` (or removes
it with `DELETE`). Keeping the old key is the way to keep them. There is no key rotation in this release.

### The state file

All of the service's state is one SQLite file, opened in WAL mode (so there are `-wal` and `-shm` files
beside it while it runs), with a busy timeout of 5 seconds and foreign keys on. The directory is made with
the mode `0700` and the file with `0600` when they are missing. Every table the service owns is named with
the prefix `af_`, its migrations are recorded in `af_schema_migrations` (not in `PRAGMA user_version`), and
it never reads or writes a table it does not own, so that the file can hold the tables of other modules.
The service also makes the directory `attempts/` beside the file, with the mode `0700`, when it is
missing: the files of each attempt of a job go in it. Migrations run when the service starts; a file that records a migration newer than the binary knows
makes the start fail, naming both versions, and is left as it was. So does a file made by an unreleased build from
before the migrations were squashed (October 2026): remove it, and the service makes a new one. Two processes must not share one file.

