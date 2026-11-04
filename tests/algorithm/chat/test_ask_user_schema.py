"""Exercise the real tool schema and card emission, including host-governed inputs."""
import copy
import json

import lazyllm
import pytest
from lazyllm.tools.agent import FunctionCall, ToolExecutionError, ToolManager

from lazymind.chat.engine.tools import ask_user as module
from lazymind.chat.engine.tools.ask_user_schema import validate_model_questions
from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolExecutionMiddleware
from lazymind.chat.service.chat_service import _build_ask_user_tool


def question(kind='single', **kwargs):
    return {'id': 'scope', 'text': '选择本次的范围', 'reason': '这会决定交付内容。',
            'required': True, 'type': kind, **kwargs}


def schema(tool=module.ask_user):
    return ToolManager([tool]).tools_description[0]['function']['parameters']['properties']['questions']


@pytest.fixture
def emitted(monkeypatch):
    events = []
    monkeypatch.setattr(module, '_write_agent_data', lambda tag, **payload: events.append((tag, payload)))
    return events


def test_model_receives_typed_questions_and_distinct_open_fixed_limits():
    questions = schema()
    assert questions['minItems'] == 1
    assert questions['maxItems'] == 3
    assert '$ref' not in json.dumps(questions)
    branches = questions['items']['oneOf']
    assert len(branches) == 6
    for branch in branches:
        properties = branch['properties']
        assert {'id', 'text', 'reason', 'required', 'type'} <= set(branch['required'])
        assert branch['additionalProperties'] is False
        kind = properties['type']['const']
        if kind in ('single', 'multiple'):
            choices = properties['choices']
            if properties['choice_policy'].get('const') == 'fixed':
                assert 'maxItems' not in choices
                assert 'choice_policy' in branch['required']
            else:
                assert choices['maxItems'] == (3 if kind == 'single' else 4)
        else:
            assert 'choices' not in properties
            if kind == 'boolean':
                assert 'boolean_labels' in branch['required']


@pytest.mark.parametrize('index', range(4))
def test_each_model_visible_example_emits_a_valid_existing_card(index, emitted):
    questions = copy.deepcopy(schema()['examples'][index])
    tool = ToolManager([module.ask_user]).all_tools[0]
    receipt = tool({'questions': questions})
    assert 'Question sent to user' in receipt
    tag, card = emitted[0]
    assert tag == 'ask_pending'
    assert card['ask_id']
    result = card['questions'][0]
    assert result['id'] == questions[0]['id']
    assert result['required'] is True
    if result['type'] in ('single', 'multiple'):
        assert result['choices'] == [*questions[0]['choices'], '__ask_user_other__']
    elif result['type'] == 'boolean':
        assert result['choices'] == ['__ask_user_yes__', '__ask_user_no__']
        assert result['boolean_labels'] == questions[0]['boolean_labels']
    else:
        assert result['choices'] == []
        assert result['validation'] == questions[0]['validation']


@pytest.mark.parametrize('kind,count', [('single', 1), ('single', 3), ('multiple', 1), ('multiple', 4)])
def test_open_choices_allow_fewer_than_the_cap_without_padding(kind, count, emitted):
    choices = [str(index) for index in range(count)]
    module.ask_user([question(kind, choices=choices)])
    assert emitted[0][1]['questions'][0]['choices'] == [*choices, '__ask_user_other__']


@pytest.mark.parametrize('kind', ['single', 'multiple'])
def test_fixed_enums_are_complete_and_do_not_get_other_by_default(kind, emitted):
    choices = list('ABCDEFGH')
    module.ask_user([question(kind, choices=choices, choice_policy='fixed')])
    result = emitted[0][1]['questions'][0]
    assert result['choices'] == choices
    assert result['allow_other'] is False


def test_one_failure_reports_missing_body_excess_choices_and_duplicate_ids_together(emitted):
    missing_body = question('text')
    del missing_body['text']
    with pytest.raises(ToolExecutionError) as error:
        module.ask_user([missing_body, question(choices=list('ABCDE'))])
    message = str(error.value)
    assert 'questions[0].text' in message
    assert 'questions[1].choices' in message
    assert 'questions[1].id' in message
    assert emitted == []


@pytest.mark.parametrize('questions,path', [
    ([], 'questions'),
    ([question('text', id=str(index)) for index in range(4)], 'questions'),
    ([question('text', text='  ')], 'questions[0].text'),
    ([question('text', required='true')], 'questions[0].required'),
    ([question('text', choices=['A'])], 'questions[0].choices'),
    ([question('boolean')], 'questions[0].boolean_labels'),
    ([question('boolean', boolean_labels={'yes': '生成', 'no': '生成'})], 'questions[0].boolean_labels'),
    ([question('multiple', choices=list('ABCDE'))], 'questions[0].choices'),
    ([question(choices=['A', ' A '])], 'questions[0].choices'),
    ([question(choices=['A', '__ask_user_other__'])], 'questions[0].choices'),
    ([question(choices=[{'label': 'A'}])], 'questions[0].choices[0]'),
    ([question('text', validation={'min_length': 20, 'max_length': 10})], 'questions[0].validation'),
    ([question('text', validation={'min': float('inf')})], 'questions[0].validation.min'),
    ([question('text', validation={'max_length': True})], 'questions[0].validation.max_length'),
    (['not a question'], 'questions[0]'),
])
def test_invalid_model_arguments_are_not_silently_repaired_or_emitted(questions, path, emitted):
    with pytest.raises(ToolExecutionError) as error:
        module.ask_user(questions)
    assert path in str(error.value)
    assert emitted == []


def test_text_business_limits_survive_card_emission(emitted):
    rule = {'format': 'positive_integer', 'example': '8', 'min': 1, 'max': 100}
    module.ask_user([question('text', validation=rule)])
    assert emitted[0][1]['questions'][0]['validation'] == rule


def test_governed_tool_keeps_schema_and_restores_complete_enum_and_upload(emitted):
    runtime = {'clarification_fields': [{
        'id': 'template', 'question': '选择模板', 'type': 'single',
        'choice_policy': 'fixed', 'choices': ['上传模板', *list('ABCDEFG')], 'allow_other': False,
        'attachment': {'when_choice': '上传模板', 'material_id': 'source',
                       'accept': ['.pptx'], 'required': True},
    }]}
    tool = _build_ask_user_tool(runtime)[0]
    assert schema(tool) == schema()
    # The host restores the enum before validation, even if the model sends the
    # full enum without choice_policy or the upload metadata.
    ToolManager([tool]).all_tools[0]({'questions': [question(
        id='template', choices=['上传模板', *list('ABCDEFG')],
    )]})
    result = emitted[0][1]['questions'][0]
    assert result['choices'] == runtime['clarification_fields'][0]['choices']
    assert result['attachment']['material_id'] == 'source'
    assert result['attachment']['required'] is True


def test_host_authored_card_contract_still_accepts_legacy_confirmation_payloads():
    card = module.question_card_payload([{'text': '确认删除？', 'type': 'boolean'}], description='此操作无法撤销。')
    assert card['questions'][0]['choices'] == ['__ask_user_yes__', '__ask_user_no__']


def test_model_validation_preserves_input_and_optional_false():
    original = [question(choices=['A'], required=False, allow_other=False)]
    before = copy.deepcopy(original)
    assert validate_model_questions(original) == original
    assert original == before


def test_function_call_sends_schema_and_all_errors_to_the_next_model_round(emitted):
    class RecordingLLM:
        def __init__(self, outputs):
            self.outputs = iter(outputs)
            self.requests = []
            self._module_id = f'ask-schema-{id(self)}'

        def share(self, prompt=None, **kwargs):
            cloned = copy.copy(self)
            cloned.prompt = prompt
            return cloned

        def used_by(self, module_id):
            return self

        def __call__(self, input):
            history = lazyllm.locals['chat_history'].get(self._module_id, [])
            self.requests.append(self.prompt.generate_prompt(input, history=history, format='openai'))
            return next(self.outputs)

    def call(questions, call_id):
        return {'role': 'assistant', 'content': '', 'tool_calls': [{
            'id': call_id, 'type': 'function', 'function': {
                'name': 'ask_user', 'arguments': json.dumps({'questions': questions}),
            },
        }]}

    invalid = question('single', choices=list('ABCDE'))
    del invalid['text']
    corrected = question('text')
    llm = RecordingLLM([call([invalid], 'invalid'), call([corrected], 'corrected')])
    previous_cfg = lazyllm.globals.get('agentic_config')
    previous_agent = lazyllm.locals.get('_lazyllm_agent')
    previous_history = lazyllm.locals['chat_history'].copy()
    lazyllm.globals['agentic_config'] = {}
    lazyllm.locals['_lazyllm_agent'] = {}
    try:
        fc = FunctionCall(llm, _tool_manager=ToolExecutionMiddleware(ToolManager([module.ask_user])))
        first = fc('帮我润色一下这段话。')
        assert emitted == []
        fc(first)
        sent_schema = llm.requests[0]['tools'][0]['function']['parameters']['properties']['questions']
        assert sent_schema == schema()
        correction_context = json.dumps(llm.requests[1]['messages'], ensure_ascii=False)
        assert 'questions[0].text' in correction_context
        assert 'questions[0].choices' in correction_context
        assert len(emitted) == 1
        assert emitted[0][1]['questions'][0]['type'] == 'text'
    finally:
        lazyllm.globals['agentic_config'] = previous_cfg
        lazyllm.locals['_lazyllm_agent'] = previous_agent
        lazyllm.locals['chat_history'].clear()
        lazyllm.locals['chat_history'].update(previous_history)
