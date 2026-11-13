from __future__ import annotations

import importlib.util
import json
from pathlib import Path
from types import SimpleNamespace

import pytest


def _load_ppt_tools():
    root = Path(__file__).resolve().parents[4]
    path = root / 'workflows' / 'ppt-workflow' / 'scripts' / 'tools.py'
    spec = importlib.util.spec_from_file_location('_test_ppt_speaker_notes_tools', path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_html_speaker_notes_keep_time_allocation():
    tools = _load_ppt_tools()
    prefix = '【建议用时 1分30秒｜累计 02:00–03:30】'
    html = '<html lang="zh"><body><aside hidden data-el="speaker-notes">' + prefix + \
        '这一页说明优化方法，以及它为什么减少等待时间。</aside></body></html>'
    assert tools._notes_from_html(html, 3).startswith(prefix)


def test_chinese_notes_are_spoken_copy_using_real_slide_content():
    tools = _load_ppt_tools()
    html = '''
    <html lang="zh-CN"><head><title>中国春节</title></head><body>
      <div class="wrapper">
        <h1 data-el="title">中国春节</h1>
        <p data-el="subtitle">团圆、传统与新的开始</p>
        <p data-el="narrative">春节是中国最重要的传统节日之一，家人会在这一天团聚。</p>
        <div data-el="bullet-1">
          <div class="point-title">年夜饭</div>
          <div class="point-desc">全家围坐共享团圆饭</div>
        </div>
        <div data-el="bullet-2">
          <div class="point-title">压岁钱</div>
          <div class="point-desc">长辈向晚辈送上祝福</div>
        </div>
        <div data-el="kpi-1">
          <div class="data-label">日期</div>
          <div class="data-value">农历正月初一</div>
        </div>
      </div>
    </body></html>
    '''

    notes = tools._notes_from_html(html, 1)

    assert notes.startswith('大家好')
    assert '春节是中国最重要的传统节日之一' in notes
    assert '年夜饭：全家围坐共享团圆饭' in notes
    assert '日期：农历正月初一' in notes
    assert all(word not in notes for word in ('讲解时', '建议', '避免', '请先'))


def test_english_notes_follow_html_language_even_with_chinese_title():
    tools = _load_ppt_tools()
    html = '''
    <html lang="en"><head><title>中国春节</title></head><body>
      <div class="wrapper">
        <h1 data-el="title">中国春节</h1>
        <p data-el="subtitle">Celebrating Family and Tradition</p>
        <p data-el="narrative">Chinese New Year brings families together.</p>
        <div data-el="bullet-1">
          <div class="point-title">Reunion Dinner</div>
          <div class="point-desc">families share a festive meal</div>
        </div>
        <div data-el="kpi-1">
          <div class="data-label">Date</div>
          <div class="data-value">first day of the first lunar month</div>
        </div>
      </div>
    </body></html>
    '''

    notes = tools._notes_from_html(html, 1)

    assert notes.startswith('Today, I would like to introduce 中国春节.')
    assert 'Chinese New Year brings families together.' in notes
    assert 'Reunion Dinner: families share a festive meal' in notes


def test_text_edit_republishes_html_and_matching_notes(monkeypatch, tmp_path):
    tools = _load_ppt_tools()
    deck = tmp_path / 'deck'
    pages = deck / 'pages'
    pages.mkdir(parents=True)
    (deck / 'task_pack.json').write_text('{}', encoding='utf-8')
    (deck / 'info_pack.json').write_text('{}', encoding='utf-8')
    (pages / 'page_001.html').write_text(
        '<html><head><title>Old</title></head><body>'
        '<div class="wrapper"><h1 data-el="title">Old</h1></div>'
        '</body></html>',
        encoding='utf-8',
    )
    captured = {}

    def publish(deck_path, page_numbers, *, with_notes=True):
        captured.update({
            'deck': deck_path,
            'pages': page_numbers,
            'with_notes': with_notes,
        })
        return {'published_count': 1, 'failed': []}

    monkeypatch.setattr(tools, '_publish_pages_from_disk', publish)

    before = tools.ppt_read_page_html(str(deck), 1)
    result = tools.ppt_edit_page_html(
        str(deck), 1,
        ops_json=[{'op': 'replace_text', 'el': 'title', 'value': 'New'}],
        expected_sha256=before['html_sha256'],
    )

    assert result['page'] == 1
    assert '>New</h1>' in (pages / 'page_001.html').read_text(encoding='utf-8')
    assert captured['pages'] == [1]
    assert captured['with_notes'] is True


def test_academic_notes_use_authored_speech_instead_of_visible_equations():
    tools = _load_ppt_tools()
    html = r'''<html lang="zh"><body><div class="wrapper">
      <h1 data-el="title">置信度调度</h1>
      <p data-el="narrative">公式：<span class="katex">\prod_i p(x_i)</span></p>
      </div><aside hidden data-el="speaker-notes">这个方法先预测哪些位置更可靠，再优先验证这些位置。
      实验中接收率从 45.7% 提高到 95.7%，但这不代表整体速度翻倍。</aside></body></html>'''
    notes = tools._notes_from_html(html, 8)
    assert notes.startswith('这个方法先预测')
    assert '45.7%' in notes and '95.7%' in notes
    assert '不代表整体速度翻倍' in notes
    assert '\\prod' not in notes and '公式' not in notes


def test_legacy_notes_exclude_mathml_and_tex_but_keep_evidence():
    tools = _load_ppt_tools()
    html = r'''<html lang="zh"><body><h1 data-el="title">实验结果</h1>
      <p data-el="narrative">模型优先验证可靠位置。<math><mrow><mi>x</mi><mo>=</mo><mi>y</mi></mrow></math>
      $$\sum_i x_i$$ 接收率达到 95.7%。</p></body></html>'''
    notes = tools._notes_from_html(html, 1)
    assert '模型优先验证可靠位置' in notes and '95.7%' in notes
    assert '\\sum' not in notes and 'x=y' not in notes


def test_english_authored_notes_keep_language_and_measurement_limits():
    tools = _load_ppt_tools()
    html = '''<html lang="en"><body><h1>Cache</h1>
      <aside hidden data-el="speaker-notes">The cache reuses the parsed document.
      Latency fell from 24 seconds to 0.013 seconds; upload hashing is excluded.</aside>
      </body></html>'''
    notes = tools._notes_from_html(html, 1)
    assert notes.startswith('The cache reuses')
    assert '0.013 seconds' in notes and 'upload hashing is excluded' in notes


def test_separate_scripts_publish_hidden_file_and_visual_only_prompts(tmp_path, monkeypatch):
    import json
    tools = _load_ppt_tools()
    monkeypatch.setattr(tools, 'require_context', lambda: object())
    monkeypatch.setattr(tools, '_resolve_artifact_text',
                        lambda _, slot: ('## Page A\nShort A\n## Page B\nShort B', 'text')
                        if slot == 'deck_outline' else (None, 'text'))
    monkeypatch.setattr(tools, '_ui_slot_order_list', lambda _: [])
    files, prompts = [], []
    monkeypatch.setattr(tools, '_save_artifact', lambda **kw: (files.append(kw), {'ok': True})[1])
    monkeypatch.setattr(tools, '_publish_ordered_ppt_artifact',
                        lambda **kw: (prompts.append(kw), {'ok': True})[1])
    scripts = ['【建议用时 30秒】第一段完整口播。', '【建议用时 1分钟】第二段完整口播。']
    result = tools._publish_text_page_briefs(tmp_path, None, ['Layout A', 'Layout B'], scripts)
    assert result['notes_ref'] == 'speaker_notes_source'
    assert scripts[0] not in str(result)
    assert [p['value'] for p in prompts] == ['Layout A', 'Layout B']
    assert files[0]['content_type'] == 'file'
    manifest = json.loads(Path(files[0]['value']).read_text(encoding='utf-8'))
    assert [p['text'] for p in manifest['pages']] == scripts


def test_native_notes_are_resolved_by_reference_without_model_text(monkeypatch):
    import json
    tools = _load_ppt_tools()
    monkeypatch.setattr(tools, 'require_context', lambda: object())
    scripts = ['【30秒】口播甲', '【60秒】口播乙']
    monkeypatch.setattr(tools, '_resolve_artifact_text', lambda *_: (
        json.dumps({'schema_version': 1, 'pages': [
            {'page': i, 'text': text} for i, text in enumerate(scripts, 1)
        ]}), 'file'))
    pages = [{'source_page': 5, 'notes': 'unwanted regeneration'}, {'source_page': 5}]
    bound = tools._pages_with_saved_notes(pages, 'speaker_notes_source')
    assert [p['notes'] for p in bound] == scripts
    assert pages[0]['notes'] == 'unwanted regeneration'  # Caller data is not mutated.
    import pytest
    with pytest.raises(ValueError, match='count differs'):
        tools._pages_with_saved_notes(pages[:1], 'speaker_notes_source')
    with pytest.raises(ValueError, match='not a path'):
        tools._load_saved_notes('/tmp/arbitrary.json')


def test_missing_or_duplicate_note_pages_fail_instead_of_regenerating(monkeypatch):
    import json
    import pytest
    tools = _load_ppt_tools()
    monkeypatch.setattr(tools, 'require_context', lambda: object())
    monkeypatch.setattr(tools, '_resolve_artifact_text', lambda *_: (None, 'text'))
    with pytest.raises(ValueError, match='Missing speaker_notes_source'):
        tools._load_saved_notes('speaker_notes_source')
    monkeypatch.setattr(tools, '_resolve_artifact_text', lambda *_: (json.dumps({
        'schema_version': 1, 'pages': [{'page': 1, 'text': 'a'}, {'page': 1, 'text': 'b'}]
    }), 'file'))
    with pytest.raises(ValueError, match='Duplicate'):
        tools._load_saved_notes('speaker_notes_source')


def test_html_inserts_exact_saved_script_once_and_escapes_markup():
    from html import unescape
    import re
    tools = _load_ppt_tools()
    script = '【建议用时 30秒】A < B & C\n第二段 </aside> 只是文本'
    html = '<html><body><h1>Chart</h1><aside hidden data-el="speaker-notes">wrong</aside></body></html>'
    result = tools._insert_saved_notes_html(html, script)
    assert result.count('data-el="speaker-notes"') == 1
    assert '<h1>Chart</h1>' in result and 'wrong' not in result
    assert unescape(re.search(r'<aside[^>]*>(.*?)</aside>', result, re.S)[1]) == script
    assert tools._insert_saved_notes_html(result, script) == result


def test_notes_source_is_hidden_and_bound_to_generation():
    import yaml
    root = Path(__file__).resolve().parents[4]
    workflow = yaml.safe_load((root / 'workflows/ppt-workflow/workflow.yaml').read_text(encoding='utf-8'))
    state = yaml.safe_load((root / 'workflows/ppt-workflow/scenario/state.yml').read_text(encoding='utf-8'))
    source = next(s for s in workflow['slots'] if s['id'] == 'speaker_notes_source')
    assert source['user_visible'] is False and source['type'] == 'file'
    assert {'material': 'speaker_notes_source'} in state['steps']['plan_page_prompts']['outputs']
    assert {'material': 'speaker_notes_source', 'required': False} in state['steps']['generate_ppt']['inputs']
    assert all(s.get('id') != 'speaker_notes_source' for tab in workflow['ui']['tabs'] for s in tab.get('slots', []))


def test_html_publication_uses_saved_notes_for_both_export_and_presenter(tmp_path, monkeypatch):
    tools = _load_ppt_tools()
    page = tmp_path / 'page.html'
    page.write_text('<html><head><title>Demo</title></head><body><h1>Result</h1></body></html>')
    script = '【建议用时 30秒】这是已经审核的讲稿，含 0.013 秒。'
    monkeypatch.setattr(tools, '_load_saved_notes', lambda: {1: script})
    monkeypatch.setattr(tools, '_page_html_path', lambda *_: page)
    monkeypatch.setattr(tools, '_inline_preview_images', lambda html, *_: (html, 0))
    monkeypatch.setattr(tools, '_load_outline', lambda *_: {'pages': [{}]})
    monkeypatch.setattr(tools, '_reserve_missing_page_positions', lambda *_: None)
    published = []
    monkeypatch.setattr(tools, '_publish_ordered_ppt_artifact',
                        lambda **kw: (published.append(kw), {'ok': True})[1])
    result = tools._publish_one_page(tmp_path, 1, slot_orders={})
    assert result['ok']
    assert script in page.read_text(encoding='utf-8')
    assert script in next(p['value'] for p in published if p['slot'] == 'preview_html')
    assert next(p['value'] for p in published if p['slot'] == 'preview_notes') == script


def test_native_fill_reads_notes_artifact_before_composing(tmp_path, monkeypatch):
    import lazymind.chat.engine.tools.file_resources.pptx_template_compose as compose
    tools = _load_ppt_tools()
    script = '【30秒】只写入一次。'
    monkeypatch.setattr(tools, '_load_saved_notes', lambda ref: {1: script})
    monkeypatch.setattr(tools, '_resolve_template_file', lambda _: (tmp_path / 'template.pptx', None))
    monkeypatch.setattr(tools, '_native_image_assets', lambda _: {})
    monkeypatch.setattr(tools, '_conversation_root', lambda: tmp_path)
    monkeypatch.setattr(tools, '_save_artifact', lambda **_: {'ok': True})
    monkeypatch.setattr(tools, '_review_native_pptx', lambda *_: {'status': 'unavailable'})
    received = []

    def fake_compose(source, output, digest, pages, assets):
        received.extend(pages)
        output.parent.mkdir(parents=True)
        output.write_bytes(b'fixture')
        return {'page_count': 1}
    monkeypatch.setattr(compose, 'compose_template', fake_compose)
    result = tools.ppt_fill_template('template.pptx', 'hash',
                                     pages=[{'source_page': 1, 'edits': []}],
                                     notes_ref='speaker_notes_source')
    assert result['representation'] == 'native_pptx'
    assert received[0]['notes'] == script
    assert script not in str(result)


def test_insert_delete_and_reorder_retime_without_rewriting_scripts():
    tools = _load_ppt_tools()
    notes = {1: '【建议用时 1分00秒｜累计 00:00–01:00】原文甲',
             2: '【建议用时 2分00秒｜累计 01:00–03:00】原文乙'}
    updated, timing = tools._retime_notes(notes, [2, None, 1], None, {'2': '新增页讲稿'})
    assert timing['total_seconds'] == 180
    assert sum(p['seconds'] for p in timing['pages']) == 180
    assert timing['pages'][-1]['end_seconds'] == 180
    assert updated[1].endswith('原文乙') and updated[3].endswith('原文甲')
    assert updated[2].endswith('新增页讲稿')
    deleted, new_timing = tools._retime_notes(updated, [1, 3], None, {})
    assert new_timing['total_seconds'] == 180
    assert deleted[2].endswith('原文甲')


def test_retiming_rejects_missing_insert_script_and_invalid_old_page():
    import pytest
    tools = _load_ppt_tools()
    with pytest.raises(ValueError, match='needs a separately authored script'):
        tools._retime_notes({1: 'Existing'}, [1, None], 3, {})
    with pytest.raises(ValueError, match='unavailable'):
        tools._retime_notes({1: 'Existing'}, [2], 3, {})


def test_without_vlm_automatic_images_never_reach_registration_or_native_fill(monkeypatch):
    import pytest
    tools = _load_ppt_tools()

    def unavailable():
        raise tools.VisionModelUnavailable('No visual model')
    monkeypatch.setattr(tools, 'select_vision_model_role', unavailable)
    monkeypatch.setattr(tools, '_tool_success', lambda _, value: value)
    monkeypatch.setattr(tools, '_parse_images_payload',
                        lambda *_: (_ for _ in ()).throw(AssertionError('download path reached')))
    result = tools.ppt_register_material_images('[{"url":"https://example.com/unrelated.png"}]')
    assert result['count'] == 0 and result['reason'] == 'vision_unavailable'
    with pytest.raises(ValueError, match='No visual model'):
        tools._native_image_assets([{'images': [{'material_index': 1}]}])


def test_planning_authors_scripts_and_generation_only_references_them():
    import yaml
    root = Path(__file__).resolve().parents[4]
    steps = yaml.safe_load((root / 'workflows/ppt-workflow/scenario/state.yml').read_text(encoding='utf-8'))['steps']
    plan, generate = steps['plan_page_prompts'], steps['generate_ppt']
    assert 'checkpoints each prompt' in plan['prompt']
    assert 'ppt_plan_page_prompts' in plan['terminal_tools']
    assert 'Talk timing plan:' in plan['prompt']
    assert 'Speaker scripts were authored once' not in plan['prompt']
    assert 'Speaker scripts were authored once' in generate['prompt']
    assert 'Write speakable prose that fits this duration' not in generate['prompt']
    assert 'ppt_update_speaker_notes' not in generate['tools']
    assert 'ppt_update_speaker_notes' in steps['plan_page_prompts']['tools']
    assert all(x['material'] != 'speaker_notes_source' for x in generate['outputs'])
    # The graph requires one producer and forbids consuming one's own output.
    producers = {}
    for step_name, step in steps.items():
        inputs = {x['material'] for x in step.get('inputs', [])}
        for output in step.get('outputs', []):
            material = output['material']
            assert material not in inputs, (step_name, material)
            assert material not in producers, (step_name, material)
            producers[material] = step_name
    assert all(x['material'] != 'speaker_notes_source' for x in steps['analyze_requirements']['outputs'])
    for name in ['build_outline', 'plan_page_prompts', 'generate_ppt']:
        assert 'Never display PR/issue/work-log identifiers' in steps[name]['prompt']


@pytest.mark.parametrize('failure', ['model', 'publish'])
def test_page_prompt_retry_keeps_published_pages_and_resumes_missing_work(monkeypatch, tmp_path, failure):
    tools = _load_ppt_tools()
    ctx = SimpleNamespace(params={'launch_user_input': '设计一份三页 PPT'})
    monkeypatch.setattr(tools, 'require_context', lambda: ctx)
    monkeypatch.setattr(tools, '_bound_workflow_deck', lambda: tmp_path)
    monkeypatch.setattr(tools, '_required_output_format', lambda: 'html')
    monkeypatch.setattr(tools, 'make_cancel_stop_condition', lambda: lambda _: None)
    timing = tools.ppt_plan_timing(3, json.dumps([{'title': str(n), 'weight': 1} for n in range(1, 4)]))
    artifacts = {
        'deck_outline': '## 封面\n介绍\n\n## 机制\n说明因果\n\n## 结论\n总结',
        'presentation_timing': json.dumps(timing),
    }
    monkeypatch.setattr(tools, '_resolve_artifact_text', lambda _, key: (artifacts.get(key), 'text'))
    visible, notes, requested = {}, {}, []
    interrupted = True

    def generate(_system, user, **_kwargs):
        data = json.loads(user)
        page = data['page']
        requested.append(page)
        if page > 1:
            assert 0 in visible, 'first prompt must be published before the next model call'
        if page == 2 and interrupted and failure == 'model':
            return 'malformed response'
        assert data['shared']['global_style'] == '已确认风格'
        return json.dumps({
            'content': f'页面 {page} 的可见正文', 'layout': '标题与证据两列',
            'assets': '无外部素材', 'speaker_note': f'第 {page} 页口播',
        })

    def save(**kwargs):
        if kwargs['key'] == 'speaker_notes_source':
            notes.update({row['page']: row['text'] for row in json.loads(
                Path(kwargs['value']).read_text())['pages']})
        else:
            index = kwargs['publisher_list_index']
            if index == 1 and interrupted and failure == 'publish':
                return {'ok': False, 'value': 'temporary publication error'}
            visible[index] = kwargs['value']
        return {'stored': True}

    monkeypatch.setattr(tools, '_agent_llm_call', generate)
    monkeypatch.setattr(tools, '_save_artifact', save)
    monkeypatch.setattr(tools, '_ui_slot_order_list', lambda _: list(visible))
    monkeypatch.setattr(tools, '_selected_slide_outline_items', lambda: list(visible.items()))
    with pytest.raises(tools.ToolExecutionError, match='Page 2 prompt failed'):
        tools.ppt_plan_page_prompts('已确认风格', 'html')
    assert list(visible) == [0]
    assert not notes
    assert tools._prepare_deferred_outline(tmp_path)['status'] == 'failed'
    assert requested == ([1, 2, 2] if failure == 'model' else [1, 2])
    visible[0] = '保留用户编辑后的第一页'
    interrupted = False
    requested.clear()
    result = tools.ppt_plan_page_prompts('重试时模型改写的风格', 'html')
    assert requested == ([2, 3] if failure == 'model' else [3])
    assert result['page_count'] == 3 and result['published_count'] == 2
    assert visible[0] == '保留用户编辑后的第一页'
    assert list(visible) == [0, 1, 2]
    assert all(notes[n].startswith(timing['pages'][n - 1]['notes_prefix']) for n in range(1, 4))
    assert tools._prepare_deferred_outline(tmp_path) == {'status': 'direct', 'page_count': 3}


def test_retime_planning_only_saves_sources_without_touching_output(tmp_path, monkeypatch):
    tools = _load_ppt_tools()
    monkeypatch.setattr(tools, '_load_saved_notes', lambda _: {1: '【建议用时 1分00秒｜累计 00:00–01:00】原文'})
    monkeypatch.setattr(tools, '_bound_workflow_deck', lambda: tmp_path)
    saved = []
    monkeypatch.setattr(tools, '_save_notes_source', lambda _, notes: saved.append(notes))
    monkeypatch.setattr(tools, '_save_artifact', lambda **_: {'ok': True})
    monkeypatch.setattr(tools, '_workflow_client', lambda: (_ for _ in ()).throw(AssertionError('output accessed')))
    result = tools.ppt_update_speaker_notes([1], total_minutes=2, update_outputs=False)
    assert result['outputs_updated'] is False and result['total_seconds'] == 120
    assert saved[0][1].startswith('【建议用时 2分00秒') and saved[0][1].endswith('原文')


def test_no_vlm_removes_previously_attached_automatic_materials(tmp_path, monkeypatch):
    import json
    tools = _load_ppt_tools()

    def unavailable():
        raise tools.VisionModelUnavailable('No visual model')
    monkeypatch.setattr(tools, 'select_vision_model_role', unavailable)
    monkeypatch.setattr(tools, '_load_material_manifest', lambda: {'images': []})
    (tmp_path / 'info_pack.json').write_text(json.dumps({'user_assets': {
        'reference_images': ['/images/material_01.png', '/images/user-logo.png'],
        'reference_image_sources': {'/images/material_01.png': {'source': 'kb'}},
    }}))
    result = tools._attach_material_images_to_deck(tmp_path)
    assert result['reason'] == 'vision_unavailable'
    data = json.loads((tmp_path / 'info_pack.json').read_text(encoding='utf-8'))
    assert data['user_assets']['reference_images'] == ['/images/user-logo.png']
