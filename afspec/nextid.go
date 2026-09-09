package afspec

import (
	"fmt"
	"regexp"
	"strconv"
)

// ---------------------------------------------------------------------------
// Compiled regex patterns for extracting numeric suffixes from entity IDs.
// Each pattern captures the trailing numeric portion of the respective ID
// format. Malformed IDs that do not match are silently skipped.
// ---------------------------------------------------------------------------

var (
	// nextReqIDRe matches IDs like "05-REQ-3" and captures "3".
	nextReqIDRe = regexp.MustCompile(`-REQ-(\d+)$`)

	// nextCriterionIDRe matches IDs like "05-REQ-2.3" and captures "3".
	// Must not match edge case IDs like "05-REQ-2.E3".
	nextCriterionIDRe = regexp.MustCompile(`\.(\d+)$`)

	// nextEdgeCaseIDRe matches IDs like "05-REQ-2.E4" and captures "4".
	nextEdgeCaseIDRe = regexp.MustCompile(`\.E(\d+)$`)

	// nextPropIDRe matches IDs like "05-PROP-2" and captures "2".
	nextPropIDRe = regexp.MustCompile(`-PROP-(\d+)$`)

	// nextPathIDRe matches IDs like "05-PATH-1" and captures "1".
	nextPathIDRe = regexp.MustCompile(`-PATH-(\d+)$`)

	// nextErrIDRe matches IDs like "05-ERR-1" and captures "1".
	nextErrIDRe = regexp.MustCompile(`-ERR-(\d+)$`)

	// nextSmokeTestIDRe matches IDs like "TS-05-SMOKE-1" and captures "1".
	nextSmokeTestIDRe = regexp.MustCompile(`-SMOKE-(\d+)$`)

	// nextTestCaseIDRe matches IDs like "TS-05-2" and captures "2".
	// Must NOT match P-prefixed, E-prefixed, or SMOKE-prefixed variants.
	nextTestCaseIDRe = regexp.MustCompile(`^TS-[^-]+-(\d+)$`)

	// nextPropTestIDRe matches IDs like "TS-05-P1" and captures "1".
	nextPropTestIDRe = regexp.MustCompile(`-P(\d+)$`)

	// nextEdgeCaseTestIDRe matches IDs like "TS-05-E3" and captures "3".
	nextEdgeCaseTestIDRe = regexp.MustCompile(`-E(\d+)$`)
)

// extractMaxSuffix scans a list of IDs, applies the given compiled regex to
// each, extracts the captured numeric suffix from the first submatch group,
// and returns the maximum value found. Returns 0 if no ID matches or the
// slice is empty.
func extractMaxSuffix(ids []string, re *regexp.Regexp) int {
	max := 0
	for _, id := range ids {
		m := re.FindStringSubmatch(id)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if n > max {
			max = n
		}
	}
	return max
}

// ---------------------------------------------------------------------------
// Sequential ID generation helpers (05-REQ-5)
// ---------------------------------------------------------------------------

// NextRequirementID scans all requirement IDs in the given RequirementsV1Json,
// extracts the trailing numeric suffix using a compiled regex, finds the
// maximum, and returns a new ID formatted as {spec_id}-REQ-{max+1}.
// Returns suffix 1 if the collection is empty.
func NextRequirementID(req RequirementsV1Json) string {
	ids := make([]string, len(req.Requirements))
	for i, r := range req.Requirements {
		ids[i] = r.Id
	}
	return fmt.Sprintf("%s-REQ-%d", req.SpecId, extractMaxSuffix(ids, nextReqIDRe)+1)
}

// NextCriterionID scans all acceptance_criteria IDs in the given Requirement,
// extracts the trailing numeric suffix, finds the maximum, and returns a new
// ID formatted as {requirement_id}.{max+1}.
// Returns suffix 1 if the collection is empty.
func NextCriterionID(r Requirement) string {
	ids := make([]string, len(r.AcceptanceCriteria))
	for i, c := range r.AcceptanceCriteria {
		ids[i] = c.Id
	}
	return fmt.Sprintf("%s.%d", r.Id, extractMaxSuffix(ids, nextCriterionIDRe)+1)
}

// NextEdgeCaseID scans all edge_cases IDs in the given Requirement, extracts
// the trailing numeric suffix after the 'E' prefix, finds the maximum, and
// returns a new ID formatted as {requirement_id}.E{max+1}.
// Returns suffix 1 if the collection is empty.
func NextEdgeCaseID(r Requirement) string {
	ids := make([]string, len(r.EdgeCases))
	for i, c := range r.EdgeCases {
		ids[i] = c.Id
	}
	return fmt.Sprintf("%s.E%d", r.Id, extractMaxSuffix(ids, nextEdgeCaseIDRe)+1)
}

// NextCorrectnessPropertyID scans all correctness_properties IDs in the given
// RequirementsV1Json, extracts the trailing numeric suffix, finds the maximum,
// and returns a new ID formatted as {spec_id}-PROP-{max+1}.
// Returns suffix 1 if the collection is empty.
func NextCorrectnessPropertyID(req RequirementsV1Json) string {
	ids := make([]string, len(req.CorrectnessProperties))
	for i, cp := range req.CorrectnessProperties {
		ids[i] = cp.Id
	}
	return fmt.Sprintf("%s-PROP-%d", req.SpecId, extractMaxSuffix(ids, nextPropIDRe)+1)
}

// NextExecutionPathID scans all execution_paths IDs in the given
// RequirementsV1Json, extracts the trailing numeric suffix, finds the maximum,
// and returns a new ID formatted as {spec_id}-PATH-{max+1}.
// Returns suffix 1 if the collection is empty.
func NextExecutionPathID(req RequirementsV1Json) string {
	ids := make([]string, len(req.ExecutionPaths))
	for i, ep := range req.ExecutionPaths {
		ids[i] = ep.Id
	}
	return fmt.Sprintf("%s-PATH-%d", req.SpecId, extractMaxSuffix(ids, nextPathIDRe)+1)
}

// NextErrorHandlingID scans all error_handling IDs in the given
// RequirementsV1Json, extracts the trailing numeric suffix, finds the maximum,
// and returns a new ID formatted as {spec_id}-ERR-{max+1}.
// Returns suffix 1 if the collection is empty.
func NextErrorHandlingID(req RequirementsV1Json) string {
	ids := make([]string, len(req.ErrorHandling))
	for i, e := range req.ErrorHandling {
		ids[i] = e.Id
	}
	return fmt.Sprintf("%s-ERR-%d", req.SpecId, extractMaxSuffix(ids, nextErrIDRe)+1)
}

// NextTestCaseID scans all test_cases IDs in the given TestSpecV1Json, extracts
// the trailing numeric suffix, finds the maximum, and returns a new ID formatted
// as TS-{spec_id}-{max+1}.
// Returns suffix 1 if the collection is empty.
func NextTestCaseID(ts TestSpecV1Json) string {
	ids := make([]string, len(ts.TestCases))
	for i, tc := range ts.TestCases {
		ids[i] = tc.Id
	}
	return fmt.Sprintf("TS-%s-%d", ts.SpecId, extractMaxSuffix(ids, nextTestCaseIDRe)+1)
}

// NextPropertyTestID scans all property_tests IDs in the given TestSpecV1Json,
// extracts the trailing numeric suffix after 'P', finds the maximum, and returns
// a new ID formatted as TS-{spec_id}-P{max+1}.
// Returns suffix 1 if the collection is empty.
func NextPropertyTestID(ts TestSpecV1Json) string {
	ids := make([]string, len(ts.PropertyTests))
	for i, pt := range ts.PropertyTests {
		ids[i] = pt.Id
	}
	return fmt.Sprintf("TS-%s-P%d", ts.SpecId, extractMaxSuffix(ids, nextPropTestIDRe)+1)
}

// NextEdgeCaseTestID scans all edge_case_tests IDs in the given TestSpecV1Json,
// extracts the trailing numeric suffix after 'E', finds the maximum, and returns
// a new ID formatted as TS-{spec_id}-E{max+1}.
// Returns suffix 1 if the collection is empty.
func NextEdgeCaseTestID(ts TestSpecV1Json) string {
	ids := make([]string, len(ts.EdgeCaseTests))
	for i, et := range ts.EdgeCaseTests {
		ids[i] = et.Id
	}
	return fmt.Sprintf("TS-%s-E%d", ts.SpecId, extractMaxSuffix(ids, nextEdgeCaseTestIDRe)+1)
}

// NextSmokeTestID scans all smoke_tests IDs in the given TestSpecV1Json, extracts
// the trailing numeric suffix after 'SMOKE-', finds the maximum, and returns a
// new ID formatted as TS-{spec_id}-SMOKE-{max+1}.
// Returns suffix 1 if the collection is empty.
func NextSmokeTestID(ts TestSpecV1Json) string {
	ids := make([]string, len(ts.SmokeTests))
	for i, st := range ts.SmokeTests {
		ids[i] = st.Id
	}
	return fmt.Sprintf("TS-%s-SMOKE-%d", ts.SpecId, extractMaxSuffix(ids, nextSmokeTestIDRe)+1)
}
