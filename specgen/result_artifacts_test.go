package specgen

import (
	"encoding/json"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func artifactAsMap(t *testing.T, a toolio.Artifact) map[string]any {
	t.Helper()
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("Marshal artifact: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal artifact: %v", err)
	}
	return m
}

// 06-REQ-4: specgen's Result.Artifacts() builds one spec_package entry per
// written package — the first plus every FollowOnSpecs entry — with valid
// read from that package's own Validation.Valid, and a comment entry when
// CommentURL is set, never from a field the model authored on its own.
func TestResultArtifactsBuildsOneSpecPackagePerWrittenPackage(t *testing.T) {
	r := &Result{
		Package: Package{
			SpecDir:    "06_trim_and_chain_results",
			SpecID:     "06",
			Validation: ValidationReport{Valid: true},
			CommentURL: "https://github.com/o/r/issues/1#issuecomment-1",
		},
		FollowOnSpecs: []Package{
			{SpecDir: "07_next_spec", SpecID: "07", Validation: ValidationReport{Valid: false}},
		},
	}

	artifacts := r.Artifacts()
	var pkgs []map[string]any
	var comments []map[string]any
	for _, a := range artifacts {
		switch a.Kind {
		case toolio.ArtifactSpecPackage:
			pkgs = append(pkgs, artifactAsMap(t, a))
		case toolio.ArtifactComment:
			comments = append(comments, artifactAsMap(t, a))
		}
	}

	if len(pkgs) != 2 {
		t.Fatalf("spec_package entries = %d, want 2: %+v", len(pkgs), pkgs)
	}
	if pkgs[0]["path"] != r.SpecDir || pkgs[0]["valid"] != true {
		t.Errorf("first spec_package = %v", pkgs[0])
	}
	if pkgs[1]["path"] != r.FollowOnSpecs[0].SpecDir || pkgs[1]["valid"] != false {
		t.Errorf("follow-on spec_package = %v", pkgs[1])
	}

	if len(comments) != 1 || comments[0]["url"] != r.CommentURL {
		t.Errorf("comment entries = %v, want one carrying %q", comments, r.CommentURL)
	}
}

// A run that posted no comment produces no comment artifact, and a result
// with nothing written produces no artifacts at all.
func TestResultArtifactsOmitsCommentWhenNonePostedAndIsEmptyWhenNothingWasWritten(t *testing.T) {
	r := &Result{Package: Package{SpecDir: "06_trim_and_chain_results", SpecID: "06"}}
	for _, a := range r.Artifacts() {
		if a.Kind == toolio.ArtifactComment {
			t.Errorf("unexpected comment artifact: %+v", a)
		}
	}

	empty := &Result{}
	if got := empty.Artifacts(); len(got) != 0 {
		t.Errorf("Artifacts() for an unwritten result = %+v, want none", got)
	}
}
