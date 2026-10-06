# Erratum: spec 13's navigation baseline names `issue` and has no numbers yet

Recorded because `.specs/13_tool_call_counts_and_relevant_files` reads as
unmet in 13-REQ-9 in two places. The first is a stale name in the spec; the
second is a measurement the change that added the procedure could not make.

## 13-REQ-9.1: the procedure runs `triage`, not `issue`

**Spec:** a procedure for running fixed inputs "per tool": `issue` (a stack
trace and a prose report), `fix`, `spec` and `impl`.

**Is:** the procedure in `docs/development.md` runs `triage` on the stack trace
and the prose report. The spec copied its tool list from PRD 06, written
before the triage tool was renamed: the program that reads a problem report
and files an issue is `triage` (`issuetriage/pipeline.go:108`,
`cmd/triage/main.go:100`). PRD 08 explains the rename and reuses the name
`issue` for a different program, one that exposes the forge client and runs
no model. It makes no tool calls, so a navigation baseline has nothing to
measure for it. `TestTS_13_33_NavigationBaselineSection`
(`docs_development_test.go:84`) checks the procedure for `triage`, `fix`,
`spec` and `impl`.

## 13-REQ-9.2: the tables have the rows but not the counts

**Spec:** one table per tool "recording the per-phase tool_calls from those
baseline runs".

**Is:** each table lists the tool's inputs and the phases it records under the
names it records them (`analyse` and `implement` for `fix`, `survey`, one
`implement` per task and `review` for `impl`), and every count is a dash. A
baseline run needs a live model and its credential, and the change that added
the procedure ran without either. A number made up to fill the table would be
worse than none: it is the figure later navigation changes are compared to.

What the change does provide is a procedure that can be run as written: the
inputs are files in `testdata/baseline/` rather than descriptions, the `fix`
defect is seeded by `testdata/baseline/fix/seed.patch`, and the `impl` input is
a package that validates. `TestTS_13_33_NavigationBaselineSection`
(`docs_development_test.go:40`) fails when an input goes missing, the seed
stops applying or the package stops validating. The first person to run the
procedure with a model replaces the dashes, names the commit and the model on
the `Measured at` line, and removes this section of the erratum.
