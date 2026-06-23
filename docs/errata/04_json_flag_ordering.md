# Errata: --json flag ordering in CLI invocations

**Spec:** 04_af_agentic_cli
**Requirements:** 04-REQ-2.5, 04-REQ-2.6, 04-REQ-3.6, 04-REQ-3.7
**Test Spec:** TS-04-8, TS-04-9, TS-04-14, TS-04-15, TS-04-16

## Divergence

The spec test pseudocode uses `['standup', '--json']` and similar patterns
where `--json` follows the subcommand name. In the implementation,
`--json` is a **group-level** flag defined on the root Click group
(`af/app.py`), not on individual subcommands. Click parses group options
before the subcommand name, so the correct invocation order is:

```
af --json standup        # correct: --json parsed by group
af standup --json        # error: standup subcommand has no --json option
```

## Implemented behavior

All test invocations were updated to place `--json` before the subcommand:

- `['--json', 'standup']` instead of `['standup', '--json']`
- `['--json', 'init']` instead of `['init', '--json']`
- `['--json', 'code']` instead of `['code', '--json']`
- `['--json', 'night-shift']` instead of `['night-shift', '--json']`

The `--json --help` case is special: `AgentFoxGroup.invoke()` intercepts
`--json` in the remaining args alongside `--help`, so both
`['standup', '--json', '--help']` and `['--json', 'standup', '--help']`
work correctly.

## Rationale

The `--json` flag is intentionally a group-level option so that it:
1. Applies uniformly to all subcommands via `OutputManager`
2. Integrates with `AF_AGENT=1` environment variable detection
3. Controls error envelope routing in `AgentFoxGroup`

Individual subcommands do not need to declare their own `--json` option.

## Test coverage

All affected tests updated with correct flag ordering:
- TS-04-8, TS-04-9: standup and init JSON output
- TS-04-14, TS-04-15, TS-04-16: JSONL streaming separation
- TS-04-P1, TS-04-P4: property tests for JSON mode
- TS-04-SMOKE-1, TS-04-SMOKE-2: smoke tests
