import copy
import json

import lazyllm

from lazymind.config import config


def _large_tool_result(value: int) -> str:
    '''Return a deterministic tool result large enough to exercise history compaction.'''
    return f'value={value}:' + ('x' * 1000)


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


def test_agentic_history_compaction_is_disabled_by_default():
    assert config['agentic_keep_full_turns'] == 0


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
        keep_full_turns=config['agentic_keep_full_turns'],
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
