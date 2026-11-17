"""Replayable generation boundary for shared artifact-action planners.

The package still owns all prompts, selection validation and mutation. External
hosts supply only model responses; context-local replay never changes global
model configuration or monkey-patches a cached package module.
"""
from contextvars import ContextVar
from contextlib import contextmanager
import hashlib
import json

_state = ContextVar('workflow_action_generation', default=None)


class GenerationRequired(Exception):
    def __init__(self, request):
        self.request = request
        super().__init__('external generation required')


@contextmanager
def generation_scope(responses):
    token = _state.set(None if responses is None else {'responses': responses, 'cursor': 0})
    try:
        yield
    finally:
        _state.reset(token)


def generate(system_prompt, user_prompt, request_name='llm'):
    state = _state.get()
    if state is None:
        return None
    request = {'system_prompt': system_prompt, 'user_prompt': user_prompt, 'request_name': request_name}
    digest = hashlib.sha256(json.dumps(request, sort_keys=True, ensure_ascii=False).encode()).hexdigest()
    index = state['cursor']
    state['cursor'] += 1
    request.update(request_hash=digest, index=index)
    if index >= len(state['responses']):
        raise GenerationRequired(request)
    response = state['responses'][index]
    if response.get('request_hash') != digest or not isinstance(response.get('text'), str):
        raise ValueError('external generation replay does not match the shared planner')
    return response['text']
