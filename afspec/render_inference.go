package afspec

import (
	"fmt"
	"regexp"
	"strings"
)

// reqIDRe matches requirement and acceptance-criterion ID patterns in free text
// (e.g. "02-REQ-1", "02-REQ-1.1", "02-REQ-1.E1"). Compiled once at package
// init time via regexp.MustCompile — not per function call.
var reqIDRe = regexp.MustCompile(`\b(\w+-REQ-\d+(?:\.\d+|\.E\d+)?)\b`)

// tsIDRe matches test spec ID patterns in free text (e.g. "TS-02-1",
// "TS-02-P1", "TS-02-E1", "TS-02-SMOKE-1"). Compiled once at package init
// time via regexp.MustCompile — not per function call.
var tsIDRe = regexp.MustCompile(`\b(TS-\w+-(?:\d+|P\d+|E\d+|SMOKE-\d+))\b`)

// inferRefsFromTraceability scans spec.Tasks.Traceability for entries whose
// TaskId starts with "{targetGroup}." and returns the collected RequirementId
// and TestSpecId values as separate deduplicated slices. Empty string fields
// are skipped. Returns (nil, nil) if no matching entries exist.
func inferRefsFromTraceability(spec *Spec, targetGroup int) (reqRefs []string, tsRefs []string) {
	if spec.Tasks == nil {
		return nil, nil
	}
	prefix := fmt.Sprintf("%d.", targetGroup)
	reqSet := make(map[string]bool)
	tsSet := make(map[string]bool)
	for _, entry := range spec.Tasks.Traceability {
		if strings.HasPrefix(entry.TaskId, prefix) {
			if entry.RequirementId != "" {
				reqSet[entry.RequirementId] = true
			}
			if entry.TestSpecId != "" {
				tsSet[entry.TestSpecId] = true
			}
		}
	}
	for id := range reqSet {
		reqRefs = append(reqRefs, id)
	}
	for id := range tsSet {
		tsRefs = append(tsRefs, id)
	}
	return reqRefs, tsRefs
}

// inferRefsFromSubtaskText scans the Title and Details fields of all subtasks
// in the target group using reqIDRe and tsIDRe, validates each match against
// the set of IDs actually present in the spec, and returns two collections
// of validated ID strings (inferred requirement refs, inferred test spec refs).
// Both may be empty if no validated matches are found.
func inferRefsFromSubtaskText(spec *Spec, targetGroup int) (reqRefs []string, tsRefs []string) {
	// Build known requirement ID set (requirement IDs + acceptance criteria + edge cases)
	knownReqIDs := make(map[string]bool)
	if spec.Requirements != nil {
		for _, r := range spec.Requirements.Requirements {
			knownReqIDs[r.Id] = true
			for _, c := range r.AcceptanceCriteria {
				knownReqIDs[c.Id] = true
			}
			for _, c := range r.EdgeCases {
				knownReqIDs[c.Id] = true
			}
		}
	}

	// Build known test spec ID set (test cases + property tests + edge case tests + smoke tests)
	knownTSIDs := make(map[string]bool)
	if spec.TestSpec != nil {
		for _, tc := range spec.TestSpec.TestCases {
			knownTSIDs[tc.Id] = true
		}
		for _, pt := range spec.TestSpec.PropertyTests {
			knownTSIDs[pt.Id] = true
		}
		for _, et := range spec.TestSpec.EdgeCaseTests {
			knownTSIDs[et.Id] = true
		}
		for _, st := range spec.TestSpec.SmokeTests {
			knownTSIDs[st.Id] = true
		}
	}

	// Find the target group
	var group *TaskGroup
	if spec.Tasks != nil {
		for i := range spec.Tasks.TaskGroups {
			if spec.Tasks.TaskGroups[i].Id == targetGroup {
				group = &spec.Tasks.TaskGroups[i]
				break
			}
		}
	}
	if group == nil {
		return nil, nil
	}

	// Scan subtask text for regex matches
	rawReqIDs := make(map[string]bool)
	rawTSIDs := make(map[string]bool)
	for _, subtask := range group.Subtasks {
		// Scan title
		for _, match := range reqIDRe.FindAllString(subtask.Title, -1) {
			rawReqIDs[match] = true
		}
		for _, match := range tsIDRe.FindAllString(subtask.Title, -1) {
			rawTSIDs[match] = true
		}
		// Scan all detail strings
		for _, detail := range subtask.Details {
			for _, match := range reqIDRe.FindAllString(detail, -1) {
				rawReqIDs[match] = true
			}
			for _, match := range tsIDRe.FindAllString(detail, -1) {
				rawTSIDs[match] = true
			}
		}
	}

	// Validate against known IDs — discard unrecognised matches
	for id := range rawReqIDs {
		if knownReqIDs[id] {
			reqRefs = append(reqRefs, id)
		}
	}
	for id := range rawTSIDs {
		if knownTSIDs[id] {
			tsRefs = append(tsRefs, id)
		}
	}
	return reqRefs, tsRefs
}
