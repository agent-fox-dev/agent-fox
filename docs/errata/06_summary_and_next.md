# Erratum: three places where the code deliberately departs from spec 06

Recorded because `.specs/06_trim_and_chain_results` reads as unmet in three
criteria, and each departure was a decision, not an omission.

## 06-REQ-3.4: the summary view keeps the covered counts

**Spec:** under `--detail summary` a `spec` result's `traceability` is reduced to
`{criteria_uncovered, paths_uncovered, tests_unowned}`, "dropping
`traceability.criteria_covered` and `traceability.paths_covered`".

**Is:** `specgen/summary.go` keeps `criteria_covered` and `paths_covered` beside
the three gap lists. Every gap list is `omitempty`, so a package with no gaps
printed `{}`, which looks the same as a trace nobody computed (issue #57,
commit `675eb72`). The counts say a trace was computed and was clean.

## 06-REQ-6.2: `next[]` suggests `impl` on every package that validates

**Spec:** one `impl` entry, for "the first validating package's `spec_dir`".

**Is:** `specgen/next.go` suggests `impl` on every package this run wrote that
validates, in split order, each with a `why` that says how many are ready. A
split writes several packages, and a caller that is told about only the first
must discover the rest by itself (issue #57, commit `675eb72`). `docs/cli.md`'s
`next` table says so.

## 06-REQ-6.6: the ambiguity `next[]` entry equals `needs_human.resume` only when rendered

**Spec:** `fix`'s ambiguity entry has an `input` that is "character-for-character
identical to the envelope's own `needs_human.resume`".

**Is:** `needs_human.resume` is one command string (`fix <same input> --context
"<answer>"`), while a `next[]` entry has separate `tool`, `input` and `flags`
fields, so the two cannot be equal field by field. Both come from one helper,
`toolio.ResumeNext`, and the entry's `Next.Command()` — `tool`, `input` and
`flags` joined by spaces — is character-for-character `needs_human.resume`
(declared in PR #32's notes). The entry is the one `next[]` entry that is not
directly runnable as printed: its input is the placeholder `<same input>`, not
the argument, because a report can be 256 KB.
