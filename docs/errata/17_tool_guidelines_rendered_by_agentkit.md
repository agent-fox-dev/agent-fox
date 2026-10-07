# Erratum 17: the tools' guidelines are rendered by AgentKit, not the tools note

**Relates to:** 17-REQ-1, 17-REQ-2, 17-REQ-3.4, 17-REQ-6.1, 17-REQ-7.1,
17-REQ-8.1 (`.specs/17_prefer_file_tools_over_shell`).

## What the spec says

`toolsNote` appends a `Tool guidelines:` section after the tool list, with
every registered tool's `PromptGuidelines` in sorted tool order, and adds
AgentKit's search-over-execute line once when the set has `search_files` and
a shell. The premise (PRD, "Why"): `prompt.Build` renders the tools'
guidelines only under its own system prompt, and every phase here supplies a
custom one, so without this the guidance never reached the model.

## What changed underneath

AgentKit dropped that premise (agentkit-go#75, its erratum
`custom_prompt_keeps_tool_guidelines.md`): a custom system prompt now keeps the
tools' guidelines, in a `Guidelines:` block right after it, and the shell
guidelines — the search-over-execute line among them — are added by the same
condition 17-REQ-2 states. With both renderers in place every guideline
reached the model twice, and `TestTS17_18_TheRunnerSendsTheGuidelines` counted
the search-over-execute line twice.

## What is implemented

`toolsNote` no longer renders guidelines. It keeps the tool list, the
no-shell sentence and the preference line (17-REQ-3, 17-REQ-4.1). The
guidelines the model sees are AgentKit's block, which follows the note
directly, so:

- the heading is `Guidelines:`, not `Tool guidelines:`;
- tools are visited in registration order (the phase's own tools, then the
  built-ins in `SelectTools` order), not sorted order — still deterministic for
  a given phase, which is what 17-REQ-6.1 protects;
- blanks are skipped, repeats appear once and the search-over-execute line
  appears exactly once with `search_files` and a shell, as 17-REQ-1.3, 1.4 and
  2 require;
- the preference line still precedes the guidelines (17-REQ-3.4).

The spec-17 tests assert these on the composed prompt (`prompt.Build` over the
phase text and the tools note), the shape 17-REQ-7.1 already tested end to
end.
