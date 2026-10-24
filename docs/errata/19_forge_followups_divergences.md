# Erratum 19: agent-fox forge follow-ups

Where the implementation of `19_agent_fox_forge_followups` differs from what the spec's text assumes, and why.

## 1. A stale listing is one asked for before the outcome (D-1)

TS-19-11 and TS-19-59 speak of a poll listing that "still shows" the label after the stream answered or retried a job. The
newer-record rule (spec 18 R7) protects a sighting from a listing asked for *before* the record was made, and that is what
the tests build: the poll's request hook has the stream act on the label while the cycle's listing, asked for already, is
still to be handled (`internal/service/poll/repeat_test.go`, `smoke_test.go`). A listing asked for *after* the record, which
still shows the label only because the forge's index lags the removal, is not protected: it would find the job blocked or
failed again once the new attempt ended and answer or retry it a second time. That is the promise spec 15 already made for
new requests ("a forge that still lists the label for a moment after it was removed can yield a second job"), and
`docs/api.md` says it of the forge path too.

## 2. Records outlive older listings without a change to the drop (D-2)

R1 asks that a record written for an answer, a retry or a reply be kept until every listing asked for before it has been
handled, and the plan's step 6.6 says to change `Records.Drop` for it. No change was needed: a workspace's listings are asked
for and handled one at a time, and `drop` runs only at the start of a cycle and after a listing is handled
(`internal/service/poll/handler.go`, `issueHandler.handle`), so no listing asked for before a record is still to be handled
when the drop runs. TS-19-11 tests the guarantee.

## 3. The anchor is the first comment with the marker

R3 says the comments that count are those after "the comment on N that carries the marker of J's latest attempt". The
trigger's own replies (`kind=no_answer`, `kind=thread_cut`) carry the same `job=J attempt=n` marker and come after the
question, so the anchor is the first comment with it: the question, the failure comment or a parked fix's summary
(`internal/service/poll/thread.go`, `counted`). A person's reply written before a reply of the trigger still counts
(TS-19-17).

## 4. Smaller choices

- **The reply's check applies to both replies.** R5 skips the no-answer reply when it is the latest comment already; the
  cut-thread reply is skipped the same way, so that a sighting of the poll and one of the stream do not both post it
  (`reply.go`, `reply`).
- **The comments are read when a rule needs them.** A sighting reads the issue's comments only for an answer or a retry; a
  new request, an absorb or a follow-up needs none, so `comments_err` on such a sighting changes nothing (19-REQ-3.8 is about
  the comments a sighting needs).
- **A 404 on the issue read is the issue's.** The cycle read the workspace record a moment before, so hub not finding the
  issue is a rejection of the one issue: it is reported and the cycle goes on (19-REQ-3.7), not the "workspace not found"
  that ends a cycle (`answer.go`, `thread`).
- **`by` of a follow-up made.** The `INFO` of a follow-up made names the linked fix job as `job` and the triage as `triage`.
- **TS-19-4.** The row of a newer record is the poll's alone: a sighting of the stream reads the issue first, and no record
  is newer than it. Each row also checks who the attempt or the job names.

## 5. Existing tests that changed

- **TS-15-29** (`internal/service/poll/owed_test.go`) asserted that a blocked job absorbs the label, the "Until PRD 04"
  behaviour of spec 15 R4 that this spec replaces with an answer. Its case now asserts that a blocked job whose removal is
  owed still absorbs the label (19-REQ-1.4) (D-10).
- **TS-12-9, TS-12-10 and TS-12-14** pin the job document's fields; they gained `parent_job_id` and `follow_up` (D-7).
- **TS-13-27** (`internal/worker/resolve_test.go`) compares whole `Settings` values; they gained `TriggersOn`, true by default
  (D-11).
- No test asserted that a label on a failed job's issue makes a new job, so none changed for the retry.

## 6. Test seams

- The worker's `reporter` gains `RecordFollowUp` and `WithdrawFollowUp`; the test `sink` answers them from a function, and
  the tests that need the jobs layer's own answer (TS-19-38, TS-19-39, TS-19-62, TS-19-63) run the worker under the real jobs
  layer, with no sink, so that it reports through the attempt's `jobs.Recorder`.
- TS-19-43 stops the first delivery after the mirror's label step (`abortAfter = "mirror_labels"`), so that the second one
  goes through the mirror again.
