from typing import Any, Dict, List, Literal, Optional

import lazyllm
import requests

from lazymind.chat.engine.tools.infra import (
    Suggestion,
    build_skill_identity,
    dump_suggestion,
    handle_tool_errors,
    is_writable_skill_source,
    list_all_skill_entries,
    normalize_skill_category,
    post_core_api,
    tool_error,
    tool_success,
    validate_skill_content,
    validate_skill_name,
)
from lazymind.config import config as _cfg

MAX_SUGGESTIONS_PER_CALL = 5


@handle_tool_errors
def skill_editor(
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
        suggestions: Ordered list of suggestions (max 5 per call). Each
            item is a dict with the following fields. ONLY for
            action='modify'. Do NOT pass for action='create' or 'remove'.

            - ``title`` (str, required): short label summarising the
              proposed change.
            - ``content`` (str, required): natural-language description of
              the modification to the existing skill content. This should
              usually describe one focused update such as adjusting trigger
              conditions, refining scope, adding/removing a rule, or
              correcting an inaccurate instruction.
            - ``reason`` (str, optional): why the change is worth making.
        reason: Why the skill should be removed. ONLY for action='remove'.
    """
    lazyllm.LOG.info(
        '[skill_editor] called '
        f'name={name!r} action={action!r} '
        f'category={category!r} content_len={len(content) if content else 0} '
        f'suggestions_count={len(suggestions) if suggestions else 0}'
    )

    name_error = validate_skill_name(name)
    if name_error:
        return tool_error('skill_editor', name_error, log_message=f'[skill_editor] fail reason={name_error!r}')

    agentic_config = lazyllm.globals['agentic_config']
    session_id = str(agentic_config.get('session_id') or '').strip()
    if not session_id:
        return tool_error(
            'skill_editor',
            "'session_id' is required in agentic_config.",
            log_message="[skill_editor] fail reason='session_id' is required in agentic_config.",
        )

    normalized_category = normalize_skill_category(category)
    if normalized_category is None:
        return tool_error(
            'skill_editor',
            f'Category {category!r} is invalid; it must be a single '
            "ASCII-safe path segment (only letters, digits, '-', '_' "
            "and '.'; no spaces, no Chinese, no '/')."
        )

    existing_skills = list_all_skill_entries(_cfg['skill_fs_url'])
    skill_id = build_skill_identity(normalized_category or '', name)
    existing_skill = existing_skills.get(skill_id)
    lazyllm.LOG.info(
        '[skill_editor] lookup '
        f'skill_id={skill_id!r} '
        f'found={existing_skill is not None} '
        f'existing_keys={list(existing_skills.keys())!r}'
    )

    if action == 'create':
        content_error = validate_skill_content(content or '')
        if content_error:
            return tool_error(
                'skill_editor',
                content_error,
                log_message=f'[skill_editor] fail reason={content_error!r}',
            )
        if suggestions:
            return tool_error('skill_editor', "action='create' must not include 'suggestions'.")
        if existing_skill:
            source = existing_skill.get('source', 'file')
            if not is_writable_skill_source(source):
                return tool_error(
                    'skill_editor',
                    f'Skill {name!r} already exists in category {normalized_category!r} '
                    f'with read-only source {source!r}; skill_editor can only write remote skills.'
                )
            return tool_error(
                'skill_editor',
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
                'skill_editor',
                f'Failed to create skill: {exc}',
                log_message=f'[skill_editor] create failed: {exc}',
                log_level='error',
            )
        return tool_success('skill_editor', result)

    if action == 'modify':
        if content is not None:
            return tool_error('skill_editor', "action='modify' must not include 'content'; use 'suggestions'.")
        if not suggestions:
            return tool_error('skill_editor', "action='modify' requires a non-empty 'suggestions' list.")
        if len(suggestions) > MAX_SUGGESTIONS_PER_CALL:
            return tool_error(
                'skill_editor',
                f'At most {MAX_SUGGESTIONS_PER_CALL} suggestions are allowed per call; '
                f'got {len(suggestions)}.'
            )
        if not existing_skill:
            return tool_error(
                'skill_editor',
                f'Skill {name!r} does not exist in category {normalized_category!r}; '
                "use action='create' to add a new skill."
            )
        source = existing_skill.get('source', 'file')
        lazyllm.LOG.info(
            '[skill_editor] modify_check '
            f'source={source!r} '
            f'writable={is_writable_skill_source(source)}'
        )
        if not is_writable_skill_source(source):
            return tool_error(
                'skill_editor',
                f'Skill {name!r} in category {normalized_category!r} has read-only source '
                f'{source!r}; skill_editor can only modify remote skills.'
            )

        result = {
            'name': name,
            'action': action,
            'category': normalized_category,
            'suggestions': [dump_suggestion(s) for s in suggestions],
        }
        payload = {
            'session_id': session_id,
            'skill_name': name,
            'category': normalized_category,
            'suggestions': [dump_suggestion(s) for s in suggestions],
        }
        try:
            result.update(post_core_api('/skill/suggestion', payload))
        except (requests.RequestException, RuntimeError) as exc:
            return tool_error(
                'skill_editor',
                f'Failed to submit skill suggestions: {exc}',
                log_message=f'[skill_editor] modify failed: {exc}',
                log_level='error',
            )
        return tool_success('skill_editor', result)

    if action == 'remove':
        if content is not None or suggestions:
            return tool_error('skill_editor', "action='remove' must not include 'content' or 'suggestions'.")
        if not existing_skill:
            return tool_error(
                'skill_editor',
                f'Skill {name!r} does not exist in category {normalized_category!r}; '
                'nothing to remove.'
            )
        source = existing_skill.get('source', 'file')
        if not is_writable_skill_source(source):
            return tool_error(
                'skill_editor',
                f'Skill {name!r} in category {normalized_category!r} has read-only source '
                f'{source!r}; skill_editor can only remove remote skills.'
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
                'skill_editor',
                f'Failed to remove skill: {exc}',
                log_message=f'[skill_editor] remove failed: {exc}',
                log_level='error',
            )
        return tool_success('skill_editor', result)

    return tool_error(
        'skill_editor',
        f"Unknown action {action!r}; expected one of 'create', 'modify', 'remove'."
    )
