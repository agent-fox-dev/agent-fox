Generate the requirements artifact: one flat list of criteria per requirement, plus the end-to-end execution paths.

## Scope first

Decide whether the PRD fits in **at most 10 requirements**, each with at most 8 criteria. If it does not, say so in your response rather than generating an oversized spec: the PRD must be split into several specs first.

## Criterion fields

Each criterion is one EARS sentence decomposed into fields. Choose the pattern first, then fill in only the fields that pattern allows.

| `pattern`       | `condition`      | `guard`    | Rendered sentence                                           |
|-----------------|------------------|------------|-------------------------------------------------------------|
| `ubiquitous`    | must be absent   | absent     | THE {system} SHALL {action}                                  |
| `event_driven`  | the trigger      | absent     | WHEN {condition}, THE {system} SHALL {action}                |
| `complex_event` | the trigger      | required   | WHEN {condition} AND {guard}, THE {system} SHALL {action}    |
| `state_driven`  | the state        | absent     | WHILE {condition}, THE {system} SHALL {action}               |
| `unwanted`      | the error case   | absent     | IF {condition}, THEN THE {system} SHALL {action}             |
| `optional`      | the feature      | absent     | WHERE {condition}, THE {system} SHALL {action}               |

Every criterion also has:

- `id` — `{spec_id}-REQ-{N}.{C}`, C sequential from 1 within the requirement.
- `system` — the component that acts. Optional; defaults to "system" when rendered.
- `action` — what the system must do. Testable and unambiguous.
- `contract` — what the caller observes: return value, exit code, status code, emitted event. **Required for every `unwanted` criterion** and expected whenever the result is consumed by something else.

A requirement may carry a one-sentence `rationale` explaining why it exists. There is no `user_story` object.

## What used to be separate sections

- **Edge cases** are ordinary criteria, usually `unwanted` or `complex_event`. There is no `edge_cases` array.
- **Correctness properties** are `ubiquitous` criteria whose `action` starts with "for any …". They are verified by a test of kind `property`.
- **Error handling** is an `unwanted` criterion with a `contract`. There is no `error_handling` array.

## Edge-case checklist

For every requirement, consider — and where it applies, write a criterion for:

- empty or null input;
- boundary values;
- the operation failing;
- authorization failing;
- a concurrent operation.

For anything that spawns processes, loops, retries or calls a service, also consider: timeout, resource cleanup on failure, an iteration cap, and the rule that library code returns errors instead of terminating the process.

## Language

Prefer measurable constraints over qualitative language. Avoid the words *appropriate*, *properly*, *correctly*, *reasonable*, *as needed* and *etc*: they are flagged and they make a criterion untestable.

Use the `glossary` for project-specific vocabulary a newcomer would not know. Do not define every identifier — the glossary is documentation, not a validation target.

## Execution paths

Each path is an end-to-end scenario with at least two `{actor, action}` steps. A path starts at a user action, CLI command, API call or scheduled trigger, ends at a concrete side effect, and uses logical actors. When several specs form a dependency chain, the last spec in the chain owns a path that crosses every spec boundary.

## Example

The fragment below is structurally correct for a recipe-manager system. Use your own domain — this is for shape only.

```json
{
  "spec_id": "07",
  "spec_name": "recipe_manager",
  "schema_version": 2,
  "introduction": "The recipe catalog stores user-created recipes and their ingredients.",
  "glossary": {
    "recipe": "A named collection of ingredients and preparation steps in the catalog."
  },
  "requirements": [
    {
      "id": "07-REQ-1",
      "title": "Recipe creation",
      "rationale": "A cook who cannot save a recipe has no reason to open the app twice.",
      "criteria": [
        {
          "id": "07-REQ-1.1",
          "pattern": "event_driven",
          "condition": "a client submits POST /recipes with a body carrying a name and at least one ingredient",
          "system": "recipe catalog",
          "action": "persist the recipe and return its assigned identifier",
          "contract": "HTTP 201 with body {id: string, name: string}"
        },
        {
          "id": "07-REQ-1.2",
          "pattern": "unwanted",
          "condition": "the submitted body has no name field",
          "system": "recipe catalog",
          "action": "reject the request and persist nothing",
          "contract": "HTTP 400 with body {error: string}; the catalog row count is unchanged"
        },
        {
          "id": "07-REQ-1.3",
          "pattern": "ubiquitous",
          "system": "recipe catalog",
          "action": "for any persisted recipe, return the same identifier for every subsequent read of it"
        }
      ]
    }
  ],
  "execution_paths": [
    {
      "id": "07-PATH-1",
      "title": "A cook saves a recipe and reads it back",
      "steps": [
        { "actor": "cook", "action": "submits POST /recipes with a recipe body" },
        { "actor": "recipe catalog", "action": "validates the body and writes a row" },
        { "actor": "recipe catalog", "action": "returns 201 with the assigned identifier" },
        { "actor": "cook", "action": "reads GET /recipes/{id} and receives the saved recipe" }
      ]
    }
  ]
}
```
