package afspec

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/agent-fox-dev/agentfox/afspec/legacy"
)

// MigrationReport records what a v1 → v2 conversion did and what a human still
// has to look at.
type MigrationReport struct {
	// Notes describe decisions the converter made that were not mechanical.
	Notes []string
	// Attached lists tests that v1 left owned by no task and that the
	// converter attached to the task owning their parent requirement.
	Attached []string
}

func (r *MigrationReport) note(format string, args ...any) {
	r.Notes = append(r.Notes, fmt.Sprintf(format, args...))
}

// v1CriterionID matches "05-REQ-2.3" and "05-REQ-2.E1", capturing the
// requirement part and the numeric suffix.
var v1CriterionID = regexp.MustCompile(`^(.+-REQ-\d+)\.(E?)(\d+)$`)

// Migrate converts the three format version 1.3 artifacts of src into version
// 2 artifacts, following the mapping table of §13.
//
// Everything in that table is mechanical except one thing: v1 had no notion of
// a task owning a test, and in practice no v1 spec owned its edge-case or
// property tests. The converter attaches each orphaned test to the task that
// owns its parent requirement, and records that in the report — a migrated
// plan is a starting point for review, not a finished one.
func Migrate(src *legacy.Spec, specID, specName string) (*RequirementsV2Json, *TestSpecV2Json, *TasksV2Json, *MigrationReport, error) {
	if src == nil || src.Requirements == nil || src.TestSpec == nil || src.Tasks == nil {
		return nil, nil, nil, nil, fmt.Errorf("migrate: the source spec is missing an artifact")
	}
	report := &MigrationReport{}

	requirements, criterionOfV1ID := migrateRequirements(src.Requirements, specID, specName, report)
	testSpec, testOfV1ID := migrateTestSpec(src.TestSpec, specID, specName, criterionOfV1ID, report)
	tasks := migrateTasks(src.Tasks, testSpec, specID, specName, criterionOfV1ID, testOfV1ID, requirements, report)

	return requirements, testSpec, tasks, report, nil
}

// migrateRequirements folds acceptance_criteria and edge_cases into one
// criteria list, correctness_properties into ubiquitous criteria, and drops
// error_handling (its behaviour already lives in the unwanted criteria it
// pointed at). It returns a map from every v1 criterion ID to its v2 ID.
func migrateRequirements(src *legacy.RequirementsV1Json, specID, specName string, report *MigrationReport) (*RequirementsV2Json, map[string]string) {
	out := &RequirementsV2Json{
		Schema:         requirementsSchemaURI,
		SpecId:         specID,
		SpecName:       specName,
		SchemaVersion:  SchemaVersion,
		Introduction:   src.Introduction,
		Requirements:   []Requirement{},
		ExecutionPaths: []ExecutionPath{},
	}
	if len(src.Glossary) > 0 {
		out.Glossary = RequirementsV2JsonGlossary{}
		for term, definition := range src.Glossary {
			out.Glossary[term] = definition
		}
	}

	idMap := map[string]string{}

	for _, r := range src.Requirements {
		req := Requirement{Id: r.Id, Title: r.Title}
		if story := renderUserStory(r.UserStory); story != "" {
			req.Rationale = strPtr(story)
		}

		// Acceptance criteria keep their numbering; edge cases continue it,
		// because v2 has one criterion series per requirement.
		next := 0
		for _, c := range r.AcceptanceCriteria {
			next++
			newID := fmt.Sprintf("%s.%d", r.Id, next)
			idMap[c.Id] = newID
			req.Criteria = append(req.Criteria, migrateCriterion(c, newID))
		}
		for _, c := range r.EdgeCases {
			next++
			newID := fmt.Sprintf("%s.%d", r.Id, next)
			idMap[c.Id] = newID
			req.Criteria = append(req.Criteria, migrateCriterion(c, newID))
		}
		out.Requirements = append(out.Requirements, req)
	}

	// Correctness properties become ubiquitous criteria on a requirement of
	// their own, verified by tests of kind property.
	if len(src.CorrectnessProperties) > 0 {
		propReq := Requirement{
			Id:        fmt.Sprintf("%s-REQ-%d", specID, len(out.Requirements)+1),
			Title:     "Correctness properties",
			Rationale: strPtr("Collected from the v1 correctness_properties list, which format v2 folds into ordinary criteria."),
		}
		for i, p := range src.CorrectnessProperties {
			newID := fmt.Sprintf("%s.%d", propReq.Id, i+1)
			idMap[p.Id] = newID
			propReq.Criteria = append(propReq.Criteria, Criterion{
				Id:      newID,
				Pattern: CriterionPatternUbiquitous,
				Action:  fmt.Sprintf("for any %s, %s", p.ForAny, p.Invariant),
			})
		}
		out.Requirements = append(out.Requirements, propReq)
		report.note("moved %d correctness properties into requirement %s as ubiquitous criteria",
			len(src.CorrectnessProperties), propReq.Id)
	}

	for _, p := range src.ExecutionPaths {
		path := ExecutionPath{Id: p.Id, Title: p.Title}
		for _, step := range p.Steps {
			path.Steps = append(path.Steps, PathStep{Actor: step.Actor, Action: step.Action})
		}
		out.ExecutionPaths = append(out.ExecutionPaths, path)
	}

	for _, api := range src.ExternalApis {
		entry := ExternalApi{
			Package: api.Package,
			Version: api.Version,
			// v1 encoded "unverified" by writing it into the version string.
			Verified: !strings.Contains(strings.ToLower(api.Version), "unverified"),
		}
		for _, sym := range api.Symbols {
			entry.Symbols = append(entry.Symbols, ExternalApiSymbol{
				Name:       sym.Name,
				ImportPath: sym.ImportPath,
				Signature:  sym.Signature,
				Notes:      sym.Notes,
			})
		}
		out.ExternalApis = append(out.ExternalApis, entry)
	}

	if len(src.ErrorHandling) > 0 {
		report.note("dropped %d error_handling entries; their behaviour is already carried by the unwanted criteria they referenced",
			len(src.ErrorHandling))
	}

	return out, idMap
}

// migrateCriterion maps the five v1 pattern-specific fields onto condition and
// guard, and return_contract onto contract.
func migrateCriterion(c legacy.Criterion, newID string) Criterion {
	out := Criterion{
		Id:      newID,
		Pattern: CriterionPattern(c.EarsPattern),
		Action:  c.Action,
	}
	if c.System != "" {
		out.System = strPtr(c.System)
	}
	if c.ReturnContract != nil && *c.ReturnContract != "" {
		out.Contract = c.ReturnContract
	}

	switch c.EarsPattern {
	case legacy.CriterionEarsPatternEventDriven:
		out.Condition = c.Trigger
	case legacy.CriterionEarsPatternComplexEvent:
		// v1 said WHEN trigger AND condition; v2 says condition AND guard.
		out.Condition = c.Trigger
		out.Guard = c.Condition
	case legacy.CriterionEarsPatternStateDriven:
		out.Condition = c.State
	case legacy.CriterionEarsPatternUnwanted:
		out.Condition = c.ErrorCondition
		if out.Contract == nil {
			// Rule C10 makes a contract mandatory here, and a placeholder that
			// says so is better than an artifact the validator rejects.
			out.Contract = strPtr("TODO: state what the caller observes")
		}
	case legacy.CriterionEarsPatternOptional:
		out.Condition = c.Feature
	}

	return out
}

func renderUserStory(story legacy.UserStory) string {
	if story.Role == "" && story.Goal == "" && story.Benefit == "" {
		return ""
	}
	return fmt.Sprintf("As a %s, I want %s, so that %s.", story.Role, story.Goal, story.Benefit)
}

// migrateTestSpec merges the four v1 test arrays into one list with a kind and
// a verifies list, renumbering into a single TS-{spec_id}-{N} series. It
// returns a map from every v1 test ID to its v2 ID.
func migrateTestSpec(src *legacy.TestSpecV1Json, specID, specName string, criterionOfV1ID map[string]string, report *MigrationReport) (*TestSpecV2Json, map[string]string) {
	out := &TestSpecV2Json{
		Schema:        testSpecSchemaURI,
		SpecId:        specID,
		SpecName:      specName,
		SchemaVersion: SchemaVersion,
		Tests:         []Test{},
	}
	idMap := map[string]string{}
	n := 0
	nextID := func() string {
		n++
		return fmt.Sprintf("TS-%s-%d", specID, n)
	}

	resolve := func(v1ID string) []string {
		if newID, ok := criterionOfV1ID[v1ID]; ok {
			return []string{newID}
		}
		if v1ID != "" {
			return []string{v1ID}
		}
		return nil
	}

	for _, tc := range src.TestCases {
		id := nextID()
		idMap[tc.Id] = id
		out.Tests = append(out.Tests, Test{
			Id:         id,
			Kind:       TestKindUnit,
			Verifies:   resolve(tc.RequirementId),
			Title:      tc.Description,
			Given:      orEmpty(tc.Preconditions),
			When:       describeValue(tc.Input, "the component under test is exercised"),
			Then:       []string{describeValue(tc.Expected, "the documented outcome holds")},
			Pseudocode: optionalString(tc.AssertionPseudocode),
		})
	}

	for _, ec := range src.EdgeCaseTests {
		id := nextID()
		idMap[ec.Id] = id
		out.Tests = append(out.Tests, Test{
			Id:         id,
			Kind:       TestKindUnit,
			Verifies:   resolve(ec.RequirementId),
			Title:      ec.Description,
			Given:      orEmpty(ec.Preconditions),
			When:       describeValue(ec.Input, "the edge case is exercised"),
			Then:       []string{describeValue(ec.Expected, "the documented outcome holds")},
			Pseudocode: optionalString(ec.AssertionPseudocode),
		})
	}

	for _, pt := range src.PropertyTests {
		id := nextID()
		idMap[pt.Id] = id
		verifies := resolve(pt.PropertyId)
		for _, v := range pt.Validates {
			if newID, ok := criterionOfV1ID[v]; ok {
				verifies = appendUnique(verifies, newID)
			}
		}
		out.Tests = append(out.Tests, Test{
			Id:       id,
			Kind:     TestKindProperty,
			Verifies: verifies,
			Title:    pt.Description,
			Given:    []string{},
			When:     "for any " + pt.ForAnyStrategy,
			Then:     []string{pt.InvariantCheck},
		})
	}

	for _, st := range src.SmokeTests {
		id := nextID()
		idMap[st.Id] = id
		out.Tests = append(out.Tests, Test{
			Id:             id,
			Kind:           TestKindSmoke,
			Verifies:       []string{st.ExecutionPathId},
			Title:          st.Description,
			Given:          []string{},
			When:           st.Trigger,
			Then:           orEmpty(st.ExpectedEffects),
			RealComponents: orEmpty(st.RealComponents),
		})
	}

	report.note("merged %d v1 test entries into one series of %d tests", n, len(out.Tests))
	return out, idMap
}

// migrateTasks flattens task groups into tasks, merges each "tests" group into
// the group that implements the same requirements, converts the
// wiring_verification group into the integration task, and attaches every test
// v1 left unowned.
func migrateTasks(
	src *legacy.TasksV1Json,
	testSpec *TestSpecV2Json,
	specID, specName string,
	criterionOfV1ID, testOfV1ID map[string]string,
	requirements *RequirementsV2Json,
	report *MigrationReport,
) *TasksV2Json {
	out := &TasksV2Json{
		Schema:        tasksSchemaURI,
		SpecId:        specID,
		SpecName:      specName,
		SchemaVersion: SchemaVersion,
		TestCommands: TestCommands{
			AllTests:  src.TestCommands.AllTests,
			Linter:    src.TestCommands.Linter,
			SpecTests: optionalString(src.TestCommands.SpecTests),
		},
		Dependencies: []Dependency{},
		Tasks:        []Task{},
	}
	for _, dep := range src.Dependencies {
		reason := dep.Relationship
		if reason == "" {
			reason = "carried over from a version 1 dependency"
		}
		out.Dependencies = append(out.Dependencies, Dependency{Spec: dep.DependsOnSpec, Reason: reason})
	}

	// A v1 plan alternated "tests" groups with "standard" groups describing
	// the same requirements. v2 writes tests inside the task that owns them,
	// so the tests groups are folded away and only their refs survive.
	var integration *Task
	nextTaskID := 0
	for _, g := range src.TaskGroups {
		criteria, tests, steps, touches := collectGroupRefs(g, criterionOfV1ID, testOfV1ID)

		if g.Kind == legacy.TaskGroupKindWiringVerification {
			integration = &Task{
				Kind:    TaskKindIntegration,
				Title:   g.Title,
				Tests:   tests,
				Steps:   orDefaultSteps(steps, integrationSteps()),
				Touches: touches,
				State:   migrateTaskState(g),
			}
			integration.Criteria = []string{}
			continue
		}

		if g.Kind == legacy.TaskGroupKindTests {
			// Fold the refs of a tests group into the next implementing task
			// by treating them as unowned; the attachment pass below places
			// them on the task that owns their requirement.
			report.note("folded the v1 %q group %d into the tasks that implement the same requirements", string(g.Kind), g.Id)
			continue
		}

		nextTaskID++
		task := Task{
			Id:       nextTaskID,
			Kind:     TaskKindImplement,
			Title:    g.Title,
			Criteria: criteria,
			Tests:    tests,
			Steps:    orDefaultSteps(steps, []string{"Carry out the work of this task."}),
			Touches:  touches,
			State:    migrateTaskState(g),
		}
		out.Tasks = append(out.Tasks, task)
	}

	if integration == nil {
		integration = &Task{
			Kind:     TaskKindIntegration,
			Title:    "Verify the spec end to end",
			Criteria: []string{},
			Steps:    integrationSteps(),
			State:    TaskStatePending,
		}
		report.note("the v1 plan had no wiring_verification group; an integration task was added")
	}
	nextTaskID++
	integration.Id = nextTaskID

	// Every criterion needs an owning implement task (C8). Attach the ones no
	// task claims to the task whose criteria share their requirement.
	attachCriteria(out, requirements, report)

	// Every test needs an owning task (C7), and the integration task needs
	// every smoke test (C9). This is the part v1 never had.
	attachTests(out, integration, testSpec, requirements, report)

	out.Tasks = append(out.Tasks, *integration)
	return out
}

// collectGroupRefs gathers a v1 group's requirement refs, test refs, subtask
// titles and details, translating the refs to their v2 IDs.
func collectGroupRefs(g legacy.TaskGroup, criterionOfV1ID, testOfV1ID map[string]string) (criteria, tests, steps, touches []string) {
	for _, sub := range g.Subtasks {
		for _, ref := range sub.RequirementRefs {
			if newID, ok := criterionOfV1ID[ref]; ok {
				criteria = appendUnique(criteria, newID)
			} else {
				criteria = appendUnique(criteria, ref)
			}
		}
		for _, ref := range sub.TestSpecRefs {
			if newID, ok := testOfV1ID[ref]; ok {
				tests = appendUnique(tests, newID)
			}
		}
		steps = append(steps, sub.Title)
		steps = append(steps, sub.Details...)
	}
	for _, check := range g.Verification.Checks {
		steps = append(steps, check)
	}
	return criteria, tests, steps, touches
}

// migrateTaskState collapses a group's subtask states into one task state: a
// group is done when every subtask is, in progress when any is, else pending.
func migrateTaskState(g legacy.TaskGroup) TaskState {
	if len(g.Subtasks) == 0 {
		return TaskStatePending
	}
	allDone := true
	for _, sub := range g.Subtasks {
		switch sub.State {
		case legacy.SubtaskStateInProgress:
			return TaskStateInProgress
		case legacy.SubtaskStateDone, legacy.SubtaskStateDropped:
		default:
			allDone = false
		}
	}
	if allDone {
		return TaskStateDone
	}
	return TaskStatePending
}

// attachCriteria gives every criterion an owning implement task. A criterion
// no task claims goes to the task that already claims a sibling from the same
// requirement, or to the first implement task.
func attachCriteria(tasks *TasksV2Json, requirements *RequirementsV2Json, report *MigrationReport) {
	if len(tasks.Tasks) == 0 {
		return
	}

	owner := map[string]int{} // requirement ID → task index
	claimed := map[string]bool{}
	for i, task := range tasks.Tasks {
		for _, ref := range task.Criteria {
			claimed[ref] = true
			if m := v1CriterionID.FindStringSubmatch(ref); m != nil {
				if _, seen := owner[m[1]]; !seen {
					owner[m[1]] = i
				}
			} else if _, seen := owner[ref]; !seen {
				owner[ref] = i
			}
		}
	}

	var orphans []string
	for _, req := range requirements.Requirements {
		for _, c := range req.Criteria {
			if claimed[c.Id] || claimed[req.Id] {
				continue
			}
			idx, ok := owner[req.Id]
			if !ok {
				idx = 0
			}
			tasks.Tasks[idx].Criteria = appendUnique(tasks.Tasks[idx].Criteria, c.Id)
			orphans = append(orphans, c.Id)
		}
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		report.note("attached %d criteria that no v1 task group claimed: %s",
			len(orphans), strings.Join(orphans, ", "))
	}
}

// attachTests gives every test an owning task and the integration task every
// smoke test. In all eight archived v1 specs this pass has real work to do:
// none of them owned a single edge-case or property test.
func attachTests(tasks *TasksV2Json, integration *Task, testSpec *TestSpecV2Json, requirements *RequirementsV2Json, report *MigrationReport) {
	owned := map[string]bool{}
	for _, task := range tasks.Tasks {
		for _, id := range task.Tests {
			owned[id] = true
		}
	}
	for _, id := range integration.Tests {
		owned[id] = true
	}

	// criterion ID → index of the implement task that owns it.
	taskOfCriterion := map[string]int{}
	for i, task := range tasks.Tasks {
		for _, ref := range task.Criteria {
			taskOfCriterion[ref] = i
			for _, req := range requirements.Requirements {
				if req.Id != ref {
					continue
				}
				for _, c := range req.Criteria {
					if _, seen := taskOfCriterion[c.Id]; !seen {
						taskOfCriterion[c.Id] = i
					}
				}
			}
		}
	}

	for _, test := range testSpec.Tests {
		if test.Kind == TestKindSmoke {
			if !containsString(integration.Tests, test.Id) {
				integration.Tests = append(integration.Tests, test.Id)
				if !owned[test.Id] {
					report.Attached = append(report.Attached, test.Id)
				}
			}
			owned[test.Id] = true
			continue
		}
		if owned[test.Id] {
			continue
		}

		idx := 0
		for _, ref := range test.Verifies {
			if i, ok := taskOfCriterion[ref]; ok {
				idx = i
				break
			}
		}
		if len(tasks.Tasks) == 0 {
			integration.Tests = append(integration.Tests, test.Id)
		} else {
			tasks.Tasks[idx].Tests = appendUnique(tasks.Tasks[idx].Tests, test.Id)
		}
		report.Attached = append(report.Attached, test.Id)
	}

	if len(report.Attached) > 0 {
		report.note("attached %d tests that no v1 task owned; review which task each belongs to", len(report.Attached))
	}
}

func integrationSteps() []string {
	return []string{
		"Trace every execution path through the real code; confirm each step calls the next and no stub remains",
		"For every criterion with a contract, confirm a caller in production code consumes the producer's result",
		"Search the files listed in every task's touches for stub markers appropriate to the language",
		"For any path whose entry point belongs to another spec, confirm that entry point is called from production code",
	}
}

func orDefaultSteps(steps, fallback []string) []string {
	out := make([]string, 0, len(steps))
	for _, s := range steps {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

func orEmpty(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// describeValue renders a v1 free-form input/expected value as a sentence.
func describeValue(v any, fallback string) string {
	switch value := v.(type) {
	case nil:
		return fallback
	case string:
		if strings.TrimSpace(value) == "" {
			return fallback
		}
		return value
	case map[string]any:
		if len(value) == 0 {
			return fallback
		}
		keys := make([]string, 0, len(value))
		for k := range value {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s=%v", k, value[k]))
		}
		return strings.Join(parts, ", ")
	case float64:
		return strconv.FormatFloat(value, 'g', -1, 64)
	default:
		return fmt.Sprintf("%v", value)
	}
}

func appendUnique(list []string, item string) []string {
	if containsString(list, item) {
		return list
	}
	return append(list, item)
}

func containsString(list []string, item string) bool {
	for _, s := range list {
		if s == item {
			return true
		}
	}
	return false
}
