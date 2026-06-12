from __future__ import annotations

from dataclasses import replace
from typing import Any

from ..artifacts import ArtifactRef
from ..internal_ids import is_synthetic_operation, latest_failed_stage_from_events, stage_group
from ..operations import OperationGraph
from ..store import EvoStore
from .models import AtomicIntent, IntentRequest, ValidationIssue

STAGES = {'dataset', 'eval', 'analysis', 'repair', 'abtest'}

_STAGE_ALIASES = {
    'dataset': ('dataset', '数据集', '样本', 'step1', '第一步'),
    'eval': ('eval', '评测', '评价', '测试', 'step2', '第二步'),
    'analysis': ('analysis', '分析', '归因', '分类', 'step3', '第三步'),
    'repair': ('repair', '修复', '改代码', 'opencode', 'step4', '第四步'),
    'abtest': ('abtest', 'ab test', 'abtest', '对比', '切流', 'step5', '第五步'),
}

_FAILURE_CUES = ('失败', '为什么', '原因', '报错', 'error', 'failed', 'failure', 'timeout', '403')


class EvoTargetResolver:
    """Bind business-level intent hints to evo runtime targets.

    The parser may choose capabilities and emit semantic hints, but users should not
    provide graph ids. This resolver is the single place that turns those hints into
    operation/stage/run targets before harness validation and compilation.
    """

    def __init__(self, *, store: EvoStore, run_id: str, graph: OperationGraph):
        self.store = store
        self.run_id = run_id
        self.graph = graph

    def resolve(self, request: IntentRequest, intents: list[AtomicIntent],
                allowed: dict[str, dict]) -> tuple[list[AtomicIntent], list[ValidationIssue]]:
        out: list[AtomicIntent] = []
        issues: list[ValidationIssue] = []
        for intent in intents:
            resolved = self._resolve_one(request, intent, allowed)
            if resolved is None:
                issues.append(ValidationIssue(
                    'control_target_unavailable', intent.intent_id, 'clarify',
                    '当前运行状态里没有可执行该控制的目标，请用业务语言说明要控制整个流程还是当前步骤。',
                ))
            else:
                out.append(resolved)
        return out, issues

    def _resolve_one(self, request: IntentRequest, intent: AtomicIntent,
                     allowed: dict[str, dict]) -> AtomicIntent | None:
        capability_id = str(intent.target.get('capability_id') or intent.action or '')
        intent = self._bind_latest_artifact_ref(intent)
        if capability_id == 'read_run_status_query' and self._asks_for_failure(request.message):
            capability_id = 'explain_run_failure_query'
            intent = self._retarget(intent, capability_id, kind='query')

        if capability_id == 'read_operation_query' and not self._operation_ref(intent):
            capability_id = 'explain_run_failure_query' if self._asks_for_failure(request.message) \
                else 'read_run_status_query'
            intent = self._retarget(intent, capability_id, kind='query')

        if capability_id == 'explain_run_failure_query':
            return self._bind_failure_query(request, intent)

        if capability_id == 'read_run_status_query':
            return replace(intent, target={**intent.target, 'run_id': intent.target.get('run_id') or self.run_id})

        if capability_id == 'retry_operation' and not self._operation_ref(intent):
            stage = self._stage_hint(request.message, intent) or self._latest_failed_stage() or self._current_stage()
            if stage and 'retry_stage' in allowed:
                return self._retarget(self._with_stage(intent, stage), 'retry_stage', kind='flow_control')
            if 'retry_thread' in allowed:
                return self._retarget(intent, 'retry_thread', kind='flow_control')
            return None

        if capability_id == 'retry_thread':
            stage = self._stage_hint(request.message, intent)
            if stage and 'retry_stage' in allowed:
                return self._retarget(self._with_stage(intent, stage), 'retry_stage', kind='flow_control')
            return intent

        if capability_id == 'retry_stage':
            stage = self._stage_hint(request.message, intent) or self._latest_failed_stage() or self._current_stage()
            return self._with_stage(intent, stage) if stage else None

        if capability_id in {'ensure_stage', 'restart_from_stage'}:
            stage = self._stage_hint(request.message, intent) or self._current_stage()
            return self._with_stage(intent, stage) if stage else None

        if capability_id in {'cancel_operation', 'cancel_running_operation'} and not self._operation_ref(intent):
            if 'cancel_thread' in allowed:
                return self._retarget(intent, 'cancel_thread', kind='flow_control')
            return None

        return intent

    def _bind_latest_artifact_ref(self, intent: AtomicIntent) -> AtomicIntent:
        raw = intent.target.get('artifact_ref')
        if not raw: return intent
        if not isinstance(raw, ArtifactRef) and '@v' not in str(raw): return intent
        ref = raw if isinstance(raw, ArtifactRef) else ArtifactRef.parse(str(raw))
        try:
            latest = self.store.artifact_graph(self.run_id).latest_ref(ref.artifact_id)
        except KeyError:
            return intent
        if latest == ref: return intent
        target = dict(intent.target)
        target['artifact_ref'] = str(latest)
        return replace(intent, target=target)

    def _bind_failure_query(self, request: IntentRequest, intent: AtomicIntent) -> AtomicIntent:
        stage = self._stage_hint(request.message, intent) or self._latest_failed_stage() or self._current_stage()
        params = {key: value for key, value in intent.params.items() if key != 'stage_hint'}
        if stage: params['stage'] = stage
        target = {**intent.target, 'run_id': intent.target.get('run_id') or self.run_id}
        if stage: target['stage'] = stage
        return replace(intent, target=target, params=params)

    def _stage_hint(self, message: str, intent: AtomicIntent) -> str:
        raw = str(intent.params.get('stage') or intent.params.get('stage_hint')
                  or intent.target.get('stage') or '').strip().lower()
        if raw in STAGES: return raw
        if raw == 'current': return self._current_stage()
        text = f'{message} {raw}'.lower()
        for stage, aliases in _STAGE_ALIASES.items():
            if any(alias in text for alias in aliases):
                return stage
        return ''

    def _latest_failed_stage(self) -> str:
        stage = latest_failed_stage_from_events(self.store.read_events(self.run_id))
        if stage: return stage
        for ref in reversed(self.graph.run_refs()):
            if is_synthetic_operation(str(ref)): continue
            run = self.graph.get_run(ref)
            if run.status == 'ended' and run.outcome == 'failed':
                stage = stage_group(run.spec.flow_tag) or stage_group(run.spec.stage_tag)
                if stage: return stage
        return ''

    def _current_stage(self) -> str:
        active = [ref for ref in self.graph.run_refs({'running', 'checkpointed'})
                  if not is_synthetic_operation(str(ref))]
        if active:
            run = self.graph.get_run(active[-1])
            return stage_group(run.spec.flow_tag) or stage_group(run.spec.stage_tag)
        return ''

    def _asks_for_failure(self, message: str) -> bool:
        text = message.lower()
        return any(cue in text for cue in _FAILURE_CUES)

    @staticmethod
    def _operation_ref(intent: AtomicIntent) -> str:
        return str(intent.params.get('operation_run_id') or intent.target.get('operation_run_id') or '')

    @staticmethod
    def _with_stage(intent: AtomicIntent, stage: str) -> AtomicIntent:
        params = {key: value for key, value in intent.params.items() if key != 'stage_hint'}
        target = dict(intent.target)
        if stage:
            params['stage'] = stage
            target['stage'] = stage
        return replace(intent, params=params, target=target)

    @staticmethod
    def _retarget(intent: AtomicIntent, capability_id: str, *, kind: str) -> AtomicIntent:
        target = {**intent.target, 'capability_id': capability_id}
        return replace(intent, kind=kind, action=capability_id, target=target)

