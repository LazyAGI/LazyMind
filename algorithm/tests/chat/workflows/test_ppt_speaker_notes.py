from __future__ import annotations

import importlib.util
from pathlib import Path


def _load_ppt_tools():
    root = Path(__file__).resolve().parents[4]
    path = root / 'workflows' / 'ppt-workflow' / 'scripts' / 'tools.py'
    spec = importlib.util.spec_from_file_location('_test_ppt_speaker_notes_tools', path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


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
    assert 'The key figures shown here are' in notes


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

    result = tools.ppt_edit_page_html(
        str(deck), 1,
        ops_json=[{'op': 'replace_text', 'el': 'title', 'value': 'New'}],
    )

    assert result['success'] is True
    assert captured['pages'] == [1]
    assert captured['with_notes'] is True
