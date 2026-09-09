---
name: agent-fox
description: Drive the agent-fox tools — spec, issue and fix — to turn an idea into a specification, a problem report into a filed GitHub issue, or an issue into a verified change on a branch.
argument-hint: "[text | file path | github issue URL]"
---

# agent-fox tools

Three programs. Each takes **one input** and writes **one JSON object** to
stdout.

| Tool | Give it | You get |
|---|---|---|
| `spec` | a product idea | a validated specification package under `.specs/NN_name/` |
| `issue` | a problem report | a structured, evidence-cited GitHub issue |
| `fix` | a problem, usually an issue URL | a verified change on a branch, and a pull request |

The input is exactly one of: a GitHub issue or pull-request URL, a path to a
readable file, `-` for stdin, or any other text. **There is no flag that
selects the kind** — the argument's shape decides. Do not try to tell the tool
what kind of input it is; it already knows.

## Driving them

```bash
issue "panic: assignment to entry in nil map in loop.go, after an abort"
issue ./crash.log --dir ./service --label af:fix
fix   https://github.com/acme/widgets/issues/42 --dir ~/src/widgets
spec  ./docs/prds/widget-cache.md --architecture
```

Five rules:

1. **Read stdout as one JSON document.** Not a stream, not a mix. Progress is
   on stderr and you can ignore it.
2. **Read `ok` first.** It is true only when the tool did the whole job. When
   it is false, `error.stage` says where it stopped and `error.category` says
   whether re-running could help.
3. **Exit codes are shared.** `0` done · `1` failed · `2` usage error, nothing
   was fetched or written · `3` a person has to answer something · `4` work
   exists and the checks do not pass.
4. **Quote a multi-word input.** Flags may come before or after it.
5. **`--dry-run` on every tool** makes it produce its result and write nothing
   remote. Use it when you are unsure.

## What each one needs

| | `--dir` must be | Credential |
|---|---|---|
| `spec` | any directory; the code is read | a model key |
| `issue` | any directory; the code is read | a model key, plus `GITHUB_TOKEN` unless `--dry-run` |
| `fix` | a **git repository with a clean working tree** | a model key, plus `GITHUB_TOKEN` unless `--dry-run` and `--land=none` |

A missing credential fails in the first second, before a token is spent. A
dirty working tree fails before anything is posted.

## Reading the results

**`issue`** — `result.action` is `created`, `updated` or `none`; `result.url`
is where it went. `result.rejected_path_calls` counts the times the tool
refused a diagnosis for citing a file that is not in the workspace. A nonzero
count is the check working; a large one is a reason to read `result.root_cause`
more carefully.

**`fix`** — `result.verdict` is the fact that matters:

| Verdict | Landed |
|---|---|
| `pass` | yes |
| `pass_was_already_failing` | yes — the repository was red before the run |
| `regressed`, `still_failing` | no; the work is a `wip:` commit on `result.branch`, exit 4 |
| `unverified` | only with `--no-verify` |

`result.changed_files` comes from git, not from the model. Exit 3 means
`result.ambiguity` holds a question that was posted to the issue: answer it in
a comment and run again.

**`spec`** — `result.validation.valid` says whether the package satisfies the
format. When it is false the package is still on disk and every error names
the rule it broke (`C1`…`C11`). `result.open_questions` are the decisions the
tool made under uncertainty and would like checked; `result.recommended_split`
is non-empty when the input was more than one spec's worth of work, and the
package then covers the first scope only.

## What they will not do

- No tool gives a model a way to write to GitHub or to run `git commit`. Every
  such call is made by the program, before or after a run.
- `spec` has no subcommands. Lifecycle management (`activate`, `seal`,
  `archive`, `supersede`, `migrate`) is library API in `afspec`, not a CLI.
- `fix` never merges its own work. `--land` is `pr` (default), `branch` or
  `none`.
- Nothing is interactive. If a tool needs an answer it exits 3 and says so.

## Flags worth knowing

`--model ADVANCED` for a hard problem · `--max-turns` and `--budget` to raise a
ceiling · `--verbose` to see the tool calls on stderr · `--trust-project` to
let the repository's `AGENTS.md`, `CLAUDE.md` and `.specs/steering.md` into the
system prompt.

The full reference is [`docs/cli.md`](https://github.com/agent-fox-dev/agent-fox/blob/main/docs/cli.md).
