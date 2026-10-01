package toolio_test

import (
	"strings"
	"testing"
	"time"

	"github.com/agentfox/agentkit-go/core"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func cachedResult() agentrun.Result {
	return agentrun.Result{
		Name: "generate:tasks", Turns: 3, Elapsed: time.Second,
		Usage: core.Usage{InputTokens: 278, OutputTokens: 4000, CacheReadTokens: 90000,
			CacheWriteTokens: 10000, CostUSD: 1.5},
	}
}

func TestPhaseFromResultCarriesTheCacheFiguresAndScope(t *testing.T) {
	p := toolio.PhaseFromResult(cachedResult(), "widget_cache")
	if p.CacheReadTokens != 90000 || p.CacheCreationTokens != 10000 {
		t.Errorf("cache tokens = %d read, %d written", p.CacheReadTokens, p.CacheCreationTokens)
	}
	if p.InputTokens != 278 || p.Scope != "widget_cache" || p.Name != "generate:tasks" {
		t.Errorf("phase = %+v", p)
	}
}

func TestEnvelopeSumsTheCacheTokensOverPhases(t *testing.T) {
	run := toolio.NewRun("spec", "test")
	run.AddPhase(toolio.PhaseFromResult(cachedResult(), "a"))
	run.AddPhase(toolio.PhaseFromResult(cachedResult(), "b"))
	u := run.Envelope(toolio.ExitOK, nil, nil).Usage
	if u == nil || u.CacheReadTokens != 180000 || u.CacheCreationTokens != 20000 {
		t.Errorf("usage = %+v", u)
	}
}

func TestPhaseSummaryShowsCachedTokens(t *testing.T) {
	if got := toolio.PhaseSummary(cachedResult()); !strings.Contains(got, "(+100.0k cached)") {
		t.Errorf("summary = %q", got)
	}
	plain := cachedResult()
	plain.Usage.CacheReadTokens, plain.Usage.CacheWriteTokens = 0, 0
	if got := toolio.PhaseSummary(plain); strings.Contains(got, "cached") {
		t.Errorf("summary = %q, want no cache note without cache tokens", got)
	}
}

func TestPhaseFromResultCarriesTheTaskAndBlockedCalls(t *testing.T) {
	res := cachedResult()
	res.Task, res.Blocked = "4", 7
	p := toolio.PhaseFromResult(res, "")
	if p.Task != "4" || p.Blocked != 7 {
		t.Errorf("phase = %+v", p)
	}
}

func TestPhaseSummaryPluralisesBlockedTools(t *testing.T) {
	res := cachedResult()
	res.Blocked = 1
	if got := toolio.PhaseSummary(res); !strings.Contains(got, "1 tool blocked") {
		t.Errorf("summary = %q", got)
	}
	res.Blocked = 2
	if got := toolio.PhaseSummary(res); !strings.Contains(got, "2 tools blocked") {
		t.Errorf("summary = %q", got)
	}
}

func TestAddPhaseWarnsOnManyToolErrors(t *testing.T) {
	run := toolio.NewRun("spec", "test")
	few := toolio.PhaseInfo{Name: "prd", ToolErrors: map[string]int{"bash": 2}}
	run.AddPhase(few)
	if len(run.Warnings()) != 0 {
		t.Fatalf("a handful of errors warned: %v", run.Warnings())
	}
	many := toolio.PhaseInfo{Name: "generate:tasks", ToolErrors: map[string]int{"search_files/invalid_arguments": 6}}
	run.AddPhase(many)
	ws := run.Warnings()
	if len(ws) != 1 || ws[0].Code != toolio.WarnToolErrors || !strings.Contains(ws[0].Message, "search_files/invalid_arguments ×6") {
		t.Errorf("warnings = %+v", ws)
	}
	res := cachedResult()
	res.ToolErrors = map[string]int{"bash": 1}
	if p := toolio.PhaseFromResult(res, ""); p.ToolErrors["bash"] != 1 {
		t.Errorf("phase = %+v", p)
	}
}
