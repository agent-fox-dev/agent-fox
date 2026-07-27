The **$artifact_name** artifact you generated has validation errors. Fix them and resubmit using the same tool.

## Validation errors

$error_list

## Original artifact

```json
$original_json
```

Fix all listed errors and resubmit using the submit_$artifact_name tool.

## Repair guidance

When fixing glossary errors (cross-file-6), prefer REMOVING backticks from non-domain terms
over adding glossary entries. Only project-specific identifiers that need definitions should
be backtick-wrapped. Numeric literals, error message strings, standard library identifiers,
and raw code expressions should appear in plain prose without backticks.

- **Missing return_contract on unwanted criteria (cross-file-10):** Add a concrete return_contract describing the error response the caller observes.
- **Missing test coverage (cross-file-2/3/4):** Add the missing test case, property test, or smoke test for the referenced requirement/property/path ID.
- **EARS pattern field mismatch:** Check the pattern name and ensure only the correct fields are present (e.g., `event_driven` needs `trigger`, not `condition` or `state`).
- **Preserve all quality rules** from the original generation instructions. Do not introduce vague language, empty arrays, or null fields that were previously populated.
