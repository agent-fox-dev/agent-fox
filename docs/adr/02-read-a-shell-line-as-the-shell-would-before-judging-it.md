# ADR 02: Read a shell line as the shell would before judging it

- **Status:** Accepted
- **Date:** 2026-10-25 (records decisions taken while specs 06 and 07 were implemented)

## Context

The tool policy (`internal/policy`, [architecture](../architecture.md#the-tool-policy))
decides whether a brain may make a tool call. Its spec had it tokenize a
command line itself, segment by segment, and bind the hands' guard as a
deny-only first layer through a function signature agentkit does not have. A
review against a real bash then found lines the policy allowed that the shell
ran as `git push` or as the suite. The rules below are what the policy does
instead; the table of its rules is in the architecture document.

## Decision

1. **The hands' reader reads the line.** The policy takes the commands that
   `guard.Commands` finds and expands the words where that can make a program
   name of them, as the shell would: `$'…'` and `$"…"`, `$IFS` and `${IFS}`,
   and braces. Quotes are removed before that expansion, so a word may be read
   as more than the shell makes of it, which can only deny more. A line the
   reader cannot read is `unparseable`.
2. **The hands' guard looks at the whole line, once, before the rules.** It is
   `guard.Restricted`, given the programs of the line as its allowlist, so it
   adds only what the policy lacks: an assignment that changes which program
   runs or what is loaded into it, and a line that runs no program. A line it
   refuses is denied as `hands_guard` even when a later rule would refuse it
   too; what it allows, the rules after it still judge. It is asked about the
   whole line because it cannot read a heredoc body or a `$(…)` alone.
3. **What cannot be read with certainty runs nothing.** A line over 64 KiB is
   `unparseable` (the guard's cost grows with the square of the length, and a
   call waits for it); so is a brace expansion of more than 4096 words or
   1 MiB, and a shell that reads its commands from its input, since what it
   runs cannot be read first.
4. **The suite is the suite whatever its options.** A command is compared with
   a suite form once every token that starts with `-`, the values of the
   options known to take one (`-timeout 5m`, `-j 4`) and the `NAME=value`
   words are removed. The wrappers that are skipped to find the program include
   `builtin` and `xargs` as well as `env`, `time`, `timeout`, `nice`, `nohup`,
   `command` and `exec`.
5. **An assignment in front of a program is its name and an `=`.** Its value
   is not read, and none of the rules read it.
6. **`guard.json` refuses a key it does not know.** The loader decodes it with
   unknown fields refused and reports a `config` failure, because a typo that
   dropped a protected path would be protection lost without a word.
7. **The policy is prevention, not a boundary.** The tool gate normalises a
   path as the box's tools will resolve it before the policy judges it (quotes,
   a leading `@`, a `file://` prefix, surrounding spaces). It cannot read what a
   program it allows goes on to write, nor decide a later call of a batch after
   the earlier ones ran, and the `local` box has no isolation. What holds is the
   box and the host's decision from the submission file
   ([ADR 04](04-decide-from-the-submission-file-not-from-the-brain.md)).

## Consequences

- `internal/policy/handsguard.go` is the one file that binds the hands' guard,
  so a change of agentkit's guard has one place to follow.
- Some lines the shell would run harmlessly are denied (a quoted brace, a word
  with `$IFS` in it); a brain rewrites them, or uses a write tool.
- `run_command`, which takes an argument list, is not a shell tool to the
  policy. It is one to the box request, which gives a brain that can run a
  command the network ([ADR 04](04-decide-from-the-submission-file-not-from-the-brain.md)).
