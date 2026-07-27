"""SpecAgent -- core agent for spec operations using shared LLM infrastructure.

Provides async methods for PRD assessment, refinement, and artifact
generation using the Anthropic messages API with tool use for structured
output.  Delegates LLM calls to :func:`agentfox.core.client.ai_call`
for model resolution, retry, caching, and token tracking.

Requirements: 03-REQ-1.*, 03-REQ-2.*, 03-REQ-3.*, 03-REQ-5.*
"""

from __future__ import annotations

import logging
from typing import TYPE_CHECKING, Any

from afspec import Requirements, Spec, Tasks, TestSpec  # type: ignore[import-untyped]
from afspec import validate_cross_file as afspec_validate_cross_file  # type: ignore[import-untyped]
from afspec import validate_schema as afspec_validate_schema  # type: ignore[import-untyped]
from anthropic import (
    APIConnectionError,
    APIStatusError,
    AuthenticationError,
    InternalServerError,
    PermissionDeniedError,
    RateLimitError,
)
from pydantic import ValidationError as PydanticValidationError

from agentspec.errors import AgentError
from agentspec.prompts import (
    assessment_system_prompt,
    assessment_user_prompt,
    generation_system_prompt,
    generation_user_prompt,
    refinement_system_prompt,
    refinement_user_prompt,
    repair_user_prompt,
)
from agentspec.tools import artifact_tool, assessment_tools, refinement_tools

if TYPE_CHECKING:
    from pydantic import BaseModel

    from agentspec.session import Assessment

logger = logging.getLogger(__name__)

_DEFAULT_MAX_TOKENS = 65536
_MAX_REPAIR_ATTEMPTS = 2

_ARTIFACT_MODELS: dict[str, type[BaseModel]] = {
    "requirements": Requirements,
    "test_spec": TestSpec,
    "tasks": Tasks,
}


def _classify_sdk_error(exc: Exception) -> tuple[str, int | None]:
    """Return (category, http_status) for an Anthropic SDK exception."""
    if isinstance(exc, RateLimitError):
        return "rate_limit", 429
    if isinstance(exc, AuthenticationError):
        return "auth", 401
    if isinstance(exc, PermissionDeniedError):
        return "permission", 403
    if isinstance(exc, InternalServerError):
        return "transient", getattr(exc, "status_code", 500)
    if isinstance(exc, APIConnectionError):
        return "transient", None
    if isinstance(exc, APIStatusError):
        code = exc.status_code
        if code == 529:
            return "overloaded", 529
        if code in (400, 413):
            return "input", code
        return "internal", code
    return "internal", None


class SpecAgent:
    """Core agent for spec operations using shared LLM infrastructure.

    Delegates LLM calls to ``ai_call()`` from ``agentfox.core.client``,
    which provides model tier resolution, platform-aware client creation,
    prompt caching, retry logic, and token tracking.
    """

    def __init__(self, model_tier: str) -> None:
        """Initialize with a model tier name.

        Args:
            model_tier: A model tier name (e.g. ``"STANDARD"``) or a
                direct model ID (e.g. ``"claude-sonnet-4-6"``).
                Resolved via ``resolve_model()`` inside ``ai_call()``.
        """
        self._model = model_tier

    # -- public methods ---------------------------------------------------

    async def assess_prd(
        self,
        prd_text: str,
        spec_name: str,
        *,
        spec_landscape: list[dict[str, Any]] | None = None,
    ) -> Assessment:
        """Send PRD to agent for assessment.

        Validates the input, sends the PRD to the Anthropic messages API
        with the assessment prompt and tool definition, then parses and
        returns the structured Assessment.

        Args:
            prd_text: The PRD markdown text to assess.
            spec_name: The name of the spec being assessed.
            spec_landscape: Optional list of existing spec metadata for
                cross-spec awareness during assessment.

        Returns:
            An ``Assessment`` with quality, summary, gaps, and questions.

        Raises:
            AgentError: If *prd_text* is empty, the API call fails
                permanently, or the response cannot be parsed.
        """
        if not prd_text or not prd_text.strip():
            raise AgentError("PRD text must not be empty", category="validation")

        system = assessment_system_prompt()
        user_msg = assessment_user_prompt(prd_text, spec_name, spec_landscape=spec_landscape)
        messages: list[dict[str, str]] = [
            {"role": "user", "content": user_msg},
        ]
        tools = assessment_tools()

        response = await self._call_api(messages, tools, system=system)
        tool_input = self._extract_tool_call(response, "submit_assessment")
        return self._parse_assessment(tool_input)

    async def refine_prd(
        self,
        prd_text: str,
        answers: dict[str, str],
        previous_assessment: Assessment,
        *,
        spec_landscape: list[dict[str, Any]] | None = None,
    ) -> tuple[str, Assessment]:
        """Send answers, get updated PRD and new assessment.

        Validates that answers are non-empty and that all answer IDs
        correspond to questions in the previous assessment. Sends the
        original PRD, answers, and previous assessment to the API, then
        extracts both the updated PRD text and a fresh Assessment.

        Args:
            prd_text: The current PRD markdown text.
            answers: Dict mapping question IDs to answer text.
            previous_assessment: The most recent Assessment with
                questions the user is answering.
            spec_landscape: Optional list of existing spec metadata for
                cross-spec awareness during refinement.

        Returns:
            A tuple of ``(updated_prd_text, new_assessment)`` where
            *updated_prd_text* is the revised PRD body (no frontmatter)
            and *new_assessment* is the fresh Assessment of the updated
            PRD.

        Raises:
            AgentError: If *answers* is empty, contains unrecognized
                question IDs, or the API call fails.
        """
        # Validate answers not empty (03-REQ-2.E1)
        if not answers:
            raise AgentError("Refinement requires answers; no answers provided", category="validation")

        # Validate answer IDs match assessment questions (03-REQ-2.E2)
        valid_ids = {q.id for q in previous_assessment.questions}
        unrecognized = set(answers.keys()) - valid_ids
        if unrecognized:
            raise AgentError(
                f"Unrecognized question IDs in answers: {', '.join(sorted(unrecognized))}",
                category="validation",
            )

        system = refinement_system_prompt()
        user_msg = refinement_user_prompt(prd_text, answers, previous_assessment, spec_landscape=spec_landscape)
        messages: list[dict[str, str]] = [
            {"role": "user", "content": user_msg},
        ]
        tools = refinement_tools()

        response = await self._call_api(messages, tools, system=system)

        # Extract updated PRD (03-REQ-2.2)
        prd_update = self._extract_tool_call(response, "submit_prd_update")
        updated_prd_text: str = prd_update["updated_prd"]

        # Extract new assessment (03-REQ-2.E3)
        # The model may not produce both tool calls in one response
        # (e.g. if the updated PRD is large). Fall back to a second
        # API call for the assessment.
        try:
            assessment_input = self._extract_tool_call(response, "submit_assessment")
        except AgentError:
            assessment_input = await self._request_assessment(updated_prd_text, system)
        new_assessment = self._parse_assessment(assessment_input)

        return updated_prd_text, new_assessment

    async def generate_artifacts(
        self,
        prd_text: str,
        spec_id: str,
        spec_name: str,
        *,
        existing_artifacts: dict[str, Any] | None = None,
        on_artifact: Any = None,
        dependent_interfaces: list[dict[str, Any]] | None = None,
        spec_landscape: list[dict[str, Any]] | None = None,
    ) -> dict[str, Any]:
        """Generate requirements, test_spec, and tasks content.

        Generates three artifacts in a fixed order: ``requirements``,
        ``test_spec``, ``tasks``.  Each artifact is generated by a
        separate API call whose prompt includes all previously generated
        artifacts as context.  Each artifact is validated by constructing
        an afspec Pydantic model before proceeding.

        Args:
            prd_text: The accepted PRD markdown text.
            spec_id: The spec identifier.
            spec_name: The spec name.
            existing_artifacts: Optional dict of previously generated
                artifacts to skip re-generation.  Used for resuming
                after partial failures.
            on_artifact: Optional callback called with
                ``(artifact_name, model)`` after each artifact is
                generated and validated.  Used for incremental disk
                writes.
            dependent_interfaces: Optional list of interface summaries
                from upstream dependency specs.
            spec_landscape: Optional list of existing spec metadata for
                cross-spec awareness during generation.

        Returns:
            A dict mapping artifact name (``"requirements"``,
            ``"test_spec"``, ``"tasks"``) to its afspec model instance.

        Raises:
            AgentError: If *prd_text* is empty, the API call fails,
                the model does not invoke the tool, or an artifact
                fails validation.
        """
        # Validate PRD not empty (03-REQ-3.E1)
        if not prd_text or not prd_text.strip():
            raise AgentError("PRD text must not be empty", category="validation")

        artifact_names = ["requirements", "test_spec", "tasks"]
        results: dict[str, Any] = dict(existing_artifacts) if existing_artifacts else {}

        system = generation_system_prompt()

        for artifact_name in artifact_names:
            if artifact_name in results:
                continue

            if artifact_name == "tasks" and results:
                prior = self._slim_prior_artifacts_context(results)
            elif results:
                prior = self._prior_artifacts_context(results)
            else:
                prior = None
            user_msg = generation_user_prompt(
                prd_text,
                artifact_name,
                prior_artifacts=prior,
                spec_id=spec_id,
                dependent_interfaces=dependent_interfaces,
                spec_landscape=spec_landscape,
            )
            messages: list[dict[str, str]] = [
                {"role": "user", "content": user_msg},
            ]
            tools = artifact_tool(artifact_name)

            response = await self._call_api(messages, tools, system=system, temperature=0.2)

            tool_name = f"submit_{artifact_name}"
            tool_input = self._extract_tool_call(response, tool_name)
            content: dict[str, Any] = tool_input["content"]

            content["spec_id"] = spec_id
            content["spec_name"] = spec_name
            content["schema_version"] = 1

            model_cls = _ARTIFACT_MODELS[artifact_name]
            try:
                artifact_model = model_cls.model_validate(content)
            except PydanticValidationError as exc:
                raise AgentError(
                    f"Artifact '{artifact_name}' failed validation: {exc}",
                    category="validation",
                ) from exc

            artifact_model = await self._repair_if_needed(
                artifact_name, artifact_model, results, system, tools, tool_name, model_cls, spec_id, spec_name
            )

            results[artifact_name] = artifact_model

            if on_artifact is not None:
                on_artifact(artifact_name, artifact_model)

        return results

    # -- internal methods -------------------------------------------------

    async def _repair_if_needed(
        self,
        artifact_name: str,
        artifact_model: Any,
        prior_results: dict[str, Any],
        system: str,
        tools: list[dict[str, Any]],
        tool_name: str,
        model_cls: type,
        spec_id: str,
        spec_name: str,
    ) -> Any:
        """Run schema validation on a generated artifact and ask the LLM to fix errors.

        Constructs a minimal Spec from the artifact and any prior results,
        runs afspec schema validation, and if errors are found, sends them
        back to the LLM for repair. Retries up to _MAX_REPAIR_ATTEMPTS times.
        """
        for attempt in range(_MAX_REPAIR_ATTEMPTS):
            spec_kwargs: dict[str, Any] = {artifact_name: artifact_model}
            for name, model in prior_results.items():
                spec_kwargs[name] = model
            mini_spec = Spec(**spec_kwargs)

            schema_errors = afspec_validate_schema(mini_spec)
            if artifact_name == "requirements":
                schema_errors.extend(afspec_validate_cross_file(mini_spec))
            relevant = [e for e in schema_errors if artifact_name in e.file or artifact_name.replace("_", "") in e.file]
            if not relevant:
                return artifact_model

            error_strs = [e.message for e in relevant]
            logger.warning(
                "Artifact '%s' has %d schema errors (repair attempt %d/%d)",
                artifact_name,
                len(relevant),
                attempt + 1,
                _MAX_REPAIR_ATTEMPTS,
            )

            content_dict = artifact_model.model_dump(by_alias=True, exclude_none=True)
            user_msg = repair_user_prompt(artifact_name, content_dict, error_strs)
            messages: list[dict[str, str]] = [
                {"role": "user", "content": user_msg},
            ]

            response = await self._call_api(messages, tools, system=system, temperature=0.2)
            tool_input = self._extract_tool_call(response, tool_name)
            content: dict[str, Any] = tool_input["content"]

            content["spec_id"] = spec_id
            content["spec_name"] = spec_name
            content["schema_version"] = 1

            try:
                artifact_model = model_cls.model_validate(content)
            except PydanticValidationError:
                logger.warning("Repair attempt %d for '%s' failed Pydantic validation", attempt + 1, artifact_name)
                break

        return artifact_model

    @staticmethod
    def _prior_artifacts_context(
        results: dict[str, Any],
    ) -> dict[str, Any]:
        """Convert model instances to dicts for prompt context."""
        context: dict[str, Any] = {}
        for name, value in results.items():
            if hasattr(value, "model_dump"):
                context[name] = value.model_dump(by_alias=True, exclude_none=True)
            else:
                context[name] = value
        return context

    @staticmethod
    def _slim_prior_artifacts_context(
        results: dict[str, Any],
    ) -> dict[str, Any]:
        """Convert model instances to slimmed dicts for tasks-generation context.

        Produces a lighter-weight representation to keep the prompt within
        token budgets when generating tasks:

        - **requirements**: keeps requirement IDs, titles, criterion IDs
          with EARS patterns, property IDs, path IDs, and glossary.
          Strips verbose action text and full step content.
        - **test_spec**: keeps only test-case ID to requirement-ID mappings.
        """
        context: dict[str, Any] = {}
        for name, value in results.items():
            full = value.model_dump(by_alias=True, exclude_none=True) if hasattr(value, "model_dump") else dict(value)

            if name == "requirements":
                slim: dict[str, Any] = {}
                if "glossary" in full:
                    slim["glossary"] = full["glossary"]
                if "requirements" in full:
                    slim_reqs = []
                    for req in full["requirements"]:
                        slim_req: dict[str, Any] = {"id": req.get("id", "")}
                        if "title" in req:
                            slim_req["title"] = req["title"]
                        if "criteria" in req:
                            slim_req["criteria"] = [
                                {
                                    k: v
                                    for k, v in c.items()
                                    if k in ("id", "ears_pattern")
                                }
                                for c in req["criteria"]
                            ]
                        slim_reqs.append(slim_req)
                    slim["requirements"] = slim_reqs
                if "properties" in full:
                    slim["properties"] = [
                        {"id": p.get("id", "")} for p in full["properties"]
                    ]
                if "paths" in full:
                    slim["paths"] = [
                        {"id": p.get("id", "")} for p in full["paths"]
                    ]
                context[name] = slim

            elif name == "test_spec":
                test_cases = full.get("test_cases", [])
                context[name] = {
                    "test_cases": [
                        {
                            "id": tc.get("id", ""),
                            "requirement_id": tc.get("requirement_id", ""),
                        }
                        for tc in test_cases
                    ],
                }

            else:
                context[name] = full

        return context

    async def _request_assessment(
        self,
        prd_text: str,
        system: str | None,
    ) -> dict[str, Any]:
        """Make a separate API call to get an assessment of a PRD.

        Used as a fallback when the refinement response did not include
        the ``submit_assessment`` tool call alongside the PRD update.
        """
        logger.debug("submit_assessment missing from refinement response; making a follow-up API call")
        messages: list[dict[str, str]] = [
            {
                "role": "user",
                "content": (
                    "Assess the following PRD and provide your "
                    "evaluation using the submit_assessment tool."
                    f"\n\n{prd_text}"
                ),
            },
        ]
        tools = assessment_tools()
        response = await self._call_api(messages, tools, system=system)
        return self._extract_tool_call(response, "submit_assessment")

    async def _call_api(
        self,
        messages: list[dict[str, str]],
        tools: list[dict[str, Any]],
        system: str | None = None,
        *,
        max_tokens: int = _DEFAULT_MAX_TOKENS,
        temperature: float | None = None,
    ) -> Any:
        """Send messages to the Anthropic API via the shared ``ai_call()`` helper.

        Delegates to :func:`agentfox.core.client.ai_call` which provides
        model tier resolution, platform-aware client creation, prompt caching,
        retry with exponential backoff, and token tracking.

        Args:
            messages: The conversation messages to send.
            tools: Tool definitions for structured output.
            system: Optional system prompt.
            max_tokens: Maximum tokens in the response.
            temperature: Optional sampling temperature override.
                When ``None`` (the default), the API default is used.

        Returns:
            The raw API response message (``anthropic.types.Message``).

        Raises:
            AgentError: On permanent failure or exhausted retries.
                The original exception is set as ``__cause__``.
        """
        from agentfox.core.client import ai_call

        extra_kwargs: dict[str, Any] = {}
        if tools:
            extra_kwargs["tools"] = tools
            extra_kwargs["tool_choice"] = {"type": "any"}
        if temperature is not None:
            extra_kwargs["temperature"] = temperature

        try:
            _text, response = await ai_call(
                model_tier=self._model,
                max_tokens=max_tokens,
                messages=messages,
                system=system,
                context="spec-generation",
                **extra_kwargs,
            )
        except (
            RateLimitError,
            InternalServerError,
            APIConnectionError,
            APIStatusError,
        ) as exc:
            category, http_status = _classify_sdk_error(exc)
            is_retryable = category in ("rate_limit", "transient", "overloaded")
            raise AgentError(
                f"API call failed: {exc}",
                category=category,
                retryable=is_retryable,
                http_status=http_status,
            ) from exc

        self._check_stop_reason(response)
        return response

    @staticmethod
    def _check_stop_reason(response: Any) -> None:
        """Raise ``AgentError`` for non-normal stop reasons.

        Inspects ``response.stop_reason`` and raises with a descriptive
        message for ``refusal``, ``model_context_window_exceeded``, and
        ``pause_turn``.  Normal stop reasons (``end_turn``, ``tool_use``,
        ``max_tokens``) pass through silently.

        Args:
            response: The API response message.

        Raises:
            AgentError: If the stop reason indicates a refusal, context
                window overflow, or server-side iteration limit.
        """
        stop_reason = getattr(response, "stop_reason", None)
        if stop_reason == "refusal":
            raise AgentError(
                "API refused the request (safety classifier refusal). "
                "Check prompt content and retry with adjusted input.",
                category="refusal",
            )
        if stop_reason == "model_context_window_exceeded":
            raise AgentError(
                "Context window exceeded. Reduce input size or split "
                "the request into smaller parts.",
                category="context_window",
            )
        if stop_reason == "pause_turn":
            raise AgentError(
                "Server-side tool iteration limit reached (pause_turn). "
                "The model was paused before completing the response.",
                category="pause_turn",
            )

    def _extract_tool_call(
        self,
        response: Any,
        tool_name: str,
    ) -> dict[str, Any]:
        """Extract the input dict from a tool_use content block.

        Searches the response content blocks for a ``tool_use`` block
        with the given name and returns its ``input`` dict.

        Args:
            response: The API response message.
            tool_name: The expected tool name to find.

        Returns:
            The tool input dict.

        Raises:
            AgentError: If the tool was not called or the response
                contains no matching tool_use blocks.
        """
        for block in response.content:
            if getattr(block, "type", None) == "tool_use" and getattr(block, "name", None) == tool_name:
                return block.input  # type: ignore[no-any-return]

        logger.debug(
            "Response content blocks: %s",
            [(b.type, getattr(b, "name", None)) for b in response.content],
        )
        raise AgentError(
            f"Model did not produce structured output: tool '{tool_name}' was not called",
            category="validation",
        )

    def _parse_assessment(self, tool_input: dict[str, Any]) -> Assessment:
        """Validate and construct an Assessment from tool input.

        Enforces the quality enum, required fields, and the invariant
        that non-ready assessments must include questions.

        Args:
            tool_input: The raw dict from the submit_assessment
                tool call.

        Returns:
            A validated ``Assessment`` instance.

        Raises:
            AgentError: If required fields are missing or invalid.
        """
        from agentspec.session import Assessment, Question  # lazy: avoid circular import

        # Validate quality enum (03-REQ-1.2)
        valid_qualities = {"ready", "needs_refinement", "incomplete"}
        quality = tool_input.get("quality")
        if quality not in valid_qualities:
            raise AgentError(
                f"Invalid quality value: {quality!r}; expected one of {sorted(valid_qualities)}",
                category="validation",
            )

        # Validate required fields (03-REQ-1.E2)
        missing = [f for f in ("summary", "gaps", "questions") if f not in tool_input]
        if missing:
            raise AgentError(
                f"Assessment is missing required fields: {', '.join(missing)}",
                category="validation",
            )

        summary: str = tool_input["summary"]
        gaps: list[str] = tool_input["gaps"]
        questions_data: list[dict[str, Any]] = tool_input["questions"]

        # Non-ready assessments must have questions (03-REQ-1.5)
        if quality != "ready" and not questions_data:
            raise AgentError(
                f"Assessment with quality {quality!r} must include at least one question",
                category="validation",
            )

        # Build Question objects
        questions = [
            Question(
                id=q["id"],
                text=q["text"],
                context=q["context"],
                options=q.get("options", []),
                required=q.get("required", False),
            )
            for q in questions_data
        ]

        return Assessment(
            quality=quality,
            summary=summary,
            gaps=gaps,
            questions=questions,
        )
