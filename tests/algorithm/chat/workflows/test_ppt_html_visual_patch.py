import importlib.util
import json
from pathlib import Path

import pytest


ROOT = Path(__file__).resolve().parents[4]
HTML = ('<!doctype html><html><head><style>.card{height:500px}</style></head><body>'
        '<main data-el="content"><div data-el="card" style="height:500px">'
        '<b data-el="label">Approved &amp; unchanged</b></div>'
        '<div data-el="other">Other content</div></main><script>const chart=[1,2,3];</script></body></html>')
ISSUE = {'element_ids': ['card'], 'problem': 'Bottom text is clipped', 'fix': 'Use automatic height'}
PLAN = {'ops': [{'op': 'set_style', 'el': 'card', 'styles': {'height': 'auto'}}]}


@pytest.fixture
def runtime():
    spec = importlib.util.spec_from_file_location('ppt_visual_patch_runtime',
                                                  ROOT / 'workflows/ppt-workflow/runtime/scripts/run_stage.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


@pytest.fixture
def deck(tmp_path):
    (tmp_path / 'pages').mkdir()
    (tmp_path / 'pages/page_001.html').write_text(HTML)
    return tmp_path


def test_html_refinement_can_change_structure_colors_charts_and_unanchored_elements(runtime, deck, monkeypatch):
    renders, reviews = [], []
    refined = HTML.replace('<main ', '<section ').replace('</main>', '</section>')
    refined = refined.replace('height:500px', 'height:auto;color:white;background:navy')
    refined = refined.replace('<script>', '<script>const chartColor="white";')
    refined = refined.replace('Other content', '<img src="asset.png">Other content')

    def render(_deck, page, **kwargs):
        path = kwargs.get('html_path') or deck / 'pages/page_001.html'
        renders.append(path.read_text())
        return deck / 'screenshot.png'

    def vision(system, prompt, images):
        assert images
        if '返回修改后的完整 HTML' in system:
            return refined
        reviews.append(prompt)
        return json.dumps({'issues': [ISSUE] if len(reviews) == 1 else []})

    monkeypatch.setattr(runtime, '_screenshot_page', render)
    code, result = runtime._capture_cmd(runtime.cmd_refine_page, deck, 1, vlm_call=vision)
    assert code == 0 and result['visual_validation'] == 'passed'
    assert (deck / result['refined_path']).read_text() == refined
    assert renders == [HTML, refined]
    assert (deck / 'pages/page_001.html').read_text() == HTML
    assert not list((deck / 'pages').glob('.refine-*'))


def test_clean_page_does_not_call_rewrite_model(runtime, deck, monkeypatch):
    monkeypatch.setattr(runtime, '_screenshot_page', lambda *_a, **_k: deck / 'shot.png')
    calls = []

    def vision(*_args, **_kwargs):
        calls.append(True)
        assert len(calls) == 1
        return '{"issues":[]}'

    code, result = runtime._capture_cmd(runtime.cmd_refine_page, deck, 1, vlm_call=vision)
    assert code == 0 and result['changed'] is False
    assert not (deck / 'pages/page_001.refined.html').exists()


@pytest.mark.parametrize('outcome', ['passed', 'repeat', 'cycle', 'stale', 'malformed', 'render', 'cancel', 'model'])
def test_html_repair_continues_without_round_limit_and_checks_each_candidate(runtime, deck, monkeypatch, outcome):
    from lazymind.chat.engine.agent_runtime.cancellation import UserCancelledError
    calls, reviews, renders = [], [], []

    def render(*_a, **_k):
        renders.append(True)
        return None if outcome == 'render' and len(renders) > 1 else deck / 'shot.png'

    def vision(system, prompt, images):
        assert images
        if '返回修改后的完整 HTML' in system:
            calls.append(prompt)
            if outcome == 'model':
                raise runtime.ModelClientError('provider failed')
            if outcome == 'malformed':
                return '<div>fragment</div>'
            if outcome == 'repeat':
                return HTML
            if outcome == 'stale':
                (deck / 'pages/page_001.html').write_text('New revision')
            iteration = 1 if outcome == 'cycle' and len(calls) == 3 else len(calls)
            return HTML.replace('height:500px', f'height:{500 + iteration}px')
        reviews.append(prompt)
        return json.dumps({'issues': [] if len(calls) >= 3 and outcome == 'passed' else [
            {'problem': 'low contrast', 'fix': 'change background'}]})

    def cancel():
        if outcome == 'cancel' and calls:
            raise UserCancelledError('stopped by user')

    monkeypatch.setattr(runtime, '_screenshot_page', render)
    if outcome == 'cancel':
        with pytest.raises(UserCancelledError):
            runtime._capture_cmd(runtime.cmd_refine_page, deck, 1, vlm_call=vision, cancel_check=cancel)
    else:
        code, result = runtime._capture_cmd(runtime.cmd_refine_page, deck, 1,
                                            vlm_call=vision, cancel_check=cancel)
        assert code == (0 if outcome == 'passed' else 1)
        if outcome == 'passed':
            assert result['repairs'] == 3 and len(reviews) == len(renders) == 4
        if outcome == 'model':
            assert 'provider failed' in result['error']
    assert (deck / 'pages/page_001.refined.html').exists() == (outcome == 'passed')
    assert (deck / 'pages/page_001.html').read_text() == ('New revision' if outcome == 'stale' else HTML)
    assert not list((deck / 'pages').glob('.refine-*'))


def test_layout_patch_rejects_stale_source_and_ambiguous_ids(runtime):
    from layout_patch import apply_patch, snapshot
    with pytest.raises(ValueError, match='Page changed'):
        apply_patch(HTML, PLAN, source_sha256='old', allowed_ids={'card'})
    with pytest.raises(ValueError, match='Ambiguous'):
        snapshot(HTML.replace('data-el="other"', 'data-el="card"'))
    assert snapshot(HTML)[0]['el'] == 'content'


def test_style_like_attribute_text_is_preserved(runtime):
    import hashlib
    from layout_patch import apply_patch
    source = '<img data-el="card" alt="example style=\'unchanged\'" src="image.png">'
    result = apply_patch(source, PLAN, source_sha256=hashlib.sha256(source.encode()).hexdigest(),
                         allowed_ids={'card'})
    assert 'alt="example style=\'unchanged\'"' in result
    assert result == source[:-1] + ' style="height: auto !important;">'
