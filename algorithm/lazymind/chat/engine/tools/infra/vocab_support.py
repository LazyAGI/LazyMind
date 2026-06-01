from __future__ import annotations

import json
from typing import Any, Dict, List, Optional

import lazyllm
from pydantic import BaseModel

from lazymind.review.vocab import ChatHistoryRecord, SynonymCandidate


class VocabSuggestion(BaseModel):
    """One durable user-specific vocabulary suggestion."""

    word: str
    synonym: str
    description: str = ''
    reason: str = ''


def norm_vocab_text(value: Any) -> str:
    return ' '.join(str(value or '').strip().split())


def norm_vocab_key(value: str) -> str:
    return norm_vocab_text(value).casefold()


def clip_vocab_text(value: Any, limit: int = 120) -> str:
    text = norm_vocab_text(value)
    if limit <= 0 or len(text) <= limit:
        return text
    return text[: max(0, limit - 3)] + '...'


def dedupe_vocab_values_keep_order(values: List[str]) -> List[str]:
    seen = set()
    result: List[str] = []
    for value in values:
        item = norm_vocab_text(value)
        if not item or item in seen:
            continue
        seen.add(item)
        result.append(item)
    return result


def resolve_vocab_user_id(agentic_config: Optional[Dict[str, Any]] = None) -> str:
    config = agentic_config if isinstance(agentic_config, dict) else lazyllm.globals['agentic_config']
    return norm_vocab_text(config.get('user_id'))


def serialize_vocab_string_list(values: List[str]) -> str:
    return json.dumps(dedupe_vocab_values_keep_order(values), ensure_ascii=False)


def serialize_vocab_backend_actions(actions: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
    serialized: List[Dict[str, Any]] = []
    for action in actions:
        serialized.append({
            'reason': norm_vocab_text(action.get('reason')),
            'words': dedupe_vocab_values_keep_order(action.get('words') or []),
            'description': norm_vocab_text(action.get('description')),
            'group_ids': serialize_vocab_string_list(action.get('group_ids') or []),
            'user_id': norm_vocab_text(action.get('user_id')),
            'message_ids': serialize_vocab_string_list(action.get('message_ids') or []),
            'action': norm_vocab_text(action.get('action')),
        })
    return serialized


def summarize_vocab_suggestion_for_log(suggestion: Dict[str, Any] | VocabSuggestion) -> Dict[str, Any]:
    payload = dump_vocab_suggestion(suggestion)
    return {
        'word': norm_vocab_text(payload.get('word')),
        'synonym': norm_vocab_text(payload.get('synonym')),
        'description': clip_vocab_text(payload.get('description'), 80),
        'reason': clip_vocab_text(payload.get('reason'), 120),
    }


def summarize_vocab_candidate_for_log(candidate: SynonymCandidate) -> Dict[str, Any]:
    return {
        'word': candidate.word,
        'synonym': candidate.synonym,
        'description': clip_vocab_text(candidate.description, 80),
        'message_ids': list(candidate.message_ids),
        'reason': clip_vocab_text(candidate.reason, 120),
    }


def summarize_vocab_action_for_log(action: Dict[str, Any]) -> Dict[str, Any]:
    return {
        'action': norm_vocab_text(action.get('action')),
        'words': dedupe_vocab_values_keep_order(action.get('words') or []),
        'group_ids': action.get('group_ids') or [],
        'description': clip_vocab_text(action.get('description'), 80),
        'message_ids': action.get('message_ids') or [],
        'reason': clip_vocab_text(action.get('reason'), 120),
    }


def vocab_message_ids_for_suggestion(
    histories: List[ChatHistoryRecord],
    suggestion: Dict[str, Any] | VocabSuggestion,
) -> List[str]:
    payload = dump_vocab_suggestion(suggestion)
    word_key = norm_vocab_key(payload.get('word', ''))
    synonym_key = norm_vocab_key(payload.get('synonym', ''))
    matched: List[str] = []
    for row in histories:
        searchable = row.searchable_text.casefold()
        if word_key and word_key in searchable:
            matched.append(row.message_id)
            continue
        if synonym_key and synonym_key in searchable:
            matched.append(row.message_id)
    if matched:
        return dedupe_vocab_values_keep_order(matched)

    for row in reversed(histories):
        if row.message_id:
            return [row.message_id]
    return []


def dump_vocab_suggestion(value: Dict[str, Any] | VocabSuggestion) -> Dict[str, Any]:
    if isinstance(value, VocabSuggestion):
        return value.model_dump(exclude_none=True)
    if isinstance(value, dict):
        payload = dict(value)
        for key in ('description', 'reason'):
            if payload.get(key) is None:
                payload.pop(key, None)
        return payload
    raise TypeError(f'unsupported vocab suggestion type: {type(value).__name__}')


def prepare_vocab_candidates(
    suggestions: List[Dict[str, Any] | VocabSuggestion],
    histories: List[ChatHistoryRecord],
    user_id: str,
) -> tuple[List[SynonymCandidate], List[Dict[str, Any]]]:
    seen_pairs = set()
    candidates: List[SynonymCandidate] = []
    skipped_logs: List[Dict[str, Any]] = []

    for suggestion in suggestions:
        payload = dump_vocab_suggestion(suggestion)
        word = norm_vocab_text(payload.get('word'))
        synonym = norm_vocab_text(payload.get('synonym'))
        pair_key = tuple(sorted([norm_vocab_key(word), norm_vocab_key(synonym)]))
        if not word or not synonym:
            skipped_logs.append({
                'reason': 'missing_word_or_synonym',
                'suggestion': summarize_vocab_suggestion_for_log(payload),
            })
            continue
        if pair_key in seen_pairs:
            skipped_logs.append({
                'reason': 'duplicate_pair',
                'suggestion': summarize_vocab_suggestion_for_log(payload),
            })
            continue
        seen_pairs.add(pair_key)

        candidates.append(SynonymCandidate(
            user_id=user_id,
            word=word,
            synonym=synonym,
            description=norm_vocab_text(payload.get('description')),
            reason=norm_vocab_text(payload.get('reason'))
            or f'User explicitly associated `{word}` with `{synonym}`.',
            message_ids=vocab_message_ids_for_suggestion(histories, payload),
        ))

    return candidates, skipped_logs
