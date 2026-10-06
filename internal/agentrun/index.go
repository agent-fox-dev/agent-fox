package agentrun

import (
	"fmt"
	"reflect"

	"github.com/agentfox/agentkit-go/tools"
)

// Index reports the code-search index this Runner was built with
// (Config.Index): the one its phases' code_search and find_symbol read. Nil
// when the run has none.
func (r *Runner) Index() tools.Index { return r.cfg.Index }

// RunIndex is the index a pipeline grants code_search for and invalidates
// after a tree change: the one the pipeline's Options carry (opt), or, when
// they carry none, the one each Runner it drives was built with.
//
// The shell builds the run's one index and puts it on both the Runner's
// Config and the tool's Options (16-REQ-1, 16-REQ-3.1). RunIndex is where the
// pipeline holds the two to being that one index: a pipeline that invalidated
// one index while its phases read another would hand the model results from a
// tree that no longer exists, so two different indexes are an error. A Runner
// without an index is not a conflict — its phases are simply offered no
// code_search, and SelectTools drops the name.
func RunIndex(opt tools.Index, runners ...*Runner) (tools.Index, error) {
	idx := opt
	for _, r := range runners {
		if r == nil || r.cfg.Index == nil {
			continue
		}
		if idx == nil {
			idx = r.cfg.Index
			continue
		}
		if !sameIndex(idx, r.cfg.Index) {
			return nil, fmt.Errorf("the pipeline's code-search index (%T) is not the one its runner was built with (%T)",
				idx, r.cfg.Index)
		}
	}
	return idx, nil
}

// sameIndex reports whether a and b are the same index. An implementation
// whose dynamic type cannot be compared (a value holding a slice or a map) has
// no identity == could check without panicking, so it is taken as different.
func sameIndex(a, b tools.Index) bool {
	ta, tb := reflect.TypeOf(a), reflect.TypeOf(b)
	if ta != tb || !ta.Comparable() {
		return false
	}
	return a == b
}
