from __future__ import annotations

import hashlib
import json
import time
import uuid
from collections.abc import Callable, Mapping
from dataclasses import fields, is_dataclass
from pathlib import Path
from typing import Any, Literal

from filelock import FileLock, Timeout
from pydantic import ValidationError

from evo.artifact_flow import commands as F
from evo.artifact_runtime.evo import actions as E
from evo.artifact_runtime.kernel import ArtifactKey, ArtifactRef

from .config_guard import ConfigValidationError, validate_config_patch
from .planner import StructuredPlanError, answer_query, plan_next_turn
from .schemas import MessageContentRef, MessageRequest, MessageTurnResult, PendingApproval, PlannedAction
from .schemas import parse_planned_action
from .storage import MessageAuditStore, MessageBlobStore, MessageInProgressError, json_bytes


def run_turn(
    origin: Literal['user', 'auto'],
    root: Path,
    runtime: Any,
    flow_runner: Callable[[str, Mapping[str, Any], object], object],
    thread_id: str,
    request: MessageRequest,
) -> MessageTurnResult:
    return _Turn(origin, root, runtime, flow_runner, thread_id, request).run()


class _Turn:
    def __init__(self, origin: str, root: Path, runtime: Any,
                 flow_runner: Callable[[str, Mapping[str, Any], object], object],
                 thread_id: str, request: MessageRequest) -> None:
        self.origin = origin
        self.runtime = runtime
        self.flow_runner = flow_runner
        self.thread_id = thread_id
        self.request = request
        self.message_id = request.message_id or f'msg_{uuid.uuid4().hex[:16]}'
        self.turn_id = f'turn_{uuid.uuid4().hex[:16]}'
        self.audit = MessageAuditStore(root)
        self.blobs = MessageBlobStore(root)
        lock_root = root / 'message-store' / 'locks'
        lock_root.mkdir(parents=True, exist_ok=True)
        self.lock = FileLock(str(lock_root / f'{_hash(thread_id)[:32]}.lock'))

    def run(self) -> MessageTurnResult:
        config = self.runtime.run_config(self.thread_id)
        if config is None:
            raise ValueError(f'thread not found: {self.thread_id}')
        try:
            with self.lock.acquire(timeout=0):
                replay = self.audit.begin_turn(
                    self.thread_id, self.turn_id, self.message_id, _hash(self.request.model_dump()),
                )
                if replay is not None:
                    return MessageTurnResult.model_validate_json(self.blobs.load(replay, self.thread_id))
                result = self._handle(config)
                return result
        except Timeout as exc:
            self.audit.abort_turn(self.thread_id, self.turn_id)
            raise MessageInProgressError('thread already has an active message turn') from exc
        except Exception:
            self.audit.abort_turn(self.thread_id, self.turn_id)
            raise

    def _handle(self, config: Mapping[str, Any]) -> MessageTurnResult:
        self.audit.record_request_ref(self.thread_id, self.turn_id,
                                      self._blob('message_received', self.request.model_dump()))
        context, base_obs, projection = self._observe(config)
        try:
            plan = plan_next_turn(context, config.get('llm_config') if isinstance(config, Mapping) else {})
        except StructuredPlanError as exc:
            return self._finish('needs_input', f'无法解析为结构化意图: {exc}', projection)
        self._blob('turn_plan', plan.model_dump())
        action = plan.next_action
        projection['active_agenda'] = list(plan.active_agenda)
        if action is None:
            return self._finish('needs_input', '模型没有给出可执行的结构化动作', projection)
        pending = self.audit.projection(self.thread_id).get('pending_approval_ref')
        if pending and action.kind != 'approval' and plan.user_message_effect not in {'amend', 'replace'}:
            return self._finish('needs_input', '仍有待确认操作；请明确确认、取消或修正。', projection)
        if pending and plan.user_message_effect in {'amend', 'replace'}:
            projection['pending_approval_ref'] = None
        if action.kind in {'clarify', 'final'}:
            return self._clarify_or_final(action, plan, projection)
        if action.kind == 'approval':
            return self._approval(action, base_obs, projection, plan.assistant_text)
        compiled_result = self._compile_with_reflection(action, context, config, projection)
        if isinstance(compiled_result, MessageTurnResult):
            return compiled_result
        compiled, approved_action = compiled_result
        if compiled['needs_approval']:
            return self._pending_approval(approved_action, compiled, base_obs, projection)
        return self._dispatch(compiled, config, projection, context, plan.assistant_text)

    def _observe(self, config: Mapping[str, Any]):
        num_case = _num_case(config)
        snapshot = self.runtime.query(num_case).snapshot(self.thread_id)
        flow_snapshot = {
            'status': snapshot.status,
            'pending_checkpoint': _safe(snapshot.pending_checkpoint),
            'checkpoint': _safe(snapshot.checkpoint),
            'progress': _safe(snapshot.progress),
        }
        base_obs = self._blob('base_observation', flow_snapshot)
        old = self.audit.projection(self.thread_id)
        projection = {
            'last_observation_ref': base_obs.model_dump(),
        }
        context = {
            'thread_id': self.thread_id,
            'origin': self.origin,
            'user_text': self.request.text,
            'projection': {
                'active_agenda': old.get('active_agenda') or [],
                'has_pending_approval': bool(old.get('pending_approval_ref')),
            },
            'recent_messages': self._recent_messages(),
            'flow_snapshot': flow_snapshot,
        }
        return context, base_obs, projection

    def _compile_with_reflection(self, action: PlannedAction, context: Mapping[str, Any],
                                 config: Mapping[str, Any],
                                 projection: dict[str, Any]) -> tuple[dict[str, Any], PlannedAction] | MessageTurnResult:
        try:
            return self._compile(action, config), action
        except ConfigValidationError as exc:
            if action.kind != 'config_patch':
                return self._finish('needs_input', _issues(exc.issues), projection)
            reflected = {**context, 'config_validation_issues': [i.model_dump() for i in exc.issues],
                         'failed_action': action.model_dump()}
            try:
                plan = plan_next_turn(reflected, config.get('llm_config') if isinstance(config, Mapping) else {})
            except StructuredPlanError as retry_exc:
                return self._finish('needs_input', f'配置校验失败且反思修正失败: {retry_exc}', projection)
            self._blob('turn_plan_reflected', plan.model_dump())
            next_action = plan.next_action
            if next_action is None or next_action.kind == 'clarify':
                text = getattr(next_action, 'message', '') if next_action is not None else ''
                return self._finish('needs_input', text or _issues(exc.issues), projection)
            if next_action.kind != 'config_patch':
                return self._finish('needs_input', '配置修正只能返回 corrected config_patch 或 clarification', projection)
            try:
                return self._compile(next_action, config), next_action
            except (ConfigValidationError, ValueError) as retry_exc:
                text = _issues(retry_exc.issues) if isinstance(retry_exc, ConfigValidationError) else str(retry_exc)
                return self._finish('needs_input', text, projection)
        except ValueError as exc:
            return self._finish('needs_input', f'结构化意图参数无效: {exc}', projection)

    def _compile(self, action: PlannedAction, config: Mapping[str, Any],
                 source_message_id: str = '') -> dict[str, Any]:
        num_case = _num_case(config)
        spec = self.runtime.spec(num_case)
        source = source_message_id or self.message_id
        command_id = f'msgi:{self.thread_id}:{source}:{_hash(action.model_dump())[:24]}'
        payload: dict[str, Any]
        needs_approval = False
        if action.kind == 'query':
            payload = action.model_dump()
            kind = 'query'
        elif action.kind == 'flow':
            if action.until_step:
                spec._require_step(action.until_step)
            payload = {'kind': 'flow', 'command': action.command, 'until_step': action.until_step}
            kind = 'flow'
            needs_approval = action.command == 'cancel' or bool(action.until_step)
        elif action.kind == 'config_patch':
            ref, current = self.runtime.config_artifact(self.thread_id, str(action.target)) or (None, None)
            if ref is None:
                raise ValueError(f'config artifact is not available: {action.target}')
            ref, pointer, value = validate_config_patch(self.thread_id, action, ref, current)
            payload = {'kind': 'mutation', 'mutation': 'edit_artifact', 'artifact_ref': _ref_json(ref),
                       'pointer': pointer, 'value': value, 'config_target': action.target}
            return self._compiled('mutation', payload, True, command_id)
        else:
            payload = self._mutation_payload(spec, action)
            kind = 'mutation'
            needs_approval = True
        return self._compiled(kind, payload, needs_approval, command_id)

    def _mutation_payload(self, spec: Any, action: PlannedAction) -> dict[str, Any]:
        if action.mutation == 'rerun_case_stage':
            spec.rerun_case_stage(action.case_id, action.stage)
        elif action.mutation == 'rerun_step':
            spec.rerun_step(action.step)
        elif action.mutation == 'invalidate_from_step':
            spec.jump_to_step(action.step)
        elif action.mutation == 'edit_artifact':
            spec.edit_target(_artifact_ref(action.artifact_ref), action.pointer)
        else:
            raise ValueError(f'unsupported mutation: {action.mutation}')
        return {'kind': 'mutation', **action.model_dump()}

    def _compiled(self, kind: str, payload: dict[str, Any], needs_approval: bool,
                  command_id: str) -> dict[str, Any]:
        if kind != 'query':
            payload = {**payload, 'command_id': command_id}
        return {'kind': kind, 'command_id': command_id, 'payload': payload, 'needs_approval': needs_approval}

    def _approval(self, action: PlannedAction, base_obs: MessageContentRef,
                  projection: dict[str, Any], reply: str) -> MessageTurnResult:
        if action.decision in {'reject', 'amend', 'replace'}:
            text = '已取消待审批操作' if action.decision == 'reject' else action.message or '请提供修正后的操作。'
            return self._finish('rejected' if action.decision == 'reject' else 'needs_input',
                                text, {**projection, 'pending_approval_ref': None})
        if action.decision == 'unclear':
            return self._finish('needs_input', action.message or '请明确确认或取消。', projection)
        ref = self.audit.projection(self.thread_id).get('pending_approval_ref')
        if not ref:
            return self._finish('needs_input', '没有可确认的待审批操作', projection)
        try:
            pending = PendingApproval.model_validate(self._load(ref))
            compiled = self._load(pending.compiled_ref.model_dump())
            intent = parse_planned_action(self._load(pending.intent_ref.model_dump()))
            if not isinstance(compiled, Mapping):
                raise ValueError('pending compiled action must be an object')
        except (ValueError, ValidationError) as exc:
            return self._finish('needs_input', f'待审批操作不可恢复，请重新发起: {exc}',
                                {**projection, 'pending_approval_ref': None})
        if action.approval_token != pending.approval_token:
            return self._finish('needs_input', f'请带确认码 {pending.approval_token} 重新确认。', projection)
        if pending.expires_at < time.time() or pending.base_observation_hash != base_obs.sha256:
            return self._finish('needs_input', '运行状态已变化或审批已过期，请重新发起。',
                                {**projection, 'pending_approval_ref': None})
        config = self.runtime.run_config(self.thread_id) or {}
        try:
            current = self._compile(intent, config, pending.origin_message_id)
        except (ConfigValidationError, ValueError) as exc:
            text = _issues(exc.issues) if isinstance(exc, ConfigValidationError) else str(exc)
            return self._finish('needs_input', text, {**projection, 'pending_approval_ref': None})
        if _hash(current) != _hash(compiled):
            return self._finish('needs_input', '待审批操作已变化，请重新确认。',
                                {**projection, 'pending_approval_ref': None})
        return self._dispatch(compiled, config, {**projection, 'pending_approval_ref': None}, {}, reply)

    def _pending_approval(self, action: PlannedAction, compiled: Mapping[str, Any], base_obs: MessageContentRef,
                          projection: dict[str, Any]) -> MessageTurnResult:
        intent_ref = self._blob('pending_intent', action.model_dump())
        compiled_ref = self._blob('pending_compiled_action', compiled)
        pending = PendingApproval(
            approval_token=f'appr_{_hash(compiled)[:24]}',
            expires_at=time.time() + 86400.0,
            origin_message_id=self.message_id,
            base_observation_hash=base_obs.sha256,
            intent_ref=intent_ref,
            compiled_ref=compiled_ref,
        )
        pending_ref = self._blob('pending_approval', pending.model_dump())
        return self._finish('needs_approval', _approval_text(compiled, pending.approval_token),
                            {**projection, 'pending_approval_ref': pending_ref.model_dump()},
                            pending_approval_ref=pending_ref)

    def _dispatch(self, compiled: Mapping[str, Any], config: Mapping[str, Any],
                  projection: dict[str, Any], context: Mapping[str, Any], reply: str) -> MessageTurnResult:
        self._blob('compiled_action', compiled)
        command_id = '' if compiled['kind'] == 'query' else str(compiled['command_id'])
        try:
            result = self._read(config, compiled) if compiled['kind'] == 'query' else self._run_command(config, compiled)
        except Exception as exc:
            if getattr(exc, 'status_code', None) is None:
                raise
            detail = str(getattr(exc, 'detail', '') or exc)
            receipt = self._blob('action_receipt', {
                'command_status': 'rejected',
                'status_code': getattr(exc, 'status_code', 0),
                'error': detail,
            })
            obs = self._blob('observation', _safe(self.runtime.query(_num_case(config)).snapshot(self.thread_id)))
            projection.update({'last_observation_ref': obs.model_dump()})
            return self._finish('needs_input', f'操作未提交: {detail}', projection, command_id=command_id,
                                observation_ref=obs, action_receipt_ref=receipt)
        status = str(getattr(result, 'command_status', 'ok'))
        ok = status in {'ok', 'blocked'} if compiled['kind'] != 'query' else True
        decision = 'query_answered' if compiled['kind'] == 'query' else ('action_submitted' if ok else 'needs_input')
        if compiled['kind'] == 'query':
            payload = compiled['payload']
            text = _progress_text(_safe(result)) if payload['query'] == 'progress_snapshot' \
                else answer_query(context, _safe(result), config.get('llm_config') or {})
        elif decision == 'action_submitted':
            text = _action_text(compiled)
        else:
            text = f'操作未完成: {getattr(result, "error", "")}'
        receipt = self._blob('action_receipt', _safe(result))
        obs = self._blob('observation', _safe(self.runtime.query(_num_case(config)).snapshot(self.thread_id)))
        projection.update({'last_observation_ref': obs.model_dump()})
        return self._finish(decision, text, projection, command_id=command_id,
                            observation_ref=obs, action_receipt_ref=receipt)

    def _read(self, config: Mapping[str, Any], compiled: Mapping[str, Any]) -> Any:
        query = self.runtime.query(_num_case(config))
        payload = compiled['payload']
        if payload['query'] == 'progress_snapshot':
            return query.snapshot(self.thread_id)
        if payload['query'] == 'read_step_root':
            return query.read(self.thread_id, E.ReadStepRoot(str(payload['step'])))
        return query.read(self.thread_id, E.ReadCaseArtifact(str(payload['case_id']), str(payload['case_kind'])))

    def _run_command(self, config: Mapping[str, Any], compiled: Mapping[str, Any]) -> Any:
        payload = compiled['payload']
        command_id = str(compiled['command_id'])
        if compiled['kind'] == 'flow':
            command = {
                'continue': F.ContinueFlow(command_id, str(payload.get('until_step') or '')),
                'pause': F.PauseFlow(command_id),
                'resume': F.ResumeFlow(command_id),
                'cancel': F.CancelFlow(command_id),
                'retry': F.RetryFlow(command_id),
            }[str(payload['command'])]
        else:
            mutation = str(payload['mutation'])
            if mutation == 'rerun_case_stage':
                action = E.RerunCaseStage(str(payload['case_id']), str(payload['stage']), command_id)
            elif mutation == 'rerun_step':
                action = E.RerunStep(str(payload['step']), command_id)
            elif mutation == 'invalidate_from_step':
                action = E.InvalidateFromStep(str(payload['step']), command_id)
            else:
                action = E.EditArtifact(_artifact_ref(payload['artifact_ref']), str(payload['pointer']),
                                        payload.get('value'), command_id)
            command = F.ApplyArtifactMutation(command_id, action)
        return self.flow_runner(self.thread_id, config, command)

    def _clarify_or_final(self, action: PlannedAction, plan: Any,
                          projection: dict[str, Any]) -> MessageTurnResult:
        text = action.message
        if action.kind == 'clarify':
            return self._finish('needs_input', text, projection)
        return self._finish('final', text, projection)

    def _finish(self, decision: str, text: str, projection: dict[str, Any], **refs: Any) -> MessageTurnResult:
        result = MessageTurnResult(thread_id=self.thread_id, turn_id=self.turn_id, message_id=self.message_id,
                                   turn_decision=decision, assistant_text=text, **refs)
        self.audit.finish_turn(self.thread_id, self.turn_id, self._blob('turn_result', result.model_dump()),
                               projection)
        return result

    def _load(self, ref: Any) -> Any:
        return json.loads(self.blobs.load(MessageContentRef.model_validate(ref), self.thread_id))

    def _blob(self, kind: str, value: object) -> MessageContentRef:
        return self.blobs.append(self.thread_id, self.turn_id, kind, json_bytes(_safe(value)))

    def _recent_messages(self) -> list[dict[str, str]]:
        items = []
        for row in self.audit.recent_turns(self.thread_id, 6):
            if row['turn_id'] == self.turn_id or not row['request_ref_json'] or not row['result_ref_json']:
                continue
            request = self._load(json.loads(row['request_ref_json']))
            result = self._load(json.loads(row['result_ref_json']))
            items.append({
                'message_id': str(row['message_id']),
                'user_text': str(request.get('text') or '')[:1000] if isinstance(request, Mapping) else '',
                'assistant_text': str(result.get('assistant_text') or '')[:1000] if isinstance(result, Mapping) else '',
                'turn_decision': str(result.get('turn_decision') or '') if isinstance(result, Mapping) else '',
                'command_id': str(result.get('command_id') or '') if isinstance(result, Mapping) else '',
            })
        return items


def _artifact_ref(value: Any) -> ArtifactRef:
    if not isinstance(value, list) or len(value) != 3:
        raise ValueError('artifact_ref must be [artifact_id, partition, version]')
    return ArtifactRef(ArtifactKey(str(value[0]), str(value[1])), int(value[2]))


def _ref_json(ref: ArtifactRef) -> list[object]:
    return [ref.key.artifact_id, ref.key.partition, ref.version]


def _num_case(config: Mapping[str, Any]) -> int:
    return int(config.get('num_case') or (config.get('inputs') or {}).get('num_case') or 0)


def _issues(issues: list[Any]) -> str:
    return '；'.join(str(getattr(issue, 'message', issue)) for issue in issues) or '配置参数无效'


def _progress_text(result: object) -> str:
    data = result if isinstance(result, Mapping) else {}
    status = str(data.get('status') or 'unknown')
    checkpoint = data.get('checkpoint') if isinstance(data.get('checkpoint'), Mapping) else {}
    progress = [item for item in data.get('progress') or [] if isinstance(item, Mapping)]
    done = [str(item.get('step') or '') for item in progress
            if item.get('root_ref') or item.get('effective_outputs')]
    current = str(checkpoint.get('current_step') or '')
    if not current:
        current = next((str(item.get('step') or '') for item in progress if str(item.get('step') or '') not in done), '')
    if status == 'idle' and not done:
        return f'当前线程尚未启动，下一步是 {current or "dataset"}。'
    if status == 'failed':
        if checkpoint.get('checkpoint_state') == 'stale':
            return f'当前流程失败，checkpoint 已失效，已完成步骤: {", ".join(done) if done else "无"}。'
        retry_from = str(checkpoint.get('retry_from_step') or '')
        suffix = f'，建议从 {retry_from} 继续' if retry_from else ''
        return f'当前流程失败{suffix}，已完成步骤: {", ".join(done) if done else "无"}。'
    if status == 'cancelled':
        return '当前流程已取消。'
    if progress and len(done) == len(progress):
        return '当前流程已完成全部步骤。'
    text = f'当前状态: {status}'
    if current:
        text += f'，当前/下一步是 {current}'
    if done:
        text += f'，已完成: {", ".join(done)}'
    if checkpoint.get('checkpoint_state') == 'stale':
        text += '，checkpoint 已失效'
    return text + '。'


def _action_text(compiled: Mapping[str, Any]) -> str:
    command_id = str(compiled['command_id'])
    verb = '已提交' if compiled['kind'] == 'flow' else '已执行'
    return f'{verb}{_action_name(compiled)}的操作，command_id={command_id}。'


def _approval_text(compiled: Mapping[str, Any], token: str) -> str:
    return f'{_action_name(compiled)}需要确认后执行，确认码 {token}。'


def _action_name(compiled: Mapping[str, Any]) -> str:
    payload = compiled['payload']
    if compiled['kind'] == 'flow':
        command = str(payload['command'])
        until = str(payload.get('until_step') or '')
        return {
            'continue': f'继续执行到 {until}' if until else '继续执行',
            'pause': '暂停流程',
            'resume': '恢复并继续执行',
            'cancel': '取消流程',
            'retry': '重试失败流程',
        }[command]
    mutation = str(payload['mutation'])
    return {
        'edit_artifact': '修改产物',
        'rerun_case_stage': '重跑 case 阶段',
        'rerun_step': '重跑步骤',
        'invalidate_from_step': '从指定步骤失效后续产物',
    }[mutation]


def _hash(value: object) -> str:
    return hashlib.sha256(json_bytes(_safe(value))).hexdigest()


def _safe(value: object) -> object:
    if hasattr(value, 'model_dump'):
        return value.model_dump()
    if is_dataclass(value):
        return {field.name: _safe(getattr(value, field.name)) for field in fields(value)}
    if isinstance(value, Mapping):
        return {str(key): _safe(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_safe(item) for item in value]
    return value
