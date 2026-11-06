"""Visual quality is best effort after a usable presentation is delivered."""
import importlib.util
import json
from pathlib import Path

import pytest
from pptx import Presentation

from lazymind.chat.engine.tools.file_resources import pptx_visual_review as visual
from lazymind.chat.engine.tools.file_resources.pptx_template_fill import _inspect_page


@pytest.fixture
def tools():
    path = Path(__file__).resolve().parents[4] / 'workflows/ppt-workflow/scripts/tools.py'
    spec = importlib.util.spec_from_file_location('_test_ppt_visual_review', path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def test_review_submits_pixels_in_batches_and_requires_all_page_findings(tmp_path):
    images = [tmp_path / f'{i}.png' for i in range(5)]
    calls = []

    def vision(system, prompt, paths):
        calls.append(paths)
        numbers = [1, 2, 3, 4] if len(calls) == 1 else [5]
        return json.dumps({'pages': [{'page': n, 'issues': []} for n in numbers]})

    assert visual.review_pages(images, vision)['status'] == 'passed'
    assert calls == [[str(p) for p in images[:4]], [str(images[4])]]
    with pytest.raises(ValueError, match='every requested page'):
        visual.review_pages(images[:1], lambda *_: '{"pages":[]}')


def test_generator_delivers_first_then_repairs_geometry_from_screenshot_findings(tools, tmp_path, monkeypatch):
    source = tmp_path / 'source.pptx'
    prs = Presentation()
    prs.slides.add_slide(prs.slide_layouts[1]).shapes.title.text = 'Original'
    prs.save(source)
    info = _inspect_page(source, 1)
    edits = [{'id': s['id'], 'expected_text': s['text'], 'action': 'replace', 'text': 'Approved content'}
             for s in info['slots']]
    monkeypatch.setattr(tools, '_resolve_template_file', lambda _: (source, source.name))
    monkeypatch.setattr(tools, '_conversation_root', lambda: tmp_path)
    monkeypatch.setattr(tools, '_native_image_assets', lambda _: {})
    monkeypatch.setattr(tools, '_material_vision_role', lambda: 'vlm')
    screenshot = tmp_path / 'page-001.png'
    monkeypatch.setattr(visual, 'render_pages', lambda *_: [screenshot])
    findings = iter([
        {'pages': [{'page': 1, 'issues': [{'shape_ids': [2], 'problem': '标题遮住正文', 'fix': '下移正文框'}]}]},
        {'pages': [{'page': 1, 'issues': []}]},
    ])
    received = []

    def vision(system, prompt, images):
        received.extend(images)
        return json.dumps(next(findings), ensure_ascii=False)

    monkeypatch.setattr(tools, '_agent_vlm_call', vision)
    published = []
    monkeypatch.setattr(tools, '_save_artifact', lambda **kw: published.append(kw) or {'ok': True})

    def repair(system, prompt, **kwargs):
        assert len(published) == 1
        layout = json.loads(prompt)['layout']
        return json.dumps({'moves': [{'page': 1, 'shape_id': layout[0]['shapes'][0]['shape_id'],
                                      'expected_box': layout[0]['shapes'][0]['box'], 'box': [1, 1, 8, 1]}]})

    monkeypatch.setattr(tools, '_agent_llm_call', repair)
    result = tools.ppt_fill_template(source.name, info['sha256'], edits=edits)
    assert result['visual_review']['status'] == 'passed'
    assert result['visual_validation'] == 'rendered_model_review'
    assert received == [str(screenshot), str(screenshot)]
    assert len(published) == 2
    assert published[0]['value'] != published[1]['value']
    assert all(p['key'] == 'native_pptx' for p in published)
    before, after = [Presentation(p['value']) for p in published]
    assert [s.text for s in before.slides[0].shapes] == [s.text for s in after.slides[0].shapes]
    assert after.slides[0].shapes[0].top != before.slides[0].shapes[0].top


def test_missing_vision_is_disclosed_without_inventing_a_review(tools, tmp_path, monkeypatch):
    screenshot = tmp_path / 'page-001.png'
    monkeypatch.setattr(visual, 'render_pages', lambda *_: [screenshot])
    monkeypatch.setattr(tools, '_material_vision_role', lambda: '')
    result = tools._review_native_pptx(tmp_path / 'deck.pptx', tmp_path)
    assert result['status'] == 'unavailable'
    assert result['screenshots'] == [str(screenshot)]
    assert 'vision' in result['reason']


@pytest.mark.parametrize('failure', ['renderer', 'reviewer', 'invalid_repair', 'repair_publication'])
def test_optional_quality_failure_keeps_delivered_file(tools, tmp_path, monkeypatch, failure):
    candidate = tmp_path / 'presentation.pptx'
    prs = Presentation()
    prs.slides.add_slide(prs.slide_layouts[1]).shapes.title.text = 'Keep this content'
    prs.save(candidate)
    original = candidate.read_bytes()
    delivered = []

    def publish(**kwargs):
        if failure == 'repair_publication' and delivered:
            raise RuntimeError('optional revision upload failed')
        delivered.append(kwargs)
        return {'ok': True}

    def render(*args):
        assert delivered, 'File must be delivered before any optional rendering'
        if failure == 'renderer':
            raise RuntimeError('renderer unavailable')
        return [tmp_path / 'page.png']

    review_calls = []

    def vision(*args):
        review_calls.append(args)
        if failure == 'repair_publication' and len(review_calls) > 1:
            return '{"pages":[{"page":1,"issues":[]}]}'
        if failure == 'reviewer':
            raise RuntimeError('visual model timed out')
        return '{"pages":[{"page":1,"issues":[{"shape_ids":[2],"problem":"标题拥挤","fix":"调整框位置"}]}]}'

    monkeypatch.setattr(tools, '_save_artifact', publish)
    monkeypatch.setattr(tools, '_material_vision_role', lambda: 'vlm')
    monkeypatch.setattr(visual, 'render_pages', render)
    monkeypatch.setattr(tools, '_agent_vlm_call', vision)

    def repair(_system, prompt, **_kwargs):
        layout = json.loads(prompt)['layout'][0]['shapes'][0]
        return 'not json' if failure == 'invalid_repair' else json.dumps({'moves': [
            {'page': 1, 'shape_id': layout['shape_id'], 'expected_box': layout['box'], 'box': [1, 1, 8, 1]}]})

    monkeypatch.setattr(tools, '_agent_llm_call', repair)
    result = tools._deliver_native_pptx(candidate, tmp_path, 'ppt_fill_template')
    assert result['delivery_preserved'] is True
    assert len(delivered) == 1
    assert candidate.read_bytes() == original
    assert Presentation(delivered[0]['value']).slides[0].shapes.title.text == 'Keep this content'
    if failure == 'repair_publication':
        assert 'optional revision upload failed' in result['repair_error']


def test_visual_repair_budget_keeps_delivered_version_when_candidates_fail(tools, tmp_path, monkeypatch):
    candidate = tmp_path / 'presentation.pptx'
    prs = Presentation()
    prs.slides.add_slide(prs.slide_layouts[1]).shapes.title.text = 'Unchanged content'
    prs.save(candidate)
    delivered = []
    monkeypatch.setattr(tools, '_save_artifact', lambda **kw: delivered.append(kw) or {'ok': True})
    monkeypatch.setattr(tools, '_review_native_pptx', lambda *_: {
        'status': 'needs_revision', 'pages': [
            {'page': 1, 'issues': [{'shape_ids': [2], 'problem': 'overlap', 'fix': 'move'}]}]})
    calls = []

    def repair(_system, prompt, **_kwargs):
        shape = json.loads(prompt)['layout'][0]['shapes'][0]
        calls.append(shape)
        return json.dumps({'moves': [{'page': 1, 'shape_id': shape['shape_id'],
                                      'expected_box': shape['box'], 'box': [1, len(calls), 8, 1]}]})

    monkeypatch.setattr(tools, '_agent_llm_call', repair)
    result = tools._deliver_native_pptx(candidate, tmp_path, 'ppt_fill_template')
    assert result['repair_limit_reached'] is True
    assert result['repair_adopted'] is False
    assert result['repairs'] == 2
    assert len(delivered) == 1
    assert delivered[0]['value'] == str(candidate)
    assert calls[1]['box'] == [1, 1, 8, 1]


def test_repair_context_excludes_healthy_pages(tools, tmp_path, monkeypatch):
    candidate = tmp_path / 'presentation.pptx'
    prs = Presentation()
    for title in ('Problem page', 'Do not edit'):
        prs.slides.add_slide(prs.slide_layouts[1]).shapes.title.text = title
    prs.save(candidate)
    delivered = []
    monkeypatch.setattr(tools, '_save_artifact', lambda **kw: delivered.append(kw) or {'ok': True})
    monkeypatch.setattr(tools, '_review_native_pptx', lambda *_: {
        'status': 'needs_revision', 'pages': [
            {'page': 1, 'issues': [{'shape_ids': [2], 'problem': 'overlap', 'fix': 'move'}]},
            {'page': 2, 'issues': []}]})

    def repair(_system, prompt, **_kwargs):
        payload = json.loads(prompt)
        assert [p['page'] for p in payload['layout']] == [1]
        return json.dumps({'moves': [{'page': 2, 'shape_id': 2,
                                      'expected_box': [0, 0, 1, 1], 'box': [1, 1, 8, 1]}]})

    monkeypatch.setattr(tools, '_agent_llm_call', repair)
    result = tools._deliver_native_pptx(candidate, tmp_path, 'ppt_fill_template')
    assert 'outside the reviewed' in result['repair_error']
    assert len(delivered) == 1


@pytest.mark.parametrize('shape_ids', [[999], ['2'], [True], None])
def test_visual_review_rejects_uninspected_shape_ids(tmp_path, shape_ids):
    layout = [{'page': 1, 'shapes': [{'shape_id': 2}]}]
    response = {'pages': [{'page': 1, 'issues': [
        {'shape_ids': shape_ids, 'problem': 'overlap', 'fix': 'move'}]}]}
    with pytest.raises(ValueError, match='inspected shape_ids'):
        visual.review_pages([tmp_path / 'page.png'], lambda *_: json.dumps(response), layout)


def test_missing_screenshot_cannot_count_as_passed_review(tmp_path):
    with pytest.raises(ValueError, match='every slide'):
        visual.review_pages([], lambda *_: '{"pages":[]}')
    with pytest.raises(ValueError, match='every slide'):
        visual.review_pages([tmp_path / 'page.png'], lambda *_: '', [{'page': 1}, {'page': 2}])
