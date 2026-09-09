# Tool reference

Three tools, one interface.

```
spec  [flags] <input>     a product idea      → a validated specification package
issue [flags] <input>     a problem report    → a structured GitHub issue
fix   [flags] <input>     a problem           → a verified change on a branch
```

Each takes **exactly one positional input** and writes **exactly one JSON
object** to stdout, on every program-driven path. Progress goes to stderr, so
the two never interleave and a caller can pipe stdout straight into a parser.

Two paths are human-driven rather than program-driven, and print text instead:
`--version` prints the build identity and exits 0, and `-h`/`--help` or a bare
invocation with no positional argument prints the help text and the flag list
to stderr and writes nothing to stdout — a person asking what the tool does
gets an answer they can read, not a JSON object to parse. A bare invocation
exits 2, since nothing was fetched or written; `-h`/`--help` exits 0.

## The input

The single argument is classified in Go, before anything else happens. There is
no flag that selects the kind; the argument's shape decides.

| The argument is | Then it is | What is used |
|---|---|---|
| a GitHub issue or pull-request URL | `github` | the issue, its body, and its comments |
| a path to a readable regular file | `file` | the file's contents |
| `-` | `stdin` | everything piped in |
| anything else | `text` | the text itself |

Two consequences worth stating. A path that does **not** exist is text, not an
error — "the `widget/` package panics" is a plausible report. And a directory
is text too, for the same reason.

Input is bounded at 256 KB, cut at a line boundary, with the cut marked in the
text and reported as `input.truncated` in the envelope. An input that ends
mid-stack-trace with no marker reads to a model as a complete stack trace that
simply had no more frames.

Flags may come before or after the input:

```sh
issue --dry-run ./crash.log
issue ./crash.log --dry-run
kubectl logs deploy/api --since 1h | issue - --repo acme/widgets
```

## The output

```jsonc
{
  "tool": "fix",
  "version": "0.4.0",
  "ok": true,
  "exit_code": 0,
  "input":  { "kind": "github", "origin": "https://github.com/acme/widgets/issues/42", "bytes": 3184 },
  "model":  { "spec": "STANDARD", "id": "claude-sonnet-5", "vendor": "anthropic",
              "api": "anthropic-messages", "thinking": "high" },
  "usage":  { "input_tokens": 48211, "output_tokens": 3104, "cost_usd": 0.19, "turns": 23,
              "phases": [ { "name": "analyse", "turns": 9, "stop_reason": "tool_terminate", … } ] },
  "result": { /* tool-specific; see below */ },
  "warnings": [ "the summary comment could not be posted on acme/widgets#42: 403" ],
  "duration_ms": 214003,
  "started_at": "2026-09-09T13:20:30Z"
}
```

`ok` is the one field a caller has to read. It is true only when the tool did
the whole job it was asked to do, and it is never true alongside a non-zero
`exit_code` or an `error` object.

`error` is present exactly when `ok` is false:

```jsonc
"error": { "stage": "verify", "category": "unverified",
           "message": "`make check` did not pass after the change (regressed); the work is on fix/issue-42-… and was not landed" }
```

`stage` names the pipeline step in the tool's own vocabulary. `category` says
whether re-running could help:

| Category | Means |
|---|---|
| `usage` | the invocation was wrong; nothing was fetched or written |
| `input` | the input could not be read (a private issue, an unreadable file) |
| `auth` | no credential for the model's vendor, or for GitHub |
| `model` | the model spec could not be resolved |
| `api` | a provider or transport failure |
| `budget`, `max_turns` | a phase hit its ceiling; raising it may help |
| `no_result` | the model finished without calling its terminating tool |
| `git`, `github` | an external system refused |
| `ambiguous` | (`fix`) the input reads two ways; a question was posted |
| `unverified` | (`fix`) code was written and the checks do not pass |
| `empty_change` | (`fix`) a fix was reported and no file differs |
| `invalid_spec` | (`spec`) the package was written and does not validate |
| `internal` | a bug in the tool |

### Exit codes

| Code | Meaning |
|---|---|
| `0` | done |
| `1` | failed; the stage is named in the JSON |
| `2` | usage error — nothing was fetched, nothing was written |
| `3` | stopped on purpose: a person has to answer something (`fix` only) |
| `4` | work exists but the checks do not pass (`fix` only) |

## Shared flags

| Flag | Default | Effect |
|---|---|---|
| `--dir` | `.` | the repository to work in; the file tools cannot reach outside it |
| `--model` | `$AF_MODEL`, else `STANDARD` | a tier (`SIMPLE`, `STANDARD`, `ADVANCED`) or any catalog spec |
| `--vendor` | `$AF_MODEL_VENDOR`, else `anthropic` | which tier table the tier names resolve against |
| `--variant` | — | tier variant, e.g. `extended` for the long-context row |
| `--max-turns` | per tool | per-phase turn ceiling, which is also the repair budget |
| `--budget` | per tool | per-phase spend ceiling, in dollars |
| `--phase-timeout` | — | wall-clock ceiling on one phase |
| `--trust-project` | off | admit `AGENTS.md`, `CLAUDE.md` and `.specs/steering.md` into the system prompt |
| `--verbose` | off | trace tool calls and timings on stderr |
| `--quiet` | off | print nothing on stderr |
| `--show-text` | off | stream the model's own prose to stderr |
| `--version` | — | print the build identity and exit |

---

## `issue`

Reads a problem report, traces it through the codebase, and files a structured
GitHub issue with every claim cited to a file it actually read.

```sh
issue "panic: assignment to entry in nil map in loop.go, after an abort"
issue ./crash.log --dir ./service --label af:fix
issue https://github.com/acme/widgets/issues/42 --overwrite
issue ./crash.log --dry-run
```

The analysis is read-only, and that is a mechanism rather than a promise: the
mutating tools are excluded from the resolved set, there is no shell and no
network tool, and the issue is created by a `net/http` call after the run.
**No sequence of model outputs can cause this tool to write to GitHub.**

Every path in `affected_files` is resolved against the workspace before the
diagnosis is accepted; one that is not there comes back to the model as an
error naming the missing path, and the run continues. The count of refusals is
reported as `result.rejected_path_calls` — a nonzero count is the check
working, and a large one means the model was writing from the report rather
than from the code.

| Flag | Default | Effect |
|---|---|---|
| `--repo owner/repo` | the input issue's, else the `origin` remote of `--dir` | where the issue is filed |
| `--label a,b` | — | labels for the created issue, e.g. `af:fix` |
| `--dry-run` | off | make no change on GitHub; report the diagnosis only |
| `--overwrite` | off | rewrite the input issue in place instead of creating a new one; needs an issue URL, and cannot be combined with `--repo` or `--label` |

Bounds: 100 turns, $2.00 per phase.

`result` carries `action` (`created` · `updated` · `none`), `url`, `number`,
the rendered `body`, and the diagnosis as fields: `severity`, `confidence`,
`root_cause`, `affected_files`, `suggested_fix`, `acceptance_criteria`.

---

## `fix`

Diagnoses a problem, writes the change on a branch, verifies it with the
project's own checks, and lands it.

```sh
fix https://github.com/acme/widgets/issues/42 --dir ~/src/widgets
fix ./bug-report.md --land branch
fix "the counter double-counts on retry" --dry-run
```

The working tree must be clean. The run branches from the branch checked out
now, and every check that can refuse the run happens **before** the model is
called and before anything is posted.

Verification is measured, not asserted. The project's checks run once before
any change and once after, and the two are compared:

| Verdict | Means | Landed |
|---|---|---|
| `pass` | green before, green after | yes |
| `pass_was_already_failing` | red before, green after | yes, and the report says so |
| `regressed` | green before, red after | no |
| `still_failing` | red before, red after | no |
| `unverified` | nothing ran | only with `--no-verify` |

A run that does not land parks the work as a `wip:` commit on its branch,
returns the checkout to the base branch, and exits 4. A run that reports a fix
and changed no file exits 1 rather than committing an empty tree — the diff
comes from git, not from the model.

| Flag | Default | Effect |
|---|---|---|
| `--land` | `pr` | `pr` · `branch` (push only) · `none` (commit only) |
| `--repo owner/repo` | the input issue's, else the `origin` remote | where the pull request is opened |
| `--dry-run` | off | make no *remote* change: push nothing, open nothing, post nothing. The branch and the commit are still made locally |
| `--verify` | detected | the command that decides success |
| `--no-verify` | off | run nothing; the result is then reported as `unverified`, not as a pass |
| `--verify-timeout` | `10m` | timeout for one verification run |
| `--push-attempts` | `4` | push retries, with exponential backoff |
| `--allow a,b` | — | extra programs the implementation phase's shell may run |
| `--draft` | off | open the pull request as a draft |

Bounds: 150 turns, $5.00 per phase.

Detection order for `--verify`: `make check`, `make test`, `go test ./...`,
`npm test`, `pytest`, `cargo test`. When nothing can be detected the run says
so and reports `unverified` rather than inventing a command.

The verification command runs with the model vendors' keys and every `*_TOKEN`,
`*_SECRET`, `*_API_KEY` and `*_PASSWORD` variable stripped from its
environment: a test suite is repository code, and a repository being fixed on a
stranger's report is not something to hand an API key to.

`result` carries `stage`, `branch`, `base_branch`, `commit`, `changed_files`
(from git), `baseline`, `verification`, `verdict`, `pull_request_url`, the
`comments` posted, and the model's own `implementation` report kept separate
from the facts.

### What the model may and may not do

The implementation phase has the file tools and a shell. On top of AgentKit's
program allowlist:

- **git is read-only.** `status`, `log`, `diff`, `show`, `blame`, `rev-parse`,
  `ls-files`, `grep`, `cat-file`, `describe`, `branch --list`, `remote -v`,
  `config --get` and a few more. The branch, the commit and the push belong to
  the tool, so "committed as `abc123`" means one thing.
- **`gh` is refused outright**, so every write to the issue goes through the
  audited path.
- **`find -exec` and `-delete` are refused**, because they turn `find` into a
  write tool.
- Every simple command on a line is checked, not only the first: `ls; git push`
  is two commands, and `GIT_AUTHOR_NAME=x git push` does not hide the program.

A refusal is a blocked tool result, so the model adapts rather than dying, and
the count is reported per phase.

This is a classifier over shell syntax, not a sandbox. `go`, `make` and `uv`
can run arbitrary code from the repository. Anything genuinely untrusted
belongs in a container.

---

## `spec`

Turns a product idea into a complete, validated version 2 specification
package under `.specs/NN_name/`.

```sh
spec "a cache in front of the widget catalog, with a TTL"
spec ./docs/prds/widget-cache.md --architecture
spec https://github.com/acme/widgets/issues/42 --comment
spec ./idea.md --dry-run
```

The run is unattended, and the design follows from that. Nobody is waiting to
answer questions, so the PRD phase resolves every open question itself, records
each in a `## Design Decisions` section, and reports the ones it is least sure
of as `result.open_questions`. A caller that wants a human in the loop reads
that array; a caller that does not gets a finished spec.

Then three phases, in the order the format fixes:

1. `requirements.json` — EARS criteria and end-to-end execution paths
2. `test_spec.json` — one flat list of tests
3. `tasks.json` — one flat list of tasks

Each artifact is submitted through a tool whose schema is the format's own JSON
Schema, and whose handler runs every cross-file rule decidable at that point. A
violation comes back to the model naming the rule, and it corrects itself — the
repair loop is the loop, and the turn ceiling is the repair budget.

The order is the rule and the reason is concrete: a generator that cannot see a
test id cannot own it, which is how the previous format produced specs whose
edge-case tests belonged to no task at all.

| Flag | Default | Effect |
|---|---|---|
| `--specs-dir` | `<dir>/.specs`, or `$AF_SPEC_DIR` | where `NN_name` packages live |
| `--name` | the model's choice | override the spec name; must match `[a-z][a-z0-9_]*` |
| `--architecture` | off | also write the optional `architecture.md` |
| `--no-activate` | off | leave a valid package in `draft` instead of activating it |
| `--comment` | off | post the finished PRD back to the issue the input came from |
| `--dry-run` | off | write nothing to disk or GitHub; report the package that would be written |

Bounds: 60 turns, $5.00 per phase.

### The project audit

The skill this replaces asks a human to read `tasks.json` afterwards and check
that `test_commands` names the project's real runner, because a model planning
work in a repository it did not look at reaches for whichever ecosystem its
training favours. Here the project's language is detected from its manifest and
a plan naming another ecosystem's runner is **refused** before the file is
written, with the real commands in the message:

```
this project is go (detected from go.mod), but test_commands.all_tests is
"pytest -q", which is a python command.
Use the project's real commands: all_tests "go test ./... -count=1",
linter "go vet ./...". Check task.steps, task.touches and task.done_when for
the same mistake — steps must use go constructs, touches must name paths that
fit this project's layout, and a stub marker here is `panic("not implemented")`.
```

The check is narrow on purpose: `go test` in a Python project is unambiguously
wrong, while `./scripts/ci.sh` is legitimate and a stricter check would reject
it.

### The result

`result` carries `spec_dir`, `spec_id`, `spec_name`, `title`, `status`,
`source`, the `artifacts` written, the counts, and:

- `validation` — the format's verdict, with every error naming its rule
  (`C1`…`C11`, `json_schema`, `completeness`)
- `traceability` — derived, never stored: `criteria_covered`,
  `criteria_uncovered`, `paths_covered`, `paths_uncovered`, `tests_unowned`
- `open_questions` — the decisions made under uncertainty
- `recommended_split` — non-empty when the input was more than one spec's worth
  of work; the package then covers the first scope only

A package that does not validate is still written, and the run exits 1 with
`category: "invalid_spec"`. A spec you can read and fix is worth more than no
spec at all, and the errors name the rules that broke. It is not activated.

## Environment

| Variable | Purpose |
|---|---|
| `AF_MODEL` | model tier or catalog spec for every phase; `AGENTKIT_MODEL` is a fallback |
| `AF_MODEL_VENDOR` | which tier table `SIMPLE`/`STANDARD`/`ADVANCED` resolve against |
| `AF_SPEC_DIR` | the spec root (`spec` only); `--specs-dir` wins |
| `GITHUB_TOKEN`, `GH_TOKEN` | GitHub credential. Reading a public issue needs none; every write does |
| `GITHUB_API_URL` | a GitHub Enterprise host; its host is then also accepted for `origin` |
| vendor keys and base URLs | see [Configuration](configuration.md) |

## See also

- [Configuration](configuration.md) — credentials, model selection, bounds
- [Model Usage](model-usage.md) — what each phase sends, and how a failure is repaired
- [ADR 03](adr/03-rebuild-the-skills-as-tools.md) — why the tools are shaped this way
