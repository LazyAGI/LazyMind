"""External Workflow package contracts, authorization and deterministic tool behavior."""
import base64
import io
import os
from pathlib import Path
import zipfile

from fastapi import FastAPI
from fastapi.testclient import TestClient
import pytest
import yaml

from lazymind.chat.api import agent_control_routes, external_workflow_tool_routes
from lazymind.chat.workflow import external_ppt_tools as ppt
from lazymind.chat.workflow.external_tools import (
    ExternalToolCallRequest, ExternalToolRequest, PPT_BINDING, call_external_tool,
    list_external_tools, package_tree_hash,
)

ROOT = Path(__file__).resolve().parents[3]
HTML = ('<html><head><meta charset="utf-8"></head><body>'
        '<div class="slide" style="width:1280px;height:720px">One page</div></body></html>')


def pptx_bytes(order=(2, 1), absolute_targets=False):
    output = io.BytesIO()
    with zipfile.ZipFile(output, 'w') as archive:
        archive.writestr('[Content_Types].xml', '<Types/>')
        slides = ''.join(f'<p:sldId id="{number}" r:id="rId{number}"/>' for number in order)
        archive.writestr('ppt/presentation.xml',
                         '<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" '
                         'xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">'
                         f'<p:sldIdLst>{slides}</p:sldIdLst></p:presentation>')
        prefix = '/ppt/' if absolute_targets else ''
        relationships = ''.join(f'<Relationship Id="rId{number}" Target="{prefix}slides/slide{number}.xml" '
                                'Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide"/>'
                                for number in order)
        archive.writestr('ppt/_rels/presentation.xml.rels', f'<Relationships>{relationships}</Relationships>')
        for number in order:
            archive.writestr(f'ppt/slides/slide{number}.xml',
                             '<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" '
                             'xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">'
                             f'<a:t>Page {number}</a:t></p:sld>')
    return output.getvalue()


def request(files=None, tools=None, policy='execution', **extra):
    tools = tools if tools is not None else ['ppt_inspect_pptx']
    workflow = {'runtime': {policy: {'version': '1', 'steps': {
        'inspect': {'tools': [{'name': name, 'source': 'package'} for name in tools]}}}},
        'tool_scripts': [{'path': 'scripts/external_tools.py', 'functions': tools}]}
    contents = {'workflow.yaml': yaml.safe_dump(workflow).encode(),
                'scripts/external_tools.py': PPT_BINDING.encode(), **(files or {})}
    tree_hash = package_tree_hash(contents)
    return {'workflow_id': 'resource-uuid', 'revision_id': 'fixed-revision', 'step_id': 'inspect',
            'tree_hash': tree_hash, 'package': {'tree_hash': tree_hash, 'files': {
                name: base64.b64encode(value).decode() for name, value in contents.items()}}, **extra}


def call(payload, name='ppt_inspect_pptx', arguments=None):
    return call_external_tool(ExternalToolCallRequest(**payload, tool_name=name, arguments=arguments or {}))


@pytest.mark.parametrize('absolute_targets', [False, True])
def test_pinned_tool_catalog_and_real_inspection_preserve_slide_order(absolute_targets):
    payload = request()
    catalog = list_external_tools(ExternalToolRequest(**payload))['tools']
    assert catalog[0]['available'] is True
    assert catalog[0]['name'] == 'ppt_inspect_pptx'
    assert catalog[0]['input_schema']['required'] == ['content_base64']
    raw = pptx_bytes(absolute_targets=absolute_targets)
    result = call(payload, arguments={'content_base64': base64.b64encode(raw).decode()})['result']
    assert result == {'page_count': 2, 'has_notes': False,
                      'pages': [{'page': 1, 'texts': ['Page 2']}, {'page': 2, 'texts': ['Page 1']}]}


@pytest.mark.parametrize('change,code', [
    ({'files': {'scripts/external_tools.py': b'raise RuntimeError("must never execute")'}}, 'TOOL_NOT_ALLOWED'),
    ({'files': {'../secret': b'no'}}, 'INVALID_PACKAGE'),
    ({'step_id': 'other-step'}, 'INVALID_PACKAGE'),
    ({'tree_hash': '0' * 64}, 'INVALID_PACKAGE'),
    ({'policy': 'external_execution'}, 'INVALID_PACKAGE'),
])
def test_snapshot_and_adapter_boundary(change, code):
    with pytest.raises(ppt.ExternalToolError) as raised:
        list_external_tools(ExternalToolRequest(**request(**change)))
    assert raised.value.code == code


def test_content_change_cannot_keep_revision_hash():
    payload = request()
    payload['package']['files']['scripts/external_tools.py'] = base64.b64encode(b'changed').decode()
    with pytest.raises(ppt.ExternalToolError, match='tree hash'):
        list_external_tools(ExternalToolRequest(**payload))


def test_mixed_core_and_python_adapters_do_not_hide_the_python_catalog():
    payload = request(tools=['ppt_inspect_pptx', 'knowledge_read_document', 'academic_context'])
    names = [tool['name'] for tool in list_external_tools(ExternalToolRequest(**payload))['tools']]
    assert names == ['ppt_inspect_pptx']
    inspected = call(payload, arguments={'content_base64': base64.b64encode(pptx_bytes()).decode()})['result']
    assert inspected['page_count'] == 2
    with pytest.raises(ppt.ExternalToolError) as raised:
        call(payload, 'academic_context', {})
    assert raised.value.code == 'TOOL_ADAPTER_UNAVAILABLE'


@pytest.mark.parametrize('name,arguments,code', [
    ('ppt_export_html', {}, 'TOOL_NOT_ALLOWED'),
    ('ppt_inspect_pptx', {'path': '/etc/passwd'}, 'INVALID_ARGUMENT'),
    ('ppt_inspect_pptx', {'content_base64': 123}, 'INVALID_ARGUMENT'),
    ('ppt_inspect_pptx', {'content_base64': 'bad!'}, 'INVALID_ARGUMENT'),
])
def test_tool_selection_and_arguments(name, arguments, code):
    with pytest.raises(ppt.ExternalToolError) as raised:
        call(request(), name, arguments)
    assert raised.value.code == code


@pytest.mark.parametrize('body', [
    '<script>fetch("http://localhost")</script>', '<img src="file:///etc/passwd">',
    '<img src="https://example.com/image.png">', '<img srcset="https://example.com/image.png 1x">',
    '<div onclick="alert(1)"></div>', '<style>@import "https://example.com";</style>',
    '<style>div{background:url(https://example.com)}</style>',
    '<meta http-equiv="refresh" content="0;url=file:///etc/passwd">',
    '<svg><use href="file:///etc/passwd"/></svg>', '<iframe src="data:text/html,test"></iframe>',
])
def test_offline_html_rejects_active_external_or_local_assets(body):
    with pytest.raises(ppt.ExternalToolError) as raised:
        ppt.ppt_render_html(f'<html><body>{body}</body></html>')
    assert raised.value.code == 'INVALID_ARGUMENT'


def test_missing_configuration_is_actionable(monkeypatch):
    monkeypatch.delenv('LAZYMIND_PPT_EXPORT_CLI', raising=False)
    with pytest.raises(ppt.ExternalToolError, match='Configure.*LazyMind.*retry') as raised:
        ppt.ppt_export_html([{'html': HTML, 'notes': 'One page notes'}])
    assert raised.value.code == 'DEPENDENCY_MISSING'


def test_authenticated_route_uses_same_pinned_validation(monkeypatch):
    monkeypatch.setattr(agent_control_routes, 'config', {'core_internal_token': 'test-internal-only'})
    app = FastAPI()
    app.include_router(external_workflow_tool_routes.router)
    client = TestClient(app)
    payload = request()
    assert client.post('/api/workflow/external-tools/list', json=payload).status_code == 401
    headers = {'X-LazyMind-Internal-Token': 'test-internal-only'}
    response = client.post('/api/workflow/external-tools/list', json=payload, headers=headers)
    assert response.status_code == 200
    assert response.json()['tools'][0]['available'] is True
    payload.update(tool_name='ppt_export_html', arguments={})
    response = client.post('/api/workflow/external-tools/call', json=payload, headers=headers)
    assert response.status_code == 400
    assert response.json()['detail']['code'] == 'TOOL_NOT_ALLOWED'
    assert 'public_message' not in response.json()['detail']

    render = request(tools=['ppt_render_html'], tool_name='ppt_render_html', arguments={'html': '<div>fragment</div>'})
    response = client.post('/api/workflow/external-tools/call', json=render, headers=headers)
    assert response.status_code == 400
    assert response.json()['detail']['public_message'] == 'Provide a complete html document with an <html> element.'


def test_every_builtin_has_one_shared_business_specification():
    packages = sorted((ROOT / 'workflows').glob('*/workflow.yaml'))
    assert len(packages) == 14
    count = 0
    for path in packages:
        workflow = yaml.safe_load(path.read_text())
        assert 'external_execution' not in workflow['runtime']
        shared = workflow['runtime']['execution']
        states = yaml.safe_load((path.parent / 'scenario/state.yml').read_text())
        steps = states['steps']
        assert set(shared['steps']) == set(steps)
        for step_id in steps:
            contract = shared['steps'][step_id]
            assert steps[step_id]['prompt'].strip()
            assert not {'prompt', 'required_outputs', 'acceptance_criteria'} & contract.keys()
            for skill in shared['skills'] + contract.get('skills', []):
                assert (path.parent / skill).is_file()
            count += 1
    assert count == 104


@pytest.mark.skipif(not os.environ.get('LAZYMIND_PPT_EXPORT_CLI'), reason='requires installed PPT exporter and Chromium')
def test_installed_exporter_one_page_and_preview_are_real_inline_files():
    payload = request(tools=['ppt_export_html', 'ppt_inspect_pptx', 'ppt_render_html'])
    exported = call(payload, 'ppt_export_html', {'pages': [{'html': HTML, 'notes': 'One page notes'}]})['result']
    assert exported['page_count'] == 1
    output = exported['files'][0]
    assert output['name'] == 'presentation.pptx'
    assert set(output) == {'name', 'mime_type', 'content_base64', 'sha256', 'size_bytes'}
    inspected = call(payload, arguments={'content_base64': output['content_base64']})['result']
    assert inspected['page_count'] == 1
    assert inspected['has_notes'] is True
    assert any('One page' in text for text in inspected['pages'][0]['texts'])
    rendered = call(payload, 'ppt_render_html', {'html': HTML})['result']['files'][0]
    assert base64.b64decode(rendered['content_base64']).startswith(b'\x89PNG\r\n\x1a\n')
    assert rendered['size_bytes'] > 100
