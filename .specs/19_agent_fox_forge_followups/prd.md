---
spec_id: "19"
spec_name: "agent_fox_forge_followups"
title: "agent-fox forge follow-ups: answer and retry from the forge, and AF_AUTO_FIX"
status: "active"
created_at: "2026-10-23T17:48:40.254014Z"
updated_at: "2026-10-24T04:07:51Z"
intent_hash: "8dbcec12b5f304ed6aa33ed562b5382525ece2c6a2dfa9e381ec1e9450e02dbd"
schema_version: 2
source: "docs/prd/04-drive-the-service-from-the-forge.md"
---
## Intent

Let a team carry an agent-fox job forward from the issue itself, without `af` or the API. A person answers a stopped job, or asks again after a failed one, by writing a comment and putting the request label back. The service takes the comments posted since its own question or failure comment as the answer, or as context for the new attempt, and says so on the issue when there are none. A workspace can also choose to have a triage that ends ready followed by a fix with no person involved. That fix job is linked to the triage on both job records and on the issue. The job record stays the truth, and the labels and comments only mirror it.

## Goals

The tests for these goals run offline. They use:
- the fake hub (`internal/hub/fakehub`), extended so that a test can add a person's comment and each comment carries its own creation time. A test makes hub cut a thread by injecting an issue read with `truncated: true` through the existing `Inject` seam;
- the real store and jobs layer on a temporary state file, with `jobs/fakerunner` and `jobs/fakeclock`;
- the label poll's `Supervisor.Cycle`, and the event stream of `agent_fox_event_stream`, which applies the same rules;
- workspace `widgets`, subscribed with prefix `af:`.

The worker goals use the worker's own harness with the fake hub.

1. **An answer from the forge.** A poll-made fix job for issue 42 is blocked on attempt 1, and its question comment (marker `kind=question`) is on the issue. A person comments "use the greeting from the README" and adds `af:fix` again. One poll cycle then does the following:
   - it queues attempt 2 with `trigger` `{"kind": "answer", "by": "label af:fix"}`, and an `answer` whose `question` is the blocked question and whose `text` contains the comment and its author;
   - it removes `af:fix` through hub;
   - the runner of attempt 2 is given that answer in `Answers`.

   The same comment and label sent as a stream event `evt-7` give the same result with `by` `evt-7`. A second sighting of the same label, by the stream or by a poll that still lists it, adds no attempt and posts no comment.
2. **Only a person's comments after the question count.** Three kinds of comment are left out of the answer:
   - comments that come before the question comment;
   - comments that carry an agent-fox marker (`<!-- agent-fox`), wherever they are;
   - blank comments.

   Several qualifying comments are joined in issue order. An answer longer than 8192 bytes is cut to 8192 bytes, and its last line says that it was cut.
3. **No answer found.** A blocked job's issue gets `af:fix` again with no new comment. No attempt is made, the label is removed, and one reply comment is posted, which says that no answer was found since the question and asks for a comment and then the label again. If the label is added a second time with still no comment, it is removed and no second reply is posted.
4. **A thread that hub cut is not read as "no answer".** Issue 42's fix job J is blocked, a person has commented after the question, and `af:fix` is added again, while hub answers the issue read with `truncated: true`. The following happens:
   - no attempt is made, and `af:fix` is removed;
   - one reply is posted. It says the thread could not be read whole, and it names `af jobs answer` and `POST …/answer` for J;
   - one `WARN` is logged.

   The same holds for a failed job: no retry is made, and the reply names `af jobs retry` and `POST …/retry`. An issue read whose `comments_err` is set leaves the label in place, makes no attempt and posts nothing.
5. **A retry from the forge.** The latest fix job of issue 42 failed, and its failure comment is on the issue. A person comments "the log is in CI run 812" and adds `af:fix`. The same job gets attempt *n*+1 with `trigger` `{"kind": "retry", "by": "label af:fix"}`, and no new job is made. The runner's `Context` ends with the comment, and the attempt document shows it in `context`. Without a new comment, the retry runs with no added context. If the latest fix job of the issue is `done` or `cancelled`, the label makes a new job as before. If it is `failed` but an open fix job exists, the label is absorbed.
6. **A triaged issue labelled again is a new triage.** Issue 42's latest triage job is `done`. The issue's body carries its diagnosis between the triage markers, after the original report. A person edits the original report and adds `af:triage`. One cycle makes a new triage job with `source` `{"kind": "label", "by": "label af:triage"}`. It is neither a retry nor an answer, and the done job is left unchanged. When the real worker runs the new triage with the scripted brain (as in goal 12), it replaces the diagnosis in the body and keeps the edited original report (PRD 01 D6).
7. **A sighting older than the attempt it would repeat.** The stream answers issue 42's blocked fix job with event `evt-7`, and the fake runner ends attempt 2 blocked at once. A poll cycle whose listing still shows 42 under `af:fix` (the fake forge's removal lag) adds no attempt and posts no reply. The same holds in two more cases:
   - a stream retry whose attempt fails at once;
   - a job made through the API (no `request_label`) that the stream answers.
8. **Hub failing, and races.** When hub answers `503` to the issue read, the label stays, no attempt is made, and one `WARN` is logged. The next cycle that can read the issue goes ahead. An API answer that lands between the poll's read and its answer makes the poll absorb the label, and only one attempt is made.
9. **The comments name the path.** The question comment, the failure comment and a parked fix's summary each say that a reply on the issue, followed by the request label of the job's kind (`af:fix`, `af:triage`), answers or retries the job. When the attempt read `AF_POLL_INTERVAL=0`, they do not say it. The instructions already in the comments stay. For example, the parked fix's summary still names `af jobs retry` and `POST …/retry`.
10. **AF_AUTO_FIX in the worker.** A triage of issue 42 ends `confirmed` with `AF_AUTO_FIX=true`. The worker records the follow-up with the jobs layer and then adds `af:fix` to the issue. The triage job shows `follow_up` `{"label": "af:fix", "state": "pending", "job_id": null}`, and `af:fix` is in its `labels.set` and `side_effects`. The worker adds nothing in these cases:
    - with `AF_AUTO_FIX` unset or `false`;
    - for a `suspected` triage;
    - for a failed triage.

    `AF_AUTO_FIX=maybe` fails the triage attempt as a `config` envelope whose `fix_hint.var` is `AF_AUTO_FIX`. Two cases record the follow-up without adding the label:
    - an open fix job of the issue at that moment: the follow-up is `absorbed` with that job's id;
    - a hub `503` on the label write: the follow-up is `withdrawn`, and the envelope carries `auto_fix_failed`.

    In three cases the worker records nothing, adds no label, and the envelope carries `auto_fix_skipped` with a message that names the reason:
    - a `carry_patch` workspace;
    - `AF_FIX_LAND=patch`;
    - `AF_POLL_INTERVAL=0`.

    When an earlier triage job T0 of issue 42 has a pending follow-up, the new triage's follow-up is `pending` and T0's becomes `withdrawn`.
11. **The linked fix job.** The trigger sees `af:fix` on issue 42 while issue 42's pending follow-up is from triage job T. It makes one fix job with:
    - `source` `{"kind": "triage", "by": T}`;
    - `parent_job_id` T;
    - `request_label` `af:fix`.

    T's `follow_up` becomes `{"state": "made", "job_id": <the fix job>}`. This holds when the event stream sees the label before T's attempt has ended. It also holds when a hold of `af:fix` from an earlier cancelled job is on the issue. A second sighting is absorbed. The fix job's summary comment names T. With no pending follow-up, `af:fix` makes a job of source `label` or `event`, as before.
12. **End to end.** Issue 44 carries `af:triage`, with `AF_AUTO_FIX=true`, the real worker and the scripted brain. One cycle makes the triage job. The worker runs it to `af:ready` and adds `af:fix`. The next cycle makes the linked fix job.
13. `make check` passes offline.

## Non-goals

- **Hub's event stream itself, and the issue-closed cancel.** These are `agent_fox_event_stream` (spec 18). This spec changes the rules that the poll and the stream share. It adds no connection, no event type and no dedup table.
- **Creating labels on subscribe, and the cancel cleanup.** These are `agent_fox_label_protocol` (spec 17), and they are used as built.
- **Editing the service's own comments.** The "no answer found" line is a reply comment. Hub's forge API is not used to edit a comment (Design Decision 8).
- **Reading more of a thread than hub returns.** When hub cuts an issue's comments, the service does not try to read past the cut. It says so on the issue and points to `af` and the API (R5).
- **Auto-fix while the label triggers are off.** With `AF_POLL_INTERVAL=0` nothing would take the label, so none is added (R7).
- **Mapping a reply to an option.** A reply of "B" is a free-text answer, as `POST /answer` with `{"answer": "B"}` would be. The brain sees the options with the question.
- **Context on the API's retry.** `POST /api/v1/jobs/{id}/retry` keeps taking no body. Only a retry from the forge carries comments as context.
- **The triage "reason" comment.** A `suspected` or `not_reproducible` triage ends `done`. Labelling it again is a new triage job, as today, and the reason comment is unchanged.
- **Chains other than triage then fix.** No automatic triage, no follow-up of a fix, and no follow-up where a fix is refused as `unsupported` (`carry_patch` workspaces, `AF_FIX_LAND=patch`). Carry patches are PRD 05, which may lift the last restriction.
- **Who added a label.** Neither hub's issue list nor this spec names the person. Answers and retries are attributed to the sighting (`label af:fix`, or the hub event id).
- **Changes to `af`** (`cmd/af`, sealed spec 16) and to hub.

## Background

PRD 04 §3 and §4 ask for three things:
- answering a `blocked` job and retrying a `failed` one from the forge, with "a comment, then the request label again";
- that "a triaged issue edited and re-labelled `af:triage` is a new triage job", with the diagnosis replaced and the original report kept (PRD 01 D6);
- `AF_AUTO_FIX`, which turns a triage that ends `af:ready` into a fix job "with no person involved". The chain is visible on both job records and on the issue, and `fix_summary` names the triage.

PRD 01 §3 lists `af:fix` as added "by a person, or the service after triage when `AF_AUTO_FIX=true`". `docs/prd/01…md` §4.1 item 3 says the service's added label "is a new request".

What exists today:

- **The trigger rules.** `cycleRun.issue` in `internal/service/poll/handler.go` decides what a request label on a listed issue becomes: absorbed, held, already taken (owed), or a new request. It gets an open job of the kind from `Jobs.OpenJob`, and that job may be queued, running or blocked. Spec 15 R4 is where these rules are stated. Its text says: "Until PRD 04, the poll handles a request label on an issue whose job is blocked by absorbing it, and treats a request label on an issue whose job failed as a new request" (`.specs/15_agent_fox_poll/prd.md:85`). `agent_fox_event_stream` makes the same rules apply to `hub.forge.issue.labeled` events, with the source `{"kind": "event", "by": <hub event id>}`. The rules then run for one sighting at a time per workspace. That spec's R7 adds a rule for the poll: when a record of the label on the issue was made at or after the listing was requested, the cycle makes no job and leaves the label to its next cycle. The stream reads the issue through hub before it applies the rules (its R5).
- **The poll's records.** `absorb` writes a record only when the removal fails (`owe`). `Records.Drop` deletes every record that owes nothing and names no job or an ended one (`poll/store.go:154`).
- **Answer and retry in the jobs layer.** `Service.Answer(ctx, id, by, AnswerRequest)` and `Service.Retry(ctx, id, by)` are in `internal/service/jobs/actions.go`. They queue attempt *n*+1 only if the job is still in the expected state, so when two race, one of them gets a `*StateError`. An answer is either free text of at most 8192 bytes (`maxAnswerBytes`) or an option. `RunRequest` (`store.go:614`) gives the runner the job's `Context` and every `Answer` up to the attempt. The worker passes both as `--context` values (`internal/worker/run.go:132-137`): context strings as material with no question, and answers with their question. A retry carries nothing new.
- **The worker's comments.** `internal/worker/comments.go` posts a `question` comment for a blocked job and a `failure` comment for a failed one. A parked fix is recorded by the jobs layer as `failed`, and it gets a `fix_summary` comment. Each comment carries the marker `<!-- agent-fox job=<id> attempt=<n> kind=<kind> -->`, and a comment whose marker is already on the issue is not posted again. The instructions the comments give today:
  - the question comment names `af jobs answer` and `POST …/answer`;
  - the failure comment names `POST …/retry`;
  - the parked fix's summary names `af jobs retry` and `POST …/retry`.

  Every other text the service writes to an issue carries an `<!-- agent-fox` marker as well: the triage body and diagnosis comment (`<!-- agent-fox:triage v1 run=…`), and fix bodies (`<!-- agent-fox:run …`).
- **The worker's mirror** (`internal/worker/mirror.go`, `planOf`). A triage whose confidence is `confirmed` or `probable` is planned as `ready` with no comment. A fix's `fix_summary` is composed in `fixSummary`.
- **The worker's variables.** These are the workspace's resolved hub variables, read at every attempt (`resolve.go:198`, `settings.go` `resolveSettings`).
  - A variable outside its range fails the attempt as a `config` envelope with `fix_hint.var`.
  - A variable of the other kind is not looked at. In particular, `AF_FIX_LAND` is read only for a fix.
  - A fix is refused as `unsupported` on a `carry_patch` workspace or with `AF_FIX_LAND=patch` (`resolve.go:214`).
  - `AF_AUTO_FIX` is read nowhere today, and `parent_job_id` does not exist. A `grep` finds both only in `docs/prd`.
- **The jobs layer accepts the source kind `triage`** (`api.go`, `sources`). No code makes such a job.
- **The worker reports to the jobs layer while it runs** through a per-attempt `*jobs.Recorder` (`recorder.go`). The worker sees it as its `reporter` interface (`runner.go`). The `Result` the worker returns is stored when the attempt ends. That is too late to tell the trigger that a label the worker just added is the triage's request, because the stream sees the label within a second.
- **The hub forge client** (`internal/hub/forge.go`) offers these calls:
  - `ReadIssue` reads an issue with its comments. It carries hub's `truncated`, which says the thread has more comments than were read, and `comments_err` (`forge.go:125-147`).
  - `ListComments` lists comments in one request. The bare array it gets back says nothing of a cut (`forge.go:149-160`). The client's own comment on hub's lists says that "a last page as long as the page asked for may have been cut by hub, which cannot say so" (`forge.go:309-312`).
  - Other calls add a comment (`AddComment`), and add and remove labels.

  No new hub route is needed.
- **The brief does not always see the whole thread.** The issue read is bounded by hub's cut, and the rendered report by 131072 bytes (`internal/toolio/input.go` `Truncate`, `internal/triage/brief.go`).
- **The fake hub** has three limits today:
  - it gives every comment the same `created_at`;
  - it has no helper to add a person's comment (`fakehub/forge.go` `commentJSON`, `issues.go`);
  - its issue read always answers `truncated: false` (`fakehub/forge.go:145`). A test can inject another body (`internal/hub/forge_test.go:431`).

This is the last of the three specs written from `docs/prd/04-drive-the-service-from-the-forge.md`.

## Requirements

### R1. The rules of a sighting, in order

A **sighting** is a request label L of kind K (`<prefix>triage` or `<prefix>fix`) on issue N of a workspace. A poll cycle or an event of the stream finds it. The sighting's `by` is what it would name as a new job's source: `label af:fix`, or the hub event id. The poll and the stream apply the same rules, in the one place `agent_fox_event_stream` leaves them. After the records are brought up to date as today, the first rule that applies decides:

1. **A follow-up the triage asked for** (K is fix, and N has a pending follow-up, R7): R8 decides.
2. **An open job of kind K exists for N.**
   - If it is `blocked`, no removal of L on N is owed, and no record of L on N is newer than the sighting, the sighting is an **answer** (R2).
   - If it is `blocked`, no removal is owed, and a record of L on N is newer than the sighting, nothing is done, and L is left to the next cycle.
   - Otherwise the label is **absorbed**, as today.
3. **Held**, as today.
4. **Already taken** (owed), as today.
5. **A record of L on N is newer than the sighting** (`agent_fox_event_stream` R7): nothing is done, and L is left to the next cycle.
6. **The latest job of kind K for N in the workspace** (by `created_at`) is `failed`: the sighting is a **retry** (R4).
7. Otherwise it is **a new request**, as today.

**Newer than the sighting.** A record is newer than a poll's sighting when it was made at or after the listing was requested. That is `agent_fox_event_stream` R7's rule, and here it guards the answer and the retry as well as a new request. A stream sighting reads the issue under the per-workspace lock before the rules apply (that spec's R5), so no record is newer than it.

**What is recorded.** An answer, a retry, a "no answer found" reply and a cut-thread reply (R5) each consume L as a new request does:
- a record of L on N is written before the removal, and the removal is owed while it fails;
- the record names the job only when the job's own `request_label` is L, because only then can a cancel put L back and hold it. Otherwise it names no job;
- the record, with or without a job, is kept at least until every listing requested before it was made has been handled. Rules 2 and 5 therefore see it, even when the attempt it queued has already ended.

**Logs.** Each outcome is logged at `INFO` with the workspace, the kind, the issue, the label and the job. The outcomes are: answered, retried, no answer found, follow-up made. A cut thread is logged at `WARN` (R5).

### R2. Answering a blocked job

For an answer sighting on the blocked job J:
1. The service reads N's comments (R3).
2. It takes the **answer comments** (R3).
3. If the thread is cut, or there are no answer comments, R5 applies.
4. Otherwise it answers J with the free text of R3, with `by` set to the sighting's `by`. The effect is the same as `POST /api/v1/jobs/{id}/answer` with that text.

Attempt *n*+1 then has `trigger` `{"kind": "answer", "by": <by>}`, and `answer` `{"question", "option": null, "text"}`. The runner is given the answer among `Answers`, as for an API answer. After the answer is stored, L is removed.

**Refusals of the answer:**
- **J moved meanwhile** (the jobs layer's state refusal, today `*StateError`): L is absorbed.
- **The workspace is not subscribed** (`ErrNotSubscribed`): nothing is written to the issue, and the cycle ends, as for a new request.
- **The token cannot be read, and other errors:** they are reported as the poll reports a failure of the jobs layer, and L stays.

### R3. What counts as the comments since the question or the failure

**The read.** The service reads N's comments through hub's issue read. That read says when hub cut the comments (`truncated`) and when it could not read them (`comments_err`). Hub's comment list says neither (Verified External API), so it is not used for this. A stream sighting may use the issue read it already makes.

**A cut thread.** When hub says it cut the comments, the comments read are not taken to be all of them. They yield no answer and no context, and R5's cut-thread reply applies, for an answer and a retry alike.

**The anchor.** The anchor is the comment on N that carries the marker of J's latest attempt (`<!-- agent-fox job=J attempt=<current_attempt> kind=`). For a blocked job that is the question. For a failed job it is the failure comment, or a parked fix's summary.
- The comments that count are those after the anchor, in the order hub lists them.
- If no comment on the list carries that marker, the anchor is a time instead. The comments that count are then those whose `created_at` is at or after the time J's latest attempt ended (`attempts[n].finished_at`).

**Which comments are left out:**
- every comment whose body contains `<!-- agent-fox`, which marks it as the service's own;
- every comment whose body is blank after trimming.

**The text.** Each comment that counts becomes `--- comment by <login> ---` followed by its body, trimmed. The blocks are joined by a blank line, in order.
- NUL bytes are dropped, and invalid UTF-8 is replaced.
- A text over 8192 bytes is cut at a UTF-8 boundary and given the last line `[… cut at 8192 bytes; the issue holds the rest …]`. With that line, it is at most 8192 bytes.

The same text serves as an answer (R2) and as a retry's context (R4).

**When the comments cannot be read:**
- **Hub unavailable, or the token refused or forbidden:** the sighting does nothing and L stays, so the next sighting tries again. The failure is reported as the poll reports such a call, and it ends a poll cycle as a failed hub call does.
- **Hub rejects the read of this one issue (another 4xx), or answers with `comments_err` set:** the failure is reported, L stays, and the cycle goes on with the next issue.
- **The stream:** it never acts on a sighting whose comments it could not read. The poll catches the label.

### R4. Retrying a failed job

For a retry sighting on the failed job J:
1. The service reads N's comments and takes the text of R3, measured from J's latest attempt. If the thread is cut, R5 applies and no retry is made.
2. It retries J with the effect of `POST /api/v1/jobs/{id}/retry`, with `by` set to the sighting's `by`. When there is any text, it becomes the new attempt's **context**.

A retry with no comments since the failure still runs, with no added context, provided the thread is not cut.

**The attempt's context:**
- It is stored with the attempt, and shown in the attempt document as `context`: a list of strings, or `null` when it has none.
- The runner is given the job's context strings followed by the context of each attempt up to the one it runs, in attempt order. The worker passes them as `--context` material with no question, as it passes the job's context today. An automatic attempt after it keeps them.
- `POST /api/v1/jobs/{id}/retry` is unchanged and adds no context.

**Refusals of the retry:**
- **Another job took the issue** (a duplicate refusal), or **J was retried or otherwise moved meanwhile** (a state refusal): L is absorbed.
- **The workspace is not subscribed, the token cannot be read, and other errors:** handled as in R2.

### R5. When no answer is found, or the thread is cut

**No answer found.** An answer sighting whose comments yield no text, from a thread that is not cut, makes no attempt. In order:
1. The service posts one reply comment on N through hub. Its text is "No answer found since the question; comment, then add the label `<L>` again.", followed by the marker `<!-- agent-fox job=J attempt=<n> kind=no_answer -->`. The reply is not posted when the latest comment on N already carries that marker.
2. It removes L, owed on failure as R1 says.

A retry never takes this path.

**A cut thread.** An answer or retry sighting whose issue read hub cut (R3) makes no attempt. In order:
1. The service logs one `WARN` that names the workspace, the issue and J.
2. It posts one reply comment on N through hub. The reply says that agent-fox could not read the whole thread, because hub returned only part of its comments, and so took nothing from it. For an answer it names `af jobs answer <J>` and `POST /api/v1/jobs/<J>/answer`. For a retry it names `af jobs retry <J>` and `POST /api/v1/jobs/<J>/retry`. The reply ends with the marker `<!-- agent-fox job=J attempt=<n> kind=thread_cut -->`.
3. It removes L, owed on failure as R1 says.

**Failure to post.** When either reply cannot be posted, a `WARN` is logged, and L is removed all the same.

### R6. The question and failure comments name the forge path

The worker's comments for a job whose input is an issue gain one sentence each:
- **The question comment:** "Or reply on this issue, then add the label `<prefix><kind>` again: agent-fox takes the comments after this one as the answer."
- **The failure comment, and the parked fix's summary:** "Or add the label `<prefix><kind>` again; comments after this one go with the new attempt."

`<prefix>` is the subscription's `label_prefix`, and `<kind>` the job's.

The sentence is left out when the attempt read `AF_POLL_INTERVAL` as a zero duration, which turns the label triggers off. The worker reads the variable with the same values as the poll:
- unset or empty is on;
- a zero duration is off;
- any other value, an invalid one included, is on. An invalid value does not fail the attempt.

The existing instructions stay:
- `af jobs answer` and `POST …/answer` in the question comment;
- `POST …/retry` in the failure comment;
- `af jobs retry` and `POST …/retry` in the parked fix's summary.

### R7. AF_AUTO_FIX: the worker asks for the fix

**The variable.** `AF_AUTO_FIX` is a hub variable that a triage attempt reads with the others.
- Unset or empty means `false`.
- `true` and `false` are accepted in any case.
- Any other value fails a triage attempt as a `config` envelope, not retryable, with `fix_hint.var` `AF_AUTO_FIX`, like any variable outside its range.
- A fix attempt does not look at it.

**When the worker asks.** This happens when the mirror of a triage attempt plans the status `ready` for an issue, and `AF_AUTO_FIX` is `true`. The issue is the input issue, or the issue that a text or file triage created. After the status label and before the lease is released, the worker has the jobs layer record a **follow-up** and learns the state the jobs layer gives it. The follow-up names the triage job, the issue and the label `<prefix>fix`. It is durable before the label is written.

The jobs layer records at most one follow-up for each triage job, and keeps at most one `pending` follow-up for each workspace and issue. It gives one of two states:
- **`absorbed`**, naming that job, when an open fix job of the issue exists. The worker adds no label.
- **`pending`** otherwise. A pending follow-up of an earlier triage job for the same issue becomes `withdrawn` in the same step, so that the fix is linked to the latest triage. The worker then adds `<prefix>fix` to the issue, as a side effect `add_labels` that counts in `labels.set`.

**When the worker does not ask.** In three cases it records no follow-up and adds no label, and the envelope carries the warning `auto_fix_skipped`, whose message names the reason:
- the attempt read `AF_POLL_INTERVAL` as a zero duration (R6), so the label triggers are off and nothing would take the label;
- the workspace is `carry_patch`;
- `AF_FIX_LAND` is `patch`, read as a fix attempt reads it. A triage attempt looks at `AF_FIX_LAND` only for this check. Any other value, an invalid one included, does not fail the triage.

The last two are the cases in which a fix is refused as `unsupported`.

**Failures:**
- If the follow-up cannot be recorded, the worker adds no label, and the envelope carries the warning `auto_fix_failed`.
- If the label cannot be added, the envelope carries `auto_fix_failed` with a failed side effect, and the worker has the jobs layer mark the follow-up `withdrawn`.

**A delivery that resumes the mirror** asks again and gets the recorded state. It adds the label again only while the follow-up is `pending`.

**What becomes of a follow-up later.** A follow-up stays as recorded whatever becomes of the triage job afterwards. A pending one stays pending until a sighting takes it (R8), or a later triage of the same issue makes it `withdrawn`.

### R8. The trigger makes the linked fix job

A fix-label sighting on an issue whose pending follow-up is from triage job T is handled in one of three ways:
- **An open fix job J exists.** The label is absorbed, and the follow-up becomes `absorbed` with J. No answer is made, even when J is blocked, because the label is the service's own.
- **Otherwise** a fix job is made with:
  - `source` `{"kind": "triage", "by": T}`;
  - `parent_job_id` T;
  - `request_label` the label;
  - `input` `{"issue": N}`.

  The fix job and the follow-up's `made` state are stored together. No reader sees the job without the follow-up `made`, or the reverse, and a crash leaves both or neither. Every hold of the label on N ends, and the poll's record of the new job is written. The label is then removed, as for a new request.
- **A duplicate refusal** absorbs the label, and the follow-up becomes `absorbed`.

**A job with a parent.** A job may have a parent only when its source is `triage`. The parent must be a triage job of the same workspace, and the same job as `source.by`. A request to make a job that breaks this is refused as invalid.

**The job document gains two fields:**
- `parent_job_id`: the job this one follows from, or `null`;
- `follow_up`: for a triage job that asked, `{"label", "issue", "state": "pending" | "made" | "absorbed" | "withdrawn", "job_id"}`, and otherwise `null`.

The retention purge of a triage job takes its follow-up with it. A fix job keeps its `parent_job_id` after its parent is purged.

### R9. The fix summary names the triage

The `fix_summary` comment of a fix job whose source is `triage` gains the line `- Asked for by the triage job \`<T>\` (AF_AUTO_FIX)`. Nothing else in the comment changes.

### R10. Documentation and tests

**Documentation.** After `make doc`, the following are updated in the same change:

| File | What it gains |
|---|---|
| `docs/api.md` | Several sections change:<ul><li>*Jobs that a request label makes*: the rule order of R1, answering, "no answer found", the cut-thread reply and retrying from the forge, and the follow-up rule. The sentence "A label on an issue whose job `failed` is a new request" is replaced.</li><li>*Answer and retry*: the forge path, `trigger.by` for a sighting, and the attempt's `context`.</li><li>*The job document*: `parent_job_id`, `follow_up`, the source `triage`, and the attempt's `context`.</li><li>*What the worker adds to an attempt*: `auto_fix_failed` and `auto_fix_skipped`, with its three reasons.</li></ul> |
| `docs/configuration.md` | `AF_AUTO_FIX` in *The hub variables of an attempt*, and that it is skipped with `AF_POLL_INTERVAL=0`, on `carry_patch` workspaces and with `AF_FIX_LAND=patch`. In *The label poll*: that `AF_POLL_INTERVAL=0` also turns off answering and retrying from the forge, and leaves the forge path out of the comments |
| `docs/architecture.md` | The forge follow-ups in the label protocol section, and the follow-up's place in the jobs layer |
| `docs/model-usage.md` | `AF_AUTO_FIX` spends on a fix with no click, and an answer or retry from the forge spends like one from the API |
| `docs/development.md` | The fake hub's comment helper and comment times |
| `README.md` | One paragraph on how a team answers, retries and chains from the issue |

`docs/cli.md` is unchanged. The existing `docs/*_docs_test.go` keep passing, including every phrase `docs/poll_docs_test.go` asserts. No new test checks the documents.

**Tests.** Every test runs offline, under the conventions of the earlier specs (`Test<Sentence>_TS_<spec>_<n>` with `// Verifies:`). Tests of `agent_fox_poll` that assert "a label on a failed job's issue makes a new job" change to assert the retry.

## Design Decisions

1. **This is the third spec of PRD 04's split, after `agent_fox_label_protocol` (17) and `agent_fox_event_stream` (18).** Answering and retrying change the rules that the poll and the stream share. Writing them once, after both exist, keeps one rule set.
2. **The forge path is part of the shared sighting rules, not a third trigger.** The stream already dedups by event id, and the poll by records. Answer and retry are conditional on the job's state, so a stream sighting and a poll sighting of one label give one attempt, and the loser is absorbed. A listing older than the attempt it would repeat is covered by Design Decision 24.
3. **An answer is for the open job of the label's kind. A retry is for the latest job of the kind on the issue, if that job failed, however old.** `af:fix` answers or retries a fix job, never a triage job. Retrying the latest failed job keeps its history in one record, which is PRD 04's "`POST /jobs/:id/retry`'s effect". A failed job that a later job superseded is not retried.
4. **Any job with an issue can be answered or retried from the forge, an API job included. The record names the job, and so can hold, only when the job's `request_label` is the consumed label.** The question comment is posted for every job with an issue, so the path must work for every such job. A cancel puts back only the job's own request label, so a record for any other job would hold a label that never comes back.
5. **"Since the question" means after the comment carrying the latest attempt's marker, in the forge's list order. Time is the fallback when that comment is missing.** The marker identifies the anchor exactly. List order cannot tie the way two equal timestamps can, and the service's clock cannot skew it. The fallback covers a question or failure comment whose write failed, and a failure that the jobs layer synthesized with no comment. It uses `issuex.Comment.CreatedAt`, whose type the repository's own use shows (Verified External API).
6. **The service's own comments are those that carry `<!-- agent-fox`, not those by a given author.** The forge author of the service's writes is hub's credential, which a person may share. Every text the service writes already carries a marker.
7. **The comments become free text in the thread's own rendering (`--- comment by <login> ---`), cut at 8192 bytes, earliest first.** 8192 bytes is the API's answer limit and the limit of one context string. The cut is not lossless. The brief reads the thread only as far as hub returns it and its own report bound (131072 bytes) allows, so on a long thread the part of an answer beyond the cut may reach the brain nowhere. The last line of a cut answer says so and points the person to the issue. Earliest first keeps the direct reply to the question.
8. **"The question comment gets one line" becomes a reply comment with its own marker, posted unless it is already the latest comment.** The hub forge client has no comment edit, and this spec adds no hub route. The marker keeps the reply out of later answers. The "already latest" check stops a poll sighting and a stream sighting from both posting it.
9. **A retry's comments are stored as the new attempt's context, not as an answer.** They answer no question, and the worker already passes question-less context as material. One nullable attempt field carries it to the runner. The API's retry stays body-less, so the sealed `af` client is untouched.
10. **No comments, no action: a sighting that cannot read the comments leaves the label.** A retry without the context the person wrote would be wrong, and so would a "no answer found" reply to an answer that is there. The label staying is how the poll tries again.
11. **`trigger.by` of a forge answer or retry is the sighting's `by` (`label af:fix`, or the hub event id).** Neither path knows the person. This matches the `source.by` that the same sighting would give a new job.
12. **A state refusal or a duplicate refusal from the jobs layer absorbs the label.** Something else moved the job meanwhile, and the label's request is already being served.
13. **The comments name the forge path only when the label triggers are on.** `AF_POLL_INTERVAL=0` turns the poll and the stream off (spec 18), and a comment must not advertise a path that does nothing. The worker reads the variable with the others.
14. **The worker reads `AF_AUTO_FIX`, for a triage only, and an invalid value fails the triage as `config`.** The scope places the label write in the worker, which already reads the variables at every attempt. The repository's rule is that a variable outside its range refuses the attempt (`docs/configuration.md`).
15. **The follow-up is recorded durably in the jobs layer before the label is added.** The stream sees a label within a second, before the triage attempt has ended, so the triage's `Result` (`labels.set`) cannot be what tells the trigger. A record written first can.
16. **An open fix job at ask time absorbs the follow-up, and no label is added.** Otherwise the service's own label would answer a blocked fix job or join a queued one.
17. **A pending follow-up takes precedence over the answer, hold and owed rules, and closes together with the job it makes.** It is fresh evidence of a request, and a hold would otherwise keep the label waiting for a person. Closing it together with the job means a crash can never leave a made job with a pending follow-up.
18. **A follow-up stays pending until a sighting takes it, and it stands whatever becomes of the triage job.** It is withdrawn in only two cases: when the worker's label write fails, and when a later triage of the same issue supersedes it (Design Decision 27). A time limit would be fragile against a 24-hour poll interval. The accepted cost: a person who removes the service's label and adds it much later gets a fix linked to the triage.
19. **Auto-fix is skipped, with `auto_fix_skipped`, wherever a fix would be refused as `unsupported`: on `carry_patch` workspaces and with `AF_FIX_LAND=patch`.** Until PRD 05 the worker refuses such a fix (`resolve.go:214`), so the follow-up would only leave `af:failed` on the issue. A triage attempt therefore looks at `AF_FIX_LAND` for this check only. It does not fail on other values, which only a fix attempt judges.
20. **A text or file triage that created an issue and ended ready gets the follow-up on the created issue.** PRD 04 asks for it for "a triage that ends `af:ready`", and the mirror already writes to that issue. The follow-up stores the issue number, because such a triage job has no `issue`.
21. **The chain is shown as `parent_job_id` on the fix job and `follow_up` on the triage job.** That makes it visible on both records, as PRD 04 asks. `follow_up` also shows a request that was absorbed or withdrawn, which `parent_job_id` alone cannot. PRD 05 can reuse `parent_job_id`.
22. **The requirements state behaviour and its guarantees. Design decides the mechanism.** The guarantees include: a follow-up is durable before its label is written; a made fix job and its follow-up's `made` state are never seen apart; a job with a parent has source `triage` and a triage parent of the same workspace; the worker reads `AF_POLL_INTERVAL` with the poll's values. Design decides the rest:
    - the names;
    - the migration, appended after those of specs 17 and 18;
    - where the follow-up lives, and how the worker reaches the jobs layer during an attempt;
    - how the fix job and its follow-up are stored together;
    - how the worker shares the poll's reading of `AF_POLL_INTERVAL`;
    - the new methods of `poll.Jobs`.
23. **The comments are read through hub's issue read, and a thread that hub cut gives no answer and no context: the service replies, naming `af` and the API, and removes the label.** Hub's comment list is one request whose bare array cannot say it was cut. The issue read says so with `truncated`. Answering from a cut thread could post "no answer found", or retry without context, while the answer sits on the issue. Leaving the label in place would be a silent no-op on the forge, because retrying cannot make a long thread short. The reply tells the person what to do instead. The accepted cost: on a thread longer than hub's cut, the forge path does not work, and the person uses `af` or the API.
24. **`agent_fox_event_stream` R7's newer-record rule also guards the answer and the retry, and every forge outcome leaves a record that the rule can see.** Answer and retry are conditional on the job's state, and that state comes back once the new attempt ends. Without the rule, a poll listing older than the attempt would find the job `blocked` or `failed` again. It would then retry a second time, or post a false "no answer found". A record with no job, for a job whose `request_label` is not the label, gives the rule something to see. It is kept until every older listing has been handled.
25. **Auto-fix is skipped while the label triggers are off.** With `AF_POLL_INTERVAL=0` nothing takes the label. The issue would carry a request that does nothing, and a linked fix, which spends money, would appear whenever the poll was turned on again. This is the same reason the comments drop the forge path (Design Decision 13).
26. **A triaged issue labelled `af:triage` again stays a new triage job, and a goal tests it.** The latest triage job is `done`, so the new retry rule does not apply. The replace-and-keep of the body is the triage's existing behaviour (PRD 01 D6). The goal guards both against the new rule order.
27. **There is at most one pending follow-up per workspace and issue, and a newer one withdraws the older one.** Two pending follow-ups would leave the parent of the fix ambiguous. The latest triage is the one whose diagnosis the body now holds.
28. **Corrections from review.** These change no behaviour:
    - `issuex.Comment.CreatedAt` is a `time.Time`, verified from the repository's own use;
    - the parked fix's summary already names `af jobs retry`, and R6 keeps it;
    - this spec replaces the "Until PRD 04" behaviour stated in spec 15's R4.

## Dependencies

| Spec | Relationship | Reason |
|---|---|---|
| `11_agent_fox_service_core` | modifies | Appends one migration to the append-only list in `internal/service/store/migrations.go`: the attempt context, the job's parent, and the follow-up. |
| `12_agent_fox_jobs` | depends on, modifies | Adds context to the retry, the attempt's `context`, the runner's context, `parent_job_id` and its validation when a job is made, the follow-up record, the way the worker records it during an attempt, a lookup of the latest job of a kind for an issue, and the document fields (`actions.go`, `api.go`, `store.go`, `recorder.go`, `document.go`, `model.go`, `retain.go`). |
| `13_agent_fox_worker` | depends on, modifies | Adds `AF_AUTO_FIX`, a triage's check of `AF_FIX_LAND=patch`, and the reading of `AF_POLL_INTERVAL` (`settings.go`). Adds the follow-up ask and label in the mirror (`mirror.go`), the forge-path sentences and the triage line in the comments (`comments.go`), and two warnings. Extends `internal/hub/fakehub` with person comments and comment times. |
| `15_agent_fox_poll` | depends on, modifies | Changes the shared rules in `internal/service/poll/handler.go`. Replaces the "Until PRD 04" behaviour of its R4 (`.specs/15_agent_fox_poll/prd.md:85`): a blocked job's label becomes an answer, and a failed job's label a retry. Its R5 records gain records for answers, retries and replies. |
| `17_agent_fox_label_protocol` | depends on | Uses the shared label table for `<prefix>fix` and `<prefix>triage`, and its hold rules for a cancelled answered or retried job. |
| `18_agent_fox_event_stream` | depends on | The stream applies the same sighting rules, with source `event` and the hub event id as `by`, one sighting at a time per workspace, after its own read of the issue (its R5). Its R7 newer-record rule also guards answers and retries here. `AF_POLL_INTERVAL=0` turns both triggers off. |

## Verified External API

**`github.com/agent-fox-dev/aflib/issuex`** (pinned `v0.0.0-20261019184100-07adcb691832`). Its source is in the module cache, outside the repository, and was not read. What the repository's own use shows:
- `(*hub.ForgeClient).ReadIssue(ctx, issuex.IssueRef) (issuex.IssueThread, error)`. It decodes hub's `{issue, comments, truncated, comments_err}` into `IssueThread{Issue, Comments, Truncated, CommentsErr}`. Read at `internal/hub/forge.go:125-147`.
- `(*hub.ForgeClient).ListComments(ctx, issuex.IssueRef) (issuex.CommentList, error)`, where `CommentList.Comments` is `[]issuex.Comment`. It is one GET that decodes a bare array, with no paging and no sign of a cut. Read at `internal/hub/forge.go:149-160`. This spec does not use it for answers.
- `(*hub.ForgeClient).AddComment(ctx, issuex.IssueRef, body string) (string, error)`: read at `internal/hub/forge.go:215`.
- `AddLabels(ctx, ref, []string) error`, `RemoveLabel(ctx, ref, string) error` and `issuex.IsNotFound(error) bool`: read at `internal/hub/forge.go` and `internal/service/poll/handler.go`.
- `issuex.Comment.Body` and `issuex.Comment.User.Login`: read from use in `internal/toolio/render.go:42`.
- `issuex.Comment.CreatedAt time.Time`: read from use in `internal/triage/brief.go:213` (`date(c.CreatedAt)`, with `func date(t time.Time)` at `:248`) and `internal/triage/brief_test.go:65`.

**Hub's HTTP API.**
- **Read from the fake (`internal/hub/fakehub/forge.go`):**
  - `GET /api/v1/workspaces/{slug}/issues/{n}` answers `{issue, comments, truncated: false}` (`:145`), and a test can inject another body (`internal/hub/forge_test.go:431`).
  - `GET …/issues/{n}/comments` answers a bare array of `{id, body, user{login}, created_at}`.
  - `POST` on the comments path creates a comment.
- **Unverified (assumed):**
  - how many comments hub's issue read returns before it sets `truncated`, and whether the comments it keeps are the oldest;
  - that the real hub lists comments oldest first, as GitHub's and GitLab's lists do when asked with `sort=asc`, which is how issuex asks.

  Hub's OpenAPI document (`../hub/docs/openapi.yaml`) is outside the repository.

**Standard library:** `strings`, `unicode/utf8`, `time`, `database/sql`, `log/slog`.
