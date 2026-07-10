from __future__ import annotations

from lazymind.rewrite.base import UnprocessableContentError


def apply_memory_tool_operation(
    current_content: str,
    *,
    op: str,
    old_text: str,
    new_text: str,
    replace_all_matches: bool,
    content: str,
) -> str:
    op_name = str(op or '').strip()
    if op_name == 'patch':
        return _apply_patch_operation(
            current_content,
            old_text=old_text,
            new_text=new_text,
            replace_all_matches=replace_all_matches,
        )
    if op_name == 'append':
        return _apply_append_operation(current_content, content=content)
    raise UnprocessableContentError(
        f"Unsupported memory_editor operation {op_name!r}; expected 'patch' or 'append'."
    )


def _apply_patch_operation(
    current_content: str,
    *,
    old_text: str,
    new_text: str,
    replace_all_matches: bool,
) -> str:
    if not isinstance(old_text, str):
        raise UnprocessableContentError("patch requires a string field 'old_text'.")
    if not old_text:
        raise UnprocessableContentError("patch requires a non-empty 'old_text'. Use append to add new content.")
    if not isinstance(new_text, str):
        raise UnprocessableContentError("patch requires a string field 'new_text'.")

    match_count = current_content.count(old_text)
    if match_count == 0:
        raise UnprocessableContentError(
            "patch could not find the requested 'old_text' substring in current content."
        )
    if match_count > 1 and not replace_all_matches:
        raise UnprocessableContentError(
            'patch old_text matched multiple locations; provide more context or set replace_all_matches=true.'
        )
    return current_content.replace(old_text, new_text, -1 if replace_all_matches else 1)


def _apply_append_operation(current_content: str, *, content: str) -> str:
    if not isinstance(content, str):
        raise UnprocessableContentError("append requires a string field 'content'.")
    appended = content.strip()
    if not appended:
        raise UnprocessableContentError('append requires non-empty content.')
    if not current_content:
        return appended
    separator = '' if current_content.endswith('\n') else '\n'
    return f'{current_content}{separator}{appended}'
