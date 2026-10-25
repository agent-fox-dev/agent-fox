package poll

import (
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

func TestNoAnswer_IsRepliedOnceAndTheLabelConsumedWithNoAttempt(t *testing.T) {
	e := newEnv(t)
	id := e.waiting(42, jobs.StateBlocked)
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.cycle()
	want := "No answer found since the question; comment, then add the label `af:fix` again.\n\n" + marker(id, "no_answer") + "\n"
	if got := e.comments(42); len(got) != 2 || got[1] != want || len(e.labels(42)) != 0 || e.Jobs.count("Answer") != 0 {
		t.Fatalf("comments %q, labels %v: want the one reply %q, the label consumed and no answer", got, e.labels(42), want)
	}
	if infos := e.Log.Find("INFO", "no answer"); len(infos) != 1 || infos[0]["job"] != id {
		t.Errorf("the INFO records are %v", infos)
	}
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.cycle()
	if got := e.comments(42); len(got) != 2 || len(e.labels(42)) != 0 {
		t.Errorf("the second sighting: comments %q, labels %v; want no second reply and the label consumed", got, e.labels(42))
	}
}

func TestThreadCut_RepliesWithAfAndTheAPIAndMakesNoAttempt(t *testing.T) {
	for state, action := range map[jobs.State]string{jobs.StateBlocked: "answer", jobs.StateFailed: "retry"} {
		e := newEnv(t)
		id := e.waiting(42, state)
		e.comment(42, "alice", "use the greeting")
		e.Hub.AddLabel(slug, 42, "af:fix")
		e.Hub.Inject("GET", issuePath(42), fakehub.Fault{Status: 200, Body: e.threadBody(42, true, false)})

		e.cycle()

		got := e.comments(42)
		last := got[len(got)-1]
		for _, want := range []string{"could not read the whole thread", "`af jobs " + action + " " + id + "`", "POST /api/v1/jobs/" + id + "/" + action} {
			if !strings.Contains(last, want) {
				t.Errorf("%s: the reply %q lacks %q", action, last, want)
			}
		}
		if !strings.HasSuffix(last, marker(id, "thread_cut")+"\n") || len(e.labels(42)) != 0 || e.Jobs.count("Answer")+e.Jobs.count("Retry") != 0 {
			t.Errorf("%s: the reply %q, labels %v: want it to end with its marker, the label consumed and no attempt", action, last, e.labels(42))
		}
		if warns := e.Log.Find("WARN", ""); len(warns) != 1 || warns[0]["job"] != id || warns[0]["issue"] != float64(42) {
			t.Errorf("%s: the warnings are %v, want one that names the job and the issue", action, warns)
		}
	}
}

func TestReply_ThatCannotBePostedIsAWarningAndTheLabelIsConsumed(t *testing.T) {
	e := newEnv(t)
	e.waiting(42, jobs.StateBlocked)
	e.Hub.AddLabel(slug, 42, "af:fix")
	e.Hub.Inject("POST", issuePath(42)+"/comments", fakehub.Fault{Status: 503})
	e.cycle()
	if len(e.Log.Find("WARN", "could not be posted")) != 1 || !slices.Equal(e.removals(), []string{"remove(42,af:fix)"}) {
		t.Errorf("logs %s, removals %v; want one WARN and the label removed", e.Log, e.removals())
	}
}
