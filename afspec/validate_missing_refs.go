package afspec

import (
	"fmt"
	"strings"
)

// checkMissingSubtaskRefs inspects all subtasks in the given TaskGroup and
// returns a ValidationEntry warning for each subtask that has empty
// RequirementRefs or TestSpecRefs. Groups with Kind == TaskGroupKindWiringVerification
// are skipped entirely. The warning message follows the format:
//
//	"Subtask {id} has empty {field_names} — scoped rendering will fall back to full spec dump"
//
// where {field_names} is "requirement_refs", "test_spec_refs", or
// "requirement_refs and test_spec_refs" depending on which fields are empty.
func checkMissingSubtaskRefs(group TaskGroup) []ValidationEntry {
	if group.Kind == TaskGroupKindWiringVerification {
		return nil
	}

	var warnings []ValidationEntry
	for _, subtask := range group.Subtasks {
		var missing []string
		if len(subtask.RequirementRefs) == 0 {
			missing = append(missing, "requirement_refs")
		}
		if len(subtask.TestSpecRefs) == 0 {
			missing = append(missing, "test_spec_refs")
		}
		if len(missing) > 0 {
			fieldNames := strings.Join(missing, " and ")
			warnings = append(warnings, ValidationEntry{
				Category: "warning",
				Check:    "missing_subtask_refs",
				Message:  fmt.Sprintf("Subtask %s has empty %s — scoped rendering will fall back to full spec dump", subtask.Id, fieldNames),
				EntityID: subtask.Id,
			})
		}
	}
	return warnings
}
