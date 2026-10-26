# ADR 11: Keep the service client a consumer of the API

- **Status:** Accepted
- **Date:** 2026-10-25 (records decisions taken while spec 16 was implemented)

## Context

`af` is the command-line client of the service: `af workspace …` and
`af jobs …` ([cli.md](../cli.md#service-client-commands),
[architecture](../architecture.md#the-service-client)). Its spec gave
`af triage` and `af fix` two modes, `--local` and `--workspace <slug>`, chosen
by a hook of the shell before anything else is parsed, and an `af login` that
checked a credential with the service and rewrote `~/.af/config.toml`. Both were
removed: the loops always run on the machine they are started on, and queueing
work is a client command of its own. What the client does where the spec left
it open is below.

## Decision

**Two kinds of command, kept apart.** `af triage` and `af fix` run the loop
here, with no mode flag; `af jobs submit triage|fix <input> --workspace <slug>`
queues the same work on the service, with the input rules, `--wait` and
`--context`. The client commands are in `cmd/af` only, so a caller of
`afcmd.New` (the worker) never gets them, and their warning codes are
constants of `cmd/af`.

**The credential is read, never written.** `af` reads `token` from
`~/.af/config.toml` and never writes the file; `afc login` writes it, or a
person does, and the hint of a missing or refused credential says so. The
credential and any workspace token are redacted from every output
([ADR 01](01-keep-one-envelope-and-one-shell-for-every-command.md)).

**How a followed job ends.** The status and error of a followed job come from
the `result` of the job document, which the service fills from the latest
finished attempt; the follower then fetches that attempt's envelope for the
result and its warnings. A followed job that failed, was cancelled or ended
`usage` has the error and the same `{job, envelope}` result: a `toolio.Failure`
can carry a result, which the shell keeps in the failed envelope. A failure
without one still gives `result: null`.

**Requests.**

- A `409 not_subscribed` or `token_unreadable` on a job route has no slug in
  its path, so its hint is `af workspace subscribe <slug> --hub-token-file FILE`
  with the placeholder; on a workspace route the slug is the real one.
- A refused connection is worded "nothing is listening there", so the message
  of a check that could not be made never says "refused".
- Following works in tries: the stream from the last event delivered, then one
  read of the job. A try that delivered no new event fails, whatever ended it,
  including a 5xx or a transport error on either request.
- The `next` of `af jobs list` repeats the command with the cursor as its last
  two words, after `--server`.

## Consequences

- The loops have one mode and the shell has no mode or help hook.
- A person who wants a job on the service says so with `af jobs submit`; the
  same input on `af triage` or `af fix` runs locally, never on the service.
