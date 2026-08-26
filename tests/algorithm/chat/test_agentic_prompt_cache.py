import copy
import json

import lazyllm

from lazymind.config import config
from lazymind.chat.engine.agent_runtime.context_estimator import estimate_tokens
from lazymind.chat.engine.agent_runtime.pruner import make_history_compactor


_VALID_SUMMARY = '\n'.join([
    '## Current task',
    'Build the complete deliverable.',
    '## Key constraints',
    'Preserve relevant tool evidence.',
    '## Progress and decisions',
    'Earlier tool rounds were completed.',
    '## Important files and tool results',
    'Deterministic test outputs were collected.',
    '## Pending work',
    'Continue remaining rounds.',
])


def _large_tool_result(value: int) -> str:
    '''Return a deterministic tool result large enough to exercise history compaction.'''
    return f'value={value}:' + ('x' * 1000)


def _compressible_tool_result(value: int) -> str:
    '''Return a deterministic result that the semantic compactor can reduce.'''
    return f'value={value}:' + ('x' * 1800)


class _RecordingLLM:
    def __init__(self, outputs):
        self._outputs = outputs
        self._cursor = 0
        self._module_id = f'cache-recording-llm-{id(self)}'
        self.inputs = []
        self.histories = []

    def share(self, prompt=None, format=None, stream=None, history=None, copy_static_params=False):
        return copy.copy(self)

    def used_by(self, module_id):
        return self

    def __call__(self, input, **kwargs):
        histories = lazyllm.locals.get('chat_history', {})
        self.histories.append(copy.deepcopy(histories.get(self._module_id, [])))
        self.inputs.append(copy.deepcopy(input))
        output = self._outputs[self._cursor]
        self._cursor += 1
        return output


def _messages_for_invocation(history, input_value):
    messages = copy.deepcopy(history)
    if isinstance(input_value, str):
        messages.append({'role': 'user', 'content': input_value})
    elif isinstance(input_value, dict) and isinstance(input_value.get('input'), list):
        messages.extend(copy.deepcopy(input_value['input']))
    else:
        raise AssertionError(f'Unsupported test input: {input_value!r}')
    return messages


def _serialize_message_prefix(messages):
    return ''.join(
        json.dumps(message, ensure_ascii=False, sort_keys=True, separators=(',', ':')) + '\n'
        for message in messages
    )


def _common_prefix_length(left, right):
    limit = min(len(left), len(right))
    index = 0
    while index < limit and left[index] == right[index]:
        index += 1
    return index


def test_agentic_history_keeps_two_recent_tool_results_by_default():
    assert config['agentic_keep_full_turns'] == 2


def test_default_agentic_history_keeps_weighted_prefix_cache_rate_above_90_percent():
    outputs = []
    for round_index in range(23):
        outputs.append({
            'role': 'assistant',
            'content': f'round {round_index + 1}',
            'tool_calls': [{
                'id': f'call-{round_index}-{tool_index}',
                'type': 'function',
                'function': {
                    'name': '_large_tool_result',
                    'arguments': json.dumps({'value': round_index * 4 + tool_index}),
                },
            } for tool_index in range(4)],
        })
    outputs.append({'role': 'assistant', 'content': 'done'})

    llm = _RecordingLLM(outputs)
    agent = lazyllm.tools.agent.ReactAgent(
        llm=llm,
        tools=[_large_tool_result],
        max_retries=24,
        keep_full_turns=0,
        stream=False,
        enable_builtin_tools=False,
    )

    assert agent('build the complete deliverable') == 'done'
    prompts = [
        _serialize_message_prefix(_messages_for_invocation(history, input_value))
        for history, input_value in zip(llm.histories, llm.inputs)
    ]

    weighted_prompt_chars = sum(len(prompt) for prompt in prompts)
    weighted_cacheable_prefix_chars = sum(
        _common_prefix_length(previous, current)
        for previous, current in zip(prompts, prompts[1:])
    )
    estimated_hit_rate = weighted_cacheable_prefix_chars / weighted_prompt_chars

    assert estimated_hit_rate >= 0.90, (
        f'agentic prompt prefix cache estimate fell to {estimated_hit_rate:.2%}; '
        'history should remain append-only'
    )
    assert all(current.startswith(previous) for previous, current in zip(prompts, prompts[1:]))


def test_compression_enabled_long_react_keeps_stable_batched_prefixes():
    outputs = []
    for round_index in range(18):
        outputs.append({
            'role': 'assistant',
            'content': f'round {round_index + 1}',
            'tool_calls': [{
                'id': f'call-{round_index}-{tool_index}',
                'type': 'function',
                'function': {
                    'name': '_compressible_tool_result',
                    'arguments': json.dumps({'value': round_index * 2 + tool_index}),
                },
            } for tool_index in range(2)],
        })
    outputs.append({'role': 'assistant', 'content': 'done'})

    llm = _RecordingLLM(outputs)
    with config.temp('context_compression_enabled', True), \
            config.temp('context_summary_compression_enabled', True), \
            config.temp('context_compression_reserved_output_tokens', 0), \
            config.temp('context_compression_trigger_ratio', 0.70), \
            config.temp('context_compression_target_ratio', 0.45):
        compactor = make_history_compactor(
            max_input_tokens=8_000,
            keep_recent=2,
            summarizer=lambda _system, _user: _VALID_SUMMARY,
        )
        agent = lazyllm.tools.agent.ReactAgent(
            llm=llm,
            tools=[_compressible_tool_result],
            max_retries=19,
            keep_full_turns=2,
            history_compactor=compactor,
            stream=False,
            enable_builtin_tools=False,
        )
        assert agent('build the complete deliverable') == 'done'

    prompts = [
        _serialize_message_prefix(_messages_for_invocation(history, input_value))
        for history, input_value in zip(llm.histories, llm.inputs)
    ]
    weighted_prompt_chars = sum(len(prompt) for prompt in prompts)
    weighted_cacheable_prefix_chars = sum(
        _common_prefix_length(previous, current)
        for previous, current in zip(prompts, prompts[1:])
    )
    estimated_hit_rate = weighted_cacheable_prefix_chars / weighted_prompt_chars
    cache_breaks = [
        index
        for index, (previous, current) in enumerate(zip(prompts, prompts[1:]), start=1)
        if not current.startswith(previous)
    ]

    assert estimated_hit_rate >= 0.50
    assert all(right - left > 1 for left, right in zip(cache_breaks, cache_breaks[1:]))
    assert all(estimate_tokens(prompt) <= 8_000 for prompt in prompts)
