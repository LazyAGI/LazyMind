import json
from typing import Any, Dict, List, Optional

import lazyllm
import requests
from lazyllm import LOG, fc_register
from typing_extensions import TypedDict

from chat.tools._common import handle_tool_errors, tool_error, tool_success
from chat.tools._utils import post_core_api
from vocab.db import (
    fetch_chat_histories_for_session,
    fetch_vocab_groups_for_user_id,
)
from vocab.evolution import ActionPlanningModule, ChatHistoryRecord, SynonymCandidate, VocabEvolutionRequest


MAX_VOCAB_SUGGESTIONS_PER_CALL = 5
_WORD_GROUP_APPLY_INTERNAL_PATH = '/inner/word_group:apply'


class VocabSuggestion(TypedDict, total=False):
    """One durable user-specific vocabulary suggestion.

    Fields:
        word (str, required): the first term in the synonym pair.
        synonym (str, required): the matching term the user wants treated as the same concept.
        description (str, optional): the semantic context where this mapping applies.
        reason (str, required): why this mapping is clearly supported by the conversation.
    """

    word: str
    synonym: str
    description: str
    reason: str


def _norm_text(value: Any) -> str:
    return ' '.join(str(value or '').strip().split())


def _norm_key(value: str) -> str:
    return _norm_text(value).casefold()


def _clip_text(value: Any, limit: int = 120) -> str:
    text = _norm_text(value)
    if limit <= 0 or len(text) <= limit:
        return text
    return text[: max(0, limit - 3)] + '...'


def _dedupe_keep_order(values: List[str]) -> List[str]:
    seen = set()
    result: List[str] = []
    for value in values:
        item = _norm_text(value)
        if not item or item in seen:
            continue
        seen.add(item)
        result.append(item)
    return result


def _resolve_user_id(agentic_config: Optional[Dict[str, Any]] = None) -> str:
    config = agentic_config if isinstance(agentic_config, dict) else lazyllm.globals['agentic_config']
    return _norm_text(config.get('user_id'))


def _serialize_string_list(values: List[str]) -> str:
    return json.dumps(_dedupe_keep_order(values), ensure_ascii=False)


def _serialize_backend_actions(actions: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
    serialized: List[Dict[str, Any]] = []
    for action in actions:
        serialized.append({
            'reason': _norm_text(action.get('reason')),
            'words': _dedupe_keep_order(action.get('words') or []),
            'description': _norm_text(action.get('description')),
            'group_ids': _serialize_string_list(action.get('group_ids') or []),
            'user_id': _norm_text(action.get('user_id')),
            'message_ids': _serialize_string_list(action.get('message_ids') or []),
            'action': _norm_text(action.get('action')),
        })
    return serialized


def _summarize_suggestion_for_log(suggestion: Dict[str, Any]) -> Dict[str, Any]:
    return {
        'word': _norm_text(suggestion.get('word')),
        'synonym': _norm_text(suggestion.get('synonym')),
        'description': _clip_text(suggestion.get('description'), 80),
        'reason': _clip_text(suggestion.get('reason'), 120),
    }


def _summarize_candidate_for_log(candidate: SynonymCandidate) -> Dict[str, Any]:
    return {
        'word': candidate.word,
        'synonym': candidate.synonym,
        'description': _clip_text(candidate.description, 80),
        'message_ids': list(candidate.message_ids),
        'reason': _clip_text(candidate.reason, 120),
    }


def _summarize_action_for_log(action: Dict[str, Any]) -> Dict[str, Any]:
    return {
        'action': _norm_text(action.get('action')),
        'words': _dedupe_keep_order(action.get('words') or []),
        'group_ids': action.get('group_ids') or [],
        'description': _clip_text(action.get('description'), 80),
        'message_ids': action.get('message_ids') or [],
        'reason': _clip_text(action.get('reason'), 120),
    }


def _message_ids_for_suggestion(histories: List[ChatHistoryRecord], suggestion: VocabSuggestion) -> List[str]:
    word_key = _norm_key(suggestion.get('word'))
    synonym_key = _norm_key(suggestion.get('synonym'))
    matched: List[str] = []
    for row in histories:
        searchable = row.searchable_text.casefold()
        if word_key and word_key in searchable:
            matched.append(row.message_id)
            continue
        if synonym_key and synonym_key in searchable:
            matched.append(row.message_id)
    if matched:
        return _dedupe_keep_order(matched)

    for row in reversed(histories):
        if row.message_id:
            return [row.message_id]
    return []


@fc_register('tool', execute_in_sandbox=False)
@handle_tool_errors
def vocab_manage(suggestions: List[Dict[str, Any]]) -> Dict[str, Any]:
    """Apply durable user-specific vocabulary updates for the current session user.

    Use this tool only when the conversation clearly establishes a stable term mapping for this user,
    such as the user explicitly saying that A means B, that A should be remembered as B, or that two
    terms should be treated as the same concept in their domain. Do not use it for vague paraphrases,
    general world-knowledge synonyms, temporary nicknames, or one-off wording choices.

    The tool automatically resolves the current session user and updates only that user's vocabulary.
    Pass a small batch of concrete synonym suggestions. Each item must contain exactly one `word`, one
    `synonym`, and a short `reason` grounded in the conversation.

    Args:
        suggestions (List[Dict[str, Any]]): A small batch of stable, user-specific term mappings for
            the current session user. Each item must contain `word`, `synonym`, and `reason`; the
            optional `description` can be used to record the domain context.
    """

    if not suggestions:
        return tool_error(
            'vocab_manage',
            "'suggestions' must be a non-empty list.",
            log_message="[VocabTool] rejected reason='suggestions' must be a non-empty list.",
        )
    if len(suggestions) > MAX_VOCAB_SUGGESTIONS_PER_CALL:
        return tool_error(
            'vocab_manage',
            f'At most {MAX_VOCAB_SUGGESTIONS_PER_CALL} suggestions are allowed per call; '
            f'got {len(suggestions)}.',
            log_message=f'[VocabTool] rejected reason=too_many_suggestions count={len(suggestions)}',
        )

    agentic_config = lazyllm.globals['agentic_config']
    session_id = str(agentic_config.get('session_id') or '').strip()
    if not session_id:
        return tool_error(
            'vocab_manage',
            "'session_id' is required in agentic_config.",
            log_message="[VocabTool] rejected reason='session_id' is required in agentic_config.",
        )

    user_id = _resolve_user_id(agentic_config)
    if not user_id:
        return tool_error(
            'vocab_manage',
            'user_id is required in agentic_config.',
            log_message="[VocabTool] rejected reason='user_id' is required in agentic_config.",
        )

    LOG.info(
        '[VocabTool] start '
        f'session_id={session_id!r} user_id={user_id!r} suggestion_count={len(suggestions)} '
        f'suggestions={json.dumps([_summarize_suggestion_for_log(item) for item in suggestions], ensure_ascii=False)}'
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
    seen_pairs = set()
    candidates: List[SynonymCandidate] = []

    for suggestion in suggestions:
        word = _norm_text(suggestion.get('word'))
        synonym = _norm_text(suggestion.get('synonym'))
        pair_key = tuple(sorted([_norm_key(word), _norm_key(synonym)]))
        if not word or not synonym:
            LOG.info(
                '[VocabTool] skipped raw suggestion '
                f'user_id={user_id!r} reason=missing_word_or_synonym '
                f'suggestion={json.dumps(_summarize_suggestion_for_log(suggestion), ensure_ascii=False)}'
            )
            continue
        if pair_key in seen_pairs:
            LOG.info(
                '[VocabTool] skipped raw suggestion '
                f'user_id={user_id!r} reason=duplicate_pair '
                f'suggestion={json.dumps(_summarize_suggestion_for_log(suggestion), ensure_ascii=False)}'
            )
            continue
        seen_pairs.add(pair_key)

        candidate = SynonymCandidate(
            user_id=user_id,
            word=word,
            synonym=synonym,
            description=_norm_text(suggestion.get('description')),
            reason=_norm_text(suggestion.get('reason')) or f'User explicitly associated `{word}` with `{synonym}`.',
            message_ids=_message_ids_for_suggestion(session_histories, suggestion),
        )
        candidates.append(candidate)
        LOG.info(
            '[VocabTool] prepared candidate '
            f'user_id={user_id!r} '
            f'candidate={json.dumps(_summarize_candidate_for_log(candidate), ensure_ascii=False)}'
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
    actions = _serialize_backend_actions(planned_actions)
    skipped = [
        {'reason': reason}
        for reason in (plan_result.get('skipped_reasons') or [])
        if isinstance(reason, str) and reason
    ]
    LOG.info(
        '[VocabTool] planner finished '
        f'user_id={user_id!r} candidate_count={len(candidates)} '
        f'action_count={len(planned_actions)} skipped_count={len(skipped)} '
        f'actions={json.dumps([_summarize_action_for_log(item) for item in planned_actions], ensure_ascii=False)} '
        f'skipped={json.dumps(skipped, ensure_ascii=False)}'
    )

    result: Dict[str, Any] = {
        'session_id': session_id,
        'user_id': user_id,
        'submitted_actions': len(actions),
        'skipped': skipped,
    }
    if not actions:
        LOG.info(f'[VocabTool] finish status=no-op result={json.dumps(result, ensure_ascii=False)}')
        return tool_success('vocab_manage', result)

    payload = {'action_list': actions}
    LOG.info(
        '[VocabTool] submitting actions '
        f'user_id={user_id!r} payload={json.dumps(payload, ensure_ascii=False)}'
    )
    try:
        result.update(post_core_api(_WORD_GROUP_APPLY_INTERNAL_PATH, payload))
    except (requests.RequestException, RuntimeError) as exc:
        return tool_error(
            'vocab_manage',
            f'Failed to submit vocab suggestions: {exc}',
            log_message=f'[VocabTool] failed to submit vocab suggestions user_id={user_id!r}: {exc}',
            log_level='error',
        )

    LOG.info(f'[VocabTool] finish status=applied result={json.dumps(result, ensure_ascii=False)}')
    return tool_success('vocab_manage', result)
