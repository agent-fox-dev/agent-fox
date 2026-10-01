package specgen

import "github.com/agent-fox-dev/agentfox/internal/toolio"

// Artifacts implements toolio.ArtifactsProvider (06-REQ-4). One
// spec_package entry per package this run wrote — the first (embedded as
// Result.Package) plus every entry of FollowOnSpecs — each with valid read
// off that package's own Validation.Valid, and a comment entry when its
// CommentURL is set. Nothing here is read from a field that is the model's
// account of its own work; SpecDir, Validation and CommentURL are all
// facts this package established itself, from the format's own validator
// and from the forge's response to AddComment.
//
// It lives in this file, not artifacts.go, because that file already
// defines the unrelated "artifact" the generation phases submit — one step
// of a spec package (requirements.json, tests_spec.json, ...) — and the two
// concepts share a name by coincidence, not by relation.
func (r *Result) Artifacts() []toolio.Artifact {
	if r == nil {
		return nil
	}
	pkgs := make([]Package, 0, 1+len(r.FollowOnSpecs))
	if r.SpecDir != "" {
		pkgs = append(pkgs, r.Package)
	}
	pkgs = append(pkgs, r.FollowOnSpecs...)

	var out []toolio.Artifact
	for _, p := range pkgs {
		out = append(out, toolio.Artifact{
			Kind:  toolio.ArtifactSpecPackage,
			Path:  p.SpecDir,
			ID:    p.SpecID,
			Valid: p.Validation.Valid,
		})
		if p.CommentURL != "" {
			out = append(out, toolio.Artifact{Kind: toolio.ArtifactComment, URL: p.CommentURL, Role: "prd"})
		}
	}
	return out
}
