package worker

import "fmt"

// stageAbort is how a delivery that a test stopped at a stage returns: the stage it was stopped after.
// A real process that dies at a stage returns nothing at all; a test cannot kill its own process, so
// the delivery returns at once instead, without the mirror, the release of the lease, the report or the
// cleanup, which are what a death skips.
type stageAbort struct{ stage string }

func (e stageAbort) Error() string {
	return fmt.Sprintf("worker: the delivery was stopped after %s", e.stage)
}
