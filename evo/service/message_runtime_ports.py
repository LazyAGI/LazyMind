from __future__ import annotations

from collections.abc import Callable, Mapping
from typing import Any

from evo.artifact_runtime import (
    ArtifactKey,
    IntentCommandRequest,
    MaterializeIntent,
    RetryFailedIntent,
    RunControlIntent,
    intent_request_fingerprint,
    intent_request_from_payload,
)
from evo.artifact_runtime.patching import parse_ref_parts, prepare_json_pointer_patch
from evo.artifact_runtime.views import ArtifactViewService
from evo.auto_agent import AutoIntervention
from evo.message_intent.models import IntentFrame
from evo.message_intent.runtime_adapter import MessageCommandContext


class EvoMessageRuntimePorts:
    def __init__(
        self,
        *,
        flow_getter: Callable[[str], Any],
        artifact_reader: Callable[[str, str], dict | None],
        background_submitter: Callable[[str, str, str, Callable[[], dict[str, Any]]], dict[str, Any]] | None = None,
        sync_runner: Callable[[str, str, Callable[[], dict[str, Any]]], dict[str, Any]] | None = None,
    ) -> None:
        self.flow_getter = flow_getter
        self.artifact_reader = artifact_reader
        self.background_submitter = background_submitter
        self.sync_runner = sync_runner

    def continue_flow(self, thread_id: str, context: MessageCommandContext) -> dict[str, Any]:
        command_id = _message_command_id(context, 'continue_flow')
        if self.background_submitter is not None:
            return self.background_submitter(thread_id, command_id, 'continue',
                                             lambda: self._continue_flow(thread_id, command_id, context))
        return self._continue_flow(thread_id, command_id, context)

    def pause_flow(self, thread_id: str, context: MessageCommandContext) -> dict[str, Any]:
        request = _message_command_request(context, 'pause_flow', RunControlIntent('pause'), advance_until_idle=False)
        if self.sync_runner is not None:
            return self.sync_runner(thread_id, 'pause', lambda: self._pause_flow(thread_id, request.command_id, context))
        return self._pause_flow(thread_id, request.command_id, context)

    def cancel_flow(self, thread_id: str, context: MessageCommandContext) -> dict[str, Any]:
        request = _message_command_request(context, 'cancel_flow', RunControlIntent('cancel'), advance_until_idle=False)
        if self.sync_runner is not None:
            return self.sync_runner(thread_id, 'cancel',
                                    lambda: self._cancel_flow(thread_id, request.command_id, context))
        return self._cancel_flow(thread_id, request.command_id, context)

    def retry_failed(self, thread_id: str, context: MessageCommandContext) -> dict[str, Any]:
        request = _message_command_request(context, 'retry_failed', RetryFailedIntent())
        if self.background_submitter is not None:
            return self.background_submitter(thread_id, request.command_id, 'retry',
                                             lambda: self._retry_failed(thread_id, request.command_id, context))
        return self._retry_failed(thread_id, request.command_id, context)

    def rerun_case(self, thread_id: str, case_id: str, context: MessageCommandContext) -> dict[str, Any]:
        artifact = ArtifactKey('eval.rag_answer', case_id)
        request = _message_command_request(context, 'rerun_case', MaterializeIntent((artifact,),
                                           include_downstream=True))
        if self.background_submitter is not None:
            return self.background_submitter(thread_id, request.command_id, 'rerun_case',
                                             lambda: self._rerun_case(thread_id, request.command_id,
                                                                      artifact, case_id, context))
        return self._rerun_case(thread_id, request.command_id, artifact, case_id, context)

    def read_report_section(
        self,
        thread_id: str,
        *,
        artifact_ref: str,
        selector: str,
        cursor: str,
        max_chars: int,
        context: MessageCommandContext,
    ) -> dict[str, Any]:
        del context
        artifact = artifact_ref or 'eval.summary'
        view = self._views(thread_id).view(artifact, selector=selector, cursor=cursor, max_chars=max_chars)
        payload = _view_payload(view)
        payload['working_set_patch'] = {'last_report': artifact, 'last_artifact_view': _view_state(view, artifact)}
        return payload

    def read_case_result(
        self,
        thread_id: str,
        *,
        case_id: str,
        selector: str,
        cursor: str,
        max_chars: int,
        context: MessageCommandContext,
    ) -> dict[str, Any]:
        del context
        artifact = f'eval.judge_result[{case_id}]'
        view = self._views(thread_id).view(artifact, selector=selector, cursor=cursor, max_chars=max_chars)
        payload = _view_payload(view)
        payload['working_set_patch'] = {'selected_cases': (case_id,), 'last_case': case_id}
        return payload

    def prepare_patch(
        self,
        thread_id: str,
        *,
        artifact_ref: str,
        json_pointer: str,
        patch_value: Any,
        provenance: Mapping[str, Any],
        context: MessageCommandContext,
    ) -> dict[str, Any]:
        row = self.artifact_reader(thread_id, artifact_ref)
        if row is None:
            raise ValueError(f'artifact not found: {artifact_ref}')
        return prepare_json_pointer_patch(
            command_id=_message_command_id(context, 'patch_artifact'),
            run_id=context.run_id,
            artifact_ref=artifact_ref,
            json_pointer=json_pointer,
            patch_value=patch_value,
            row=row,
            provenance=provenance,
            preview_reconcile=lambda artifact: self.flow_getter(thread_id).preview_reconcile(artifact),
            patch_source=f'message_turn:{context.turn_id}',
            reason=f'message:{context.turn_id}',
        )

    def stale_expected_refs(self, thread_id: str, expected_refs: tuple[str, ...]) -> list[str]:
        if self.sync_runner is not None:
            result = self.sync_runner(
                thread_id,
                'stale_expected_refs',
                lambda: {'status': 'ok', 'stale_refs': self._stale_expected_refs(thread_id, expected_refs)},
            )
            if str(result.get('status') or '') == 'conflict':
                raise RuntimeError(str(result.get('reason') or 'flow operation is busy'))
            return [str(item) for item in result.get('stale_refs') or ()]

        return self._stale_expected_refs(thread_id, expected_refs)

    def _stale_expected_refs(self, thread_id: str, expected_refs: tuple[str, ...]) -> list[str]:
        out: list[str] = []
        flow = self.flow_getter(thread_id)
        for value in expected_refs:
            key, version = parse_ref_parts(value)
            latest = flow.latest_ref(key)
            if latest is None or latest.version != version:
                out.append(value)
        return out

    def execute_approval(
        self,
        thread_id: str,
        *,
        command_id: str,
        prepared_payload: Mapping[str, Any],
        expected_fingerprint: str,
    ) -> dict[str, Any]:
        if self.sync_runner is not None:
            return self.sync_runner(
                thread_id,
                'approve_pending',
                lambda: self._execute_approval(
                    thread_id,
                    command_id=command_id,
                    prepared_payload=prepared_payload,
                    expected_fingerprint=expected_fingerprint,
                ),
            )
        return self._execute_approval(
            thread_id,
            command_id=command_id,
            prepared_payload=prepared_payload,
            expected_fingerprint=expected_fingerprint,
        )

    def _execute_approval(
        self,
        thread_id: str,
        *,
        command_id: str,
        prepared_payload: Mapping[str, Any],
        expected_fingerprint: str,
    ) -> dict[str, Any]:
        request = intent_request_from_payload(command_id, prepared_payload, expected_fingerprint=expected_fingerprint)
        result = self.flow_getter(thread_id).runtime.execute_intent(request)
        if result.status == 'failed' and result.reason == 'command_in_progress':
            return {'status': 'in_progress', 'reason': result.reason}
        return {'status': result.status, 'reason': result.reason}

    def _views(self, thread_id: str) -> ArtifactViewService:
        return ArtifactViewService(lambda artifact_id: self.artifact_reader(thread_id, artifact_id))

    def _continue_flow(self, thread_id: str, command_id: str, context: MessageCommandContext) -> dict[str, Any]:
        state = self.flow_getter(thread_id).continue_flow(command_id=command_id, run_id=context.run_id)
        return {
            'status': state.gate_status,
            'current_step': state.current_step,
            'completed_steps': list(state.completed_steps),
        }

    def _pause_flow(self, thread_id: str, command_id: str, context: MessageCommandContext) -> dict[str, Any]:
        state = self.flow_getter(thread_id).pause_flow(command_id=command_id, run_id=context.run_id)
        return {'status': state.gate_status, 'current_step': state.current_step}

    def _cancel_flow(self, thread_id: str, command_id: str, context: MessageCommandContext) -> dict[str, Any]:
        state = self.flow_getter(thread_id).cancel_flow(command_id=command_id, run_id=context.run_id)
        return {'status': state.gate_status, 'current_step': state.current_step}

    def _retry_failed(self, thread_id: str, command_id: str, context: MessageCommandContext) -> dict[str, Any]:
        state = self.flow_getter(thread_id).retry_failed_flow(command_id=command_id, run_id=context.run_id)
        return {'status': state.gate_status, 'current_step': state.current_step}

    def _rerun_case(
        self,
        thread_id: str,
        command_id: str,
        artifact: ArtifactKey,
        case_id: str,
        context: MessageCommandContext,
    ) -> dict[str, Any]:
        state = self.flow_getter(thread_id).materialize_flow(
            command_id=command_id,
            run_id=context.run_id,
            artifacts=(artifact,),
        )
        return {'status': state.gate_status, 'current_step': state.current_step, 'case_id': case_id}


def _message_command_request(
    context: MessageCommandContext,
    intent_kind: str,
    intent: MaterializeIntent | RetryFailedIntent | RunControlIntent,
    *,
    metadata: Mapping[str, Any] | None = None,
    advance_until_idle: bool = True,
) -> IntentCommandRequest:
    provisional = IntentCommandRequest(
        'msg:pending',
        context.run_id,
        intent,
        advance_until_idle=advance_until_idle,
        metadata=dict(metadata or {}),
    )
    fingerprint = intent_request_fingerprint(provisional)
    return IntentCommandRequest(
        _message_command_id(context, intent_kind, fingerprint),
        context.run_id,
        intent,
        advance_until_idle=advance_until_idle,
        metadata=dict(metadata or {}),
    )


def _message_command_id(context: MessageCommandContext, intent_kind: str, fingerprint: str = '') -> str:
    return f'msg:{context.turn_id}:{context.intent_index}:{intent_kind}:{fingerprint or context.run_id}'


def auto_intervention_frame(intervention: Mapping[str, Any]) -> dict[str, Any]:
    typed = AutoIntervention.model_validate(intervention)
    payload = typed.intent_frame_payload()
    intent = payload.get('intent')
    if isinstance(intent, dict) and intent.get('kind') == 'patch_artifact':
        args = intent.get('args') if isinstance(intent.get('args'), dict) else {}
        intent['args'] = {
            'artifact_ref': f'eval.judge_result[{args.get("case_ref") or typed.case_id}]',
            'json_pointer': '/' + _escape_pointer(str(args.get('field') or typed.field)),
            'value': args.get('value', typed.value),
        }
    return IntentFrame.model_validate(payload).model_dump(mode='json')


def _view_payload(view: Mapping[str, Any]) -> dict[str, Any]:
    return {
        'source_ref': str(view.get('source_ref') or ''),
        'facts': view.get('facts') or {},
        'truncated': bool(view.get('truncated')),
        'next_cursor': str(view.get('next_cursor') or ''),
        'selector': str(view.get('selector') or ''),
        'available_sections': list(view.get('available_sections') or ()),
        'excerpt': str(view.get('excerpt') or ''),
    }


def _view_state(view: Mapping[str, Any], artifact_id: str) -> dict[str, Any]:
    return {
        'artifact_id': artifact_id,
        'source_ref': str(view.get('source_ref') or artifact_id),
        'selector': str(view.get('selector') or ''),
        'truncated': bool(view.get('truncated')),
        'next_cursor': str(view.get('next_cursor') or ''),
    }


def _escape_pointer(value: str) -> str:
    return str(value).replace('~', '~0').replace('/', '~1')
