from __future__ import annotations

import hashlib
from typing import Any

from evo.artifact_runtime.utils import canonical_json

from .models import AutoAction, AutoAgentState, AutoDecision, CommandStatus, PortCommandResult
from .ports import AutoAgentPorts
from .store import AutoAgentLease, AutoAgentStore


class AutoActionExecutor:
    _APPROVAL_ACTIONS = {
        'approve_pending': 'approve',
        'reject_pending': 'reject',
        'cancel_pending': 'cancel',
    }

    def __init__(self, ports: AutoAgentPorts, store: AutoAgentStore) -> None:
        self.ports = ports
        self.store = store

    def execute(
        self,
        thread_id: str,
        decision: AutoDecision,
        state: AutoAgentState,
        *,
        lease: AutoAgentLease,
    ) -> AutoAgentState:
        action = decision.action
        action_id = _action_id(thread_id, decision.observation_hash, action, state.config.model_dump(mode='json'))
        if action_id in state.completed_action_ids:
            return self.store.record_action(
                state,
                action_id=action_id,
                kind='noop',
                target=action.target,
                status='duplicate',
                reason='action already executed',
                response={},
                lease=lease,
            )
        if state.action_failure_counts.get(action_id, 0) >= state.config.max_action_failures:
            blocked = state.model_copy(update={
                'running': False,
                'stop_reason': f'action failure budget exhausted: {action.kind}:{action.target}',
            })
            return self.store.record_action(
                blocked,
                action_id=action_id,
                kind=action.kind,
                target=action.target,
                status='blocked',
                reason=action.reason,
                response={
                    'error': 'action_failure_budget_exhausted',
                    'failures': state.action_failure_counts.get(action_id, 0),
                },
                lease=lease,
            )
        try:
            self.store.assert_lease(lease)
            result = self._execute_action(thread_id, action, action_id)
            response = result.raw if not result.error else {**result.raw, 'error': result.error}
            status = {
                CommandStatus.OK: 'ok',
                CommandStatus.RUNNING: 'running',
                CommandStatus.ERROR: 'error',
            }[result.status]
        except Exception as exc:  # noqa: BLE001 - auto executor must persist failure for inspection.
            response = {'error_type': type(exc).__name__, 'error_message': str(exc)}
            status = 'error'

        patch: dict[str, Any] = {
            'last_observation_hash': decision.observation_hash,
            'last_decision': decision.model_dump(mode='json'),
        }
        if status == 'ok':
            if action.kind == 'continue_flow':
                patch['continue_count'] = state.continue_count + 1
            elif action.kind == 'retry_failed':
                target = action.target or 'flow'
                patch['retry_counts'] = {**state.retry_counts, target: state.retry_counts.get(target, 0) + 1}
            elif action.kind == 'send_message' and action.target:
                pending = response.get('pending_approval') if isinstance(
                    response.get('pending_approval'), dict) else None
                patch['auto_pending_approvals'] = (
                    tuple(dict.fromkeys((*state.auto_pending_approvals, str(pending['approval_token']))))
                    if pending and pending.get('approval_token')
                    else state.auto_pending_approvals
                )
                patch['intervention_counts'] = {
                    **state.intervention_counts,
                    action.target: state.intervention_counts.get(action.target, 0) + 1,
                }
            elif action.kind == 'stop_agent':
                patch['running'] = False
                patch['stop_reason'] = action.reason
        elif status != 'running':
            patch['action_failure_counts'] = {
                **state.action_failure_counts,
                action_id: state.action_failure_counts.get(action_id, 0) + 1,
            }
        updated = state.model_copy(update=patch)
        if (
            status not in {'ok', 'running'}
            and updated.action_failure_counts.get(action_id, 0) >= state.config.max_action_failures
        ):
            updated = updated.model_copy(update={
                'running': False,
                'stop_reason': f'action failure budget exhausted: {action.kind}:{action.target}',
            })
            status = 'blocked'
            response = {**response, 'error': 'action_failure_budget_exhausted'}
        return self.store.record_action(
            updated,
            action_id=action_id,
            kind=action.kind,
            target=action.target,
            status=status,
            reason=action.reason,
            response=response,
            lease=lease,
        )

    def _execute_action(self, thread_id: str, action: AutoAction, action_id: str) -> PortCommandResult:
        command_id = action.command_id or f'auto:{action_id}'
        if action.kind == 'noop':
            return PortCommandResult(status=CommandStatus.OK)
        flow_actions = {
            'start_flow': self.ports.start_flow,
            'continue_flow': self.ports.continue_flow,
            'pause_flow': self.ports.pause_flow,
            'cancel_flow': self.ports.cancel_flow,
            'retry_failed': self.ports.retry_failed,
        }
        if action.kind in flow_actions:
            return flow_actions[action.kind](thread_id, command_id=command_id)
        if action.kind == 'send_message':
            if action.intervention is None:
                raise ValueError('send_message action requires typed intervention')
            metadata = {'source': 'auto_agent', **action.metadata}
            return self.ports.submit_intervention(
                thread_id,
                message_id=f'msg_{action_id[:24]}',
                metadata=metadata,
                intervention=action.intervention,
            )
        if action.kind in self._APPROVAL_ACTIONS:
            return self.ports.resolve_approval(
                thread_id,
                action=self._APPROVAL_ACTIONS[action.kind],
                approval_token=action.approval_token,
                command_id=command_id,
            )
        if action.kind == 'stop_agent':
            return PortCommandResult(status=CommandStatus.OK, raw={'reason': action.reason})
        raise ValueError(f'unsupported auto action: {action.kind}')


def _action_id(thread_id: str, observation_hash: str, action: AutoAction, config: dict[str, Any]) -> str:
    action_payload = action.model_dump(mode='json')
    if action.intervention is not None:
        action_payload['message'] = ''
        action_payload['metadata'] = {
            key: value for key, value in action_payload.get('metadata', {}).items() if key != 'display_message'
        }
    payload = {
        'thread_id': thread_id,
        'observation_hash': observation_hash,
        'action': action_payload,
        'policy': {'config': config},
    }
    return hashlib.sha256(canonical_json(payload).encode('utf-8')).hexdigest()
