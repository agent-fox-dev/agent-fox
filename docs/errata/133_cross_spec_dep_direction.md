# Erratum: CrossSpecDep from_spec/to_spec Direction (Spec 133)

## Divergence

Requirement 133-REQ-3.1 and the design.md mapping table specify:

- `from_spec = TaskDependency.depends_on_spec`
- `to_spec = current_spec`

This is **reversed** relative to the project's established `CrossSpecDep`
convention, confirmed by:

- `builder.py` lines 216-219: "CrossSpecDep direction: from_spec declares
  dependency on to_spec"
- `parser.py` lines 293-299 (alternative format): uses
  `from_spec=spec_name` for the current/declaring spec
- `test_builder.py` lines 164-175: `CrossSpecDep(from_spec='02_beta',
  to_spec='01_alpha')` produces edge `('01_alpha:2', '02_beta:1')`

## Resolution

The implementation and tests follow the **codebase convention**:

- `from_spec = current_spec` (the spec declaring the dependency)
- `to_spec = TaskDependency.depends_on_spec` (the spec being depended on)
- `from_group = TaskDependency.to_group` (group in the declaring spec)
- `to_group = TaskDependency.from_group` (group in the dependency spec)

The group number swap (`dep.to_group -> from_group`, `dep.from_group ->
to_group`) is consistent with the spec and retained as-is.

## Test Impact

TS-133-6's expected values for `from_spec` and `to_spec` have been
corrected in the test implementation to match the codebase convention.
