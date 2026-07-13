from __future__ import annotations

import re
from collections import defaultdict
from collections.abc import Mapping
from typing import Any

import networkx as nx

from .code_index import CodeIndex, CodeSymbol

PATH_KEYS = {'allowed_roots', 'blocked_roots', 'candidate_files', 'file', 'files', 'path', 'seed_files', 'target_files'}
CHAT_TERMS = {
    'answer', 'chat', 'context', 'context_assembly', 'doc', 'doc_id', 'generate',
    'kb', 'llm', 'query', 'rag', 'recall', 'retrieval', 'retrieve', 'search',
    'top_k', 'tool',
}
PARSING_TERMS = {
    'chunk', 'document_prepare', 'extract', 'file', 'loader', 'ocr', 'parse',
    'parser', 'parsing', 'split', 'structure',
}
METRIC_TERMS = {'correctness', 'precision', 'quality', 'recall', 'score'}
TOKEN = re.compile(r'[A-Za-z_][A-Za-z0-9_]{2,}')


def localize_repair(index: CodeIndex, plan: Mapping[str, Any]) -> dict[str, Any]:
    term_groups = _term_groups(plan)
    terms = set().union(*term_groups.values()) if term_groups else set()
    domain = _domain(terms)
    hints = _hint_files(plan)
    symbols = tuple(index.symbols)
    graph = _reference_graph(symbols)
    rows = [_rank(symbol, term_groups, domain, hints) for symbol in symbols]
    rows = _add_graph_proximity(rows, graph)
    ranked = sorted(
        (row for row in rows if row['evidence_score'] > 0.0),
        key=lambda row: (-row['evidence_score'], -row['score'], row['path'], row['symbol']),
    )
    return {
        'domain': domain or 'mixed',
        'status': 'localized' if ranked else 'insufficient_evidence',
        'terms': {key: sorted(value) for key, value in term_groups.items()},
        'ranked_symbols': ranked[:40],
        'weak_hints': {'candidate_files': sorted(hints), 'used_as_authority': False},
        'index': {'file_count': len(index.files), 'symbol_count': len(index.symbols),
                  'reference_edges': graph.number_of_edges()},
    }


def _rank(symbol: CodeSymbol, term_groups: Mapping[str, set[str]], domain: str, hints: set[str]) -> dict[str, Any]:
    haystacks = {
        'symbol': symbol.qualname.lower(),
        'path': symbol.path.lower(),
        'args': ' '.join(symbol.args).lower(),
        'calls': ' '.join(symbol.calls).lower(),
        'exceptions': ' '.join(symbol.exceptions).lower(),
        'identifiers': ' '.join(symbol.identifiers).lower(),
        'literals': ' '.join(symbol.literals).lower(),
        'source_text': ' '.join(symbol.source_terms).lower(),
    }
    score, evidence_score, evidence = 0.0, 0.0, []
    if domain and symbol.domain == domain:
        score += 0.2
        evidence.append(f'domain matched {domain}')
    if symbol.path in hints:
        score += 0.1
        evidence.append('weak candidate file hint matched')
    weights = {
        'trace': 0.18,
        'failure': 0.2,
        'metric': 0.16,
        'error': 0.2,
        'objective': 0.12,
        'analysis': 0.1,
    }
    fields = {
        'symbol': 1.0,
        'path': 0.8,
        'args': 0.7,
        'calls': 0.9,
        'exceptions': 0.9,
        'identifiers': 0.8,
        'literals': 0.8,
        'source_text': 0.6,
    }
    for category, terms in term_groups.items():
        for term in terms:
            for field, text in haystacks.items():
                if term and term in text:
                    delta = weights.get(category, 0.08) * fields[field]
                    score += delta
                    evidence_score += delta
                    evidence.append(f'{category} term {term} matched {field}')
                    break
    return _row(symbol, score, evidence_score, evidence)


def _add_graph_proximity(rows: list[dict[str, Any]], graph: nx.DiGraph) -> list[dict[str, Any]]:
    direct = {row['id'] for row in rows if row['evidence_score'] > 0.0}
    if not direct:
        return rows
    for row in rows:
        if row['id'] in direct:
            continue
        neighbors = set(graph.predecessors(row['id'])) | set(graph.successors(row['id']))
        near = sorted(neighbors & direct)
        if near:
            row['score'] = round(row['score'] + 0.06, 4)
            row['evidence_score'] = round(row['evidence_score'] + 0.06, 4)
            row['evidence'].append(f'call graph adjacent to {near[0]}')
    return rows


def _reference_graph(symbols: tuple[CodeSymbol, ...]) -> nx.DiGraph:
    graph = nx.DiGraph()
    by_name: dict[str, list[str]] = defaultdict(list)
    for symbol in symbols:
        sid = _id(symbol)
        graph.add_node(sid)
        by_name[symbol.qualname.rsplit('.', 1)[-1]].append(sid)
    for symbol in symbols:
        sid = _id(symbol)
        for call in symbol.calls:
            for target in by_name.get(call.rsplit('.', 1)[-1], ()):
                if target != sid:
                    graph.add_edge(sid, target)
    return graph


def _term_groups(plan: Mapping[str, Any]) -> dict[str, set[str]]:
    groups: dict[str, set[str]] = defaultdict(set)
    _collect_terms(plan.get('objective'), 'objective', groups)
    _collect_terms(plan.get('brief'), 'analysis', groups)
    _collect_terms(plan.get('selected_group'), 'analysis', groups)
    _collect_terms(plan.get('repair_group_queue'), 'analysis', groups)
    for term in set().union(*groups.values()) if groups else set():
        if term in CHAT_TERMS or term in PARSING_TERMS:
            groups['trace'].add(term)
        if any(metric in term for metric in METRIC_TERMS):
            groups['metric'].add(term)
    return dict(groups)


def _collect_terms(value: Any, category: str, groups: dict[str, set[str]], key: str = '') -> None:
    if isinstance(value, Mapping):
        for child_key, child in value.items():
            text_key = str(child_key)
            if text_key in PATH_KEYS:
                continue
            _collect_terms(child, _category(text_key, category), groups, text_key)
    elif isinstance(value, (list, tuple, set)):
        for item in value:
            _collect_terms(item, category, groups, key)
    elif value is not None:
        terms = _expand(str(value).lower())
        groups[_category(key, category)].update(terms)


def _category(key: str, fallback: str) -> str:
    lowered = key.lower()
    if any(token in lowered for token in ('trace', 'stage', 'route', 'block')):
        return 'trace'
    if any(token in lowered for token in ('failure', 'issue', 'symptom', 'reason')):
        return 'failure'
    if any(token in lowered for token in ('metric', 'score', 'recall', 'precision', 'correctness')):
        return 'metric'
    if any(token in lowered for token in ('error', 'exception', 'traceback')):
        return 'error'
    return fallback


def _expand(text: str) -> set[str]:
    terms = {match.group(0) for match in TOKEN.finditer(text)}
    expanded = terms | {part for term in terms for part in term.split('_') if len(part) > 2}
    variants = {
        'retrieval': 'retrieve',
        'parsing': 'parse',
        'generation': 'generate',
        'chunking': 'chunk',
    }
    return expanded | {variants[term] for term in expanded if term in variants}


def _domain(terms: set[str]) -> str:
    chat = bool(terms & CHAT_TERMS)
    parsing = bool(terms & PARSING_TERMS)
    return 'mixed' if chat and parsing else 'chat' if chat else 'parsing' if parsing else ''


def _hint_files(plan: Mapping[str, Any]) -> set[str]:
    brief = plan.get('brief') if isinstance(plan.get('brief'), Mapping) else {}
    group = plan.get('selected_group') if isinstance(plan.get('selected_group'), Mapping) else {}
    values = [*list(brief.get('target_files') or ()), *list(brief.get('seed_files') or ()),
              *list(group.get('candidate_files') or ())]
    return {str(value).strip().strip('/') for value in values if str(value or '').strip()}


def _row(symbol: CodeSymbol, score: float, evidence_score: float, evidence: list[str]) -> dict[str, Any]:
    return {
        'id': _id(symbol),
        'path': symbol.path,
        'domain': symbol.domain,
        'symbol': symbol.qualname,
        'lineno': symbol.lineno,
        'end_lineno': symbol.end_lineno,
        'score': round(score, 4),
        'evidence_score': round(evidence_score, 4),
        'evidence': evidence[:12],
        'args': list(symbol.args),
    }


def _id(symbol: CodeSymbol) -> str:
    return f'{symbol.path}:{symbol.qualname}'
