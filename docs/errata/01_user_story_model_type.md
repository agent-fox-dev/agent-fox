# Erratum: Requirement.user_story is a UserStory model, not a plain string

**Spec:** 01_nightshift_afspec_models
**Requirement:** 01-REQ-2.3
**Test Spec:** TS-01-6

## Divergence

The specification states:

> THE build_afspec_from_triage SHALL set `Requirement.user_story` to the
> criterion description verbatim, without any template or LLM call.

This implies `user_story` is a plain string field. However, the afspec
`Requirement` model defines `user_story` as a `UserStory` Pydantic model
with fields `role`, `goal`, and `benefit`.

## Resolution

The criterion description is stored verbatim in `UserStory.goal`. The
`role` and `benefit` fields are left at their Pydantic defaults (empty
strings). This satisfies the intent of the requirement — the description
is preserved verbatim without any template or LLM call — while conforming
to the actual afspec model structure.

Test TS-01-6 asserts `spec.requirements.items[0].user_story.goal == <description>`
rather than `spec.requirements.items[0].user_story == <description>`.
