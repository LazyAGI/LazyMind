"""Exercise the middleware / FunctionCall / generated model-request boundary."""
import copy
import json

import lazyllm
import pytest
from lazyllm.tools.agent import FunctionCall, ToolManager, fc_register
from lazyllm.tools.agent.base import _model_facing_prefix

from lazymind.chat.engine.agent_runtime.compactors import compact_skill_result
from lazymind.chat.engine.agent_runtime.tool_call_guard import ToolExecutionMiddleware
from lazymind.chat.engine.agent_runtime.workflow_compactor import make_workflow_history_compactor


class RecordingLLM:
    def __init__(self, outputs):
        self.outputs = iter(outputs)
        self.requests = []
        self._module_id = f'recording-{id(self)}'

    def share(self, prompt=None, **kwargs):
        cloned = copy.copy(self)
        cloned.prompt = prompt
        return cloned

    def used_by(self, _module_id):
        return self

    def __call__(self, input):
        history = lazyllm.locals['chat_history'].get(self._module_id, [])
        self.requests.append(self.prompt.generate_prompt(input, history=history, format='openai'))
        return next(self.outputs)


@pytest.fixture(autouse=True)
def isolated_context():
    previous_cfg = lazyllm.globals.get('agentic_config')
    previous_agent = lazyllm.locals.get('_lazyllm_agent')
    previous_history = lazyllm.locals['chat_history'].copy()
    lazyllm.globals['agentic_config'] = {}
    lazyllm.locals['_lazyllm_agent'] = {}
    try:
        yield
    finally:
        lazyllm.globals['agentic_config'] = previous_cfg
        lazyllm.locals['_lazyllm_agent'] = previous_agent
        lazyllm.locals['chat_history'].clear()
        lazyllm.locals['chat_history'].update(previous_history)


@fc_register(host_file='NONE')
def get_skill(name: str) -> dict:
    '''Read a skill.

    Args:
        name (str): Skill name.
    '''
    return {'status': 'ok', 'name': name, 'path': '/skills/demo/SKILL.md',
            'content': 'Always preserve the original word target.'}


@fc_register(host_file='NONE')
def read_reference(name: str, rel_path: str) -> dict:
    '''Read a skill reference.

    Args:
        name (str): Skill name.
        rel_path (str): Reference path.
    '''
    return {'status': 'ok', 'name': name, 'rel_path': rel_path,
            'content': 'Reference: output must include a bibliography.'}


def call(name, args, call_id):
    return {'role': 'assistant', 'content': '', 'tool_calls': [
        {'id': call_id, 'type': 'function', 'function': {'name': name, 'arguments': json.dumps(args)}},
    ]}


def test_skill_and_reference_bodies_reach_model_without_hidden_pin():
    llm = RecordingLLM([
        call('get_skill', {'name': 'demo'}, 'skill'),
        call('read_reference', {'name': 'demo', 'rel_path': 'ref.md'}, 'ref'),
        {'content': 'done'},
    ])
    fc = FunctionCall(llm, _tool_manager=ToolExecutionMiddleware(ToolManager([get_skill, read_reference])))
    assert fc(fc(fc('start'))) == 'done'
    sent = json.dumps(llm.requests[-1])
    assert 'Always preserve the original word target.' in sent
    assert 'Reference: output must include a bibliography.' in sent
    assert not lazyllm.globals['agentic_config'].get('pinned_skill_prompt')


def test_budget_prefix_does_not_include_unsent_business_state():
    lazyllm.globals['agentic_config'] = {'pinned_skill_prompt': 'UNSENT PIN'}
    manager = ToolManager([get_skill])
    prefix = _model_facing_prefix('system', manager)
    llm = RecordingLLM([{'content': 'done'}])
    fc = FunctionCall(llm, _tool_manager=manager, _prompt='system')
    assert fc('start') == 'done'
    assert prefix['system_prompt'] in llm.requests[0]['messages'][0]['content']
    assert 'UNSENT PIN' not in prefix['system_prompt']


@pytest.mark.parametrize('tool,args', [
    ('get_skill', {'name': 'demo'}),
    ('read_reference', {'name': 'demo', 'rel_path': 'reference/rules.md'}),
])
def test_compacted_skill_result_is_reloadable_without_claiming_pin(tool, args):
    payload = globals()[tool](**args)
    text, kind = compact_skill_result(tool, payload)
    assert kind == 'skill_locator'
    assert 'pinned' not in text.lower()
    assert tool in text
    assert 'demo' in text
    assert 'Reload' in text
    if tool == 'read_reference':
        assert 'reference/rules.md' in text
    assert lazyllm.globals['agentic_config'] == {}


def test_function_call_supplies_complete_current_turn_contract(tmp_path):
    seen = []
    workflow = make_workflow_history_compactor(max_input_tokens='32K', workspace=str(tmp_path))

    def compact(history, keep, *, tool_turns=None, **kwargs):
        if kwargs.get('current_round_messages'):
            assert tool_turns is not None
            seen.extend(tool_turns)
        return workflow(history, keep, tool_turns=tool_turns, **kwargs)

    llm = RecordingLLM([call('get_skill', {'name': 'demo'}, 'skill'), {'content': 'done'}])
    fc = FunctionCall(llm, _tool_manager=ToolManager([get_skill]), history_compactor=compact)
    assert fc(fc('start')) == 'done'
    assert len(seen) == 1
    assert seen[0].current is True
    assert (seen[0].start, seen[0].stop, seen[0].result_indexes) == (1, 3, (2,))


def test_real_reference_result_can_be_compacted_and_reloaded(tmp_path):
    from lazyllm.tools.agent import SkillManager

    skill = tmp_path / 'demo'
    skill.mkdir()
    (skill / 'SKILL.md').write_text('---\nname: demo\ndescription: Demo skill\n---\nAlways preserve targets.')
    (skill / 'rules.md').write_text('Reference rule\n' * 200)
    manager = SkillManager(dir=str(tmp_path))
    result = manager.read_reference('demo', 'rules.md')
    text, kind = compact_skill_result('read_reference', result)
    assert kind == 'skill_locator'
    assert 'demo' in text and 'rules.md' in text
    reload_args = json.loads(text.split('Reload with read_reference(', 1)[1].split(') before', 1)[0])
    assert manager.read_reference(**reload_args)['content'] == result['content']


def test_legacy_active_skill_locators_remain_readable_for_tool_activation():
    from lazymind.chat.engine.agent_runtime.active_context import active_skills_from_model_context

    skills = active_skills_from_model_context({'active_skills': [
        {'name': 'demo', 'path': '/skills/demo/SKILL.md', 'hash': 'abc'},
    ]})
    assert skills[0]['name'] == 'demo'
    assert lazyllm.globals['agentic_config'] == {}


def test_legacy_compactor_signature_still_receives_skill_results():
    def compact(history, keep):
        return history

    llm = RecordingLLM([call('get_skill', {'name': 'demo'}, 'skill'), {'content': 'done'}])
    fc = FunctionCall(llm, _tool_manager=ToolManager([get_skill]), history_compactor=compact)
    assert fc(fc('start')) == 'done'
    assert 'Always preserve the original word target.' in json.dumps(llm.requests[-1])


def test_current_skill_resource_tool_keeps_reload_identity(tmp_path):
    from lazyllm.tools.agent import SkillManager
    from lazymind.chat.engine.agent_runtime.compactors import compact_tool_result

    skill = tmp_path / 'demo'
    skill.mkdir()
    (skill / 'references').mkdir()
    (skill / 'SKILL.md').write_text(
        '---\nname: demo\ndescription: Demo skill\n---\nRead references/rules.md before writing.',
    )
    (skill / 'references' / 'rules.md').write_text('Must preserve constraints\n' * 200)
    manager = SkillManager(dir=str(tmp_path))
    manager.get_skill('demo')
    read_resource = next(tool for tool in manager.get_skill_tools() if tool.__name__ == 'read_skill_resource')
    result = read_resource('demo', 'references/rules.md')
    text, kind, _, _ = compact_tool_result('read_skill_resource', result)
    assert kind == 'skill_locator'
    reload_args = json.loads(text.split('Reload with read_skill_resource(', 1)[1].split(') before', 1)[0])
    assert reload_args == {'name': 'demo', 'rel_path': 'references/rules.md'}
    assert read_resource(**reload_args)['content'] == result['content']
