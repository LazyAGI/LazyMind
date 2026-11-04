"""Exercise the real question schema, ToolManager and event queue; no model substitutes."""
import copy
import json
import uuid

import lazyllm
import pytest
from lazyllm.tools import ToolManager

from lazymind.chat.engine.tools.ask_user import AskUserQuestion, _normalise_questions
from lazymind.chat.service.chat_service import _build_ask_user_tool
from lazymind.chat.workflow.workflow_manager import (
    _merge_startup_clarification_context,
    _history_startup_ask_index,
)


@pytest.fixture
def recorded_question():
    # The parameter shape that failed three times in 3cb16dfa... on 2026-10-21.
    return {
        'question': '实验运行方式：是否允许本次任务运行实验？',
        'header': '实验运行方式', 'multi_select': False,
        'options': [
            {'label': '不允许，仅生成代码和模拟数据', 'description': '不运行代码。'},
            {'label': '允许受限运行', 'description': '仅受限环境。'},
        ],
    }


def test_tool_schema_describes_actual_question_fields():
    manager = ToolManager(_build_ask_user_tool())
    schema = manager.tools_description[0]['function']['parameters']
    branches = schema['properties']['questions']['items']['oneOf']
    assert {branch['properties']['type']['const'] for branch in branches} == {
        'boolean', 'single', 'multiple', 'text'}
    for branch in branches:
        assert {'id', 'text', 'reason', 'required', 'type'} <= set(branch['required'])
        if 'choices' in branch['properties']:
            assert branch['properties']['choices']['items'] == {'type': 'string', 'minLength': 1}



@pytest.mark.parametrize('kind,choices', [
    ('boolean', []), ('text', []), ('single', ['A', 'B']), ('multiple', ['A', 'B']),
])
def test_existing_question_types_remain_compatible(kind, choices):
    manager = ToolManager(_build_ask_user_tool())
    question = {'id': 'scope', 'text': 'Question?', 'reason': 'Choose the scope.',
                'required': True, 'type': kind}
    if kind in {'single', 'multiple'}:
        question['choices'] = choices
    elif kind == 'boolean':
        question['boolean_labels'] = {'yes': 'Use this scope', 'no': 'Choose another scope'}
    arguments, failure = manager._validate_tool('ask_user', {'questions': [question]})
    assert failure is None
    result = _normalise_questions(arguments['questions'])[0]
    assert result['type'] == kind
    if kind in {'single', 'multiple'}:
        assert result['choices'] == [*choices, '__ask_user_other__']


@pytest.mark.parametrize('governed', [False, True])
@pytest.mark.parametrize('wire_format', ['options', 'item_wrapper'])
def test_recorded_arguments_publish_one_real_question_event(recorded_question, governed, wire_format):
    before = copy.deepcopy(recorded_question)
    question_input = recorded_question
    if wire_format == 'item_wrapper':
        question_input = {
            'text': recorded_question['question'], 'type': 'single',
            'choices': {'item': [item['label'] for item in recorded_question['options']]},
            'allow_other': 'true',
        }
    question_input = AskUserQuestion.model_validate(question_input).model_dump(exclude_none=True, exclude_unset=True)
    question_input.update(id='execution', reason='本次实验需要明确运行范围。', required=True)
    runtime = {'clarification_fields': [{
        'id': 'execution', 'question': '是否允许本次任务运行实验？',
        'type': 'single', 'choice_policy': 'fixed', 'allow_other': False,
        'choices': ['不允许，仅生成代码和模拟数据', '允许受限运行'],
    }]} if governed else None
    with lazyllm.globals._bind_sid('ask-user-test-' + uuid.uuid4().hex):
        queue = lazyllm.FileSystemQueue()
        manager = ToolManager(_build_ask_user_tool(runtime))
        from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolExecutionMiddleware

        result = ToolExecutionMiddleware(manager).execute_with_records({
            'id': 'recorded-call', 'type': 'function', 'function': {
                'name': 'ask_user', 'arguments': json.dumps({'questions': [question_input]}),
            },
        }).results[0]
        assert result['ok'], result
        events = [event for item in queue.dequeue()
                  if (event := json.loads(item)).get('tag') == 'ask_pending']
    assert len(events) == 1
    question = events[0]['questions'][0]
    assert events[0]['tag'] == 'ask_pending'
    assert question['text'] == recorded_question['question']
    assert question['type'] == 'single'
    assert question['choices'][:2] == [item['label'] for item in recorded_question['options']]
    assert question['allow_other'] is not governed
    assert ('__ask_user_other__' in question['choices']) is not governed
    assert recorded_question == before


def test_accepted_aliases_preserve_goal_on_answer_turn(recorded_question):
    history = [
        {'role': 'user', 'content': '比较均值和中位数，设计实验'},
        {'role': 'assistant', 'tool_calls': [{
            'id': 'ask-1', 'function': {'name': 'ask_user', 'arguments': json.dumps({
                'questions': [recorded_question],
            })},
        }]},
        {'role': 'tool', 'tool_call_id': 'ask-1', 'name': 'ask_user',
         'content': 'Question sent to user. Waiting for answer on next turn.'},
    ]
    runtime = {'clarification_fields': [{
        'id': 'execution', 'question': '是否允许本次任务运行实验？', 'type': 'single',
        'choices': ['不允许', '允许'],
    }]}
    assert _history_startup_ask_index(history, runtime) == 1
    merged = _merge_startup_clarification_context('不允许', history, runtime)
    assert '比较均值和中位数，设计实验' in merged
    assert 'Clarification answers:\n不允许' in merged
    history[-1]['content'] = json.dumps({'ok': False, 'value': 'Invalid arguments'})
    assert _history_startup_ask_index(history, runtime) == -1
    assert _merge_startup_clarification_context('重新开始', history, runtime) == '重新开始'


@pytest.mark.parametrize('question', [
    {'text': ''}, {'text': 'Q', 'type': 'unsupported'},
    {'text': 'Q', 'question': 'Different Q'},
    {'text': 'Q', 'choices': [1]}, {'text': 'Q', 'allow_other': 'maybe'},
    {'question': 'Q', 'options': ['A'], 'multi_select': 'maybe'},
    {'text': 'Q', 'choices': {'item': ['A'], 'extra': ['B']}},
])
def test_invalid_or_conflicting_values_are_not_silently_guessed(question):
    with pytest.raises(ValueError):
        AskUserQuestion.model_validate(question)


def test_multiple_alias_and_existing_choice_wrapper_are_normalized():
    multiple = AskUserQuestion.model_validate({
        'question': 'Q', 'options': [{'label': 'A'}, {'label': 'B'}], 'multi_select': True,
    })
    assert multiple.type == 'multiple'
    assert multiple.choices == ['A', 'B']
    canonical = AskUserQuestion.model_validate({
        'text': 'Q', 'type': 'single', 'choices': [{'label': 'A'}],
    })
    assert canonical.choices == ['A']


@pytest.mark.parametrize('flag,expected', [('false', False), ('true', True)])
def test_provider_boolean_strings_preserve_their_value(flag, expected):
    question = AskUserQuestion.model_validate({
        'text': 'Q', 'type': 'single', 'choices': {'item': ['A', 'B']}, 'allow_other': flag,
    })
    assert question.allow_other is expected
