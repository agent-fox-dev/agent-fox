package codeimpl

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// resolvedFixture is issue #191's run: task 2 declares 09-REQ-2.2 (with
// TS-09-5) unmet, the first review finds it missing, the resolve phase lands
// a change for it, and the review after that change answers with final.
func resolvedFixture(t *testing.T, final func(conform.Review) conform.Review) (Options, *issueForge) {
	t.Helper()
	o := implOriginFixture(t, filepath.Join(t.TempDir(), "origin.git"))
	forge := &issueForge{mockAuthClient: o.Forge.(*mockAuthClient)}
	o.Forge = forge
	b := o.brain.(*scriptedBrain)
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		sub, err := goodWork(root, task, attempt)
		if task.Id == 2 {
			sub.Deviations = []conform.Deviation{{Key: "09-REQ-2.2", Test: "TS-09-5",
				Reason: "the host library cannot emit the trailing newline the requirement asks for"}}
		}
		return sub, err
	}
	reviews := 0
	b.review = func(in conform.ReviewInput) (conform.Review, error) {
		reviews++
		r := conformingReview(in.Scope)
		if reviews == 1 {
			for i := range r.Requirements {
				if r.Requirements[i].ID == "09-REQ-2.2" {
					r.Requirements[i].Status = conform.StatusMissing
				}
			}
			return r, nil
		}
		return final(r), nil
	}
	b.resolve = func(root string, in resolveInput) (ResolveSubmission, error) {
		write(t, root, "task2.go", "package x // task 2, with the trailing newline\n")
		return ResolveSubmission{Summary: "Emitted the trailing newline.", CommitSubject: "emit the trailing newline",
			Changes: []FileChange{{Path: "task2.go", Change: "modified"}}}, nil
	}
	return o, forge
}

// Issue #191: a deviation a task declared, which the resolve phase then fixed
// and the review after it found implemented, is not reported as unmet and is
// not filed as an issue.
func TestADeclarationTheResolvedReviewFindsMetIsRetracted(t *testing.T) {
	o, forge := resolvedFixture(t, func(r conform.Review) conform.Review { return r })
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Resolve == nil || got.Resolve.Commit == "" {
		t.Fatalf("the resolve phase's change did not land: %+v", got.Resolve)
	}
	if len(got.Unmet) != 0 {
		t.Errorf("Unmet = %+v, want none: the review after the resolve found 09-REQ-2.2 implemented", got.Unmet)
	}
	if len(forge.issues) != 0 {
		t.Errorf("issues filed = %+v", forge.issues)
	}
	if hasWarning(o.Run, toolio.WarnUnmetRequirements) {
		t.Error("unmet_requirements warned for a retracted declaration")
	}
}

// A declaration the review after the resolve finds only partly met stands:
// its requirement is implemented, its test is still weaker.
func TestADeclarationThatIsPartlyMetStands(t *testing.T) {
	o, forge := resolvedFixture(t, func(r conform.Review) conform.Review {
		for i := range r.Tests {
			if r.Tests[i].ID == "TS-09-5" {
				r.Tests[i].Assessment = conform.AssessWeaker
			}
		}
		return r
	})
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var declared int
	for _, u := range got.Unmet {
		if u.Source == conform.SourceDeclared && u.Requirement == "09-REQ-2.2" {
			declared++
		}
	}
	if declared != 1 || len(forge.issues) != 1 {
		t.Errorf("Unmet = %+v, issues = %d: the declaration should stand and be filed", got.Unmet, len(forge.issues))
	}
}

// Two tasks declaring the same key are one unmet item, with the later reason,
// and one item in the issue.
func TestDuplicateDeclarationsAreOneItem(t *testing.T) {
	o := implOriginFixture(t, filepath.Join(t.TempDir(), "origin.git"))
	forge := &issueForge{mockAuthClient: o.Forge.(*mockAuthClient)}
	o.Forge = forge
	b := o.brain.(*scriptedBrain)
	b.implement = func(root string, task afspec.Task, attempt int) (Submission, error) {
		sub, err := goodWork(root, task, attempt)
		switch task.Id {
		case 2:
			sub.Deviations = []conform.Deviation{{Key: "09-REQ-2.2", Test: "TS-09-5", Reason: "the first reason"}}
		case 3:
			sub.Deviations = []conform.Deviation{{Key: "09-req-2.2", Reason: "the later reason"}}
		}
		return sub, err
	}
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got.Unmet) != 1 || got.Unmet[0].What != "the later reason" || got.Unmet[0].Test != "TS-09-5" {
		t.Fatalf("Unmet = %+v, want one item with the later reason and the earlier test", got.Unmet)
	}
	if len(forge.issues) != 1 || forge.issues[0].Title != "Spec 09: 09-req-2.2 is not met" {
		t.Errorf("issues = %+v", forge.issues)
	}
}

// A review row answers for the id it names and for that id's sub-criteria,
// and a declaration is met only when every id it names that the review
// answers is met, and at least one is.
func TestDeclarationMetByReview(t *testing.T) {
	r := conform.Review{
		Requirements: []conform.RequirementRow{{ID: "16-REQ-3", Status: conform.StatusImplemented},
			{ID: "16-REQ-2", Status: conform.StatusPartial}},
		Tests:     []conform.TestRow{{ID: "TS-16-14", Assessment: conform.AssessAssertsContract}, {ID: "TS-16-34", Assessment: conform.AssessWeaker}},
		Decisions: []conform.DecisionRow{{ID: "D-8", Status: conform.DecisionFollowed}},
	}
	for _, c := range []struct {
		d    conform.Deviation
		want bool
	}{
		{conform.Deviation{Key: "16-REQ-3.1"}, true},
		{conform.Deviation{Key: "16-req-3"}, true},
		{conform.Deviation{Key: "16-REQ-3", Test: "TS-16-14"}, true},
		{conform.Deviation{Key: "TS-16-14"}, true},
		{conform.Deviation{Key: "D-8"}, true},
		{conform.Deviation{Key: "16-REQ-3", Test: "TS-16-34"}, false},
		{conform.Deviation{Key: "16-REQ-2"}, false},
		{conform.Deviation{Key: "16-REQ-30"}, false},
		{conform.Deviation{Key: "16-REQ-1.3..1.6"}, false},
		{conform.Deviation{Key: "16-REQ-9", Test: "TS-16-14"}, true},
	} {
		if got := declarationMet(c.d, r); got != c.want {
			t.Errorf("declarationMet(%+v) = %v, want %v", c.d, got, c.want)
		}
	}
}
