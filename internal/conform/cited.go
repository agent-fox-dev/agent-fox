package conform

import (
	"regexp"
	"slices"
	"strings"

	"github.com/agent-fox-dev/agentfox/afspec"
)

var (
	// citedReqRe is a requirement or criterion id, as the spec format writes
	// one: 20-REQ-1, 20-REQ-1.2.
	citedReqRe = regexp.MustCompile(`\b[A-Za-z0-9_]+-REQ-\d+(?:\.\d+)?\b`)
	// citedTestRe is a test id: TS-20-3.
	citedTestRe = regexp.MustCompile(`\bTS-[A-Za-z0-9_]+-\d+\b`)
)

// Cited is the specs a problem report's requirement and test ids belong
// to, and the ids themselves: what a fix that answers the report must be
// reviewed against.
type Cited struct {
	Specs []*afspec.Spec
	Scope ReviewScope
}

// FindCited reads the requirement and test ids out of text and looks them up
// in the spec packages under specsDir. An id no package defines is dropped:
// the review answers for what a spec says, not for what a report guessed.
func FindCited(text, specsDir string) Cited {
	reqs := unique(citedReqRe.FindAllString(text, -1))
	tests := unique(citedTestRe.FindAllString(text, -1))
	if len(reqs) == 0 && len(tests) == 0 {
		return Cited{}
	}
	metas, err := afspec.DiscoverSpecs(specsDir)
	if err != nil {
		return Cited{}
	}
	var out Cited
	for _, m := range metas {
		spec, err := afspec.LoadSpec(m.Dir)
		if err != nil {
			continue
		}
		defined := specIDs(spec)
		used := false
		for _, id := range reqs {
			if defined[strings.ToUpper(id)] {
				out.Scope.Requirements = append(out.Scope.Requirements, id)
				used = true
			}
		}
		for _, id := range tests {
			if defined[strings.ToUpper(id)] {
				out.Scope.Tests = append(out.Scope.Tests, id)
				used = true
			}
		}
		if used {
			out.Specs = append(out.Specs, spec)
		}
	}
	return out
}

// specIDs is every requirement, criterion and test id a spec defines.
func specIDs(spec *afspec.Spec) map[string]bool {
	ids := map[string]bool{}
	if spec.Requirements != nil {
		for _, r := range spec.Requirements.Requirements {
			ids[strings.ToUpper(r.Id)] = true
			for _, c := range r.Criteria {
				ids[strings.ToUpper(c.Id)] = true
			}
		}
	}
	if spec.TestSpec != nil {
		for _, t := range spec.TestSpec.Tests {
			ids[strings.ToUpper(t.Id)] = true
		}
	}
	return ids
}

func unique(ids []string) []string {
	var out []string
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}
