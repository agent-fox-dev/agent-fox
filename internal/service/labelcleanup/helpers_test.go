package labelcleanup_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/labelcleanup"
)

const pat = "pat-of-widgets-0123456789"

// The paths of hub that a try of the cleanup of issue 42 calls.
const (
	record  = "/api/v1/workspaces/widgets"
	issue   = record + "/issues/42"
	labelsP = issue + "/labels"
)

type subs struct{ err error }

func (s subs) Token(context.Context, string) (string, error) { return pat, s.err }

// world is a fake hub with the workspace widgets, and a cleaner over it.
type world struct {
	hub     *fakehub.Hub
	cleaner *labelcleanup.Cleaner
}

func newWorld(t *testing.T) *world {
	w := &world{hub: fakehub.New(t)}
	w.hub.AddWorkspace(t, fakehub.Workspace{Slug: "widgets", PAT: pat})
	w.cleaner = &labelcleanup.Cleaner{Subscriptions: subs{}, Hub: hub.New(w.hub.URL, "test")}
	return w
}

func (w *world) issue(labels ...string) {
	w.hub.AddIssue("widgets", fakehub.Issue{Number: 42, Title: "issue 42", Labels: labels})
}

// labels are the labels of issue 42 now, sorted.
func (w *world) labels() []string {
	i, _ := w.hub.IssueOf("widgets", 42)
	return slices.Sorted(slices.Values(i.Labels))
}

// writes counts the label writes hub received for issue 42.
func (w *world) writes() int {
	n := 0
	for _, r := range w.hub.Requests() {
		if strings.HasPrefix(r.Path, labelsP) && r.Method != "GET" {
			n++
		}
	}
	return n
}

// fix is the cleanup of a fix job of issue 42 from the label af:fix, whose attempts set the labels.
func fix(set ...string) jobs.CleanupRequest {
	return jobs.CleanupRequest{Workspace: "widgets", JobID: "job_1", Issue: 42, RequestLabel: "af:fix", LabelsSet: set}
}

// fault is hub's error object with the status and the message.
func fault(status int, message, errorType string) fakehub.Fault {
	return fakehub.Fault{Status: status, Body: fmt.Sprintf(`{"error":{"code":%d,"message":%q,"error_type":%q}}`, status, message, errorType)}
}
