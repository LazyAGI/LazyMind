from typing import Any, Dict, List, Literal, Optional

import lazyllm

from lazymind.chat.engine.tools.infra import (
    build_skill_identity,
    create_remote_skill,
    handle_tool_errors,
    is_writable_skill_source,
    list_all_skill_entries,
    normalize_skill_category,
    parse_skill_frontmatter,
    remove_remote_skill,
    tool_error,
    tool_success,
    validate_skill_content,
    validate_skill_name,
)
from lazymind.chat.engine.tools.infra.skill_operations import (
    SkillEditOperation,
    apply_skill_edit_operations,
)
from lazymind.chat.engine.tools.infra.skill_review_store import (
    SKILL_REVIEW_TYPE_PATCH,
    find_pending_skill_review,
    insert_skill_review_result,
)
from lazymind.config import config as _cfg


_PENDING_CHANGE_MESSAGE = '存在未处理的变更，请先处理'
_SUCCESS_MESSAGE = '已写入变更，等待确认'


@handle_tool_errors
def skill_editor(
    name: str,
    action: Literal['create', 'modify', 'remove'],
    category: Optional[str],
    content: Optional[str] = None,
    operations: Optional[List[SkillEditOperation]] = None,
    reason: Optional[str] = None,
) -> Dict[str, Any]:
    """Manage skills by creating, modifying, or removing a skill entry.

    Use this tool to curate reusable skills. It has three actions:

    - action='create': after completing a complex task (5+ tool calls),
      fixing a tricky error, or discovering a non-trivial workflow, save the
      approach as a new skill by passing the full SKILL.md body in
      content.
    - action='modify': when finding a skill outdated, incomplete, or
      wrong, submit targeted edit proposals via suggestions
      (natural-language, max 5 per call).
    - action='remove': when a skill is superseded or no longer correct,
      request its deletion.

    Only skills with source=remote are writable. Skills with
    source=file or any other source are read-only; do not use this tool
    to modify or remove them.

    Args:
        name: Skill name.
        action: Skill workflow to run. Use 'create' to submit a new SKILL.md
            content row for review, 'modify' to edit an existing remote skill
            using the 'operations' argument and submit the edited content for
            review, or 'remove' to mark an existing remote skill for deletion.
            For 'modify' and 'remove', a pending review row for the same
            category/name blocks the request.
        category: Skill category directory used to locate category/name/SKILL.md.
        content: Full SKILL.md content, including YAML frontmatter with
            name/category/description. ONLY for action='create'. Do NOT pass
            for action='modify' or 'remove'.
        operations: Ordered JSON edit operations. ONLY for action='modify'.
            Do NOT pass for action='create' or 'remove'. Supported operations:

            - ``{"op": "replace_text", "old": "...", "new": "..."}``:
              replace the first exact ``old`` substring with ``new``.
              Prefer multiple small replace_text operations for local edits.
            - ``{"op": "replace_all", "content": "..."}``: replace the
              full original SKILL.md content with ``content``. Use this only
              when exact local replacement is not safe enough.
        reason: Why the skill should be removed. ONLY for action='remove'.
    """
    lazyllm.LOG.info(
        '[skill_editor] called '
        f'name={name!r} action={action!r} '
        f'category={category!r} content_len={len(content) if content else 0} '
        f'operations_count={len(operations) if operations else 0}'
    )

    name_error = validate_skill_name(name)
    if name_error:
        return tool_error('skill_editor', name_error, log_message=f'[skill_editor] fail reason={name_error!r}')

    agentic_config = lazyllm.globals['agentic_config']
    user_id = str(agentic_config.get('user_id') or '').strip()
    session_id = str(agentic_config.get('session_id') or '').strip()

    normalized_category = normalize_skill_category(category)
    if not normalized_category:
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
        if operations:
            return tool_error('skill_editor', "action='create' must not include 'operations'.")
        content_category, content_name = _skill_identity_from_content(content or '')
        if content_category != normalized_category or content_name != name:
            return tool_error(
                'skill_editor',
                'SKILL.md frontmatter name/category must match the tool name/category for create.'
            )
        pending = find_pending_skill_review(content_category, content_name, user_id)
        if pending or existing_skill:
            return tool_error('skill_editor', _PENDING_CHANGE_MESSAGE)

        create_remote_skill(content_category, content_name, content or '')
        return tool_success('skill_editor', _SUCCESS_MESSAGE)

    if action == 'modify':
        if content is not None:
            return tool_error('skill_editor', "action='modify' must not include 'content'; use 'operations'.")
        if not operations:
            return tool_error('skill_editor', "action='modify' requires a non-empty 'operations' list.")
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

        try:
            from lazymind.rewrite.base import UnprocessableContentError

            edited_content, operation_payload = apply_skill_edit_operations(
                existing_skill.get('content') or '',
                operations,
            )
        except UnprocessableContentError as exc:
            return tool_error('skill_editor', str(exc))

        content_error = validate_skill_content(edited_content)
        if content_error:
            return tool_error('skill_editor', content_error)
        edited_category, edited_name = _skill_identity_from_content(edited_content)
        pending = find_pending_skill_review(edited_category, edited_name, user_id)
        if pending:
            return tool_error('skill_editor', _PENDING_CHANGE_MESSAGE)

        insert_skill_review_result(
            category=normalized_category,
            skill_name=name,
            review_type=SKILL_REVIEW_TYPE_PATCH,
            skill_content=edited_content,
            user_id=user_id,
            requestid=session_id,
            summary=reason or f'skill_editor operations: {len(operation_payload)}',
        )
        return tool_success('skill_editor', _SUCCESS_MESSAGE)

    if action == 'remove':
        if content is not None or operations:
            return tool_error('skill_editor', "action='remove' must not include 'content' or 'operations'.")
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

        pending = find_pending_skill_review(normalized_category, name, user_id)
        if pending:
            return tool_error('skill_editor', _PENDING_CHANGE_MESSAGE)

        remove_remote_skill(normalized_category, name)
        return tool_success('skill_editor', _SUCCESS_MESSAGE)

    return tool_error(
        'skill_editor',
        f"Unknown action {action!r}; expected one of 'create', 'modify', 'remove'."
    )


def _skill_identity_from_content(content: str) -> tuple[str, str]:
    frontmatter, _ = parse_skill_frontmatter(content)
    category = str(frontmatter.get('category') or '').strip()
    name = str(frontmatter.get('name') or '').strip()
    return category, name
