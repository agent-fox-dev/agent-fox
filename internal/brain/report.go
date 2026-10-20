package brain

import (
	"sync"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// Report is what Execute reports to: the events, warnings and artifacts of the
// run, and its usage. *toolio.Run is one.
type Report interface {
	Emit(typ string, data any)
	Warn(code, message string)
	Artifact(a toolio.Artifact)
	SetUsage(u toolio.Usage)
}

var _ Report = (*toolio.Run)(nil)

// Tally accumulates what the brains of the attempts of a run cost, so that the
// envelope reports the run and not the last attempt. The zero Tally is empty and
// is safe for concurrent use.
type Tally struct {
	mu                 sync.Mutex
	cost               float64
	turns              int
	input, output      int
	measured, reported bool
}

// Add adds the outcome of an attempt, one that failed included: the cost was
// spent.
func (t *Tally) Add(o Outcome) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cost += o.CostUSD
	t.turns += o.Turns
	t.input += o.InputTokens
	t.output += o.OutputTokens
	switch o.CostSource {
	case CostMeasured:
		t.measured = true
	case CostReported:
		t.reported = true
	}
}

// Usage is the total, as the envelope reports it, for the brain of the name. The
// source of the cost is the best any attempt had: measured, then reported, then
// none.
func (t *Tally) Usage(brainName string) toolio.Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	source := CostNone
	switch {
	case t.measured:
		source = CostMeasured
	case t.reported:
		source = CostReported
	}
	return toolio.Usage{
		Brain: brainName, CostUSD: t.cost, CostSource: source,
		InputTokens: int64(t.input), OutputTokens: int64(t.output), Turns: t.turns,
	}
}
