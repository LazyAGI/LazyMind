from __future__ import annotations

from collections.abc import Mapping
from typing import Any, Callable

from evo.operations.eval.judge import judge_case
from evo.operations.public_contracts import build_eval_summary_root

from .candidate import candidate_rag_answer, candidate_service, finalize_candidate
from .comparison import compare_abtest


def abtest_materializers() -> dict[str, Callable[[Any, Mapping[str, object]], Mapping[str, object]]]:
    def service(ctx: Any, inputs: Mapping[str, object]) -> Mapping[str, object]:
        return {'service': candidate_service(
            _mapping(inputs['config'], 'config'),
            _mapping(inputs['patch'], 'patch'),
            ctx,
            _mapping(inputs['workspace'], 'workspace'),
        )}

    def answer(ctx: Any, inputs: Mapping[str, object]) -> Mapping[str, object]:
        return {'answer': candidate_rag_answer(
            _mapping(inputs['case'], 'case'),
            _mapping(inputs['service'], 'service'),
        )}

    def judge(ctx: Any, inputs: Mapping[str, object]) -> Mapping[str, object]:
        return {'judge': judge_case(
            _mapping(inputs['case'], 'case'),
            _mapping(inputs['answer'], 'answer'),
            _mapping(inputs.get('policy') or {}, 'policy'),
        )}

    def summary(ctx: Any, inputs: Mapping[str, object]) -> Mapping[str, object]:
        judges = inputs.get('judges')
        if not isinstance(judges, tuple):
            raise ValueError('abtest.candidate_eval_summary judges input must be a partitioned tuple')
        return {'summary': build_eval_summary_root(ctx.run_id, judges)}

    def compare(ctx: Any, inputs: Mapping[str, object]) -> Mapping[str, object]:
        comparison = compare_abtest(
            ctx.run_id,
            _mapping(inputs['baseline'], 'baseline'),
            _mapping(inputs['candidate'], 'candidate'),
            _mapping(inputs['service'], 'service'),
        )
        finalize_candidate(_mapping(inputs['service'], 'service'), comparison)
        return {'comparison': comparison}

    return {
        'abtest.candidate_service': service,
        'abtest.candidate_rag_answer': answer,
        'abtest.candidate_judge': judge,
        'abtest.candidate_eval_summary': summary,
        'abtest.compare': compare,
    }


def _mapping(value: object, name: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise ValueError(f'{name} must be a mapping')
    return value
