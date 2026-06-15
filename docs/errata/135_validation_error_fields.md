# Erratum: afspec.ValidationError Field Mismatch

**Spec:** 135 (v1.2 Skill Template and Validation Migration)
**Date:** 2026-06-15

## Divergence

The design document (design.md) states that `afspec.ValidationError` has
the following interface:

```python
@dataclass
class ValidationError:
    file: str
    rule: str
    severity: str   # "error" | "warning" | "hint"
    message: str
    line: int | None
```

The actual `afspec.ValidationError` (Pydantic v2 model) has only four
fields:

```python
class ValidationError(BaseModel):
    file: str = ""
    path: str = ""
    message: str = ""
    rule: str = ""
```

**Missing fields:** `severity` and `line` are absent from the actual
interface. The `path` field is a JSON element path string (e.g.,
`requirements.title`), not a line number.

## Impact on Requirements

- **135-REQ-2.1** references mapping `ValidationError.severity` and
  `ValidationError.line` -- these fields do not exist.
- **135-REQ-2.2** tests behavior for unknown severity values -- since
  there is no severity field at all, all findings from afspec should
  default to `"error"` severity.
- **TS-135-4, TS-135-5, TS-135-E3, TS-135-P1** all reference severity
  and/or line fields that do not exist on ValidationError.

## Adaptation

The `_map_afspec_findings()` function adapts as follows:

- `Finding.file` <- `ValidationError.file`
- `Finding.rule` <- `ValidationError.rule`
- `Finding.message` <- `ValidationError.message`
- `Finding.severity` <- `"error"` (hardcoded default; no severity on
  ValidationError)
- `Finding.line` <- `None` (hardcoded default; no line on
  ValidationError)
- `ValidationError.path` is unused in the mapping (JSON element path,
  not useful for Finding)

Tests have been adapted to match the real interface. TS-135-5 and
TS-135-E3 now verify the default severity behavior rather than testing
unknown severity values.
