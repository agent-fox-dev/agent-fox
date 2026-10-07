# Erratum: spec 17's documentation has the `find_references` column but no counts

Recorded because `.specs/17_find_references_tool` reads as unmet in one place:
17-REQ-7 asks for regenerated baseline tables.

## 17-REQ-7: no regenerated counts

**Spec:** the navigation baseline tables in `docs/development.md` are
regenerated to show the effect of `find_references` on `search_files` and
`read_file` call counts per tool.

**Is:** every table has a `find_references` column after `find_symbol`
(`docs/development.md`), and the `jq` snippet extracts it
(`.tool_calls.find_references`). Every count is still a
dash, as it was before this spec: spec 13's, spec 15's and spec 16's tables
were never filled ([erratum](13_navigation_baseline.md),
[erratum](15_navigation_baseline.md),
[erratum](16_navigation_baseline.md)), and a run needs a live model and its
credential, which this change did not have. There is therefore no measurement
that `search_files` and `read_file` calls fall once `find_references` is
available, the effect the spec expects. A number made up to fill the table
would be the figure later navigation changes are compared to, and worse than
none.

`TestTS17_45_BaselineTablesHaveFindReferencesColumn`
(`docs_findrefs_test.go`) checks the column in each tool's table, the 12-cell
row count, the `jq` extraction, and that the dashes agree with the `Measured
at` line; `TestTS15_16_NavigationBaselineHasSymbolColumns`
(`docs_symbols_test.go`) still checks that the dashes agree with the
`Measured at` line. What neither can check is the counts. The first person to run the procedure with a model, on a commit without `find_references` and on one with it, fills both rows of every table, names the commit and the model on the `Measured at` line, and removes this erratum.
