The **$artifact_name** artifact you generated has validation errors. Fix them and resubmit using the same tool.

## Validation errors

$error_list

## Original artifact

```json
$original_json
```

Fix all listed errors and resubmit using the submit_$artifact_name tool.

When fixing glossary errors (cross-file-6), prefer REMOVING backticks from non-domain terms
over adding glossary entries. Only project-specific identifiers that need definitions should
be backtick-wrapped. Numeric literals, error message strings, standard library identifiers,
and raw code expressions should appear in plain prose without backticks.
