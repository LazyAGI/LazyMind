from __future__ import annotations

import re
from typing import Iterable

from lazymind.common.skill_document import (
    SkillDocumentError,
    parse_skill_document,
)
from lazymind.review.skill_organize.schemas import SkillSummary, SourceSkill

_HEADING_RE = re.compile(r'^(#{1,6})\s+(.+?)\s*$', re.M)
_LIST_ITEM_RE = re.compile(r'^\s*(?:[-*+]|\d+[.)])\s+(.+?)\s*$', re.M)
_STEP_HEADING_HINTS = {
    'sop',
    'workflow',
    'steps',
    'procedure',
    'process',
    '操作流程',
    '步骤',
    '流程',
}


def parse_skill_summaries(skills: Iterable[SourceSkill]) -> list[SkillSummary]:
    return [parse_skill_summary(skill) for skill in skills]


def parse_skill_summary(skill: SourceSkill) -> SkillSummary:
    try:
        document = parse_skill_document(skill.content)
        raw_description = document.metadata.get('description')
        description = raw_description.strip() if isinstance(raw_description, str) else ''
        body = document.body
    except SkillDocumentError as exc:
        description = ''
        body = exc.body if exc.body is not None else skill.content
    core_steps = _extract_core_steps(body)
    return SkillSummary(
        key=skill.key,
        name=skill.name,
        category=skill.category,
        description=description,
        core_steps=core_steps,
    )


def _extract_core_steps(body: str | None) -> list[str]:
    section = _extract_section_text(body, _STEP_HEADING_HINTS, compact=False)
    source = section or body or ''
    steps: list[str] = []
    for match in _LIST_ITEM_RE.finditer(source):
        item = _first_sentence(match.group(1))
        if item:
            steps.append(item)
        if len(steps) >= 8:
            break
    return steps


def _extract_section_text(body: str, headings: set[str], *, compact: bool = True) -> str:
    matches = list(_HEADING_RE.finditer(body or ''))
    for index, match in enumerate(matches):
        heading_level = len(match.group(1))
        title = _normalize_heading(match.group(2))
        if title not in headings:
            continue
        start = match.end()
        end = len(body)
        for next_match in matches[index + 1:]:
            if len(next_match.group(1)) <= heading_level:
                end = next_match.start()
                break
        value = body[start:end]
        return _compact_text(value) if compact else value.strip()
    return ''


def _normalize_heading(value: str) -> str:
    return re.sub(r'\s+', ' ', value.strip().strip(':：')).lower()


def _compact_text(value: str) -> str:
    lines = [line.strip() for line in value.splitlines()]
    text = ' '.join(line for line in lines if line)
    return re.sub(r'\s+', ' ', text).strip()


def _first_sentence(value: str) -> str:
    cleaned = _compact_text(value)
    if not cleaned:
        return ''
    parts = re.split(r'(?<=[。.!?])\s+', cleaned, maxsplit=1)
    return parts[0].strip()
