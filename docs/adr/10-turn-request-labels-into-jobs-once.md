# ADR 10: Turn request labels into jobs once

- **Status:** Accepted
- **Date:** 2026-10-25 (records decisions taken while specs 15, 17, 18 and 19 were implemented)

## Context

The label poll turns `af:triage` and `af:fix` on issues into jobs, answers a
blocked job and retries a failed one from the issue, and a cancel puts the
request label back ([architecture](../architecture.md#the-label-poll),
[api.md](../api.md#request-labels)). A label must make one job, not two, though
hub's lists cannot say they were cut, the forge's index lags a removal, and two
cycles of a workspace can see the same label. The specs compared records by
time, assumed hub's paging, and left several failures open. The rules below are
how the poll keeps "once" with what hub and the forge give it.

## Decision

**The poll takes no more than it asked for.** An issue that is `closed`, or
that does not carry the label (in any case), is not handed to the rules, so a
hub that dropped a parameter cannot turn a repository's issues into jobs. A
list that may be cut ([ADR 06](06-follow-hubs-api-where-the-specs-assumed-it.md))
is incomplete: it releases no hold and settles no owed removal.

**A newer record is newer by id or by time.** A listing notes the last record
id when it is asked for, and a record is newer than the listing when its id is
above that or its time is later. A clock that stands still (as in tests, or a
host whose clock stalls) cannot make a record look as old as the listing.

**Which listings the guard protects.** The newer-record guard protects a
sighting from a listing asked for before an answer or a retry was recorded. A
listing asked for after it, which still shows the label only because the
forge's index lags the removal, is not protected and can answer or retry the
job a second time, as a lagging forge can make a second job of a new request.
Records need no change to their drop for this: a workspace's listings are asked
for and handled one at a time, and the drop runs only at the start of a cycle
and after a listing is handled.

**The thread of an answer.** The comments that count are those after the
first comment carrying the latest attempt's marker (the question, the failure
comment or a parked fix's summary), since the trigger's own replies carry the
same marker and come after it. A person's reply written before a reply of the
trigger still counts. Both replies (no answer, cut thread) are skipped when the
issue's latest comment is that reply already. The comments are read only when
a rule needs them, an answer or a retry, so `comments_err` on any other sighting
changes nothing.

**Failures keep the poll's levels.** Hub unavailable is a `WARN`; a token hub
refuses or forbids is an `ERROR` naming the `PUT` route that cures it, on a read
of the issue as everywhere else; hub's `502` for a forge that refuses its
credential ends the cycle as an `ERROR`, on a removal as on a read. A `404` on
an issue read rejects that one issue and the cycle goes on, since the cycle read
the workspace a moment before.

**The cancel cleanup.**

- A try that leaves the cleanup pending (hub became unavailable half-way)
  records nothing, so the next try still finds in `labels.set` the status labels
  the attempts set; it reads the issue again and writes only what is missing.
  The try that ends the cleanup records what it wrote.
- A try removes the status labels of the job's `labels.set` whatever prefix
  they carry, and puts back the request label under its own prefix, since a
  replace with another prefix does not rename labels.

## Consequences

Known limits, left as they are:

- A label on a workspace whose forge URL names no repository ends the cycle
  with a `WARN`.
- A failed record write is retried only by the next cycle's read of the
  records, and an unreadable table ends the cycle with the label left: the
  choice that never makes a second job of a label that may be owed.
- A workspace with 100 or more issues under one request label has an
  incomplete listing every cycle until the backlog drains, which holds no hold
  and settles no owed removal meanwhile.
