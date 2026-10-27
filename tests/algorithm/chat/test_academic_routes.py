import asyncio
import hashlib
from pathlib import Path
from types import SimpleNamespace

import pytest
from fastapi import HTTPException
from lazymind.chat.api import academic_routes as ar


def test_router_serves_authenticated_academic_bridge(monkeypatch):
    import importlib
    from fastapi.testclient import TestClient
    from lazymind.config import config
    from lazymind.chat.workflow import remote_executor

    monkeypatch.setitem(config._impl, 'enable_router', True)
    monkeypatch.setitem(config._impl, 'background_jobs_enabled', False)
    monkeypatch.setitem(config._impl, 'core_internal_token', 'academic-test-token')
    monkeypatch.setattr(remote_executor, 'start_remote_workflow_executor', lambda: None)
    app = importlib.import_module('lazymind.chat.app').create_app()
    client = TestClient(app)
    payload = {'file_ref': 'missing.pdf', 'content_hash': '0' * 64}
    assert client.post('/internal/academic/pdf:parse', json=payload).status_code == 401
    response = client.post('/internal/academic/pdf:parse', json=payload,
                           headers={'X-LazyMind-Internal-Token': 'academic-test-token'})
    assert response.status_code == 422


def test_pdf_bridge_validates_version_and_preserves_pages(tmp_path, monkeypatch):
    from lazymind.chat.engine.tools.file_resources import ingest
    monkeypatch.setattr(ar, 'require_internal_token', lambda token: token)
    monkeypatch.setenv('LAZYMIND_SHARED_UPLOAD_DIR', str(tmp_path))
    path = tmp_path / 'academic' / 'paper.pdf'
    path.parent.mkdir()
    path.write_bytes(b'%PDF-fixture')
    monkeypatch.setattr(ingest, 'parse_pdf_pages', lambda path: [(2, 'body B'), (1, 'body A')])
    payload = ar.PDFRequest(file_ref='paper.pdf', content_hash=hashlib.sha256(path.read_bytes()).hexdigest())
    result = asyncio.run(ar.parse_pdf(payload, 'token'))
    assert result == {'pages': [{'page': 1, 'text': 'body A'}, {'page': 2, 'text': 'body B'}]}
    path.write_bytes(b'%PDF-changed')
    with pytest.raises(HTTPException) as exc:
        asyncio.run(ar.parse_pdf(payload, 'token'))
    assert exc.value.status_code == 422


def test_pdf_bridge_rejects_unregistered_path(tmp_path, monkeypatch):
    monkeypatch.setattr(ar, 'require_internal_token', lambda token: token)
    monkeypatch.setenv('LAZYMIND_SHARED_UPLOAD_DIR', str(tmp_path))
    outside = tmp_path / 'outside.pdf'
    outside.write_bytes(b'%PDF')
    with pytest.raises(HTTPException) as exc:
        asyncio.run(ar.parse_pdf(ar.PDFRequest(file_ref='../outside.pdf', content_hash='0' * 64), 'token'))
    assert exc.value.status_code == 422


def test_expansion_result_contract_rejects_nested_relation_metadata(tmp_path, monkeypatch):
    import copy
    import importlib.util
    import json
    from pathlib import Path
    import pytest

    path = Path(__file__).resolve().parents[3] / 'workflows/paper_expansion/scripts/tools.py'
    spec = importlib.util.spec_from_file_location('paper_expansion_contract', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    result = {
        'user_instruction': 'original requirements', 'candidate_directions': ['tools'],
        'selected_directions': ['tools'],
        'papers': [{'paper': {'paper_id': 'real-id', 'abstract': None},
                    'is_seed': True, 'is_important': False, 'direction_relation': 'related to tools'}],
    }
    assert module.academic_validate_result(result)['status'] == 'valid'
    invalid = copy.deepcopy(result)
    invalid['papers'][0]['direction_relation'] = [{'direction': 'tools'}]
    with pytest.raises(ValueError, match='plain description string'):
        module.academic_validate_result(invalid)
    monkeypatch.setenv('LAZYMIND_SUBAGENT_WORKSPACE', str(tmp_path))
    result['papers'][0]['direction_relation'] = 'full description ' * 4000
    carrier = tmp_path / 'large.txt'
    carrier.write_text(json.dumps(result), encoding='utf-8')
    assert module.academic_validate_result({'type': 'json', 'path': str(carrier)})['status'] == 'valid'


def test_expansion_tools_forward_owned_task_and_pinned_candidate_context(monkeypatch):
    import importlib.util
    path = Path(__file__).resolve().parents[3] / 'workflows/paper_expansion/scripts/tools.py'
    spec = importlib.util.spec_from_file_location('paper_expansion_transport', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    requests = []

    def request(method, url, **kwargs):
        requests.append((method, url, kwargs))
        return SimpleNamespace(raise_for_status=lambda: None, json=lambda: {'data': {'papers': []}})

    monkeypatch.setattr(module, '_client', lambda: SimpleNamespace(base_url='http://core',
                        transport=SimpleNamespace(request=request), _headers=lambda: {'X-User-Id': 'owner'}))
    assert module.academic_search_papers('owned-expansion', 'all:agents') == {'papers': []}
    assert requests[-1][1] == 'http://core/academic/papers:search'
    assert requests[-1][2]['json'] == {
        'expansion_id': 'owned-expansion', 'query': 'all:agents', 'max_results': 10,
        'sources': [], 'mode': 'keyword', 'paper_id': '',
    }
    module.academic_read_paper('owned-expansion', 'discovered-paper')
    assert requests[-1][2]['json'] == {
        'expansion_id': 'owned-expansion', 'paper_id': 'discovered-paper',
        'keywords': [], 'page_start': 0, 'page_end': 0,
    }
    module.academic_read_paper('owned-expansion', cursor='bound-cursor')
    assert requests[-1][2]['json'] == {'expansion_id': 'owned-expansion', 'cursor': 'bound-cursor'}
    assert requests[-1][2]['headers'] == {'X-User-Id': 'owner'}


@pytest.mark.parametrize('package,slot,kind', [
    ('paper_workflow_mock', 'handoff_receipt', 'review'),
    ('paper_workflow_mock', 'handoff_receipt', 'reflection'),
    ('paper_workflow_mock', 'handoff_receipt', 'ppt'),
    ('paper_ppt_with_reflection_mock', 'ppt_request', 'ppt'),
])
def test_mock_publisher_preserves_authoritative_handoff(monkeypatch, package, slot, kind):
    from lazymind.chat.engine.subagent import tools
    from lazymind.chat.service import academic_handoff
    from lazymind.workflow_toolkit import load_workflow_package_tools

    root = Path(__file__).resolve().parents[3] / 'workflows' / package
    functions = load_workflow_package_tools(
        {'files': {'workflow.yaml': (root / 'workflow.yaml').read_bytes(),
                   'scripts/tools.py': (root / 'scripts/tools.py').read_bytes()}},
        ['academic_handoff_context', 'academic_handoff_submit', 'academic_read_paper'], package, 'test-revision')
    paper = {'paper_id': 'original-paper', 'title': 'Frozen <paper> [method]', 'abstract': 'Full original abstract',
             'year': 2021, 'source_provider': 'arxiv', 'source_version': 'v2'}
    papers = [{'paper': paper, 'is_seed': True, 'is_important': False}] if kind == 'review' else [paper]
    frozen = {'status': 'awaiting_downstream', 'kind': kind,
              'input': {'user_instruction': 'Study the selected method', 'papers': papers},
              'feedback': 'Mock, no real PPT file'}
    if slot == 'ppt_request':
        frozen['reflection_markdown'] = 'saved reflection body'
    elif kind == 'reflection':
        frozen['input']['notes'] = [{'paper_id': paper['paper_id'], 'content': 'Original note'}]
    requests = []

    def request(method, url, **kwargs):
        requests.append((method, url, kwargs))
        data = ({'handoff_id': 'owned-handoff', 'input': frozen['input']}
                if method == 'GET' else frozen)
        if method == 'GET' and kwargs['params'].get('view') == 'summary':
            data = {'handoff_id': 'owned-handoff'}
        return SimpleNamespace(raise_for_status=lambda: None, json=lambda: data)
    monkeypatch.setattr(academic_handoff, '_client', lambda: SimpleNamespace(base_url='http://core',
                        transport=SimpleNamespace(request=request), _headers=lambda: {'X-User-Id': 'owner'}))
    monkeypatch.setattr(academic_handoff, 'require_context',
                        lambda: SimpleNamespace(conversation_id='owned-chat', output_slots=[slot]))
    published = []
    monkeypatch.setattr(tools, '_save_artifact', lambda key, value, **kwargs:
                        published.append((key, value, kwargs)) or {'status': 'ok'})
    assert functions['academic_handoff_context']()['input'] == frozen['input']
    assert requests[-1][2]['params'] == {'conversation_id': 'owned-chat'}
    requests.clear()
    receipt = functions['academic_handoff_submit']()
    assert receipt['artifact']['status'] == 'ok' and receipt['paper_count'] == 1
    assert len(published) == 1 and published[0][0] == slot
    value = published[0][1]
    if slot == 'handoff_receipt':
        assert value.count(frozen['feedback']) == 1
        assert 'Frozen &lt;paper&gt; \\[method\\]' in value and paper['source_version'] in value
        assert frozen['input']['user_instruction'] in value
        assert paper['paper_id'] not in value and paper['abstract'] not in value
        assert '```json' not in value
        if kind == 'reflection':
            assert '1 条已有笔记' in value
    else:
        assert value == frozen

    assert requests[0][2]['params'] == {'conversation_id': 'owned-chat', 'view': 'summary'}
    assert requests[1][1] == 'http://core/academic/mock-handoffs/owned-handoff:submit'
    assert requests[1][2]['json'] == {} and requests[1][2]['timeout'] == 30
    functions['academic_read_paper']('original-paper')
    assert requests[-1][2]['json'] == {'handoff_id': 'owned-handoff', 'paper_id': 'original-paper'}
    functions['academic_read_paper'](cursor='saved-cursor')
    assert requests[-1][2]['json'] == {'handoff_id': 'owned-handoff', 'cursor': 'saved-cursor'}
    assert requests[-2][2]['params'] == {'conversation_id': 'owned-chat', 'view': 'summary'}
    assert requests[-1][2]['timeout'] == 90
    assert all(request[2]['headers'] == {'X-User-Id': 'owner'} for request in requests)
    with pytest.raises(ValueError, match='but not both'):
        functions['academic_read_paper']('original-paper', cursor='saved-cursor')


@pytest.mark.parametrize('status', [401, 402, 403, 429])
def test_search_bridge_preserves_actionable_source_status(monkeypatch, status):
    import httpx
    from lazymind.chat.service import academic_preparation
    monkeypatch.setattr(ar, 'require_internal_token', lambda _: None)

    def fail(_):
        response = httpx.Response(status, request=httpx.Request('GET', 'https://source.invalid'))
        raise httpx.HTTPStatusError('secret must not appear', request=response.request, response=response)

    monkeypatch.setattr(academic_preparation, 'search_registry', fail)
    with pytest.raises(HTTPException) as exc:
        asyncio.run(ar.academic_search(ar.AcademicSearchRequest(source='web', query='paper', limit=2), 'token'))
    assert exc.value.status_code == status
    assert 'secret' not in exc.value.detail


def test_preparation_typed_tools_accept_grounded_direction_schema():
    from lazyllm.tools.agent import ToolManager
    from lazymind.chat.lazyllm_tool_docs import ensure_lazyllm_tool_docs
    from lazymind.chat.service.academic_preparation import PreparationDirection, PreparationQuestion

    def finish(directions: list[PreparationDirection], questions: list[PreparationQuestion] | None = None):
        """Save grounded directions and questions."""
        return directions, questions

    ensure_lazyllm_tool_docs([finish])
    tool = ToolManager([finish]).all_tools[0]
    value = tool._validate_input({
        'directions': [{'name': 'retrieval', 'question': 'why', 'reason': 'evidence',
                        'difference': 'scope', 'paper_ids': ['real-paper']}],
        'questions': [{'id': 'scope', 'question': 'Which setting?'}],
    })
    assert value['directions'][0]['paper_ids'] == ['real-paper']


def test_unknown_pdf_locations_preserve_distinct_blocks(tmp_path, monkeypatch):
    from lazymind.chat.engine.tools.file_resources import ingest
    monkeypatch.setattr(ar, 'require_internal_token', lambda _: None)
    monkeypatch.setenv('LAZYMIND_SHARED_UPLOAD_DIR', str(tmp_path))
    path = tmp_path / 'academic' / 'paper.pdf'
    path.parent.mkdir()
    path.write_bytes(b'%PDF-test')
    monkeypatch.setattr(ingest, 'parse_pdf_pages', lambda _: [(0, 'first block'), (0, 'second block')])
    result = asyncio.run(ar.parse_pdf(ar.PDFRequest(file_ref='paper.pdf',
                         content_hash=hashlib.sha256(path.read_bytes()).hexdigest()), 'token'))
    assert result['pages'] == [{'page': 0, 'text': 'first block'}, {'page': 0, 'text': 'second block'}]


def test_preparation_tools_execute_with_normal_workspace_authorization(monkeypatch):
    import json
    from lazymind.chat.service import academic_preparation as preparation
    from lazymind.chat.engine.agent_runtime.workspace_policy import WorkspaceAuthorizationPolicy
    from lazymind.chat.engine.tools.workspace_context import WorkspaceContext
    from lazyllm.tools.agent import ToolManager

    class Client:
        base_url = 'http://fixture'
        transport = SimpleNamespace(request=lambda *args, **kwargs: SimpleNamespace(
            raise_for_status=lambda: None,
            json=lambda: {'data': {'papers': [{'paper_id': 'real-paper', 'title': 'Actual candidate'}]}},
        ))

        def __init__(self, *args, **kwargs):
            pass

        def _headers(self):
            return {}

    def execute(self, llm, plan):
        manager = ToolManager(plan.tools)
        policy = WorkspaceAuthorizationPolicy(WorkspaceContext(), set(), lambda _: False)
        batch = manager.prepare_tool_calls([
            {'id': 'search', 'type': 'function', 'function': {'name': 'search_papers',
             'arguments': json.dumps({'query': 'RAG'})}},
        ], authorization_policy=policy)
        results = manager.execute_prepared(batch)
        assert 'real-paper' in str(results)
        plan.tools[-1]('Grounded directions', [{'name': 'retrieval', 'question': 'why', 'reason': 'evidence',
                                               'difference': 'scope', 'paper_ids': ['real-paper']}])

    monkeypatch.setattr(preparation, 'WorkflowClient', Client)
    monkeypatch.setattr(preparation, 'inject_model_config', lambda _: None)
    monkeypatch.setattr(preparation, 'AutoModel', lambda **_: object())
    monkeypatch.setattr(preparation.AgentExecutor, 'run', execute)
    result = preparation.run_preparation({
        'llm_config': {}, 'timeout_seconds': 10, 'user_id': 'u',
        'expansion_id': 'exp', 'revision': 1, 'preparation': {'user_instruction': 'RAG'}, 'input': {},
    })
    assert result['directions'][0]['paper_ids'] == ['real-paper']
