# ADR 09: Carry out an attempt in one delivery of the worker

- **Status:** Accepted
- **Date:** 2026-10-25 (records decisions taken while specs 13 and 14 were implemented)

## Context

The worker is the jobs layer's production runner: it carries out one attempt
of a job against a hub workspace, from resolving the workspace to reporting to
hub ([architecture](../architecture.md#the-worker)). Its spec drove the loops
through a command line, passed the token to every git command, and assumed that
git reports the status of a refused clone and that a lease's time is a whole
number. A later spec added a journal so that a redelivered attempt could carry
on where the last one stopped; it was removed, and every delivery starts
afresh ([architecture](../architecture.md#redelivery-and-reconciliation)). The
rules below are what the worker does where the specs were silent or wrong.

## Decision

**The loop is a Go call.** The worker builds no argv and parses no stdout: it
runs the loop through `toolio.App.Invoke` with the settings as
`afcmd.LoopOptions` ([ADR 01](01-keep-one-envelope-and-one-shell-for-every-command.md)).
The loops, brains and settings get an environment lookup that finds nothing, so
the service's environment changes no attempt. The process's environment is
read in three other places only: git is given `PATH`, the locale, the proxies
and the certificates; the machine's temporary directory holds git's home; and
`aflib` reads `GITHUB_API_URL` and `GITLAB_API_URL` to recognise a forge host
([ADR 06](06-follow-hubs-api-where-the-specs-assumed-it.md)).

**Git and the token.**

- Every git command of the attempt runs hermetically, and its environment is
  built from the short list git needs, not from the whole environment less the
  names that look like secrets.
- Only `clone`, `fetch`, `pull`, `push` and `ls-remote` are given the token, as
  an `extraHeader` through `GIT_CONFIG_*`. A commit runs the repository's hooks
  and needs no token.
- `git clone` does not tell its caller the status of the answer, so a refused
  credential is recognised by what git prints: `returned error: 401` or `403`,
  `Authentication failed`, `could not read Username` or `Password`, and
  `terminal prompts disabled`. Any other clone failure is transient.
- A branch that is pushed to hub's git server and that hub did not publish to
  the forge ends the landing `failed`/`git`, and `next` is `git show --stat
  <branch>`, since a second push would do nothing. A parked change in that case
  stays parked with the warning `push_failed`.

**The lease.** The loop's timeout is the lease's time left less two minutes,
cut to whole seconds; a lease whose cut is under a second is refused as too
short, because a timeout of 0 means none. The lease warnings are added to the
envelope after the loop wrote it.

**What the result says.**

- The labels of the result are the net of the attempt: a label the mark set and
  the mirror took off again is reported removed, not set. A label that is gone
  when it is removed (404) counts as removed; a label that cannot be set removes
  none of the others.
- Every write of the mirror to the forge is a side effect of the envelope
  (`add_labels`, `remove_label`, `add_comment`), `done` or `failed`.
- The events posted to hub are the attempt's: every line of the loop and every
  `step` of the worker in the order they happened, numbered from 1 for the
  attempt so that hub's id `<run id>-<number>` is unique. They go in batches of
  at most 500, and a batch hub does not take ends the posting (warning
  `hub_events_failed`), so what hub holds is a prefix.

**Reconciliation keeps what it cannot classify.** The pass releases a lease
only when its labels name a job of the database and an attempt, a number from 1,
that no runner of this process carries out. A lease with no labels, an unknown
job or an attempt label that is not such a number is kept. An attempt waiting
for a slot or a backoff is in no runner, so a lease it still holds is an
orphan. A `404` on the release counts as released.

## Consequences

Known limits, left as they are:

- **A panic in a stage ends the attempt with the closers only.** `Runner.Run`
  has no `recover`: the release of the lease and the removal of git's home run,
  and the jobs layer fails the attempt, but the mirror, the report and the
  removal of `clone/` and `input/` do not. The issue keeps `in-progress` and hub
  keeps an open session until the attempt runs again. Running the end of the
  attempt from the unwinding needs a decision on what a half-built outcome
  means.
- **The marker of a comment is a string anyone can write.** The worker skips a
  comment when any comment of the issue carries its `<!-- agent-fox job=…
  attempt=… kind=… -->` marker, whoever wrote it, since it does not know the
  login behind its token. A person who writes the marker, with a job id read
  from the API, suppresses that attempt's comment.
- **A redelivery redoes work.** A fix branch pushed before a crash and before
  its pull request stays on hub's git server, and the next delivery lands the
  change under another branch name; a triage of text that created its issue
  creates another; a brain that was running is paid for again.
