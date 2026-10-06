# Erratum: spec 16's documentation has the `code_search` column but no counts

Recorded because `.specs/16_indexed_code_search` reads as unmet in one place:
16-REQ-9.3 asks for regenerated baseline tables.

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
