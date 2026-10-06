package toolio

import (
	"github.com/agentfox/agentkit-go/codesearch"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
)

// newIndex builds the run's code-search index. It is a variable so that a
// test can stand in for codesearch.New (see SetIndexBuilder).
var newIndex = func(ws *tools.Workspace) (tools.Index, error) {
	return codesearch.New(ws, codesearch.Options{})
}

// runnerConfigHook, when set, adjusts the configuration the run's Runner is
// built from, after the index has been put on it. It is nil in production.
var runnerConfigHook func(*agentrun.Config)

// SetIndexBuilder replaces the function that builds the run's index, for a
// test of an entry point that runs the real shell. It returns the function
// that puts the previous builder back. It is not safe to call while a run is
// in flight.
func SetIndexBuilder(fn func(*tools.Workspace) (tools.Index, error)) (restore func()) {
	old := newIndex
	newIndex = fn
	return func() { newIndex = old }
}

// SetRunnerConfigHook sets a function that adjusts the configuration the run's
// Runner is built from, for a smoke test of an entry point that gives the real
// shell a scripted model in place of a vendor's. It returns the function that
// removes it.
func SetRunnerConfigHook(fn func(*agentrun.Config)) (restore func()) {
	old := runnerConfigHook
	runnerConfigHook = fn
	return func() { runnerConfigHook = old }
}

// openIndex builds the run's one code-search index (16-REQ-3.1) and returns it
// with the function that closes it. When the index cannot be built it returns a
// nil index, why, and a no-op closer, after a low code_search_unavailable
// warning: navigation never fails a run (16-REQ-3.4).
func (e execArgs) openIndex(ws *tools.Workspace) (tools.Index, string, func()) {
	idx, err := newIndex(ws)
	reason := ""
	switch {
	case err != nil:
		reason = err.Error()
	case idx == nil:
		reason = "the index builder returned no index"
	}
	if reason != "" {
		e.run.Warn(WarnCodeSearchUnavailable, "low",
			"code_search is unavailable and the run falls back to search_files: %s", reason)
		return nil, reason, func() {}
	}
	return idx, "", func() { _ = idx.Close() }
}
