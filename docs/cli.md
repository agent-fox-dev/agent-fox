# Command-line reference

agent-fox ships two binaries. `af` is the command-line tool: it triages and fixes
issues on your machine, and it is the client of the agent-fox service. `agent-fox`
is the long-running service itself.

## Contents

- [Overview](#overview)
- [Conventions](#conventions)
  - [Invoking a command](#invoking-a-command)
  - [Common flags](#common-flags)
  - [The input argument](#the-input-argument)
  - [The envelope](#the-envelope)
  - [Statuses and exit codes](#statuses-and-exit-codes)
  - [Questions and --context](#questions-and---context)
  - [Dry runs](#dry-runs)
  - [Timeouts and signals](#timeouts-and-signals)
  - [Help and version](#help-and-version)
- [Local commands](#local-commands)
  - [af triage](#af-triage)
  - [af fix](#af-fix)
  - [af lang detect](#af-lang-detect)
  - [af gate run](#af-gate-run)
  - [af revert-check](#af-revert-check)
- [Sandboxes and brains](#sandboxes-and-brains)
- [Service client commands](#service-client-commands)
  - [Connecting to the service](#connecting-to-the-service)
  - [af workspace](#af-workspace)
  - [af jobs submit](#af-jobs-submit)
  - [af jobs list, get, watch, cancel, answer and retry](#af-jobs-list-get-watch-cancel-answer-and-retry)
  - [Following a job](#following-a-job)
  - [How service answers map to statuses](#how-service-answers-map-to-statuses)
- [Internal commands](#internal-commands)
- [The agent-fox command](#the-agent-fox-command)
- [Reference](#reference)
  - [Error categories](#error-categories)
  - [Warning codes](#warning-codes)
  - [The run directory](#the-run-directory)
  - [Envelope versioning](#envelope-versioning)

## Overview

`make build` writes both binaries to `bin/`. `af` has eighteen commands:

| Command | What it does |
|---|---|
| [`af triage`](#af-triage) | Diagnose a report with a model and write the diagnosis to the issue. |
| [`af fix`](#af-fix) | Fix an issue: a model edits the code, `af` verifies the change and opens a pull request. |
| [`af lang detect`](#af-lang-detect) | Report the languages of a repository and the checks that verify it. |
| [`af gate run`](#af-gate-run) | Run those checks and report the result, cached by the content of the tree. |
| [`af revert-check`](#af-revert-check) | Check that a change's tests fail without the rest of the change. |
| [`af workspace subscribe`, `list`, `get`, `unsubscribe`](#af-workspace) | Manage the hub workspaces the service works on. |
| [`af jobs submit`](#af-jobs-submit) | Queue a triage or fix job on the service. |
| [`af jobs list`, `get`, `watch`, `cancel`, `answer`, `retry`](#af-jobs-list-get-watch-cancel-answer-and-retry) | Read, follow and steer jobs on the service. |
| [`af triage run`, `af fix run`](#internal-commands) | Run inside a sandbox on behalf of `af triage` and `af fix`. Not for people. |

`af triage` and `af fix` always run on this machine. `af jobs submit triage|fix`
queues the same work on the agent-fox service instead, which runs it in a sandbox
that hub leases.

## Conventions

Every `af` command follows the same contract: it prints exactly one JSON
[envelope](#the-envelope) on stdout, exits with a code that matches its
[status](#statuses-and-exit-codes), and stops with a question rather than guessing.

### Invoking a command

```
af <command> [flags] [argument]
af help [command]
af --version
```

- A command is one or more words (`triage`, `gate run`). `af` picks the longest
  command that matches the words before the first flag. The command is the
  envelope's `tool`.
- Flags may come before or after the argument, as `--flag value` or `--flag=value`.
  `--` ends the flags. A flag that takes a value takes the next word, whatever it
  looks like.
- A command takes at most one positional argument unless it says otherwise. An
  extra argument or an unknown flag is a `usage` error.
- `af` with no arguments, or with an unknown command, prints a `usage` envelope and
  exits 2.

### Common flags

Each command accepts only the flags that apply to it. These recur:

| Flag | Meaning |
|---|---|
| `--dir <path>` | The checkout to work in. Default `.`. |
| `--context <text>` | An answer to a question an earlier run asked. Repeatable. See [Questions and --context](#questions-and---context). |
| `--dry-run` | Send no write to the forge. See [Dry runs](#dry-runs). |
| `--repo <owner/name>` | The repository to work on, when the input does not name one. |
| `--timeout <duration>` | Stop after this long, such as `90s` or `15m`. `0` means no limit. Each command has its own default. |

### The input argument

`af triage` and `af jobs submit` take an input written `<issue|file|text|->`. `af fix`
takes an issue. The first rule that matches decides what the argument is:

1. `-` reads stdin until end of file. Kind `stdin`.
2. A GitHub or GitLab issue, pull request or merge request URL. Kind `issue`. `af`
   reads the thread (title, author, labels, body, comments) through the forge.
3. Any other `http://` or `https://` URL is a `usage` error. For a self-hosted
   forge, set `GITHUB_API_URL` or `GITLAB_API_URL` (see
   [configuration](configuration.md#forge-access)).
4. The name of an existing regular file. Kind `file`. A directory, or a file with a
   NUL byte in its first 8 KiB, is a `usage` error; an unreadable file is
   `failed`/`io`.
5. Anything else is kind `text`.

The text is trimmed; an empty input is a `usage` error. Input over 131072 bytes is
cut at a UTF-8 boundary, ends with `[... input truncated at 131072 bytes ...]`, sets
`input.truncated` and raises the warning `input_truncated`.

Reading an issue can fail. A 404 without a token is `failed`/`auth`, since the issue
may be private. A 404 with a token is `failed`/`forge`, not retryable. A rate limit,
a server error or a lost connection is `failed`/`forge`, retryable.

### The envelope

Every invocation except `--version` and help writes one JSON object to stdout,
indented by two spaces, and nothing else. Every field is always present, in this
order. Arrays are `[]`, never null; an object that does not apply is `null`.

```json
{
  "schema_version": "5.0.0",
  "tool": "triage",
  "run_id": "20261020_072649_a31f9c",
  "status": "done",
  "exit_code": 0,
  "summary": "diagnosed; 2 writes planned",
  "input": {"kind": "issue", "origin": "https://github.com/acme/shop/issues/12", "truncated": false},
  "result": { },
  "needs_human": null,
  "error": null,
  "warnings": [],
  "artifacts": [
    {"kind": "run_dir", "path": "/work/repo/.agent-fox/run/20261020_072649_a31f9c"}
  ],
  "side_effects": [
    {"kind": "update_issue", "target": "acme/shop#12", "state": "planned"},
    {"kind": "add_labels", "target": "acme/shop#12", "state": "planned"}
  ],
  "next": [],
  "usage": {
    "brain": "agentkit",
    "cost_usd": 0.41,
    "cost_source": "measured",
    "input_tokens": 52310,
    "output_tokens": 2104,
    "turns": 14
  },
  "started_at": "2026-10-20T07:26:48.120Z",
  "finished_at": "2026-10-20T07:26:55.440Z",
  "duration_ms": 7320
}
```

| Field | Type | Meaning |
|---|---|---|
| `schema_version` | string | `"5.0.0"`. See [Envelope versioning](#envelope-versioning). |
| `tool` | string | The command, or `"af"` when no command was found. |
| `run_id` | string or null | The id of the [run directory](#the-run-directory); null for a command without one. |
| `status` | string | `done`, `failed`, `usage`, `needs_human` or `unverified`. |
| `exit_code` | integer | The code the process exits with. |
| `summary` | string | One line saying what happened. |
| `input` | object | `{kind, origin, truncated}`. `kind` is `issue`, `file`, `text`, `stdin` or `none`. `origin` is the issue URL or the file's absolute path, else `""`. |
| `result` | any or null | The command's own result, described with each command. Null on failure. |
| `needs_human` | object or null | The question the command stopped on. Set exactly when `status` is `needs_human`. |
| `error` | object or null | `{category, message, retryable, fix_hint}`. Set exactly when `status` is `failed` or `usage`. `fix_hint` is null or holds any of `flag`, `env`, `var` and `suggest`. See [Error categories](#error-categories). |
| `warnings` | array | `[{code, message}]` in the order raised. See [Warning codes](#warning-codes). |
| `artifacts` | array | `[{kind, path?, url?, name?, number?}]`: files, branches, issues, pull requests and jobs the command made. Paths are absolute. |
| `side_effects` | array | `[{kind, target, state, detail?}]`: writes to the forge or git. `state` is `done`, `planned` or `failed`. |
| `next` | array | `[{argv, reason}]`: suggested follow-up commands. |
| `usage` | object | `{brain, cost_usd, cost_source, input_tokens, output_tokens, turns}`. For a command that runs no model, `brain` is `""`, the numbers are 0 and `cost_source` is `none`. Otherwise `cost_source` is `measured` or `reported`, or `none` when the model has no known price. See [model-usage.md](model-usage.md#how-cost-is-reported). |
| `started_at`, `finished_at` | string | RFC 3339, UTC, with milliseconds. |
| `duration_ms` | integer | Wall time of the invocation. |

When the command has a run directory, the same bytes are also written to
`envelope.json` there. If that copy fails, the envelope carries the warning
`envelope_unwritable` and its status does not change. `af` writes nothing to stderr
except the stack trace of a crash; the client's `af jobs watch` also writes job
events there.

### Statuses and exit codes

| `status` | Exit code | Meaning |
|---|---|---|
| `done` | 0 | The command did what it was asked. |
| `failed` | 1 | It could not; `error` says why. |
| `usage` | 2 | The invocation was wrong: flags, arguments or the form of the input. |
| `needs_human` | 3 | It stopped on a question. |
| `unverified` | 4 | Work exists but could not be verified. A warning says what was not verified. |

An unexpected error or a crash is `failed` with the category `internal`. When there
is a run directory, a crash's stack trace is saved in `panic.txt` there.

### Questions and --context

When a command cannot go on without a decision, it stops with status
`needs_human` (exit 3) and says what it needs and how to answer:

```json
"needs_human": {
  "question": "Which service is affected?",
  "options": [
    { "id": "A", "label": "api" },
    { "id": "B", "label": "web" }
  ],
  "resume": {
    "argv": ["af", "triage", "the login page crashes"],
    "flag": "--context"
  }
}
```

To answer, run `resume.argv` with `--context <answer>` appended, for example
`af triage "the login page crashes" --context B`.

- `options` are lettered `A`, `B`, … (at most 26). No options means a free-text
  answer is expected.
- `resume.argv` is normally the original invocation. For a blocked job of the
  service it is `af jobs answer <id> --wait` instead, since re-running the original
  command would queue a second job.
- `--context` may be repeated. Answers accumulate, because earlier `--context` values
  stay in `argv`. An empty value is a `usage` error.
- If the original invocation used `--` before an input that starts with a dash, put
  `--context` before the `--`.

### Dry runs

`--dry-run` lets a command read from the forge but not write to it. Each write it
would have made (create or update an issue, add a comment or labels, open a pull
request, …) is sent nowhere and appears in `side_effects` with the state
`planned`. A dry run still creates the local run directory, and a dry run of
`af triage` still runs the model and costs what any run costs.

### Timeouts and signals

`--timeout` limits the command, not the reading of its input. When it runs out, the
envelope is `failed`/`timeout`. SIGINT, SIGTERM and SIGHUP stop the command the same
way, with `failed`/`cancelled`; a command that had already finished its work keeps
its result.

A stopped command gets 10 seconds to clean up. After that `af` prints the
`timeout` or `cancelled` envelope itself and exits 1, and a second signal during
those 10 seconds is ignored. `af revert-check` asks for up to 10 minutes, so that it
can put the tree back.

### Help and version

`-h`, `--help`, `af help` and `af help <command>` print plain text on stdout and exit
0. This is the only output that is not JSON. Help does not list
[`triage run` and `fix run`](#internal-commands). `af help` with an unknown command is
a `usage` envelope.

`af --version` prints a JSON document and exits 0:

```json
{
  "name": "af",
  "version": "v0.1.0",
  "commit": "bf4a001",
  "build_time": "2026-10-20T10:20:24Z",
  "go_version": "go1.27.2"
}
```

`make build` sets `version`, `commit` and `build_time`. A binary built another way
reports what the Go toolchain recorded, or `"dev"` with an empty commit and build
time.

Help and version are answered wherever they appear before `--`, without reading any
input or touching the forge. If both are given, help wins.

## Local commands

These commands run on this machine, in the checkout given by `--dir`. `af triage`
and `af fix` call a model; the other three only read files and run the project's own
checks.

### af triage

Diagnose a report and write the diagnosis back to the forge. The model reads the
checkout and hands over one diagnosis; it cannot write anything. `af` checks the
diagnosis and makes every write itself.

```
af triage <issue|file|text|-> [--dir DIR] [--context ANSWER]... [--repo owner/name]
          [--land issue|none] [--label L]... [--dry-run]
          [--brain NAME] [--sandbox NAME] [--budget USD] [--max-turns N] [--timeout 15m]
```

| Flag | Meaning |
|---|---|
| `--land issue\|none` | Where the diagnosis goes. `issue` (default) writes it to the forge: it replaces the body of the given issue, or creates an issue for any other input. `none` only writes `diagnosis.md` in the run directory. |
| `--label <label>` | Add this label to the issue. Repeatable, up to 10 distinct labels of 1 to 50 characters, with no comma and no leading or trailing space. Not with `--land none`. |
| `--repo <owner/name>` | The repository to create the issue in, for an input that is not an issue. |
| `--context <answer>` | Answer the question an earlier run stopped on. |
| `--dry-run` | Run the model as usual, but send no write to the forge. |
| `--brain`, `--sandbox`, `--budget`, `--max-turns`, `--script` | See [Sandboxes and brains](#sandboxes-and-brains). Defaults: `agentkit`, `$AF_SANDBOX` or `podman`, USD 2, 100 turns. |
| `--timeout` | Default 15 minutes. |

Triage does not need a clean working tree. It runs no git write and writes nothing
in the checkout outside `.agent-fox/`.

**What happens.**

1. **Preflight.** Before any model call, `af` checks the flags, the input and the
   landing. A pull or merge request URL, or text that is not valid UTF-8, is a
   `usage` error. A closed issue raises `issue_closed` and the run goes on. With
   `--land issue` (and no `--dry-run`): an input that is not an issue needs
   `--repo`; writing needs a forge token (`failed`/`auth`, with the variable in
   `fix_hint.env`); the repository must be readable and not archived; and a text or
   file report must fit in an issue body together with the diagnosis (65536 bytes on
   GitHub, 1048576 on GitLab, less 16896 bytes for the diagnosis). Otherwise the
   message suggests `--land none`.
2. **Brief.** `af` renders the brief for the model. See
   [model-usage.md](model-usage.md#what-the-model-is-sent).
3. **Model.** The model runs in a [sandbox](#sandboxes-and-brains) over a copy of the
   checkout, with read-only tools only, and submits one diagnosis. A diagnosis that
   breaks the [rules below](#the-diagnosis) is refused and the model tries again.
4. **Landing.** `af` checks the diagnosis against its own tree once more and writes
   it to the forge (see [Landing](#landing)).

A run that ends without a valid diagnosis writes nothing to the forge. It is
`failed`/`budget` if the model spent its budget or turns (with `fix_hint` naming the
flag and twice its value), else `failed`/`no_result` (retryable).

**Result.**

```json
{
  "run_id": "20261020_180425_8202d5",
  "issue": {"repo": "owner/name", "host": "github.com", "number": 7, "url": "https://github.com/owner/name/issues/7"},
  "action": "replaced_body",
  "diagnosis": {"title": "…", "severity": "high", "confidence": "probable", "root_cause": "…",
                "affected_files": [{"path": "main.go", "line": 6, "note": "…"}],
                "suggested_fix": "…", "acceptance_criteria": ["…"]},
  "labels": ["triaged"],
  "original_report_sha256": "…"
}
```

| Field | Meaning |
|---|---|
| `issue` | The issue the diagnosis was written to (or would have been, in a dry run). `null` when there is none. |
| `action` | `replaced_body`, `commented` (the body would have been too long, so the diagnosis went into a comment), `created_issue`, `planned` (dry run) or `none` (`--land none`, or a question). |
| `diagnosis` | The diagnosis as `af` normalised it. |
| `labels` | The labels added. Empty in a dry run. |
| `original_report_sha256` | SHA-256 of the original report kept in the body. |

**Statuses.** A landing, `--land none`, a dry run and a `not_reproducible` diagnosis
without a question are `done`. A diagnosis with a question is `needs_human`. When
the diagnosis is `confirmed` or `probable` and names an issue, `next` suggests
`af fix <issue url> --dir <dir>`.

#### Landing

1. `af` writes the rendered body to `diagnosis.md` in the run directory before
   anything goes to the forge. With `--land none`, that is all.
2. **For an issue**, `af` reads the body again and keeps the person's original
   report as it is now (if it changed since the brief, the warning
   `report_changed_during_triage` is raised). It replaces the body with the
   diagnosis followed by that report. If the result would be too long for the forge,
   the diagnosis goes into one comment instead and the body is left alone
   (`body_too_long`). Then the labels are added; if that fails, the warning
   `labels_failed` is raised.
3. **For any other input**, `af` creates an issue with the diagnosis title, the body
   and the labels.

Each write is listed in `side_effects` (`update_issue`, `add_comment`,
`create_issue`, `add_labels`). A write the forge refuses for lack of a valid
credential is `failed`/`auth`; after a rate limit or lost connection it is
`failed`/`forge`, retryable; any other refusal is `failed`/`forge`, not retryable.

#### The issue body

`af` renders the body the same way every time. `af fix`, and later triages, read it
back:

```
<!-- agent-fox:triage v1 run=<run_id> -->
## Diagnosis

**Severity:** <severity> · **Confidence:** <confidence>

<root_cause>

## Affected files

- `<path>:<line>` — <note>

## Suggested fix

<suggested_fix>

## Acceptance Criteria

- [ ] <criterion>

## Question

<text>

A. <option>

<sub>Diagnosed by agent-fox `af triage` (run <run_id>). The original report is kept below.</sub>

## Original report

<details>
<summary>Original report</summary>

<!-- agent-fox:original-report:begin -->
<the original report, as the person wrote it>
<!-- agent-fox:original-report:end -->

</details>
```

Empty sections are left out; `## Diagnosis` and `## Original report` are always
there. Triaging an issue again replaces the old diagnosis and keeps the first report
byte for byte. A body that does not start with the marker line is all original
report. A body that starts with the marker but was edited so that `af` cannot read it
back safely (a broken first line, unpaired markers, a missing heading, text after the
original report) is also treated as all original report, with the warning
`original_report_unparsed`, so that no one's words are lost. Bodies saved with CRLF
line endings are read as LF.

#### The diagnosis

The model submits one JSON object of at most 65536 bytes with exactly these fields:

| Field | Rule |
|---|---|
| `title` | Required; one line of 1 to 120 characters. |
| `severity` | `critical`, `high`, `medium` or `low`. |
| `confidence` | `confirmed`, `probable`, `suspected` or `not_reproducible`. |
| `root_cause` | Required; 1 to 6000 bytes. |
| `affected_files` | 1 to 30 items of `{path, line?, note}`. `note` at most 300 bytes; `line` from 1 to the length of the file. |
| `suggested_fix` | 1 to 6000 bytes. |
| `acceptance_criteria` | 1 to 15 strings of 1 to 300 bytes. |
| `question` | Optional `{text, options}`: `text` 1 to 2000 bytes, `options` none or 2 to 6 of 1 to 200 bytes. Only with confidence `suspected` or `not_reproducible`. |

With confidence `not_reproducible`, or with a question, `affected_files`,
`suggested_fix` and `acceptance_criteria` may be empty. Every path must name a file of
the checkout that git tracks or that is untracked and not ignored. Paths outside the
checkout, under `.git/` or `.agent-fox/`, directories, and links that lead outside are
refused, and the message names files with the same name. No string may contain a NUL
byte or the text `<!-- agent-fox:`, and the rendered diagnosis may be at most 16384
bytes.

#### Questions in triage

A diagnosis with a question lands nothing and posts no comment. The status is
`needs_human` with the question and its options. Re-run with `--context <answer>`:
the new run finds the earlier question in the run directories under `--dir` and gives
the model both the question and the answer.

#### Files of a triage run

Besides the [usual run files](#the-run-directory), a triage run writes to
`.agent-fox/run/<id>/`:

| File | Content |
|---|---|
| `brief.md` | The brief the model read. |
| `original.md` | The original report, exactly. |
| `triage.json` | The facts of the run: input, target repository, issue, hash of the original report, size and hash of the brief's prefix. |
| `submission.json` | The accepted diagnosis. |
| `diagnosis.md` | The rendered issue body. |

### af fix

Fix an issue. The model edits files in a sandbox and reports what it did. `af`
measures the change with the project's own checks, and only a change that does not
make things worse lands, on a branch of its own with a pull request.

```
af fix <issue> [--dir DIR] [--base BRANCH] [--context ANSWER]... [--repo owner/name]
       [--land pr|branch|none] [--no-verify]
       [--brain NAME] [--sandbox NAME] [--budget USD] [--max-turns N] [--timeout 45m]
```

| Flag | Meaning |
|---|---|
| `--land pr\|branch\|none` | Where the change goes. `pr` (default) pushes the branch and opens a pull request. `branch` only pushes. `none` keeps the branch local and contacts neither forge nor remote. |
| `--no-verify` | Land without measuring: no baseline, no checks after, a draft pull request that says `Refs`, status `unverified` and the warning `not_verified`. |
| `--base <branch>` | The branch to fix. It must be the branch that is checked out. |
| `--context <answer>` | Answer the question an earlier run stopped on. |
| `--repo <owner/name>` | The repository to open the pull request in, when `origin` does not name it. |
| `--brain`, `--sandbox`, `--budget`, `--max-turns`, `--script` | See [Sandboxes and brains](#sandboxes-and-brains). Defaults: `agentkit`, `$AF_SANDBOX` or `podman`, USD 10, 150 turns. |
| `--timeout` | Default 45 minutes. |

`af fix` takes only an issue URL. Text, files, stdin and pull request URLs are
`usage` errors; triage a report first to turn it into an issue.

**What happens.**

1. **Preflight.** These checks spend nothing, and the first that fails ends the run
   before any model call, branch or forge write:
   - flags and input are valid (`usage`);
   - `--dir` is the top level of a git work tree, on a branch, and `--base` (if
     given) is that branch (`usage`);
   - the working tree is clean (`failed`/`preflight`, naming up to 20 dirty paths);
   - unless `--land none`: there is an `origin` remote (`preflight`), and for `pr` a
     repository for the pull request (`usage`, hint `--repo`), a forge token
     (`auth`) and a readable, non-archived repository (`forge`);
   - the model's credential is set (`auth`).

   Then the sandbox opens, and inside it:
   - the repository has at least one check that can run (see
     [`af lang detect`](#af-lang-detect)), else `failed`/`preflight` with hint
     `--no-verify`;
   - the **baseline**: the checks run once on the tree as it is. A failing baseline
     raises `baseline_failing` and the run goes on; a check that already failed is
     not held against the change.
2. **Brief.** `af` renders the brief (see
   [model-usage.md](model-usage.md#what-the-model-is-sent)).
3. **Rounds.** The model edits files in the sandbox and submits one report, or one
   question. `af` copies the changed tree onto the checkout, uncommitted, and
   measures it:
   - changes under protected paths (`.git/`, `.agent-fox/`, `.specs/`,
     `.github/workflows/`, `.gitlab-ci.yml`) are put back, with the warning
     `protected_path_reverted`;
   - an empty change is refused as `no_change`;
   - the checks run again and are compared with the baseline (see
     [Comparing two gate results](#comparing-two-gate-results));
   - if the verdict allows landing, [`af revert-check`](#af-revert-check) runs. Only a
     change whose tests are proven by it **closes** the issue; any other says `Refs`
     (`fix_not_proven`).

   A refused change (`regressed`, `still_failing`, `no_change`, or `unverified` when
   nothing could be compared) goes back to the model as the next round, with the end
   of each failing check's output. A new round starts only while budget, turns and
   time remain.
4. **Land** or **park** (below).

All checks of the repository run inside the sandbox, never on this host, because they
are the repository's code and the model has written tests.

#### How a fix run ends

| The run | `status` | Exit | `outcome` | What is left |
|---|---|---|---|---|
| A change that may land | `done` | 0 | `landed` | A branch, one commit, the push and the pull request. The checkout is back on the base, clean. |
| The same, with `--no-verify` | `unverified` | 4 | `landed` | As above, as a draft pull request, with `not_verified`. |
| Budget, turns or rounds used up, with a refused or unreported change in the tree | `unverified` | 4 | `parked` | A `wip:` commit on its own branch, pushed unless `--land none`, and with `pr` a draft pull request that says `Refs`. Warnings `fix_not_landed` and, if the last round produced no report, `attempt_failed`. `next` says how to inspect the branch. |
| A question | `needs_human` | 3 | `needs_human` | The checkout as the run found it (`question_tree_kept` if a change could not be put back). |
| Budget, turns or rounds used up, nothing applied | `failed` | 1 | | The category of the last round's failure (`brain`, `no_result`, `budget`, `timeout`, `sandbox`, …). |
| Signal, or `--timeout` ending the command | `failed` | 1 | | `cancelled` or `timeout`. An applied change is parked on a local branch within 10 seconds (`park_failed` if that fails), so paid-for work is not lost. |

Usually `--timeout` ends a round rather than the whole command: each round gets the
time left minus the time reserved for measuring, so the run then ends as if its
bounds were used up.

**Landing** creates the branch `fix/<N>-<slug>` (made unique with `-2`, `-3`, …) from
the base commit, and one commit with the repository's hooks. The commit message is
`<type>: <subject>`, the change description, `Closes <issue URL>` or
`Refs <issue URL>`, and the trailer `Agent-Fox-Run: <run id>`. The push is tried up to
3 times (`push_retried`). With `pr`, the pull request body says what `af` measured and
quotes what the model reported; it is a draft when an acceptance criterion is `unmet`
or `partial` (`criteria_unmet`). The body is saved as `report.md`.

- A commit a hook refuses parks the change instead (`commit_hook_failed`).
- A push that still fails is `failed`/`git`; the branch and commit are kept and
  `next` names the push to run.
- A pull request the forge refuses raises `pull_request_failed`; the run is still
  `done`.

**Parking** commits `wip: <subject>` with the verdict, the failing checks, `Refs` and
the trailer, without running hooks. A failed push or pull request afterwards is a
warning (`push_failed`, `pull_request_failed`).

**Result.**

| Field | Meaning |
|---|---|
| `issue` | The issue URL. |
| `base`, `base_head` | The base branch and the full hash of its commit when the run began. |
| `rounds` | How many rounds ran. |
| `outcome` | `landed`, `parked`, `needs_human` or `failed`. |
| `verdict`, `landable`, `closes` | What `af` measured (`pass`, `pass_was_already_failing`, `regressed`, `still_failing`, `unverified`, `no_change`, or `null`), whether that lets the change land, and whether the revert check proved it. |
| `baseline`, `after` | `{gate_id, passed}` of the checks before and after, or `null`. |
| `revert_check` | `{outcome, reason}`, or `null`. |
| `changed_files`, `reverted_paths` | Paths git sees changed, and protected paths that were put back. |
| `land` | `pr`, `branch` or `none`. |
| `branch`, `commit`, `pushed` | The branch and full commit hash (or `null`), and whether the branch is on the remote. |
| `pull_request` | `{number, url, draft}` or `null`. |
| `box` | `{provider, image, network}` of the sandbox, or `null`. |

Side effects are `branch`, `commit`, `push` and `create_pull_request`. Artifacts
include the branch, commit and pull request, and the brief, submission, report, run
directory and transcript. `usage` is summed over all rounds.

#### Files of a fix run

Besides the [usual run files](#the-run-directory), a fix run writes to
`.agent-fox/run/<id>/`:

| File | Content |
|---|---|
| `brief.md` | The brief of the first round. |
| `original.md` | The original report, exactly. |
| `run.json` | The attempt number and nonce the sandbox reads. |
| `fix.json` | The state of the run (input, issue, base, pinned checks, baseline, rounds, cost so far). Written last and atomically. |
| `submission.json` | The accepted report. |
| `report.md` | The pull request body. Absent for a parked change. |

### af lang detect

Report what the repository at `--dir` is written in and which checks verify it. It
only reads files (and in a git work tree runs `git rev-parse` and `git ls-files`). No
model, no network, no run directory. Default `--timeout` 1 minute.

```
af lang detect [--dir DIR] [--write] [--timeout DURATION]
```

| Flag | Meaning |
|---|---|
| `--write` | Save the freshly detected checks to `.agent-fox/gate.json` (see [configuration](configuration.md#gatejson)), replacing any file there. Listed as the artifact `gate_file`. |

**Result.**

| Field | Meaning |
|---|---|
| `languages` | `[{name, files, manifest?}]`, most files first. Files are those git tracks plus untracked files that are not ignored (a directory walk outside git), never `.git/` or `.agent-fox/`. Languages: `go`, `python`, `javascript`, `typescript`, `rust`, `java`, `kotlin`, `ruby`, `c`, `c++`, `c#`, `php`, `scala`, `lua`, `bash`. `manifest` is the root file that marks the project. |
| `gate` | `{source, checks}`. `source` is `file` (from `gate.json`) or `detected`. Each check is `{name, argv, kind, source, available}`: `argv` is the command without a shell; `kind` is `lint`, `test` or `check` (both); `source` says where it came from (`Makefile:check`, `go.mod`, `gate.json`); `available` says whether the program exists on this machine. |
| `suite` | The commands that count as running the full test suite. |
| `programs` | The programs the checks may run. |

**How checks are detected.** Without `gate.json`, `af` looks at the files at the root
and the first rule that yields a check wins:

1. A Makefile (`GNUmakefile`, `makefile` or `Makefile`) with a `check` target:
   `make check`.
2. Else a Makefile with a `test` target: `make lint` (if that target exists) and
   `make test`.
3. Else each manifest adds its checks, in the order Go, Python, Node, Rust, Java:
   - `go.mod`: `go vet ./...` and `go test ./...`;
   - Python: `pytest` (when the project shows it uses pytest) or
     `python -m unittest discover`, behind `uv run` or `poetry run` when their lock
     file exists, plus `ruff check .` when ruff is configured;
   - `package.json`: `<pm> test` (unless it is npm's placeholder) and
     `<pm> run lint`, with `pnpm`, `yarn` or `bun` chosen by lock file, else `npm`;
   - `Cargo.toml`: `cargo test`;
   - `pom.xml`: `./mvnw test` or `mvn -q test`; `build.gradle(.kts)`: `./gradlew test`
     or `gradle test`.

The selected checks depend only on the files, never on which programs are installed;
only `available` does.

| Outcome | When |
|---|---|
| `done` | The repository could be read. A check whose program is missing raises `check_unavailable`. |
| `failed`/`config` | `.agent-fox/gate.json` cannot be used. There is no fallback to the Makefile. |
| `failed`/`io` | `--dir` cannot be read, or `gate.json` cannot be written. |

Example, on this repository:

```json
{
  "languages": [{"name": "go", "files": 57, "manifest": "go.mod"}],
  "gate": {
    "source": "detected",
    "checks": [
      {"name": "make check", "argv": ["make", "check"], "kind": "check", "source": "Makefile:check", "available": true}
    ]
  },
  "suite": [["make", "check"], ["make", "test"], ["go", "vet", "./..."], ["go", "test", "./..."]],
  "programs": ["make", "go", "gofmt"]
}
```

### af gate run

Run the checks that [`af lang detect`](#af-lang-detect) reports on the tree in
`--dir`, or return the stored result for the same tree. No model, no network, no run
directory. It writes only under `.agent-fox/`. Default `--timeout` 30 minutes.

```
af gate run [--dir DIR] [--timeout DURATION]
```

The available checks run one after another, in order. Each has 20 minutes; a check
that takes longer is stopped and gets the status `timeout`, and the next check still
runs. Unavailable checks are skipped. Checks run without credential-like environment
variables (model vendor and cloud keys, and anything ending in `_TOKEN`, `_SECRET`,
`_KEY`, `_PASSWORD` and similar), because a test suite is repository code. Each
check's full output is saved to `.agent-fox/gate/<id>/NN-<name>.log`.

**Result.**

| Field | Meaning |
|---|---|
| `id` | The cache id. The result is stored in `.agent-fox/gate/<id>/`. |
| `tree` | A hash of the content of the files in the tree (tracked and untracked-not-ignored). It does not depend on `HEAD`, the checkout's location or ignored files. |
| `table_source` | `file` or `detected`. |
| `cached` | True when a stored result was returned and nothing ran. |
| `passed` | True only when at least one check ran and every check that ran passed. |
| `checks` | `[{name, status, exit_code, duration_ms, log, tail}]`. `status` is `pass`, `fail`, `error` (could not start; `exit_code` -1) or `timeout` (`exit_code` -1). `tail` is the last 40 lines of output, at most 4096 bytes. |
| `skipped` | `[{name, reason}]` for checks that did not run. |

| Outcome | When |
|---|---|
| `done`, `passed: false` | A check failed, errored or timed out. A red gate is a measurement, not a failure of `af`. |
| `done`, warning `no_checks` | No check can run here. Nothing ran. |
| `done`, warning `tree_changed_during_gate` | The checks changed the tree. The result is kept under the hash taken before. |
| `failed`/`timeout`, `failed`/`cancelled` | `--timeout` ran out or a signal arrived. Nothing is stored. |
| `failed`/`config` | `.agent-fox/gate.json` cannot be used. |
| `failed`/`io` | The tree cannot be read, or a log or the result cannot be written. |

**Caching.** The cache key combines the tree hash and the list of checks. The same
tree and the same checks give the same key on any machine, so checks run inside a
sandbox count for the same tree outside it. Changing one byte of a file in the tree
gives a new key; changing only ignored files does not. A stored result is not used
when it skipped a check that can run here now. `.agent-fox/gate/` is safe to delete.

#### Comparing two gate results

`af fix` compares the checks after a change with the baseline, check by check
(`fail`, `error` and `timeout` all count as "not pass"):

| Before | After | Verdict |
|---|---|---|
| pass | pass | `pass` |
| not pass | pass | `pass_was_already_failing` |
| pass | not pass | `regressed` |
| not pass | not pass | `still_failing` |

The overall verdict is the worst one any check has, in the order `regressed`,
`still_failing`, `pass_was_already_failing`, `pass`, so fixing one check while
breaking another is a regression. If either side ran no check, the verdict is
`unverified`. Only `pass` and `pass_was_already_failing` may land.

### af revert-check

Check that a change's tests actually test it: do they fail when the rest of the
change is taken out? No model, no network, no run directory. Default `--timeout` 30
minutes.

```
af revert-check --since REF [--dir DIR] [--timeout DURATION]
```

| Flag | Meaning |
|---|---|
| `--since <ref>` | Required. The commit the change starts from. |

`--dir` must be the top level of a git work tree and `--since` must name a commit;
otherwise the status is `usage` and nothing is touched.

**What it does.**

1. Refuse (`failed`/`restore`) if `.agent-fox/revert.patch` exists: an earlier check
   was interrupted, or another is running.
2. List the paths that differ from `REF` (staged, unstaged, and untracked files that
   are not ignored), and sort them into test files and other files.
3. Stop early, changing nothing, when there is nothing to measure: no changes, only
   test changes, no test changes, or no test check in the gate.
4. Run the checks on the current tree (or use the cached result). If a test check
   fails or cannot run here, the outcome is `inconclusive`.
5. Save the non-test part of the change to `.agent-fox/revert.patch`, take it out of
   the working tree (the index is never touched), and run only the test checks.
6. Put the change back, even after a signal, the timeout or a crash, and check that
   the tree and `.git/index` are exactly as before. Then remove the patch.

Test files are, for example: Go `_test.go` and `testdata/`; Python `test_*.py`,
`*_test.py`, `conftest.py`; JavaScript and TypeScript `*.test.*`, `*.spec.*`,
`__tests__/`; Java `src/test/`, `*Test.java`, `*Tests.java`, `*IT.java`; Ruby
`*_spec.rb`, `*_test.rb`; and anything under a directory named `test`, `tests` or
`spec`.

Checks must not leave new files in the tree outside ignored paths: that changes the
tree and counts as a failed restore even though the change is back.

**Recovering.** If `af revert-check` is killed (SIGKILL) or abandoned, the patch stays
in `.agent-fox/revert.patch` and the next check refuses to start. When
`git apply --check .agent-fox/revert.patch` passes, the tree is missing the change:
run `git apply .agent-fox/revert.patch`, then delete the file.

**Result.**

| Field | Meaning |
|---|---|
| `outcome` | `proven`, `not_proven`, `inconclusive`, `no_changes`, `no_code_changes` or `no_test_changes`. |
| `reason` | `proven`: `tests_fail_without_the_change`. `not_proven`: `tests_pass_without_the_change`. `inconclusive`: `no_test_check`, `after_not_green`, `test_check_unavailable`, `reverted_check_error` (a check could not start on the reverted tree), `table_changed_by_revert` (the reverted tree has different test checks, for example because the change added the Makefile) or `nothing_to_revert`. Others: `no_paths_changed`, `only_test_files_changed`, `no_test_files_changed`. |
| `closes` | True only for `proven`. |
| `since` | The full hash `--since` resolved to. |
| `test_files` | The changed test files. |
| `reverted_files` | The files that were set back; empty when nothing was. |
| `after` | The gate id for the current tree, or `""`. |
| `reverted` | `{id, passed, checks}` of the reverted tree, as in [`af gate run`](#af-gate-run), or `null`. |
| `restored` | Always true in a result: a tree that could not be restored is a failure. |

| Outcome | When |
|---|---|
| `done` | Every outcome above. All except `proven` and `no_changes` raise `fix_not_proven`. |
| `usage` | `--dir` is not a work tree top level, or `--since` is missing or not a commit. |
| `failed`/`restore` | A leftover patch is in the way, or the change could not be put back (something changed a path during the checks) or the tree came back different. The patch is kept and the message names it. |
| `failed`/`git` | git failed. |
| `failed`/`config` | `.agent-fox/gate.json` cannot be used. |
| `failed`/`timeout`, `failed`/`cancelled` | `--timeout` ran out or a signal arrived. The tree is restored first. |
| `failed`/`io` | A file could not be read or written. |

## Sandboxes and brains

`af triage` and `af fix` run a *brain* (the agent that talks to the model) inside a
*box* (a sandbox that holds a copy of the checkout). The model works on the copy; `af`
reads back what it submitted and makes every git and forge write on the host.

**Shared flags.**

| Flag | Meaning |
|---|---|
| `--brain <name>` | `agentkit` (the AgentKit agent on a real model; the default) or `scripted` (replays a script, no model; see [development.md](development.md#the-scripted-brain)). |
| `--sandbox <provider>` | `podman` or `local`. Default: the value of `AF_SANDBOX`, else `podman`, on every operating system. If the provider cannot open, `af` fails; it never falls back to `local`. |
| `--budget <usd>` | Stop after this much model cost. Must be above 0, and the model must have a known price (otherwise a `usage` error naming `AF_MODEL`). |
| `--max-turns <n>` | Stop after this many turns. At least 1. |
| `--script <file>` | The script for `--brain scripted`, and only with it. |

Bad values are `usage` errors that name the flag, and they are found before any box
opens. The model is set with `AF_MODEL` and needs `ANTHROPIC_API_KEY`; see
[configuration](configuration.md#model-and-credentials).

**Providers.**

| Provider | Isolated | What it is |
|---|---|---|
| `podman` | yes | A container of the image `AF_SANDBOX_IMAGE`, default `quay.io/agentfox/tools:latest` (the sandbox image with `af` in it; `make build-tools-container` builds it). The agent runs inside the container. An unreachable engine is `failed`/`sandbox`, not retryable (off Linux, try `podman machine start`). A missing image that cannot be pulled is `failed`/`sandbox`, retryable. |
| `local` | no | The tools run in the `af` process with its privileges, on a copy of the tree. Every box raises the warning `box_unconfined`. Useful for development, not for untrusted work. |

**What reaches the box.**

- The checkout goes in with its `.git`, so git commands inside see the real history.
  It must be the top level of a regular work tree (not a subdirectory, linked
  worktree or submodule). Files the repository ignores (a `.env`, build output) stay
  out, unless git tracks them.
- Credentials stay on the host. No forge token ever enters a box, and credentials in
  `.git/config` (remote URLs, extra headers, credential helpers) are stripped from the
  box's copy. Under `podman`, the box gets `ANTHROPIC_API_KEY` and nothing else, since
  the agent runs there. Under `local` the agent runs on the host and the box gets no
  credential.
- Network: `podman` gives the box the whole network (the agent needs its vendor, and
  checks may fetch packages); `af triage` with the agent on the host gets none. Under
  `local` nothing is enforced.
- Nothing the model commits in the box reaches the host's branch, and `.git` never
  comes back out.

**What the model may not do.** Every tool call passes a gate before it runs. The model
may use only the tools of its brief: read-only tools for triage; for fix also writing
files and running commands. Commands are further restricted: no git command that
writes (`af` makes every commit, branch and push), no running the full test suite
directly (use `af gate run`, whose result `af` reuses; targeted test runs are fine), no
`af` command other than `lang detect`, `gate run`, `revert-check` and `--version`, and
no writes under protected paths (by default `.git/`, `.agent-fox/`, `.specs/`,
`.github/workflows/`, `.gitlab-ci.yml`). A denied call is not run and the model is told
why. This gate is a safeguard, not a security boundary; the box is. The rules are
configured with [`guard.json`](configuration.md#guardjson) and described in detail in
[architecture.md](architecture.md#the-tool-policy).

The model hands back its result as one JSON submission. `af` decides from the
submission file alone, never from what the model says, and checks it again against
its own tree. See [architecture.md](architecture.md#the-submission-protocol).

## Service client commands

Eleven commands make `af` the client of the agent-fox service: four `workspace`
commands and seven `jobs` commands. They follow the [envelope contract](#conventions),
run no model and make no run directory. The service does the work; its cost is in
`result.job.usage`. The HTTP API they call is in [api.md](api.md).

```bash
export AF_URL=http://127.0.0.1:8090
af workspace subscribe widgets --hub-token-file pat   # hand the service a workspace token
af jobs submit fix 42 --workspace widgets --wait      # queue a fix and follow it
af jobs list --workspace widgets --state blocked      # jobs waiting for an answer
af jobs answer job_5f0c1a9e2b7d4c3a8e10 B --wait      # answer one
```

### Connecting to the service

**Server.** Every client command takes `--server <url>`; without it, `AF_URL` is
used (an empty value counts as unset). The URL must be absolute `http` or `https`,
with an optional path prefix and no query, fragment or user info. A trailing `/` is
dropped. A plain `http` URL to a host other than loopback raises `server_insecure`,
because the credential crosses the network in clear. No server at all is a `usage`
error with the hint `{flag: "--server", env: "AF_URL"}`.

**Credential.** Every request carries your hub credential as
`Authorization: Bearer <token>`. Its only source is the top-level `token` key of
`~/.af/config.toml`, which hub's `afc login` writes. `af` never writes this file.

```toml
# ~/.af/config.toml
token = "your hub credential"
```

| Problem | Result |
|---|---|
| No home directory; the file exists but cannot be read | `failed`/`io` |
| The file is missing; `token` is missing, empty or not a string | `failed`/`auth`, suggesting `afc login` |
| The file is not valid TOML; the token contains a control character | `failed`/`config` |

On Unix, a file readable by group or others raises `credential_file_permissions`.

**Order of checks.** Before sending anything, a client command checks, in order: its
flags; its arguments and input (including stdin and token files); the server; the
credential. So a usage mistake is always reported before an `auth` or `config`
problem.

**Requests.** A request (other than the event stream) may take at most two minutes,
or less if `--timeout` is shorter; `--timeout` has no default. At most 8 MiB of an
answer is read. Redirects are not followed, so the credential reaches only the server.
The standard proxy variables (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`) apply. The
credential and any workspace token are removed from all output (values shorter than
8 bytes excepted).

### af workspace

A workspace is a hub workspace the service works on. Subscribing hands the service a
token it uses to act on that workspace.

| Command | Request | Result |
|---|---|---|
| `af workspace subscribe <slug> --hub-token-file FILE\|-` | `PUT /api/v1/workspaces/<slug>` | The subscription |
| `af workspace list` | `GET /api/v1/workspaces` | `{workspaces}` |
| `af workspace get <slug>` | `GET /api/v1/workspaces/<slug>` | The subscription |
| `af workspace unsubscribe <slug>` | `DELETE /api/v1/workspaces/<slug>` | `{slug}` |

`subscribe` reads the workspace token from a file, or from stdin with `-`, never from
the command line where it would end up in shell history. After trimming, the token must
be one non-empty line of valid UTF-8, without control characters, at most 4096 bytes.
Subscribing again replaces the subscription. Subscribing also creates the agent-fox
labels on the workspace's repository (see [api.md](api.md#subscribe)).

`unsubscribe` cancels the workspace's open jobs first. It can take up to a minute.

### af jobs submit

Queue a triage or fix job on a subscribed workspace.

```
af jobs submit triage <issue|file|text|-> --workspace SLUG [--wait] [--context ANSWER]... [--server URL] [--timeout D]
af jobs submit fix <issue> --workspace SLUG [--wait] [--context ANSWER]... [--server URL] [--timeout D]
```

The input is read by the first rule that matches:

| Input | Sent as |
|---|---|
| `-` | `text`, read from stdin |
| any `http(s)://` URL | `issue`, as given. The service accepts issue URLs of the forges it knows and refuses pull requests and other URLs (`usage`). |
| a positive integer, with or without `#` | `issue`, as a number |
| `owner/repo#N` | `issue`, as a string |
| an existing regular file | `file: {name, content}` |
| anything else | `text` |

A fix job takes only the three issue forms. Text and file content must not be empty,
contain NUL bytes or be invalid UTF-8. Content over 131072 bytes is cut and raises
`input_truncated`. `--context` values are sent in order.

The job is recorded as an artifact `{kind: "job", name: <id>, url}` as soon as the
service accepts it. Without `--wait`, the status is `done`, the result is
`{job, envelope: null}`, and `next` suggests `af jobs watch <id>`. With `--wait`, the
command [follows the job](#following-a-job). If a job of the same kind is already
open for the issue, the service refuses with a `conflict` that names it.

### af jobs list, get, watch, cancel, answer and retry

| Command | Request | Result |
|---|---|---|
| `af jobs list [--workspace S] [--state S] [--cursor C]` | `GET /api/v1/workspaces/<S>/jobs`, or `GET /api/v1/jobs` without `--workspace` | One page: `{jobs, next_cursor}` |
| `af jobs get <id>` | `GET /api/v1/jobs/<id>` | `{job, envelope: null}` |
| `af jobs watch <id> [--since N]` | [Follows the job](#following-a-job) | The job and its last envelope |
| `af jobs cancel <id>` | `POST /api/v1/jobs/<id>/cancel` | `{job, envelope: null}` |
| `af jobs answer <id> (<text> \| --context <text> \| --option <id>) [--wait]` | `POST /api/v1/jobs/<id>/answer` | `{job, envelope: null}`, or the followed job with `--wait` |
| `af jobs retry <id> [--wait]` | `POST /api/v1/jobs/<id>/retry` | `{job, envelope: null}`, or the followed job with `--wait` |

- `list` returns one page. When there is another, `next` repeats the command with
  `--cursor <next_cursor>`.
- `answer` takes exactly one answer. `--option` is uppercased and sent as the option
  id. A text answer that matches an option id of the question (ignoring case) is sent
  as that option; any other text is sent as a free answer.
- `cancel` takes no `--wait`. `answer` and `retry` with `--wait` follow the job.

### Following a job

`af jobs watch`, and `--wait` on `submit`, `answer` and `retry`, read the job's event
stream until the job is *at rest*: `blocked`, `done`, `failed` or `cancelled`, with no
attempt running. Each event is delivered once, in order, as a `job_event` event of the
command. `watch` also prints each event to stderr as one line of JSON; `--wait` prints
nothing to stderr.

**Reconnecting.** If the stream ends early (dropped connection, 60 seconds of
silence, a server error), `af` reads the job and, if it is not at rest, reconnects
from the last event it saw. It waits 1 second after a try that delivered events, and
1, 2, 4 and 8 seconds after consecutive failed tries. The fifth failed try in a row
ends the command with a retryable `service` (or `hub`) failure; the job may still be
running, and `next` suggests `af jobs watch <id> --since <last seq>`. A 401, 404 or
other 4xx ends it at once.

**The result** is `{job, envelope}`, where `envelope` is the envelope of the job's
latest finished attempt (null if unavailable; `attempt_envelope_unavailable` if
fetching it failed). That envelope's warnings are added to the command's own. The
status follows the job:

| Job state | Status (exit code) |
|---|---|
| `done` | `done` (0), or `unverified` (4) when the attempt was unverified |
| `blocked` | `needs_human` (3), with the question and options. `resume` is `af jobs answer <id> --wait` with `--context`. |
| `failed` | `failed` (1) with the attempt's error, or `unverified` (4) or `usage` (2) when the attempt ended so |
| `cancelled` | `failed`/`job_cancelled` (1), not retryable |

A failed or cancelled ending has no `result`; the job artifact is still listed, and
`af jobs get <id>` shows it.

**Stopping `af`.** A signal or `--timeout` while following stops following but never
cancels the job, which keeps running on the service. The envelope is
`failed`/`cancelled` or `failed`/`timeout`, and `next` suggests `af jobs watch <id>`
and `af jobs cancel <id>`.

### How service answers map to statuses

| The service answers | Status (exit) | `error.category` | `retryable` |
|---|---|---|---|
| 2xx | the command's own | | |
| 400 | `usage` (2) | `usage` | false |
| 401 | `failed` (1) | `auth` | false |
| 404 | `usage` (2) | `usage` | false |
| 409 | `failed` (1) | `conflict` | false |
| 422 | `failed` (1) | `auth` | false |
| 502 | `failed` (1) | `hub` | true |
| 500 | `failed` (1) | `service` | false |
| 503, 504, other 5xx | `failed` (1) | `service` | true |
| 3xx, other 4xx | `failed` (1) | `service` | false |
| transport error, or the two minutes ran out | `failed` (1) | `service` | true |
| body too large or not the expected JSON | `failed` (1) | `service` | false |

The message is the service's `error.message` followed by its `error.code`. Hints: a
401 suggests `afc login`; a 404 on a workspace, or a 409 `not_subscribed` or
`token_unreadable`, suggests `af workspace subscribe <slug> --hub-token-file FILE`;
a 422 points at `--hub-token-file`.

## Internal commands

`af triage run --run-id ID` and `af fix run --run-id ID` are what a sandbox runs when
the agent works inside it. `af triage` and `af fix` start them; people do not, and
help does not list them. They run the agent on the brief already placed in the run
directory and record its submission there. A run directory without a placed brief is
a `usage` error: "run <id> has no brief placed in it".

| Command | Flags | Result |
|---|---|---|
| `af triage run` | `--run-id`, `--dir`, `--budget`, `--max-turns` | `{run_id, submitted}` |
| `af fix run` | `--run-id`, `--dir`, `--brain`, `--sandbox`, `--budget`, `--max-turns`, `--script` | `{run_id, round, submission, submission_path, question, submitted}` |

Both default to the `--timeout` of their loop. `af triage run` reads the directory in
place, without a copy, and says so with `box_unconfined`. See
[architecture.md](architecture.md#brains-that-run-as-a-process).

## The agent-fox command

`agent-fox` is the long-running service. It reads one configuration file, keeps its
state in one SQLite file, and asks [hub](https://github.com/agent-fox-dev/hub) who is
calling. It does not use the envelope: it runs until stopped and logs JSON to stderr.
Its file is described in [configuration.md](configuration.md#the-agent-fox-service)
and its API in [api.md](api.md).

```
agent-fox [--config PATH]
agent-fox --version
agent-fox --help
```

| Flag | What it does |
|---|---|
| `--config PATH` | The configuration file. Default: the first that exists of `./config.toml` and `$XDG_CONFIG_HOME/agent-fox/config.toml` (`~/.config/agent-fox/config.toml`). If none exists, the start fails and lists the paths tried. |
| `--version` | Print the version document (as for [`af --version`](#help-and-version), with the name `agent-fox`) and exit 0. `GET /version` returns the same. |
| `--help` | Print usage and exit 0. |

An unknown flag, a missing flag value or a positional argument prints the error and
usage on stderr and exits 2.

### Logs

stdout carries only `--version` and `--help`. stderr is a log with one JSON record per
line:

```
{"time":"2026-10-22T10:14:39.492024+02:00","level":"ERROR","msg":"start failed","error":"config file /etc/agent-fox/config.toml: unknown key server.external_url"}
{"time":"2026-10-22T10:14:47.581638+02:00","level":"INFO","msg":"listening","addr":"127.0.0.1:62760"}
{"time":"2026-10-22T10:14:47.728386+02:00","level":"INFO","msg":"request","method":"GET","path":"/healthz","status":200,"duration":66708}
```

| `msg` | Level | When |
|---|---|---|
| `start failed` | ERROR | The service could not start. The only record of such a run; `error` names the key, file, address or reason. |
| `hub.url is plain http to a host that is not a loopback address: …` | WARN | `hub.url` uses plain `http` to a remote host. The service starts anyway. |
| `listening` | INFO | The listener is bound; `addr` is the address (useful with port `0`). |
| `request` | INFO | One per request: `method`, `path` (no query), `status`, `duration` (ns) and `user` (the hub user id, once known). |
| `hub is unavailable` | WARN | A call to hub failed; the request got 502. |
| `panic in handler` | ERROR | A handler crashed; the request got 500 and the service goes on. |
| `the subscription could not be stored` (and `listed`, `read`, `removed`) | ERROR | A database failure; the request got 500. |
| `an unexpected failure of the check of the caller` | ERROR | Checking the caller with hub failed unexpectedly; the request got 500. |
| `the listener failed` | ERROR | The listener failed while serving. Exit 1. |
| `requests in flight did not finish within the grace period` | WARN | Shutdown waited 30 seconds; the service exits 0 anyway. |
| `the database could not be closed` | ERROR | Closing the database at exit failed. |
| `http: …` | WARN | A message from Go's HTTP server. |

The jobs, the worker and the label poll log more records; they are described in
[api.md](api.md). Tokens, the key file's content and the `Authorization` header are
never logged.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Stopped by SIGINT or SIGTERM, or printed the version or usage. |
| 1 | The listener could not be bound (address in use, not allowed, unresolvable), or failed while serving. |
| 2 | The command line, configuration, key file or database is wrong, or the database was written by a newer `agent-fox`. An operator has to fix a file. |

A failed start writes one `start failed` record whose `error` names the problem, such
as `unknown key server.external_url`, `hub.auth_cache_ttl: 16m is outside 1s to 15m0s`,
`the environment variable HUB is not set`, or a key file that is missing, not base64,
not 32 bytes, or readable by group or others.

### Starting and stopping

The service loads, in order, the configuration, the key file and the database (created
and migrated if needed), then binds the listener. A bad configuration or key leaves no
database behind.

On SIGINT or SIGTERM it stops accepting connections, gives requests in flight up to 30
seconds, closes the database and exits 0. A second signal changes nothing; only
SIGKILL stops it sooner. `GET /healthz` answers while the process runs; `GET /readyz`
answers once the database does.

## Reference

### Error categories

| `error.category` | Meaning |
|---|---|
| `usage` | The invocation is wrong. Status `usage`. |
| `auth` | A credential is missing or refused: no forge token for a write or a private issue, no model credential (`fix_hint.env` names the variable), a missing or refused service credential (401), or a workspace token hub refuses (422, hint `--hub-token-file`). |
| `forge` | The forge could not be reached or refused. Retryable after a rate limit, server error or lost connection; not after a refusal. |
| `io` | A file, stdin or the run directory could not be read or written, including `~/.af/config.toml`. |
| `config` | A configuration file cannot be used: `.agent-fox/gate.json`, `.agent-fox/guard.json`, `~/.af/config.toml`, or the model provider's settings. The message names the file. |
| `cancelled` | `af` received SIGINT, SIGTERM or SIGHUP. |
| `timeout` | `--timeout` ran out, or the model ran out of time without a submission. Not retryable; `fix_hint` suggests twice the value. |
| `internal` | A bug: an unexpected error or a crash. |
| `restore` | A tree changed for measuring could not be put back, or a leftover `.agent-fox/revert.patch` is in the way. The message names the patch. |
| `git` | git failed in a way the command cannot work around. |
| `sandbox` | The box could not be opened, synced or exported. Not retryable when the engine is unreachable or `af` in the image is missing or broken; retryable when the image cannot be pulled or a sync or export fails. When the provider was the default, `fix_hint` names `--sandbox` and `AF_SANDBOX`. |
| `brain` | The agent returned an error without a submission (retryable) or crashed (not retryable; the stack is in `brain-panic.<attempt>.txt`). |
| `no_result` | The agent ended without a valid submission. Retryable. |
| `budget` | The agent used its cost or turn limit without a submission. Not retryable; `fix_hint` suggests twice the limit. |
| `submission` | A submission was refused. Retryable. No command ends with it, since a refusal goes back to the model. |
| `preflight` | A fact about the repository stops `af fix` before it spends anything: a dirty tree, no runnable check (hint `--no-verify`), or no `origin` remote. Not retryable. |
| `service` | The agent-fox service could not be reached or gave an unusable answer. See [the mapping](#how-service-answers-map-to-statuses). |
| `conflict` | The service refused because of a job's or workspace's state (409): `invalid_state`, `duplicate_job`, `not_subscribed`, `token_unreadable`. Not retryable. |
| `hub` | The service could not reach hub (502). Retryable. |
| `job_cancelled` | A job `af` was following was cancelled on the service. Not retryable. |

### Warning codes

A warning never changes the status. A warning identical to an earlier one is recorded
once.

**Any command**

| Code | Raised when |
|---|---|
| `input_truncated` | The input was cut at 131072 bytes, or an issue has more comments than were read. |
| `comments_unreadable` | The issue was read but its comments were not. |
| `envelope_unwritable` | `envelope.json` could not be written to the run directory. |
| `events_unwritable` | An event could not be written to the run directory. Raised once. |
| `invalid_warning_code` | A command raised a malformed warning code; the message starts with it. |

**Fact commands**

| Code | Raised when |
|---|---|
| `check_unavailable` | `lang detect`: a check's program is not installed. The check stays, with `available: false`. |
| `no_checks` | `gate run`: no check can run here. Nothing ran. |
| `tree_changed_during_gate` | `gate run`, `fix`: the checks changed the tree. |
| `fix_not_proven` | `revert-check`, `fix`: the tests are not proven to test the change, so it must not close its issue. |

**Sandboxes and brains** (`triage`, `fix`)

| Code | Raised when |
|---|---|
| `box_unconfined` | A `local` box was opened (no isolation), or `triage run` read a directory in place. |
| `box_af_version_mismatch` | The `af` inside the box image is another version than the running one. The run goes on; the host re-checks everything. |
| `box_close_failed` | The box could not be closed. |
| `brain_usage_unavailable` | The agent in the box left no readable usage, so the cost is reported as 0. |
| `brain_output_unreadable` | The agent's output could not be copied out of the box. |
| `brain_error` | The agent failed or crashed after it had submitted. The submission stands. |
| `model_unpriced` | The model has no known price, so no cost is reported. |
| `map_outline_skipped` | The repository map lists file names only, because the repository is too large. |
| `submission_invalid` | The submission file did not count; the message lists the problems. |
| `submission_claim_mismatch` | The agent says it submitted and there is no valid submission, or the reverse. The file decides. |
| `submission_not_removed` | A submission file `af` did not accept could not be removed. |
| `transcript_unwritable` | The transcript could not be written in full. |
| `ledger_unwritable` | A ledger entry could not be written. |

**af triage**

| Code | Raised when |
|---|---|
| `issue_closed` | The issue is closed (also `fix`). The run goes on. |
| `report_changed_during_triage` | The issue body changed during the run. The current report is kept. |
| `body_too_long` | The body with the diagnosis would be too long, so the diagnosis was posted as a comment. |
| `labels_failed` | The diagnosis was written but adding labels failed. |
| `original_report_unparsed` | The body starts with the triage marker but cannot be read back, so all of it is kept as the report. |

**af fix**

| Code | Raised when |
|---|---|
| `baseline_failing` | The checks already fail before any change. |
| `question_not_found` | No earlier run asked the question a `--context` answers. |
| `protected_path_reverted` | The change touched a protected path, which was put back. One per path. |
| `criteria_unmet` | An acceptance criterion is `unmet` or `partial`, so the pull request is a draft. |
| `not_verified` | The change landed with `--no-verify`. Status `unverified`. |
| `push_retried` | A push failed and is being retried (up to 3 attempts). |
| `commit_hook_failed` | A repository hook refused the commit; the change was parked. |
| `pull_request_failed` | The branch is pushed but the forge refused the pull request. |
| `fix_not_landed` | The change did not land and is parked on a branch; the message names it. |
| `push_failed` | A parked branch could not be pushed; `next` names the push. |
| `attempt_failed` | The last round produced no report; the change is parked as it is. |
| `park_failed` | The run was stopped and the change could not be parked; it is in the working tree. |
| `question_tree_kept` | The run ended on a question and an earlier round's change could not be put back. |
| `report_unwritable` | `report.md` could not be written. |

**Service client**

| Code | Raised when |
|---|---|
| `server_insecure` | The server is plain `http` to a non-loopback host. |
| `credential_file_permissions` | `~/.af/config.toml` is readable by group or others (Unix). |
| `attempt_envelope_unavailable` | The envelope of the job's last attempt could not be fetched. |

Jobs run on the service add the worker's own warnings to their envelopes; see
[api.md](api.md#what-the-worker-adds-to-an-attempt).

### The run directory

Commands that run a model (`af triage`, `af fix` and their `run` commands) create
`<dir>/.agent-fox/run/<id>/` just before they start, also under `--dry-run`. The id is
`YYYYMMDD_HHMMSS_<6 hex>` in UTC; it is the envelope's `run_id`, and the directory is
listed as the artifact `run_dir`. An invocation that is refused leaves nothing behind.

`.agent-fox/` holds a `.gitignore` containing `*`, so git ignores all of it. `af`
creates that file only when it is missing and never edits the repository's own
`.gitignore`.

```
<dir>/.agent-fox/
  .gitignore                  "*"
  run/<id>/
    envelope.json             the envelope, exactly as printed
    events.jsonl              the event stream (below)
    ledger.jsonl              what the command recorded and measured
    report.md                 a report, if the command wrote one
    panic.txt                 the stack trace, if the command crashed
    brief.<attempt>.md        the brief given to the agent
    hook.json                 the tool-gate settings placed with it
    submission.json           the accepted submission, if any
    transcript.<attempt>.jsonl  the agent's tool calls (in-process agent)
    brain-panic.<attempt>.txt   the stack trace of a crashed agent
    agentkit.<attempt>.json, agentkit.<attempt>.stderr   output of an agent that ran inside the box
    …                         the command's own files: see af triage and af fix
```

Delete a run directory when you no longer need it. Only a later run of the same input
with `--context` reads an old one, to find the question it asked. Other files under
`.agent-fox/` are described in [configuration](configuration.md#repository-files).

**Events.** `events.jsonl` has one JSON object per line, written as things happen:

```
{"seq": 1, "ts": "<RFC 3339>", "run_id": "<id>", "type": "<type>", "data": { }}
```

| `type` | `data` |
|---|---|
| `run_start` | `{tool, argv, input_kind}`. Always first. |
| `warning` | `{code, message}`, one per warning. |
| `step` | `{name, brain?}`: a stage of the run. `triage` uses `preflight`, `brief`, `brain`, `land`. |
| `heartbeat` | `{brain, elapsed_ms}`, every 30 seconds while the agent runs. |
| `tool_call` | `{brain, tool, decision, reason, ok, duration_ms}` for each tool call that reached the gate. Arguments and results are left out, since they can hold file content. |
| `job_event` | A service event, as received, while a client command follows a job. |
| `run_end` | `{status, exit_code, duration_ms}`. Always last. |

The ledger's entries are described in
[architecture.md](architecture.md#the-run-directory-and-ledger).

### Envelope versioning

`schema_version` versions the envelope's fields and types, separately from the build
that `af --version` reports. Check the major number before reading the rest.

Within a major version, changes are additive: new top-level fields, new fields on
existing objects, and new values in open sets (warning codes, error categories,
artifact kinds, event types). These need a new major version: removing, renaming or
retyping a field, or changing a closed set (`status`, `exit_code`, `input.kind`,
`side_effects[].state`, `usage.cost_source`).

`5.0.0` is not compatible with the envelope of the earlier `triage`, `fix` and `impl`
tools (up to `4.0.0`). [ADR 01](adr/01-keep-one-envelope-and-one-shell-for-every-command.md#consequences)
lists the differences.
