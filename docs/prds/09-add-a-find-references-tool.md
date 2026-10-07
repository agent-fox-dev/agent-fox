# Add a find_references tool

Status: **active**. Mechanism in AgentKit, policy here, as
[ADR 07](../adr/07-split-code-navigation-between-agentkit-and-agent-fox.md)
decides for every navigation item. Follows AgentKit
[PRD 04](https://github.com/agent-fox-dev/agentkit-go/blob/main/docs/prd/04-add-symbol-navigation-tools.md)
(`file_outline`, `find_symbol`) and is independent of
[PRD 05](https://github.com/agent-fox-dev/agentkit-go/blob/main/docs/prd/05-add-an-indexed-code-search-module.md)
(`code_search`).

## Intent

Every phase of every tool can now ask what a file declares (`file_outline`)
and where a name is declared (`find_symbol`), and will soon be able to search
ranked text (`code_search`). None of them answers the third question a
reader of unfamiliar code asks: **who calls this?** AgentKit PRD 04 named
that a non-goal and left it to `search_files` with `\bX\b`. That answer is
weak in three ways a model pays for in turns:

- It returns declarations, comments, strings and same-named identifiers from
  other packages alongside the calls, and the model reads files to tell them
  apart.
- It says which *line* mentions the name, not which *function* is calling. A
  caller is a declaration, and attributing a hit to its enclosing declaration
  is exactly what the outline already knows and the regex does not.
- It cannot see a call through a receiver, an interface method or a
  renamed import, which is where the behaviour that matters in a harness
  tends to live.

The Harness Handbook paper (Wang et al., arXiv 2607.13285) measured where a
planner with a behaviour-organized map of a harness beat one without: the
largest gains were on requests whose implementation sites are scattered
across modules and on paths keyword search does not reach. The map's first
construction step is a program graph whose edges are **call relations**. That
is the one program fact AgentKit's tools cannot produce today. This PRD adds
it, so that `fix`'s analysis, `impl`'s survey and `spec`'s PRD phase can
answer "what depends on this" in one call, and so that a later repository
map or handbook phase can build a call graph in Go without asking the model.

## Goals

- A model can list the call and reference sites of a declaration across the
  workspace in one call, each site attributed to the declaration that
  contains it, ranked so that resolved calls come first and text matches
  last.
- For Go, the answer is exact within the workspace: a method call through a
  receiver, an interface method and a renamed import resolve to the right
  declaration, and a same-named identifier in another package does not.
- For every other language, the answer is at least as good as `search_files`
  and better in one way: every hit names its enclosing declaration.
- An embedder can get the same data without a model, through an exported
  function, so agent-fox can compute call edges for a repository map.
- AgentKit's root module stays standard-library-only and cgo-free. Go
  resolution uses `go/parser` and `go/types`; nothing from `golang.org/x/tools`.
- On the navigation baseline of [PRD 06](06-stop-re-reading-the-codebase-every-phase.md),
  `search_files` calls in `fix`'s `analyse` and `impl`'s `survey` fall, with
  no regression in those tools' outcomes.

## Non-goals

- A language server client. Resolution is a Go type-check over workspace
  sources with external imports stubbed; it is not `gopls`.
- Exact resolution for languages other than Go. ctags and the heuristic
  outline give enclosing declarations; they do not give types.
- Cross-module resolution. A call from the workspace into a dependency, or
  from a dependency into the workspace, is out of scope. The question is
  always "who, in this repository, uses this".
- A persistent index. The reference table lives as long as the agent's
  tool set, like the symbol table (AgentKit PRD 04 §4.3).
- Changing `find_symbol`, `search_files` or the repository map's contents.
  Call edges in the map are a later PRD, once this one has numbers.

## Functional requirements

### 1. AgentKit: the `find_references` tool

```
find_references(name: string, path?: string, kind?: string, include_tests?: bool, max_results?: int)
```

- `name` is a declaration name, optionally qualified as `Container.Name`
  (`Runner.Run`). It is resolved through the symbol table first; when it
  matches no declaration, the tool answers with text matches only and says
  so in the header.
- `path` restricts the search to a subdirectory, as it does on `find_symbol`.
  `kind` filters the *referenced* declaration when the name is ambiguous
  (`func` vs `type`). `include_tests` defaults to true; false drops hits in
  files the language's test convention names.
- `max_results` defaults to 30 and is capped at 100. A truncated result
  carries the REQ-TOOL-09 marker naming `max_results`.
- Results are grouped by file and rendered as compact text, the
  `find_symbol` precedent:

  ```
  find_references Runner.Run  (go/types, 7 references in 4 files; 0 text matches)
  codefix/phases.go
    L212  resolved  func runAnalysis(...)        res, err := r.Run(ctx, phase)
    L301  resolved  func runImplement(...)       res, err := r.Run(ctx, phase)
  internal/agentrun/phase_test.go
    L88   resolved  func TestRunRejectsUnknown   _, err := r.Run(ctx, p)
  ```

  Each site carries the line, a confidence, the enclosing declaration from
  the outline (or `<file>` when the hit is at top level), and the trimmed
  source line, capped at 200 bytes and stripped of control characters.
- `Data` carries the structured sites for the embedder: path, line, column,
  confidence, enclosing `outline.Decl`, and the referenced declaration.
- `PromptGuidelines`: *"Use find_symbol for where a name is declared,
  find_references for who uses it, and search_files for text."*

### 2. Confidence, and the three backends behind it

Every site carries one of three confidence values, and the header counts
each. Confidence is the honest version of PRD 04's "exact parity between
backends" non-goal: the tool does not pretend one answer, it labels them.

| Confidence | Backend | Languages | What it means |
|---|---|---|---|
| `resolved` | `go/types` | Go | The identifier at this position type-checks to the declaration named. Receiver calls, interface methods, renamed imports and embedded fields resolve. Same-named identifiers that resolve elsewhere are excluded. |
| `lexical` | symbol table + tokenizer | every language the outline covers | An identifier-boundary match of the name, in a non-comment, non-string position where the backend can tell, attributed to the enclosing declaration from the outline. |
| `text` | `search_files`'s matcher | everything else | A `\bname\b` match in a file the outline could not parse, or in a comment or string. |

**Go resolution.** The workspace's Go packages are parsed with `go/parser`
and checked with `go/types` using a workspace-local importer: a package
under the workspace is checked from source; any other import path (standard
library included) is answered with a stub package that defines nothing,
`Config.Error` collects rather than aborts, and `FakeImportC` is set. The
check is therefore incomplete by design and never fails the call. An
identifier that cannot be resolved because its type came from outside the
workspace is reported as `lexical`, not dropped. Build-tag variants are
checked under the host's default tags only. The checked packages are cached
on the tool set and invalidated the way the symbol table is (§4).

**Candidate search.** Both lower backends start from the files that mention
the name. When `tools.Options.Index` is set (PRD 05), the candidate list
comes from the index; otherwise from the same walk `search_files` uses.
Either way the result is the same set, which the `search_files` suites
already establish for rg versus native.

### 3. Ranking and bounds

- Order: `resolved` before `lexical` before `text`; within a confidence,
  non-test files before test files, then path, then line. Deterministic.
- `name` is at most 256 bytes. `kind` is a closed set. A `path` outside the
  workspace is refused before any walk, with `read_file`'s codes.
- The type-check is bounded by `SymbolOptions.MaxFiles` and
  `SymbolOptions.MaxDuration` (50 000 files, 2 s by default). When a bound
  is hit, the result carries `partial: true` and a note to narrow with
  `path`, and the sites found so far are still returned, downgraded to
  `lexical` where the check did not finish.

### 4. Freshness

The reference table shares the symbol table's freshness rules (AgentKit PRD
04 §4.3): `write_file` and `edit_file` mark the written path dirty;
`execute`, `run_command` and `powershell` mark the whole table; a dirty Go
package is re-checked before the next answer. agent-fox builds a fresh tool
set per phase, so the changes Go makes between phases (checkout, reset,
gate runs) are covered the way they are for `find_symbol`.

### 5. AgentKit: the embedder seam

```go
func (ws *Workspace) References(ctx context.Context, target outline.Decl, opts ReferenceOptions) (ReferenceResult, error)
```

exported from `tools`, returning the same sites `find_references` renders,
so agent-fox can enumerate callers for a repository map or a handbook phase
without a model turn. It is the only new exported API; the type-checker and
tokenizer stay unexported.

### 6. agent-fox: wiring and policy

- `agentrun.ReadOnlyFileTools` gains `find_references`. The six read tools
  become seven, in every phase of `triage`, `fix`, `spec` and `impl`. The
  tool is read-only and reports no write capability, so `AssertReadOnly`
  and the shell guard are unchanged. The docs and system prompts that count
  the read tools are updated.
- `impl`'s survey prompt, step 2 ("Read the file it lives in, its callers,
  and its tests"), and `fix`'s analysis prompt name the tool where they say
  "callers". A model that is not told has to discover it.
- `--preflight` reports, for all four tools, whether the workspace's Go
  packages type-check under the bounds (a count of checked packages and of
  collected errors), beside the existing ctags line.
- `usage.phases[].tool_calls` already counts the new tool with no change
  (PRD 06 A).
- The navigation baseline tables in `docs/development.md` gain a row for
  this change, per tool, so the fall in `search_files` and `read_file` calls
  is a number in review.

### 7. Order

1. AgentKit: §1 to §5 as one spec, after `02_symbol_navigation_tools` is
   merged (it is), independent of `03_indexed_code_search`.
2. agent-fox: §6, after the AgentKit spec is merged and the `replace`
   target carries it.
3. agent-fox PRD 06's repository map (spec `14_repo_map`) is not blocked.
   Whether its ranking should use call edges from §5 is decided on this
   PRD's numbers, as PRD 06 says.

## Acceptance criteria

- On AgentKit's own repository, `find_references Runner.Run` returns every
  call of `(*Runner).Run` as `resolved`, attributed to its enclosing function,
  and no hit for an unrelated `Run` method on another type.
- A call through an interface value and a call through a renamed import
  both resolve. A fixture pins each.
- On a Python fixture with ctags present, every hit carries its enclosing
  `def` or `class`; with ctags absent, the heuristic backend gives the same
  enclosing declarations for top-level functions.
- A name that matches no declaration returns `text` sites only, and the
  header says so.
- A 60 000-file synthetic tree returns `partial: true` within the deadline,
  with the sites found so far.
- Editing a caller through `edit_file` changes the next answer; deleting it
  through `execute` removes the site.
- `internal/policy` stays green: no new module, no cgo, all cross-targets.
- In agent-fox, every phase of all four tools registers the tool, the
  read-only phases pass `AssertReadOnly`, and `--repo-map-tokens 0` prompts
  differ from today's only by the added tool and the sentence naming it.
- The baseline tables show the before and after counts per tool.

## Documentation

- AgentKit: `docs/api.md` (the `References` seam), README tool list and
  *What is not built* (callers move out of it), `docs/GAPS.md`.
- agent-fox: `docs/cli.md` (the seven read tools in each tool's *What the
  model may and may not do*, the `--preflight` line), `docs/model-usage.md`
  (*Reading the codebase*, the phase table), `docs/development.md` (the
  baseline rows), and this file's row in [`README.md`](README.md).
