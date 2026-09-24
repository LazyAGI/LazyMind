from __future__ import annotations

import json
import re
from pathlib import Path
from typing import Any
from uuid import uuid4

from lazyllm import LOG

from lazymind.common.skill.document import (
    SkillDocument,
    SkillDocumentError,
    parse_skill_document,
    require_valid_skill_document,
)
from lazymind.common.skill.remote_store import SkillRemoteStore
from lazymind.common.skill.storage_key import parse_skill_storage_key
from lazymind.review.traj_to_skill.config import STAGE_FILES, STAGE_WHEN_TO_USE
from lazymind.review.traj_to_skill.json_call import call_json
from lazymind.review.traj_to_skill.reports import finish_stage_report, start_stage, write_json_file
from lazymind.review.traj_to_skill.schemas import SkillReviewResolution

_WHEN_TO_USE_HEADINGS = {
    'when to use',
    'when to use this skill',
    '适用场景',
    '何时使用',
}
_HEADING_RE = re.compile(r'^(#{1,6})\s+(.+?)\s*$', re.M)

_WHEN_TO_USE_SCHEMA = {
    'title': 'traj_to_skill_when_to_use',
    'type': 'object',
    'properties': {
        'patches': {
            'type': 'array',
            'items': {
                'type': 'object',
                'properties': {
                    'skill_key': {'type': 'string', 'minLength': 1},
                    'when_to_use': {'type': 'string', 'minLength': 1},
                    'reason': {'type': 'string'},
                },
                'required': ['skill_key', 'when_to_use'],
                'additionalProperties': False,
            },
        },
        'conflicts': {
            'type': 'array',
            'items': {
                'type': 'object',
                'properties': {
                    'id': {'type': 'string', 'minLength': 1},
                    'skill_keys': {
                        'type': 'array',
                        'minItems': 2,
                        'items': {'type': 'string', 'minLength': 1},
                    },
                    'reason': {'type': 'string'},
                },
                'required': ['id', 'skill_keys', 'reason'],
                'additionalProperties': False,
            },
        },
    },
    'required': ['patches', 'conflicts'],
    'additionalProperties': False,
}


def when_to_use_prompt(skill_summaries: list[dict[str, Any]]) -> str:
    return f"""
You are the Skill Review When to Use auditor.

Every installed skill is routed from its When to Use text (frontmatter `description`,
plus any When to Use body section). Vague or overlapping When to Use text causes
several skills to match the same user intent.

Audit the provided skills and do two things:

1. Clarify: rewrite When to Use so nearby skills have exclusive duties, priority, and
   non-overlap boundaries. Keep the original language of each skill.
2. Conflict: when two or more skills still cover the same intent after clarification
   is impossible without inventing new capabilities, emit a conflict group.

# Rules

- Do not invent capabilities, tools, or domains that the source skill does not already cover.
- Prefer a single routing sentence. Mention what the skill should NOT handle when a neighbor exists.
- A skill with a unique, already-clear boundary should not appear in patches or conflicts.
- A skill key must appear in at most one of: a clarify patch, or one conflict group.
- Conflict groups are for remaining overlap. Do not auto-pick a winner.
- Keep invocation policy separate: never add manual-only or explicit-selection requirements to descriptions.
- skill_key must exactly match an input key such as "internal/name".

# Output

Return ONLY valid JSON:
{{
  "patches": [
    {{
      "skill_key": "internal/example",
      "when_to_use": "Use when ... Do not use when ...",
      "reason": "why this boundary is exclusive"
    }}
  ],
  "conflicts": [
    {{
      "id": "overlap-mail-search",
      "skill_keys": ["internal/a", "internal/b"],
      "reason": "why they still collide"
    }}
  ]
}}

# Skills
{json.dumps(skill_summaries, ensure_ascii=False, indent=2)}
"""


def apply_when_to_use_description(content: str, when_to_use: str, *, expected_name: str) -> str:
    document = require_valid_skill_document(content, expected_name=expected_name)
    updated = document.with_metadata(description=when_to_use.strip())
    return SkillDocument(
        metadata=updated.metadata,
        body=_upsert_when_to_use_section(updated.body, when_to_use.strip()),
    ).render()


def build_when_to_use_audit(
    llm,
    *,
    remote_store: SkillRemoteStore,
    pending_resolutions: list[SkillReviewResolution] | None = None,
    artifact_dir: Path | None = None,
) -> tuple[list[SkillReviewResolution], list[dict[str, Any]], list[dict[str, Any]], dict[str, Any]]:
    started_at = start_stage()
    catalog = _load_skill_catalog(remote_store, pending_resolutions or [])
    if len(catalog) < 2:
        report = finish_stage_report(
            STAGE_WHEN_TO_USE,
            started_at,
            input_count=len(catalog),
            output_count=0,
            status='completed',
        )
        payload = {'patches': [], 'conflicts': [], 'skills': list(catalog)}
        if artifact_dir is not None:
            write_json_file(artifact_dir / STAGE_FILES[STAGE_WHEN_TO_USE], payload)
        return [], [], [], report

    summaries = [_skill_summary(item) for item in catalog.values()]
    raw = call_json(llm, when_to_use_prompt(summaries), _WHEN_TO_USE_SCHEMA)
    patches, conflicts = _normalize_audit(raw, catalog)
    pending_updates = [item for item in patches if catalog[item['skill_key']].get('pending')]
    resolutions = _patches_to_resolutions(
        [item for item in patches if not catalog[item['skill_key']].get('pending')],
        catalog,
    )
    payload = {
        'patches': patches,
        'conflicts': conflicts,
        'skills': summaries,
    }
    if artifact_dir is not None:
        write_json_file(artifact_dir / STAGE_FILES[STAGE_WHEN_TO_USE], payload)
    report = finish_stage_report(
        STAGE_WHEN_TO_USE,
        started_at,
        input_count=len(catalog),
        output_count=len(resolutions) + len(conflicts),
        status='completed',
    )
    return resolutions, conflicts, pending_updates, report


def _load_skill_catalog(
    remote_store: SkillRemoteStore,
    pending_resolutions: list[SkillReviewResolution],
) -> dict[str, dict[str, Any]]:
    catalog: dict[str, dict[str, Any]] = {}
    try:
        packages = remote_store.list_packages()
    except Exception as exc:
        LOG.warning(f'[TrajToSkill] failed to list skills for when-to-use audit: {exc}')
        packages = []
    for package in packages:
        category = str(package.get('category') or '').strip()
        name = str(package.get('name') or '').strip()
        if not category or not name:
            continue
        key = f'{category}/{name}'
        try:
            content = remote_store.read_skill_md(category, name)
        except Exception as exc:
            LOG.warning(f'[TrajToSkill] failed to read {key} for when-to-use audit: {exc}')
            continue
        catalog[key] = {
            'skill_key': key,
            'category': category,
            'name': name,
            'content': content,
            'pending': False,
        }
    for record in pending_resolutions:
        content = str(record.skill_content or '').strip()
        if not content:
            continue
        if record.type == 'patch' and record.target_skill_key:
            key = record.target_skill_key.strip()
        else:
            key = f'internal/{record.skill_name}'.strip()
        try:
            category, name = parse_skill_storage_key(key)
            key = f'{category}/{name}'
        except Exception:
            continue
        catalog[key] = {
            'skill_key': key,
            'category': category,
            'name': name,
            'content': content,
            'pending': True,
            'pending_id': record.id,
        }
    return catalog


def _skill_summary(item: dict[str, Any]) -> dict[str, Any]:
    content = str(item.get('content') or '')
    description = ''
    section = ''
    try:
        document = parse_skill_document(content)
        raw = document.metadata.get('description')
        description = raw.strip() if isinstance(raw, str) else ''
        section = _extract_when_to_use_section(document.body)
    except SkillDocumentError:
        description = ''
    return {
        'skill_key': item['skill_key'],
        'name': item['name'],
        'category': item['category'],
        'when_to_use': section or description,
        'description': description,
    }


def _normalize_audit(
    raw: dict[str, Any],
    catalog: dict[str, dict[str, Any]],
) -> tuple[list[dict[str, Any]], list[dict[str, Any]]]:
    used: set[str] = set()
    patches: list[dict[str, Any]] = []
    for item in raw.get('patches') or []:
        key = str(item.get('skill_key') or '').strip()
        when_to_use = str(item.get('when_to_use') or '').strip()
        if key not in catalog or key in used or not when_to_use:
            continue
        current = str(_skill_summary(catalog[key]).get('when_to_use') or '').strip()
        if when_to_use == current:
            continue
        used.add(key)
        patches.append({
            'skill_key': key,
            'when_to_use': when_to_use,
            'reason': str(item.get('reason') or '').strip(),
        })

    conflicts: list[dict[str, Any]] = []
    for item in raw.get('conflicts') or []:
        keys = [
            str(key).strip()
            for key in (item.get('skill_keys') or [])
            if str(key).strip() in catalog and str(key).strip() not in used
        ]
        keys = list(dict.fromkeys(keys))
        if len(keys) < 2:
            continue
        used.update(keys)
        conflicts.append({
            'id': str(item.get('id') or uuid4()),
            'skill_keys': keys,
            'skills': [
                {
                    'skill_key': key,
                    'name': catalog[key]['name'],
                    'category': catalog[key]['category'],
                    'current_when_to_use': _skill_summary(catalog[key]).get('when_to_use') or '',
                }
                for key in keys
            ],
            'reason': str(item.get('reason') or '').strip(),
        })
    return patches, conflicts


def _patches_to_resolutions(
    patches: list[dict[str, Any]],
    catalog: dict[str, dict[str, Any]],
) -> list[SkillReviewResolution]:
    resolutions: list[SkillReviewResolution] = []
    for item in patches:
        source = catalog[item['skill_key']]
        if source.get('pending'):
            continue
        name = source['name']
        try:
            content = apply_when_to_use_description(
                source['content'],
                item['when_to_use'],
                expected_name=name,
            )
        except Exception as exc:
            LOG.warning(
                f'[TrajToSkill] skipped when-to-use patch for {item["skill_key"]}: {exc}'
            )
            continue
        resolutions.append(SkillReviewResolution(
            id=str(uuid4()),
            skill_name=name,
            target_skill_key=item['skill_key'],
            type='patch',
            skill_content=content,
            summary=item.get('reason') or 'Normalize When to Use routing boundary.',
        ))
    return resolutions


def _extract_when_to_use_section(body: str) -> str:
    matches = list(_HEADING_RE.finditer(body or ''))
    for index, match in enumerate(matches):
        title = re.sub(r'\s+', ' ', match.group(2).strip().strip(':：')).lower()
        if title not in _WHEN_TO_USE_HEADINGS:
            continue
        start = match.end()
        end = len(body)
        heading_level = len(match.group(1))
        for next_match in matches[index + 1:]:
            if len(next_match.group(1)) <= heading_level:
                end = next_match.start()
                break
        return re.sub(r'\s+', ' ', body[start:end]).strip()
    return ''


def _upsert_when_to_use_section(body: str, when_to_use: str) -> str:
    section = f'## When to Use\n\n{when_to_use.strip()}\n'
    matches = list(_HEADING_RE.finditer(body or ''))
    for index, match in enumerate(matches):
        title = re.sub(r'\s+', ' ', match.group(2).strip().strip(':：')).lower()
        if title not in _WHEN_TO_USE_HEADINGS:
            continue
        heading_level = len(match.group(1))
        end = len(body)
        for next_match in matches[index + 1:]:
            if len(next_match.group(1)) <= heading_level:
                end = next_match.start()
                break
        prefix = body[:match.start()].rstrip()
        suffix = body[end:].lstrip()
        parts = [part for part in (prefix, section.strip(), suffix) if part]
        return '\n\n'.join(parts) + ('\n' if body.endswith('\n') or suffix else '\n')
    body = (body or '').strip()
    if not body:
        return section
    return f'{section}\n{body}\n'
