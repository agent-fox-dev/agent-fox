package codeimpl

import "github.com/agent-fox-dev/agentfox/internal/toolio"

// Next implements toolio.NextProvider (06-REQ-6.4, 06-REQ-6.5). A parked run
// suggests itself on the same spec; when the baseline was red and no repair
// was attempted it also suggests --repair. Everything it reads — Stage,
// SpecDir, Baseline, Repair — is a fact the pipeline established, not the
// model's account of its work.
func (r *Result) Next() []toolio.Next {
	if r == nil || r.Stage != "parked" {
		return nil
	}
	n := toolio.Next{
		Tool: "impl", Input: r.SpecDir, Flags: []string{},
		Why: "the run parked; re-running continues from the last landed task",
	}
	if len(r.Baseline.failing()) > 0 && r.Repair == nil {
		n.Flags = []string{"--repair"}
		n.Why = "the run parked with a red baseline; the baseline needs repairing first, then re-running continues from the last landed task"
	}
	return []toolio.Next{n}
}
