import os
import re
import sys
from typing import Any, Dict, List, Literal, Optional

from pydantic import BaseModel

import requests
import lazyllm
from lazyllm import fc_register
from lazyllm.tools.agent.skill_manager import SkillManager as LazySkillManager

if __package__ in (None, ''):
    _algorithm_root = os.path.abspath(os.path.join(os.path.dirname(__file__), '..', '..'))
    if _algorithm_root not in sys.path:
        sys.path.insert(0, _algorithm_root)

from lazyllm.tools.fs.client import FS
from common.remote_fs import RemoteFileSystem  # noqa: F401
from chat.tools._common import handle_tool_errors, tool_error, tool_success
from chat.tools._utils import post_core_api
from chat.utils.load_config import extract_skill_fs_source
from config import config as _cfg

_PATH_SEGMENT_RE = re.compile(r'^[^\s/\\]+$')
_UUID_SEGMENT_RE = re.compile(
    r'^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
)
_FRONTMATTER_RE = re.compile(r'^---\s*\n(.*?)\n---\s*\n(.*)$', re.DOTALL)
_MAX_DESCRIPTION_LENGTH = 1024
MAX_SUGGESTIONS_PER_CALL = 5


class Suggestion(BaseModel):
    title: str
    content: str


def _validate_skill_name(name: str) -> Optional[str]:
    if not name or not name.strip():
        return "'name' must be a non-empty skill name."
    if name in {'.', '..'} or not _PATH_SEGMENT_RE.match(name):
        return (
            f'Skill name {name!r} is invalid; whitespace and slashes '
            'are not allowed.'
        )
    return None


def _normalize_category(category: Optional[str]) -> Optional[str]:
    if category is None:
        return ''
    cleaned = str(category).strip().strip('/')
    if not cleaned:
        return ''
    if cleaned in {'.', '..'} or not _PATH_SEGMENT_RE.match(cleaned):
        return None
    return cleaned


def _parse_frontmatter(content: str) -> tuple[dict[str, Any], str]:
    match = _FRONTMATTER_RE.match(content or '')
    if not match:
        return {}, content or ''

    yaml_text, body = match.group(1), match.group(2)
    try:
        import yaml  # type: ignore

        parsed = yaml.safe_load(yaml_text)
        if isinstance(parsed, dict):
            return parsed, body
    except Exception:
        pass

    return {}, body


def _validate_skill_content(content: str) -> Optional[str]:
    if not content or not content.strip():
        return "action='create' requires a non-empty 'content' (full SKILL.md body)."

    frontmatter, body = _parse_frontmatter(content)
    if not frontmatter:
        return 'SKILL.md must contain YAML frontmatter.'
    if 'name' not in frontmatter:
        return "Frontmatter must include 'name'."
    if 'description' not in frontmatter:
        return "Frontmatter must include 'description'."
    if len(str(frontmatter.get('description', ''))) > _MAX_DESCRIPTION_LENGTH:
        return f'Description exceeds {_MAX_DESCRIPTION_LENGTH} characters.'
    if not body.strip():
        return 'SKILL.md must have markdown content after frontmatter.'
    return None


def _extract_category_from_path(skill_dir: str, skill_name: str) -> str:
    path = str(skill_dir or '').rstrip('/')
    marker = '/skills/'

    if marker in path:
        tail = path.split(marker, 1)[1]
    else:
        tail = path.strip('/')

    parts = [p for p in tail.split('/') if p and p not in {'.'}]
    if not parts:
        return ''

    if parts[-1] == skill_name:
        parts = parts[:-1]

    if parts and _UUID_SEGMENT_RE.match(parts[0]):
        parts = parts[1:]

    return parts[-1] if parts else ''


def _skill_identity(category: str, skill_name: str) -> str:
    return f'{category}/{skill_name}' if category else skill_name


def list_all_skill_entries(
    skill_fs_url: str,
) -> Dict[str, Dict[str, str]]:
    manager = LazySkillManager(dir=skill_fs_url, fs=FS)
    results: Dict[str, Dict[str, str]] = {}

    for skill_dir, skill_md in manager._iter_skill_files():
        if manager._fs_getsize(skill_md) > manager._max_skill_md_bytes:
            continue
        try:
            content = manager._fs_read(skill_md)
        except Exception:
            continue

        meta = manager._extract_yaml_meta(content)
        if not manager._is_meta_valid(meta):
            continue

        name = str(meta.get('name') or '').strip()
        if not name:
            continue

        category = _extract_category_from_path(skill_dir, name)
        skill_id = _skill_identity(category, name)
        if skill_id in results:
            continue

        results[skill_id] = {
            'name': name,
            'category': category,
            'path': skill_dir,
            'source': extract_skill_fs_source(skill_dir),
        }
    return results


def list_all_skills_with_category(
    skill_fs_url: str,
) -> Dict[str, str]:
    results: Dict[str, str] = {}
    for info in list_all_skill_entries(skill_fs_url).values():
        results.setdefault(info['name'], info['category'])
    return results


def _is_writable_skill_source(source: str) -> bool:
    return source == 'remote'


@fc_register('tool', execute_in_sandbox=False)
@handle_tool_errors
def skill_manage(
    name: str,
    action: Literal['create', 'modify', 'remove'],
    category: Optional[str],
    content: Optional[str] = None,
    suggestions: Optional[List[Suggestion]] = None,
    reason: Optional[str] = None,
) -> Dict[str, Any]:
    """Manage skills by creating, modifying, or removing a skill entry.

    Args:
        name: Skill name.
        action: Action to perform.
        category: Skill category directory.
        content: Full SKILL.md content. ONLY for action='create'.
            Do NOT pass for action='modify' or 'remove'.
        suggestions: List of {title, content} objects. ONLY for action='modify'.
            Do NOT pass for action='create' or 'remove'.
        reason: Why the skill should be removed. ONLY for action='remove'.
    """
    lazyllm.LOG.info(
        '[skill_manage] called '
        f'name={name!r} action={action!r} '
        f'category={category!r} content_len={len(content) if content else 0} '
        f'suggestions_count={len(suggestions) if suggestions else 0}'
    )

    name_error = _validate_skill_name(name)
    if name_error:
        return tool_error('skill_manage', name_error, log_message=f'[skill_manage] fail reason={name_error!r}')

    agentic_config = lazyllm.globals['agentic_config']
    session_id = str(agentic_config.get('session_id') or '').strip()
    if not session_id:
        return tool_error(
            'skill_manage',
            "'session_id' is required in agentic_config.",
            log_message="[skill_manage] fail reason='session_id' is required in agentic_config.",
        )

    normalized_category = _normalize_category(category)
    if normalized_category is None:
        return tool_error(
            'skill_manage',
            f'Category {category!r} is invalid; it must be a single '
            "ASCII-safe path segment (only letters, digits, '-', '_' "
            "and '.'; no spaces, no Chinese, no '/')."
        )

    existing_skills = list_all_skill_entries(_cfg['skill_fs_url'])
    skill_id = _skill_identity(normalized_category or '', name)
    existing_skill = existing_skills.get(skill_id)
    lazyllm.LOG.info(
        '[skill_manage] lookup '
        f'skill_id={skill_id!r} '
        f'found={existing_skill is not None} '
        f'existing_keys={list(existing_skills.keys())!r}'
    )

    if action == 'create':
        content_error = _validate_skill_content(content or '')
        if content_error:
            return tool_error(
                'skill_manage',
                content_error,
                log_message=f'[skill_manage] fail reason={content_error!r}',
            )
        if suggestions:
            return tool_error('skill_manage', "action='create' must not include 'suggestions'.")
        if existing_skill:
            source = existing_skill.get('source', 'file')
            if not _is_writable_skill_source(source):
                return tool_error(
                    'skill_manage',
                    f'Skill {name!r} already exists in category {normalized_category!r} '
                    f'with read-only source {source!r}; skill_manage can only write remote skills.'
                )
            return tool_error(
                'skill_manage',
                f'Skill {name!r} already exists in category {normalized_category!r}; '
                "use action='modify' to edit it or action='remove' to delete it first."
            )

        result: Dict[str, Any] = {
            'name': name,
            'action': action,
            'category': normalized_category,
            'content': content,
        }
        payload = {
            'session_id': session_id,
            'category': normalized_category,
            'skill_name': name,
            'content': content,
        }
        try:
            result.update(post_core_api('/skill/create', payload))
        except (requests.RequestException, RuntimeError) as exc:
            return tool_error(
                'skill_manage',
                f'Failed to create skill: {exc}',
                log_message=f'[skill_manage] create failed: {exc}',
                log_level='error',
            )
        return tool_success('skill_manage', result)

    if action == 'modify':
        if content is not None:
            return tool_error('skill_manage', "action='modify' must not include 'content'; use 'suggestions'.")
        if not suggestions:
            return tool_error('skill_manage', "action='modify' requires a non-empty 'suggestions' list.")
        if len(suggestions) > MAX_SUGGESTIONS_PER_CALL:
            return tool_error(
                'skill_manage',
                f'At most {MAX_SUGGESTIONS_PER_CALL} suggestions are allowed per call; '
                f'got {len(suggestions)}.'
            )
        if not existing_skill:
            return tool_error(
                'skill_manage',
                f'Skill {name!r} does not exist in category {normalized_category!r}; '
                "use action='create' to add a new skill."
            )
        source = existing_skill.get('source', 'file')
        lazyllm.LOG.info(
            '[skill_manage] modify_check '
            f'source={source!r} '
            f'writable={_is_writable_skill_source(source)}'
        )
        if not _is_writable_skill_source(source):
            return tool_error(
                'skill_manage',
                f'Skill {name!r} in category {normalized_category!r} has read-only source '
                f'{source!r}; skill_manage can only modify remote skills.'
            )

        result = {
            'name': name,
            'action': action,
            'category': normalized_category,
            'suggestions': [s.model_dump() for s in suggestions],
        }
        payload = {
            'session_id': session_id,
            'skill_name': name,
            'category': normalized_category,
            'suggestions': [s.model_dump() for s in suggestions],
        }
        try:
            result.update(post_core_api('/skill/suggestion', payload))
        except (requests.RequestException, RuntimeError) as exc:
            return tool_error(
                'skill_manage',
                f'Failed to submit skill suggestions: {exc}',
                log_message=f'[skill_manage] modify failed: {exc}',
                log_level='error',
            )
        return tool_success('skill_manage', result)

    if action == 'remove':
        if content is not None or suggestions:
            return tool_error('skill_manage', "action='remove' must not include 'content' or 'suggestions'.")
        if not existing_skill:
            return tool_error(
                'skill_manage',
                f'Skill {name!r} does not exist in category {normalized_category!r}; '
                'nothing to remove.'
            )
        source = existing_skill.get('source', 'file')
        if not _is_writable_skill_source(source):
            return tool_error(
                'skill_manage',
                f'Skill {name!r} in category {normalized_category!r} has read-only source '
                f'{source!r}; skill_manage can only remove remote skills.'
            )

        result = {
            'name': name,
            'action': action,
            'category': normalized_category,
            'reason': reason,
        }
        payload = {
            'session_id': session_id,
            'skill_name': name,
            'category': normalized_category,
            'reason': reason or '',
        }
        try:
            result.update(post_core_api('/skill/remove', payload))
        except (requests.RequestException, RuntimeError) as exc:
            return tool_error(
                'skill_manage',
                f'Failed to remove skill: {exc}',
                log_message=f'[skill_manage] remove failed: {exc}',
                log_level='error',
            )
        return tool_success('skill_manage', result)

    return tool_error(
        'skill_manage',
        f"Unknown action {action!r}; expected one of 'create', 'modify', 'remove'."
    )
