# Erratum: spec 15's symbol backend is tree-sitter, not ctags

Recorded because `.specs/15_symbol_navigation_tools` describes a symbol
backend AgentKit no longer has. AgentKit replaced universal-ctags and its
anchored-line heuristics with in-process tree-sitter grammars (agentkit-go
`ec8a6a6`, "outline non-Go languages with tree-sitter instead of ctags") and
removed `tools.CtagsRunner`, `tools.ErrCtagsUnavailable` and
`outline.Options.Runner`.

## 15-REQ-3, 15-REQ-4, 15-PATH-2: the `symbol_backend` check

**Spec:** the `symbol_backend` preflight check's `detail` is `ctags` when
universal-ctags is installed and usable and `heuristics` when it is not.

**Is:** there is no external binary to find. Go is always outlined with
`go/ast`; the only thing that varies is whether AgentKit was built with cgo,
which brings the tree-sitter grammars for fourteen other languages. The
`detail` is `tree-sitter` when it was and `go-only` when it was not
(`internal/agentrun/symbols.go`). The probe outlines a small Python source with
AgentKit's own `outline.Outline` and reads the backend it reports, so it still
cannot diverge from what `file_outline` does. The check stays informational,
always `ok`, and left out when the detection fails.

## 15-REQ-6.2 / 15-REQ-7.3: the docs

**Spec:** `docs/model-usage.md` and `docs/cli.md` describe ctags as the
accelerator and heuristics as the fallback.

**Is:** both describe `go/ast` for Go and tree-sitter for the other languages
under cgo. `TestTS15_13_…`, `TestTS15_14_…` and
`TestTS15_15_CLIPreflightProseMentionsSymbolBackend` (`docs_symbols_test.go`)
check for the new terms.

## Issue #221: the repo map's outline runner

The repo map passed `tools.CtagsRunner(nil)` so it outlined non-Go files the
way `file_outline` did. With the runner gone, both call `outline.Outline` with
no backend options, so they agree by construction;
`TestTheMapOutlinesNonGoFilesLikeFileOutline` checks the map shows the
declarations `outline.Outline` returns for a Python file (and skips without
cgo).
