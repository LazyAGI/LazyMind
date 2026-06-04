from typing import Any, Dict, List, Literal

import lazyllm
import requests

from lazymind.chat.engine.tools.infra import (
    Suggestion,
    dump_suggestion,
    handle_tool_errors,
    post_core_api,
    tool_error,
    tool_success,
)

MAX_SUGGESTIONS_PER_CALL = 5


@handle_tool_errors
def memory_editor(
    target: Literal['memory', 'user'],
    suggestions: List[Suggestion],
) -> Dict[str, Any]:
    """Record natural-language edit suggestions for the user's
    memory (``target='memory'``) or user profile / preference
    (``target='user'``).

    Call this tool when, while handling the current query, you learn
    something that should persist across future sessions, but it must still
    go through the review and draft-confirmation workflow before becoming the
    final stored text.

    Each call accepts a batch of at most 5 suggestions; every suggestion
    describes ONE proposed change in natural language and will be reviewed
    before being merged. For ``target='memory'``, suggestions should describe
    atomic memory events or updates, not the final merged memory text.

    Args:
        target: Which buffer the suggestions belong to. ``'memory'`` is the
            agent's own working memory about the user's ongoing context and
            prior discussions; ``'user'`` is the user
            profile / preference text.
        suggestions: Ordered list of suggestions (max 3 per call). Each
            item is a dict with the following fields:

            - ``title`` (str, required): short label summarising the change.
            - ``content`` (str, required): natural-language description of
              the modification. For ``target='memory'``, this should usually
              be one timestamped memory event, one same-day update, or one
              correction to an existing memory thread.
            - ``reason`` (str, optional): rationale for the change.
    """
    if target not in {'memory', 'user'}:
        return tool_error(
            'memory_editor',
            f"Unknown target {target!r}; expected one of 'memory', 'user'."
        )
    if not suggestions:
        return tool_error('memory_editor', "'suggestions' must be a non-empty list.")
    if len(suggestions) > MAX_SUGGESTIONS_PER_CALL:
        return tool_error(
            'memory_editor',
            f'At most {MAX_SUGGESTIONS_PER_CALL} suggestions are allowed per '
            f'call; got {len(suggestions)}.'
        )

    session_id = str(lazyllm.globals['agentic_config'].get('session_id') or '').strip()
    if not session_id:
        return tool_error('memory_editor', "'session_id' is required in agentic_config.")

    endpoint = (
        '/memory/suggestion'
        if target == 'memory'
        else '/user_preference/suggestion'
    )
    payload = {
        'session_id': session_id,
        'suggestions': [dump_suggestion(s) for s in suggestions],
    }

    result: Dict[str, Any] = {
        'target': target,
        'appended_suggestions': len(suggestions),
    }
    try:
        result.update(post_core_api(endpoint, payload))
    except (requests.RequestException, RuntimeError) as exc:
        return tool_error(
            'memory_editor',
            f'Failed to submit memory suggestions: {exc}',
            log_message=f'Failed to submit memory suggestions: {exc}',
            log_level='error',
        )

    return tool_success('memory_editor', result)
