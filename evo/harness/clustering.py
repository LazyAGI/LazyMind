from __future__ import annotations
from typing import Any, Callable
import numpy as np
from sklearn.decomposition import PCA
from sklearn.preprocessing import RobustScaler
from evo.domain.clustering import (
    ClusterSummary,
    ClusteringResult,
    FlowAnalysisResult,
    PerStepClusteringResult,
    PerStepSummary,
    StepTransition,
)
from evo.domain.step_features import build_step_matrix, flatten_case_features
from evo.domain.tool_result import ErrorCode, ToolResult
from evo.runtime.session import get_current_session


def _run_kmeans(X: np.ndarray) -> np.ndarray:
    from sklearn.cluster import KMeans
    from sklearn.metrics import silhouette_score

    n = X.shape[0]
    best_k, best_sc = (2, -1.0)
    for k in range(2, min(11, n)):
        km = KMeans(n_clusters=k, n_init='auto', random_state=42).fit(X)
        sc = silhouette_score(X, km.labels_)
        if sc > best_sc:
            best_k, best_sc = (k, sc)
    return KMeans(n_clusters=best_k, n_init='auto', random_state=42).fit_predict(X)


def _run_clustering(mat: np.ndarray, method: str, min_cluster_size: int | None) -> np.ndarray:
    n = mat.shape[0]
    if n < 4:
        return np.zeros(n, dtype=int)
    X = RobustScaler().fit_transform(mat)
    if X.shape[1] > 30:
        X = PCA(n_components=min(30, n)).fit_transform(X)
    if method == 'hdbscan':
        try:
            from hdbscan import HDBSCAN

            mcs = min_cluster_size or max(2, min(5, n // 2))
            labels = HDBSCAN(min_cluster_size=mcs).fit_predict(X)
            if (labels >= 0).sum() > 0:
                return labels
        except ImportError:
            pass
    return _run_kmeans(X)


def _build_cluster_summaries(
    ids: list[str], keys: list[str], mat: np.ndarray, labels: np.ndarray, score_lookup: Callable[[str], float | None]
) -> list[ClusterSummary]:
    global_mean = mat.mean(axis=0)
    global_std = mat.std(axis=0)
    global_std[global_std == 0] = 1.0
    out: list[ClusterSummary] = []
    for lab in sorted(set(labels)):
        mask = labels == lab
        member_ids = [ids[i] for i in range(len(ids)) if mask[i]]
        sub = mat[mask]
        scores: list[float] = []
        for did in member_ids:
            val = score_lookup(did)
            if val is not None:
                scores.append(float(val))
        deltas = (sub.mean(axis=0) - global_mean) / global_std
        top_idx = np.argsort(np.abs(deltas))[::-1][:10]
        centroid = sub.mean(axis=0)
        dists = np.linalg.norm(sub - centroid, axis=1)
        exemplars = [member_ids[i] for i in np.argsort(dists)[: min(5, len(member_ids))]]
        step_deltas: dict[str, dict[str, float]] = {}
        for i in top_idx:
            parts = keys[i].split(':', 1)
            step = parts[0] if len(parts) == 2 else '_global'
            metric = parts[1] if len(parts) == 2 else keys[i]
            step_deltas.setdefault(step, {})[metric] = round(float(deltas[i]), 3)
        out.append(
            ClusterSummary(
                cluster_id=f'cluster_{lab}' if lab >= 0 else 'noise',
                size=int(mask.sum()),
                score_stats={
                    'mean': round(float(np.mean(scores)), 4) if scores else None,
                    'min': round(float(min(scores)), 4) if scores else None,
                    'max': round(float(max(scores)), 4) if scores else None,
                },
                top_feature_deltas={keys[i]: round(float(deltas[i]), 3) for i in top_idx},
                step_grouped_deltas=step_deltas,
                exemplar_case_ids=exemplars,
            )
        )
    return out


def cluster_badcases(
    score_field: str = 'answer_correctness',
    order: str = 'asc',
    limit: int = 500,
    method: str = '',
    min_cluster_size: int = 0,
) -> ToolResult[ClusteringResult]:
    if order.lower() not in ('asc', 'desc'):
        return ToolResult.failure('cluster_badcases', ErrorCode.INVALID_ARGUMENT, "order must be 'asc' or 'desc'")
    if limit < 1:
        return ToolResult.failure('cluster_badcases', ErrorCode.INVALID_ARGUMENT, 'limit must be >= 1')
    session = get_current_session()
    if session is None or not session.parsed_judge:
        return ToolResult.failure('cluster_badcases', ErrorCode.DATA_NOT_LOADED, 'No data.')
    effective_method = method or session.config.cluster_method
    mcs = min_cluster_size or session.config.cluster_min_size
    ranked: list[tuple[str, float]] = []
    for did, j in session.iter_judge():
        val = getattr(j, score_field, None)
        if val is not None:
            ranked.append((did, float(val)))
    ranked.sort(key=lambda r: r[1], reverse=order.lower() != 'asc')
    target = {r[0] for r in ranked[:limit]}
    ids: list[str] = []
    frows: list[dict[str, float]] = []
    for cid, sf in session.case_step_features.items():
        flat = flatten_case_features(sf)
        if flat:
            ids.append(cid)
            frows.append(flat)
    if not frows:
        for did, judge in session.iter_judge():
            ids.append(did)
            key_count = max(1, len(judge.key))
            frows.append(
                {
                    'judge:answer_correctness': judge.answer_correctness,
                    'judge:context_recall': judge.context_recall,
                    'judge:doc_recall': judge.doc_recall,
                    'judge:faithfulness': judge.faithfulness,
                    'judge:key_hit_rate': len(judge.hit_key) / key_count,
                    'judge:retrieved_contexts': float(len(judge.retrieved_text)),
                    'judge:retrieved_docs': float(len(judge.retrieved_file)),
                }
            )
    all_keys = sorted({k for r in frows for k in r})
    mat = np.array([[r.get(k, 0.0) for k in all_keys] for r in frows], dtype=np.float64)
    np.nan_to_num(mat, copy=False)
    keep = [i for (i, cid) in enumerate(ids) if cid in target]
    if not keep:
        return ToolResult.failure(
            'cluster_badcases', ErrorCode.INTERNAL_ERROR, 'No features matched the ranked target set.'
        )
    ids = [ids[i] for i in keep]
    mat = mat[keep]
    labels = _run_clustering(mat, effective_method, mcs)
    summaries = _build_cluster_summaries(ids, all_keys, mat, labels, session.score_lookup(score_field))
    result = ClusteringResult(
        method=effective_method,
        n_cases=len(ids),
        n_clusters=len([c for c in set(labels) if c >= 0]),
        noise_count=int((labels == -1).sum()) if -1 in labels else 0,
        cluster_summaries=summaries,
    )
    return ToolResult.success('cluster_badcases', result)


def cluster_per_step(
    score_field: str = 'answer_correctness', limit: int = 500, method: str = '', min_cluster_size: int = 0
) -> ToolResult[PerStepClusteringResult]:
    if limit < 1:
        return ToolResult.failure('cluster_per_step', ErrorCode.INVALID_ARGUMENT, 'limit must be >= 1')
    session = get_current_session()
    if session is None or not session.case_step_features:
        return ToolResult.failure('cluster_per_step', ErrorCode.DATA_NOT_LOADED, 'Step features not computed yet.')
    effective_method = method or session.config.cluster_method
    mcs = min_cluster_size or session.config.cluster_min_size
    pipeline = session.trace_meta.pipeline
    ranked: list[tuple[str, float]] = []
    for did, j in session.iter_judge():
        val = getattr(j, score_field, None)
        if val is not None:
            ranked.append((did, float(val)))
    ranked.sort(key=lambda r: r[1])
    target_ids = {r[0] for r in ranked[:limit]}
    score_lookup = session.score_lookup(score_field)
    per_step: dict[str, PerStepSummary] = {}
    for step_key in pipeline:
        ids, keys, mat = build_step_matrix(session.case_step_features, step_key, target_ids)
        if len(ids) < 4 or mat.shape[1] < 2:
            per_step[step_key] = PerStepSummary(n_cases=len(ids), skipped=True)
            continue
        labels = _run_clustering(mat, effective_method, mcs)
        summaries = _build_cluster_summaries(ids, keys, mat, labels, score_lookup)
        per_step[step_key] = PerStepSummary(
            n_cases=len(ids),
            n_clusters=len([c for c in set(labels) if c >= 0]),
            cluster_summaries=summaries,
            labels={cid: int(lab) for (cid, lab) in zip(ids, labels)},
        )
    return ToolResult.success('cluster_per_step', PerStepClusteringResult(pipeline=list(pipeline), per_step=per_step))


def _entropy(labels: np.ndarray) -> float:
    _, counts = np.unique(labels, return_counts=True)
    p = counts / counts.sum()
    return float(-np.sum(p * np.log2(p + 1e-12)))


def _nmi(a: np.ndarray, b: np.ndarray) -> float:
    from sklearn.metrics import normalized_mutual_info_score

    return float(normalized_mutual_info_score(a, b))


def _edges_from_skeleton(skeleton: list[dict[str, Any]]) -> list[tuple[str, str]]:
    edges: list[tuple[str, str]] = []
    prev_terminals: list[str] = []
    for node in skeleton:
        if node.get('type') == 'flow':
            if node.get('name') == 'Parallel':
                branch_terminals: list[str] = []
                for br in node.get('branches', []) or []:
                    steps = br.get('steps') or []
                    if not steps:
                        continue
                    for p in prev_terminals:
                        edges.append((p, steps[0]))
                    for a, b in zip(steps, steps[1:]):
                        edges.append((a, b))
                    branch_terminals.append(steps[-1])
                if branch_terminals:
                    prev_terminals = branch_terminals
            continue
        key = node.get('key')
        if not key:
            continue
        for p in prev_terminals:
            edges.append((p, key))
        prev_terminals = [key]
    return edges


def _edges_from_skeletons(skeletons: dict[str, list[dict[str, Any]]]) -> list[tuple[str, str]]:
    seen: set[tuple[str, str]] = set()
    out: list[tuple[str, str]] = []
    for skeleton in skeletons.values():
        for edge in _edges_from_skeleton(skeleton):
            if edge not in seen:
                seen.add(edge)
                out.append(edge)
    return out


def _classify_transition(delta_h: float, nmi_val: float) -> str:
    if delta_h < -0.2:
        return 'convergence'
    if delta_h > 0.2 and nmi_val < 0.5:
        return 'divergence'
    if nmi_val > 0.7 and abs(delta_h) < 0.1:
        return 'stable'
    return 'shift'


def analyze_step_flow() -> ToolResult[FlowAnalysisResult]:
    session = get_current_session()
    if session is None:
        return ToolResult.failure('analyze_step_flow', ErrorCode.DATA_NOT_LOADED, 'No session.')
    per = session.clustering_per_step
    if per is None:
        return ToolResult.failure('analyze_step_flow', ErrorCode.DATA_NOT_LOADED, 'Run cluster_per_step first.')
    all_cids = sorted({cid for s in per.per_step.values() if s.labels for cid in s.labels})
    step_labels: dict[str, np.ndarray] = {}
    for sk, info in per.per_step.items():
        if info.labels:
            step_labels[sk] = np.array([info.labels.get(c, -1) for c in all_cids])
    edges = _edges_from_skeletons(session.trace_meta.flow_skeletons) or _edges_from_skeleton(
        session.trace_meta.flow_skeleton
    )
    transitions: list[StepTransition] = []
    for s_a, s_b in edges:
        la = step_labels.get(s_a)
        lb = step_labels.get(s_b)
        if la is None or lb is None:
            continue
        h_a, h_b = (_entropy(la), _entropy(lb))
        delta_h = h_b - h_a
        nmi_val = _nmi(la, lb)
        unique_a, unique_b = (sorted(set(la)), sorted(set(lb)))
        a_idx = {v: i for (i, v) in enumerate(unique_a)}
        b_idx = {v: i for (i, v) in enumerate(unique_b)}
        tmat = np.zeros((len(unique_a), len(unique_b)), dtype=int)
        for ca, cb in zip(la, lb):
            tmat[a_idx[ca], b_idx[cb]] += 1
        transitions.append(
            StepTransition(
                from_step=s_a,
                to_step=s_b,
                entropy_from=round(h_a, 3),
                entropy_to=round(h_b, 3),
                entropy_change=round(delta_h, 3),
                nmi=round(nmi_val, 3),
                type=_classify_transition(delta_h, nmi_val),
                transition_matrix=tmat.tolist(),
                from_clusters=[f'cluster_{c}' for c in unique_a],
                to_clusters=[f'cluster_{c}' for c in unique_b],
            )
        )
    critical = sorted({t.to_step for t in transitions if t.type in ('divergence', 'convergence')})
    case_flow: dict[str, dict[str, str]] = {}
    for cid in all_cids[:50]:
        flow: dict[str, str] = {}
        for sk, info in per.per_step.items():
            lab = info.labels.get(cid)
            if lab is None:
                continue
            flow[sk] = f'cluster_{lab}' if lab >= 0 else 'noise'
        case_flow[cid] = flow
    return ToolResult.success(
        'analyze_step_flow',
        FlowAnalysisResult(transition_analysis=transitions, critical_steps=critical, case_label_flow=case_flow),
    )
