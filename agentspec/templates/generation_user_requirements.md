---
name: generation_user_requirements
description: Additional instructions for requirements artifact generation
---
Generate the requirements artifact following the EARS syntax format. Include acceptance criteria, execution paths, error handling, and external APIs.

## EARS Pattern Field Rules

Each acceptance criterion uses exactly one EARS pattern. The schema enforces a discriminated `oneOf` — using wrong or extra fields causes validation failure. Follow these rules precisely:

### ubiquitous
- **Required fields:** `id`, `ears_pattern`, `system`, `action`, `return_contract`
- **Forbidden fields:** `trigger`, `condition`, `error_condition`, `state`, `feature`
- Template: THE {system} SHALL {action}

### event_driven
- **Required fields:** `id`, `ears_pattern`, `trigger`, `system`, `action`, `return_contract`
- **Forbidden fields:** `condition`, `error_condition`, `state`, `feature`
- Template: WHEN {trigger}, THE {system} SHALL {action}

### complex_event
- **Required fields:** `id`, `ears_pattern`, `trigger`, `condition`, `system`, `action`, `return_contract`
- **Forbidden fields:** `error_condition`, `state`, `feature`
- Template: WHEN {trigger} AND {condition}, THE {system} SHALL {action}

### state_driven
- **Required fields:** `id`, `ears_pattern`, `state`, `system`, `action`, `return_contract`
- **Forbidden fields:** `trigger`, `condition`, `error_condition`, `feature`
- Template: WHILE {state}, THE {system} SHALL {action}

### unwanted
- **Required fields:** `id`, `ears_pattern`, `error_condition`, `system`, `action`, `return_contract`
- **Forbidden fields:** `trigger`, `condition`, `state`, `feature`
- Template: IF {error_condition}, THEN THE {system} SHALL {action}

### optional
- **Required fields:** `id`, `ears_pattern`, `feature`, `system`, `action`, `return_contract`
- **Forbidden fields:** `trigger`, `condition`, `error_condition`, `state`
- Template: WHERE {feature}, THE {system} SHALL {action}

## Glossary Backtick Convention

In the `action`, `trigger`, `condition`, `error_condition`, `state`, `feature`, `for_any`, and `invariant` fields: any domain-specific term must be wrapped in backticks (e.g. `` `SpaceManager` ``, `` `LivingSpec` ``). Every backtick-wrapped token must have a corresponding entry in the top-level `glossary` object. Unquoted natural-language words are not checked — only backtick-wrapped tokens are treated as domain terms requiring glossary entries.

## Example

The fragment below shows a correctly structured requirements artifact excerpt for a recipe-manager system. Use a different domain for your actual output — this example is for structural reference only.

```json
{
  "spec_id": "07",
  "spec_name": "recipe-manager",
  "schema_version": "1.0",
  "introduction": "The recipe catalog stores user-created recipes and their ingredients.",
  "glossary": {
    "Recipe": "A named collection of ingredients and preparation steps in the catalog",
    "Ingredient": "A measurable food item referenced by a Recipe"
  },
  "requirements": [
    {
      "id": "07-REQ-1",
      "title": "Recipe creation",
      "user_story": "As a cook, I want to save a Recipe so that I can retrieve it later.",
      "acceptance_criteria": [
        {
          "id": "07-REQ-1.1",
          "ears_pattern": "event_driven",
          "trigger": "a user submits POST /recipes with a valid `Recipe` body",
          "system": "recipe catalog",
          "action": "persist the `Recipe` and return its assigned ID",
          "return_contract": "returns HTTP 201 with JSON body {id: string, name: string}"
        },
        {
          "id": "07-REQ-1.2",
          "ears_pattern": "unwanted",
          "error_condition": "the submitted `Recipe` body is missing the required name field",
          "system": "recipe catalog",
          "action": "reject the request with a descriptive validation error",
          "return_contract": "returns HTTP 400 with JSON body {error: string}"
        }
      ],
      "edge_cases": []
    }
  ]
}
```
