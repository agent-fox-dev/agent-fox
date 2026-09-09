package afspec

import (
	"fmt"
	"regexp"
	"strconv"
)

// Format v2 has five ID formats (Appendix A), so there are five ID helpers.
// Malformed IDs that do not match are silently skipped when scanning for the
// highest suffix in use.
var (
	// nextReqIDRe matches "05-REQ-3" and captures "3".
	nextReqIDRe = regexp.MustCompile(`-REQ-(\d+)$`)

	// nextCriterionIDRe matches "05-REQ-2.3" and captures "3".
	nextCriterionIDRe = regexp.MustCompile(`\.(\d+)$`)

	// nextPathIDRe matches "05-PATH-1" and captures "1".
	nextPathIDRe = regexp.MustCompile(`-PATH-(\d+)$`)

	// nextTestIDRe matches "TS-05-12" and captures "12". One series covers
	// every test kind, so there is no P/E/SMOKE variant to exclude.
	nextTestIDRe = regexp.MustCompile(`^TS-[^-]+-(\d+)$`)
)

// extractMaxSuffix scans a list of IDs, applies the given compiled regex to
// each, extracts the captured numeric suffix from the first submatch group,
// and returns the maximum value found. Returns 0 when nothing matches.
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

// NextRequirementID returns the next free requirement ID, {spec_id}-REQ-{N}.
func NextRequirementID(req RequirementsV2Json) string {
	ids := make([]string, len(req.Requirements))
	for i, r := range req.Requirements {
		ids[i] = r.Id
	}
	return fmt.Sprintf("%s-REQ-%d", req.SpecId, extractMaxSuffix(ids, nextReqIDRe)+1)
}

// NextCriterionID returns the next free criterion ID within a requirement,
// {requirement_id}.{C}.
func NextCriterionID(r Requirement) string {
	ids := make([]string, len(r.Criteria))
	for i, c := range r.Criteria {
		ids[i] = c.Id
	}
	return fmt.Sprintf("%s.%d", r.Id, extractMaxSuffix(ids, nextCriterionIDRe)+1)
}

// NextExecutionPathID returns the next free path ID, {spec_id}-PATH-{N}.
func NextExecutionPathID(req RequirementsV2Json) string {
	ids := make([]string, len(req.ExecutionPaths))
	for i, ep := range req.ExecutionPaths {
		ids[i] = ep.Id
	}
	return fmt.Sprintf("%s-PATH-%d", req.SpecId, extractMaxSuffix(ids, nextPathIDRe)+1)
}

// NextTestID returns the next free test ID, TS-{spec_id}-{N}. All four test
// kinds share one number series (§7.2).
func NextTestID(ts TestSpecV2Json) string {
	ids := make([]string, len(ts.Tests))
	for i, t := range ts.Tests {
		ids[i] = t.Id
	}
	return fmt.Sprintf("TS-%s-%d", ts.SpecId, extractMaxSuffix(ids, nextTestIDRe)+1)
}

// NextTaskID returns the next free task ID. Task IDs are plain integers
// sequential from 1 (Appendix A).
func NextTaskID(t TasksV2Json) int {
	max := 0
	for _, task := range t.Tasks {
		if task.Id > max {
			max = task.Id
		}
	}
	return max + 1
}
