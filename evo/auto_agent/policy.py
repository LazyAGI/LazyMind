from __future__ import annotations

from .intervention import AutoIntervention
from .models import AutoAction, AutoAgentConfig, AutoAgentState, AutoDecision, AutoObservation


class AutoPolicy:
    def decide(self, observation: AutoObservation, state: AutoAgentState) -> AutoDecision:
        config = state.config
        action = self._action(observation, state, config)
        return AutoDecision(observation_hash=observation.hash, action=action, reason=action.reason)

    def _action(
        self,
        observation: AutoObservation,
        state: AutoAgentState,
        config: AutoAgentConfig,
    ) -> AutoAction:
        if not config.enabled:
            return AutoAction(kind='stop_agent', reason='auto agent disabled')
        if observation.mode != 'auto':
            return AutoAction(kind='stop_agent', reason='thread mode is not auto')
        if observation.status in {'ended', 'cancelled'}:
            return AutoAction(kind='stop_agent', reason=f'flow status is {observation.status}')
        if observation.active_approval is not None:
            return self._approval_action(observation, state, config)
        if observation.status == 'idle' and config.start_when_idle:
            return AutoAction(kind='start_flow', reason='auto thread is idle; start flow')
        if observation.status == 'failed':
            target = observation.current_step or 'flow'
            if config.retry_failed_enabled and state.retry_counts.get(target, 0) < config.retry_failed_max_per_step:
                return AutoAction(kind='retry_failed', reason='flow failed', target=target)
            return AutoAction(kind='pause_flow', reason='flow failed; retry budget exhausted', target=target)
        if observation.status != 'waiting_checkpoint':
            return AutoAction(kind='noop', reason=f'flow status is {observation.status}')
        suggestions = observation.facts.get('intervention_suggestions')
        if isinstance(suggestions, list):
            for suggestion in suggestions:
                if isinstance(suggestion, dict):
                    try:
                        action = self._suggestion_action(observation, state, config, suggestion)
                    except ValueError:
                        action = None
                    if action is not None:
                        return action
        if config.auto_continue:
            if state.continue_count >= config.max_continue_actions:
                return AutoAction(kind='pause_flow', reason='auto continue budget exhausted')
            return AutoAction(
                kind='continue_flow',
                reason='stage checkpoint reached; continue next flow step',
                target=observation.current_step,
            )
        return AutoAction(kind='noop', reason='auto continue disabled')

    def _approval_action(
        self,
        observation: AutoObservation,
        state: AutoAgentState,
        config: AutoAgentConfig,
    ) -> AutoAction:
        approval = observation.active_approval
        if approval is None:
            return AutoAction(kind='noop', reason='no active approval')
        auto_owned = approval.approval_token in state.auto_pending_approvals
        if approval.status == 'resolving':
            if auto_owned:
                return AutoAction(
                    kind='approve_pending',
                    reason='auto-owned pending approval is resolving; probe command result',
                    approval_token=approval.approval_token,
                    target=approval.intent_kind,
                )
            return AutoAction(kind='noop', reason='pending approval is already resolving')
        allowed = config.auto_approve == 'all_mutations' or (
            config.auto_approve == 'evidence_backed'
            and auto_owned
            and approval.risk_level in {'low', 'medium'}
        )
        if allowed:
            return AutoAction(
                kind='approve_pending',
                reason='auto-owned pending approval is allowed by policy',
                approval_token=approval.approval_token,
                target=approval.intent_kind,
            )
        if config.pause_on_risk:
            return AutoAction(kind='pause_flow', reason='pending approval requires human review')
        return AutoAction(kind='noop', reason='pending approval is waiting for human review')

    def _suggestion_action(
        self,
        observation: AutoObservation,
        state: AutoAgentState,
        config: AutoAgentConfig,
        suggestion: dict,
    ) -> AutoAction | None:
        kind = str(suggestion.get('kind') or '')
        if kind == 'rerun_case':
            return self._rerun_case_or_pause(observation, state, config, suggestion)
        if kind == 'patch_judge_score':
            return self._patch_score_or_continue(observation, state, config, suggestion)
        return None

    def _rerun_case_or_pause(
        self,
        observation: AutoObservation,
        state: AutoAgentState,
        config: AutoAgentConfig,
        suggestion: dict,
    ) -> AutoAction:
        args = suggestion.get('args') if isinstance(suggestion.get('args'), dict) else {}
        case_id = str(args.get('case_id') or args.get('case_ref') or '').strip()
        reason = ' '.join(str(suggestion.get('reason') or 'unknown').split()) or 'unknown'
        intervention = AutoIntervention(
            kind='rerun_case',
            case_id=case_id,
            source_ref=str(suggestion.get('source_ref') or observation.hash),
        )
        key = intervention.fingerprint
        if config.rerun_case_enabled and state.intervention_counts.get(key, 0) < config.rerun_case_max_per_ref:
            return AutoAction(
                kind='send_message',
                reason='case execution failure detected',
                target=key,
                intervention=intervention,
                metadata={'display_message': f'{case_id} 执行失败，请重跑这个 case。失败原因：{reason}'},
            )
        return AutoAction(kind='pause_flow', reason='case rerun budget exhausted', target=case_id)

    def _patch_score_or_continue(
        self,
        observation: AutoObservation,
        state: AutoAgentState,
        config: AutoAgentConfig,
        suggestion: dict,
    ) -> AutoAction:
        args = suggestion.get('args') if isinstance(suggestion.get('args'), dict) else {}
        case_id = str(args.get('case_id') or args.get('case_ref') or '').strip()
        field = str(args.get('field') or '').strip()
        value = args.get('value', args.get('suggested'))
        intervention = AutoIntervention(
            kind='patch_judge_score',
            case_id=case_id,
            field=field,
            value=value,
            source_ref=str(suggestion.get('source_ref') or observation.hash),
        )
        key = intervention.fingerprint
        if (
            config.patch_artifact_enabled
            and config.patch_judge_score_enabled
            and state.intervention_counts.get(key, 0) < 1
        ):
            return AutoAction(
                kind='send_message',
                reason='artifact suggested score patch detected',
                target=key,
                intervention=intervention,
                metadata={
                    'display_message': (
                        f'{case_id} 的 {field} 评分不合理，请将 {field} 修改为 {value}。'
                        f'理由：{suggestion.get("reason") or "artifact suggested intervention"}'
                    )
                },
            )
        if config.auto_continue:
            if state.continue_count >= config.max_continue_actions:
                return AutoAction(kind='pause_flow', reason='auto continue budget exhausted')
            return AutoAction(kind='continue_flow', reason='score intervention already proposed; continue flow')
        return AutoAction(kind='noop', reason='score intervention already proposed')
