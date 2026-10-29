"""Shared presentation behavior, collected by the algorithm CI suite."""
import importlib.util
import json
import sys
import types
from pathlib import Path
from unittest import mock

import pytest
import yaml


ROOT = Path(__file__).resolve().parents[4]


@pytest.fixture
def tools():
    spec = importlib.util.spec_from_file_location(
        'ppt_shared_test_helpers', ROOT / 'workflows/ppt-workflow/scripts/tests/test_style_flow.py',
    )
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module.TOOLS


def test_multiple_attachments_read_to_eof_and_keep_distinct_sources(tools, tmp_path, monkeypatch):
    reader = types.ModuleType('lazymind.chat.engine.tools.file_resources.tools')
    reader.read_file_resource = mock.Mock(side_effect=lambda path, offset, limit: {
        'text': f'{Path(path).name}: evidence {offset}', 'eof': offset == 2,
        'next_offset': 2 if offset == 1 else None,
    })
    monkeypatch.setitem(sys.modules, reader.__name__, reader)
    monkeypatch.setattr(tools, '_conversation_root', lambda: tmp_path)
    monkeypatch.setattr(tools, 'find_user_attachment', lambda name: {'path': str(tmp_path / name)})
    for name in ('paper-a.pdf', 'paper-b.pdf'):
        (tmp_path / name).write_text(name)
    result = tools.ppt_cache_academic_sources(['paper-a.pdf', 'paper-b.pdf', 'missing.pdf'])
    assert result['success_count'] == 2
    assert result['sources'][2]['status'] == 'error'
    ids = [source['source_id'] for source in result['sources'][:2]]
    assert ids[0] != ids[1]
    for source_id, name in zip(ids, ('paper-a.pdf', 'paper-b.pdf')):
        chunk = tools.ppt_read_academic_source(source_id, 2)
        assert chunk['reader_result']['text'] == f'{name}: evidence 2'
        assert chunk['next_chunk'] is None
    tools.ppt_cache_academic_sources(['paper-a.pdf', 'paper-b.pdf'])
    assert reader.read_file_resource.call_count == 4


@pytest.mark.parametrize('kind,content,changed', [
    ('formula', 'ROI = 30<sup>2</sup>', 'ROI = 40<sup>2</sup>'),
    ('chart', '<svg><text>研发 60%，运维 40%</text></svg>', '<svg><text>研发 70%，运维 30%</text></svg>'),
    ('icon', '<svg><circle r="5"></circle></svg>', '<svg><circle r="6"></circle></svg>'),
])
def test_general_presentation_elements_edit_in_place(tools, monkeypatch, kind, content, changed):
    original = f'<div data-el="{kind}-1" data-ppt-kind="{kind}">{content}</div>'
    fragment = f'<div data-el="{kind}-1" data-ppt-kind="{kind}">{changed}</div>'
    html = f'<html><body>{original}<p>2026 述职报告，其他内容不变</p></body></html>'
    monkeypatch.setattr(tools, '_agent_llm_call',
                        lambda *a, **kw: json.dumps({'op': 'replace_academic_element', 'html': fragment}))
    ops, _, _ = tools._selection_edit_ops('按新数据修改选中元素', {'el': f'{kind}-1'}, tools._HtmlTree(html))
    edited, _, _, _ = tools._apply_html_ops(html, ops)
    tools._validate_local_html_edit(html, edited)
    assert fragment in edited
    assert '<p>2026 述职报告，其他内容不变</p>' in edited


def test_generation_modes_share_executable_graph_and_source_inputs():
    state = yaml.safe_load((ROOT / 'workflows/ppt-workflow/scenario/state.yml').read_text())
    # No generation-mode condition can remove an execution stage.
    assert not any('generation_mode' in edge.get('when', '')
                   for edges in state['transitions'].values() for edge in edges)
    assert state['transitions']['build_outline'] == [{'to': 'plan_page_prompts'}]
    planner = state['steps']['plan_page_prompts']
    assert {'material': 'academic_brief', 'required': False} in planner['inputs']
    assert all(step['mode'] == 'auto' for step in state['steps'].values())
    workflow = yaml.safe_load((ROOT / 'workflows/ppt-workflow/workflow.yaml').read_text())
    fields = {field['id']: field for field in workflow['runtime']['clarification_fields']}
    assert fields['academic_materials']['attachment']['max_files'] == -1
    slots = {slot['id']: slot for slot in workflow['slots']}
    assert slots['uploaded_materials']['cardinality'] == 'list'
    tabs = workflow['ui']['tabs']
    assert all(tab.get('presentation_hide_when_material') == 'compact_presentation'
               for tab in tabs if tab['id'] != 'composite_preview')
    assert 'presentation_hide_when_material' not in tabs[-1]


@pytest.fixture
def library(monkeypatch, tools, tmp_path):
    service = types.ModuleType('lazymind.chat.service.academic_handoff')
    frozen = {'handoff_id': 'owned', 'kind': 'ppt', 'reflection_first': True,
              'input': {'user_instruction': '8页组会', 'papers': [
                  {'paper_id': 'a', 'title': 'Paper A', 'source_version': 'v2'},
                  {'paper_id': 'b', 'title': 'Paper B', 'source_version': 'v1'}]}}
    service.academic_handoff_context = lambda: frozen
    monkeypatch.setitem(sys.modules, service.__name__, service)
    monkeypatch.setattr(tools, '_conversation_root', lambda: tmp_path)
    return service, frozen


def library_page(paper_id='a', version='v2', cursor='', text='Full paper text', content_hash='same'):
    return {'status': 'ready', 'content_level': 'fulltext', 'paper': {'paper_id': paper_id},
            'source_version': version, 'content_hash': content_hash,
            'pages': [{'page': 6 if cursor == '' else 1, 'text': text}], 'next_cursor': cursor}


def test_library_fulltext_pages_feed_shared_academic_reader(tools, library):
    service, _ = library
    calls = []

    def read(**kwargs):
        calls.append(kwargs)
        if kwargs.get('paper_id') == 'a':
            return library_page(cursor='opaque-cursor', text='A first batch')
        if kwargs.get('cursor'):
            return library_page(text='A final batch')
        return library_page('b', 'v1', text='B full text')
    service.academic_read_paper = read
    result = tools.ppt_cache_library_papers()
    assert result['complete'] and result['success_count'] == 2
    assert calls == [{'paper_id': 'a'}, {'cursor': 'opaque-cursor'}, {'paper_id': 'b'}]
    source = result['sources'][0]
    assert source['paper']['title'] == 'Paper A' and source['chunk_count'] == 2
    chunk = tools.ppt_read_academic_source(source['source_id'], 2)
    assert chunk['reader_result']['pages'] == [{'page': 6, 'text': 'A final batch'}]
    assert chunk['next_chunk'] is None


@pytest.mark.parametrize('status', ['processing', 'failed', 'unavailable'])
def test_library_not_ready_is_not_cached_or_replaced_by_abstract(tools, library, tmp_path, status):
    service, frozen = library
    frozen['input']['papers'] = frozen['input']['papers'][:1]
    service.academic_read_paper = lambda **kwargs: {
        'status': status, 'content_level': 'none', 'warnings': ['not ready'], 'abstract': 'a tempting summary',
    }
    result = tools.ppt_cache_library_papers()
    assert not result['complete'] and result['success_count'] == 0
    assert result['sources'][0]['status'] == status
    assert not list((tmp_path / 'academic_sources').glob('*.json'))


@pytest.mark.parametrize('failure', ['version', 'hash', 'cursor', 'permission'])
def test_library_partial_read_failure_never_publishes_complete_source(tools, library, tmp_path, failure):
    service, frozen = library
    frozen['input']['papers'] = frozen['input']['papers'][:1]

    def read(**kwargs):
        if kwargs.get('paper_id'):
            return library_page(cursor='opaque')
        if failure == 'permission':
            raise PermissionError('403 forbidden')
        return library_page(version='v3' if failure == 'version' else 'v2',
                            content_hash='changed' if failure == 'hash' else 'same',
                            cursor='opaque' if failure == 'cursor' else '')
    service.academic_read_paper = read
    result = tools.ppt_cache_library_papers()
    assert not result['complete'] and result['sources'][0]['status'] == 'error'
    assert 'source_id' not in result['sources'][0]
    assert not list((tmp_path / 'academic_sources').glob('*.json'))


def test_reflection_context_keeps_saved_prose_and_original_selection(tools, library, monkeypatch):
    _, frozen = library
    monkeypatch.setattr(tools, '_resolve_artifact_text', lambda context, key: ('已保存的真实心得', 'text'))
    result = tools.ppt_paper_handoff_context()
    assert result['reflection_markdown'] == '已保存的真实心得'
    assert result['input'] == frozen['input'] and result['reflection_first'] is True
    assert 'reflection_markdown' not in frozen


def test_library_launch_binds_authoritative_input_and_satisfies_upload_question():
    spec = importlib.util.spec_from_file_location(
        'paper_handoff_launch', ROOT / 'algorithm/lazymind/chat/workflow/paper_handoff.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    policy = yaml.safe_load((ROOT / 'workflows/ppt-workflow/workflow.yaml').read_text())['runtime']
    frozen = {'handoff_id': 'h', 'kind': 'ppt', 'input': {'papers': [{'paper_id': 'a'}, {'paper_id': 'b'}]}}
    requests = []
    response = types.SimpleNamespace(status_code=200, raise_for_status=lambda: None, json=lambda: frozen)
    client = types.SimpleNamespace(base_url='http://core', _headers=lambda: {'Authorization': 'bound-user'},
                                   transport=types.SimpleNamespace(request=lambda *a, **kw:
                                                                   requests.append((a, kw)) or response))
    bindings, satisfied = module.resolve_paper_handoff(client, 'owned-chat', policy)
    assert json.loads(bindings['task_context']) == frozen
    assert satisfied == ['academic_materials']
    assert requests[0][1]['params'] == {'conversation_id': 'owned-chat'}
    assert requests[0][1]['headers'] == {'Authorization': 'bound-user'}
    response.status_code = 404
    assert module.resolve_paper_handoff(client, 'ordinary-chat', policy) == ({}, [])


def test_reflection_publisher_rejects_empty_and_preserves_original_papers(tools, library, monkeypatch):
    _, frozen = library
    monkeypatch.setattr(tools, '_resolve_artifact_text', lambda context, key: (None, 'text'))
    published = []
    monkeypatch.setattr(tools, '_save_artifact', lambda *a, **kw: published.append((a, kw)) or {'status': 'ok'})
    for invalid in ('', '  ', '阅读心得生成能力待对接'):
        with pytest.raises(ValueError):
            tools.ppt_publish_paper_reflection(invalid)
    assert published == []
    prose = 'Paper A 的方法贡献与 Paper B 的实验条件不同，应分开讨论。读者观点：进一步复现实验。'
    result = tools.ppt_publish_paper_reflection(prose)
    assert result == {'artifact': {'status': 'ok'}, 'paper_count': 2}
    assert published[0][0] == ('reflection_markdown', prose)
    assert published[0][1]['internal_publish'] is True
    assert [paper['paper_id'] for paper in frozen['input']['papers']] == ['a', 'b']


@pytest.mark.parametrize('layout', ['files_only', 'same_turn', 'across_turns'])
def test_direct_upload_manifest_resolves_and_caches_every_same_named_paper(tools, tmp_path, monkeypatch, layout):
    from lazymind.chat.workflow.paper_handoff import paper_attachment_context
    from lazymind.chat.engine.tools.file_resources import resolver

    paths = []
    for index in range(2):
        directory = tmp_path / str(index)
        directory.mkdir()
        path = directory / 'paper.pdf'
        path.write_text(f'Complete paper {index}')
        paths.append(str(path))
    cfg = {'files': paths}
    if layout == 'same_turn':
        cfg['history_files_per_turn'] = {'3': paths}
    elif layout == 'across_turns':
        cfg['history_files_per_turn'] = {'2': paths[:1], '3': paths[1:]}
    runtime = yaml.safe_load((ROOT / 'workflows/ppt-workflow/workflow.yaml').read_text())['runtime']
    context, satisfied = paper_attachment_context(cfg, runtime)
    sources = json.loads(context.splitlines()[3])
    assert len(sources) == 2
    assert satisfied == ['academic_materials']
    monkeypatch.setattr(resolver, '_agentic_config', lambda: cfg)
    reader = types.ModuleType('lazymind.chat.engine.tools.file_resources.tools')
    reader.read_file_resource = mock.Mock(side_effect=lambda path, **kwargs: {
        'text': Path(path).read_text(), 'eof': True,
    })
    monkeypatch.setitem(sys.modules, reader.__name__, reader)
    monkeypatch.setattr(tools, '_conversation_root', lambda: tmp_path)
    result = tools.ppt_cache_academic_sources(
        [item['filename'] for item in sources], [item['turn'] for item in sources],
    )
    assert result['success_count'] == 2, result
    assert len({item['source_id'] for item in result['sources']}) == 2
    assert {item['excerpt'] for item in result['sources']} == {'Complete paper 0', 'Complete paper 1'}
    assert {call.args[0] for call in reader.read_file_resource.call_args_list} == set(paths)


def test_nonpaper_or_unavailable_uploads_do_not_satisfy_paper_question(tmp_path):
    from lazymind.chat.workflow.paper_handoff import paper_attachment_context
    paths = [tmp_path / name for name in ('image.png', 'template.pptx', 'missing.pdf')]
    for path in paths[:2]:
        path.write_text('not a paper')
    runtime = yaml.safe_load((ROOT / 'workflows/ppt-workflow/workflow.yaml').read_text())['runtime']
    assert paper_attachment_context({'files': list(map(str, paths))}, runtime) == ('', [])
