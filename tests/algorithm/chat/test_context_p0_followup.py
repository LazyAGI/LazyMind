import json

from lazyllm.tools.agent import PreparedToolCall, ResolvedToolAccess, ToolExecutionDisposition, ToolExecutionRecord

from lazymind.chat.engine.agent_runtime.active_context import sidecar_fields
from lazymind.chat.engine.agent_runtime.summary_prompt import (
    has_required_goal_text,
    has_required_profile_blocks,
)
from lazymind.chat.engine.agent_runtime.summary_range import (
    AUTHORITATIVE_TASK_KIND,
    RUNTIME_SUMMARY_DISCLAIMER_PREFIX,
    select_summary_range,
)
from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolCallQuota
from lazymind.chat.engine.subagent.tools import _apply_str_replace


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
    '## Active skills',
    '[]',
    '## Artifact coordinates',
    '[]',
    '## Citation map',
    '[]',
    '## Spill paths',
    '[]',
])


def _history(n_user: int = 4):
    messages = []
    for index in range(n_user):
        messages.append({'role': 'user', 'content': f'user-{index} ' + ('x' * 80)})
        messages.append({'role': 'assistant', 'content': f'assistant-{index} ' + ('y' * 80)})
    return messages


def test_patch_miss_does_not_ask_model_to_get_artifact():
    _new, error = _apply_str_replace('hello draft', {'old_str': 'missing', 'new_str': 'x'})
    assert error is not None
    assert 'get_artifact' in error
    assert 'Do not call get_artifact' in error


def _prepared(name='get_artifact', arguments=None, call_id='call-1'):
    arguments = {'key': 'draft'} if arguments is None else arguments
    return PreparedToolCall(
        index=0,
        tool_call={
            'id': call_id,
            'function': {'name': name, 'arguments': json.dumps(arguments)},
        },
        call_id=call_id,
        tool_name=name,
        arguments=arguments,
        validated_arguments=arguments,
        access=ResolvedToolAccess(),
        polling=False,
    )


def test_tool_call_quota_blocks_after_cap():
    quota = ToolCallQuota({'get_artifact': 1})
    first = _prepared(call_id='a')
    assert quota.decide([first]) == {}
    quota.observe([
        ToolExecutionRecord(
            first, {'ok': True, 'value': {}},
            disposition=ToolExecutionDisposition.EXECUTED,
            reason='',
        ),
    ])
    blocked = quota.decide([_prepared(call_id='b')])
    assert 0 in blocked
    assert 'call cap' in str(blocked[0])


def test_select_summary_range_keeps_first_user_and_authoritative_task():
    history = _history(6)
    history[0]['_lazymind_meta'] = {'kind': AUTHORITATIVE_TASK_KIND}
    selected = select_summary_range(
        history,
        effective_input_budget=80,
        keep_recent_ratio=0.25,
        min_recent_user_turns=1,
    )
    assert selected is not None
    assert selected.replace_start >= 1
    assert selected.summary_messages[0] is not history[0]
    assert history[0] not in selected.summary_messages


def test_select_summary_range_still_rolls_leading_summary():
    history = [
        {
            'role': 'user',
            'content': f'{RUNTIME_SUMMARY_DISCLAIMER_PREFIX}\n\n{_VALID_SUMMARY}',
        },
        *(_history(5)),
    ]
    selected = select_summary_range(
        history,
        effective_input_budget=80,
        keep_recent_ratio=0.2,
        min_recent_user_turns=1,
    )
    assert selected is not None
    assert selected.replace_start == 0


def test_goal_sidecar_fail_closed_requires_verbatim_text():
    state = {
        'task_goal': 'Write the bid outline only',
        'hard_constraints': 'word_target=5000',
    }
    assert not has_required_goal_text(_VALID_SUMMARY, state)
    ok = _VALID_SUMMARY.replace(
        'Build the complete deliverable.',
        'Write the bid outline only',
    ).replace(
        'Preserve relevant tool evidence.',
        'word_target=5000',
    )
    assert has_required_goal_text(ok, state)
    assert has_required_profile_blocks(ok, 'default', state)


def test_subagent_does_not_truncate_artifact_locators():
    from lazymind.chat.engine.subagent.context import LARGE_TOOL_RESULT_SCAN_THRESHOLD_BYTES
    from lazymind.chat.engine.subagent.runner import _truncate_tool_result

    class _Ctx:
        workspace_path = '/tmp'

        def write_large_content(self, text, hint=''):
            raise AssertionError('artifact results must not use the generic spill cut')

    body = 'x' * (LARGE_TOOL_RESULT_SCAN_THRESHOLD_BYTES + 32)
    assert _truncate_tool_result(_Ctx(), body, 'get_artifact') == body


def test_sidecar_fields_include_goals():
    fields = sidecar_fields({
        'active_skills': [],
        'artifact_coords': [],
        'spill_paths': [],
        'citation_map': [],
        'task_goal': 'Keep the original user request',
        'key_instructions': '',
        'hard_constraints': 'output_format=docx',
    })
    assert fields['task_goal'] == 'Keep the original user request'
    assert fields['hard_constraints'] == 'output_format=docx'
