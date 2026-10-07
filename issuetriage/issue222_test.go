package issuetriage

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// limitedClient is a forge with a small body limit.
type limitedClient struct {
	mockClient
	limit int
}

func (l *limitedClient) MaxBodyLength() int { return l.limit }

// Issue #222 (smaller): a body over the forge's limit is cut to fit before
// the write, with a marker and a warning, rather than refused by the forge
// after the paid analysis.
func TestABodyOverTheForgeLimitIsCutToFit(t *testing.T) {
	ws := newWorkspace(t)
	long := validIssue()
	long["root_cause"] = strings.Repeat("The cached token is returned without its expiry compared. ", 200)
	o := newOptions(t, ws, newRunner(t, ws, toolCall("c1", ToolFileIssue, long)))
	o.DryRun = false
	forge := &limitedClient{mockClient: mockClient{authenticated: true}, limit: 2000}
	o.Forge = forge
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := utf8.RuneCountInString(forge.capturedReq.Body); n == 0 || n > 2000 {
		t.Errorf("the forge was sent %d characters, limit 2000", n)
	}
	if !strings.Contains(got.Body, "cut to fit") {
		t.Errorf("the cut body does not say it was cut")
	}
	warned := false
	for _, w := range o.Run.Warnings() {
		warned = warned || w.Code == toolio.WarnIssueBodyTruncated
	}
	if !warned {
		t.Error("no issue_body_truncated warning")
	}
}

// Issue #222 (smaller): a cited path must be the repository's own spelling,
// relative to its root and in its exact case.
func TestACitedPathMustBeRelativeAndExactlyCased(t *testing.T) {
	ws := newWorkspace(t)
	for _, bad := range []string{"Session.go", filepath.Join(ws.Root, "session.go")} {
		runner := newRunner(t, ws,
			toolCall("c1", ToolFileIssue, validIssue(bad)),
			toolCall("c2", ToolFileIssue, validIssue("session.go")),
		)
		got, err := Run(context.Background(), newOptions(t, ws, runner))
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got.RejectedPathCalls != 1 || strings.Contains(got.Body, bad) {
			t.Errorf("%s: RejectedPathCalls=%d; it was accepted or rendered", bad, got.RejectedPathCalls)
		}
	}
}

// Issue #222 (smaller): Labels, a fact, lists labels only once the forge
// applied them.
func TestLabelsAreReportedOnlyWhenApplied(t *testing.T) {
	ws := newWorkspace(t)
	o := newOptions(t, ws, newRunner(t, ws, toolCall("c1", ToolFileIssue, validIssue())))
	o.Labels = []string{"af:fix"}
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(got.Labels) != 0 {
		t.Errorf("a dry run reports labels applied: %v", got.Labels)
	}

	o = newOptions(t, ws, newRunner(t, ws, toolCall("c1", ToolFileIssue, validIssue())))
	o.Labels, o.DryRun = []string{"af:fix"}, false
	o.Forge = &mockClient{authenticated: true, createErr: errors.New("500")}
	got, _ = Run(context.Background(), o)
	if got != nil && len(got.Labels) != 0 {
		t.Errorf("a failed create reports labels applied: %v", got.Labels)
	}

	o = newOptions(t, ws, newRunner(t, ws, toolCall("c1", ToolFileIssue, validIssue())))
	o.Labels, o.DryRun = []string{"af:fix"}, false
	o.Forge = &mockClient{authenticated: true}
	got, err = Run(context.Background(), o)
	if err != nil || len(got.Labels) != 1 {
		t.Errorf("a created issue: labels=%v err=%v", got.Labels, err)
	}
}

var _ issuex.Client = (*limitedClient)(nil)
