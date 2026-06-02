"""Vocabulary evolution pipeline.

This module keeps only the algorithm-side extraction flow:

1. Read recent chat histories by user.
2. Slice histories into LLM-friendly chunks.
3. Extract high-confidence synonym pairs with evidence message IDs.
4. Compare them against the existing vocab groups.
5. Serialize backend action dicts and submit them back to core.
"""
from __future__ import annotations

import json
import re
from collections import defaultdict
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from typing import Any, Callable, Dict, Iterable, List, Optional, Sequence, Tuple

import lazyllm
from lazyllm import LOG, pipeline, AutoModel
from lazyllm.components import ChatPrompter
from lazyllm.components.formatter import JsonFormatter
from lazyllm.module import ModuleBase

LAZYLLM_CONTEXT_CREATE_USER_ATTR = 'user' + '_id'


_EXTRACTION_PROMPT = """You are a "Vocabulary Evolution Extractor".

Task: From a given segment of user chat history, extract only synonym pairs that are "very clearly and directly suitable for the user's vocabulary".  # noqa: E501

Only extract when the following evidence is sufficiently clear:
1. The user explicitly says "remember A is B", "A refers to B", "A and B are the same thing".
2. The user repeatedly and consistently uses A and B interchangeably across multiple turns with consistent meaning.

Rules:
1. Quality over quantity. Return an empty list [] when there is no clear evidence.
2. Each record can only contain one word and one synonym; arrays, compound phrases, or multi-word mixing are not allowed.  # noqa: E501
3. message_ids must come from the message IDs provided in the input, and must include at least 1.
4. description briefly explains the semantic context where this synonym relationship applies.
5. reason explains why this record is valid; write reason in the same language as the cited user history segments (Chinese segments -> Chinese reason, English segments -> English reason).
6. Return at most {max_pairs} records.

Below are the available user history segments. Each line binds a message_id with the corresponding user's original text; the returned message_ids can only be selected from these segments:  # noqa: E501
{history_segments}

Output must be a JSON array with elements strictly structured as follows:
[
    {
    "word": "apple",
    "synonym": "apple_cn",
    "description": "fruit context",
    "reason": "user explicitly asked to remember that apple is apple_cn",
    "message_ids": ["msg_1"]
    }
]
Do not output any explanation other than JSON."""

_CONFLICT_PROMPT = """You are a "Synonym Group Conflict Resolver".

Task: A new word and an anchor word have been extracted as synonyms, but the anchor word already belongs to multiple synonym groups. Determine which existing groups the new word can unambiguously join.  # noqa: E501

Input will provide:
1. candidate_word: The new word to be added to the vocabulary.
2. anchor_word: The word that already exists in multiple synonym groups.
3. description: Semantic description of the synonym relationship.
4. evidence: Conversation evidence (containing message_id and text snippets).
5. existing_groups: Existing candidate synonym groups, each containing group_id, description, words.

Decision principles:
1. Only place candidate_word in group_ids_can_join when the context is sufficiently clear.
2. If the context is clear enough to definitively exclude certain groups, place them in excluded_group_ids.
3. Groups that cannot be clearly determined, may still belong, and require user confirmation go into conflict_group_ids.
4. If nothing is clear, place all candidate groups in conflict_group_ids.
5. Do not fabricate new group_ids.

Important semantic constraints:
1. conflict_group_ids means "multiple possible memberships remain and the model cannot determine", NOT "semantic conflict" or "clearly does not belong".  # noqa: E501
2. If evidence clearly rules out certain groups (e.g. "this is an engineering context, not a financial term, not a chemical reagent"), those group_ids must go into excluded_group_ids, not conflict_group_ids.  # noqa: E501
3. Each candidate group_id can only appear in one of the three categories: group_ids_can_join, excluded_group_ids, conflict_group_ids.  # noqa: E501
4. If a group_id has been clearly excluded, do not ask the user to confirm it again.

Candidate word: {candidate_word}
Anchor word: {anchor_word}
Semantic description: {description}

Conversation evidence:
{evidence}

Existing candidate groups:
{existing_groups}

Example:
If evidence clearly states "this is a railway engineering context, not a financial term, not a chemical reagent", and the candidate groups are g1=railway engineering, g2=finance, g3=chemistry, the output should be:  # noqa: E501
{
    "reason": "K clearly belongs to the railway engineering context, and finance and chemistry contexts have been excluded.",  # noqa: E501
    "group_ids_can_join": ["g1"],
    "excluded_group_ids": ["g2", "g3"],
    "conflict_group_ids": []
}

Output JSON (reason must match the language of the conversation evidence: Chinese for Chinese, English for English):
{
  "reason": "concise explanation",
    "group_ids_can_join": ["g1"],
    "excluded_group_ids": [],
  "conflict_group_ids": ["g2", "g3"]
}

Do not output any explanation other than JSON."""


_SENTENCE_BOUNDARY_RE = re.compile(r'.*?(?:[。！？!?；;]+|[\n]+|$)', re.S)


def _now_utc() -> datetime:
    return datetime.now(timezone.utc)


def norm_text(value: Any) -> str:
    return ' '.join(str(value or '').strip().split())


def _norm_key(value: str) -> str:
    return norm_text(value).casefold()


def _dedupe_keep_order(values: Iterable[str]) -> List[str]:
    seen = set()
    out = []
    for value in values:
        item = norm_text(value)
        if not item or item in seen:
            continue
        seen.add(item)
        out.append(item)
    return out


def _clip_text(value: str, limit: int) -> str:
    value = norm_text(value)
    if limit <= 0 or len(value) <= limit:
        return value
    return value[: max(0, limit - 3)] + '...'


def _split_text_for_limit(value: Any, limit: int) -> List[str]:
    raw = str(value or '').replace('\r\n', '\n').replace('\r', '\n').strip()
    if not raw:
        return []
    limit = max(1, limit)
    pieces = []
    for match in _SENTENCE_BOUNDARY_RE.finditer(raw):
        piece = norm_text(match.group(0))
        if piece:
            pieces.append(piece)
    if not pieces:
        pieces = [norm_text(raw)]

    segments: List[str] = []
    current = ''
    for piece in pieces:
        if len(piece) > limit:
            if current:
                segments.append(current)
                current = ''
            for start in range(0, len(piece), limit):
                fragment = norm_text(piece[start:start + limit])
                if fragment:
                    segments.append(fragment)
            continue
        if not current:
            current = piece
            continue
        candidate = f'{current} {piece}'
        if len(candidate) <= limit:
            current = candidate
        else:
            segments.append(current)
            current = piece
    if current:
        segments.append(current)
    return segments


def _format_evidence_lines(evidence: Sequence[Dict[str, str]]) -> str:
    lines = [f'- [message_id={item["message_id"]}] {item["text"]}' for item in evidence if item.get('message_id')]
    return '\n'.join(lines) if lines else 'N/A'


def _format_group_summaries(groups: Sequence[Dict[str, Any]]) -> str:
    lines = []
    for group in groups:
        group_id = norm_text(group.get('group_id'))
        description = norm_text(group.get('description')) or 'N/A'
        words = ', '.join(_dedupe_keep_order(group.get('words') or [])) or 'N/A'
        lines.append(f'[group_id={group_id}] description={description}; words={words}')
    return '\n'.join(lines) if lines else 'N/A'


def json_dump_list(values: Sequence[str]) -> str:
    return json.dumps(_dedupe_keep_order(values), ensure_ascii=False)


def _summarize_candidate_for_log(candidate: 'SynonymCandidate') -> Dict[str, Any]:
    return {
        'word': candidate.word,
        'synonym': candidate.synonym,
        'description': _clip_text(candidate.description, 80),
        'reason': _clip_text(candidate.reason, 120),
        'message_ids': list(candidate.message_ids),
    }


def summarize_action_for_log(action: Dict[str, Any]) -> Dict[str, Any]:
    return {
        'action': norm_text(action.get('action')),
        'words': _dedupe_keep_order(action.get('words') or []),
        'group_ids': _dedupe_keep_order(action.get('group_ids') or []),
        'description': _clip_text(action.get('description'), 80),
        'reason': _clip_text(action.get('reason'), 120),
        'message_ids': _dedupe_keep_order(action.get('message_ids') or []),
    }


@dataclass
class VocabEvolutionRequest:
    user_id: str = ''
    start_time: Optional[datetime] = None
    end_time: Optional[datetime] = None
    lookback_days: int = 7
    max_chunk_chars: int = 3200
    max_pairs_per_chunk: int = 3
    extraction_retries: int = 3
    conflict_retries: int = 3
    core_db_dsn: Optional[str] = None
    core_db_url: Optional[str] = None
    vocab_db_url: Optional[str] = None

    @classmethod
    def from_value(cls, value: 'VocabEvolutionRequest | Dict[str, Any] | None') -> 'VocabEvolutionRequest':
        if isinstance(value, cls):
            return value
        if isinstance(value, dict):
            payload = dict(value)
            user_id = norm_text(payload.pop('user_id', ''))
            if user_id:
                payload['user_id'] = user_id
            return cls(**payload)
        return cls()

    def resolve_time_range(self) -> Tuple[datetime, datetime]:
        end_time = self.end_time or _now_utc()
        start_time = self.start_time or (end_time - timedelta(days=max(1, self.lookback_days)))
        return start_time, end_time


@dataclass
class ChatHistoryRecord:
    user_id: str
    conversation_id: str
    message_id: str
    seq: int
    raw_content: str = ''
    content: str = ''
    result: str = ''
    create_time: Optional[datetime] = None

    @classmethod
    def from_dict(cls, value: Dict[str, Any]) -> 'ChatHistoryRecord':
        return cls(
            user_id=norm_text(value.get('user_id')),
            conversation_id=norm_text(value.get('conversation_id')),
            message_id=norm_text(value.get('message_id')),
            seq=int(value.get('seq') or 0),
            raw_content=str(value.get('raw_content') or ''),
            content=str(value.get('content') or ''),
            result=str(value.get('result') or ''),
            create_time=value.get('create_time'),
        )

    @property
    def user_text(self) -> str:
        return str(self.content or self.raw_content or '')

    @property
    def searchable_text(self) -> str:
        return norm_text(self.user_text)

    def prompt_block(self, per_field_limit: int = 320) -> str:
        return f'[message_id={self.message_id}] {_clip_text(self.user_text, per_field_limit)}'


@dataclass
class SynonymCandidate:
    user_id: str
    word: str
    synonym: str
    description: str = ''
    reason: str = ''
    message_ids: List[str] = field(default_factory=list)

    def pair_key(self) -> Tuple[str, str]:
        items = sorted([_norm_key(self.word), _norm_key(self.synonym)])
        return items[0], items[1]


class HistoryCollector(ModuleBase):
    def __init__(
        self,
        fetch_histories_fn: Optional[Callable[..., List[Dict[str, Any]]]] = None,
        return_trace: bool = False,
    ) -> None:
        super().__init__(return_trace=return_trace)
        self._fetch_histories = fetch_histories_fn or (lambda *args, **kwargs: [])

    def forward(self, payload: Dict[str, Any], **kwargs: Any) -> Dict[str, Any]:
        request = VocabEvolutionRequest.from_value(payload.get('request'))
        user_id = norm_text(payload.get('user_id'))
        start_time, end_time = request.resolve_time_range()
        histories = self._fetch_histories(
            user_id,
            start_time=start_time,
            end_time=end_time,
            db_dsn=request.core_db_dsn,
            db_url=request.core_db_url,
        )
        rows = [ChatHistoryRecord.from_dict(item) for item in histories]
        return {
            'request': request,
            'user_id': user_id,
            'histories': rows,
        }


class HistoryChunker(ModuleBase):
    def __init__(self, return_trace: bool = False) -> None:
        super().__init__(return_trace=return_trace)

    def forward(self, payload: Dict[str, Any], **kwargs: Any) -> Dict[str, Any]:
        request: VocabEvolutionRequest = payload['request']
        histories: List[ChatHistoryRecord] = payload['histories']
        max_chunk_chars = max(1, request.max_chunk_chars)
        chunks = []
        current_parts: List[str] = []
        current_message_ids: List[str] = []
        current_chars = 0

        def _flush_current() -> None:
            nonlocal current_parts, current_message_ids, current_chars
            if not current_parts:
                return
            chunks.append({
                'chunk_id': f'{payload["user_id"]}-chunk-{len(chunks) + 1}',
                'message_ids': _dedupe_keep_order(current_message_ids),
                'text': '\n'.join(current_parts),
            })
            current_parts = []
            current_message_ids = []
            current_chars = 0

        for row in histories:
            prefix = f'[message_id={row.message_id}] '
            available_chars = max(1, max_chunk_chars - len(prefix))
            for segment in _split_text_for_limit(row.user_text, available_chars):
                block = f'{prefix}{segment}'
                block_len = len(block)
                sep_len = 1 if current_parts else 0
                if current_parts and current_chars + sep_len + block_len > max_chunk_chars:
                    _flush_current()
                    sep_len = 0
                current_parts.append(block)
                current_message_ids.append(row.message_id)
                current_chars += sep_len + block_len

        _flush_current()
        payload = dict(payload)
        payload['chunks'] = chunks
        return payload


class SynonymExtractionModule(ModuleBase):
    def __init__(self, llm: Optional[Any] = None, *, return_trace: bool = False) -> None:
        super().__init__(return_trace=return_trace)
        if llm is None:
            llm = AutoModel(model='llm')
        base_llm = llm
        self._llm = base_llm.share(
            prompt=ChatPrompter(instruction=_EXTRACTION_PROMPT),
            format=JsonFormatter(),
            stream=False,
        )

    def _coerce_output(self, value: Any) -> List[Dict[str, Any]]:
        if isinstance(value, list):
            return [item for item in value if isinstance(item, dict)]
        if isinstance(value, dict):
            for key in ('pairs', 'items', 'results', 'data'):
                item = value.get(key)
                if isinstance(item, list):
                    return [part for part in item if isinstance(part, dict)]
        return []

    def _validate_candidate(
        self,
        user_id: str,
        item: Dict[str, Any],
        history_by_id: Dict[str, ChatHistoryRecord],
    ) -> Optional[SynonymCandidate]:
        word = norm_text(item.get('word'))
        synonym = norm_text(item.get('synonym'))
        if not word or not synonym or _norm_key(word) == _norm_key(synonym):
            return None
        message_ids = item.get('message_ids') or []
        if not isinstance(message_ids, list):
            return None
        valid_ids = []
        for message_id in message_ids:
            msg_id = norm_text(message_id)
            row = history_by_id.get(msg_id)
            if not row:
                continue
            searchable = row.searchable_text.casefold()
            if _norm_key(word) in searchable or _norm_key(synonym) in searchable:
                valid_ids.append(msg_id)
        valid_ids = _dedupe_keep_order(valid_ids)
        if not valid_ids:
            return None
        return SynonymCandidate(
            user_id=user_id,
            word=word,
            synonym=synonym,
            description=norm_text(item.get('description')),
            reason=norm_text(item.get('reason')),
            message_ids=valid_ids,
        )

    def _dedupe_candidates(self, items: Sequence[SynonymCandidate]) -> List[SynonymCandidate]:
        merged: Dict[Tuple[str, str], SynonymCandidate] = {}
        for item in items:
            key = item.pair_key()
            if key not in merged:
                merged[key] = item
                continue
            existing = merged[key]
            existing.message_ids = _dedupe_keep_order(existing.message_ids + item.message_ids)
            if not existing.description and item.description:
                existing.description = item.description
            if not existing.reason and item.reason:
                existing.reason = item.reason
        return list(merged.values())

    def forward(self, payload: Dict[str, Any], **kwargs: Any) -> Dict[str, Any]:
        request: VocabEvolutionRequest = payload['request']
        user_id = payload['user_id']
        histories: List[ChatHistoryRecord] = payload['histories']
        history_by_id = {row.message_id: row for row in histories}
        extracted: List[SynonymCandidate] = []

        for chunk in payload.get('chunks', []):
            prompt_payload = {
                'max_pairs': str(request.max_pairs_per_chunk),
                'history_segments': chunk['text'],
            }
            raw_result: Any = []
            for attempt in range(max(1, request.extraction_retries)):
                try:
                    raw_result = self._llm(prompt_payload, **kwargs)
                    records = self._coerce_output(raw_result)
                    if records is not None:
                        break
                except Exception as exc:
                    LOG.warning(
                        f'[VocabEvolution] extraction failed user={user_id!r} '
                        f'attempt={attempt + 1} error={exc}'
                    )
            for item in self._coerce_output(raw_result):
                candidate = self._validate_candidate(user_id, item, history_by_id)
                if candidate is not None:
                    extracted.append(candidate)

        payload = dict(payload)
        payload['candidates'] = self._dedupe_candidates(extracted)
        return payload


class ActionPlanningModule(ModuleBase):
    def __init__(
        self,
        llm: Optional[Any] = None,
        *,
        fetch_vocab_groups_fn: Optional[Callable[..., Dict[str, Dict[str, Any]]]] = None,
        return_trace: bool = False,
    ) -> None:
        super().__init__(return_trace=return_trace)
        self._base_llm = llm
        self._llm = None
        self._fetch_vocab_groups = fetch_vocab_groups_fn or (lambda *args, **kwargs: {})

    def _get_llm(self) -> Any:
        if self._llm is None:
            base_llm = self._base_llm or AutoModel(model='llm')
            self._llm = base_llm.share(
                prompt=ChatPrompter(instruction=_CONFLICT_PROMPT),
                format=JsonFormatter(),
                stream=False,
            )
        return self._llm

    def _build_memberships(self, groups: Dict[str, Dict[str, Any]]) -> Dict[str, List[str]]:
        memberships: Dict[str, List[str]] = defaultdict(list)
        for group_id, group in groups.items():
            for word in group.get('words', []):
                key = _norm_key(word)
                if group_id not in memberships[key]:
                    memberships[key].append(group_id)
        return dict(memberships)

    def _should_split_single_group_by_description(
        self,
        candidate: SynonymCandidate,
        groups: Dict[str, Dict[str, Any]],
        group_id: str,
    ) -> bool:
        candidate_description = _norm_key(candidate.description)
        if not candidate_description:
            return False

        group = groups.get(group_id) or {}
        group_description = _norm_key(group.get('description'))
        if not group_description or candidate_description == group_description:
            return False

        return True

    def _resolve_conflict(
        self,
        request: VocabEvolutionRequest,
        candidate_word: str,
        anchor_word: str,
        candidate: SynonymCandidate,
        histories: Dict[str, ChatHistoryRecord],
        groups: Dict[str, Dict[str, Any]],
        candidate_group_ids: List[str],
        **kwargs: Any,
    ) -> Dict[str, Any]:
        evidence = [
            {
                'message_id': message_id,
                'text': _clip_text(histories[message_id].searchable_text, 240),
            }
            for message_id in candidate.message_ids
            if message_id in histories
        ]
        existing_groups = [groups[group_id] for group_id in candidate_group_ids if group_id in groups]
        prompt_payload = {
            'candidate_word': candidate_word,
            'anchor_word': anchor_word,
            'description': candidate.description or 'N/A',
            'evidence': _format_evidence_lines(evidence),
            'existing_groups': _format_group_summaries(existing_groups),
        }
        response: Dict[str, Any] = {}
        for attempt in range(max(1, request.conflict_retries)):
            try:
                raw = self._get_llm()(prompt_payload, **kwargs)
                if isinstance(raw, dict):
                    response = raw
                    break
            except Exception as exc:
                LOG.warning(
                    f'[VocabEvolution] conflict resolve failed user={candidate.user_id!r} '
                    f'attempt={attempt + 1} error={exc}'
                )
        allowed = _dedupe_keep_order(response.get('group_ids_can_join') or response.get('allowed_group_ids') or [])
        excluded = _dedupe_keep_order(
            response.get('excluded_group_ids')
            or response.get('group_ids_cannot_join')
            or response.get('rejected_group_ids')
            or response.get('ruled_out_group_ids')
            or []
        )
        conflicts = _dedupe_keep_order(response.get('conflict_group_ids') or [])
        allowed = [group_id for group_id in allowed if group_id in candidate_group_ids]
        excluded = [group_id for group_id in excluded if group_id in candidate_group_ids and group_id not in allowed]
        conflicts = [
            group_id for group_id in conflicts
            if group_id in candidate_group_ids and group_id not in allowed and group_id not in excluded
        ]
        unresolved = [
            group_id for group_id in candidate_group_ids
            if group_id not in allowed and group_id not in excluded and group_id not in conflicts
        ]
        conflicts = _dedupe_keep_order(conflicts + unresolved)
        if not allowed and len(conflicts) < 2 and not excluded:
            conflicts = list(candidate_group_ids)
        return {
            'reason': (
                norm_text(response.get('reason'))
                or candidate.reason
                or f'`{candidate_word}` and `{anchor_word}` membership requires further confirmation.'
            ),
            'allowed_group_ids': allowed,
            'excluded_group_ids': excluded,
            'conflict_group_ids': conflicts,
        }

    def _build_action(
        self,
        *,
        reason: str,
        words: Sequence[str],
        description: str,
        group_ids: Sequence[str],
        user_id: str,
        message_ids: Sequence[str],
        action: str,
    ) -> Dict[str, Any]:
        return {
            'reason': norm_text(reason),
            'words': _dedupe_keep_order(words),
            'description': norm_text(description),
            'group_ids': _dedupe_keep_order(group_ids),
            'user_id': norm_text(user_id),
            'message_ids': _dedupe_keep_order(message_ids),
            'action': norm_text(action),
        }

    def _merge_related_actions(self, actions: List[Dict[str, Any]]) -> List[Dict[str, Any]]:
        """Merge actions that belong to the same target so the backend receives batched groups.

        When the LLM extracts pairwise candidates for the same underlying concept
        (e.g. A=B, A=C, A=D), each pair is planned independently. Without merging,
        ``create_new_group`` actions would produce a separate group per pair.

        Strategy:
        * ``create_new_group`` — merge via connected components (overlapping words).
        * ``add_to_group`` / ``conflict`` — all actions sharing the same *group_ids*
          are merged unconditionally, because they target the same existing group(s).
        """
        if len(actions) <= 1:
            return actions

        # ---- bucket by (action_type, sorted_group_ids) ----
        buckets: Dict[Tuple[str, ...], List[int]] = defaultdict(list)
        for idx, action in enumerate(actions):
            key = (action.get('action', ''), tuple(sorted(action.get('group_ids', []))))
            buckets[key].append(idx)

        merged_indices: set = set()
        merged_results: List[Dict[str, Any]] = []

        for (action_type, _group_ids), indices in buckets.items():
            if len(indices) <= 1:
                continue

            if action_type == 'create_new_group':
                # Merge via connected-components on word overlap.
                word_sets = {
                    idx: set(_norm_key(w) for w in actions[idx].get('words', []))
                    for idx in indices
                }
                remaining = set(indices)
                while remaining:
                    seed = remaining.pop()
                    component = {seed}
                    changed = True
                    while changed:
                        changed = False
                        for idx in list(remaining):
                            if any(word_sets[idx] & word_sets[ci] for ci in component):
                                component.add(idx)
                                remaining.discard(idx)
                                changed = True
                    if len(component) <= 1:
                        continue
                    merged_indices.update(component)
                    merged_results.append(self._build_merged_action(actions, list(component)))
            else:
                # add_to_group / conflict: same group_ids → always safe to batch.
                merged_indices.update(indices)
                merged_results.append(self._build_merged_action(actions, list(indices)))

        result: List[Dict[str, Any]] = []
        for idx, action in enumerate(actions):
            if idx not in merged_indices:
                result.append(action)
        result.extend(merged_results)
        return result

    @staticmethod
    def _build_merged_action(
        actions: List[Dict[str, Any]],
        indices: List[int],
    ) -> Dict[str, Any]:
        """Return a single action that unions words and message_ids from *indices*."""
        merged = dict(actions[indices[0]])
        all_words: List[str] = []
        all_msg_ids: List[str] = []
        for idx in indices:
            all_words.extend(actions[idx].get('words', []))
            all_msg_ids.extend(actions[idx].get('message_ids', []))
        merged['words'] = _dedupe_keep_order(all_words)
        merged['message_ids'] = _dedupe_keep_order(all_msg_ids)
        for idx in indices[1:]:
            act = actions[idx]
            if not merged.get('description') and act.get('description'):
                merged['description'] = act['description']
            if not merged.get('reason') and act.get('reason'):
                merged['reason'] = act['reason']
        return merged

    def _dedupe_actions(self, actions: Sequence[Dict[str, Any]]) -> List[Dict[str, Any]]:
        merged: Dict[Tuple[str, str, Tuple[str, ...], Tuple[str, ...]], Dict[str, Any]] = {}
        for action in actions:
            words_key = tuple(sorted(_norm_key(word) for word in action.get('words', [])))
            groups_key = tuple(sorted(action.get('group_ids', [])))
            key = (
                action.get('action', ''),
                action.get('user_id', ''),
                words_key,
                groups_key,
            )
            if key not in merged:
                merged[key] = dict(action)
                continue
            existing = merged[key]
            existing['message_ids'] = _dedupe_keep_order(
                existing.get('message_ids', []) + action.get('message_ids', [])
            )
            if not existing.get('reason') and action.get('reason'):
                existing['reason'] = action['reason']
            if not existing.get('description') and action.get('description'):
                existing['description'] = action['description']
        return list(merged.values())

    def forward(self, payload: Dict[str, Any], **kwargs: Any) -> Dict[str, Any]:
        request: VocabEvolutionRequest = payload['request']
        user_id = payload['user_id']
        histories: Dict[str, ChatHistoryRecord] = {row.message_id: row for row in payload['histories']}
        groups = self._fetch_vocab_groups(user_id, db_url=request.vocab_db_url)
        memberships = self._build_memberships(groups)
        actions: List[Dict[str, Any]] = []
        skipped: List[str] = []
        candidates = list(payload.get('candidates', []))

        LOG.info(
            '[VocabEvolution] planner start '
            f'user_id={user_id!r} candidate_count={len(candidates)} existing_group_count={len(groups)}'
        )

        for candidate in candidates:
            word_groups = memberships.get(_norm_key(candidate.word), [])
            synonym_groups = memberships.get(_norm_key(candidate.synonym), [])
            common = sorted(set(word_groups) & set(synonym_groups))
            candidate_summary = _summarize_candidate_for_log(candidate)

            if common:
                reason = f'{candidate.word}/{candidate.synonym}: already covered by existing group(s) {common}.'
                skipped.append(reason)
                LOG.info(
                    '[VocabEvolution] planner decision '
                    f'user_id={user_id!r} decision=skip_already_covered '
                    f'candidate={json.dumps(candidate_summary, ensure_ascii=False)} common_groups={common}'
                )
                continue
            if not word_groups and not synonym_groups:
                action = self._build_action(
                    reason=candidate.reason or f'Extracted a clear synonym relationship between `{candidate.word}` and `{candidate.synonym}` from chat history.',  # noqa: E501
                    words=[candidate.word, candidate.synonym],
                    description=candidate.description,
                    group_ids=[],
                    user_id=user_id,
                    message_ids=list(candidate.message_ids),
                    action='create_new_group',
                )
                actions.append(action)
                LOG.info(
                    '[VocabEvolution] planner decision '
                    f'user_id={user_id!r} decision=create_new_group '
                    f'candidate={json.dumps(candidate_summary, ensure_ascii=False)} '
                    f'action={json.dumps(summarize_action_for_log(action), ensure_ascii=False)}'
                )
                continue
            if word_groups and synonym_groups:
                reason = (
                    f'{candidate.word}/{candidate.synonym}: both words already exist '
                    'in different groups; skip merge proposal.'
                )
                skipped.append(reason)
                LOG.info(
                    '[VocabEvolution] planner decision '
                    f'user_id={user_id!r} decision=skip_existing_split_groups '
                    f'candidate={json.dumps(candidate_summary, ensure_ascii=False)} '
                    f'word_groups={word_groups} synonym_groups={synonym_groups}'
                )
                continue

            if word_groups:
                new_word, anchor_word, anchor_groups = candidate.synonym, candidate.word, word_groups
            else:
                new_word, anchor_word, anchor_groups = candidate.word, candidate.synonym, synonym_groups

            if len(anchor_groups) == 1:
                if self._should_split_single_group_by_description(candidate, groups, anchor_groups[0]):
                    action = self._build_action(
                        reason=(
                            candidate.reason
                            or f'`{candidate.word}` and `{candidate.synonym}` were '
                            'provided under a new domain-specific description.'
                        ),
                        words=[candidate.word, candidate.synonym],
                        description=candidate.description,
                        group_ids=[],
                        user_id=user_id,
                        message_ids=list(candidate.message_ids),
                        action='create_new_group',
                    )
                    actions.append(action)
                    LOG.info(
                        '[VocabEvolution] planner decision '
                        f'user_id={user_id!r} decision=create_new_group_description_split '
                        f'candidate={json.dumps(candidate_summary, ensure_ascii=False)} '
                        f'anchor_group_id={anchor_groups[0]!r} '
                        f'existing_description={groups.get(anchor_groups[0], {}).get("description", "")} '
                        f'action={json.dumps(summarize_action_for_log(action), ensure_ascii=False)}'
                    )
                    continue
                action = self._build_action(
                    reason=candidate.reason or f'`{new_word}` can be directly added to the synonym group containing `{anchor_word}`.',  # noqa: E501
                    words=[new_word],
                    description='',
                    group_ids=list(anchor_groups),
                    user_id=user_id,
                    message_ids=list(candidate.message_ids),
                    action='add_to_group',
                )
                actions.append(action)
                LOG.info(
                    '[VocabEvolution] planner decision '
                    f'user_id={user_id!r} decision=add_to_group '
                    f'candidate={json.dumps(candidate_summary, ensure_ascii=False)} '
                    f'anchor_word={anchor_word!r} anchor_groups={anchor_groups} '
                    f'action={json.dumps(summarize_action_for_log(action), ensure_ascii=False)}'
                )
                continue

            decision = self._resolve_conflict(
                request,
                new_word,
                anchor_word,
                candidate,
                histories,
                groups,
                list(anchor_groups),
                **kwargs,
            )
            LOG.info(
                '[VocabEvolution] planner conflict resolution '
                f'user_id={user_id!r} candidate={json.dumps(candidate_summary, ensure_ascii=False)} '
                f'anchor_word={anchor_word!r} anchor_groups={anchor_groups} '
                f'decision={json.dumps(decision, ensure_ascii=False)}'
            )
            if decision['allowed_group_ids']:
                action = self._build_action(
                    reason=decision['reason'],
                    words=[new_word],
                    description='',
                    group_ids=list(decision['allowed_group_ids']),
                    user_id=user_id,
                    message_ids=list(candidate.message_ids),
                    action='add_to_group',
                )
                actions.append(action)
                LOG.info(
                    '[VocabEvolution] planner decision '
                    f'user_id={user_id!r} decision=add_to_group_after_conflict '
                    f'action={json.dumps(summarize_action_for_log(action), ensure_ascii=False)}'
                )
            if decision['conflict_group_ids']:
                action = self._build_action(
                    reason=decision['reason'],
                    words=[new_word],
                    description='',
                    group_ids=list(decision['conflict_group_ids']),
                    user_id=user_id,
                    message_ids=list(candidate.message_ids),
                    action='conflict',
                )
                actions.append(action)
                LOG.info(
                    '[VocabEvolution] planner decision '
                    f'user_id={user_id!r} decision=conflict '
                    f'action={json.dumps(summarize_action_for_log(action), ensure_ascii=False)}'
                )
            if (
                not decision['allowed_group_ids']
                and not decision['conflict_group_ids']
                and decision.get('excluded_group_ids')
            ):
                reason = (
                    f'{new_word}/{anchor_word}: ruled out from candidate groups '
                    f'{decision["excluded_group_ids"]}.'
                )
                skipped.append(reason)
                LOG.info(
                    '[VocabEvolution] planner decision '
                    f'user_id={user_id!r} decision=skip_ruled_out '
                    f'candidate={json.dumps(candidate_summary, ensure_ascii=False)} '
                    f'excluded_group_ids={decision["excluded_group_ids"]}'
                )

        payload = dict(payload)
        deduped_actions = self._dedupe_actions(actions)
        merged_actions = self._merge_related_actions(deduped_actions)
        LOG.info(
            '[VocabEvolution] planner finished '
            f'user_id={user_id!r} action_count={len(merged_actions)} skipped_count={len(skipped)} '
            f'actions={json.dumps([summarize_action_for_log(item) for item in merged_actions], ensure_ascii=False)}'
        )
        payload['actions'] = merged_actions
        payload['skipped_reasons'] = skipped
        return payload


def get_ppl_vocab_evolution(
    *,
    extraction_llm: Optional[Any] = None,
    conflict_llm: Optional[Any] = None,
    fetch_histories_fn: Optional[Callable[..., List[Dict[str, Any]]]] = None,
    fetch_vocab_groups_fn: Optional[Callable[..., Dict[str, Dict[str, Any]]]] = None,
):
    """Build the per-user vocabulary evolution pipeline."""
    with lazyllm.save_pipeline_result():
        with pipeline() as ppl:
            ppl.collect_histories = HistoryCollector(fetch_histories_fn=fetch_histories_fn)
            ppl.build_chunks = HistoryChunker()
            ppl.extract_candidates = SynonymExtractionModule(llm=extraction_llm)
            ppl.plan_actions = ActionPlanningModule(
                llm=conflict_llm,
                fetch_vocab_groups_fn=fetch_vocab_groups_fn,
            )
    return ppl


__all__ = [
    'ActionPlanningModule',
    'ChatHistoryRecord',
    'HistoryChunker',
    'HistoryCollector',
    'LAZYLLM_CONTEXT_CREATE_USER_ATTR',
    'SynonymCandidate',
    'SynonymExtractionModule',
    'VocabEvolutionRequest',
    'get_ppl_vocab_evolution',
    'json_dump_list',
    'norm_text',
    'summarize_action_for_log',
]
