package repomap

import (
	"context"

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// Source hands each phase of fix or impl its repository map and turns a build
// failure into a warning. It wraps a Refresher, so the first phase builds and a
// later one rebuilds only when the tree changed (14-REQ-9.1, 14-REQ-9.2).
type Source struct {
	refresh *Refresher
	run     *toolio.Run
}

// NewSource returns the Source of one run. build and injected are the test
// seams of the run's Options: a nil build means Build, and a nil injected
// TreeState means git, the run's own wrapper.
func NewSource(ws *tools.Workspace, budget int, build BuildFunc, injected, git TreeState, run *toolio.Run) *Source {
	state := git
	if injected != nil {
		state = injected
	}
	return &Source{refresh: NewRefresher(ws, budget, build, state), run: run}
}

// Get returns the map for the named phase, "" when there is none. It never
// fails the run: navigation is an optimisation, so on an error the phase runs
// without a map and a low warning records why (14-REQ-10.1).
func (s *Source) Get(ctx context.Context, phase string, inputPaths []string) string {
	m, err := s.refresh.Get(ctx, inputPaths)
	if err != nil {
		s.run.Warn(toolio.WarnRepoMapBuildFailed, "low",
			"the repository map could not be built, so the %s phase runs without it: %v", phase, err)
		return ""
	}
	return m
}
