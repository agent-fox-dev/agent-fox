"""Tests for agentspec.tools — tool definition structure and schema.

Covers TS-03-18 through TS-03-20.
"""

from __future__ import annotations

from agentspec.tools import artifact_tool, assessment_tools, refinement_tools

# ===================================================================
# TS-03-18: Assessment tool definition structure
# ===================================================================


def test_assessment_tools_returns_single_tool():
    """TS-03-18: assessment_tools returns a list with one tool definition."""
    tools = assessment_tools()
    assert len(tools) == 1


def test_assessment_tool_has_correct_name():
    """TS-03-18: The tool has name='submit_assessment'."""
    tools = assessment_tools()
    tool = tools[0]
    assert tool["name"] == "submit_assessment"


def test_assessment_tool_has_description():
    """TS-03-18: The tool has a description field."""
    tools = assessment_tools()
    tool = tools[0]
    assert "description" in tool
    assert len(tool["description"]) > 0


def test_assessment_tool_schema_quality_enum():
    """TS-03-18/TS-03-4.6: input_schema enforces quality as enum."""
    tools = assessment_tools()
    schema = tools[0]["input_schema"]

    assert schema["type"] == "object"
    assert "quality" in schema["properties"]
    assert schema["properties"]["quality"]["enum"] == [
        "ready",
        "needs_refinement",
        "incomplete",
    ]


def test_assessment_tool_schema_required_fields():
    """TS-03-18: input_schema requires quality, summary, gaps, questions."""
    tools = assessment_tools()
    schema = tools[0]["input_schema"]

    assert set(schema["required"]) == {"quality", "summary", "gaps", "questions"}


def test_assessment_tool_schema_summary():
    """TS-03-18: input_schema has summary as a string property."""
    tools = assessment_tools()
    schema = tools[0]["input_schema"]

    assert "summary" in schema["properties"]
    assert schema["properties"]["summary"]["type"] == "string"


def test_assessment_tool_schema_gaps():
    """TS-03-18: input_schema has gaps as an array of strings."""
    tools = assessment_tools()
    schema = tools[0]["input_schema"]

    assert "gaps" in schema["properties"]
    assert schema["properties"]["gaps"]["type"] == "array"


def test_assessment_tool_schema_questions():
    """TS-03-18: input_schema has questions as an array of objects."""
    tools = assessment_tools()
    schema = tools[0]["input_schema"]

    assert "questions" in schema["properties"]
    assert schema["properties"]["questions"]["type"] == "array"
    question_schema = schema["properties"]["questions"]["items"]
    assert question_schema["type"] == "object"
    expected = {"id", "text", "context", "options", "required"}
    assert set(question_schema["required"]) == expected


# ===================================================================
# TS-03-19: Refinement tool definitions structure
# ===================================================================


def test_refinement_tools_returns_two_tools():
    """TS-03-19: refinement_tools returns a list with two tool definitions."""
    tools = refinement_tools()
    assert len(tools) == 2


def test_refinement_tools_correct_names():
    """TS-03-19: One tool is submit_prd_update, the other submit_assessment."""
    tools = refinement_tools()
    names = {t["name"] for t in tools}
    assert names == {"submit_prd_update", "submit_assessment"}


def test_refinement_prd_update_tool_schema():
    """TS-03-19: submit_prd_update tool has updated_prd field."""
    tools = refinement_tools()
    update_tool = next(t for t in tools if t["name"] == "submit_prd_update")
    assert "updated_prd" in update_tool["input_schema"]["properties"]


# ===================================================================
# TS-03-20: Artifact tool definition structure
# ===================================================================


def test_artifact_tool_returns_single_tool():
    """TS-03-20: artifact_tool returns a list with one tool definition."""
    tools = artifact_tool("requirements")
    assert len(tools) == 1


def test_artifact_tool_correct_name():
    """TS-03-20: The tool has per-artifact name (submit_requirements, etc.)."""
    for name in ["requirements", "test_spec", "tasks"]:
        tools = artifact_tool(name)
        tool = tools[0]
        assert tool["name"] == f"submit_{name}"


def test_artifact_tool_schema_fields():
    """TS-03-20: input_schema has content field with structured schema."""
    tools = artifact_tool("requirements")
    schema = tools[0]["input_schema"]
    assert "content" in schema["properties"]
    content_schema = schema["properties"]["content"]
    assert content_schema["type"] == "object"
    assert "properties" in content_schema
