// Package specgen turns a product idea into a complete, validated version 2
// specification package.
//
// It is the `afspec` skill rebuilt as a program. The skill is ~750 lines of
// markdown describing a seven-step workflow across fifteen CLI subcommands,
// with a refinement loop that stops to ask a person questions. This package
// keeps the two things that genuinely need a model — write the PRD, and
// generate the three artifacts — and turns everything else into a mechanism:
//
//   - The workflow is the pipeline, not a numbered list the model may skip.
//   - The refinement loop is gone. Nobody is waiting to answer questions, so
//     the PRD phase resolves every open question itself, records each in a
//     `## Design Decisions` section, and reports the ones it is least sure of
//     as `open_questions` in the result. A caller that wants a human in the
//     loop reads that array; a caller that does not gets a finished spec.
//   - Each artifact is submitted through a tool whose schema is the format's
//     own JSON Schema, and whose handler runs every cross-file rule decidable
//     at that point. A violation is a tool error the loop hands back, so the
//     repair loop IS the loop — there is no second conversation assembled by
//     hand, and the system prompt and tool schemas stay byte-identical across
//     the attempt and its correction so the provider's cache prefix survives.
//   - The "post-generation language audit" the skill asks a human to perform
//     is a check: the project's real test runner and linter are detected in
//     Go, and a tasks artifact that names another ecosystem's runner is
//     refused with the detected commands in the message.
//   - The spec is validated, and activated only if it validates. The
//     traceability matrix in the result is derived, never stored.
//
// The three generation phases are separate agents rather than one
// conversation, because sharing a transcript would carry the requirements
// phase's reasoning into the tests that are supposed to check the
// requirements.
package specgen
