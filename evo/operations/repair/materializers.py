from __future__ import annotations

import time
import os
from collections.abc import Mapping
from pathlib import Path
from typing import Any, Callable

from evo.operations.analysis.summary import build_analysis_detail

from .loop import build_verified_patch, prepare_candidate_workspace, run_repair_loop
from .plan import build_repair_plan
from .trace import RepairTraceSink, RepairTraceStore


def repair_materializers() -> dict[str, Callable[[Any, Mapping[str, object]], Mapping[str, object]]]:
    def plan(ctx: Any, inputs: Mapping[str, object]) -> Mapping[str, object]:
        classifications = inputs.get('classifications')
        if not isinstance(classifications, tuple):
            raise ValueError('repair.plan classifications input must be a partitioned tuple')
        analysis = build_analysis_detail(classifications, _mapping(inputs['clusters'], 'clusters'))
        return {'plan': build_repair_plan(analysis, _mapping(inputs['policy'], 'policy'))}

    def workspace(ctx: Any, inputs: Mapping[str, object]) -> Mapping[str, object]:
        return {'workspace': prepare_candidate_workspace(_mapping(inputs['plan'], 'plan'),
                                                         _mapping(inputs['policy'], 'policy'))}

    def loop(ctx: Any, inputs: Mapping[str, object]) -> Mapping[str, object]:
        cases, judges = inputs.get('cases'), inputs.get('baseline_judges')
        if not isinstance(cases, tuple):
            raise ValueError('repair.loop_result cases input must be a partitioned tuple')
        if not isinstance(judges, tuple):
            raise ValueError('repair.loop_result baseline_judges input must be a partitioned tuple')
        trace = _trace_sink(ctx)
        return {'result': run_repair_loop(
            _mapping(inputs['workspace'], 'workspace'),
            cases,
            judges,
            _mapping(inputs['eval_policy'], 'eval_policy'),
            _mapping(inputs['candidate_config'], 'candidate_config'),
            _mapping(inputs['policy'], 'policy'),
            ctx,
            _mapping(inputs['plan'], 'plan'),
            trace,
        )}

    def verified(ctx: Any, inputs: Mapping[str, object]) -> Mapping[str, object]:
        trace = _trace_sink(ctx)
        patch = build_verified_patch(ctx.run_id, _mapping(inputs['loop'], 'loop'))
        trace.emit('repair.patch_verified', status='completed' if patch.get('status') == 'verified' else 'skipped',
                   terminal=True, payload={'status': patch.get('status'), 'file_count': len(patch.get('diff') or {})})
        return {'patch': patch}

    return {
        'repair.plan': plan,
        'repair.candidate_workspace': workspace,
        'repair.loop_result': loop,
        'repair.verified_patch': verified,
    }


def _mapping(value: object, name: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise ValueError(f'{name} must be a mapping')
    return value


class _NoopTrace:
    def emit(self, *_args: object, **_kwargs: object) -> None:
        return None

    def cursor(self) -> dict[str, object]:
        return {'status': 'unavailable'}


def _trace_sink(ctx: Any) -> RepairTraceSink | _NoopTrace:
    try:
        thread_id = str(ctx.run_id)
        key = str(getattr(ctx, 'op_id', '') or 'repair.loop_result')
        trace_id = f'{thread_id}:{key}:{int(time.time() * 1000)}'
        return RepairTraceSink(RepairTraceStore(_trace_root()), thread_id, trace_id, key)
    except Exception:
        return _NoopTrace()


def _trace_root() -> Path:
    return Path(os.getenv('LAZYMIND_EVO_BASE_DIR') or '/var/lib/lazymind/evo')
