from __future__ import annotations
import re
from collections import Counter
from typing import Any, Callable
import numpy as np
from scipy.stats import entropy as scipy_entropy, kendalltau
from sklearn.feature_extraction.text import CountVectorizer
from sklearn.metrics import jaccard_score
from evo.domain.models import JudgeRecord, TraceRecord, ModuleOutput
from evo.domain.node import NodeResolver

EmbedFn = Callable[[str], 'np.ndarray | None']
_RERANKER_DROP_RATIO = 0.8
_GEN_DRIFT_OVERLAP = 0.3
_GEN_DRIFT_LENGTH = 0.5
_TOKEN_RE = re.compile('\\w+')
_TEXT_KEYS = ('context_str', 'text', 'content', 'query', 'snippet', 'title', 'reason', 'error', 'output')


def _tokenize(text: str) -> set[str]:
    return set(_TOKEN_RE.findall(text.lower())) if text else set()


def _embed(text: str, embed_fn: EmbedFn | None) -> np.ndarray | None:
    if not text or embed_fn is None:
        return None
    return embed_fn(text)


def _cosine(a: np.ndarray | None, b: np.ndarray | None) -> float | None:
    if a is None or b is None or a.size == 0 or (b.size == 0):
        return None
    na = float(np.linalg.norm(a))
    nb = float(np.linalg.norm(b))
    if na == 0.0 or nb == 0.0:
        return None
    return float(np.dot(a, b) / (na * nb))


def _get_args(inp: Any) -> list[Any]:
    if isinstance(inp, dict):
        return inp.get('args', [])
    if isinstance(inp, list):
        return inp
    return [inp] if inp else []


def _extract_text(data: Any) -> str:
    if isinstance(data, str):
        return data
    if isinstance(data, dict):
        parts: list[str] = []
        for key in _TEXT_KEYS:
            v = data.get(key)
            if isinstance(v, str) and v:
                parts.append(v)
        return '\n'.join(parts)
    if isinstance(data, list) and data and isinstance(data[0], (str, dict)):
        return '\n'.join((text for item in data[:8] if (text := _extract_text(item))))
    return str(data) if data else ''


def _text_len(val: Any) -> int:
    if isinstance(val, str):
        return len(val)
    if isinstance(val, dict):
        return sum((_text_len(v) for v in val.values()))
    if isinstance(val, list):
        return sum((_text_len(v) for v in val))
    return 0


def _text_jaccard(text_a: str, text_b: str) -> float:
    if not text_a.strip() or not text_b.strip():
        return 0.0
    vec = CountVectorizer(binary=True, lowercase=True)
    try:
        X = vec.fit_transform([text_a, text_b])
    except ValueError:
        return 0.0
    return float(jaccard_score(X[0].toarray().ravel(), X[1].toarray().ravel(), average='binary', zero_division=0.0))


def _is_text(data: Any) -> bool:
    if isinstance(data, str) and len(data) > 10:
        return True
    if isinstance(data, dict):
        return any((isinstance(v, str) and len(v) > 20 for v in data.values()))
    return False


def _looks_like_node_id(value: str) -> bool:
    s = value.strip()
    if not s or any((ch.isspace() for ch in s)):
        return False
    if s.startswith(('doc_', 'chunk_', 'node_', 'seg_', 'segment_', 'uid_')):
        return True
    return len(s) >= 24 and all((ch.isalnum() or ch in '_-' for ch in s))


def _collect_ids_docids(
    items: Any, resolver: NodeResolver
) -> tuple[list[str], list[str | None], list[str | None], list[int | None], list[str | None]]:
    if not isinstance(items, list):
        return ([], [], [], [], [])
    chunks: list[str] = []
    docs: list[str | None] = []
    files: list[str | None] = []
    pages: list[int | None] = []
    texts: list[str | None] = []
    for item in items:
        if isinstance(item, str):
            if not _looks_like_node_id(item):
                continue
            cid = item
            did = page = text = file_name = None
        elif isinstance(item, dict):
            cid = (
                item.get('id')
                or item.get('uid')
                or item.get('chunk_id')
                or item.get('node_id')
                or item.get('segment_id')
                or item.get('segement_id')
            )
            did = item.get('docid') or item.get('doc_id') or item.get('document_id')
            page = item.get('page')
            text = item.get('text') or item.get('content')
            file_name = item.get('file_name') or item.get('filename')
        else:
            continue
        if not cid:
            continue
        if did is None or text is None or file_name is None:
            info = resolver(str(cid)) or {}
            did = did if did is not None else info.get('docid')
            text = text if text is not None else info.get('text')
            file_name = file_name if file_name is not None else info.get('file_name')
            page = page if page is not None else info.get('page')
        chunks.append(str(cid))
        docs.append(str(did) if did else None)
        files.append(str(file_name) if file_name else None)
        pages.append(int(page) if isinstance(page, (int, float)) else None)
        texts.append(str(text) if text else None)
    return (chunks, docs, files, pages, texts)


def _score_features(scores: np.ndarray) -> dict[str, float]:
    if not scores.size:
        return {}
    f = {
        'score_mean': float(np.mean(scores)),
        'score_max': float(np.max(scores)),
        'score_min': float(np.min(scores)),
        'score_active_ratio': float((scores > 1e-06).mean()),
    }
    if scores.size >= 2:
        sorted_s = np.sort(scores)
        f['score_std'] = float(np.std(scores))
        f['top1_margin'] = float(sorted_s[-1] - sorted_s[-2])
        f['score_gap'] = float(sorted_s[-1] - sorted_s[0])
        if sorted_s[-1] != 0:
            f['top1_dominance'] = float((sorted_s[-1] - sorted_s[-2]) / abs(sorted_s[-1]))
        pos = np.clip(scores, 0.0, None)
        total = float(pos.sum())
        if total > 0:
            f['score_entropy'] = float(scipy_entropy(pos / total))
    return f


def _score_gt_features(scores: np.ndarray, ids: list[str], gt: set[str]) -> dict[str, float]:
    if not (gt and scores.size == len(ids) and scores.size):
        return {}
    out = np.array(ids, dtype=object)
    hit = np.isin(out, np.array(list(gt), dtype=object))
    if not hit.any():
        return {}
    gt_scores = scores[hit]
    f = {'score_at_first_gt': float(scores[int(np.argmax(hit))]), 'score_gt_mean': float(gt_scores.mean())}
    if (~hit).any():
        non_gt = scores[~hit]
        f['score_non_gt_mean'] = float(non_gt.mean())
        f['score_gt_vs_non_gt'] = f['score_gt_mean'] - f['score_non_gt_mean']
    return f


def _shape_features(in_ids: list[str], out_ids: list[str]) -> dict[str, float]:
    f: dict[str, float] = {'retrieval_count': float(len(out_ids))}
    if not in_ids:
        return f
    f['input_count'] = float(len(in_ids))
    f['output_count'] = float(len(out_ids))
    f['selectivity'] = float(len(out_ids) / len(in_ids))
    if len(in_ids) >= 2 and len(out_ids) >= 2:
        rank_of = {d: r for (r, d) in enumerate(in_ids)}
        mapped = np.fromiter((rank_of[d] for d in out_ids if d in rank_of), dtype=np.int64)
        if mapped.size >= 2:
            tau, _ = kendalltau(np.arange(mapped.size), mapped)
            if np.isfinite(tau):
                f['rank_correlation'] = float(tau)
                f['rank_correlation_coverage'] = float(mapped.size / len(out_ids))
    return f


def _id_output_features(prefix: str, ids: list[str], gt: set[str]) -> dict[str, float]:
    if not ids:
        return {}
    out = np.array(ids, dtype=object)
    unique = np.unique(out)
    f = {
        f'{prefix}_unique_count': float(unique.size),
        f'{prefix}_duplication_rate': float(1.0 - unique.size / out.size),
    }
    if not gt:
        return f
    gt_arr = np.array(list(gt), dtype=object)
    hit = np.isin(out, gt_arr)
    f[f'{prefix}_recall_at_k'] = float(np.isin(unique, gt_arr).sum() / len(gt))
    f[f'{prefix}_precision_at_k'] = float(hit.mean())
    f[f'{prefix}_unique_precision'] = float(np.isin(unique, gt_arr).sum() / unique.size)
    for n in (1, 3, 5):
        if out.size >= n:
            f[f'{prefix}_hit_at_{n}'] = float(hit[:n].any())
    first = int(np.argmax(hit)) if hit.any() else -1
    f[f'{prefix}_mrr'] = float(1.0 / (first + 1)) if first >= 0 else 0.0
    return f


def _id_filtering_features(prefix: str, in_ids: list[str], out_ids: list[str], gt: set[str]) -> dict[str, float]:
    if not (in_ids and out_ids and gt):
        return {}
    gt_arr = np.array(list(gt), dtype=object)
    in_unique = np.unique(np.array(in_ids, dtype=object))
    out_unique = np.unique(np.array(out_ids, dtype=object))
    in_hits = set(in_unique[np.isin(in_unique, gt_arr)].tolist())
    out_hits = set(out_unique[np.isin(out_unique, gt_arr)].tolist())
    denom = len(gt)
    f = {f'{prefix}_input_recall': float(len(in_hits) / denom), f'{prefix}_output_recall': float(len(out_hits) / denom)}
    f[f'{prefix}_recall_delta'] = f[f'{prefix}_output_recall'] - f[f'{prefix}_input_recall']
    f[f'{prefix}_gt_dropped'] = float(len(in_hits - out_hits))
    f[f'{prefix}_gt_survival_rate'] = float(len(in_hits & out_hits) / len(in_hits)) if in_hits else 1.0
    return f


def _diversity_features(prefix: str, pages: list[int | None]) -> dict[str, float]:
    ps = [p for p in pages if p is not None]
    if not ps:
        return {}
    return {f'{prefix}_page_diversity': float(len(set(ps)) / len(ps))}


def _failure_tags(f: dict[str, float]) -> dict[str, float]:
    tags: dict[str, float] = {}
    doc_r = f.get('doc_recall_at_k')
    hit_1 = f.get('chunk_hit_at_1')
    in_r = f.get('chunk_input_recall')
    out_r = f.get('chunk_output_recall')
    gt_ovl = f.get('answer_gt_overlap')
    ans_len = f.get('answer_length_ratio')
    if doc_r is not None:
        tags['tag_retrieval_miss'] = 1.0 if doc_r == 0.0 else 0.0
        if hit_1 is not None:
            tags['tag_ranking_fail'] = 1.0 if doc_r > 0 and hit_1 == 0.0 else 0.0
    if in_r is not None and out_r is not None:
        tags['tag_reranker_drop'] = 1.0 if in_r > 0 and out_r < _RERANKER_DROP_RATIO * in_r else 0.0
    if gt_ovl is not None and ans_len is not None:
        tags['tag_generation_drift'] = 1.0 if gt_ovl < _GEN_DRIFT_OVERLAP and ans_len < _GEN_DRIFT_LENGTH else 0.0
    return tags


def _resolve_chunks_text(items: Any, resolver: NodeResolver) -> str:
    if not isinstance(items, list) or not items:
        return ''
    _, _, _, _, texts = _collect_ids_docids(items, resolver)
    return '\n'.join((t for t in texts if t))


def _text_output_features(
    mod: ModuleOutput, judge: JudgeRecord, query: str, resolver: NodeResolver, embed_fn: EmbedFn | None
) -> dict[str, float]:
    args = _get_args(mod.input)
    out_text = _extract_text(mod.output) or _resolve_chunks_text(
        mod.output if isinstance(mod.output, list) else [], resolver
    )
    in_text = _extract_text(args) or _resolve_chunks_text(args, resolver)
    f: dict[str, float] = {'output_text_len': float(len(out_text)), 'input_context_len': float(len(in_text))}
    if len(in_text) >= 10:
        f['answer_context_ratio'] = len(out_text) / len(in_text)
    if judge.gt_answer:
        gt_len = len(judge.gt_answer)
        f['answer_length_ratio'] = float(len(out_text) / gt_len) if gt_len else 0.0
        f['answer_gt_overlap'] = _text_jaccard(out_text, judge.gt_answer)
    if in_text:
        f['context_utilization'] = _text_jaccard(out_text, in_text)
    if query and out_text:
        q_tokens = _tokenize(query)
        if q_tokens:
            f['answer_query_coverage'] = len(q_tokens & _tokenize(out_text)) / len(q_tokens)
    if out_text:
        out_emb = _embed(out_text, embed_fn)
        if judge.gt_answer:
            sim = _cosine(out_emb, _embed(judge.gt_answer, embed_fn))
            if sim is not None:
                f['answer_gt_semantic'] = sim
        if in_text:
            sim = _cosine(out_emb, _embed(in_text, embed_fn))
            if sim is not None:
                f['context_semantic_utilization'] = sim
        if query:
            sim = _cosine(out_emb, _embed(query, embed_fn))
            if sim is not None:
                f['query_answer_semantic'] = sim
    if out_text:
        char_freq = np.array(list(Counter(out_text).values()), dtype=np.float64)
        f['answer_entropy'] = float(scipy_entropy(char_freq / char_freq.sum()))
    if len(out_text) >= 3:
        trigrams = [out_text[i: i + 3] for i in range(len(out_text) - 2)]
        tri_counts = Counter(trigrams)
        repeated = sum((c - 1 for c in tri_counts.values() if c > 1))
        f['repetition_rate'] = float(repeated / len(trigrams))
    return f


def features_for_step(
    mod: ModuleOutput, judge: JudgeRecord, resolver: NodeResolver, query: str = '', embed_fn: EmbedFn | None = None
) -> dict[str, float]:
    args = _get_args(mod.input)
    in_items = args if isinstance(args, list) else []
    out_items = mod.output if isinstance(mod.output, list) else []
    in_chunks, in_docs, in_files, _, _ = _collect_ids_docids(in_items, resolver)
    out_chunks, out_docs, out_files, out_pages, out_texts = _collect_ids_docids(out_items, resolver)
    f: dict[str, float] = {
        'input_text_len': float(_text_len(mod.input)),
        'output_text_len': float(_text_len(mod.output)),
    }
    if query:
        f['query_text_len'] = float(len(query))
        f['query_token_count'] = float(len(_tokenize(query)))
    if out_chunks:
        scores = np.array(mod.scores, dtype=np.float64) if mod.scores else np.empty(0)
        gt_chunks = set(judge.gt_chunk_id or [])
        gt_docs = set(judge.gt_docid or judge.gt_file or [])
        in_docs_clean = [d for d in in_docs if d]
        out_docs_clean = [d for d in out_docs if d]
        if not judge.gt_docid and judge.gt_file:
            in_docs_clean = [d for d in in_files if d]
            out_docs_clean = [d for d in out_files if d]
        f.update(_shape_features(in_chunks, out_chunks))
        f.update(_score_features(scores))
        f.update(_score_gt_features(scores, out_chunks, gt_chunks))
        f.update(_id_output_features('chunk', out_chunks, gt_chunks))
        f.update(_id_output_features('doc', out_docs_clean, gt_docs))
        f.update(_diversity_features('chunk', out_pages))
        if in_chunks:
            f.update(_id_filtering_features('chunk', in_chunks, out_chunks, gt_chunks))
            f.update(_id_filtering_features('doc', in_docs_clean, out_docs_clean, gt_docs))
    if _extract_text(mod.output) or any(out_texts):
        f.update(_text_output_features(mod, judge, query, resolver, embed_fn))
    f.update(_failure_tags(f))
    return {k: round(v, 6) for (k, v) in f.items()}


def build_case_step_features(
    judge: JudgeRecord, trace: TraceRecord, pipeline: list[str], resolver: NodeResolver, embed_fn: EmbedFn | None = None
) -> dict[str, dict[str, float]]:
    result: dict[str, dict[str, float]] = {}
    for key in pipeline:
        mod = trace.modules.get(key)
        if mod is None:
            continue
        result[key] = features_for_step(mod, judge, resolver, query=trace.query, embed_fn=embed_fn)
    return result


def flatten_case_features(step_feats: dict[str, dict[str, float]]) -> dict[str, float]:
    flat: dict[str, float] = {}
    for step_key, metrics in step_feats.items():
        for metric, val in metrics.items():
            flat[f'{step_key}:{metric}'] = val
    return flat


def build_step_matrix(
    all_case_feats: dict[str, dict[str, dict[str, float]]], step_key: str, target_ids: set[str] | None = None
) -> tuple[list[str], list[str], np.ndarray]:
    ids, rows = ([], [])
    for cid, sf in all_case_feats.items():
        if target_ids and cid not in target_ids:
            continue
        step_feats = sf.get(step_key, {})
        if step_feats:
            ids.append(cid)
            rows.append(step_feats)
    if not rows:
        return ([], [], np.empty((0, 0)))
    all_keys = sorted({k for r in rows for k in r})
    mat = np.array([[r.get(k, 0.0) for k in all_keys] for r in rows], dtype=np.float64)
    np.nan_to_num(mat, copy=False)
    std = mat.std(axis=0)
    keep = std > 1e-09
    if keep.sum() < 2:
        return ([], [], np.empty((0, 0)))
    return (ids, [k for (k, m) in zip(all_keys, keep) if m], mat[:, keep])


def aggregate_global_step_analysis(
    all_case_feats: dict[str, dict[str, dict[str, float]]], all_judge: dict[str, JudgeRecord], pipeline: list[str]
) -> dict[str, Any]:
    result: dict[str, Any] = {}
    case_ids = list(all_case_feats.keys())
    if not case_ids:
        return result
    correctness = np.array(
        [all_judge[cid].answer_correctness for cid in case_ids if cid in all_judge], dtype=np.float64
    )
    for step_key in pipeline:
        step_vectors: dict[str, list[float]] = {}
        for cid in case_ids:
            sf = all_case_feats[cid].get(step_key, {})
            for metric, val in sf.items():
                step_vectors.setdefault(metric, []).append(val)
        if not step_vectors:
            result[step_key] = {'n_cases': 0}
            continue
        stats: dict[str, Any] = {}
        for metric, vals in step_vectors.items():
            arr = np.array(vals, dtype=np.float64)
            stats[metric] = {
                'mean': round(float(arr.mean()), 4),
                'std': round(float(arr.std()), 4),
                'min': round(float(arr.min()), 4),
                'max': round(float(arr.max()), 4),
            }
            if arr.size >= 3 and correctness.size == arr.size and (np.std(arr) > 0):
                rho = float(np.corrcoef(arr, correctness)[0, 1])
                if np.isfinite(rho):
                    stats[metric]['corr_correctness'] = round(rho, 4)
        result[step_key] = {'n_cases': len(case_ids), 'stats': stats}
    return result
