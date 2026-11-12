import base64
import importlib.util
import json
import sys
from pathlib import Path
from types import ModuleType, SimpleNamespace

import pytest
import yaml


def _load_workflow_routes(monkeypatch):
    # Load this router independently of api/__init__, which starts unrelated
    # knowledge/RAG routers. The client transport is mocked by these unit tests.
    __import__('lazyllm')
    try:
        __import__('httpx')
    except ImportError:
        monkeypatch.setitem(sys.modules, 'httpx', ModuleType('httpx'))
    path = Path(__file__).resolve().parents[3] / 'algorithm/lazymind/chat/api/workflow_routes.py'
    spec = importlib.util.spec_from_file_location('workflow_action_routes_test', path)
    module = importlib.util.module_from_spec(spec)
    monkeypatch.setitem(sys.modules, spec.name, module)
    spec.loader.exec_module(module)
    return module


def _smoke_package():
    from lazymind.chat.workflow.external_tools import package_tree_hash

    root = Path(__file__).resolve().parents[3] / 'workflows/test-workflow'
    files = {str(path.relative_to(root)): path.read_bytes() for path in root.rglob('*')
             if path.is_file() and '__pycache__' not in path.parts}
    tree_hash = package_tree_hash(files)
    return root, {'tree_hash': tree_hash, 'files': {
        name: base64.b64encode(raw).decode() for name, raw in files.items()
    }}


def _external_request(package, step):
    return {'workflow_id': 'test-workflow', 'revision_id': 'pinned-smoke',
            'tree_hash': package['tree_hash'], 'package': package, 'step_id': step}


@pytest.fixture(params=['native', 'external'])
def smoke_call(request):
    from lazymind.chat.workflow.external_tools import (
        ExternalToolCallRequest, ExternalToolRequest, call_external_tool, list_external_tools,
    )
    from lazymind.workflow_toolkit import load_workflow_package_tools

    root, package = _smoke_package()
    names = ['smoke_generate', 'smoke_update', 'smoke_verify']
    native = load_workflow_package_tools(package, names, 'test-workflow', 'pinned-smoke', package_root=root)
    assert set(native) == set(names)

    def call(step, **arguments):
        if request.param == 'native':
            return native['smoke_' + step](**arguments)
        payload = _external_request(package, step)
        catalog = list_external_tools(ExternalToolRequest(**payload))['tools']
        assert [tool['name'] for tool in catalog] == ['smoke_' + step]
        assert catalog[0]['available'] is True
        return call_external_tool(ExternalToolCallRequest(
            **payload, tool_name='smoke_' + step, arguments=arguments,
        ))['result']
    return call


def _save_tool_files(result, directory):
    import hashlib

    directory.mkdir()
    paths = []
    for item in result['files']:
        raw = (Path(item['local_path']).read_bytes() if 'local_path' in item
               else base64.b64decode(item['content_base64'], validate=True))
        assert item['size_bytes'] == len(raw)
        assert item['sha256'] == hashlib.sha256(raw).hexdigest()
        path = directory / item['name']
        path.write_bytes(raw)
        paths.append(path)
    return paths


def test_three_step_smoke_tools_read_saved_contents_on_both_hosts(smoke_call, tmp_path):
    summary = '测试 hello'
    generated = smoke_call('generate', summary=summary)
    source, = _save_tool_files(generated, tmp_path / 'generate')
    source_text = source.read_text(encoding='utf-8')
    assert source_text == 'Workflow smoke test\n测试 hello\nrevision-1\n'
    metadata_path = tmp_path / 'metadata.json'
    metadata_path.write_text(json.dumps(generated['metadata']), encoding='utf-8')

    updated = smoke_call('update', summary=summary, source_text=source_text)
    first, second, item1, item2 = _save_tool_files(updated, tmp_path / 'update')
    assert first.read_bytes() == source.read_bytes()
    assert second.read_bytes() == first.read_bytes() + b'revision-2\n'
    assert [path.read_text() for path in (item1, item2)] == ['list-item-1\n', 'list-item-2\n']
    for revision in (2, 4):  # A repeated attempt keeps monotonic Core revision numbers.
        report = smoke_call(
            'verify', summary=summary, metadata=json.loads(metadata_path.read_text()),
            source_text=source.read_text(), rewritten_text=second.read_text(),
            rewritten_revision=revision, list_texts=[path.read_text() for path in (item1, item2)],
        )
        assert report['status'] == 'Workflow smoke test passed'
        assert report['rewritten_revision'] == revision
        assert all(report['checks'].values())


@pytest.mark.parametrize(('field', 'value', 'reason'), [
    ('metadata', {'smoke_test': True, 'schema': 'test.v2', 'summary': 'other'}, 'metadata'),
    ('metadata', {}, 'metadata'),
    ('source_text', '/tmp/source.txt', 'source'),
    ('rewritten_text', 'Workflow smoke test\nhello\nrevision-1\n', 'updated_content'),
    ('rewritten_revision', 1, 'updated_revision'),
    ('list_texts', ['list-item-2\n', 'list-item-1\n'], 'ordered_list'),
    ('list_texts', ['list-item-1\n', 'list-item-1\n'], 'ordered_list'),
    ('list_texts', ['list-item-1\n', 'list-item-2\n', 'extra'], 'ordered_list'),
    ('list_texts', ['list-item-1\n'], 'ordered_list'),
])
def test_smoke_verification_rejects_bad_saved_values(smoke_call, field, value, reason):
    arguments = {
        'summary': 'hello', 'metadata': {'smoke_test': True, 'schema': 'test.v2', 'summary': 'hello'},
        'source_text': 'Workflow smoke test\nhello\nrevision-1\n',
        'rewritten_text': 'Workflow smoke test\nhello\nrevision-1\nrevision-2\n',
        'rewritten_revision': 2, 'list_texts': ['list-item-1\n', 'list-item-2\n'],
    }
    arguments[field] = value
    with pytest.raises(ValueError, match=reason):
        smoke_call('verify', **arguments)


def test_smoke_update_rejects_a_path_or_truncated_content(smoke_call):
    for source_text in ('/tmp/source.txt', 'Workflow smoke test\nhello\nrevision-1'):
        with pytest.raises(ValueError, match='source_text must be the complete saved'):
            smoke_call('update', summary='hello', source_text=source_text)


def test_native_smoke_tools_have_usable_model_schemas():
    from lazyllm.tools.agent import ToolManager
    from lazymind.workflow_toolkit import load_workflow_package_tools

    root, package = _smoke_package()
    names = ['smoke_generate', 'smoke_update', 'smoke_verify']
    loaded = load_workflow_package_tools(package, names, 'test-workflow', 'pinned-smoke', package_root=root)
    schemas = {tool['function']['name']: tool['function']['parameters']
               for tool in ToolManager(list(loaded.values())).tools_description}
    assert schemas['smoke_generate']['required'] == ['summary']
    assert set(schemas['smoke_verify']['required']) == {
        'summary', 'metadata', 'source_text', 'rewritten_text', 'rewritten_revision', 'list_texts',
    }
    assert schemas['smoke_verify']['properties']['rewritten_revision']['type'] == 'integer'
    assert schemas['smoke_verify']['properties']['list_texts']['type'] == 'array'


def test_smoke_error_route_preserves_field_level_remediation(monkeypatch):
    from fastapi import FastAPI
    from fastapi.testclient import TestClient
    from lazymind.chat.api import agent_control_routes, external_workflow_tool_routes

    monkeypatch.setattr(agent_control_routes, 'config', {'core_internal_token': 'smoke-test-only'})
    app = FastAPI()
    app.include_router(external_workflow_tool_routes.router)
    _, package = _smoke_package()
    payload = {**_external_request(package, 'update'), 'tool_name': 'smoke_update',
               'arguments': {'summary': 'hello', 'source_text': '/tmp/source.txt'}}
    response = TestClient(app).post('/api/workflow/external-tools/call', json=payload,
                                    headers={'X-LazyMind-Internal-Token': 'smoke-test-only'})
    assert response.status_code == 400
    assert response.json()['detail']['code'] == 'INVALID_ARGUMENT'
    assert response.json()['detail']['public_message'] == (
        'source_text must be the complete saved source.txt content, including newlines.'
    )


def test_workflow_action_route_uses_pinned_definition_and_server_owned_arguments(monkeypatch):
    from fastapi import HTTPException
    workflow_routes = _load_workflow_routes(monkeypatch)

    definition = yaml.safe_dump({'artifact_actions': {'rewrite_selection': {
        'slots': ['draft_document'], 'preview_tool': 'preview_rewrite',
    }}}).encode()
    package = {
        'revision_id': 'revision-1', 'tree_hash': 'tree-1',
        'files': {'workflow.yaml': base64.b64encode(definition).decode()},
    }
    fetches = []

    class FakeWorkflowClient:
        def __init__(self, *_args, **_kwargs):
            pass

        def get_workflow(self, workflow_id, revision_id):
            fetches.append((workflow_id, revision_id))
            return SimpleNamespace(result=package)

    def preview_rewrite(*, artifact, artifact_store, slot, instruction):
        return artifact, artifact_store, slot, instruction

    monkeypatch.setattr(workflow_routes, 'WorkflowClient', FakeWorkflowClient)
    monkeypatch.setattr(
        workflow_routes, 'load_workflow_package_tools',
        lambda loaded, names, *_identity: (
            {'preview_rewrite': preview_rewrite} if loaded is package and names else {}
        ),
    )
    monkeypatch.setattr(workflow_routes, 'inject_model_config', lambda _config: None)
    monkeypatch.setattr(workflow_routes, 'inject_tool_config', lambda _config: None)

    payload = {
        'workflow_id': 'writer-workflow',
        'revision_id': 'revision-1',
        'tree_hash': 'tree-1',
        'action': 'rewrite_selection',
        'phase': 'preview',
        'slot': 'draft_document',
        'artifact': {'path': '/tmp/draft.md'},
        'artifact_store': '/tmp/action',
        'arguments': {'instruction': '润色'},
    }

    request = workflow_routes.WorkflowActionInvokeRequest.model_validate(payload)
    assert workflow_routes.invoke_workflow_action(request)['result'] == (
        {'path': '/tmp/draft.md'}, '/tmp/action', 'draft_document', '润色',
    )
    assert fetches == [('writer-workflow', 'revision-1')]

    payload['arguments'] = {'instruction': '润色', 'slot': 'outline_document'}
    with pytest.raises(HTTPException, match='reserved arguments') as error:
        workflow_routes.invoke_workflow_action(
            workflow_routes.WorkflowActionInvokeRequest.model_validate(payload),
        )
    assert error.value.status_code == 422


@pytest.mark.parametrize(('workflow', 'slot'), [
    ('bid_tech_proposal_writer', 'draft_document'),
    ('product_solution_delivery', 'prd_document'),
    ('academic_research_pipeline', 'second_revised_document'),
])
def test_writer_workflows_preview_and_execute_paragraph_list(monkeypatch, tmp_path, workflow, slot):
    from lazymind.rewrite import selection

    routes = _load_workflow_routes(monkeypatch)
    manifest = Path(__file__).resolve().parents[3] / 'workflows' / workflow / 'workflow.yaml'
    package = {'revision_id': 'revision-1', 'tree_hash': 'tree-1', 'files': {
        'workflow.yaml': base64.b64encode(manifest.read_bytes()).decode(),
    }}
    monkeypatch.setattr(routes, 'WorkflowClient', lambda *_args, **_kwargs: SimpleNamespace(
        get_workflow=lambda *_args: SimpleNamespace(result=package),
    ))
    monkeypatch.setattr(routes, 'inject_model_config', lambda _config: None)
    monkeypatch.setattr(routes, 'inject_tool_config', lambda _config: None)
    monkeypatch.setattr(routes, 'load_workflow_package_tools',
                        lambda *_args: pytest.fail('rewrite must use the shared document action'))
    calls = []

    def generate(prompt):
        payload = json.loads(prompt.split('\n')[-1])
        calls.append(payload)
        return json.dumps({'results': [
            {'id': item['id'], 'content': item['content'].replace('原文', '润色')}
            for item in payload['paragraphs']
        ]})

    monkeypatch.setattr(selection, 'AutoModel', lambda **_kwargs: generate)
    source = '原文甲完整段落。\n\n中间段不变。\n\n原文乙完整段落。'
    artifact = tmp_path / 'article.md'
    artifact.write_text(source, encoding='utf-8')
    request = routes.WorkflowActionInvokeRequest(
        workflow_id=yaml.safe_load(manifest.read_text(encoding='utf-8'))['id'],
        revision_id='revision-1', tree_hash='tree-1', action='rewrite_selection', phase='preview',
        slot=slot, artifact={'path': str(artifact)}, artifact_store=str(tmp_path), arguments={
            'type': 'markdown', 'instruction': '润色',
            'selection_ranges': [{'selected_text': '原文乙'}, {'selected_text': '原文甲'}],
        },
    )
    result = routes.invoke_workflow_action(request)['result']
    assert [item['preview']['old_text'] for item in result['results']] == [
        '原文甲完整段落。', '原文乙完整段落。',
    ]
    assert result['artifact']['value'] == source.replace('原文', '润色')
    committed = routes.invoke_workflow_action(request.model_copy(update={
        'phase': 'execute', 'arguments': {'commit_token': result['commit']['token']},
    }))['result']
    assert committed['artifact'] == result['artifact']
    assert len(calls) == 1
    assert artifact.read_text(encoding='utf-8') == source


def test_builtin_document_action_route_does_not_load_a_workflow(monkeypatch):
    workflow_routes = _load_workflow_routes(monkeypatch)
    calls = []

    monkeypatch.setattr(
        workflow_routes,
        'WorkflowClient',
        lambda *_args, **_kwargs: pytest.fail('public document actions must not load a workflow'),
    )
    monkeypatch.setattr(workflow_routes, 'inject_model_config', lambda config: calls.append(('model', config)))
    monkeypatch.setattr(workflow_routes, 'inject_tool_config', lambda config: calls.append(('tool', config)))
    monkeypatch.setattr(
        workflow_routes,
        'invoke_document_action',
        lambda reference, phase, arguments, **context: {
            'reference': reference,
            'phase': phase,
            'artifact': context['artifact'],
        },
    )

    request = workflow_routes.DocumentActionInvokeRequest(
        reference='builtin:document.rewrite_selection.v1',
        phase='preview',
        artifact='# Draft',
        arguments={
            'instruction': '润色',
            'selection': {'type': 'markdown', 'selected_text': 'Draft'},
        },
    )
    result = workflow_routes.invoke_builtin_document_action(request)['result']

    assert result == {
        'reference': 'builtin:document.rewrite_selection.v1',
        'phase': 'preview',
        'artifact': '# Draft',
    }
    assert calls == [('model', {}), ('tool', {})]


@pytest.mark.parametrize('workflow_id', ['academic-writer', 'custom-writing-workflow'])
def test_portable_conversion_available_to_pinned_workflows_without_action_declaration(monkeypatch, workflow_id):
    workflow_routes = _load_workflow_routes(monkeypatch)

    package = {
        'revision_id': 'old-revision', 'tree_hash': 'tree',
        'files': {'workflow.yaml': base64.b64encode(b'id: arbitrary\n').decode()},
    }

    class FakeWorkflowClient:
        def __init__(self, *args, **kwargs):
            pass

        def get_workflow(self, *args):
            return SimpleNamespace(result=package)

    monkeypatch.setattr(workflow_routes, 'WorkflowClient', FakeWorkflowClient)
    monkeypatch.setattr(workflow_routes, 'inject_model_config', lambda _config: None)
    monkeypatch.setattr(workflow_routes, 'inject_tool_config', lambda _config: None)
    request = workflow_routes.WorkflowActionInvokeRequest(
        workflow_id=workflow_id, revision_id='old-revision', tree_hash='tree',
        action='convert_document', phase='preview', slot='custom_article',
        artifact='# Article', arguments={'output_format': 'latex'},
    )
    result = workflow_routes.invoke_workflow_action(request)['result']
    assert result['format'] == 'latex'
    assert 'Article' in result['content']


@pytest.mark.asyncio
@pytest.mark.parametrize('count, embed_fonts', [(31, None), (100, None), (31, False)])
async def test_ppt_export_keeps_all_pages_above_former_limit(monkeypatch, tmp_path, count, embed_fonts):
    import io
    import urllib.request

    routes = _load_workflow_routes(monkeypatch)
    monkeypatch.setattr(routes, 'editable_pptx_enabled', lambda: True)
    monkeypatch.setenv('LAZYMIND_SUBAGENT_WORKSPACE', str(tmp_path))
    monkeypatch.setenv('LAZYMIND_PPT_EXPORT_CLI', '')

    def export(request, timeout):
        payload = json.loads(request.data)
        assert payload.get('embed_fonts') is embed_fonts
        deck = Path(payload['deck_dir'])
        pages = sorted((deck / 'pages').glob('*.html'))
        assert len(pages) == count
        assert pages[-1].read_text() == f'<html>Page {count}</html>'
        (deck / 'output.pptx').write_bytes(b'export-result')
        return io.BytesIO(json.dumps({'status': 'ok', 'fontEmbeddingEnabled': embed_fonts is not False}).encode())

    monkeypatch.setattr(urllib.request, 'urlopen', export)
    response = await routes.export_ppt_from_html(routes.PptExportRequest(
        pages=[routes.PptExportPage(html=f'<html>Page {index}</html>') for index in range(1, count + 1)],
        **({'embed_fonts': embed_fonts} if embed_fonts is not None else {}),
    ))
    assert Path(response.path).read_bytes() == b'export-result'
    await response.background()
    assert not Path(response.path).exists()


@pytest.mark.asyncio
@pytest.mark.parametrize('supports_opt_out', [True, False])
async def test_local_ppt_export_honors_explicit_font_opt_out(monkeypatch, tmp_path, supports_opt_out):
    import asyncio

    routes = _load_workflow_routes(monkeypatch)
    monkeypatch.setattr(routes, 'editable_pptx_enabled', lambda: True)
    monkeypatch.setattr(routes, '_resolve_executable', lambda _name: 'node')
    cli = tmp_path / 'html_to_pptx.mjs'
    cli.touch()
    monkeypatch.setenv('LAZYMIND_SUBAGENT_WORKSPACE', str(tmp_path))
    monkeypatch.setenv('LAZYMIND_PPT_EXPORT_CLI', str(cli))

    async def launch(node, cli_path, *args, **kwargs):
        assert node == 'node' and cli_path == str(cli)
        assert args[-1] == '--no-embed-fonts'
        deck = Path(args[args.index('--deck-dir') + 1])
        (deck / 'output.pptx').write_bytes(b'export-result')

        async def communicate():
            payload = {'status': 'ok'}
            if supports_opt_out:
                payload['fontEmbeddingEnabled'] = False
            return json.dumps(payload).encode(), b''

        return SimpleNamespace(returncode=0, communicate=communicate)

    monkeypatch.setattr(asyncio, 'create_subprocess_exec', launch)
    request = routes.PptExportRequest(pages=[routes.PptExportPage(html='<html>Page</html>')], embed_fonts=False)
    if supports_opt_out:
        response = await routes.export_ppt_from_html(request)
        assert Path(response.path).read_bytes() == b'export-result'
        await response.background()
    else:
        with pytest.raises(routes.HTTPException, match='does not support'):
            await routes.export_ppt_from_html(request)
    assert list((tmp_path / '.ppt_on_demand').iterdir()) == []


def test_ppt_selection_preview_loads_pinned_runtime_dependencies(monkeypatch, tmp_path):
    routes = _load_workflow_routes(monkeypatch)
    root = Path(__file__).resolve().parents[3] / 'workflows/ppt-workflow'
    package = {'revision_id': 'ppt-action-runtime-test', 'tree_hash': 'ppt-action-runtime-test', 'files': {
        path: base64.b64encode((root / path).read_bytes()).decode()
        for path in ('workflow.yaml', 'scripts/tools.py', 'runtime/lib/html_structure.py')
    }}

    class Client:
        def __init__(self, *args, **kwargs):
            pass

        def get_workflow(self, workflow_id, revision_id):
            assert workflow_id == 'ppt-workflow' and revision_id == package['revision_id']
            return SimpleNamespace(result=package)

    monkeypatch.setattr(routes, 'WorkflowClient', Client)
    monkeypatch.setattr(routes, 'inject_model_config', lambda config: None)
    monkeypatch.setattr(routes, 'inject_tool_config', lambda config: None)
    monkeypatch.setattr('tempfile.gettempdir', lambda: str(tmp_path))
    monkeypatch.delitem(sys.modules, 'html_structure', raising=False)
    monkeypatch.setattr(sys, 'path', [p for p in sys.path if not p.endswith('/runtime/lib')])
    original = ('<html><head></head><body><div class="wrapper"><div id="ct">'
                '<h1 data-el="title">标题</h1><p data-el="footer">来源：资料.pdf</p>'
                '</div></div><aside hidden data-el="speaker-notes">保留文稿</aside></body></html>')
    encoded = base64.b64encode(original.encode()).decode()
    request = routes.WorkflowActionInvokeRequest(
        workflow_id='ppt-workflow', revision_id=package['revision_id'], tree_hash=package['tree_hash'],
        action='rewrite_selection', phase='preview', slot='preview_html',
        artifact={'path': f'data:text/plain;base64,{encoded}', 'type': 'text'}, artifact_store=str(tmp_path),
        arguments={'instruction': '删除', 'selection': {'type': 'ppt_html', 'page': 2, 'el': 'footer'}},
    )
    result = routes.invoke_workflow_action(request)['result']
    assert '来源：资料.pdf' not in result['candidate_html']
    assert '标题' in result['candidate_html'] and '保留文稿' in result['candidate_html']
    assert result['patch']['type'] == 'ppt_html_ops'
