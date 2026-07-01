from __future__ import annotations

from collections.abc import Callable
from typing import Any

from .models import (
    ApprovalIntent,
    BoundedRunIntent,
    IntentFrame,
    MessageIntentPayload,
    RunControlIntentFrame,
    ResolvedIntent,
)

MIN_PLANNER_CONFIDENCE = 0.65
READ_ONLY_KINDS = frozenset({
    'no_action_ack',
    'chat',
    'status_query',
    'read_case_result',
    'read_report_section',
    'explain_current_gate',
})
MUTATING_KINDS = frozenset({
    'continue_flow', 'pause_flow', 'cancel_flow',
    'retry_failed', 'rerun_case', 'patch_artifact'
})
PENDING_RESOLUTION_KINDS = frozenset({'approve_pending', 'reject_pending', 'cancel_pending'})
UNSUPPORTED_RUNTIME_KINDS = {
    'bounded_continue_flow': '当前 evo runtime 还不支持带步骤边界的继续执行；为避免误执行，未执行任何流程控制。',
    'explain_current_gate': '当前 evo runtime 还不支持解释指定 gate/checkpoint；可以先读取报告或查看流程状态。',
}


class MessageIntentRouter:
    """Resolves parsed message intent into the existing command shape and policy gate."""

    def __init__(
        self,
        *,
        has_flow: Callable[[str], bool],
        case_count_getter: Callable[[str], int],
        selected_cases_getter: Callable[[str], tuple[str, ...]],
        case_ref_resolver: Callable[[str], str | tuple[str, ...]],
    ) -> None:
        self.has_flow = has_flow
        self.case_count_getter = case_count_getter
        self.selected_cases_getter = selected_cases_getter
        self.case_ref_resolver = case_ref_resolver

    def resolve(self, thread_id: str, frame: IntentFrame) -> ResolvedIntent:
        payload = frame.intent
        args = _payload_args(payload)
        kind = runtime_kind(payload)
        case_ref = str(args.get('case_ref') or args.get('case') or '')
        case_id = str(args.get('case_id') or '') or self._case_id_from_ref(case_ref)
        case_ids = _string_tuple(args.get('case_ids')) or self._case_ids_from_ref(thread_id, case_ref)
        if kind in {'patch_artifact', 'rerun_case', 'read_case_result'} and case_ref and not case_id and not case_ids:
            return ResolvedIntent(kind='unsupported', reason='请指定有效的 case，例如 case_0001。')
        if kind == 'patch_artifact':
            if not str(args.get('artifact_ref') or '').strip() or not str(args.get('json_pointer') or '').strip():
                return ResolvedIntent(kind='unsupported', reason='请指定要修改的产物和 JSON Pointer 路径。')
        if case_id or case_ids:
            allowed = {f'case_{index:04d}' for index in range(1, self.case_count_getter(thread_id) + 1)}
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
            json_pointer=str(args.get('json_pointer') or '').strip(),
            value=args.get('value'),
            approval_token=str(args.get('approval_token') or ''),
            reason=str(frame.reason or args.get('reason') or ''),
            raw_args=args,
        )

    def gate(
        self,
        thread_id: str,
        frame: IntentFrame,
        *,
        has_active_approval: bool,
        active_approval_token: str = '',
        flow_status: dict[str, Any] | None = None,
        resolved: ResolvedIntent | None = None,
    ) -> str:
        if frame.confidence < MIN_PLANNER_CONFIDENCE:
            return '我不够确定要执行哪个 evo 操作，请更明确地描述。'
        kind = runtime_kind(frame.intent)
        if kind in UNSUPPORTED_RUNTIME_KINDS:
            return UNSUPPORTED_RUNTIME_KINDS[kind]
        args = _payload_args(frame.intent)
        if (
            kind in PENDING_RESOLUTION_KINDS
            and not has_active_approval
            and not str(args.get('approval_token') or '').strip()
        ):
            return '当前没有可处理的待确认操作。'
        if kind in PENDING_RESOLUTION_KINDS:
            token = str(args.get('approval_token') or '').strip()
            if not token and not active_approval_token:
                return '请提供待确认操作的 approval token。'
            if token and active_approval_token and token != active_approval_token:
                return 'approval_token mismatch'
            if _has_stage_gate(flow_status) and active_approval_token and not token:
                return '当前同时存在流程 checkpoint 和待确认修改，请明确是继续流程，还是确认待修改操作。'
        if has_active_approval and kind not in READ_ONLY_KINDS and kind not in PENDING_RESOLUTION_KINDS:
            if kind == 'continue_flow' and _has_stage_gate(flow_status):
                return ''
            return '已有待确认操作，请先确认或取消后再发起新的修改或流程控制。'
        if kind in MUTATING_KINDS and not self.has_flow(thread_id):
            return '当前还没有可控制的 evo 流程；请先启动流程，或先查看当前状态。'
        if resolved is not None and resolved.kind == 'unsupported':
            return resolved.reason or '暂不支持该 evo 操作。'
        if resolved is not None:
            if kind == 'read_case_result':
                if resolved.case_ids:
                    return '当前仅支持一次读取单个 case，请指定一个 case。'
                if not resolved.case_id:
                    return '请指定要读取的 case。'
            if kind == 'rerun_case':
                if resolved.case_ids:
                    return '当前仅支持单 case 重跑，请指定一个 case。'
                if not resolved.case_id:
                    return '请指定要重跑的 case。'
        return ''

    def _case_id_from_ref(self, case_ref: str) -> str:
        normalized = self.case_ref_resolver(case_ref)
        if isinstance(normalized, str) and normalized.startswith('case_'):
            return normalized
        return ''

    def _case_ids_from_ref(self, thread_id: str, case_ref: str) -> tuple[str, ...]:
        normalized = self.case_ref_resolver(case_ref)
        if normalized != 'selected_cases':
            return ()
        return self.selected_cases_getter(thread_id)


def _string_tuple(value: Any) -> tuple[str, ...]:
    if value is None or value == '':
        return ()
    if isinstance(value, str):
        return (value,)
    if isinstance(value, (list, tuple, set)):
        return tuple(str(item) for item in value if str(item).strip())
    return (str(value),)


def _has_stage_gate(flow_status: dict[str, Any] | None) -> bool:
    if not isinstance(flow_status, dict):
        return False
    checkpoint = flow_status.get('pending_checkpoint')
    return (
        str(flow_status.get('status') or '') == 'waiting_checkpoint'
        and isinstance(checkpoint, dict)
        and str(checkpoint.get('checkpoint_kind') or '') == 'stage_gate'
    )


def runtime_kind(payload: MessageIntentPayload) -> str:
    if isinstance(payload, RunControlIntentFrame):
        return {
            'continue': 'continue_flow',
            'pause': 'pause_flow',
            'cancel': 'cancel_flow',
            'retry_failed': 'retry_failed',
        }[payload.args.action]
    if isinstance(payload, BoundedRunIntent):
        return 'bounded_continue_flow'
    if isinstance(payload, ApprovalIntent):
        return f'{payload.action}_pending'
    return payload.kind


def _payload_args(payload: MessageIntentPayload) -> dict[str, Any]:
    args = getattr(payload, 'args', None)
    if args is None:
        return {}
    return args.model_dump(mode='python')
