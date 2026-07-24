from __future__ import annotations

from collections.abc import Mapping
from typing import Any, Callable

from .classify import classify_case
from .cluster import cluster_traces
from .summary import build_analysis_summary
from .trace_summary import build_trace_summary


def analysis_materializers() -> dict[str, Callable[[Any, Mapping[str, object]], Mapping[str, object]]]:
    def trace_summary(ctx: Any, inputs: Mapping[str, object]) -> Mapping[str, object]:
        return {'summary': build_trace_summary(_mapping(inputs['case'], 'case'),
                                               _mapping(inputs['answer'], 'answer'))}

    def classify(ctx: Any, inputs: Mapping[str, object]) -> Mapping[str, object]:
        return {'classification': classify_case(_mapping(inputs['case'], 'case'),
                                                _mapping(inputs['answer'], 'answer'),
                                                _mapping(inputs['judge'], 'judge'),
                                                _mapping(inputs['trace'], 'trace'))}

    def clusters(ctx: Any, inputs: Mapping[str, object]) -> Mapping[str, object]:
        values = inputs.get('classifications')
        if not isinstance(values, tuple):
            raise ValueError('analysis.trace_clusters classifications input must be a partitioned tuple')
        return {'clusters': cluster_traces(values)}

    def summary(ctx: Any, inputs: Mapping[str, object]) -> Mapping[str, object]:
        values = inputs.get('classifications')
        if not isinstance(values, tuple):
            raise ValueError('analysis.summary classifications input must be a partitioned tuple')
        return {'summary': build_analysis_summary(ctx.run_id, values, _mapping(inputs['clusters'], 'clusters'))}

    return {
        'analysis.trace_summary': trace_summary,
        'analysis.classify_case': classify,
        'analysis.trace_clusters': clusters,
        'analysis.summary': summary,
    }


def _mapping(value: object, name: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise ValueError(f'{name} must be a mapping')
    return value
