from lazymind.chat.engine.prompts import resolve_task_profile
from lazymind.chat.engine.agent_runtime.models import AgentRole
from lazymind.chat.engine.agent_runtime.prompt_builder import PromptBuilder
from lazymind.chat.engine.tools.skill_listing import append_loaded_skill_invocations
import json
import pytest


def test_task_profile_does_not_reinterpret_host_skill_selection():
    profile = resolve_task_profile(
        '不要使用 alpha，paper', enable_llm_fallback=False,
        explicit_resources={
            'skill_names': ['external/paper'],
            'mentions': [{'resource_type': 'skill', 'resource_ref': 'external/paper', 'display_name': 'paper'}],
        },
    )
    assert profile.explicit_resources.skill_names == ('external/paper',)
    assert profile.excluded_resources.skill_names == ()


def test_explicit_l2_is_appended_without_changing_system_prefix():
    content = '---\nname: paper\ndescription: Write papers\ntags: [academic]\n---\nFollow these steps.\n'

    def build():
        builder = PromptBuilder.for_role(AgentRole.CHAT)
        builder.system('base', 'Assistant', 'System instructions', 'platform')
        return builder.input(content='Write the paper', source='user').build()

    original = build()
    selected = build()
    history = append_loaded_skill_invocations(
        [], [{'skill_key': 'external/paper', 'revision_id': 'rev1', 'content': content}],
    )
    assert selected.system_prompt == original.system_prompt
    assert json.loads(history[1]['content'])['content'].strip() == content.strip()


@pytest.mark.parametrize('source', ['openai', 'openai_responses'])
@pytest.mark.parametrize('stream', [False, True])
def test_loaded_skill_history_reaches_native_transport(monkeypatch, source, stream):
    """Exercise the real model serializer with the product's Skill tool history."""
    from io import BytesIO
    import lazyllm
    import requests

    content = '---\nname: protocol-probe\n---\n保留 Unicode 和工具关联。'
    history = append_loaded_skill_invocations(
        [], [{'skill_key': 'external/protocol-probe', 'revision_id': 'rev1', 'content': content}],
    )
    call_id = history[0]['tool_calls'][0]['id']
    sent = []

    def send(session, request, **kwargs):
        sent.append(json.loads(request.body))
        if source == 'openai_responses':
            payload = {'status': 'completed', 'output': [
                {'type': 'message', 'role': 'assistant', 'content': [
                    {'type': 'output_text', 'text': 'Skill received'}]},
            ]}
            if stream:
                payload = {'type': 'response.completed', 'response': payload}
        else:
            payload = {'choices': [{'index': 0, 'delta' if stream else 'message': {
                'role': 'assistant', 'content': 'Skill received'}, 'finish_reason': 'stop'}]}
        response = requests.Response()
        response.status_code = 200
        body = json.dumps(payload)
        response._content = (('data: ' + body + '\n\ndata: [DONE]\n\n') if stream else body).encode()
        response._content_consumed = True
        response.raw = BytesIO()
        return response

    monkeypatch.setattr(requests.Session, 'send', send)
    with lazyllm.new_session():
        model = lazyllm.OnlineChatModule(source=source, model='gpt-test', api_key='test', stream=stream)
        assert model('Apply the selected Skill', llm_chat_history=history) == 'Skill received'
    assert len(sent) == 1
    if source == 'openai_responses':
        items = sent[0]['input']
        call = next(item for item in items if item.get('type') == 'function_call')
        result = next(item for item in items if item.get('type') == 'function_call_output')
        assert call['call_id'] == result['call_id'] == call_id
        assert call['name'] == 'get_skill'
        assert json.loads(result['output'])['content'] == content
    else:
        items = sent[0]['messages']
        call = next(item for item in items if item.get('tool_calls'))['tool_calls'][0]
        result = next(item for item in items if item.get('role') == 'tool')
        assert call['id'] == result['tool_call_id'] == call_id
        assert call['function']['name'] == 'get_skill'
        assert json.loads(result['content'])['content'] == content
