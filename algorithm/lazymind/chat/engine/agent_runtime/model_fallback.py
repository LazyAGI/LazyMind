"""Recover one prepared Agent model call without entering the Agent again."""
from __future__ import annotations

import copy
import json
import random
import socket
from typing import Any

import lazyllm
import requests
from lazyllm.module import ModuleBase
from lazyllm.module.servermodule import LLMBase
from lazyllm.module.llms.onlinemodule.base.model_outcome import (
    ModelCallError, ModelCallTerminal, ModelFailure, ModelFailureCode, ModelFailureOrigin,
)
from lazymind.chat.engine.attachment_reader import is_chat_image_file

from .budget import build_context_budget, output_budget_kwargs, parse_token_limit
from .context_estimator import estimate_tokens
from .cancellation import UserCancelledError
from .models import FallbackRunState, ModelCandidate
from .message_fields import estimate_message_tokens
from lazymind.config import config
from lazymind.chat.runtime_events import runtime_event


_RECOVERABLE = frozenset({
    'authentication_failed', 'permission_denied', 'not_found', 'rate_limited',
    'usage_limit_exceeded', 'concurrency_limited', 'quota_exhausted', 'balance_exhausted',
    'organization_spend_limit_exceeded', 'project_spend_limit_exceeded', 'token_limit',
    'request_timeout', 'provider_overloaded', 'service_unavailable', 'provider_internal_error',
    'transport_error',
})
_INVALID_KEY_CODES = frozenset({'invalid_api_key', 'api_key_invalid', 'expired_api_key',
                                'revoked_api_key', 'invalid_authentication'})
_UNSUPPORTED_CALL_CODES = frozenset({'unsupported_tools', 'tools_not_supported', 'unsupported_function_call'})


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
        budget=build_context_budget(current.max_input_tokens, llm_config=options.llm_config),
        candidates=tuple([*known, *unknown]),
        initial_model_switch=copy.deepcopy(getattr(options, 'initial_model_switch', None)),
        workspace=getattr(options, 'workspace', None), keep_recent=getattr(options, 'keep_full_turns', None),
    )
    if state.initial_model_switch is not None:
        state.failed_candidates.add(state.initial_model_switch['from']['candidate_id'])
    return state


def _comparison_window(state: FallbackRunState):
    if state.initial_model_switch is not None:
        return parse_token_limit(state.initial_model_switch.get('from_max_input_tokens'))
    return state.current.max_input_tokens


def _ordered_candidates(state: FallbackRunState) -> list[ModelCandidate]:
    window = _comparison_window(state)

    def key(candidate):
        size = candidate.max_input_tokens
        if size is None:
            return 3, 0
        if window is None:
            return 0, -size
        if size == window:
            return 0, 0
        return (1, size) if size > window else (2, -size)

    return sorted((item for item in state.candidates
                   if item.candidate_id not in state.failed_candidates
                   and (item.provider_group_id, item.credential_revision) not in state.failed_credentials), key=key)


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


def _contains_image(value) -> bool:
    if isinstance(value, dict):
        return value.get('type') == 'image_url' or value.get('type') == 'input_image' or any(
            _contains_image(item) for item in value.values())
    return isinstance(value, (list, tuple)) and any(_contains_image(item) for item in value)


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
        timeout=config.get('timeout', 180), stream=False,
    )


def _preparation_error(code: ModelFailureCode, message: str) -> ModelCallError:
    return ModelCallError(message,
                          ModelCallTerminal('', 0, 'failure', False, failure=ModelFailure(
                              ModelFailureOrigin.PROTOCOL, code)))


def _failure_from_error(error: Exception):
    seen = set()
    network_failure = None
    while error is not None and id(error) not in seen:
        seen.add(id(error))
        if isinstance(error, ModelCallError):
            return error.terminal.failure
        failure = getattr(error, 'failure', None)
        if isinstance(failure, ModelFailure):
            return failure
        if isinstance(error, (requests.exceptions.Timeout, socket.timeout)):
            network_failure = ModelFailure(ModelFailureOrigin.TRANSPORT, ModelFailureCode.REQUEST_TIMEOUT)
        elif isinstance(error, (requests.exceptions.ConnectionError, requests.exceptions.ChunkedEncodingError,
                                ConnectionResetError, ConnectionAbortedError, BrokenPipeError, socket.gaierror)):
            network_failure = ModelFailure(ModelFailureOrigin.TRANSPORT, ModelFailureCode.TRANSPORT_ERROR)
        error = error.__cause__ or error.__context__
    return network_failure


def _request_tokens(view, input, history, files, kwargs) -> int:
    normalized, attached = view._get_files(copy.deepcopy(input), copy.deepcopy(files))
    data = view._prompt.generate_prompt(
        input=normalized, history=copy.deepcopy(history), format=view._message_format, tools=kwargs.get('tools'),
    )
    if view.type == 'VLM' and (attached or view._vlm_force_format_input_with_files):
        data['messages'][-1]['content'] = view._format_input_with_files(data['messages'][-1]['content'], attached)
        for message in data['messages'][:-1]:
            if message.get('role') == 'user' and isinstance(message.get('content'), str):
                message['content'] = view._format_vl_chat_query(message['content'])
    data.update(view._static_params)
    data.update({key: value for key, value in kwargs.items() if key not in {'max_retries', 'tools'}})
    data = view._prepare_request_data(data)
    messages = data.pop('messages', [])
    return estimate_tokens(json.dumps({**data, 'messages': []}, ensure_ascii=False, default=str)) + sum(
        estimate_message_tokens(message) + 4 for message in messages
    )


def _prepare_context(raw, supplier, input, history, files, kwargs, *, state, budget, projection, summary_runtime):
    from .pruner import prepare_history_compaction, _pressure_rearm_tokens
    from .projection_state import split_projection
    from .message_fields import model_facing_history

    wrapped_input = isinstance(input, dict) and set(input) == {'input'}
    current_input = input['input'] if wrapped_input else input
    current_count = int((projection or {}).get('_projected_current_count') or 0)
    if current_count:
        _, expected_current = split_projection(projection['entries'], projection['_source_prior_len'])
        if not isinstance(current_input, list) \
                or model_facing_history(current_input[:current_count]) != expected_current:
            raise _preparation_error(ModelFailureCode.TOKEN_LIMIT, 'The prepared source boundary no longer matches.')
    current = copy.deepcopy(current_input[:current_count]) if isinstance(current_input, list) else []
    fixed_current = current_input[current_count:] if isinstance(current_input, list) else current_input
    fixed_input = {'input': fixed_current or ''} if wrapped_input else fixed_current
    fixed_tokens = _request_tokens(supplier, fixed_input or '', [], files, kwargs)
    if fixed_tokens > budget.effective_input_budget:
        raise _preparation_error(ModelFailureCode.TOKEN_LIMIT, 'The fixed request does not fit this candidate.')
    prepared = prepare_history_compaction(
        copy.deepcopy(history or []), budget=budget, keep_recent=state.keep_recent, workspace=state.workspace,
        llm=raw, projection_state=projection, current_round_messages=current,
        non_history_tokens=fixed_tokens, summary_runtime=summary_runtime,
    )
    if prepared.summary_event is not None and prepared.summary_event.decision != 'summarized':
        raise _preparation_error(ModelFailureCode.TOKEN_LIMIT, 'The candidate summary did not pass validation.')
    if isinstance(current_input, list) and current_count:
        prepared.input = [*prepared.current_messages, *fixed_current]
        if wrapped_input:
            prepared.input = {'input': prepared.input}
    else:
        prepared.input = input
    prepared.context_compacted = prepared.history != (history or []) or prepared.input != input
    total = _request_tokens(supplier, prepared.input, prepared.history, files, kwargs)
    if total > budget.effective_input_budget:
        raise _preparation_error(ModelFailureCode.TOKEN_LIMIT, 'The prepared request does not fit this candidate.')
    prepared.projection['next_pressure_tokens'] = _pressure_rearm_tokens(total, budget)
    return prepared


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

    def forward(self, input=None, *, llm_chat_history=None, lazyllm_files=None,
                stream_output=None, stream=None, **kwargs):
        state = self.state
        prior_usage = state.usage_snapshot
        current, raw = state.current, state.raw_llm
        original = state.current
        workspace = (lazyllm.locals.get('_lazyllm_agent') or {}).get('workspace') or {}
        projection_target = workspace.get('_history_projection_state')
        projection, summary_runtime = None, None
        reason = ''
        only_context_failures = True
        stream = stream_output if stream_output is not None else stream
        stream = self._stream if stream is None else stream
        outer_sink = stream.get('_stream_sink') if isinstance(stream, dict) else getattr(self, '_stream_sink', None)
        _, attached_files = self._get_files(copy.deepcopy(input), copy.deepcopy(lazyllm_files))
        requires_vision = any(is_chat_image_file(path) for path in attached_files) \
            or _contains_image([input, llm_chat_history])

        def emit(event):
            try:
                if outer_sink is not None:
                    outer_sink(event)
                else:
                    lazyllm.FileSystemQueue().enqueue(json.dumps(event, ensure_ascii=False))
            except Exception as exc:
                exc._model_fallback_local_failure = True
                raise

        while True:
            if self._cancel_check is not None:
                if self._cancel_check(None):
                    raise UserCancelledError('stopped before the next model attempt')
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
            budget = build_context_budget(current.max_input_tokens) if current.max_input_tokens else state.budget
            with lazyllm.globals.stack_enter(view.identities):
                supplier_factory = getattr(view, '_get_supplier', None)
                supplier = supplier_factory() if supplier_factory is not None else view
                supplier.used_by(usage_module_id)
                call_kwargs = output_budget_kwargs(view, supplier, budget.reserved_output_tokens, copy.deepcopy(kwargs))
            call_kwargs['max_retries'] = 1
            history, files = copy.deepcopy(llm_chat_history), copy.deepcopy(lazyllm_files)
            prepared = None
            preparing_context = False
            candidate_input = copy.deepcopy(input)
            try:
                role_config = current.llm_config.get('llm') or {}
                if requires_vision and supplier.type != 'VLM' and role_config.get('vision') is not True:
                    raise _preparation_error(ModelFailureCode.PROVIDER_REJECTED,
                                             'The configured model does not support this image input.')
                if current.max_input_tokens:
                    if _request_tokens(supplier, input, history, files, call_kwargs) > budget.effective_input_budget:
                        if not config['context_compression_enabled']:
                            raise _preparation_error(ModelFailureCode.TOKEN_LIMIT,
                                                     'The complete request exceeds the configured input budget.')
                        if summary_runtime is None:
                            from .active_context import collect_summary_runtime_state
                            projection = copy.deepcopy(projection_target) \
                                if projection_target and '_source_prior_len' in projection_target else None
                            summary_runtime = copy.deepcopy(collect_summary_runtime_state(
                                lazyllm.globals.get('agentic_config') or {},
                            ))
                        preparing_context = True
                        prepared = _prepare_context(
                            raw, supplier, input, history, files, call_kwargs, state=state, budget=budget,
                            projection=projection, summary_runtime=summary_runtime,
                        )
                        candidate_input, history = prepared.input, prepared.history
                preparing_context = False
                if self._cancel_check is not None and self._cancel_check(None):
                    raise UserCancelledError('stopped before the prepared model attempt')
                state.model_output_index += 1
                call_index = state.model_output_index
                output_metadata['call_index'] = call_index
                call_open = True
                emit({'tag': 'model_output', 'model_output': {'call_index': call_index, 'phase': 'begin'}})
                response = view(candidate_input, llm_chat_history=history, lazyllm_files=files,
                                stream_output=selected_stream, **call_kwargs)
            except UserCancelledError:
                rollback()
                raise
            except Exception as exc:
                rollback()
                if local_failure or getattr(exc, '_model_fallback_local_failure', False):
                    failure = local_failure[0] if local_failure else exc
                    failure._model_fallback_local_failure = True
                    if failure is exc:
                        raise
                    raise failure from exc
                failure = _failure_from_error(exc)
                unsupported_call = failure is not None and str(
                    failure.provider_error_code or '',
                ).lower() in _UNSUPPORTED_CALL_CODES
                terminal = getattr(exc, 'terminal', None)
                unsupported_call = unsupported_call or (
                    failure is not None and terminal is not None
                    and terminal.attempt_count == 0 and failure.code == ModelFailureCode.PROVIDER_REJECTED
                )
                if (failure is None
                        or (failure.code.value not in _RECOVERABLE and not unsupported_call and not preparing_context)):
                    raise
                state.failed_candidates.add(current.candidate_id)
                if (current.provider_group_id and current.credential_count == 1
                        and str(failure.provider_error_code or '').lower() in _INVALID_KEY_CODES):
                    state.failed_credentials.add((current.provider_group_id, current.credential_revision))
                if not reason:
                    reason = 'provider_rejected' if unsupported_call else failure.code.value
                only_context_failures = only_context_failures and failure.code.value == 'token_limit'
                candidates = _ordered_candidates(state)
                if requires_vision:
                    candidates = [item for item in candidates
                                  if (item.llm_config.get('llm') or {}).get('vision') is True
                                  or str((item.llm_config.get('llm') or {}).get('type') or '').lower() == 'vlm']
                for candidate in candidates:
                    try:
                        raw = _make_llm(candidate)
                    except (ValueError, KeyError, AttributeError, TypeError):
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
                state.usage_snapshot = copy.deepcopy(supplier._merge_usage_records(
                    prior_usage, lazyllm.globals['usage'].get(usage_module_id) or {},
                ))

            # Provider/protocol success is followed by local formatting and commit.
            # Failures here must leave the recovery catch, never request another model.
            try:
                if local_failure:
                    raise local_failure[0]
                result = self._formatter(response)
                if prepared is not None:
                    from .pruner import commit_prepared_context
                    commit_prepared_context(prepared, runtime_state=projection_target, workspace=state.workspace)
                elif projection_target is not None and current.candidate_id != original.candidate_id:
                    from .pruner import _pressure_rearm_tokens
                    total = _request_tokens(supplier, candidate_input, history, files, call_kwargs)
                    projection_target['next_pressure_tokens'] = _pressure_rearm_tokens(total, budget)
                state.current, state.raw_llm, state.budget = current, raw, budget
                if state.tool_retrieval is not None:
                    state.tool_retrieval.threshold_tokens = int(budget.effective_input_budget * 0.1)
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
                        'context_compacted': prepared.context_compacted if prepared is not None else False,
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
