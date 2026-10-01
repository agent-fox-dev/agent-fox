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
