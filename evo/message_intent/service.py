from __future__ import annotations

from collections.abc import Callable
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

from .models import PlannerIntent, PlannerOutput, ResolvedIntent
from .planner import StructuredJSONNextIntentPlanner
from .store import MessageLease, MessageSessionStore, MessageStoreConflict, PendingApproval, PendingTurnBuffer
from .views import ArtifactViewService, artifact_id_for_key

RUN_ID = 'run_1'
MIN_PLANNER_CONFIDENCE = 0.65

_PATCH_FIELD_TARGETS: dict[str, tuple[str, str]] = {
    'difficulty': ('eval.case_preparation', '/difficulty'),
    'question': ('eval.case', '/question'),
    'reference_answer': ('eval.case', '/reference_answer'),
    'expected_answer': ('eval.case', '/expected_answer'),
    'target_chat_url': ('eval.target_config', '/target_chat_url'),
    'evaluation_policy': ('eval.policy', '/evaluation_policy'),
    'quality_threshold': ('eval.policy', '/quality_threshold'),
    'primary_metric': ('abtest.candidate_config', '/primary_metric'),
    'target_mean_delta': ('abtest.candidate_config', '/target_mean_delta'),
    'goodcase_regression_ratio_limit': ('abtest.candidate_config', '/goodcase_regression_ratio_limit'),
    'regression_epsilon': ('abtest.candidate_config', '/regression_epsilon'),
}
_CASE_PATCH_FIELDS = frozenset({'difficulty', 'question', 'reference_answer', 'expected_answer'})
_DIFFICULTIES = frozenset({'easy', 'medium', 'hard'})
_EVALUATION_POLICIES = frozenset({'balanced', 'answer_only', 'retrieval_diagnostic'})
_METRICS = frozenset({'answer_correctness', 'faithfulness', 'doc_recall', 'context_recall', 'correct_rate'})


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
    consumed_prefix_start: int
    consumed_prefix_len: int
    consumed_message_ids: tuple[str, ...]
    consumed_text: str


@dataclass(frozen=True)
class _BufferSegment:
    turn_id: str
    message_id: str
    content: str
    processed_cursor: int
    start: int
    end: int


@dataclass(frozen=True)
class _PendingBuffer:
    text: str
    segments: tuple[_BufferSegment, ...]


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
    ) -> None:
        self.store = store
        self.planner = planner
        self.flow_getter = flow_getter
        self.has_flow = has_flow
        self.flow_status = flow_status
        self.artifact_reader = artifact_reader
        self.case_count_getter = case_count_getter

    def handle(self, thread_id: str, payload: dict[str, Any], *,
               sync_budget_seconds: float = 3.0) -> MessageHandleResult:
        content = str(payload.get('content') or payload.get('message') or '').strip()
        if not content:
            raise ValueError('message content required')
        message_id = str(payload.get('message_id') or f'msg_{uuid.uuid4().hex[:12]}')
        started = time.monotonic()
        with self.store.lease(thread_id) as lease:
            turn_id, received = self.store.begin_turn(lease, message_id, content)
            result = self._handle_locked(lease, turn_id, message_id, content)
            if time.monotonic() - started > sync_budget_seconds and result.status == 'done':
                return MessageHandleResult('accepted', thread_id, turn_id, message_id, result.response, received.seq)
            return result

    def subscribe_events(self, thread_id: str, since: int = 0) -> list[dict[str, Any]]:
        return [self._event_payload(event) for event in self.store.scan_events(thread_id, since)]

    def _handle_locked(self, lease: MessageLease, turn_id: str, message_id: str, content: str) -> MessageHandleResult:
        thread_id = lease.thread_id
        pending = _pending_buffer(self.store.pending_turn_buffers(thread_id))
        if not pending.text.strip():
            response = self._assistant(lease, turn_id, message_id, '没有需要执行的新操作。')
            self.store.finish_turn(lease, turn_id, status=response.status,
                                   request_fingerprint='', processed_cursor=len(content))
            return response
        cursor = 0
        intent_index = 0
        last_response: MessageHandleResult | None = None
        while cursor < len(pending.text):
            buffer = pending.text[cursor:]
            if not buffer.strip():
                cursor = len(pending.text)
                break
            try:
                plan = self.planner.plan(buffer, message_id=_message_id_at_cursor(
                    pending, cursor) or message_id, working_set=self._planner_context(thread_id))
            except ValueError as exc:
                response = self._clarify(lease, turn_id, message_id, f'planner_failed: {exc}')
                self._finish_current_turn(lease, turn_id, content, response.status, pending, cursor)
                return response
            consumed_len = self._consumed_prefix_len(plan, buffer)
            if consumed_len < 0:
                response = self._clarify(lease, turn_id, message_id, '无法可靠确认已消费的消息片段，请重新描述要执行的操作。')
                self._finish_current_turn(lease, turn_id, content, response.status, pending, cursor)
                return response
            consumed_segments = _segments_for_range(pending, cursor, consumed_len)
            if consumed_len > 0 and not _consumed_message_ids_match(plan, consumed_segments):
                response = self._clarify(lease, turn_id, message_id, '无法可靠确认已消费的消息归属，请重新描述要执行的操作。')
                self._finish_current_turn(lease, turn_id, content, response.status, pending, cursor)
                return response
            self.store.append_event(
                lease,
                'intent_parsed',
                {'intent': plan.intent.model_dump(mode='json'), 'confidence': plan.confidence, 'status': plan.status},
                turn_id=turn_id,
                message_id=message_id,
            )
            if plan.status == 'done':
                if consumed_len > 0 and (cursor + consumed_len) < len(pending.text):
                    response = self._clarify(lease, turn_id, message_id, 'planner_done 不能只消费消息前缀，请重新描述要执行的操作。')
                    self._finish_current_turn(lease, turn_id, content, response.status, pending, cursor)
                    return response
                cursor = len(pending.text)
                break
            if plan.status == 'clarification':
                response = self._clarify(lease, turn_id, message_id, plan.needs_clarification or '请明确要执行的 evo 操作。')
                self._finish_current_turn(lease, turn_id, content, response.status, pending, cursor)
                return response
            if plan.confidence < MIN_PLANNER_CONFIDENCE:
                response = self._clarify(lease, turn_id, message_id,
                                         plan.needs_clarification or '我不够确定要执行哪个 evo 操作，请更明确地描述。')
                self._finish_current_turn(lease, turn_id, content, response.status, pending, cursor)
                return response
            if consumed_len <= 0:
                response = self._clarify(lease, turn_id, message_id, '无法推进消息消费游标，请更明确地描述。')
                self._finish_current_turn(lease, turn_id, content, response.status, pending, cursor)
                return response
            context = _ExecutionContext(
                intent_index,
                cursor,
                consumed_len,
                tuple(segment.message_id for segment in consumed_segments) or tuple(
                    plan.consumed_message_ids or (message_id,)),
                plan.consumed_text or buffer[:consumed_len],
            )
            resolved = self._resolve(thread_id, plan.intent)
            response = self._execute(lease, turn_id, message_id, resolved, context)
            cursor += consumed_len
            self._mark_consumed(lease, pending, cursor)
            if response.status != 'done':
                self._finish_current_turn(lease, turn_id, content, response.status, pending, cursor)
                return response
            last_response = response
            intent_index += 1
        if last_response is not None:
            self._mark_consumed(lease, pending, cursor)
            done = self.store.append_event(lease, 'done', {'status': 'done'}, turn_id=turn_id, message_id=message_id)
            self._finish_current_turn(lease, turn_id, content, last_response.status, pending, cursor)
            return MessageHandleResult(
                last_response.status,
                last_response.thread_id,
                last_response.turn_id,
                last_response.message_id,
                last_response.response,
                max(last_response.message_event_cursor, done.seq),
                last_response.pending_approval,
            )
        response = self._assistant(lease, turn_id, message_id, '没有需要执行的新操作。')
        self._finish_current_turn(lease, turn_id, content, response.status, pending, cursor)
        return response

    def _finish_current_turn(
        self,
        lease: MessageLease,
        turn_id: str,
        content: str,
        status: str,
        pending: _PendingBuffer,
        consumed_cursor: int,
    ) -> None:
        self._mark_consumed(lease, pending, consumed_cursor)
        current = next((segment for segment in pending.segments if segment.turn_id == turn_id), None)
        processed_cursor = len(content) if current is None else min(
            len(content),
            max(
                current.processed_cursor,
                current.processed_cursor + max(0, min(consumed_cursor, current.end) - current.start),
            ),
        )
        self.store.finish_turn(lease, turn_id, status=status, request_fingerprint='', processed_cursor=processed_cursor)

    def _mark_consumed(self, lease: MessageLease, pending: _PendingBuffer, consumed_cursor: int) -> None:
        updates: dict[str, int] = {}
        for segment in pending.segments:
            overlap = max(0, min(consumed_cursor, segment.end) - segment.start)
            if overlap > 0:
                updates[segment.turn_id] = max(updates.get(
                    segment.turn_id, segment.processed_cursor), segment.processed_cursor + overlap)
        for segment in pending.segments:
            value = updates.get(segment.turn_id)
            if value is not None:
                self.store.update_turn_cursor(lease, segment.turn_id, min(len(segment.content), value))

    def _planner_context(self, thread_id: str) -> dict[str, Any]:
        return {
            'conversation_working_set': self.store.working_set(thread_id),
            'flow_status': self.flow_status(thread_id),
        }

    def _resolve(self, thread_id: str, intent: PlannerIntent) -> ResolvedIntent:
        case_id = intent.case_id or self._case_id_from_ref(thread_id, intent.case_ref)
        case_ids = intent.case_ids or self._case_ids_from_ref(thread_id, intent.case_ref)
        if case_id:
            allowed = set(flow_case_ids(self.case_count_getter(thread_id)))
            if case_id not in allowed:
                return ResolvedIntent(kind='unsupported', reason=f'unknown case id: {case_id}')
        if case_ids:
            allowed = set(flow_case_ids(self.case_count_getter(thread_id)))
            unknown = [item for item in case_ids if item not in allowed]
            if unknown:
                return ResolvedIntent(kind='unsupported', reason=f'unknown case id: {unknown[0]}')
        return ResolvedIntent(**(intent.model_dump(mode='python') | {'case_id': case_id, 'case_ids': tuple(case_ids)}))

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
            return self._assistant(
                lease,
                turn_id,
                message_id,
                '我可以处理 evo 状态、继续/暂停/取消/重试、读取报告、查看 case 或受控修改 case 字段。',
                final=False)
        if kind == 'status_query':
            status = self.flow_status(lease.thread_id)
            return self._assistant(lease, turn_id, message_id, canonical_json(status), final=False)
        if kind == 'list_failed_cases':
            return self._list_failed_cases(lease, turn_id, message_id)
        if kind in {'read_report_section', 'explain_current_gate'}:
            return self._read_report(lease, turn_id, message_id)
        if kind == 'read_case_result':
            if intent.case_ids:
                return self._clarify(lease, turn_id, message_id, 'v1 仅支持一次读取单个 case，请指定一个 case。')
            if not intent.case_id:
                return self._clarify(lease, turn_id, message_id, '请指定要读取的 case。')
            return self._read_case(lease, turn_id, message_id, intent.case_id)
        if kind in {
                'continue_flow',
                'pause_flow',
                'cancel_flow',
                'retry_failed',
                'rerun_case',
                'patch_artifact'} and not self.has_flow(
                lease.thread_id):
            return self._clarify(lease, turn_id, message_id, 'flow_not_started')
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
        if kind in {'approve_pending', 'reject_pending', 'cancel_pending'}:
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
        if self.store.active_approval(lease.thread_id) is not None:
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
            _validate_patched_artifact(artifact_key, patched)
        except (TypeError, ValueError, JsonPointerException) as exc:
            return self._clarify(lease, turn_id, message_id, f'patch validation failed: {exc}')
        provenance = {
            'patch_source': f'message_turn:{turn_id}',
            'turn_id': turn_id,
            'message_ids': list(context.consumed_message_ids),
            'consumed_prefix_start': context.consumed_prefix_start,
            'consumed_prefix_len': context.consumed_prefix_len,
            'consumed_text': context.consumed_text,
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
                                 'preview': preview,
                                 'provenance': {**provenance,
                                                'request_fingerprint': approval.request_fingerprint,
                                                'command_id': request.command_id},
                                 },
                                turn_id=turn_id,
                                message_id=message_id,
                                )
        return MessageHandleResult(
            'blocked',
            lease.thread_id,
            turn_id,
            message_id,
            '需要确认后执行该修改。',
            self._assistant_event(lease, turn_id, message_id, '需要确认后执行该修改。').seq,
            pending_approval={'approval_token': approval.approval_token, 'preview_hash': approval.preview_hash},
        )

    def _resolve_pending(
            self,
            lease: MessageLease,
            turn_id: str,
            message_id: str,
            kind: str,
            approval_token: str) -> MessageHandleResult:
        approval = self.store.active_approval(lease.thread_id)
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
        request = self._request_from_approval(approval)
        if intent_request_fingerprint(request) != approval.request_fingerprint:
            return self._clarify(lease, turn_id, message_id, 'request_fingerprint mismatch')
        self.store.resolve_approval(lease, approval.approval_token, status='approved',
                                    event_payload={}, turn_id=turn_id, message_id=message_id)
        result = self.flow_getter(lease.thread_id).runtime.execute_intent(request)
        return self._command_response(
            lease, turn_id, message_id, 'approve_pending', {
                'status': result.status, 'reason': result.reason})

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
        self.store.update_working_set(lease, {'selected_cases': selected, 'last_report': 'eval.summary'})
        return self._assistant(lease, turn_id, message_id, canonical_json(view['facts']), final=False)

    def _read_report(self, lease: MessageLease, turn_id: str, message_id: str) -> MessageHandleResult:
        artifact = 'analysis.summary' if self.artifact_reader(lease.thread_id, 'analysis.summary') else 'eval.summary'
        view = self._views(lease.thread_id).view(artifact)
        self.store.append_event(lease, 'artifact_view', view, turn_id=turn_id, message_id=message_id)
        self.store.update_working_set(lease, {'last_report': artifact})
        return self._assistant(lease, turn_id, message_id, view['excerpt'], final=False)

    def _read_case(self, lease: MessageLease, turn_id: str, message_id: str, case_id: str) -> MessageHandleResult:
        artifact = f'eval.judge_result[{case_id}]'
        view = self._views(lease.thread_id).view(artifact)
        self.store.append_event(lease, 'artifact_view', view, turn_id=turn_id, message_id=message_id)
        self.store.update_working_set(lease, {'selected_cases': (case_id,), 'last_case': case_id})
        return self._assistant(lease, turn_id, message_id, view['excerpt'], final=False)

    def _views(self, thread_id: str) -> ArtifactViewService:
        return ArtifactViewService(lambda artifact_id: self.artifact_reader(thread_id, artifact_id))

    def _command_response(self, lease: MessageLease, turn_id: str, message_id: str, kind: str,
                          payload: dict[str, Any], *, final: bool = False) -> MessageHandleResult:
        event = self.store.append_event(lease, 'command_applied', {
                                        'kind': kind, **payload}, turn_id=turn_id, message_id=message_id)
        text = canonical_json({'kind': kind, **payload})
        assistant = self._assistant_event(lease, turn_id, message_id, text)
        status = 'error' if payload.get('status') == 'failed' else 'done'
        if final and status == 'done':
            self.store.append_event(lease, 'done', {'status': 'done'}, turn_id=turn_id, message_id=message_id)
        return MessageHandleResult(status, lease.thread_id, turn_id, message_id, text, max(event.seq, assistant.seq))

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

    @staticmethod
    def _consumed_prefix_len(plan: PlannerOutput, content: str) -> int:
        if plan.consumed_prefix_len and plan.consumed_prefix_len > len(content):
            return -1
        if plan.consumed_text and not content.startswith(plan.consumed_text):
            return -1
        if plan.consumed_text and plan.consumed_prefix_len and len(plan.consumed_text) != plan.consumed_prefix_len:
            return -1
        if plan.consumed_prefix_len:
            return plan.consumed_prefix_len
        if plan.consumed_text:
            return len(plan.consumed_text)
        return 0


def _patch_target(intent: ResolvedIntent) -> tuple[ArtifactKey, str] | None:
    field = intent.field
    target = _PATCH_FIELD_TARGETS.get(field)
    if target is None:
        return None
    artifact_id, pointer = target
    if field in _CASE_PATCH_FIELDS:
        if not intent.case_id:
            return None
        return case_key(artifact_id, intent.case_id), pointer
    return ArtifactKey.of(artifact_id), pointer


def _pending_buffer(rows: list[PendingTurnBuffer]) -> _PendingBuffer:
    text_parts: list[str] = []
    segments: list[_BufferSegment] = []
    cursor = 0
    for row in rows:
        if text_parts:
            text_parts.append('\n')
            cursor += 1
        start = cursor
        text_parts.append(row.remaining)
        cursor += len(row.remaining)
        segments.append(_BufferSegment(row.turn_id, row.message_id, row.content, row.processed_cursor, start, cursor))
    return _PendingBuffer(''.join(text_parts), tuple(segments))


def _segments_for_range(pending: _PendingBuffer, start: int, length: int) -> tuple[_BufferSegment, ...]:
    end = start + length
    return tuple(segment for segment in pending.segments if segment.start < end and segment.end > start)


def _message_id_at_cursor(pending: _PendingBuffer, cursor: int) -> str:
    for segment in pending.segments:
        if segment.start <= cursor < segment.end:
            return segment.message_id
    return ''


def _consumed_message_ids_match(plan: PlannerOutput, segments: tuple[_BufferSegment, ...]) -> bool:
    if not plan.consumed_message_ids:
        return True
    expected = {segment.message_id for segment in segments}
    actual = set(plan.consumed_message_ids)
    return bool(actual) and actual.issubset(expected)


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
    if field == 'evaluation_policy':
        text = str(value or '').strip()
        if text not in _EVALUATION_POLICIES:
            raise ValueError('evaluation_policy must be balanced, answer_only, or retrieval_diagnostic')
        return text
    if field in {'quality_threshold', 'target_mean_delta', 'goodcase_regression_ratio_limit', 'regression_epsilon'}:
        number = float(value)
        if not 0.0 <= number <= 1.0:
            raise ValueError(f'{field} must be between 0 and 1')
        return round(number, 4)
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
        if 'evaluation_policy' in value and str(value.get('evaluation_policy') or '') not in _EVALUATION_POLICIES:
            raise ValueError('eval.policy evaluation_policy is invalid')
        if 'quality_threshold' in value:
            _validate_patch_value('quality_threshold', value.get('quality_threshold'))
    elif artifact.artifact_id == 'abtest.candidate_config':
        if 'primary_metric' in value:
            _validate_patch_value('primary_metric', value.get('primary_metric'))
        for field in ('target_mean_delta', 'goodcase_regression_ratio_limit', 'regression_epsilon'):
            if field in value:
                _validate_patch_value(field, value.get(field))
    else:
        raise ValueError(f'artifact is not patchable: {artifact}')
    return None


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
