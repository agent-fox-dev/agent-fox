package specgen

import (
	"fmt"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// Next implements toolio.NextProvider (06-REQ-6.2, 06-REQ-6.3). It suggests
// impl on every written package that validates, in split order, and spec
// again on the same input when the split is unfinished. Every value is a fact the
// pipeline established — SpecDir, Validation.Valid, SplitPlan and the input's
// own origin — never anything a model wrote.
func (r *Result) Next() []toolio.Next {
	if r == nil {
		return nil
	}
	var out []toolio.Next

	pkgs := make([]Package, 0, 1+len(r.FollowOnSpecs))
	if r.SpecDir != "" {
		pkgs = append(pkgs, r.Package)
	}
	pkgs = append(pkgs, r.FollowOnSpecs...)
	var ready []Package
	for _, p := range pkgs {
		if p.Validation.Valid && p.SpecDir != "" {
			ready = append(ready, p)
		}
	}
	for _, p := range ready {
		why := "the package validates and is ready to implement"
		if len(ready) > 1 {
			why = fmt.Sprintf("the package validates and is ready to implement (%d of this run's packages are ready, in split order)", len(ready))
		}
		out = append(out, toolio.Next{Tool: "impl", Input: p.SpecDir, Flags: []string{}, Why: why})
	}

	if r.Resumable() {
		in := r.inputRef
		if in == "" {
			in = toolio.SameInputPlaceholder
		}
		out = append(out, toolio.Next{
			Tool: "spec", Input: in, Flags: []string{},
			Why: "the split is unfinished; running spec on the same input continues from the next scope",
		})
	}
	return out
}
