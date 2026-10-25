# ADR 04: Decide from the submission file, not from the brain

- **Status:** Accepted
- **Date:** 2026-10-25 (records decisions taken while specs 07, 10 and 13 were implemented)

## Context

A brain works in a box and hands back one submission
([architecture](../architecture.md#boxes-and-brains)). The specs of the box and
the brains assumed parts of agentkit-go that it does not have (a
`sandbox.Open(name)`, a network mode for "the host", a read tool that returns
exact bytes) and left open what the host does with a file a brain could have
forged. The code follows agentkit as it is, and the rules below are what it
does instead. Their common thread is that the host trusts the file it checked
and nothing the brain says.

## Decision

**The box.**

- `internal/box` keeps its own registry of providers by name (`podman`,
  `local`, and a per-attempt `gateway`). Every name of agentkit's `sandbox`
  module that is not a type is used in one file, `internal/box/adapter.go`. A
  provider that can refuse to be made, or that connects first, is made by
  `Entry.Build`; `Entry.Unconfined` marks one that puts no boundary around the
  tools (`local`, which raises `box_unconfined`), and `Isolates()` is its
  negation.
- The network is a string on the request, `none` or `open`, mapped onto the
  provider's modes by `networkFor`: the one asked for when the provider offers
  it, else the first it offers. `local` offers only an unrestricted network,
  and the ledger records `host` for it.
- The sync applies the hands' ignore rules, so a `.env` and build output stay
  out, and then sends verbatim the regular files that git tracks and a rule
  ignores, so git in the box sees what git on the host sees. A sync mirrors the
  host and removes git's own state in the box first, since the transfer takes
  a file of the same size and second for unchanged. The box's short `HEAD`
  matches the host's only for the same `core.abbrev`.
- A `--dir` whose `.git` is a file (a linked worktree, a submodule) is refused,
  because a commit in the box would write the repository it links to. Remote
  URLs, extra headers, credential helpers and URL rewrites are removed from the
  box's `.git/config` after the first sync and after every sync.
- The host reads the box with the list tool (at most 500 entries a directory)
  and the export of a single file (exact bytes, up to 32 MiB), never with the
  read tool, which shows capped lines and takes the box down on a file that is
  not UTF-8. A link is not followed, and transfers go one at a time because the
  box takes one stream.
- The failures of a box that is remote (`Entry.Remote`, the gateway) are
  retryable: a box out of reach is an outage, not a fault. Under `podman` a box
  that cannot run a command is a failure that is not retryable.
- When the provider was chosen by default, the `fix_hint` of a `sandbox`
  failure names `--sandbox` and `AF_SANDBOX`, and the failure's own hint (such
  as `AF_SANDBOX_IMAGE`) is told by the message.

**The brain.**

- The driver answers a call to a tool it does not know itself, before the
  gate, so the box's tools that the brief does not allow are in its toolset as
  stubs that deny with the allowlist's words. The model is not offered them and
  their guidelines are left out of the system prompt.
- `brain.Getenv` is `func(name string) (string, bool)`, the lookup agentkit's
  providers resolve credentials with, so an empty value is told from an unset
  one.
- A driver that runs as a process in a box submits under the nonce the outer
  `Execute` placed (`Request.Nonce`), and goes through `Execute` over
  `box.Here`, a session over the box's own directory in place. `af triage run`
  and `af fix run` do this only for a brief that `Execute` placed.
- The settings of the host reach a process in the box with their values,
  single-quoted in the command (locale, time zone, proxies, certificates),
  since the box's shell has not the host's environment; the box's own `HOME`
  and `TMPDIR` are referred to. A password in a proxy URL is therefore in the
  command's text; the credentials of `Spec.Env` never are.
- The box request treats `execute` and `run_command` as the tools that run a
  command, and a nil tool list as every tool: a brain that can run a command
  gets the network whichever tool it uses.
- An envelope of the driver whose model has no price keeps `cost_source`
  `none`.

**The submission.**

- The host writes `<run dir>/submission.json` from what `submission.Check`
  accepted (`submission.Marshal`), not by copying the box's file, so a second
  `payload` key or a field no kind has is not carried over. A run directory
  holds no `submission.json` that `af` did not accept: it is removed when an
  attempt starts and when one ends without a submission, since a `local` brain
  could write the host's files. `Check` reads at most 2 MiB of file and 1 MiB
  of payload, requires valid UTF-8, and turns a panic of the kind into a
  problem.
- A brain that panicked is decided like one that returned an error: a valid
  submission wins, with the warning `brain_error` and the stack kept; without
  one the failure is `brain`.
- Under a typed schema the two brains may leave different payload bytes, since
  the driver coerces a number given as a string and drops an optional `null`
  before the tool sees them. Both payloads are valid; `Kind.Validate` judges
  what arrives and the host checks the file again.

## Consequences

- Under `podman`, `Spec.Env` (the vendor key of a driver in the box) is on the
  engine's command line while the box lives, readable by other users of the
  machine. [configuration.md](../configuration.md) says so.
- A `local` box can be written by a stray process of its brain after the host
  decided; consumers take the payload from the run's result or check the file's
  hash against the ledger's `submission_accepted`.
- A change of agentkit's sandbox API has one file to follow in this
  repository.
