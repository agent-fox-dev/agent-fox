# Test Specification: v1.2 Parsing Pipeline

## Overview

Tests validate that afspec Pydantic models are correctly mapped to agent-fox
dataclasses and that the planner routes v1.2 specs through the new parser.

## Test Cases

### TS-133-1: Subtask mapping sets completed from state

**Requirement:** 133-REQ-1.1, 133-REQ-1.2
**Type:** unit
**Description:** Verify that _map_subtask maps id, title, and completed correctly.

**Preconditions:**
- afspec.Subtask instances with various states.

**Input:**
- Subtask with state=DONE, id="1.1", title="Write tests"
- Subtask with state=PENDING, id="1.2", title="Implement feature"

**Expected:**
- First subtask: SubtaskDef(id="1.1", title="Write tests", completed=True)
- Second subtask: SubtaskDef(id="1.2", title="Implement feature", completed=False)

**Assertion pseudocode:**
```
done_subtask = Subtask(id="1.1", title="Write tests", state=SubtaskState.DONE, ...)
result = _map_subtask(done_subtask)
ASSERT result.id == "1.1"
ASSERT result.title == "Write tests"
ASSERT result.completed == True

pending_subtask = Subtask(id="1.2", title="Implement feature", state=SubtaskState.PENDING, ...)
result = _map_subtask(pending_subtask)
ASSERT result.completed == False
```

### TS-133-2: Task group mapping produces correct TaskGroupDef

**Requirement:** 133-REQ-2.1
**Type:** unit
**Description:** Verify that _map_task_group maps id, title, optional, archetype.

**Preconditions:**
- An afspec.TaskGroup with id=2, title="Implement parser", kind=STANDARD,
  one PENDING subtask.

**Input:**
- The TaskGroup instance.

**Expected:**
- TaskGroupDef with number=2, title="Implement parser", optional=False,
  archetype=None, completed=False.

**Assertion pseudocode:**
```
group = TaskGroup(id=2, title="Implement parser", kind=TaskGroupKind.STANDARD,
                  subtasks=[Subtask(state=SubtaskState.PENDING, ...)], ...)
result = _map_task_group(group)
ASSERT result.number == 2
ASSERT result.title == "Implement parser"
ASSERT result.optional == False
ASSERT result.archetype is None
ASSERT result.completed == False
ASSERT len(result.subtasks) == 1
```

### TS-133-3: Group completed when all non-dropped subtasks are DONE

**Requirement:** 133-REQ-2.2
**Type:** unit
**Description:** Verify TaskGroupDef.completed is True when all non-dropped
subtasks have state DONE.

**Preconditions:**
- A TaskGroup with two subtasks: one DONE, one DROPPED.

**Input:**
- The TaskGroup instance.

**Expected:**
- TaskGroupDef.completed is True (the dropped subtask is excluded).

**Assertion pseudocode:**
```
group = TaskGroup(id=1, subtasks=[
    Subtask(state=SubtaskState.DONE, ...),
    Subtask(state=SubtaskState.DROPPED, ...),
], ...)
result = _map_task_group(group)
ASSERT result.completed == True
```

### TS-133-4: Group not completed when any non-dropped subtask is not DONE

**Requirement:** 133-REQ-2.3
**Type:** unit
**Description:** Verify TaskGroupDef.completed is False when a non-dropped
subtask is not DONE.

**Preconditions:**
- A TaskGroup with two subtasks: one DONE, one IN_PROGRESS.

**Input:**
- The TaskGroup instance.

**Expected:**
- TaskGroupDef.completed is False.

**Assertion pseudocode:**
```
group = TaskGroup(id=1, subtasks=[
    Subtask(state=SubtaskState.DONE, ...),
    Subtask(state=SubtaskState.IN_PROGRESS, ...),
], ...)
result = _map_task_group(group)
ASSERT result.completed == False
```

### TS-133-5: Group body contains markdown content

**Requirement:** 133-REQ-2.4
**Type:** unit
**Description:** Verify TaskGroupDef.body is a non-empty markdown string
containing subtask information.

**Preconditions:**
- A TaskGroup with subtasks that have titles and details.

**Input:**
- The TaskGroup instance.

**Expected:**
- TaskGroupDef.body is a non-empty string containing subtask titles.

**Assertion pseudocode:**
```
group = TaskGroup(id=1, subtasks=[
    Subtask(id="1.1", title="Write tests", details=["Detail line"], ...),
], ...)
result = _map_task_group(group)
ASSERT len(result.body) > 0
ASSERT "Write tests" in result.body
```

### TS-133-6: Cross-spec dependency mapping

**Requirement:** 133-REQ-3.1
**Type:** unit
**Description:** Verify _map_dependency maps fields correctly with proper
field assignment.

**Preconditions:**
- An afspec.TaskDependency with depends_on_spec="132_afspec_integration",
  from_group=2, to_group=1.

**Input:**
- The TaskDependency and current_spec="133_v12_parsing_pipeline".

**Expected:**
- CrossSpecDep with from_spec="132_afspec_integration",
  to_spec="133_v12_parsing_pipeline", from_group=1, to_group=2.

**Assertion pseudocode:**
```
dep = TaskDependency(depends_on_spec="132_afspec_integration",
                     from_group=2, to_group=1, ...)
result = _map_dependency(dep, "133_v12_parsing_pipeline")
ASSERT result.from_spec == "132_afspec_integration"
ASSERT result.to_spec == "133_v12_parsing_pipeline"
ASSERT result.from_group == 1
ASSERT result.to_group == 2
```

### TS-133-7: parse_tasks_v12 returns list of TaskGroupDef

**Requirement:** 133-REQ-4.1
**Type:** integration
**Description:** Verify that parse_tasks_v12 loads a v1.2 spec and returns
TaskGroupDef instances.

**Preconditions:**
- A temporary directory with valid v1.2 spec files (prd.md with frontmatter,
  requirements.json, test_spec.json, tasks.json with task groups).

**Input:**
- Path to the temporary spec directory.

**Expected:**
- Returns a non-empty list of TaskGroupDef instances.
- Each element has the correct type and populated fields.

**Assertion pseudocode:**
```
groups = parse_tasks_v12(tmp_spec_dir)
ASSERT len(groups) > 0
ASSERT all(isinstance(g, TaskGroupDef) for g in groups)
ASSERT groups[0].number > 0
ASSERT groups[0].title != ""
```

### TS-133-8: parse_cross_deps_v12 returns list of CrossSpecDep

**Requirement:** 133-REQ-3.1, 133-REQ-3.E1
**Type:** integration
**Description:** Verify that parse_cross_deps_v12 loads a v1.2 spec and
returns CrossSpecDep instances, or an empty list if no dependencies.

**Preconditions:**
- A temporary directory with valid v1.2 spec files including task
  dependencies in tasks.json.

**Input:**
- Path to the temporary spec directory, spec_name="test_spec".

**Expected:**
- Returns a list of CrossSpecDep instances matching the dependencies in
  tasks.json.

**Assertion pseudocode:**
```
# With dependencies
deps = parse_cross_deps_v12(tmp_spec_dir_with_deps, "test_spec")
ASSERT len(deps) > 0
ASSERT all(isinstance(d, CrossSpecDep) for d in deps)

# Without dependencies
deps = parse_cross_deps_v12(tmp_spec_dir_no_deps, "test_spec")
ASSERT len(deps) == 0
```

## Property Test Cases

### TS-133-P1: Subtask completion is a function of state alone

**Property:** Property 1 from design.md
**Validates:** 133-REQ-1.1, 133-REQ-1.2
**Type:** property
**Description:** For any subtask state, completed is True iff state is DONE.

**For any:** SubtaskState value
**Invariant:** `_map_subtask(subtask).completed == (subtask.state == SubtaskState.DONE)`

**Assertion pseudocode:**
```
FOR ANY state IN SubtaskState:
    subtask = Subtask(id="1.1", title="task", state=state, ...)
    result = _map_subtask(subtask)
    ASSERT result.completed == (state == SubtaskState.DONE)
```

### TS-133-P2: Group completion is consistent with subtask states

**Property:** Property 2 from design.md
**Validates:** 133-REQ-2.2, 133-REQ-2.3, 133-REQ-2.E1
**Type:** property
**Description:** A group is completed iff all non-dropped subtasks are DONE.

**For any:** list of SubtaskState values (non-empty)
**Invariant:** `_map_task_group(group).completed == all(s.state in {DONE, DROPPED} for s in group.subtasks) and any(s.state != DROPPED for s in group.subtasks) or all(s.state == DROPPED for s in group.subtasks)`

**Assertion pseudocode:**
```
FOR ANY states IN list(SubtaskState) with len >= 1:
    subtasks = [Subtask(state=s, ...) for s in states]
    group = TaskGroup(id=1, subtasks=subtasks, ...)
    result = _map_task_group(group)
    non_dropped = [s for s in states if s != SubtaskState.DROPPED]
    IF len(non_dropped) == 0:
        ASSERT result.completed == True
    ELSE:
        ASSERT result.completed == all(s == SubtaskState.DONE for s in non_dropped)
```

## Edge Case Tests

### TS-133-E1: Dropped subtask excluded from completion check

**Requirement:** 133-REQ-1.E1, 133-REQ-2.E1
**Type:** unit
**Description:** A group with all subtasks dropped is vacuously complete.

**Preconditions:**
- A TaskGroup where every subtask has state DROPPED.

**Input:**
- The TaskGroup instance.

**Expected:**
- TaskGroupDef.completed is True.
- Each SubtaskDef.completed is False (DROPPED is not DONE).

**Assertion pseudocode:**
```
group = TaskGroup(id=1, subtasks=[
    Subtask(state=SubtaskState.DROPPED, ...),
    Subtask(state=SubtaskState.DROPPED, ...),
], ...)
result = _map_task_group(group)
ASSERT result.completed == True
ASSERT all(not st.completed for st in result.subtasks)
```

### TS-133-E2: Spec with no dependencies returns empty list

**Requirement:** 133-REQ-3.E1
**Type:** unit
**Description:** parse_cross_deps_v12 returns [] when tasks.json has no
dependency entries.

**Preconditions:**
- A valid v1.2 spec with no dependencies in tasks.json.

**Input:**
- Path to the spec directory.

**Expected:**
- Empty list returned.

**Assertion pseudocode:**
```
deps = parse_cross_deps_v12(tmp_spec_dir_no_deps, "test_spec")
ASSERT deps == []
```

### TS-133-E3: afspec.load_spec error propagates from planner

**Requirement:** 133-REQ-4.E1
**Type:** unit
**Description:** When afspec.load_spec raises LoadError, it propagates
through parse_tasks_v12 uncaught.

**Preconditions:**
- A spec directory with malformed JSON.

**Input:**
- Path to the malformed spec directory.

**Expected:**
- afspec.LoadError (or ValidationError) is raised.

**Assertion pseudocode:**
```
ASSERT_RAISES (afspec.LoadError OR ValidationError):
    parse_tasks_v12(tmp_malformed_dir)
```

## Integration Smoke Tests

### TS-133-SMOKE-1: Full pipeline from discovery through planner

**Execution Path:** Path 1 + Path 2 + Path 3 from design.md
**Description:** Discover a v1.2 spec, parse it through the new pipeline,
and verify the graph builder receives correct TaskGroupDef instances.

**Setup:** Temp specs root with one valid v1.2 spec folder containing
task groups and dependencies. Mockable: None (real filesystem, real afspec).

**Trigger:** `build_plan(specs_dir, None, False, config)` with the temp
specs root.

**Expected side effects:**
- build_plan returns a TaskGraph
- The graph contains nodes matching the task groups in the v1.2 spec
- Node IDs follow the `{spec_name}:{group_number}` pattern
- Cross-spec edges are present if dependencies were declared

**Must NOT satisfy with:** No mocking of parser_v12, afspec.load_spec, or
build_graph.

**Assertion pseudocode:**
```
graph = build_plan(tmp_specs_root, None, False, config)
ASSERT len(graph.nodes) > 0
spec_name = "01_test_spec"
ASSERT any(n.spec_name == spec_name for n in graph.nodes.values())
coder_nodes = [n for n in graph.nodes.values() if n.archetype == "coder"]
ASSERT len(coder_nodes) >= 1
```

## Coverage Matrix

| Requirement | Test Spec Entry | Type |
|-------------|-----------------|------|
| 133-REQ-1.1 | TS-133-1 | unit |
| 133-REQ-1.2 | TS-133-1 | unit |
| 133-REQ-1.E1 | TS-133-E1 | unit |
| 133-REQ-2.1 | TS-133-2 | unit |
| 133-REQ-2.2 | TS-133-3 | unit |
| 133-REQ-2.3 | TS-133-4 | unit |
| 133-REQ-2.4 | TS-133-5 | unit |
| 133-REQ-2.E1 | TS-133-E1 | unit |
| 133-REQ-3.1 | TS-133-6, TS-133-8 | unit, integration |
| 133-REQ-3.E1 | TS-133-E2, TS-133-8 | unit, integration |
| 133-REQ-4.1 | TS-133-7 | integration |
| 133-REQ-4.2 | TS-133-SMOKE-1 | integration |
| 133-REQ-4.E1 | TS-133-E3 | unit |
| Property 1 | TS-133-P1 | property |
| Property 2 | TS-133-P2 | property |
