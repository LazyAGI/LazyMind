"""Native HTTP -> real AutoModel/agent -> publisher -> Workflow acceptance."""
import asyncio
import json
from io import BytesIO
from unittest.mock import MagicMock

import lazyllm
import pytest
import requests

from lazyllm.module.servermodule import LLMBase
from lazymind.chat.engine.subagent import runner, tools as artifact_tools
from lazymind.chat.engine.subagent.context import require_context
from lazymind.chat.engine.tools.workspace_context import workflow_execution_scope


@pytest.mark.parametrize('source', ['openai', 'openai_responses'])
@pytest.mark.parametrize('stream', [False, True])
@pytest.mark.parametrize('missing_required', [False, True])
def test_ppt_publisher_finishes_without_content_or_review(monkeypatch, tmp_path, source, stream, missing_required):
    """Keep the runner streaming events while exercising both model wire modes."""
    requests_sent, published, stores = [], [], []
    real_share = LLMBase.share

    def share(model, *args, **kwargs):
        # AgentExecutor requests streamed agent events unconditionally. Configure
        # only the real model's wire mode; FunctionCall tool events stay enabled.
        kwargs['stream'] = stream
        return real_share(model, *args, **kwargs)

    monkeypatch.setattr(LLMBase, 'share', share)
    config_file = tmp_path / 'models.yaml'
    config_file.write_text('llm:\n  source: dynamic\n  dynamic_auth: true\n  type: llm\n')
    monkeypatch.setitem(lazyllm.config._impl, 'auto_model_config_map_path', str(config_file))

    def ppt_publish_pages():
        """Publish the completed presentation pages into the workflow output slot."""
        result = artifact_tools._save_artifact(
            'preview_html', '<section><h1>Completed presentation</h1></section>', 'text',
            source_tool='ppt_publish_pages', internal_publish=True,
        )
        published.extend(require_context().local_artifacts())
        return result

    monkeypatch.setattr(runner, 'load_workflow_tools', lambda params, names: {
        'ppt_publish_pages': ppt_publish_pages,
    })
    monkeypatch.setattr(runner, 'make_cancel_stop_condition', lambda: lambda _: False)
    review = MagicMock(side_effect=AssertionError('Published terminal output must not trigger review'))
    monkeypatch.setattr(runner, '_evaluate_completion', review)
    real_store = runner.MemorySubAgentStore

    def store(*args, **kwargs):
        instance = real_store(*args, **kwargs)
        stores.append(instance)
        return instance

    monkeypatch.setattr(runner, 'MemorySubAgentStore', store)

    def send(session, request, **kwargs):
        assert request.method == 'POST'
        body = json.loads(request.body)
        requests_sent.append((request.url, body))
        assert len(requests_sent) == 1, 'Stop tool must avoid another model/reviewer round'
        assert body['stream'] is stream
        if source == 'openai_responses':
            assert request.url == 'https://provider.example/v1/responses'
            output = [
                {'type': 'reasoning', 'id': 'rs-ppt', 'summary': [], 'encrypted_content': 'opaque'},
                {'type': 'function_call', 'id': 'fc-ppt', 'call_id': 'call-ppt',
                 'name': 'ppt_publish_pages', 'arguments': '{}', 'status': 'completed'},
            ]
            payload = {'id': 'resp-ppt', 'status': 'completed', 'output': output}
            if stream:
                payload = {'type': 'response.completed', 'response': payload}
        else:
            assert request.url == 'https://provider.example/v1/chat/completions'
            # Real completion providers may omit content entirely on tool turns.
            message = {'role': 'assistant', 'tool_calls': [{
                'index': 0, 'id': 'call-ppt', 'type': 'function',
                'function': {'name': 'ppt_publish_pages', 'arguments': '{}'},
            }]}
            payload = {'choices': [{'index': 0, 'delta' if stream else 'message': message,
                                    'finish_reason': 'tool_calls'}]}
        response = requests.Response()
        response.status_code = 200
        response.url = request.url
        response.request = request
        content = json.dumps(payload)
        response._content = (f'data: {content}\n\ndata: [DONE]\n\n' if stream else content).encode()
        response._content_consumed = True
        response.raw = BytesIO()
        return response

    monkeypatch.setattr(requests.Session, 'send', send)
    task = {
        'id': f'ppt-native-{source}-{stream}', 'conversation_id': 'ppt-protocol-test',
        'agent_type': 'workflow_step', 'objective': 'Publish the completed presentation.',
        'workspace_path': str(tmp_path / 'workspace'), 'input_slots': [], 'output_slots': ['preview_html'],
        'params': {'tools_only': True, 'terminal_tools': ['ppt_publish_pages'],
                   'required_output_artifact_keys': ['preview_html'],
                   'output_slot_types': {'preview_html': 'text'},
                   'workflow_runtime': {'publisher_owned_slots': ['preview_html']}},
    }
    if missing_required:
        # A successful stop tool is insufficient when a second required output
        # was not published; exercise the actual runner's acceptance boundary.
        task['output_slots'].append('preview_notes')
        task['params']['required_output_artifact_keys'].append('preview_notes')
        task['params']['output_slot_types']['preview_notes'] = 'text'
        task['params']['workflow_runtime']['publisher_owned_slots'].append('preview_notes')

    async def collect():
        return ''.join([part async for part in runner.run_subagent_stream(
            task['id'], task_spec=task, tools=['ppt_publish_pages'], resume=True,
            initial_steps=[{'seq': 0, 'role': 'plan', 'content': {
                'scope_version': 2, 'steps': ['Render', 'Publish', 'Finish']}}],
            model_config={'llm': {'source': source, 'model': 'gpt-test', 'api_key': 'test-key',
                                  'base_url': 'https://provider.example/v1'}},
        )])

    # Match the trusted immutable-workflow boundary owned by RemoteWorkflowExecutor.
    with lazyllm.new_session(), workflow_execution_scope():
        raw = asyncio.run(collect())
    events = [json.loads(line[6:]) for line in raw.splitlines()
              if line.startswith('data: ') and line != 'data: [DONE]']
    terminal = [event for event in events if event['type'] in {'done', 'error'}]
    assert len(terminal) == 1, terminal
    assert terminal[0]['status'] == ('failed' if missing_required else 'succeeded'), terminal
    if missing_required:
        assert terminal[0]['current_phase'] == 'missing_required_artifacts'
    assert raw.endswith('data: [DONE]\n\n')
    assert len(requests_sent) == 1
    assert len(published) == 1 and published[0]['slot'] == 'preview_html'
    assert 'Completed presentation' in json.dumps(published[0]['value'])
    artifacts = [event for event in events if event['type'] == 'artifact']
    assert len(artifacts) == 1 and artifacts[0]['slot'] == 'preview_html'
    assert any(event['type'] == 'tool_results' for event in events)
    steps = stores[0].load_steps(task['id'])
    assistant = next(step['content']['assistant_message'] for step in steps
                     if step['role'] == 'assistant' and step['content'].get('assistant_message'))
    assert assistant['tool_calls'][0]['function']['name'] == 'ppt_publish_pages'
    if source == 'openai_responses':
        assert assistant['_lazyllm_provider_items']['output'][0]['encrypted_content'] == 'opaque'
    review.assert_not_called()
