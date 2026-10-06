# Track the work in forge issues

Status: **proposed**. A workflow across `spec`, `impl`, `fix` and the two
tools that exist as PRDs so far, [`issue`](08-add-the-issue-tool.md) and
[`review`](11-review-a-landed-change-before-it-merges.md), with one new
shared package and one optional field in the spec format. Those two PRDs
are amended by this one; the amendments are marked there.

## Intent

A spec's life today is recorded in three places that do not connect. The
package on disk says what was asked and, once `impl` has run, which tasks
are `done`, but only on the branch `impl` worked on. The envelope and the
report file say what one run did, on the machine that ran it. The forge has
whatever the tools happened to write: `spec --comment` posts the PRD on the
issue the input came from, when the input was an issue and the flag was
given; `impl` opens a pull request and files an issue titled `Spec <id>:
<key> is not met` for each declared deviation; `fix` comments on the issue
it worked on. None of those says "spec 15 exists, came from PRD 06, is being
implemented on `impl/15-symbol-navigation-tools-every-phase`, three of five
tasks have landed, the pull request is #174 and it is a draft". A person who
wants that answer finds the branch, reads `tasks.json`, finds the pull
request and reads the body a program wrote. A skill or a scheduler that
wants to pick the work up has to do the same, and gets no notification when
it changes.

The forges already have the object for this. An issue is the unit of work on
GitHub, GitLab and every forge `issuex` may grow to speak: it has a checklist
the forge renders as a progress bar, a thread, cross-references from
commits and pull requests, a `Closes` convention that finishes it on merge,
labels and boards built on top, and notifications for free. The tools write
issues already; they do not write *the* issue.

This PRD gives every spec package one **tracking issue**, created where the
spec is created and recording where it came from, and makes the tools that
touch the spec afterwards write their progress to it: `impl` as each task
lands and when the run ends, `review` when it decides and when it merges,
`fix` when it corrects a gap the spec's issues track. The issue a `fix` works
on is already the tracking issue of that fix; the PRD names what `fix` writes
there, lets a run be told to write nothing, and links the correction back
to the spec it belongs to. Every write is rendered by Go from facts the tool
holds, in the shape the tools already use for pull-request bodies and
comments, and no model phase ever sees the tracking issue.

## Goals

- Every spec package `spec` writes and every package `impl` implements has
  one open issue on the target repository that says where the spec came
  from, which tasks are done, which branch and pull request carry the work,
  and what is still owed, kept current by the tools and never by a person.
- The link from the package to its issue lives in the package, so a tool that
  has the spec has the issue without a forge search, on any checkout and any
  branch.
- A run's progress on the issue is derived from `tasks.json`, git, the
  checks and the forge's own answers; the issue carries no sentence a model
  wrote about its own work. The model never reads the tracking issue.
- The workflow is one vocabulary across the tools: one body shape, one set
  of comment kinds, one marker a program can find, one flag (`--track`) and
  one variable (`AF_TRACK`) with the same meaning everywhere.
- A failure to track never fails a run whose work is done: the package, the
  branch and the pull request are the deliverable, and a lost comment is a
  warning, as it is today.
- Everything is forge-neutral: the writes are `issuex.Client` calls that
  exist (`CreateIssue`, `UpdateIssue`, `AddComment`, `ReadIssue`), on GitHub
  and GitLab alike, and a forge `issuex` gains later is covered without a
  change here.
- The envelope change is additive (`result.tracking`, a `role` on `issue`
  artifacts, a `kind` on issue writes, new comment kinds and warning codes);
  no field is removed, renamed or retyped.

## Non-goals

- **A model phase.** Nothing here needs judgment. The tracking issue is a
  program's record of program-measured facts.
- **Parsing prose.** A tool reads from the forge only what a tool wrote, in
  fixed shapes it matches exactly (§8), and uses it to *find* things, never
  to decide.
- **Labels, milestones, assignees, boards.** A label state machine needs
  labels that exist and a permission to create them, and every project names
  its own. The checklist and the comments carry the state; a skill that wants
  labels adds them with `issue --op label` (PRD 08).
- **Replacing the envelope or the report file.** The issue is a view for
  people and for callers that only have the forge. The envelope stays the
  machine interface.
- **Changing `triage`.** The issue `triage` files is a problem report for a
  person; it is the tracking issue of the `fix` that takes it, as it is
  today, and gains no marker.
- **Creating an issue for a `fix` on text or a file.** `triage` is the tool
  that turns a report into an issue, and `next[]` chains it to `fix`. A `fix`
  on text leaves no trail on any issue, as today.
- **Hub tasks.** [The hub draft](../drafts/add_tasks_to_the_hub.md) gives the
  hub its own work queue. The tracking issue is where the project already
  is; the hub reads the same issues and the same envelopes.
- **Closing issues from a tool, beyond the two rules that exist.** A
  pull request's `Closes` line lets the forge close the issue on merge, as
  `fix` does today; `review` closes an issue after a merge under PRD 11 §8.
  No tool closes an issue because its own run went well.
- **Updating a comment.** `issuex` has no comment update, and this PRD adds
  no forge capability. Progress that changes lives in the issue body, which
  `UpdateIssue` rewrites; events that happened are comments.

## Background: what the tools write today

| Tool | Writes to the forge | When |
|---|---|---|
| `spec` | one comment (`kind: prd`) with the finished PRD on the input issue | `--comment`, input is an issue |
| `impl` | a pull request; one issue per declared deviation with no erratum, `Spec <id>: <key> is not met` | `--land pr` |
| `fix` | a pull request whose body says `Closes #N` or `Refs #N`; comments `analysis`, `summary` or `failure`, `clarification` on the input issue | the input is an issue |
| `triage` | an issue, or a rewrite of the input issue (`--overwrite`) | always |
| `review` (PRD 11) | a verdict comment on the pull request and one on the issue; one issue per gap; a merge; a close | by verdict |

Three facts shape the design.

**The package already records where it came from, and nothing else about
its life.** `prd.md`'s frontmatter has `source`: the file path or issue URL
the input was, or `interactive`. Every package under `.specs/` has one. The
frontmatter schema (`afspec/schemas/prd-frontmatter.v2.json`) is closed
(`additionalProperties: false`), is owned by the
[`spec`](https://github.com/agent-fox-dev/spec) repository and is copied here
under the [schema workflow](../development.md#schema-workflow). A new field
is a format change in that repository first.

**`impl` writes one file into the package, on purpose.** `codeimpl/state.go`
writes `tasks.json` alone, by temp-and-rename, and leaves the rest of the
package byte for byte as found, because `afspec.Save` rewrites every
artifact and refuses an active spec whose intent drifted. A second
program-written field has to respect the same rule: one field, one file, the
body untouched.

**The forge calls exist.** `issuex.Client` creates, reads, updates and
comments on issues on both forges, and `UpdateIssueRequest` carries a body.
`issuex` cannot update a comment, cannot read whether an issue body was
edited by a person, and is deliberately kept at REST (PRD 08).

## The workflow

One package, one issue, from `spec` to the merge:

```
spec ./docs/prds/06-….md
  └─ writes .specs/15_symbol_navigation_tools, validates it
  └─ creates  acme/widgets#88  "Spec 15: Symbol navigation tools in every phase"
       body: where it came from, the task checklist, the intent        (§2)
  └─ writes  tracking: "https://github.com/acme/widgets/issues/88"  into prd.md
  └─ next[]: impl .specs/15_symbol_navigation_tools

impl 15
  └─ reads tracking from prd.md (or --issue, or creates one and commits it) (§4.1)
  └─ comment  impl_start: branch, base, tasks to do, continued or fresh   (§4.2)
  └─ task 1 lands → body: - [x] 1. … — `a3f9c1e`                          (§4.3)
  └─ …
  └─ opens PR #174 whose body says  Closes #88  or  Refs #88               (§4.5)
  └─ files  Spec 15: TS-15-16 is not met  with  Tracking: #88              (§4.4)
  └─ comment  impl_end: landed 5 of 5, verdict, PR, unmet and their issues (§4.4)

review https://github.com/acme/widgets/pull/174 --merge
  └─ reads tracking from prd.md on the head                               (§6)
  └─ comment  verdict_tracking on #88: merge / correct / needs_human, why
  └─ files gaps with  Tracking: #88 ; merges; #88 is closed by the forge,
     or review says on #88 what is still owed

fix https://github.com/acme/widgets/issues/172     (a gap review filed)
  └─ comments on #172 as today (analysis, summary)
  └─ reads  Tracking: #88  from #172's body                               (§5)
  └─ comment  correction on #88: fixed 172 (scope), PR #180, closes
```

Each arrow is a program-written write from facts the tool already has, and
each is recorded in the envelope as an artifact and a side effect, under the
one flag that governs them all:

| `--track` | Means |
|---|---|
| `auto` (default; `$AF_TRACK`) | write when the run can: the forge is authenticated, the target repository is known, the run is not `--dry-run`, and (for `impl`, `fix`, `review`) `--land` is not `none`. Otherwise write nothing, say so in `result.tracking.reason`, and warn only when a write was attempted and failed |
| `on` | the same writes, and a run that *could not* write refuses at preflight, before any model call: `auth` with the token variables in `fix_hint`, or `usage` naming `--repo`. `--track on` with `--land none` is a usage error, because `--land none` means nothing leaves the machine |
| `off` | no tracking write at all. For `fix` that includes the comments on its input issue (§5); the pull request is still opened |

## Functional requirements

### 1. The link: `tracking` in the frontmatter

- `prd.md`'s frontmatter gains an optional `tracking` field: the absolute
  `http(s)` URL of the issue that tracks the package. The field is added to
  `prd-frontmatter.v2.json` in the `spec` repository as an optional string
  (format version stays 2; the change is additive), synced into
  `afspec/schemas/`, and `make json-gen` regenerates the types.
- `afspec.Spec` gains `Tracking`; `LoadSpec`, `Save` and `Transition`
  round-trip it; `Validate` reports a value that is not an absolute `http` or
  `https` URL as a schema error. `SpecMeta` carries it so `DiscoverSpecs`
  callers can list which packages are tracked.
- `afspec` gains one function that sets the field in place:
  `SetTracking(dir, url string) error` rewrites `prd.md`'s frontmatter block
  from its parsed values with `tracking` set and leaves the body's bytes
  untouched, by the same temp-and-rename `codeimpl` uses for `tasks.json`.
  It is the only way a tool other than `spec` writes to `prd.md`.
- Every tool resolves the tracking issue in one order, implemented once
  (§8): a `--issue` flag when the tool has one; else the package's
  `tracking`; else, for `impl` alone, create one (§4.1). A `fix` has no
  package and resolves through its input issue (§5).
- A `tracking` URL whose host is not the forge the run's client talks to is
  reported and not written to: `tracking_unreachable`, low, with the URL and
  the host the client has, and `result.tracking.state: "unreachable"`. Issues
  in another repository on the same host are written to as any
  `issuex.IssueRef` is.

### 2. The tracking issue

**Title:** `Spec <id>: <title>`, the spec's own title. It is the shape every
tool matches to recognise a tracker by its title alone, and it does not
collide with `impl`'s `Spec <id>: <key> is not met` or `review`'s
`Spec <id>: <key> — <what>` (PRD 11 §7), whose second word after the colon is
an id or `scope`.

**Body:** rendered by Go, with a region the program owns between two
markers. Text outside the markers is a person's, and every rewrite preserves
it byte for byte:

```markdown
<!-- af:tracking spec=15 dir=.specs/15_symbol_navigation_tools -->
## Spec 15: Symbol navigation tools in every phase of every tool

- **Package:** `.specs/15_symbol_navigation_tools` (active)
- **Created from:** `docs/prds/06-stop-re-reading-the-codebase-every-phase.md` at `2dae58d`
- **Created by:** `spec` on 2026-10-02
- **Size:** 10 requirements, 23 criteria, 4 execution paths, 37 tests, 5 tasks
- **Branch:** `impl/15-symbol-navigation-tools-every-phase` at `6cc0751`
- **Pull request:** https://github.com/acme/widgets/pull/174

### Tasks

- [x] 1. Add file_outline and find_symbol to the read-only tool set — `b4cd1bc`
- [x] 2. Report the symbol backend in every --preflight — `bfcd2d3`
- [ ] 3. Document the six read tools *(in progress)*
- [ ] 4. Regenerate the navigation baseline tables
- [ ] 5. Smoke-test the symbol tools across fix, preflight and triage

### Decisions worth checking

- **Should ctags be required?** — No: a pure-Go outline is the fallback (the tool must run with no external program)

### Intent

Every phase of every agent-fox tool has four read-only file tools …

<!-- /af:tracking -->

---
*Written by `spec`. It is not a substitute for review.*
```

- The opening marker carries the spec id and the package directory; both are
  facts, and they are what a reader of the body (§8) uses to recognise it.
- **Created from** is the package's `source`: the path, as committed, with the
  commit the repository was at when `spec` ran (`git rev-parse --short HEAD`
  of `--dir`, when it is a repository); the input issue's URL; or `text given
  on the command line` for `interactive`. When the input was an issue, the
  tracking issue is a new issue and this line is the link back (design
  decision 1).
- **Created by** names the tool (`spec`, or `impl` under §4.1) and the date,
  from the program's clock.
- **Branch** and **Pull request** are absent until `impl` has them. **Merged**
  is added by `review` after a merge (§6).
- **Tasks** is one line per task in `tasks.json` order, rendered from the
  task's state in the file as it is on disk after the write: `- [ ]` for
  `pending`, `- [ ] … *(in progress)*` for `in_progress`, `- [x] … — <sha>`
  for `done`, with the short SHA of the commit that landed it when the run
  that renders it knows one (its own commits; a continued run renders earlier
  tasks without a SHA). The forges render the list as `n of m` progress.
- **Decisions worth checking** is `result.open_questions` as `spec --comment`
  renders them, present only when there are any.
- **Intent** is the PRD's `## Intent` section verbatim: it is the text the
  intent hash freezes, so it is what the issue should say the spec is for.
  The rest of the PRD stays in the repository; the issue links the package.
- The footer is outside the region and is the writing tool's own: `spec`'s
  `*Written by `spec`. It is not a substitute for review.*` on creation. A
  later tool rewriting the region leaves the footer as it is.
- **The region rule.** A tool that updates the body reads the current body
  (`ReadIssue`), finds the region by its markers, replaces exactly that
  span, and sends the result with `UpdateIssue`. When the markers are
  missing or unpaired the body is not touched: `tracking_body_unmanaged`,
  low, once per run, and the run's progress is carried by its comments
  (§4.4) instead. A person who deletes the markers has taken the body over,
  and the program respects that.

### 3. `spec` creates it

- After the package is written and validates, and before it is activated:
  `CreateIssue` on the target repository with the title and body of §2, then
  `SetTracking` on the package, then activation. Activation rewrites
  `prd.md` and must preserve the field, which `Transition` does under §1.
- The **target repository** is `--repo` (a new `spec` flag, `owner/repo` or
  `group/subgroup/project`, as the three other tools take it); else the
  input issue's repository when the input was an issue; else the `origin`
  remote of `--dir`. The client is the one the shell already builds for the
  run.
- **One issue per package.** A split run (ADR 05) creates one issue per
  package it writes, in split order, and each body gains a section
  `### Part of a split` naming the split's scopes, with links to the
  siblings' issues that exist when it is written. A package that does not
  validate gets no issue: it is not a plan yet, the run exits 1 and says so,
  and the next `spec` run on the same input does not write it again.
- **Never the input issue.** When the input was an issue, the new tracking
  issue's `Created from` links it, and the `--comment` PRD comment on the
  input issue gains one program-written closing line, `Tracked in <url>.`
  A body the tools rewrite is a body the tools wrote (design decision 1).
- Under `--track auto` with no authenticated client or no target, nothing is
  created and `result.tracking` is `{state: "skipped", reason}`; no warning,
  because `spec ./idea.md` in a directory with no remote is an ordinary run.
  Under `--track on` the same condition is a preflight refusal, with the same
  message `--comment` uses for a missing credential. A `CreateIssue` that
  fails after the package is written is `tracking_not_created`, **high**
  under `on` and **low** under `auto`, and the run's exit code is the
  package's, not the issue's.
- `--dry-run` creates nothing and writes no frontmatter, and reports what it
  would have sent as `result.tracking.would_write` (title and body), with an
  `issue` artifact marked `dry_run`.
- `result.tracking` on each `Package`: `{url, number, repo, state, reason,
  would_write}` with `state` one of `created`, `skipped`, `failed`,
  `dry_run`. `url`, `number` and `repo` are `fact`. The summary view keeps
  `url` and `state`.
- Artifacts and side effects: `{kind: issue, url, number, role: tracking}`
  and `{action: create_issue, kind: tracking, target: <repo>, url, ok}`; a
  `forge` timing `create_issue:tracking`; a `step` event `track`.
- `next[]` is unchanged: `impl` on the package. `impl` finds the issue in the
  package.

### 4. `impl` updates it

#### 4.1 Resolving, or creating, at preflight

- `--issue <url | owner/repo#N | N>` names the tracking issue outright, for
  a package whose frontmatter has none or names the wrong one; `N` is in the
  target repository. It is checked for shape in `PreCheck` and written into
  the frontmatter (§1) so the next run needs no flag.
- Else the package's `tracking`, read from the package **on the branch the
  run checked out**, since the branch is chosen before the package is read
  (`docs/cli.md`, "The branch").
- Else, when the run writes (the `--track` table): `impl` creates the issue
  with the §2 body, `Created by: impl`, and commits the frontmatter edit on
  its own, before any task, as `chore: track spec <id> in <owner>/<repo>#<N>`.
  This is how every package written before this PRD, or by a `spec` run with
  no forge, gets a tracker; it is a second file `impl` writes into the
  package, under the same one-field rule as `tasks.json` (Background). The
  commit is a `commit` artifact like any task's.
- Else (`--track off`, `--land none`, `--dry-run`, or no forge under `auto`):
  `result.tracking.state: "skipped"` with the reason, and nothing below
  happens. A `--dry-run` reports the start and end comments and the body it
  would have sent under `would_write`.
- A tracking issue that `ReadIssue` reports closed is written to anyway: a
  run on a closed spec is a person's decision, and the comment is where they
  see it. One `tracking_closed` low warning says so.

#### 4.2 The start comment

Posted once preflight has passed and before the first model phase, as a
comment of `kind: impl_start`:

```markdown
## `impl` started

- **Branch:** `impl/15-symbol-navigation-tools-every-phase` from `main` at `2dae58d` — continued (tasks 1–2 already done; task 3 was in progress and starts again)
- **Tasks this run:** 3, 4, 5 of 5
- **Checks:** `make lint`, `make test`
- **Model:** `claude-sonnet-5-5` (anthropic) · **Land:** pr

---
*Written by `impl`. Trust, but verify!*
```

Every value is a fact from preflight: `result.branch`, `result.base_branch`,
`result.resumed`, the task ids chosen, `st.gate`, the resolved model,
`--land`. The model is named because a person deciding whether to trust the
run wants to know what ran it; no cost is posted, because spend is the
operator's business and is in the report file.

#### 4.3 Progress: the checklist

After every commit the run makes that changes a task's state — a task
landing, a parked `wip:` commit, the conformance stage's resolve commit —
the body region is re-rendered from `tasks.json` as written and the commit
from git, and sent with `UpdateIssue` under the region rule (§2). One write
per state change; a continued run therefore starts by rendering the state
it inherited. **Branch** and, once opened, **Pull request** are kept
current in the same writes.

A body update that fails is `tracking_not_updated`, low, once per run; the
end comment then carries the task table (§4.4) so the issue is still right.

#### 4.4 The end comment and the deviation issues

A run that posted a start comment posts an end comment, whatever happened
after, as `kind: impl_end`:

```markdown
## `impl` finished: landed 5 of 5 tasks

- **Outcome:** landed · verification `pass` in a clean environment (`HOME` empty, no git config)
- **Pull request:** https://github.com/acme/widgets/pull/174 (draft: blocking findings)
- **Unmet:** 15-REQ-8 (partial) → #173 · TS-15-16 (weaker) → #176 · scope: 3 files → #172
- **Blocking:** none

---
*Written by `impl`. Trust, but verify!*
```

- **Outcome** is one of `landed`, `parked at task <n>` (with the `wip:`
  commit and the `next[]` command to resume), `not ready` (`nonconformant`,
  with the count of blocking findings and the draft pull request), or
  `failed at <stage>` (`<category>`: the one-line message), from the
  envelope's own `status`, `stage` and `error`.
- **Unmet** and **Blocking** are `result.unmet` and `result.blocking` with
  their tracking column: the erratum path or the issue URL, as the
  pull-request body renders them. They are counts and links, not the review's
  prose; the pull request has the tables.
- When the body could not be updated (§4.3), the comment adds the task table
  the pull request carries.
- Every deviation issue `impl` files (`codeimpl/conformance.go`,
  `trackDeviations`) gains one program-written line after the requirement
  and test lines: `- **Tracking:** <url>`. The title is unchanged, so PRD 11
  §6's matching is unchanged. The line is what `fix` and `review` follow back
  (§5, §8).

#### 4.5 The pull request

The pull-request body's footer block gains one line, as `fix`'s has: `Closes
<url>` when every task of the package is `done` and the run is not
`nonconformant`; `Refs <url>` otherwise. The URL form (`Closes
https://github.com/acme/widgets/issues/88`) is what both forges accept for an
issue in any repository on the host. The forge closes the tracker when the
pull request merges; a merge `review` performs needs nothing more (§6). The
spec's unmet items keep their own issues, which link back, so a closed
tracker with open gaps is honest: the unit of work landed, and what is owed
is tracked where `fix` can take it.

#### 4.6 Flags, result and side effects

- `--track auto|on|off` (default `$AF_TRACK`, else `auto`) and `--issue`.
- `result.tracking`: `{url, number, repo, state, reason, created, comments:
  [{kind, url}], body_updates, would_write}`; `state` adds `updated`,
  `unreachable` and `created` (when §4.1 created it). The summary view keeps
  `url`, `state` and `created`.
- Side effects: `create_issue` with `kind: tracking` when created;
  `update_issue` with `kind: checklist` per body write; `comment` with
  `kind: impl_start` or `impl_end`. Each body write is a `forge` timing
  `update_issue:checklist`; the comments are `comment:impl_start` and
  `comment:impl_end`. Artifacts: the `issue` with `role: tracking` when
  created; `comment` entries with their `role`.
- The deviation issues' artifacts gain `role: deviation`, so the three kinds
  of issue a run can produce are told apart without reading titles.

### 5. `fix` updates it

- The input issue is the tracking issue of a fix, as today. The four comments
  (`analysis`, `summary` or `failure`, `clarification`) are its tracking
  writes, and `--track` governs them: `auto` and `on` post them exactly as
  today, `off` posts none. That is a new ability for `fix` — until now only
  `--dry-run` could keep it quiet, and `--dry-run` also stops the push.
- **The parent tracker.** A gap issue filed by `impl` (§4.4) or `review` (PRD
  11 §7) carries `- **Tracking:** <url>`. When the input issue's body has
  that line (matched exactly, §8), `fix` posts one comment on the tracking
  issue when the run ends, `kind: correction`:

  ```markdown
  ## `fix` worked on #172 (Spec 15: scope)

  - **Outcome:** landed · verification `pass` · the change closes #172
  - **Branch:** `fix/issue-172-spec-touches` → `impl/15-symbol-navigation-tools-every-phase`
  - **Pull request:** https://github.com/acme/widgets/pull/180

  ---
  *Written by `fix`. Trust, but verify!*
  ```

  The key in the heading is the gap issue's own title after `Spec <id>:`,
  copied as a label and not interpreted. The outcome line is the envelope's
  verdict and `result.closes_issue`. The base branch is named because PRD 11
  design decision 6 stacks corrections on the `impl` branch while the pull
  request is open, and the tracker should say which branch the fix went to.
- When the input issue has no `Tracking:` line and the report cites ids
  (`20-REQ-1.2`, `TS-20-3`) that **exactly one** spec under the spec root
  defines, and that spec's frontmatter has `tracking`, the same comment goes
  there. Two specs cited, or none tracked, means no parent comment; the
  envelope says why in `result.tracking.reason`.
- A `fix` whose input issue *is* a tracking issue (its body carries the
  `af:tracking` marker) is a usage error, exit 2, before any model call:
  `<url> is the tracking issue of spec <id>; run impl <dir> to implement it,
  or fix on one of the issues it tracks`. A spec is not a bug report, and the
  acceptance-criteria extraction would find nothing to hold the fix to.
- `fix` on text or a file writes to no issue, as today, and `result.tracking`
  is `{state: "skipped", reason: "the input is not an issue"}`.
- `result.tracking`: `{url, number, repo, state, reason, comments}` where
  `url` is the **parent** tracker when one was found and `comments` lists
  every comment the run posted, on the input issue and the parent, with its
  kind and URL (`result.comments` stays as it is).

### 6. `review` updates it (amends PRD 11)

PRD 11 is amended in place; this section is the summary.

- **Finding the tracker.** For an `impl` origin, `tracking` from `prd.md` on
  the **head commit**, beside the `tasks.json` it already reads. For a `fix`
  origin, the `- **Tracking:** <url>` line of the input issue's body. An
  issue input whose body carries the `af:tracking` marker is a tracker, and
  its region's **Pull request** line is the pull request under review (PRD 11
  §2.1 gains this second shape).
- **The verdict.** On every verdict but `already_merged`, one paragraph on the
  tracking issue, `kind: verdict_tracking`: the verdict, the pull request,
  the counts of blocking and unmet items and the issues filed or found, with
  the `review` footer. PRD 11's `verdict` and `verdict_issue` comments are
  unchanged.
- **The issues it files** carry `- **Tracking:** <url>` as `impl`'s do.
- **After a merge.** The body region gains `- **Merged:** <sha> (<method>)`
  and **Pull request** stays. When the pull request's body said `Closes
  <tracker>` the forge closes it and `review` does nothing more. When it said
  `Refs <tracker>` (the `impl` run was `nonconformant`, and the corrections
  since have made the verdict `merge`), `review` closes the tracker with a
  comment, `kind: closed`, naming the merge, by the same rule PRD 11 §8
  applies to a `fix` issue: nothing blocking remains, and every unmet item is
  tracked. `result.closes_issue` records it.
- `--track` is accepted with the shared meaning; `review` has no `--land`, so
  `auto` depends on the credential and the target alone.

### 7. `issue` reads it (amends PRD 08)

`issue` stays a primitive and gains no tracking operation. Two things change
in PRD 08, marked there:

- `issue --op read` adds `result.links` to the issue section: the
  program-written lines §8 defines, found in the body and the comments —
  `tracking` (the `Tracking:` line), `pull_request` (the `- Pull request:`
  line of a `fix` summary, or the region's line), `spec` (`{id, dir}` from
  the marker) — each labelled `external`, because the forge returned it,
  and each matched by the fixed grammar, never inferred. A skill that has
  only an issue URL learns in one read whether it is a tracker, a gap, or a
  `fix`'s issue, and where its pull request is.
- The comment and issue-write `kind` vocabulary is open, and PRD 08's
  `comment` and `create` operations record no `kind`: a skill writing to a
  tracker by hand is writing a person's note, and `issue` must not dress it
  as a tool's.

What `issue` is to the workflow is the escape hatch: `issue --op comment
<tracker> --body-file -` to add a note a tool would not, `issue --op close
<tracker> --body "superseded by spec 19"` to retire a spec, `issue --op
update <tracker> --title …` to rename one, `issue --op list --repo
acme/widgets` to see what is open. None of that needs a renderer, and none of
it is this PRD's to specify.

### 8. The shared mechanism: `internal/tracking`

One package, used by `spec`, `impl`, `fix` and `review`, and by `issue`'s
`read` for the grammar alone. Nothing in it calls a model or runs a program.

- **The grammar.** Three fixed shapes, matched exactly, used only to find:
  - the marker pair `<!-- af:tracking spec=<id> dir=<path> -->` …
    `<!-- /af:tracking -->`, one per body, `id` matching `[0-9]+` and `dir` a
    relative path with no spaces;
  - the line `- **Tracking:** <url>` in an issue body;
  - the line `- Pull request: <url>` in a comment or a region, as
    `codefix/render.go` writes it today.
  `Find(body) (Region, bool)`, `Replace(body string, r Region, inner string)
  string`, `TrackingLine(body) (string, bool)`, `PullRequestLine(text)
  (string, bool)`. A property test holds `Replace` to preserving every byte
  outside the region.
- **The renderers.** Pure functions from facts to markdown: `Body(BodyState)`,
  `Start(StartState)`, `End(EndState)`, `Correction(CorrectionState)`,
  `Verdict(VerdictState)`, `Closed(ClosedState)`. Their inputs are structs
  of `fact` fields the pipelines already hold; none takes a model struct.
  Each has a golden test, and the task-list renderer has one per task state.
- **Resolution.** `Resolve(flag string, spec *afspec.Spec, thread
  *issuex.IssueThread) (issuex.IssueRef, Source, error)` implements the §1
  order, `Source` one of `flag`, `frontmatter`, `issue_line`, `none`.
- **The tracker.** `type Tracker struct` built from the `issuex.Client`, the
  `*toolio.Run`, the `*toolio.Progress`, the resolved `Mode` and the ref.
  `Create(ctx, repo, title, body) (issuex.Issue, error)`, `Comment(ctx,
  kind, body)`, `UpdateBody(ctx, inner)`. Every method records its side
  effect (`action`, `kind`, `target`, `url`, `ok`, `warning`), its `forge`
  timing and its `step` event, degrades to the warning this PRD names, and
  returns an error only from `Create`, which the callers treat as §3 and
  §4.1 say. Under `--dry-run` the methods record `would_write` entries and
  send nothing. A `Tracker` whose mode is `off` or `skipped` is a no-op
  with a reason, so call sites have no `if`.
- **The mode.** `ParseMode(flag, env string) (Mode, error)` and
  `Decide(mode Mode, authenticated, targetKnown, dryRun bool, land string)
  (Decision)`, where `Decision` is `{Write bool, Reason string, Refuse
  *toolio.UsageError}` and implements the `--track` table. One function,
  four call sites.

### 9. Envelope, flags, events and versions

Per [ADR 06](../adr/06-version-the-envelope-interface.md), everything here is
additive, under the current major:

- `result.tracking` on `spec` (per package), `impl`, `fix` and `review`, as
  the sections above define; every string field carries a trust tag, and
  `CheckTrust` applies.
- `artifacts[]`: an `issue` entry may carry `role` (`tracking`, `deviation`,
  `review`). The field exists for comments; its description is widened to
  issues. The artifact kind set stays closed.
- `side_effects[]`: `kind` is documented for comments today; it is now also
  set on `create_issue` (`tracking`, `deviation`, `review`) and
  `update_issue` (`checklist`, `merged`). New comment kinds: `impl_start`,
  `impl_end`, `correction`, `verdict_tracking`, `closed`. The description
  says both sets are open.
- New warning codes, all at the stage they occur: `tracking_not_created`
  (high under `on`, low under `auto`), `tracking_not_updated` (low),
  `tracking_body_unmanaged` (low), `tracking_unreachable` (low),
  `tracking_closed` (low).
- Flags: `--track` on `spec`, `impl`, `fix` and `review`; `--issue` on
  `impl`; `--repo` on `spec`. `AF_TRACK` in the environment table. The four
  tools' `--schema` golden files change by exactly these flags and fields,
  and the spec written from this PRD records the diff as deliberate.
- Events: `step` events named `track` for each tracking write; `forge`
  timings as named above. No new event type.
- `schema_version`: the next minor, with an entry under
  [Interface versions](../cli.md#interface-versions). PRD 11 claims `3.2.0`
  for its own additions; whichever lands second takes the next number.

### 10. Tests

- **Renderers:** a golden file per renderer and per task state; the region
  property test; the three grammar matchers against bodies with the line
  present, absent, duplicated and hand-edited.
- **Both forges:** `httptest` servers for GitHub and GitLab, as `issuex`'s
  tests use, asserting the request each write sent and the body the region
  rule produced, for create, comment and update, including a body with text
  outside the markers that must come back unchanged.
- **Every execution path has a smoke test**, per `.specs/steering.md`, in
  each tool: `spec` creates; `spec` with an issue input creates a new issue
  and links it; `spec --dry-run` reports `would_write`; `spec --track off`;
  `spec --track auto` with no forge skips silently; `spec --track on` with
  no forge refuses at preflight; `impl` resolves from the frontmatter; from
  `--issue`; creates and commits when none; `--land none` skips; `--track on
  --land none` is a usage error; a body without markers warns and the end
  comment carries the table; a tracker on another host is `unreachable`; a
  parked run posts an end comment; `fix` with a `Tracking:` line posts the
  correction; with a cited spec; on text posts nothing; on a tracking issue
  refuses; `fix --track off` posts no comment and still opens the pull
  request. Each asserts that no request reached the server when none should
  have.
- **The frontmatter:** `afspec` round-trips `tracking` through `LoadSpec`,
  `Save` and `Transition`; `SetTracking` leaves the body bytes identical;
  `Validate` refuses a relative URL; a package without the field validates
  as before, and every package under `.specs/` still validates.
- **The model never sees it:** a test on each tool's prompts asserts the
  tracking issue's body and comments appear in no phase's input.
- **The pull request:** `impl`'s body says `Closes` when every task is done
  and the run is not `nonconformant`, `Refs` otherwise; the deviation issue
  body has the `Tracking:` line.
- **Docs:** the doc tests that walk `docs/cli.md`'s flag tables and warning
  codes cover the new entries.

## Design decisions

1. **A new issue, never the input issue.** When `spec` runs on an issue, the
   obvious move is to make that issue the tracker: one thread. But the
   tracker's body is rewritten by programs, and the input issue's body is a
   person's. A program that edits a person's text, even between markers it
   appended, is doing something the person did not ask for and cannot
   predict. So the tracker is always new, links the input issue in `Created
   from`, and the PRD comment on the input issue links the tracker. A person
   who wants one thread closes one as a duplicate; the tools never decide
   that for them.

2. **The link lives in the frontmatter.** `tasks.json` is `impl`'s state
   file; `source` is where the package came from; `tracking` is where its
   life is recorded, and it belongs beside `source`. It travels with the
   branch, so a continued `impl` run and a `review` of the head commit both
   find it without a forge search, and a package copied to another machine
   still knows its issue. The cost is a format change in another repository
   — one optional string, additive — which is why the field is the only
   format change this PRD makes.

3. **The program owns a region, not the body.** A checklist the forge
   renders as progress has to be in the body, and `issuex` cannot update a
   comment, so the body is rewritten. Markers make the rewrite safe: a
   person can write above or below them and keep it, and can delete them
   and keep the whole body. The alternatives — rewriting the whole body, or
   never rewriting and posting a comment per task — are either destructive
   or noisy.

4. **`auto` writes when it can, like the four tools do.** `impl` opens a
   pull request by default, `triage` files an issue by default, `fix`
   comments by default; a run that can reach a forge leaves its trail
   there, and `--dry-run` and `--land none` are how a run stays on the
   machine. `auto` follows that and is silent when there is no forge,
   because `spec ./idea.md` with no remote is an ordinary run and a warning
   on every such run would teach people to ignore warnings. `on` exists for
   the unattended chain, where a run that silently did not track is a
   failure a person finds a week later.

5. **Facts only, and the model never reads the tracker.** Every line on the
   issue comes from `tasks.json`, git, the checks, the resolved model and
   the forge's own responses, through renderers whose inputs are `fact`
   structs. That keeps ADR 03's rule — the parts you cannot afford to have
   wrong stop being prompt — on the forge too. And no phase is given the
   tracking issue: a tracker that said "task 3 landed" would otherwise be a
   claim a later phase could repeat.

6. **Tracking never fails a done run.** The package, the branch and the pull
   request are the work; a comment that did not post is what it has always
   been, a warning. The one exception is `--track on` at preflight, where
   refusing before a token is spent is the cheap, honest answer.

7. **The pull request closes the tracker, not the tool.** `fix` already
   leaves closing to the forge through `Closes #N`, and PRD 11 makes `review`
   the only tool that closes an issue in its own name, after a merge, on a
   rule. `impl` follows `fix`: `Closes` when the package is done and the
   run is not `nonconformant`, `Refs` otherwise, so a draft that `review`
   later merges leaves the tracker to `review`'s rule.

8. **`impl` may create the tracker.** Otherwise every package that exists
   today stays untracked until someone runs `issue --op create` and edits
   the frontmatter by hand. Creating it is one forge call and one
   frontmatter write `impl` commits on its own, under the same one-field
   discipline as `tasks.json`, before any task, so a run that parks before
   its first commit still leaves a clean tree and a tracker that says so.

9. **A shared package, four call sites.** The renderers, the grammar, the
   mode and the degrade-to-warning writer are one implementation. The
   alternative is four copies of the same `if o.Forge == nil || !Authenticated
   …` block and four body renderers that drift; `codefix` and `codeimpl`
   already have two of them for the pull request.

10. **No labels.** Every forge renders a checklist and threads comments;
    labels have to exist, be spelled the project's way, and be creatable by
    the token. A state label would be the first thing this workflow got
    wrong on a repository it had not seen. The `issue` tool is there for a
    skill that wants them.

## Success criteria

| Metric | Today | Target |
|---|---|---|
| A person can see the state of a spec on the forge | no: find the branch, read `tasks.json`, find the pull request | one issue per spec: origin, `n of m` tasks, branch, pull request, gaps, verdict |
| The link from package to issue | none | `tracking` in the frontmatter, written by `spec` or `impl`, read by `impl`, `review` and `fix` |
| Tracking writes derived from a model's sentence | — | zero: every renderer takes `fact` structs; a test holds the tracker out of every phase's input |
| A run whose tracking failed is reported as failed | n/a | never, except `--track on` at preflight; the warning names what was lost |
| A run that leaves no trail on request | only `--dry-run`, which also stops the push | `--track off`, on every tool, with the pull request still opened |
| A gap issue leads back to its spec | by title shape, if you know it | `- **Tracking:** <url>` on every gap `impl` and `review` file; `fix` follows it |

The spec written from this PRD holds the mechanism to the same smoke tests
the tools have: the shell's golden `--schema` files, the envelope on every
path, `--dry-run`'s `would_write`, `httptest` forges for GitHub and GitLab,
and real temporary repositories for the frontmatter commit.

## Changes outside the new package

| Where | Change |
|---|---|
| `spec` repository, `specification/schemas/prd-frontmatter.v2.json` | optional `tracking` string; format version unchanged. **First**, since `afspec` copies it |
| `afspec` | the synced schema and `make json-gen`; `Spec.Tracking`, `SpecMeta.Tracking`; round-trip in `LoadSpec`, `Save`, `Transition`; the URL check in `Validate`; `SetTracking` |
| `specgen` | the create step between validate and activate; `--repo`; `--track`; `Tracked in` on the PRD comment; `result.tracking`; the split section |
| `codeimpl` | resolve-or-create at preflight with its commit; `--issue`; `--track`; start and end comments; the body write after each state-changing commit; the `Tracking:` line on deviation issues; `Closes`/`Refs` on the pull request; `result.tracking`; `role: deviation` |
| `codefix` | `--track` over the existing comments; the parent tracker (issue line, then the one cited spec); the `correction` comment; the tracker-as-input refusal; `result.tracking` |
| `codereview` (PRD 11, when built) | the tracker from the head's `prd.md` or the issue line; `verdict_tracking`; the `Tracking:` line on filed issues; `Merged` and the `Refs` close |
| `issueops` (PRD 08, when built) | `result.links` on `read`, using `internal/tracking`'s grammar |
| `internal/toolio` | `role` on `issue` artifacts; `kind` on issue writes; the new comment kinds and warning codes; `--track` and `AF_TRACK` in the shared flag plumbing (the flag is per tool, the parsing shared); `schema_version` |
| `issuex` | nothing. Every call exists. Recommended later: `UpdateComment`, which would let a tracker's progress live in a comment on an existing issue and reopen design decision 1 |
| `docs/cli.md` | `--track`, `--issue`, `--repo` in the per-tool flag tables; `result.tracking` per tool and its summary view; the new comment kinds in the artifacts and side-effects section; the warning codes; the interface-versions entry; a "Tracking" section under each of `spec`, `impl` and `fix` |
| `docs/configuration.md` | `AF_TRACK`; a sentence under the forge credentials that tracking writes need a token like every other write |
| `docs/development.md` | `internal/tracking` in the layout and the package list |
| `README.md` | one paragraph under "The tools": each spec has an issue, and what writes to it |
| `afspec/README.md` | `tracking` in the frontmatter table and `SetTracking` in the API |
| `docs/adr/` | an ADR recording the decision that the tools track their work in forge issues, the program-owned region, and the never-the-input-issue rule; written with the implementation |

## Worked example: spec 15 and pull request #174

What the chain would have left on the forge, had this PRD been built when
spec 15 was written, alongside the pull request PRD 11 reviews:

1. `spec docs/prds/06-stop-re-reading-the-codebase-every-phase.md` writes
   `.specs/15_symbol_navigation_tools`, validates it, creates #88 `Spec 15:
   Symbol navigation tools in every phase of every tool` with `Created from`
   the PRD path at `2dae58d`, five unchecked tasks and the intent, writes
   `tracking` into `prd.md`, activates the package. `next[]`: `impl
   .specs/15_symbol_navigation_tools`.
2. `impl 15` reads #88 from the frontmatter, posts `impl started` (fresh
   branch from `main` at `2dae58d`, tasks 1–5, `make lint` and `make test`),
   and after each of `b4cd1bc`, `bfcd2d3`, `66e598b`, `8460c4b` and the
   fifth task's commit rewrites the region: `- [x] n. … — <sha>`.
3. The conformance stage declares 15-REQ-8.1 and TS-15-16 unmet and files
   #173 and the TS-15-16 issue with `- **Tracking:** …/issues/88`; the
   resolve commit `9fa4476` is one more body write. The pull request #174 is
   opened as a draft with `Refs https://github.com/acme/widgets/issues/88`
   (the run is `nonconformant`). `impl finished: landed 5 of 5 tasks` is
   posted with the outcome `not ready`, the draft, and the three unmet items
   with their issues.
4. `review …/pull/174 --merge` reads #88 from `prd.md` on `6cc0751`, posts
   its verdict paragraph on #88 (`needs_human`: a passing draft, PRD 11
   design decision 7), and after the click merges and writes `- **Merged:**
   <sha> (squash)` into the region. The pull request said `Refs`, so `review`
   closes #88 with the `closed` comment: nothing blocking, every unmet item
   tracked by #172, #173 and the TS-15-16 issue.
5. `fix …/issues/172` (the scope gap) comments on #172 as today, reads
   `Tracking:` from its body, and when it lands posts `fix worked on #172
   (Spec 15: scope)` on #88 — now closed, which is where the person who
   closed it still reads it.

A person opening #88 at any point sees where the spec came from, how far it
is, which pull request carries it, and what is owed; a skill reading it with
`issue --op read` gets the same as `links`.

## Open questions

- **Where the format change lands first.** `tracking` has to exist in the
  `spec` repository's schema before `afspec` can accept it, and packages
  written with the field do not validate against the old schema. The spec
  written from this PRD should name the `spec` repository change as its
  first task, or the format should be bumped there independently and this
  PRD's spec depend on it.
- **One tracker for a split.** Each package of a split gets its own issue and
  names its siblings as they exist when it is written; the first package's
  issue never learns the later ones. A parent issue for the split, or a
  back-fill of the earlier regions, would make the split visible as one
  thing. Left out until a split has been tracked once.
- **The region on GitLab.** GitLab renders `- [x]` task lists in issue
  descriptions and counts them as GitHub does; HTML comments are preserved
  in the description. Both should be confirmed against a live instance
  before the renderer is frozen.
- **`UpdateComment`.** If `issuex` gains it, the tracker's progress could
  live in a pinned comment on the input issue and design decision 1 could
  be revisited. Not proposed here; noted so the decision is traceable.
- **Cost on the issue.** The start comment names the model and no cost.
  A team that runs the chain unattended may want spend per run where the
  work is; a team on a shared forge may not want it public. `--track` could
  grow a `cost` sub-option, or the report file stays the only place.
