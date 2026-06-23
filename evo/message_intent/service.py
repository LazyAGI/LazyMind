from __future__ import annotations

from collections.abc import Callable, Mapping
from dataclasses import dataclass
import hashlib
import re
import time
import uuid
from typing import Any

from jsonpointer import JsonPointer, JsonPointerException

from evo import normalize_chat_stream_url
from evo.artifact_flow import EvoFlowRuntime
from evo.artifact_flow.contract import case_ids as flow_case_ids
from evo.artifact_flow.contract import case_key
from evo.artifact_runtime import (
    ArtifactKey,
    ArtifactPayload,
    ArtifactRef,
    IntentCommandRequest,
    MaterializeIntent,
    PatchAndReconcileIntent,
    RetryFailedIntent,
    RunControlIntent,
    intent_request_fingerprint,
    intent_request_from_payload,
    prepare_intent_payload,
)
from evo.artifact_runtime.utils import canonical_json, normalize_json_value

from evo.operations.eval import ANSWER_METRICS, FAILURE_TYPES, answer_score_from_metrics
from .models import (
    MUTATING_KINDS,
    OPERATION_SPECS,
    PENDING_RESOLUTION_KINDS,
    READ_ONLY_KINDS,
    NextOps,
    ResolvedIntent,
)
from .planner import LLMCallable, StructuredJSONNextIntentPlanner
from .store import MessageLease, MessageSessionStore, MessageStoreConflict, PendingApproval
from .views import ArtifactViewService, artifact_id_for_key

RUN_ID = 'run_1'
MIN_PLANNER_CONFIDENCE = 0.65
DEFAULT_READ_CHARS = 1200

_PATCH_FIELD_TARGETS: dict[str, tuple[str, str]] = {
    'difficulty': ('eval.case_preparation', '/difficulty'),
    'question': ('eval.case', '/question'),
    'reference_answer': ('eval.case', '/reference_answer'),
    'expected_answer': ('eval.case', '/expected_answer'),
    'answer_correctness': ('eval.judge_result', '/answer_correctness'),
    'answer_relevance': ('eval.judge_result', '/answer_relevance'),
    'completeness': ('eval.judge_result', '/completeness'),
    'format_compliance': ('eval.judge_result', '/format_compliance'),
    'answer_score': ('eval.judge_result', '/answer_score'),
    'quality_label': ('eval.judge_result', '/quality_label'),
    'failure_type': ('eval.judge_result', '/failure_type'),
    'reason': ('eval.judge_result', '/reason'),
    'target_chat_url': ('eval.target_config', '/target_chat_url'),
    'answer_good_threshold': ('eval.policy', '/answer_good_threshold'),
    'primary_metric': ('abtest.candidate_config', '/primary_metric'),
    'target_mean_delta': ('abtest.candidate_config', '/target_mean_delta'),
    'goodcase_regression_ratio_limit': ('abtest.candidate_config', '/goodcase_regression_ratio_limit'),
    'regression_epsilon': ('abtest.candidate_config', '/regression_epsilon'),
}
_CASE_PATCH_FIELDS = frozenset({'difficulty', 'question', 'reference_answer', 'expected_answer'})
_CASE_PARTITION_PATCH_ARTIFACTS = frozenset({'eval.case_preparation', 'eval.case', 'eval.judge_result'})
_NUMERIC_PATCH_FIELDS = frozenset((
    *ANSWER_METRICS,
    'answer_score',
    'answer_good_threshold',
    'target_mean_delta',
    'goodcase_regression_ratio_limit',
    'regression_epsilon',
))
_DIFFICULTIES = frozenset({'easy', 'medium', 'hard'})
_QUALITY_LABELS = frozenset({'good', 'bad', 'partial', 'infra_failure', 'skipped'})
_METRICS = frozenset({
    'answer_score',
    'answer_correctness',
    'answer_relevance',
    'completeness',
    'format_compliance',
    'chunk_recall',
    'chunk_precision',
    'doc_recall',
    'doc_precision',
    'retrieval_score',
    'correct_rate',
})


@dataclass(frozen=True)
class MessageHandleResult:
    status: str
    thread_id: str
    turn_id: str
    message_id: str
    response: str
    message_event_cursor: int
    pending_approval: dict[str, Any] | None = None


@dataclass(frozen=True)
class _ExecutionContext:
    intent_index: int
    consumed_message_ids: tuple[str, ...]


class MessageIntentService:
    def __init__(
        self,
        store: MessageSessionStore,
        *,
        flow_getter: Callable[[str], EvoFlowRuntime],
        has_flow: Callable[[str], bool],
        flow_status: Callable[[str], dict],
        artifact_reader: Callable[[str, str], dict | None],
        case_count_getter: Callable[[str], int],
        planner: StructuredJSONNextIntentPlanner,
        response_llm: LLMCallable | None = None,
    ) -> None:
        self.store = store
        self.planner = planner
        self.response_llm = response_llm
        self.flow_getter = flow_getter
        self.has_flow = has_flow
        self.flow_status = flow_status
        self.artifact_reader = artifact_reader
        self.case_count_getter = case_count_getter

    def handle(
        self,
        thread_id: str,
        payload: dict[str, Any],
        *,
        sync_budget_seconds: float = 3.0,
        trusted_auto_agent: bool = False,
    ) -> MessageHandleResult:
        content = str(payload.get('content') or payload.get('message') or '').strip()
        if not content:
            raise ValueError('message content required')
        message_id = str(payload.get('message_id') or f'msg_{uuid.uuid4().hex[:12]}')
        started = time.monotonic()
        with self.store.lease(thread_id) as lease:
            replay = self._replay_duplicate_message(thread_id, message_id)
            if replay is not None:
                return replay
            try:
                turn_id, received = self.store.begin_turn(lease, message_id, content)
            except MessageStoreConflict:
                replay = self._replay_duplicate_message(thread_id, message_id)
                if replay is not None:
                    return replay
                raise
            result = self._handle_locked(lease, turn_id, message_id, content, payload,
                                         trusted_auto_agent=trusted_auto_agent)
            if time.monotonic() - started > sync_budget_seconds and result.status == 'done':
                return MessageHandleResult('accepted', thread_id, turn_id, message_id, result.response, received.seq)
            return result

    def subscribe_events(self, thread_id: str, since: int = 0) -> list[dict[str, Any]]:
        return [self._event_payload(event) for event in self.store.scan_events(thread_id, since)]

    def active_approval(self, thread_id: str) -> PendingApproval | None:
        return self.store.active_approval(thread_id)

    def resolve_pending_structured(
        self,
        thread_id: str,
        *,
        action: str,
        approval_token: str,
        command_id: str,
    ) -> MessageHandleResult:
        if action not in {'approve', 'reject', 'cancel'}:
            raise ValueError(f'unsupported approval action: {action}')
        kind = {'approve': 'approve_pending', 'reject': 'reject_pending', 'cancel': 'cancel_pending'}[action]
        message_id = f'{command_id}:approval'
        content = f'structured approval action: {kind}'
        with self.store.lease(thread_id, owner_id=f'message-intent-structured:{command_id}') as lease:
            replay = self._replay_structured_approval(thread_id, message_id)
            if replay is not None:
                active = self.store.active_approval(thread_id)
                if replay.status == 'done' or active is None or active.approval_token != approval_token:
                    return replay
                attempt = self.store.message_turn_count(thread_id, message_id)
                message_id = f'{message_id}:repair:{attempt}'
            try:
                turn_id, _ = self.store.begin_turn(lease, message_id, content)
            except MessageStoreConflict:
                replay = self._replay_duplicate_message(thread_id, message_id)
                if replay is not None:
                    return replay
                raise
            result = self._resolve_pending(lease, turn_id, message_id, kind, approval_token)
            self.store.finish_turn(lease, turn_id, status=result.status)
            return result

    def _replay_structured_approval(self, thread_id: str, message_id: str) -> MessageHandleResult | None:
        existing = self.store.last_turn_for_message_family(thread_id, message_id)
        if existing is None:
            return None
        return self._message_result_from_turn(thread_id, existing)

    def _replay_duplicate_message(self, thread_id: str, message_id: str) -> MessageHandleResult | None:
        existing = self.store.last_turn_for_message(thread_id, message_id)
        if existing is None:
            return None
        return self._message_result_from_turn(thread_id, existing)

    def _message_result_from_turn(self, thread_id: str, existing: Mapping[str, Any]) -> MessageHandleResult:
        turn_id = str(existing['turn_id'])
        message_id = str(existing['message_id'])
        assistant = self.store.latest_assistant_for_turn(thread_id, turn_id)
        response = '' if assistant is None else str(assistant.payload.get('content') or '')
        cursor = int(existing.get('message_event_cursor') or 0)
        if assistant is not None:
            cursor = max(cursor, assistant.seq)
        confirmation = self.store.confirmation_for_turn(thread_id, turn_id, message_id)
        pending = confirmation.payload if confirmation is not None else {}
        approval_token = str(pending.get('approval_token') or '')
        preview_hash = str(pending.get('preview_hash') or '')
        return MessageHandleResult(
            str(existing.get('status') or 'done'),
            thread_id,
            turn_id,
            message_id,
            response,
            cursor,
            None if not approval_token else {'approval_token': approval_token, 'preview_hash': preview_hash},
        )

    def _handle_locked(
        self,
        lease: MessageLease,
        turn_id: str,
        message_id: str,
        content: str,
        payload: dict[str, Any] | None = None,
        trusted_auto_agent: bool = False,
    ) -> MessageHandleResult:
        thread_id = lease.thread_id
        prior_reminder = self.store.reminder(thread_id)
        try:
            plan = self.planner.plan(
                content,
                message_id=message_id,
                working_set=self._planner_context(lease, turn_id, message_id, payload or {}, trusted_auto_agent),
                reminder=prior_reminder,
            )
        except ValueError as exc:
            self.store.set_reminder(lease, prior_reminder)
            self._set_blocked_next_ops(lease, content, None, f'planner_failed: {exc}')
            response = self._clarify(
                lease,
                turn_id,
                message_id,
                '我没能可靠解析这条消息要执行的 evo 操作，请换个更明确的说法。',
            )
            self.store.finish_turn(lease, turn_id, status=response.status)
            return response

        self.store.append_event(
            lease,
            'intent_parsed',
            {
                'status': plan.status,
                'next_ops': None if plan.next_ops is None else plan.next_ops.model_dump(mode='json'),
                'confidence': plan.confidence,
                'reminder': plan.reminder,
                'clarification': plan.clarification,
            },
            turn_id=turn_id,
            message_id=message_id,
        )

        if plan.status == 'done':
            self.store.set_reminder(lease, plan.reminder)
            self.store.clear_blocked_next_ops(lease)
            response = self._assistant(lease, turn_id, message_id, '没有需要执行的新操作。')
            self.store.finish_turn(lease, turn_id, status=response.status)
            return response
        if plan.status == 'clarification':
            self.store.set_reminder(lease, plan.reminder)
            self._set_blocked_next_ops(lease, content, plan.next_ops, plan.clarification or 'clarification')
            response = self._clarify(lease, turn_id, message_id, plan.clarification or '请明确要执行的 evo 操作。')
            self.store.finish_turn(lease, turn_id, status=response.status)
            return response
        if plan.next_ops is None:
            self.store.set_reminder(lease, plan.reminder)
            self._set_blocked_next_ops(lease, content, None, 'planner did not provide next_ops')
            response = self._clarify(
                lease,
                turn_id,
                message_id,
                '我没能可靠解析这条消息要执行的 evo 操作，请换个更明确的说法。',
            )
            self.store.finish_turn(lease, turn_id, status=response.status)
            return response
        return self._handle_next_ops(
            lease,
            turn_id,
            message_id,
            content,
            plan.next_ops,
            reminder_to_store=plan.reminder,
            update_reminder=True,
        )

    def _handle_next_ops(
        self,
        lease: MessageLease,
        turn_id: str,
        message_id: str,
        content: str,
        next_ops: NextOps,
        *,
        reminder_to_store: str,
        update_reminder: bool,
    ) -> MessageHandleResult:
        if update_reminder:
            self.store.set_reminder(lease, reminder_to_store)
        gate = self._gate_next_ops(lease, turn_id, message_id, next_ops)
        if gate:
            self._set_blocked_next_ops(lease, content, next_ops, gate)
            response = self._clarify(lease, turn_id, message_id, gate)
            self.store.finish_turn(lease, turn_id, status=response.status)
            return response
        resolved = self._resolve_next_ops(lease.thread_id, next_ops)
        gate = self._gate_next_ops(lease, turn_id, message_id, next_ops, resolved)
        if gate:
            self._set_blocked_next_ops(lease, content, next_ops, gate)
            response = self._clarify(lease, turn_id, message_id, gate)
            self.store.finish_turn(lease, turn_id, status=response.status)
            return response
        context = _ExecutionContext(intent_index=0, consumed_message_ids=(message_id,))
        response = self._execute(lease, turn_id, message_id, resolved, context)
        if response.status in {'clarification', 'error'}:
            self._set_blocked_next_ops(lease, content, next_ops, response.response)
        else:
            self.store.clear_blocked_next_ops(lease)
        self.store.finish_turn(lease, turn_id, status=response.status)
        return response

    def _planner_context(
        self,
        lease: MessageLease,
        turn_id: str,
        message_id: str,
        payload: Mapping[str, Any] | None = None,
        trusted_auto_agent: bool = False,
    ) -> dict[str, Any]:
        thread_id = lease.thread_id
        working_set = dict(self.store.working_set(thread_id))
        working_set.pop('reminder', None)
        approval = self._active_approval(lease, turn_id, message_id)
        context = {
            'conversation_working_set': working_set,
            'flow_status': self.flow_status(thread_id),
            'active_approval': None if approval is None else {
                'approval_token': approval.approval_token,
                'intent_kind': approval.intent_kind,
                'risk_level': approval.risk_level,
                'expires_at': approval.expires_at,
            },
        }
        auto_context = _auto_agent_context(payload or {}, trusted=trusted_auto_agent)
        if auto_context:
            context['auto_agent_context'] = auto_context
        return context

    def _gate_next_ops(
        self,
        lease: MessageLease,
        turn_id: str,
        message_id: str,
        next_ops: NextOps,
        resolved: ResolvedIntent | None = None,
    ) -> str:
        if next_ops.confidence < MIN_PLANNER_CONFIDENCE:
            return '我不够确定要执行哪个 evo 操作，请更明确地描述。'
        kind = next_ops.kind
        spec = OPERATION_SPECS[kind]
        if not spec.runtime_supported:
            return spec.unsupported_reason or f'{kind} is not supported by the runtime yet'
        if self._active_approval(lease, turn_id, message_id) is not None:
            if kind not in READ_ONLY_KINDS and kind not in PENDING_RESOLUTION_KINDS:
                return '已有待确认操作，请先确认或取消后再发起新的修改或流程控制。'
        if kind in MUTATING_KINDS and not self.has_flow(lease.thread_id):
            return '当前还没有可控制的 evo 流程；请先启动流程，或先查看当前状态。'
        if resolved is not None and resolved.kind == 'unsupported':
            return resolved.reason or '暂不支持该 evo 操作。'
        return ''

    def _set_blocked_next_ops(
        self,
        lease: MessageLease,
        content: str,
        next_ops: NextOps | None,
        reason: str,
    ) -> None:
        self.store.set_blocked_next_ops(lease, {
            'source_message': str(content or '').strip(),
            'next_ops': None if next_ops is None else next_ops.model_dump(mode='json'),
            'reason': str(reason or '').strip(),
            'created_at': time.time(),
        })

    def _active_approval(
        self,
        lease: MessageLease,
        turn_id: str = '',
        message_id: str = '',
    ) -> PendingApproval | None:
        approval = self.store.active_approval(lease.thread_id)
        if approval is None:
            return None
        if approval.expires_at > time.time():
            return approval
        self.store.expire_approval(lease, approval.approval_token, turn_id=turn_id, message_id=message_id)
        return None

    def _resolve_next_ops(self, thread_id: str, next_ops: NextOps) -> ResolvedIntent:
        args = next_ops.args.model_dump(mode='python') if hasattr(
            next_ops.args, 'model_dump') else dict(next_ops.args or {})
        kind = next_ops.kind
        case_ref = str(args.get('case_ref') or args.get('case') or '')
        case_id = str(args.get('case_id') or '') or self._case_id_from_ref(thread_id, case_ref)
        case_ids = _string_tuple(args.get('case_ids')) or self._case_ids_from_ref(thread_id, case_ref)
        if case_id or case_ids:
            allowed = set(flow_case_ids(self.case_count_getter(thread_id)))
            if case_id and case_id not in allowed:
                return ResolvedIntent(kind='unsupported', reason=f'unknown case id: {case_id}')
            unknown = [item for item in case_ids if item not in allowed]
            if unknown:
                return ResolvedIntent(kind='unsupported', reason=f'unknown case id: {unknown[0]}')
        return ResolvedIntent(
            kind=kind,
            case_id=case_id,
            case_ref=case_ref,
            case_ids=tuple(case_ids),
            artifact_id=str(args.get('artifact_id') or args.get('artifact_ref') or ''),
            field=_normalize_patch_field(str(args.get('field') or '')),
            value=args.get('value'),
            approval_token=str(args.get('approval_token') or ''),
            reason=str(next_ops.reason or args.get('reason') or ''),
            raw_args=args,
        )

    def _case_id_from_ref(self, thread_id: str, case_ref: str) -> str:
        del thread_id
        normalized = _normalize_case_ref(case_ref)
        if isinstance(normalized, str) and normalized.startswith('case_'):
            return normalized
        return ''

    def _case_ids_from_ref(self, thread_id: str, case_ref: str) -> tuple[str, ...]:
        normalized = _normalize_case_ref(case_ref)
        if normalized != 'selected_cases':
            return ()
        selected = self.store.working_set(thread_id).get('selected_cases') or ()
        return tuple(str(item) for item in selected if str(item).strip())

    def _execute(
            self,
            lease: MessageLease,
            turn_id: str,
            message_id: str,
            intent: ResolvedIntent,
            context: _ExecutionContext) -> MessageHandleResult:
        kind = intent.kind
        if kind == 'unsupported':
            return self._clarify(lease, turn_id, message_id, intent.reason or '暂不支持该 evo 操作。')
        if kind == 'general_chat':
            fallback = '我无法在 evo 服务里获取实时外部信息，但可以继续处理你剩下的 evo 请求。'
            return self._assistant_from_tool_result(
                lease,
                turn_id,
                message_id,
                kind='general_chat',
                tool_result={
                    'topic': str(intent.raw_args.get('topic') or ''),
                    'reply_intent': str(intent.raw_args.get('reply_intent') or ''),
                    'message': fallback,
                    'external_tools_available': False,
                },
                fallback=fallback,
                final=False)
        if kind == 'status_query':
            status = self.flow_status(lease.thread_id)
            return self._assistant_from_tool_result(
                lease,
                turn_id,
                message_id,
                kind='status_query',
                tool_result=status,
                fallback=_natural_fallback('status_query', status),
                final=False,
            )
        if kind == 'list_failed_cases':
            return self._list_failed_cases(lease, turn_id, message_id)
        if kind == 'read_report_section':
            return self._read_report(lease, turn_id, message_id, intent)
        if kind == 'read_case_result':
            if intent.case_ids:
                return self._clarify(lease, turn_id, message_id, 'v1 仅支持一次读取单个 case，请指定一个 case。')
            if not intent.case_id:
                return self._clarify(lease, turn_id, message_id, '请指定要读取的 case。')
            return self._read_case(lease, turn_id, message_id, intent.case_id, intent)
        if kind in MUTATING_KINDS and not self.has_flow(lease.thread_id):
            return self._clarify(lease, turn_id, message_id, '当前还没有可控制的 evo 流程；请先启动流程，或先查看当前状态。')
        if kind == 'continue_flow':
            return self._continue(lease, turn_id, message_id, context)
        if kind == 'pause_flow':
            command_id = _message_command_request(turn_id, context.intent_index, kind, RUN_ID, RunControlIntent(
                'pause'), advance_until_idle=False).command_id
            state = self.flow_getter(lease.thread_id).pause_flow(command_id=command_id, run_id=RUN_ID)
            return self._command_response(
                lease, turn_id, message_id, kind, {
                    'status': state.gate_status, 'current_step': state.current_step})
        if kind == 'cancel_flow':
            command_id = _message_command_request(turn_id, context.intent_index, kind, RUN_ID, RunControlIntent(
                'cancel'), advance_until_idle=False).command_id
            state = self.flow_getter(lease.thread_id).cancel_flow(command_id=command_id, run_id=RUN_ID)
            return self._command_response(
                lease, turn_id, message_id, kind, {
                    'status': state.gate_status, 'current_step': state.current_step})
        if kind == 'retry_failed':
            return self._retry_failed(lease, turn_id, message_id, context)
        if kind == 'rerun_case':
            if intent.case_ids:
                return self._clarify(lease, turn_id, message_id, 'v1 仅支持单 case 重跑，请指定一个 case。')
            return self._rerun_case(lease, turn_id, message_id, intent.case_id, context)
        if kind == 'patch_artifact':
            return self._patch_artifact(lease, turn_id, message_id, intent, context)
        if kind in PENDING_RESOLUTION_KINDS:
            return self._resolve_pending(lease, turn_id, message_id, kind, intent.approval_token)
        return self._clarify(lease, turn_id, message_id, 'unsupported')

    def _continue(self, lease: MessageLease, turn_id: str, message_id: str,
                  context: _ExecutionContext) -> MessageHandleResult:
        flow = self.flow_getter(lease.thread_id)
        state = flow.continue_flow(command_id=flow.continue_flow_command_id(
            turn_id=turn_id, intent_index=context.intent_index, run_id=RUN_ID), run_id=RUN_ID)
        return self._command_response(
            lease,
            turn_id,
            message_id,
            'continue_flow',
            {'status': state.gate_status, 'current_step': state.current_step,
                'completed_steps': list(state.completed_steps)},
        )

    def _retry_failed(self, lease: MessageLease, turn_id: str, message_id: str,
                      context: _ExecutionContext) -> MessageHandleResult:
        command_id = _message_command_request(turn_id, context.intent_index,
                                              'retry_failed', RUN_ID, RetryFailedIntent()).command_id
        state = self.flow_getter(lease.thread_id).retry_failed_flow(command_id=command_id, run_id=RUN_ID)
        return self._command_response(
            lease, turn_id, message_id, 'retry_failed', {
                'status': state.gate_status, 'current_step': state.current_step})

    def _rerun_case(
            self,
            lease: MessageLease,
            turn_id: str,
            message_id: str,
            case_id: str,
            context: _ExecutionContext) -> MessageHandleResult:
        if not case_id:
            return self._clarify(lease, turn_id, message_id, '请指定要重跑的 case。')
        key = case_key('eval.rag_answer', case_id)
        command_id = _message_command_request(turn_id, context.intent_index, 'rerun_case',
                                              RUN_ID, MaterializeIntent((key,), include_downstream=True)).command_id
        state = self.flow_getter(lease.thread_id).materialize_flow(
            command_id=command_id,
            run_id=RUN_ID,
            artifacts=(key,),
        )
        return self._command_response(
            lease, turn_id, message_id, 'rerun_case', {
                'status': state.gate_status, 'current_step': state.current_step, 'case_id': case_id})

    def _patch_artifact(
            self,
            lease: MessageLease,
            turn_id: str,
            message_id: str,
            intent: ResolvedIntent,
            context: _ExecutionContext) -> MessageHandleResult:
        if self._active_approval(lease, turn_id, message_id) is not None:
            return self._clarify(lease, turn_id, message_id, '已有待确认操作，请先确认或取消后再发起新的修改。')
        target = _patch_target(intent)
        if target is None:
            return self._clarify(lease, turn_id, message_id, 'unsupported patch field')
        artifact_key, pointer = target
        row = self.artifact_reader(lease.thread_id, artifact_id_for_key(artifact_key))
        if row is None:
            return self._clarify(lease, turn_id, message_id, f'artifact not found: {artifact_id_for_key(artifact_key)}')
        ref = _parse_ref(str(row.get('ref') or ''), artifact_key)
        value = row.get('data')
        try:
            validated = _validate_patch_value(intent.field, intent.value)
            patched = _replace_json_value(value, pointer, validated)
            patched = _normalize_patched_artifact(artifact_key, patched, intent.field)
            _validate_patched_artifact(artifact_key, patched)
            patch_preview = _patch_preview(artifact_key, ref, value, patched, intent.field, pointer)
        except (TypeError, ValueError, JsonPointerException) as exc:
            return self._clarify(lease, turn_id, message_id, f'patch validation failed: {exc}')
        provenance = {
            'patch_source': f'message_turn:{turn_id}',
            'turn_id': turn_id,
            'message_ids': list(context.consumed_message_ids),
            'parsed_next_ops_kind': intent.kind,
            'original_expected_ref': str(ref),
            'field': intent.field,
            'json_pointer': pointer,
            'source': 'message_intent',
        }
        payload = ArtifactPayload(str(row.get('schema') or 'ManualPatch'), patched, metadata=provenance)
        intent_request = PatchAndReconcileIntent(
            artifact_key,
            payload,
            ref,
            patch_source=f'message_turn:{turn_id}',
            include_downstream=True,
            reason=f'message:{turn_id}',
        )
        request = _message_command_request(
            turn_id,
            context.intent_index,
            'patch_artifact',
            RUN_ID,
            intent_request,
            metadata=provenance,
        )
        prepared = prepare_intent_payload(request)
        preview = self.flow_getter(lease.thread_id).preview_reconcile(artifact_key)
        try:
            approval = self.store.put_pending_approval(
                lease,
                approval_token=f'appr_{uuid.uuid4().hex[:12]}',
                command_id=request.command_id,
                run_id=request.run_id,
                intent_kind=request.kind,
                prepared_payload=prepared.payload,
                request_fingerprint=prepared.request_fingerprint,
                preview_hash=_preview_hash(preview),
                expected_refs=(str(ref),),
                risk_level='medium',
                expires_at=time.time() + 3600,
            )
        except MessageStoreConflict:
            return self._clarify(lease, turn_id, message_id, '已有待确认操作，请先确认或取消后再发起新的修改。')
        self.store.append_event(lease,
                                'confirmation_required',
                                {'approval_token': approval.approval_token,
                                 'request_fingerprint': approval.request_fingerprint,
                                 'preview_hash': approval.preview_hash,
                                 'expected_refs': list(approval.expected_refs),
                                 'risk_level': approval.risk_level,
                                 'patch_preview': patch_preview,
                                 'preview': preview,
                                 'provenance': {**provenance,
                                                'request_fingerprint': approval.request_fingerprint,
                                                'command_id': request.command_id},
                                 },
                                turn_id=turn_id,
                                message_id=message_id,
                                )
        response = self._synthesize_response(
            lease.thread_id,
            turn_id,
            message_id,
            kind='patch_artifact',
            tool_result={
                'message': '需要确认后执行该修改。',
                'approval_token': approval.approval_token,
                'preview_hash': approval.preview_hash,
                'risk_level': approval.risk_level,
                'expected_refs': list(approval.expected_refs),
                'patch_preview': patch_preview,
                'preview': preview,
                'provenance': provenance,
            },
            fallback=_patch_confirmation_fallback(approval.approval_token, patch_preview),
        )
        return MessageHandleResult(
            'blocked',
            lease.thread_id,
            turn_id,
            message_id,
            response,
            self._assistant_event(lease, turn_id, message_id, response).seq,
            pending_approval={'approval_token': approval.approval_token, 'preview_hash': approval.preview_hash},
        )

    def _resolve_pending(
            self,
            lease: MessageLease,
            turn_id: str,
            message_id: str,
            kind: str,
            approval_token: str) -> MessageHandleResult:
        approval = self._active_approval(lease, turn_id, message_id)
        if approval is None:
            return self._clarify(lease, turn_id, message_id, '没有待确认操作。')
        if approval_token and approval.approval_token != approval_token:
            return self._clarify(lease, turn_id, message_id, 'approval_token mismatch')
        if approval.expires_at <= time.time():
            self.store.resolve_approval(lease, approval.approval_token, status='expired', event_payload={
                                        'reason': 'expired'}, turn_id=turn_id, message_id=message_id)
            return self._clarify(lease, turn_id, message_id, '待确认操作已过期。')
        if kind in {'reject_pending', 'cancel_pending'}:
            status = 'rejected' if kind == 'reject_pending' else 'cancelled'
            self.store.resolve_approval(lease, approval.approval_token, status=status,
                                        event_payload={}, turn_id=turn_id, message_id=message_id)
            return self._assistant(lease, turn_id, message_id, '已取消待确认操作。')
        request = self._request_from_approval(approval)
        if intent_request_fingerprint(request) != approval.request_fingerprint:
            return self._clarify(lease, turn_id, message_id, 'request_fingerprint mismatch')
        result = self.flow_getter(lease.thread_id).runtime.execute_intent(request)
        if result.status == 'applied':
            self.store.resolve_approval(lease, approval.approval_token, status='approved',
                                        event_payload={'reason': result.reason}, turn_id=turn_id, message_id=message_id)
            return self._command_response(
                lease, turn_id, message_id, 'approve_pending', {
                    'status': result.status, 'reason': result.reason})
        stale = self._stale_expected_ref(lease.thread_id, approval)
        if stale:
            self.store.resolve_approval(
                lease,
                approval.approval_token,
                status='cancelled',
                event_payload={
                    'reason': 'stale_expected_ref',
                    'stale_refs': stale},
                turn_id=turn_id,
                message_id=message_id)
            return self._clarify(lease, turn_id, message_id, '待修改 artifact 已变化，请重新发起修改。')
        return self._command_response(
            lease, turn_id, message_id, 'approve_pending', {
                'status': 'failed',
                'reason': result.reason or result.status,
            })

    def _request_from_approval(self, approval: PendingApproval) -> IntentCommandRequest:
        return intent_request_from_payload(
            approval.command_id,
            approval.prepared_payload,
            expected_fingerprint=approval.request_fingerprint,
        )

    def _stale_expected_ref(self, thread_id: str, approval: PendingApproval) -> list[str]:
        out: list[str] = []
        flow = self.flow_getter(thread_id)
        for value in approval.expected_refs:
            key, version = _parse_ref_parts(value)
            latest = flow.latest_ref(key)
            if latest is None or latest.version != version:
                out.append(value)
        return out

    def _list_failed_cases(self, lease: MessageLease, turn_id: str, message_id: str) -> MessageHandleResult:
        view = self._views(lease.thread_id).view('eval.summary')
        self.store.append_event(lease, 'artifact_view', view, turn_id=turn_id, message_id=message_id)
        selected = tuple(view.get('facts', {}).get('failed_cases') or ())
        self.store.update_working_set(lease, {
            'selected_cases': selected,
            'last_report': 'eval.summary',
            'last_artifact_view': _view_state(view, 'eval.summary'),
        })
        tool_result = _view_payload(view, facts_only=True)
        return self._assistant_from_tool_result(
            lease,
            turn_id,
            message_id,
            kind='list_failed_cases',
            tool_result=tool_result,
            fallback=_natural_fallback('list_failed_cases', tool_result),
            final=False,
        )

    def _read_report(
        self,
        lease: MessageLease,
        turn_id: str,
        message_id: str,
        intent: ResolvedIntent,
    ) -> MessageHandleResult:
        args = intent.raw_args
        artifact = intent.artifact_id or (
            'analysis.summary' if self.artifact_reader(lease.thread_id, 'analysis.summary') else 'eval.summary'
        )
        view = self._views(lease.thread_id).view(
            artifact,
            selector=str(args.get('section') or args.get('selector') or ''),
            cursor=str(args.get('cursor') or ''),
            max_chars=_int_arg(args.get('max_chars'), DEFAULT_READ_CHARS),
        )
        self.store.append_event(lease, 'artifact_view', view, turn_id=turn_id, message_id=message_id)
        self.store.update_working_set(lease, {
            'last_report': artifact,
            'last_artifact_view': _view_state(view, artifact),
        })
        tool_result = _view_payload(view)
        return self._assistant_from_tool_result(
            lease,
            turn_id,
            message_id,
            kind='read_report_section',
            tool_result=tool_result,
            fallback=_natural_fallback('read_report_section', tool_result),
            final=False,
        )

    def _read_case(
        self,
        lease: MessageLease,
        turn_id: str,
        message_id: str,
        case_id: str,
        intent: ResolvedIntent,
    ) -> MessageHandleResult:
        args = intent.raw_args
        artifact = f'eval.judge_result[{case_id}]'
        view = self._views(lease.thread_id).view(
            artifact,
            selector=str(args.get('selector') or ''),
            cursor=str(args.get('cursor') or ''),
            max_chars=_int_arg(args.get('max_chars'), DEFAULT_READ_CHARS),
        )
        self.store.append_event(lease, 'artifact_view', view, turn_id=turn_id, message_id=message_id)
        self.store.update_working_set(lease, {
            'selected_cases': (case_id,),
            'last_case': case_id,
            'last_artifact_view': _view_state(view, artifact),
        })
        tool_result = _view_payload(view)
        return self._assistant_from_tool_result(
            lease,
            turn_id,
            message_id,
            kind='read_case_result',
            tool_result=tool_result,
            fallback=_natural_fallback('read_case_result', tool_result),
            final=False,
        )

    def _views(self, thread_id: str) -> ArtifactViewService:
        return ArtifactViewService(lambda artifact_id: self.artifact_reader(thread_id, artifact_id))

    def _command_response(self, lease: MessageLease, turn_id: str, message_id: str, kind: str,
                          payload: dict[str, Any], *, final: bool = False) -> MessageHandleResult:
        event = self.store.append_event(lease, 'command_applied', {
                                        'kind': kind, **payload}, turn_id=turn_id, message_id=message_id)
        tool_result = {'kind': kind, **payload}
        text = self._synthesize_response(lease.thread_id, turn_id, message_id, kind=kind,
                                         tool_result=tool_result, fallback=_natural_fallback(kind, tool_result))
        assistant = self._assistant_event(lease, turn_id, message_id, text)
        status = 'error' if payload.get('status') == 'failed' else 'done'
        if final and status == 'done':
            self.store.append_event(lease, 'done', {'status': 'done'}, turn_id=turn_id, message_id=message_id)
        return MessageHandleResult(status, lease.thread_id, turn_id, message_id, text, max(event.seq, assistant.seq))

    def _assistant_from_tool_result(
        self,
        lease: MessageLease,
        turn_id: str,
        message_id: str,
        *,
        kind: str,
        tool_result: Mapping[str, Any],
        fallback: str,
        final: bool = True,
    ) -> MessageHandleResult:
        text = self._synthesize_response(lease.thread_id, turn_id, message_id, kind=kind,
                                         tool_result=tool_result, fallback=fallback)
        return self._assistant(lease, turn_id, message_id, text, final=final)

    def _synthesize_response(
        self,
        thread_id: str,
        turn_id: str,
        message_id: str,
        *,
        kind: str,
        tool_result: Mapping[str, Any],
        fallback: str,
    ) -> str:
        if self.response_llm is None:
            return fallback
        prompt = _response_prompt(thread_id, turn_id, message_id, kind, tool_result)
        try:
            raw = self.response_llm(prompt)
        except Exception:
            return fallback
        text = str(raw or '').strip()
        return text or fallback

    def _assistant(
            self,
            lease: MessageLease,
            turn_id: str,
            message_id: str,
            content: str,
            *,
            final: bool = True) -> MessageHandleResult:
        event = self._assistant_event(lease, turn_id, message_id, content)
        if final:
            self.store.append_event(lease, 'done', {'status': 'done'}, turn_id=turn_id, message_id=message_id)
        return MessageHandleResult('done', lease.thread_id, turn_id, message_id, content, event.seq)

    def _clarify(self, lease: MessageLease, turn_id: str, message_id: str, content: str) -> MessageHandleResult:
        event = self.store.append_event(lease, 'clarification_required', {
                                        'content': content}, turn_id=turn_id, message_id=message_id)
        assistant = self._assistant_event(lease, turn_id, message_id, content)
        return MessageHandleResult('clarification', lease.thread_id, turn_id,
                                   message_id, content, max(event.seq, assistant.seq))

    def _assistant_event(self, lease: MessageLease, turn_id: str, message_id: str, content: str):
        return self.store.append_event(
            lease, 'assistant_response', {
                'content': content}, turn_id=turn_id, message_id=message_id)

    @staticmethod
    def _event_payload(event) -> dict[str, Any]:
        return {
            'id': str(
                event.seq),
            'event': event.event_type,
            'data': {
                'seq': event.seq,
                'type': event.event_type,
                **event.payload}}


def _patch_target(intent: ResolvedIntent) -> tuple[ArtifactKey, str] | None:
    field = intent.field
    target = _PATCH_FIELD_TARGETS.get(field)
    if target is None:
        return None
    artifact_id, pointer = target
    if field in _CASE_PATCH_FIELDS or artifact_id in _CASE_PARTITION_PATCH_ARTIFACTS:
        if not intent.case_id:
            return None
        return case_key(artifact_id, intent.case_id), pointer
    return ArtifactKey.of(artifact_id), pointer


def _string_tuple(value: Any) -> tuple[str, ...]:
    if value is None or value == '':
        return ()
    if isinstance(value, str):
        return (value,)
    if isinstance(value, (list, tuple, set)):
        return tuple(str(item) for item in value if str(item).strip())
    return (str(value),)


def _normalize_patch_field(value: str) -> str:
    text = str(value or '').strip()
    if text in _PATCH_FIELD_TARGETS:
        return text
    aliases = {
        '难度': 'difficulty',
        '题目': 'question',
        '问题': 'question',
        '提问': 'question',
        '参考答案': 'reference_answer',
        '标准答案': 'expected_answer',
        '期望答案': 'expected_answer',
        '目标地址': 'target_chat_url',
        '目标url': 'target_chat_url',
        'target url': 'target_chat_url',
        '质量阈值': 'answer_good_threshold',
        '评分': 'answer_score',
        '答案分': 'answer_score',
        '正确性': 'answer_correctness',
        '相关性': 'answer_relevance',
        '完整性': 'completeness',
        '格式': 'format_compliance',
        '评分理由': 'reason',
        '原因': 'reason',
        '质量标签': 'quality_label',
        '失败类型': 'failure_type',
        '主指标': 'primary_metric',
        '主要指标': 'primary_metric',
    }
    normalized = aliases.get(text.lower()) or aliases.get(text)
    return normalized or text


def _auto_agent_context(payload: Mapping[str, Any], *, trusted: bool = False) -> dict[str, Any]:
    metadata = payload.get('metadata') if isinstance(payload.get('metadata'), Mapping) else {}
    if not trusted or metadata.get('source') != 'auto_agent':
        return {}
    raw = payload.get('auto_intervention')
    if raw is None:
        raw = metadata.get('auto_intervention')
    return {
        'metadata': dict(metadata),
        'auto_intervention': dict(raw) if isinstance(raw, Mapping) else None,
    }


def _view_state(view: dict[str, Any], artifact_id: str) -> dict[str, Any]:
    return {
        'artifact_id': artifact_id,
        'source_ref': str(view.get('source_ref') or artifact_id),
        'selector': str(view.get('selector') or ''),
        'max_chars': int(view.get('max_chars') or DEFAULT_READ_CHARS),
        'truncated': bool(view.get('truncated')),
        'next_cursor': str(view.get('next_cursor') or ''),
        'available_sections': list(view.get('available_sections') or ()),
    }


def _response_prompt(
    thread_id: str,
    turn_id: str,
    message_id: str,
    kind: str,
    tool_result: Mapping[str, Any],
) -> str:
    return (
        'You are the user-facing response writer for an Evo message agent. '
        'The operation has already been parsed and validated; tool_result tells you whether it was executed, '
        'read-only, or pending approval. '
        'Write a concise Chinese answer for the user based only on the tool result. '
        'Do not output raw JSON. Do not invent facts not present in the tool result. '
        'If the result is truncated or has next_cursor, say that more content can be read. '
        'For pending approvals, clearly say confirmation is required and mention the approval token. '
        'For patch approvals, include patch_preview target_artifact, source_ref, field, old_value, and new_value '
        'so the user can audit the exact change and source version. '
        'For status, summarize the current state and important completed/current steps. '
        'For case/report reads, summarize the key facts and cite source_ref when present.\n\n'
        f'Thread id: {thread_id}\n'
        f'Turn id: {turn_id}\n'
        f'Message id: {message_id}\n'
        f'Operation kind: {kind}\n'
        f'Tool result JSON:\n{canonical_json(normalize_json_value(dict(tool_result), allow_tuple=True))}'
    )


def _view_payload(view: dict[str, Any], *, facts_only: bool = False) -> dict[str, Any]:
    payload: dict[str, Any] = {
        'source_ref': str(view.get('source_ref') or ''),
        'facts': view.get('facts') or {},
        'truncated': bool(view.get('truncated')),
        'next_cursor': str(view.get('next_cursor') or ''),
        'selector': str(view.get('selector') or ''),
        'available_sections': list(view.get('available_sections') or ()),
    }
    if not facts_only:
        payload['excerpt'] = str(view.get('excerpt') or '')
    return payload


def _natural_fallback(kind: str, payload: Mapping[str, Any]) -> str:
    if kind == 'status_query':
        status = str(payload.get('status') or 'unknown')
        current = str(payload.get('current_step') or '')
        completed = ', '.join(str(item) for item in payload.get('completed_steps') or ())
        detail = f'当前步骤：{current}。' if current else ''
        if completed:
            detail += f' 已完成步骤：{completed}。'
        return f'当前 evo 流程状态是 {status}。{detail}'.strip()
    if kind == 'list_failed_cases':
        failed = payload.get('facts') if isinstance(payload.get('facts'), Mapping) else {}
        cases = failed.get('failed_cases') or ()
        text = ', '.join(str(item) for item in cases) or '未发现失败 case'
        return f'失败 case：{text}。'
    if kind in {'read_case_result', 'read_report_section'}:
        source = str(payload.get('source_ref') or '当前产物')
        excerpt = str(payload.get('excerpt') or '').strip()
        truncated = '内容已截断，可以继续读取后续部分。' if payload.get('truncated') or payload.get('next_cursor') else ''
        return f'{source} 的读取结果：{excerpt or "暂无可展示内容。"} {truncated}'.strip()
    if kind == 'approve_pending':
        status = str(payload.get('status') or 'unknown')
        reason = str(payload.get('reason') or '').strip()
        return f'待确认操作处理结果：{status}。{reason}'.strip()
    status = str(payload.get('status') or 'done')
    current = str(payload.get('current_step') or '')
    return f'{kind} 已处理，状态：{status}。' + (f' 当前步骤：{current}。' if current else '')


def _int_arg(value: Any, default: int) -> int:
    try:
        return int(value)
    except (TypeError, ValueError):
        return default


def _validate_patch_value(field: str, value: Any) -> Any:
    if field == 'difficulty':
        text = str(value or '').strip().lower()
        if text not in _DIFFICULTIES:
            raise ValueError('difficulty must be easy, medium, or hard')
        return text
    if field in {'question', 'reference_answer', 'expected_answer'}:
        text = str(value or '').strip()
        if not text:
            raise ValueError(f'{field} must be non-empty')
        if len(text) > 4000:
            raise ValueError(f'{field} is too long')
        return text
    if field == 'target_chat_url':
        return normalize_chat_stream_url(str(value or '').strip(), 'target_chat_url')
    if field in _NUMERIC_PATCH_FIELDS:
        number = float(value)
        if not 0.0 <= number <= 1.0:
            raise ValueError(f'{field} must be between 0 and 1')
        return round(number, 4)
    if field == 'quality_label':
        label = str(value or '').strip()
        if label not in _QUALITY_LABELS:
            raise ValueError(f'quality_label must be one of: {", ".join(sorted(_QUALITY_LABELS))}')
        return label
    if field == 'failure_type':
        failure = str(value or '').strip()
        allowed = FAILURE_TYPES | {'infra_failure', 'candidate_not_run', 'unknown'}
        if failure not in allowed:
            raise ValueError(f'failure_type must be one of: {", ".join(sorted(allowed))}')
        return failure
    if field == 'reason':
        text = str(value or '').strip()
        if not text:
            raise ValueError('reason must be non-empty')
        return text[:500]
    if field == 'primary_metric':
        text = str(value or '').strip()
        if text not in _METRICS:
            raise ValueError('primary_metric is not supported')
        return text
    raise ValueError(f'unsupported patch field: {field}')


def _validate_patched_artifact(artifact: ArtifactKey, value: Any) -> None:
    if not isinstance(value, dict):
        raise ValueError('patched artifact must be an object')
    if artifact.artifact_id == 'eval.case_preparation':
        if str(value.get('difficulty') or '').strip() not in _DIFFICULTIES:
            raise ValueError('eval.case_preparation difficulty is invalid')
    elif artifact.artifact_id == 'eval.case':
        if 'question' in value and not str(value.get('question') or '').strip():
            raise ValueError('eval.case question must be non-empty')
    elif artifact.artifact_id == 'eval.target_config':
        normalize_chat_stream_url(str(value.get('target_chat_url') or ''), 'target_chat_url')
    elif artifact.artifact_id == 'eval.policy':
        if 'answer_good_threshold' in value:
            _validate_patch_value('answer_good_threshold', value.get('answer_good_threshold'))
    elif artifact.artifact_id == 'eval.judge_result':
        _validate_judge_result_patch(value)
    elif artifact.artifact_id == 'abtest.candidate_config':
        if 'primary_metric' in value:
            _validate_patch_value('primary_metric', value.get('primary_metric'))
        for field in ('target_mean_delta', 'goodcase_regression_ratio_limit', 'regression_epsilon'):
            if field in value:
                _validate_patch_value(field, value.get(field))
    else:
        raise ValueError(f'artifact is not patchable: {artifact}')
    return None


def _normalize_patched_artifact(artifact: ArtifactKey, value: Any, changed_field: str) -> Any:
    if artifact.artifact_id != 'eval.judge_result' or not isinstance(value, dict):
        return value
    out = dict(value)
    if changed_field in ANSWER_METRICS and any(metric in out for metric in ANSWER_METRICS):
        out['answer_score'] = answer_score_from_metrics(out)
        out['is_correct'] = str(out.get('quality_label') or '') == 'good'
        out['defect'] = '' if str(out.get('failure_type') or '') == 'none' else str(out.get('failure_type') or '')
    return out


def _validate_judge_result_patch(value: dict[str, Any]) -> None:
    if not str(value.get('case_id') or '').strip():
        raise ValueError('eval.judge_result case_id is required')
    for field in (*ANSWER_METRICS, 'answer_score'):
        if field in value:
            _validate_patch_value(field, value.get(field))
    if 'quality_label' in value:
        _validate_patch_value('quality_label', value.get('quality_label'))
    if 'failure_type' in value:
        _validate_patch_value('failure_type', value.get('failure_type'))
    if 'reason' in value:
        _validate_patch_value('reason', value.get('reason'))


def _replace_json_value(value: Any, pointer: str, replacement: Any) -> Any:
    normalized = normalize_json_value(value, allow_tuple=True)
    if not isinstance(normalized, dict):
        raise TypeError('patch target must be a JSON object')
    if '*' in pointer:
        raise ValueError('wildcard paths are forbidden')
    clone = _deepcopy_json(normalized)
    ptr = JsonPointer(pointer)
    parts = ptr.parts
    if not parts:
        raise ValueError('root replacement is forbidden')
    parent = JsonPointer.from_parts(parts[:-1]).resolve(clone)
    key = parts[-1]
    if isinstance(parent, list):
        index = int(key)
        if index < 0 or index >= len(parent):
            raise ValueError('array index out of bounds')
        parent[index] = normalize_json_value(replacement, allow_tuple=True)
    elif isinstance(parent, dict):
        if key not in parent:
            raise ValueError(f'path does not exist: {pointer}')
        parent[key] = normalize_json_value(replacement, allow_tuple=True)
    else:
        raise ValueError('patch parent is not mutable')
    return clone


def _patch_preview(
    artifact: ArtifactKey,
    ref: ArtifactRef,
    before: Any,
    after: Any,
    field: str,
    pointer: str,
) -> dict[str, Any]:
    normalized_before = normalize_json_value(before, allow_tuple=True)
    normalized_after = normalize_json_value(after, allow_tuple=True)
    return {
        'target_artifact': artifact_id_for_key(artifact),
        'source_ref': str(ref),
        'field': field,
        'json_pointer': pointer,
        'old_value': normalize_json_value(JsonPointer(pointer).resolve(normalized_before), allow_tuple=True),
        'new_value': normalize_json_value(JsonPointer(pointer).resolve(normalized_after), allow_tuple=True),
        'effective_changes': _top_level_changes(normalized_before, normalized_after),
    }


def _patch_confirmation_fallback(approval_token: str, patch_preview: Mapping[str, Any]) -> str:
    return (
        '需要确认后执行该修改：'
        f'{patch_preview.get("target_artifact")} '
        f'基于 {patch_preview.get("source_ref")} '
        f'{patch_preview.get("field")} '
        f'{patch_preview.get("old_value")!r} -> {patch_preview.get("new_value")!r}。'
        f'确认令牌：{approval_token}'
    )


def _top_level_changes(before: Any, after: Any) -> list[dict[str, Any]]:
    if not isinstance(before, dict) or not isinstance(after, dict):
        return []
    out: list[dict[str, Any]] = []
    for key in sorted(set(before) | set(after)):
        old = before.get(key)
        new = after.get(key)
        if old != new:
            out.append({'json_pointer': f'/{key}', 'old_value': old, 'new_value': new})
    return out


def _deepcopy_json(value: Any) -> Any:
    import json

    return json.loads(canonical_json(value))


def _preview_hash(preview: dict[str, Any]) -> str:
    return hashlib.sha256(canonical_json(normalize_json_value(preview, allow_tuple=True)).encode()).hexdigest()


def _message_command_request(
    turn_id: str,
    intent_index: int,
    intent_kind: str,
    run_id: str,
    intent: PatchAndReconcileIntent | MaterializeIntent | RetryFailedIntent | RunControlIntent,
    *,
    metadata: dict[str, Any] | None = None,
    advance_until_idle: bool = True,
) -> IntentCommandRequest:
    provisional = IntentCommandRequest('msg:pending', run_id, intent,
                                       advance_until_idle=advance_until_idle, metadata=metadata or {})
    fingerprint = intent_request_fingerprint(provisional)
    return IntentCommandRequest(
        f'msg:{turn_id}:{intent_index}:{intent_kind}:{fingerprint}',
        run_id,
        intent,
        advance_until_idle=advance_until_idle,
        metadata=metadata or {})


def _parse_ref(value: str, fallback_key: ArtifactKey) -> ArtifactRef:
    key, version = _parse_ref_parts(value)
    if version < 1:
        raise ValueError('artifact ref version required')
    return ArtifactRef(key if key.artifact_id else fallback_key, version)


def _parse_ref_parts(value: str) -> tuple[ArtifactKey, int]:
    text = str(value or '')
    version = 0
    if '@v' in text:
        text, raw = text.rsplit('@v', 1)
        version = int(raw)
    partition = ''
    if text.endswith(']') and '[' in text:
        text, partition = text[:-1].split('[', 1)
    return ArtifactKey(text, partition), version


def _normalize_case_ref(value: str) -> str | tuple[str, ...]:
    text = str(value or '').strip()
    if not text:
        return ''
    if text in {'selected_cases', 'these_cases'}:
        return 'selected_cases'
    if match := re.search(r'case[_\s-]*(\d{1,4})', text, re.IGNORECASE):
        return f'case_{int(match.group(1)):04d}'
    if match := re.search(r'第\s*(\d{1,4})\s*(?:个)?\s*case', text, re.IGNORECASE):
        return f'case_{int(match.group(1)):04d}'
    if match := re.search(r'第\s*([一二三四五六七八九十百千万零〇两\d]+)\s*(?:个)?\s*case', text, re.IGNORECASE):
        try:
            from cn2an import cn2an
        except ImportError as exc:
            raise RuntimeError('cn2an is required to resolve Chinese ordinal case references') from exc
        number = int(cn2an(match.group(1), 'smart'))
        return f'case_{number:04d}'
    return ''
