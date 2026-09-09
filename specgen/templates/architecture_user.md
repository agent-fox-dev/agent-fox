Write the architecture document for spec "{{spec_id}}" ({{spec_name}}), against the code in {{root}}.

It is the optional fifth artifact of a spec package, and it exists for one reason: to hold the design detail that does not belong in a PRD and cannot be expressed as an EARS criterion. Modules and their responsibilities, the interfaces between them with real type signatures, the data models, and the technology choices with their reasons.

PRD:

{{prd}}

Generated artifacts:
{{prior_block}}
Write it as Markdown with these sections:

- `# Architecture: {{spec_name}}` — the title.
- `## Overview` — two or three sentences.
- `## Architecture` — a Mermaid flowchart of the modules and the flow between them, followed by a numbered list giving each module one line of responsibility.
- `## Components and Interfaces` — the public surface: commands, functions, types, with real signatures in the project's language.
- `## Data Models` — the shapes that cross a boundary: configuration, file formats, wire payloads.
- `## Technology Stack` — what the implementation uses and why, naming the libraries already in the project rather than proposing new ones.
- `## Definition of Done` — when this spec is finished.

Two constraints:

1. **Fit the code that exists.** Use the project's own module names, its own vocabulary, and the libraries it already depends on. Read before you write.
2. **Put nothing load-bearing here.** A renderer working to a token budget drops this document first, so a coder must be able to implement every task without it. If a fact is required to implement, it belongs in a criterion or a task step.

Call submit_architecture once with the finished document.
