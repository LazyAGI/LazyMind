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


def test_html_refinement_patches_only_reviewed_element_then_verifies(runtime, deck, monkeypatch):
    renders, reviews = [], []

    def render(_deck, page, **kwargs):
        path = kwargs.get('html_path') or deck / 'pages/page_001.html'
        renders.append(path.read_text())
        return deck / 'screenshot.png'

    def vision(system, prompt, images):
        reviews.append(prompt)
        return json.dumps({'issues': [ISSUE] if len(reviews) == 1 else []})

    monkeypatch.setattr(runtime, '_screenshot_page', render)
    code, result = runtime._capture_cmd(runtime.cmd_refine_page, deck, 1,
                                        llm_call=lambda *_a, **_k: json.dumps(PLAN), vlm_call=vision)
    assert code == 0 and result['visual_validation'] == 'passed'
    refined = (deck / result['refined_path']).read_text()
    assert len(renders) == 2 and renders[1] == refined
    assert refined.replace('height:500px; height: auto !important;', 'height:500px') == HTML
    assert (deck / 'pages/page_001.html').read_text() == HTML
    assert not list((deck / 'pages').glob('.refine-*'))


def test_clean_page_does_not_call_rewrite_model(runtime, deck, monkeypatch):
    monkeypatch.setattr(runtime, '_screenshot_page', lambda *_a, **_k: deck / 'shot.png')

    def unexpected(*_args, **_kwargs):
        raise AssertionError('No repair is necessary')

    code, result = runtime._capture_cmd(runtime.cmd_refine_page, deck, 1, llm_call=unexpected,
                                        vlm_call=lambda *_a, **_k: '{"issues":[]}')
    assert code == 0 and result['changed'] is False
    assert not (deck / 'pages/page_001.refined.html').exists()


@pytest.mark.parametrize('failure', ['whole_html', 'other_element', 'unverified', 'stale', 'hidden', 'delete'])
def test_rejected_html_candidate_never_becomes_refined_output(runtime, deck, monkeypatch, failure):
    monkeypatch.setattr(runtime, '_screenshot_page', lambda *_a, **_k: deck / 'shot.png')
    calls = []

    def vision(*_a, **_k):
        calls.append(True)
        if len(calls) == 2 and failure == 'stale':
            (deck / 'pages/page_001.html').write_text(HTML.replace('Other content', 'New revision'))
        return json.dumps({'issues': [ISSUE] if len(calls) == 1 or failure == 'unverified' else []})

    plans = {
        'whole_html': HTML,
        'other_element': json.dumps({'ops': [{'op': 'set_style', 'el': 'other', 'styles': {'height': 'auto'}}]}),
        'hidden': json.dumps({'ops': [{'op': 'set_style', 'el': 'card', 'styles': {'display': 'none'}}]}),
        'delete': json.dumps({'ops': [{'op': 'delete_node', 'el': 'card'}]}),
    }
    code, _ = runtime._capture_cmd(runtime.cmd_refine_page, deck, 1, vlm_call=vision,
                                   llm_call=lambda *_a, **_k: plans.get(failure, json.dumps(PLAN)))
    assert code == 1
    assert not (deck / 'pages/page_001.refined.html').exists()
    expected = HTML.replace('Other content', 'New revision') if failure == 'stale' else HTML
    assert (deck / 'pages/page_001.html').read_text() == expected
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
