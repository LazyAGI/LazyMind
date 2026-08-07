from __future__ import annotations

import re
from collections.abc import Mapping
from dataclasses import dataclass, replace
from types import MappingProxyType
from typing import Any

import yaml  # type: ignore


_PATH_SEGMENT_RE = re.compile(r'^[A-Za-z0-9._-]+$')
_MAX_DESCRIPTION_LENGTH = 1024


class SkillDocumentError(ValueError):
    """Raised when a SKILL.md document violates the shared document contract."""

    def __init__(
        self,
        code: str,
        message: str,
        field: str | None = None,
        body: str | None = None,
    ):
        super().__init__(code, message, field, body)
        self.code = code
        self.field = field
        self.body = body
        self.message = message

    def __str__(self) -> str:
        return self.message


@dataclass(frozen=True)
class SkillDocument:
    metadata: Mapping[str, Any]
    body: str

    def __post_init__(self) -> None:
        object.__setattr__(self, 'metadata', _freeze_mapping(self.metadata))

    def with_metadata(self, **updates: Any) -> 'SkillDocument':
        metadata = dict(self.metadata)
        metadata.update(updates)
        return replace(self, metadata=metadata)

    def render(self) -> str:
        yaml_text = yaml.safe_dump(
            _thaw_value(self.metadata),
            allow_unicode=True,
            sort_keys=False,
        ).strip()
        return f'---\n{yaml_text}\n---\n{self.body}'


def _freeze_mapping(value: Mapping[Any, Any]) -> Mapping[Any, Any]:
    return MappingProxyType({key: _freeze_value(item) for key, item in value.items()})


def _freeze_value(value: Any) -> Any:
    if isinstance(value, Mapping):
        return _freeze_mapping(value)
    if isinstance(value, (list, tuple)):
        return tuple(_freeze_value(item) for item in value)
    if isinstance(value, (set, frozenset)):
        return frozenset(_freeze_value(item) for item in value)
    return value


def _thaw_value(value: Any) -> Any:
    if isinstance(value, Mapping):
        return {key: _thaw_value(item) for key, item in value.items()}
    if isinstance(value, tuple):
        return [_thaw_value(item) for item in value]
    if isinstance(value, frozenset):
        return {_thaw_value(item) for item in value}
    return value


def parse_skill_document(content: str) -> SkillDocument:
    if not isinstance(content, str) or not content.strip():
        raise SkillDocumentError(
            'empty_content',
            "action='create' requires a non-empty 'content' (full SKILL.md body).",
        )

    lines = content.splitlines(keepends=True)
    if not lines or lines[0].strip() != '---':
        raise SkillDocumentError(
            'missing_frontmatter',
            'SKILL.md must contain YAML frontmatter.',
        )
    end_index = next(
        (index for index, line in enumerate(lines[1:], start=1) if line.strip() == '---'),
        None,
    )
    if end_index is None:
        raise SkillDocumentError(
            'missing_frontmatter',
            'SKILL.md must contain YAML frontmatter.',
        )

    body = ''.join(lines[end_index + 1:])
    try:
        metadata = yaml.safe_load(''.join(lines[1:end_index]))
    except yaml.YAMLError as exc:
        raise SkillDocumentError(
            'invalid_frontmatter_yaml',
            'SKILL.md contains invalid YAML frontmatter.',
            body=body,
        ) from exc
    if not isinstance(metadata, dict):
        raise SkillDocumentError(
            'frontmatter_not_mapping',
            'SKILL.md YAML frontmatter must be a mapping.',
            body=body,
        )
    return SkillDocument(metadata=metadata, body=body)


def require_skill_name(value: Any) -> str:
    if not isinstance(value, str):
        raise SkillDocumentError(
            'invalid_field_type',
            "'name' must be a string.",
            field='name',
        )
    cleaned = value.strip()
    if not cleaned:
        raise SkillDocumentError(
            'missing_field',
            "'name' must be a non-empty skill name.",
            field='name',
        )
    if value != cleaned or cleaned in {'.', '..'} or not _PATH_SEGMENT_RE.fullmatch(cleaned):
        raise SkillDocumentError(
            'invalid_name',
            (
                f'Skill name {value!r} is invalid; only ASCII letters, digits, '
                "'-', '_' and '.' are allowed."
            ),
            field='name',
        )
    return cleaned


def require_valid_skill_document(
    content: str,
    expected_name: str | None = None,
) -> SkillDocument:
    document = parse_skill_document(content)
    if 'name' not in document.metadata:
        raise SkillDocumentError(
            'missing_field',
            "Frontmatter must include non-empty 'name'.",
            field='name',
        )
    raw_name = document.metadata.get('name')
    if not isinstance(raw_name, str):
        raise SkillDocumentError(
            'invalid_field_type',
            "Frontmatter field 'name' must be a string.",
            field='name',
        )
    if not raw_name.strip():
        raise SkillDocumentError(
            'missing_field',
            "Frontmatter must include non-empty 'name'.",
            field='name',
        )
    name = require_skill_name(raw_name)

    description = document.metadata.get('description')
    if not isinstance(description, str):
        if description is None:
            raise SkillDocumentError(
                'missing_field',
                "Frontmatter must include non-empty 'description'.",
                field='description',
            )
        raise SkillDocumentError(
            'invalid_field_type',
            "Frontmatter field 'description' must be a string.",
            field='description',
        )
    description = description.strip()
    if not description:
        raise SkillDocumentError(
            'missing_field',
            "Frontmatter must include non-empty 'description'.",
            field='description',
        )
    if len(description) > _MAX_DESCRIPTION_LENGTH:
        raise SkillDocumentError(
            'description_too_long',
            f'Description exceeds {_MAX_DESCRIPTION_LENGTH} characters.',
            field='description',
        )
    if not document.body.strip():
        raise SkillDocumentError(
            'empty_body',
            'SKILL.md must have markdown content after frontmatter.',
        )
    if expected_name is not None:
        expected_name = require_skill_name(expected_name)
        if name != expected_name:
            raise SkillDocumentError(
                'name_mismatch',
                (
                    f'SKILL.md frontmatter name {name!r} must match '
                    f'expected name {expected_name!r}.'
                ),
                field='name',
            )
    return document
