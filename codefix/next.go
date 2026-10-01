package codefix

import "github.com/agent-fox-dev/agentfox/internal/toolio"

// Next implements toolio.NextProvider (06-REQ-6.6, 06-REQ-6.7). A run that
// lands suggests nothing. A run that stopped on an ambiguity suggests fix
// again with --context, built by the same helper needs_human.resume uses, so
// the two render character for character alike. It reads only Ambiguity,
// which this package set when it stopped — the entry is deliberately not
// runnable as printed, because the placeholder stands for a report that may
// be too large to repeat.
func (r *Result) Next() []toolio.Next {
	if r == nil || r.Ambiguity == nil {
		return nil
	}
	return []toolio.Next{toolio.ResumeNext("fix", "the run stopped on an ambiguity; run fix again with the answer as --context")}
}
