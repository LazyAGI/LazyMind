from __future__ import annotations

from collections.abc import Callable, Mapping
from contextlib import contextmanager
from dataclasses import dataclass
from threading import Event, Thread
import time
import uuid
from typing import Any, Iterator

from evo.artifact_runtime.utils import canonical_json, normalize_json_value

from .models import (
    ApprovalIntent,
    IntentFrame,
    SourceSpan,
    ResolvedIntent,
)
from .planner import LLMCallable, StructuredJSONNextIntentPlanner
from .router import MessageIntentRouter
from .runtime_adapter import (
    MessageCommandContext,
    MessageRuntimePort,
)
from .store import MessageLease, MessageSessionStore, MessageStoreConflict, PendingApproval
DEFAULT_READ_CHARS = 1200
MAX_INTENT_LOOP_STEPS = 6


@dataclass(frozen=True)
class MessageHandleResult:
    status: str
    thread_id: str
    turn_id: str
    message_id: str
    response: str
    message_event_cursor: int
    pending_approval: dict[str, Any] | None = None


class MessageIntentService:
    def __init__(
        self,
        store: MessageSessionStore,
        *,
        has_flow: Callable[[str], bool],
        flow_status: Callable[[str], dict],
        case_count_getter: Callable[[str], int],
        runtime_port: MessageRuntimePort,
        planner: StructuredJSONNextIntentPlanner,
        response_llm: LLMCallable | None = None,
    ) -> None:
        self.store = store
        self.planner = planner
        self.response_llm = response_llm
        self.has_flow = has_flow
        self.flow_status = flow_status
        self.case_count_getter = case_count_getter
        self.commands = runtime_port
        self.router = MessageIntentRouter(
            has_flow=has_flow,
            case_count_getter=case_count_getter,
            selected_cases_getter=self._selected_cases,
            case_ref_resolver=self._resolve_case_ref,
        )

    def handle(
        self,
        thread_id: str,
        payload: dict[str, Any],
    ) -> MessageHandleResult:
        content = str(payload.get('content') or payload.get('message') or '').strip()
        if not content:
            raise ValueError('message content required')
        message_id = str(payload.get('message_id') or f'msg_{uuid.uuid4().hex[:12]}')
        with self.store.lease(thread_id) as lease:
            replay = self._replay_duplicate_message(thread_id, message_id)
            if replay is not None:
                return replay
            try:
                turn_id, _ = self.store.begin_turn(lease, message_id, content)
            except MessageStoreConflict:
                replay = self._replay_duplicate_message(thread_id, message_id)
                if replay is not None:
                    return replay
                raise
            result = self._handle_message_turn(lease, turn_id, message_id, content, payload)
            self.store.finish_turn(lease, turn_id, status=result.status)
            return result

    def handle_typed_intervention(
        self,
        thread_id: str,
        intervention: Mapping[str, Any],
        *,
        command_id: str,
    ) -> MessageHandleResult:
        frame = self._typed_intervention_frame(intervention)
        message_id = f'{command_id}:typed'
        content = f'typed auto intervention: {intervention.get("kind") or "unknown"}'
        with self.store.lease(thread_id, owner_id=f'message-intent-typed:{command_id}') as lease:
            replay = self._replay_duplicate_message(thread_id, message_id)
            if replay is not None:
                return replay
            turn_id, _ = self.store.begin_turn(lease, message_id, content)
            self.store.append_event(
                lease,
                'intent_parsed',
                {'status': 'next_ops', 'current': frame.model_dump(mode='json'), 'typed': True},
                turn_id=turn_id,
                message_id=message_id,
            )
            result = self._handle_frame(lease, turn_id, message_id, frame, update_agenda=None)
            self.store.finish_turn(lease, turn_id, status=result.status)
            return result

    def subscribe_events(self, thread_id: str, since: int = 0) -> list[dict[str, Any]]:
        return [self._event_payload(event) for event in self.store.scan_events(thread_id, since)]

    def turn_events(self, thread_id: str, turn_id: str, message_id: str) -> list[dict[str, Any]]:
        return [self._event_payload(event) for event in self.store.turn_events(thread_id, turn_id, message_id)]

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
        frame = IntentFrame(
            intent=ApprovalIntent(kind='approval', action=action, args={'approval_token': approval_token}),
            source=SourceSpan(text=f'structured approval action: {action}'),
            confidence=1.0,
            reason='structured approval action',
        )
        return self.handle_typed_intervention(
            thread_id,
            {
                'kind': 'approval', 'action': action, 'approval_token': approval_token,
                '_frame': frame.model_dump(mode='json')
            },
            command_id=command_id,
        )

    def probe_resolving_approval(self, thread_id: str, *, approval_token: str) -> dict[str, Any]:
        with self.store.lease(thread_id, owner_id=f'message-intent-probe:{approval_token}') as lease:
            approval = self._active_approval(lease)
            if approval is None:
                return {'status': 'done', 'reason': 'no_active_approval'}
            if approval.approval_token != approval_token:
                return {'status': 'conflict', 'reason': 'approval_token mismatch'}
            if approval.status != 'resolving':
                return {'status': approval.status, 'reason': 'approval_not_resolving'}
            result = self.commands.execute_approval(
                thread_id,
                command_id=approval.command_id,
                prepared_payload=approval.prepared_payload,
                expected_fingerprint=approval.request_fingerprint,
            )
            status = str(result.get('status') or '')
            if status == 'in_progress':
                return {'status': 'in_progress', 'reason': str(result.get('reason') or '')}
            if status == 'conflict':
                self.store.reopen_approval(
                    lease,
                    approval.approval_token,
                    event_payload={'reason': str(result.get('reason') or 'conflict')},
                )
                return {'status': 'conflict', 'reason': str(result.get('reason') or 'conflict')}
            final = 'approved' if status == 'applied' else 'cancelled'
            self.store.resolve_approval(
                lease,
                approval.approval_token,
                status=final,
                event_payload={'reason': str(result.get('reason') or status)},
            )
            return {'status': status or final, 'reason': str(result.get('reason') or status or final)}

    def _handle_message_turn(
        self,
        lease: MessageLease,
        turn_id: str,
        message_id: str,
        content: str,
        payload: Mapping[str, Any],
    ) -> MessageHandleResult:
        last_result: MessageHandleResult | None = None
        next_text = content
        for loop_index in range(MAX_INTENT_LOOP_STEPS):
            prior_agenda = self.store.active_agenda(lease.thread_id)
            try:
                planner_context = self._planner_context(lease, turn_id, message_id, payload)
                plan = self.planner.plan(
                    next_text,
                    message_id=message_id,
                    working_set=planner_context,
                    active_agenda=prior_agenda,
                )
            except ValueError as exc:
                self.store.set_active_agenda(lease, prior_agenda)
                self._set_blocked_intent(lease, next_text or prior_agenda, None, f'planner_failed: {exc}')
                return self._clarify(
                    lease,
                    turn_id,
                    message_id,
                    '我没能可靠解析这条消息要执行的 evo 操作，请换个更明确的说法。',
                )

            self.store.append_event(
                lease,
                'intent_parsed',
                {'loop_index': loop_index, **plan.model_dump(mode='json')},
                turn_id=turn_id,
                message_id=message_id,
            )
            self.store.set_active_agenda(lease, plan.active_agenda.text)
            if plan.status == 'done':
                self.store.clear_blocked_intent(lease)
                return last_result or self._assistant(lease, turn_id, message_id, '没有需要处理的新操作。')
            if plan.status == 'clarification':
                self._set_blocked_intent(lease, next_text or prior_agenda,
                                         None if plan.current is None else plan.current,
                                         plan.clarification,
                                         )
                return self._clarify(lease, turn_id, message_id, plan.clarification)
            if plan.current is None:
                self._set_blocked_intent(lease, next_text or prior_agenda, None, 'planner did not provide this intent')
                return self._clarify(lease, turn_id, message_id, '我没能可靠解析这条消息要执行的 evo 操作，请换个更明确的说法。')

            last_result = self._handle_frame(
                lease,
                turn_id,
                message_id,
                plan.current,
                update_agenda=plan.active_agenda.text,
                intent_index=loop_index,
            )
            if last_result.status != 'done' or not plan.active_agenda.text.strip():
                return last_result
            next_text = ''

        remaining = self.store.active_agenda(lease.thread_id)
        self._set_blocked_intent(lease, remaining, None, 'message_intent loop limit reached')
        return self._clarify(
            lease,
            turn_id,
            message_id,
            f'本轮已连续处理 {MAX_INTENT_LOOP_STEPS} 个操作，剩余内容已保留，请确认是否继续：{remaining}',
        )

    def _handle_frame(
        self,
        lease: MessageLease,
        turn_id: str,
        message_id: str,
        frame: IntentFrame,
        *,
        update_agenda: str | None,
        intent_index: int = 0,
    ) -> MessageHandleResult:
        approval = self._active_approval(lease, turn_id, message_id)
        resolved = self.router.resolve(lease.thread_id, frame)
        gate = self.router.gate(
            lease.thread_id,
            frame,
            has_active_approval=approval is not None,
            active_approval_token='' if approval is None else approval.approval_token,
            flow_status=self.flow_status(lease.thread_id),
            resolved=resolved,
        )
        if gate:
            if update_agenda is not None:
                self.store.set_active_agenda(lease, update_agenda)
            self._set_blocked_intent(lease, frame.source.text, frame, gate)
            return self._clarify(lease, turn_id, message_id, gate)
        result = self._execute(lease, turn_id, message_id, resolved, intent_index=intent_index)
        if result.status in {'done', 'blocked', 'accepted'}:
            self.store.clear_blocked_intent(lease)
        else:
            self._set_blocked_intent(lease, frame.source.text, frame, result.response)
        if update_agenda is not None:
            self.store.set_active_agenda(lease, update_agenda)
        return result

    def _planner_context(
        self,
        lease: MessageLease,
        turn_id: str,
        message_id: str,
        payload: Mapping[str, Any],
    ) -> dict[str, Any]:
        del payload
        working_set = dict(self.store.working_set(lease.thread_id))
        approval = self._active_approval(lease, turn_id, message_id)
        return {
            'active_agenda': self.store.active_agenda(lease.thread_id),
            'blocked_current_intent': working_set.get('blocked_current_intent'),
            'conversation_working_set': working_set,
            'flow_status': self.flow_status(lease.thread_id),
            'active_approval': None if approval is None else {
                'approval_token': approval.approval_token,
                'intent_kind': approval.intent_kind,
                'risk_level': approval.risk_level,
                'status': approval.status,
                'expires_at': approval.expires_at,
            },
            'recent_actions': self.store.recent_events(
                lease.thread_id,
                ('intent_parsed', 'command_applied', 'confirmation_required', 'approval_resolved'),
                limit=8,
            ),
        }

    def _execute(
        self,
        lease: MessageLease,
        turn_id: str,
        message_id: str,
        intent: ResolvedIntent,
        *,
        intent_index: int = 0,
    ) -> MessageHandleResult:
        context = MessageCommandContext(turn_id=turn_id, intent_index=intent_index)
        kind = intent.kind
        if kind == 'unsupported':
            return self._clarify(lease, turn_id, message_id, intent.reason or '暂不支持该 evo 操作。')
        if kind == 'no_action_ack':
            return self._assistant_from_tool_result(
                lease,
                turn_id,
                message_id,
                kind=kind,
                tool_result={'message': intent.raw_args.get('summary') or '已理解，不需要执行额外操作。'},
                fallback=str(intent.raw_args.get('summary') or '已理解，不需要执行额外操作。'),
            )
        if kind == 'chat':
            return self._assistant_from_tool_result(
                lease,
                turn_id,
                message_id,
                kind=kind,
                tool_result={'topic': intent.raw_args.get('topic') or '', 'message': '当前 evo 服务没有外部实时查询工具。'},
                fallback='当前 evo 服务没有外部实时查询工具；我可以继续处理剩余的 evo 请求。',
            )
        if kind == 'status_query':
            return self._assistant_from_tool_result(
                lease,
                turn_id,
                message_id,
                kind=kind,
                tool_result=self.flow_status(lease.thread_id),
                fallback=_natural_fallback(kind, self.flow_status(lease.thread_id)),
                final=False,
            )
        if kind == 'read_report_section':
            return self._read_report(lease, turn_id, message_id, intent)
        if kind == 'read_case_result':
            if intent.case_ids:
                return self._clarify(lease, turn_id, message_id, '当前仅支持一次读取单个 case，请指定一个 case。')
            if not intent.case_id:
                return self._clarify(lease, turn_id, message_id, '请指定要读取的 case。')
            return self._read_case(lease, turn_id, message_id, intent)
        if kind == 'continue_flow':
            return self._command_response(
                lease, turn_id, message_id, kind,
                self.commands.continue_flow(lease.thread_id, context)
            )
        if kind == 'pause_flow':
            return self._command_response(
                lease, turn_id, message_id, kind,
                self.commands.pause_flow(lease.thread_id, context)
            )
        if kind == 'cancel_flow':
            return self._command_response(
                lease, turn_id, message_id, kind,
                self.commands.cancel_flow(lease.thread_id, context)
            )
        if kind == 'retry_failed':
            return self._command_response(
                lease, turn_id, message_id, kind,
                self.commands.retry_failed(lease.thread_id, context)
            )
        if kind == 'rerun_case':
            return self._command_response(
                lease,
                turn_id,
                message_id,
                kind,
                self.commands.rerun_case(lease.thread_id, intent.case_id, context),
            )
        if kind == 'patch_artifact':
            return self._patch_artifact(lease, turn_id, message_id, intent, context)
        if kind in {'approve_pending', 'reject_pending', 'cancel_pending'}:
            return self._resolve_pending(lease, turn_id, message_id, kind, intent.approval_token)
        return self._clarify(lease, turn_id, message_id, f'暂不支持该操作：{kind}')

    def _patch_artifact(
        self,
        lease: MessageLease,
        turn_id: str,
        message_id: str,
        intent: ResolvedIntent,
        context: MessageCommandContext,
    ) -> MessageHandleResult:
        provenance = {
            'source': 'message_intent',
            'turn_id': turn_id,
            'message_id': message_id,
            'artifact_ref': intent.artifact_id,
            'json_pointer': intent.json_pointer,
        }
        try:
            prepared = self.commands.prepare_patch(
                lease.thread_id,
                artifact_ref=intent.artifact_id,
                json_pointer=intent.json_pointer,
                patch_value=intent.value,
                provenance=provenance,
                context=context,
            )
        except (TypeError, ValueError) as exc:
            return self._clarify(lease, turn_id, message_id, f'产物修改校验失败：{exc}')
        try:
            approval = self.store.put_pending_approval(
                lease,
                approval_token=f'appr_{uuid.uuid4().hex[:12]}',
                command_id=str(prepared['command_id']),
                run_id=str(prepared['run_id']),
                intent_kind=str(prepared['intent_kind']),
                prepared_payload=prepared['prepared_payload'],
                request_fingerprint=prepared['request_fingerprint'],
                preview_hash=prepared['preview_hash'],
                expected_refs=tuple(str(item) for item in prepared['expected_refs']),
                risk_level='medium',
                expires_at=time.time() + 3600,
            )
        except MessageStoreConflict:
            return self._clarify(lease, turn_id, message_id, '已有待确认操作，请先确认或取消后再发起新的修改。')
        event = self.store.append_event(
            lease,
            'confirmation_required',
            {
                'approval_token': approval.approval_token,
                'request_fingerprint': approval.request_fingerprint,
                'preview_hash': approval.preview_hash,
                'expected_refs': list(approval.expected_refs),
                'risk_level': approval.risk_level,
                'patch_preview': prepared['patch_preview'],
                'preview': prepared['preview'],
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
                'patch_preview': prepared['patch_preview'],
                'preview': prepared['preview'],
            },
            fallback=_patch_confirmation_fallback(approval.approval_token, prepared['patch_preview']),
        )
        assistant = self._assistant_event(lease, turn_id, message_id, response)
        return MessageHandleResult(
            'blocked',
            lease.thread_id,
            turn_id,
            message_id,
            response,
            max(event.seq, assistant.seq),
            pending_approval={'approval_token': approval.approval_token, 'preview_hash': approval.preview_hash},
        )

    def _resolve_pending(
        self,
        lease: MessageLease,
        turn_id: str,
        message_id: str,
        kind: str,
        approval_token: str,
    ) -> MessageHandleResult:
        approval = self._active_approval(lease, turn_id, message_id)
        if approval is None:
            return self._clarify(lease, turn_id, message_id, '没有待确认操作。')
        if approval_token and approval.approval_token != approval_token:
            return self._clarify(lease, turn_id, message_id, 'approval_token mismatch')
        if kind in {'reject_pending', 'cancel_pending'}:
            if approval.status == 'resolving':
                return self._clarify(lease, turn_id, message_id, '该待确认操作已经开始执行，无法取消。')
            status = 'rejected' if kind == 'reject_pending' else 'cancelled'
            self.store.resolve_approval(
                lease, approval.approval_token, status=status, event_payload={},
                turn_id=turn_id, message_id=message_id
            )
            return self._assistant(lease, turn_id, message_id, '已取消待确认操作。')
        with self._lease_keepalive(lease):
            if approval.status == 'active':
                try:
                    stale = self.commands.stale_expected_refs(lease.thread_id, approval.expected_refs)
                except RuntimeError as exc:
                    return self._command_response(
                        lease,
                        turn_id,
                        message_id,
                        'approve_pending',
                        {'status': 'conflict', 'reason': str(exc) or 'flow operation is busy'},
                    )
                if stale:
                    self.store.resolve_approval(
                        lease,
                        approval.approval_token,
                        status='cancelled',
                        event_payload={'reason': 'stale_expected_ref', 'stale_refs': stale},
                        turn_id=turn_id,
                        message_id=message_id,
                    )
                    return self._clarify(lease, turn_id, message_id, '待修改产物已变化，请重新发起修改。')
                approval = self.store.begin_approval_resolution(
                    lease,
                    approval.approval_token,
                    turn_id=turn_id,
                    message_id=message_id,
                )
            result = self.commands.execute_approval(
                lease.thread_id,
                command_id=approval.command_id,
                prepared_payload=approval.prepared_payload,
                expected_fingerprint=approval.request_fingerprint,
            )
        if str(result.get('status') or '') == 'conflict':
            if approval.status == 'resolving':
                self.store.reopen_approval(
                    lease,
                    approval.approval_token,
                    event_payload={'reason': str(result.get('reason') or 'conflict')},
                    turn_id=turn_id,
                    message_id=message_id,
                )
            return self._command_response(lease, turn_id, message_id, 'approve_pending', result)
        if str(result.get('status') or '') == 'in_progress':
            return self._command_response(lease, turn_id, message_id, 'approve_pending', result)
        status = 'approved' if result.get('status') == 'applied' else 'cancelled'
        self.store.resolve_approval(
            lease,
            approval.approval_token,
            status=status,
            event_payload={'reason': str(result.get('reason') or result.get('status') or '')},
            turn_id=turn_id,
            message_id=message_id,
        )
        return self._command_response(lease, turn_id, message_id, 'approve_pending', result)

    def _read_report(
        self,
        lease: MessageLease,
        turn_id: str,
        message_id: str,
        intent: ResolvedIntent
    ) -> MessageHandleResult:
        args = intent.raw_args
        payload = dict(self.commands.read_report_section(
            lease.thread_id,
            artifact_ref=intent.artifact_id,
            selector=str(args.get('section') or args.get('selector') or ''),
            cursor=str(args.get('cursor') or ''),
            max_chars=_int_arg(args.get('max_chars'), DEFAULT_READ_CHARS),
            context=MessageCommandContext(turn_id=turn_id),
        ))
        patch = payload.pop('working_set_patch', None)
        if isinstance(patch, Mapping):
            self.store.update_working_set(lease, patch)
        self.store.append_event(lease, 'artifact_view', payload, turn_id=turn_id, message_id=message_id)
        return self._assistant_from_tool_result(
            lease,
            turn_id,
            message_id,
            kind='read_report_section',
            tool_result=payload,
            fallback=_natural_fallback('read_report_section', payload),
            final=False,
        )

    def _read_case(
        self,
        lease: MessageLease,
        turn_id: str,
        message_id: str,
        intent: ResolvedIntent
    ) -> MessageHandleResult:
        args = intent.raw_args
        payload = dict(self.commands.read_case_result(
            lease.thread_id,
            case_id=intent.case_id,
            selector=str(args.get('selector') or ''),
            cursor=str(args.get('cursor') or ''),
            max_chars=_int_arg(args.get('max_chars'), DEFAULT_READ_CHARS),
            context=MessageCommandContext(turn_id=turn_id),
        ))
        patch = payload.pop('working_set_patch', None)
        if isinstance(patch, Mapping):
            self.store.update_working_set(lease, patch)
        self.store.append_event(lease, 'artifact_view', payload, turn_id=turn_id, message_id=message_id)
        return self._assistant_from_tool_result(
            lease,
            turn_id,
            message_id,
            kind='read_case_result',
            tool_result=payload,
            fallback=_natural_fallback('read_case_result', payload),
            final=False,
        )

    def _command_response(
        self,
        lease: MessageLease,
        turn_id: str,
        message_id: str,
        kind: str,
        payload: Mapping[str, Any],
    ) -> MessageHandleResult:
        event = self.store.append_event(
            lease,
            'command_applied',
            {'kind': kind, **dict(payload)},
            turn_id=turn_id,
            message_id=message_id,
        )
        text = self._synthesize_response(
            lease.thread_id,
            turn_id,
            message_id,
            kind=kind,
            tool_result={'kind': kind, **dict(payload)},
            fallback=_natural_fallback(kind, payload),
        )
        assistant = self._assistant_event(lease, turn_id, message_id, text)
        payload_status = str(payload.get('status') or '')
        if payload_status in {'accepted', 'accepted_existing', 'in_progress'}:
            status = 'accepted'
        elif payload_status == 'failed':
            status = 'error'
        else:
            status = 'done'
        terminal = self.store.append_event(lease, 'done', {'status': status}, turn_id=turn_id, message_id=message_id)
        return MessageHandleResult(
            status, lease.thread_id, turn_id, message_id, text,
            max(event.seq, assistant.seq, terminal.seq)
        )

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
        return self._assistant(
            lease,
            turn_id,
            message_id,
            self._synthesize_response(
                lease.thread_id, turn_id, message_id,
                kind=kind, tool_result=tool_result, fallback=fallback
            ),
            final=final,
        )

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
        try:
            raw = self.response_llm(_response_prompt(thread_id, turn_id, message_id, kind, tool_result))
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
        final: bool = True,
    ) -> MessageHandleResult:
        event = self._assistant_event(lease, turn_id, message_id, content)
        if final:
            self.store.append_event(lease, 'done', {'status': 'done'}, turn_id=turn_id, message_id=message_id)
        return MessageHandleResult('done', lease.thread_id, turn_id, message_id, content, event.seq)

    def _clarify(self, lease: MessageLease, turn_id: str, message_id: str, content: str) -> MessageHandleResult:
        event = self.store.append_event(
            lease,
            'clarification_required',
            {'content': content},
            turn_id=turn_id,
            message_id=message_id
        )
        assistant = self._assistant_event(lease, turn_id, message_id, content)
        return MessageHandleResult(
            'clarification',
            lease.thread_id,
            turn_id,
            message_id,
            content,
            max(event.seq, assistant.seq),
        )

    def _assistant_event(self, lease: MessageLease, turn_id: str, message_id: str, content: str):
        return self.store.append_event(
            lease,
            'assistant_response',
            {'content': content},
            turn_id=turn_id,
            message_id=message_id,
        )

    def _active_approval(self, lease: MessageLease, turn_id: str = '', message_id: str = '') -> PendingApproval | None:
        approval = self.store.active_approval(lease.thread_id)
        if approval is None:
            return None
        if approval.status == 'resolving':
            return approval
        if approval.expires_at > time.time():
            return approval
        self.store.expire_approval(lease, approval.approval_token, turn_id=turn_id, message_id=message_id)
        return None

    @contextmanager
    def _lease_keepalive(self, lease: MessageLease) -> Iterator[None]:
        self.store.heartbeat(lease)
        stop = Event()

        def beat() -> None:
            while not stop.wait(max(1.0, min(30.0, self.store.lease_seconds / 3.0))):
                self.store.heartbeat(lease)

        thread = Thread(target=beat, daemon=True)
        thread.start()
        try:
            yield
        finally:
            stop.set()
            thread.join(timeout=1.0)
            self.store.heartbeat(lease)

    def _set_blocked_intent(
        self,
        lease: MessageLease,
        source: str,
        frame: IntentFrame | None,
        reason: str,
    ) -> None:
        self.store.set_blocked_intent(lease, {
            'source_message': str(source or '').strip(),
            'current': None if frame is None else frame.model_dump(mode='json'),
            'reason': str(reason or '').strip(),
            'created_at': time.time(),
        })

    def _selected_cases(self, thread_id: str) -> tuple[str, ...]:
        return tuple(
            str(item)
            for item in self.store.working_set(thread_id).get('selected_cases') or ()
            if str(item).strip()
        )

    def _resolve_case_ref(self, case_ref: str) -> str:
        text = str(case_ref or '').strip()
        if text in {'selected_cases', 'these_cases'}:
            return 'selected_cases'
        if text.startswith('case_') and text[5:].isdigit():
            return f'case_{int(text[5:]):04d}'
        return ''

    def _typed_intervention_frame(self, intervention: Mapping[str, Any]) -> IntentFrame:
        if '_frame' in intervention and isinstance(intervention['_frame'], Mapping):
            return IntentFrame.model_validate(intervention['_frame'])
        raise ValueError('typed intervention requires an IntentFrame payload')

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

    @staticmethod
    def _event_payload(event) -> dict[str, Any]:
        return {
            'id': str(event.seq), 'event': event.event_type,
            'data': {'seq': event.seq, 'type': event.event_type, **event.payload},
        }


def _response_prompt(thread_id: str, turn_id: str, message_id: str, kind: str, tool_result: Mapping[str, Any]) -> str:
    return (
        'You are the user-facing response writer for an Evo message agent. '
        'The operation has already been parsed, gated, and executed or prepared. '
        'Write concise Chinese for the user. Do not output raw JSON. '
        'Use only the tool result; do not invent facts. '
        'A flow stage_gate checkpoint is flow control, not an approval; do not call checkpoint_id an approval token. '
        'For stage_gate status, say the flow is waiting for confirmation to continue; '
        'do not expose internal checkpoint_id unless asked. '
        'For pending approvals, clearly mention the approval token and exact previewed change.\n\n'
        f'Thread id: {thread_id}\nTurn id: {turn_id}\nMessage id: {message_id}\nOperation: {kind}\n'
        f'Tool result JSON:\n{canonical_json(normalize_json_value(dict(tool_result), allow_tuple=True))}'
    )


def _natural_fallback(kind: str, payload: Mapping[str, Any]) -> str:
    if str(payload.get('status') or '') in {'accepted', 'accepted_existing'}:
        events_url = str(payload.get('events_url') or '')
        suffix = f' 后续进度可通过 {events_url} 查看。' if events_url else ' 后续进度可通过事件流查看。'
        return f'{kind} 已受理，正在后台执行。{suffix}'
    if str(payload.get('status') or '') == 'conflict':
        return '当前已有长任务正在运行，本次操作没有执行。请先等待当前任务完成，或通过事件流查看进度。'
    if kind == 'status_query':
        status = str(payload.get('status') or 'unknown')
        current = str(payload.get('current_step') or '')
        completed = ', '.join(str(item) for item in payload.get('completed_steps') or ())
        detail = f'当前步骤：{current}。' if current else ''
        if completed:
            detail += f' 已完成步骤：{completed}。'
        return f'当前 evo 流程状态是 {status}。{detail}'.strip()
    if kind in {'read_case_result', 'read_report_section'}:
        source = str(payload.get('source_ref') or '当前产物')
        excerpt = str(payload.get('excerpt') or '').strip()
        more = ' 内容已截断，可以继续读取后续部分。' if payload.get('truncated') or payload.get('next_cursor') else ''
        return f'{source} 的读取结果：{excerpt or "暂无可展示内容。"}{more}'
    if kind == 'approve_pending':
        if str(payload.get('status') or '') == 'in_progress':
            return '待确认操作已经开始执行，当前仍在处理中；可以稍后再查看或再次确认以补写结果。'
        return f'待确认操作处理结果：{payload.get("status") or "unknown"}。{payload.get("reason") or ""}'.strip()
    status = str(payload.get('status') or 'done')
    current = str(payload.get('current_step') or '')
    return f'{kind} 已处理，状态：{status}。' + (f' 当前步骤：{current}。' if current else '')


def _patch_confirmation_fallback(approval_token: str, preview: Mapping[str, Any]) -> str:
    return (
        '需要确认后执行该修改：'
        f'{preview.get("target_artifact")} {preview.get("json_pointer")} '
        f'{preview.get("old_value")!r} -> {preview.get("new_value")!r}。'
        f'确认令牌：{approval_token}'
    )


def _int_arg(value: Any, default: int) -> int:
    try:
        return int(value)
    except (TypeError, ValueError):
        return default
