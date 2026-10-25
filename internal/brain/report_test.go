package brain

import (
	"math"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestTally_AddsTheAttemptsAndKeepsTheBestCostSource(t *testing.T) {
	reported := Outcome{CostUSD: 0.1, CostSource: CostReported, Turns: 1, InputTokens: 100, OutputTokens: 10}
	measured := Outcome{CostUSD: 0.2, CostSource: CostMeasured, Turns: 2, InputTokens: 200, OutputTokens: 20}
	free := Outcome{CostSource: CostNone, Turns: 4}
	for _, tc := range []struct {
		name     string
		outcomes []Outcome
		want     toolio.Usage
	}{
		{"nothing", nil, toolio.Usage{Brain: "x", CostSource: CostNone}},
		{"reported", []Outcome{reported, free}, toolio.Usage{Brain: "x", CostUSD: 0.1, CostSource: CostReported, Turns: 5, InputTokens: 100, OutputTokens: 10}},
		{"measured", []Outcome{reported, measured, {}}, toolio.Usage{Brain: "x", CostUSD: 0.3, CostSource: CostMeasured, Turns: 3, InputTokens: 300, OutputTokens: 30}},
	} {
		var tally Tally
		for _, o := range tc.outcomes {
			tally.Add(o)
		}
		got := tally.Usage("x")
		cost := got.CostUSD
		got.CostUSD = tc.want.CostUSD
		if got != tc.want || math.Abs(cost-tc.want.CostUSD) > 1e-9 {
			t.Errorf("%s: Usage = %+v (cost %v), want %+v", tc.name, got, cost, tc.want)
		}
	}
}

func TestTally_SpendReportsEachTotalInTurn(t *testing.T) {
	var tally Tally
	var totals []float64 // appended under the tally's lock
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			tally.Spend(Outcome{CostUSD: 1, CostSource: CostMeasured}, "x", func(u toolio.Usage) { totals = append(totals, u.CostUSD) })
		})
	}
	wg.Wait()

	for i := range totals {
		if totals[i] != float64(i+1) {
			t.Fatalf("the totals reported are %v, want 1 to 64 in order", totals)
		}
	}
	if len(totals) != 64 {
		t.Errorf("%d totals reported, want 64", len(totals))
	}
}
