package worker

import (
	"encoding/json"

	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/worker/recovery"
)

// costRank orders the sources of a cost: the best that any run had is the source of the total.
var costRank = map[string]int{"": 0, brain.CostNone: 1, brain.CostReported: 2, brain.CostMeasured: 3}

// addSpend is the spend of two sets of brain runs.
func addSpend(a, b recovery.Spend) recovery.Spend {
	sum := recovery.Spend{
		CostUSD: a.CostUSD + b.CostUSD, CostSource: a.CostSource,
		InputTokens: a.InputTokens + b.InputTokens, OutputTokens: a.OutputTokens + b.OutputTokens, Turns: a.Turns + b.Turns,
	}
	if costRank[b.CostSource] > costRank[a.CostSource] {
		sum.CostSource = b.CostSource
	}
	return sum
}

// carry is the spend of a discarded resume that is carried over as already spent: the larger of what the
// journal records and what was carried before plus what the ledger of the removed run records, or what
// the journal records when that ledger does not read (removed is nil then).
func carry(recorded, carried recovery.Spend, removed *recovery.Spend) recovery.Spend {
	if removed == nil {
		return recorded
	}
	if sum := addSpend(carried, *removed); moreThan(sum, recorded) {
		return sum
	}
	return recorded
}

// moreThan says whether a is more spend than b: the larger cost, and between equal costs (a brain that
// reports none, or a source that is not priced) the more turns, and then the more tokens.
func moreThan(a, b recovery.Spend) bool {
	switch {
	case a.CostUSD != b.CostUSD:
		return a.CostUSD > b.CostUSD
	case a.Turns != b.Turns:
		return a.Turns > b.Turns
	}
	return a.InputTokens+a.OutputTokens > b.InputTokens+b.OutputTokens
}

// ledgerSpend is what the brain runs that a run directory records cost: the sum of its brain_end
// records. A ledger that does not read is an error.
func ledgerSpend(run *toolio.RunDir) (recovery.Spend, error) {
	entries, err := run.Ledger()
	if err != nil {
		return recovery.Spend{}, err
	}
	var total recovery.Spend
	for _, e := range entries {
		if e.Kind != "brain_end" {
			continue
		}
		var end struct {
			CostUSD      float64 `json:"cost_usd"`
			CostSource   string  `json:"cost_source"`
			Turns        int     `json:"turns"`
			InputTokens  int64   `json:"input_tokens"`
			OutputTokens int64   `json:"output_tokens"`
		}
		if json.Unmarshal(e.Data, &end) != nil {
			continue
		}
		total = addSpend(total, recovery.Spend{CostUSD: end.CostUSD, CostSource: end.CostSource, Turns: end.Turns, InputTokens: end.InputTokens, OutputTokens: end.OutputTokens})
	}
	return total, nil
}

// outcomeOf is the spend as the tally takes it, for the run that goes on from it.
func outcomeOf(s recovery.Spend) brain.Outcome {
	return brain.Outcome{CostUSD: s.CostUSD, CostSource: s.CostSource, Turns: s.Turns, InputTokens: int(s.InputTokens), OutputTokens: int(s.OutputTokens)}
}
