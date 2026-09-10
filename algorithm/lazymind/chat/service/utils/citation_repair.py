"""Conservative citation insertion when the model omits refs."""

from __future__ import annotations

import re
from typing import Any

from .citations import (
    CITATION_PATTERN,
    SOURCE_LINK_PATTERN,
    citation_source,
    materialize_source_views,
)

_TOKEN_PATTERN = re.compile(r'[A-Za-z0-9]+|[\u4e00-\u9fff]')
_FETCHED_OVERLAP = 0.35
_SEARCHED_OVERLAP = 0.5
_WHOLE_ANSWER_OVERLAP = 0.25
_MIN_SENTENCE_TOKENS = 6
_MIN_FETCHED_CONTENT = 40
_MIN_SEARCHED_CONTENT = 200


def citation_indices_in_text(text: str) -> list[str]:
    found: list[str] = []
    seen: set[str] = set()
    for match in CITATION_PATTERN.finditer(text or ''):
        index = match.group(1)
        if index not in seen:
            seen.add(index)
            found.append(index)
    for match in SOURCE_LINK_PATTERN.finditer(text or ''):
        index = match.group(2)
        if index not in seen:
            seen.add(index)
            found.append(index)
    return found


def answer_has_citations(text: str) -> bool:
    return bool(citation_indices_in_text(text))


def _tokens(text: str) -> set[str]:
    return {token.lower() for token in _TOKEN_PATTERN.findall(text or '')}


def _split_sentences(text: str) -> list[str]:
    parts: list[str] = []
    buf: list[str] = []
    length = len(text)
    for index, char in enumerate(text):
        buf.append(char)
        ended = char in '。！？!?\n' or (
            char == '.' and (index + 1 == length or text[index + 1].isspace())
        )
        if ended:
            parts.append(''.join(buf))
            buf = []
    if buf:
        parts.append(''.join(buf))
    return parts


def _evidence_candidates(config: dict[str, Any]) -> list[tuple[str, set[str], float]]:
    candidates: list[tuple[str, set[str], float]] = []
    for view in materialize_source_views(config):
        index = str(view.get('index') or '').strip()
        content = str(view.get('content') or '').strip()
        if not index or not citation_source(config, index):
            continue
        roles = set(view.get('source_roles') or ())
        if 'fetched' in roles or view.get('source_type') == 'knowledge_base':
            if len(content) < _MIN_FETCHED_CONTENT:
                continue
            threshold = _FETCHED_OVERLAP
        elif 'searched' in roles and len(content) >= _MIN_SEARCHED_CONTENT:
            threshold = _SEARCHED_OVERLAP
        else:
            continue
        tokens = _tokens(content)
        if tokens:
            candidates.append((index, tokens, threshold))
    return candidates


def _best_index(sentence_tokens: set[str], candidates: list[tuple[str, set[str], float]]) -> str:
    if len(sentence_tokens) < _MIN_SENTENCE_TOKENS:
        return ''
    best_index = ''
    best_score = 0.0
    for index, content_tokens, threshold in candidates:
        score = len(sentence_tokens & content_tokens) / len(sentence_tokens)
        if score >= threshold and score > best_score:
            best_index = index
            best_score = score
    return best_index


def attach_missing_citations(text: str, config: dict[str, Any]) -> str:
    """Insert known `[[index]]` markers after claims that match fetched evidence.

    Leaves the original wording unchanged. Snippet-only hits are used only when
    the snippet is long enough. If nothing overlaps, the text is returned as-is.
    """
    if not text or not text.strip():
        return text
    candidates = _evidence_candidates(config)
    if not candidates:
        return text

    attached = False
    repaired_parts: list[str] = []
    for part in _split_sentences(text):
        if citation_indices_in_text(part):
            repaired_parts.append(part)
            continue
        index = _best_index(_tokens(part), candidates)
        if not index:
            repaired_parts.append(part)
            continue
        attached = True
        repaired_parts.append(f'{part.rstrip()} [[{index}]]{part[len(part.rstrip()):]}')

    repaired = ''.join(repaired_parts)
    if attached or answer_has_citations(repaired):
        return repaired

    index = _best_index(_tokens(text), [
        (item[0], item[1], _WHOLE_ANSWER_OVERLAP)
        for item in candidates
        if item[2] <= _FETCHED_OVERLAP
    ])
    if not index:
        return text
    return f'{text.rstrip()} [[{index}]]'


def added_citation_markers(original: str, repaired: str) -> str:
    original_indices = set(citation_indices_in_text(original))
    added = [
        index for index in citation_indices_in_text(repaired)
        if index not in original_indices
    ]
    if not added:
        return ''
    return ''.join(f'[[{index}]]' for index in added)
