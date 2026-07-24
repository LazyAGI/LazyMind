from __future__ import annotations

import asyncio
import hashlib
import json
import time
import uuid
from collections.abc import Mapping
from dataclasses import fields, is_dataclass
from pathlib import Path
from typing import Any, Literal

from filelock import FileLock, Timeout
from pydantic import ValidationError

from evo import artifacts as A
from evo.artifact_flow import ArtifactFlow, FlowSnapshot
from evo.artifact_runtime import ArtifactKey, ArtifactRef, ArtifactRuntimeError

from . import planner
from .actions import ActionExecutor, PreparedAction, intent_catalog
from .config_guard import ConfigValidationError
from .schemas import (
    ClarifyAction,
    ConfirmationAction,
    FinalAction,
    MessageContentRef,
    MessageHistoryResponse,
    MessageRequest,
    MessageTurnResult,
    PendingConfirmation,
    QueryAction,
    parse_planned_action,
)
from .storage import (
    MessageAuditStore,
    MessageBlobStore,
    MessageInProgressError,
    json_bytes,
    message_history,
)


class MessageIntent:
    def __init__(self, root: str | Path, flow: ArtifactFlow) -> None:
        if not isinstance(flow, ArtifactFlow):
            raise TypeError('flow must be ArtifactFlow')
        self.root = Path(root)
        self.flow = flow
        self.audit = MessageAuditStore(self.root)
        self.blobs = MessageBlobStore(self.root)
        self.lock_root = self.root / 'message-store' / 'locks'
        self.lock_root.mkdir(parents=True, exist_ok=True)

    async def run(self, origin: Literal['user', 'auto'], thread_id: str,
                  request: MessageRequest
                  ) -> MessageTurnResult:
        return await _Turn(self, origin, thread_id, request).run()

    def history(self, thread_id: str, page_size: int = 20,
                page_token: str = ''
                ) -> MessageHistoryResponse:
        return message_history(self.root, thread_id, page_size, page_token)

    async def delete_thread(self, thread_id: str) -> None:
        lock = FileLock(str(self.lock_root / f'{_hash(thread_id)[:32]}.lock'))
        await asyncio.to_thread(
            _delete_thread,
            lock,
            self.audit,
            self.blobs,
            thread_id,
        )


async def run_turn(origin: Literal['user', 'auto'], root: str | Path,
                   flow: ArtifactFlow, thread_id: str,
                   request: MessageRequest
                   ) -> MessageTurnResult:
    return await MessageIntent(root, flow).run(origin, thread_id, request)


def _delete_thread(lock: FileLock, audit: MessageAuditStore,
                   blobs: MessageBlobStore, thread_id: str
                   ) -> None:
    with lock:
        audit.delete_thread(thread_id)
        blobs.delete_thread(thread_id)


class _Turn:
    def __init__(self, intent: MessageIntent, origin: str,
                 thread_id: str, request: MessageRequest
                 ) -> None:
        self.intent = intent
        self.origin = origin
        self.thread_id = thread_id
        self.request = request
        self.message_id = request.message_id or f'msg_{uuid.uuid4().hex[:16]}'
        self.turn_id = f'turn_{uuid.uuid4().hex[:16]}'
        self.executor = ActionExecutor(intent.flow, thread_id)
        self.lock = FileLock(str(intent.lock_root / f'{_hash(thread_id)[:32]}.lock'))

    async def run(self) -> MessageTurnResult:
        if not await self.intent.flow.has_run(self.thread_id):
            raise ValueError(f'thread not found: {self.thread_id}')
        try:
            with self.lock.acquire(timeout=0):
                replay = self.intent.audit.begin_turn(
                    self.thread_id,
                    self.turn_id,
                    self.message_id,
                    _hash({'origin': self.origin, 'request': self.request.model_dump()}),
                )
                if replay is not None:
                    return MessageTurnResult.model_validate_json(
                        self.intent.blobs.load(replay, self.thread_id)
                    )
                return await self._handle()
        except Timeout as exc:
            self.intent.audit.abort_turn(self.thread_id, self.turn_id)
            raise MessageInProgressError('thread already has an active message turn') from exc
        except BaseException:
            self.intent.audit.abort_turn(self.thread_id, self.turn_id)
            raise

    async def _handle(self) -> MessageTurnResult:
        request_ref = self._blob('message_received', self.request.model_dump())
        self.intent.audit.record_request_ref(self.thread_id, self.turn_id, request_ref)
        config = await self._run_config()
        context, base_observation, projection = await self._observe()
        try:
            plan = await asyncio.to_thread(
                planner.plan_next_turn,
                context,
                config.get('llm_config') if isinstance(config, Mapping) else {},
            )
        except planner.StructuredPlanError as exc:
            return self._finish(
                'needs_input',
                f'无法解析为结构化意图: {exc}',
                projection,
            )
        self._blob('turn_plan', plan.model_dump())
        projection['active_agenda'] = list(plan.active_agenda)
        action = plan.next_action
        if action is None:
            return self._finish('needs_input', '没有得到可执行的结构化动作', projection)

        pending_ref = self.intent.audit.projection(self.thread_id).get(
            'pending_confirmation_ref'
        )
        if isinstance(action, ConfirmationAction):
            if pending_ref is None:
                return self._finish('needs_input', '当前没有待确认操作', projection)
            return await self._confirm(
                action,
                pending_ref,
                base_observation,
                projection,
                config,
            )
        if pending_ref is not None:
            if plan.user_message_effect not in {'amend', 'replace', 'cancel'}:
                return self._finish(
                    'needs_input',
                    '仍有待确认操作；请先确认、拒绝或明确替换它。',
                    projection,
                )
            projection['pending_confirmation_ref'] = None
        if isinstance(action, (ClarifyAction, FinalAction)):
            decision = 'needs_input' if isinstance(action, ClarifyAction) else 'final'
            return self._finish(
                decision,
                action.message or plan.assistant_text,
                projection,
            )
        try:
            prepared = await self.executor.prepare(action, self.message_id)
        except (ArtifactRuntimeError, ConfigValidationError, ValueError) as exc:
            return self._finish('needs_input', _error_text(exc), projection)
        if prepared.needs_confirmation:
            return self._pending(prepared, base_observation, projection)
        return await self._dispatch(prepared, projection, context, config)

    async def _run_config(self) -> Mapping[str, Any]:
        key = ArtifactKey.scalar(A.RUN_CONFIG)
        record = await self.intent.flow.head(self.thread_id, key)
        if record is None:
            return {}
        value = await self.intent.flow.read(self.thread_id, record.ref)
        return value if isinstance(value, Mapping) else {}

    async def _observe(self) -> tuple[dict[str, Any], MessageContentRef, dict[str, Any]]:
        snapshot = await self.intent.flow.snapshot(self.thread_id)
        observation = _flow_observation(snapshot)
        observation_ref = self._blob('base_observation', observation)
        previous = self.intent.audit.projection(self.thread_id)
        projection = {'last_observation_ref': observation_ref.model_dump()}
        context = {
            'thread_id': self.thread_id,
            'origin': self.origin,
            'user_text': self.request.text,
            'intent_catalog': _jsonable(intent_catalog()),
            'projection': {
                'active_agenda': previous.get('active_agenda') or [],
                'has_pending_confirmation': bool(
                    previous.get('pending_confirmation_ref')
                ),
            },
            'recent_messages': self._recent_messages(),
            'flow_snapshot': observation,
        }
        return context, observation_ref, projection

    async def _confirm(self, action: ConfirmationAction,
                       pending_ref: object,
                       base_observation: MessageContentRef,
                       projection: dict[str, Any],
                       config: Mapping[str, Any]
                       ) -> MessageTurnResult:
        if action.decision in {'reject', 'amend', 'replace'}:
            decision = 'rejected' if action.decision == 'reject' else 'needs_input'
            text = (
                '已拒绝待确认操作'
                if action.decision == 'reject'
                else action.message or '请提供修正后的操作。'
            )
            projection['pending_confirmation_ref'] = None
            return self._finish(decision, text, projection)
        if action.decision == 'unclear':
            return self._finish(
                'needs_input',
                action.message or '请明确确认或拒绝。',
                projection,
            )
        try:
            pending = PendingConfirmation.model_validate(self._load(pending_ref))
            intent = parse_planned_action(self._load(pending.intent_ref.model_dump()))
        except (ValueError, ValidationError) as exc:
            projection['pending_confirmation_ref'] = None
            return self._finish(
                'needs_input',
                f'待确认操作不可恢复，请重新发起: {exc}',
                projection,
            )
        if action.confirmation_token != pending.confirmation_token:
            return self._finish(
                'needs_input',
                f'请使用确认码 {pending.confirmation_token}。',
                projection,
            )
        if (
            pending.expires_at < time.time()
            or pending.base_observation_hash != base_observation.sha256
        ):
            projection['pending_confirmation_ref'] = None
            return self._finish(
                'needs_input',
                '流程状态已变化或确认已过期，请重新发起操作。',
                projection,
            )
        try:
            prepared = await self.executor.prepare(intent, pending.origin_message_id)
        except (ArtifactRuntimeError, ConfigValidationError, ValueError) as exc:
            projection['pending_confirmation_ref'] = None
            return self._finish('needs_input', _error_text(exc), projection)
        projection['pending_confirmation_ref'] = None
        context = {
            'thread_id': self.thread_id,
            'flow_snapshot': _flow_observation(
                await self.intent.flow.snapshot(self.thread_id)
            ),
        }
        return await self._dispatch(prepared, projection, context, config)

    def _pending(self, prepared: PreparedAction,
                 base_observation: MessageContentRef,
                 projection: dict[str, Any]
                 ) -> MessageTurnResult:
        intent_ref = self._blob('pending_intent', prepared.action.model_dump())
        token = f'confirm_{_hash(prepared.command_id)[:16]}'
        pending = PendingConfirmation(
            confirmation_token=token,
            expires_at=time.time() + 86400.0,
            origin_message_id=self.message_id,
            base_observation_hash=base_observation.sha256,
            intent_ref=intent_ref,
        )
        pending_ref = self._blob('pending_confirmation', pending.model_dump())
        projection['pending_confirmation_ref'] = pending_ref.model_dump()
        return self._finish(
            'needs_confirmation',
            f'{prepared.summary}需要确认后执行，确认码 {token}。',
            projection,
            pending_confirmation_ref=pending_ref,
        )

    async def _dispatch(self, prepared: PreparedAction,
                        projection: dict[str, Any],
                        context: Mapping[str, Any],
                        config: Mapping[str, Any]
                        ) -> MessageTurnResult:
        self._blob('prepared_action', {
            'command_id': prepared.command_id,
            'summary': prepared.summary,
            'action': prepared.action.model_dump(),
        })
        try:
            result = await self.executor.execute(prepared)
        except (ArtifactRuntimeError, ConfigValidationError, ValueError) as exc:
            receipt = self._blob('action_receipt', {
                'status': 'rejected',
                'error': _error_text(exc),
            })
            return self._finish(
                'needs_input',
                _error_text(exc),
                projection,
                command_id=prepared.command_id,
                action_receipt_ref=receipt,
            )
        receipt = self._blob('action_receipt', _jsonable(result))
        snapshot = await self.intent.flow.snapshot(self.thread_id)
        observation = _flow_observation(snapshot)
        observation_ref = self._blob('observation', observation)
        projection['last_observation_ref'] = observation_ref.model_dump()
        if isinstance(prepared.action, QueryAction):
            if prepared.action.query == 'progress':
                text = _progress_text(observation)
            else:
                text = await asyncio.to_thread(
                    planner.answer_query,
                    context,
                    _jsonable(result),
                    config.get('llm_config') if isinstance(config, Mapping) else {},
                )
            decision = 'query_answered'
        else:
            text = (
                f'{prepared.summary}已提交；当前流程状态为 {snapshot.status}，'
                f'当前阶段为 {snapshot.current_stage}。'
            )
            decision = 'action_executed'
        return self._finish(
            decision,
            text,
            projection,
            command_id=prepared.command_id,
            observation_ref=observation_ref,
            action_receipt_ref=receipt,
        )

    def _finish(self, decision: str, text: str,
                projection: dict[str, Any], **refs: Any
                ) -> MessageTurnResult:
        result = MessageTurnResult(
            thread_id=self.thread_id,
            turn_id=self.turn_id,
            message_id=self.message_id,
            turn_decision=decision,
            assistant_text=text,
            **refs,
        )
        result_ref = self._blob('turn_result', result.model_dump())
        self.intent.audit.finish_turn(
            self.thread_id,
            self.turn_id,
            result_ref,
            projection,
        )
        return result

    def _load(self, ref: object) -> object:
        content_ref = MessageContentRef.model_validate(ref)
        return json.loads(self.intent.blobs.load(content_ref, self.thread_id))

    def _blob(self, kind: str, value: object) -> MessageContentRef:
        return self.intent.blobs.append(
            self.thread_id,
            self.turn_id,
            kind,
            json_bytes(_jsonable(value)),
        )

    def _recent_messages(self) -> list[dict[str, str]]:
        messages = []
        for row in self.intent.audit.recent_turns(self.thread_id, 6):
            if not row['request_ref_json'] or not row['result_ref_json']:
                continue
            request = self._load(json.loads(row['request_ref_json']))
            result = self._load(json.loads(row['result_ref_json']))
            if not isinstance(request, Mapping) or not isinstance(result, Mapping):
                continue
            messages.append({
                'message_id': str(row['message_id']),
                'user_text': str(request.get('text') or '')[:1000],
                'assistant_text': str(result.get('assistant_text') or '')[:1000],
                'turn_decision': str(result.get('turn_decision') or ''),
                'command_id': str(result.get('command_id') or ''),
            })
        return messages


def _flow_observation(snapshot: FlowSnapshot) -> dict[str, Any]:
    pending = snapshot.pending_approval
    return {
        'status': snapshot.status,
        'current_stage': snapshot.current_stage,
        'pending_stage_approval': None if pending is None else pending.stage,
        'stages': [
            {
                'stage': stage.stage,
                'completed': stage.completed,
                'approved': stage.approved,
                'result_ref': _ref_data(stage.result_ref),
                'approval_ref': _ref_data(stage.approval_ref),
            }
            for stage in snapshot.stages
        ],
        'runtime': {
            'status': snapshot.runtime.status,
            'ready_count': snapshot.runtime.ready_count,
            'running': [
                {
                    'operation_id': invocation.operation_id,
                    'partition_key': invocation.partition_key,
                }
                for invocation in snapshot.runtime.running
            ],
            'awaiting_artifacts': [
                _key_data(key) for key in snapshot.runtime.awaiting_artifacts
            ],
        },
    }


def _progress_text(observation: Mapping[str, Any]) -> str:
    status = str(observation.get('status') or 'unknown')
    stage = str(observation.get('current_stage') or '')
    pending = str(observation.get('pending_stage_approval') or '')
    completed = [
        str(item.get('stage'))
        for item in observation.get('stages') or ()
        if isinstance(item, Mapping) and item.get('completed')
    ]
    if pending:
        return f'{pending} 阶段已经完成，正在等待用户批准。'
    if status == 'completed':
        return '当前流程已经完成全部阶段。'
    if status == 'failed':
        return f'当前流程执行失败，停在 {stage} 阶段。'
    if status == 'cancelled':
        return '当前流程已经终止。'
    text = f'当前流程状态为 {status}'
    if stage:
        text += f'，当前阶段为 {stage}'
    if completed:
        text += f'，已完成 {", ".join(completed)}'
    return text + '。'


def _error_text(error: Exception) -> str:
    if isinstance(error, ConfigValidationError):
        return '；'.join(issue.message for issue in error.issues)
    return str(error)


def _ref_data(ref: ArtifactRef | None) -> dict[str, object] | None:
    if ref is None:
        return None
    return {**_key_data(ref.key), 'version': ref.version}


def _key_data(key: ArtifactKey) -> dict[str, str]:
    return {
        'artifact_id': key.artifact_id,
        'partition_key': key.partition_key,
    }


def _hash(value: object) -> str:
    return hashlib.sha256(json_bytes(_jsonable(value))).hexdigest()


def _jsonable(value: object) -> object:
    if hasattr(value, 'model_dump'):
        return value.model_dump(mode='json')
    if is_dataclass(value):
        return {
            field.name: _jsonable(getattr(value, field.name))
            for field in fields(value)
        }
    if isinstance(value, Mapping):
        return {str(key): _jsonable(item) for key, item in value.items()}
    if isinstance(value, (list, tuple, set)):
        return [_jsonable(item) for item in value]
    return value


__all__ = ['MessageIntent', 'run_turn']
