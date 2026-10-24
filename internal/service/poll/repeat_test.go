package poll

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
)

// TestARepeatedSightingOfOneLabelAddsNoAttemptAndPostsNoComment_TS_19_11 verifies TS-19-11, a property over the jobs a
// sighting serves (blocked and failed, made by the poll with af:fix and made by the API with no request label), with a
// runner that ends the new attempt blocked or failed at once: exactly one attempt follows the first sighting, the later
// cycles add none, and no reply is posted.
// Verifies: 19-REQ-1.8
func TestARepeatedSightingOfOneLabelAddsNoAttemptAndPostsNoComment_TS_19_11(t *testing.T) {
	for _, state := range []jobs.State{jobs.StateBlocked, jobs.StateFailed} {
		for _, label := range []string{"af:fix", ""} {
			t.Run(fmt.Sprintf("%s, %q", state, label), func(t *testing.T) {
				step := blockedStep()
				if state == jobs.StateFailed {
					step = fakerunner.Failed("sandbox", false)
				}
				e := newEnv(t, withRunner(fakerunner.New(step, step).Then(fakerunner.Block(fakerunner.Step{}))))
				e.issue(42)
				src := jobs.Source{Kind: "api", By: "user-1"}
				if label != "" {
					src = jobs.Source{Kind: "label", By: "label af:fix"}
				}
				doc, err := e.Jobs.Create(ctx, jobs.CreateRequest{Workspace: slug, Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage("42")}, Source: src, RequestLabel: label})
				if err != nil {
					t.Fatal(err)
				}
				e.waitState(doc.ID, state)
				kind := map[jobs.State]string{jobs.StateBlocked: "question", jobs.StateFailed: "failure"}[state]
				e.comment(42, "agent-fox", fmt.Sprintf("<!-- agent-fox job=%s attempt=1 kind=%s -->", doc.ID, kind))
				e.comment(42, "alice", "here is what you need")
				e.Hub.AddLabel(slug, 42, "af:fix")
				comments := e.comments(42)

				e.cycle()
				e.waitState(doc.ID, state) // the new attempt ended at once
				e.cycle()
				e.cycle()

				if n := len(e.doc(doc.ID).Attempts); n != 2 {
					t.Errorf("%d attempts, want exactly one after the first", n)
				}
				if n := e.comments(42) - comments; n != 0 {
					t.Errorf("%d comments were posted, want none", n)
				}
				if e.jobsFor(42) != 1 {
					t.Errorf("%d jobs for 42, want the one", e.jobsFor(42))
				}
			})
		}
	}
}
