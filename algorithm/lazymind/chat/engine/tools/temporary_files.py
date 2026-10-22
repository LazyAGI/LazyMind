"""Search conversation uploads independently of knowledge-base service readiness."""
from typing import Any, Dict, List, Optional, Sequence

import os
from concurrent.futures import TimeoutError as FuturesTimeoutError
from pathlib import Path

import lazyllm
from lazyllm import LOG
from lazyllm.common.threading import ThreadPoolExecutor
from lazyllm.tools.agent import ToolExecutionError

from lazymind.chat.engine.tools.infra import get_vocab_manager

_TMP_WHITELIST_SUFFIXES = frozenset({
    '.pdf', '.doc', '.docx', '.pptx', '.txt', '.md', '.markdown', '.lmd',
})
_TMP_OFFICE_SUFFIXES = frozenset({'.doc', '.docx', '.pptx'})
_TMP_PARSE_TIMEOUT_SECONDS = 60
_TMP_DEFAULT_TOP_K = 10
_TMP_MAX_TOP_K = 30
_TMP_DEFAULT_RETRIEVER_TOPK = 20
_TMP_DEFAULT_RERANK_TOPK = 20
_TMP_DEFAULT_K_MAX = 10
_TMP_HINT = (
    'After a hit, call read_file_resource(target, offset=max(1, line-20), limit=80) '
    'for surrounding context. Read footers decide EOF, not document headings. '
    'Do not use this tool for knowledge bases, fetched web PDFs, workspace drafts, '
    'or source code; use kb_* tools or grep instead.'
)


def _tmp_suffix(path: str) -> str:
    return Path(str(path or '').split('?', 1)[0]).suffix.lower()


def _tmp_clamp_top_k(top_k: Optional[int]) -> int:
    try:
        value = int(top_k if top_k is not None else _TMP_DEFAULT_TOP_K)
    except (TypeError, ValueError):
        value = _TMP_DEFAULT_TOP_K
    if value <= 0:
        return 1
    return min(value, _TMP_MAX_TOP_K)


def _tmp_pattern_list(grep_patterns: Any) -> List[str]:
    if grep_patterns is None:
        return []
    if isinstance(grep_patterns, str):
        item = grep_patterns.strip()
        return [item] if item else []
    if isinstance(grep_patterns, Sequence) and not isinstance(grep_patterns, (bytes, bytearray)):
        return [str(item).strip() for item in grep_patterns if str(item).strip()]
    item = str(grep_patterns).strip()
    return [item] if item else []


def _tmp_run_with_timeout(fn, timeout: float):
    with ThreadPoolExecutor(max_workers=1) as pool:
        future = pool.submit(fn)
        try:
            return future.result(timeout=timeout)
        except FuturesTimeoutError as exc:
            raise TimeoutError(
                f'parse exceeded {_TMP_PARSE_TIMEOUT_SECONDS}s'
            ) from exc


def _tmp_agentic_config() -> dict:
    cfg = lazyllm.globals.get('agentic_config') or {}
    return cfg if isinstance(cfg, dict) else {}


def _tmp_collect_uploads() -> tuple[list[dict], list[dict]]:
    from lazymind.chat.engine.tools.file_resources.resolver import (
        _dedupe_turn,
        materialize_local_path,
    )

    cfg = _tmp_agentic_config()
    history = cfg.get('history_files_per_turn') or {}
    if not isinstance(history, dict):
        history = {}
    current = [str(path) for path in (cfg.get('files') or []) if str(path).strip()]
    skipped: list[dict] = []
    items: list[dict] = []
    seen: set[str] = set()

    def consider(display: str, raw: str, turn: Optional[int]) -> None:
        if str(raw).lower().startswith(('http://', 'https://')):
            skipped.append({'target': display, 'reason': 'remote_url'})
            return
        source = materialize_local_path(raw)
        if not source or not os.path.isfile(source):
            skipped.append({'target': display, 'reason': 'missing'})
            return
        real = os.path.realpath(source)
        if real in seen:
            return
        seen.add(real)
        suffix = _tmp_suffix(source)
        if suffix not in _TMP_WHITELIST_SUFFIXES:
            skipped.append({'target': display, 'reason': 'not_in_whitelist'})
            return
        items.append({
            'target': display,
            'source_path': real,
            'suffix': suffix,
            'turn': turn,
        })

    seqs = sorted((int(key) for key in history if str(key).isdigit()), reverse=True)
    for seq in seqs:
        paths = [str(path) for path in (history.get(str(seq)) or []) if str(path).strip()]
        for display, path in _dedupe_turn(paths):
            consider(display, path, seq)
    for display, path in _dedupe_turn(current):
        consider(display, path, None)
    return items, skipped


def _tmp_prepare_doc(item: dict) -> dict:
    from lazymind.chat.engine.tools.file_resources.store import workspace_for_request

    suffix = item['suffix']
    source = item['source_path']
    if suffix == '.pdf':
        from lazymind.chat.engine.tools.file_resources.ingest import ingest_pdf_file
        from lazymind.chat.engine.tools.file_resources.store import FileResourceStore

        store = FileResourceStore(workspace_for_request())

        def ingest():
            return ingest_pdf_file(
                source,
                source='upload',
                display_name=item['target'],
                turn_seq=item.get('turn'),
                store=store,
            )

        manifest = _tmp_run_with_timeout(ingest, _TMP_PARSE_TIMEOUT_SECONDS)
        if manifest.get('parse_status') != 'ready':
            raise ValueError(manifest.get('parse_error') or 'pdf parse failed')
        parsed = str(manifest.get('parsed_path') or '')
        if not os.path.isfile(parsed):
            raise FileNotFoundError(parsed)
        return {
            **item,
            'text_path': parsed,
            'file_id': manifest.get('file_id'),
            'kind': 'pdf',
            'parse': 'ready',
        }
    if suffix in _TMP_OFFICE_SUFFIXES:
        from lazymind.chat.engine.tools.file_resources.resolver import _materialize_document_text

        workspace = workspace_for_request()

        def parse_office():
            return _materialize_document_text(source, workspace)

        parsed = _tmp_run_with_timeout(parse_office, _TMP_PARSE_TIMEOUT_SECONDS)
        return {
            **item,
            'text_path': parsed,
            'file_id': None,
            'kind': 'office',
            'parse': 'ready',
        }
    return {
        **item,
        'text_path': source,
        'file_id': None,
        'kind': 'text',
        'parse': 'ready',
    }


def _tmp_chunk_line(lines: List[str], chunk: str) -> tuple[int, bool]:
    text = str(chunk or '').strip()
    if not text:
        return 1, True
    first = next((part.strip() for part in text.splitlines() if part.strip()), '')
    needle = first[:160] if first else text[:160]
    if needle:
        for index, line in enumerate(lines, start=1):
            if needle in line:
                return index, False
    compact = ' '.join(text.split())[:120]
    if compact:
        for index, line in enumerate(lines, start=1):
            if compact[:40] and compact[:40] in ' '.join(line.split()):
                return index, False
    for index, line in enumerate(lines, start=1):
        if line.startswith('<!-- page:'):
            return index, True
    return 1, True


def _tmp_grep_hits(docs: List[dict], patterns: List[str]) -> List[dict]:
    from lazymind.chat.engine.tools.file_resources.text_window import grep_lines, load_text_lines

    hits: list[dict] = []
    for pattern in patterns:
        for doc in docs:
            try:
                lines = load_text_lines(doc['text_path'])
            except (OSError, ValueError):
                continue
            found = grep_lines(lines, pattern, max_results=50)
            for item in found.get('matches') or []:
                hits.append({
                    'target': doc['target'],
                    'file_id': doc.get('file_id'),
                    'line': item.get('line'),
                    'snippet': item.get('text'),
                    'channels': ['grep'],
                    'patterns': [pattern],
                })
    return hits


def _tmp_semantic_hits(docs: List[dict], query: str, user_id: str) -> tuple[List[dict], str]:
    from lazyllm.tools.rag.global_metadata import RAG_DOC_PATH
    from lazymind.chat.engine.tools.algo.search_temp import embed_available, retrieve_temp_nodes
    from lazymind.chat.engine.tools.file_resources.text_window import load_text_lines

    expanded = get_vocab_manager(user_id)(query)
    files = [doc['text_path'] for doc in docs]
    channel = 'vector' if embed_available() else 'bm25_chinese'
    by_path = {doc['text_path']: doc for doc in docs}
    nodes = retrieve_temp_nodes(
        files,
        expanded,
        retriever_topk=_TMP_DEFAULT_RETRIEVER_TOPK,
        rerank_topk=_TMP_DEFAULT_RERANK_TOPK,
        k_max=_TMP_DEFAULT_K_MAX,
    )
    hits: list[dict] = []
    for node in nodes or []:
        meta = getattr(node, 'global_metadata', None) or {}
        if not isinstance(meta, dict):
            meta = {}
        local_meta = getattr(node, 'metadata', None) or {}
        if not isinstance(local_meta, dict):
            local_meta = {}
        path = (
            str(meta.get(RAG_DOC_PATH) or meta.get('file_path') or meta.get('source_path') or '')
            or str(local_meta.get(RAG_DOC_PATH) or local_meta.get('file_path') or local_meta.get('source_path') or '')
        )
        doc = by_path.get(os.path.realpath(path) if path else '')
        if doc is None and path:
            for candidate in docs:
                if os.path.basename(candidate['text_path']) == os.path.basename(path):
                    doc = candidate
                    break
        if doc is None and len(docs) == 1:
            doc = docs[0]
        if doc is None:
            continue
        try:
            lines = load_text_lines(doc['text_path'])
        except (OSError, ValueError):
            continue
        line, estimated = _tmp_chunk_line(lines, getattr(node, 'text', '') or '')
        snippet = str(getattr(node, 'text', '') or '').strip().replace('\n', ' ')
        if len(snippet) > 240:
            snippet = snippet[:237] + '...'
        hits.append({
            'target': doc['target'],
            'file_id': doc.get('file_id'),
            'line': line,
            'snippet': snippet,
            'channels': [channel],
            'line_estimate': estimated,
        })
    return hits, channel


def _tmp_merge_hits(grep_hits: List[dict], semantic_hits: List[dict], top_k: int) -> List[dict]:
    merged: list[dict] = []
    seen: dict[tuple, dict] = {}

    def add(hit: dict, prefer_grep: bool) -> None:
        key = (hit.get('target'), int(hit.get('line') or 0))
        existing = seen.get(key)
        if existing is None:
            seen[key] = hit
            merged.append(hit)
            return
        channels = list(dict.fromkeys(
            list(existing.get('channels') or []) + list(hit.get('channels') or [])
        ))
        existing['channels'] = channels
        patterns = list(dict.fromkeys(
            list(existing.get('patterns') or []) + list(hit.get('patterns') or [])
        ))
        if patterns:
            existing['patterns'] = patterns
        if prefer_grep and 'grep' in (hit.get('channels') or []):
            if hit.get('snippet'):
                existing['snippet'] = hit['snippet']

    for hit in grep_hits:
        add(hit, prefer_grep=True)
    for hit in semantic_hits:
        add(hit, prefer_grep=False)
    return merged[:top_k]


def kb_tmp_search(
    semantic_query: Optional[str] = None,
    grep_patterns: Optional[List[str]] = None,
    top_k: int = 10,
) -> Dict[str, Any]:
    """Locate passages in this conversation's uploaded documents.

    Use for user-uploaded PDFs, Word/PPT, and prose text (txt/md). After hits,
    call read_file_resource on the returned target and line. Do not use for knowledge
    bases, url_fetch web PDFs, workspace drafts, desktop folders, or source
    code — use kb_* tools or grep for those.

    At least one of semantic_query or grep_patterns is required. Refine
    grep_patterns across later calls; parsed text is reused. Each grep pattern
    is a literal substring or regular expression (same rules as grep).

    Args:
        semantic_query: Optional natural-language question over uploaded files.
        grep_patterns: Optional list of search strings (regex or literal).
        top_k: Maximum hits to return (default 10, max 30).
    """
    query = str(semantic_query or '').strip() or None
    patterns = _tmp_pattern_list(grep_patterns)
    if not query and not patterns:
        raise ToolExecutionError('at least one of semantic_query or grep_patterns is required')
    top_k = _tmp_clamp_top_k(top_k)
    uploads, skipped = _tmp_collect_uploads()
    docs: list[dict] = []
    for item in uploads:
        try:
            docs.append(_tmp_prepare_doc(item))
        except TimeoutError:
            skipped.append({'target': item['target'], 'reason': 'parse_timeout'})
            LOG.warning(f'[kb_tmp_search] parse timeout target={item["target"]}')
        except Exception as exc:
            reason = str(exc)[:200]
            skipped.append({'target': item['target'], 'reason': reason})
            LOG.warning(f'[kb_tmp_search] skip target={item["target"]} reason={reason}')

    grep_hits = _tmp_grep_hits(docs, patterns) if patterns else []
    semantic_hits: list[dict] = []
    semantic_channel = None
    if query and docs:
        user_id = str(_tmp_agentic_config().get('user_id') or '')
        try:
            semantic_hits, semantic_channel = _tmp_semantic_hits(docs, query, user_id)
        except Exception as exc:
            skipped.append({'target': '*', 'reason': f'semantic_degraded:{exc}'[:200]})
            semantic_channel = 'degraded'

    hits = _tmp_merge_hits(grep_hits, semantic_hits, top_k)
    LOG.info(
        f'[kb_tmp_search] corpus={len(docs)} skipped={len(skipped)} '
        f'total={len(hits)} semantic={semantic_channel} '
        f'skipped_reasons={[item.get("reason") for item in skipped]}'
    )
    return {
        'semantic_query': query,
        'grep_patterns': patterns,
        'corpus': [
            {
                'target': doc['target'],
                'file_id': doc.get('file_id'),
                'kind': doc.get('kind'),
                'parse': doc.get('parse'),
            }
            for doc in docs
        ],
        'skipped': skipped,
        'channels': {
            'grep': bool(patterns),
            'semantic': semantic_channel,
        },
        'total': len(hits),
        'hits': hits,
        'hint': _TMP_HINT,
        'footer': (
            'No matches.'
            if not hits
            else f'Showing {len(hits)} locating hits. Call read_file_resource for surrounding context.'
        ),
    }
