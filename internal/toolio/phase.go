package toolio

import (
	"fmt"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
)

// PhaseFromResult is the envelope's record of one finished phase. Every
// pipeline builds it here so that none can drop a figure the others report.
//
// The model API's input count is net of the prompt cache, so on a run that
// caches well it is a few hundred tokens beside a cost in dollars; the cache
// figures are what make the two agree. scope names the spec the phase worked
// on and may be empty.
func PhaseFromResult(res agentrun.Result, scope string) PhaseInfo {
	return PhaseInfo{
		Name:                res.Name,
		Scope:               scope,
		Task:                res.Task,
		Blocked:             res.Blocked,
		ToolErrors:          res.ToolErrors,
		Turns:               res.Turns,
		StopReason:          string(res.StopReason),
		InputTokens:         res.Usage.InputTokens,
		OutputTokens:        res.Usage.OutputTokens,
		CacheReadTokens:     res.Usage.CacheReadTokens,
		CacheCreationTokens: res.Usage.CacheWriteTokens,
		CostUSD:             res.Usage.CostUSD,
		DurationMS:          res.Elapsed.Milliseconds(),
	}
}

// PhaseSummary is the footer a phase prints when it ends: turns, tokens in
// and out, the cached tokens when there were any, and the tools the guard
// refused.
func PhaseSummary(res agentrun.Result) string {
	s := fmt.Sprintf("· %d turns · %s↑", res.Turns, FormatTokens(res.Usage.InputTokens))
	if cached := res.Usage.CacheReadTokens + res.Usage.CacheWriteTokens; cached > 0 {
		s += fmt.Sprintf(" (+%s cached)", FormatTokens(cached))
	}
	s += fmt.Sprintf(" %s↓", FormatTokens(res.Usage.OutputTokens))
	if res.Blocked > 0 {
		noun := "tools"
		if res.Blocked == 1 {
			noun = "tool"
		}
		s += fmt.Sprintf(" · %d %s blocked", res.Blocked, noun)
	}
	return s
}
