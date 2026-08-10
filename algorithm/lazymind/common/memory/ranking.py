from __future__ import annotations

import math
import re
import unicodedata

import jieba


_ASCII_OR_CJK = re.compile(r'[\w\u3400-\u9fff]+', re.UNICODE)
_ASCII_TOKEN = re.compile(
    r'(?<![A-Za-z0-9_])'
    r'(?:[A-Za-z0-9_]+(?:[-./:][A-Za-z0-9_]+)+|[A-Za-z0-9_]*[A-Za-z][A-Za-z0-9_]*)'
    r'(?![A-Za-z0-9_])'
)
_RETRIEVAL_NOISE = {
    '还', '还记得', '记得', '吗', '呢', '之前', '那个', '这个', '这', '的', '了', '过',
    '请问', '一下', '我', '你', '我们', '曾经', '提到', '说过', '关于', '是否', '是不是',
    '帮', '帮我', '帮忙', '看看', '看', '看一下', '知道', '知道吗', '告诉', '告诉我',
    '想问', '问', '问题', '什么', '是什么', '什么意思', '怎么', '怎么样', '如何',
    '继续', '接着', '下去', '为什么', '再', '再来', '一个', '再来一个', '详细',
    '具体', '说', '说说', '讲', '讲讲', '介绍', '解释', '说明', '展开', '补充',
    '请', '能', '可以', '一点', '一些', '然后', '这样', '那样', '进一步',
    'do', 'you', 'remember', 'recall', 'please', 'about', 'the', 'a', 'an', 'what', 'is',
    'continue', 'why', 'explain', 'elaborate', 'more', 'detail', 'details', 'again',
    'tell', 'me', 'can', 'could', 'would',
    '不好', '好不好', '我查', '给', '多',
}
_RETRIEVAL_NOISE_PHRASES = tuple(sorted({
    '还记得', '帮我看看', '你知道吗', '再详细说说', '请继续介绍', '好不好',
    '帮我查一下', '给我看看', '多说一点', '继续说', '展开一下',
    '能详细一点吗', '然后呢', '再来一个', '告诉我', '我想问一下',
    'do you remember', 'can you explain', 'could you explain', 'tell me more',
}, key=len, reverse=True))


def tokenize_episode_text(text: str) -> str:
    normalized = unicodedata.normalize('NFKC', str(text)).casefold()
    pieces: list[str] = []
    for block in _ASCII_OR_CJK.findall(normalized):
        if any('\u3400' <= char <= '\u9fff' for char in block):
            pieces.extend(token.strip() for token in jieba.cut_for_search(block) if token.strip())
        else:
            pieces.append(block)
    return ' '.join(pieces)


def informative_query_terms(query: str) -> list[str]:
    cleaned_query = unicodedata.normalize('NFKC', str(query)).casefold()
    for phrase in _RETRIEVAL_NOISE_PHRASES:
        cleaned_query = cleaned_query.replace(phrase, ' ')
    terms: list[str] = []
    seen: set[str] = set()
    for term in tokenize_episode_text(cleaned_query).split():
        normalized = term.casefold().strip()
        if not normalized or normalized in _RETRIEVAL_NOISE or normalized in seen:
            continue
        seen.add(normalized)
        terms.append(normalized)
    return terms


def _required_exact_tokens(query: str) -> tuple[set[str], set[str]]:
    normalized = unicodedata.normalize('NFKC', str(query))
    identifiers: set[str] = set()
    identifier_spans: list[tuple[int, int]] = []
    for match in _ASCII_TOKEN.finditer(normalized):
        token = match.group(0)
        if (
            any(char.isdigit() for char in token)
            or any(char in '_-./:' for char in token)
            or any(char.isupper() for char in token[1:])
        ):
            identifiers.add(token.casefold())
            identifier_spans.append(match.span())
    numbers = {
        match.group(0)
        for match in re.finditer(r'\d+', normalized)
        if not any(
            start <= match.start() and match.end() <= end
            for start, end in identifier_spans
        )
    }
    return identifiers, numbers


def _contains_exact_ascii(candidate: str, token: str) -> bool:
    return re.search(
        rf'(?<![A-Za-z0-9_]){re.escape(token)}(?![A-Za-z0-9_])',
        candidate,
    ) is not None


def episode_query_coverage(query: str, summary: str) -> float | None:
    """Return coverage for an admitted candidate, otherwise None."""

    query_terms = informative_query_terms(query)
    if not query_terms:
        return None

    candidate_terms = set(tokenize_episode_text(summary).split())
    matched = sum(term in candidate_terms for term in query_terms)
    required = len(query_terms) if len(query_terms) <= 2 else math.ceil(len(query_terms) * 0.5)
    if matched < required:
        return None

    normalized_candidate = unicodedata.normalize('NFKC', str(summary)).casefold()
    identifiers, numbers = _required_exact_tokens(query)
    if any(not _contains_exact_ascii(normalized_candidate, token) for token in identifiers):
        return None
    if any(not _contains_exact_ascii(normalized_candidate, number) for number in numbers):
        return None
    return matched / len(query_terms)
