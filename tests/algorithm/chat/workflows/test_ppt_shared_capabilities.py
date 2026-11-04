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


def test_talk_timing_is_weighted_contiguous_and_matches_exact_budget(tools):
    result = tools.ppt_plan_timing(10, json.dumps([
        {'title': '开场', 'weight': .4}, {'title': '方法', 'weight': 2},
        {'title': '结果', 'weight': 2}, {'title': '总结', 'weight': .6},
    ]))
    pages = result['pages']
    assert result['total_seconds'] == sum(p['seconds'] for p in pages) == 600
    assert pages[1]['seconds'] > pages[0]['seconds']
    assert pages[0]['start_seconds'] == 0 and pages[-1]['end_seconds'] == 600
    assert all(a['end_seconds'] == b['start_seconds'] for a, b in zip(pages, pages[1:]))
    assert '10:00' in pages[-1]['notes_prefix']


@pytest.mark.parametrize('minutes,pages', [
    (0, [{'title': 'A'}]), (float('nan'), [{'title': 'A'}]),
    (10, [{'title': 'A', 'weight': -1}]), (.01, [{'title': 'A'}]),
])
def test_invalid_timing_is_rejected(tools, minutes, pages):
    with pytest.raises(ValueError):
        tools.ppt_plan_timing(minutes, json.dumps(pages))


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


def test_single_generation_route_and_source_inputs():
    state = yaml.safe_load((ROOT / 'workflows/ppt-workflow/scenario/state.yml').read_text(encoding='utf-8'))
    assert state['transitions']['__start__'] == [{'to': 'analyze_requirements'}]
    assert state['transitions']['build_outline'] == [{'to': 'plan_page_prompts'}]
    planner = state['steps']['plan_page_prompts']
    assert {'material': 'academic_brief', 'required': False} in planner['inputs']
    assert all(step['mode'] == 'auto' for step in state['steps'].values())
    workflow = yaml.safe_load((ROOT / 'workflows/ppt-workflow/workflow.yaml').read_text(encoding='utf-8'))
    fields = {field['id']: field for field in workflow['runtime']['clarification_fields']}
    assert 'generation_mode' not in fields
    assert fields['academic_materials']['attachment']['max_files'] == -1
    slots = {slot['id']: slot for slot in workflow['slots']}
    assert slots['uploaded_materials']['cardinality'] == 'list'
    tabs = workflow['ui']['tabs']
    assert all(tab.get('presentation_hide_when_material') == 'compact_presentation'
               for tab in tabs if tab['id'] != 'composite_preview')
    assert 'presentation_hide_when_material' not in tabs[-1]


@pytest.mark.parametrize('failure', ['empty', 'cursor', 'permission', 'changed'])
def test_attachment_partial_read_never_caches_complete_source(tools, tmp_path, monkeypatch, failure):
    paper = tmp_path / 'paper.pdf'
    paper.write_text('original bytes')
    reader = types.ModuleType('lazymind.chat.engine.tools.file_resources.tools')

    def read(path, offset, limit):
        if failure == 'empty':
            return {'text': '', 'eof': True}
        if offset == 1:
            return {'text': 'first batch', 'eof': False, 'next_offset': 2}
        if failure == 'permission':
            raise PermissionError('403 forbidden')
        if failure == 'changed':
            paper.write_text('replacement bytes')
            return {'text': 'second batch', 'eof': True}
        return {'text': 'second batch', 'eof': False, 'next_offset': 2}
    reader.read_file_resource = read
    monkeypatch.setitem(sys.modules, reader.__name__, reader)
    monkeypatch.setattr(tools, '_conversation_root', lambda: tmp_path)
    monkeypatch.setattr(tools, 'find_user_attachment', lambda name: {'path': str(paper)})
    result = tools.ppt_cache_academic_sources(['paper.pdf'])
    assert result['success_count'] == 0
    assert result['sources'][0]['status'] == 'error'
    assert 'source_id' not in result['sources'][0]
    assert list((tmp_path / 'academic_sources').glob('*.json')) == [tmp_path / 'academic_sources/manifest.json']


def test_reflection_publisher_rejects_empty_and_preserves_original_papers(tools, monkeypatch):
    monkeypatch.setattr(tools, '_resolve_artifact_text', lambda context, key: (None, 'text'))
    published = []
    monkeypatch.setattr(tools, '_save_artifact', lambda *a, **kw: published.append((a, kw)) or {'status': 'ok'})
    for invalid in ('', '  ', '阅读心得生成能力待对接'):
        with pytest.raises(ValueError):
            tools.ppt_publish_paper_reflection(invalid)
    assert published == []
    prose = 'Paper A 的方法贡献与 Paper B 的实验条件不同，应分开讨论。读者观点：进一步复现实验。'
    result = tools.ppt_publish_paper_reflection(prose)
    assert result == {'artifact': {'status': 'ok'}}
    assert published[0][0] == ('reflection_markdown', prose)
    assert published[0][1]['internal_publish'] is True


def test_reflection_publishes_in_real_material_collection_context(tools, tmp_path, monkeypatch):
    from lazymind.chat.engine.subagent import tools as artifact_tools
    from lazymind.chat.engine.subagent.context import SubAgentContext
    from lazymind.chat.engine.subagent.db import MemorySubAgentStore

    package = yaml.safe_load((ROOT / 'workflows/ppt-workflow/workflow.yaml').read_text(encoding='utf-8'))
    state = yaml.safe_load((ROOT / 'workflows/ppt-workflow/scenario/state.yml').read_text(encoding='utf-8'))
    events = []
    ctx = SubAgentContext(
        task_id='collect', conversation_id='papers', agent_type='workflow_step', objective='先写心得',
        params={'workflow_runtime': package['runtime']}, workspace_path=str(tmp_path),
        input_slots=[], output_slots=[item['material'] for item in state['steps']['collect_materials']['outputs']],
        db=MemorySubAgentStore({'id': 'collect'}), emit=events.append,
    )
    monkeypatch.setattr(artifact_tools, 'require_context', lambda: ctx)
    monkeypatch.setattr(tools, '_save_artifact', artifact_tools._save_artifact)
    prose = '两篇论文的研究问题不同，应分别讨论方法与实验局限。'
    tools.ppt_publish_paper_reflection(prose)
    assert events[0]['slot'] == 'reflection_markdown'
    assert ctx.read_draft('reflection_markdown') == (prose, 'text')


@pytest.mark.parametrize('layout', ['files_only', 'same_turn', 'across_turns'])
def test_direct_upload_manifest_resolves_and_caches_every_same_named_paper(tools, tmp_path, monkeypatch, layout):
    from lazymind.chat.workflow.attachments import attachment_context
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
    runtime = yaml.safe_load((ROOT / 'workflows/ppt-workflow/workflow.yaml').read_text(encoding='utf-8'))['runtime']
    context, satisfied = attachment_context(cfg, runtime)
    sources = json.loads(context.splitlines()[3])
    assert len(sources) == 2
    assert satisfied == ['academic_materials']
    monkeypatch.setattr(resolver, '_agentic_config', lambda: cfg)
    reader = types.ModuleType('lazymind.chat.engine.tools.file_resources.tools')
    reader.read_file_resource = mock.Mock(side_effect=lambda path, **kwargs: {
        'text': Path(path).read_text(encoding='utf-8'), 'eof': True,
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
    from lazymind.chat.workflow.attachments import attachment_context
    paths = [tmp_path / name for name in ('image.png', 'template.pptx', 'missing.pdf')]
    for path in paths[:2]:
        path.write_text('not a paper')
    runtime = yaml.safe_load((ROOT / 'workflows/ppt-workflow/workflow.yaml').read_text(encoding='utf-8'))['runtime']
    context, satisfied = attachment_context({'files': list(map(str, paths))}, runtime)
    assert 'image.png' in context and 'template.pptx' in context
    assert 'missing.pdf' not in context and satisfied == []


def test_paper_order_places_transitive_prerequisites_before_dependents(tools):
    papers = [{'source_id': key, 'title': f'Paper {key}'} for key in ['A', 'C', 'B', 'D']]
    edges = [
        {'prerequisite': 'B', 'dependent': 'A', 'evidence': 'A §3 extends the encoder in B §2'},
        {'prerequisite': 'C', 'dependent': 'B', 'evidence': 'B §2 uses the representation in C §4'},
    ]
    result = tools.ppt_plan_paper_order(json.dumps(papers), json.dumps(edges))
    assert [paper['source_id'] for paper in result['paper_order']] == ['C', 'B', 'A', 'D']
    assert result['dependencies'] == edges
    assert result['paper_count'] == 4


def test_independent_papers_keep_narrative_order_without_invented_dependencies(tools):
    papers = [{'source_id': key, 'title': f'Paper {key}'} for key in ['B', 'A', 'C']]
    result = tools.ppt_plan_paper_order(json.dumps(papers))
    assert result['paper_order'] == papers
    assert result['dependencies'] == []


@pytest.mark.parametrize('edges,reason', [
    ([{'prerequisite': 'B', 'dependent': 'A', 'evidence': ''}], 'require source evidence'),
    ([{'prerequisite': 'missing', 'dependent': 'A', 'evidence': 'A §3'}], 'selected source_ids'),
    ([{'prerequisite': 'A', 'dependent': 'A', 'evidence': 'A §3'}], 'depend on itself'),
    ([{'prerequisite': 'B', 'dependent': 'A', 'evidence': 'A §3'},
      {'prerequisite': 'A', 'dependent': 'B', 'evidence': 'B §2'}], 'Cyclic dependencies'),
])
def test_paper_order_rejects_unsupported_or_inconsistent_graph(tools, edges, reason):
    papers = [{'source_id': key, 'title': f'Paper {key}'} for key in ['A', 'B']]
    with pytest.raises(ValueError, match=reason):
        tools.ppt_plan_paper_order(json.dumps(papers), json.dumps(edges))
