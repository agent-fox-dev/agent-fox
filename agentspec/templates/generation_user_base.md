---
name: generation_user_base
description: Base user prompt for artifact generation
---
Generate the $artifact_name artifact for spec "$spec_id".

PRD Content:
$prd_text

$spec_landscape_block

$dependent_interfaces_block

$prior_artifacts_block

$language_block

Every ID you reference must come from the artifacts above. Your output is validated against the format's schema and its cross-file rules before it is written; anything that fails comes back to you with the rule that failed, and a generation that is still invalid after repair is discarded.