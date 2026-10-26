# ADR 05: Run triage and fix as one loop each

- **Status:** Accepted
- **Date:** 2026-10-25 (records decisions taken while specs 08, 09 and 10 were implemented)

## Context

The specs of `af triage` and `af fix` gave a person at their own checkout the
steps of each loop as commands of their own (`brief`, `submit`, `land`), gave
`af fix` attempts, a dry run, text input and a scope check, and kept a file of
refused submit calls. None of it was used by the service or by the loops
themselves, and each step command needed state on disk that a loop keeps in
memory. They were removed. The loops are in `internal/afcmd`
([triage](../architecture.md#triage-internals),
[fix](../architecture.md#fix-internals)); the rules that came out of building
them are here.

## Decision

**One way to triage, one way to fix.**

- `af triage` and `af fix` are each one loop: preflight, brief, brain, the
  host's check, and the landing. The brain submits through the kind's
  terminating tool (`file_diagnosis`, `submit_fix`). `af triage run` and
  `af fix run` are what a box executes, are not listed in help, and run only on
  a brief that `brain.Execute` placed in their run directory; `af fix run`
  applies nothing.
- A fix takes an issue, by its URL. A text, a file or stdin is a `usage`
  failure that says to triage it first, which creates the issue.
- A fix run is one series of rounds within `--budget`, `--max-turns` and
  `--timeout`; a round that ends without a report ends it. Further attempts are
  the jobs layer's. There is no dry run of a fix and no scope comparison; the
  report keeps its `files`, which the pull request body quotes.
- A refused `file_diagnosis` call is the brain's next turn, with the problems
  and files of the same name as hints; nothing is written to disk for it.

**The tools of a brief.** The triage allowlist sits with the triage kind,
because the kind's tool and the box's tools are one decision. The loop hands
the brain `Brief.Tools` without the terminating tool; the driver adds the
kind's own tool (`Kind.ToolName`), and a name the box does not have is refused.

**A refused credential is `auth`.** At the repository check and at a write, a
401 or a 403 that is not a rate limit is `failed`/`auth` with the forge's token
variable as the hint, as a missing token is: the person has to change a token,
not a repository. Not found is `forge` and not retryable; a rate limit or a
transport error is `forge` and retryable; any other 4xx, and a 5xx on a write,
is not retryable.

**What triage accepts and writes.**

- A string of nothing but white space is empty. A title is one line when it
  has no CR and no LF. A cited path with a trailing slash is cleaned; a file of
  the view that is a link out of the checkout, or a nested repository, is no
  file to cite. A refusal lists at most 50 problems and says how many were left
  out.
- The extraction of the original report takes the whole body as the original,
  with `original_report_unparsed`, when keeping only part of it would lose a
  person's words: a first line that looks like the marker and is not it, a
  missing `## Original report` heading, or text after the end marker other
  than the close of the details block. CRLF line endings are read as LF.
- `next` suggests `af fix` when the diagnosis is `confirmed` or `probable` and
  the result names an issue (written to, created, or that a dry run would have
  written to); `--land none` has none.

**What fix keeps between rounds.** `fix.json` holds, besides the input and
the base, what a later round cannot recompute: where the brief's prefix ends
(`prefix_bytes`) and its hash, the suite and programs the box's `guard.json`
is made of, the baseline's id, result and duration, and the brain and cost so
far, which the pull request's footer reports.

**A question puts the checkout back.** A question asked in a round after an
applied one finds the earlier change in the tree; `Finish` puts it back so the
run can be resumed, with `question_tree_kept` when it cannot.

**The skills are flat files.** Each skill is one file `skills/af-<name>` with
its frontmatter, embedded with `//go:embed af-*`; `make install-skills` copies
each to `~/.claude/skills/<name>/SKILL.md`.

## Consequences

- A person who wants to drive a step by hand runs the fact commands
  (`lang detect`, `gate run`, `revert-check`), not a step of the loop.
- Known limits, left as they are: `af triage "<text>" --repo o/r` in a
  checkout with no origin and no `GITHUB_API_URL` is refused by the shell as
  having no repository, since the shell cannot tell the forge from `--repo`
  alone.
