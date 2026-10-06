# Erratum: spec 16's documentation has the `code_search` column but no counts, and the review phases do not get `code_search`

Recorded because `.specs/16_indexed_code_search` reads as unmet in two places:
16-REQ-9.3 asks for regenerated baseline tables, and 16-REQ-2 asks for
`code_search` in every phase's grant.

## 16-REQ-9.3: no regenerated counts

**Spec:** the navigation baseline tables in `docs/development.md` are
regenerated "to show the effect of code_search on search_files call counts per
tool".

**Is:** every table has a `code_search` column after `find_symbol`
(`docs/development.md:324`), and the `jq` snippet extracts it
(`.tool_calls.code_search`, `docs/development.md:285`). Every count is still a
dash, as it was before this spec: spec 13's and spec 15's tables were never
filled ([erratum](13_navigation_baseline.md),
[erratum](15_navigation_baseline.md)), and a run needs a live model and its
credential, which this change did not have. There is therefore no measurement
that `search_files` is still a large share of navigation once the map and the
symbol tools are in, the precondition the spec gives for shipping the index at
all. A number made up to fill the table would be the figure later navigation
changes are compared to, and worse than none.

`TestTS16_29_NavigationBaselineHasCodeSearchColumn`
(`docs_codesearch_test.go:106`) checks the column in each tool's table and the
`jq` extraction; `TestTS15_16_NavigationBaselineHasSymbolColumns`
(`docs_symbols_test.go:123`) still checks that the dashes agree with the
`Measured at` line. What neither can check is the counts. The first person to
run the procedure with a model, on a commit without the index and on one with
it, fills both rows of every table, names the commit and the model on the
`Measured at` line, and removes this section.

## 16-REQ-2: the conformance review phases have no `code_search`

**Spec:** each tool's pipeline appends `code_search` to every phase's
`BuiltinTools` when the run has an index.

**Is:** every phase the four pipelines build gets it (`triage`; `fix`'s
`analyse` and `implement`; `spec`'s `prd`, `generate:*` and `architecture`;
`impl`'s `survey`, `repair`, `implement` and `resolve`). The `review` phase of
`fix` and `impl` is built by the shared conformance review, which is outside
this change's file list, with `agentrun.ReadOnlyFileTools` and `execute` only
(`internal/conform/review.go:217`). It keeps the six read tools. The
documentation says so: `docs/model-usage.md` lists `code_search` on every row
but the two `review` rows, and `TestTS16_28_ModelUsageListsCodeSearchAndDescribesIndex`
(`docs_codesearch_test.go:60`) checks both. Granting it there takes the index
as a field of `conform.ReviewInput` and one `append` at that line.
