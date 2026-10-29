"""Recover one Agent model call without entering the Agent again."""
from __future__ import annotations

import copy
import json
import math
import random
import time
from asyncio import CancelledError
from typing import Any

import lazyllm
from lazyllm.common import HandledException
from lazyllm.module import ModuleBase
from lazyllm.module.module import ModuleExecutionError
from lazyllm.module.servermodule import LLMBase
from lazyllm.module.llms.onlinemodule.base.model_outcome import ModelCallError

from .budget import build_context_budget, parse_token_limit
from .model_calls import ContextCapacityError, UnsupportedModelInput, request_tokens, configured_output_limit
from .message_fields import model_facing_history
from lazyllm.module.llms.onlinemodule.base import ModelCallControl
from .cancellation import UserCancelledError
from .models import FallbackRunState, ModelCandidate
from lazymind.config import config
from lazymind.chat.runtime_events import runtime_event


_RECOVERABLE = frozenset({
    'authentication_failed', 'permission_denied', 'not_found', 'rate_limited',
    'usage_limit_exceeded', 'concurrency_limited', 'quota_exhausted', 'balance_exhausted',
    'organization_spend_limit_exceeded', 'project_spend_limit_exceeded',
    'request_timeout', 'provider_overloaded', 'service_unavailable', 'provider_internal_error',
    'transport_error',
})
_INVALID_KEY_CODES = frozenset({'invalid_api_key', 'api_key_invalid', 'expired_api_key',
                                'revoked_api_key', 'invalid_authentication'})
_UNSUPPORTED_CALL_CODES = frozenset({'unsupported_tools', 'tools_not_supported', 'unsupported_function_call'})


class _RecoveryBudgetExhausted(HandledException):
    pass


def model_candidate(value: ModelCandidate | dict[str, Any]) -> ModelCandidate:
    if isinstance(value, ModelCandidate):
        return ModelCandidate(**{**vars(value), 'llm_config': copy.deepcopy(value.llm_config),
                                 'max_input_tokens': parse_token_limit(value.max_input_tokens)})
    source, model_id = str(value.get('source') or ''), str(value.get('model_id') or '')
    return ModelCandidate(
        candidate_id=str(value.get('candidate_id') or f'{source}:{model_id}'),
        source=source, model_id=model_id,
        provider_group_id=str(value.get('provider_group_id') or ''),
        credential_revision=int(value.get('credential_revision') or 0),
        credential_count=int(value.get('credential_count') or 1),
        max_input_tokens=parse_token_limit(value.get('max_input_tokens')),
        llm_config=copy.deepcopy(value.get('llm_config') or {}),
    )


def create_fallback_state(llm: Any, options: Any) -> FallbackRunState:
    candidates = [model_candidate(item) for item in options.model_candidates]
    current_id = getattr(options, 'current_model_candidate_id', '')
    current = next((item for item in candidates if item.candidate_id == current_id), None)
    if current_id and current is None:
        raise ValueError('current model candidate is missing from the execution candidates')
    if current is None:
        role = (options.llm_config or {}).get('llm') or {}
        current = ModelCandidate(
            candidate_id='', source='', model_id=str(role.get('model') or ''),
            max_input_tokens=parse_token_limit(options.max_input_tokens)
            or parse_token_limit(role.get('max_input_tokens')),
            llm_config=copy.deepcopy(options.llm_config or {}),
        )
    known = [item for item in candidates if item.max_input_tokens]
    unknown = [item for item in candidates if not item.max_input_tokens]
    random.shuffle(unknown)
    state = FallbackRunState(
        current=current, raw_llm=llm,
        budget=build_context_budget(current.max_input_tokens),
        candidates=tuple([*known, *unknown]),
        initial_model_switch=copy.deepcopy(getattr(options, 'initial_model_switch', None)),
    )
    if state.initial_model_switch is not None:
        state.failed_candidates.add(state.initial_model_switch['from']['candidate_id'])
    return state


def _ordered_candidates(state: FallbackRunState, *, requires_vision=False) -> list[ModelCandidate]:
    return sorted((item for item in state.candidates
                   if item.candidate_id not in state.failed_candidates
                   and (item.provider_group_id, item.credential_revision) not in state.failed_credentials
                   and (not requires_vision or (item.llm_config.get('llm') or {}).get('vision') is True
                        or str((item.llm_config.get('llm') or {}).get('type') or '').lower() == 'vlm')),
                  key=lambda item: -build_context_budget(item.max_input_tokens).effective_input_budget)


def _public(candidate: ModelCandidate) -> dict[str, str]:
    return {name: getattr(candidate, name) for name in
            ('candidate_id', 'source', 'model_id', 'provider_group_id')}


def _copy_prompt(prompt):
    cloned = copy.copy(prompt)
    # Dynamic tool providers retain their original FunctionCall owner. Deep-copy
    # only stored containers, never the Agent, tool manager, or SDK resources.
    for name in ('_history', '_tools', '_skills'):
        value = getattr(cloned, name, None)
        if isinstance(value, (list, dict)):
            setattr(cloned, name, copy.deepcopy(value))
    return cloned


def _make_llm(candidate: ModelCandidate):
    config = candidate.llm_config.get('llm') or {}
    if not config.get('source') or not config.get('model') or not (config.get('skip_auth') or config.get('api_key')):
        raise ValueError('candidate runtime configuration is incomplete')
    return lazyllm.OnlineChatModule(
        source=config.get('source'), model=config.get('model'),
        url=config.get('base_url') or config.get('url'), api_key=config.get('api_key'),
        skip_auth=bool(config.get('skip_auth', False)),
        type='vlm' if config.get('vision') is True else config.get('type'),
        static_params=copy.deepcopy(config.get('static_params') or {}),
        stream=False,
    )


def _request_timeout(candidate):
    role = candidate.llm_config.get('llm') or {}
    configured = role.get('timeout')
    if configured is None:
        configured = (config['model_fallback_connect_timeout_seconds'], config['model_fallback_read_timeout_seconds'])
    values = configured if isinstance(configured, (list, tuple)) else (configured, configured)
    if len(values) != 2 or any(not math.isfinite(float(value)) or float(value) <= 0 for value in values):
        raise ValueError('Model timeouts must contain finite positive connect and read limits.')
    return tuple(float(value) for value in values)


class FallbackLLM(ModuleBase, LLMBase):
    def __init__(self, state: FallbackRunState, *, cancel_check=None):
        ModuleBase.__init__(self)
        LLMBase.__init__(self, stream=getattr(state.raw_llm, 'stream', False), init_prompt=False,
                         type=getattr(state.raw_llm, 'type', 'LLM'))
        self.state = state
        self._cancel_check = cancel_check
        self._prompt = _copy_prompt(state.raw_llm._prompt)
        self._formatter = copy.copy(state.raw_llm._formatter)

    @property
    def raw_llm(self):
        return self.state.raw_llm

    def share(self, prompt=None, format=None, stream=None, history=None, copy_static_params=False):
        view = LLMBase.share(self, prompt=prompt, format=format, stream=stream, history=history,
                             copy_static_params=copy_static_params)
        view._prompt = _copy_prompt(view._prompt)
        view._formatter = copy.copy(view._formatter)
        view._stream = copy.copy(view._stream)
        return view

    def prompt(self, prompt=None, history=None):
        return LLMBase.prompt(self, '' if prompt is None else _copy_prompt(prompt), history=copy.deepcopy(history))

    def _check_cancel(self):
        if self._cancel_check is not None and self._cancel_check(None):
            raise UserCancelledError('stopped during a model request')
        return False

    def initialization_budget(self):
        candidates = [item for item in _ordered_candidates(self.state)
                      if item.candidate_id != self.state.current.candidate_id]
        budgets = [self.state.budget]
        budgets.extend(build_context_budget(item.max_input_tokens) for item in candidates)
        return max(budgets, key=lambda budget: budget.effective_input_budget)

    def auxiliary_call(self, system, user):
        from .summarizer import _call_summarizer_llm
        return _call_summarizer_llm(
            self.state.raw_llm, system, user, candidate_budget=self.state.budget,
            control=ModelCallControl(cancel_check=self._check_cancel), timeout=_request_timeout(self.state.current))

    def forward(self, input=None, *, llm_chat_history=None, lazyllm_files=None,
                stream_output=None, stream=None, **kwargs):
        try:
            return self._recover(input, llm_chat_history=llm_chat_history, lazyllm_files=lazyllm_files,
                                 stream_output=stream_output, stream=stream, **kwargs)
        except ModuleExecutionError as exc:
            exc._model_fallback_local_failure = True
            raise

    def _recover(self, input=None, *, llm_chat_history=None, lazyllm_files=None,
                 stream_output=None, stream=None, **kwargs):
        state = self.state
        prior_usage = state.usage_snapshot
        current, raw = state.current, state.raw_llm
        original = state.current
        history, files = copy.deepcopy(llm_chat_history), copy.deepcopy(lazyllm_files)
        history = model_facing_history(history) if history is not None else None
        reason = ''
        only_context_failures = True
        stream = stream_output if stream_output is not None else stream
        stream = self._stream if stream is None else stream
        outer_sink = stream.get('_stream_sink') if isinstance(stream, dict) else getattr(self, '_stream_sink', None)
        requires_vision = False
        control = ModelCallControl(cancel_check=self._check_cancel)
        deadline = time.monotonic() + max(0.001, float(config['model_fallback_timeout_seconds']))

        def emit(event):
            try:
                if outer_sink is not None:
                    outer_sink(event)
                else:
                    lazyllm.FileSystemQueue().enqueue(json.dumps(event, ensure_ascii=False))
            except Exception as exc:
                exc._model_fallback_local_failure = True
                raise

        attempted = set()
        max_candidates = max(1, int(config['model_fallback_max_candidates']))
        sent_count = 0

        def remaining_candidates():
            candidates = [item for item in _ordered_candidates(state, requires_vision=requires_vision)
                          if item.candidate_id not in attempted]
            return candidates if sent_count < max_candidates and time.monotonic() < deadline else []

        while True:
            control.check()
            attempted.add(current.candidate_id)
            local_failure = []
            call_index = 0
            call_open = False
            output_metadata = {'call_index': 0}

            def rollback(output_metadata=output_metadata):
                nonlocal call_open
                if call_open:
                    call_open = False
                    emit({'tag': 'model_output', 'model_output': {
                        'call_index': output_metadata['call_index'], 'phase': 'rollback',
                    }})

            def sink(event, output_metadata=output_metadata, local_failure=local_failure):
                try:
                    if event.get('tag') in {'text', 'think'} or (
                        event.get('tag') == 'runtime_event' and (event.get('runtime_event') or {}).get('type')
                        in {'model_call_started', 'model_retry_scheduled', 'model_call_finished'}
                    ):
                        event = {**event, 'model_output': dict(output_metadata)}
                    emit(event)
                except Exception as exc:
                    local_failure.append(exc)
                    raise

            selected_stream = {**(stream if isinstance(stream, dict) else {}), '_stream_sink': sink} if stream else False
            view = raw.share(prompt=_copy_prompt(self._prompt), format=lambda message: message,
                             stream=selected_stream, copy_static_params=True)
            usage_module_id = self._used_by_moduleid or self._module_id
            view.used_by(usage_module_id)
            budget = build_context_budget(current.max_input_tokens)
            call_kwargs = dict(kwargs)
            call_kwargs['max_retries'] = 1
            call_kwargs['timeout'] = _request_timeout(current)
            call_kwargs['output_limit'] = configured_output_limit(view, kwargs, budget.reserved_output_tokens)

            def begin_output(output_metadata=output_metadata):
                nonlocal call_index, call_open
                if call_open:
                    return
                state.model_output_index += 1
                call_index = state.model_output_index
                output_metadata['call_index'] = call_index
                call_open = True
                emit({'tag': 'model_output', 'model_output': {'call_index': call_index, 'phase': 'begin'}})

            def check_request(request, current=current, budget=budget, begin_output=begin_output):
                nonlocal requires_vision, sent_count
                control.check()
                requires_vision = requires_vision or request.requires_vision
                role_config = current.llm_config.get('llm') or {}
                if requires_vision and request.model_type != 'VLM' and role_config.get('vision') is not True:
                    raise UnsupportedModelInput('The configured model does not support this image input.')
                if request_tokens(request) > budget.effective_input_budget:
                    raise ContextCapacityError('The complete request exceeds the configured input budget.')
                control.check()
                if time.monotonic() >= deadline:
                    raise _RecoveryBudgetExhausted('The model recovery budget is exhausted.')
                sent_count += 1
                begin_output()

            try:
                response = view(
                    input, llm_chat_history=history, lazyllm_files=files,
                    call_control=ModelCallControl(cancel_check=self._check_cancel, request_check=check_request),
                    **call_kwargs)
                # A cache hit has no transport or stream callbacks, but its returned
                # value still belongs to a normal output commit boundary.
                begin_output()
            except CancelledError:
                rollback()
                raise
            except ModuleExecutionError as exc:
                rollback()
                exc._model_fallback_local_failure = True
                raise
            except Exception as exc:
                rollback()
                if local_failure or getattr(exc, '_model_fallback_local_failure', False):
                    failure = local_failure[0] if local_failure else exc
                    failure._model_fallback_local_failure = True
                    if failure is exc:
                        raise
                    raise failure from exc
                control.check()
                if isinstance(exc, _RecoveryBudgetExhausted):
                    exc.fallback_code = 'model_fallback_exhausted'
                    emit({'tag': 'model_fallback_failed', 'fallback_code': exc.fallback_code})
                    raise
                capacity_rejection = isinstance(exc, ContextCapacityError)
                input_rejection = capacity_rejection or isinstance(exc, UnsupportedModelInput)
                failure = exc.terminal.failure if isinstance(exc, ModelCallError) else None
                unsupported_call = isinstance(exc, UnsupportedModelInput) or failure is not None and str(
                    failure.provider_error_code or '',
                ).lower() in _UNSUPPORTED_CALL_CODES
                if failure is None and not input_rejection:
                    if not isinstance(exc, ModelCallError):
                        exc._model_fallback_local_failure = True
                    raise
                if not input_rejection and failure.code.value not in _RECOVERABLE and not unsupported_call:
                    raise
                if not input_rejection:
                    state.failed_candidates.add(current.candidate_id)
                if (failure is not None and current.provider_group_id and current.credential_count == 1
                        and str(failure.provider_error_code or '').lower() in _INVALID_KEY_CODES):
                    state.failed_credentials.add((current.provider_group_id, current.credential_revision))
                if not reason:
                    reason = ('model_context_not_fit' if capacity_rejection else
                              'provider_rejected' if unsupported_call else failure.code.value)
                only_context_failures = only_context_failures and capacity_rejection
                candidates = remaining_candidates()
                for candidate in candidates:
                    try:
                        raw = _make_llm(candidate)
                    except (ValueError, KeyError, AttributeError, TypeError):
                        attempted.add(candidate.candidate_id)
                        state.failed_candidates.add(candidate.candidate_id)
                        only_context_failures = False
                        continue
                    current = candidate
                    break
                else:
                    fallback_code = 'model_context_not_fit' if only_context_failures else 'model_fallback_exhausted'
                    exc.fallback_code = fallback_code
                    emit({'tag': 'model_fallback_failed', 'fallback_code': fallback_code})
                    raise exc
                continue
            finally:
                # FunctionCall clears its SDK aggregate after each tool round.
                # Merge its current round with the prior snapshot once, even
                # when this round tried several candidates.
                state.usage_snapshot = copy.deepcopy(lazyllm.OnlineChatModule.merge_usage_records(
                    prior_usage, lazyllm.globals['usage'].get(usage_module_id) or {},
                ))

            # Provider/protocol success is followed by local formatting and commit.
            # Failures here must leave the recovery catch, never request another model.
            try:
                if local_failure:
                    raise local_failure[0]
                result = self._formatter(response)
                state.current, state.raw_llm, state.budget = current, raw, budget
                initial = state.initial_model_switch
                commit_event = {'tag': 'model_output',
                                'model_output': {'call_index': call_index, 'phase': 'commit'}}
                if initial is not None or current.candidate_id != original.candidate_id:
                    commit_event.update(tag='runtime_event', runtime_event=runtime_event('model_switched', '', {
                        'switch_index': state.switch_index + 1,
                        'from': {key: initial['from'].get(key, '') for key in _public(original)}
                        if initial is not None else _public(original),
                        'to': _public(current),
                        'reason_code': initial['reason_code'] if initial is not None else reason,
                        'from_max_input_tokens': parse_token_limit(initial.get('from_max_input_tokens'))
                        if initial is not None else original.max_input_tokens,
                        'to_max_input_tokens': current.max_input_tokens,
                        'context_compacted': False,
                    }))
                emit(commit_event)
                call_open = False
                if initial is not None or current.candidate_id != original.candidate_id:
                    state.switch_index += 1
                    state.initial_model_switch = None
            except UserCancelledError:
                rollback()
                raise
            except Exception as exc:
                rollback()
                exc._model_fallback_local_failure = True
                raise
            return result
