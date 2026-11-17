"""Exercise the shared page/model boundary with the real native HTML path."""
import base64
from concurrent.futures import ThreadPoolExecutor
import importlib.util
from pathlib import Path
import sys
from types import SimpleNamespace

import lazyllm
import pytest
import yaml

from lazymind.chat.workflow import external_ppt_tools as ppt
from lazymind.chat.workflow.external_tool_context import external_package_scope, get_external_package_files
from lazymind.chat.workflow.external_tools import (
    ExternalToolCallRequest, GENERATION_BINDING, call_external_tool, package_tree_hash,
)


ROOT = Path(__file__).resolve().parents[4]
PROMPT_PATH = 'runtime/prompts/page_html.md'
HTML = '<!doctype html><html><head><title>结果</title></head><body><h1>增长 30%</h1></body></html>'
BRIEF = 'Target language: zh-Hans. 标题：增长 30%；白底黑字，保留统计条件。不新增数字。'


@pytest.fixture
def package():
    return {PROMPT_PATH: (ROOT / 'workflows/ppt-workflow' / PROMPT_PATH).read_bytes()}


@pytest.fixture
def no_model(monkeypatch):
    def forbidden(*args, **kwargs):
        raise AssertionError('External page preparation must never invoke a local model')
    monkeypatch.setattr(lazyllm, 'AutoModel', forbidden)


@pytest.fixture
def native(monkeypatch):
    spec = importlib.util.spec_from_file_location(
        'ppt_external_native_generation', ROOT / 'workflows/ppt-workflow/runtime/scripts/run_stage.py',
    )
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    # Named assets are an independent optional catalog service. This deck has
    # no requested icons; retain all native preparation/normalization logic.
    monkeypatch.setattr(module, 'prompt_catalog', lambda _query: '')
    return module


def accept(package, text=HTML, *, page_no=1, brief=BRIEF):
    with external_package_scope(package):
        prepared = ppt.ppt_prepare_page(page_no, brief)
        return ppt.ppt_prepare_page(page_no, brief, [{
            'request_hash': prepared['generation_request']['request_hash'], 'text': text,
        }])


def test_external_and_native_use_same_request_and_normalized_html(package, native, no_model, tmp_path):
    wrapped = '<think>private reasoning</think>\n```html\n' + HTML + '\n```'
    requests = []

    def host_model(system, user, **kwargs):
        requests.append({'system_prompt': system, 'user_prompt': user, **kwargs})
        return wrapped

    status = native._write_page_html_from_query(
        tmp_path, 1, BRIEF, page_plan={'page_no': 1, 'slots': []},
        inherited_image=None, background_image=None,
        prompt_mode='complete-page-prompt', language='', llm_call=host_model,
    )
    assert status == 0
    with external_package_scope(package):
        prepared = ppt.ppt_prepare_page(1, BRIEF)
    request = prepared['generation_request']
    assert prepared['status'] == 'generation_required'
    assert request['system_prompt'].startswith(requests[0]['system_prompt'])
    assert request['user_prompt'] == requests[0]['user_prompt'] == BRIEF
    assert request['request_name'] == requests[0]['request_name'] == 'page-html:1'
    assert 'OFFLINE TRANSFER CONTRACT' in request['system_prompt']
    ready = accept(package, wrapped)
    assert ready['status'] == 'ready' and 'html' not in ready
    assert len(ready['files']) == 1
    file = ready['files'][0]
    assert file['name'] == 'page_001.html' and file['mime_type'] == 'text/html'
    assert base64.b64decode(file['content_base64']).decode() == HTML
    assert (tmp_path / 'pages/page_001.html').read_text() == HTML


@pytest.mark.parametrize('changed', ['brief', 'page', 'prompt'])
def test_replay_is_bound_to_page_brief_and_pinned_prompt(package, no_model, changed):
    with external_package_scope(package):
        request = ppt.ppt_prepare_page(1, BRIEF)['generation_request']
    page, brief = 1, BRIEF
    if changed == 'brief':
        brief += ' 修改标题。'
    elif changed == 'page':
        page = 2
    else:
        package = {PROMPT_PATH: b'Updated package contract'}
    with external_package_scope(package), pytest.raises(ppt.ExternalToolError, match='does not match'):
        ppt.ppt_prepare_page(page, brief, [{'request_hash': request['request_hash'], 'text': HTML}])


@pytest.mark.parametrize('response', [
    '<html><body>truncated', 'Only a prose summary',
    '<html><body><script>alert(1)</script></body></html>',
    '<html><body><img src="file:///etc/passwd"></body></html>',
    '<html><body><img src="https://example.com/image.png"></body></html>',
    '<html><body><img data-ppt-asset="guessed:asset"></body></html>',
])
def test_accept_rejects_incomplete_active_and_unresolved_output(package, no_model, response):
    with pytest.raises(ppt.ExternalToolError):
        accept(package, response)


def test_pinned_prompt_includes_are_data_not_current_package_code(no_model):
    package = {PROMPT_PATH: b'Pinned contract <<<INLINE: prompts/rules.md>>>',
               'runtime/prompts/rules.md': b'Retain exact numbers',
               'runtime/scripts/run_stage.py': b'raise AssertionError("do not execute")'}
    with external_package_scope(package):
        prepared = ppt.ppt_prepare_page(1, BRIEF)
    assert prepared['generation_request']['system_prompt'].startswith('Pinned contract Retain exact numbers')


@pytest.mark.parametrize('include', ['../../secret', '/etc/passwd', 'prompts/page_html.md'])
def test_unsafe_or_recursive_prompt_include_is_rejected(no_model, include):
    package = {PROMPT_PATH: f'<<<INLINE: {include}>>>'.encode()}
    with external_package_scope(package), pytest.raises(ppt.ExternalToolError):
        ppt.ppt_prepare_page(1, BRIEF)


def test_repeated_includes_obey_total_expansion_limit(no_model):
    package = {PROMPT_PATH: b'<<<INLINE: prompts/large.md>>>' * 10,
               'runtime/prompts/large.md': b'X' * (200 * 1024)}
    with external_package_scope(package), pytest.raises(ppt.ExternalToolError, match='expanded page prompt'):
        ppt.ppt_prepare_page(1, BRIEF)


def test_timing_and_paper_order_share_native_planning():
    timing = ppt.ppt_compute_timing(10, [
        {'title': '开场', 'weight': .4}, {'title': '方法', 'weight': 2},
        {'title': '结果', 'weight': 2}, {'title': '总结', 'weight': .6},
    ])
    assert sum(page['seconds'] for page in timing['pages']) == timing['total_seconds'] == 600
    assert timing['pages'][1]['seconds'] > timing['pages'][0]['seconds']
    assert timing['pages'][-1]['end_seconds'] == 600
    order = ppt.ppt_order_papers(
        [{'source_id': 'B', 'title': 'Dependent'}, {'source_id': 'A', 'title': 'Prerequisite'}],
        [{'prerequisite': 'A', 'dependent': 'B', 'evidence': 'B section 2 extends A section 3'}],
    )
    assert [paper['source_id'] for paper in order['paper_order']] == ['A', 'B']
    with pytest.raises(ppt.ExternalToolError):
        ppt.ppt_compute_timing(0, [{'title': 'Invalid'}])
    with pytest.raises(ppt.ExternalToolError, match='200 selected papers'):
        ppt.ppt_order_papers([{'source_id': str(index), 'title': 'Paper'} for index in range(201)])
    with pytest.raises(ppt.ExternalToolError, match='1000 paper dependencies'):
        ppt.ppt_order_papers([{'source_id': 'A', 'title': 'Paper'}], [{}] * 1001)


def public_request(prompt, **arguments):
    declaration = {'source': 'package', 'name': 'ppt_prepare_page'}
    workflow = {'runtime': {'execution': {'version': '1', 'steps': {
        'generate': {'tools': [declaration]},
    }}}, 'tool_scripts': [{'path': 'scripts/external_generation_tools.py', 'functions': ['ppt_prepare_page']}]}
    files = {'workflow.yaml': yaml.safe_dump(workflow).encode(),
             'scripts/external_generation_tools.py': GENERATION_BINDING.encode(), PROMPT_PATH: prompt}
    digest = package_tree_hash(files)
    return ExternalToolCallRequest(
        workflow_id='ppt-workflow', revision_id='pinned-revision', tree_hash=digest,
        step_id='generate', tool_name='ppt_prepare_page',
        package={'tree_hash': digest, 'files': {
            name: base64.b64encode(content).decode() for name, content in files.items()
        }}, arguments={'page_no': 1, 'brief': BRIEF, **arguments},
    )


def test_public_audited_binding_prepares_and_accepts_without_package_execution(no_model):
    prepared = call_external_tool(public_request(b'Pinned native rules'))['result']
    request = prepared['generation_request']
    accepted = call_external_tool(public_request(b'Pinned native rules', generation_responses=[{
        'request_hash': request['request_hash'], 'text': HTML,
    }]))['result']
    assert accepted['status'] == 'ready'
    assert base64.b64decode(accepted['files'][0]['content_base64']).decode() == HTML
    with pytest.raises(RuntimeError, match='verified pinned'):
        get_external_package_files()


def test_concurrent_public_requests_keep_their_own_pinned_package(no_model):
    prompts = [f'Pinned contract {index}'.encode() for index in range(8)]
    with ThreadPoolExecutor(max_workers=4) as executor:
        results = list(executor.map(lambda prompt: call_external_tool(public_request(prompt)), prompts))
    requests = [result['result']['generation_request'] for result in results]
    assert all(request['system_prompt'].startswith(prompt.decode()) for request, prompt in zip(requests, prompts))
    assert len({request['request_hash'] for request in requests}) == len(prompts)
    with pytest.raises(RuntimeError, match='verified pinned'):
        get_external_package_files()


@pytest.mark.parametrize('configured', ['', '/configured/python'])
def test_export_font_subset_uses_service_python_or_explicit_override(monkeypatch, configured):
    observed = []
    monkeypatch.setenv('LAZYMIND_FONT_SUBSET_PYTHON', configured)
    monkeypatch.setattr(ppt.subprocess, 'run', lambda command, **kwargs: (
        observed.append(kwargs) or SimpleNamespace(returncode=0, stdout='{"status":"ok"}')))
    assert ppt._run(['node', 'configured-exporter'], 30) == {'status': 'ok'}
    assert observed[0]['env']['LAZYMIND_FONT_SUBSET_PYTHON'] == (configured or sys.executable)
    assert observed[0]['env']['LAZYMIND_PPT_EXTERNAL_OFFLINE'] == '1'
