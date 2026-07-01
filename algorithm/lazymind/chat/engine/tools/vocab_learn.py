import json
from typing import Any, Dict, List

import lazyllm
import requests
from lazyllm import LOG

from lazymind.chat.engine.tools.infra import (
    VocabSuggestion,
    ActionPlanningModule,
    ChatHistoryRecord,
    VocabEvolutionRequest,
    fetch_chat_histories_for_session,
    fetch_vocab_groups_for_user_id,
    post_core_api,
    prepare_vocab_candidates,
    resolve_vocab_user_id,
    serialize_vocab_backend_actions,
    summarize_vocab_action_for_log,
    summarize_vocab_candidate_for_log,
    summarize_vocab_suggestion_for_log,
    tool_error,
    tool_success,
)


MAX_VOCAB_SUGGESTIONS_PER_CALL = 5
_WORD_GROUP_APPLY_INTERNAL_PATH = '/inner/word_group:apply'


def vocab_learn(suggestions: List[VocabSuggestion]) -> Dict[str, Any]:
    """Apply durable user-specific vocabulary updates for the current session user.

    Use this tool only when the conversation clearly establishes a stable term mapping for this user,
    such as the user explicitly saying that A means B, that A should be remembered as B, or that two
    terms should be treated as the same concept in their domain. Do not use it for vague paraphrases,
    general world-knowledge synonyms, temporary nicknames, or one-off wording choices.

    Prefer this tool over memory when the user asks to remember a mapping in a vocabulary,
    glossary, domain terminology, or synonym list, or says that one term means, equals,
    or is another term in a specific domain.

    The tool automatically resolves the current session user and updates only that user's vocabulary.
    Pass a small batch of concrete synonym suggestions. Each item must contain exactly one `word`, one
    `synonym`, a short `description` for the domain context when useful, and a short `reason`
    grounded in the conversation.

    Args:
        suggestions (List[Dict[str, Any]]): A small batch of stable, user-specific term mappings for
            the current session user. Each item must contain `word`, `synonym`, and `reason`; the
            optional `description` can be used to record the domain context.
    """

    if not suggestions:
        return tool_error(
            'vocab_learn',
            "'suggestions' must be a non-empty list.",
            log_message="[VocabTool] rejected reason='suggestions' must be a non-empty list.",
        )
    if len(suggestions) > MAX_VOCAB_SUGGESTIONS_PER_CALL:
        return tool_error(
            'vocab_learn',
            f'At most {MAX_VOCAB_SUGGESTIONS_PER_CALL} suggestions are allowed per call; '
            f'got {len(suggestions)}.',
            log_message=f'[VocabTool] rejected reason=too_many_suggestions count={len(suggestions)}',
        )

    agentic_config = lazyllm.globals['agentic_config']
    session_id = str(agentic_config.get('session_id') or '').strip()
    if not session_id:
        return tool_error(
            'vocab_learn',
            "'session_id' is required in agentic_config.",
            log_message="[VocabTool] rejected reason='session_id' is required in agentic_config.",
        )

    user_id = resolve_vocab_user_id(agentic_config)
    if not user_id:
        return tool_error(
            'vocab_learn',
            'user_id is required in agentic_config.',
            log_message="[VocabTool] rejected reason='user_id' is required in agentic_config.",
        )

    suggestion_log = json.dumps(
        [summarize_vocab_suggestion_for_log(item) for item in suggestions],
        ensure_ascii=False,
    )
    LOG.info(
        '[VocabTool] start '
        f'session_id={session_id!r} user_id={user_id!r} suggestion_count={len(suggestions)} '
        f'suggestions={suggestion_log}'
    )

    groups = fetch_vocab_groups_for_user_id(user_id)
    session_histories = [
        ChatHistoryRecord.from_dict(item)
        for item in fetch_chat_histories_for_session(session_id)
    ]
    LOG.info(
        '[VocabTool] loaded context '
        f'session_id={session_id!r} user_id={user_id!r} '
        f'existing_group_count={len(groups)} session_history_count={len(session_histories)}'
    )
    candidates, skipped_logs = prepare_vocab_candidates(
        suggestions=suggestions,
        histories=session_histories,
        user_id=user_id,
    )
    for skipped_log in skipped_logs:
        LOG.info(
            '[VocabTool] skipped raw suggestion '
            f'user_id={user_id!r} '
            f'reason={skipped_log["reason"]} '
            f'suggestion={json.dumps(skipped_log["suggestion"], ensure_ascii=False)}'
        )
    for candidate in candidates:
        LOG.info(
            '[VocabTool] prepared candidate '
            f'user_id={user_id!r} '
            f'candidate={json.dumps(summarize_vocab_candidate_for_log(candidate), ensure_ascii=False)}'
        )

    planner = ActionPlanningModule(
        fetch_vocab_groups_fn=lambda _user_id, **kwargs: groups,
    )
    plan_result = planner.forward({
        'request': VocabEvolutionRequest(user_id=user_id),
        'user_id': user_id,
        'histories': session_histories,
        'candidates': candidates,
    })
    planned_actions = plan_result.get('actions', [])
    actions = serialize_vocab_backend_actions(planned_actions)
    skipped = [
        {'reason': reason}
        for reason in (plan_result.get('skipped_reasons') or [])
        if isinstance(reason, str) and reason
    ]
    action_log = json.dumps(
        [summarize_vocab_action_for_log(item) for item in planned_actions],
        ensure_ascii=False,
    )
    skipped_log = json.dumps(skipped, ensure_ascii=False)
    LOG.info(
        '[VocabTool] planner finished '
        f'user_id={user_id!r} candidate_count={len(candidates)} '
        f'action_count={len(planned_actions)} skipped_count={len(skipped)} '
        f'actions={action_log} '
        f'skipped={skipped_log}'
    )

    result: Dict[str, Any] = {
        'session_id': session_id,
        'user_id': user_id,
        'submitted_actions': len(actions),
        'skipped': skipped,
    }
    if not actions:
        LOG.info(f'[VocabTool] finish status=no-op result={json.dumps(result, ensure_ascii=False)}')
        return tool_success('vocab_learn', result)

    payload = {'action_list': actions}
    LOG.info(
        '[VocabTool] submitting actions '
        f'user_id={user_id!r} payload={json.dumps(payload, ensure_ascii=False)}'
    )
    try:
        result.update(post_core_api(_WORD_GROUP_APPLY_INTERNAL_PATH, payload))
    except (requests.RequestException, RuntimeError) as exc:
        return tool_error(
            'vocab_learn',
            f'Failed to submit vocab suggestions: {exc}',
            log_message=f'[VocabTool] failed to submit vocab suggestions user_id={user_id!r}: {exc}',
            log_level='error',
        )

    LOG.info(f'[VocabTool] finish status=applied result={json.dumps(result, ensure_ascii=False)}')
    return tool_success('vocab_learn', result)
